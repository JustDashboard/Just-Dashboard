package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

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
