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
	if bare.Bulk() != st.DB || bare.Reader() != st.DB {
		t.Fatal("a store with no handles of its own must answer with its database")
	}
	st.SingleConnection()
	if st.Bulk() != st.DB || st.Reader() != st.DB || st.DB.Stats().MaxOpenConnections != 1 {
		t.Fatalf("after SingleConnection: bulk is its own handle = %v, read pool is its own = %v, connections = %d",
			st.Bulk() != st.DB, st.Reader() != st.DB, st.DB.Stats().MaxOpenConnections)
	}
}

// The read pool is the same database through connections that cannot write,
// and it sees what the writers have committed.
func TestTheReadPoolReadsWhatWasCommittedAndRefusesToWrite(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	read := st.Reader()
	if read == st.DB || read == st.Bulk() {
		t.Fatal("an opened store has no read pool of its own")
	}
	if got := pragma(t, read, "query_only"); got != "1" {
		t.Fatalf("the read pool's query_only = %s, want 1", got)
	}
	if got := read.Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("the read pool has %d connections, want 4", got)
	}
	if err := st.SetSetting(ctx, "durable-probe", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Bulk().Exec(`INSERT INTO settings(key, value) VALUES('bulk-probe', 'two')`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := read.QueryRow(`SELECT COUNT(*) FROM settings WHERE key IN ('durable-probe', 'bulk-probe')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("the read pool sees %d of the 2 committed rows (%v)", count, err)
	}
	if _, err := read.Exec(`INSERT INTO settings(key, value) VALUES('from-a-reader', 'no')`); err == nil {
		t.Fatal("the read pool accepted a write")
	}
	// A transaction on it begins without asking for the write lock, and
	// reads one snapshot throughout.
	tx, err := read.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin on the read pool: %v", err)
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'durable-probe'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("read in a transaction = %d (%v)", count, err)
	}
	if err := st.SetSetting(ctx, "written-meanwhile", "three"); err != nil {
		t.Fatalf("a write while a read transaction is open: %v", err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'written-meanwhile'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("the open read transaction saw a later commit: %d (%v)", count, err)
	}
}

// What the pools are apart for: slow reads piled up on the read pool do not
// stand between a request and the database, and a pile-up on the shared pool
// does not stop a chart from being read.
func TestSlowReadsAndTheSharedPoolDoNotWaitForEachOther(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSetting(context.Background(), "probe", "value"); err != nil {
		t.Fatal(err)
	}

	t.Run("the read pool is full", func(t *testing.T) {
		holdEveryConnection(t, st.Reader())
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, _, err := st.Setting(ctx, "probe"); err != nil {
			t.Fatalf("a read on the shared pool behind a full read pool: %v", err)
		}
		if err := st.SetSetting(ctx, "probe", "changed"); err != nil {
			t.Fatalf("a write behind a full read pool: %v", err)
		}
	})
	t.Run("the shared pool is full", func(t *testing.T) {
		holdEveryConnection(t, st.DB)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var value string
		if err := st.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'probe'`).Scan(&value); err != nil {
			t.Fatalf("a read on the read pool behind a full shared pool: %v", err)
		}
	})
}
