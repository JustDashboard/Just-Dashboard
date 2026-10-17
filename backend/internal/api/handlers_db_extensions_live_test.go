package api

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// The extension routes against real PostgreSQL servers, through the handlers.

type extensionsView struct {
	Extensions []dbx.Extension   `json:"extensions"`
	Supported  bool              `json:"supported"`
	Editable   bool              `json:"editable"`
	Preload    *dbx.PreloadState `json:"preload"`
	Reason     string            `json:"reason"`
}

func (v extensionsView) find(name string) dbx.Extension {
	for _, e := range v.Extensions {
		if e.Name == name {
			return e
		}
	}
	return dbx.Extension{}
}

type plannedView struct {
	Statement  string   `json:"statement"`
	Statements []string `json:"statements"`
	Preview    bool     `json:"preview"`
	Libraries  []string `json:"libraries"`
	Notes      []string `json:"notes"`
}

// ownPostgresDatabase makes a database for the test on the server dsn names,
// and returns the connection string for it: an extension belongs to one
// database, and the server's others are somebody else's.
func ownPostgresDatabase(t *testing.T, dsn, name string) string {
	t.Helper()
	server, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	drop := func() { _, _ = server.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`) }
	drop()
	if _, err := server.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Skipf("the server will not make a database for the test: %v", err)
	}
	t.Cleanup(drop)
	return dbx.DSNForDatabase(dbx.DriverPostgres, dsn, name)
}

// Every route, who may use it, what a preview leaves behind and what a change
// is recorded as.
func TestLiveAPIExtensionsPostgres(t *testing.T) {
	dsn := ownPostgresDatabase(t, opsLiveDSN(t, "JD_TEST_POSTGRES_DSN"), "jdt_extension_manager_api")
	s, _, id := liveAPIRouter(t, dbx.DriverPostgres, dsn)
	admin, viewer := asRole(s, auth.RoleAdmin), asRole(s, auth.RoleReadOnly)
	base := pathf("/databases/%d/server", id)
	audited := func(action string) int {
		t.Helper()
		var n int
		if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	list := readJSON[extensionsView](t, do(t, viewer, http.MethodGet, base+"/extensions", ""))
	if !list.Supported || !list.Editable || list.Preload == nil || list.find("hstore").Category != dbx.ExtensionDataTypes {
		t.Fatalf("the list = supported %v, editable %v, preload %+v, hstore %+v",
			list.Supported, list.Editable, list.Preload, list.find("hstore"))
	}
	detail := readJSON[dbx.ExtensionDetail](t, do(t, viewer, http.MethodGet, base+"/extensions/hstore", ""))
	if len(detail.Versions) < 2 || detail.Installed {
		t.Fatalf("hstore's detail = %+v", detail)
	}
	if rec := do(t, viewer, http.MethodGet, base+"/extensions/jdt_extension_manager_none", ""); rec.Code != http.StatusNotFound {
		t.Errorf("the detail of an extension the server does not ship = %d %s", rec.Code, rec.Body.String())
	}

	body := `{"name":"hstore","version":"1.4"}`
	preview := readJSON[plannedView](t, do(t, admin, http.MethodPost, base+"/extensions?preview=1", body))
	if !preview.Preview || preview.Statement != `CREATE EXTENSION IF NOT EXISTS "hstore" VERSION '1.4'` {
		t.Fatalf("preview = %+v", preview)
	}
	if n := audited("database.extension.create"); n != 0 {
		t.Errorf("a preview was audited %d times", n)
	}
	if rec := do(t, viewer, http.MethodPost, base+"/extensions", body); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer's create = %d", rec.Code)
	}
	ran := readJSON[plannedView](t, do(t, admin, http.MethodPost, base+"/extensions", body))
	if ran.Statement != preview.Statement || ran.Preview {
		t.Errorf("ran %+v after previewing %q", ran, preview.Statement)
	}
	if _, entry, status := auditEntry(t, s, "database.extension.create"); status != http.StatusOK ||
		!strings.Contains(entry, `"version":"1.4"`) || !strings.Contains(entry, "VERSION '1.4'") {
		t.Errorf("create audited as %d %s", status, entry)
	}

	update := readJSON[plannedView](t, do(t, admin, http.MethodPatch, base+"/extensions/hstore", `{"version":"1.5"}`))
	if update.Statement != `ALTER EXTENSION "hstore" UPDATE TO '1.5'` {
		t.Errorf("update = %+v", update)
	}
	if _, entry, status := auditEntry(t, s, "database.extension.update"); status != http.StatusOK ||
		!strings.Contains(entry, "UPDATE TO '1.5'") {
		t.Errorf("update audited as %d %s", status, entry)
	}
	if rec := do(t, admin, http.MethodPatch, base+"/extensions/hstore", `{"version":"9.9"}`); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "no update for hstore") {
		t.Errorf("an update with no path = %d %s", rec.Code, rec.Body.String())
	}

	if rec := do(t, viewer, http.MethodDelete, base+"/extensions/hstore?cascade=1", ""); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer's drop = %d", rec.Code)
	}
	drop := readJSON[plannedView](t, do(t, admin, http.MethodDelete, base+"/extensions/hstore?cascade=1", ""))
	if drop.Statement != `DROP EXTENSION "hstore" CASCADE` {
		t.Errorf("drop = %+v", drop)
	}
	if _, entry, status := auditEntry(t, s, "database.extension.drop"); status != http.StatusOK ||
		!strings.Contains(entry, `"cascade":true`) {
		t.Errorf("drop audited as %d %s", status, entry)
	}

	// The fixture is started with its list on the command line, which no
	// ALTER SYSTEM outranks: the preload says so, and runs nothing.
	if !list.Preload.Settable {
		rec := do(t, admin, http.MethodPost, base+"/preload?preview=1", `{"library":"pg_cron"}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), strings.TrimSuffix(list.Preload.Reason, ".")) {
			t.Errorf("a preload where the list cannot be changed = %d %s", rec.Code, rec.Body.String())
		}
		if n := audited("database.extension.preload"); n != 0 {
			t.Errorf("a refused preview was audited %d times", n)
		}
	}
}

