package api

import (
	"context"
	"errors"
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
//
// The routes under /mongo are the engine's own surface: documents travel as
// Extended JSON with their types, and every text field accepts the shell's
// spelling. The handful outside it are the first Mongo browser's, kept
// answering as they did.
func (s *Server) mountDatabaseMongoRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{id}/collections/indexes", s.handle(s.handleMongoIndexes))
	// Reading. These are POSTs where the query does not fit a URL — a filter
	// is a document, not a parameter — and they change nothing, so any role
	// reaches them, as it reaches the SQL browse and explain routes.
	r.Method(http.MethodPost, "/{id}/mongo/find", s.handle(s.handleMongoFind))
	r.Method(http.MethodPost, "/{id}/mongo/count", s.handle(s.handleMongoCount))
	r.Method(http.MethodPost, "/{id}/mongo/document", s.handle(s.handleMongoDocument))
	r.Method(http.MethodPost, "/{id}/mongo/explain", s.handle(s.handleMongoExplain))
	r.Method(http.MethodPost, "/{id}/mongo/aggregate/preview", s.handle(s.handleMongoPreview))
	r.Method(http.MethodPost, "/{id}/mongo/schema", s.handle(s.handleMongoSchema))
	r.Method(http.MethodGet, "/{id}/mongo/export", s.handle(s.handleMongoExport))
	r.Method(http.MethodGet, "/{id}/mongo/collections", s.handle(s.handleMongoCollections))
	r.Method(http.MethodGet, "/{id}/mongo/collection", s.handle(s.handleMongoCollection))
	r.Method(http.MethodGet, "/{id}/mongo/indexes", s.handle(s.handleMongoIndexList))
	r.Method(http.MethodGet, "/{id}/mongo/validation", s.handle(s.handleMongoValidation))
	r.Method(http.MethodPost, "/{id}/mongo/validation/check", s.handle(s.handleMongoValidationCheck))
	// Reading the server is reading what it reports about itself, which the
	// SQL engines' activity, statements and settings already show any role.
	// The account list is names and roles; a credential is never asked for.
	r.Method(http.MethodGet, "/{id}/mongo/server", s.handle(s.handleMongoServer))
	r.Method(http.MethodGet, "/{id}/mongo/databases", s.handle(s.handleMongoDatabases))
	r.Method(http.MethodGet, "/{id}/mongo/ops", s.handle(s.handleMongoOps))
	r.Method(http.MethodGet, "/{id}/mongo/profiler", s.handle(s.handleMongoProfiler))
	r.Method(http.MethodGet, "/{id}/mongo/replication", s.handle(s.handleMongoReplication))
	r.Method(http.MethodGet, "/{id}/mongo/users", s.handle(s.handleMongoUsers))
	r.Method(http.MethodGet, "/{id}/mongo/roles", s.handle(s.handleMongoRoles))
	// Says what a console command would need, without running it.
	r.Method(http.MethodPost, "/{id}/mongo/command/classify", s.handle(s.handleMongoCommandClassify))
	r.Method(http.MethodGet, "/{id}/mongo/commands", s.handle(s.handleMongoCommandList))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		r.Method(http.MethodPost, "/{id}/documents", s.handle(s.handleMongoInsert))
		r.Method(http.MethodPatch, "/{id}/documents", s.handle(s.handleMongoReplace))
		r.Method(http.MethodPost, "/{id}/aggregate", s.handle(s.handleMongoAggregate))
		r.Method(http.MethodPost, "/{id}/collections", s.handle(s.handleMongoCreateCollection))
		r.Method(http.MethodPost, "/{id}/mongo/documents", s.handle(s.handleMongoInsertDocuments))
		r.Method(http.MethodPut, "/{id}/mongo/documents", s.handle(s.handleMongoReplaceDocument))
		r.Method(http.MethodPost, "/{id}/mongo/documents/clone", s.handle(s.handleMongoCloneDocument))
		// Several routes here are routine as asked for by default and have
		// one option that removes data: an update of every document, a
		// rename over an existing collection, a TTL that starts expiring
		// what is already there. Their handlers demand the destructive
		// capability for those by hand, as the query runner does for SQL.
		r.Method(http.MethodPatch, "/{id}/mongo/documents", s.handle(s.handleMongoUpdateDocuments))
		r.Method(http.MethodPost, "/{id}/mongo/collections", s.handle(s.handleMongoCreateCollectionWith))
		r.Method(http.MethodPost, "/{id}/mongo/collections/rename", s.handle(s.handleMongoRenameCollection))
		r.Method(http.MethodPatch, "/{id}/mongo/collections", s.handle(s.handleMongoModifyCollection))
		r.Method(http.MethodPost, "/{id}/mongo/indexes", s.handle(s.handleMongoCreateIndex))
		r.Method(http.MethodPatch, "/{id}/mongo/indexes", s.handle(s.handleMongoModifyIndex))
		r.Method(http.MethodPut, "/{id}/mongo/validation", s.handle(s.handleMongoValidationPut))
		// The console is a text box that can say anything, so nobody reaches
		// it on the read surface, and what a given command needs beyond this
		// is decided from the command.
		r.Method(http.MethodPost, "/{id}/mongo/command", s.handle(s.handleMongoCommand))
	})
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/{id}/documents", s.handle(s.handleMongoDelete))
		r.Method(http.MethodDelete, "/{id}/collections", s.handle(s.handleMongoDropCollection))
		r.Method(http.MethodDelete, "/{id}/mongo/documents", s.handle(s.handleMongoDeleteDocuments))
		r.Method(http.MethodDelete, "/{id}/mongo/collections", s.handle(s.handleMongoDropCollection))
		r.Method(http.MethodDelete, "/{id}/mongo/indexes", s.handle(s.handleMongoDropIndex))
		// Killing an operation stops work in flight, the same as killing a
		// SQL session.
		r.Method(http.MethodPost, "/{id}/mongo/killop", s.handle(s.handleMongoKillOp))
	})
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// The profiler's slow threshold is one setting for the whole server,
		// and level 2 records every operation on a database: a server
		// setting, changed by whoever may change those.
		r.Method(http.MethodPut, "/{id}/mongo/profiler", s.handle(s.handleMongoProfilerPut))
		r.Method(http.MethodPost, "/{id}/mongo/users", s.handle(s.handleMongoUserCreate))
		r.Method(http.MethodPut, "/{id}/mongo/users", s.handle(s.handleMongoUserPassword))
		r.Method(http.MethodPost, "/{id}/mongo/users/grant", s.handle(s.handleMongoUserGrant))
		r.Method(http.MethodPost, "/{id}/mongo/users/revoke", s.handle(s.handleMongoUserRevoke))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}/mongo/users", s.handle(s.handleMongoUserDrop))
		})
	})
}

