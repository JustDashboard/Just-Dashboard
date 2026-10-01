package api

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/go-chi/chi/v5"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"go.mongodb.org/mongo-driver/bson"
)

// The inventory's routes, driven against a machine written out by hand.
//
// Nothing here touches the machine the test runs on. Docker is a server the
// test starts, the host's sockets and units are slices, and — the part that
// matters most — signing in to a database is a function the test supplies.
// Connecting is the one thing this file does that reaches out, and a test
// that dialled whatever happened to be listening on 5432 would be sending
// failed logins to somebody's real database.

type fakePort struct {
	private, public int
	ip              string
}

type fakeContainer struct {
	id, name, image, state string
	env                    []string
	entrypoint, cmd        []string
	labels                 map[string]string
	ports                  []fakePort
	ip, networkMode        string
	mounts                 []map[string]any
	// changed is the files in the container's own writable layer.
	changed map[string][]byte
}

// fakeMachine is a server with Docker, a socket table and systemd, none real.
type fakeMachine struct {
	mu         sync.Mutex
	containers []fakeContainer
	dockerDown bool
	listeners  []proxysvc.Listener
	sockets    []proxysvc.UnixListener
	units      []procs.Unit
	// unitFiles is every installed unit file and its state, loaded or not.
	unitFiles map[string]string
	// refuse maps a fragment of a DSN to the error the engine answers it with.
	refuse map[string]string
	dialed []string
}

func (m *fakeMachine) dial(s *Server) func(context.Context, dbx.Driver, string) error {
	return func(ctx context.Context, driver dbx.Driver, dsn string) error {
		if driver == dbx.DriverSQLite {
			// A file in the test's own temporary directory: the real thing.
			return s.probeConnection(ctx, driver, dsn)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.dialed = append(m.dialed, dsn)
		for fragment, message := range m.refuse {
			if strings.Contains(dsn, fragment) {
				return errors.New(message)
			}
		}
		return nil
	}
}

func (m *fakeMachine) dials() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.dialed...)
}

func (m *fakeMachine) engine(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		down, containers := m.dockerDown, m.containers
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("API-Version", "1.47")
			return
		}
		if down {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "the daemon is not answering"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			list := []map[string]any{}
			for _, c := range containers {
				ports := []map[string]any{}
				if c.state == "running" {
					for _, p := range c.ports {
						port := map[string]any{"PrivatePort": p.private, "Type": "tcp"}
						if p.public > 0 {
							port["PublicPort"], port["IP"] = p.public, p.ip
						}
						ports = append(ports, port)
					}
				}
				list = append(list, map[string]any{
					"Id": c.id, "Names": []string{"/" + c.name}, "Image": c.image, "State": c.state,
					"Status": c.state, "Command": strings.Join(append(append([]string{}, c.entrypoint...), c.cmd...), " "),
					"Ports": ports, "Labels": c.labels, "Mounts": c.mounts,
				})
			}
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		for _, c := range containers {
			switch {
			case strings.HasSuffix(r.URL.Path, "/containers/"+c.id+"/json"):
				networks, bindings := map[string]any{}, map[string]any{}
				if c.ip != "" && c.state == "running" {
					networks["bridge"] = map[string]any{"IPAddress": c.ip}
				}
				if c.state == "running" {
					for _, p := range c.ports {
						key := fmt.Sprintf("%d/tcp", p.private)
						if p.public > 0 {
							bindings[key] = []map[string]any{{"HostIp": p.ip, "HostPort": fmt.Sprint(p.public)}}
						} else {
							bindings[key] = nil
						}
					}
				}
				path, args := "", append(append([]string{}, c.entrypoint...), c.cmd...)
				if len(args) > 0 {
					path, args = args[0], args[1:]
				}
				mode := c.networkMode
				if mode == "" {
					mode = "bridge"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"Id": c.id, "Name": "/" + c.name, "Path": path, "Args": args,
					"Config": map[string]any{
						"Image": c.image, "Env": c.env, "Labels": c.labels, "Entrypoint": c.entrypoint, "Cmd": c.cmd,
					},
					"State":           map[string]any{"Status": c.state, "Running": c.state == "running", "StartedAt": "2026-01-01T00:00:00Z"},
					"HostConfig":      map[string]any{"NetworkMode": mode},
					"NetworkSettings": map[string]any{"Networks": networks, "Ports": bindings},
				})
				return
			case strings.HasSuffix(r.URL.Path, "/containers/"+c.id+"/changes"):
				changes := []map[string]any{}
				for path := range c.changed {
					changes = append(changes, map[string]any{"Path": path, "Kind": 1})
				}
				_ = json.NewEncoder(w).Encode(changes)
				return
			case strings.HasSuffix(r.URL.Path, "/containers/"+c.id+"/archive"):
				content, ok := c.changed[r.URL.Query().Get("path")]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					_ = json.NewEncoder(w).Encode(map[string]string{"message": "no such file"})
					return
				}
				stat, _ := json.Marshal(map[string]any{"name": "f", "size": len(content), "mode": 0o644})
				w.Header().Set("X-Docker-Container-Path-Stat", base64.StdEncoding.EncodeToString(stat))
				w.Header().Set("Content-Type", "application/x-tar")
				tw := tar.NewWriter(w)
				_ = tw.WriteHeader(&tar.Header{Name: "f", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg, ModTime: time.Unix(1_700_000_000, 0)})
				_, _ = tw.Write(content)
				_ = tw.Close()
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "no such container"})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// inventoryRouter is a dashboard whose whole view of the machine is m, signed
// in as role.
func inventoryRouter(t *testing.T, role auth.Role, m *fakeMachine) (*Server, http.Handler) {
	t.Helper()
	s := testServer(t)
	t.Cleanup(s.Shutdown)
	s.modules.docker = dockerx.New(m.engine(t).URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
	s.dbInventory.host = &dbInventoryHost{
		listeners: func(context.Context) ([]proxysvc.Listener, error) { return m.listeners, nil },
		sockets: func(_ context.Context, want func(string) bool) ([]proxysvc.UnixListener, error) {
			out := []proxysvc.UnixListener{}
			for _, u := range m.sockets {
				if want(u.Path) {
					out = append(out, u)
				}
			}
			return out, nil
		},
		systemd: func() bool { return true },
		units: func(context.Context) ([]procs.Unit, map[string]string, error) {
			// Whatever is loaded has a unit file; the test adds the ones that
			// are installed and not loaded.
			files := map[string]string{}
			for _, u := range m.units {
				files[u.Name] = "enabled"
			}
			for name, state := range m.unitFiles {
				files[name] = state
			}
			return m.units, files, nil
		},
		etc:   filepath.Join(s.Cfg.FileRoots[0], "etc"),
		roots: []string{filepath.Join(s.Cfg.FileRoots[0], "srv")},
	}
	s.dbInventory.dial = m.dial(s)

	r := chi.NewRouter()
	r.Use(httpx.AuditMutations(s.Audit))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: "tester"},
				Role: role, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountDatabaseRoutes(r)
	return s, r
}

func decodeInventory(t *testing.T, rec *httptest.ResponseRecorder) inventoryResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("inventory answered %d: %s", rec.Code, rec.Body.String())
	}
	var out inventoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func inventoryInstance(t *testing.T, inv inventoryResponse, key string) dbx.Instance {
	t.Helper()
	keys := []string{}
	for _, inst := range inv.Instances {
		if inst.Key == key {
			return inst
		}
		keys = append(keys, inst.Key)
	}
	t.Fatalf("no instance %q in %v", key, keys)
	return dbx.Instance{}
}

func inventoryHas(inv inventoryResponse, key string) bool {
	for _, inst := range inv.Instances {
		if inst.Key == key {
			return true
		}
	}
	return false
}

