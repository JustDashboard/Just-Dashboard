package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// mountDatabaseSchemaRoutes registers the routes that read and change the objects a schema holds: the catalogue, definitions and structure changes. It is called inside the /databases
// route, so paths are relative to it and each group states the capability it
// needs.
func (s *Server) mountDatabaseSchemaRoutes(r chi.Router) {
	// Read surface. The catalogue and an object's definition are the schema
	// read a level deeper than the table list — what a view selects, what a
	// trigger does — and any role that may open a table may read them.
	r.Method(http.MethodGet, "/{id}/catalog", s.handle(s.handleDBCatalog))
	r.Method(http.MethodGet, "/{id}/object", s.handle(s.handleDBObject))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		// Changes that add to a schema or adjust it without discarding
		// anything, beside the create-table and add-column forms they extend.
		// One of them can still lose data depending on what it is asked for —
		// a column's new type — and checks for that by hand.
		r.Method(http.MethodPatch, "/{id}/ddl/column", s.handle(s.handleDDLAlterColumn))
		r.Method(http.MethodPost, "/{id}/ddl/foreign-key", s.handle(s.handleDDLAddForeignKey))
		r.Method(http.MethodPost, "/{id}/ddl/constraint", s.handle(s.handleDDLAddConstraint))
		r.Method(http.MethodPost, "/{id}/ddl/view", s.handle(s.handleDDLCreateView))
		r.Method(http.MethodPost, "/{id}/ddl/schema", s.handle(s.handleDDLCreateSchema))
		r.Method(http.MethodPost, "/{id}/ddl/comment", s.handle(s.handleDDLComment))
		r.Method(http.MethodPost, "/{id}/ddl/enum", s.handle(s.handleDDLCreateEnum))
		r.Method(http.MethodPost, "/{id}/ddl/enum/value", s.handle(s.handleDDLAddEnumValue))
	})
	// Anything that removes an object. A dropped constraint or view holds no
	// rows, and it is still a DROP: the query runner asks for the destructive
	// capability for one, so the form does too.
	s.destructive(r, func(r chi.Router) {
		r.Method(http.MethodDelete, "/{id}/ddl/foreign-key", s.handle(s.handleDDLDropForeignKey))
		r.Method(http.MethodDelete, "/{id}/ddl/constraint", s.handle(s.handleDDLDropConstraint))
		r.Method(http.MethodDelete, "/{id}/ddl/view", s.handle(s.handleDDLDropView))
		r.Method(http.MethodDelete, "/{id}/ddl/schema", s.handle(s.handleDDLDropSchema))
	})
}

// --- the catalogue ----------------------------------------------------------

func queryFlag(r *http.Request, name string) bool {
	v := r.URL.Query().Get(name)
	return v == "1" || strings.EqualFold(v, "true")
}

// handleDBCatalog returns the object tree: the schemas, and the objects of one
// of them grouped by kind.
//
// With no schema named it answers for the one the connection resolves an
// unqualified name to, which is the schema the operator is in. `all=1` asks
// for every schema at once instead.
func (s *Server) handleDBCatalog(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	catalog, err := dbx.ReadCatalog(ctx, pool, conn.Driver, dbx.CatalogOptions{
		Schema: q.Get("schema"), All: queryFlag(r, "all"), Limit: atoiDefault(q.Get("limit"), 0),
	})
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, catalog)
	return nil
}

