package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// mountDatabaseWorkbenchRoutes registers the routes the table editor and the
// SQL editor are built on. It is called inside the /databases route, so paths
// are relative to it and each group states the capability it needs.
func (s *Server) mountDatabaseWorkbenchRoutes(r chi.Router) {
	// One whole value of a cell the grid showed a preview of. Read surface: it
	// is a column of a row the browse route already returns to any role.
	r.Method(http.MethodGet, "/{id}/cell", s.handle(s.handleDBCell))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapServiceControl))
		// The grid's staged edits. A set that deletes is checked by content in
		// the handler, the way the query route checks its statement: the path
		// cannot know what the body holds.
		r.Method(http.MethodPost, "/{id}/changes", s.handle(s.handleDBChanges))
		r.Method(http.MethodPost, "/{id}/script", s.handle(s.handleDBScript))
		// Stopping a run is the capability that started it. It is not in the
		// destructive group: what it stops rolls back, and only the principal
		// who started a run can name it.
		r.Method(http.MethodPost, "/{id}/query/cancel", s.handle(s.handleDBQueryCancel))
		r.Method(http.MethodPut, "/{id}/queries/{qid}", s.handle(s.handleDBSavedUpdate))
	})
}

// --- staged edits ---------------------------------------------------------

type changesRequest struct {
	Schema  string       `json:"schema"`
	Table   string       `json:"table"`
	Changes []dbx.Change `json:"changes"`
	DryRun  bool         `json:"dryRun"`
}

