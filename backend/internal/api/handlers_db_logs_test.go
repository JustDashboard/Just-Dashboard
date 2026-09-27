package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/logsx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// A database's page reads its own server's log. These tests build the
// machine the resolution reads — the socket table, the process's cgroup, its
// open files under /proc, the Docker daemon — so every step of "which log is
// this server's" is checked against a known answer rather than whatever the
// host running the tests happens to have.

// dbLogRouter mounts the database routes for one connection, signed in with
// the given role.
func dbLogRouter(t *testing.T, role auth.Role, driver dbx.Driver, dsn string) (*Server, http.Handler) {
	t.Helper()
	s := testServer(t)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "reader"}, Role: role, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountDatabaseRoutes(r)
	if driver == dbx.DriverSQLite {
		dsn = filepath.Join(s.Cfg.FileRoots[0], dsn)
	}
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,0)`,
		"shop", string(driver), sealed); err != nil {
		t.Fatal(err)
	}
	return s, r
}

// dbLogEngine is a Docker daemon running these containers, each published as
// its entry says.
func dbLogEngine(t *testing.T, s *Server, containers ...map[string]any) {
	t.Helper()
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			_ = json.NewEncoder(w).Encode(containers)
			return
		}
		for _, c := range containers {
			id := c["Id"].(string)
			name := strings.TrimPrefix(c["Names"].([]string)[0], "/")
			if strings.HasSuffix(r.URL.Path, "/containers/"+id+"/json") || strings.HasSuffix(r.URL.Path, "/containers/"+name+"/json") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"Id": id, "Name": "/" + name, "Image": c["ImageID"],
					"Config": map[string]any{"Image": c["ConfigImage"]},
					"State":  map[string]any{"Status": "running", "Running": true},
				})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "no such container"})
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
}

// useHostProbe points the resolution at a made-up machine for one test.
func useHostProbe(t *testing.T, probe dbHostProbe) {
	t.Helper()
	was := hostLogProbe
	hostLogProbe = probe
	t.Cleanup(func() { hostLogProbe = was })
}

// holdOpen records that a process has a file open, as /proc shows it: the
// descriptor's link and its octal flags.
func holdOpen(t *testing.T, proc string, pid, fd int, target, flags string) {
	t.Helper()
	dir := filepath.Join(proc, strconv.Itoa(pid))
	for _, sub := range []string{"fd", "fdinfo"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, filepath.Join(dir, "fd", strconv.Itoa(fd))); err != nil {
		t.Fatal(err)
	}
	info := "pos:\t0\nflags:\t" + flags + "\nmnt_id:\t31\n"
	if err := os.WriteFile(filepath.Join(dir, "fdinfo", strconv.Itoa(fd)), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// freePort is a loopback port nothing listens on, so a pool dialled at it is
// refused at once rather than knocking on a real server.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

const (
	appendWrite = "0102001" // O_WRONLY|O_APPEND|O_LARGEFILE: how a server holds its log
	readWrite   = "0100002" // O_RDWR: a data file
)

// hostPostgres is this host's own case: a Debian Postgres whose unit writes
// nothing to the journal, whose log file logrotate emptied overnight, and one
// of whose children writes a second log where the roots do not reach.
type hostPostgres struct {
	port             int
	live, archive    string
	outside, dataDir string
}

func newHostPostgres(t *testing.T, s *Server, archive string) hostPostgres {
	t.Helper()
	root := s.Cfg.LogRoots[0]
	h := hostPostgres{
		port:    freePort(t),
		live:    filepath.Join(root, "postgresql", "postgresql-17-main.log"),
		archive: filepath.Join(root, "postgresql", "postgresql-17-main.log.1"),
		outside: filepath.Join(t.TempDir(), "17", "main", "log", "postgresql-Mon.log"),
		dataDir: filepath.Join(t.TempDir(), "17", "main"),
	}
	writeFile(t, h.live, "")
	writeFile(t, h.archive, archive)
	writeFile(t, h.outside, "")
	writeFile(t, filepath.Join(h.dataDir, "pg_wal", "000000010000000000000001"), "")
	writeFile(t, filepath.Join(h.dataDir, "binlog.000001"), "")

	proc := filepath.Join(t.TempDir(), "proc")
	holdOpen(t, proc, 4242, 0, "/dev/null", "0100000")
	holdOpen(t, proc, 4242, 1, h.live, appendWrite)
	holdOpen(t, proc, 4242, 2, h.live, appendWrite)
	holdOpen(t, proc, 4242, 3, "pipe:[388905203]", "02")
	holdOpen(t, proc, 4242, 10, filepath.Join(h.dataDir, "pg_wal", "000000010000000000000001"), readWrite)
	holdOpen(t, proc, 4242, 11, filepath.Join(h.dataDir, "binlog.000001"), appendWrite)
	writeFile(t, filepath.Join(proc, "4242", "task", "4242", "children"), "4243 ")
	holdOpen(t, proc, 4243, 2, h.outside, appendWrite)

	useHostProbe(t, dbHostProbe{
		listeners: func(context.Context) ([]proxysvc.Listener, error) {
			return []proxysvc.Listener{
				{Protocol: "tcp", Address: "127.0.0.1", Port: 22, PID: 1, Process: "sshd"},
				{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(h.port), PID: 4242, Process: "postgres"},
			}, nil
		},
		managerOf: func(pid int32, _ string) (string, string) {
			if pid == 4242 {
				return "systemd", "postgresql@17-main.service"
			}
			return "unmanaged", ""
		},
		proc:   proc,
		logDir: root,
	})
	dbLogEngine(t, s)
	return h
}

func getJSON(t *testing.T, router http.Handler, path string, into any) int {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if into != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("%s: %v in %s", path, err, rec.Body.String())
		}
	}
	return rec.Code
}

// A server on the machine is followed from its port to the file its process
// writes, and that file is the primary log even while logrotate has left it
// empty — its archive is where the history is. The data files it holds open
// are not logs, and the log its child writes outside the roots is named as
// refused rather than dropped.
func TestDBLogSourcesFollowAHostServerToItsFiles(t *testing.T) {
	s, router := dbLogRouter(t, auth.RoleAdmin, dbx.DriverPostgres, "")
	h := newHostPostgres(t, s, "2026-09-25 12:31:56.094 UTC [4242] LOG:  database system is ready to accept connections\n")
	sealed, _ := s.Sealer.Seal("postgres://app:pw@127.0.0.1:" + strconv.Itoa(h.port) + "/shop?sslmode=disable")
	if _, err := s.Store.DB.Exec(`UPDATE db_connections SET dsn_enc = ?`, sealed); err != nil {
		t.Fatal(err)
	}

	var got dbLogSources
	if code := getJSON(t, router, "/databases/1/logs/sources", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.Reason != "" || len(got.Sources) == 0 {
		t.Fatalf("no sources: %+v", got)
	}
	file := got.Sources[0]
	if file.ID != "file:"+h.live || !file.Primary || file.Lens != "postgres" || file.Label != "postgresql-17-main.log" {
		t.Fatalf("primary source = %+v", file)
	}
	if file.Size != 0 || file.Archives != 1 || !strings.Contains(file.Detail, "rotated") {
		t.Errorf("an emptied file should say its history is in its archive: %+v", file)
	}
	for _, src := range got.Sources[1:] {
		if src.Primary || src.Kind != logsx.KindJournal {
			t.Errorf("after the file only the unit's journal is offered, got %+v", src)
		}
	}
	if s.modules.systemd.Available() {
		if len(got.Sources) != 2 || got.Sources[1].ID != "journal:postgresql@17-main.service" || got.Sources[1].Lens != "postgres" {
			t.Errorf("the unit's journal should be the second source: %+v", got.Sources)
		}
	}
	if len(got.Refused) != 1 || got.Refused[0].Path != h.outside || !strings.Contains(got.Refused[0].Reason, "log roots") {
		t.Errorf("the log outside the roots should be refused by name: %+v", got.Refused)
	}
}

// A container published to the host answers through docker-proxy, whose own
// unit is Docker's: the page reads the container instead, found by the port
// it publishes even when its image is listed by a bare id and detection by
// image cannot say it is a database.
func TestDBLogSourcesFindAContainerBehindDockerProxy(t *testing.T) {
	port := freePort(t)
	s, router := dbLogRouter(t, auth.RoleAdmin, dbx.DriverPostgres,
		"postgres://app:pw@127.0.0.1:"+strconv.Itoa(port)+"/lampino?sslmode=disable")
	dbLogEngine(t, s, map[string]any{
		"Id": "2a6ae53ec52d", "Names": []string{"/lampino-db"}, "Image": "3c5c8892d184", "ImageID": "sha256:3c5c8892d184",
		"ConfigImage": "postgres:16", "State": "running",
		"Ports": []map[string]any{{"IP": "127.0.0.1", "PrivatePort": 5432, "PublicPort": port, "Type": "tcp"}},
	})
	useHostProbe(t, dbHostProbe{
		listeners: func(context.Context) ([]proxysvc.Listener, error) {
			return []proxysvc.Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port), PID: 900, Process: "docker-proxy"}}, nil
		},
		managerOf: func(int32, string) (string, string) {
			t.Error("docker-proxy's own unit must not be followed")
			return "systemd", "docker.service"
		},
		proc:   t.TempDir(),
		logDir: s.Cfg.LogRoots[0],
	})

	var got dbLogSources
	getJSON(t, router, "/databases/1/logs/sources", &got)
	if len(got.Sources) != 1 {
		t.Fatalf("sources = %+v", got)
	}
	src := got.Sources[0]
	if src.ID != "docker:lampino-db" || src.Kind != logsx.KindDocker || src.Lens != "postgres" || !src.Primary {
		t.Errorf("container source = %+v", src)
	}
}

// Every way of having no log says which it is.
func TestDBLogSourcesSayWhyThereAreNone(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		_, router := dbLogRouter(t, auth.RoleAdmin, dbx.DriverSQLite, "shop.db")
		var got dbLogSources
		getJSON(t, router, "/databases/1/logs/sources", &got)
		if len(got.Sources) != 0 || !strings.Contains(got.Reason, "SQLite") {
			t.Errorf("sqlite = %+v", got)
		}
	})
	t.Run("remote", func(t *testing.T) {
		_, router := dbLogRouter(t, auth.RoleAdmin, dbx.DriverPostgres, "postgres://app:pw@db.example.com:5432/shop")
		var got dbLogSources
		getJSON(t, router, "/databases/1/logs/sources", &got)
		if len(got.Sources) != 0 || !strings.Contains(got.Reason, "another machine (db.example.com)") {
			t.Errorf("remote = %+v", got)
		}
	})
	t.Run("nothing listening", func(t *testing.T) {
		port := freePort(t)
		s, router := dbLogRouter(t, auth.RoleAdmin, dbx.DriverRedis, "redis://127.0.0.1:"+strconv.Itoa(port))
		dbLogEngine(t, s)
		useHostProbe(t, dbHostProbe{
			listeners: func(context.Context) ([]proxysvc.Listener, error) { return nil, nil },
			managerOf: func(int32, string) (string, string) { return "", "" },
			proc:      t.TempDir(), logDir: s.Cfg.LogRoots[0],
		})
		var got dbLogSources
		getJSON(t, router, "/databases/1/logs/sources", &got)
		if len(got.Sources) != 0 || !strings.Contains(got.Reason, "Nothing on this machine is listening on port "+strconv.Itoa(port)) {
			t.Errorf("stopped = %+v", got)
		}
	})
	t.Run("a process with no log", func(t *testing.T) {
		port := freePort(t)
		s, router := dbLogRouter(t, auth.RoleAdmin, dbx.DriverRedis, "redis://127.0.0.1:"+strconv.Itoa(port))
		dbLogEngine(t, s)
		useHostProbe(t, dbHostProbe{
			listeners: func(context.Context) ([]proxysvc.Listener, error) {
				return []proxysvc.Listener{{Protocol: "tcp", Port: uint32(port), PID: 77, Process: "redis-server"}}, nil
			},
			managerOf: func(int32, string) (string, string) { return "session", "session-3.scope" },
			proc:      t.TempDir(), logDir: s.Cfg.LogRoots[0],
		})
		var got dbLogSources
		getJSON(t, router, "/databases/1/logs/sources", &got)
		if len(got.Sources) != 0 || !strings.Contains(got.Reason, "redis-server (pid 77)") {
			t.Errorf("no log = %+v", got)
		}
	})
}

// Redis opens its logfile for each line and holds nothing between, so its
// file is found where its package writes it.
func TestDBLogSourcesFindAConventionalLog(t *testing.T) {
	port := freePort(t)
	s, router := dbLogRouter(t, auth.RoleAdmin, dbx.DriverRedis, "redis://127.0.0.1:"+strconv.Itoa(port))
	dbLogEngine(t, s)
	logFile := filepath.Join(s.Cfg.LogRoots[0], "redis", "redis-server.log")
	writeFile(t, logFile, "1:M 01 Jun 2024 13:24:10.034 * Ready to accept connections tcp\n")
	useHostProbe(t, dbHostProbe{
		listeners: func(context.Context) ([]proxysvc.Listener, error) {
			return []proxysvc.Listener{{Protocol: "tcp", Port: uint32(port), PID: 77, Process: "redis-server"}}, nil
		},
		managerOf: func(int32, string) (string, string) { return "unmanaged", "" },
		proc:      t.TempDir(), logDir: s.Cfg.LogRoots[0],
	})
	var got dbLogSources
	getJSON(t, router, "/databases/1/logs/sources", &got)
	if len(got.Sources) != 1 || got.Sources[0].ID != "file:"+logFile || got.Sources[0].Lens != "redis" || !got.Sources[0].Primary {
		t.Errorf("sources = %+v", got)
	}
}

// Postgres's slow statements are the log's, read through its lens: from the
// rotated file when the live one was emptied, newest first, a statement that
// spans lines joined back into one, and log_duration's bare "duration:" lines
// left out, because those are every statement rather than the slow ones.
func TestDBQueryLogReadsPostgresSlowStatementsFromTheLog(t *testing.T) {
	s, router := dbLogRouter(t, auth.RoleAdmin, dbx.DriverPostgres, "")
	h := newHostPostgres(t, s, strings.Join([]string{
		"2026-09-25 12:31:56.094 UTC [4242] LOG:  database system is ready to accept connections",
		"2026-09-25 13:00:01.500 UTC [4001] app@shop LOG:  duration: 1523.412 ms  statement: SELECT * FROM orders WHERE customer_id = 42",
		"2026-09-25 13:05:00.000 UTC [4002] app@shop LOG:  duration: 312.000 ms  statement: SELECT o.id",
		"\tFROM orders o",
		"\tWHERE o.total > 100",
		"2026-09-25 13:06:00.000 UTC [4003] app@shop LOG:  duration: 0.412 ms",
		"",
	}, "\n"))
	sealed, _ := s.Sealer.Seal("postgres://app:pw@127.0.0.1:" + strconv.Itoa(h.port) + "/shop?sslmode=disable")
	if _, err := s.Store.DB.Exec(`UPDATE db_connections SET dsn_enc = ?`, sealed); err != nil {
		t.Fatal(err)
	}

	window := "since=2026-09-25T00:00:00Z&until=2026-09-26T00:00:00Z"
	var got dbx.QueryLog
	if code := getJSON(t, router, "/databases/1/querylog?"+window, &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if !got.Supported || got.Source != dbx.QuerySourceLog || got.Reason != "" {
		t.Fatalf("query log = %+v", got)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %+v", got.Entries)
	}
	newest, oldest := got.Entries[0], got.Entries[1]
	if newest.Query != "SELECT o.id\nFROM orders o\nWHERE o.total > 100" || newest.DurationMs != 312 {
		t.Errorf("the statement spanning lines = %+v", newest)
	}
	if !newest.At.Equal(time.Date(2026, 9, 25, 13, 5, 0, 0, time.UTC)) || newest.User != "app" || newest.DB != "shop" {
		t.Errorf("who and when = %+v", newest)
	}
	if oldest.DurationMs != 1523.412 || oldest.FP != logsx.Fingerprint("SELECT * FROM orders WHERE customer_id = 42") {
		t.Errorf("the slowest = %+v", oldest)
	}

	getJSON(t, router, "/databases/1/querylog?"+window+"&minMs=1000", &got)
	if len(got.Entries) != 1 || got.Entries[0].DurationMs != 1523.412 {
		t.Errorf("minMs=1000 kept %+v", got.Entries)
	}
}

// Remote servers have no log here, and MongoDB's is still read over the
// connection. Its lines are the file's JSON, read through the same lens.
func TestMongoSlowLinesReadGetLogThroughTheLens(t *testing.T) {
	lines := []string{
		`{"t":{"$date":"2024-06-01T13:24:09.000+00:00"},"s":"I","c":"NETWORK","id":22943,"ctx":"listener","msg":"Connection accepted","attr":{"remote":"192.168.1.100:12345","connectionId":3,"connectionCount":1}}`,
		`{"t":{"$date":"2024-06-01T13:24:10.034+00:00"},"s":"I","c":"COMMAND","id":51803,"ctx":"conn3","msg":"Slow query","attr":{"type":"command","ns":"db.coll","appName":"MongoDB Shell","command":{"find":"coll","filter":{"b":-1},"sort":{"splitPoint":1},"$db":"db"},"planSummary":"COLLSCAN","planningTimeMicros":87,"keysExamined":0,"docsExamined":20889,"hasSortStage":true,"nBatches":1,"nreturned":0,"queryHash":"ABC123","planCacheShapeHash":"DEF456","planCacheKey":"GHI789","queues":{"execution":{"totalTimeQueuedMicros":50}},"locks":{"Global":{"acquireCount":{"r":1}}},"remote":"192.168.1.100:12345","protocol":"op_msg","durationMillis":1234,"workingMillis":1200,"reslen":5000}}`,
	}
	got := mongoSlowLines(lines)
	if len(got) != 1 {
		t.Fatalf("entries = %+v", got)
	}
	e := got[0]
	if e.DurationMs != 1234 || e.DB != "db" || e.Client != "192.168.1.100" || e.FP == "" ||
		!strings.Contains(e.Query, `"find":"coll"`) || e.Examined == nil || *e.Examined != 20889 || e.Rows == nil || *e.Rows != 0 {
		t.Errorf("slow operation = %+v", e)
	}
	if !e.At.Equal(time.Date(2024, 6, 1, 13, 24, 10, 34_000_000, time.UTC)) {
		t.Errorf("at = %s", e.At)
	}
}

// Reading a database's log is reading its server's output, which any role
// may: the page used to refuse everyone but an administrator because it went
// through the access route, which lists the firewall.
func TestDBLogRoutesAreReadSurface(t *testing.T) {
	_, router := dbLogRouter(t, auth.RoleReadOnly, dbx.DriverSQLite, "shop.db")
	var sources dbLogSources
	if code := getJSON(t, router, "/databases/1/logs/sources", &sources); code != http.StatusOK {
		t.Fatalf("a read-only account got %d for the log sources", code)
	}
	var queries dbx.QueryLog
	if code := getJSON(t, router, "/databases/1/querylog", &queries); code != http.StatusOK {
		t.Fatalf("a read-only account got %d for the query log", code)
	}
	if queries.Supported || !strings.Contains(queries.Reason, "SQLite") || queries.Entries == nil {
		t.Errorf("sqlite query log = %+v", queries)
	}
}

func TestQueryLogWindowRefusesWhatItCannotRead(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	w, err := queryLogWindow(map[string][]string{}, now)
	if err != nil || !w.Since.Equal(now.Add(-24*time.Hour)) || w.Limit != 200 || w.MinMs != 0 {
		t.Errorf("the default window = %+v, %v", w, err)
	}
	w, err = queryLogWindow(map[string][]string{"limit": {"9000"}, "minMs": {"250"}}, now)
	if err != nil || w.Limit != maxQueryLog || w.MinMs != 250 {
		t.Errorf("a limit past the cap = %+v, %v", w, err)
	}
	for _, bad := range []map[string][]string{
		{"since": {"yesterday"}},
		{"until": {"2026-09-27"}},
		{"since": {"2026-09-27T12:00:00Z"}, "until": {"2026-09-27T11:00:00Z"}},
		{"limit": {"0"}},
		{"minMs": {"-1"}},
		{"minMs": {"NaN"}},
	} {
		if _, err := queryLogWindow(bad, now); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

func TestConventionLogPathsNameTheDebianCluster(t *testing.T) {
	got := conventionLogPaths("/var/log", dbx.DriverPostgres, "postgresql@17-main.service")
	if len(got) != 1 || got[0] != "/var/log/postgresql/postgresql-17-main.log" {
		t.Errorf("cluster path = %v", got)
	}
	if got := conventionLogPaths("/var/log", dbx.DriverPostgres, "postgresql.service"); len(got) != 0 {
		t.Errorf("a unit that names no cluster names no file: %v", got)
	}
	if got := conventionLogPaths("/var/log", dbx.DriverPostgres, "postgresql@../../etc.service"); len(got) != 0 {
		t.Errorf("a unit name that climbs is not a path: %v", got)
	}
}

// The resolution against the machine it runs on, as root, since a process's
// descriptors are readable only by their owner or root:
//
//	JD_TEST_HOST_PG_PORT=5438 sudo -E go test ./internal/api -run TestLiveHostDBLogSources -v
//
// It only reads: the socket table, the server's cgroup, its open descriptors
// and the files they name. Skipped where there is no server or no root.
func TestLiveHostDBLogSources(t *testing.T) {
	port, _ := strconv.Atoi(os.Getenv("JD_TEST_HOST_PG_PORT"))
	if port == 0 {
		t.Skip("JD_TEST_HOST_PG_PORT not set")
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root to read another account's open files")
	}
	s := testServer(t)
	s.modules.logs = logsx.New([]string{"/var/log"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	got := s.dbHostLogs(ctx, dbx.DriverPostgres, port, "postgres", hostLogProbe)
	t.Logf("%+v", got)
	if len(got.Sources) == 0 {
		t.Fatalf("no log found for the server on %d: %s", port, got.Reason)
	}
	if first := got.Sources[0]; !first.Primary || !strings.HasPrefix(first.ID, "file:/var/log/postgresql/") {
		t.Errorf("the server's own file should be the primary log: %+v", first)
	}
}
