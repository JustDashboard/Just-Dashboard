package api

import (
	"context"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The three reads that look at more than a page: what a pipeline produces a
// stage at a time, how the server plans a query, and what a collection's
// documents are made of. Each is bounded by a Max time, because each can be
// pointed at a collection of any size by somebody who only meant to look.

type mongoPreviewRequest struct {
	mongoNamespaceRequest
	dbx.MongoPreviewSpec
}

// handleMongoPreview returns a few documents of what a pipeline has produced
// by one of its stages. It cannot write: a writing stage is never run, and a
// stage that is not known to be a read is refused — which is why this route,
// unlike running the pipeline, needs no capability.
func (s *Server) handleMongoPreview(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	var req mongoPreviewRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := req.target(conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, dbx.MongoRequestTimeout(req.MaxTimeMS))
	defer cancel()
	res, err := dbx.MongoPreviewPipeline(ctx, client, db, collection, req.MongoPreviewSpec)
	if err != nil {
		return mongoReadFailed(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

type mongoExplainRequest struct {
	mongoNamespaceRequest
	dbx.MongoExplainSpec
}

// handleMongoExplain plans a find or an aggregation. With executionStats the
// server runs the query to count what it touches, which is a read; a
// pipeline that writes is only ever planned.
func (s *Server) handleMongoExplain(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	var req mongoExplainRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := req.target(conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, dbx.MongoRequestTimeout(req.MaxTimeMS))
	defer cancel()
	res, err := dbx.MongoExplain(ctx, client, db, collection, req.MongoExplainSpec)
	if err != nil {
		return mongoReadFailed(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

type mongoSchemaRequest struct {
	mongoNamespaceRequest
	dbx.MongoSchemaSpec
}

func (s *Server) handleMongoSchema(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	var req mongoSchemaRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := req.target(conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, dbx.MongoRequestTimeout(req.MaxTimeMS))
	defer cancel()
	schema, err := dbx.MongoAnalyseSchema(ctx, client, db, collection, req.MongoSchemaSpec)
	if err != nil {
		return mongoReadFailed(err)
	}
	httpx.JSON(w, http.StatusOK, schema)
	return nil
}
