package store

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The server runs as root under the umask it was started with, which is 022
// in the container. These tests set it, so that what they prove does not
// depend on the umask of whoever runs them.
func underTheServersUmask(t *testing.T) {
	t.Helper()
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
}

func modeOf(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// A first start is where the write-ahead log was world-readable: the store's
// file was made private only after the schema had created the log beside it.
func TestOpenKeepsTheStoreItsLogAndItsIndexPrivateFromTheFirstStart(t *testing.T) {
	underTheServersUmask(t)
	// Docker makes the source of a bind mount 0755, and that is the data
	// directory the server finds on a new install.
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	for _, name := range []string{DatabaseFile, DatabaseFile + "-wal", DatabaseFile + "-shm"} {
		if got := modeOf(t, filepath.Join(dir, name)); got != 0o600 {
			t.Errorf("%s is %04o on a first start; want 0600", name, got)
		}
	}
	// Searchable, because the host's nginx reaches its cache under this
	// directory as its own account; not listable and not writable.
	if got := modeOf(t, dir); got != 0o711 {
		t.Errorf("the data directory is %04o; want 0711", got)
	}
}

// The log and the index are private from the moment SQLite makes them, not
// from a chmod that follows: nothing here touches them after they exist.
func TestTheLogAndTheIndexAreCreatedPrivateNotMadePrivateLater(t *testing.T) {
	underTheServersUmask(t)
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, DatabaseFile)
	if err := prepareStoreFiles(path); err != nil {
		t.Fatalf("prepareStoreFiles: %v", err)
	}
	db, err := sql.Open("sqlite", path+"?"+writerPragmas)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; CREATE TABLE made (id INTEGER)`); err != nil {
		t.Fatalf("write through a new log: %v", err)
	}
	for _, suffix := range storeSidecars {
		if got := modeOf(t, path+suffix); got != 0o600 {
			t.Errorf("SQLite made %s%s %04o beside a 0600 database; want 0600", DatabaseFile, suffix, got)
		}
	}
}

// A store that an older version left readable, with its log and index still
// on disk as they are after a crash, is private before a connection opens it.
func TestOpenMakesAStoreThatWasLeftReadablePrivate(t *testing.T) {
	underTheServersUmask(t)
	first := t.TempDir()
	st, err := Open(first)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The files as a crash leaves them: copied while the store is open, since
	// a clean close folds the log into the database and removes it.
	crashed := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(crashed, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{DatabaseFile, DatabaseFile + "-wal", DatabaseFile + "-shm"}
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(crashed, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	again, err := Open(crashed)
	if err != nil {
		t.Fatalf("Open what a crash left: %v", err)
	}
	defer again.Close()
	for _, name := range names {
		if got := modeOf(t, filepath.Join(crashed, name)); got != 0o600 {
			t.Errorf("%s is %04o after reopening; want 0600", name, got)
		}
	}
}

// Open only ever takes permissions away from the data directory: one an
// operator closed further stays closed, and its other bits are kept.
func TestOpenNeverLoosensTheDataDirectory(t *testing.T) {
	underTheServersUmask(t)
	for _, tc := range []struct {
		name      string
		had, want fs.FileMode
	}{
		{"closed to everyone else", 0o700, 0o700},
		{"open to a group", 0o770, 0o710},
		{"writable by anyone", 0o777, 0o711},
		{"sticky", 0o755 | fs.ModeSticky, 0o711 | fs.ModeSticky},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "data")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, tc.had); err != nil {
				t.Fatal(err)
			}
			st, err := Open(dir)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer st.Close()
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode() & (fs.ModePerm | fs.ModeSticky | fs.ModeSetgid); got != tc.want {
				t.Errorf("a %v directory became %v; want %v", tc.had, got, tc.want)
			}
		})
	}
}
