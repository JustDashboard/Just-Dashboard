package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
	"go.mongodb.org/mongo-driver/mongo"
)

// MongoDB has its own file for the same reason Redis does: a client opened
// per request, and a vocabulary of documents and collections rather than rows.

// mountDatabaseMongoRoutes registers every route that speaks MongoDB. It is
// called inside the /databases route, so paths are relative to it and each
// group states the capability it needs.
func (s *Server) mountDatabaseMongoRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/collections/indexes", s.handle(s.handleMongoIndexes))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/{id}/documents", s.handle(s.handleMongoInsert))
		r.Method(http.MethodPatch, "/{id}/documents", s.handle(s.handleMongoReplace))
		r.Method(http.MethodPost, "/{id}/aggregate", s.handle(s.handleMongoAggregate))
		r.Method(http.MethodPost, "/{id}/collections", s.handle(s.handleMongoCreateCollection))
	})
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/{id}/documents", s.handle(s.handleMongoDelete))
		r.Method(http.MethodDelete, "/{id}/collections", s.handle(s.handleMongoDropCollection))
	})
}

// --- MongoDB documents ----------------------------------------------------

func (s *Server) mongoClient(r *http.Request) (*mongo.Client, *dbConnection, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, nil, err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	if conn.Driver != dbx.DriverMongo {
		return nil, nil, httpx.BadRequest("this endpoint is for MongoDB connections")
	}
	client, err := dbx.MongoClient(r.Context(), dsn)
	if err != nil {
		return nil, conn, httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	return client, conn, nil
}

type mongoDocRequest struct {
	Database   string `json:"database"`
	Collection string `json:"collection"`
	Filter     string `json:"filter"`
	Document   string `json:"document"`
	Pipeline   string `json:"pipeline"`
	Many       bool   `json:"many"`
	Limit      int    `json:"limit"`
}

// mongoTarget resolves the database and collection, defaulting the database to
// the one named in the connection string so the common single-database setup
// needs no extra field.
func mongoTarget(req *mongoDocRequest, conn *dbConnection) (string, string, error) {
	db := req.Database
	if db == "" {
		db = conn.Database
	}
	if db == "" {
		return "", "", httpx.BadRequest("a database is required")
	}
	if strings.TrimSpace(req.Collection) == "" {
		return "", "", httpx.BadRequest("a collection is required")
	}
	return db, req.Collection, nil
}

func (s *Server) handleMongoIndexes(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	q := r.URL.Query()
	db := q.Get("schema")
	if db == "" {
		db = conn.Database
	}
	collection := q.Get("table")
	if db == "" || collection == "" {
		return httpx.BadRequest("schema and table are required")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	indexes, err := dbx.MongoIndexes(ctx, client, db, collection)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	stats, _ := dbx.MongoCollStats(ctx, client, db, collection)
	httpx.JSON(w, http.StatusOK, map[string]any{"indexes": indexes, "stats": stats})
	return nil
}

func (s *Server) handleMongoInsert(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	id, err := dbx.MongoInsert(ctx, client, db, collection, req.Document)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.document.insert", conn.Name,
		map[string]any{"database": db, "collection": collection, "id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"insertedId": id})
	return nil
}

func (s *Server) handleMongoReplace(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	n, err := dbx.MongoReplace(ctx, client, db, collection, req.Filter, req.Document)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.document.replace", conn.Name,
		map[string]any{"database": db, "collection": collection, "filter": req.Filter, "modified": n})
	httpx.JSON(w, http.StatusOK, map[string]any{"modified": n})
	return nil
}

func (s *Server) handleMongoDelete(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	// No typed phrase: a document is Mongo's row, and deleting one is the same
	// everyday act the SQL side stopped typing for. Dropping the whole
	// collection below uses ordinary confirmation too.
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	n, err := dbx.MongoDelete(ctx, client, db, collection, req.Filter, req.Many)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.document.delete", conn.Name,
		map[string]any{"database": db, "collection": collection, "filter": req.Filter, "deleted": n})
	httpx.JSON(w, http.StatusOK, map[string]any{"deleted": n})
	return nil
}

// handleMongoAggregate runs a pipeline. A pipeline ending in $out or $merge
// writes a whole collection, so it is checked by content the way SQL is
// classified by content — the route cannot tell from its path.
func (s *Server) handleMongoAggregate(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Pipeline) == "" {
		return httpx.BadRequest("a pipeline is required")
	}
	destructive := dbx.MongoWritesInPipeline(req.Pipeline)
	if destructive {
		p := httpx.MustPrincipal(r)
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"this pipeline writes a collection and your role does not permit it")
		}
		if !s.destrLim.Allow(p.Username() + "|mongoagg") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited",
				"too many writing pipelines, slow down")
		}
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 120*time.Second)
	defer cancel()
	res, err := dbx.MongoAggregate(ctx, client, db, collection, req.Pipeline, req.Limit)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.aggregate", conn.Name, map[string]any{
		"database": db, "collection": collection,
		"writes": destructive, "rowCount": res.RowCount,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"result": res, "writes": destructive})
	return nil
}

func (s *Server) handleMongoCreateCollection(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := dbx.MongoCreateCollection(ctx, client, db, collection); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.collection.create", conn.Name,
		map[string]any{"database": db, "collection": collection})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) handleMongoDropCollection(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoTarget(&req, conn)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := dbx.MongoDropCollection(ctx, client, db, collection); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.collection.drop", conn.Name,
		map[string]any{"database": db, "collection": collection})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
