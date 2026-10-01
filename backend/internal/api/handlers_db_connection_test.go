package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// --- a machine of the test's own ----------------------------------------------
//
// Where a server runs is read off Docker and off the host's sockets, and a
// test that read the real ones would be a test of whatever this machine
// happens to be running. These two stand in for them: an Engine API that
// lists the containers it was given and records what it was asked to do to
// them, and a probe that reports the listeners and units it was handed.

type fakeDBContainer struct {
	id, name, image, state string
	hostIP                 string
	hostPort, port         int
	labels                 map[string]string
}

type fakeDockerEngine struct {
	mu         sync.Mutex
	containers []*fakeDBContainer
	calls      []string
	// graces is the t= each stop and restart was sent with, and inspections
	// how many times each container's configuration was asked for.
	graces      []string
	inspections map[string]int
}

var dockerAPIVersion = regexp.MustCompile(`^/v[0-9.]+`)

func (f *fakeDockerEngine) find(ref string) *fakeDBContainer {
	for _, c := range f.containers {
		if c.id == ref || c.name == ref {
			return c
		}
	}
	return nil
}

func (f *fakeDockerEngine) did() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeDockerEngine) stopGraces() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.graces...)
}

func (f *fakeDockerEngine) inspected(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inspections[name]
}

func (f *fakeDockerEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := dockerAPIVersion.ReplaceAllString(r.URL.Path, "")
	binding := func(c *fakeDBContainer) map[string]any {
		return map[string]any{strconv.Itoa(c.port) + "/tcp": []map[string]string{
			{"HostIp": c.hostIP, "HostPort": strconv.Itoa(c.hostPort)},
		}}
	}
	switch {
	case path == "/_ping":
		w.Header().Set("API-Version", "1.47")
	case path == "/containers/json":
		all := r.URL.Query().Get("all") == "1" || r.URL.Query().Get("all") == "true"
		out := []map[string]any{}
		for _, c := range f.containers {
			if c.state != "running" && c.state != "paused" && !all {
				continue
			}
			item := map[string]any{
				"Id": c.id, "Names": []string{"/" + c.name}, "Image": c.image,
				"State": c.state, "Status": c.state, "Labels": c.labels, "Ports": []any{},
			}
			// The Engine lists a binding only while the container runs.
			if c.state == "running" || c.state == "paused" {
				item["Ports"] = []map[string]any{
					{"IP": c.hostIP, "PrivatePort": c.port, "PublicPort": c.hostPort, "Type": "tcp"},
				}
			}
			out = append(out, item)
		}
		_ = json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
		c := f.find(strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json"))
		if c == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such container"}`))
			return
		}
		if f.inspections == nil {
			f.inspections = map[string]int{}
		}
		f.inspections[c.name]++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Id": c.id, "Name": "/" + c.name,
			"Config": map[string]any{"Image": c.image, "Labels": c.labels},
			"State": map[string]any{
				"Status": c.state, "Running": c.state == "running", "StartedAt": "2026-01-02T03:04:05Z",
			},
			"HostConfig":      map[string]any{"NetworkMode": "bridge", "PortBindings": binding(c)},
			"NetworkSettings": map[string]any{"Ports": binding(c)},
		})
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/containers/"):
		ref, verb, _ := strings.Cut(strings.TrimPrefix(path, "/containers/"), "/")
		c := f.find(ref)
		if c == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such container"}`))
			return
		}
		f.calls = append(f.calls, verb+" "+c.name)
		if verb == "stop" || verb == "restart" {
			f.graces = append(f.graces, r.URL.Query().Get("t"))
		}
		switch verb {
		case "start", "restart":
			c.state = "running"
		case "stop":
			c.state = "exited"
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not served by the test engine"}`))
	}
}

// connHarness is a server whose Docker and whose host are the test's.
type connHarness struct {
	t         *testing.T
	s         *Server
	engine    *fakeDockerEngine
	listeners []proxysvc.Listener
	units     []procs.Unit
	// unitLists counts how often the machine's units were asked for.
	unitLists atomic.Int64

	// The host's systemctl: whether there is one, what it was asked, and what
	// it answers. A verb in systemctlFails fails with that output.
	noSystemctl    bool
	systemctlMu    sync.Mutex
	systemctlCalls []string
	systemctlFails map[string]string
}

func (h *connHarness) systemctlDid() []string {
	h.systemctlMu.Lock()
	defer h.systemctlMu.Unlock()
	return append([]string(nil), h.systemctlCalls...)
}

// systemctl stands in for the host's: it records the verb and the unit, and
// keeps the unit list in step so that what was stopped then reads as stopped.
func (h *connHarness) systemctl(_ context.Context, verb, unit string) (string, error) {
	h.systemctlMu.Lock()
	defer h.systemctlMu.Unlock()
	h.systemctlCalls = append(h.systemctlCalls, verb+" "+unit)
	if output, fails := h.systemctlFails[verb]; fails {
		return output, errors.New("exit status 1")
	}
	state := map[string]string{"start": "active", "restart": "active", "stop": "inactive"}[verb]
	for i := range h.units {
		if h.units[i].Name != unit {
			continue
		}
		if state != "" {
			h.units[i].ActiveState = state
		}
		return h.units[i].ActiveState + "\n", nil
	}
	return state + "\n", nil
}

func newConnHarness(t *testing.T) *connHarness {
	t.Helper()
	h := &connHarness{t: t, s: testServer(t), engine: &fakeDockerEngine{}}
	srv := httptest.NewServer(h.engine)
	t.Cleanup(srv.Close)
	h.s.modules.docker = dockerx.New(srv.URL)
	t.Cleanup(func() { _ = h.s.modules.docker.Close() })
	h.s.Cfg.BackupLocalDir = t.TempDir()
	h.s.dbConns.probe = &dbHostProbe{
		listeners: func(context.Context) ([]proxysvc.Listener, error) { return h.listeners, nil },
		managerOf: func(int32, string) (string, string) { return "unmanaged", "" },
		systemd:   func() bool { return true },
		units: func(context.Context) ([]procs.Unit, error) {
			h.unitLists.Add(1)
			return h.units, nil
		},
		proc:   t.TempDir(),
		logDir: t.TempDir(),
	}
	h.s.dbConns.systemctl = &dbSystemctl{
		available: func() bool { return !h.noSystemctl },
		run:       h.systemctl,
	}
	return h
}

// as is the database routes behind a principal of the given role.
func (h *connHarness) as(role auth.Role) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: "tester"},
				Role: role, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	h.s.mountDatabaseRoutes(r)
	return r
}

func (h *connHarness) add(name string, driver dbx.Driver, dsn string) int64 {
	h.t.Helper()
	sealed, err := h.s.Sealer.Seal(dsn)
	if err != nil {
		h.t.Fatal(err)
	}
	res, err := h.s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		name, string(driver), sealed, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// deafPort is a port that accepts a connection and says nothing, counting
// how many it was offered: the difference between "was dialled" and "was not"
// that the fleet's promises are about.
func deafPort(t *testing.T) (int, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var dials atomic.Int64
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			dials.Add(1)
			conn.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, &dials
}

func readJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not the JSON expected: %v: %s", err, rec.Body.String())
	}
	return out
}

