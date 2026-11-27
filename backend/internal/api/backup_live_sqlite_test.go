package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/requesthistory"
)

// A "Dashboard data" job saved before the request history existed lists the
// store alone as a snapshot. The history it now archives is committed every
// few seconds, and copied as a file the run failed ("write too long", "source
// changed while archiving") whenever the recorder wrote during the copy. The
// runner snapshots it whatever the job lists, and leaves its journal out.
func TestABackupSnapshotsTheRequestHistoryItsJobDoesNotList(t *testing.T) {
	cfg := testServerConfig(t)
	cfg.FileRoots = append(cfg.FileRoots, cfg.DataDir)
	s := newTestServer(t, cfg, nil)
	history := s.modules.requestHistory
	if history == nil {
		t.Fatal("the test server opened no request history")
	}
	line := `{"ts":"` + time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano) + `","msg":"handled request",` +
		`"request":{"client_ip":"203.0.113.7","proto":"HTTP/1.1","method":"GET","host":"shop.example.com","uri":"/",` +
		`"headers":{"User-Agent":["Mozilla/5.0"]}},"status":200,"size":1}`
	entry, ok := accesslog.Parse(line)
	if !ok {
		t.Fatal("the test's line did not parse")
	}
	if err := history.Append(t.Context(), "r", []accesslog.Entry{entry}, requesthistory.Cursor{Generation: "g", Offset: 1}); err != nil {
		t.Fatal(err)
	}
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "data-admin", auth.RoleAdmin)}
	request := map[string]any{
		"name": "dashboard-data", "sources": []string{cfg.DataDir},
		"excludes":   []string{filepath.Join(cfg.DataDir, "staging")},
		"targetKind": "local", "target": map[string]string{"path": filepath.Join(cfg.FileRoots[0], "data-artifacts")},
		"retention": 3,
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
	if len(job.SQLitePaths) != 0 {
		t.Fatalf("the job lists %v; it stands for one saved before the history", job.SQLitePaths)
	}
	run, err := s.modules.backupRunner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != backups.StatusSuccess {
		t.Fatalf("backup: %+v %v", run, err)
	}
	if run.Manifest == nil || len(run.Manifest.SQLiteSnapshots) != 1 || run.Manifest.SQLiteSnapshots[0].Path != history.Path() {
		t.Fatalf("the request history was not snapshotted: %+v", run.Manifest)
	}
	entries, err := s.modules.backupRunner.ListArchive(t.Context(), run.ID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if strings.HasSuffix(e.Name, requesthistory.File+"-wal") || strings.HasSuffix(e.Name, requesthistory.File+"-shm") {
			t.Fatalf("the history's live journal is in the archive: %s", e.Name)
		}
		found = found || strings.HasSuffix(e.Name, "/"+requesthistory.File)
	}
	if !found {
		t.Fatal("the archive holds no request history")
	}
}
