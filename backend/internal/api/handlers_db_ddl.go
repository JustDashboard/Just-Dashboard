package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Schema editing and bulk loading, split from handlers_db.go because the data
// surface and the schema surface are different jobs with different guards, and
// one file carrying both had stopped being readable.
//
// The split in capability is by what a change can cost. One that only adds —
// a table, a column, an index, a new name — needs service.control, the same
// capability the query runner asks for the CREATE it classifies as medium
// risk. One that removes — DROP, TRUNCATE — is in the destructive group. A
// form must never be a cheaper way to destroy something than the SQL console
// is, and the forms in handlers_db_schema.go follow the same rule.
//
// Every handler here plans its statement and hands it to runDDL, which shows
// it for `?preview=1` and runs it otherwise.

type ddlRequest struct {
	Schema  string          `json:"schema"`
	Table   string          `json:"table"`
	Columns []dbx.NewColumn `json:"columns"`
	Column  dbx.NewColumn   `json:"column"`
	Name    string          `json:"name"`
	Unique  bool            `json:"unique"`
	Fields  []string        `json:"fields"`
	To      string          `json:"to"`
	Kind    string          `json:"kind"`
	// Index options. Each is refused by name on an engine that has no such
	// thing rather than dropped from the statement.
	Method       string `json:"method"`
	Where        string `json:"where"`
	IfNotExists  bool   `json:"ifNotExists"`
	Concurrently bool   `json:"concurrently"`
}

// ddlContext decodes the request and identifies the connection, without
// opening a pool.
//
// The pool comes after request validation, so invalid requests do not dial the database.
func (s *Server) ddlContext(r *http.Request) (*ddlRequest, *dbConnection, error) {
	var req ddlRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(req.Table) == "" {
		return nil, nil, httpx.BadRequest("table is required")
	}
	return &req, conn, nil
}

func (s *Server) handleDDLCreateTable(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.ddlContext(r)
	if err != nil {
		return err
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.create_table",
		map[string]any{"table": req.Table}, "",
		planned(dbx.PlanCreateTable(conn.Driver, req.Schema, req.Table, req.Columns)))
}

func (s *Server) handleDDLAddColumn(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.ddlContext(r)
	if err != nil {
		return err
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.add_column",
		map[string]any{"table": req.Table, "column": req.Column.Name}, "",
		planned(dbx.PlanAddColumn(conn.Driver, req.Schema, req.Table, req.Column)))
}

func (s *Server) handleDDLCreateIndex(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.ddlContext(r)
	if err != nil {
		return err
	}
	// Building an index locks or rewrites a large table on several engines, so
	// this gets the long timeout the dumps get rather than the short one the
	// other DDL uses.
	return s.runDDL(w, r, conn, 30*time.Minute, "database.ddl.create_index",
		map[string]any{"table": req.Table, "index": req.Name}, "",
		planCreateIndex(conn.Driver, dbx.IndexSpec{
			Schema: req.Schema, Table: req.Table, Name: req.Name, Columns: req.Fields,
			Unique: req.Unique, Method: req.Method, Where: req.Where,
			IfNotExists: req.IfNotExists, Concurrently: req.Concurrently,
		}))
}

// handleDDLRename covers both a table rename and a column rename, because they
// are the same intention and differ only in which name is being changed.
func (s *Server) handleDDLRename(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.ddlContext(r)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.To) == "" {
		return httpx.BadRequest("a new name is required")
	}
	plan := planned(dbx.PlanRenameTable(conn.Driver, req.Schema, req.Table, req.To))
	if req.Kind == "column" {
		if strings.TrimSpace(req.Name) == "" {
			return httpx.BadRequest("the column to rename is required")
		}
		plan = planned(dbx.PlanRenameColumn(conn.Driver, req.Schema, req.Table, req.Name, req.To))
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.rename",
		map[string]any{"kind": req.Kind, "table": req.Table, "to": req.To}, "", plan)
}

// --- destructive schema changes -------------------------------------------

func (s *Server) handleDDLDropTable(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.ddlContext(r)
	if err != nil {
		return err
	}
	return s.runDDL(w, r, conn, 5*time.Minute, "database.ddl.drop_table",
		map[string]any{"table": req.Table}, "",
		planned(dbx.PlanDropTable(conn.Driver, req.Schema, req.Table)))
}

func (s *Server) handleDDLDropColumn(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.ddlContext(r)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("the column to drop is required")
	}
	return s.runDDL(w, r, conn, 5*time.Minute, "database.ddl.drop_column",
		map[string]any{"table": req.Table, "column": req.Name}, "",
		planDropColumn(conn.Driver, req.Schema, req.Table, req.Name))
}

