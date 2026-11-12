package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// The store's file holds every account's password hash, the audit trail and
// every sealed secret, and so do the write-ahead log and its index beside it:
// the log is the pages written most recently. The data directory is the
// host's own, and the dashboard manages Linux accounts, so other people with
// a shell on the server are an expected case. None of the three files may be
// readable by them at any moment, the first start included.
//
// SQLite creates the -wal and the -shm with the mode of the database file,
// whatever the umask is. So the database is made private before a connection
// opens it, not after the schema has run: that used to leave both 0644 from
// a first start until the restart that recreated them.
//
// The process umask is left alone on purpose. The server starts host
// commands (package installs, builds, the terminal), and a umask of 077
// handed down to them makes what they install unreadable to the accounts
// that are meant to read it.

// storeSidecars are the files SQLite keeps beside the database in WAL mode.
var storeSidecars = []string{"-wal", "-shm"}

// tightenDataDir takes listing and writing away from everyone but the owner
// of the data directory. It stays searchable: the host's nginx reaches its
// proxy cache under it as its own account (proxysvc.siteCacheRoot), and what
// must be private in it is private by its own mode. Bits are only removed,
// so a directory an operator closed further stays closed.
func tightenDataDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	const listOrWrite = 0o066
	if info.Mode().Perm()&listOrWrite == 0 {
		return nil
	}
	if err := os.Chmod(dir, info.Mode()&^listOrWrite); err != nil {
		return fmt.Errorf("close the data dir to other accounts: %w", err)
	}
	return nil
}

// prepareStoreFiles makes the database at path private before SQLite opens
// it, creating it when this is the first start, and does the same for a log
// and an index left by a run that did not close.
func prepareStoreFiles(path string) error {
	// O_EXCL: a descriptor is opened only for a file made here. Opening and
	// closing one on an existing database would drop SQLite's locks on it in
	// this process (see walGuard). An empty file is a new SQLite database.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	switch {
	case err == nil:
		if err := f.Close(); err != nil {
			return fmt.Errorf("create the store: %w", err)
		}
	case !errors.Is(err, fs.ErrExist):
		return fmt.Errorf("create the store: %w", err)
	}
	return keepStoreFilesPrivate(path)
}

// keepStoreFilesPrivate sets the database and whichever of its sidecars
// exist to 0600.
func keepStoreFilesPrivate(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	for _, suffix := range storeSidecars {
		if err := os.Chmod(path+suffix, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
