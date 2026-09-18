package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
)

// fileBackupJob creates a plain local job over one directory under the file
// root, which is the smallest job the API accepts.
func fileBackupJob(t *testing.T, s *Server, admin *client, name string, extra map[string]any) (*backups.Job, string) {
	t.Helper()
	source := filepath.Join(s.Cfg.FileRoots[0], name+"-source")
	if err := os.MkdirAll(filepath.Join(source, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "etc", "app.conf"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"name": name, "sources": []string{source}, "targetKind": "local",
		"target":    map[string]string{"path": filepath.Join(s.Cfg.FileRoots[0], name+"-artifacts")},
		"retention": 3, "enabled": true, "schedule": "0 3 * * *",
	}
	for k, v := range extra {
		request[k] = v
	}
	encoded, _ := json.Marshal(request)
	response := admin.do(http.MethodPost, "/api/v1/backups/", string(encoded), nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create job: %d %s", response.Code, response.Body.String())
	}
	var job backups.Job
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	return &job, source
}

func TestBackupJobPauseAndResumeIsItsOwnAuditedRoute(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "pause-admin", auth.RoleAdmin)}
	job, _ := fileBackupJob(t, s, admin, "nightly", map[string]any{"retentionDays": 30, "pauseContainers": []string{"web"}})
	if job.RetentionDays != 30 || len(job.PauseContainers) != 1 || job.PauseContainers[0] != "web" {
		t.Fatalf("new fields not stored: %+v", job)
	}
	response := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/%d/enabled", job.ID), `{"enabled":false}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", response.Code, response.Body.String())
	}
	var paused backups.Job
	if err := json.Unmarshal(response.Body.Bytes(), &paused); err != nil {
		t.Fatal(err)
	}
	if paused.Enabled || paused.PauseContainers[0] != "web" {
		t.Fatalf("pause changed more than the flag: %+v", paused)
	}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "pause-reader", auth.RoleReadOnly)}
	if response := reader.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/%d/enabled", job.ID), `{"enabled":true}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("read-only resumed a job: %d", response.Code)
	}
	if response := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/%d/enabled", job.ID), `{"enabled":true}`, nil); response.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPost, "/api/v1/backups/999/enabled", `{"enabled":true}`, nil); response.Code != http.StatusNotFound {
		t.Fatalf("missing job: %d", response.Code)
	}
}

