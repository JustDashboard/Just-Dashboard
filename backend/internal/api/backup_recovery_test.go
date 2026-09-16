package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
)

func sqliteRecoveryJob(t *testing.T, s *Server, image string) (*backups.Job, *sql.DB, string) {
	t.Helper()
	source := filepath.Join(s.Cfg.FileRoots[0], "recovery-source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(source, "state.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; PRAGMA user_version=7; CREATE TABLE records(id INTEGER PRIMARY KEY,value TEXT); INSERT INTO records VALUES(1,'known-canary')`); err != nil {
		t.Fatal(err)
	}
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "recovery-admin", auth.RoleAdmin)}
	request := map[string]any{
		"name": "application-recovery", "sources": []string{source}, "sqlitePaths": []string{path}, "targetKind": "local", "target": map[string]string{"path": filepath.Join(s.Cfg.FileRoots[0], "recovery-artifacts")}, "retention": 3,
		"recovery":               map[string]any{"image": image, "command": []string{"python", "/app/app.py", "verify", "/restore/source-0001/state.sqlite"}, "schemaVersion": "7", "timeoutSeconds": 30},
		"expectedRecoveryOutput": "schema-7:known-canary",
	}
	encoded, _ := json.Marshal(request)
	response := admin.do(http.MethodPost, "/api/v1/backups/", string(encoded), nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create recovery job: %d %s", response.Code, response.Body.String())
	}
	var job backups.Job
	if err := json.Unmarshal(response.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), "known-canary") {
		t.Fatal("expected canary output was stored/returned instead of its fingerprint")
	}
	return &job, db, source
}

func TestSQLiteBackupUsesNativeSnapshotAndExcludesLiveJournal(t *testing.T) {
	s := testServer(t)
	job, db, source := sqliteRecoveryJob(t, s, "sha256:"+strings.Repeat("a", 64))
	run, err := s.modules.backupRunner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != backups.StatusSuccess {
		t.Fatalf("backup: %+v %v", run, err)
	}
	if run.Manifest == nil || len(run.Manifest.SQLiteSnapshots) != 1 || run.Manifest.SQLiteSnapshots[0].Method != "sqlite_vacuum_into" {
		t.Fatalf("no consistency evidence: %+v", run.Manifest)
	}
	if _, err := db.Exec(`UPDATE records SET value='later-live-value' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(s.Cfg.FileRoots[0], "restored")
	if _, err := s.modules.backupRunner.Restore(t.Context(), run.ID, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", filepath.Join(destination, "source-0001", "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.QueryRow(`SELECT value FROM records WHERE id=1`).Scan(&value); err != nil || value != "known-canary" {
		t.Fatalf("restored record=%q %v", value, err)
	}
	entries, err := s.modules.backupRunner.ListArchive(t.Context(), run.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name, "-wal") || strings.HasSuffix(entry.Name, "-shm") {
			t.Fatal("live journal was mixed with consistent snapshot")
		}
	}
	if err := s.modules.backupRunner.VerifyCoverage(t.Context(), run.ID, []string{source}); err != nil {
		t.Fatal(err)
	}
	readonly := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "recovery-reader", auth.RoleReadOnly)}
	response := readonly.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/runs/%d/verify-restore", run.ID), "", nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read-only restore execution: %d", response.Code)
	}
}

func TestSQLiteSnapshotRefusesExcludedOrMissingSources(t *testing.T) {
	s := testServer(t)
	job, _, source := sqliteRecoveryJob(t, s, "sha256:"+strings.Repeat("a", 64))
	job.Excludes = []string{filepath.Base(source)}
	if _, err := s.modules.backupStore.Update(context.Background(), job.ID, job, nil); err == nil {
		// Ancestor globs may only become applicable while walking; they must
		// still fail execution rather than claiming a missing SQLite snapshot.
		run, err := s.modules.backupRunner.Execute(t.Context(), job.ID, "test")
		if err == nil && run.Status == backups.StatusSuccess {
			t.Fatal("excluded SQLite source reported successful backup")
		}
	}
	job.Excludes = nil
	job.SQLitePaths = []string{filepath.Join(source, "missing.sqlite")}
	if _, err := s.modules.backupStore.Update(t.Context(), job.ID, job, nil); err == nil {
		t.Fatal("accepted missing SQLite snapshot source")
	}
}
