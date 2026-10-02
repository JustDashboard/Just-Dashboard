package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A connection saved before discovery recorded where connections come from
// has no origin. It must open as one whose origin is empty — which the
// inventory reads as "match it by its address" — and never as NULL, which the
// plain string scan every reader of this table uses would fail on.
func TestOpenAddsTheConnectionOriginToAnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	old, err := sql.Open("sqlite", filepath.Join(dir, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
		CREATE TABLE db_connections (
		  id         INTEGER PRIMARY KEY AUTOINCREMENT,
		  name       TEXT NOT NULL UNIQUE,
		  driver     TEXT NOT NULL,
		  dsn_enc    TEXT NOT NULL,
		  created_at INTEGER NOT NULL
		);
		INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES('legacy', 'postgres', 'sealed', 1700000000);`)
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

	var name, origin string
	if err := st.DB.QueryRow(`SELECT name, origin FROM db_connections WHERE id = 1`).Scan(&name, &origin); err != nil {
		t.Fatalf("the pre-existing connection did not survive: %v", err)
	}
	if name != "legacy" || origin != "" {
		t.Errorf("row = %q origin %q, want the row unchanged with an empty origin", name, origin)
	}
	// And the table the ignore list lives in arrives with it.
	if _, err := st.DB.Exec(`INSERT INTO db_inventory_ignored(origin, ignored_at) VALUES('docker:pg', 1)`); err != nil {
		t.Fatalf("db_inventory_ignored: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO db_inventory_ignored(origin, ignored_at) VALUES('docker:pg', 2)`); err == nil {
		t.Error("the same key was ignored twice")
	}
}