func TestBackupResourcesReportCoverageAndSuggestAJob(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "resources-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "resources-reader", auth.RoleReadOnly)}
	read := func() backupResourceReport {
		t.Helper()
		response := reader.do(http.MethodGet, "/api/v1/backups/resources", "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("resources: %d %s", response.Code, response.Body.String())
		}
		var report backupResourceReport
		if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	find := func(report backupResourceReport, kind string) *backupResource {
		for i := range report.Resources {
			if report.Resources[i].Kind == kind {
				return &report.Resources[i]
			}
		}
		return nil
	}
	report := read()
	dashboard := find(report, "dashboard")
	if dashboard == nil || dashboard.Protected || len(dashboard.CoveredBy) != 0 {
		t.Fatalf("dashboard resource before any job: %+v", dashboard)
	}
	if len(dashboard.Suggest.Sources) != 1 || dashboard.Suggest.Sources[0] != filepath.Clean(s.Cfg.DataDir) || len(dashboard.Suggest.SQLitePaths) != 1 {
		t.Fatalf("dashboard suggestion: %+v", dashboard.Suggest)
	}
	if _, ok := report.Unavailable["docker"]; !ok {
		t.Fatalf("no Docker in the test, yet not reported unavailable: %+v", report.Unavailable)
	}

	// A job whose source contains the proxy's configuration covers it; a
	// paused job is listed but does not protect.
	nginx := filepath.Join(s.Cfg.FileRoots[0], "etc", "nginx")
	if err := os.MkdirAll(nginx, 0o755); err != nil {
		t.Fatal(err)
	}
	s.Cfg.NginxDir = nginx
	proxy := find(read(), "proxy")
	if proxy == nil || proxy.Protected || proxy.Paths[0] != nginx {
		t.Fatalf("proxy resource: %+v", proxy)
	}
	request := map[string]any{
		"name": "etc", "sources": []string{filepath.Dir(nginx)}, "targetKind": "local",
		"target":    map[string]string{"path": filepath.Join(s.Cfg.FileRoots[0], "artifacts")},
		"retention": 1, "enabled": false, "schedule": "",
	}
	encoded, _ := json.Marshal(request)
	response := admin.do(http.MethodPost, "/api/v1/backups/", string(encoded), nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var job backups.Job
	_ = json.Unmarshal(response.Body.Bytes(), &job)
	proxy = find(read(), "proxy")
	if proxy.Protected || len(proxy.CoveredBy) != 1 || proxy.CoveredBy[0].Enabled {
		t.Fatalf("paused job counted as protection: %+v", proxy)
	}
	if response := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/%d/enabled", job.ID), `{"enabled":true}`, nil); response.Code != http.StatusOK {
		t.Fatalf("resume: %d", response.Code)
	}
	report = read()
	if proxy = find(report, "proxy"); !proxy.Protected {
		t.Fatalf("enabled covering job not counted: %+v", proxy)
	}
	// Unprotected resources sort first, so the dashboard now precedes nginx.
	if report.Resources[0].Kind != "dashboard" {
		t.Fatalf("unprotected resource not first: %+v", report.Resources[0])
	}
}

func TestBackupRestoreInPlaceAndSubsetThroughTheAPI(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "restore-admin", auth.RoleAdmin)}
	job, source := fileBackupJob(t, s, admin, "inplace", nil)
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("notes v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run, err := s.modules.backupRunner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != backups.StatusSuccess {
		t.Fatalf("backup: %+v %v", run, err)
	}
	conf := filepath.Join(source, "etc", "app.conf")
	if err := os.WriteFile(conf, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("notes v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The typed phrase is required, and it is the literal words.
	path := fmt.Sprintf("/api/v1/backups/runs/%d/restore", run.ID)
	if response := admin.do(http.MethodPost, path, `{"inPlace":true}`, nil); response.Code != http.StatusPreconditionRequired && response.Code != http.StatusBadRequest && response.Code != http.StatusConflict {
		t.Fatalf("restore in place without confirmation: %d %s", response.Code, response.Body.String())
	}
	if body, _ := os.ReadFile(conf); string(body) != "v2" {
		t.Fatal("an unconfirmed restore wrote files")
	}
	response := admin.do(http.MethodPost, path, `{"inPlace":true,"paths":["source-0001/etc"]}`, map[string]string{"X-Confirm": "restore in place"})
	if response.Code != http.StatusOK {
		t.Fatalf("restore in place: %d %s", response.Code, response.Body.String())
	}
	var res backups.RestoreResult
	if err := json.Unmarshal(response.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Targets) != 1 || res.Targets[0] != source {
		t.Fatalf("targets: %+v", res.Targets)
	}
	if body, _ := os.ReadFile(conf); string(body) != "v1" {
		t.Fatalf("chosen path not restored: %q", body)
	}
	if body, _ := os.ReadFile(filepath.Join(source, "notes.txt")); string(body) != "notes v2" {
		t.Fatalf("path outside the subset was overwritten: %q", body)
	}

	// A download is an administrator's act and carries the artifact's name.
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "restore-reader", auth.RoleReadOnly)}
	download := fmt.Sprintf("/api/v1/backups/runs/%d/download", run.ID)
	if response := reader.do(http.MethodGet, download, "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("read-only downloaded an archive: %d", response.Code)
	}
	response = admin.do(http.MethodGet, download, "", nil)
	if response.Code != http.StatusOK || response.Header().Get("Content-Disposition") == "" || response.Body.Len() == 0 {
		t.Fatalf("download: %d %q %d bytes", response.Code, response.Header().Get("Content-Disposition"), response.Body.Len())
	}
	if len(response.Body.Bytes()) < 2 || response.Body.Bytes()[0] != 0x1f || response.Body.Bytes()[1] != 0x8b {
		t.Fatal("download is not a gzip stream")
	}

	// The list carries the readings the page draws.
	response = admin.do(http.MethodGet, "/api/v1/backups/", "", nil)
	var jobs []backups.Job
	if err := json.Unmarshal(response.Body.Bytes(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Stored.Runs != 1 || jobs[0].Stored.Bytes == 0 || jobs[0].LastSuccessAt == nil || jobs[0].Overdue {
		t.Fatalf("readings: %+v", jobs[0])
	}
}
