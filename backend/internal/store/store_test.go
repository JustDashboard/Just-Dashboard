package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// The schema is one CREATE TABLE IF NOT EXISTS block and there is no migration
// tool, so a column added later is a no-op against a database that already has
// the table. This is the test that the second mechanism — applyAddedColumns —
// actually closes that gap, and that it does so without touching the rows that
// are already there.
func TestOpenAddsColumnsToAPreExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DatabaseFile)

	// A database as it was shipped before the CPU breakdown, pressure and
	// socket columns existed, holding one sample.
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
		CREATE TABLE metric_samples (
		  ts             INTEGER PRIMARY KEY,
		  cpu_percent    REAL NOT NULL DEFAULT 0,
		  load1          REAL NOT NULL DEFAULT 0,
		  mem_percent    REAL NOT NULL DEFAULT 0,
		  mem_used       INTEGER NOT NULL DEFAULT 0,
		  mem_total      INTEGER NOT NULL DEFAULT 0,
		  swap_percent   REAL NOT NULL DEFAULT 0,
		  net_rx         REAL NOT NULL DEFAULT 0,
		  net_tx         REAL NOT NULL DEFAULT 0,
		  disk_read      REAL NOT NULL DEFAULT 0,
		  disk_write     REAL NOT NULL DEFAULT 0,
		  disk_percent   REAL NOT NULL DEFAULT 0,
		  uptime_seconds INTEGER NOT NULL DEFAULT 0
		);
		INSERT INTO metric_samples (ts, cpu_percent, uptime_seconds) VALUES (1700000000, 42.5, 99);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open on a pre-existing database: %v", err)
	}
	defer st.Close()

	cols, err := tableColumns(context.Background(), st.DB, "metric_samples")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cpu_steal", "psi_io", "disk_await", "tcp_conns", "load15"} {
		if !cols[want] {
			t.Errorf("column %q was not added to the existing table", want)
		}
	}

	// The old row must still be there, and its new columns must read as the
	// declared default rather than as NULL — a NULL would break every AVG()
	// in the range queries for as long as that row is retained.
	var cpu, steal float64
	var uptime int64
	err = st.DB.QueryRow(
		`SELECT cpu_percent, cpu_steal, uptime_seconds FROM metric_samples WHERE ts = 1700000000`).
		Scan(&cpu, &steal, &uptime)
	if err != nil {
		t.Fatalf("the pre-existing row did not survive: %v", err)
	}
	if cpu != 42.5 || uptime != 99 {
		t.Errorf("row changed: cpu=%v uptime=%v, want 42.5/99", cpu, uptime)
	}
	if steal != 0 {
		t.Errorf("backfilled cpu_steal = %v, want the 0 default", steal)
	}
}

// Open runs on every boot, so adding the columns has to be idempotent — the
// second start must not fail with "duplicate column name".
func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := range 3 {
		st, err := Open(dir)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Every added column needs a DEFAULT: SQLite refuses to add a NOT NULL column
// to a table that has rows without one, so a missing default is a boot failure
// on exactly the installs the mechanism exists to serve.
func TestAddedColumnsAllDeclareADefault(t *testing.T) {
	for _, c := range addedColumns {
		if !strings.Contains(c.spec, "DEFAULT") {
			t.Errorf("%s.%s has no DEFAULT: %q", c.table, c.column, c.spec)
		}
	}
}

func TestReopenReleasesPreviouslyArchivedDeploymentNames(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at, archived_at) VALUES('same-repository', '/srv/app', 'sealed', 'old-hook', 1, 2)`)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var historical, reserved string
	if err := st.DB.QueryRow(`SELECT archived_name, name FROM deploy_projects WHERE hook_id = 'old-hook'`).Scan(&historical, &reserved); err != nil {
		t.Fatal(err)
	}
	if historical != "same-repository" || reserved == historical {
		t.Fatalf("history=%q reserved=%q", historical, reserved)
	}
	if _, err := st.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at) VALUES('same-repository', '/srv/app', 'sealed', 'new-hook', 3)`); err != nil {
		t.Fatalf("name reuse after upgrade: %v", err)
	}
	var count int
	st.DB.QueryRow(`SELECT COUNT(*) FROM deploy_projects`).Scan(&count)
	if count != 2 {
		t.Fatal("migration erased project history")
	}
}