func inventoryErrorCode(rec *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

func connectionRows(t *testing.T, s *Server) map[string]string {
	t.Helper()
	rows, err := s.Store.DB.Query(`SELECT name, origin FROM db_connections`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, origin string
		if err := rows.Scan(&name, &origin); err != nil {
			t.Fatal(err)
		}
		out[name] = origin
	}
	return out
}

func inventorySQLiteFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

const secretPassword = "s3cr3t-never-on-the-wire"

// ordinaryMachine is the mix a real server has: a container known by its
// image, one known only by what its image sets, a stopped one, an application
// keeping a database inside itself, an engine with no driver, and a
// PostgreSQL installed on the host.
func ordinaryMachine() *fakeMachine {
	return &fakeMachine{
		containers: []fakeContainer{
			{
				id: "aaaaaaaaaaaa0001", name: "shop-db", image: "postgres:16-alpine", state: "running",
				env:        []string{"POSTGRES_USER=shop", "POSTGRES_PASSWORD=" + secretPassword, "POSTGRES_DB=shop", "PG_MAJOR=16", "PG_VERSION=16.4"},
				entrypoint: []string{"docker-entrypoint.sh"}, cmd: []string{"postgres"},
				ports: []fakePort{{5432, 15432, "127.0.0.1"}}, ip: "172.18.0.2",
			},
			{
				// Retagged: the name says nothing, the environment says Postgres.
				id: "aaaaaaaaaaaa0002", name: "fixture", image: "jdcc/fixture-pg:16", state: "running",
				env:        []string{"POSTGRES_PASSWORD=" + secretPassword, "PG_MAJOR=16", "PG_VERSION=16.4", "PGDATA=/var/lib/postgresql/data"},
				entrypoint: []string{"docker-entrypoint.sh"}, cmd: []string{"-c", "shared_preload_libraries=pg_stat_statements"},
				ports: []fakePort{{5432, 25432, "127.0.0.1"}}, ip: "172.18.0.3",
			},
			{
				id: "aaaaaaaaaaaa0003", name: "old-cache", image: "redis:7", state: "exited",
				entrypoint: []string{"docker-entrypoint.sh"}, cmd: []string{"redis-server"},
			},
			{
				id: "aaaaaaaaaaaa0004", name: "notes-app", image: "acme/notes:1", state: "exited",
				entrypoint: []string{"/app/server"},
				changed: map[string][]byte{
					"/data/notes.sqlite3": append([]byte("SQLite format 3\x00"), make([]byte, 64)...),
					"/data/cache.db":      []byte("this is not a database at all"),
					"/app/readme.txt":     []byte("hello"),
				},
			},
			{
				id: "aaaaaaaaaaaa0005", name: "cache", image: "memcached:1", state: "running",
				entrypoint: []string{"docker-entrypoint.sh"}, cmd: []string{"memcached"},
				ports: []fakePort{{11211, 0, ""}}, ip: "172.18.0.5",
			},
		},
		listeners: []proxysvc.Listener{
			{Protocol: "tcp", Address: "127.0.0.1", Port: 5438, PID: 900, Process: "postgres", User: "postgres",
				Cmdline: "/usr/lib/postgresql/17/bin/postgres -D /var/lib/postgresql/17/main", Manager: "systemd", ManagerName: "postgresql@17-main.service"},
			{Protocol: "tcp", Address: "0.0.0.0", Port: 15432, PID: 50, Process: "docker-proxy"},
			{Protocol: "tcp", Address: "127.0.0.1", Port: 9187, PID: 51, Process: "postgres_exporter"},
		},
		sockets: []proxysvc.UnixListener{
			{Path: "/var/run/postgresql/.s.PGSQL.5438", PID: 900, Process: "postgres", Manager: "systemd", ManagerName: "postgresql@17-main.service"},
			{Path: "/run/user/1000/bus", PID: 77, Process: "dbus-daemon"},
		},
		units: []procs.Unit{
			{Name: "postgresql.service", LoadState: "loaded", ActiveState: "active", SubState: "exited"},
			{Name: "postgresql@17-main.service", LoadState: "loaded", ActiveState: "active", SubState: "running", Enabled: true},
			{Name: "redis-server.service", LoadState: "loaded", ActiveState: "inactive", SubState: "dead"},
		},
	}
}

func TestInventoryListsEverythingOnTheMachine(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	root := s.Cfg.FileRoots[0]
	inventorySQLiteFile(t, filepath.Join(root, "srv", "blog", "content", "ghost.db"))
	if err := os.WriteFile(filepath.Join(root, "srv", "blog", "not-a-database.db"), []byte("plain text"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The dashboard's own store, where the configuration says it is.
	inventorySQLiteFile(t, filepath.Join(s.Cfg.DataDir, store.DatabaseFile))
	// A compose file nobody has brought up.
	s.Cfg.ComposeRoots = []string{filepath.Join(root, "stacks")}
	compose := "services:\n  db:\n    image: mariadb:${TAG:-11}\n  web:\n    image: nginx:alpine\n"
	if err := os.MkdirAll(filepath.Join(root, "stacks", "wiki"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stacks", "wiki", "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := do(t, r, http.MethodPost, "/databases/inventory/scan", "{}")
	inv := decodeInventory(t, rec)
	if inv.Detail != "full" {
		t.Errorf("detail = %q", inv.Detail)
	}
	if strings.Contains(rec.Body.String(), secretPassword) {
		t.Fatal("a container's password reached the inventory")
	}

	db := inventoryInstance(t, inv, "docker:shop-db")
	if db.Engine != "postgres" || db.Confidence != dbx.ConfidenceImage || db.Credentials != dbx.CredentialsEnv ||
		!db.Connectable || db.User != "shop" || db.Database != "shop" || db.Version != "16.4" {
		t.Errorf("shop-db = %+v", db)
	}
	if len(db.Endpoints) != 2 || !db.Endpoints[0].Primary || db.Endpoints[0].Port != 15432 || db.Endpoints[1].Kind != "container" {
		t.Errorf("shop-db endpoints = %+v", db.Endpoints)
	}
	if fixture := inventoryInstance(t, inv, "docker:fixture"); fixture.Engine != "postgres" || fixture.Confidence != dbx.ConfidenceFingerprint {
		t.Errorf("the retagged image was not recognised by its environment: %+v", fixture)
	}
	if old := inventoryInstance(t, inv, "docker:old-cache"); old.State != "exited" || old.Connectable || old.Engine != "redis" {
		t.Errorf("the stopped container = %+v", old)
	}
	if cache := inventoryInstance(t, inv, "docker:cache"); cache.Driver != "" || cache.Engine != "memcached" || cache.Connectable {
		t.Errorf("an engine with no driver = %+v", cache)
	}
	embedded := inventoryInstance(t, inv, "embedded:notes-app:/data/notes.sqlite3")
	if embedded.Kind != dbx.KindEmbedded || embedded.Connectable || embedded.State != "exited" || embedded.Container.Image != "acme/notes:1" {
		t.Errorf("the database inside the application container = %+v", embedded)
	}
	if inventoryHas(inv, "embedded:notes-app:/data/cache.db") {
		t.Error("a file that only has a database's name was listed as one")
	}

	pg := inventoryInstance(t, inv, "host:postgresql@17-main.service")
	if pg.Version != "17" || pg.Host.Cluster != "17/main" || pg.Credentials != dbx.CredentialsPeer || !pg.Connectable {
		t.Errorf("the native cluster = %+v host %+v", pg, pg.Host)
	}
	if len(pg.Endpoints) != 2 || pg.Endpoints[0].Port != 5438 || pg.Endpoints[1].Kind != "unix" {
		t.Errorf("the native cluster's endpoints = %+v", pg.Endpoints)
	}
	if redis := inventoryInstance(t, inv, "host:redis-server.service"); redis.State != dbx.StateInactive || redis.Connectable {
		t.Errorf("the installed, stopped server = %+v", redis)
	}
	for _, key := range []string{"host:postgres:9187", "host:postgres:15432"} {
		if inventoryHas(inv, key) {
			t.Errorf("%s: an exporter or a published port was listed as a native server", key)
		}
	}

	declared := inventoryInstance(t, inv, "compose:wiki/db")
	if declared.State != dbx.StateDeclared || declared.Flavor != "mariadb" || declared.Connectable {
		t.Errorf("the declared service = %+v", declared)
	}

	ghost := inventoryInstance(t, inv, "file:"+filepath.Join(root, "srv", "blog", "content", "ghost.db"))
	if ghost.Driver != dbx.DriverSQLite || !ghost.Connectable || ghost.File.Holder != dbx.HolderApplication {
		t.Errorf("the SQLite file = %+v file %+v", ghost, ghost.File)
	}
	if inventoryHas(inv, "file:"+filepath.Join(root, "srv", "blog", "not-a-database.db")) {
		t.Error("a file was listed on the strength of its extension")
	}
	self := inventoryInstance(t, inv, "file:"+filepath.Join(s.Cfg.DataDir, store.DatabaseFile))
	if !self.Self || self.Connectable {
		t.Errorf("the dashboard's own store = %+v", self)
	}

	sources := map[string]bool{}
	for _, scan := range inv.Scans {
		sources[scan.Source] = true
		if !scan.OK {
			t.Errorf("collector %s failed: %s", scan.Source, scan.Reason)
		}
	}
	for _, want := range []string{"docker", "compose", "listeners", "sockets", "units", "files"} {
		if !sources[want] {
			t.Errorf("no scan line for %s", want)
		}
	}
	if len(m.dials()) != 0 {
		t.Errorf("listing dialled %v; an inventory is a read", m.dials())
	}
}

// A role without system.admin is shown a list built without reading any
// container's environment: nothing recognised by a variable, and nothing said
// about credentials.
func TestInventoryForAReadRoleReadsNoEnvironment(t *testing.T) {
	_, r := inventoryRouter(t, auth.RoleReadOnly, ordinaryMachine())
	rec := do(t, r, http.MethodGet, "/databases/inventory", "")
	inv := decodeInventory(t, rec)
	if inv.Detail != "reduced" {
		t.Errorf("detail = %q", inv.Detail)
	}
	// The retagged image is there only as what its exposed port suggests.
	if fixture := inventoryInstance(t, inv, "docker:fixture"); fixture.Confidence != dbx.ConfidencePort {
		t.Errorf("a container was recognised from its environment for a role that may not read it: %+v", fixture)
	}
	db := inventoryInstance(t, inv, "docker:shop-db")
	if db.Credentials != dbx.CredentialsUnknown || db.User != "" || db.Version != "" {
		t.Errorf("the reduced list states what only the environment says: %+v", db)
	}
	for _, evidence := range db.Evidence {
		if strings.Contains(evidence, "POSTGRES_") {
			t.Errorf("evidence %q came from the environment", evidence)
		}
	}
	inventoryInstance(t, inv, "host:postgresql@17-main.service")
	if strings.Contains(rec.Body.String(), secretPassword) {
		t.Fatal("a container's password reached the reduced inventory")
	}
}

func TestInventoryMutationsNeedSystemAdmin(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		m := ordinaryMachine()
		s, r := inventoryRouter(t, role, m)
		for _, c := range []struct{ path, body string }{
			{"/databases/inventory/scan", `{}`},
			{"/databases/inventory/connect", `{"key":"docker:shop-db"}`},
			{"/databases/inventory/ignore", `{"key":"docker:shop-db","ignored":true}`},
		} {
			if rec := do(t, r, http.MethodPost, c.path, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s as %s answered %d", c.path, role, rec.Code)
			}
		}
		if rows := connectionRows(t, s); len(rows) != 0 || len(m.dials()) != 0 {
			t.Errorf("%s: rows %v dials %v", role, rows, m.dials())
		}
	}
}

// A missing Docker daemon is a stated silence about containers, not a failed
// request: the servers installed on the machine are still there to list.
func TestInventorySurvivesWithoutDocker(t *testing.T) {
	m := ordinaryMachine()
	m.dockerDown = true
	_, r := inventoryRouter(t, auth.RoleAdmin, m)
	inv := decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	inventoryInstance(t, inv, "host:postgresql@17-main.service")
	if inventoryHas(inv, "docker:shop-db") {
		t.Error("a container was listed with Docker down")
	}
	found := false
	for _, scan := range inv.Scans {
		if scan.Source == "docker" {
			found = true
			if scan.OK || scan.Reason == "" {
				t.Errorf("docker scan = %+v, want a failure that says why", scan)
			}
		}
	}
	if !found {
		t.Error("the silence about Docker was not stated")
	}

	// The reconcile runs its host half all the same, and the legacy listing
	// no longer answers 502 for the whole question.
	if rec := do(t, r, http.MethodPost, "/databases/sync", "{}"); rec.Code != http.StatusOK {
		t.Errorf("sync with Docker down answered %d: %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, r, http.MethodGet, "/databases/detected", ""); rec.Code != http.StatusOK {
		t.Errorf("detected with Docker down answered %d: %s", rec.Code, rec.Body.String())
	}
}

func TestConnectByKeySignsInBeforeSaving(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)

	rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:fixture","name":"Fixture"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect answered %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secretPassword) {
		t.Fatal("the password came back in the response")
	}
	var conn dbConnection
	_ = json.Unmarshal(rec.Body.Bytes(), &conn)
	if conn.Name != "Fixture" || conn.Driver != dbx.DriverPostgres || conn.Port != "25432" || conn.User != "postgres" {
		t.Errorf("connection = %+v", conn)
	}
	dials := m.dials()
	if len(dials) != 1 || !strings.Contains(dials[0], secretPassword) || !strings.Contains(dials[0], "127.0.0.1:25432") {
		t.Fatalf("dials = %v, want one sign-in with the container's own password", dials)
	}
	if rows := connectionRows(t, s); rows["Fixture"] != "docker:fixture" {
		t.Errorf("rows = %v, want the origin recorded", rows)
	}
	_, stored, err := s.dbConnRow(t.Context(), conn.ID)
	if err != nil || !strings.Contains(stored, secretPassword) {
		t.Errorf("stored DSN = %q (%v), want the password sealed into it", stored, err)
	}

	// The same instance again is the same row, and nothing is dialled for it.
	again := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:fixture"}`)
	var same dbConnection
	_ = json.Unmarshal(again.Body.Bytes(), &same)
	if again.Code != http.StatusOK || same.ID != conn.ID || len(m.dials()) != 1 {
		t.Errorf("repeat = %d id %d dials %v", again.Code, same.ID, m.dials())
	}

	// The inventory now says so.
	inv := decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	if got := inventoryInstance(t, inv, "docker:fixture").Connections; len(got) != 1 || got[0] != conn.ID {
		t.Errorf("connections = %v", got)
	}

	// A container recreated with a new password is the same row, re-sealed
	// once the new password has been seen to work.
	m.mu.Lock()
	m.containers[1].env[0] = "POSTGRES_PASSWORD=rotated-" + secretPassword
	m.mu.Unlock()
	rotated := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:fixture"}`)
	if rotated.Code != http.StatusOK {
		t.Fatalf("rotated = %d %s", rotated.Code, rotated.Body.String())
	}
	if _, stored, _ := s.dbConnRow(t.Context(), conn.ID); !strings.Contains(stored, "rotated-") {
		t.Errorf("stored DSN = %q, want the rotated password", stored)
	}
	if rows := connectionRows(t, s); len(rows) != 1 {
		t.Errorf("rows = %v, want the one row refreshed in place", rows)
	}
}

// A connection that could not sign in is not kept, and the answer says what
// the engine said.
func TestConnectRefusesWhatItCannotSignInTo(t *testing.T) {
	m := ordinaryMachine()
	m.refuse = map[string]string{":25432": `password authentication failed for user "postgres"`}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)

	cases := []struct {
		name, body string
		status     int
		code       string
		says       string
	}{
		{"credentials the container states are refused", `{"key":"docker:fixture"}`, http.StatusConflict, "sign_in_failed", "password authentication failed"},
		{"a typed password is refused", `{"key":"docker:fixture","password":"guess"}`, http.StatusBadRequest, "sign_in_failed", "password authentication failed"},
		{"a native server needs a password", `{"key":"host:postgresql@17-main.service"}`, http.StatusConflict, "credentials_required", "needs a password for postgres"},
		{"another account has no stated password", `{"key":"docker:shop-db","user":"reporting"}`, http.StatusConflict, "credentials_required", "that account"},
		{"a stopped container", `{"key":"docker:old-cache"}`, http.StatusConflict, "not_connectable", "exited"},
		{"a stopped unit", `{"key":"host:redis-server.service"}`, http.StatusConflict, "not_connectable", "not running"},
		{"an engine with no driver", `{"key":"docker:cache"}`, http.StatusConflict, "not_connectable", "no driver"},
		{"something that is gone", `{"key":"docker:vanished"}`, http.StatusNotFound, "not_found", "any more"},
		{"not a key", `{"key":"shop-db"}`, http.StatusBadRequest, "bad_request", "key"},
		{"an unknown field", `{"key":"docker:shop-db","dsn":"postgres://x"}`, http.StatusBadRequest, "bad_request", "unknown field"},
		{"a name that is not one", `{"key":"docker:shop-db","name":"../etc"}`, http.StatusBadRequest, "bad_request", "name"},
	}
	for _, tc := range cases {
		rec := do(t, r, http.MethodPost, "/databases/inventory/connect", tc.body)
		if rec.Code != tc.status || inventoryErrorCode(rec) != tc.code || !strings.Contains(rec.Body.String(), tc.says) {
			t.Errorf("%s: %d %s, want %d %s saying %q", tc.name, rec.Code, strings.TrimSpace(rec.Body.String()), tc.status, tc.code, tc.says)
		}
	}
	if rows := connectionRows(t, s); len(rows) != 0 {
		t.Errorf("rows = %v; nothing that failed may be kept", rows)
	}
	for _, dsn := range m.dials() {
		if strings.Contains(dsn, ":5438") {
			t.Errorf("the native server was dialled with no password: %q", dsn)
		}
	}
}

func TestConnectHostServerWithATypedPassword(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	rec := do(t, r, http.MethodPost, "/databases/inventory/connect",
		`{"key":"host:postgresql@17-main.service","user":"app","password":"typed-pw","database":"shop"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect answered %d: %s", rec.Code, rec.Body.String())
	}
	var conn dbConnection
	_ = json.Unmarshal(rec.Body.Bytes(), &conn)
	if conn.Host != "127.0.0.1" || conn.Port != "5438" || conn.User != "app" || conn.Database != "shop" {
		t.Errorf("connection = %+v", conn)
	}
	if conn.Name != "postgres 17 main on this host" {
		t.Errorf("name = %q", conn.Name)
	}
	if rows := connectionRows(t, s); rows[conn.Name] != "host:postgresql@17-main.service" {
		t.Errorf("rows = %v", rows)
	}
	if dials := m.dials(); len(dials) != 1 || dials[0] != "postgres://app:typed-pw@127.0.0.1:5438/shop?sslmode=disable" {
		t.Errorf("dials = %v", dials)
	}
}

// A password a container keeps in a file is read at the moment of connecting,
// on the server, and used like one its environment stated.
func TestSecretFilePasswordIsReadAtConnect(t *testing.T) {
	m := &fakeMachine{containers: []fakeContainer{{
		id: "bbbbbbbbbbbb0001", name: "vault-db", image: "postgres:16", state: "running",
		env:   []string{"POSTGRES_PASSWORD_FILE=/run/secrets/pg"},
		ports: []fakePort{{5432, 35432, "127.0.0.1"}},
	}}}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	asked := ""
	s.dbInventory.secret = func(_ context.Context, container, path string) ([]byte, error) {
		asked = container + ":" + path
		return []byte("from-the-file\n"), nil
	}
	inv := decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	if got := inventoryInstance(t, inv, "docker:vault-db").Credentials; got != dbx.CredentialsSecretFile {
		t.Fatalf("credentials = %q", got)
	}
	if asked != "" {
		t.Fatal("the secret file was read to list the container")
	}
	rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:vault-db"}`)
	if rec.Code != http.StatusCreated || asked != "bbbbbbbbbbbb0001:/run/secrets/pg" {
		t.Fatalf("connect = %d %s, read %q", rec.Code, rec.Body.String(), asked)
	}
	if dials := m.dials(); len(dials) != 1 || !strings.Contains(dials[0], "postgres:from-the-file@") {
		t.Errorf("dials = %v", dials)
	}

	// Unreadable: the operator is asked, and nothing is guessed.
	s.dbInventory.secret = func(context.Context, string, string) ([]byte, error) { return nil, errors.New("no such file") }
	if _, err := s.Store.DB.Exec(`DELETE FROM db_connections`); err != nil {
		t.Fatal(err)
	}
	rec = do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:vault-db"}`)
	if rec.Code != http.StatusConflict || inventoryErrorCode(rec) != "credentials_required" {
		t.Errorf("unreadable secret = %d %s", rec.Code, rec.Body.String())
	}
}

// The official Redis image reads no REDIS_PASSWORD. A server that refuses the
// password its container states is tried once with none, which sends no guess.
func TestAnUnreadPasswordFallsBackToTheOpenServer(t *testing.T) {
	m := &fakeMachine{
		containers: []fakeContainer{{
			id: "cccccccccccc0001", name: "kv", image: "redis:7", state: "running",
			env: []string{"REDIS_PASSWORD=never-applied"}, cmd: []string{"redis-server"},
			ports: []fakePort{{6379, 16379, "127.0.0.1"}},
		}},
		refuse: map[string]string{"never-applied": "ERR AUTH <password> called without any password configured"},
	}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:kv"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect answered %d: %s", rec.Code, rec.Body.String())
	}
	var conn dbConnection
	_ = json.Unmarshal(rec.Body.Bytes(), &conn)
	if _, stored, _ := s.dbConnRow(t.Context(), conn.ID); stored != "redis://127.0.0.1:16379/0" {
		t.Errorf("stored = %q, want the DSN that actually signed in", stored)
	}
}

