package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
)

func requestForBackupJob(job *backups.Job) jobRequest {
	return jobRequest{ExpectedRevision: job.Revision, Name: job.Name,
		Sources: job.Sources, Excludes: job.Excludes, TargetKind: job.TargetKind, Target: job.Target,
		Schedule: job.Schedule, Retention: job.Retention, RetentionDays: job.RetentionDays, Enabled: job.Enabled,
		Recovery: job.Recovery, SQLitePaths: job.SQLitePaths, DatabaseDumps: job.DatabaseDumps, PauseContainers: job.PauseContainers}
}

func TestBackupJobAPIConflictsKeepNewerSettingsAndCredentials(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "job-update-admin", auth.RoleAdmin)}
	job, _ := fileBackupJob(t, s, admin, "versioned", map[string]any{
		"targetKind": "s3", "target": map[string]any{"bucket": "fixture-bucket", "region": "fixture-region"},
		"secrets": map[string]string{"accessKeyId": "fixture-key", "secretAccessKey": "fixture-secret"},
	})
	if job.Revision != 1 {
		t.Fatalf("new job revision: %d", job.Revision)
	}
	path := fmt.Sprintf("/api/v1/backups/%d", job.ID)
	a := requestForBackupJob(job)
	a.Retention = 10
	encoded, _ := json.Marshal(a)
	response := admin.do(http.MethodPut, path, string(encoded), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("first editor save: %d %s", response.Code, response.Body.String())
	}
	b := requestForBackupJob(job)
	b.Name = "stale-name"
	b.Secrets = &backups.TargetSecrets{AccessKeyID: "stale-key", SecretAccessKey: "stale-secret"}
	encoded, _ = json.Marshal(b)
	response = admin.do(http.MethodPut, path, string(encoded), nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "backup_job_conflict") {
		t.Fatalf("stale editor save: %d %s", response.Code, response.Body.String())
	}
	saved, err := s.modules.backupStore.Get(t.Context(), job.ID)
	keys, keyErr := s.modules.backupStore.Secrets(t.Context(), job.ID)
	if err != nil || keyErr != nil || saved.Retention != 10 || saved.Name != "versioned" || saved.Revision != 2 || keys.AccessKeyID != "fixture-key" {
		t.Fatal("API conflict changed the definition or credentials", err, keyErr)
	}
	// The next full save must notice a narrow pause made after the editor read.
	response = admin.do(http.MethodPost, path+"/enabled", `{"enabled":false}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", response.Code, response.Body.String())
	}
	encoded, _ = json.Marshal(requestForBackupJob(saved))
	if response = admin.do(http.MethodPut, path, string(encoded), nil); response.Code != http.StatusConflict {
		t.Fatalf("stale editor undid pause: %d %s", response.Code, response.Body.String())
	}
	// Older API clients keep their unversioned contract, but still advance the
	// version so an open dashboard form cannot overwrite their saved settings.
	legacy := requestForBackupJob(saved)
	legacy.ExpectedRevision = 0
	legacy.Enabled = false
	legacy.Retention = 12
	encoded, _ = json.Marshal(legacy)
	response = admin.do(http.MethodPut, path, string(encoded), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("legacy client save: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "fixture-secret") || strings.Contains(response.Body.String(), "fixture-key") {
		t.Fatal("save response exposed destination credentials")
	}
}

func TestBackupJobAPIFailedPublicationDoesNotReplaceCredentials(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "fault-update-admin", auth.RoleAdmin)}
	job, _ := fileBackupJob(t, s, admin, "fault-original", map[string]any{
		"secrets": map[string]string{"accessKeyId": "fixture-old-key", "secretAccessKey": "fixture-old-secret"},
	})
	if _, err := s.Store.DB.Exec(`CREATE TRIGGER reject_backup_definition BEFORE UPDATE OF name ON backup_jobs
 BEGIN SELECT RAISE(ABORT, 'fixture publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	req := requestForBackupJob(job)
	req.Name = "failed-change"
	req.Secrets = &backups.TargetSecrets{AccessKeyID: "fixture-new-key", SecretAccessKey: "fixture-new-secret"}
	encoded, _ := json.Marshal(req)
	response := admin.do(http.MethodPut, fmt.Sprintf("/api/v1/backups/%d", job.ID), string(encoded), nil)
	if response.Code < 400 {
		t.Fatalf("fault-injected publication succeeded: %d", response.Code)
	}
	saved, err := s.modules.backupStore.Get(t.Context(), job.ID)
	keys, keyErr := s.modules.backupStore.Secrets(t.Context(), job.ID)
	if err != nil || keyErr != nil || saved.Name != job.Name || saved.Revision != job.Revision || keys.AccessKeyID != "fixture-old-key" {
		t.Fatal("failed API publication changed the definition or credentials", err, keyErr)
	}
}

func TestBackupJobAPIAddDatabaseDumpIsNarrowAuditedAndAuthorized(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "dump-add-admin", auth.RoleAdmin)}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "dump-add-reader", auth.RoleReadOnly)}
	job, _ := fileBackupJob(t, s, admin, "native-add", map[string]any{"retention": 10, "enabled": false})
	sealed, err := s.Sealer.Seal("postgres://fixture:fixture@127.0.0.1:1/fixture")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Store.DB.Exec(`INSERT INTO db_connections(name,driver,dsn_enc,created_at) VALUES('dump-fixture','postgres',?,1)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, _ := result.LastInsertId()
	path := fmt.Sprintf("/api/v1/backups/%d/database-dumps", job.ID)
	body := fmt.Sprintf(`{"connectionId":%d}`, connectionID)
	if response := reader.do(http.MethodPost, path, body, nil); response.Code != http.StatusForbidden {
		t.Fatalf("reader added a native dump: %d", response.Code)
	}
	for range 2 {
		response := admin.do(http.MethodPost, path, body, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("add database dump: %d %s", response.Code, response.Body.String())
		}
	}
	saved, err := s.modules.backupStore.Get(t.Context(), job.ID)
	if err != nil || saved.Retention != 10 || saved.Enabled || len(saved.DatabaseDumps) != 1 || saved.DatabaseDumps[0] != connectionID || saved.Revision != job.Revision+1 {
		t.Fatalf("narrow helper changed other settings or duplicated a dump: %+v %v", saved, err)
	}
	var audits int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'backup.job.database_dump.add'`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("dump additions were not audited: %d %v", audits, err)
	}
	if _, err := s.Store.DB.Exec(`DELETE FROM db_connections WHERE id = ?`, connectionID); err != nil {
		t.Fatal(err)
	}
	if response := admin.do(http.MethodPost, path, body, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("deleted connection accepted: %d %s", response.Code, response.Body.String())
	}
	if response := admin.do(http.MethodPost, "/api/v1/backups/999/database-dumps", body, nil); response.Code != http.StatusNotFound {
		t.Fatalf("missing job: %d %s", response.Code, response.Body.String())
	}
}