// The whole of what enabling pg_cron takes, on a server of the test's own: the
// library added to what the server loads at start with every entry already
// there kept, the restart by the power route, then the extension.
//
// JD_TEST_PRELOAD_POSTGRES_DSN names a PostgreSQL in a container this test may
// reconfigure and restart: published on loopback, with "preload" in its name,
// signed in to as a superuser, and with nothing on its command line about the
// libraries it loads. pg_cron is used where the image has it. The container is
// left as it was found.
func TestLivePreloadRestartAndCreate(t *testing.T) {
	dsn := os.Getenv("JD_TEST_PRELOAD_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_PRELOAD_POSTGRES_DSN to a PostgreSQL in a container this test may reconfigure and restart")
	}
	h := &connHarness{t: t, s: testServer(t)}
	docker := dockerx.New(envOr("JD_TEST_DOCKER_HOST", "unix:///var/run/docker.sock"))
	t.Cleanup(func() { _ = docker.Close() })
	h.s.modules.docker = docker
	admin := h.as(auth.RoleAdmin)
	id := h.add("preload", dbx.DriverPostgres, dsn)
	path := pathf("/databases/%d", id)

	summary := readJSON[summaryView](t, do(t, admin, http.MethodGet, path, ""))
	if summary.Source != "docker" || summary.Container == nil || !strings.Contains(summary.Container.Name, "preload") ||
		!summary.Power.Restart {
		t.Skipf("%s is not a running container of this test's own: %+v", dsn, summary)
	}
	direct, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { direct.Close() })
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DROP EXTENSION IF EXISTS pg_cron`, `DROP EXTENSION IF EXISTS pg_stat_statements`,
			`ALTER SYSTEM RESET shared_preload_libraries`, `ALTER SYSTEM RESET cron.database_name`,
			`ALTER SYSTEM RESET auto_explain.log_min_duration`,
		} {
			_, _ = direct.Exec(stmt)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = docker.Lifecycle(ctx, summary.Container.Name, dockerx.ActionRestart, nil)
	})

	state := func() dbx.PreloadState {
		t.Helper()
		view := readJSON[extensionsView](t, do(t, admin, http.MethodGet, path+"/server/extensions", ""))
		if view.Preload == nil {
			t.Fatalf("no preload state: %+v", view)
		}
		return *view.Preload
	}
	before := state()
	if !before.Settable || before.PendingRestart || len(before.Loaded) != 0 {
		t.Skipf("the server does not start clean: %+v", before)
	}
	preload := func(library string) plannedView {
		t.Helper()
		rec := do(t, admin, http.MethodPost, path+"/server/preload", `{"library":"`+library+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("preload %s = %d %s", library, rec.Code, rec.Body.String())
		}
		return readJSON[plannedView](t, rec)
	}
	restart := func() {
		t.Helper()
		if rec := do(t, admin, http.MethodPost, path+"/power", `{"action":"restart","timeoutSeconds":20}`); rec.Code != http.StatusOK {
			t.Fatalf("restart = %d %s", rec.Code, rec.Body.String())
		}
		deadline := time.Now().Add(time.Minute)
		for do(t, admin, http.MethodGet, path+"/server/extensions", "").Code != http.StatusOK {
			if time.Now().After(deadline) {
				t.Fatal("the server did not come back from its restart")
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	create := func(name string) {
		t.Helper()
		if rec := do(t, admin, http.MethodPost, path+"/server/extensions", `{"name":"`+name+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("create %s = %d %s", name, rec.Code, rec.Body.String())
		}
	}

	// From nothing: the library, then the restart, then the extension.
	first := preload("pg_stat_statements")
	if strings.Join(first.Libraries, ",") != "pg_stat_statements" ||
		first.Statements[0] != `ALTER SYSTEM SET shared_preload_libraries = 'pg_stat_statements'` {
		t.Fatalf("first preload = %+v", first)
	}
	if got := state(); !got.PendingRestart || strings.Join(got.Configured, ",") != "pg_stat_statements" || len(got.Loaded) != 0 {
		t.Errorf("before the restart: %+v", got)
	}
	if rec := do(t, admin, http.MethodPost, path+"/server/preload", `{"library":"pg_stat_statements"}`); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "loaded once the server restarts") {
		t.Errorf("the same library again = %d %s", rec.Code, rec.Body.String())
	}
	restart()
	if got := state(); got.PendingRestart || strings.Join(got.Loaded, ",") != "pg_stat_statements" {
		t.Fatalf("after the restart: %+v", got)
	}
	create("pg_stat_statements")
	var calls int64
	if err := direct.QueryRow(`SELECT COALESCE(sum(calls), 0)::bigint FROM pg_stat_statements`).Scan(&calls); err != nil {
		t.Errorf("pg_stat_statements does not answer once created: %v", err)
	}

	// Two more, one after the other before a restart: the second keeps the
	// first, and both keep what was loaded.
	explain := preload("auto_explain")
	if strings.Join(explain.Libraries, ",") != "pg_stat_statements,auto_explain" ||
		!containsAll(explain.Statements, `ALTER SYSTEM SET "auto_explain"."log_min_duration" = '1s'`) {
		t.Fatalf("auto_explain = %+v", explain)
	}
	want := "pg_stat_statements,auto_explain"
	var database string
	if err := direct.QueryRow(`SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	var cron bool
	_ = direct.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'pg_cron')`).Scan(&cron)
	if cron {
		plan := preload("pg_cron")
		want += ",pg_cron"
		if strings.Join(plan.Libraries, ",") != want {
			t.Fatalf("pg_cron after a change not yet restarted for = %+v", plan)
		}
		if database != "postgres" && !containsAll(plan.Statements, `ALTER SYSTEM SET "cron"."database_name" = '`+database+`'`) {
			t.Errorf("pg_cron's database = %+v", plan)
		}
	}
	restart()
	if got := state(); got.PendingRestart || strings.Join(got.Loaded, ",") != want {
		t.Fatalf("after the second restart: %+v, want %s", got, want)
	}
	var threshold string
	if err := direct.QueryRow(`SHOW auto_explain.log_min_duration`).Scan(&threshold); err != nil || threshold != "1s" {
		t.Errorf("auto_explain.log_min_duration = %q (%v)", threshold, err)
	}
	if cron {
		create("pg_cron")
		var jobs int
		if err := direct.QueryRow(`SELECT count(*) FROM cron.job`).Scan(&jobs); err != nil {
			t.Errorf("pg_cron does not answer once created: %v", err)
		}
	}
}

func containsAll(list []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, item := range list {
			found = found || item == w
		}
		if !found {
			return false
		}
	}
	return true
}
