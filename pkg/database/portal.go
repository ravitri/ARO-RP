package database

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/ARO-RP/pkg/api"
	"github.com/Azure/ARO-RP/pkg/database/cosmosdb"
)

type portals struct {
	c cosmosdb.PortalDocumentClient
}

// Portal is the database interface for PortalDocuments
type Portal interface {
	Create(context.Context, *api.PortalDocument) (*api.PortalDocument, error)
	Get(context.Context, string) (*api.PortalDocument, error)
	Patch(context.Context, string, func(*api.PortalDocument) error) (*api.PortalDocument, error)
}

// NewPortal returns a new Portal
func NewPortal(ctx context.Context, isLocalDevelopmentMode bool, dbc cosmosdb.DatabaseClient) (Portal, error) {
	dbid, err := databaseName(isLocalDevelopmentMode)
	if err != nil {
		return nil, err
	}

	collc := cosmosdb.NewCollectionClient(dbc, dbid)

	documentClient := cosmosdb.NewPortalDocumentClient(collc, collPortal)
	return NewPortalWithProvidedClient(documentClient), nil
}

func NewPortalWithProvidedClient(client cosmosdb.PortalDocumentClient) Portal {
	return &portals{
		c: client,
	}
}

func (c *portals) Create(ctx context.Context, doc *api.PortalDocument) (*api.PortalDocument, error) {
	if doc.ID != strings.ToLower(doc.ID) {
		return nil, fmt.Errorf("id %q is not lower case", doc.ID)
	}

	doc, err := c.c.Create(ctx, doc.ID, doc, nil)

	if err, ok := err.(*cosmosdb.Error); ok && err.StatusCode == http.StatusConflict {
		err.StatusCode = http.StatusPreconditionFailed
	}

	return doc, err
}

func (c *portals) Get(ctx context.Context, id string) (*api.PortalDocument, error) {
	if id != strings.ToLower(id) {
		return nil, fmt.Errorf("id %q is not lower case", id)
	}

	return c.c.Get(ctx, id, id, nil)
}

func (c *portals) Patch(ctx context.Context, id string, f func(*api.PortalDocument) error) (*api.PortalDocument, error) {
	var doc *api.PortalDocument

	err := cosmosdb.RetryOnPreconditionFailed(func() (err error) {
		doc, err = c.Get(ctx, id)
		if err != nil {
			return
		}

		err = f(doc)
		if err != nil {
			return
		}

		doc, err = c.c.Replace(ctx, doc.ID, doc, nil)
		return
	})

	return doc, err
}
