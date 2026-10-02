package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The operations routes, driven through the whole router: the allowlist, the
// session, the capability groups and the audit middleware are all in front of
// the handler, as they are in production.
//
// The database is a real SQLite file inside the file roots. SQLite has no
// sessions, locks or replication, which is useful here rather than a gap: it
// is the engine that answers "there is none" on half of these routes, and
// that answer has to be a body the page can print, not an error.

type opsHarness struct {
	s      *Server
	id     int64
	base   string
	path   string
	admin  *client
	worker *client
	viewer *client
}

func newOpsHarness(t *testing.T) *opsHarness {
	t.Helper()
	s := testServer(t)
	// The test configuration names no backup directory, which would put a
	// connection's dumps in a path relative to wherever the test runs.
	s.Cfg.BackupLocalDir = t.TempDir()
	path := filepath.Join(s.Cfg.FileRoots[0], "ops.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE parent (id INTEGER PRIMARY KEY, name TEXT)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id), v TEXT)`,
		`CREATE INDEX child_parent ON child (parent_id)`,
		`CREATE INDEX child_parent_copy ON child (parent_id)`,
		`INSERT INTO parent VALUES (1, 'a')`,
		`INSERT INTO child VALUES (1, 1, 'x')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	id := saveOpsConnection(t, s, "opsdb", dbx.DriverSQLite, path)
	routes := s.Routes()
	return &opsHarness{
		s: s, id: id, path: path, base: fmt.Sprintf("/api/v1/databases/%d", id),
		admin:  &client{t: t, h: routes, cookie: signIn(t, s)},
		worker: &client{t: t, h: routes, cookie: signInAs(t, s, "ops-limited", auth.RoleLimited)},
		viewer: &client{t: t, h: routes, cookie: signInAs(t, s, "ops-viewer", auth.RoleReadOnly)},
	}
}

func saveOpsConnection(t *testing.T, s *Server, name string, driver dbx.Driver, dsn string) int64 {
	t.Helper()
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		name, string(driver), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func decodeOps(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
}

// auditEntry reads the newest audit row for an action. The log is written
// after the response, so it is waited for rather than read once.
func auditEntry(t *testing.T, s *Server, action string) (target, detail string, status int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := s.Store.DB.QueryRow(`SELECT target, detail, status FROM audit_log WHERE action = ? ORDER BY id DESC LIMIT 1`, action).
			Scan(&target, &detail, &status)
		if err == nil {
			return target, detail, status
		}
		if time.Now().After(deadline) {
			t.Fatalf("no audit entry for %s: %v", action, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Every read is on the read surface, and every one of them answers a viewer
// with a body — including the ones this engine has nothing to say about.
func TestOpsReadRoutesAnswerAViewer(t *testing.T) {
	h := newOpsHarness(t)

	var stats struct {
		Supported     bool               `json:"supported"`
		At            time.Time          `json:"at"`
		Driver        string             `json:"driver"`
		Version       string             `json:"version"`
		DatabaseBytes int64              `json:"databaseBytes"`
		Gauges        map[string]float64 `json:"gauges"`
		Counters      map[string]float64 `json:"counters"`
		Facts         map[string]string  `json:"facts"`
		Pool          *struct {
			Open int `json:"open"`
		} `json:"pool"`
	}
	rec := h.viewer.do(http.MethodGet, h.base+"/stats", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body.String())
	}
	decodeOps(t, rec, &stats)
	if !stats.Supported || stats.Driver != "sqlite" || stats.At.IsZero() || stats.DatabaseBytes <= 0 ||
		stats.Gauges["pageCount"] <= 0 || stats.Counters == nil || stats.Facts["journalMode"] == "" || !strings.HasPrefix(stats.Version, "SQLite ") {
		t.Errorf("stats: %s", rec.Body.String())
	}
	// The pool was the whole answer before the snapshot and keeps its key.
	if stats.Pool == nil {
		t.Errorf("the pool is missing from the stats: %s", rec.Body.String())
	}

	for _, c := range []struct {
		path string
		want string
	}{
		{"/locks", `"supported":false`},
		{"/replication", `"supported":false`},
		{"/tablestats", `"table":"child"`},
		{"/tablestats?limit=1", `"truncated":true`},
		{"/indexstats?table=child", `"duplicateOf":"child_parent`},
		{"/maintenance", `"id":"integrity_check"`},
		{"/settings", `"name":"journal_mode"`},
		{"/settings?all=1", `"all":true`},
		{"/sqlite/file", `"pageSize"`},
		{"/server/privileges", `"supported":false`},
		{"/statements?sort=calls&limit=5", `"sort":"calls"`},
		{"/activity", `"supported":false`},
	} {
		rec := h.viewer.do(http.MethodGet, h.base+c.path, "", nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("GET %s = %d %s, want 200 with %s", c.path, rec.Code, rec.Body.String(), c.want)
		}
	}

	// A route that is another engine's says whose it is.
	for _, path := range []string{"/clickhouse/parts", "/clickhouse/merges", "/clickhouse/mutations", "/clickhouse/queries"} {
		rec := h.viewer.do(http.MethodGet, h.base+path, "", nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "ClickHouse") {
			t.Errorf("GET %s on SQLite = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	// SQLite has no accounts: the refusal is the one the role list gives.
	for _, path := range []string{"/server/roles/app", "/server/grants"} {
		rec := h.viewer.do(http.MethodGet, h.base+path, "", nil)
		if rec.Code != http.StatusBadRequest && !strings.Contains(rec.Body.String(), `"supported":false`) {
			t.Errorf("GET %s on SQLite = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := h.viewer.do(http.MethodGet, h.base+"/statements?sort=duration%20desc", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown sort = %d %s", rec.Code, rec.Body.String())
	}
	// A read leaves nothing on the audit trail.
	var audited int
	if err := h.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE path LIKE '%/databases/%' AND method = 'GET'`).Scan(&audited); err != nil || audited != 0 {
		t.Errorf("%d reads were audited (%v)", audited, err)
	}
}

// The capability is on the route. A viewer is refused every write here; a
// limited account may run maintenance and nothing else; stopping a statement,
// discarding the statement statistics and changing the server are an
// administrator's.
func TestOpsWritesNeedTheirCapability(t *testing.T) {
	h := newOpsHarness(t)
	for _, c := range []struct {
		method, path, body string
		limited            bool
	}{
		{http.MethodPost, "/maintenance", `{"action":"quick_check"}`, true},
		{http.MethodPost, "/statements/reset", `{}`, false},
		{http.MethodPut, "/settings", `{"name":"user_version","value":"3"}`, false},
		{http.MethodPost, "/server/roles/app/privileges", `{"level":"table","schema":"s","table":"t","privileges":["SELECT"]}`, false},
		{http.MethodPost, "/server/roles/app/privileges/revoke", `{"level":"table","schema":"s","table":"t","privileges":["SELECT"]}`, false},
		{http.MethodPost, "/server/roles/app/privileges?preview=1", `{"level":"table","schema":"s","table":"t","privileges":["SELECT"]}`, false},
		{http.MethodPost, "/server/roles/app/grant?preview=1", `{"level":"read","database":"x"}`, false},
		{http.MethodPost, "/activity/cancel", `{"pid":"7"}`, false},
	} {
		if rec := h.viewer.do(c.method, h.base+c.path, c.body, nil); rec.Code != http.StatusForbidden {
			t.Errorf("a viewer's %s %s = %d %s, want 403", c.method, c.path, rec.Code, rec.Body.String())
		}
		rec := h.worker.do(c.method, h.base+c.path, c.body, nil)
		if c.limited && rec.Code == http.StatusForbidden {
			t.Errorf("a limited account's %s %s was refused: %s", c.method, c.path, rec.Body.String())
		}
		if !c.limited && rec.Code != http.StatusForbidden {
			t.Errorf("a limited account's %s %s = %d %s, want 403", c.method, c.path, rec.Code, rec.Body.String())
		}
		if rec := h.admin.do(c.method, h.base+c.path, c.body, nil); rec.Code == http.StatusForbidden {
			t.Errorf("an administrator's %s %s was refused: %s", c.method, c.path, rec.Body.String())
		}
	}
	var version int
	db, err := sql.Open("sqlite", h.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 3 {
		t.Errorf("user_version = %d after only the administrator's change went through (%v)", version, err)
	}
}

// None of these is one of the four deletions that take a typed phrase, and
// asking for one on a routine action is what empties the phrase of meaning.
func TestOpsRoutesDoNotAskForAPhrase(t *testing.T) {
	h := newOpsHarness(t)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/activity/cancel", `{"pid":"7"}`},
		{http.MethodPost, "/maintenance", `{"action":"vacuum"}`},
		{http.MethodPost, "/statements/reset", `{}`},
		{http.MethodPut, "/settings", `{"name":"journal_mode","value":"wal"}`},
		{http.MethodPost, "/server/roles/app/privileges/revoke", `{"level":"table","schema":"s","table":"t","privileges":["ALL"]}`},
	} {
		rec := h.admin.do(c.method, h.base+c.path, c.body, nil)
		if strings.Contains(rec.Body.String(), "confirmation") || rec.Code == http.StatusPreconditionRequired {
			t.Errorf("%s %s asked for a phrase: %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

func TestMaintenanceRunsOneClosedActionAndIsAudited(t *testing.T) {
	h := newOpsHarness(t)
	var result struct {
		Action     string   `json:"action"`
		Statements []string `json:"statements"`
		Output     []string `json:"output"`
		OK         bool     `json:"ok"`
		Duration   string   `json:"duration"`
	}
	rec := h.worker.do(http.MethodPost, h.base+"/maintenance", `{"action":"integrity_check"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("integrity check: %d %s", rec.Code, rec.Body.String())
	}
	decodeOps(t, rec, &result)
	if result.Action != "integrity_check" || !result.OK || len(result.Output) != 1 || result.Output[0] != "ok" ||
		len(result.Statements) != 1 || result.Statements[0] != "PRAGMA integrity_check" || result.Duration == "" {
		t.Errorf("result: %s", rec.Body.String())
	}
	target, detail, status := auditEntry(t, h.s, "database.maintenance")
	if target != "opsdb" || status != http.StatusOK || !strings.Contains(detail, `PRAGMA integrity_check`) || !strings.Contains(detail, `"action":"integrity_check"`) {
		t.Errorf("audit: target %q status %d detail %s", target, status, detail)
	}

	rec = h.worker.do(http.MethodPost, h.base+"/maintenance", `{"action":"reindex","table":"child"}`, nil)
	decodeOps(t, rec, &result)
	if rec.Code != http.StatusOK || result.Statements[0] != `REINDEX "child"` {
		t.Errorf("reindex: %d %s", rec.Code, rec.Body.String())
	}
	rec = h.worker.do(http.MethodPost, h.base+"/maintenance", `{"action":"wal_checkpoint","options":{"mode":"passive"}}`, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "PRAGMA wal_checkpoint(PASSIVE)") {
		t.Errorf("checkpoint: %d %s", rec.Code, rec.Body.String())
	}

	for body, want := range map[string]string{
		`{"action":"drop_everything"}`:                                     "action must be one of",
		`{"action":""}`:                                                    "action must be one of",
		`{"action":"vacuum","table":"child"}`:                              "takes no table",
		`{"action":"analyze","table":"child\"; DROP TABLE parent; --"}`:    "",
		`{"action":"wal_checkpoint","options":{"mode":"TRUNCATE); DROP"}}`: "mode must be",
		`{"action":"vacuum","statement":"DROP TABLE parent"}`:              "unknown field",
	} {
		// An administrator's, so that each refusal is about the request and
		// not about who sent it.
		rec := h.admin.do(http.MethodPost, h.base+"/maintenance", body, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s = %d %s, want 400 with %q", body, rec.Code, rec.Body.String(), want)
		}
	}
	db, err := sql.Open("sqlite", h.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM parent`).Scan(&rows); err != nil || rows != 1 {
		t.Errorf("the table did not survive the refused requests: %d rows, %v", rows, err)
	}
}

// Whether an action needs the destructive capability is in the body, so the
// route cannot know from its path and the handler checks by hand — before
// anything is dialled, which is why a connection that goes nowhere can prove
// it. The actions that lock a table against the application, or can lose
// rows, are the ones the SQL console refuses the same account; a form must
// not be the cheaper way to run them.
func TestBlockingMaintenanceIsCheckedByContent(t *testing.T) {
	h := newOpsHarness(t)
	for _, c := range []struct {
		driver  dbx.Driver
		dsn     string
		refused []string // 403 for a limited account
		allowed []string // reach the dial, which fails: the server is not there
		foreign string   // an action of another engine
		ids     string
	}{
		{dbx.DriverMySQL, "u:p@tcp(127.0.0.1:1)/x",
			[]string{`{"action":"repair","table":"t"}`, `{"action":"optimize","table":"t"}`, `{"action":"optimize"}`},
			[]string{`{"action":"analyze","table":"t"}`, `{"action":"check"}`},
			`{"action":"vacuum_full","table":"t"}`, "analyze, check, optimize, repair"},
		{dbx.DriverPostgres, "postgres://u:p@127.0.0.1:1/x?sslmode=disable",
			[]string{`{"action":"vacuum_full"}`, `{"action":"vacuum_full","table":"t"}`, `{"action":"reindex"}`,
				`{"action":"reindex","table":"t","options":{"concurrently":true}}`},
			[]string{`{"action":"vacuum","table":"t"}`, `{"action":"vacuum_analyze"}`, `{"action":"analyze"}`},
			`{"action":"repair","table":"t"}`, "vacuum, vacuum_analyze, analyze, vacuum_full, reindex"},
		// A rebuild takes the table away from the application, online or not;
		// the rest of SQL Server's list works beside it.
		{dbx.DriverMSSQL, "sqlserver://u:p@127.0.0.1:1?database=x&encrypt=disable&dial+timeout=2",
			[]string{`{"action":"rebuild","table":"t"}`, `{"action":"rebuild","table":"t","options":{"online":true}}`},
			[]string{`{"action":"update_statistics"}`, `{"action":"reorganize","table":"t"}`, `{"action":"check"}`},
			`{"action":"vacuum_full","table":"t"}`, "update_statistics, reorganize, rebuild, check"},
		{dbx.DriverOracle, "oracle://u:p@127.0.0.1:1/x",
			nil,
			[]string{`{"action":"gather_stats"}`, `{"action":"gather_stats","table":"T"}`},
			`{"action":"rebuild","table":"T"}`, "gather_stats"},
	} {
		id := saveOpsConnection(t, h.s, "nowhere-"+string(c.driver), c.driver, c.dsn)
		base := fmt.Sprintf("/api/v1/databases/%d", id)
		for _, body := range c.refused {
			rec := h.worker.do(http.MethodPost, base+"/maintenance", body, nil)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "your role does not permit it") {
				t.Errorf("%s: a limited account's %s = %d %s, want 403", c.driver, body, rec.Code, rec.Body.String())
			}
			if rec := h.admin.do(http.MethodPost, base+"/maintenance", body, nil); rec.Code != http.StatusBadGateway {
				t.Errorf("%s: an administrator's %s = %d %s, want it to reach the dial", c.driver, body, rec.Code, rec.Body.String())
			}
		}
		// The same account, the same route, an action that works alongside
		// the application: it gets as far as the server.
		for _, body := range c.allowed {
			rec := h.worker.do(http.MethodPost, base+"/maintenance", body, nil)
			if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "connect_failed") {
				t.Errorf("%s: a limited account's %s = %d %s, want it to reach the dial", c.driver, body, rec.Code, rec.Body.String())
			}
		}
		// An action this engine does not have never reaches the capability
		// check or the server.
		rec := h.worker.do(http.MethodPost, base+"/maintenance", c.foreign, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.ids) {
			t.Errorf("%s: an action of another engine = %d %s", c.driver, rec.Code, rec.Body.String())
		}
	}

	// SQLite's VACUUM rewrites the whole file with nothing else able to use it.
	rec := h.worker.do(http.MethodPost, h.base+"/maintenance", `{"action":"vacuum"}`, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a limited account's SQLite VACUUM = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if rec := h.admin.do(http.MethodPost, h.base+"/maintenance", `{"action":"vacuum"}`, nil); rec.Code != http.StatusOK {
		t.Errorf("an administrator's SQLite VACUUM = %d %s", rec.Code, rec.Body.String())
	}
	// The list says which is which, so the page hides what the route refuses.
	var list struct {
		Actions []dbx.MaintenanceAction `json:"actions"`
	}
	decodeOps(t, h.viewer.do(http.MethodGet, h.base+"/maintenance", "", nil), &list)
	for _, a := range list.Actions {
		// The words are the session's capability names, so the page can ask
		// its own can() the same question.
		want := string(auth.CapServiceControl)
		if a.ID == "vacuum" {
			want = string(auth.CapDestructive)
		}
		if a.Requires != want {
			t.Errorf("%s is published as requiring %q, want %q", a.ID, a.Requires, want)
		}
	}
	// The budget is the destructive one: it runs out.
	limited := 0
	for i := 0; i < 40; i++ {
		if rec := h.admin.do(http.MethodPost, h.base+"/maintenance", `{"action":"vacuum"}`, nil); rec.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Error("forty blocking actions in a row were never rate limited")
	}
}

// A request to change an account is honoured whole or refused. Redis and
// MongoDB read a password and one flag; a request to lock an account there
// used to be answered 200 and written to the audit trail as a change, with
// nothing sent to the server. The refusal comes before the dial, so servers
// that are not there can show it.
func TestRoleRequestsTheEngineCannotHonourAreRefused(t *testing.T) {
	h := newOpsHarness(t)
	redis := fmt.Sprintf("/api/v1/databases/%d", saveOpsConnection(t, h.s, "kv", dbx.DriverRedis, "redis://127.0.0.1:1/0"))
	mongo := fmt.Sprintf("/api/v1/databases/%d", saveOpsConnection(t, h.s, "doc", dbx.DriverMongo, "mongodb://127.0.0.1:1/x"))
	clickhouse := fmt.Sprintf("/api/v1/databases/%d", saveOpsConnection(t, h.s, "ch", dbx.DriverClickHouse, "clickhouse://u:p@127.0.0.1:1/x"))
	for _, c := range []struct {
		method, path, body, want string
	}{
		{http.MethodPut, redis + "/server/roles/app", `{"locked":true}`, "Redis accounts have no locked attribute"},
		{http.MethodPut, redis + "/server/roles/app", `{"login":false}`, "Redis accounts have no login attribute"},
		{http.MethodPut, redis + "/server/roles/app", `{"superuser":false}`, "no administrator flag to clear"},
		{http.MethodPut, redis + "/server/roles/app", `{"password":"new-pw","connectionLimit":5}`, "no connectionLimit attribute"},
		{http.MethodPut, redis + "/server/roles/app", `{}`, "nothing to change"},
		{http.MethodPut, mongo + "/server/roles/app", `{"createDb":true}`, "MongoDB accounts have no createDb attribute"},
		{http.MethodPut, mongo + "/server/roles/app", `{"validUntil":"2030-01-01"}`, "no validUntil attribute"},
		{http.MethodPut, clickhouse + "/server/roles/app", `{"locked":true}`, "ClickHouse accounts have no locked attribute"},
		{http.MethodPost, redis + "/server/roles", `{"name":"app","password":"pw-long-enough","locked":true}`, "Redis accounts have no locked attribute"},
		{http.MethodPost, mongo + "/server/roles", `{"name":"app","password":"pw-long-enough","createRole":true}`, "no createRole attribute"},
		{http.MethodPost, clickhouse + "/server/roles", `{"name":"app","password":"pw-long-enough","login":false}`, "no login attribute"},
	} {
		rec := h.admin.do(c.method, c.path, c.body, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s %s = %d %s, want 400 with %q", c.method, c.path, c.body, rec.Code, rec.Body.String(), c.want)
		}
	}
	// Nothing was changed, and nothing says it was.
	var recorded int
	if err := h.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action IN ('database.role.alter', 'database.role.create')`).Scan(&recorded); err != nil || recorded != 0 {
		t.Errorf("%d refused requests were recorded as changes (%v)", recorded, err)
	}
	// What these engines can do still goes to the server, which is not there.
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPut, redis + "/server/roles/app", `{"password":"new-pw-long"}`},
		{http.MethodPut, mongo + "/server/roles/app", `{"superuser":true}`},
		// A create that carries a form's every field, none of them asking for anything.
		{http.MethodPost, redis + "/server/roles", `{"name":"app","password":"pw-long-enough","login":true,"superuser":false,"createDb":false,"createRole":false,"locked":false}`},
	} {
		if rec := h.admin.do(c.method, c.path, c.body, nil); rec.Code != http.StatusBadGateway {
			t.Errorf("%s %s %s = %d %s, want it to reach the dial", c.method, c.path, c.body, rec.Code, rec.Body.String())
		}
	}
}

// The account a connection signs in with cannot be barred from signing in,
// or demoted, from the connection it would cut off. A password change, and
// anything at all on another account, goes through.
func TestRoleAlterCannotLockTheConnectionOut(t *testing.T) {
	h := newOpsHarness(t)
	pg := fmt.Sprintf("/api/v1/databases/%d", saveOpsConnection(t, h.s, "pg-self", dbx.DriverPostgres, "postgres://dash:pw@127.0.0.1:1/x?sslmode=disable"))
	my := fmt.Sprintf("/api/v1/databases/%d", saveOpsConnection(t, h.s, "my-self", dbx.DriverMySQL, "dash:pw@tcp(127.0.0.1:1)/x"))
	for _, c := range []struct{ path, body string }{
		{pg + "/server/roles/dash", `{"login":false}`},
		{pg + "/server/roles/dash", `{"superuser":false}`},
		{pg + "/server/roles/DASH", `{"password":"new-pw-long","superuser":false}`},
		{my + "/server/roles/dash", `{"locked":true}`},
		{my + "/server/roles/dash?host=localhost", `{"login":false}`},
	} {
		rec := h.admin.do(http.MethodPut, c.path, c.body, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "the account this connection signs in with") {
			t.Errorf("PUT %s %s = %d %s, want 400", c.path, c.body, rec.Code, rec.Body.String())
		}
	}
	for _, c := range []struct{ path, body string }{
		{pg + "/server/roles/dash", `{"password":"new-pw-long"}`},
		{pg + "/server/roles/dash", `{"login":true,"superuser":true,"createDb":false}`},
		{pg + "/server/roles/other", `{"login":false,"superuser":false}`},
		{my + "/server/roles/other", `{"locked":true}`},
	} {
		if rec := h.admin.do(http.MethodPut, c.path, c.body, nil); rec.Code != http.StatusBadGateway {
			t.Errorf("PUT %s %s = %d %s, want it to reach the dial", c.path, c.body, rec.Code, rec.Body.String())
		}
	}
}

// ALTER ROLE … SET is where an application's secrets end up: PostgREST's
// documented configuration keeps its JWT signing key there. The role detail is
// on the read surface, so those values are an administrator's to see.
func TestRoleSettingsThatMayBeCredentialsAreWithheld(t *testing.T) {
	detail := func() *dbx.RoleDetail {
		return &dbx.RoleDetail{Config: []string{"search_path=app, public", "pgrst.jwt_secret=s3cr3t-signing-key",
			"statement_timeout=5s", "app.api_token=tok_live_123", "app.tenant=acme", "ssl_passphrase_command=echo x"}}
	}
	viewer := detail()
	redactRoleConfig(dbx.DriverPostgres, viewer, false)
	if got := strings.Join(viewer.Config, "|"); got != "search_path=app, public|statement_timeout=5s" {
		t.Errorf("a viewer sees %q", got)
	}
	if got := strings.Join(viewer.ConfigRedacted, "|"); got != "pgrst.jwt_secret|app.api_token|app.tenant|ssl_passphrase_command" {
		t.Errorf("a viewer is told %q was withheld", got)
	}
	encoded, err := json.Marshal(viewer)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"s3cr3t-signing-key", "tok_live_123", "echo x"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("%q reached a viewer: %s", secret, encoded)
		}
	}
	admin := detail()
	redactRoleConfig(dbx.DriverPostgres, admin, true)
	if len(admin.Config) != 6 || len(admin.ConfigRedacted) != 0 {
		t.Errorf("an administrator sees %v, withheld %v", admin.Config, admin.ConfigRedacted)
	}
	// Nothing set is an empty list, not a missing one.
	empty := &dbx.RoleDetail{Config: []string{}}
	redactRoleConfig(dbx.DriverPostgres, empty, false)
	if empty.Config == nil || len(empty.ConfigRedacted) != 0 {
		t.Errorf("an empty configuration became %v / %v", empty.Config, empty.ConfigRedacted)
	}
}

