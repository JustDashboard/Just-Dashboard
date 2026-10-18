package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// An install from before databases could be brought over from another server
// has a db_connections table and none of the imports. Opening it adds the
// table, and the rules that make it safe to use: a name is held only while an
// import under way holds what is named, and forgetting a connection takes its
// imports' history with it.
func TestOpenAddsOnlineImportsToAnExistingDatabase(t *testing.T) {
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

	add := func(name, status string) error {
		_, err := st.DB.Exec(`
			INSERT INTO db_online_imports(connection_id, name, mode, status, target_database, source_label, created_at, updated_at)
			VALUES(1, ?, 'replication', ?, 'copy', 'host:5432/shop', 1, 1)`, name, status)
		return err
	}
	if err := add("jd_import_shop", "replicating"); err != nil {
		t.Fatalf("db_online_imports: %v", err)
	}
	// The publication, the subscription and the slot are called the name, so
	// two imports under way cannot share one...
	if err := add("jd_import_shop", "running"); err == nil {
		t.Error("two imports under way took the same name")
	}
	// ...and one that has ended holds none.
	if _, err := st.DB.Exec(`UPDATE db_online_imports SET status = 'done', source_enc = ''`); err != nil {
		t.Fatal(err)
	}
	if err := add("jd_import_shop", "running"); err != nil {
		t.Errorf("a name was not free once its import ended: %v", err)
	}

	// The columns a row can leave out have defaults, so a row written by an
	// older build of this table's writer still reads.
	var enc, detail string
	var made int
	if err := st.DB.QueryRow(`SELECT source_enc, detail, publication_made FROM db_online_imports WHERE status = 'running'`).Scan(&enc, &detail, &made); err != nil {
		t.Fatal(err)
	}
	if enc != "" || detail != "" || made != 0 {
		t.Errorf("defaults = %q %q %d", enc, detail, made)
	}

	if _, err := st.DB.Exec(`DELETE FROM db_connections WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM db_online_imports`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d imports outlived their connection", left)
	}
}
