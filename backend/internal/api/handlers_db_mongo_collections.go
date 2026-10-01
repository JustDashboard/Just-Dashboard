package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Collections, indexes and validation rules: the structure a database has,
// which in MongoDB is everything but the shape of the documents.

// mongoNamespaceQuery reads ?database= and ?collection= for the GET routes.
// The collection is required only where the route is about one.
func mongoNamespaceQuery(r *http.Request, conn *dbConnection, needCollection bool) (string, string, error) {
	q := r.URL.Query()
	db, err := mongoDatabase(q.Get("database"), conn)
	if err != nil {
		return "", "", err
	}
	collection := q.Get("collection")
	if needCollection && strings.TrimSpace(collection) == "" {
		return "", "", httpx.BadRequest("a collection is required")
	}
	return db, collection, nil
}

func (s *Server) handleMongoCollections(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, _, err := mongoNamespaceQuery(r, conn, false)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	list, err := dbx.MongoListCollections(ctx, client, db)
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

func (s *Server) handleMongoCollection(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoNamespaceQuery(r, conn, true)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	info, err := dbx.MongoCollectionInfo(ctx, client, db, collection)
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"database": db, "collection": info})
	return nil
}

type mongoCreateCollectionRequest struct {
	mongoNamespaceRequest
	dbx.MongoCollectionSpec
}

// kind names what a create request makes, for the audit trail.
func (req mongoCreateCollectionRequest) kind() string {
	switch {
	case strings.TrimSpace(req.ViewOn) != "":
		return "view"
	case req.TimeSeries != nil:
		return "timeseries"
	case req.Capped:
		return "capped"
	}
	return "collection"
}

