package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
)

// The two ways a PostgreSQL server comes to count its statements, on real
// servers in containers of the test's own.
//
// JD_TEST_STATEMENTS_CONTAINER names them: each is that name with what it
// is after it ("<name>-postgres", "<name>-setup"), with a volume of the same
// name and "-data", made one at a time and removed with its volume when its
// part is done. A template's image is pulled where it is missing.

type liveStatements struct {
	Supported  bool `json:"supported"`
	Statements []struct {
		Query string `json:"query"`
		Calls int64  `json:"calls"`
	} `json:"statements"`
	Enable *dbx.StatementsEnable `json:"enable"`
}

const liveStatementsPassword = "jdt-statements-9"

// A server from each PostgreSQL template counts its statements from its first
// start: the module is loaded, the extension is in the new database, and the
// statements page reads what was run. auto_explain is there when it was asked
// for, with its thresholds; and TimescaleDB, whose image loads a library of
// its own, keeps it.
func TestLiveTemplatesCountStatementsFromTheFirstStart(t *testing.T) {
	prefix := os.Getenv("JD_TEST_STATEMENTS_CONTAINER")
	if prefix == "" {
		t.Skip("JD_TEST_STATEMENTS_CONTAINER unset")
	}
	for _, c := range []struct {
		engine      string
		autoExplain bool
		libraries   []string
	}{
		{"postgres", true, []string{"pg_stat_statements", "auto_explain"}},
		{"pgvector", false, []string{"pg_stat_statements"}},
		{"postgis", false, []string{"pg_stat_statements"}},
		{"timescaledb", false, []string{"timescaledb", "pg_stat_statements"}},
	} {
		t.Run(c.engine, func(t *testing.T) {
			docker, router := liveDockerHarness(t)
			name := prefix + "-" + c.engine
			liveRemoveOnCleanup(t, docker, name)

			rec := do(t, router, http.MethodPost, "/databases/provision", fmt.Sprintf(
				`{"engine":%q,"name":%q,"password":%q,"autoExplain":%v}`, c.engine, name, liveStatementsPassword, c.autoExplain))
			if rec.Code == http.StatusBadRequest && strings.Contains(rec.Body.String(), "x86-64 only") {
				t.Skip(rec.Body.String())
			}
			if rec.Code != http.StatusAccepted {
				t.Fatalf("provision = %d %s", rec.Code, rec.Body.String())
			}
			started := readJSON[struct {
				Port int `json:"port"`
			}](t, rec)
			// What the test leaves running on a shared machine stays small.
			if _, err := docker.UpdateResources(t.Context(), name, dockerx.ResourceLimits{MemoryMB: 512}); err != nil {
				t.Fatal(err)
			}
			id := liveAdoptWhenReady(t, router, name)
			db := liveStatementsDB(t, started.Port)

			var loaded, extensions string
			liveScan(t, db, `SELECT current_setting('shared_preload_libraries')`, &loaded)
			for _, want := range c.libraries {
				if !strings.Contains(loaded, want) {
					t.Errorf("the server loads %q, without %s", loaded, want)
				}
			}
			liveScan(t, db, `SELECT string_agg(extname, ',' ORDER BY extname) FROM pg_extension`, &extensions)
			if !strings.Contains(extensions, "pg_stat_statements") {
				t.Errorf("the new database has the extensions %q", extensions)
			}
			if c.engine == "timescaledb" && !strings.Contains(extensions, "timescaledb") {
				t.Errorf("TimescaleDB lost its own extension: %q", extensions)
			}
			if c.autoExplain {
				for setting, want := range map[string]string{
					"auto_explain.log_min_duration": "1s", "auto_explain.log_analyze": "on",
					"auto_explain.log_buffers": "on", "auto_explain.log_timing": "off",
				} {
					var got string
					liveScan(t, db, `SELECT current_setting('`+setting+`')`, &got)
					if got != want {
						t.Errorf("%s = %q, want %q", setting, got, want)
					}
				}
			}
			// The library is in postgresql.auto.conf, where the Parameters
			// page changes it again, and not on the command line, which would
			// outrank it.
			var source string
			liveScan(t, db, `SELECT source FROM pg_settings WHERE name = 'shared_preload_libraries'`, &source)
			if source != "configuration file" {
				t.Errorf("shared_preload_libraries comes from the %s", source)
			}

			for range 5 {
				liveExec(t, db, `SELECT count(*) FROM pg_class WHERE relname = 'jdt_statements_probe'`)
			}
			got := readJSON[liveStatements](t, do(t, router, http.MethodGet, pathf("/databases/%d/statements?sort=calls", id), ""))
			if !got.Supported || !liveCounted(got, "FROM pg_class WHERE relname = $1", 5) {
				t.Errorf("statements of a server from the %s template: %+v", c.engine, got)
			}
		})
	}
}

