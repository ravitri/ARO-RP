package main

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"

	"github.com/sirupsen/logrus"

	utillog "github.com/Azure/ARO-RP/pkg/util/log"
	"github.com/Azure/ARO-RP/pkg/util/recover"
	"github.com/Azure/ARO-RP/pkg/util/version"
)

// When a developer runs the ARO-RP codebase locally with RP_MODE=development,
// all services listen on localhost with no authentication required.  `az aro`
// and `hack/cluster` are similarly configured to connect to localhost.
//
// When a developer runs the ARO-RP codebase remotely for development, it's not
// convenient to listen only on localhost and a protective authentication layer
// is hence also required.
//
// hack/tunnel patches up the second architecture so that it looks like the
// first from the perspective of our client tooling.  The tunnel listens on
// localhost, terminating local HTTPS connections without authentication and
// forwarding them to a remote location in a second connection protected by a
// TLS client certificate.  For better or worse, this means we can avoid having
// to add more configurability to the client libraries.

var (
	certFile       = flag.String("certFile", "secrets/localhost.crt", "file containing server certificate")
	keyFile        = flag.String("keyFile", "secrets/localhost.key", "file containing server key")
	clientCertFile = flag.String("clientCertFile", "secrets/dev-client.crt", "file containing client certificate")
	clientKeyFile  = flag.String("clientKeyFile", "secrets/dev-client.key", "file containing client key")
)

func run(ctx context.Context, log *logrus.Entry) error {
	if len(flag.Args()) != 1 {
		return fmt.Errorf("usage: %s IP", os.Args[0])
	}

	certb, err := ioutil.ReadFile(*certFile)
	if err != nil {
		return err
	}

	cert, err := x509.ParseCertificate(certb)
	if err != nil {
		return err
	}

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	keyb, err := ioutil.ReadFile(*keyFile)
	if err != nil {
		return err
	}

	key, err := x509.ParsePKCS1PrivateKey(keyb)
	if err != nil {
		return err
	}

	clientCertb, err := ioutil.ReadFile(*clientCertFile)
	if err != nil {
		return err
	}

	clientKeyb, err := ioutil.ReadFile(*clientKeyFile)
	if err != nil {
		return err
	}

	clientKey, err := x509.ParsePKCS1PrivateKey(clientKeyb)
	if err != nil {
		return err
	}

	l, err := tls.Listen("tcp", "localhost:8443", &tls.Config{
		Certificates: []tls.Certificate{
			{
				Certificate: [][]byte{
					certb,
				},
				PrivateKey: key,
			},
		},
	})
	if err != nil {
		return err
	}

	log.Print("listening")

	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}

		go func(c net.Conn) {
			defer c.Close()

			c2, err := tls.Dial("tcp", flag.Arg(0)+":443", &tls.Config{
				Certificates: []tls.Certificate{
					{
						Certificate: [][]byte{
							clientCertb,
						},
						PrivateKey: clientKey,
					},
				},
				ServerName: cert.Subject.CommonName,
				RootCAs:    pool,
			})
			if err != nil {
				log.Error(err)
				return
			}

			defer c2.Close()
			ch := make(chan struct{})

			go func() {
				defer recover.Panic(log)
				defer close(ch)
				defer func() {
					_ = c2.CloseWrite()
				}()
				_, _ = io.Copy(c2, c)
			}()

			defer func() {
				_ = c.(*tls.Conn).CloseWrite()
			}()
			_, _ = io.Copy(c, c2)

			<-ch
		}(c)
	}
}

func main() {
	log := utillog.GetLogger()

	log.Printf("starting, git commit %s", version.GitCommit)

	flag.Parse()

	if err := run(context.Background(), log); err != nil {
		log.Fatal(err)
	}
}
