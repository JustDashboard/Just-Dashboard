package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"

	"golang.org/x/sys/unix"
)

// stagePrefix names a transfer that is still arriving: an upload in pieces or
// a fetched link. It sits beside its destination, on its filesystem, until it
// is whole and published under its real name.
const stagePrefix = ".jd-part-"

// ErrNoRoom refuses a transfer the filesystem has no room for, in words.
var ErrNoRoom = errors.New("not enough room on this disk")

// uploadTarget is a destination opened for publication: the client's path
// resolved, its parent pinned through the allowed root and held open, and the
// owner and mode the published file is to have. Every write path that ends
// in a published file goes through one — the one-request upload, an upload
// in pieces and a fetched link — so staging, the revision check, the owner
// handed to the folder's owner and the no-replace publication exist once.
type uploadTarget struct {
	path       string
	full       string
	name       string
	allowed    *os.Root
	parent     *os.Root
	directory  *os.File
	parentInfo os.FileInfo
	perm       os.FileMode
	uid, gid   int
}

// openUploadTarget resolves path and refuses what UploadChecked always
// refused before a byte is staged: a directory, an occupied name without
// overwrite, a replacement of something that is not a regular file, and a
// stale expected revision.
func (s *Service) openUploadTarget(path string, overwrite bool, expected *string) (*uploadTarget, error) {
	full, err := s.Resolve(path)
	if err != nil {
		return nil, err
	}
	t := &uploadTarget{path: path, full: full, perm: 0o644, uid: -1, gid: -1}
	ok := false
	defer func() {
		if !ok {
			t.Close()
		}
	}()
	allowed, relative, err := s.openRootEntry(full)
	if err != nil {
		return nil, err
	}
	t.allowed = allowed
	if t.parent, err = allowed.OpenRoot(filepath.Dir(relative)); err != nil {
		return nil, err
	}
	if t.directory, err = t.parent.Open("."); err != nil {
		return nil, err
	}
	if t.parentInfo, err = t.directory.Stat(); err != nil {
		return nil, err
	}
	t.name = filepath.Base(relative)
	if err := checkUploadRevision(t.parent, t.name, expected); err != nil {
		return nil, err
	}
	if st, err := t.parent.Lstat(t.name); err == nil {
		if st.IsDir() {
			return nil, ErrIsDir
		}
		if !overwrite {
			return nil, fmt.Errorf("%w: %s", fs.ErrExist, path)
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("refusing to replace %s: it is not a regular file", path)
		}
		t.perm = st.Mode().Perm()
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			t.uid, t.gid = int(sys.Uid), int(sys.Gid)
		}
	} else if os.IsNotExist(err) {
		t.uid, t.gid = heir(t.parentInfo)
	} else {
		return nil, err
	}
	ok = true
	return t, nil
}

func (t *uploadTarget) Close() {
	if t.directory != nil {
		t.directory.Close()
	}
	if t.parent != nil {
		t.parent.Close()
	}
	if t.allowed != nil {
		t.allowed.Close()
	}
}

// seal gives a staged file the owner and mode it is published with, through
// its own descriptor, and makes it durable. It closes tmp.
func (t *uploadTarget) seal(tmp *os.File) error {
	if t.uid >= 0 {
		if err := tmp.Chown(t.uid, t.gid); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Chmod(t.perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	return tmp.Close()
}

// publishStaged puts the staged file tmpName in the target's place. The final
// comparison and the rename are one step under the Files/Compose path lock;
// the resolved path and the parent's identity are checked again, so a parent
// moved or swapped during the transfer refuses rather than publishes
// somewhere else. Without overwrite (or with an empty expected revision) the
// name must still be free: a no-replace rename, or a hard link where the
// filesystem has none, after which the staged name is removed.
func (s *Service) publishStaged(t *uploadTarget, tmpName string, overwrite bool, expected *string) error {
	unlock := contentguard.Lock(t.full)
	defer unlock()
	if !overwrite {
		if _, err := t.parent.Lstat(t.name); err == nil {
			return fmt.Errorf("%w: %s", fs.ErrExist, t.path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	resolved, err := s.Resolve(t.path)
	if err != nil {
		return err
	}
	if resolved != t.full {
		return contentguard.ErrConflict
	}
	currentParent, err := os.Stat(filepath.Dir(resolved))
	if err != nil {
		return err
	}
	if !os.SameFile(t.parentInfo, currentParent) {
		return contentguard.ErrConflict
	}
	if err := checkUploadRevision(t.parent, t.name, expected); err != nil {
		return err
	}
	if !overwrite || (expected != nil && *expected == "") {
		// A network transfer leaves time for another writer to claim the
		// destination. Publish only if the name is still free; a hard link
		// keeps this atomic on filesystems without no-replace rename support.
		fd := int(t.directory.Fd())
		err := unix.Renameat2(fd, tmpName, fd, t.name, unix.RENAME_NOREPLACE)
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
			if err := t.parent.Link(tmpName, t.name); err != nil {
				return err
			}
			_ = t.parent.Remove(tmpName)
			return nil
		}
		if err != nil {
			return &os.LinkError{Op: "rename", Old: tmpName, New: t.full, Err: err}
		}
		return nil
	}
	return t.parent.Rename(tmpName, t.name)
}

// newStageName is a fresh staging name and the id it carries.
func newStageName() (id, name string, err error) {
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return "", "", err
	}
	id = hex.EncodeToString(token)
	return id, stagePrefix + id, nil
}

// diskRoom is the free space on the filesystem holding dir, and its size.
// A variable so tests can say the disk is nearly full.
var diskRoom = func(dir *os.File) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(dir.Fd()), &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}

// diskReserve is what a transfer leaves free: two per cent of the disk,
// between 64 MiB and 1 GiB. A disk filled to the last byte stops the
// database, the logs and the services the dashboard is there to look after.
func diskReserve(total uint64) uint64 {
	r := total / 50
	return min(max(r, 64<<20), 1<<30)
}

// checkRoom refuses need more bytes on dir's filesystem when they would eat
// into the reserve.
func checkRoom(dir *os.File, need int64) error {
	if need <= 0 {
		return nil
	}
	free, total, err := diskRoom(dir)
	if err != nil {
		return nil // a filesystem that cannot say is not refused on a guess
	}
	reserve := diskReserve(total)
	if free < reserve || uint64(need) > free-reserve {
		usable := uint64(0)
		if free > reserve {
			usable = free - reserve
		}
		return fmt.Errorf("%w: this needs %s more and %s can be used", ErrNoRoom, HumanBytes(need), HumanBytes(int64(usable)))
	}
	return nil
}

// HumanBytes says a size the way the page does.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

// SafeName is a file name a client or a server sent, made into a single
// entry: any folder part dropped, and refused when what is left is not a name
// a person would want created (empty, a dot entry, a control character, too
// long, or one of the dashboard's own staging names).
func SafeName(raw string) (string, error) {
	name := strings.ReplaceAll(raw, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	switch {
	case name == "" || name == "." || name == "..":
		return "", fmt.Errorf("%w: %q is not a file name", fs.ErrInvalid, raw)
	case len(name) > 255 || !utf8.ValidString(name):
		return "", fmt.Errorf("%w: the name is too long or not text", fs.ErrInvalid)
	case IsStagingName(name):
		return "", fmt.Errorf("%w: %q is a name the dashboard keeps for itself", fs.ErrInvalid, name)
	}
	for _, r := range name {
		if r == 0 || unicode.IsControl(r) {
			return "", fmt.Errorf("%w: the name has a control character in it", fs.ErrInvalid)
		}
	}
	return name, nil
}