// --- MongoDB documents ----------------------------------------------------

// mongoRow resolves a connection row, refusing any connection that is not
// MongoDB. It dials nothing.
func (s *Server) mongoRow(r *http.Request) (*dbConnection, string, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, "", err
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, "", err
	}
	if conn.Driver != dbx.DriverMongo {
		return nil, "", httpx.BadRequest("this endpoint is for MongoDB connections")
	}
	return conn, dsn, nil
}

func (s *Server) mongoClient(r *http.Request) (*mongo.Client, *dbConnection, error) {
	conn, dsn, err := s.mongoRow(r)
	if err != nil {
		return nil, nil, err
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
	db, err := mongoDatabase(req.Database, conn)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(req.Collection) == "" {
		return "", "", httpx.BadRequest("a collection is required")
	}
	return db, req.Collection, nil
}

// mongoDatabase names the database a request is about, defaulting to the one
// in the connection string.
func mongoDatabase(name string, conn *dbConnection) (string, error) {
	if name == "" {
		name = conn.Database
	}
	if name == "" {
		return "", httpx.BadRequest("a database is required")
	}
	return name, nil
}

// mongoFailure turns an error from the Mongo layer into a response, for a
// request that carried something the server could object to: a filter, a
// document, a pipeline. The server refusing that is the request's fault and
// is answered 400 in the server's own words; the connection failing is not.
func mongoFailure(err error) error {
	var apiErr *httpx.APIError
	switch {
	case errors.As(err, &apiErr):
		return err
	case errors.Is(err, dbx.ErrMongoNotFound):
		return httpx.Err(http.StatusNotFound, "document_not_found", err.Error())
	case errors.Is(err, dbx.ErrMongoWithheld):
		return httpx.Err(http.StatusForbidden, "credentials_withheld", err.Error())
	case errors.Is(err, dbx.ErrMongoNoCollection):
		return httpx.Err(http.StatusNotFound, "collection_not_found", err.Error())
	case errors.Is(err, dbx.ErrMongoChanged):
		return httpx.Err(http.StatusConflict, "document_changed", err.Error())
	case dbx.MongoTimedOut(err):
		return httpx.Err(http.StatusGatewayTimeout, "query_timeout",
			"the server did not finish within the time allowed; raise Max time or narrow the query")
	case dbx.MongoUnreachable(err):
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	return httpx.BadRequest("%v", err)
}

// mongoReadFailure is the same for a catalogue read, which carries nothing
// but names: there the server refusing is the server's state — an account
// without the privilege, a command this version lacks — and is answered 502
// like every other failed read.
func mongoReadFailure(err error) error {
	var refused mongo.ServerError
	if errors.As(err, &refused) && !dbx.MongoTimedOut(err) {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	return mongoFailure(err)
}

// mongoLegacyFailure is for the routes the first Mongo browser used, which
// answer 400 for whatever went wrong and are kept answering so. The one
// thing they did not refuse then and do now gets the code it has everywhere.
func mongoLegacyFailure(err error) error {
	if errors.Is(err, dbx.ErrMongoWithheld) {
		return mongoFailure(err)
	}
	return httpx.BadRequest("%v", err)
}

// mongoNeedsDestructive applies, by hand, what s.destructive applies to a
// whole route: the capability and the tighter budget. It is for the routes
// that are routine by default and destructive by one option.
func (s *Server) mongoNeedsDestructive(r *http.Request, why string) error {
	p := httpx.MustPrincipal(r)
	if !p.Can(auth.CapDestructive) {
		return httpx.Err(http.StatusForbidden, "forbidden", why+", and your role does not permit that (destructive)")
	}
	if !s.destrLim.Allow(p.Username() + "|mongo") {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited", "too many destructive requests, slow down")
	}
	return nil
}

// mongoAuditText shortens text bound for the audit trail. A filter says what
// a write reached, which is what the trail is for; it is cut so one request
// cannot write a megabyte into it.
func mongoAuditText(text string) string {
	const limit = 500
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && text[cut]&0xC0 == 0x80 {
		cut--
	}
	return text[:cut] + "…"
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
		return mongoLegacyFailure(err)
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
		return mongoLegacyFailure(err)
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
		return mongoLegacyFailure(err)
	}
	httpx.SetAudit(r, "database.document.delete", conn.Name,
		map[string]any{"database": db, "collection": collection, "filter": req.Filter, "deleted": n})
	httpx.JSON(w, http.StatusOK, map[string]any{"deleted": n})
	return nil
}

type mongoAggregateRequest struct {
	Database   string `json:"database"`
	Collection string `json:"collection"`
	dbx.MongoAggregateSpec
	// The three fields below are read and ignored. This route used to share
	// a request shape with the document routes, and a body that carried one
	// of them was accepted; refusing it now as an unknown field would break
	// a caller that changed nothing.
	Filter   string `json:"filter"`
	Document string `json:"document"`
	Many     bool   `json:"many"`
}

// handleMongoAggregate runs a pipeline. A pipeline ending in $out or $merge
// writes a whole collection, so it is checked by content the way SQL is
// classified by content — the route cannot tell from its path. The check
// reads the parsed pipeline, and a stage it does not know is not assumed to
// be a read.
func (s *Server) handleMongoAggregate(w http.ResponseWriter, r *http.Request) error {
	var req mongoAggregateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Pipeline) == "" {
		return httpx.BadRequest("a pipeline is required")
	}
	info, err := dbx.MongoClassifyPipeline(req.Pipeline)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if info.Destructive() {
		why := "this pipeline writes a collection"
		if !info.Writes {
			why = "this pipeline holds " + info.Unknown[0] + ", which is not known to be a read"
		}
		if err := s.mongoNeedsDestructive(r, why); err != nil {
			return err
		}
	}
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, err := mongoDatabase(req.Database, conn)
	if err != nil {
		return err
	}
	// The two defaults are the ones this route had before it took either
	// from the request.
	if req.Limit <= 0 {
		req.Limit = 100
	}
	if req.MaxTimeMS <= 0 {
		req.MaxTimeMS = (2 * time.Minute).Milliseconds()
	}
	ctx, cancel := timeoutCtx(r, dbx.MongoRequestTimeout(req.MaxTimeMS))
	defer cancel()
	res, err := dbx.MongoRunPipeline(ctx, client, db, req.Collection, req.MongoAggregateSpec)
	if err != nil {
		return mongoFailure(err)
	}
	httpx.SetAudit(r, "database.aggregate", conn.Name, map[string]any{
		"database": db, "collection": req.Collection, "stages": info.Stages,
		"writes": info.Destructive(), "rowCount": res.Returned,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{
		"result": res.Grid, "writes": info.Destructive(), "pipeline": info,
		"documents": res.Documents, "returned": res.Returned, "hasMore": res.HasMore,
		"truncated": res.Truncated, "durationMs": res.DurationMs, "statement": res.Statement,
	})
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
		return mongoLegacyFailure(err)
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
		return mongoLegacyFailure(err)
	}
	httpx.SetAudit(r, "database.collection.drop", conn.Name,
		map[string]any{"database": db, "collection": collection})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}