// handleDBChanges applies the grid's staged edits in one transaction, or — with
// dryRun — renders the statements they would be without running anything.
//
// The audit entry counts what was done and to which table. It never carries a
// value or a key: those are the operator's data, and the audit log is read by
// people who were not meant to see it.
func (s *Server) handleDBChanges(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req changesRequest
	if err := httpx.DecodeJSONNumbers(r, &req); err != nil {
		return err
	}
	if req.Table == "" {
		return httpx.BadRequest("table is required")
	}
	if len(req.Changes) == 0 {
		return httpx.BadRequest("no changes supplied")
	}
	counts := map[string]int{}
	for _, c := range req.Changes {
		counts[c.Op]++
	}
	if req.DryRun {
		// Rendering statements changes nothing and is not worth an entry.
		httpx.SkipAudit(r)
	} else if counts[dbx.ChangeDelete] > 0 {
		// A set that removes rows is held to what removing a row costs
		// everywhere else: the destructive capability and its budget.
		p := httpx.MustPrincipal(r)
		if !p.Can(auth.CapDestructive) {
			return httpx.Err(http.StatusForbidden, "forbidden",
				"this change set deletes rows and your role does not permit it")
		}
		if !s.destrLim.Allow(p.Username() + "|dbchanges") {
			return httpx.Err(http.StatusTooManyRequests, "rate_limited",
				"too many destructive changes, slow down")
		}
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := dbx.ApplyChanges(ctx, pool, conn.Driver, dbx.ChangeSet{
		Schema: req.Schema, Table: req.Table, Changes: req.Changes, DryRun: req.DryRun,
	})
	detail := map[string]any{
		"schema": req.Schema, "table": req.Table,
		"inserts": counts[dbx.ChangeInsert], "updates": counts[dbx.ChangeUpdate],
		"deletes": counts[dbx.ChangeDelete],
	}
	if err != nil {
		var change *dbx.ChangeError
		if errors.As(err, &change) {
			detail["failedChange"] = change.Index
			if change.Conflict {
				detail["matched"] = change.Matched
			}
		}
		detail["applied"] = false
		if !req.DryRun {
			httpx.SetAudit(r, "database.rows.change", conn.Name, detail)
		}
		return changeError(err)
	}
	if !req.DryRun {
		detail["applied"], detail["attempts"] = true, res.Attempts
		httpx.SetAudit(r, "database.rows.change", conn.Name, detail)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// changeError renders a change set that was not applied.
//
// A conflict — a change that did not touch exactly one row — is a 409 that
// names the change in `field` as changes[i] and says how many rows it matched
// in `reason` as "matched N rows", so the grid can mark the row it staged
// without reading prose. Everything else that names a change is a 400 the same
// way. In every case nothing was applied.
func changeError(err error) error {
	var change *dbx.ChangeError
	switch {
	case errors.As(err, &change):
		field := "changes[" + strconv.Itoa(change.Index) + "]"
		if !change.Conflict {
			out := httpx.Err(http.StatusBadRequest, "change_failed", err.Error())
			out.Field, out.Operation = field, change.Op
			return out
		}
		out := httpx.Err(http.StatusConflict, "change_conflict", err.Error())
		out.Field, out.Operation = field, change.Op
		out.Reason = "matched " + strconv.FormatInt(change.Matched, 10) + " rows"
		return out
	case errors.Is(err, dbx.ErrChangesUnsupported):
		return httpx.Err(http.StatusBadRequest, "unsupported", err.Error())
	case dbx.IsSerializationFailure(err):
		// The engine gave up ordering this transaction against another after
		// every retry. Nothing was applied, and the same request may well
		// succeed a moment later.
		return httpx.Err(http.StatusConflict, "serialization_failure",
			"the database could not apply these changes alongside another transaction; try again").
			Because("serialization failure", err.Error()).Retry()
	}
	return httpx.BadRequest("%v", err)
}

// keyColumns is which columns a key named, sorted, for the audit log.
func keyColumns(key map[string]any) []string {
	out := make([]string, 0, len(key))
	for name := range key {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// --- one cell, whole ------------------------------------------------------

// handleDBCell returns the full value of one cell.
//
// The key travels as a JSON object in the query string, like the grid's
// filters: a GET has no body, and a key is structured. Numbers in it are kept
// as the digits they were written with, because a key is the last place a
// 64-bit integer may be rounded.
func (s *Server) handleDBCell(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	table, column := q.Get("table"), q.Get("column")
	if table == "" || column == "" {
		return httpx.BadRequest("table and column are required")
	}
	var key map[string]any
	dec := json.NewDecoder(strings.NewReader(q.Get("key")))
	dec.UseNumber()
	if err := dec.Decode(&key); err != nil || len(key) == 0 {
		return httpx.BadRequest("key must be a JSON object naming the row")
	}
	pool, conn, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	cell, err := dbx.ReadCell(ctx, pool, conn.Driver, q.Get("schema"), table, column, key)
	if err != nil {
		var tooLarge *dbx.CellTooLargeError
		switch {
		case errors.Is(err, dbx.ErrRowNotFound):
			return httpx.Err(http.StatusNotFound, "row_not_found", err.Error())
		case errors.Is(err, dbx.ErrRowAmbiguous):
			return httpx.Err(http.StatusConflict, "row_ambiguous", err.Error())
		case errors.As(err, &tooLarge):
			return httpx.Err(http.StatusRequestEntityTooLarge, "cell_too_large", err.Error())
		}
		return httpx.BadRequest("%v", err)
	}
	// A cell can be megabytes of somebody's data; nothing between here and the
	// browser has a reason to keep a copy.
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, cell)
	return nil
}

// --- scripts --------------------------------------------------------------

type scriptRequest struct {
	Script string `json:"script"`
	// Transaction runs the whole script in one transaction.
	Transaction bool   `json:"transaction"`
	MaxRows     int    `json:"maxRows"`
	QueryID     string `json:"queryId"`
}

type scriptResponse struct {
	*dbx.ScriptResult
	Risk dbx.Risk `json:"risk"`
}

// maxAuditedScript bounds how much of a script the audit entry carries. The
// log is for finding out what was run, not for storing it.
const maxAuditedScript = 2 << 10

// handleDBScript runs several statements in order on one connection.
//
// What the script may do is decided by the worst thing in it: the statements
// are classified one by one and the strongest verdict is the script's, so a
// DROP on line forty needs the destructive capability however harmless the
// thirty-nine lines above it are. Text the lexer cannot attribute is refused
// before anything runs.
func (s *Server) handleDBScript(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req scriptRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	conn, err := s.sqlConnection(r, id)
	if err != nil {
		return err
	}
	statements, err := dbx.ParseScript(conn.Driver, req.Script)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if len(statements) == 0 {
		return httpx.BadRequest("script is required")
	}
	risk := dbx.WorstRisk(statements)
	if err := s.authoriseSQL(r, risk); err != nil {
		return err
	}
	pool, _, err := s.dbPool(r.Context(), id)
	if err != nil {
		return err
	}
	// The budget of one statement would starve a script; the budget of all of
	// them would let one request hold a connection for an hour.
	ctx, cancel := timeoutCtx(r, 10*time.Minute)
	defer cancel()
	ctx, run, err := s.trackRun(ctx, r, id, req.QueryID)
	if err != nil {
		return err
	}
	defer run.release()

	res, err := dbx.RunScript(ctx, pool, conn.Driver, statements, dbx.ScriptOptions{
		Transaction: req.Transaction, MaxRows: req.MaxRows,
	})
	audited := req.Script
	if len(audited) > maxAuditedScript {
		audited = strings.ToValidUTF8(audited[:maxAuditedScript], "") + "…"
	}
	detail := map[string]any{
		"risk": risk.Level, "destructive": risk.Destructive,
		"statements": len(statements), "transaction": req.Transaction, "script": audited,
	}
	if err != nil {
		err = run.explain(err)
		detail["error"] = err.Error()
		httpx.SetAudit(r, "database.script", strconv.FormatInt(id, 10), detail)
		return err
	}
	executed := 0
	for i, step := range res.Steps {
		if step.Status == dbx.StepSkipped {
			continue
		}
		executed++
		entry := dbHistoryEntry{
			SQL: step.SQL, Risk: step.Risk.Level, Success: step.Status == dbx.StepOK,
			DurationMs: step.DurationMs, Error: step.Error,
		}
		if step.Result != nil {
			entry.RowCount, entry.RowsAffected = step.Result.RowCount, step.Result.Affected
		}
		if step.Status == dbx.StepError && run.cancelled.Load() {
			res.Steps[i].Error = "the statement was cancelled"
			entry.Error = res.Steps[i].Error
		}
		s.recordDBHistory(r.Context(), id, entry)
	}
	detail["executed"], detail["failed"], detail["outcome"] = executed, res.Failed, res.Transaction
	httpx.SetAudit(r, "database.script", strconv.FormatInt(id, 10), detail)
	httpx.JSON(w, http.StatusOK, scriptResponse{ScriptResult: res, Risk: risk})
	return nil
}

// --- stopping a run -------------------------------------------------------

// A run is a statement or a script in flight that its client named, so the
// client can ask for it to be stopped. The name only means something while the
// request that carries it is open: the entry is made when the statement starts
// and removed when its request returns, whatever the outcome, so the table
// holds exactly the runs that are in flight and cannot grow past them.
type dbRunKey struct {
	server *Server
	conn   int64
	user   string
	id     string
}

type dbRun struct {
	key    dbRunKey
	cancel context.CancelFunc
	// cancelled records that the stop was asked for, which is what tells a
	// statement the operator stopped from one that failed.
	cancelled atomic.Bool
}

var dbRuns = struct {
	sync.Mutex
	running map[dbRunKey]*dbRun
}{running: map[dbRunKey]*dbRun{}}

var queryIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// trackRun registers a run under the id its client chose and returns the
// context to run it under. With no id there is nothing to register and the
// returned run does nothing.
//
// The key carries the principal: a run can only be stopped by whoever started
// it, and two people using the same id on one connection do not collide.
func (s *Server) trackRun(ctx context.Context, r *http.Request, connID int64, queryID string) (context.Context, *dbRun, error) {
	if queryID == "" {
		return ctx, &dbRun{}, nil
	}
	if !queryIDRe.MatchString(queryID) {
		return nil, nil, httpx.BadRequest("queryId must be 1 to 64 letters, digits, underscores or hyphens")
	}
	key := dbRunKey{server: s, conn: connID, user: httpx.MustPrincipal(r).Username(), id: queryID}
	ctx, cancel := context.WithCancel(ctx)
	run := &dbRun{key: key, cancel: cancel}
	dbRuns.Lock()
	defer dbRuns.Unlock()
	if _, busy := dbRuns.running[key]; busy {
		cancel()
		return nil, nil, httpx.Err(http.StatusConflict, "query_id_in_use",
			"a statement is already running under this queryId")
	}
	dbRuns.running[key] = run
	return ctx, run, nil
}

// release forgets the run. It is deferred by the handler that tracked it.
func (run *dbRun) release() {
	if run.cancel == nil {
		return
	}
	dbRuns.Lock()
	if dbRuns.running[run.key] == run {
		delete(dbRuns.running, run.key)
	}
	dbRuns.Unlock()
	run.cancel()
}

// explain renders why a run failed: stopped by its operator, or refused by the
// engine. The first is not the request's fault and must not read like it.
func (run *dbRun) explain(err error) error {
	if run.cancelled.Load() {
		return httpx.Err(http.StatusConflict, "query_cancelled", "the statement was cancelled")
	}
	var apiErr *httpx.APIError
	if errors.As(err, &apiErr) {
		return err
	}
	return httpx.BadRequest("%v", err)
}

type cancelRequest struct {
	QueryID string `json:"queryId"`
}

// handleDBQueryCancel stops a run its caller started.
//
// It answers 200 either way: a run that has already finished is not an error,
// it is the race every stop button loses some of the time, and `cancelled`
// says which happened. A connection that does not exist, or has no statements
// to stop, is answered as it is on the routes that start a run.
func (s *Server) handleDBQueryCancel(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	var req cancelRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if !queryIDRe.MatchString(req.QueryID) {
		return httpx.BadRequest("queryId must be 1 to 64 letters, digits, underscores or hyphens")
	}
	if _, err := s.sqlConnection(r, id); err != nil {
		return err
	}
	key := dbRunKey{server: s, conn: id, user: httpx.MustPrincipal(r).Username(), id: req.QueryID}
	dbRuns.Lock()
	run := dbRuns.running[key]
	dbRuns.Unlock()
	if run != nil {
		run.cancelled.Store(true)
		run.cancel()
	}
	httpx.SetAudit(r, "database.query.cancel", strconv.FormatInt(id, 10),
		map[string]any{"queryId": req.QueryID, "cancelled": run != nil})
	httpx.JSON(w, http.StatusOK, map[string]any{"cancelled": run != nil})
	return nil
}

// --- saved queries --------------------------------------------------------

// handleDBSavedUpdate renames or edits a saved query. Either field may be left
// out, and the one that is stays as it was.
func (s *Server) handleDBSavedUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	qid, err := strconv.ParseInt(chi.URLParam(r, "qid"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid query id")
	}
	// Decoded by hand rather than into the create request: an absent field and
	// an empty one mean different things here.
	var req struct {
		Name *string `json:"name"`
		SQL  *string `json:"sql"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Name == nil && req.SQL == nil {
		return httpx.BadRequest("name or sql is required")
	}
	var current savedQuery
	var created, updated int64
	err = s.Store.DB.QueryRowContext(r.Context(),
		`SELECT id, name, sql, created_at, updated_at FROM db_saved_queries WHERE id = ? AND connection_id = ?`,
		qid, id).Scan(&current.ID, &current.Name, &current.SQL, &created, &updated)
	if err == sql.ErrNoRows {
		return httpx.ErrNotFound
	}
	if err != nil {
		return httpx.Internal(err)
	}
	next := savedQueryRequest{Name: current.Name, SQL: current.SQL}
	if req.Name != nil {
		next.Name = *req.Name
	}
	if req.SQL != nil {
		next.SQL = *req.SQL
	}
	if err := next.validate(); err != nil {
		return err
	}
	now := time.Now().Unix()
	if _, err := s.Store.DB.ExecContext(r.Context(),
		`UPDATE db_saved_queries SET name = ?, sql = ?, updated_at = ? WHERE id = ? AND connection_id = ?`,
		next.Name, next.SQL, now, qid, id); err != nil {
		return httpx.BadRequest("could not save query: %v", err)
	}
	httpx.SetAudit(r, "database.query.update", next.Name, map[string]any{
		"id": qid, "renamed": next.Name != current.Name, "edited": next.SQL != current.SQL,
	})
	current.Name, current.SQL = next.Name, next.SQL
	current.setTimes(created, now)
	httpx.JSON(w, http.StatusOK, current)
	return nil
}