type summaryView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	ReadOnly    bool   `json:"readOnly"`
	Notes       string `json:"notes"`
	Broken      bool   `json:"broken"`
	Reason      string `json:"brokenReason"`
	Flavor      string `json:"flavor"`
	State       string `json:"state"`
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	Source      string `json:"source"`
	Unit        *struct {
		Name        string `json:"name"`
		ActiveState string `json:"activeState"`
	} `json:"unit"`
	Container *struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"container"`
	File *struct {
		Path string `json:"path"`
	} `json:"file"`
	Power struct {
		Via     string `json:"via"`
		Start   bool   `json:"start"`
		Stop    bool   `json:"stop"`
		Restart bool   `json:"restart"`
		Reason  string `json:"reason"`
	} `json:"power"`
	Exposure     string         `json:"exposure"`
	Capabilities map[string]any `json:"capabilities"`
}

// --- the connection record -----------------------------------------------------

// A connection the dashboard cannot open is still one the operator has to be
// able to see, read the reason for, repair and forget. It used to be dropped
// from the list, which left it in the table with no route that could reach it.
func TestBrokenConnectionsAreListedAndCanBeRepairedOrForgotten(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	good := h.add("good", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "good.db"))
	outside := h.add("outside", dbx.DriverSQLite, filepath.Join(filepath.Dir(h.s.Cfg.FileRoots[0]), "escaped.db"))
	res, err := h.s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('unsealable','postgres','not-a-sealed-value',0)`)
	if err != nil {
		t.Fatal(err)
	}
	unsealable, _ := res.LastInsertId()

	list := readJSON[[]summaryView](t, do(t, router, http.MethodGet, "/databases/", ""))
	if len(list) != 3 {
		t.Fatalf("the list has %d connections, want all 3: %+v", len(list), list)
	}
	byID := map[int64]summaryView{}
	for _, c := range list {
		byID[c.ID] = c
	}
	if byID[good].Broken {
		t.Errorf("a usable connection is flagged broken: %+v", byID[good])
	}
	if !byID[outside].Broken || !strings.Contains(byID[outside].Reason, "outside") {
		t.Errorf("a SQLite file outside the roots is not flagged with why: %+v", byID[outside])
	}
	if !byID[unsealable].Broken || !strings.Contains(byID[unsealable].Reason, "JD_MASTER_KEY") {
		t.Errorf("a DSN that does not unseal is not flagged with why: %+v", byID[unsealable])
	}

	// Its own page answers, and says what is wrong rather than failing.
	rec := do(t, router, http.MethodGet, pathf("/databases/%d", unsealable), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("summary of a broken connection = %d %s", rec.Code, rec.Body.String())
	}
	if got := readJSON[summaryView](t, rec); got.State != dbStateBroken || got.Error == "" || got.Power.Reason == "" {
		t.Errorf("summary of a broken connection = %+v", got)
	}

	// A new DSN repairs it, through the same route that edits any other.
	rec = do(t, router, http.MethodPut, pathf("/databases/%d", unsealable),
		`{"dsn":"postgres://app:pw@127.0.0.1:1/shop"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("repairing a broken connection = %d %s", rec.Code, rec.Body.String())
	}
	if got := readJSON[summaryView](t, rec); got.Broken {
		t.Errorf("a connection given a new DSN is still broken: %+v", got)
	}

	// And one that cannot be repaired can be forgotten.
	if rec = do(t, router, http.MethodDelete, pathf("/databases/%d", outside), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("forgetting a broken connection = %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, router, http.MethodGet, pathf("/databases/%d", outside), ""); rec.Code != http.StatusNotFound {
		t.Errorf("a forgotten connection still answers: %d", rec.Code)
	}
}

// The three labels are changed one at a time: a request that names one leaves
// the others as they were, and a rename does not erase the notes.
func TestConnectionLabelsAreEditedIndependently(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	id := h.add("orders", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "orders.db"))
	path := pathf("/databases/%d", id)

	rec := do(t, router, http.MethodPut, path, `{"environment":"production","readOnly":true,"notes":"the one that matters"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setting labels = %d %s", rec.Code, rec.Body.String())
	}
	got := readJSON[summaryView](t, rec)
	if got.Environment != "production" || !got.ReadOnly || got.Notes != "the one that matters" || got.Name != "orders" {
		t.Fatalf("labels after the first edit = %+v", got)
	}

	got = readJSON[summaryView](t, do(t, router, http.MethodPut, path, `{"name":"orders-db"}`))
	if got.Name != "orders-db" || got.Environment != "production" || !got.ReadOnly || got.Notes == "" {
		t.Errorf("a rename changed the labels: %+v", got)
	}
	got = readJSON[summaryView](t, do(t, router, http.MethodPut, path, `{"readOnly":false}`))
	if got.ReadOnly || got.Environment != "production" || got.Notes == "" {
		t.Errorf("turning protection off changed the other labels: %+v", got)
	}
	got = readJSON[summaryView](t, do(t, router, http.MethodPut, path, `{"environment":"","notes":""}`))
	if got.Environment != "" || got.Notes != "" {
		t.Errorf("empty labels did not clear: %+v", got)
	}

	for _, body := range []string{
		`{"environment":"prod/../etc"}`,
		`{"environment":"` + strings.Repeat("x", 40) + `"}`,
		`{"notes":"` + strings.Repeat("n", maxConnNotes+1) + `"}`,
		`{"name":"bad/name"}`,
	} {
		if rec := do(t, router, http.MethodPut, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %.40s = %d, want 400", body, rec.Code)
		}
	}

	// The list carries them, which is where the control center reads them.
	do(t, router, http.MethodPut, path, `{"environment":"staging","readOnly":true}`)
	list := readJSON[[]summaryView](t, do(t, router, http.MethodGet, "/databases/", ""))
	if len(list) != 1 || list[0].Environment != "staging" || !list[0].ReadOnly {
		t.Errorf("the list does not carry the labels: %+v", list)
	}
}

// A connection saved under an older naming rule keeps its name through an
// edit that does not touch it. It used to refuse every change, including the
// rename that would have fixed it only if the operator knew to send one.
func TestALegacyNameSurvivesAnEditThatLeavesItAlone(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	id := h.add("main · reports", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "r.db"))
	rec := do(t, router, http.MethodPut, pathf("/databases/%d", id), `{"environment":"production"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("labelling a connection with a legacy name = %d %s", rec.Code, rec.Body.String())
	}
	if rec = do(t, router, http.MethodPut, pathf("/databases/%d", id), `{"name":"also · bad"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("a new name outside the rule was accepted: %d", rec.Code)
	}
}

func TestSiblingConnectionNamesFollowTheNameRule(t *testing.T) {
	for _, c := range []struct{ parent, database, want string }{
		{"shop", "reports", "shop.reports"},
		{strings.Repeat("p", 64), "reports", strings.Repeat("p", 52) + ".reports"},
		{"old · name", "reports", "reports"},
		{"old · name", "_private", "db._private"},
	} {
		got := siblingConnectionName(c.parent, c.database)
		if got != c.want || !connNameRe.MatchString(got) {
			t.Errorf("siblingConnectionName(%q, %q) = %q, want %q within the name rule", c.parent, c.database, got, c.want)
		}
		// A second and a hundredth sibling of the same name still fit.
		taken := map[string]string{got: got}
		for n := 2; n < 100; n++ {
			next := uniqueConnectionName(got, taken)
			if !connNameRe.MatchString(next) {
				t.Fatalf("the %dth sibling %q breaks the name rule", n, next)
			}
			taken[next] = next
		}
	}
}