// A server that was started without the module is set up the way the page
// sets it up — the library stored beside the one the server already loads,
// the restart, the extension — and comes back counting, with every library
// it had.
func TestLiveStatementsSetupKeepsTheLibrariesAServerLoads(t *testing.T) {
	prefix := os.Getenv("JD_TEST_STATEMENTS_CONTAINER")
	if prefix == "" {
		t.Skip("JD_TEST_STATEMENTS_CONTAINER unset")
	}
	docker, router := liveDockerHarness(t)
	name := prefix + "-setup"
	liveRemoveOnCleanup(t, docker, name)
	port, err := portalloc.Select(55900, 1024, nil, func(port int) error { return portalloc.Available("127.0.0.1", "tcp", port) })
	if err != nil {
		t.Fatal(err)
	}
	// A server somebody configured by hand: it loads auto_explain and not
	// pg_stat_statements.
	if _, err := docker.Create(t.Context(), dockerx.ContainerSpec{
		Name: name, Image: "postgres:17-alpine", Start: true,
		Env: []dockerx.EnvVar{
			{Name: "POSTGRES_USER", Value: "jd"}, {Name: "POSTGRES_PASSWORD", Value: liveStatementsPassword},
			{Name: "POSTGRES_DB", Value: "app"},
		},
		Ports:  []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: 5432, Protocol: "tcp"}},
		Mounts: []dockerx.MountSpec{{Type: "volume", Source: name + "-data", Target: "/var/lib/postgresql/data"}},
		Limits: dockerx.ResourceLimits{MemoryMB: 512},
		Files: []dockerx.ContainerFile{{
			Path:    "/docker-entrypoint-initdb.d/by-hand.sql",
			Content: []byte("ALTER SYSTEM SET shared_preload_libraries = 'auto_explain';\n"),
		}},
	}, nil); err != nil {
		t.Fatal(err)
	}
	id := liveAdoptWhenReady(t, router, name)
	base := pathf("/databases/%d", id)
	statements := func() liveStatements {
		t.Helper()
		return readJSON[liveStatements](t, do(t, router, http.MethodGet, base+"/statements?sort=calls", ""))
	}

	got := statements()
	const preload = `ALTER SYSTEM SET "shared_preload_libraries" = 'auto_explain', 'pg_stat_statements'`
	if got.Supported || got.Enable == nil || got.Enable.Preloaded || got.Enable.Configured ||
		got.Enable.Preload != preload || got.Enable.Blocked != "" {
		t.Fatalf("a server without the module: %+v", got.Enable)
	}
	rec := do(t, router, http.MethodPost, base+"/statements/preload", `{}`)
	change := readJSON[dbx.SettingChange](t, rec)
	if rec.Code != http.StatusOK || len(change.Statements) == 0 || change.Statements[0] != preload || !change.RestartRequired {
		t.Fatalf("preload = %d %s", rec.Code, rec.Body.String())
	}
	if got = statements(); got.Enable == nil || !got.Enable.Configured || got.Enable.Preload != "" {
		t.Errorf("after the preload: %+v", got.Enable)
	}
	// Asked again, it stores nothing and still says a restart is what is left.
	if again := readJSON[dbx.SettingChange](t, do(t, router, http.MethodPost, base+"/statements/preload", `{}`)); len(again.Statements) != 0 || !again.RestartRequired {
		t.Errorf("a second preload = %+v", again)
	}

	if rec := do(t, router, http.MethodPost, base+"/power", `{"action":"restart","timeoutSeconds":30}`); rec.Code != http.StatusOK {
		t.Fatalf("restart = %d %s", rec.Code, rec.Body.String())
	}
	liveAdoptWhenReady(t, router, name)
	if got = statements(); got.Enable == nil || !got.Enable.Preloaded || got.Enable.SQL == "" {
		t.Fatalf("after the restart: %+v", got.Enable)
	}
	if rec := do(t, router, http.MethodPost, base+"/server/extensions", `{"name":"pg_stat_statements"}`); rec.Code != http.StatusOK {
		t.Fatalf("create the extension = %d %s", rec.Code, rec.Body.String())
	}

	db := liveStatementsDB(t, port)
	var loaded string
	liveScan(t, db, `SELECT current_setting('shared_preload_libraries')`, &loaded)
	if !strings.Contains(loaded, "auto_explain") || !strings.Contains(loaded, "pg_stat_statements") {
		t.Errorf("after the setup the server loads %q", loaded)
	}
	for range 5 {
		liveExec(t, db, `SELECT count(*) FROM pg_class WHERE relname = 'jdt_statements_probe'`)
	}
	if got = statements(); !got.Supported || !liveCounted(got, "FROM pg_class WHERE relname = $1", 5) {
		t.Errorf("after the setup: %+v", got)
	}
}