// handleDBObject returns one object's definition: the CREATE statement as the
// engine reports it, and what else the catalogue says about the object.
func (s *Server) handleDBObject(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	id, err := parseID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	ref := dbx.ObjectRef{
		Kind: strings.TrimSpace(q.Get("kind")), Schema: q.Get("schema"), Name: q.Get("name"),
		Signature: q.Get("signature"), Table: q.Get("table"),
	}
	if ref.Kind == "" || ref.Name == "" {
		return httpx.BadRequest("kind and name are required")
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	def, err := dbx.ReadObjectDefinition(ctx, pool, conn.Driver, ref)
	switch {
	case err == nil:
	case errors.Is(err, dbx.ErrObjectNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, dbx.ErrAmbiguousObject):
		return httpx.Err(http.StatusConflict, "ambiguous_object", err.Error())
	case errors.Is(err, dbx.ErrNoDefinition), errors.Is(err, dbx.ErrUnsupported), errors.Is(err, dbx.ErrInvalidObject):
		return httpx.BadRequest("%v", err)
	default:
		return httpx.Err(http.StatusBadGateway, "query_failed", err.Error())
	}
	httpx.JSON(w, http.StatusOK, def)
	return nil
}

// --- structure changes ------------------------------------------------------

// ddlPreview reports whether the caller asked to be shown the statement
// rather than have it run.
func ddlPreview(r *http.Request) bool { return queryFlag(r, "preview") }

// ddlDecode reads a schema-change request and identifies the connection,
// without opening a pool: a request that is malformed, or aimed at an engine
// with no schema to edit, does not dial the database.
func (s *Server) ddlDecode(r *http.Request, req any) (*dbConnection, error) {
	id, err := parseID(r)
	if err != nil {
		return nil, err
	}
	if err := httpx.DecodeJSON(r, req); err != nil {
		return nil, err
	}
	conn, _, err := s.dbConnRow(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if !conn.Driver.IsSQL() {
		return nil, httpx.BadRequest("schema editing is for SQL engines; %s has its own surface", conn.Driver)
	}
	return conn, nil
}

// A ddlPlanner draws up the statements for a change. It is handed the pool
// because some engines can only restate a column by reading it first; it must
// not change anything.
type ddlPlanner func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error)

// runDDL is the second half of every schema-change handler: draw up the plan,
// then either show it or run it.
//
// `?preview=1` is what keeps the confirmation honest. A form used to show a
// statement it had assembled itself, in generic syntax, and the server then
// ran its own — quoted differently, spelled the engine's way — so what the
// operator approved was not what happened. Now the statement in the dialog is
// this function's, and it is the same text whether it is shown or run.
//
// A preview changes nothing, so it is not on the audit trail. A change is,
// whether or not the engine accepted it: the statement that was refused is as
// much a part of the record as the one that ran.
func (s *Server) runDDL(w http.ResponseWriter, r *http.Request, conn *dbConnection, timeout time.Duration,
	action string, detail map[string]any, plan ddlPlanner) error {
	pool, _, err := s.dbPool(r.Context(), conn.ID)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, timeout)
	defer cancel()
	p, err := plan(ctx, pool)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	statement := p.String()
	if ddlPreview(r) {
		httpx.SkipAudit(r)
		httpx.JSON(w, http.StatusOK, map[string]any{
			"statement": statement, "statements": p.Statements, "preview": true,
		})
		return nil
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["statement"] = statement
	httpx.SetAudit(r, action, conn.Name, detail)
	if err := p.Exec(ctx, pool); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"statement": statement, "statements": p.Statements})
	return nil
}

// planned adapts a plan that needed no connection to draw up.
func planned(plan *dbx.DDLPlan, err error) ddlPlanner {
	return func(context.Context, *sql.DB) (*dbx.DDLPlan, error) { return plan, err }
}

func planCreateIndex(driver dbx.Driver, spec dbx.IndexSpec) ddlPlanner {
	return func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error) {
		return dbx.PlanCreateIndex(ctx, pool, driver, spec)
	}
}

func planDropColumn(driver dbx.Driver, schema, table, column string) ddlPlanner {
	return func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error) {
		return dbx.PlanDropColumn(ctx, pool, driver, schema, table, column)
	}
}

type alterColumnRequest struct {
	Schema      string  `json:"schema"`
	Table       string  `json:"table"`
	Name        string  `json:"name"`
	Type        string  `json:"type"`
	Using       string  `json:"using"`
	Nullable    *bool   `json:"nullable"`
	Default     *string `json:"default"`
	DropDefault bool    `json:"dropDefault"`
}