// Dialling before saving is opt-in, and a server that does not answer is then
// not saved at all.
func TestCreateWithProbeRefusesAServerThatDoesNotAnswer(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	const closed = `"driver":"postgres","dsn":"postgres://app:pw@127.0.0.1:1/shop?sslmode=disable"`

	rec := do(t, router, http.MethodPost, "/databases/", `{"name":"probed",`+closed+`,"probe":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a probed create against a closed port = %d %s", rec.Code, rec.Body.String())
	}
	var rows int
	_ = h.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_connections`).Scan(&rows)
	if rows != 0 {
		t.Errorf("a connection that failed its probe was saved")
	}

	rec = do(t, router, http.MethodPost, "/databases/",
		`{"name":"unprobed",`+closed+`,"environment":"staging","readOnly":true,"notes":"n"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("an unprobed create = %d %s", rec.Code, rec.Body.String())
	}
	if got := readJSON[summaryView](t, rec); got.Environment != "staging" || !got.ReadOnly || got.Notes != "n" {
		t.Errorf("labels given at creation were not saved: %+v", got)
	}
}

// Redis has no SQL dialect, and the test button used to report it unreachable
// for that reason rather than for anything the server said.
func TestConnectionTestAsksEveryEngineInItsOwnWords(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	for _, body := range []string{
		`{"driver":"redis","dsn":"redis://127.0.0.1:1/0"}`,
		`{"driver":"postgres","dsn":"postgres://app:pw@127.0.0.1:1/shop?sslmode=disable"}`,
	} {
		rec := do(t, router, http.MethodPost, "/databases/test", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("test %s = %d %s", body, rec.Code, rec.Body.String())
		}
		got := readJSON[struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}](t, rec)
		if got.OK || got.Error == "" || strings.Contains(got.Error, "unsupported") {
			t.Errorf("test %s = %+v, want the engine's own refusal", body, got)
		}
	}
}

// --- where it runs, and its power -----------------------------------------------

func TestSummaryOfAFileConnection(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleReadOnly)
	path := filepath.Join(h.s.Cfg.FileRoots[0], "app.db")
	id := h.add("app", dbx.DriverSQLite, path)

	rec := do(t, router, http.MethodGet, pathf("/databases/%d", id), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("summary = %d %s", rec.Code, rec.Body.String())
	}
	got := readJSON[summaryView](t, rec)
	if got.Source != "file" || got.File == nil || got.File.Path != path {
		t.Errorf("a SQLite connection is not reported as its file: %+v", got)
	}
	if got.State != dbStateRunning || !got.OK || got.Flavor != dbx.FlavorSQLite {
		t.Errorf("state = %q ok=%v flavour=%q (%s)", got.State, got.OK, got.Flavor, got.Error)
	}
	if got.Power.Via != "" || got.Power.Start || got.Power.Stop || got.Power.Restart || got.Power.Reason == "" {
		t.Errorf("a file was offered power controls: %+v", got.Power)
	}
	if got.Capabilities["sql"] != true || got.Capabilities["server"] != false {
		t.Errorf("capabilities = %v", got.Capabilities)
	}
	if rec = do(t, router, http.MethodGet, "/databases/999", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a connection that does not exist = %d, want 404", rec.Code)
	}
}

// A stopped container says it is stopped, and that is the whole answer: the
// port is not dialled to find out again, which would spend the request's
// budget waiting on a server everyone already knows is down.
func TestAStoppedContainerIsReportedAndNotDialled(t *testing.T) {
	h := newConnHarness(t)
	port, dials := deafPort(t)
	h.engine.containers = []*fakeDBContainer{{
		id: "c0ffee", name: "cache", image: "redis:7-alpine", state: "exited",
		hostIP: "127.0.0.1", hostPort: port, port: 6379,
		labels: map[string]string{"com.docker.compose.project": "shop"},
	}}
	id := h.add("session-store", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	admin := h.as(auth.RoleAdmin)

	got := readJSON[summaryView](t, do(t, admin, http.MethodGet, pathf("/databases/%d", id), ""))
	if got.Source != "docker" || got.Container == nil || got.Container.Name != "cache" || got.Container.State != "exited" {
		t.Fatalf("the stopped container was not found behind the connection: %+v", got)
	}
	if got.State != dbStateStopped || got.OK || got.Error != "" {
		t.Errorf("state = %q ok=%v error=%q, want stopped with nothing to report", got.State, got.OK, got.Error)
	}
	if got.Power.Via != "docker" || !got.Power.Start || got.Power.Stop || got.Power.Restart {
		t.Errorf("power for a stopped container = %+v", got.Power)
	}

	fleet := readJSON[struct {
		Connections []struct {
			ID        int64  `json:"id"`
			State     string `json:"state"`
			OK        bool   `json:"ok"`
			Source    string `json:"source"`
			Container string `json:"container"`
			Flavor    string `json:"flavor"`
		} `json:"connections"`
	}](t, do(t, admin, http.MethodGet, "/databases/fleet", ""))
	if len(fleet.Connections) != 1 || fleet.Connections[0].State != dbStateStopped ||
		fleet.Connections[0].Container != "cache" || fleet.Connections[0].Flavor != dbx.FlavorRedis {
		t.Errorf("fleet entry for a stopped container = %+v", fleet.Connections)
	}
	if n := dials.Load(); n != 0 {
		t.Fatalf("a stopped container's port was dialled %d times", n)
	}

	// Starting it is service.control's to do, and it is the container that is
	// started — compose-owned or not, as on the Docker page.
	rec := do(t, h.as(auth.RoleLimited), http.MethodPost, pathf("/databases/%d/power", id), `{"action":"start"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	result := readJSON[map[string]string](t, rec)
	if result["via"] != "docker" || result["target"] != "cache" || result["state"] != "running" {
		t.Errorf("start result = %v", result)
	}
	if calls := h.engine.did(); len(calls) != 1 || calls[0] != "start cache" {
		t.Errorf("Docker was asked %v, want one start", calls)
	}

	// Running now, so it is dialled; nothing real answers, and that is a
	// different state from stopped.
	got = readJSON[summaryView](t, do(t, admin, http.MethodGet, pathf("/databases/%d", id), ""))
	if got.State != dbStateUnreachable || got.Error == "" || dials.Load() == 0 {
		t.Errorf("after start: state = %q error=%q dials=%d", got.State, got.Error, dials.Load())
	}
	if !got.Power.Stop || !got.Power.Restart || got.Power.Start {
		t.Errorf("power for a running container = %+v", got.Power)
	}
}

// Stopping and restarting interrupt whatever is using the database, so they
// need what the Docker page's stop needs. They share a path with start, so
// the handler decides — and it must decide before anything is touched.
func TestPowerChecksTheCapabilityByAction(t *testing.T) {
	h := newConnHarness(t)
	port, _ := deafPort(t)
	h.engine.containers = []*fakeDBContainer{{
		id: "c0ffee", name: "cache", image: "redis:7-alpine", state: "running",
		hostIP: "127.0.0.1", hostPort: port, port: 6379,
	}}
	id := h.add("cache", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	path := pathf("/databases/%d/power", id)

	for _, action := range []string{"stop", "restart"} {
		rec := do(t, h.as(auth.RoleLimited), http.MethodPost, path, `{"action":"`+action+`"}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s by a role without the destructive capability = %d %s", action, rec.Code, rec.Body.String())
		}
	}
	if rec := do(t, h.as(auth.RoleReadOnly), http.MethodPost, path, `{"action":"start"}`); rec.Code != http.StatusForbidden {
		t.Errorf("start by a read-only role = %d", rec.Code)
	}
	for _, body := range []string{`{"action":"kill"}`, `{"action":""}`, `{}`} {
		if rec := do(t, h.as(auth.RoleAdmin), http.MethodPost, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("power %s = %d, want 400", body, rec.Code)
		}
	}
	if calls := h.engine.did(); len(calls) != 0 {
		t.Fatalf("a refused request reached Docker: %v", calls)
	}

	for _, action := range []string{"restart", "stop"} {
		rec := do(t, h.as(auth.RoleAdmin), http.MethodPost, path, `{"action":"`+action+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s by an admin = %d %s", action, rec.Code, rec.Body.String())
		}
	}
	if calls := h.engine.did(); len(calls) != 2 || calls[0] != "restart cache" || calls[1] != "stop cache" {
		t.Errorf("Docker was asked %v", calls)
	}
}

// Nothing is guessed. A database on another machine, a file, and a port no
// container or unit can be tied to are each refused with the reason the
// summary gave, and nothing on the machine is touched.
func TestPowerIsRefusedWhereNothingHereRunsTheServer(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	port, _ := deafPort(t)
	h.listeners = []proxysvc.Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port), PID: 4242, Process: "redis-server", Manager: "unmanaged"}}
	for name, id := range map[string]int64{
		"remote":    h.add("remote", dbx.DriverPostgres, "postgres://app:pw@db.example.com:5432/shop"),
		"file":      h.add("file", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "f.db")),
		"unmanaged": h.add("unmanaged", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port)),
	} {
		rec := do(t, router, http.MethodPost, pathf("/databases/%d/power", id), `{"action":"start"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "power_unavailable") {
			t.Errorf("%s: power = %d %s, want 409 power_unavailable", name, rec.Code, rec.Body.String())
		}
	}
	if calls := h.engine.did(); len(calls) != 0 {
		t.Errorf("Docker was asked %v for servers it does not run", calls)
	}
}

// A native server is followed from its port to the process listening there
// and to the unit that process belongs to; once it has stopped, the engine's
// own unit is what is left to name it by.
func TestSummaryOfANativeServer(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	port, dials := deafPort(t)
	id := h.add("native", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	path := pathf("/databases/%d", id)

	h.listeners = []proxysvc.Listener{
		{Protocol: "tcp", Address: "0.0.0.0", Port: uint32(port), PID: 812, Process: "redis-server",
			Manager: "systemd", ManagerName: "redis-server.service", Scope: proxysvc.ScopeAll},
	}
	got := readJSON[summaryView](t, do(t, router, http.MethodGet, path, ""))
	if got.Source != "host" || got.Unit == nil || got.Unit.Name != "redis-server.service" || got.Unit.ActiveState != "active" {
		t.Fatalf("a native server was not followed to its unit: %+v", got)
	}
	if got.Exposure != string(exposurePublic) {
		t.Errorf("a server bound to every interface reads as %q", got.Exposure)
	}
	if got.Power.Via != "systemd" || !got.Power.Stop || !got.Power.Restart || got.Power.Start {
		t.Errorf("power for a running unit = %+v", got.Power)
	}

	// Stopped: nothing listens, and the unit it was last seen running under
	// is what is left to name it by. The dial is skipped as it is for a
	// stopped container.
	before := dials.Load()
	h.listeners = nil
	h.units = []procs.Unit{
		{Name: "redis-server.service", LoadState: "loaded", ActiveState: "inactive", SubState: "dead"},
		{Name: "nginx.service", LoadState: "loaded", ActiveState: "active", SubState: "running"},
	}
	got = readJSON[summaryView](t, do(t, router, http.MethodGet, path, ""))
	if got.State != dbStateStopped || got.Unit == nil || got.Unit.ActiveState != "inactive" {
		t.Errorf("a stopped native server = %+v", got)
	}
	if dials.Load() != before {
		t.Errorf("a stopped unit's port was dialled")
	}
	if got.Power.Via != "systemd" || !got.Power.Start || got.Power.Stop || got.Power.Restart {
		t.Errorf("power for a stopped unit = %+v", got.Power)
	}

	// Two units of the engine: the one it was last seen running under still
	// settles it.
	h.units = append(h.units, procs.Unit{Name: "redis-cache.service", LoadState: "loaded", ActiveState: "inactive"})
	got = readJSON[summaryView](t, do(t, router, http.MethodGet, path, ""))
	if got.Unit == nil || got.Unit.Name != "redis-server.service" {
		t.Errorf("the unit last seen serving the connection was not remembered: %+v", got)
	}

	// A machine with no systemctl to ask says so instead of drawing a button.
	h.noSystemctl = true
	got = readJSON[summaryView](t, do(t, router, http.MethodGet, path, ""))
	if got.Unit == nil || got.Power.Via != "" || got.Power.Start || !strings.Contains(got.Power.Reason, "systemctl") {
		t.Errorf("power without systemctl = %+v", got.Power)
	}
	rec := do(t, router, http.MethodPost, path+"/power", `{"action":"start"}`)
	if rec.Code != http.StatusConflict || len(h.systemctlDid()) != 0 {
		t.Errorf("start without systemctl = %d %s; systemctl was asked %v", rec.Code, rec.Body.String(), h.systemctlDid())
	}
}

// What a unit is taken for decides what Start starts, so a stopped server's
// unit is named only on evidence: the unit the connection was last seen
// running under, or the engine's only unit when the connection is to the
// engine's own port. A connection to some other port that nothing answers on
// — a tunnel that dropped, a container that was removed — used to be given
// the machine's one PostgreSQL unit, read "stopped", and offered a Start that
// started a server it had nothing to do with.
func TestAUnitIsTakenForAConnectionOnlyOnEvidence(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	h.units = []procs.Unit{{Name: "postgresql@16-main.service", LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}}

	tunnel := h.add("tunnel", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:1/shop?sslmode=disable")
	got := readJSON[summaryView](t, do(t, router, http.MethodGet, pathf("/databases/%d", tunnel), ""))
	if got.Unit != nil || got.State != dbStateUnreachable || got.Error == "" {
		t.Errorf("a dead port was given the machine's only unit of the engine: %+v", got)
	}
	if got.Power.Via != "" || got.Power.Start || got.Power.Reason == "" || strings.Contains(got.Power.Reason, "postgresql@16-main") {
		t.Errorf("power for a dead port = %+v", got.Power)
	}
	rec := do(t, router, http.MethodPost, pathf("/databases/%d/power", tunnel), `{"action":"start"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "power_unavailable") || len(h.systemctlDid()) != 0 {
		t.Errorf("start on a dead port = %d %s; systemctl was asked %v", rec.Code, rec.Body.String(), h.systemctlDid())
	}

	// Read off the placement rather than through the route: the route would
	// go on to dial the engine's real port on the machine running the test.
	placed := func(id int64) (dbPlacement, dbPower) {
		t.Helper()
		conn, dsn, err := h.s.dbConnRow(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		place := h.s.newDBHostView(t.Context()).place(t.Context(), conn, dsn)
		return place, place.power(conn, true)
	}
	native := h.add("native", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:5432/shop?sslmode=disable")
	place, power := placed(native)
	if place.Unit == nil || place.Unit.Name != "postgresql@16-main.service" || place.Down != dbStateStopped {
		t.Errorf("the engine's only unit was not taken for a connection to its own port: %+v", place)
	}
	if power.Via != "systemd" || !power.Start || power.Stop {
		t.Errorf("power for the engine's stopped unit = %+v", power)
	}

	// Two units and nothing to choose between them by: neither is guessed,
	// and both are named so the operator can.
	h.units = append(h.units, procs.Unit{Name: "postgresql@15-old.service", LoadState: "loaded", ActiveState: "inactive"})
	place, power = placed(native)
	if place.Unit != nil || power.Via != "" || !strings.Contains(power.Reason, "postgresql@15-old.service") || !strings.Contains(power.Reason, "postgresql@16-main.service") {
		t.Errorf("an ambiguous unit was guessed: unit=%+v power=%+v", place.Unit, power)
	}
	if place, power = placed(tunnel); place.Unit != nil || strings.Contains(power.Reason, "postgresql@") {
		t.Errorf("units were offered as candidates for a port that is not the engine's: %+v", power)
	}
}

// The systemd half of the power route: the only code in these routes that
// runs a command on the host. The capability is decided by the action before
// anything is asked of systemctl, the unit and the verb reach it as they
// stand, a failure is a 502 that says what systemctl said, and what was kept
// about the server is dropped once its state has changed.
func TestPowerStartsAndStopsAUnitThroughSystemctl(t *testing.T) {
	h := newConnHarness(t)
	const unit = "redis-server.service"
	port, _ := deafPort(t)
	id := h.add("native", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	path := pathf("/databases/%d/power", id)
	listening := []proxysvc.Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port), PID: 812,
		Process: "redis-server", Manager: "systemd", ManagerName: unit}}
	h.listeners = listening
	h.units = []procs.Unit{{Name: unit, LoadState: "loaded", ActiveState: "active", SubState: "running"}}
	admin, limited := h.as(auth.RoleAdmin), h.as(auth.RoleLimited)

	// A reading is kept by the fleet, to be dropped by the stop below.
	do(t, admin, http.MethodGet, "/databases/fleet", "")
	if _, kept := h.s.dbConns.readings.Load(id); !kept {
		t.Fatal("the fleet kept no reading to drop")
	}

	for _, action := range []string{"stop", "restart"} {
		rec := do(t, limited, http.MethodPost, path, `{"action":"`+action+`"}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s by a role without the destructive capability = %d %s", action, rec.Code, rec.Body.String())
		}
	}
	if rec := do(t, limited, http.MethodPost, path, `{"action":"start"}`); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "already running") {
		t.Errorf("start of a running unit = %d %s", rec.Code, rec.Body.String())
	}
	if calls := h.systemctlDid(); len(calls) != 0 {
		t.Fatalf("a refused request reached systemctl: %v", calls)
	}

	rec := do(t, admin, http.MethodPost, path, `{"action":"stop"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("stop = %d %s", rec.Code, rec.Body.String())
	}
	if result := readJSON[map[string]string](t, rec); result["via"] != "systemd" || result["target"] != unit || result["state"] != "inactive" {
		t.Errorf("stop result = %v", result)
	}
	if calls := h.systemctlDid(); len(calls) != 2 || calls[0] != "stop "+unit || calls[1] != "is-active "+unit {
		t.Errorf("systemctl was asked %v, want the stop and then the state", calls)
	}
	if _, kept := h.s.dbConns.readings.Load(id); kept {
		t.Error("the reading taken before the stop was kept after it")
	}

	// Stopped now: nothing listens. Starting it is service.control's to do.
	h.listeners = nil
	for _, action := range []string{"stop", "restart"} {
		if rec := do(t, admin, http.MethodPost, path, `{"action":"`+action+`"}`); rec.Code != http.StatusConflict ||
			!strings.Contains(rec.Body.String(), "power_unavailable") {
			t.Errorf("%s of a stopped unit = %d %s", action, rec.Code, rec.Body.String())
		}
	}
	rec = do(t, limited, http.MethodPost, path, `{"action":"start"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("start by service.control = %d %s", rec.Code, rec.Body.String())
	}
	if result := readJSON[map[string]string](t, rec); result["target"] != unit || result["state"] != "active" {
		t.Errorf("start result = %v", result)
	}
	if calls := h.systemctlDid(); len(calls) != 4 || calls[2] != "start "+unit || calls[3] != "is-active "+unit {
		t.Errorf("systemctl was asked %v", calls)
	}

	// A failure, through the whole stack so that the audit trail is the one
	// the middleware writes.
	h.listeners = listening
	h.systemctlFails = map[string]string{
		"restart": "\nJob for redis-server.service failed because the control process exited with error code.\nSee \"systemctl status redis-server.service\" for details.\n",
	}
	c := &client{t: t, h: h.s.Routes(), cookie: signIn(t, h.s)}
	rec = c.do(http.MethodPost, "/api/v1"+path, `{"action":"restart"}`, nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "power_failed") ||
		!strings.Contains(rec.Body.String(), "Job for redis-server.service failed") || strings.Contains(rec.Body.String(), "systemctl status") {
		t.Errorf("a failed restart = %d %s, want 502 power_failed with systemctl's first line", rec.Code, rec.Body.String())
	}
	var status int
	var target, detail string
	if err := h.s.Store.DB.QueryRow(
		`SELECT status, target, detail FROM audit_log WHERE action = 'database.power.restart'`).Scan(&status, &target, &detail); err != nil {
		t.Fatalf("the failed restart is not on the audit trail: %v", err)
	}
	if status != http.StatusBadGateway || target != "native" || !strings.Contains(detail, unit) || !strings.Contains(detail, "control process exited") {
		t.Errorf("audit entry = %d %q %s", status, target, detail)
	}
}

// The summary says which actions it would accept, and the route holds the
// same line: an action that is not offered for the state the server is in is
// refused before Docker is asked.
func TestPowerRefusesAnActionTheSummaryDoesNotOffer(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	port, _ := deafPort(t)
	box := &fakeDBContainer{id: "c0ffee", name: "cache", image: "redis:7-alpine", state: "exited",
		hostIP: "127.0.0.1", hostPort: port, port: 6379}
	h.engine.containers = []*fakeDBContainer{box}
	id := h.add("cache", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	path := pathf("/databases/%d/power", id)

	refused := func(state, action, why string) {
		t.Helper()
		box.state = state
		rec := do(t, router, http.MethodPost, path, `{"action":"`+action+`"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "power_unavailable") || !strings.Contains(rec.Body.String(), why) {
			t.Errorf("%s of a container that is %s = %d %s", action, state, rec.Code, rec.Body.String())
		}
	}
	refused("exited", "restart", "Start it instead")
	refused("exited", "stop", "not running")
	refused("running", "start", "already running")
	refused("paused", "start", "paused")
	refused("paused", "restart", "paused")
	if calls := h.engine.did(); len(calls) != 0 {
		t.Errorf("Docker was asked %v for actions the summary does not offer", calls)
	}
}

// Docker gives a container ten seconds between SIGTERM and SIGKILL unless
// told otherwise, which is long enough for a web server and not for a
// database writing its snapshot. A stop from here says how long.
func TestPowerGivesAContainerTimeToShutDown(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	port, _ := deafPort(t)
	h.engine.containers = []*fakeDBContainer{{id: "c0ffee", name: "cache", image: "redis:7-alpine", state: "running",
		hostIP: "127.0.0.1", hostPort: port, port: 6379}}
	id := h.add("cache", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	path := pathf("/databases/%d/power", id)

	for _, body := range []string{
		`{"action":"stop","timeoutSeconds":-1}`,
		`{"action":"stop","timeoutSeconds":601}`,
		`{"action":"stop","timeoutSeconds":9223372036854775807}`,
		`{"action":"stop","timeoutSeconds":"long"}`,
	} {
		if rec := do(t, router, http.MethodPost, path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("power %s = %d, want 400", body, rec.Code)
		}
	}
	if calls := h.engine.did(); len(calls) != 0 {
		t.Fatalf("a refused request reached Docker: %v", calls)
	}
	for _, body := range []string{`{"action":"restart"}`, `{"action":"stop","timeoutSeconds":300}`} {
		if rec := do(t, router, http.MethodPost, path, body); rec.Code != http.StatusOK {
			t.Fatalf("power %s = %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if graces := h.engine.stopGraces(); len(graces) != 2 || graces[0] != "90" || graces[1] != "300" {
		t.Errorf("Docker was given %v to shut the container down in, want 90 and then 300 seconds", graces)
	}
}

// What listens on a database's port is not always the database. An ssh tunnel
// to a server elsewhere is sshd, and a published container's proxy belongs to
// the Docker daemon: a "stop" that followed the port to its unit would stop
// one of those. Only a unit named for the engine is taken for the server.
func TestAPortServedBySomethingElseIsNotOfferedThatUnitsPower(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	port, _ := deafPort(t)
	id := h.add("tunnelled", dbx.DriverPostgres, fmt.Sprintf("postgres://app:pw@127.0.0.1:%d/shop?sslmode=disable", port))
	for _, l := range []proxysvc.Listener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port), PID: 900, Process: "sshd", Manager: "systemd", ManagerName: "ssh.service"},
		{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port), PID: 901, Process: "docker-proxy", Manager: "systemd", ManagerName: "docker.service"},
		{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port), PID: 902, Process: "pgbouncer", Manager: "systemd", ManagerName: "pgbouncer.service"},
		{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port), PID: 903, Process: "postgres_exporter", Manager: "systemd", ManagerName: "postgresql-exporter.service"},
		{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port)},
	} {
		h.listeners = []proxysvc.Listener{l}
		got := readJSON[summaryView](t, do(t, router, http.MethodGet, pathf("/databases/%d", id), ""))
		if got.Unit != nil || got.Power.Via != "" || got.Power.Stop || got.Power.Reason == "" {
			t.Errorf("a port held by %q under %q: unit=%+v power=%+v", l.Process, l.ManagerName, got.Unit, got.Power)
		}
		rec := do(t, router, http.MethodPost, pathf("/databases/%d/power", id), `{"action":"stop"}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "power_unavailable") {
			t.Errorf("stop through %q = %d %s", l.ManagerName, rec.Code, rec.Body.String())
		}
	}
	// The engine's own unit, by contrast, is the server.
	h.listeners = []proxysvc.Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port), PID: 904,
		Process: "postgres", Manager: "systemd", ManagerName: "postgresql@16-main.service"}}
	got := readJSON[summaryView](t, do(t, router, http.MethodGet, pathf("/databases/%d", id), ""))
	if got.Unit == nil || got.Unit.Name != "postgresql@16-main.service" {
		t.Errorf("the engine's own unit was not taken for the server: %+v", got)
	}
}

// The same caution for a container: one found only because it publishes the
// connection's port, in an image that is not the engine's, is shown as what
// answers and is not offered power.
func TestAContainerOfAnotherImageIsShownAndNotOfferedPower(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	port, _ := deafPort(t)
	h.engine.containers = []*fakeDBContainer{{
		id: "babe01", name: "edge-proxy", image: "haproxy:3", state: "running",
		hostIP: "0.0.0.0", hostPort: port, port: 5432,
	}}
	id := h.add("through-proxy", dbx.DriverPostgres, fmt.Sprintf("postgres://app:pw@127.0.0.1:%d/shop?sslmode=disable", port))
	got := readJSON[summaryView](t, do(t, router, http.MethodGet, pathf("/databases/%d", id), ""))
	if got.Source != "docker" || got.Container == nil || got.Container.Name != "edge-proxy" || got.Exposure != string(exposurePublic) {
		t.Errorf("the container answering for the connection is not shown: %+v", got)
	}
	if got.Power.Via != "" || got.Power.Stop || !strings.Contains(got.Power.Reason, "haproxy:3") {
		t.Errorf("power for a container of another image = %+v", got.Power)
	}
	rec := do(t, router, http.MethodPost, pathf("/databases/%d/power", id), `{"action":"stop"}`)
	if rec.Code != http.StatusConflict || len(h.engine.did()) != 0 {
		t.Errorf("stop = %d %s; Docker was asked %v", rec.Code, rec.Body.String(), h.engine.did())
	}
}

// The unit name reaches systemctl as one argument of an argument vector, and
// only a service unit's name is accepted as one.
func TestUnitCommandIsAnArgumentVectorOverAValidatedName(t *testing.T) {
	cmd, err := unitCommand(context.Background(), "restart", "postgresql@16-main.service")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, "\x00")
	if !strings.HasSuffix(args, "systemctl\x00restart\x00postgresql@16-main.service") {
		t.Errorf("argv = %q", cmd.Args)
	}
	ignoresChroot := false
	for _, kv := range cmd.Env {
		ignoresChroot = ignoresChroot || kv == "SYSTEMD_IGNORE_CHROOT=1"
	}
	if !ignoresChroot {
		t.Error("systemctl would read the shared PID namespace as a chroot and ignore the command")
	}
	for _, unit := range []string{
		"", "redis-server", "redis.socket", "-rf.service", "a b.service", "x;reboot.service",
		"../etc.service", "$(id).service", "redis.service\nstop",
	} {
		if _, err := unitCommand(context.Background(), "start", unit); err == nil {
			t.Errorf("unit name %q was accepted", unit)
		}
	}
}

// --- the fleet ------------------------------------------------------------------

type dbFleetView struct {
	Connections []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		State       string `json:"state"`
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		Flavor      string `json:"flavor"`
		Environment string `json:"environment"`
		ReadOnly    bool   `json:"readOnly"`
		Broken      bool   `json:"broken"`
		Source      string `json:"source"`
		Unit        string `json:"unit"`
		ObjectWord  string `json:"objectWord"`
	} `json:"connections"`
}

// Several pages poll the fleet, and each poll used to dial every server
// again. A reading is kept for a few seconds, so three pages open at once
// cost one dial; a change to the connection drops it.
func TestFleetKeepsADialForAFewSecondsAndFlagsBrokenRows(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleReadOnly)
	port, dials := deafPort(t)
	h.listeners = []proxysvc.Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(port), PID: 77, Process: "redis-server", Manager: "unmanaged"}}
	id := h.add("cache", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	if _, err := h.s.Store.DB.Exec(`UPDATE db_connections SET environment='production', read_only=1 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('broken','mongodb','nonsense',0)`); err != nil {
		t.Fatal(err)
	}

	first := readJSON[dbFleetView](t, do(t, router, http.MethodGet, "/databases/fleet", ""))
	if len(first.Connections) != 2 {
		t.Fatalf("fleet lists %d connections, want the broken one too: %+v", len(first.Connections), first.Connections)
	}
	for _, c := range first.Connections {
		switch c.Name {
		case "broken":
			if !c.Broken || c.State != dbStateBroken || c.Error == "" || c.ObjectWord != "collections" {
				t.Errorf("broken row = %+v", c)
			}
		case "cache":
			if c.State != dbStateUnreachable || c.Source != "host" || c.Environment != "production" || !c.ReadOnly || c.Flavor != dbx.FlavorRedis {
				t.Errorf("fleet entry = %+v", c)
			}
		}
	}
	after := dials.Load()
	if after == 0 {
		t.Fatal("the fleet did not dial a connection it had no reading for")
	}
	for range 3 {
		do(t, router, http.MethodGet, "/databases/fleet", "")
	}
	if n := dials.Load(); n != after {
		t.Errorf("three more polls dialled %d more times; the reading was not kept", n-after)
	}
	// A change to the connection is a reason to ask again.
	h.s.dbConns.forget(id)
	do(t, router, http.MethodGet, "/databases/fleet", "")
	if dials.Load() == after {
		t.Error("a dropped reading was not taken again")
	}
}

// A fleet asks where each of its connections runs, and for a server that is
// down that means the machine's units and what every stopped container of the
// engine publishes. Both are read once for the request and shared: six
// connections used to list the units six times and inspect the same stopped
// container six times over.
func TestAFleetReadsTheMachineOnceForAllItsConnections(t *testing.T) {
	h := newConnHarness(t)
	h.engine.containers = []*fakeDBContainer{{
		id: "0ddba11", name: "old-cache", image: "redis:7-alpine", state: "exited",
		hostIP: "127.0.0.1", hostPort: 7000, port: 6379,
	}}
	// The engine's only unit, stopped: every connection to the engine's own
	// port is its, and none of them is dialled.
	h.units = []procs.Unit{{Name: "redis-server.service", LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}}
	for i := range 6 {
		h.add(fmt.Sprintf("cache-%d", i), dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:6379/%d", i))
	}
	fleet := readJSON[dbFleetView](t, do(t, h.as(auth.RoleReadOnly), http.MethodGet, "/databases/fleet", ""))
	if len(fleet.Connections) != 6 {
		t.Fatalf("fleet lists %d connections, want 6", len(fleet.Connections))
	}
	for _, c := range fleet.Connections {
		if c.State != dbStateStopped || c.Unit != "redis-server.service" {
			t.Errorf("fleet entry = %+v, want the stopped unit", c)
		}
	}
	if n := h.unitLists.Load(); n != 1 {
		t.Errorf("the machine's units were listed %d times for one fleet", n)
	}
	if n := h.engine.inspected("old-cache"); n != 1 {
		t.Errorf("a stopped container was inspected %d times for one fleet", n)
	}
}

// --- the catalogue ----------------------------------------------------------------

// The flags the frontend gates on, by name. Written out by hand: a flag that
// is renamed or dropped is a control that silently stops being drawn.
var gatedCapabilities = []string{
	"sql", "ddl", "schemas", "views", "routines", "triggers", "sequences", "enums", "extensions", "roles",
	"sessions", "cancel", "locks", "statements", "maintenance", "settings", "replication", "explainJSON",
	"explainAnalyze", "changeSets", "transactions", "queryLog", "advisor", "dump", "orm", "console",
	"server", "provision",
}

func TestDriverCatalogueCarriesCapabilitiesAndFlavours(t *testing.T) {
	h := newConnHarness(t)
	rec := do(t, h.as(auth.RoleReadOnly), http.MethodGet, "/databases/drivers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /databases/drivers = %d", rec.Code)
	}
	drivers := readJSON[[]struct {
		ID           dbx.Driver     `json:"id"`
		Label        string         `json:"label"`
		Kind         string         `json:"kind"`
		SQL          bool           `json:"sql"`
		DDL          bool           `json:"ddl"`
		DefaultPort  int            `json:"defaultPort"`
		DSNExample   string         `json:"dsnExample"`
		Capabilities map[string]any `json:"capabilities"`
		Flavors      []struct {
			ID           string         `json:"id"`
			Label        string         `json:"label"`
			Capabilities map[string]any `json:"capabilities"`
		} `json:"flavors"`
	}](t, rec)
	if len(drivers) != len(dbx.Drivers()) {
		t.Fatalf("%d drivers in the catalogue, want %d", len(drivers), len(dbx.Drivers()))
	}
	ports := map[dbx.Driver]int{
		dbx.DriverPostgres: 5432, dbx.DriverMySQL: 3306, dbx.DriverSQLite: 0, dbx.DriverMSSQL: 1433,
		dbx.DriverClickHouse: 9000, dbx.DriverOracle: 1521, dbx.DriverMongo: 27017, dbx.DriverRedis: 6379,
	}
	for _, d := range drivers {
		if d.Label == "" || d.Kind == "" || d.DSNExample == "" || d.DefaultPort != ports[d.ID] {
			t.Errorf("%s: label=%q kind=%q port=%d example=%q", d.ID, d.Label, d.Kind, d.DefaultPort, d.DSNExample)
		}
		// The example has to be one this engine's own parser reads the
		// default port out of, or it teaches the form to produce a DSN the
		// server then reports a different address for.
		if info, err := dbx.ParseDSN(d.ID, d.DSNExample); err != nil || (d.DefaultPort != 0 && info.Port != strconv.Itoa(d.DefaultPort)) {
			t.Errorf("%s: its own DSN example does not parse to port %d: %+v %v", d.ID, d.DefaultPort, info, err)
		}
		for _, flag := range gatedCapabilities {
			if _, ok := d.Capabilities[flag].(bool); !ok {
				t.Errorf("%s: capability %q is %v, want a boolean", d.ID, flag, d.Capabilities[flag])
			}
		}
		// The two older fields say what the reading says.
		if d.SQL != d.Capabilities["sql"] || d.DDL != d.Capabilities["ddl"] {
			t.Errorf("%s: sql/ddl disagree with the capabilities", d.ID)
		}
		if len(d.Flavors) == 0 || d.Flavors[0].ID != string(d.ID) {
			t.Errorf("%s: its own flavour is not first: %+v", d.ID, d.Flavors)
			continue
		}
		for _, f := range d.Flavors {
			if f.Label == "" || len(f.Capabilities) != len(d.Capabilities) {
				t.Errorf("%s/%s: label=%q with %d flags, want %d", d.ID, f.ID, f.Label, len(f.Capabilities), len(d.Capabilities))
			}
		}
	}
}

// The capability says an engine can be started from here; the templates are
// what starts it. Neither may claim what the other cannot do.
func TestProvisionCapabilityMatchesTheTemplates(t *testing.T) {
	offered := map[string]bool{}
	for engine, tmpl := range provisionTemplates {
		if !dbx.FlavorOf(tmpl.driver, tmpl.flavor) {
			t.Errorf("%s: flavour %q is not one of %s's", engine, tmpl.flavor, tmpl.driver)
		}
		if !dbx.Capable(tmpl.driver, tmpl.flavor, "provision") {
			t.Errorf("%s: a template exists and the capability says %s cannot be provisioned", engine, tmpl.flavor)
		}
		offered[tmpl.flavor] = true
	}
	for _, d := range dbx.Drivers() {
		for _, flavor := range dbx.Flavors(d) {
			if dbx.Capable(d, flavor, "provision") && !offered[flavor] {
				t.Errorf("%s is said to be provisionable and no template starts one", flavor)
			}
		}
	}
}

// --- reads that leave a trail -----------------------------------------------------

// Four GETs hand something off the server — a password, a table, a dump, a
// sweep of every table — and are meant to be on the audit trail. They called
// SetAudit, which does nothing on a GET, so none of them ever was.
func TestSensitiveReadsAreAudited(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	dbFile := filepath.Join(h.s.Cfg.FileRoots[0], "audited.db")
	id := h.add("audited", dbx.DriverSQLite, dbFile)
	do(t, router, http.MethodPost, pathf("/databases/%d/query", id), `{"query":"create table t (id integer primary key, v text)"}`)

	dumps := h.s.dbDumpDir("audited")
	if err := os.MkdirAll(dumps, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dumps, "audited-20260102-030405.sqlite"), []byte("dump"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ path, action string }{
		{pathf("/databases/%d/url", id), "database.connection.reveal"},
		{pathf("/databases/%d/search", id) + "?q=needle", "database.search"},
		{pathf("/databases/%d/export", id) + "?table=t&format=csv", "database.export"},
		{pathf("/databases/%d/backup/download", id) + "?file=audited-20260102-030405.sqlite", "database.backup.download"},
	} {
		rec := do(t, router, http.MethodGet, c.path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d %s", c.path, rec.Code, rec.Body.String())
			continue
		}
		var n int
		var target, username string
		err := h.s.Store.DB.QueryRow(
			`SELECT COUNT(*), COALESCE(MAX(target), ''), COALESCE(MAX(username), '') FROM audit_log WHERE action = ?`,
			c.action).Scan(&n, &target, &username)
		if err != nil || n != 1 || target != "audited" || username != "tester" {
			t.Errorf("GET %s left %d audit entries for %s (target %q, by %q, %v)", c.path, n, c.action, target, username, err)
		}
	}
	// What was revealed is never in the trail.
	var leaked int
	_ = h.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%' || ? || '%' OR detail LIKE '%needle%'`,
		dbFile).Scan(&leaked)
	if leaked != 0 {
		t.Error("an audit entry carries the connection string or the search needle")
	}
}