// liveDockerHarness is the connection routes as an administrator, with the
// machine's own Docker and nothing else of the machine faked.
func liveDockerHarness(t *testing.T) (*dockerx.Client, http.Handler) {
	t.Helper()
	h := &connHarness{t: t, s: testServer(t)}
	h.s.Cfg.BackupLocalDir = t.TempDir()
	docker := dockerx.New(envOr("JD_TEST_DOCKER_HOST", "unix:///var/run/docker.sock"))
	t.Cleanup(func() { _ = docker.Close() })
	h.s.modules.docker = docker
	return docker, h.as("admin")
}

func liveRemoveOnCleanup(t *testing.T, docker *dockerx.Client, name string) {
	t.Helper()
	remove := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = docker.RemoveContainer(ctx, name, true, true)
		_ = docker.RemoveVolume(ctx, name+"-data", true)
	}
	remove()
	t.Cleanup(remove)
}

// liveAdoptWhenReady saves the connection to the container the way the page
// does, retrying until the engine answers, and returns its id.
func liveAdoptWhenReady(t *testing.T, router http.Handler, name string) int64 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		rec := do(t, router, http.MethodPost, "/databases/adopt", `{"container":"`+name+`"}`)
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated {
			id := readJSON[summaryView](t, rec).ID
			if readJSON[struct {
				OK bool `json:"ok"`
			}](t, do(t, router, http.MethodGet, pathf("/databases/%d/ping", id), "")).OK {
				return id
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not answer in time: %d %s", name, rec.Code, rec.Body.String())
		}
		time.Sleep(2 * time.Second)
	}
}

// liveStatementsDB opens the test's own server on the port it answers on.
func liveStatementsDB(t *testing.T, port int) *sql.DB {
	t.Helper()
	return liveOpen(t, dbx.DriverPostgres,
		fmt.Sprintf("postgres://jd:%s@127.0.0.1:%d/app?sslmode=disable", liveStatementsPassword, port))
}

func liveScan(t *testing.T, db *sql.DB, query string, into any) {
	t.Helper()
	if err := db.QueryRowContext(t.Context(), query).Scan(into); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// liveCounted reports a statement of that shape counted at least calls times.
func liveCounted(got liveStatements, shape string, calls int64) bool {
	for _, s := range got.Statements {
		if strings.Contains(s.Query, shape) && s.Calls >= calls {
			return true
		}
	}
	return false
}
