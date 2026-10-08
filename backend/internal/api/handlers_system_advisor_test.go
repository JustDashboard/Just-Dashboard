package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

func TestStorageAdvisorRoutesAndContainment(t *testing.T) {
	_, s := newClient(t)
	root := fileFixture(t, s)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "advisor-reader", auth.RoleReadOnly)}
	response := reader.do(http.MethodGet, query("/api/v1/system/advisor/storage", map[string]string{"path": root}), "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("scan: %d %s", response.Code, response.Body.String())
	}
	var report files.StorageReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Entries == 0 || report.DuplicateScope == "" {
		t.Fatalf("scan evidence missing: %+v", report)
	}
	response = reader.do(http.MethodGet, query("/api/v1/system/advisor/storage", map[string]string{"path": filepath.Dir(root)}), "", nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("escaped root: %d", response.Code)
	}
	response = reader.do(http.MethodPost, "/api/v1/system/advisor/storage/cleanup", `{"selections":[]}`, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("reader cleanup: %d %s", response.Code, response.Body.String())
	}
}

func TestStorageCleanupRejectsInvalidSelectionBeforeMutation(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	for _, body := range []string{`{"selections":[]}`, `{"selections":[{"kind":"arbitrary","file":{"path":"` + root + `/index.html","identity":"invented"}}]}`} {
		response := c.do(http.MethodPost, "/api/v1/system/advisor/storage/cleanup", body, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid cleanup: %d %s", response.Code, strings.TrimSpace(response.Body.String()))
		}
	}
	response := c.do(http.MethodGet, query("/api/v1/files/stat", map[string]string{"path": filepath.Join(root, "index.html")}), "", nil)
	if response.Code != http.StatusOK {
		t.Fatal("invalid cleanup removed file")
	}
}

func TestWorkloadAdvisorAnswersReaderAndRejectsUnknownSort(t *testing.T) {
	_, s := newClient(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "workload-reader", auth.RoleReadOnly)}
	response := reader.do(http.MethodGet, "/api/v1/system/advisor/workloads?sort=invalid", "", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("sort validation: %d %s", response.Code, response.Body.String())
	}
	response = reader.do(http.MethodGet, "/api/v1/system/advisor/workloads?sort=cpu", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("local attribution: %d %s", response.Code, response.Body.String())
	}
	var report procs.WorkloadReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.CheckedAt.IsZero() || report.Silences == nil || report.Sort != "cpu" || len(report.Processes) > 20 {
		t.Fatalf("invalid evidence: %+v", report)
	}
	for _, row := range report.Processes {
		if row.CPUReady && row.CPUWindow <= 0 {
			t.Fatal("CPU without a sampling interval")
		}
	}
}

func TestAdvisorDockerUpdatesValidateBeforeDaemonAndEnforceCapabilities(t *testing.T) {
	c, s := newClient(t)
	for _, req := range []struct{ path, body string }{
		{"restart-policy", `{"policy":"invalid"}`}, {"restart-policy", `{"policy":"always","maxRetries":3}`}, {"resources", `{"memoryMb":1}`}, {"resources", `{}`},
	} {
		response := c.do(http.MethodPatch, "/api/v1/docker/containers/not-a-real-container/"+req.path, req.body, nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s validation: %d %s", req.path, response.Code, response.Body.String())
		}
	}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "policy-reader", auth.RoleReadOnly)}
	response := reader.do(http.MethodPatch, "/api/v1/docker/containers/not-a-real-container/restart-policy", `{"policy":"unless-stopped"}`, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("reader mutation: %d", response.Code)
	}
}

func TestStorageCleanupAuditsPerFileRefusalWithoutRemovingChangedEvidence(t *testing.T) {
	c, s := newClient(t)
	root := fileFixture(t, s)
	selections := []files.StorageSelection{{Kind: "temporary", File: files.StorageFile{Path: filepath.Join(root, "index.html"), Identity: "stale-measurement"}}}
	body, _ := json.Marshal(map[string]any{"selections": selections})
	response := c.do(http.MethodPost, "/api/v1/system/advisor/storage/cleanup", string(body), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("per-file outcome: %d %s", response.Code, response.Body.String())
	}
	var result files.StorageCleanupResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Removed || result.Items[0].Error == "" {
		t.Fatalf("refusal: %+v", result)
	}
	entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: "advisor.storage.cleanup"})
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Detail, "index.html") || !strings.Contains(entries[0].Detail, "error") {
		t.Fatalf("missing audit outcome: %+v %v", entries, err)
	}
}