// hangUp is a client that goes away once the first bytes of a download have
// reached it.
type hangUp struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *hangUp) Write(b []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(b)
	w.cancel()
	return n, err
}

// An export is on the trail before its first row leaves, and closed with how
// much was sent. Both used to be one entry written after the stream on the
// request's own context, so a client that dropped the connection once it had
// what it wanted took the record with it.
func TestAnExportIsOnTheTrailEvenWhenTheClientGoesAway(t *testing.T) {
	h := newConnHarness(t)
	id := h.add("audited", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "audited.db"))
	admin := h.as(auth.RoleAdmin)
	for _, statement := range []string{
		"create table t (id integer primary key, v text)",
		"insert into t (v) values ('a'), ('b'), ('c')",
	} {
		if rec := do(t, admin, http.MethodPost, pathf("/databases/%d/query", id), `{"query":"`+statement+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", statement, rec.Code, rec.Body.String())
		}
	}
	// How many entries an action left, how many of them record a failure, and
	// the detail of the newest.
	entries := func(action string) (n, failed int, detail string) {
		t.Helper()
		if err := h.s.Store.DB.QueryRow(
			`SELECT COUNT(*), COALESCE(SUM(success = 0 AND status = 502), 0),
			        COALESCE((SELECT detail FROM audit_log WHERE action = ?1 ORDER BY id DESC LIMIT 1), '')
			   FROM audit_log WHERE action = ?1`,
			action).Scan(&n, &failed, &detail); err != nil {
			t.Fatal(err)
		}
		return n, failed, detail
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, pathf("/databases/%d/export", id)+"?table=t&format=csv", nil).WithContext(ctx)
	gone := &hangUp{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	h.as(auth.RoleReadOnly).ServeHTTP(gone, req)
	if ctx.Err() == nil {
		t.Fatal("the export wrote nothing, so the client never went away")
	}
	if n, _, detail := entries("database.export"); n != 1 || !strings.Contains(detail, `"table":"t"`) {
		t.Errorf("an export whose client went away left %d opening entries (%s)", n, detail)
	}
	if n, _, _ := entries("database.export.finished"); n != 1 {
		t.Errorf("an export whose client went away left %d closing entries", n)
	}

	// One that fails is recorded as a failure, not as a download.
	do(t, admin, http.MethodGet, pathf("/databases/%d/export", id)+"?table=no_such_table&format=csv", "")
	if n, failed, detail := entries("database.export.finished"); n != 2 || failed != 1 || !strings.Contains(detail, `"error"`) {
		t.Errorf("after a failed export: %d closing entries, %d failed (%s)", n, failed, detail)
	}
}

