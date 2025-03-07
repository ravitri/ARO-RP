package portal

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"html/template"
	"log"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/Azure/ARO-RP/pkg/api"
	"github.com/Azure/ARO-RP/pkg/database"
	"github.com/Azure/ARO-RP/pkg/env"
	frontendmiddleware "github.com/Azure/ARO-RP/pkg/frontend/middleware"
	"github.com/Azure/ARO-RP/pkg/portal/kubeconfig"
	"github.com/Azure/ARO-RP/pkg/portal/middleware"
	"github.com/Azure/ARO-RP/pkg/portal/prometheus"
	"github.com/Azure/ARO-RP/pkg/portal/ssh"
	"github.com/Azure/ARO-RP/pkg/proxy"
)

type Runnable interface {
	Run(context.Context) error
}

type portal struct {
	env           env.Core
	audit         *logrus.Entry
	log           *logrus.Entry
	baseAccessLog *logrus.Entry
	l             net.Listener
	sshl          net.Listener
	verifier      middleware.Verifier

	hostname     string
	servingKey   *rsa.PrivateKey
	servingCerts []*x509.Certificate
	clientID     string
	clientKey    *rsa.PrivateKey
	clientCerts  []*x509.Certificate
	sessionKey   []byte
	sshKey       *rsa.PrivateKey

	groupIDs         []string
	elevatedGroupIDs []string

	dbPortal            database.Portal
	dbOpenShiftClusters database.OpenShiftClusters

	dialer proxy.Dialer

	t *template.Template

	aad middleware.AAD
}

func NewPortal(env env.Core,
	audit *logrus.Entry,
	log *logrus.Entry,
	baseAccessLog *logrus.Entry,
	l net.Listener,
	sshl net.Listener,
	verifier middleware.Verifier,
	hostname string,
	servingKey *rsa.PrivateKey,
	servingCerts []*x509.Certificate,
	clientID string,
	clientKey *rsa.PrivateKey,
	clientCerts []*x509.Certificate,
	sessionKey []byte,
	sshKey *rsa.PrivateKey,
	groupIDs []string,
	elevatedGroupIDs []string,
	dbOpenShiftClusters database.OpenShiftClusters,
	dbPortal database.Portal,
	dialer proxy.Dialer) Runnable {
	return &portal{
		env:           env,
		audit:         audit,
		log:           log,
		baseAccessLog: baseAccessLog,
		l:             l,
		sshl:          sshl,
		verifier:      verifier,

		hostname:     hostname,
		servingKey:   servingKey,
		servingCerts: servingCerts,
		clientID:     clientID,
		clientKey:    clientKey,
		clientCerts:  clientCerts,
		sessionKey:   sessionKey,
		sshKey:       sshKey,

		groupIDs:         groupIDs,
		elevatedGroupIDs: elevatedGroupIDs,

		dbOpenShiftClusters: dbOpenShiftClusters,
		dbPortal:            dbPortal,

		dialer: dialer,
	}
}

