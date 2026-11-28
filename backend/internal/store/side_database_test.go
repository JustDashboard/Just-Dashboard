package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A side database is held to what the store's own file is: private from the
// first start, in WAL mode, with a writer of one connection and readers that
// cannot write.
func TestSideDatabaseIsPrivateAndReadersCannotWrite(t *testing.T) {
	underTheServersUmask(t)
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := OpenSideDatabase(dir, "side.db", `CREATE TABLE IF NOT EXISTS t (v INTEGER);`)
	if err != nil {
		t.Fatalf("OpenSideDatabase: %v", err)
	}
	defer db.Close()
	for _, name := range []string{"side.db", "side.db-wal", "side.db-shm"} {
		if got := modeOf(t, filepath.Join(dir, name)); got != 0o600 {
			t.Errorf("%s is %04o; want 0600", name, got)
		}
	}
	ctx := context.Background()
	var mode string
	if err := db.Writer.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q (%v), want wal", mode, err)
	}
	if _, err := db.Writer.ExecContext(ctx, `INSERT INTO t(v) VALUES (1)`); err != nil {
		t.Fatalf("the writer cannot write: %v", err)
	}
	if _, err := db.Reader.ExecContext(ctx, `INSERT INTO t(v) VALUES (2)`); err == nil {
		t.Fatal("a reader connection wrote")
	}
	var n int
	if err := db.Reader.QueryRowContext(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the reader sees %d rows (%v), want the writer's one", n, err)
	}
}

// The store's own file is never opened a second time under another schema,
// and a name is a file in the data directory, not a path out of it.
func TestSideDatabaseRefusesTheStoreAndPaths(t *testing.T) {
	for _, name := range []string{"", DatabaseFile, "../x.db", "sub/x.db"} {
		if db, err := OpenSideDatabase(t.TempDir(), name, ``); err == nil {
			db.Close()
			t.Errorf("%q was opened as a side database", name)
		}
	}
}