func TestSettingChangeIsValidatedPersistedAndAudited(t *testing.T) {
	h := newOpsHarness(t)
	var change struct {
		Name       string   `json:"name"`
		Value      string   `json:"value"`
		Statements []string `json:"statements"`
		Persisted  bool     `json:"persisted"`
	}
	rec := h.admin.do(http.MethodPut, h.base+"/settings", `{"name":"user_version","value":"42"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	decodeOps(t, rec, &change)
	if change.Name != "user_version" || change.Value != "42" || !change.Persisted || change.Statements[0] != "PRAGMA user_version = 42" {
		t.Errorf("change: %s", rec.Body.String())
	}
	target, detail, _ := auditEntry(t, h.s, "database.setting.set")
	if target != "opsdb" || !strings.Contains(detail, `"name":"user_version"`) || !strings.Contains(detail, `"value":"42"`) {
		t.Errorf("audit: %q %s", target, detail)
	}

	rec = h.admin.do(http.MethodPut, h.base+"/settings", `{"name":"journal_mode","value":"WAL"}`, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"value":"wal"`) {
		t.Errorf("journal mode: %d %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Settings []dbx.Setting `json:"settings"`
		Writable bool          `json:"writable"`
	}
	decodeOps(t, h.viewer.do(http.MethodGet, h.base+"/settings?all=1", "", nil), &list)
	seen := map[string]dbx.Setting{}
	for _, s := range list.Settings {
		seen[s.Name] = s
	}
	if !list.Writable || seen["journal_mode"].Value != "wal" || seen["user_version"].Value != "42" || !seen["user_version"].Changed ||
		!seen["journal_mode"].Editable || seen["foreign_keys"].Editable || seen["journal_mode"].Type != "enum" {
		t.Errorf("list after the changes: %+v", seen)
	}

	rec = h.admin.do(http.MethodPut, h.base+"/settings", `{"name":"user_version","reset":true}`, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"value":"0"`) {
		t.Errorf("reset: %d %s", rec.Code, rec.Body.String())
	}
	if _, detail, _ := auditEntry(t, h.s, "database.setting.reset"); !strings.Contains(detail, `"name":"user_version"`) {
		t.Errorf("reset audit: %s", detail)
	}

	for body, want := range map[string]string{
		`{"name":"user_version","value":"1; DROP TABLE parent"}`: "takes a number",
		`{"name":"journal_mode","value":"off"}`:                  "is one of",
		`{"name":"foreign_keys","value":"off"}`:                  "cannot be changed from here",
		`{"name":"writable_schema","value":"1"}`:                 "is not a parameter of this server",
		`{"name":"","value":"1"}`:                                "a parameter name is required",
		`{"name":"user_version","value":"1","scope":"session"}`:  "unknown field",
	} {
		rec := h.admin.do(http.MethodPut, h.base+"/settings", body, nil)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s = %d %s, want 400 with %q", body, rec.Code, rec.Body.String(), want)
		}
	}
}

// A parameter that can hold a credential is shown to an administrator and
// withheld, marked, from everyone else: the engine shows it to a superuser,
// and the dashboard's connection is usually one.
func TestSensitiveSettingsAreWithheldFromNonAdministrators(t *testing.T) {
	list := func() []dbx.Setting {
		return []dbx.Setting{
			{Name: "primary_conninfo", Value: "host=10.0.0.1 password=hunter2", Default: "x"},
			{Name: "ssl_passphrase_command", Value: "echo hunter2"},
			{Name: "archive_command", Value: "aws s3 cp --secret"},
			{Name: "max_connections", Value: "100"},
			{Name: "primary_slot_name", Value: ""},
			{Name: "password_encryption", Value: "scram-sha-256"},
		}
	}
	redacted := redactSettings(dbx.DriverPostgres, list(), false)
	for _, s := range redacted[:3] {
		if s.Value != "" || s.Default != "" || !s.Redacted {
			t.Errorf("%s reached a viewer: %+v", s.Name, s)
		}
	}
	if redacted[3].Value != "100" || redacted[3].Redacted {
		t.Errorf("an ordinary parameter was withheld: %+v", redacted[3])
	}
	// Nothing to withhold is not marked as withheld.
	if redacted[4].Redacted {
		t.Errorf("an empty value was marked redacted")
	}
	// How passwords are hashed is policy, which the advisor reports to the
	// same viewer; hiding it here said two different things on two pages.
	if redacted[5].Value != "scram-sha-256" || redacted[5].Redacted {
		t.Errorf("password_encryption was withheld: %+v", redacted[5])
	}
	for _, s := range redactSettings(dbx.DriverPostgres, list(), true) {
		if s.Redacted {
			t.Errorf("%s was withheld from an administrator", s.Name)
		}
	}
}

// Which names are a credential's is decided per engine and by the whole last
// word of the name, not by a word appearing somewhere in it.
func TestSensitiveSettingNames(t *testing.T) {
	for driver, names := range map[dbx.Driver][]string{
		dbx.DriverPostgres: {"primary_conninfo", "ssl_passphrase_command", "archive_command", "restore_command",
			"archive_cleanup_command", "recovery_end_command", "PRIMARY_CONNINFO",
			// An extension's or an application's own parameter: the name is
			// all there is to go on.
			"pgrst.jwt_secret", "citus.node_conninfo", "app.api_token", "app.settings.jwt", "pgsodium.getkey_script",
			"timescaledb.license", "myapp.db_passwd"},
		dbx.DriverMySQL: {"report_password", "wsrep_sst_auth", "file_key_management_filekey",
			"authentication_ldap_simple_bind_root_pwd", "hashicorp_key_management_token", "keyring_hashicorp_secret_id"},
		dbx.DriverMSSQL:      {"linked_server_password"},
		dbx.DriverClickHouse: {"s3_secret", "interserver_http_credentials"},
	} {
		for _, name := range names {
			if !sensitiveSetting(driver, name) {
				t.Errorf("%s %s is shown to a viewer", driver, name)
			}
		}
	}
	for driver, names := range map[dbx.Driver][]string{
		dbx.DriverPostgres: {"password_encryption", "ssl_passphrase_command_supports_reload", "ssl_key_file", "krb_server_keyfile",
			"max_connections", "log_line_prefix", "shared_preload_libraries", "pg_stat_statements.max",
			"auto_explain.log_min_duration", "pg_trgm.similarity_threshold", "plpgsql.variable_conflict"},
		dbx.DriverMySQL: {"default_password_lifetime", "password_history", "password_reuse_interval", "password_require_current",
			"validate_password.length", "validate_password_policy", "old_passwords", "innodb_ft_max_token_size",
			"ngram_token_size", "foreign_key_checks", "key_buffer_size", "ssl_key", "sha256_password_private_key_path",
			"caching_sha2_password_private_key_path", "secure_file_priv", "max_connections"},
		dbx.DriverMSSQL:  {"cost threshold for parallelism", "max server memory (MB)", "contained database authentication"},
		dbx.DriverOracle: {"remote_login_passwordfile", "sec_case_sensitive_logon", "open_cursors", "wallet_root"},
	} {
		for _, name := range names {
			if sensitiveSetting(driver, name) {
				t.Errorf("%s %s is hidden from a viewer and holds no credential", driver, name)
			}
		}
	}
}

// The snapshot route answered with the dashboard's own pool before there was
// a snapshot, and still answers when the server refuses one: unsupported, with
// the engine's refusal as the reason and the pool beside it.
func TestStatsAnswersWithThePoolWhenTheSnapshotIsRefused(t *testing.T) {
	h := newOpsHarness(t)
	if rec := h.viewer.do(http.MethodGet, h.base+"/stats", "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"supported":true`) {
		t.Fatalf("stats before: %d %s", rec.Code, rec.Body.String())
	}
	// The pool is open; what it reads from is no longer a database.
	if err := os.WriteFile(h.path, []byte(strings.Repeat("this is not a database\n", 400)), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := h.viewer.do(http.MethodGet, h.base+"/stats", "", nil)
	var stats struct {
		Supported bool               `json:"supported"`
		Reason    string             `json:"reason"`
		At        time.Time          `json:"at"`
		Driver    string             `json:"driver"`
		Counters  map[string]float64 `json:"counters"`
		Gauges    map[string]float64 `json:"gauges"`
		Pool      *struct {
			MaxOpen int `json:"maxOpen"`
		} `json:"pool"`
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("stats of a server that refuses the snapshot: %d %s", rec.Code, rec.Body.String())
	}
	decodeOps(t, rec, &stats)
	if stats.Supported || stats.Reason == "" || stats.At.IsZero() || stats.Driver != "sqlite" ||
		stats.Counters == nil || stats.Gauges == nil || stats.Pool == nil {
		t.Errorf("stats: %s", rec.Body.String())
	}
}

func TestCancelOnAnEngineWithNoSessionsSaysSo(t *testing.T) {
	h := newOpsHarness(t)
	rec := h.admin.do(http.MethodPost, h.base+"/activity/cancel", `{"pid":"7"}`, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no server-side session list") {
		t.Errorf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	if rec := h.admin.do(http.MethodPost, h.base+"/activity/cancel", `{"pid":" "}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("an empty session id = %d", rec.Code)
	}
	rec = h.admin.do(http.MethodPost, h.base+"/statements/reset", `{}`, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "keeps no statement statistics") {
		t.Errorf("reset: %d %s", rec.Code, rec.Body.String())
	}
}

// The findings only this dashboard can make. A database nobody has dumped is
// said to have no backup; one whose newest dump is old is said to have a
// stale one; and a viewer is told which checks were an administrator's.
func TestAdvisorAddsWhatOnlyTheDashboardKnows(t *testing.T) {
	h := newOpsHarness(t)
	type report struct {
		Findings []dbx.Advice `json:"findings"`
		Silences []string     `json:"silences"`
	}
	read := func(c *client) report {
		t.Helper()
		rec := c.do(http.MethodGet, h.base+"/advisor", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("advisor: %d %s", rec.Code, rec.Body.String())
		}
		var out report
		decodeOps(t, rec, &out)
		return out
	}
	find := func(r report, id string) *dbx.Advice {
		for i := range r.Findings {
			if r.Findings[i].ID == id {
				return &r.Findings[i]
			}
		}
		return nil
	}

	first := read(h.viewer)
	none := find(first, "no-backup")
	if none == nil || none.Category != dbx.AdviceReliability || none.Link != h.base[len("/api/v1"):]+"/backups" || len(none.Targets) != 1 {
		t.Fatalf("no-backup finding: %+v in %+v", none, first.Findings)
	}
	said := false
	for _, s := range first.Silences {
		said = said || strings.Contains(s, "administrator")
	}
	if !said {
		t.Errorf("a viewer was not told which checks were skipped: %v", first.Silences)
	}
	for _, s := range read(h.admin).Silences {
		if strings.Contains(s, "administrator") {
			t.Errorf("an administrator was told a check was skipped: %s", s)
		}
	}
	// Every finding is in one of the four categories and names its object;
	// every link is a page of this connection.
	for _, f := range first.Findings {
		switch f.Category {
		case dbx.AdviceSecurity, dbx.AdvicePerformance, dbx.AdviceReliability, dbx.AdviceMaintenance:
		default:
			t.Errorf("%s has category %q", f.ID, f.Category)
		}
		if len(f.Targets) == 0 {
			t.Errorf("%s names no object", f.ID)
		}
		if f.Link != "" && !strings.HasPrefix(f.Link, fmt.Sprintf("/databases/%d/", h.id)) {
			t.Errorf("%s links to %q", f.ID, f.Link)
		}
	}

	dir := h.s.dbDumpDir("opsdb")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dump := filepath.Join(dir, "ops-20260101-000000.sqlite")
	if err := os.WriteFile(dump, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := read(h.viewer)
	if find(fresh, "no-backup") != nil || find(fresh, "stale-backup") != nil {
		t.Errorf("a fresh dump still produced a backup finding: %+v", fresh.Findings)
	}
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(dump, old, old); err != nil {
		t.Fatal(err)
	}
	stale := find(read(h.viewer), "stale-backup")
	if stale == nil || !strings.Contains(stale.Title, "10 days") {
		t.Errorf("stale finding: %+v", stale)
	}
}

// An alter carries what was sent and nothing else. A body with only a
// password must not say anything about the role's attributes.
func TestRoleAlterRequestCarriesOnlyWhatWasSent(t *testing.T) {
	var passwordOnly roleRequest
	if err := json.Unmarshal([]byte(`{"password":"new-pw","host":"%"}`), &passwordOnly); err != nil {
		t.Fatal(err)
	}
	spec := passwordOnly.spec(false)
	if got := strings.Join(spec.Changes(), ","); got != "password" {
		t.Errorf("a password change carries %q", got)
	}
	if spec.SetLogin || spec.SetSuperuser || spec.SetCreateDB || spec.SetCreateRole {
		t.Errorf("a password change sets flags: %+v", spec)
	}

	var demote roleRequest
	if err := json.Unmarshal([]byte(`{"superuser":false,"connectionLimit":-1,"validUntil":"infinity","locked":true}`), &demote); err != nil {
		t.Fatal(err)
	}
	spec = demote.spec(false)
	if got := strings.Join(spec.Changes(), ","); got != "superuser,connectionLimit,locked,validUntil" {
		t.Errorf("changes = %q", got)
	}
	if spec.Superuser || !spec.SetSuperuser || spec.SetPassword {
		t.Errorf("spec: %+v", spec)
	}

	// A create has a shape: it signs in, and is nothing else, unless asked.
	var create roleRequest
	if err := json.Unmarshal([]byte(`{"name":"app","password":"pw","createDb":true}`), &create); err != nil {
		t.Fatal(err)
	}
	spec = create.spec(true)
	if !spec.Login || spec.Superuser || !spec.CreateDB || spec.CreateRole || !spec.SetPassword {
		t.Errorf("create spec: %+v", spec)
	}
}

// The log resolver asks Docker which container publishes a port. With no
// Docker client it must find nothing, not fall over.
func TestDatabaseLogResolverSurvivesWithoutDocker(t *testing.T) {
	s := testServer(t)
	s.modules.docker = nil
	if c := s.containerPublishing(t.Context(), 5432); c != nil {
		t.Errorf("found %+v with no Docker client", c)
	}
	conn := &dbConnection{ID: 1, Name: "pg", Driver: dbx.DriverPostgres}
	out := s.dbLogsWithoutListener(t.Context(), conn, 5432, "postgres", nil, dbHostProbe{logDir: t.TempDir()})
	if out.Reason == "" && len(out.Sources) == 0 {
		t.Errorf("no sources and no reason: %+v", out)
	}
}

// ACL LIST prints each password's hash inside the rule, and the account list
// is on the read surface. The rule is shown; the hashes are not.
func TestRedisAccountRulesAreListedWithoutTheirPasswordHashes(t *testing.T) {
	roles := withoutACLSecrets([]dbx.Role{
		{Name: "default", MemberOf: []string{"on nopass sanitize-payload ~* &* +@all"}},
		{Name: "app", MemberOf: []string{"on sanitize-payload #5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8 ~app:* resetchannels -@all +get +set"}},
		{Name: "odd", MemberOf: []string{"on >cleartext <older !deadbeef ~* +@read"}},
		{Name: "bare"},
	})
	if got := roles[0].MemberOf[0]; got != "on nopass sanitize-payload ~* &* +@all" {
		t.Errorf("a rule with no password changed: %q", got)
	}
	if got := roles[1].MemberOf[0]; got != "on sanitize-payload ~app:* resetchannels -@all +get +set" {
		t.Errorf("app rule: %q", got)
	}
	if got := roles[2].MemberOf[0]; got != "on ~* +@read" {
		t.Errorf("odd rule: %q", got)
	}
	if roles[3].MemberOf != nil {
		t.Errorf("an account with no rule gained one: %v", roles[3].MemberOf)
	}
}

// opsDockerEngine is a Docker daemon running one PostgreSQL container that
// publishes its port on every interface, with the memory limit given.
func opsDockerEngine(t *testing.T, s *Server, hostPort int, memory int64) {
	t.Helper()
	container := map[string]any{
		"Id": "c0ffee", "Names": []string{"/shop-db"}, "Image": "postgres:16", "ImageID": "sha256:abc", "State": "running",
		"Ports": []map[string]any{{"IP": "0.0.0.0", "PrivatePort": 5432, "PublicPort": hostPort, "Type": "tcp"}},
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/_ping":
			w.Header().Set("API-Version", "1.47")
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_ = json.NewEncoder(w).Encode([]map[string]any{container})
		case strings.HasSuffix(r.URL.Path, "/containers/shop-db/json"), strings.HasSuffix(r.URL.Path, "/containers/c0ffee/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id": "c0ffee", "Name": "/shop-db", "Image": "sha256:abc",
				"Config":          map[string]any{"Image": "postgres:16"},
				"State":           map[string]any{"Status": "running", "Running": true},
				"HostConfig":      map[string]any{"Memory": memory},
				"NetworkSettings": map[string]any{"Networks": map[string]any{}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "not found"})
		}
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
}

// What only an administrator's view reads: a port published to every
// address, and a container the kernel may let grow until it kills something.
func TestAdvisorReadsTheContainerForAnAdministratorOnly(t *testing.T) {
	as := func(s *Server, role auth.Role) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/databases/1/advisor", nil)
		p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: role, Kind: "session", IP: "127.0.0.1"}
		return req.WithContext(httpx.WithPrincipal(req.Context(), p))
	}
	ids := func(findings []dbx.Advice) string {
		out := []string{}
		for _, f := range findings {
			out = append(out, f.ID)
		}
		return strings.Join(out, ",")
	}
	for _, c := range []struct {
		memory int64
		want   string
	}{
		{0, "no-backup,published-everywhere,container-no-memory-limit"},
		{512 << 20, "no-backup,published-everywhere"},
	} {
		s := testServer(t)
		s.Cfg.BackupLocalDir = t.TempDir()
		opsDockerEngine(t, s, 55440, c.memory)
		id := saveOpsConnection(t, s, "shop", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:55440/shop?sslmode=disable")
		conn, _, err := s.dbConnRow(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		admin := as(s, auth.RoleAdmin)
		findings, silences := s.panelAdvice(admin.Context(), admin, conn)
		if got := ids(findings); got != c.want {
			t.Errorf("memory %d: an administrator sees %q, want %q", c.memory, got, c.want)
		}
		if len(silences) != 0 {
			t.Errorf("an administrator was told a check was skipped: %v", silences)
		}
		for _, f := range findings {
			if f.ID == "container-no-memory-limit" && (len(f.Targets) != 1 || f.Targets[0].Kind != "container" || f.Targets[0].Name != "shop-db") {
				t.Errorf("the finding does not name the container: %+v", f)
			}
		}
		// A viewer may not list containers, so neither finding is made for
		// one, and that is said rather than left to read as a clean result.
		viewer := as(s, auth.RoleReadOnly)
		findings, silences = s.panelAdvice(viewer.Context(), viewer, conn)
		if got := ids(findings); got != "no-backup" {
			t.Errorf("a viewer sees %q", got)
		}
		if len(silences) != 1 {
			t.Errorf("a viewer's silences: %v", silences)
		}
	}
}