// --- a password is never repeated -------------------------------------------------

// A driver reports a connection string it could not use by quoting it, and
// the password is in what it quotes. What a failed dial said goes to the read
// surface and into the audit trail, so the password is taken out first.
func TestAFailedDialNeverRepeatsThePassword(t *testing.T) {
	h := newConnHarness(t)
	const secret = "s3cretpw"
	// go-redis cannot parse a database that is not a number, falls back to
	// dialling the text as an address, and the dial error names it whole.
	const unparsed = "redis://:" + secret + "@127.0.0.1:1/main"
	id := h.add("cache", dbx.DriverRedis, unparsed)
	// net/url prints the URL it could not parse, which is what the SQL Server
	// and ClickHouse drivers hand back.
	mssql := h.add("warehouse", dbx.DriverMSSQL, "sqlserver://sa:"+secret+"@127.0.0.1:1%zz?database=shop")

	reader := h.as(auth.RoleReadOnly)
	for _, path := range []string{
		pathf("/databases/%d", id), pathf("/databases/%d/ping", id), pathf("/databases/%d/stats", id), "/databases/fleet",
		pathf("/databases/%d", mssql), pathf("/databases/%d/ping", mssql), pathf("/databases/%d/tables", mssql),
	} {
		rec := do(t, reader, http.MethodGet, path, "")
		body := rec.Body.String()
		if strings.Contains(body, secret) {
			t.Errorf("GET %s repeats the password: %s", path, body)
		}
		if !strings.Contains(body, "error") {
			t.Errorf("GET %s says nothing about the failure: %d %s", path, rec.Code, body)
		}
	}
	rec := do(t, h.as(auth.RoleAdmin), http.MethodPost, "/databases/test", `{"driver":"redis","dsn":"`+unparsed+`"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), secret) || !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Errorf("test = %d %s", rec.Code, rec.Body.String())
	}

	// A probed create is refused with what the engine said, and the refusal
	// is written to the audit trail by the middleware: neither has it.
	c := &client{t: t, h: h.s.Routes(), cookie: signIn(t, h.s)}
	rec = c.do(http.MethodPost, "/api/v1/databases/", `{"name":"probed","driver":"redis","dsn":"`+unparsed+`","probe":true}`, nil)
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), secret) {
		t.Errorf("a probed create = %d %s", rec.Code, rec.Body.String())
	}
	var entries, leaked int
	if err := h.s.Store.DB.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(detail LIKE '%' || ? || '%'), 0) FROM audit_log WHERE action = 'database.connection.create'`,
		secret).Scan(&entries, &leaked); err != nil {
		t.Fatal(err)
	}
	if entries != 1 || leaked != 0 {
		t.Errorf("%d audit entries for the refused create, %d of them carrying the password", entries, leaked)
	}
}

