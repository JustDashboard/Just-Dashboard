package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

func TestProcessListReportsTheWholeInventoryBehindAFilter(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodGet,
		"/api/v1/processes/inventory?limit=50&q=jd-process-name-that-cannot-exist", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET process list: %d: %s", w.Code, w.Body.String())
	}
	var got procs.ProcessList
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Available == 0 {
		t.Fatal("the host process inventory unexpectedly reported no processes")
	}
	if got.Total != 0 || len(got.Processes) != 0 || got.Truncated {
		t.Fatalf("impossible filter returned %+v", got)
	}
}

func TestOriginalProcessListResponseRemainsAnArray(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/processes/?limit=50", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET process list: %d: %s", w.Code, w.Body.String())
	}
	var got []procs.Process
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("legacy process list is no longer an array: %v", err)
	}
}

func TestProcessIdentityRefusesAReusedPID(t *testing.T) {
	started := time.Date(2026, 9, 1, 12, 0, 0, 123, time.UTC)
	process := &procs.Process{PID: 42, CreateTime: started}
	if err := requireSameProcess(process, started.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("same process was refused: %v", err)
	}
	other := started.Add(time.Second).Format(time.RFC3339Nano)
	if err := requireSameProcess(process, other); err == nil {
		t.Fatal("a reused PID was accepted")
	}
	if err := requireSameProcess(process, "not-a-time"); err == nil {
		t.Fatal("an invalid identity was accepted")
	}
}

// Renicing arbitrary host processes changes scheduling for the whole server.
// It belongs to system.admin; the process detail UI hiding the control is only
// an affordance and must not be the permission boundary.
func TestProcessPriorityNeedsSystemAdmin(t *testing.T) {
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "limited-procs", auth.RoleLimited)}
	w := c.do(http.MethodPut, "/api/v1/processes/2/priority", `{"nice":10}`, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("limited priority update = %d, want 403: %s", w.Code, w.Body.String())
	}
}

func TestProcessPriorityRequiresANiceValue(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodPut, "/api/v1/processes/2/priority", `{}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing nice value = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// Starting a program under PM2 runs an operator-named file as a host account.
// That is code execution, not service control, and is gated accordingly.
func TestPM2StartNeedsSystemAdmin(t *testing.T) {
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "limited-pm2", auth.RoleLimited)}
	w := c.do(http.MethodPost, "/api/v1/pm2/start", `{"account":"deploy","script":"/srv/a.js"}`, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("limited pm2 start = %d, want 403: %s", w.Code, w.Body.String())
	}
}

func TestPM2StartRequiresAnAccountAndAScript(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodPost, "/api/v1/pm2/start", `{"script":"/srv/a.js"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing account = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestProcessTreeReportsTheParentChain(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/processes/"+strconv.Itoa(os.Getpid())+"/tree", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET process tree: %d: %s", w.Code, w.Body.String())
	}
	var got procs.ProcessTree
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Ancestors) == 0 || got.Ancestors[len(got.Ancestors)-1].PID != int32(os.Getppid()) {
		t.Fatalf("ancestors = %+v, want the chain ending in ppid %d", got.Ancestors, os.Getppid())
	}
	if got.Children == nil {
		t.Fatal("children must be a list, not null")
	}
	if w := c.do(http.MethodGet, "/api/v1/processes/2147483000/tree", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("missing pid = %d, want 404", w.Code)
	}
}

func TestProcessDetailReportsSocketsWithoutEnvironment(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/processes/"+strconv.Itoa(os.Getpid()), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET process detail: %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, leaked := got["environ"]; leaked {
		t.Fatal("process detail must never carry the environment")
	}
	if got["openFilesLimit"] == nil {
		t.Fatalf("open files limit missing from %v", got)
	}
}

// The kernel ignores a signal sent from user space to one of its threads, so
// the route says so instead of reporting a Kill that changed nothing.
func TestSignalRefusesAKernelThread(t *testing.T) {
	stat, err := os.ReadFile("/proc/2/stat")
	if err != nil || !strings.Contains(string(stat), "(kthreadd)") {
		t.Skip("no kthreadd in this PID namespace")
	}
	c, _ := newClient(t)
	w := c.do(http.MethodPost, "/api/v1/processes/2/signal", `{"signal":"SIGCONT"}`, nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "kernel_thread") {
		t.Fatalf("signal to kthreadd = %d %s, want 409 kernel_thread", w.Code, w.Body.String())
	}
}

func TestProcessDetailCarriesItsHistory(t *testing.T) {
	c, _ := newClient(t)
	path := "/api/v1/processes/" + strconv.Itoa(os.Getpid())
	var got procs.Process
	for range 2 {
		w := c.do(http.MethodGet, path, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET detail: %d: %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond)
	}
	if !got.CPUReady || len(got.History) != 1 {
		t.Fatalf("second read: cpuReady %v, %d history points, want a measured window", got.CPUReady, len(got.History))
	}
}
