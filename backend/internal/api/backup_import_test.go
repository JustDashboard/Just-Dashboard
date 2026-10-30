package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/config"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// backupServerWithRoots is testServer with the file roots named, so two
// servers in one test can share a directory the way two hosts share a
// bucket: one writes its archives there and the other reads them.
func backupServerWithRoots(t *testing.T, roots ...string) *Server {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	cfg := &config.Config{
		Addr: "127.0.0.1:8080", DataDir: t.TempDir(), AllowedCIDRs: []*net.IPNet{loopback},
		SessionTTL: time.Hour, IdleTTL: time.Minute, FileRoots: roots, LogRoots: []string{t.TempDir()},
		// The live import test makes and reads volumes; the others never
		// reach Docker, and a host without it answers them as unavailable.
		DockerHost: "unix:///var/run/docker.sock",
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := auth.NewService(st, sealer, cfg.SessionTTL, cfg.IdleTTL, cfg.Require2FA)
	s := New(cfg, log, st, svc, sealer, audit.New(st, log), nil)
	t.Cleanup(s.Shutdown)
	return s
}

type importFixture struct {
	old, fresh   *Server
	oldAdmin     *client
	admin        *client
	limited      *client
	reader       *client
	destination  string
	source       string
	oldJob       backups.Job
	first, other *backups.Run
}

// newImportFixture is a server that backed a directory up twice to a shared
// directory, and a fresh server beside it with nothing of its own yet.
func newImportFixture(t *testing.T, encrypt bool) *importFixture {
	t.Helper()
	shared := t.TempDir()
	f := &importFixture{
		old: backupServerWithRoots(t, shared, t.TempDir()), fresh: backupServerWithRoots(t, shared, t.TempDir()),
		destination: filepath.Join(shared, "archives"), source: filepath.Join(shared, "srv", "app"),
	}
	f.oldAdmin = &client{t: t, h: f.old.Routes(), cookie: signInAs(t, f.old, "old-admin", auth.RoleAdmin)}
	f.admin = &client{t: t, h: f.fresh.Routes(), cookie: signInAs(t, f.fresh, "import-admin", auth.RoleAdmin)}
	f.limited = &client{t: t, h: f.fresh.Routes(), cookie: signInAs(t, f.fresh, "import-operator", auth.RoleLimited)}
	f.reader = &client{t: t, h: f.fresh.Routes(), cookie: signInAs(t, f.fresh, "import-reader", auth.RoleReadOnly)}
	if err := os.MkdirAll(filepath.Join(f.source, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.source, "etc", "app.conf"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"name": "App files", "sources": []string{f.source}, "targetKind": "local",
		"target": map[string]string{"path": f.destination}, "retention": 5, "enabled": true, "schedule": "0 3 * * *", "encrypt": encrypt})
	response := f.oldAdmin.do(http.MethodPost, "/api/v1/backups/", string(body), nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create job: %d %s", response.Code, response.Body.String())
	}
	f.oldJob = decodeBackupJob(t, response.Body.Bytes())
	var err error
	if f.first, err = f.old.modules.backupRunner.Execute(t.Context(), f.oldJob.ID, "test"); err != nil || f.first.Status != backups.StatusSuccess {
		t.Fatalf("first run = %+v, %v", f.first, err)
	}
	if err := os.WriteFile(filepath.Join(f.source, "etc", "app.conf"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f.other, err = f.old.modules.backupRunner.Execute(t.Context(), f.oldJob.ID, "test"); err != nil || f.other.Status != backups.StatusSuccess {
		t.Fatalf("second run = %+v, %v", f.other, err)
	}
	return f
}

func (f *importFixture) entered() string {
	return fmt.Sprintf(`"targetKind":"local","target":{"path":%q}`, f.destination)
}

func (f *importFixture) catalogue(t *testing.T, who *client) backups.Catalogue {
	t.Helper()
	response := who.do(http.MethodPost, "/api/v1/backups/catalogue", "{"+f.entered()+"}", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("catalogue: %d %s", response.Code, response.Body.String())
	}
	var catalogue backups.Catalogue
	if err := json.Unmarshal(response.Body.Bytes(), &catalogue); err != nil {
		t.Fatal(err)
	}
	return catalogue
}

func lastBackupAudit(t *testing.T, s *Server, action string) (target, detail string, success bool) {
	t.Helper()
	if err := s.Store.DB.QueryRow(`SELECT target, detail, success FROM audit_log WHERE action = ? ORDER BY id DESC LIMIT 1`, action).
		Scan(&target, &detail, &success); err != nil {
		t.Fatalf("no %s audit entry: %v", action, err)
	}
	return target, detail, success
}

func TestAFreshServerReadsADestinationsCatalogueAndImportsAJobsHistory(t *testing.T) {
	f := newImportFixture(t, false)
	for _, who := range []*client{f.reader, f.limited} {
		for _, route := range []string{"/api/v1/backups/catalogue", "/api/v1/backups/import"} {
			if response := who.do(http.MethodPost, route, "{"+f.entered()+`,"keys":["x"]}`, nil); response.Code != http.StatusForbidden {
				t.Fatalf("%s without the administrator's capability: %d", route, response.Code)
			}
		}
	}

	catalogue := f.catalogue(t, f.admin)
	if len(catalogue.Origins) != 1 || len(catalogue.Origins[0].Jobs) != 1 || len(catalogue.Origins[0].Jobs[0].Archives) != 2 {
		t.Fatalf("catalogue = %+v", catalogue)
	}
	origin, job := catalogue.Origins[0], catalogue.Origins[0].Jobs[0]
	if origin.ThisServer || job.Name != "App files" || job.OriginJobID != f.oldJob.ID || job.Archives[0].Key != f.other.Artifact || !job.Archives[0].Importable {
		t.Fatalf("job = %+v", job)
	}
	if _, detail, _ := lastBackupAudit(t, f.fresh, "backup.catalogue"); !strings.Contains(detail, `"archives":2`) {
		t.Fatalf("catalogue audit = %s", detail)
	}
	// The server that wrote them lists none of them, and says how many it left out.
	own := f.catalogue(t, f.oldAdmin)
	if own.Archives != 0 || own.Ours != 2 {
		t.Fatalf("the writer's own catalogue = %+v", own)
	}

	response := f.admin.do(http.MethodPost, "/api/v1/backups/import",
		fmt.Sprintf(`{%s,"originId":%q,"originJobId":%d}`, f.entered(), origin.ID, job.OriginJobID), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
	var result backups.ImportResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 1 || len(result.Runs) != 2 {
		t.Fatalf("result = %+v", result)
	}
	imported := result.Jobs[0]
	target, detail, success := lastBackupAudit(t, f.fresh, "backup.import")
	if !success || target != f.destination || !strings.Contains(detail, fmt.Sprintf(`"runs":[%d,%d]`, result.Runs[0].ID, result.Runs[1].ID)) {
		t.Fatalf("import audit = %q %s %v", target, detail, success)
	}

	// Beside this server's own jobs, marked as what it is.
	response = f.reader.do(http.MethodGet, "/api/v1/backups/", "", nil)
	var jobs []backups.Job
	if err := json.Unmarshal(response.Body.Bytes(), &jobs); err != nil || len(jobs) != 1 {
		t.Fatalf("jobs = %s, %v", response.Body.String(), err)
	}
	if !jobs[0].Imported || jobs[0].Origin == nil || jobs[0].Origin.JobName != "App files" || jobs[0].Stored.Runs != 2 || jobs[0].NextRun != nil {
		t.Fatalf("the imported job reads as %+v", jobs[0])
	}
	response = f.reader.do(http.MethodGet, fmt.Sprintf("/api/v1/backups/%d/runs", imported.ID), "", nil)
	var runs struct {
		Runs []backups.Run `json:"runs"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &runs); err != nil || len(runs.Runs) != 2 || !runs.Runs[0].Imported || runs.Runs[0].Artifact != f.other.Artifact {
		t.Fatalf("runs = %s", response.Body.String())
	}

	// It is a record, not a schedule: nothing here runs, resumes or edits it.
	for _, attempt := range []struct{ method, path, body string }{
		{http.MethodPost, fmt.Sprintf("/api/v1/backups/%d/run", imported.ID), ""},
		{http.MethodPost, fmt.Sprintf("/api/v1/backups/%d/enabled", imported.ID), `{"enabled":true}`},
		{http.MethodPut, fmt.Sprintf("/api/v1/backups/%d", imported.ID), fmt.Sprintf(`{"name":"x","sources":[%q],"targetKind":"local","target":{"path":%q},"schedule":"0 3 * * *"}`, f.source, f.destination)},
	} {
		response := f.admin.do(attempt.method, attempt.path, attempt.body, nil)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "backup_job_imported") {
			t.Fatalf("%s %s on an imported job: %d %s", attempt.method, attempt.path, response.Code, response.Body.String())
		}
	}

	// It restores in place on this host through the same route, and makes
	// the tree it lacks.
	if err := os.RemoveAll(filepath.Dir(f.source)); err != nil {
		t.Fatal(err)
	}
	newest := result.Runs[1]
	response = f.reader.do(http.MethodGet, fmt.Sprintf("/api/v1/backups/runs/%d/restore-plan", newest.ID), "", nil)
	var plan struct {
		Targets []backups.RestoreTarget `json:"targets"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil || response.Code != http.StatusOK || len(plan.Targets) != 1 ||
		plan.Targets[0].Creates != "directory" || plan.Targets[0].Exists {
		t.Fatalf("plan = %d %s", response.Code, response.Body.String())
	}
	if response := f.limited.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/runs/%d/restore", newest.ID), `{"inPlace":true}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("a restore without the destructive capability: %d", response.Code)
	}
	response = f.admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/runs/%d/restore", newest.ID), `{"inPlace":true}`, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", response.Code, response.Body.String())
	}
	if conf, err := os.ReadFile(filepath.Join(f.source, "etc", "app.conf")); err != nil || string(conf) != "v2" {
		t.Fatalf("restored app.conf = %q, %v", conf, err)
	}
	if _, detail, _ := lastBackupAudit(t, f.fresh, "backup.restore"); !strings.Contains(detail, `"created":["`+f.source+`"]`) {
		t.Fatalf("restore audit = %s", detail)
	}

	// Retention here never prunes what was imported: a job of this server's
	// own writing to the same place, keeping one archive, prunes its own.
	mine := f.admin.do(http.MethodPost, "/api/v1/backups/", fmt.Sprintf(`{"name":"Mine","sources":[%q],"targetKind":"local","target":{"path":%q},"retention":1,"enabled":false,"schedule":""}`, f.source, f.destination), nil)
	if mine.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", mine.Code, mine.Body.String())
	}
	mineJob := decodeBackupJob(t, mine.Body.Bytes())
	for range 2 {
		if run, err := f.fresh.modules.backupRunner.Execute(t.Context(), mineJob.ID, "test"); err != nil || run.Status != backups.StatusSuccess {
			t.Fatalf("own run = %+v, %v", run, err)
		}
	}
	if kept, _ := f.fresh.modules.backupStore.Runs(t.Context(), imported.ID, 10); len(kept) != 2 {
		t.Fatalf("retention pruned imported runs: %d left", len(kept))
	}
	for _, run := range []*backups.Run{f.first, f.other} {
		if _, err := os.Stat(run.Artifact); err != nil {
			t.Fatalf("retention removed an imported archive: %v", err)
		}
	}
	// And the coverage report does not count the imported job as protecting
	// a path here.
	response = f.reader.do(http.MethodGet, "/api/v1/backups/resources", "", nil)
	if strings.Contains(response.Body.String(), fmt.Sprintf(`"jobId":%d`, imported.ID)) {
		t.Fatalf("the imported job covers something here: %s", response.Body.String())
	}
}

func TestAnImportRefusesAnArchiveWhoseBytesAreNotItsManifests(t *testing.T) {
	f := newImportFixture(t, false)
	data, err := os.ReadFile(f.other.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-9] ^= 0x5a
	if err := os.WriteFile(f.other.Artifact, data, 0o600); err != nil {
		t.Fatal(err)
	}
	response := f.admin.do(http.MethodPost, "/api/v1/backups/import", fmt.Sprintf(`{%s,"keys":[%q]}`, f.entered(), f.other.Artifact), nil)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "archive_digest_mismatch") {
		t.Fatalf("a changed archive: %d %s", response.Code, response.Body.String())
	}
	if _, detail, success := lastBackupAudit(t, f.fresh, "backup.import"); success || !strings.Contains(detail, "digest") {
		t.Fatalf("a refused import's audit = %s %v", detail, success)
	}
	// The untouched one beside it still imports.
	response = f.admin.do(http.MethodPost, "/api/v1/backups/import", fmt.Sprintf(`{%s,"keys":[%q,%q]}`, f.entered(), f.first.Artifact, f.other.Artifact), nil)
	var result backups.ImportResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || len(result.Runs) != 1 ||
		len(result.Skipped) != 1 || result.Skipped[0].Key != f.other.Artifact {
		t.Fatalf("import beside a changed archive: %d %s", response.Code, response.Body.String())
	}
}