func (s *Server) handleDDLDropIndex(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.ddlContext(r)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("the index to drop is required")
	}
	// No typed phrase: an index holds no data of its own and the Structure tab
	// shows the definition that recreates it. Dropping one costs a rebuild, not
	// a restore from backup.
	return s.runDDL(w, r, conn, 5*time.Minute, "database.ddl.drop_index",
		map[string]any{"table": req.Table, "index": req.Name}, "",
		planned(dbx.PlanDropIndex(conn.Driver, req.Schema, req.Table, req.Name)))
}

func (s *Server) handleDDLTruncate(w http.ResponseWriter, r *http.Request) error {
	req, conn, err := s.ddlContext(r)
	if err != nil {
		return err
	}
	return s.runDDL(w, r, conn, 5*time.Minute, "database.ddl.truncate",
		map[string]any{"table": req.Table}, "",
		planned(dbx.PlanTruncate(conn.Driver, req.Schema, req.Table)))
}

// --- import ---------------------------------------------------------------

type importRequest struct {
	Schema      string   `json:"schema"`
	Table       string   `json:"table"`
	Format      string   `json:"format"`
	Data        string   `json:"data"`
	Columns     []string `json:"columns"`
	HasHeader   bool     `json:"hasHeader"`
	Truncate    bool     `json:"truncate"`
	StopOnError bool     `json:"stopOnError"`
	NullAs      string   `json:"nullAs"`
}

// handleDBImport loads pasted or uploaded data into a table.
//
// The body carries the data inline rather than as a multipart upload, which
// bounds it at DecodeJSON's 4 MB cap. That is a deliberate ceiling: a load
// larger than that belongs in the engine's own bulk loader, which is faster by
// orders of magnitude and does not hold an HTTP request open for it.
//
// Truncate makes this destructive, so it demands the capability by hand — the route cannot know, exactly as the query runner
// cannot know from its path whether the SQL in it deletes anything.
func (s *Server) handleDBImport(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req importRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Table) == "" {
		return httpx.BadRequest("table is required")
	}
	if strings.TrimSpace(req.Data) == "" {
		return httpx.BadRequest("no data to import")
	}
	if req.Truncate {
		p := httpx.MustPrincipal(r)
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"replacing a table's contents is destructive and your role does not permit it")
		}
		if !s.destrLim.Allow(p.Username() + "|dbimport") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited",
				"too many destructive imports, slow down")
		}
	}
	conn, dsn, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return err
	}
	if conn.Driver == dbx.DriverMongo {
		return s.importMongo(w, r, conn, dsn, &req)
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	opts := dbx.ImportOptions{
		Schema: req.Schema, Table: req.Table, Columns: req.Columns,
		HasHeader: req.HasHeader, Truncate: req.Truncate,
		StopOnError: req.StopOnError, NullAs: req.NullAs,
	}
	ctx, cancel := timeoutCtx(r, 15*time.Minute)
	defer cancel()

	var res *dbx.ImportResult
	if strings.EqualFold(req.Format, "json") {
		res, err = dbx.ImportJSON(ctx, pool, conn.Driver, strings.NewReader(req.Data), opts)
	} else {
		res, err = dbx.ImportCSV(ctx, pool, conn.Driver, strings.NewReader(req.Data), opts)
	}
	if err != nil {
		httpx.SetAudit(r, "database.import", conn.Name,
			map[string]any{"table": req.Table, "error": err.Error()})
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.import", conn.Name, map[string]any{
		"table": req.Table, "inserted": res.Inserted, "failed": res.Failed,
		"truncated": req.Truncate,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// importMongo is the document-store half of the import route.
//
// It is a separate path rather than a branch inside the shared one because the
// guarantees differ and the difference is worth being explicit about: the SQL
// import wraps everything in a transaction and either commits or rolls back,
// while a standalone Mongo server has no transaction to offer. Truncate here
// means dropping the collection, which is why it demands the same confirmation
// the SQL truncate does — that check has already run by the time we arrive.
func (s *Server) importMongo(w http.ResponseWriter, r *http.Request, conn *dbConnection, dsn string, req *importRequest) error {
	ctx, cancel := timeoutCtx(r, 15*time.Minute)
	defer cancel()

	client, err := dbx.MongoClient(ctx, dsn)
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "connect_failed", err.Error())
	}
	defer client.Disconnect(context.Background())

	database := req.Schema
	if database == "" {
		database = conn.Database
	}
	if database == "" {
		return httpx.BadRequest("a database is required")
	}
	if req.Truncate {
		if err := dbx.MongoDropCollection(ctx, client, database, req.Table); err != nil {
			return httpx.BadRequest("could not empty the collection first: %v", err)
		}
	}
	res, err := dbx.MongoImport(ctx, client, database, req.Table, req.Format, req.Data, req.StopOnError)
	if err != nil {
		httpx.SetAudit(r, "database.import", conn.Name,
			map[string]any{"collection": req.Table, "error": err.Error()})
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "database.import", conn.Name, map[string]any{
		"collection": req.Table, "inserted": res.Inserted, "failed": res.Failed,
		"truncated": req.Truncate,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