func TestConnectErrorTakesThePasswordOutInEverySpelling(t *testing.T) {
	for _, c := range []struct{ name, dsn, said, want string }{
		{"a URL quoted whole", "postgres://app:hunter22@db:5432/shop",
			`parse "postgres://app:hunter22@db:5432/shop": invalid port`, `parse "postgres://app:***@db:5432/shop": invalid port`},
		{"an address that is the rest of a URL", "redis://:hunter22@127.0.0.1:1/main",
			"dial tcp: address :hunter22@127.0.0.1:1/main: too many colons in address", "dial tcp: address :***@127.0.0.1:1/main: too many colons in address"},
		{"the go-sql-driver form, with an @ in the password", "app:p@ss:word@tcp(127.0.0.1:3306)/shop",
			"invalid DSN near app:p@ss:word@tcp", "invalid DSN near app:***@tcp"},
		{"a password given as a parameter", "sqlserver://127.0.0.1:1433?database=shop&password=hunter22&user id=sa",
			"login failed with password=hunter22", "login failed with password=***"},
		{"a key=value string", "host=db user=app password='hunter 22' dbname=shop",
			`cannot use password='hunter 22' for app`, `cannot use password='***' for app`},
		{"written encoded, repeated decoded", "mongodb://app:p%40ssw0rd@db/shop",
			"auth failed for app with p@ssw0rd", "auth failed for app with ***"},
		{"written plain, repeated encoded", "postgres://app:p@ssw0rd@db/shop",
			"cannot parse app:p%40ssw0rd@db", "cannot parse app:***@db"},
		{"too short to blank everywhere", "postgres://app:pw@db/shop",
			`parse "postgres://app:pw@db/shop": no answer from pwd`, `parse "postgres://app:***@db/shop": no answer from pwd`},
		{"no password at all", "postgres://app@db/shop", "connection refused by app@db", "connection refused by app@db"},
		{"a file", "/var/lib/app/data.db", "unable to open /var/lib/app/data.db", "unable to open /var/lib/app/data.db"},
	} {
		if got := connectError(c.dsn, errors.New(c.said)); got != c.want {
			t.Errorf("%s: connectError = %q, want %q", c.name, got, c.want)
		}
	}
}