func (s *Server) handleMongoCreateCollectionWith(w http.ResponseWriter, r *http.Request) error {
	var req mongoCreateCollectionRequest
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
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := dbx.MongoCreateCollectionWith(ctx, client, db, collection, req.MongoCollectionSpec); err != nil {
		return mongoFailure(err)
	}
	detail := map[string]any{"database": db, "collection": collection, "kind": req.kind()}
	if req.ViewOn != "" {
		detail["viewOn"] = req.ViewOn
	}
	httpx.SetAudit(r, "database.collection.create", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

type mongoRenameCollectionRequest struct {
	mongoNamespaceRequest
	To string `json:"to"`
	// DropTarget replaces a collection that already has the new name.
	DropTarget bool `json:"dropTarget"`
}

// handleMongoRenameCollection renames a collection. Renaming over an existing
// collection drops it, which is a drop by another route and is held to what
// a drop needs.
func (s *Server) handleMongoRenameCollection(w http.ResponseWriter, r *http.Request) error {
	var req mongoRenameCollectionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.DropTarget {
		if err := s.mongoNeedsDestructive(r, "renaming over an existing collection drops it"); err != nil {
			return err
		}
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
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := dbx.MongoRenameCollection(ctx, client, db, collection, req.To, req.DropTarget); err != nil {
		return mongoFailure(err)
	}
	httpx.SetAudit(r, "database.collection.rename", conn.Name, map[string]any{
		"database": db, "collection": collection, "to": req.To, "dropTarget": req.DropTarget,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

type mongoModifyCollectionRequest struct {
	mongoNamespaceRequest
	dbx.MongoCollectionChange
}

// changed lists which settings a collMod request carries, for the audit
// trail: what was changed, not what it was changed to.
func (req mongoModifyCollectionRequest) changed() []string {
	out := []string{}
	add := func(set bool, name string) {
		if set {
			out = append(out, name)
		}
	}
	add(req.Validator != nil, "validator")
	add(req.ValidationLevel != "", "validationLevel")
	add(req.ValidationAction != "", "validationAction")
	add(req.Pipeline != nil || req.ViewOn != "", "view")
	add(req.CappedSize != nil, "cappedSize")
	add(req.CappedMax != nil, "cappedMax")
	add(req.ExpireAfterSeconds != nil, "expireAfterSeconds")
	add(req.Granularity != "", "granularity")
	return out
}

// handleMongoModifyCollection changes a collection's options. Shrinking a
// capped collection and setting an expiry both delete documents that are
// already there, so those two are held to the destructive capability.
func (s *Server) handleMongoModifyCollection(w http.ResponseWriter, r *http.Request) error {
	var req mongoModifyCollectionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.RemovesData() {
		if err := s.mongoNeedsDestructive(r, "a smaller cap or a new expiry deletes documents already in the collection"); err != nil {
			return err
		}
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
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := dbx.MongoModifyCollection(ctx, client, db, collection, req.MongoCollectionChange); err != nil {
		return mongoFailure(err)
	}
	httpx.SetAudit(r, "database.collection.modify", conn.Name, map[string]any{
		"database": db, "collection": collection, "changed": req.changed(), "removesData": req.RemovesData(),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// --- Indexes --------------------------------------------------------------

func (s *Server) handleMongoIndexList(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoNamespaceQuery(r, conn, true)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	list, err := dbx.MongoListIndexes(ctx, client, db, collection)
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, list)
	return nil
}

type mongoCreateIndexRequest struct {
	mongoNamespaceRequest
	dbx.MongoIndexSpec
}

// handleMongoCreateIndex builds an index. A TTL index is the exception to an
// index being additive: the moment it exists, the server starts deleting
// every document older than its limit.
func (s *Server) handleMongoCreateIndex(w http.ResponseWriter, r *http.Request) error {
	var req mongoCreateIndexRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Expires() {
		if err := s.mongoNeedsDestructive(r, "a TTL index deletes every document already older than its limit"); err != nil {
			return err
		}
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
	// As long as the SQL index route allows: a build reads the whole
	// collection.
	ctx, cancel := timeoutCtx(r, 30*time.Minute)
	defer cancel()
	name, err := dbx.MongoCreateIndex(ctx, client, db, collection, req.MongoIndexSpec)
	if err != nil {
		return mongoFailure(err)
	}
	fields := make([]string, 0, len(req.Keys))
	for _, k := range req.Keys {
		fields = append(fields, k.Field+":"+k.Type)
	}
	httpx.SetAudit(r, "database.index.create", conn.Name, map[string]any{
		"database": db, "collection": collection, "index": name, "keys": fields,
		"unique": req.Unique, "ttl": req.Expires(),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"name": name})
	return nil
}

type mongoModifyIndexRequest struct {
	mongoNamespaceRequest
	dbx.MongoIndexChange
}

func (s *Server) handleMongoModifyIndex(w http.ResponseWriter, r *http.Request) error {
	var req mongoModifyIndexRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.RemovesData() {
		if err := s.mongoNeedsDestructive(r, "a new TTL limit deletes every document already older than it"); err != nil {
			return err
		}
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
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := dbx.MongoModifyIndex(ctx, client, db, collection, req.MongoIndexChange); err != nil {
		return mongoFailure(err)
	}
	detail := map[string]any{"database": db, "collection": collection, "index": req.Name}
	if req.Hidden != nil {
		detail["hidden"] = *req.Hidden
	}
	if req.ExpireAfterSeconds != nil {
		detail["expireAfterSeconds"] = *req.ExpireAfterSeconds
	}
	httpx.SetAudit(r, "database.index.modify", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

type mongoDropIndexRequest struct {
	mongoNamespaceRequest
	Name string `json:"name"`
}

func (s *Server) handleMongoDropIndex(w http.ResponseWriter, r *http.Request) error {
	var req mongoDropIndexRequest
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
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	if err := dbx.MongoDropIndex(ctx, client, db, collection, req.Name); err != nil {
		return mongoFailure(err)
	}
	httpx.SetAudit(r, "database.index.drop", conn.Name,
		map[string]any{"database": db, "collection": collection, "index": req.Name})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

// --- Validation -----------------------------------------------------------

func (s *Server) handleMongoValidation(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoNamespaceQuery(r, conn, true)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	rule, err := dbx.MongoReadValidation(ctx, client, db, collection)
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, rule)
	return nil
}

type mongoValidationRequest struct {
	mongoNamespaceRequest
	// Validator replaces the rule; an empty one removes it, and leaving the
	// field out keeps the rule and changes only the level or the action.
	Validator *string `json:"validator"`
	Level     string  `json:"level"`
	Action    string  `json:"action"`
}

func (s *Server) handleMongoValidationPut(w http.ResponseWriter, r *http.Request) error {
	var req mongoValidationRequest
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
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	if err := dbx.MongoWriteValidation(ctx, client, db, collection, req.Validator, req.Level, req.Action); err != nil {
		return mongoFailure(err)
	}
	// A validator is the collection's schema, so it is recorded the way a
	// DDL statement is: as what it now says.
	detail := map[string]any{"database": db, "collection": collection}
	if req.Validator != nil {
		detail["validator"] = mongoAuditText(*req.Validator)
	}
	if req.Level != "" {
		detail["level"] = req.Level
	}
	if req.Action != "" {
		detail["action"] = req.Action
	}
	httpx.SetAudit(r, "database.validation.set", conn.Name, detail)
	rule, err := dbx.MongoReadValidation(ctx, client, db, collection)
	if err != nil {
		return mongoReadFailure(err)
	}
	httpx.JSON(w, http.StatusOK, rule)
	return nil
}

type mongoValidationCheckRequest struct {
	mongoNamespaceRequest
	// Validator is a rule to try. Left out, the collection's own is checked.
	Validator string `json:"validator"`
	Samples   int    `json:"samples"`
	MaxTimeMS int64  `json:"maxTimeMS"`
}

func (s *Server) handleMongoValidationCheck(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	var req mongoValidationCheckRequest
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
	// Twice the Max time: once for the count and once for the samples.
	ctx, cancel := timeoutCtx(r, 2*dbx.MongoRequestTimeout(req.MaxTimeMS))
	defer cancel()
	check, err := dbx.MongoCheckValidation(ctx, client, db, collection, req.Validator, req.Samples, req.MaxTimeMS)
	if err != nil {
		return mongoFailure(err)
	}
	httpx.JSON(w, http.StatusOK, check)
	return nil
}
