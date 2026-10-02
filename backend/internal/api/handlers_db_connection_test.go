package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
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
	// What the container may use and how it restarts, as its configuration
	// holds them: bytes, billionths of a processor, Docker's own word.
	memory, nanoCPUs int64
	restart          string
}

type fakeDockerEngine struct {
	mu         sync.Mutex
	containers []*fakeDBContainer
	calls      []string
	// graces is the t= each stop and restart was sent with, and inspections
	// how many times each container's configuration was asked for.
	graces      []string
	inspections map[string]int
	// during is called with the verb while a start, stop or restart is being
	// carried out and before it takes effect: where a test looks at what the
	// rest of the dashboard reads in the middle of one.
	during func(verb string)
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
	path := dockerAPIVersion.ReplaceAllString(r.URL.Path, "")
	if _, verb, acts := strings.Cut(strings.TrimPrefix(path, "/containers/"), "/"); acts && r.Method == http.MethodPost && f.during != nil {
		// Outside the lock: what it calls reads the engine through this handler.
		f.during(verb)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
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
			"HostConfig": map[string]any{
				"NetworkMode": "bridge", "PortBindings": binding(c),
				"Memory": c.memory, "NanoCpus": c.nanoCPUs, "RestartPolicy": map[string]any{"Name": c.restart},
			},
			"NetworkSettings": map[string]any{"Ports": binding(c)},
		})
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/containers/"):
		c := f.find(strings.TrimPrefix(path, "/containers/"))
		if c == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such container"}`))
			return
		}
		f.calls = append(f.calls, "remove "+c.name)
		f.containers = slices.DeleteFunc(f.containers, func(have *fakeDBContainer) bool { return have == c })
		w.WriteHeader(http.StatusNoContent)
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
	// unitLists counts how often the machine's units were asked for, and
	// managerReads how often a listening process was asked what runs it.
	unitLists    atomic.Int64
	managerReads atomic.Int64

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
		managerOf: func(int32, string) (string, string) {
			h.managerReads.Add(1)
			return "unmanaged", ""
		},
		systemd: func() bool { return true },
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
	Origin      string `json:"origin"`
	Flavor      string `json:"flavor"`
	Number      string `json:"versionNumber"`
	State       string `json:"state"`
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	Source      string `json:"source"`
	Unit        *struct {
		Name        string `json:"name"`
		ActiveState string `json:"activeState"`
	} `json:"unit"`
	Container *struct {
		Name          string  `json:"name"`
		State         string  `json:"state"`
		MemoryLimit   int64   `json:"memoryLimit"`
		CPULimit      float64 `json:"cpuLimit"`
		RestartPolicy string  `json:"restartPolicy"`
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
	InFlight *struct {
		Action string    `json:"action"`
		Since  time.Time `json:"since"`
	} `json:"inFlight"`
	Exposure     string         `json:"exposure"`
	Managed      bool           `json:"managed"`
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

	// The file's size is the database's, and the fleet says so to a role that
	// reads nothing else about the machine. SQLite's catalogue has no figure
	// for it, so the card of a file used to say its size was not reported.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT); INSERT INTO notes(body) VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	h.s.dbConns.stale(id)
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		t.Fatalf("the file was not written: %v", err)
	}
	fleet := readJSON[dbFleetView](t, do(t, router, http.MethodGet, "/databases/fleet", ""))
	if len(fleet.Connections) != 1 || !fleet.Connections[0].SizesKnown || fleet.Connections[0].Bytes != st.Size() {
		t.Errorf("fleet entry of a file of %d bytes = %+v", st.Size(), fleet.Connections)
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
	// And a restart that could not be carried out is not one in flight.
	if got := readJSON[summaryView](t, do(t, admin, http.MethodGet, pathf("/databases/%d", id), "")); got.InFlight != nil {
		t.Errorf("a restart systemctl refused is still reported in flight: %+v", got.InFlight)
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

// A stop takes as long as the server takes to shut down, and the browser that
// asked for it is the only one that knows it is happening: a second one reads
// "running" beside a server that is going away, and offers to stop it. So the
// change is held by the server and carried by every reading of the connection
// — while the route is acting, whatever the server reads as, and afterwards
// until it reads the state the action leaves it in, or the time allowed for
// that runs out.
func TestAChangeOfPowerIsReadByEveryBrowserUntilItSettles(t *testing.T) {
	h := newConnHarness(t)
	admin, reader := h.as(auth.RoleAdmin), h.as(auth.RoleReadOnly)
	port, dials := deafPort(t)
	h.engine.containers = []*fakeDBContainer{{id: "c0ffee", name: "cache", image: "redis:7-alpine", state: "running",
		hostIP: "127.0.0.1", hostPort: port, port: 6379}}
	id := h.add("cache", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	path := pathf("/databases/%d", id)
	// What another browser reads: the connection's own page and its card.
	read := func() (summaryView, dbFleetView) {
		return readJSON[summaryView](t, do(t, reader, http.MethodGet, path, "")),
			readJSON[dbFleetView](t, do(t, reader, http.MethodGet, "/databases/fleet", ""))
	}
	held := func(summary summaryView, fleet dbFleetView) string {
		if len(fleet.Connections) != 1 {
			return "no fleet entry"
		}
		entry := fleet.Connections[0].InFlight
		switch {
		case summary.InFlight == nil && entry == nil:
			return ""
		case summary.InFlight == nil || entry == nil:
			return "the summary and the fleet disagree"
		case summary.InFlight.Action != entry.Action || !summary.InFlight.Since.Equal(entry.Since):
			return "the summary and the fleet disagree"
		}
		return entry.Action
	}
	if got := held(read()); got != "" {
		t.Fatalf("before anything was asked: %q in flight", got)
	}

	// In the middle of each action, before Docker has done anything.
	seen := map[string]string{}
	h.engine.during = func(verb string) {
		summary, fleet := read()
		seen[verb] = held(summary, fleet)
		if since := summary.InFlight; since != nil && time.Since(since.Since) > time.Minute {
			seen[verb] = "since " + since.Since.String()
		}
	}
	began := time.Now()
	for _, action := range []string{"restart", "stop"} {
		if rec := do(t, admin, http.MethodPost, path+"/power", `{"action":"`+action+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", action, rec.Code, rec.Body.String())
		}
		if seen[action] != action {
			t.Errorf("while the %s was being carried out another browser read %q in flight", action, seen[action])
		}
	}
	h.engine.during = nil

	// Stopped, which is what a stop leaves: it is over the moment that is read.
	summary, fleet := read()
	if summary.State != dbStateStopped || held(summary, fleet) != "" {
		t.Errorf("after the stop: state %q with %q in flight", summary.State, held(summary, fleet))
	}

	// A request the route refuses was never a change.
	if rec := do(t, admin, http.MethodPost, path+"/power", `{"action":"restart"}`); rec.Code != http.StatusConflict {
		t.Fatalf("restart of a stopped container = %d", rec.Code)
	}
	if got := held(read()); got != "" {
		t.Errorf("a refused restart is reported as %q in flight", got)
	}

	// Started: the container runs and the engine does not answer yet, which
	// is still the start going on. The reading that says so is not kept, or
	// the card would go on saying it after the engine had begun to answer.
	if rec := do(t, admin, http.MethodPost, path+"/power", `{"action":"start"}`); rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
	}
	summary, fleet = read()
	if summary.State != dbStateUnreachable || held(summary, fleet) != "start" {
		t.Errorf("after the start: state %q with %q in flight, want the start still held", summary.State, held(summary, fleet))
	}
	if since := summary.InFlight; since != nil && (since.Since.Before(began) || since.Since.After(time.Now())) {
		t.Errorf("since = %v, want when the start was asked for", since.Since)
	}
	before := dials.Load()
	do(t, reader, http.MethodGet, "/databases/fleet", "")
	if dials.Load() == before {
		t.Error("the fleet kept a refusal from a server that is still starting")
	}

	// It does not answer within the time a start is given: the change ends,
	// and what is left is a server that is unreachable.
	v, _ := h.s.dbConns.power.Load(id)
	late := *v.(*dbPowerChange)
	late.done = time.Now().Add(-dbPowerSettle - time.Second)
	h.s.dbConns.power.Store(id, &late)
	summary, fleet = read()
	if summary.State != dbStateUnreachable || held(summary, fleet) != "" {
		t.Errorf("a start that never settled is still held: state %q with %q in flight", summary.State, held(summary, fleet))
	}
	if _, kept := h.s.dbConns.power.Load(id); kept {
		t.Error("the change that ran out of time was not let go of")
	}

	// A handler that never said it had finished is not believed for ever.
	h.s.dbConns.power.Store(id, &dbPowerChange{Action: "stop", Since: time.Now().Add(-dbPowerActing - time.Second)})
	if got := held(read()); got != "" {
		t.Errorf("a change older than any request can run is still held: %q", got)
	}
	// And forgetting the connection forgets what was being done to it.
	h.s.dbConns.begin(id, "restart")
	h.s.dbConns.forget(id)
	if _, kept := h.s.dbConns.power.Load(id); kept {
		t.Error("a forgotten connection kept its change of power")
	}
}

// A second request for the same connection takes its slot, and the first one
// finishing must not end what the second is still doing.
func TestAChangeOfPowerEndsOnlyItsOwnRecord(t *testing.T) {
	var st dbConnState
	first := st.begin(7, "stop")
	second := st.begin(7, "start")
	st.finish(7, first, true)
	if got := st.inFlight(7, dbStateStopped); got != second || got.Action != "start" {
		t.Fatalf("the first request finishing replaced the second's change: %+v", got)
	}
	st.finish(7, first, false)
	if got := st.inFlight(7, dbStateUnreachable); got != second {
		t.Fatalf("the first request failing ended the second's change: %+v", got)
	}
	st.finish(7, second, true)
	if got := st.inFlight(7, dbStateUnreachable); got == nil || got.Action != "start" {
		t.Errorf("a start that was carried out is not held while the engine comes up: %+v", got)
	}
	if got := st.inFlight(7, dbStateRunning); got != nil {
		t.Errorf("a start is still held once the server answers: %+v", got)
	}
	if got := st.inFlight(7, dbStateUnreachable); got != nil {
		t.Errorf("a change that settled came back: %+v", got)
	}
}

// What a container may use is part of where a database runs, and the page
// that shows it read it from the Docker section's own route — which a role
// may not have, and which a stack with no Docker answers with an error. The
// summary carries it, for a container that runs and for one that does not.
func TestSummaryCarriesTheContainersLimits(t *testing.T) {
	h := newConnHarness(t)
	admin := h.as(auth.RoleAdmin)
	port, _ := deafPort(t)
	box := &fakeDBContainer{id: "c0ffee", name: "cache", image: "redis:7-alpine", state: "running",
		hostIP: "127.0.0.1", hostPort: port, port: 6379,
		memory: 512 << 20, nanoCPUs: 1_500_000_000, restart: "unless-stopped"}
	h.engine.containers = []*fakeDBContainer{box}
	id := h.add("cache", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port))
	path := pathf("/databases/%d", id)

	for _, state := range []string{"running", "exited"} {
		box.state = state
		got := readJSON[summaryView](t, do(t, admin, http.MethodGet, path, ""))
		if c := got.Container; c == nil || c.State != state || c.MemoryLimit != 512<<20 || c.CPULimit != 1.5 || c.RestartPolicy != "unless-stopped" {
			t.Errorf("a container that is %s: %+v, want 512 MiB, 1.5 processors and unless-stopped", state, c)
		}
	}
	// No limit is an absent field, as on the Docker page, not a zero.
	box.state, box.memory, box.nanoCPUs, box.restart = "running", 0, 0, "no"
	rec := do(t, admin, http.MethodGet, path, "")
	if body := rec.Body.String(); strings.Contains(body, "memoryLimit") || strings.Contains(body, "cpuLimit") || !strings.Contains(body, `"restartPolicy":"no"`) {
		t.Errorf("a container with no limits: %s", body)
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
		Number      string `json:"versionNumber"`
		Environment string `json:"environment"`
		ReadOnly    bool   `json:"readOnly"`
		Broken      bool   `json:"broken"`
		Source      string `json:"source"`
		Unit        string `json:"unit"`
		ObjectWord  string `json:"objectWord"`
		Bytes       int64  `json:"bytes"`
		SizesKnown  bool   `json:"sizesKnown"`
		Power       struct {
			Via     string `json:"via"`
			Start   bool   `json:"start"`
			Stop    bool   `json:"stop"`
			Restart bool   `json:"restart"`
			Reason  string `json:"reason"`
		} `json:"power"`
		Managed  bool `json:"managed"`
		InFlight *struct {
			Action string    `json:"action"`
			Since  time.Time `json:"since"`
		} `json:"inFlight"`
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

// A card offers what the connection's own page offers. The fleet used to
// carry neither which power actions the route would take nor whether the
// access route can change the port's reach, so the control center worked both
// out again from the entry's other fields — a second copy of two decisions the
// server already makes. Each entry now carries the summary's own answers.
func TestFleetEntriesCarryTheSummarysPowerAndReach(t *testing.T) {
	h := newConnHarness(t)
	admin := h.as(auth.RoleAdmin)
	stopped, _ := deafPort(t)
	running, _ := deafPort(t)
	native, _ := deafPort(t)
	h.engine.containers = []*fakeDBContainer{
		{id: "aaa111", name: "sessions", image: "redis:7-alpine", state: "exited", hostIP: "127.0.0.1", hostPort: stopped, port: 6379,
			labels: map[string]string{"com.docker.compose.project": "shop"}},
		{id: "bbb222", name: "cache", image: "redis:7-alpine", state: "running", hostIP: "127.0.0.1", hostPort: running, port: 6379},
	}
	h.listeners = []proxysvc.Listener{{Protocol: "tcp", Address: "127.0.0.1", Port: uint32(native), PID: 812,
		Process: "redis-server", Manager: "systemd", ManagerName: "redis-server.service"}}
	ids := map[string]int64{
		"stopped container": h.add("sessions", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", stopped)),
		"running container": h.add("cache", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", running)),
		"native unit":       h.add("native", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", native)),
		"file":              h.add("file", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "f.db")),
		"remote":            h.add("remote", dbx.DriverPostgres, "postgres://app:pw@db.example.com:5432/shop"),
	}
	res, err := h.s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('broken','mongodb','nonsense',0)`)
	if err != nil {
		t.Fatal(err)
	}
	ids["broken"], _ = res.LastInsertId()

	fleet := readJSON[dbFleetView](t, do(t, admin, http.MethodGet, "/databases/fleet", ""))
	if len(fleet.Connections) != len(ids) {
		t.Fatalf("fleet lists %d connections, want %d", len(fleet.Connections), len(ids))
	}
	for name, id := range ids {
		summary := readJSON[summaryView](t, do(t, admin, http.MethodGet, pathf("/databases/%d", id), ""))
		for _, entry := range fleet.Connections {
			if entry.ID != id {
				continue
			}
			if entry.Power != summary.Power || entry.Managed != summary.Managed {
				t.Errorf("%s: the fleet says power %+v managed=%v, the summary power %+v managed=%v",
					name, entry.Power, entry.Managed, summary.Power, summary.Managed)
			}
			offered := entry.Power.Start || entry.Power.Stop || entry.Power.Restart
			switch name {
			case "stopped container":
				if entry.Power.Via != "docker" || !entry.Power.Start || entry.Power.Stop || entry.Managed {
					t.Errorf("%s: power %+v managed=%v, want a start through Docker and a port compose owns", name, entry.Power, entry.Managed)
				}
			case "running container":
				if entry.Power.Via != "docker" || entry.Power.Start || !entry.Power.Stop || !entry.Power.Restart || !entry.Managed {
					t.Errorf("%s: power %+v managed=%v, want stop and restart through Docker and a port that can be restricted", name, entry.Power, entry.Managed)
				}
			case "native unit":
				if entry.Power.Via != "systemd" || !entry.Power.Stop || entry.Managed {
					t.Errorf("%s: power %+v managed=%v", name, entry.Power, entry.Managed)
				}
			default:
				if entry.Power.Via != "" || offered || entry.Power.Reason == "" || entry.Managed {
					t.Errorf("%s: power %+v managed=%v, want nothing offered and the reason said", name, entry.Power, entry.Managed)
				}
			}
		}
	}
}

// A server that has stopped answering is still the product it was. The fleet
// fell back to the driver's own flavour on a failed dial, so a Valkey tile
// turned into a Redis one with no version at the moment its server went away,
// while the connection's own page went on saying Valkey.
func TestAnUnreachableServerKeepsItsFlavourInTheFleet(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleReadOnly)
	port, _ := deafPort(t)
	dsn := fmt.Sprintf("redis://127.0.0.1:%d/0", port)
	id := h.add("cache", dbx.DriverRedis, dsn)
	// What it said the last time it answered, an hour ago.
	h.s.dbConns.identities.Store(id, dbIdentityKept{
		dsn: sha256.Sum256([]byte(dsn)), at: time.Now().Add(-time.Hour),
		identity: dbx.Identity{Flavor: dbx.FlavorValkey, Version: "8.1.10", Number: "8.1.10"},
	})

	fleet := readJSON[dbFleetView](t, do(t, router, http.MethodGet, "/databases/fleet", ""))
	if len(fleet.Connections) != 1 {
		t.Fatalf("fleet = %+v", fleet.Connections)
	}
	if c := fleet.Connections[0]; c.State != dbStateUnreachable || c.Error == "" || c.Flavor != dbx.FlavorValkey || c.Number != "8.1.10" {
		t.Errorf("fleet entry of a server that stopped answering = %+v, want it still a Valkey 8.1.10", c)
	}
	summary := readJSON[summaryView](t, do(t, router, http.MethodGet, pathf("/databases/%d", id), ""))
	if summary.State != dbStateUnreachable || summary.Flavor != dbx.FlavorValkey || summary.Number != "8.1.10" {
		t.Errorf("summary of the same server = %+v; the two must agree", summary)
	}

	// What is kept is about the server the connection pointed at. One that
	// has never answered where it points now is the driver's own, in both.
	other, _ := deafPort(t)
	never := h.add("never", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", other))
	fleet = readJSON[dbFleetView](t, do(t, router, http.MethodGet, "/databases/fleet", ""))
	for _, c := range fleet.Connections {
		if c.ID == never && (c.Flavor != dbx.FlavorRedis || c.Number != "") {
			t.Errorf("a server that never answered = %+v, want the driver's own flavour and no version", c)
		}
	}
}

// A fleet asks where each of its connections runs, and for a server that is
// down that means the machine's units, which unit each listening process
// belongs to and what every stopped container of the engine publishes. Each is
// read once for the request and shared: six connections used to list the
// units six times, read every listener's cgroup six times and inspect the
// same stopped container six times over.
func TestAFleetReadsTheMachineOnceForAllItsConnections(t *testing.T) {
	h := newConnHarness(t)
	h.engine.containers = []*fakeDBContainer{{
		id: "0ddba11", name: "old-cache", image: "redis:7-alpine", state: "exited",
		hostIP: "127.0.0.1", hostPort: 7000, port: 6379,
	}}
	// The engine's only unit, stopped: every connection to the engine's own
	// port is its, and none of them is dialled.
	h.units = []procs.Unit{{Name: "redis-server.service", LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}}
	// Two processes listening on other ports: each is asked what runs it, to
	// leave out a unit that is busy serving another port.
	h.listeners = []proxysvc.Listener{
		{Protocol: "tcp", Address: "127.0.0.1", Port: 5432, PID: 41, Process: "postgres"},
		{Protocol: "tcp", Address: "0.0.0.0", Port: 22, PID: 42, Process: "sshd"},
	}
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
	if n := h.managerReads.Load(); n != 2 {
		t.Errorf("two listening processes were asked what runs them %d times for one fleet", n)
	}
	if n := h.engine.inspected("old-cache"); n != 1 {
		t.Errorf("a stopped container was inspected %d times for one fleet", n)
	}
}

// --- the catalogue ----------------------------------------------------------------

// The flags the frontend gates on that are a yes or a no, by name. Written out
// by hand: a flag that is renamed or dropped is a control that silently stops
// being drawn.
var gatedCapabilities = []string{
	// connections and discovery
	"server", "provision", "inventoryConnect", "hostAccount", "fileBased", "openByDefault", "dump",
	"serverDatabaseCreate", "serverDatabaseConnect",
	// the workbench
	"sql", "console", "changeSets", "keylessEdits", "updateDefault", "script", "transactions", "queryCancel",
	"dollarQuoting", "regexFilter", "rowEstimate", "cellRead", "explainJSON", "explainAnalyze",
	// the schema
	"ddl", "schemas", "catalog", "views", "materializedViews", "routines", "triggers", "sequences", "enums",
	"comments", "indexes", "extensions",
	// watching and maintaining a server
	"stats", "sessions", "kill", "cancel", "locks", "replication", "tableStats", "indexStats", "maintenance",
	"settings", "settingsWrite", "roles", "privileges", "statements", "statementsReset", "advisor",
	"engineAdvisor", "queryLog", "clickhouseViews", "sqliteFile",
	// Redis
	"keys", "keyTree", "keyTypeFilter", "keyMeta", "valueDownload", "keyEncoding", "bulkKeys", "streams",
	"logicalDatabases", "consoleClassify", "serverInfo", "commandStats", "latency", "queryLogReset",
	"persistence", "aofRewrite", "memoryAnalysis", "aclRules", "pubsub", "pubsubLive", "monitor",
	// MongoDB
	"documents", "shellSyntax", "collections", "collectionOptions", "aggregation", "schemaAnalysis",
	"indexUsage", "indexHide", "validation", "profiler",
	// code generation and moving data
	"orm", "export", "exportColumns", "exportQuery", "import", "importMapping", "importUpsert",
	"importReplace", "importCreateTable", "dumpSchemaOnly", "dumpDataOnly", "dumpTables", "dumpCompression",
	"dumpDatabases", "dumpUpload", "restoreNewDatabase", "copy", "copyStructureOnly",
}

// The flags that are a list of words: empty for an engine without the
// feature, and never null.
var listedCapabilities = []string{
	"catalogGroups", "ddlOperations", "maintenanceActions", "ormTargets", "exportFormats", "importFormats",
}

// The flags that are a word where the feature comes in more than one form,
// and false where the engine has none of them.
var wordedCapabilities = []string{
	"rowIdentity", "returnsChangedRow", "readOnlyScope", "importAtomic", "json", "hashFieldTtl", "commandReference",
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
		FilterOps    []string       `json:"filterOps"`
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
		for _, flag := range listedCapabilities {
			if _, ok := d.Capabilities[flag].([]any); !ok {
				t.Errorf("%s: capability %q is %v, want a list", d.ID, flag, d.Capabilities[flag])
			}
		}
		for _, flag := range wordedCapabilities {
			switch d.Capabilities[flag].(type) {
			case bool, string:
			default:
				t.Errorf("%s: capability %q is %v, want a word or false", d.ID, flag, d.Capabilities[flag])
			}
		}
		// Every flag served is one of the three kinds above: a flag added to
		// the table and to none of these lists is one no page was told about.
		if known := len(gatedCapabilities) + len(listedCapabilities) + len(wordedCapabilities); len(d.Capabilities) != known {
			t.Errorf("%s: %d capabilities served, %d are listed here", d.ID, len(d.Capabilities), known)
		}
		// The operators a filter may use are the engine's own: a regular
		// expression where it has one.
		if regex, _ := d.Capabilities["regexFilter"].(bool); regex != slices.Contains(d.FilterOps, "regex") {
			t.Errorf("%s: regexFilter = %v and the filter operators are %v", d.ID, regex, d.FilterOps)
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

// updateDriverCatalogue rewrites the snapshot below from what the route
// serves: go test ./internal/api -run TestTheDriverCatalogueSnapshotIsCurrent -update-drivers
var updateDriverCatalogue = flag.Bool("update-drivers", false, "rewrite testdata/database-drivers.json from GET /databases/drivers")

// The frontend's tests draw their pages from what this route answers, and
// they cannot ask it: they run with no server. So the answer is kept beside
// this test as a file, which the engine registry's tests and the browser
// fixture read (frontend/src/components/database/engine.test.js,
// frontend/tests/browser/database-fixture.ts), and this test holds the file to
// the route. A capability given to an engine, or taken from one, fails here
// until the file is written again — and then every frontend test runs against
// the table the server really serves, not against a copy somebody remembered.
func TestTheDriverCatalogueSnapshotIsCurrent(t *testing.T) {
	h := newConnHarness(t)
	rec := do(t, h.as(auth.RoleReadOnly), http.MethodGet, "/databases/drivers", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /databases/drivers = %d", rec.Code)
	}
	var served any
	if err := json.Unmarshal(rec.Body.Bytes(), &served); err != nil {
		t.Fatal(err)
	}
	want, err := json.MarshalIndent(served, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := filepath.Join("testdata", "database-drivers.json")
	if *updateDriverCatalogue {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: write it with -update-drivers", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is not what GET /databases/drivers serves any more; write it again with -update-drivers and run the frontend's tests", path)
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

// --- a server that is not there ----------------------------------------------------

// errorView is the error envelope as a page reads it.
type errorView struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

// A closed port on this machine, for every engine with an address: what a
// connection reads while its server is stopped, restarting or not up yet.
func stoppedServers(h *connHarness) map[dbx.Driver]int64 {
	return map[dbx.Driver]int64{
		dbx.DriverPostgres:   h.add("pg", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:1/shop?sslmode=disable"),
		dbx.DriverMySQL:      h.add("my", dbx.DriverMySQL, "app:pw@tcp(127.0.0.1:1)/shop"),
		dbx.DriverMSSQL:      h.add("ms", dbx.DriverMSSQL, "sqlserver://sa:pw@127.0.0.1:1?database=shop&encrypt=disable"),
		dbx.DriverClickHouse: h.add("ch", dbx.DriverClickHouse, "clickhouse://app:pw@127.0.0.1:1/shop"),
		dbx.DriverOracle:     h.add("ora", dbx.DriverOracle, "oracle://app:pw@127.0.0.1:1/FREEPDB1"),
		dbx.DriverMongo:      h.add("doc", dbx.DriverMongo, "mongodb://127.0.0.1:1/shop"),
		dbx.DriverRedis:      h.add("kv", dbx.DriverRedis, "redis://127.0.0.1:1/0"),
	}
}

// The page's error state offers "Try again" only where the server marked the
// failure worth it, and no database route ever did: a page opened in the
// minute its server was restarting had no button, on any engine. A server
// that could not be reached is marked, whichever engine it is and whichever
// route found out. One that answered and said no is not: asking again gets
// the same answer.
func TestAServerThatIsNotThereIsWorthAskingAgain(t *testing.T) {
	h := newConnHarness(t)
	reader := h.as(auth.RoleReadOnly)
	servers := stoppedServers(h)
	reads := map[dbx.Driver][]string{
		dbx.DriverRedis: {"/databases/%d/keys", "/databases/%d/keys/meta?key=k", "/databases/%d/redis/server", "/databases/%d/schemas"},
		dbx.DriverMongo: {"/databases/%d/schemas"},
	}
	for driver, id := range servers {
		paths := reads[driver]
		if paths == nil {
			paths = []string{"/databases/%d/tables", "/databases/%d/catalog", "/databases/%d/browse?table=t"}
		}
		for _, path := range paths {
			rec := do(t, reader, http.MethodGet, pathf(path, id), "")
			got := readJSON[errorView](t, rec)
			if rec.Code != http.StatusBadGateway || got.Error.Code != "connect_failed" || !got.Error.Retryable {
				t.Errorf("%s: GET %s on a stopped server = %d %s", driver, path, rec.Code, rec.Body.String())
			}
		}
	}

	// A server that is there and refuses what it was sent is not marked. The
	// connection string of this one names a database that is not a number,
	// which no number of attempts will make one.
	refused := h.add("unparsed", dbx.DriverRedis, "redis://127.0.0.1:1/main")
	rec := do(t, reader, http.MethodGet, pathf("/databases/%d/keys", refused), "")
	if got := readJSON[errorView](t, rec); rec.Code != http.StatusBadGateway || got.Error.Code != "connect_failed" || got.Error.Retryable {
		t.Errorf("a connection string nobody can read = %d %s, want it not worth asking again", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "retryable") {
		t.Errorf("an answer that is not worth asking again says so by leaving the field out: %s", rec.Body.String())
	}
}

// The mark is for a request that can be sent again without consequence. A
// read whose server went away mid-reply can; a write whose reply was lost may
// already have been carried out, and is answered without it.
func TestOnlyAReadIsMarkedWorthAskingAgain(t *testing.T) {
	gone := fmt.Errorf("read tcp 127.0.0.1:52000->127.0.0.1:6379: %w", syscall.ECONNRESET)
	// How MongoDB's driver says the same thing.
	unselected := errors.New("server selection error: context deadline exceeded, current topology: { Type: Unknown, Servers: [{ Addr: 127.0.0.1:27017, Type: Unknown, Last error: dial tcp 127.0.0.1:27017: connect: connection refused }, ] }")
	for _, c := range []struct {
		name string
		err  error
		want bool
	}{
		{"a Redis read", redisFail(gone, false), true},
		{"a Redis write", redisFail(gone, true), false},
		{"a Redis read the server refused", redisFail(redis.Nil, false), false},
		{"a SQL read", queryFailed(gone), true},
		{"a SQL read the server refused", queryFailed(errors.New("permission denied for table pg_authid")), false},
		{"a MongoDB read", mongoReadFailed(unselected), true},
		{"a MongoDB catalogue read", mongoReadFailure(unselected), true},
		{"a MongoDB write", mongoFailure(unselected), false},
		{"a MongoDB filter the server refused", mongoReadFailed(mongo.CommandError{Code: 2, Message: "unknown operator: $nope"}), false},
		{"any request whose server could not be opened", connectFailed("redis://127.0.0.1:1/0", gone), true},
		{"a password the server refused", connectFailed("redis://:pw@127.0.0.1:1/0", errors.New("WRONGPASS invalid username-password pair")), false},
	} {
		var answer *httpx.APIError
		if !errors.As(c.err, &answer) {
			t.Fatalf("%s: %v is not an API error", c.name, c.err)
		}
		if answer.Retryable != c.want {
			t.Errorf("%s: retryable = %v, want %v (%d %s)", c.name, answer.Retryable, c.want, answer.Status, answer.Code)
		}
	}
}

// Whether another database can be made on a connection's server, and whether
// one can be opened as a connection of its own, are two flags of the driver
// catalogue — and the two routes ask the same question before they do
// anything, so a flag is never a button whose route answers that the engine
// has no such thing. An engine that can make a database and cannot open it is
// refused before the database is made, not after.
func TestServerDatabaseFlagsAreWhatTheRoutesDo(t *testing.T) {
	h := newConnHarness(t)
	admin := h.as(auth.RoleAdmin)
	servers := stoppedServers(h)
	servers[dbx.DriverSQLite] = h.add("file", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "f.db"))
	if len(servers) != len(dbx.Drivers()) {
		t.Fatalf("%d engines asked, %d exist", len(servers), len(dbx.Drivers()))
	}
	for driver, id := range servers {
		creates, connects := dbx.Capable(driver, "", "serverDatabaseCreate"), dbx.Capable(driver, "", "serverDatabaseConnect")

		rec := do(t, admin, http.MethodPost, pathf("/databases/%d/server/databases", id), `{"name":"jd_other"}`)
		got := readJSON[errorView](t, rec)
		// Every server here is stopped, so a request the route takes up ends
		// at the dial.
		if taken := rec.Code == http.StatusBadGateway && got.Error.Code == "connect_failed"; taken != creates {
			t.Errorf("%s: serverDatabaseCreate = %v and the route answers %d %s", driver, creates, rec.Code, rec.Body.String())
		}
		if !creates && (rec.Code != http.StatusBadRequest || got.Error.Code != "unsupported") {
			t.Errorf("%s: creating a database = %d %s, want 400 unsupported", driver, rec.Code, rec.Body.String())
		}

		rec = do(t, admin, http.MethodPost, pathf("/databases/%d/server/databases/connect", id), `{"database":"jd_other"}`)
		got = readJSON[errorView](t, rec)
		if refused := rec.Code == http.StatusBadRequest && got.Error.Code == "unsupported"; refused == connects {
			t.Errorf("%s: serverDatabaseConnect = %v and the route answers %d %s", driver, connects, rec.Code, rec.Body.String())
		}

		if creates && !connects {
			rec = do(t, admin, http.MethodPost, pathf("/databases/%d/server/databases", id), `{"name":"jd_other","connect":true}`)
			got = readJSON[errorView](t, rec)
			if rec.Code != http.StatusBadRequest || got.Error.Code != "unsupported" || !strings.Contains(got.Error.Message, "Nothing was created") {
				t.Errorf("%s: create and connect = %d %s, want it refused before anything is made", driver, rec.Code, rec.Body.String())
			}
		}
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

// A connection's row goes away by four roads: forgetting it, dropping the
// database it pointed at, removing the container that served it, and removing
// a deployment's resources. Each ran its own DELETE, and only the first went
// on to drop what is kept under the id and to tell discovery. The others left
// a reading of nothing behind, and a server the next sync would have connected
// again.
func TestEveryRoadThatRemovesAConnectionForgetsIt(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleAdmin)
	root := h.s.Cfg.FileRoots[0]
	port, _ := deafPort(t)
	h.engine.containers = []*fakeDBContainer{{
		id: "c0ffee", name: "jd-redis", image: "redis:7-alpine", state: "running",
		hostIP: "127.0.0.1", hostPort: port, port: 6379,
	}}
	for _, road := range []struct {
		name   string
		driver dbx.Driver
		dsn    string
		// path is under the connection, and confirm the phrase the route asks
		// to be typed.
		path, body, confirm string
		want                int
	}{
		{"forgotten", dbx.DriverSQLite, filepath.Join(root, "forgotten.db"), "", "", "", http.StatusNoContent},
		{"dropped", dbx.DriverSQLite, filepath.Join(root, "dropped.db"), "/database", `{}`, "dropped.db", http.StatusOK},
		{"removed", dbx.DriverRedis, fmt.Sprintf("redis://127.0.0.1:%d/0", port), "/database", `{"removeContainer":true}`, "db0", http.StatusOK},
		// No route of its own: the deployment removal hands the connection to
		// its remover, which is what is asked here.
		{"undeployed", dbx.DriverSQLite, filepath.Join(root, "undeployed.db"), "", "", "", 0},
	} {
		id := h.add(road.name, road.driver, road.dsn)
		origin := "docker:" + road.name
		if _, err := h.s.Store.DB.Exec(`UPDATE db_connections SET origin = ? WHERE id = ?`, origin, id); err != nil {
			t.Fatal(err)
		}
		// Everything the routes keep about a connection between requests.
		if road.driver.IsSQL() {
			if _, _, err := h.s.dbPool(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		}
		h.s.dbConns.identities.Store(id, dbIdentityKept{})
		h.s.dbConns.readings.Store(id, fleetReadingKept{})
		h.s.dbConns.units.Store(id, "redis-server.service")

		if road.want == 0 {
			remover := newDeploymentResourceRemover(h.s, "tester")
			target := deploy.RemovalTarget{Kind: "database_connection", ResourceID: strconv.FormatInt(id, 10)}
			if err := remover.RemoveManagedResource(context.Background(), target); err != nil {
				t.Errorf("%s: %v", road.name, err)
				continue
			}
			if len(remover.ignored) != 1 || remover.ignored[0] != origin {
				t.Errorf("%s: the remover reports %v as ignored, want %s for the audit entry", road.name, remover.ignored, origin)
			}
		} else {
			req := httptest.NewRequest(http.MethodDelete, pathf("/databases/%d", id)+road.path, strings.NewReader(road.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(httpx.ConfirmHeader, road.confirm)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != road.want {
				t.Errorf("%s: %d %s", road.name, rec.Code, rec.Body.String())
				continue
			}
		}
		var rows int
		if err := h.s.Store.DB.QueryRow(`SELECT COUNT(*) FROM db_connections WHERE id = ?`, id).Scan(&rows); err != nil || rows != 0 {
			t.Errorf("%s: the connection's row is still there (%d, %v)", road.name, rows, err)
		}
		if h.s.modules.dbs.Stats(id) != nil {
			t.Errorf("%s: the connection's pool is still open", road.name)
		}
		for what, kept := range map[string]*sync.Map{
			"what its server said it is": &h.s.dbConns.identities,
			"its last fleet reading":     &h.s.dbConns.readings,
			"the unit it ran under":      &h.s.dbConns.units,
		} {
			if _, ok := kept.Load(id); ok {
				t.Errorf("%s: %s is still kept under its id", road.name, what)
			}
		}
		// The server the connection was made from is put on the list the sync
		// leaves alone, by whoever removed it.
		var by string
		err := h.s.Store.DB.QueryRow(`SELECT ignored_by FROM db_inventory_ignored WHERE origin = ?`, origin).Scan(&by)
		if err != nil || by != "tester" {
			t.Errorf("%s: its server %s is not on discovery's ignore list (by %q, %v)", road.name, origin, by, err)
		}
	}
	if did := h.engine.did(); len(did) != 1 || did[0] != "remove jd-redis" {
		t.Errorf("Docker was asked %v, want only the removal of the one container", did)
	}
}

// A connection says which found server it was made from, in its own summary,
// in the list and in the fleet; one typed in by hand says it came from none.
func TestAConnectionCarriesItsOrigin(t *testing.T) {
	h := newConnHarness(t)
	router := h.as(auth.RoleReadOnly)
	id := h.add("found", dbx.DriverSQLite, filepath.Join(h.s.Cfg.FileRoots[0], "found.db"))
	for _, want := range []string{"", "docker:found"} {
		if _, err := h.s.Store.DB.Exec(`UPDATE db_connections SET origin = ? WHERE id = ?`, want, id); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{pathf("/databases/%d", id), "/databases/", "/databases/fleet"} {
			rec := do(t, router, http.MethodGet, path, "")
			if !strings.Contains(rec.Body.String(), `"origin":"`+want+`"`) {
				t.Errorf("GET %s does not carry origin %q: %s", path, want, rec.Body.String())
			}
		}
	}
}

// A connection made through discovery names its server by key, and that key
// is what finds the container again once it is stopped or recreated — the
// image's name is no help for a private build.
func TestAStoppedContainerIsFoundByTheConnectionsOrigin(t *testing.T) {
	plain := &dockerx.Container{Name: "shop-db"}
	composed := &dockerx.Container{Name: "shop-db-1", Labels: map[string]string{
		"com.docker.compose.project": "shop", "com.docker.compose.service": "db",
	}}
	cases := []struct {
		origin string
		c      *dockerx.Container
		want   bool
	}{
		{"docker:shop-db", plain, true},
		{"docker:shop-db-1", plain, false},
		{"docker:", plain, false},
		{"compose:shop/db", composed, true},
		{"compose:shop/api", composed, false},
		{"compose:shop/db", plain, false},
		{"host:postgresql@17-main.service", plain, false},
		{"", plain, false},
	}
	for _, tc := range cases {
		if got := containerOrigin(tc.origin, tc.c); got != tc.want {
			t.Errorf("containerOrigin(%q, %s) = %v, want %v", tc.origin, tc.c.Name, got, tc.want)
		}
	}

	published := []dockerx.PortMapping{{HostIP: "0.0.0.0", HostPort: 5432}, {HostIP: "127.0.0.1", HostPort: 5433}}
	if got := stoppedExposure(published, 5432); got != exposurePublic {
		t.Errorf("a port published on every address reads %q", got)
	}
	if got := stoppedExposure(published, 5433); got != exposureLocal {
		t.Errorf("a loopback binding reads %q", got)
	}
	if got := stoppedExposure(nil, 5432); got != exposureLocal {
		t.Errorf("a container that publishes nothing reads %q", got)
	}
}