func TestConnectSQLiteFileThroughContainment(t *testing.T) {
	m := &fakeMachine{}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	root := s.Cfg.FileRoots[0]
	path := filepath.Join(root, "srv", "app", "data.sqlite")
	inventorySQLiteFile(t, path)

	rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"file:`+path+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect answered %d: %s", rec.Code, rec.Body.String())
	}
	var conn dbConnection
	_ = json.Unmarshal(rec.Body.Bytes(), &conn)
	if conn.Driver != dbx.DriverSQLite || conn.Database != path || conn.Name != "data" {
		t.Errorf("connection = %+v", conn)
	}
	if rows := connectionRows(t, s); rows["data"] != "file:"+path {
		t.Errorf("rows = %v", rows)
	}
	if again := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"file:`+path+`"}`); again.Code != http.StatusOK {
		t.Errorf("repeat = %d %s", again.Code, again.Body.String())
	}

	// The dashboard's own store is never connected, by any spelling.
	own := filepath.Join(s.Cfg.DataDir, store.DatabaseFile)
	inventorySQLiteFile(t, own)
	s.modules.files = filesAt(root, s.Cfg.DataDir)
	rec = do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"file:`+own+`"}`)
	if rec.Code != http.StatusConflict || inventoryErrorCode(rec) != "self_database" {
		t.Errorf("own store = %d %s", rec.Code, rec.Body.String())
	}
	link := filepath.Join(root, "srv", "innocent.db")
	if err := os.Symlink(own, link); err != nil {
		t.Fatal(err)
	}
	rec = do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"file:`+link+`"}`)
	if rec.Code != http.StatusConflict || inventoryErrorCode(rec) != "self_database" {
		t.Errorf("own store through a link = %d %s", rec.Code, rec.Body.String())
	}
	// And not through the route that takes a host and a password either.
	if err := s.refuseOwnStore(dbx.DriverSQLite, own); err == nil {
		t.Error("the own store was not refused as a DSN")
	}

	outside := filepath.Join(t.TempDir(), "elsewhere.db")
	inventorySQLiteFile(t, outside)
	rec = do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"file:`+outside+`"}`)
	if rec.Code != http.StatusForbidden || inventoryErrorCode(rec) != "outside_root" {
		t.Errorf("a file outside the roots = %d %s", rec.Code, rec.Body.String())
	}

	plain := filepath.Join(root, "srv", "app", "notes.db")
	if err := os.WriteFile(plain, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"file:`+plain+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("a file that is not a database = %d %s", rec.Code, rec.Body.String())
	}
	if rows := connectionRows(t, s); len(rows) != 1 {
		t.Errorf("rows = %v", rows)
	}
}

