package files

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Identity names the entry at path itself rather than its name: its device
// and inode, and its birth time where the filesystem records one, so an entry
// that took the name since has another even when it was handed the inode a
// deleted one freed. A rename keeps it; a copy, or a save that replaces the
// file by renaming a new one over it, does not. The page keeps it with an
// undo, and a move or a delete given it as `expect` refuses an entry that is
// no longer the one the undo was about.
func (s *Service) Identity(path string) (string, error) {
	if s.run != nil {
		return hosted2(s, func(s *Service) (string, error) { return s.Identity(path) })
	}
	full, err := s.ResolveEntry(path)
	if err != nil {
		return "", err
	}
	return entryIdentity(full)
}

func entryIdentity(full string) (string, error) {
	var st unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, full, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_INO|unix.STATX_BTIME, &st)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) {
		// No statx here (an old kernel, a sandbox that filters it): the
		// device and inode alone.
		info, err := os.Lstat(full)
		if err != nil {
			return "", err
		}
		sys, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return "", fmt.Errorf("%s: the filesystem gives no inode", full)
		}
		return fmt.Sprintf("%d:%d", uint64(sys.Dev), sys.Ino), nil
	}
	if err != nil {
		return "", &fs.PathError{Op: "statx", Path: full, Err: err}
	}
	id := fmt.Sprintf("%d:%d", unix.Mkdev(st.Dev_major, st.Dev_minor), st.Ino)
	if st.Mask&unix.STATX_BTIME != 0 {
		id += fmt.Sprintf(":%d.%09d", st.Btime.Sec, st.Btime.Nsec)
	}
	return id, nil
}
