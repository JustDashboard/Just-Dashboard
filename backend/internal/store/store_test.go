package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAddsSealedEnvironmentToExistingDrafts(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`ALTER TABLE deploy_drafts DROP COLUMN environment_enc`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO deploy_drafts(id,owner_user_id,owner_username,created_at,updated_at,expires_at) VALUES('existing',1,'operator',1,1,2)`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var owner, sealed string
	if err := st.DB.QueryRow(`SELECT owner_username,environment_enc FROM deploy_drafts WHERE id='existing'`).Scan(&owner, &sealed); err != nil {
		t.Fatal(err)
	}
	if owner != "operator" || sealed != "" {
		t.Fatal("draft migration changed the existing row")
	}
}

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

// Pull request previews added columns to two shipped tables and one new table.
// An install that predates them must gain the columns with their defaults on
// the next boot, with every existing preview and variable row left as it was.
func TestOpenAddsPullRequestPreviewColumnsToAPreExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DatabaseFile)
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
		CREATE TABLE deploy_preview_refs (
		  id             INTEGER PRIMARY KEY AUTOINCREMENT,
		  trigger_id     INTEGER NOT NULL,
		  provider_ref   TEXT NOT NULL,
		  environment_id INTEGER NOT NULL,
		  state          TEXT NOT NULL DEFAULT 'open',
		  updated_at     INTEGER NOT NULL,
		  UNIQUE(trigger_id, provider_ref)
		);
		INSERT INTO deploy_preview_refs (trigger_id, provider_ref, environment_id, state, updated_at) VALUES (3, '42', 9, 'open', 1700000000);
		CREATE TABLE deploy_variable_revisions (
		  id             INTEGER PRIMARY KEY AUTOINCREMENT,
		  environment_id INTEGER NOT NULL,
		  key            TEXT NOT NULL,
		  revision       INTEGER NOT NULL,
		  sensitivity    TEXT NOT NULL DEFAULT 'secret',
		  scopes         TEXT NOT NULL DEFAULT 'runtime',
		  value_enc      TEXT NOT NULL,
		  value_digest   TEXT NOT NULL DEFAULT '',
		  active         INTEGER NOT NULL DEFAULT 1,
		  created_by     TEXT NOT NULL DEFAULT 'migration',
		  created_at     INTEGER NOT NULL,
		  UNIQUE(environment_id, key, revision)
		);
		INSERT INTO deploy_variable_revisions (environment_id, key, revision, value_enc, created_at) VALUES (9, 'TOKEN', 1, 'sealed', 1700000000);`)
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

	ctx := context.Background()
	previews, err := tableColumns(ctx, st.DB, "deploy_preview_refs")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"origin", "title", "head_revision", "variables_copied_revision"} {
		if !previews[want] {
			t.Errorf("deploy_preview_refs.%s was not added to the existing table", want)
		}
	}
	variables, err := tableColumns(ctx, st.DB, "deploy_variable_revisions")
	if err != nil {
		t.Fatal(err)
	}
	if !variables["copied_from_environment"] {
		t.Error("deploy_variable_revisions.copied_from_environment was not added to the existing table")
	}
	addresses, err := tableColumns(ctx, st.DB, "deploy_preview_addresses")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"environment_id", "kind", "port", "upstream_port", "url", "published", "updated_at"} {
		if !addresses[want] {
			t.Errorf("deploy_preview_addresses.%s is missing", want)
		}
	}

	var state, origin, title, head, copiedRevision string
	err = st.DB.QueryRow(
		`SELECT state, origin, title, head_revision, variables_copied_revision FROM deploy_preview_refs WHERE provider_ref = '42'`).
		Scan(&state, &origin, &title, &head, &copiedRevision)
	if err != nil {
		t.Fatalf("the pre-existing preview did not survive: %v", err)
	}
	if state != "open" || origin != "" || title != "" || head != "" || copiedRevision != "" {
		t.Errorf("preview row = %q/%q/%q/%q/%q, want open and empty defaults", state, origin, title, head, copiedRevision)
	}
	var sealed string
	var copiedFrom int64
	if err := st.DB.QueryRow(`SELECT value_enc, copied_from_environment FROM deploy_variable_revisions WHERE key = 'TOKEN'`).Scan(&sealed, &copiedFrom); err != nil {
		t.Fatalf("the pre-existing variable did not survive: %v", err)
	}
	if sealed != "sealed" || copiedFrom != 0 {
		t.Errorf("variable row = %q/%d, want sealed/0", sealed, copiedFrom)
	}
}
