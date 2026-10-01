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
// limited account may run maintenance and reset statistics and nothing else;
// stopping a statement and changing the server are an administrator's.
func TestOpsWritesNeedTheirCapability(t *testing.T) {
	h := newOpsHarness(t)
	for _, c := range []struct {
		method, path, body string
		limited            bool
	}{
		{http.MethodPost, "/maintenance", `{"action":"quick_check"}`, true},
		{http.MethodPost, "/statements/reset", `{}`, true},
		{http.MethodPut, "/settings", `{"name":"user_version","value":"3"}`, false},
		{http.MethodPost, "/server/roles/app/privileges", `{"level":"table","schema":"s","table":"t","privileges":["SELECT"]}`, false},
		{http.MethodPost, "/server/roles/app/privileges/revoke", `{"level":"table","schema":"s","table":"t","privileges":["SELECT"]}`, false},
		{http.MethodPost, "/server/roles/app/privileges?preview=1", `{"level":"table","schema":"s","table":"t","privileges":["SELECT"]}`, false},
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
		rec := h.worker.do(http.MethodPost, h.base+"/maintenance", body, nil)
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

// Whether an action is destructive is in the body, so the route cannot know
// from its path and the handler checks by hand — before anything is dialled,
// which is why a connection that goes nowhere can prove it.
func TestDestructiveMaintenanceIsCheckedByContent(t *testing.T) {
	h := newOpsHarness(t)
	id := saveOpsConnection(t, h.s, "nowhere", dbx.DriverMySQL, "u:p@tcp(127.0.0.1:1)/x")
	base := fmt.Sprintf("/api/v1/databases/%d", id)

	rec := h.worker.do(http.MethodPost, base+"/maintenance", `{"action":"repair","table":"t"}`, nil)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "repair") {
		t.Errorf("a limited account's REPAIR = %d %s, want 403", rec.Code, rec.Body.String())
	}
	// The same account, the same route, an action that loses nothing: it gets
	// as far as the server, which is not there.
	rec = h.worker.do(http.MethodPost, base+"/maintenance", `{"action":"analyze","table":"t"}`, nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "connect_failed") {
		t.Errorf("a limited account's ANALYZE = %d %s, want it to reach the dial", rec.Code, rec.Body.String())
	}
	rec = h.admin.do(http.MethodPost, base+"/maintenance", `{"action":"repair","table":"t"}`, nil)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("an administrator's REPAIR = %d %s, want it to reach the dial", rec.Code, rec.Body.String())
	}
	// An action this engine does not have never reaches the capability check
	// or the server.
	rec = h.worker.do(http.MethodPost, base+"/maintenance", `{"action":"vacuum_full","table":"t"}`, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "analyze, check, optimize, repair") {
		t.Errorf("an action of another engine = %d %s", rec.Code, rec.Body.String())
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
			{Name: "password_encryption", Value: ""},
		}
	}
	redacted := redactSettings(list(), false)
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
	for _, s := range redactSettings(list(), true) {
		if s.Redacted {
			t.Errorf("%s was withheld from an administrator", s.Name)
		}
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
