package api

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// A database a backup job dumps every night is backed up, though nothing is
// in its dump directory: the fleet and the connection's own summary say when
// it was last backed up by the newer of the directory's newest dump and the
// newest successful job run that holds the connection's dump. Either alone
// was wrong one way or the other, and a reader sees the same answer.
func TestFleetLastBackupIsTheNewerOfADumpAndABackupJobRun(t *testing.T) {
	h := newConnHarness(t)
	reader := h.as(auth.RoleReadOnly)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)

	ids := map[string]int64{}
	for _, name := range []string{"shop", "crm", "logs", "notes"} {
		path := filepath.Join(h.s.Cfg.FileRoots[0], name+".db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE rows (id INTEGER PRIMARY KEY, body TEXT); INSERT INTO rows(body) VALUES ('one')`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		ids[name] = h.add(name, dbx.DriverSQLite, path)
	}
	// crm was dumped by hand before the job last ran, logs after it.
	dumpedAt := map[string]time.Time{"crm": now.Add(-30 * time.Hour), "logs": now.Add(-time.Hour)}
	for name, at := range dumpedAt {
		dir := h.s.dbDumpDir(name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, name+"-by-hand.sql")
		if err := os.WriteFile(file, []byte("CREATE TABLE rows (id INTEGER PRIMARY KEY, body TEXT);\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, at, at); err != nil {
			t.Fatal(err)
		}
	}

	job, err := h.s.modules.backupStore.Create(ctx, &backups.Job{
		Name: "nightly", DatabaseDumps: []int64{ids["shop"], ids["crm"], ids["logs"]},
		TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: filepath.Join(h.s.Cfg.FileRoots[0], "archives")},
		Schedule: "0 3 * * *", Enabled: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.s.modules.backupRunner.Execute(ctx, job.ID, "schedule")
	if err != nil || run.Status != backups.StatusSuccess {
		t.Fatalf("the job's run = %+v, %v", run, err)
	}
	jobRanAt := now.Add(-10 * time.Hour)
	if _, err := h.s.Store.DB.Exec(`UPDATE backup_runs SET started_at = ? WHERE id = ?`, jobRanAt.Unix(), run.ID); err != nil {
		t.Fatal(err)
	}
	// A later run that failed backs up nothing.
	if _, err := h.s.Store.DB.Exec(`INSERT INTO backup_runs(job_id, started_at, status, manifest_json) VALUES(?,?,?,?)`,
		job.ID, now.Add(-2*time.Hour).Unix(), string(backups.StatusFailed), `{"databaseDumps":[]}`); err != nil {
		t.Fatal(err)
	}

	logsDumpedAt := dumpedAt["logs"]
	want := map[string]*time.Time{"shop": &jobRanAt, "crm": &jobRanAt, "logs": &logsDumpedAt, "notes": nil}
	same := func(got, want *time.Time) bool {
		return (got == nil) == (want == nil) && (got == nil || got.Equal(*want))
	}

	fleet := readJSON[struct {
		Connections []struct {
			Name       string     `json:"name"`
			LastBackup *time.Time `json:"lastBackup"`
		} `json:"connections"`
	}](t, do(t, reader, http.MethodGet, "/databases/fleet", ""))
	if len(fleet.Connections) != len(want) {
		t.Fatalf("fleet = %+v", fleet.Connections)
	}
	for _, entry := range fleet.Connections {
		if !same(entry.LastBackup, want[entry.Name]) {
			t.Errorf("fleet: %s last backed up %v, want %v", entry.Name, entry.LastBackup, want[entry.Name])
		}
	}
	for name, id := range ids {
		summary := readJSON[struct {
			LastBackup *time.Time `json:"lastBackup"`
		}](t, do(t, reader, http.MethodGet, pathf("/databases/%d", id), ""))
		if !same(summary.LastBackup, want[name]) {
			t.Errorf("summary: %s last backed up %v, want %v", name, summary.LastBackup, want[name])
		}
	}
}
