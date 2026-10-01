package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// The query history learned an error text and a count of rows changed, and a
// saved query learned when it was last edited. An install that predates them
// must gain the columns on the next boot with every existing row left as it
// was: the history and the saved queries are an operator's own work.
func TestOpenAddsWorkbenchColumnsToAPreExistingDatabase(t *testing.T) {
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
		INSERT INTO db_connections (name, driver, dsn_enc, created_at) VALUES ('main', 'postgres', 'sealed', 1700000000);
		CREATE TABLE db_saved_queries (
		  id            INTEGER PRIMARY KEY AUTOINCREMENT,
		  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
		  name          TEXT NOT NULL,
		  sql           TEXT NOT NULL,
		  created_at    INTEGER NOT NULL
		);
		INSERT INTO db_saved_queries (connection_id, name, sql, created_at) VALUES (1, 'active users', 'SELECT 1', 1700000001);
		CREATE TABLE db_query_history (
		  id            INTEGER PRIMARY KEY AUTOINCREMENT,
		  connection_id INTEGER NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
		  sql           TEXT NOT NULL,
		  risk          TEXT NOT NULL DEFAULT 'read',
		  success       INTEGER NOT NULL DEFAULT 1,
		  duration_ms   INTEGER NOT NULL DEFAULT 0,
		  row_count     INTEGER NOT NULL DEFAULT 0,
		  ran_at        INTEGER NOT NULL
		);
		INSERT INTO db_query_history (connection_id, sql, duration_ms, row_count, ran_at) VALUES (1, 'SELECT 2', 12, 3, 1700000002);`)
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
	for table, columns := range map[string][]string{
		"db_query_history": {"error", "rows_affected"},
		"db_saved_queries": {"updated_at"},
	} {
		have, err := tableColumns(ctx, st.DB, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range columns {
			if !have[want] {
				t.Errorf("%s.%s was not added to the existing table", table, want)
			}
		}
	}
	var (
		query, failure  string
		rows, affected  int
		name            string
		created, edited int64
	)
	if err := st.DB.QueryRow(`SELECT sql, row_count, rows_affected, error FROM db_query_history`).
		Scan(&query, &rows, &affected, &failure); err != nil {
		t.Fatal(err)
	}
	if query != "SELECT 2" || rows != 3 || affected != 0 || failure != "" {
		t.Errorf("history row = %q %d %d %q", query, rows, affected, failure)
	}
	if err := st.DB.QueryRow(`SELECT name, created_at, updated_at FROM db_saved_queries`).
		Scan(&name, &created, &edited); err != nil {
		t.Fatal(err)
	}
	if name != "active users" || created != 1700000001 || edited != 0 {
		t.Errorf("saved query = %q %d %d", name, created, edited)
	}
}
