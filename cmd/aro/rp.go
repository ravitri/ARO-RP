package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Azure/go-autorest/tracing"
	"github.com/sirupsen/logrus"
	kmetrics "k8s.io/client-go/tools/metrics"

	"github.com/Azure/ARO-RP/pkg/api"
	_ "github.com/Azure/ARO-RP/pkg/api/admin"
	_ "github.com/Azure/ARO-RP/pkg/api/v20191231preview"
	_ "github.com/Azure/ARO-RP/pkg/api/v20200430"
	_ "github.com/Azure/ARO-RP/pkg/api/v20210131preview"
	"github.com/Azure/ARO-RP/pkg/backend"
	"github.com/Azure/ARO-RP/pkg/database"
	"github.com/Azure/ARO-RP/pkg/env"
	"github.com/Azure/ARO-RP/pkg/frontend"
	"github.com/Azure/ARO-RP/pkg/frontend/adminactions"
	"github.com/Azure/ARO-RP/pkg/metrics/statsd"
	"github.com/Azure/ARO-RP/pkg/metrics/statsd/azure"
	"github.com/Azure/ARO-RP/pkg/metrics/statsd/k8s"
	"github.com/Azure/ARO-RP/pkg/util/clusterdata"
	"github.com/Azure/ARO-RP/pkg/util/encryption"
)

func rp(ctx context.Context, log, audit *logrus.Entry) error {
	_env, err := env.NewEnv(ctx, log)
	if err != nil {
		return err
	}

	var keys []string
	if _env.IsLocalDevelopmentMode() {
		keys = []string{
			"PULL_SECRET",
		}
	} else {
		keys = []string{
			"ACR_RESOURCE_ID",
			"ADMIN_API_CLIENT_CERT_COMMON_NAME",
			"MDM_ACCOUNT",
			"MDM_NAMESPACE",
		}

		if _, found := os.LookupEnv("PULL_SECRET"); found {
			return fmt.Errorf(`environment variable "PULL_SECRET" set`)
		}
	}
	for _, key := range keys {
		if _, found := os.LookupEnv(key); !found {
			return fmt.Errorf("environment variable %q unset", key)
		}
	}

	err = _env.InitializeAuthorizers()
	if err != nil {
		return err
	}

	m := statsd.New(ctx, log.WithField("component", "metrics"), _env, os.Getenv("MDM_ACCOUNT"), os.Getenv("MDM_NAMESPACE"))

	tracing.Register(azure.New(m))
	kmetrics.Register(kmetrics.RegisterOpts{
		RequestResult:  k8s.NewResult(m),
		RequestLatency: k8s.NewLatency(m),
	})

	dbKey, err := _env.ServiceKeyvault().GetBase64Secret(ctx, env.EncryptionSecretName)
	if err != nil {
		return err
	}

	aead, err := encryption.NewXChaCha20Poly1305(ctx, dbKey)
	if err != nil {
		return err
	}

	dbc, err := database.NewDatabaseClient(ctx, log.WithField("component", "database"), _env, m, aead)
	if err != nil {
		return err
	}

	dbAsyncOperations, err := database.NewAsyncOperations(ctx, _env.IsLocalDevelopmentMode(), dbc)
	if err != nil {
		return err
	}

	dbBilling, err := database.NewBilling(ctx, _env.IsLocalDevelopmentMode(), dbc)
	if err != nil {
		return err
	}

	dbOpenShiftClusters, err := database.NewOpenShiftClusters(ctx, _env.IsLocalDevelopmentMode(), dbc)
	if err != nil {
		return err
	}

	dbSubscriptions, err := database.NewSubscriptions(ctx, _env.IsLocalDevelopmentMode(), dbc)
	if err != nil {
		return err
	}

	go database.EmitMetrics(ctx, log, dbOpenShiftClusters, m)

	feKey, err := _env.ServiceKeyvault().GetBase64Secret(ctx, env.FrontendEncryptionSecretName)
	if err != nil {
		return err
	}

	feAead, err := encryption.NewXChaCha20Poly1305(ctx, feKey)
	if err != nil {
		return err
	}

	f, err := frontend.NewFrontend(ctx, audit, log.WithField("component", "frontend"), _env, dbAsyncOperations, dbOpenShiftClusters, dbSubscriptions, api.APIs, m, feAead, adminactions.NewKubeActions, adminactions.NewAzureActions, clusterdata.NewBestEffortEnricher)
	if err != nil {
		return err
	}

	b, err := backend.NewBackend(ctx, log.WithField("component", "backend"), _env, dbAsyncOperations, dbBilling, dbOpenShiftClusters, dbSubscriptions, aead, m)
	if err != nil {
		return err
	}

	// This part of the code orchestrates shutdown sequence. When sigterm is
	// received, it will trigger backend to stop accepting new documents and
	// finish old ones. Frontend will stop advertising itself to the loadbalancer.
	// When shutdown completes for frontend and backend "/healthz" endpoint
	// will go dark and external observer will know that shutdown sequence is finished
	sigterm := make(chan os.Signal, 1)
	stop := make(chan struct{})
	doneF := make(chan struct{})
	doneB := make(chan struct{})
	signal.Notify(sigterm, syscall.SIGTERM)

	log.Print("listening")
	go b.Run(ctx, stop, doneB)
	go f.Run(ctx, stop, doneF)

	<-sigterm
	log.Print("received SIGTERM")
	close(stop)
	<-doneB
	<-doneF

	return nil
}
