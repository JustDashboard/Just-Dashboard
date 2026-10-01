package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The table editor and the query runner, driven over HTTP against a real
// SQLite file: real handlers, real capability checks, the real audit
// middleware, and a database small enough to assert on row by row.

type workbench struct {
	t      *testing.T
	server *Server
	id     int64
	path   string
}

// newWorkbench makes a server with one SQLite connection holding a keyed table
// and a keyless one.
func newWorkbench(t *testing.T) *workbench {
	t.Helper()
	s := testServer(t)
	path := filepath.Join(s.Cfg.FileRoots[0], "workbench.db")
	sealed, err := s.Sealer.Seal(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"workbench", string(dbx.DriverSQLite), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	pool, _, err := s.dbPool(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER DEFAULT 18, big INTEGER, data BLOB, bio TEXT)`,
		`INSERT INTO people(id, name, age) VALUES (1, 'Ann', 31), (2, 'Bo', 24), (3, 'Cy', 40)`,
		`CREATE TABLE notes (msg TEXT, n INTEGER)`,
		`INSERT INTO notes VALUES ('one', 1), ('dup', 7), ('dup', 7)`,
	} {
		if _, err := pool.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return &workbench{t: t, server: s, id: id, path: path}
}

// as returns the database routes with a principal of the given role in front
// of them, behind the same audit middleware the real router uses.
func (w *workbench) as(role auth.Role, username string) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: username},
				Role: role, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(rw, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(w.server.Audit))
	w.server.mountDatabaseRoutes(r)
	return r
}

func (w *workbench) url(suffix string) string {
	return "/databases/" + strconv.FormatInt(w.id, 10) + suffix
}

func (w *workbench) count(query string) int {
	w.t.Helper()
	pool, _, err := w.server.dbPool(context.Background(), w.id)
	if err != nil {
		w.t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(query).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", query, err)
	}
	return n
}

// audit returns the detail of the newest audit entry for an action.
func (w *workbench) audit(action string) string {
	w.t.Helper()
	var detail string
	err := w.server.Store.DB.QueryRow(
		`SELECT detail FROM audit_log WHERE action = ? ORDER BY id DESC LIMIT 1`, action).Scan(&detail)
	if err != nil {
		w.t.Fatalf("no audit entry for %s: %v", action, err)
	}
	return detail
}

func decodeJSONBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not the expected JSON: %v\n%s", err, rec.Body.String())
	}
	return out
}

type errorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Field     string `json:"field"`
		Operation string `json:"operation"`
		Reason    string `json:"reason"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

// No route here asks for a typed phrase. Applying staged edits and running a
// script are what the page is for; the typed phrase is kept for dropping the
// database itself.
func TestWorkbenchRoutesDoNotAskForAPhrase(t *testing.T) {
	w := newWorkbench(t)
	router := w.as(auth.RoleAdmin, "admin")
	for _, c := range []confirmCase{
		{http.MethodPost, w.url("/changes"), `{"table":"people","changes":[{"op":"delete","key":{"id":1}}]}`, "a row delete is the unit of work in a grid"},
		{http.MethodPost, w.url("/script"), `{"script":"DROP TABLE notes; SELECT 1"}`, "the script route is held to the query route's rules"},
		{http.MethodPost, w.url("/query"), `{"query":"DELETE FROM people"}`, "the query route never took a phrase"},
		{http.MethodPost, w.url("/query/cancel"), `{"queryId":"q1"}`, "stopping a statement loses nothing"},
		{http.MethodPut, w.url("/queries/1"), `{"name":"renamed"}`, "a saved query is dashboard state"},
		{http.MethodPost, w.url("/explain"), `{"query":"DELETE FROM people","analyze":true}`, "an analysed plan is a run that is rolled back"},
	} {
		rec := driveWithoutConfirmation(t, router, c)
		if strings.Contains(rec.Body.String(), "confirmation") {
			t.Errorf("%s %s asked for a phrase, but %s: %d %s", c.method, c.path, c.why, rec.Code, rec.Body.String())
		}
	}
}

// The capability each route needs is enforced by the server, and where it
// depends on what the request holds, by reading what the request holds.
func TestWorkbenchCapabilities(t *testing.T) {
	w := newWorkbench(t)
	reader, limited := w.as(auth.RoleReadOnly, "reader"), w.as(auth.RoleLimited, "limited")

	// A reader may read a cell and ask for a plan, and nothing else here.
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, w.url("/changes"), `{"table":"people","changes":[{"op":"insert","values":{"name":"x"}}]}`},
		{http.MethodPost, w.url("/changes"), `{"table":"people","dryRun":true,"changes":[{"op":"insert","values":{"name":"x"}}]}`},
		{http.MethodPost, w.url("/script"), `{"script":"SELECT 1"}`},
		{http.MethodPost, w.url("/query"), `{"query":"SELECT 1"}`},
		{http.MethodPost, w.url("/query/cancel"), `{"queryId":"q1"}`},
		{http.MethodPut, w.url("/queries/1"), `{"name":"x"}`},
		// An analysed plan runs the statement — even a SELECT is a run.
		{http.MethodPost, w.url("/explain"), `{"query":"SELECT * FROM people","analyze":true}`},
	} {
		rec := do(t, reader, c.method, c.path, c.body)
		if rec.Code != http.StatusForbidden {
			t.Errorf("a reader's %s %s %s = %d, want 403: %s", c.method, c.path, c.body, rec.Code, rec.Body.String())
		}
	}
	if rec := do(t, reader, http.MethodGet, w.url("/cell")+"?table=people&column=name&key="+url.QueryEscape(`{"id":1}`), ""); rec.Code != http.StatusOK {
		t.Errorf("a reader's cell read = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, reader, http.MethodPost, w.url("/explain"), `{"query":"DELETE FROM people WHERE id = 1"}`); rec.Code != http.StatusOK {
		t.Errorf("a reader's plain plan = %d: %s", rec.Code, rec.Body.String())
	}

	// A limited operator may insert and update, in the grid and in a script,
	// and may do nothing that removes or restructures.
	for _, c := range []struct {
		path, body string
		status     int
	}{
		{"/changes", `{"table":"people","changes":[{"op":"insert","values":{"name":"Di"}},{"op":"update","key":{"id":1},"values":{"age":32}}]}`, http.StatusOK},
		{"/changes", `{"table":"people","changes":[{"op":"update","key":{"id":1},"values":{"age":33}},{"op":"delete","key":{"id":2}}]}`, http.StatusForbidden},
		// A dry run of a delete renders a statement and removes nothing.
		{"/changes", `{"table":"people","dryRun":true,"changes":[{"op":"delete","key":{"id":2}}]}`, http.StatusOK},
		{"/script", `{"script":"INSERT INTO notes VALUES ('three', 3); SELECT COUNT(*) FROM notes"}`, http.StatusOK},
		{"/script", `{"script":"SELECT 1; DROP TABLE notes"}`, http.StatusForbidden},
		{"/script", `{"script":"SELECT 1; BEGIN; SELECT 2"}`, http.StatusForbidden},
		// Text the lexer cannot read is refused before anything runs.
		{"/script", `{"script":"SELECT 1; SELECT 'never closed"}`, http.StatusBadRequest},
		{"/query", `{"query":"SELECT 1"}`, http.StatusOK},
		{"/query", `{"query":"DELETE FROM people WHERE id = 3"}`, http.StatusForbidden},
		{"/query", `{"query":"WITH gone AS (DELETE FROM people RETURNING id) SELECT * FROM gone"}`, http.StatusForbidden},
		{"/explain", `{"query":"DELETE FROM people WHERE id = 3","analyze":true}`, http.StatusForbidden},
	} {
		rec := do(t, limited, http.MethodPost, w.url(c.path), c.body)
		if rec.Code != c.status {
			t.Errorf("a limited operator's POST %s %s = %d, want %d: %s", c.path, c.body, rec.Code, c.status, rec.Body.String())
		}
	}
	if n := w.count(`SELECT COUNT(*) FROM people WHERE id IN (2, 3)`); n != 2 {
		t.Errorf("rows a limited operator may not delete: %d left of 2", n)
	}
	if n := w.count(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'notes'`); n != 1 {
		t.Error("a limited operator dropped a table")
	}
	if n := w.count(`SELECT age FROM people WHERE id = 1`); n != 32 {
		t.Errorf("age = %d: an update from a refused set was applied", n)
	}
}

type changesResponse struct {
	Applied    bool     `json:"applied"`
	DryRun     bool     `json:"dryRun"`
	KeyColumns []string `json:"keyColumns"`
	Statements []string `json:"statements"`
	Results    []struct {
		Index     int            `json:"index"`
		Op        string         `json:"op"`
		Statement string         `json:"statement"`
		Affected  int64          `json:"rowsAffected"`
		Row       map[string]any `json:"row"`
	} `json:"results"`
	Attempts int `json:"attempts"`
}

func TestChangesRouteAppliesReportsAndAudits(t *testing.T) {
	w := newWorkbench(t)
	admin := w.as(auth.RoleAdmin, "admin")

	rec := do(t, admin, http.MethodPost, w.url("/changes"), `{"table":"people","changes":[
		{"op":"insert","values":{"name":"Secret Name","big":9223372036854775807,"data":{"$hex":"00ff"}}},
		{"op":"update","key":{"id":1},"values":{"age":32}},
		{"op":"delete","key":{"id":3}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("changes = %d: %s", rec.Code, rec.Body.String())
	}
	res := decodeJSONBody[changesResponse](t, rec)
	if !res.Applied || res.DryRun || len(res.Results) != 3 || res.KeyColumns[0] != "id" {
		t.Fatalf("response = %+v", res)
	}
	// A 64-bit integer survives the request and the response as its digits.
	if row := res.Results[0].Row; row["big"] != "9223372036854775807" || row["data"] != `\x00ff` || row["age"] != "18" {
		t.Errorf("inserted row = %v", row)
	}
	if res.Results[1].Row["age"] != "32" || res.Results[2].Row != nil {
		t.Errorf("results = %+v", res.Results)
	}
	if !strings.HasPrefix(res.Statements[2], `DELETE FROM "people"`) {
		t.Errorf("statements = %v", res.Statements)
	}
	if w.count(`SELECT COUNT(*) FROM people WHERE big = 9223372036854775807`) != 1 || w.count(`SELECT COUNT(*) FROM people`) != 3 {
		t.Error("the set was not applied as sent")
	}
	// The audit entry says what was done and where. It never says to what.
	detail := w.audit("database.rows.change")
	for _, want := range []string{`"inserts":1`, `"updates":1`, `"deletes":1`, `"table":"people"`, `"applied":true`} {
		if !strings.Contains(detail, want) {
			t.Errorf("audit detail %s is missing %s", detail, want)
		}
	}
	for _, leaked := range []string{"Secret Name", "9223372036854775807", "00ff"} {
		if strings.Contains(detail, leaked) {
			t.Errorf("a row value reached the audit log: %s", detail)
		}
	}

	// A change that matches no row: 409, the change named, nothing applied.
	rec = do(t, admin, http.MethodPost, w.url("/changes"), `{"table":"people","changes":[
		{"op":"insert","values":{"name":"should not stay"}},
		{"op":"update","key":{"id":999},"values":{"age":1}}]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict = %d: %s", rec.Code, rec.Body.String())
	}
	conflict := decodeJSONBody[errorEnvelope](t, rec).Error
	if conflict.Code != "change_conflict" || conflict.Field != "changes[1]" || conflict.Reason != "matched 0 rows" || conflict.Operation != "update" {
		t.Errorf("conflict = %+v", conflict)
	}
	if w.count(`SELECT COUNT(*) FROM people WHERE name = 'should not stay'`) != 0 {
		t.Error("an insert from a failed set stayed")
	}
	if detail := w.audit("database.rows.change"); !strings.Contains(detail, `"applied":false`) || !strings.Contains(detail, `"failedChange":1`) {
		t.Errorf("a failed set was audited as %s", detail)
	}
	// Two identical rows of a keyless table: 409 with the count.
	rec = do(t, admin, http.MethodPost, w.url("/changes"),
		`{"table":"notes","changes":[{"op":"delete","key":{"msg":"dup","n":7}}]}`)
	if conflict := decodeJSONBody[errorEnvelope](t, rec).Error; rec.Code != http.StatusConflict || conflict.Reason != "matched 2 rows" || conflict.Field != "changes[0]" {
		t.Errorf("duplicate rows = %d %+v", rec.Code, conflict)
	}
	// A change the engine refuses, or that names something that is not there.
	rec = do(t, admin, http.MethodPost, w.url("/changes"),
		`{"table":"people","changes":[{"op":"update","key":{"name":"Ann"},"values":{"age":1}}]}`)
	if failed := decodeJSONBody[errorEnvelope](t, rec).Error; rec.Code != http.StatusBadRequest || failed.Code != "change_failed" || failed.Field != "changes[0]" {
		t.Errorf("a key that is not the primary key = %d %+v", rec.Code, failed)
	}

	// A dry run renders, applies nothing, and leaves no audit entry.
	var before int
	_ = w.server.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&before)
	rec = do(t, admin, http.MethodPost, w.url("/changes"),
		`{"table":"people","dryRun":true,"changes":[{"op":"delete","key":{"id":1}}]}`)
	dry := decodeJSONBody[changesResponse](t, rec)
	if rec.Code != http.StatusOK || dry.Applied || !dry.DryRun || dry.Statements[0] != `DELETE FROM "people" WHERE "id" = 1;` {
		t.Errorf("dry run = %d %+v", rec.Code, dry)
	}
	var after int
	_ = w.server.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&after)
	if w.count(`SELECT COUNT(*) FROM people WHERE id = 1`) != 1 || after != before {
		t.Errorf("a dry run deleted a row or was audited (%d entries, then %d)", before, after)
	}

	for _, bad := range []string{
		`{"changes":[{"op":"insert","values":{"name":"x"}}]}`,
		`{"table":"people","changes":[]}`,
		`{"table":"people","changes":[{"op":"insert","values":{"name":"x"}}],"sql":"DROP TABLE people"}`,
	} {
		if rec := do(t, admin, http.MethodPost, w.url("/changes"), bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, rec.Code)
		}
	}
}

// The older single-row routes are held to the same key and the same one-row
// rule the change set is.
func TestRowRoutesAreHeldToOneRow(t *testing.T) {
	w := newWorkbench(t)
	admin := w.as(auth.RoleAdmin, "admin")
	rec := do(t, admin, http.MethodPatch, w.url("/rows"), `{"table":"people","key":{"name":"Ann"},"values":{"age":1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an update keyed on a column that is not the key = %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, admin, http.MethodDelete, w.url("/rows"), `{"table":"people","key":{"id":999}}`)
	if rec.Code != http.StatusConflict || decodeJSONBody[errorEnvelope](t, rec).Error.Code != "change_conflict" {
		t.Errorf("a delete of a row that is gone = %d: %s", rec.Code, rec.Body.String())
	}
	rec = do(t, admin, http.MethodPatch, w.url("/rows"), `{"table":"people","key":{"id":1},"values":{"age":50}}`)
	if rec.Code != http.StatusOK || w.count(`SELECT age FROM people WHERE id = 1`) != 50 {
		t.Errorf("an update by the key = %d: %s", rec.Code, rec.Body.String())
	}
	// The key's columns are audited; its values are not.
	if detail := w.audit("database.row.update"); !strings.Contains(detail, `"key":["id"]`) {
		t.Errorf("audit detail = %s", detail)
	}
}

type scriptStepJSON struct {
	Index  int    `json:"index"`
	SQL    string `json:"sql"`
	Line   int    `json:"line"`
	Status string `json:"status"`
	Error  string `json:"error"`
	Result *struct {
		Rows      [][]any `json:"rows"`
		Affected  int64   `json:"rowsAffected"`
		Truncated bool    `json:"truncated"`
	} `json:"result"`
	Risk dbx.Risk `json:"risk"`
}

type scriptJSON struct {
	Statements  []scriptStepJSON `json:"statements"`
	Failed      int              `json:"failed"`
	Transaction string           `json:"transaction"`
	Risk        dbx.Risk         `json:"risk"`
}

type historyJSON struct {
	SQL          string `json:"sql"`
	Risk         string `json:"risk"`
	Success      bool   `json:"success"`
	Duration     int64  `json:"durationMs"`
	RowCount     int    `json:"rowCount"`
	RowsAffected int64  `json:"rowsAffected"`
	Error        string `json:"error"`
}

func TestScriptRouteRunsInOrderAndRecordsEachStatement(t *testing.T) {
	w := newWorkbench(t)
	admin := w.as(auth.RoleAdmin, "admin")

	rec := do(t, admin, http.MethodPost, w.url("/script"), `{"script":"CREATE TEMP TABLE scratch (a);\nINSERT INTO scratch VALUES (1), (2);\nSELECT COUNT(*) FROM scratch;\nINSERT INTO missing VALUES (1);\nSELECT 2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("script = %d: %s", rec.Code, rec.Body.String())
	}
	res := decodeJSONBody[scriptJSON](t, rec)
	if res.Failed != 3 || res.Transaction != "none" || len(res.Statements) != 5 || res.Risk.Level != "medium" {
		t.Fatalf("script = %+v", res)
	}
	status := []string{}
	for _, st := range res.Statements {
		status = append(status, st.Status)
	}
	if strings.Join(status, ",") != "ok,ok,ok,error,skipped" {
		t.Errorf("statuses = %v", status)
	}
	if res.Statements[2].Result.Rows[0][0] != "2" || res.Statements[1].Result.Affected != 2 || res.Statements[3].Line != 4 || res.Statements[3].Error == "" {
		t.Errorf("steps = %+v", res.Statements)
	}

	// One history entry per statement that ran, newest first, with what the
	// failed one said.
	history := decodeJSONBody[[]historyJSON](t, do(t, admin, http.MethodGet, w.url("/history"), ""))
	if len(history) != 4 {
		t.Fatalf("history holds %d entries, want the four statements that ran: %+v", len(history), history)
	}
	if failed := history[0]; failed.Success || failed.Error == "" || !strings.Contains(failed.SQL, "missing") {
		t.Errorf("the failed statement in the history = %+v", failed)
	}
	if insert := history[2]; !insert.Success || insert.RowsAffected != 2 || insert.Risk != "medium" || insert.Error != "" {
		t.Errorf("the insert in the history = %+v", insert)
	}
	if detail := w.audit("database.script"); !strings.Contains(detail, `"executed":4`) || !strings.Contains(detail, `"failed":3`) {
		t.Errorf("audit detail = %s", detail)
	}

	// In one transaction the failure undoes what came before it.
	rec = do(t, admin, http.MethodPost, w.url("/script"),
		`{"transaction":true,"script":"INSERT INTO notes VALUES ('kept?', 1); INSERT INTO missing VALUES (1)"}`)
	if res := decodeJSONBody[scriptJSON](t, rec); rec.Code != http.StatusOK || res.Transaction != "rolled_back" || res.Failed != 1 {
		t.Errorf("transaction script = %d %+v", rec.Code, res)
	}
	if w.count(`SELECT COUNT(*) FROM notes WHERE msg = 'kept?'`) != 0 {
		t.Error("a rolled-back script kept a row")
	}
	// A script of reads says so.
	rec = do(t, admin, http.MethodPost, w.url("/script"), `{"script":"SELECT 1; SELECT 2","maxRows":1}`)
	if res := decodeJSONBody[scriptJSON](t, rec); res.Transaction != "read_only" || res.Risk.Level != "read" || res.Failed != -1 {
		t.Errorf("read script = %+v", res)
	}
	for _, bad := range []string{`{"script":""}`, `{"script":"-- nothing here"}`, `{"script":"SELECT 1","queryId":"not valid!"}`} {
		if rec := do(t, admin, http.MethodPost, w.url("/script"), bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, rec.Code)
		}
	}
}

func TestQueryRouteClampsRecordsAndClassifies(t *testing.T) {
	w := newWorkbench(t)
	admin := w.as(auth.RoleAdmin, "admin")

	type queryJSON struct {
		Result struct {
			RowCount  int      `json:"rowCount"`
			Truncated bool     `json:"truncated"`
			Statement string   `json:"statement"`
			Kinds     []string `json:"kinds"`
		} `json:"result"`
		Risk dbx.Risk `json:"risk"`
	}
	// More rows asked for than the cap: the cap, not the default.
	rec := do(t, admin, http.MethodPost, w.url("/query"),
		`{"query":"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 6000) SELECT i FROM n","maxRows":100000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("query = %d: %s", rec.Code, rec.Body.String())
	}
	res := decodeJSONBody[queryJSON](t, rec)
	if res.Result.RowCount != dbx.MaxResultRows || !res.Result.Truncated || res.Risk.Level != "read" {
		t.Errorf("rows = %d truncated = %v risk = %s", res.Result.RowCount, res.Result.Truncated, res.Risk.Level)
	}
	rec = do(t, admin, http.MethodPost, w.url("/query"), `{"query":"-- which people\nSELECT id FROM people; -- that is all\n","maxRows":3}`)
	if res := decodeJSONBody[queryJSON](t, rec); res.Result.RowCount != 3 || res.Result.Truncated || res.Result.Statement != "SELECT id FROM people" {
		t.Errorf("a result that fits = %+v", res.Result)
	}

	// A failure is a 400 with the engine's words, and the history keeps them.
	rec = do(t, admin, http.MethodPost, w.url("/query"), `{"query":"SELECT * FROM nowhere"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "nowhere") {
		t.Errorf("a failing query = %d: %s", rec.Code, rec.Body.String())
	}
	history := decodeJSONBody[[]historyJSON](t, do(t, admin, http.MethodGet, w.url("/history")+"?limit=1", ""))
	if len(history) != 1 || history[0].Success || !strings.Contains(history[0].Error, "nowhere") {
		t.Errorf("history = %+v", history)
	}
	// The limit is clamped rather than reset.
	if all := decodeJSONBody[[]historyJSON](t, do(t, admin, http.MethodGet, w.url("/history")+"?limit=100000", "")); len(all) != 3 {
		t.Errorf("history with a huge limit holds %d entries, want all 3", len(all))
	}

	// Two statements are a script, not a query.
	if rec := do(t, admin, http.MethodPost, w.url("/query"), `{"query":"SELECT 1; SELECT 2"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("two statements on the query route = %d", rec.Code)
	}

	// The verdict the editor is shown is the engine's reading, statement by
	// statement.
	type classifyJSON struct {
		dbx.Risk
		Statements []struct {
			SQL  string   `json:"sql"`
			Line int      `json:"line"`
			Risk dbx.Risk `json:"risk"`
		} `json:"statements"`
	}
	rec = do(t, admin, http.MethodPost, w.url("/classify"), `{"query":"SELECT [a;b] FROM t;\nDELETE FROM t"}`)
	verdict := decodeJSONBody[classifyJSON](t, rec)
	if verdict.Level != "critical" || len(verdict.Statements) != 2 || verdict.Statements[0].SQL != "SELECT [a;b] FROM t" ||
		verdict.Statements[0].Risk.Level != "read" || verdict.Statements[1].Line != 2 {
		t.Errorf("classify = %+v", verdict)
	}
	rec = do(t, admin, http.MethodPost, w.url("/classify"), `{"query":"SELECT 'never closed"}`)
	if verdict := decodeJSONBody[classifyJSON](t, rec); !verdict.Destructive || verdict.Statements == nil || len(verdict.Statements) != 0 {
		t.Errorf("unreadable text classified %+v", verdict)
	}
	if rec := do(t, admin, http.MethodPost, "/databases/9999/classify", `{"query":"SELECT 1"}`); rec.Code != http.StatusNotFound {
		t.Errorf("classify on a connection that does not exist = %d", rec.Code)
	}
}

// A run its client named can be stopped by that client, and by nobody else.
func TestARunCanBeCancelledByWhoeverStartedIt(t *testing.T) {
	w := newWorkbench(t)
	admin, other := w.as(auth.RoleAdmin, "admin"), w.as(auth.RoleAdmin, "someone-else")
	const endless = `{"queryId":"run-1","query":"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n) SELECT COUNT(*) FROM n"}`

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- do(t, admin, http.MethodPost, w.url("/query"), endless) }()
	waitForRun := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			dbRuns.Lock()
			n := len(dbRuns.running)
			dbRuns.Unlock()
			if n == 1 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the run never registered")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForRun()

	// The id is taken while the run is in flight.
	if rec := do(t, admin, http.MethodPost, w.url("/query"), `{"queryId":"run-1","query":"SELECT 1"}`); rec.Code != http.StatusConflict ||
		decodeJSONBody[errorEnvelope](t, rec).Error.Code != "query_id_in_use" {
		t.Errorf("a second run under the same id = %d: %s", rec.Code, rec.Body.String())
	}
	// Somebody else naming the same id stops nothing.
	rec := do(t, other, http.MethodPost, w.url("/query/cancel"), `{"queryId":"run-1"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "true") {
		t.Errorf("another principal's cancel = %d %s", rec.Code, rec.Body.String())
	}
	select {
	case rec := <-done:
		t.Fatalf("the run ended when somebody else asked: %d %s", rec.Code, rec.Body.String())
	case <-time.After(200 * time.Millisecond):
	}

	rec = do(t, admin, http.MethodPost, w.url("/query/cancel"), `{"queryId":"run-1"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cancelled":true`) {
		t.Fatalf("cancel = %d %s", rec.Code, rec.Body.String())
	}
	select {
	case rec := <-done:
		if rec.Code != http.StatusConflict || decodeJSONBody[errorEnvelope](t, rec).Error.Code != "query_cancelled" {
			t.Errorf("the cancelled run answered %d: %s", rec.Code, rec.Body.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the run did not stop")
	}
	// Nothing is left behind, and a late cancel is not an error.
	dbRuns.Lock()
	left := len(dbRuns.running)
	dbRuns.Unlock()
	if left != 0 {
		t.Errorf("%d runs still registered after the request returned", left)
	}
	rec = do(t, admin, http.MethodPost, w.url("/query/cancel"), `{"queryId":"run-1"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cancelled":false`) {
		t.Errorf("a late cancel = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, admin, http.MethodPost, w.url("/query/cancel"), `{"queryId":"../etc"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a malformed id = %d", rec.Code)
	}
	// The history says the statement was cancelled, and the id is free again.
	history := decodeJSONBody[[]historyJSON](t, do(t, admin, http.MethodGet, w.url("/history")+"?limit=1", ""))
	if len(history) != 1 || history[0].Success || !strings.Contains(history[0].Error, "cancelled") {
		t.Errorf("history = %+v", history)
	}
	if rec := do(t, admin, http.MethodPost, w.url("/query"), `{"queryId":"run-1","query":"SELECT 1"}`); rec.Code != http.StatusOK {
		t.Errorf("the id was not released: %d %s", rec.Code, rec.Body.String())
	}
}

type browseJSON struct {
	Columns       []string          `json:"columns"`
	Kinds         []string          `json:"kinds"`
	Rows          [][]any           `json:"rows"`
	RowCount      int               `json:"rowCount"`
	Truncated     bool              `json:"truncated"`
	PrimaryKey    []string          `json:"primaryKey"`
	EstimatedRows *int64            `json:"estimatedRows"`
	Sort          []dbx.SortKey     `json:"sort"`
	Limit         int               `json:"limit"`
	Offset        int               `json:"offset"`
	Clipped       []dbx.ClippedCell `json:"clipped"`
}

func TestBrowseRouteReadsTheGridsQueryString(t *testing.T) {
	w := newWorkbench(t)
	admin := w.as(auth.RoleAdmin, "admin")
	browse := func(query string) browseJSON {
		t.Helper()
		rec := do(t, admin, http.MethodGet, w.url("/browse")+"?table=people&"+query, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("browse?%s = %d: %s", query, rec.Code, rec.Body.String())
		}
		return decodeJSONBody[browseJSON](t, rec)
	}
	names := func(page browseJSON) string {
		at := -1
		for i, c := range page.Columns {
			if c == "name" {
				at = i
			}
		}
		var out []string
		for _, row := range page.Rows {
			out = append(out, row[at].(string))
		}
		return strings.Join(out, ",")
	}

	page := browse("limit=2")
	if page.RowCount != 2 || !page.Truncated || page.Limit != 2 || page.PrimaryKey[0] != "id" || page.EstimatedRows != nil {
		t.Errorf("first page = %+v", page)
	}
	if len(page.Sort) != 1 || page.Sort[0].Column != "id" || names(page) != "Ann,Bo" {
		t.Errorf("default order = %+v %s", page.Sort, names(page))
	}
	if last := browse("limit=2&offset=2"); last.RowCount != 1 || last.Truncated || last.Offset != 2 {
		t.Errorf("last page = %+v", last)
	}
	// The three spellings of an order.
	if got := names(browse("orderBy=age&dir=desc")); got != "Cy,Ann,Bo" {
		t.Errorf("orderBy+dir = %s", got)
	}
	if got := names(browse("orderBy=" + url.QueryEscape("age:desc,name:asc"))); got != "Cy,Ann,Bo" {
		t.Errorf("orderBy list = %s", got)
	}
	if got := names(browse("sort=" + url.QueryEscape(`[{"column":"name","desc":true}]`))); got != "Cy,Bo,Ann" {
		t.Errorf("sort = %s", got)
	}
	// A page size past the cap is the cap.
	if page := browse("limit=999999"); page.Limit != dbx.MaxBrowseRows {
		t.Errorf("limit = %d, want %d", page.Limit, dbx.MaxBrowseRows)
	}
	// Projection, the new operators, and either-or.
	page = browse("columns=name,id&filters=" + url.QueryEscape(`[{"column":"id","op":"in","values":["1","3"]}]`))
	if strings.Join(page.Columns, ",") != "name,id" || names(page) != "Ann,Cy" {
		t.Errorf("projection + in = %+v", page)
	}
	either := url.QueryEscape(`[{"column":"name","op":"icontains","value":"ANN"},{"column":"age","op":"between","values":["39","41"]}]`)
	if got := names(browse("match=any&filters=" + either)); got != "Ann,Cy" {
		t.Errorf("match=any = %s", got)
	}
	if got := names(browse("filters=" + either)); got != "" {
		t.Errorf("match=all = %s", got)
	}
	rec := do(t, admin, http.MethodGet, w.url("/count")+"?table=people&match=any&filters="+either, "")
	if !strings.Contains(rec.Body.String(), `"count":2`) {
		t.Errorf("count with match=any = %s", rec.Body.String())
	}

	// A long cell is cut, said to be cut, and one request away.
	pool, _, _ := w.server.dbPool(context.Background(), w.id)
	long := strings.Repeat("0123456789", 1000)
	if _, err := pool.Exec(`UPDATE people SET bio = ? WHERE id = 2`, long); err != nil {
		t.Fatal(err)
	}
	page = browse("cellLimit=300")
	if len(page.Clipped) != 1 || page.Clipped[0].Row != 1 || page.Clipped[0].Size != 10000 {
		t.Fatalf("clipped = %+v", page.Clipped)
	}
	if got := page.Rows[1][page.Clipped[0].Column].(string); len(got) != 300 {
		t.Errorf("the clipped cell is %d bytes, want 300", len(got))
	}
	type cellJSON struct {
		Encoding string `json:"encoding"`
		Value    any    `json:"value"`
		Size     int64  `json:"size"`
		Kind     string `json:"kind"`
	}
	rec = do(t, admin, http.MethodGet, w.url("/cell")+"?table=people&column=bio&key="+url.QueryEscape(`{"id":2}`), "")
	if cell := decodeJSONBody[cellJSON](t, rec); rec.Code != http.StatusOK || cell.Encoding != "text" || cell.Value != long || cell.Size != 10000 {
		t.Errorf("cell = %d encoding %s size %d", rec.Code, cell.Encoding, cell.Size)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("a cell's value may be cached")
	}
	for query, status := range map[string]int{
		"table=people&column=bio&key=" + url.QueryEscape(`{"id":99}`):      http.StatusNotFound,
		"table=notes&column=msg&key=" + url.QueryEscape(`{"msg":"dup"}`):   http.StatusConflict,
		"table=people&column=bio&key=" + url.QueryEscape(`{"name":"Ann"}`): http.StatusBadRequest,
		"table=people&column=nope&key=" + url.QueryEscape(`{"id":1}`):      http.StatusBadRequest,
		"table=people&column=bio&key=" + url.QueryEscape(`[1]`):            http.StatusBadRequest,
		"table=people&column=bio":                                          http.StatusBadRequest,
		"column=bio&key=" + url.QueryEscape(`{"id":1}`):                    http.StatusBadRequest,
	} {
		if rec := do(t, admin, http.MethodGet, w.url("/cell")+"?"+query, ""); rec.Code != status {
			t.Errorf("cell?%s = %d, want %d: %s", query, rec.Code, status, rec.Body.String())
		}
	}
	if _, err := pool.Exec(`UPDATE people SET data = zeroblob(?) WHERE id = 3`, dbx.MaxCellBytes+1); err != nil {
		t.Fatal(err)
	}
	rec = do(t, admin, http.MethodGet, w.url("/cell")+"?table=people&column=data&key="+url.QueryEscape(`{"id":3}`), "")
	if rec.Code != http.StatusRequestEntityTooLarge || decodeJSONBody[errorEnvelope](t, rec).Error.Code != "cell_too_large" {
		t.Errorf("a cell past the bound = %d: %.200s", rec.Code, rec.Body.String())
	}

	for _, bad := range []string{
		"match=some", "sort=" + url.QueryEscape(`{"column":"id"}`), "columns=" + url.QueryEscape(`["id"`),
		"filters=" + url.QueryEscape(`[{"column":"id","op":"like","value":"%"}]`),
	} {
		if rec := do(t, admin, http.MethodGet, w.url("/browse")+"?table=people&"+bad, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("browse?%s = %d, want 400", bad, rec.Code)
		}
	}
}

func TestParseSortKeepsEveryOlderSpellingWorking(t *testing.T) {
	for _, c := range []struct {
		raw, orderBy, dir string
		want              string
	}{
		{"", "", "", ""},
		{"", "id", "", "id asc"},
		{"", "id", "desc", "id desc"},
		{"", "a:asc,b:DESC", "", "a asc,b desc"},
		// A name with a comma or a colon in it is one column unless every part
		// carries a direction.
		{"", "last, first", "desc", "last, first desc"},
		{"", "a:b", "", "a:b asc"},
		{"", "a:asc,b", "", "a:asc,b asc"},
		{`[{"column":"a,b","desc":true},{"column":"c"}]`, "ignored", "desc", "a,b desc,c asc"},
	} {
		keys, err := parseSort(c.raw, c.orderBy, c.dir)
		if err != nil {
			t.Errorf("parseSort(%q, %q, %q): %v", c.raw, c.orderBy, c.dir, err)
			continue
		}
		var got []string
		for _, k := range keys {
			dir := "asc"
			if k.Desc {
				dir = "desc"
			}
			got = append(got, k.Column+" "+dir)
		}
		if strings.Join(got, ",") != c.want {
			t.Errorf("parseSort(%q, %q, %q) = %q, want %q", c.raw, c.orderBy, c.dir, strings.Join(got, ","), c.want)
		}
	}
}

func TestExplainRouteForms(t *testing.T) {
	w := newWorkbench(t)
	admin := w.as(auth.RoleAdmin, "admin")
	// The request the editor has always sent still works, extra field and all.
	rec := do(t, admin, http.MethodPost, w.url("/explain"), `{"query":"SELECT * FROM people WHERE id = 1","maxRows":500}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"result"`) || !strings.Contains(rec.Body.String(), `"analyzed":false`) {
		t.Errorf("plain plan = %d: %s", rec.Code, rec.Body.String())
	}
	// SQLite has neither a JSON plan nor an executing one, and says so.
	for _, body := range []string{
		`{"query":"SELECT * FROM people","format":"json"}`,
		`{"query":"SELECT * FROM people","analyze":true}`,
	} {
		rec := do(t, admin, http.MethodPost, w.url("/explain"), body)
		if rec.Code != http.StatusBadRequest || decodeJSONBody[errorEnvelope](t, rec).Error.Code != "unsupported" {
			t.Errorf("%s = %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := do(t, admin, http.MethodPost, w.url("/explain"), `{"query":"SELECT 1","format":"xml"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown format = %d", rec.Code)
	}
	if w.count(`SELECT COUNT(*) FROM people`) != 3 {
		t.Error("a plan changed the table")
	}
}

func TestSavedQueriesCanBeRenamedAndEdited(t *testing.T) {
	w := newWorkbench(t)
	admin := w.as(auth.RoleAdmin, "admin")
	type savedJSON struct {
		ID        int64     `json:"id"`
		Name      string    `json:"name"`
		SQL       string    `json:"sql"`
		CreatedAt time.Time `json:"createdAt"`
		UpdatedAt time.Time `json:"updatedAt"`
	}
	rec := do(t, admin, http.MethodPost, w.url("/queries"), `{"name":"  people  ","sql":"SELECT * FROM people"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	saved := decodeJSONBody[savedJSON](t, rec)
	if saved.Name != "people" || saved.UpdatedAt.IsZero() || !saved.UpdatedAt.Equal(saved.CreatedAt) {
		t.Errorf("saved = %+v", saved)
	}
	path := w.url("/queries/" + strconv.FormatInt(saved.ID, 10))

	// Either field alone; the other stays.
	rec = do(t, admin, http.MethodPut, path, `{"name":"everyone"}`)
	if got := decodeJSONBody[savedJSON](t, rec); rec.Code != http.StatusOK || got.Name != "everyone" || got.SQL != "SELECT * FROM people" {
		t.Errorf("rename = %d %+v", rec.Code, got)
	}
	rec = do(t, admin, http.MethodPut, path, `{"sql":"SELECT id FROM people"}`)
	if got := decodeJSONBody[savedJSON](t, rec); rec.Code != http.StatusOK || got.Name != "everyone" || got.SQL != "SELECT id FROM people" {
		t.Errorf("edit = %d %+v", rec.Code, got)
	}
	if detail := w.audit("database.query.update"); !strings.Contains(detail, `"edited":true`) || !strings.Contains(detail, `"renamed":false`) {
		t.Errorf("audit detail = %s", detail)
	}
	list := decodeJSONBody[[]savedJSON](t, do(t, admin, http.MethodGet, w.url("/queries"), ""))
	if len(list) != 1 || list[0].Name != "everyone" || list[0].SQL != "SELECT id FROM people" {
		t.Errorf("list = %+v", list)
	}
	for body, status := range map[string]int{
		`{}`:             http.StatusBadRequest,
		`{"name":"   "}`: http.StatusBadRequest,
		`{"sql":""}`:     http.StatusBadRequest,
		`{"name":"` + strings.Repeat("n", 201) + `"}`: http.StatusBadRequest,
		`{"name":"x","folder":"y"}`:                   http.StatusBadRequest,
	} {
		if rec := do(t, admin, http.MethodPut, path, body); rec.Code != status {
			t.Errorf("PUT %s = %d, want %d", body, rec.Code, status)
		}
	}
	if rec := do(t, admin, http.MethodPut, w.url("/queries/9999"), `{"name":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("a query that does not exist = %d", rec.Code)
	}
	// A query saved against one connection cannot be edited through another.
	if rec := do(t, admin, http.MethodPut, "/databases/9999/queries/"+strconv.FormatInt(saved.ID, 10), `{"name":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("another connection's query = %d", rec.Code)
	}
}
