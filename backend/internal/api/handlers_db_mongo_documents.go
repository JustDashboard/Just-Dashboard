package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Documents, the way MongoDB has them.
//
// The first Mongo browser read documents through the SQL browse route, which
// flattens them into a grid of strings, and wrote them back from that grid —
// so an edit turned a date into text and an ObjectId reference into a
// string. The routes here do not share a shape with SQL at all: a document
// arrives as canonical Extended JSON, is addressed by its _id as Extended
// JSON, and is written back from the same text, so what was not edited is
// stored as it was read.

// mongoNamespaceRequest is the part of every request that says where.
type mongoNamespaceRequest struct {
	Database   string `json:"database"`
	Collection string `json:"collection"`
}

// target resolves the database and requires a collection.
func (n mongoNamespaceRequest) target(conn *dbConnection) (string, string, error) {
	db, err := mongoDatabase(n.Database, conn)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(n.Collection) == "" {
		return "", "", httpx.BadRequest("a collection is required")
	}
	return db, n.Collection, nil
}

type mongoFindRequest struct {
	mongoNamespaceRequest
	dbx.MongoFindSpec
	// Count asks for the total beside the page. It is asked for unless this
	// is false, which is what a second page sends.
	Count *bool `json:"count"`
}

func (s *Server) handleMongoFind(w http.ResponseWriter, r *http.Request) error {
	// A read that is a POST only because a filter is a document.
	httpx.SkipAudit(r)
	var req mongoFindRequest
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
	res, err := dbx.MongoFindDocuments(ctx, client, db, collection, req.MongoFindSpec, req.Count == nil || *req.Count)
	if err != nil {
		return mongoReadFailed(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleMongoCount(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	var req mongoFindRequest
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
	count, err := dbx.MongoCountDocuments(ctx, client, db, collection, req.MongoFindSpec)
	if err != nil {
		return mongoReadFailed(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"count": count})
	return nil
}

type mongoDocumentRequest struct {
	mongoNamespaceRequest
	// ID is the _id as Extended JSON: {"$oid": "…"}, "a string", 42.
	ID string `json:"id"`
}

func (s *Server) handleMongoDocument(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	var req mongoDocumentRequest
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
	doc, err := dbx.MongoGetDocument(ctx, client, db, collection, req.ID)
	if err != nil {
		return mongoReadFailed(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"document": doc})
	return nil
}

// mongoDocumentBody is how large a request that carries whole documents may
// be. httpx.DecodeJSON stops at 4 MiB, which is less than one document can
// need: 16 MiB of BSON is, as canonical Extended JSON inside a JSON string,
// about as much for a document of long strings and four times as much for
// one of small numbers.
const mongoDocumentBody = 64 << 20

// decodeMongoDocuments is httpx.DecodeJSON with that limit and nothing else
// changed: the same content type, the same refusal of a field the request
// type does not have, the same refusal of a second value.
func decodeMongoDocuments(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return httpx.Err(http.StatusUnsupportedMediaType, "json_content_type_required",
			"request body must use application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, mongoDocumentBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return httpx.Err(http.StatusRequestEntityTooLarge, "document_too_large",
				"the request is larger than 64 MiB, which is what a document of 16 MiB usually needs as text; change the document with update operators instead of sending it whole")
		}
		return httpx.BadRequest("malformed request body: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return httpx.BadRequest("request body must contain exactly one JSON value")
	}
	return nil
}

type mongoInsertRequest struct {
	mongoNamespaceRequest
	// Documents is one document, or a list of documents, as text.
	Documents string `json:"documents"`
	// Ordered stops at the first document the server refuses. It is the
	// default; false tries every document.
	Ordered *bool `json:"ordered"`
}

func (s *Server) handleMongoInsertDocuments(w http.ResponseWriter, r *http.Request) error {
	var req mongoInsertRequest
	if err := decodeMongoDocuments(w, r, &req); err != nil {
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
	res, err := dbx.MongoInsertDocuments(ctx, client, db, collection, req.Documents, req.Ordered == nil || *req.Ordered)
	if err != nil {
		return mongoFailure(err)
	}
	detail := map[string]any{
		"database": db, "collection": collection,
		"inserted": res.Inserted, "refused": len(res.Errors),
	}
	// One document is identified by its id, as the first browser's insert
	// was. A batch is identified by its size.
	if len(res.InsertedIDs) == 1 {
		detail["id"] = mongoAuditText(res.InsertedIDs[0])
	}
	httpx.SetAudit(r, "database.document.insert", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

type mongoReplaceRequest struct {
	mongoNamespaceRequest
	dbx.MongoReplacement
}

func (s *Server) handleMongoReplaceDocument(w http.ResponseWriter, r *http.Request) error {
	var req mongoReplaceRequest
	if err := decodeMongoDocuments(w, r, &req); err != nil {
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
	res, err := dbx.MongoReplaceDocument(ctx, client, db, collection, req.MongoReplacement)
	// The audit entry is set before the error is returned: a replace that
	// found the document changed or gone is worth a line saying which.
	detail := map[string]any{
		"database": db, "collection": collection, "id": mongoAuditText(req.ID),
		"guarded": strings.TrimSpace(req.ExpectedDigest) != "" || strings.TrimSpace(req.Expected) != "",
	}
	if err != nil {
		httpx.SetAudit(r, "database.document.replace", conn.Name, detail)
		return mongoFailure(err)
	}
	detail["matched"], detail["modified"] = res.Matched, res.Modified
	httpx.SetAudit(r, "database.document.replace", conn.Name, detail)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

type mongoUpdateRequest struct {
	mongoNamespaceRequest
	dbx.MongoUpdate
	// All confirms that an empty filter is meant: every document in the
	// collection. Without it an empty filter with many is refused, so a
	// filter lost on the way here cannot become "all of them".
	All bool `json:"all"`
}

// handleMongoUpdateDocuments applies update operators to one document or to
// many. Updating every document in a collection is the one form of it that
// cannot be taken back by another update, so that form needs the destructive
// capability, checked here because the route cannot tell from its path.
func (s *Server) handleMongoUpdateDocuments(w http.ResponseWriter, r *http.Request) error {
	var req mongoUpdateRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	empty, err := dbx.MongoFilterIsEmpty(req.Filter)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	whole := empty && req.Many
	if whole && !req.DryRun {
		if !req.All {
			return httpx.BadRequest("the filter is empty, which matches every document in the collection; send all: true to mean that")
		}
		if err := s.mongoNeedsDestructive(r, "this updates every document in the collection"); err != nil {
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
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	res, err := dbx.MongoUpdateDocuments(ctx, client, db, collection, req.MongoUpdate)
	if err != nil {
		return mongoFailure(err)
	}
	if req.DryRun {
		// Nothing was written; a count is a read.
		httpx.SkipAudit(r)
	} else {
		httpx.SetAudit(r, "database.document.update", conn.Name, map[string]any{
			"database": db, "collection": collection, "filter": mongoAuditText(req.Filter),
			"many": req.Many, "upsert": req.Upsert, "everyDocument": whole,
			"matched": res.Matched, "modified": res.Modified, "upserted": res.UpsertedID != "",
		})
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

type mongoDeleteRequest struct {
	mongoNamespaceRequest
	dbx.MongoDeletion
	// All confirms that an empty filter is meant; see mongoUpdateRequest.
	All bool `json:"all"`
}

func (s *Server) handleMongoDeleteDocuments(w http.ResponseWriter, r *http.Request) error {
	var req mongoDeleteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	empty, err := dbx.MongoFilterIsEmpty(req.Filter)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	whole := empty && req.Many && strings.TrimSpace(req.ID) == ""
	if whole && !req.DryRun && !req.All {
		return httpx.BadRequest("the filter is empty, which matches every document in the collection; send all: true to mean that")
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
	// No typed phrase: a document is Mongo's row, and deleting one is the
	// same everyday act the SQL side stopped typing for.
	ctx, cancel := timeoutCtx(r, 5*time.Minute)
	defer cancel()
	res, err := dbx.MongoDeleteDocuments(ctx, client, db, collection, req.MongoDeletion)
	if err != nil {
		return mongoFailure(err)
	}
	if req.DryRun {
		httpx.SkipAudit(r)
	} else {
		detail := map[string]any{
			"database": db, "collection": collection, "many": req.Many,
			"everyDocument": whole, "deleted": res.Deleted,
		}
		if strings.TrimSpace(req.ID) != "" {
			detail["id"] = mongoAuditText(req.ID)
		} else {
			detail["filter"] = mongoAuditText(req.Filter)
		}
		httpx.SetAudit(r, "database.document.delete", conn.Name, detail)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleMongoCloneDocument(w http.ResponseWriter, r *http.Request) error {
	var req mongoDocumentRequest
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
	doc, err := dbx.MongoCloneDocument(ctx, client, db, collection, req.ID)
	if err != nil {
		return mongoFailure(err)
	}
	httpx.SetAudit(r, "database.document.clone", conn.Name, map[string]any{
		"database": db, "collection": collection,
		"id": mongoAuditText(req.ID), "newId": mongoAuditText(doc.ID),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"document": doc})
	return nil
}

// handleMongoExport streams a query's documents as a file: Extended JSON,
// one document per line, unless asked for an array or a CSV. It is a read,
// like the export every engine shares, and it is a GET because a browser
// download has to be a navigable URL.
//
// Everything that can go wrong for a reason worth a sentence is done before
// the first header is written, so it can still be an error response: a
// connection that fails here is a 502, not an empty file with a name that
// says it is the collection.
func (s *Server) handleMongoExport(w http.ResponseWriter, r *http.Request) error {
	client, conn, err := s.mongoClient(r)
	if err != nil {
		return err
	}
	defer client.Disconnect(context.Background())
	db, collection, err := mongoNamespaceQuery(r, conn, true)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	spec := dbx.MongoExportSpec{
		MongoFindSpec: dbx.MongoFindSpec{
			Filter: q.Get("filter"), Projection: q.Get("projection"), Sort: q.Get("sort"),
			Collation: q.Get("collation"), Hint: q.Get("hint"),
			Skip: int64(atoiDefault(q.Get("skip"), 0)), Limit: int64(atoiDefault(q.Get("limit"), 0)),
		},
		Format: q.Get("format"), Relaxed: q.Get("relaxed") == "1",
	}
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()
	export, err := dbx.MongoOpenExport(ctx, client, db, collection, spec)
	if err != nil {
		return mongoReadFailed(err)
	}
	defer export.Close()

	h := w.Header()
	h.Set("Content-Type", export.ContentType())
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", collection+"."+export.Extension()))
	// What a client needs to know about the file before it has read it: how
	// many documents it can hold, and whether that is all of them.
	h.Set("X-Export-Limit", strconv.FormatInt(export.Limit, 10))
	h.Set("X-Export-Truncated", export.Truncated())
	if export.Total != nil {
		h.Set("X-Export-Total", strconv.FormatInt(*export.Total, 10))
	}
	// A GET is not recorded by the mutation middleware, and a collection
	// leaving the server is worth a line. It is written once the cursor is
	// open and before the first byte, when httpx.AuditRead writes its own, so
	// an export that hangs or outlives the process is still on record.
	// AuditRead takes no detail, though, and which collection left is the
	// whole point of this entry, so it is written here with one.
	detail := map[string]any{
		"database": db, "collection": collection, "format": export.Extension(),
		"limit": export.Limit, "truncated": export.Truncated(), "filtered": strings.TrimSpace(spec.Filter) != "",
	}
	if export.Total != nil {
		detail["total"] = *export.Total
	}
	s.recordAudit(r, "database.export", conn.Name, detail)
	rows, err := export.Write(ctx, w)
	if err != nil {
		// The status has gone out, so the failure cannot be a body. Breaking
		// the response off is what a client can see: a download that failed,
		// rather than a short file that looks whole. The trail gets a second
		// entry saying how far it got, marked as the failure it is.
		detail["rows"], detail["error"] = rows, err.Error()
		s.recordExportFailure(r, conn.Name, detail)
		panic(http.ErrAbortHandler)
	}
	return nil
}

// recordExportFailure records an export that broke off after its first byte.
// The usual reason is the browser going away, which cancels the request's
// context, so the entry is written on one that is not cancelled with it.
func (s *Server) recordExportFailure(r *http.Request, target string, detail any) {
	p := httpx.MustPrincipal(r)
	s.Audit.Record(context.WithoutCancel(r.Context()), audit.Entry{
		UserID:   p.UserID(),
		Username: p.Username(),
		Role:     string(p.Role),
		IP:       httpx.ClientIP(r),
		Actor:    p.Kind,
		Action:   "database.export",
		Target:   target,
		Method:   r.Method,
		Path:     r.URL.Path,
		// What the request would have answered had it failed a moment
		// earlier, before its headers went out.
		Status:  http.StatusBadGateway,
		Success: false,
		Detail:  audit.Detail(detail),
	})
}