// The reconcile signs in before it saves, records where each connection came
// from, and says something about every server it could not connect.
func TestSyncProbesRecordsOriginAndReports(t *testing.T) {
	m := ordinaryMachine()
	m.containers = append(m.containers,
		fakeContainer{
			// Its volume was initialised under another password.
			id: "aaaaaaaaaaaa0006", name: "stale", image: "postgres:16", state: "running",
			env: []string{"POSTGRES_PASSWORD=stale-pw"}, ports: []fakePort{{5432, 45432, "127.0.0.1"}},
		},
		fakeContainer{
			id: "aaaaaaaaaaaa0007", name: "nowhere", image: "postgres:16", state: "running",
			env: []string{"POSTGRES_PASSWORD=pw"}, networkMode: "none",
		},
		fakeContainer{
			id: "aaaaaaaaaaaa0008", name: "guess", image: "acme/thing:1", state: "running",
			entrypoint: []string{"/start"}, ports: []fakePort{{5432, 55432, "127.0.0.1"}},
		},
	)
	m.listeners = append(m.listeners, proxysvc.Listener{Protocol: "tcp", Address: "127.0.0.1", Port: 6379, PID: 70, Process: "redis-server"})
	m.refuse = map[string]string{"stale-pw": `password authentication failed for user "postgres"`}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)

	// A connection typed by hand to localhost covers the server found at
	// 127.0.0.1: the two are one address.
	sealed, err := s.Sealer.Seal("postgres://shop:typed@localhost:15432/shop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('by-hand','postgres',?,0)`, sealed); err != nil {
		t.Fatal(err)
	}

	rec := do(t, r, http.MethodPost, "/databases/sync", "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("sync answered %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Added            []string            `json:"added"`
		Already          []string            `json:"already"`
		Unreachable      []unreachableServer `json:"unreachable"`
		NeedsCredentials []credentialServer  `json:"needsCredentials"`
		Ignored          []string            `json:"ignored"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Join(out.Added, ",") != "fixture,redis on this host" {
		t.Errorf("added = %v", out.Added)
	}
	if strings.Join(out.Already, ",") != "by-hand" {
		t.Errorf("already = %v, want the hand-typed localhost connection to cover shop-db", out.Already)
	}
	reasons := map[string]string{}
	for _, u := range out.Unreachable {
		reasons[u.Container] = u.Reason
	}
	if !strings.Contains(reasons["stale"], "password authentication failed") {
		t.Errorf("stale: %q, want the engine's own refusal", reasons["stale"])
	}
	if !strings.Contains(reasons["nowhere"], "no published port") {
		t.Errorf("nowhere: %q", reasons["nowhere"])
	}
	if _, reported := reasons["old-cache"]; reported {
		t.Error("a stopped container was reported as a failure of the reconcile")
	}
	if len(out.NeedsCredentials) != 1 || out.NeedsCredentials[0].Port != 5438 || out.NeedsCredentials[0].User != "postgres" {
		t.Errorf("needsCredentials = %+v", out.NeedsCredentials)
	}
	rows := connectionRows(t, s)
	if rows["fixture"] != "docker:fixture" || rows["redis on this host"] != "host:redis:6379" {
		t.Errorf("rows = %v, want each connection's origin", rows)
	}
	if rows["by-hand"] != "docker:shop-db" {
		t.Errorf("rows = %v, want the hand-typed connection to learn which server it is", rows)
	}
	if _, kept := rows["stale"]; kept {
		t.Error("a connection that could not sign in was saved")
	}
	for _, dsn := range m.dials() {
		if strings.Contains(dsn, ":55432") {
			t.Errorf("a guess from a port number was dialled: %q", dsn)
		}
		if strings.Contains(dsn, ":5438") {
			t.Errorf("a server that needs a password was dialled without one: %q", dsn)
		}
	}

	// Converges: a second run adds nothing.
	before := len(connectionRows(t, s))
	again := do(t, r, http.MethodPost, "/databases/sync", "{}")
	if len(connectionRows(t, s)) != before || !strings.Contains(again.Body.String(), `"added":[]`) {
		t.Errorf("a second reconcile changed something: %s", again.Body.String())
	}
}

// Forgetting an auto-connected server has to mean something. The reconcile
// used to bring it straight back.
func TestAForgottenServerStaysForgotten(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	do(t, r, http.MethodPost, "/databases/sync", "{}")
	var id int64
	if err := s.Store.DB.QueryRow(`SELECT id FROM db_connections WHERE name = 'shop-db'`).Scan(&id); err != nil {
		t.Fatalf("the reconcile did not connect shop-db: %v", err)
	}

	// Forgotten through the route, as an operator forgets it.
	origin := s.originOfConnection(t.Context(), id)
	if origin != "docker:shop-db" {
		t.Fatalf("origin = %q", origin)
	}
	if rec := do(t, r, http.MethodDelete, pathf("/databases/%d", id), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("forget = %d %s", rec.Code, rec.Body.String())
	}
	if ignored, _ := s.ignoredOrigins(t.Context()); !ignored[origin] {
		t.Fatal("forgetting the last connection did not mark the server ignored")
	}
	// The mark is a second thing the request changed, so its entry says so.
	if trail := auditTrail(t, s); !strings.Contains(trail, "database.connection.delete shop-db 204") ||
		!strings.Contains(trail, `"ignored":true`) || !strings.Contains(trail, `"origin":"docker:shop-db"`) {
		t.Errorf("the forget's audit entry does not say the server was marked ignored:\n%s", trail)
	}
	// Forgetting is not counted twice: the mark was already there.
	if marked, err := s.ignoreOriginOnForget(t.Context(), origin, "tester"); err != nil || marked {
		t.Fatalf("a second forget reported a new mark = %v, %v", marked, err)
	}

	rec := do(t, r, http.MethodPost, "/databases/sync", "{}")
	if _, back := connectionRows(t, s)["shop-db"]; back {
		t.Fatal("the forgotten server was connected again")
	}
	if !strings.Contains(rec.Body.String(), `"ignored":["docker:shop-db"]`) {
		t.Errorf("sync = %s, want it to say what it left alone", rec.Body.String())
	}
	inv := decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	if !inventoryInstance(t, inv, "docker:shop-db").Ignored || len(inv.Ignored) != 1 {
		t.Errorf("the inventory does not show it as ignored: %v", inv.Ignored)
	}

	// Connecting it on purpose takes the mark back.
	if rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:shop-db"}`); rec.Code != http.StatusCreated {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	inv = decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	if inventoryInstance(t, inv, "docker:shop-db").Ignored {
		t.Error("a server connected on purpose is still marked ignored")
	}

	// A connection older than the origin column is matched by its address.
	sealed, _ := s.Sealer.Seal("postgres://postgres:x@localhost:25432/postgres")
	res, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('legacy','postgres',?,0)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	legacy, _ := res.LastInsertId()
	if got := s.originOfConnection(t.Context(), legacy); got != "docker:fixture" {
		t.Errorf("origin of a legacy row = %q", got)
	}
	// While another connection to the same server remains, nothing is ignored.
	if _, err := s.Store.DB.Exec(`UPDATE db_connections SET origin = 'docker:fixture' WHERE id = ?`, legacy); err != nil {
		t.Fatal(err)
	}
	if marked, err := s.ignoreOriginOnForget(t.Context(), "docker:fixture", "tester"); err != nil || marked {
		t.Fatalf("ignoreOriginOnForget = %v, %v", marked, err)
	}
	if ignored, _ := s.ignoredOrigins(t.Context()); ignored["docker:fixture"] {
		t.Error("a server that is still connected was marked ignored")
	}
	// And a file is never marked: nothing connects one unasked.
	if marked, err := s.ignoreOriginOnForget(t.Context(), "file:/srv/x.db", "tester"); err != nil || marked {
		t.Fatalf("ignoreOriginOnForget = %v, %v", marked, err)
	}
	if ignored, _ := s.ignoredOrigins(t.Context()); ignored["file:/srv/x.db"] {
		t.Error("a file was marked ignored on forget")
	}
}

// The fleet's list of what is on the machine and not connected is the
// inventory's reading by the sync's rules, without the dialling: a container
// it cannot reach is listed with the reason, a server installed on the host is
// offered for its password, and what the sync leaves alone — a stopped
// container, one the operator set aside, one that is connected — is not listed
// as something waiting to be dealt with.
func TestTheFleetListsWhatIsNotConnectedAsTheInventorySeesIt(t *testing.T) {
	m := ordinaryMachine()
	m.containers = append(m.containers, fakeContainer{
		id: "aaaaaaaaaaaa0006", name: "nowhere", image: "postgres:16", state: "running",
		env: []string{"POSTGRES_PASSWORD=" + secretPassword}, entrypoint: []string{"docker-entrypoint.sh"}, cmd: []string{"postgres"},
	})
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	listed := func() (map[string]string, []credentialServer) {
		t.Helper()
		s.dropInventory()
		rec := do(t, r, http.MethodGet, "/databases/fleet", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("fleet = %d %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Unreachable      []unreachableServer `json:"unreachable"`
			NeedsCredentials []credentialServer  `json:"needsCredentials"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		reasons := map[string]string{}
		for _, u := range out.Unreachable {
			reasons[u.Container] = u.Reason
		}
		return reasons, out.NeedsCredentials
	}

	reasons, needs := listed()
	if !strings.Contains(reasons["nowhere"], "no published port") {
		t.Errorf("nowhere: %q, want the reason it cannot be reached", reasons["nowhere"])
	}
	for _, name := range []string{"shop-db", "fixture", "old-cache", "cache", "notes-app"} {
		if reason, reported := reasons[name]; reported {
			t.Errorf("%s is listed as unreachable (%q): the sync can connect it, or leaves it alone", name, reason)
		}
	}
	if len(needs) != 1 || needs[0].Port != 5438 || needs[0].Driver != "postgres" || needs[0].User != "postgres" || needs[0].Name != "postgres on this host" {
		t.Fatalf("needsCredentials = %+v, want the host's PostgreSQL offered for its password", needs)
	}

	// Set aside by the operator: the sync leaves it alone, and so does this.
	inv := decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	var hostKey string
	for _, inst := range inv.Instances {
		if inst.Source == dbx.SourceHost && inst.Driver == dbx.DriverPostgres {
			hostKey = inst.Key
		}
	}
	if hostKey == "" {
		t.Fatal("the inventory does not list the host's PostgreSQL")
	}
	for _, key := range []string{hostKey, "docker:nowhere"} {
		if rec := do(t, r, http.MethodPost, "/databases/inventory/ignore", fmt.Sprintf(`{"key":%q,"ignored":true}`, key)); rec.Code != http.StatusOK {
			t.Fatalf("ignore %s = %d %s", key, rec.Code, rec.Body.String())
		}
	}
	if reasons, needs := listed(); len(reasons) != 0 || len(needs) != 0 {
		t.Errorf("servers that were set aside are still listed: %v %+v", reasons, needs)
	}

	// And none of it is shown to somebody who could not act on it.
	for _, key := range []string{hostKey, "docker:nowhere"} {
		do(t, r, http.MethodPost, "/databases/inventory/ignore", fmt.Sprintf(`{"key":%q,"ignored":false}`, key))
	}
	viewer := chi.NewRouter()
	viewer.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 2, Username: "viewer"}, Role: auth.RoleReadOnly, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountDatabaseRoutes(viewer)
	rec := do(t, viewer, http.MethodGet, "/databases/fleet", "")
	if !strings.Contains(rec.Body.String(), `"unreachable":[]`) || !strings.Contains(rec.Body.String(), `"needsCredentials":[]`) {
		t.Errorf("a viewer's fleet lists what is not connected: %s", rec.Body.String())
	}
}

func TestIgnoreIsSetClearedAndAudited(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	for _, c := range []struct {
		body   string
		status int
	}{
		{`{"key":"docker:fixture","ignored":true}`, http.StatusOK},
		{`{"key":"docker:fixture","ignored":true}`, http.StatusOK},
		{`{"key":"docker:fixture"}`, http.StatusBadRequest},
		{`{"key":"fixture","ignored":true}`, http.StatusBadRequest},
	} {
		if rec := do(t, r, http.MethodPost, "/databases/inventory/ignore", c.body); rec.Code != c.status {
			t.Errorf("%s answered %d: %s", c.body, rec.Code, rec.Body.String())
		}
	}
	do(t, r, http.MethodPost, "/databases/sync", "{}")
	if _, connected := connectionRows(t, s)["fixture"]; connected {
		t.Fatal("the reconcile connected a server the operator said to ignore")
	}
	if rec := do(t, r, http.MethodPost, "/databases/inventory/ignore", `{"key":"docker:fixture","ignored":false}`); rec.Code != http.StatusOK {
		t.Fatalf("unignore = %d", rec.Code)
	}
	do(t, r, http.MethodPost, "/databases/sync", "{}")
	if _, connected := connectionRows(t, s)["fixture"]; !connected {
		t.Error("the server was not connected once the mark was taken back")
	}
	var actions []string
	rows, err := s.Store.DB.Query(`SELECT action FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action string
		_ = rows.Scan(&action)
		actions = append(actions, action)
	}
	joined := strings.Join(actions, ",")
	for _, want := range []string{"database.inventory.ignore", "database.inventory.unignore", "database.connection.sync"} {
		if !strings.Contains(joined, want) {
			t.Errorf("audit trail %v lacks %s", actions, want)
		}
	}
}

// The legacy listing keeps its shape, now read off the inventory.
func TestDetectedStillAnswersInItsOwnShape(t *testing.T) {
	m := ordinaryMachine()
	_, r := inventoryRouter(t, auth.RoleAdmin, m)
	rec := do(t, r, http.MethodGet, "/databases/detected", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), secretPassword) {
		t.Fatalf("detected = %d %s", rec.Code, rec.Body.String())
	}
	var out detectedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byName := map[string]detectedServer{}
	for _, server := range out.Servers {
		byName[server.Container+server.Process] = server
	}
	if got := byName["shop-db"]; got.Driver != dbx.DriverPostgres || got.Port != 15432 || got.Source != dbx.SourceDocker {
		t.Errorf("shop-db = %+v", got)
	}
	if got := byName["postgres"]; got.Port != 5438 || got.Source != dbx.SourceHost || !got.NeedsCredentials {
		t.Errorf("the native server = %+v", got)
	}
	if _, listed := byName["old-cache"]; listed {
		t.Error("a stopped container was listed as detected")
	}
}

// The password-typed host route used to hand back whichever connection had the
// address and throw the new credentials away.
func TestHostConnectMatchesTheAccountNotJustTheAddress(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	post := func(user, password string) (*httptest.ResponseRecorder, dbConnection) {
		body := fmt.Sprintf(`{"driver":"postgres","host":"127.0.0.1","port":5438,"user":%q,"password":%q,"database":"postgres","name":""}`, user, password)
		rec := do(t, r, http.MethodPost, "/databases/host", body)
		var conn dbConnection
		_ = json.Unmarshal(rec.Body.Bytes(), &conn)
		return rec, conn
	}
	first, a := post("postgres", "one")
	if first.Code != http.StatusCreated {
		t.Fatalf("first = %d %s", first.Code, first.Body.String())
	}
	second, b := post("reporting", "two")
	if second.Code != http.StatusCreated || b.ID == a.ID {
		t.Fatalf("another account at the same address = %d id %d (first %d)", second.Code, b.ID, a.ID)
	}
	third, c := post("postgres", "rotated")
	if third.Code != http.StatusOK || c.ID != a.ID {
		t.Fatalf("the same account again = %d id %d", third.Code, c.ID)
	}
	if _, stored, _ := s.dbConnRow(t.Context(), a.ID); !strings.Contains(stored, "postgres:rotated@") {
		t.Errorf("stored = %q, want the password that was just seen to work", stored)
	}
	if _, stored, _ := s.dbConnRow(t.Context(), b.ID); !strings.Contains(stored, "reporting:two@") {
		t.Errorf("the other account's connection was touched: %q", stored)
	}
	rows := connectionRows(t, s)
	if rows[a.Name] != "host:postgresql@17-main.service" || rows[b.Name] != "host:postgresql@17-main.service" {
		t.Errorf("rows = %v, want both to know which server they are", rows)
	}
}

// Resetting an account changes its password on the server, which a protected
// connection's own routes refuse. The account route concerns no connection by
// its path, so it has to ask: the account a protected connection signs in with
// is left alone, and another account on the same server is not held back.
//
// The check is asked directly rather than through the route: past it the route
// runs psql on this machine, and a test must never get that far.
func TestHostGrantLeavesAProtectedConnectionsAccountAlone(t *testing.T) {
	s := testServer(t)
	sealed, _ := s.Sealer.Seal("postgres://app:" + secretPassword + "@localhost:25432/shop?sslmode=disable")
	if _, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at, read_only) VALUES('shop','postgres',?,0,1)`, sealed); err != nil {
		t.Fatal(err)
	}
	account := func(user, database string) dbx.Candidate {
		return dbx.Candidate{Driver: dbx.DriverPostgres, Source: dbx.SourceHost, Host: "127.0.0.1", Port: 25432, User: user, Database: database}
	}
	err := s.refuseGrantOnProtected(t.Context(), account("app", "shop"))
	var refused *httpx.APIError
	if !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Code != "connection_read_only" {
		t.Fatalf("resetting a protected connection's account = %v, want 409 connection_read_only", err)
	}
	for _, other := range []dbx.Candidate{account("reports", "shop"), account("app", "billing")} {
		if err := s.refuseGrantOnProtected(t.Context(), other); err != nil {
			t.Errorf("%s on %s is not the protected connection's and was refused: %v", other.User, other.Database, err)
		}
	}
	if _, err := s.Store.DB.Exec(`UPDATE db_connections SET read_only = 0`); err != nil {
		t.Fatal(err)
	}
	if err := s.refuseGrantOnProtected(t.Context(), account("app", "shop")); err != nil {
		t.Errorf("with protection off the account was still refused: %v", err)
	}
}

// The account route ends the same way: it re-seals the connection as that
// account, and leaves any other connection to the same server alone. Redis has
// one password rather than accounts, so with the password given there is no
// host command to run and the whole tail is reachable from a test.
func TestHostGrantReSealsOnlyTheSameConnection(t *testing.T) {
	m := &fakeMachine{listeners: []proxysvc.Listener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 6379, PID: 70, Process: "redis-server", Manager: "systemd", ManagerName: "redis-server.service"},
	}}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	sealed, _ := s.Sealer.Seal("redis://:other@127.0.0.1:6379/3")
	if _, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('sessions','redis',?,0)`, sealed); err != nil {
		t.Fatal(err)
	}
	grant := func(password string) (*httptest.ResponseRecorder, dbConnection) {
		rec := do(t, r, http.MethodPost, "/databases/host/grant",
			fmt.Sprintf(`{"driver":"redis","port":6379,"password":%q}`, password))
		var conn dbConnection
		_ = json.Unmarshal(rec.Body.Bytes(), &conn)
		return rec, conn
	}
	first, a := grant("one")
	if first.Code != http.StatusCreated || a.Database != "0" {
		t.Fatalf("first = %d %s", first.Code, first.Body.String())
	}
	second, b := grant("two")
	if second.Code != http.StatusOK || b.ID != a.ID {
		t.Fatalf("second = %d id %d, want the same row re-sealed", second.Code, b.ID)
	}
	if _, stored, _ := s.dbConnRow(t.Context(), a.ID); stored != "redis://:two@127.0.0.1:6379/0" {
		t.Errorf("stored = %q", stored)
	}
	if _, stored, _ := s.connectionByName(t.Context(), "sessions"); stored != "redis://:other@127.0.0.1:6379/3" {
		t.Errorf("a connection to another database of the same server was overwritten: %q", stored)
	}
	if rows := connectionRows(t, s); rows[a.Name] != "host:redis-server.service" {
		t.Errorf("rows = %v", rows)
	}
}

