package backups

import (
	"database/sql"
	"path/filepath"
	"testing"

	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestRecoveryMigrationPreservesExistingJobsAndDoesNotInventEvidence(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, basestore.DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE backup_jobs (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, sources TEXT NOT NULL, excludes TEXT NOT NULL DEFAULT '',
 target_kind TEXT NOT NULL, target_cfg TEXT NOT NULL DEFAULT '{}', secrets_enc TEXT NOT NULL DEFAULT '',
 schedule TEXT NOT NULL DEFAULT '', retention INTEGER NOT NULL DEFAULT 7, enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL);
 CREATE TABLE backup_runs (
 id INTEGER PRIMARY KEY, job_id INTEGER NOT NULL REFERENCES backup_jobs(id), started_at INTEGER NOT NULL,
 ended_at INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, artifact TEXT NOT NULL DEFAULT '', size_bytes INTEGER NOT NULL DEFAULT 0,
 log TEXT NOT NULL DEFAULT '', trigger TEXT NOT NULL DEFAULT 'manual');
 INSERT INTO backup_jobs(id,name,sources,target_kind,target_cfg,schedule,created_at) VALUES(42,'existing-job','["/srv/important"]','local','{"path":"/var/backups"}','0 3 * * *',100);
 INSERT INTO backup_runs(id,job_id,started_at,ended_at,status,artifact) VALUES(43,42,101,102,'success','/var/backups/existing.tar.gz');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := basestore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	s := NewStore(upgraded, nil)
	job, err := s.Get(t.Context(), 42)
	if err != nil || job.Name != "existing-job" || job.Schedule != "0 3 * * *" || job.Sources[0] != "/srv/important" || job.Recovery != nil || len(job.SQLitePaths) != 0 {
		t.Fatalf("existing job changed: %+v %v", job, err)
	}
	run, err := s.Run(t.Context(), 43)
	if err != nil || run.Status != StatusSuccess || run.Artifact != "/var/backups/existing.tar.gz" || run.Manifest != nil || run.RestoreVerification != nil {
		t.Fatalf("invented legacy recovery evidence: %+v %v", run, err)
	}
}
