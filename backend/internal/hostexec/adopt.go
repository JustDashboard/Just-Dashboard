package hostexec

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Adopt hands every root-owned entry of the tree at root to owner, and
// returns how many it changed.
//
// It is GiveBack for a whole tree, with the same threat in mind: it runs as
// root over directories owner may be able to write, so no name is ever
// followed. Each directory is opened relative to its parent with O_NOFOLLOW
// and chowned through its descriptor, and every other entry is opened with
// O_PATH — which neither follows a link nor opens a device — re-checked on the
// descriptor, and chowned through that. A directory is handed over only after
// everything inside it, so a tree that starts out root-owned stays unwritable
// by owner for as long as the walk is inside it.
//
// What is left alone is as deliberate as what is not. Only root's entries
// move; another account's files are that account's. A regular file with more
// than one link may be a name for something outside the tree. A file or
// directory that only its owner can read is a secret — the installer's .env —
// and owner has no reason to read it. Another filesystem mounted inside the
// tree is not part of it.
func Adopt(root string, owner Owner) (int, error) {
	top, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", root, err)
	}
	defer unix.Close(top)
	var st unix.Stat_t
	if err := unix.Fstat(top, &st); err != nil {
		return 0, err
	}
	if st.Uid != 0 && st.Uid != owner.UID {
		return 0, fmt.Errorf("%s belongs to uid %d, not to root or uid %d", root, st.Uid, owner.UID)
	}
	a := adopter{owner: owner, dev: st.Dev}
	err = a.dir(top, &st)
	return a.changed, err
}

type adopter struct {
	owner   Owner
	dev     uint64
	changed int
}

func (a *adopter) dir(fd int, st *unix.Stat_t) error {
	if st.Uid == 0 && st.Mode&0o055 == 0 {
		return nil
	}
	names, err := readNames(fd)
	if err != nil {
		return err
	}
	for _, name := range names {
		var child unix.Stat_t
		if err := unix.Fstatat(fd, name, &child, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			continue // gone since it was listed
		}
		if child.Dev != a.dev {
			continue
		}
		switch child.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			sub, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				continue
			}
			err = unix.Fstat(sub, &child)
			if err == nil && child.Dev == a.dev {
				err = a.dir(sub, &child)
			}
			unix.Close(sub)
			if err != nil {
				return err
			}
		case unix.S_IFREG, unix.S_IFLNK:
			if child.Uid != 0 {
				continue
			}
			if err := a.leaf(fd, name); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	if st.Uid != 0 {
		return nil
	}
	if err := unix.Fchown(fd, int(a.owner.UID), int(a.owner.GID)); err != nil {
		return err
	}
	a.changed++
	return nil
}

// leaf hands over one file or link, judged again on the descriptor it is
// chowned through, so a name swapped after the listing is judged as what it
// now is.
func (a *adopter) leaf(dir int, name string) error {
	fd, err := unix.Openat(dir, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil
	}
	if st.Uid != 0 || st.Dev != a.dev {
		return nil
	}
	switch st.Mode & unix.S_IFMT {
	case unix.S_IFREG:
		if st.Nlink != 1 || st.Mode&0o044 == 0 {
			return nil
		}
	case unix.S_IFLNK:
	default:
		return nil
	}
	if err := unix.Fchownat(fd, "", int(a.owner.UID), int(a.owner.GID), unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	a.changed++
	return nil
}

func readNames(fd int) ([]string, error) {
	dup, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), "")
	defer f.Close()
	return f.Readdirnames(-1)
}