// The scan reads sixteen bytes of a file named like a database and nothing of
// anything else, follows no link, and stops where it is told to.
func TestFileScanFindsDatabasesByTheirFirstBytes(t *testing.T) {
	m := &fakeMachine{}
	s, _ := inventoryRouter(t, auth.RoleAdmin, m)
	root := filepath.Join(s.Cfg.FileRoots[0], "srv")
	inventorySQLiteFile(t, filepath.Join(root, "app", "data", "app.db"))
	duck := append([]byte{0, 0, 0, 0, 0, 0, 0, 0}, []byte("DUCK\x40\x00\x00\x00 and the rest")...)
	write := func(path string, content []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "analytics", "events.duckdb"), duck)
	write(filepath.Join(root, "app", "fake.sqlite"), []byte("nope"))
	write(filepath.Join(root, "app", "empty.db"), nil)
	write(filepath.Join(root, "app", "node_modules", "pkg", "cache.db"), append([]byte("SQLite format 3\x00"), 0, 0))
	write(filepath.Join(root, "a", "b", "c", "d", "e", "f", "g", "too-deep.db"), append([]byte("SQLite format 3\x00"), 0, 0))
	// A stopped PostgreSQL's data directory, with a file inside it that must
	// not be reached.
	write(filepath.Join(root, "pgdata", "PG_VERSION"), []byte("15\n"))
	write(filepath.Join(root, "pgdata", "base", "1", "inner.db"), append([]byte("SQLite format 3\x00"), 0, 0))
	write(filepath.Join(root, "pgdata", "global", "pg_control"), []byte("x"))
	// A Redis's working directory is its data. An application's directory with
	// one stray dump in it is still the application's, and is walked.
	write(filepath.Join(root, "kv", "dump.rdb"), []byte("REDIS0011"))
	write(filepath.Join(root, "kv", "appendonlydir", "appendonly.aof.1.base.rdb"), []byte("REDIS0011"))
	write(filepath.Join(root, "shop", "dump.rdb"), []byte("REDIS0011"))
	inventorySQLiteFile(t, filepath.Join(root, "shop", "orders.sqlite3"))
	inventorySQLiteFile(t, filepath.Join(root, "shop", "var", "sessions.db"))
	outside := filepath.Join(t.TempDir(), "secret.db")
	inventorySQLiteFile(t, outside)
	if err := os.Symlink(outside, filepath.Join(root, "app", "linked.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "app", "linked-dir")); err != nil {
		t.Fatal(err)
	}

	result := s.scanDatabaseFiles(t.Context())
	found := map[string]string{}
	for _, f := range result.files {
		found[strings.TrimPrefix(f.Path, root+"/")] = f.Engine
	}
	want := map[string]string{
		"app/data/app.db": "sqlite", "analytics/events.duckdb": "duckdb",
		"shop/orders.sqlite3": "sqlite", "shop/var/sessions.db": "sqlite",
	}
	if len(found) != len(want) {
		t.Errorf("found %v, want %v", found, want)
	}
	for path, engine := range want {
		if found[path] != engine {
			t.Errorf("%s: %q, want %s", path, found[path], engine)
		}
	}
	dirs := map[string]string{}
	for _, d := range result.dirs {
		dirs[strings.TrimPrefix(d.Path, root+"/")] = d.Engine + " " + d.Version
	}
	if len(dirs) != 2 || dirs["pgdata"] != "postgres 15" || dirs["kv"] != "redis " {
		t.Errorf("data directories = %v, want the PostgreSQL and the Redis one and not the application's", dirs)
	}
	if !result.scan.OK || result.scan.Truncated {
		t.Errorf("scan = %+v", result.scan)
	}
	for _, path := range []string{filepath.Join(root, "app", "data", "app.db-wal"), filepath.Join(root, "app", "data", "app.db-shm")} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s exists: the scan opened a database with the driver", path)
		}
	}

	// A root outside the file roots is not scanned, and the answer says so.
	s.dbInventory.host.roots = []string{filepath.Dir(outside)}
	result = s.scanDatabaseFiles(t.Context())
	if len(result.files) != 0 || !strings.Contains(result.scan.Reason, "outside the file roots") {
		t.Errorf("a root outside JD_FILE_ROOTS: files %v reason %q", result.files, result.scan.Reason)
	}
}

func TestPostgresClusterConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "postgresql.conf")
	conf := "# comment\ndata_directory = '/var/lib/postgresql/17/main'\t\t# use data in another directory\n#port = 5432\nport = 5438\t\t\t\t# (change requires restart)\nmax_connections = 100\n"
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	if port, dir := postgresClusterConfig(path); port != 5438 || dir != "/var/lib/postgresql/17/main" {
		t.Errorf("port %d dir %q", port, dir)
	}
	if port, dir := postgresClusterConfig(filepath.Join(t.TempDir(), "missing.conf")); port != 0 || dir != "" {
		t.Errorf("a missing file gave port %d dir %q", port, dir)
	}
}

func TestOwnedBindSources(t *testing.T) {
	for source, want := range map[string]bool{
		"/srv/app/data": true, "/home/ubuntu/project": true, "/var/lib/myapp": true, "/data/db": true,
		"/": false, "/home": false, "/etc/caddy": false, "/var/run/docker.sock": false, "/proc": false,
		"/var/lib/docker/volumes": false, "/var/log": false, "/tmp/x/y": false,
	} {
		if got := ownedBindSource(source); got != want {
			t.Errorf("ownedBindSource(%q) = %v", source, got)
		}
	}
}

// The role statement runs as the postgres superuser, with a password the
// operator supplied inside a dollar-quoted block. Whatever the password is,
// the tag that closes the block must not appear in it.
func TestPostgresAccountStatementCannotBeClosedByThePassword(t *testing.T) {
	for _, password := range []string{"plain", "$jd$; DROP ROLE postgres; --", "it's", `back\slash`, "$$"} {
		stmt, err := postgresAccountStatement("just_dashboard", password, "LOGIN")
		if err != nil {
			t.Fatalf("%q: %v", password, err)
		}
		open := strings.Index(stmt, "$jd")
		tag := stmt[open : strings.Index(stmt[open+1:], "$")+open+2]
		if !strings.HasPrefix(stmt, "DO "+tag+" BEGIN") || !strings.HasSuffix(stmt, "END "+tag+";") {
			t.Fatalf("%q: statement is not one block: %s", password, stmt)
		}
		if strings.Count(stmt, tag) != 2 {
			t.Errorf("%q: the tag %s appears %d times", password, tag, strings.Count(stmt, tag))
		}
		if !strings.Contains(stmt, sqlLiteral(password)) {
			t.Errorf("%q: the password is not in the statement as a literal", password)
		}
	}
}

