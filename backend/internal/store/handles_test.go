package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func pragma(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var value string
	if err := db.QueryRow(`PRAGMA ` + name).Scan(&value); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return value
}

// holdEveryConnection takes every connection a pool will ever give out and
// keeps them until the test ends, which is what a pile-up of slow calls does.
func holdEveryConnection(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range db.Stats().MaxOpenConnections {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("take a connection: %v", err)
		}
		t.Cleanup(func() { conn.Close() })
	}
}

// What the dashboard has acknowledged survives a power cut, so the pool
// everything uses syncs every commit; the bulk writer, for samples and build
// output, does not. Both are the same database, in WAL mode, with the same
// rules for writers.
func TestTheDurablePoolSyncsEveryCommitAndTheBulkWriterDoesNot(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.Bulk() == st.DB {
		t.Fatal("an opened store has no bulk writer of its own")
	}
	// synchronous: 2 is FULL, 1 is NORMAL.
	if got := pragma(t, st.DB, "synchronous"); got != "2" {
		t.Fatalf("the durable pool runs with synchronous=%s, want 2 (FULL)", got)
	}
	if got := pragma(t, st.Bulk(), "synchronous"); got != "1" {
		t.Fatalf("the bulk writer runs with synchronous=%s, want 1 (NORMAL)", got)
	}
	for name, db := range map[string]*sql.DB{"durable": st.DB, "bulk": st.Bulk()} {
		if got := pragma(t, db, "journal_mode"); got != "wal" {
			t.Fatalf("%s journal_mode = %s", name, got)
		}
		if got := pragma(t, db, "foreign_keys"); got != "1" {
			t.Fatalf("%s foreign_keys = %s", name, got)
		}
		if got := pragma(t, db, "busy_timeout"); got != "5000" {
			t.Fatalf("%s busy_timeout = %s", name, got)
		}
	}
	if got := st.Bulk().Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("the bulk writer has %d connections, want the one writer SQLite has", got)
	}
	// A row written through one is read through the other at once.
	if _, err := st.Bulk().Exec(`INSERT INTO settings(key, value) VALUES('bulk-probe', 'written')`); err != nil {
		t.Fatal(err)
	}
	if value, ok, err := st.Setting(context.Background(), "bulk-probe"); err != nil || !ok || value != "written" {
		t.Fatalf("read back %q %v %v", value, ok, err)
	}
}

// The bulk writer is its own connection: when every connection of the pool is
// taken, a sample is still written.
func TestTheBulkWriterDoesNotWaitForThePool(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	holdEveryConnection(t, st.DB)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := st.Bulk().ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('bulk-probe', 'written')`); err != nil {
		t.Fatalf("a bulk write behind an exhausted pool: %v", err)
	}
}

// A store somebody built around a database of their own has only that
// database, and a store collapsed for a test answers with it everywhere.
func TestAStoreWithoutHandlesAnswersWithItsDatabase(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bare := &Store{DB: st.DB}
	if bare.Bulk() != st.DB {
		t.Fatal("a store with no bulk writer must answer with its database")
	}
	st.SingleConnection()
	if st.Bulk() != st.DB || st.DB.Stats().MaxOpenConnections != 1 {
		t.Fatalf("after SingleConnection: bulk is its own handle = %v, connections = %d",
			st.Bulk() != st.DB, st.DB.Stats().MaxOpenConnections)
	}
}