func TestWorkloadBatchRoutesValidateAndEnforceCapabilities(t *testing.T) {
	c, s := newClient(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "batch-reader", auth.RoleReadOnly)}
	limited := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "batch-limited", auth.RoleLimited)}
	target := `[{"pid":123456,"startedAt":"2026-01-01T00:00:00Z"}]`
	for _, who := range []*client{reader, limited} {
		if response := who.do(http.MethodPost, "/api/v1/system/advisor/workloads/priority", `{"targets":`+target+`,"nice":10}`, nil); response.Code != http.StatusForbidden {
			t.Fatalf("priority without system.admin: %d", response.Code)
		}
	}
	if response := reader.do(http.MethodPost, "/api/v1/system/advisor/workloads/signal", `{"targets":`+target+`}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader signal: %d", response.Code)
	}
	for _, req := range []struct{ path, body string }{
		{"signal", `{"targets":[]}`},
		{"signal", `{"targets":[{"pid":123456}]}`},
		{"signal", `{"targets":` + target + `,"signal":"SIGHUP"}`},
		{"priority", `{"targets":` + target + `}`},
		{"priority", `{"targets":` + target + `,"nice":-5}`},
	} {
		if response := c.do(http.MethodPost, "/api/v1/system/advisor/workloads/"+req.path, req.body, nil); response.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: %d %s", req.path, req.body, response.Code, response.Body.String())
		}
	}
}

// A batch answers with each process's outcome: a PID measured as one process
// and now another is refused by name, the measured one is signalled, and the
// request is audited once.
func TestWorkloadBatchSignalChecksEveryIdentity(t *testing.T) {
	c, s := newClient(t)
	sleeper := exec.Command("sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Skip("no sleep binary:", err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _, _ = sleeper.Process.Wait() })
	pid := int32(sleeper.Process.Pid)
	detail, err := s.modules.table.Detail(t.Context(), pid)
	if err != nil {
		t.Fatal(err)
	}
	started := detail.CreateTime.Format(time.RFC3339Nano)

	// The test may itself run reniced, so lowering goes to the floor.
	if detail.Nice < 19 {
		priority := c.do(http.MethodPost, "/api/v1/system/advisor/workloads/priority",
			fmt.Sprintf(`{"targets":[{"pid":%d,"startedAt":%q}],"nice":19}`, pid, started), nil)
		var lowered struct {
			Items   []workloadOutcome `json:"items"`
			Changed int               `json:"changed"`
		}
		if err := json.Unmarshal(priority.Body.Bytes(), &lowered); err != nil || priority.Code != http.StatusOK || lowered.Changed != 1 {
			t.Fatalf("lower priority: %d %s", priority.Code, priority.Body.String())
		}
	}
	again := c.do(http.MethodPost, "/api/v1/system/advisor/workloads/priority",
		fmt.Sprintf(`{"targets":[{"pid":%d,"startedAt":%q}],"nice":%d}`, pid, started, detail.Nice), nil)
	if !strings.Contains(again.Body.String(), `"skipped":true`) {
		t.Fatalf("raising priority was not refused: %s", again.Body.String())
	}

	body := fmt.Sprintf(`{"targets":[{"pid":%d,"startedAt":%q},{"pid":%d,"startedAt":%q}]}`,
		pid, detail.CreateTime.Add(-time.Hour).Format(time.RFC3339Nano), pid, started)
	response := c.do(http.MethodPost, "/api/v1/system/advisor/workloads/signal", body, nil)
	var result struct {
		Items     []workloadOutcome `json:"items"`
		Signalled int               `json:"signalled"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK {
		t.Fatalf("signal: %d %s", response.Code, response.Body.String())
	}
	if len(result.Items) != 2 || result.Items[0].OK || !strings.Contains(result.Items[0].Error, "different process") ||
		!result.Items[1].OK || result.Signalled != 1 {
		t.Fatalf("outcomes = %+v", result)
	}
	if err := sleeper.Wait(); err == nil {
		t.Fatal("measured process was not signalled")
	}
	entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: "advisor.workloads.signal"})
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Detail, "SIGTERM") {
		t.Fatalf("audit: %+v %v", entries, err)
	}
}