func TestComposeImagesReadsOnlyInsideTheRoots(t *testing.T) {
	s, _ := inventoryRouter(t, auth.RoleAdmin, &fakeMachine{})
	inside := filepath.Join(s.Cfg.FileRoots[0], "compose.yml")
	if err := os.WriteFile(inside, []byte("services:\n  db:\n    image: postgres:${TAG:-16}\n  app:\n    image: ${APP_IMAGE}\n  built:\n    build: .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "compose.yml")
	if err := os.WriteFile(outside, []byte("services:\n  db:\n    image: mysql:8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	images := s.composeImages([]string{inside, outside})
	if len(images) != 1 || images["db"] != "postgres" {
		t.Errorf("images = %v", images)
	}
}

func filesAt(roots ...string) *files.Service { return files.New(roots) }

func auditActions(t *testing.T, s *Server) []string {
	t.Helper()
	rows, err := s.Store.DB.Query(`SELECT action FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			t.Fatal(err)
		}
		out = append(out, action)
	}
	return out
}

// A container's creator chooses its environment, and creating a container
// takes far less than the right to make a connection. A database name with the
// driver's own options in it must never reach a stored connection string —
// allowAllFiles would let that container's server read this process's files.
func TestNamesNeverCarryDriverOptionsIntoAConnection(t *testing.T) {
	m := &fakeMachine{containers: []fakeContainer{
		{
			id: "ffffffffffff0001", name: "evil", image: "acme/whatever:1", state: "running",
			env:        []string{"MYSQL_ROOT_PASSWORD=pw", "MYSQL_DATABASE=app?allowAllFiles=true"},
			entrypoint: []string{"mysqld"}, ports: []fakePort{{3306, 13306, "127.0.0.1"}},
		},
		{
			id: "ffffffffffff0002", name: "db", image: "mysql:8", state: "running",
			env: []string{"MYSQL_ROOT_PASSWORD=p@ss/w?rd&allowAllFiles=true"}, ports: []fakePort{{3306, 23306, "127.0.0.1"}},
		},
	}}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)

	rec := do(t, r, http.MethodPost, "/databases/sync", "{}")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "no connection string can carry") {
		t.Fatalf("sync = %d %s, want the container reported as one that cannot be connected", rec.Code, rec.Body.String())
	}
	if rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:evil"}`); rec.Code != http.StatusConflict || inventoryErrorCode(rec) != "not_connectable" {
		t.Errorf("connect = %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, r, http.MethodPost, "/databases/adopt", `{"container":"evil"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("adopt = %d %s", rec.Code, rec.Body.String())
	}
	for _, body := range []string{
		`{"key":"docker:db","database":"x?allowAllFiles=true&allowCleartextPasswords=true"}`,
		`{"key":"docker:db","database":"a/b"}`,
		`{"key":"docker:db","user":"root:x","password":"pw"}`,
		`{"key":"docker:db","user":"root@tcp(evil:3306)/","password":"pw"}`,
		`{"key":"docker:db","user":"ro\not","password":"pw"}`,
	} {
		if rec := do(t, r, http.MethodPost, "/databases/inventory/connect", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{
		`{"driver":"mysql","host":"127.0.0.1","port":23306,"user":"root","password":"pw","database":"x?allowAllFiles=true","name":""}`,
		`{"driver":"mysql","host":"127.0.0.1","port":23306,"user":"a:b","password":"pw","database":"","name":""}`,
	} {
		if rec := do(t, r, http.MethodPost, "/databases/host", body); rec.Code != http.StatusBadRequest {
			t.Errorf("/host %s answered %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := do(t, r, http.MethodPost, "/databases/host/grant",
		`{"driver":"redis","host":"127.0.0.1","port":6379,"user":"","password":"pw","database":"0?x=y","name":"","superuser":null}`); rec.Code != http.StatusBadRequest {
		t.Errorf("/host/grant answered %d: %s", rec.Code, rec.Body.String())
	}

	// Every stored string, read the way the driver reads it, has no option set.
	rows, err := s.Store.DB.Query(`SELECT id FROM db_connections`)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 1 {
		t.Fatalf("%d connections, want only the honest container's", len(ids))
	}
	_, stored, err := s.dbConnRow(t.Context(), ids[0])
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mysql.ParseDSN(stored)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowAllFiles || cfg.AllowCleartextPasswords || cfg.Passwd != "p@ss/w?rd&allowAllFiles=true" || cfg.Addr != "127.0.0.1:23306" {
		t.Errorf("stored DSN reads as %+v", cfg)
	}
	for _, dsn := range m.dials() {
		if strings.Contains(dsn, ":13306") {
			t.Errorf("the container naming an unusable database was dialled: %q", dsn)
		}
	}
}

// The reconcile runs on every visit to the page. A server that refused what
// its container states is asked once, and told about from memory after.
func TestSyncDoesNotAskARefusedServerAgain(t *testing.T) {
	m := &fakeMachine{
		containers: []fakeContainer{
			{
				id: "ffffffffffff0003", name: "stale", image: "postgres:16", state: "running",
				env: []string{"POSTGRES_PASSWORD=stale-pw"}, ports: []fakePort{{5432, 45432, "127.0.0.1"}},
			},
			{
				id: "ffffffffffff0004", name: "vault", image: "postgres:16", state: "running",
				env: []string{"POSTGRES_PASSWORD_FILE=/run/secrets/pg"}, ports: []fakePort{{5432, 46432, "127.0.0.1"}},
			},
			{
				id: "ffffffffffff0005", name: "starting", image: "postgres:16", state: "running",
				env: []string{"POSTGRES_PASSWORD=fine"}, ports: []fakePort{{5432, 47432, "127.0.0.1"}},
			},
		},
		// A Redis on the host that turns out to want a password.
		listeners: []proxysvc.Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: 6379, PID: 70, Process: "redis-server"}},
		refuse: map[string]string{
			"stale-pw": `password authentication failed for user "postgres"`,
			"in-file":  `password authentication failed for user "postgres"`,
			":47432":   "dial tcp 127.0.0.1:47432: connect: connection refused",
			":6379":    "NOAUTH Authentication required.",
		},
	}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	reads := 0
	s.dbInventory.secret = func(context.Context, string, string) ([]byte, error) {
		reads++
		return []byte("in-file\n"), nil
	}
	sync := func() (map[string]string, []credentialServer) {
		t.Helper()
		rec := do(t, r, http.MethodPost, "/databases/sync", "{}")
		var out struct {
			Unreachable      []unreachableServer `json:"unreachable"`
			NeedsCredentials []credentialServer  `json:"needsCredentials"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		reasons := map[string]string{}
		for _, u := range out.Unreachable {
			reasons[u.Container] = u.Reason
		}
		return reasons, out.NeedsCredentials
	}
	countDials := func(fragment string) int {
		n := 0
		for _, dsn := range m.dials() {
			if strings.Contains(dsn, fragment) {
				n++
			}
		}
		return n
	}

	for range 5 {
		reasons, asks := sync()
		if !strings.Contains(reasons["stale"], "did not accept the credentials") || !strings.Contains(reasons["stale"], "password authentication failed") {
			t.Fatalf("stale: %q", reasons["stale"])
		}
		if !strings.Contains(reasons["vault"], "did not accept the credentials") {
			t.Fatalf("vault: %q", reasons["vault"])
		}
		// Not answering is not refusing the password, and is not said to be.
		if !strings.Contains(reasons["starting"], "did not answer") || strings.Contains(reasons["starting"], "did not accept") {
			t.Fatalf("starting: %q", reasons["starting"])
		}
		if len(asks) != 1 || asks[0].Port != 6379 {
			t.Fatalf("needsCredentials = %+v", asks)
		}
	}
	for fragment, want := range map[string]int{"stale-pw": 1, "in-file": 1, ":47432": 1, ":6379": 1} {
		if got := countDials(fragment); got != want {
			t.Errorf("%s was dialled %d times over five reconciles, want %d", fragment, got, want)
		}
	}
	if reads != 1 {
		t.Errorf("the secret file was read %d times, want once", reads)
	}

	// A server that did not answer is tried again once it has had time to start.
	s.dbInventory.mu.Lock()
	kept := s.dbInventory.refused["docker:starting"]
	kept.at = time.Now().Add(-2 * dbUnansweredRetry)
	s.dbInventory.refused["docker:starting"] = kept
	s.dbInventory.mu.Unlock()
	m.mu.Lock()
	delete(m.refuse, ":47432")
	m.mu.Unlock()
	sync()
	if _, connected := connectionRows(t, s)["starting"]; !connected || countDials(":47432") != 2 {
		t.Errorf("rows %v dials %d, want the server connected on its second try", connectionRows(t, s), countDials(":47432"))
	}

	// The container recreated with other credentials is another question.
	m.mu.Lock()
	m.containers[0].id, m.containers[0].env = "ffffffffffff00aa", []string{"POSTGRES_PASSWORD=right-pw"}
	m.mu.Unlock()
	sync()
	if _, connected := connectionRows(t, s)["stale"]; !connected || countDials("right-pw") != 1 {
		t.Errorf("rows %v, want the recreated container connected", connectionRows(t, s))
	}

	// And connecting by hand asks whatever was remembered.
	if rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:vault","password":"typed"}`); rec.Code != http.StatusCreated {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	s.dbInventory.mu.Lock()
	_, remembered := s.dbInventory.refused["docker:vault"]
	s.dbInventory.mu.Unlock()
	if remembered {
		t.Error("the refusal outlived a successful sign-in")
	}
}

// A password file that cannot be read is not read out of the container again
// on every visit either; it is tried again after a while, since a file can
// appear.
func TestSyncDoesNotRereadAnUnreadableSecret(t *testing.T) {
	m := &fakeMachine{containers: []fakeContainer{{
		id: "ffffffffffff000b", name: "vault", image: "postgres:16", state: "running",
		env: []string{"POSTGRES_PASSWORD_FILE=/run/secrets/pg"}, ports: []fakePort{{5432, 46432, "127.0.0.1"}},
	}}}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	reads := 0
	s.dbInventory.secret = func(context.Context, string, string) ([]byte, error) {
		reads++
		return nil, errors.New("no such file")
	}
	for range 3 {
		rec := do(t, r, http.MethodPost, "/databases/sync", "{}")
		if !strings.Contains(rec.Body.String(), "/run/secrets/pg inside the container, which could not be read") {
			t.Fatalf("sync = %s", rec.Body.String())
		}
	}
	if reads != 1 || len(m.dials()) != 0 {
		t.Errorf("the file was read %d times and %d sign-ins were sent, want one read and none", reads, len(m.dials()))
	}
	s.dbInventory.mu.Lock()
	kept := s.dbInventory.refused["docker:vault"]
	kept.at = time.Now().Add(-2 * dbUnansweredRetry)
	s.dbInventory.refused["docker:vault"] = kept
	s.dbInventory.mu.Unlock()
	s.dbInventory.secret = func(context.Context, string, string) ([]byte, error) { return []byte("now-there"), nil }
	do(t, r, http.MethodPost, "/databases/sync", "{}")
	if _, connected := connectionRows(t, s)["vault"]; !connected {
		t.Errorf("rows = %v, want the container connected once its file could be read", connectionRows(t, s))
	}
}

// The reconcile signs in to several servers at once, and a sign-in it did not
// get to is reported as that — never as a refusal by a server nobody asked.
func TestSyncSignInsAreBoundedAndHonestAboutTime(t *testing.T) {
	s := testServer(t)
	t.Cleanup(s.Shutdown)
	var (
		mu               sync.Mutex
		inFlight, widest int
	)
	s.dbInventory.dial = func(ctx context.Context, _ dbx.Driver, _ string) error {
		mu.Lock()
		inFlight++
		widest = max(widest, inFlight)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}
	attempts := func() []*syncAttempt {
		out := []*syncAttempt{}
		for i := range 12 {
			out = append(out, &syncAttempt{
				inst: &dbx.Instance{Key: fmt.Sprintf("docker:db%d", i), Source: dbx.SourceDocker, Credentials: dbx.CredentialsOpen},
				access: dbx.Access{Candidate: dbx.Candidate{
					Driver: dbx.DriverPostgres, Host: "127.0.0.1", Port: 40000 + i, User: "postgres", Database: "postgres",
				}},
			})
		}
		return out
	}
	list := attempts()
	s.signInAll(t.Context(), list)
	for _, a := range list {
		if a.dsn == "" || a.err != nil || a.unattempted {
			t.Fatalf("attempt %s = %+v", a.inst.Key, a)
		}
	}
	if widest < 2 || widest > syncDialWorkers {
		t.Errorf("%d sign-ins ran at once, want more than one and at most %d", widest, syncDialWorkers)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	list, dialled := attempts(), 0
	s.dbInventory.dial = func(context.Context, dbx.Driver, string) error { dialled++; return nil }
	s.signInAll(ctx, list)
	for _, a := range list {
		if !a.unattempted || a.err != nil || a.dsn != "" {
			t.Errorf("attempt %s after the deadline = %+v, want it marked as not tried", a.inst.Key, a)
		}
	}
	if dialled != 0 {
		t.Errorf("%d sign-ins were sent after the request's time ran out", dialled)
	}

	// The deadline passing under a dial is the clock's failure, not the server's.
	ctx, cancel = context.WithCancel(t.Context())
	s.dbInventory.dial = func(context.Context, dbx.Driver, string) error {
		cancel()
		return context.Canceled
	}
	list = attempts()[:1]
	s.signInAll(ctx, list)
	if !list[0].unattempted || list[0].err != nil {
		t.Errorf("attempt = %+v, want a sign-in cut short by the clock reported as not tried", list[0])
	}
}

func TestCredentialRefusalIsToldFromSilence(t *testing.T) {
	for message, want := range map[string]bool{
		`password authentication failed for user "postgres"`:             true,
		"Error 1045 (28000): Access denied for user 'root'@'172.18.0.1'": true,
		"WRONGPASS invalid username-password pair or user is disabled.":  true,
		"NOAUTH Authentication required.":                                true,
		"(Unauthorized) command listDatabases requires authentication":   true,
		"mssql: login error: Login failed for user 'sa'.":                true,
		"code: 516, message: default: Authentication failed":             true,
		"ORA-01017: invalid username/password; logon denied":             true,
		"dial tcp 127.0.0.1:5432: connect: connection refused":           false,
		"mssql: login error: Login failed for user 'sa'. Reason: Server is in script upgrade mode. Only administrator can connect at this time.": false,
		"context deadline exceeded": false,
		"ORA-12514: TNS:listener does not currently know of service requested":         false,
		"FATAL: the database system is starting up (SQLSTATE 57P03)":                   false,
		`FATAL: database "shop" does not exist (SQLSTATE 3D000)`:                       false,
		"read tcp 127.0.0.1:1->127.0.0.1:2: read: connection reset by peer":            false,
		"server selection error: context deadline exceeded, current topology: { ... }": false,
	} {
		if got := credentialRefusal(errors.New(message)); got != want {
			t.Errorf("credentialRefusal(%q) = %v", message, got)
		}
	}
	if !credentialRefusal(&pgconn.PgError{Code: "28P01", Message: "nope"}) || credentialRefusal(&pgconn.PgError{Code: "57P03", Message: "authentication failed"}) {
		t.Error("a PostgreSQL error is read by its code")
	}
	if !credentialRefusal(&mysql.MySQLError{Number: 1045, Message: "x"}) || credentialRefusal(&mysql.MySQLError{Number: 1049, Message: "Unknown database"}) {
		t.Error("a MySQL error is read by its number")
	}
	if credentialRefusal(nil) {
		t.Error("no error is not a refusal")
	}
}

// A found server that could not be signed in to for a reason other than its
// credentials is not answered with "type the password".
func TestConnectSaysWhenTheCredentialsAreNotTheProblem(t *testing.T) {
	m := &fakeMachine{
		containers: []fakeContainer{{
			id: "ffffffffffff0006", name: "xe", image: "container-registry.oracle.com/database/express:21.3.0-xe", state: "running",
			env: []string{"ORACLE_PWD=pw", "ORACLE_PDB=NOSUCH"}, ports: []fakePort{{1521, 11521, "127.0.0.1"}},
		}},
		refuse: map[string]string{"NOSUCH": "ORA-12514: TNS:listener does not currently know of service requested in connect descriptor"},
	}
	_, r := inventoryRouter(t, auth.RoleAdmin, m)
	rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:xe"}`)
	if rec.Code != http.StatusConflict || inventoryErrorCode(rec) != "sign_in_failed" {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "type the password") || !strings.Contains(rec.Body.String(), "not because of the credentials") {
		t.Errorf("connect = %s, want it to say the credentials are not what failed", rec.Body.String())
	}
	// Its own default service is the edition's, so a stock container signs in.
	m.mu.Lock()
	m.containers[0].env = []string{"ORACLE_PWD=pw"}
	m.mu.Unlock()
	rec = do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:xe"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	if dials := m.dials(); !strings.HasSuffix(dials[len(dials)-1], "/XEPDB1") {
		t.Errorf("dialled %q, want the Express edition's own pluggable database", dials[len(dials)-1])
	}
}

// Connecting what is already connected makes nothing, and can still change
// what the next reconcile does. Whatever it changed is in the audit trail.
func TestConnectingAgainRecordsWhatItChanged(t *testing.T) {
	m := ordinaryMachine()
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	if rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:shop-db"}`); rec.Code != http.StatusCreated {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body.String())
	}
	do(t, r, http.MethodPost, "/databases/inventory/ignore", `{"key":"docker:shop-db","ignored":true}`)
	if rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:shop-db"}`); rec.Code != http.StatusOK {
		t.Fatalf("second connect: %d %s", rec.Code, rec.Body.String())
	}
	if ignored, _ := s.ignoredOrigins(t.Context()); ignored["docker:shop-db"] {
		t.Error("the mark was not taken back")
	}
	want := []string{"database.inventory.connect", "database.inventory.ignore", "database.inventory.unignore"}
	if got := auditActions(t, s); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("audit trail = %v, want %v", got, want)
	}
	// A third time changes nothing, and records nothing.
	do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:shop-db"}`)
	if got := auditActions(t, s); len(got) != len(want) {
		t.Errorf("audit trail = %v after a connect that changed nothing", got)
	}

	// A connection typed by hand learns which server it is: once, and on record.
	sealed, err := s.Sealer.Seal("postgres://postgres:" + secretPassword + "@localhost:25432/postgres?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('by-hand','postgres',?,0)`, sealed); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"docker:fixture"}`); rec.Code != http.StatusOK {
		t.Fatalf("connect of a server a hand-typed connection covers: %d %s", rec.Code, rec.Body.String())
	}
	if rows := connectionRows(t, s); rows["by-hand"] != "docker:fixture" {
		t.Errorf("rows = %v", rows)
	}
	if got := auditActions(t, s); len(got) != len(want)+1 || got[len(got)-1] != "database.inventory.connect" {
		t.Errorf("audit trail = %v, want the link recorded", got)
	}
}

// The reconcile's own backfill of an origin is a change, and is recorded even
// when nothing was added.
func TestSyncRecordsAConnectionLearningItsServer(t *testing.T) {
	m := &fakeMachine{containers: []fakeContainer{{
		id: "ffffffffffff0007", name: "db", image: "postgres:16", state: "running",
		env: []string{"POSTGRES_PASSWORD=pw"}, ports: []fakePort{{5432, 15432, "127.0.0.1"}},
	}}}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	sealed, err := s.Sealer.Seal("postgres://postgres:pw@localhost:15432/postgres")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('by-hand','postgres',?,0)`, sealed); err != nil {
		t.Fatal(err)
	}
	do(t, r, http.MethodPost, "/databases/sync", "{}")
	if rows := connectionRows(t, s); rows["by-hand"] != "docker:db" {
		t.Fatalf("rows = %v", rows)
	}
	if got := auditActions(t, s); len(got) != 1 || got[0] != "database.connection.sync" {
		t.Fatalf("audit trail = %v, want the backfill recorded", got)
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log`).Scan(&detail); err != nil || !strings.Contains(detail, `"linked":["by-hand"]`) {
		t.Errorf("detail = %q (%v)", detail, err)
	}
	do(t, r, http.MethodPost, "/databases/sync", "{}")
	if got := auditActions(t, s); len(got) != 1 {
		t.Errorf("audit trail = %v after a reconcile that changed nothing", got)
	}
	if len(m.dials()) != 0 {
		t.Errorf("dials = %v; a server that is already connected is not signed in to", m.dials())
	}
}

// The legacy listing has no way to say "this is a guess", so it does not list
// one: every row in it reads as a server that was detected.
func TestDetectedLeavesOutAGuess(t *testing.T) {
	m := &fakeMachine{containers: []fakeContainer{
		{
			id: "ffffffffffff0008", name: "guess", image: "acme/thing:1", state: "running",
			entrypoint: []string{"/start"}, ports: []fakePort{{5432, 55432, "127.0.0.1"}},
		},
		{
			id: "ffffffffffff0009", name: "real", image: "postgres:16", state: "running",
			env: []string{"POSTGRES_PASSWORD=pw"}, ports: []fakePort{{5432, 15432, "127.0.0.1"}},
		},
	}}
	_, r := inventoryRouter(t, auth.RoleAdmin, m)
	var out detectedResponse
	rec := do(t, r, http.MethodGet, "/databases/detected", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Servers) != 1 || out.Servers[0].Container != "real" {
		t.Errorf("detected = %s", rec.Body.String())
	}
	inv := decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	if got := inventoryInstance(t, inv, "docker:guess"); got.Confidence != dbx.ConfidencePort {
		t.Errorf("the inventory lost the guess: %+v", got)
	}
}

// A `docker compose run` container carries its service's labels. It takes
// neither the service's key nor the address the service is dialled at.
func TestAComposeRunContainerIsNotItsService(t *testing.T) {
	labels := func(oneoff string) map[string]string {
		return map[string]string{
			"com.docker.compose.project": "shop", "com.docker.compose.service": "db",
			"com.docker.compose.container-number": "1", "com.docker.compose.oneoff": oneoff,
		}
	}
	m := &fakeMachine{containers: []fakeContainer{
		{
			id: "ffffffffffff0010", name: "shop-db-run-1a2b3c", image: "postgres:16", state: "running",
			env: []string{"POSTGRES_PASSWORD=pw"}, entrypoint: []string{"docker-entrypoint.sh"}, cmd: []string{"psql", "-h", "db"},
			labels: labels("True"), ip: "172.18.0.7",
		},
		{
			id: "ffffffffffff0011", name: "shop-db-1", image: "postgres:16", state: "running",
			env: []string{"POSTGRES_PASSWORD=pw"}, entrypoint: []string{"docker-entrypoint.sh"}, cmd: []string{"postgres"},
			labels: labels("False"), ports: []fakePort{{5432, 15432, "127.0.0.1"}}, ip: "172.18.0.2",
		},
	}}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	inv := decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	servers := []string{}
	for _, inst := range inv.Instances {
		if inst.Kind == dbx.KindServer {
			servers = append(servers, inst.Key)
		}
	}
	if strings.Join(servers, ",") != "compose:shop/db" {
		t.Fatalf("servers = %v, want the service and nothing for its one-off", servers)
	}
	if rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"compose:shop/db"}`); rec.Code != http.StatusCreated {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	if dials := m.dials(); len(dials) != 1 || !strings.Contains(dials[0], "127.0.0.1:15432") {
		t.Errorf("dials = %v, want the service's own published port", dials)
	}
	if rows := connectionRows(t, s); rows["shop-db-1"] != "compose:shop/db" {
		t.Errorf("rows = %v", rows)
	}
	// Adopting the service by its container name finds the same instance.
	if rec := do(t, r, http.MethodPost, "/databases/adopt", `{"container":"shop-db-1"}`); rec.Code != http.StatusOK {
		t.Errorf("adopt = %d %s", rec.Code, rec.Body.String())
	}
}

// A server that is installed, stopped and disabled is in no listing of units:
// systemd unloads it. The unit files, and for Debian's PostgreSQL the cluster
// directories, are what say it is there.
func TestInstalledAndDisabledServersAreListed(t *testing.T) {
	m := &fakeMachine{
		units: []procs.Unit{
			{Name: "nginx.service", LoadState: "loaded", ActiveState: "active", SubState: "running"},
			{Name: "mongod.service", LoadState: "loaded", ActiveState: "failed", SubState: "failed"},
		},
		unitFiles: map[string]string{
			"redis-server.service": "disabled",
			// Another name for the unit above, not a second server.
			"redis.service": "alias",
			// A template with no instance, a unit that cannot be started.
			"mysql@.service":      "disabled",
			"mariadb.service":     "masked",
			"postgresql.service":  "enabled",
			"postgresql@.service": "indirect",
			"cron.service":        "enabled",
		},
	}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)
	etc := filepath.Join(s.Cfg.FileRoots[0], "etc", "postgresql")
	for cluster, conf := range map[string]string{"16/main": "port = 5433\ndata_directory = '/var/lib/postgresql/16/main'\n", "17/reports": "port = 5434\n"} {
		if err := os.MkdirAll(filepath.Join(etc, cluster), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(etc, cluster, "postgresql.conf"), []byte(conf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A version directory with no cluster in it is not one.
	if err := os.MkdirAll(filepath.Join(etc, "15"), 0o755); err != nil {
		t.Fatal(err)
	}

	inv := decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	redis := inventoryInstance(t, inv, "host:redis-server.service")
	if redis.State != dbx.StateInactive || redis.Connectable || redis.Host.Enabled || !strings.Contains(redis.Reason, "not running") {
		t.Errorf("the disabled, stopped server = %+v host %+v", redis, redis.Host)
	}
	main := inventoryInstance(t, inv, "host:postgresql@16-main.service")
	if main.State != dbx.StateInactive || main.Version != "16" || !main.Host.Enabled || main.Host.DataDir != "/var/lib/postgresql/16/main" ||
		len(main.Endpoints) != 1 || main.Endpoints[0].Port != 5433 {
		t.Errorf("the stopped cluster = %+v host %+v", main, main.Host)
	}
	if reports := inventoryInstance(t, inv, "host:postgresql@17-reports.service"); reports.Endpoints[0].Port != 5434 {
		t.Errorf("the second cluster = %+v", reports)
	}
	if failed := inventoryInstance(t, inv, "host:mongod.service"); failed.State != dbx.StateFailed {
		t.Errorf("the loaded, failed unit = %+v", failed)
	}
	for _, key := range []string{"host:redis.service", "host:mysql@.service", "host:mariadb.service", "host:postgresql.service", "host:postgresql@.service", "host:postgresql@15-.service"} {
		if inventoryHas(inv, key) {
			t.Errorf("%s was listed as a server", key)
		}
	}
}

// Two servers one supervisor starts are both read under the supervisor's unit.
// Each is keyed by where it listens, so the connection and the ignore mark
// recorded against one are still that one's after the machine restarts and
// their process ids come back the other way round.
func TestServersUnderOneSupervisorKeepTheirOwnKeys(t *testing.T) {
	supervised := func(redisPID, mongoPID int32) []proxysvc.Listener {
		return []proxysvc.Listener{
			{Protocol: "tcp", Address: "127.0.0.1", Port: 6379, PID: redisPID, Process: "redis-server", Manager: "systemd", ManagerName: "supervisor.service"},
			{Protocol: "tcp", Address: "127.0.0.1", Port: 27017, PID: mongoPID, Process: "mongod", Manager: "systemd", ManagerName: "supervisor.service"},
		}
	}
	m := &fakeMachine{
		listeners: supervised(100, 200),
		units:     []procs.Unit{{Name: "supervisor.service", LoadState: "loaded", ActiveState: "active", SubState: "running"}},
	}
	s, r := inventoryRouter(t, auth.RoleAdmin, m)

	if rec := do(t, r, http.MethodPost, "/databases/inventory/connect", `{"key":"host:redis:6379","name":"queue"}`); rec.Code != http.StatusCreated {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	if dials := m.dials(); len(dials) != 1 || !strings.Contains(dials[0], "127.0.0.1:6379") {
		t.Fatalf("connecting the Redis signed in to %v", dials)
	}
	if rec := do(t, r, http.MethodPost, "/databases/inventory/ignore", `{"key":"host:mongodb:27017","ignored":true}`); rec.Code != http.StatusOK {
		t.Fatalf("ignore = %d %s", rec.Code, rec.Body.String())
	}
	if rows := connectionRows(t, s); rows["queue"] != "host:redis:6379" {
		t.Fatalf("connections = %v", rows)
	}

	// The machine restarts.
	m.listeners = supervised(200, 100)
	s.dropInventory()
	inv := decodeInventory(t, do(t, r, http.MethodGet, "/databases/inventory", ""))
	redis := inventoryInstance(t, inv, "host:redis:6379")
	if redis.Engine != "redis" || len(redis.Connections) != 1 || redis.Ignored || redis.Host.Unit != "" {
		t.Errorf("the Redis after a restart = %+v host %+v", redis, redis.Host)
	}
	mongo := inventoryInstance(t, inv, "host:mongodb:27017")
	if mongo.Engine != "mongodb" || len(mongo.Connections) != 0 || !mongo.Ignored {
		t.Errorf("the MongoDB after a restart = %+v", mongo)
	}
	if inventoryHas(inv, "host:supervisor.service") || inventoryHas(inv, "host:supervisor.service#2") {
		t.Error("a server was keyed by the supervisor's unit")
	}

	// The reconcile leaves both alone: one is connected, one is ignored.
	before := len(m.dials())
	rec := do(t, r, http.MethodPost, "/databases/sync", "{}")
	if len(m.dials()) != before || !strings.Contains(rec.Body.String(), `"ignored":["host:mongodb:27017"]`) ||
		!strings.Contains(rec.Body.String(), `"already":["queue"]`) {
		t.Errorf("sync after a restart dialled %d more and answered %s", len(m.dials())-before, rec.Body.String())
	}
}

// pingOnlyMongo is a MongoDB that completes the handshake, answers a ping and
// refuses every other command for want of an account: what a mongod with
// access control on does to a connection that names nobody. It speaks just
// enough of the wire protocol for the real driver to talk to it.
func pingOnlyMongo(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	const opReply, opQuery, opMsg = 1, 2004, 2013
	serve := func(conn net.Conn) {
		defer conn.Close()
		for {
			header := make([]byte, 16)
			if _, err := io.ReadFull(conn, header); err != nil {
				return
			}
			length, opcode := int(binary.LittleEndian.Uint32(header)), binary.LittleEndian.Uint32(header[12:])
			if length < 16 || length > 1<<20 {
				return
			}
			body := make([]byte, length-16)
			if _, err := io.ReadFull(conn, body); err != nil {
				return
			}
			switch opcode {
			case opQuery:
				// Flags, the collection's name, skip and limit, then the command.
				body = body[4:]
				body = body[bytes.IndexByte(body, 0)+9:]
			case opMsg:
				// Flags and the one section's kind, then the command.
				body = body[5:]
			default:
				return
			}
			elements, err := bson.Raw(body[:binary.LittleEndian.Uint32(body)]).Elements()
			if err != nil || len(elements) == 0 {
				return
			}
			var answer bson.D
			switch name := elements[0].Key(); strings.ToLower(name) {
			case "hello", "ismaster":
				answer = bson.D{
					{Key: "ismaster", Value: true}, {Key: "isWritablePrimary", Value: true}, {Key: "helloOk", Value: true},
					{Key: "maxBsonObjectSize", Value: int32(16 << 20)}, {Key: "maxMessageSizeBytes", Value: int32(48_000_000)},
					{Key: "maxWriteBatchSize", Value: int32(100_000)}, {Key: "minWireVersion", Value: int32(0)},
					{Key: "maxWireVersion", Value: int32(21)}, {Key: "ok", Value: 1.0},
				}
			case "ping":
				answer = bson.D{{Key: "ok", Value: 1.0}}
			default:
				answer = bson.D{
					{Key: "ok", Value: 0.0}, {Key: "errmsg", Value: "command " + name + " requires authentication"},
					{Key: "code", Value: int32(13)}, {Key: "codeName", Value: "Unauthorized"},
				}
			}
			doc, err := bson.Marshal(answer)
			if err != nil {
				return
			}
			var reply []byte
			if opcode == opQuery {
				reply = make([]byte, 36, 36+len(doc))
				binary.LittleEndian.PutUint32(reply[12:], opReply)
				binary.LittleEndian.PutUint32(reply[32:], 1)
			} else {
				reply = make([]byte, 21, 21+len(doc))
				binary.LittleEndian.PutUint32(reply[12:], opMsg)
			}
			reply = append(reply, doc...)
			binary.LittleEndian.PutUint32(reply, uint32(len(reply)))
			copy(reply[8:12], header[4:8])
			if _, err := conn.Write(reply); err != nil {
				return
			}
		}
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(conn)
		}
	}()
	return "mongodb://" + ln.Addr().String() + "/admin"
}

// A MongoDB answers a ping from anybody. Signing in to one with no account is
// therefore proved by a command it refuses unless it really is open — or a
// server with access control on is saved as a connection that then fails
// everything asked of it.
func TestMongoSignInIsNotAPing(t *testing.T) {
	s := testServer(t)
	t.Cleanup(s.Shutdown)
	uri := pingOnlyMongo(t)

	// The driver's own connection check passes: this is the server the old
	// sign-in called connected.
	client, err := dbx.MongoClient(t.Context(), uri)
	if err != nil {
		t.Fatalf("the ping itself failed, so this proves nothing: %v", err)
	}
	_ = client.Disconnect(context.Background())

	err = s.probeConnection(t.Context(), dbx.DriverMongo, uri)
	if err == nil {
		t.Fatal("a server that answers only a ping was signed in to with no account")
	}
	if !strings.Contains(err.Error(), "requires authentication") || !credentialRefusal(err) {
		t.Errorf("err = %v, want the server's own refusal, read as one", err)
	}

	for uri, nobody := range map[string]bool{
		"mongodb://127.0.0.1:27017/admin":                       true,
		"mongodb://127.0.0.1:27017":                             true,
		"mongodb://h1:27017,h2:27017/app?replicaSet=rs0":        true,
		"mongodb://:pw@127.0.0.1:27017/admin":                   true,
		"mongodb://root:pw@127.0.0.1:27017/admin":               false,
		"mongodb://app@h1:27017,h2:27017/app?replicaSet=rs0":    false,
		"mongodb+srv://app:p%40ss@cluster.example.net/app?w=1":  false,
		"mongodb://127.0.0.1:27017/app?appName=someone@example": true,
	} {
		if got := mongoURINamesNobody(uri); got != nobody {
			t.Errorf("mongoURINamesNobody(%q) = %v", uri, got)
		}
	}
}
