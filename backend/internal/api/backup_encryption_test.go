package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
)

func decodeBackupJob(t *testing.T, body []byte) backups.Job {
	t.Helper()
	var job backups.Job
	if err := json.Unmarshal(body, &job); err != nil {
		t.Fatal(err)
	}
	return job
}

// A job that leaves the server encrypts unless the operator says not to; one
// on this server's disk does not. A caller that predates the setting keeps
// whatever the job had.
func TestBackupJobEncryptionDefaultsByDestinationAndSurvivesOlderCallers(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "encrypt-admin", auth.RoleAdmin)}
	local, _ := fileBackupJob(t, s, admin, "local-plain", nil)
	if local.Encrypt {
		t.Fatal("a job on this server's disk encrypts by default")
	}
	remote := admin.do(http.MethodPost, "/api/v1/backups/", `{"name":"offsite","sources":["`+s.Cfg.FileRoots[0]+`"],
		"targetKind":"s3","target":{"bucket":"offsite"},"retention":3,"enabled":false,"schedule":""}`, nil)
	if remote.Code != http.StatusCreated || !decodeBackupJob(t, remote.Body.Bytes()).Encrypt {
		t.Fatalf("a new remote job does not encrypt: %d %s", remote.Code, remote.Body.String())
	}
	optedOut := admin.do(http.MethodPost, "/api/v1/backups/", `{"name":"offsite-clear","sources":["`+s.Cfg.FileRoots[0]+`"],
		"targetKind":"b2","target":{"bucket":"clear","endpoint":"s3.us-west-004.backblazeb2.com"},"retention":3,"enabled":false,"schedule":"","encrypt":false}`, nil)
	if optedOut.Code != http.StatusCreated || decodeBackupJob(t, optedOut.Body.Bytes()).Encrypt {
		t.Fatalf("an explicit opt-out was not kept: %d %s", optedOut.Code, optedOut.Body.String())
	}

	job := decodeBackupJob(t, remote.Body.Bytes())
	update := func(extra string) backups.Job {
		t.Helper()
		body := fmt.Sprintf(`{"name":"offsite","sources":[%q],"targetKind":"s3","target":{"bucket":"offsite"},"retention":5,"enabled":false,"schedule":""%s}`,
			s.Cfg.FileRoots[0], extra)
		response := admin.do(http.MethodPut, fmt.Sprintf("/api/v1/backups/%d", job.ID), body, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("update: %d %s", response.Code, response.Body.String())
		}
		return decodeBackupJob(t, response.Body.Bytes())
	}
	if updated := update(""); !updated.Encrypt || updated.Retention != 5 {
		t.Fatalf("an update without the field changed encryption: %+v", updated)
	}
	if updated := update(`,"encrypt":false`); updated.Encrypt {
		t.Fatal("turning encryption off was not saved")
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'backup.job.update' ORDER BY id DESC LIMIT 1`).Scan(&detail); err != nil || !strings.Contains(detail, `"encrypt":false`) {
		t.Fatalf("the update audit does not say what encryption became: %q %v", detail, err)
	}
}

func TestBackupKeyStatusIsReadableAndItsExportIsAnAuditedAdministratorAct(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "key-admin", auth.RoleAdmin)}
	limited := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "key-operator", auth.RoleLimited)}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "key-reader", auth.RoleReadOnly)}
	status := func() backups.ArchiveKey {
		t.Helper()
		response := reader.do(http.MethodGet, "/api/v1/backups/key", "", nil)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "AGE-SECRET-KEY") {
			t.Fatalf("key status: %d %s", response.Code, response.Body.String())
		}
		var key backups.ArchiveKey
		if err := json.Unmarshal(response.Body.Bytes(), &key); err != nil {
			t.Fatal(err)
		}
		return key
	}
	if key := status(); key.Exists {
		t.Fatalf("a key exists before anything asked for one: %+v", key)
	}
	// Saving a job that encrypts makes the key, so it can be exported before
	// the job first runs.
	job, _ := fileBackupJob(t, s, admin, "sealed", map[string]any{"encrypt": true})
	key := status()
	if !key.Exists || !strings.HasPrefix(key.Recipient, "age1") || key.ExportedAt != nil || key.EncryptingJobs != 1 {
		t.Fatalf("key after an encrypting job was saved: %+v", key)
	}

	for _, who := range []*client{reader, limited} {
		if response := who.do(http.MethodPost, "/api/v1/backups/key/export", `{}`, nil); response.Code != http.StatusForbidden {
			t.Fatalf("a non-administrator exported the key: %d", response.Code)
		}
	}
	response := admin.do(http.MethodPost, "/api/v1/backups/key/export", `{}`, nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(response.Header().Get("Content-Disposition"), "just-dashboard-backup-key.txt") {
		t.Fatalf("export: %d %v", response.Code, response.Header())
	}
	identities, err := age.ParseIdentities(bytes.NewReader(response.Body.Bytes()))
	if err != nil || len(identities) != 1 || identities[0].(*age.X25519Identity).Recipient().String() != key.Recipient {
		t.Fatalf("the exported file is not this key: %v", err)
	}
	var target, detail string
	if err := s.Store.DB.QueryRow(`SELECT target, detail FROM audit_log WHERE action = 'backup.key.export' AND success = 1`).Scan(&target, &detail); err != nil ||
		target != key.Recipient || strings.Contains(detail, "AGE-SECRET-KEY") {
		t.Fatalf("export audit: target %q detail %q, %v", target, detail, err)
	}
	if after := status(); after.ExportedAt == nil {
		t.Fatal("the export was not recorded on the key")
	}

	// An encrypted run downloads as the archive inside it, under its own name.
	run, err := s.modules.backupRunner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != backups.StatusSuccess || !strings.HasSuffix(run.Artifact, ".tar.gz.age") {
		t.Fatalf("run = %+v, %v", run, err)
	}
	download := admin.do(http.MethodGet, fmt.Sprintf("/api/v1/backups/runs/%d/download", run.ID), "", nil)
	disposition := download.Header().Get("Content-Disposition")
	if download.Code != http.StatusOK || !strings.HasSuffix(strings.Trim(disposition, `"`), ".tar.gz") ||
		download.Header().Get("Content-Length") != fmt.Sprint(run.Manifest.Encryption.PlaintextBytes) {
		t.Fatalf("download: %d %v", download.Code, download.Header())
	}
	gz, err := gzip.NewReader(download.Body)
	if err != nil {
		t.Fatalf("the download is not the decrypted archive: %v", err)
	}
	if _, err := io.Copy(io.Discard, gz); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'backup.download' ORDER BY id DESC LIMIT 1`).Scan(&detail); err != nil ||
		!strings.Contains(detail, `"decrypted":true`) {
		t.Fatalf("download audit: %q %v", detail, err)
	}
}

// An encrypted archive the server has no key for is refused by name, with a
// status the page can tell apart from a damaged archive.
func TestEncryptedRunWithoutItsKeyAnswersArchiveKeyUnavailable(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "lost-key-admin", auth.RoleAdmin)}
	job, _ := fileBackupJob(t, s, admin, "lost-key", map[string]any{"encrypt": true})
	run, err := s.modules.backupRunner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != backups.StatusSuccess {
		t.Fatalf("run = %+v, %v", run, err)
	}
	if _, err := s.Store.DB.Exec(`DELETE FROM settings WHERE key = 'backups.archive-key'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"contents", "download"} {
		response := admin.do(http.MethodGet, fmt.Sprintf("/api/v1/backups/runs/%d/%s", run.ID, path), "", nil)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "archive_key_unavailable") {
			t.Fatalf("%s without the key: %d %s", path, response.Code, response.Body.String())
		}
	}
}
