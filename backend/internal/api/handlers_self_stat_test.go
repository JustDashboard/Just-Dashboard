package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// The dashboard had nothing to say about its own process. An administrator
// can now read what it uses and how each background task is going.
func TestTheDashboardReportsItsOwnProcessToAnAdministrator(t *testing.T) {
	c, s := newClient(t)
	const name = "api test loop that reports its passes"
	done := safego.Once(s.Log, name, func() {})
	<-done
	passes := safego.NewPasses(s.Log, name)
	passes.Report(context.Background(), nil)
	passes.Report(context.Background(), errors.New("upstream said no"))
	// A database and a write-ahead log of sizes the answer must repeat. The
	// test server's store is opened elsewhere, so its data directory has no
	// database of its own; were that to change, these files are not written
	// over a real one.
	database := filepath.Join(s.Cfg.DataDir, store.DatabaseFile)
	if _, err := os.Stat(database); !os.IsNotExist(err) {
		t.Fatalf("the test server's data directory holds a database (%v): this test would write over it", err)
	}
	if err := os.WriteFile(database, bytes.Repeat([]byte("d"), 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(database+"-wal", bytes.Repeat([]byte("w"), 4096), 0o600); err != nil {
		t.Fatal(err)
	}

	w := c.do(http.MethodGet, "/api/v1/dashboard/self", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
	var got struct {
		Process struct {
			PID        int    `json:"pid"`
			Goroutines int    `json:"goroutines"`
			GoVersion  string `json:"goVersion"`
			Memory     struct {
				HeapAllocBytes uint64 `json:"heapAllocBytes"`
				SysBytes       uint64 `json:"sysBytes"`
			} `json:"memory"`
		} `json:"process"`
		Store struct {
			Path      string `json:"path"`
			SizeBytes int64  `json:"sizeBytes"`
			WALBytes  int64  `json:"walBytes"`
		} `json:"store"`
		Tasks []struct {
			Name   string `json:"name"`
			Panics int    `json:"panics"`
			Passes *struct {
				Passes       int64  `json:"passes"`
				FailedPasses int    `json:"failedPasses"`
				FailingSince string `json:"failingSince"`
				LastError    string `json:"lastError"`
			} `json:"passes"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Process.PID != os.Getpid() || got.Process.Goroutines < 1 || got.Process.GoVersion == "" ||
		got.Process.Memory.HeapAllocBytes == 0 || got.Process.Memory.SysBytes < got.Process.Memory.HeapAllocBytes {
		t.Fatalf("process: %+v", got.Process)
	}
	if got.Store.SizeBytes != 8192 || got.Store.WALBytes != 4096 || filepath.Base(got.Store.Path) != store.DatabaseFile {
		t.Fatalf("store: %+v", got.Store)
	}
	found := false
	for _, task := range got.Tasks {
		if task.Name != name {
			continue
		}
		found = true
		if task.Passes == nil || task.Passes.Passes != 2 || task.Passes.FailedPasses != 1 ||
			task.Passes.FailingSince == "" || task.Passes.LastError != "upstream said no" {
			t.Fatalf("the loop's passes: %+v", task.Passes)
		}
	}
	if !found {
		t.Fatalf("the task is not listed among %d", len(got.Tasks))
	}
}

// Nothing of it without a credential, and nothing below an administrator:
// a goroutine dump names what the dashboard is doing for whom.
func TestTheDashboardsOwnProcessIsAnAdministrators(t *testing.T) {
	c, s := newClient(t)
	paths := []string{
		"/api/v1/dashboard/self",
		"/api/v1/dashboard/debug/pprof/",
		"/api/v1/dashboard/debug/pprof/goroutine",
		"/api/v1/dashboard/debug/pprof/heap",
		"/api/v1/dashboard/debug/pprof/profile?seconds=1",
	}
	anonymous := &client{t: t, h: c.h}
	for _, role := range []auth.Role{auth.RoleLimited, auth.RoleReadOnly} {
		who := &client{t: t, h: c.h, cookie: signInAs(t, s, "self-"+string(role), role)}
		for _, path := range paths {
			if w := who.do(http.MethodGet, path, "", nil); w.Code != http.StatusForbidden {
				t.Errorf("%s as %s answered %d: %.200s", path, role, w.Code, w.Body)
			}
		}
	}
	for _, path := range paths {
		if w := anonymous.do(http.MethodGet, path, "", nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without a credential answered %d", path, w.Code)
		}
	}
	// And not at the addresses net/http/pprof would have put them.
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap", "/api/v1/debug/pprof/"} {
		if w := c.do(http.MethodGet, path, "", nil); w.Code != http.StatusNotFound {
			t.Errorf("%s answered %d", path, w.Code)
		}
	}
	if http.DefaultServeMux != nil {
		if _, pattern := http.DefaultServeMux.Handler(mustRequest(t, "/debug/pprof/")); pattern != "" {
			t.Errorf("the default mux serves %q: net/http/pprof was imported", pattern)
		}
	}
}

func mustRequest(t *testing.T, path string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The profiles are the files `go tool pprof` reads, and asking for one is
// on the audit trail though it is a read.
func TestAnAdministratorTakesAProfileOfTheDashboard(t *testing.T) {
	c, s := newClient(t)

	w := c.do(http.MethodGet, "/api/v1/dashboard/debug/pprof/", "", nil)
	var list struct {
		Profiles []struct{ Name, Path string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != http.StatusOK || len(list.Profiles) == 0 {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
	for _, profile := range list.Profiles {
		if profile.Path != "/api/v1/dashboard/debug/pprof/"+profile.Name {
			t.Errorf("%s is listed at %s", profile.Name, profile.Path)
		}
	}

	w = c.do(http.MethodGet, "/api/v1/dashboard/debug/pprof/heap", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Disposition"), "just-dashboard-heap-") ||
		w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("heap: %d %v", w.Code, w.Header())
	}
	unzipped, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("the heap profile is not the gzipped pprof format: %v", err)
	}
	if raw, err := io.ReadAll(unzipped); err != nil || len(raw) == 0 {
		t.Fatalf("the heap profile is empty: %v", err)
	}

	w = c.do(http.MethodGet, "/api/v1/dashboard/debug/pprof/goroutine?debug=2", "", nil)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") ||
		!strings.Contains(w.Body.String(), "handleSelfProfile") {
		t.Fatalf("goroutines as text: %d %.300s", w.Code, w.Body)
	}

	w = c.do(http.MethodGet, "/api/v1/dashboard/debug/pprof/profile?seconds=1", "", nil)
	if w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Fatalf("cpu: %d, %d bytes", w.Code, w.Body.Len())
	}
	if _, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes())); err != nil {
		t.Fatalf("the CPU profile is not the gzipped pprof format: %v", err)
	}

	for path, want := range map[string]int{
		"/api/v1/dashboard/debug/pprof/cmdline":            http.StatusNotFound,
		"/api/v1/dashboard/debug/pprof/mutex":              http.StatusNotFound,
		"/api/v1/dashboard/debug/pprof/profile?seconds=61": http.StatusBadRequest,
		"/api/v1/dashboard/debug/pprof/heap?debug=9":       http.StatusBadRequest,
	} {
		if w := c.do(http.MethodGet, path, "", nil); w.Code != want {
			t.Errorf("%s answered %d, want %d: %.200s", path, w.Code, want, w.Body)
		}
	}

	if entry := lastAudit(t, s, "dashboard.profile"); entry.Target != "cpu 1s" || entry.Actor == "" {
		t.Fatalf("the last profile on the trail: %+v", entry)
	}
}

// The runtime takes one CPU profile at a time. A second is told so, as an
// answer, instead of a 200 with nothing in it.
func TestASecondCPUProfileIsRefusedWhileOneRuns(t *testing.T) {
	c, _ := newClient(t)
	var sink bytes.Buffer
	if err := pprof.StartCPUProfile(&sink); err != nil {
		t.Skipf("a CPU profile is already running in this test binary: %v", err)
	}
	defer pprof.StopCPUProfile()

	w := c.do(http.MethodGet, "/api/v1/dashboard/debug/pprof/profile?seconds=1", "", nil)
	var answer ingressAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("%v in %s", err, w.Body)
	}
	if w.Code != http.StatusConflict || answer.Error.Code != "profile_running" || !answer.Error.Retryable {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Content-Disposition") != "" {
		t.Fatalf("a refusal was sent as a file: %v", w.Header())
	}
}