// handleDDLAlterColumn changes a column's type, nullability or default.
//
// Whether it is destructive depends on the body, which the route cannot see: a
// new default loses nothing, and a new type rewrites every value in the
// column and discards whatever does not fit. So a change of type demands the
// destructive capability and spends its budget by hand, exactly as the query
// runner does for the ALTER it would classify the same way.
func (s *Server) handleDDLAlterColumn(w http.ResponseWriter, r *http.Request) error {
	var req alterColumnRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Table) == "" || strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("table and the column's name are required")
	}
	change := dbx.ColumnChange{
		Schema: req.Schema, Table: req.Table, Column: req.Name,
		Type: req.Type, Using: req.Using, Nullable: req.Nullable,
		Default: req.Default, DropDefault: req.DropDefault,
	}
	if change.ChangesType() {
		p := httpx.MustPrincipal(r)
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"changing a column's type rewrites its values and can lose them; your role does not permit it")
		}
		// A preview runs nothing, so it does not draw on the budget that
		// exists to slow down what does.
		if !ddlPreview(r) && !s.destrLim.Allow(p.Username()+"|dbddl") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited",
				"too many destructive schema changes, slow down")
		}
	}
	return s.runDDL(w, r, conn, 30*time.Minute, "database.ddl.alter_column",
		map[string]any{"table": req.Table, "column": req.Name, "typeChanged": change.ChangesType()},
		func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanAlterColumn(ctx, pool, conn.Driver, change)
		})
}

type foreignKeyRequest struct {
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"refSchema"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	OnDelete   string   `json:"onDelete"`
	OnUpdate   string   `json:"onUpdate"`
}

func (s *Server) handleDDLAddForeignKey(w http.ResponseWriter, r *http.Request) error {
	var req foreignKeyRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Table) == "" {
		return httpx.BadRequest("table is required")
	}
	// Adding a key validates every existing row against the referenced table,
	// which on a large one is as long as building an index.
	return s.runDDL(w, r, conn, 30*time.Minute, "database.ddl.add_foreign_key",
		map[string]any{"table": req.Table, "references": req.RefTable},
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanAddForeignKey(conn.Driver, dbx.ForeignKeySpec{
				Schema: req.Schema, Table: req.Table, Name: req.Name, Columns: req.Columns,
				RefSchema: req.RefSchema, RefTable: req.RefTable, RefColumns: req.RefColumns,
				OnDelete: req.OnDelete, OnUpdate: req.OnUpdate,
			})
		})
}

func (s *Server) handleDDLDropForeignKey(w http.ResponseWriter, r *http.Request) error {
	var req foreignKeyRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Table) == "" || strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("table and the foreign key's name are required")
	}
	return s.runDDL(w, r, conn, 5*time.Minute, "database.ddl.drop_foreign_key",
		map[string]any{"table": req.Table, "constraint": req.Name},
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanDropForeignKey(conn.Driver, req.Schema, req.Table, req.Name)
		})
}

type constraintRequest struct {
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Columns    []string `json:"columns"`
	Expression string   `json:"expression"`
}

func (s *Server) handleDDLAddConstraint(w http.ResponseWriter, r *http.Request) error {
	var req constraintRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Table) == "" {
		return httpx.BadRequest("table is required")
	}
	return s.runDDL(w, r, conn, 30*time.Minute, "database.ddl.add_constraint",
		map[string]any{"table": req.Table, "type": req.Type},
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanAddConstraint(conn.Driver, dbx.ConstraintSpec{
				Schema: req.Schema, Table: req.Table, Name: req.Name, Type: req.Type,
				Columns: req.Columns, Expression: req.Expression,
			})
		})
}

func (s *Server) handleDDLDropConstraint(w http.ResponseWriter, r *http.Request) error {
	var req constraintRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Table) == "" || strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("table and the constraint's name are required")
	}
	return s.runDDL(w, r, conn, 5*time.Minute, "database.ddl.drop_constraint",
		map[string]any{"table": req.Table, "constraint": req.Name},
		func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanDropConstraint(ctx, pool, conn.Driver, req.Schema, req.Table, req.Name, req.Type)
		})
}

type viewRequest struct {
	Schema       string `json:"schema"`
	Name         string `json:"name"`
	Query        string `json:"query"`
	Replace      bool   `json:"replace"`
	Materialized bool   `json:"materialized"`
}

