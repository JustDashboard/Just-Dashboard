package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// A database of the data directory's own beside the store.
//
// Some records are written at the rate traffic arrives rather than the rate an
// operator acts, and are worth keeping but not worth what the store's file
// costs: every row in vpsd.db is copied by each update (selfupdate.dbcopy),
// carried by every backup snapshot of it, and written through the one writer
// the session checks and the deployment engine queue behind. A file of their
// own gives them a writer of their own, so a burst of requests never waits on
// a deployment's transition or the other way round, and lets the store stay
// the size of its configuration.
//
// What the store's file needs, this one needs too: it is private from the
// first byte, it keeps its log beside it, and it holds the same lock guard
// against a descriptor this process closes on it (wal_guard.go).

// SideDatabase is one such file, opened.
type SideDatabase struct {
	// Writer is the only connection that writes. One, because SQLite has one
	// writer and a second connection would only wait inside SQLite. Its
	// commits are synchronous=NORMAL: a power cut can lose the last seconds
	// written, never the file, which is the bargain Store.Bulk makes.
	Writer *sql.DB
	// Reader is a pool of connections that can only read, for the long scans
	// that must not stand between the writer and its next commit.
	Reader *sql.DB
	Path   string
	guard  *walGuard
}

// OpenSideDatabase opens, creating it on first use, the file name inside
// dataDir and applies schema. The schema is run on every open, so it is
// written as IF NOT EXISTS statements; anything that must precede the first
// table (auto_vacuum) belongs at its head.
func OpenSideDatabase(dataDir, name, schema string) (*SideDatabase, error) {
	if name == "" || name == DatabaseFile || filepath.Base(name) != name {
		return nil, fmt.Errorf("side database name %q is not a file of its own", name)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if err := tightenDataDir(dataDir); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, name)
	if err := prepareStoreFiles(path); err != nil {
		return nil, err
	}
	writer, err := sql.Open("sqlite", path+"?"+writerPragmas+"&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(1)
	if _, err := writer.ExecContext(context.Background(), schema); err != nil {
		writer.Close()
		return nil, fmt.Errorf("apply the %s schema: %w", name, err)
	}
	// Whatever the schema says: readers that never wait for the writer need
	// the WAL, and the guard below needs the -shm it keeps. A write after the
	// switch is what makes SQLite create the log and the index, which the
	// chmod and the guard both need to exist.
	if _, err := writer.ExecContext(context.Background(), `PRAGMA journal_mode = WAL; PRAGMA user_version = 1`); err != nil {
		writer.Close()
		return nil, fmt.Errorf("put %s in WAL mode: %w", name, err)
	}
	if err := keepStoreFilesPrivate(path); err != nil {
		writer.Close()
		return nil, err
	}
	guard, err := holdWALGuard(path)
	if err != nil {
		writer.Close()
		return nil, err
	}
	reader, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err == nil {
		reader.SetMaxOpenConns(4)
		err = reader.PingContext(context.Background())
	}
	if err != nil {
		if reader != nil {
			reader.Close()
		}
		guard.release()
		writer.Close()
		return nil, fmt.Errorf("open the %s read pool: %w", name, err)
	}
	return &SideDatabase{Writer: writer, Reader: reader, Path: path, guard: guard}, nil
}

// Close closes the readers, then the guard and the writer, in the order the
// store closes its own: the guard is released only on the way to closing the
// last connection.
func (d *SideDatabase) Close() error {
	if d == nil {
		return nil
	}
	d.Reader.Close()
	d.guard.release()
	return d.Writer.Close()
}
