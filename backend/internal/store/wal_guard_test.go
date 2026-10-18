package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// The incident this guards against: the database scanner read the store's
// first bytes and closed the file, which released every lock SQLite held on
// it, and a sqlite3 shell run on the host then closed believing it was the
// last connection, checkpointed, and deleted the -wal and -shm the dashboard
// was still writing through. The next writes zeroed a table's root page.

// The two locks that decide it, tried the way SQLite tries them from another
// process: the exclusive lock a closing connection must win before it deletes
// the -wal, and the dead-man switch a connection opening the -shm must win
// before it treats the index as abandoned and rebuilds it.
func TestNoOtherProcessCanTakeTheStoreLocksAfterAStrayClose(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(dir, DatabaseFile)
	strayClose(t, path)
	strayClose(t, path+"-shm")

	for _, probe := range []struct {
		name  string
		file  string
		start int64
		len   int64
	}{
		{"the exclusive lock that deletes the -wal", path, sqliteSharedFirst, sqliteSharedSize},
		{"the -shm dead-man switch", path + "-shm", sqliteShmDMS, 1},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLockProbeProcess$")
		cmd.Env = append(os.Environ(), "JD_TEST_LOCK_PROBE="+probe.file,
			"JD_TEST_LOCK_START="+strconv.FormatInt(probe.start, 10),
			"JD_TEST_LOCK_LEN="+strconv.FormatInt(probe.len, 10))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("another process took %s: %v\n%s", probe.name, err, out)
		}
	}
}

// The same incident end to end, with the shell that caused it. A newer SQLite,
// the one linked here among them, also looks at the dead-man switch before it
// deletes anything, so only an older shell like that one shows the failure
// without the guard; with it, none can.
func TestTheSQLiteShellCannotDeleteTheStoreWALAfterAStrayClose(t *testing.T) {
	shell, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3 shell on this machine")
	}
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('probe', 'before')`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, DatabaseFile)
	wal, shm := fileIdentity(t, path+"-wal"), fileIdentity(t, path+"-shm")
	strayClose(t, path)

	if out, err := exec.Command(shell, path, `SELECT value FROM settings WHERE key = 'probe'`).CombinedOutput(); err != nil {
		t.Fatalf("the shell could not read the store: %v\n%s", err, out)
	}

	if fileIdentity(t, path+"-wal") != wal {
		t.Fatal("the shell deleted the store's -wal while the store was open")
	}
	if fileIdentity(t, path+"-shm") != shm {
		t.Fatal("the shell deleted the store's -shm while the store was open")
	}
	if _, err := st.DB.ExecContext(ctx, `UPDATE settings SET value = 'after' WHERE key = 'probe'`); err != nil {
		t.Fatal(err)
	}
	var check string
	if err := st.DB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check = %q, %v", check, err)
	}
}

// Closing the store releases the guard, so the last connection checkpoints
// and removes the -wal as SQLite normally does instead of leaving it behind.
func TestClosingTheStoreStillRemovesItsWAL(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, DatabaseFile+"-wal")); !os.IsNotExist(err) {
		t.Fatalf("the -wal outlived the store: %v", err)
	}
}

// TestLockProbeProcess is the other process. It asks for a write lock the way
// SQLite's unix VFS does, with a process-owned record lock, and fails when it
// is granted.
func TestLockProbeProcess(t *testing.T) {
	path := os.Getenv("JD_TEST_LOCK_PROBE")
	if path == "" {
		t.Skip("lock probe helper process")
	}
	start, _ := strconv.ParseInt(os.Getenv("JD_TEST_LOCK_START"), 10, 64)
	length, _ := strconv.ParseInt(os.Getenv("JD_TEST_LOCK_LEN"), 10, 64)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = unix.FcntlFlock(f.Fd(), unix.F_SETLK, &unix.Flock_t{Type: unix.F_WRLCK, Start: start, Len: length})
	if err == nil {
		t.Fatalf("write lock on %s [%d, +%d) was granted", filepath.Base(path), start, length)
	}
}

// strayClose opens and closes a file the way any reader in this process
// might, which releases every record lock the process holds on it.
func strayClose(t *testing.T, path string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func fileIdentity(t *testing.T, path string) uint64 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return st.Ino
}