// handleDDLCreateView creates a view, or replaces one. Replacing is not in the
// destructive group: it discards a definition and no rows, and the query
// runner classifies CREATE OR REPLACE VIEW the same way.
func (s *Server) handleDDLCreateView(w http.ResponseWriter, r *http.Request) error {
	var req viewRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("the view's name is required")
	}
	// A materialized view runs its query to completion as it is created.
	return s.runDDL(w, r, conn, 30*time.Minute, "database.ddl.create_view",
		map[string]any{"view": req.Name, "replace": req.Replace, "materialized": req.Materialized},
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanCreateView(conn.Driver, dbx.ViewSpec{
				Schema: req.Schema, Name: req.Name, Query: req.Query,
				Replace: req.Replace, Materialized: req.Materialized,
			})
		})
}

func (s *Server) handleDDLDropView(w http.ResponseWriter, r *http.Request) error {
	var req viewRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("the view's name is required")
	}
	return s.runDDL(w, r, conn, 5*time.Minute, "database.ddl.drop_view",
		map[string]any{"view": req.Name, "materialized": req.Materialized},
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanDropView(conn.Driver, req.Schema, req.Name, req.Materialized)
		})
}

type schemaRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleDDLCreateSchema(w http.ResponseWriter, r *http.Request) error {
	var req schemaRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("the schema's name is required")
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.create_schema",
		map[string]any{"schema": req.Name},
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanCreateSchema(conn.Driver, req.Name)
		})
}

// handleDDLDropSchema removes an empty schema. There is no cascade to ask
// for: a schema that still holds tables is refused by the engine, and that
// refusal is the guard — "drop this schema and everything in it" is a
// database drop by another name, and that takes a typed phrase this route
// does not have.
func (s *Server) handleDDLDropSchema(w http.ResponseWriter, r *http.Request) error {
	var req schemaRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("the schema's name is required")
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.drop_schema",
		map[string]any{"schema": req.Name},
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanDropSchema(conn.Driver, req.Name)
		})
}

type commentRequest struct {
	Schema  string `json:"schema"`
	Table   string `json:"table"`
	Column  string `json:"column"`
	Comment string `json:"comment"`
}

func (s *Server) handleDDLComment(w http.ResponseWriter, r *http.Request) error {
	var req commentRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Table) == "" {
		return httpx.BadRequest("table is required")
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.comment",
		map[string]any{"table": req.Table, "column": req.Column},
		func(ctx context.Context, pool *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanComment(ctx, pool, conn.Driver, dbx.CommentSpec{
				Schema: req.Schema, Table: req.Table, Column: req.Column, Comment: req.Comment,
			})
		})
}

type enumRequest struct {
	Schema      string   `json:"schema"`
	Name        string   `json:"name"`
	Values      []string `json:"values"`
	Value       string   `json:"value"`
	Before      string   `json:"before"`
	After       string   `json:"after"`
	IfNotExists bool     `json:"ifNotExists"`
}

func (s *Server) handleDDLCreateEnum(w http.ResponseWriter, r *http.Request) error {
	var req enumRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("the type's name is required")
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.create_enum",
		map[string]any{"type": req.Name, "labels": len(req.Values)},
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanCreateEnum(conn.Driver, req.Schema, req.Name, req.Values)
		})
}

func (s *Server) handleDDLAddEnumValue(w http.ResponseWriter, r *http.Request) error {
	var req enumRequest
	conn, err := s.ddlDecode(r, &req)
	if err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return httpx.BadRequest("the type's name is required")
	}
	return s.runDDL(w, r, conn, 60*time.Second, "database.ddl.add_enum_value",
		map[string]any{"type": req.Name, "label": req.Value},
		func(context.Context, *sql.DB) (*dbx.DDLPlan, error) {
			return dbx.PlanAddEnumValue(conn.Driver, dbx.EnumValueSpec{
				Schema: req.Schema, Name: req.Name, Value: req.Value,
				Before: req.Before, After: req.After, IfNotExists: req.IfNotExists,
			})
		})
}
