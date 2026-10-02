package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A connection saved before it could be labelled or protected has to open as
// what it was: unlabelled, writable, with no notes. The columns arrive through
// applyAddedColumns, and a missing default would be a boot failure on exactly
// the installs that already have databases connected.
func TestOpenAddsConnectionLabelsToExistingConnections(t *testing.T) {
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
		INSERT INTO db_connections (name, driver, dsn_enc, created_at) VALUES ('orders', 'postgres', 'sealed', 1700000000);`)
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
	cols, err := tableColumns(context.Background(), st.DB, "db_connections")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"environment", "read_only", "notes"} {
		if !cols[want] {
			t.Errorf("column %q was not added to the existing table", want)
		}
	}
	var (
		name, dsn, environment, notes string
		readOnly                      int
	)
	err = st.DB.QueryRow(`SELECT name, dsn_enc, environment, read_only, notes FROM db_connections WHERE id = 1`).
		Scan(&name, &dsn, &environment, &readOnly, &notes)
	if err != nil {
		t.Fatalf("the pre-existing connection did not survive: %v", err)
	}
	if name != "orders" || dsn != "sealed" {
		t.Errorf("the connection changed: %q %q", name, dsn)
	}
	if environment != "" || readOnly != 0 || notes != "" {
		t.Errorf("an existing connection came back labelled or protected: %q %d %q", environment, readOnly, notes)
	}

	// A fresh install has the same columns from its CREATE TABLE.
	fresh, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	freshCols, err := tableColumns(context.Background(), fresh.DB, "db_connections")
	if err != nil {
		t.Fatal(err)
	}
	if len(freshCols) != len(cols) {
		t.Errorf("a fresh database has %d connection columns, an upgraded one %d", len(freshCols), len(cols))
	}
}