// --- the pool is not taken from under other requests --------------------------------

func TestAPingThatTimedOutDoesNotCloseThePool(t *testing.T) {
	h := newConnHarness(t)
	id := h.add("pooled", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "pooled.db"))
	if _, _, err := h.s.dbPool(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, fmt.Errorf("ping: %w", context.DeadlineExceeded)} {
		h.s.dropPoolAfter(id, err)
		if h.s.modules.dbs.Stats(id) == nil {
			t.Fatalf("the pool was closed because a request went away (%v)", err)
		}
	}
	h.s.dropPoolAfter(id, errors.New("connection refused"))
	if h.s.modules.dbs.Stats(id) != nil {
		t.Error("a pool whose server refused it was kept")
	}
}

// --- links ------------------------------------------------------------------------

func TestTopologyLinksADatabaseToItsOwnPage(t *testing.T) {
	h := newConnHarness(t)
	id := h.add("linked", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "linked.db"))
	topo := readJSON[struct {
		Nodes []struct {
			Kind string `json:"kind"`
			Href string `json:"href"`
		} `json:"nodes"`
	}](t, do(t, h.as(auth.RoleReadOnly), http.MethodGet, "/databases/topology", ""))
	if len(topo.Nodes) != 1 || topo.Nodes[0].Href != pathf("/databases/%d", id) {
		t.Errorf("topology nodes = %+v, want one linking to /databases/%d", topo.Nodes, id)
	}
}

// Nothing about a connection outlives it: an id is never reused by SQLite's
// AUTOINCREMENT, but a reading kept under one is still a reading of nothing.
func TestForgettingAConnectionClearsWhatWasKeptAboutIt(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	id := h.add("gone", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "gone.db"))
	do(t, router, http.MethodGet, pathf("/databases/%d", id), "")
	if _, ok := h.s.dbConns.identities.Load(id); !ok {
		t.Fatal("a server that answered was not remembered")
	}
	if rec := do(t, router, http.MethodDelete, pathf("/databases/%d", id), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("forget = %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := h.s.dbConns.identities.Load(id); ok {
		t.Error("what a forgotten connection's server said is still kept under its id")
	}
}
