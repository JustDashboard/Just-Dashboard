package store

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// The store's file is opened by more than SQLite: the database scanner reads
// its first bytes, the file browser can download it, a backup can archive the
// directory, an operator can point a sqlite3 shell at it. SQLite's locks are
// record locks owned by the process, and closing any descriptor for the file
// in this process releases all of them without SQLite knowing. Once they are
// gone, another SQLite closing its connection believes it was the last one,
// checkpoints, and deletes the -wal and -shm this process is still writing
// through, and the writes that follow corrupt the database.
//
// An open file description lock belongs to the descriptor that took it, not
// to the process, so no other close releases it, and it conflicts with every
// other process's record locks. walGuard holds two in read mode for as long
// as the store is open: on the range SQLite takes for a shared lock, so no
// other connection wins the exclusive lock that deleting the -wal requires,
// and on the -shm dead-man switch, so no newcomer mistakes the index for
// abandoned and rebuilds it under us. Neither blocks SQLite in WAL mode:
// readers and writers coordinate through the -shm, and only a connection
// that wants to delete the -wal, or rebuild the index, asks for these.
const (
	// SQLite's shared-lock range on the database (PENDING_BYTE + 2, 510 bytes)
	// and the dead-man switch byte in the -shm (UNIX_SHM_DMS), both fixed by
	// its file formats.
	sqliteSharedFirst = 0x40000000 + 2
	sqliteSharedSize  = 510
	sqliteShmDMS      = 128
)

type walGuard struct{ files []*os.File }

// holdWALGuard takes the guard on the database at path, which must be open in
// WAL mode in this process so that its -shm exists.
func holdWALGuard(path string) (*walGuard, error) {
	g := &walGuard{}
	for _, lock := range []struct {
		path        string
		start, size int64
	}{
		{path, sqliteSharedFirst, sqliteSharedSize},
		{path + "-shm", sqliteShmDMS, 1},
	} {
		f, err := os.OpenFile(lock.path, os.O_RDWR, 0)
		if err != nil {
			g.release()
			return nil, fmt.Errorf("guard the store's locks: %w", err)
		}
		g.files = append(g.files, f)
		flock := unix.Flock_t{Type: unix.F_RDLCK, Whence: io.SeekStart, Start: lock.start, Len: lock.size}
		if err := unix.FcntlFlock(f.Fd(), unix.F_OFD_SETLK, &flock); err != nil {
			g.release()
			return nil, fmt.Errorf("guard the store's locks on %s: %w", lock.path, err)
		}
	}
	return g, nil
}

// release closes the guard's descriptors, which drops its locks. Closing the
// database's descriptor also drops SQLite's own record locks, which is why
// the store releases the guard only on its way to closing the database.
func (g *walGuard) release() {
	if g == nil {
		return
	}
	for _, f := range g.files {
		f.Close()
	}
	g.files = nil
}