func TestAnEncryptedImportNamesTheKeyItNeedsUntilThatKeyIsImported(t *testing.T) {
	f := newImportFixture(t, true)
	recipient := f.other.Manifest.Encryption.Recipient
	response := f.admin.do(http.MethodPost, "/api/v1/backups/import", fmt.Sprintf(`{%s,"keys":[%q]}`, f.entered(), f.other.Artifact), nil)
	var body struct {
		Error struct {
			Code     string `json:"code"`
			Message  string `json:"message"`
			Resource string `json:"resource"`
		} `json:"error"`
	}
	refused := &body.Error
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusConflict ||
		refused.Code != "archive_key_unavailable" || refused.Resource != recipient || !strings.Contains(refused.Message, recipient) {
		t.Fatalf("import without the key: %d %s", response.Code, response.Body.String())
	}

	exported := f.oldAdmin.do(http.MethodPost, "/api/v1/backups/key/export", `{}`, nil)
	if exported.Code != http.StatusOK {
		t.Fatalf("export: %d", exported.Code)
	}
	keyBody, _ := json.Marshal(map[string]string{"key": exported.Body.String()})
	for _, who := range []*client{f.reader, f.limited} {
		if response := who.do(http.MethodPost, "/api/v1/backups/key/import", string(keyBody), nil); response.Code != http.StatusForbidden {
			t.Fatalf("a key import without the administrator's capability: %d", response.Code)
		}
	}
	if response := f.admin.do(http.MethodPost, "/api/v1/backups/key/import", `{"key":"not a key"}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("a key import of nothing: %d", response.Code)
	}
	response = f.admin.do(http.MethodPost, "/api/v1/backups/key/import", string(keyBody), nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"became":"own"`) {
		t.Fatalf("key import: %d %s", response.Code, response.Body.String())
	}
	target, detail, success := lastBackupAudit(t, f.fresh, "backup.key.import")
	if !success || target != recipient || strings.Contains(detail, "AGE-SECRET-KEY") {
		t.Fatalf("key import audit = %q %s", target, detail)
	}
	var key backups.ArchiveKey
	_ = json.Unmarshal(f.reader.do(http.MethodGet, "/api/v1/backups/key", "", nil).Body.Bytes(), &key)
	if key.Recipient != recipient || key.ExportedAt == nil {
		t.Fatalf("key status = %+v", key)
	}

	catalogue := f.catalogue(t, f.admin)
	if archive := catalogue.Origins[0].Jobs[0].Archives[0]; !archive.Openable || !archive.Importable || archive.Recipient != recipient {
		t.Fatalf("an archive the key now opens = %+v", archive)
	}
	response = f.admin.do(http.MethodPost, "/api/v1/backups/import", fmt.Sprintf(`{%s,"keys":[%q]}`, f.entered(), f.other.Artifact), nil)
	var result backups.ImportResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || len(result.Runs) != 1 {
		t.Fatalf("import with the key: %d %s", response.Code, response.Body.String())
	}
	restoreDir := filepath.Join(f.fresh.Cfg.FileRoots[1], "restored")
	response = f.admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/runs/%d/restore", result.Runs[0].ID), fmt.Sprintf(`{"destination":%q}`, restoreDir), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", response.Code, response.Body.String())
	}
	if conf, err := os.ReadFile(filepath.Join(restoreDir, "source-0001", "etc", "app.conf")); err != nil || string(conf) != "v2" {
		t.Fatalf("restored = %q, %v", conf, err)
	}

	// With the key gone again, a restore names the key it needs.
	if _, err := f.fresh.Store.DB.Exec(`DELETE FROM settings WHERE key = 'backups.archive-key'`); err != nil {
		t.Fatal(err)
	}
	response = f.admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/runs/%d/restore", result.Runs[0].ID), fmt.Sprintf(`{"destination":%q}`, restoreDir), nil)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), recipient) {
		t.Fatalf("a restore without the key: %d %s", response.Code, response.Body.String())
	}
}

func TestAnImportedRunsDumpIsNamedByItsPlaceInTheArchive(t *testing.T) {
	f := newImportFixture(t, false)
	response := f.admin.do(http.MethodPost, "/api/v1/backups/import", fmt.Sprintf(`{%s,"keys":[%q]}`, f.entered(), f.other.Artifact), nil)
	var result backups.ImportResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || len(result.Runs) != 1 {
		t.Fatalf("import: %d %s", response.Code, response.Body.String())
	}
	response = f.admin.do(http.MethodPost, fmt.Sprintf("/api/v1/backups/runs/%d/restore-database", result.Runs[0].ID), `{"connectionId":1}`, nil)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "another server") {
		t.Fatalf("an imported dump restored by the other server's connection id: %d %s", response.Code, response.Body.String())
	}
}