func (p *portal) Run(ctx context.Context) error {
	asset, err := Asset("index.html")
	if err != nil {
		return err
	}

	p.t, err = template.New("index.html").Parse(string(asset))
	if err != nil {
		return err
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{
			{
				PrivateKey: p.servingKey,
			},
		},
		NextProtos: []string{"h2", "http/1.1"},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		},
		PreferServerCipherSuites: true,
		SessionTicketsDisabled:   true,
		MinVersion:               tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{
			tls.CurveP256,
			tls.X25519,
		},
	}

	for _, cert := range p.servingCerts {
		config.Certificates[0].Certificate = append(config.Certificates[0].Certificate, cert.Raw)
	}

	r := mux.NewRouter()
	r.Use(middleware.Panic(p.log))

	unauthenticatedRouter := r.NewRoute().Subrouter()
	p.unauthenticatedRoutes(unauthenticatedRouter)

	allGroups := append([]string{}, p.groupIDs...)
	allGroups = append(allGroups, p.elevatedGroupIDs...)

	p.aad, err = middleware.NewAAD(p.log, p.audit, p.env, p.baseAccessLog, p.hostname, p.sessionKey, p.clientID, p.clientKey, p.clientCerts, allGroups, unauthenticatedRouter, p.verifier)
	if err != nil {
		return err
	}

	aadAuthenticatedRouter := r.NewRoute().Subrouter()
	aadAuthenticatedRouter.Use(p.aad.AAD)
	aadAuthenticatedRouter.Use(middleware.Log(p.env, p.audit, p.baseAccessLog))
	aadAuthenticatedRouter.Use(p.aad.Redirect)
	aadAuthenticatedRouter.Use(csrf.Protect(p.sessionKey, csrf.SameSite(csrf.SameSiteStrictMode), csrf.MaxAge(0)))

	p.aadAuthenticatedRoutes(aadAuthenticatedRouter)

	ssh, err := ssh.New(p.env, p.log, p.baseAccessLog, p.sshl, p.sshKey, p.elevatedGroupIDs, p.dbOpenShiftClusters, p.dbPortal, p.dialer, aadAuthenticatedRouter)
	if err != nil {
		return err
	}

	err = ssh.Run()
	if err != nil {
		return err
	}

	kubeconfig.New(p.log, p.audit, p.env, p.baseAccessLog, p.servingCerts[0], p.elevatedGroupIDs, p.dbOpenShiftClusters, p.dbPortal, p.dialer, aadAuthenticatedRouter, unauthenticatedRouter)

	prometheus.New(p.log, p.dbOpenShiftClusters, p.dialer, aadAuthenticatedRouter)

	s := &http.Server{
		Handler:     frontendmiddleware.Lowercase(r),
		ReadTimeout: 10 * time.Second,
		IdleTimeout: 2 * time.Minute,
		ErrorLog:    log.New(p.log.Writer(), "", 0),
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	return s.Serve(tls.NewListener(p.l, config))
}

func (p *portal) unauthenticatedRoutes(r *mux.Router) {
	logger := middleware.Log(p.env, p.audit, p.baseAccessLog)

	r.NewRoute().Methods(http.MethodGet).Path("/healthz/ready").Handler(logger(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
}

func (p *portal) aadAuthenticatedRoutes(r *mux.Router) {
	for _, name := range AssetNames() {
		if name == "index.html" {
			continue
		}

		r.NewRoute().Methods(http.MethodGet).Path("/" + name).HandlerFunc(p.serve(name))
	}

	r.NewRoute().Methods(http.MethodGet).Path("/").HandlerFunc(p.index)

	r.NewRoute().Methods(http.MethodGet).Path("/api/clusters").HandlerFunc(p.clusters)
	r.NewRoute().Methods(http.MethodPost).Path("/api/logout").Handler(p.aad.Logout("/"))
}

func (p *portal) serve(path string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := Asset(path)
		if err != nil {
			p.internalServerError(w, err)
			return
		}

		http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(b))
	}
}

func (p *portal) index(w http.ResponseWriter, r *http.Request) {
	buf := &bytes.Buffer{}

	err := p.t.ExecuteTemplate(buf, "index.html", map[string]interface{}{
		"location":       p.env.Location(),
		csrf.TemplateTag: csrf.TemplateField(r),
	})
	if err != nil {
		p.internalServerError(w, err)
		return
	}

	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(buf.Bytes()))
}

func (p *portal) clusters(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	docs, err := p.dbOpenShiftClusters.ListAll(ctx)
	if err != nil {
		p.internalServerError(w, err)
		return
	}

	clusters := make([]string, 0, len(docs.OpenShiftClusterDocuments))
	for _, doc := range docs.OpenShiftClusterDocuments {
		ps := doc.OpenShiftCluster.Properties.ProvisioningState
		fps := doc.OpenShiftCluster.Properties.FailedProvisioningState

		switch {
		case ps == api.ProvisioningStateCreating,
			ps == api.ProvisioningStateDeleting,
			ps == api.ProvisioningStateFailed &&
				(fps == api.ProvisioningStateCreating ||
					fps == api.ProvisioningStateDeleting):
		default:
			clusters = append(clusters, doc.OpenShiftCluster.ID)
		}
	}

	sort.Strings(clusters)

	b, err := json.MarshalIndent(clusters, "", "    ")
	if err != nil {
		p.internalServerError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (p *portal) internalServerError(w http.ResponseWriter, err error) {
	p.log.Warn(err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
