package hostexec

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Foreign is a directory in a tree that belongs to an account other than the
// tree's owner.
type Foreign struct {
	Path  string `json:"path"`
	Owner string `json:"owner"`
	UID   uint32 `json:"-"`
}

// Unwritable answers which directories a process running as owner could not
// write the given paths into: every existing directory from root down to each
// path, wherever one belongs to another account.
//
// Git replaces a file by unlinking it and creating it again, so what decides
// whether a checkout can be updated is who owns the *directories* — a file
// owned by root in a directory of the owner's is replaced without complaint,
// and a file of the owner's in root's directory is not. The walk stops at the
// first component that does not exist, because git creates that one inside
// the directory above it, and at a symbolic link, because git does not write
// through one.
func Unwritable(root string, owner Owner, rels []string) []Foreign {
	seen := map[string]bool{}
	var out []Foreign
	check := func(rel string) bool {
		if seen[rel] {
			return true
		}
		seen[rel] = true
		fi, err := os.Lstat(filepath.Join(root, rel))
		if err != nil || !fi.IsDir() {
			return false
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != owner.UID {
			out = append(out, Foreign{Path: rel, Owner: accountName(st.Uid), UID: st.Uid})
		}
		return true
	}
	for _, rel := range rels {
		dir := ""
		for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/") {
			if part == "." || part == "" {
				continue
			}
			dir = filepath.Join(dir, part)
			if !check(dir) {
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func accountName(uid uint32) string {
	id := strconv.FormatUint(uint64(uid), 10)
	if u, err := user.LookupId(id); err == nil {
		return u.Username
	}
	return id
}

// GiveBack hands each of rels, relative to root, to owner.
//
// This runs as root on paths inside a tree the owner can write, which is
// exactly the arrangement in which a path-based chown is a privilege
// escalation: swap a directory for a symbolic link between the check and the
// call, and root chowns wherever the link points. So every component is
// opened relative to its parent with O_NOFOLLOW and the descriptor is what
// gets chowned — there is no moment at which a name is resolved twice. The
// root must already belong to owner, which is what makes a root replaced by
// a link to somewhere else fail rather than succeed.
func GiveBack(root string, owner Owner, rels []string) error {
	top, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", root, err)
	}
	defer syscall.Close(top)
	var st syscall.Stat_t
	if err := syscall.Fstat(top, &st); err != nil {
		return err
	}
	if st.Uid != owner.UID {
		return fmt.Errorf("%s does not belong to uid %d", root, owner.UID)
	}
	for _, rel := range rels {
		if err := giveBack(top, owner, rel); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
	}
	return nil
}

func giveBack(top int, owner Owner, rel string) error {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return errors.New("not a path inside the tree")
		}
	}
	dir := top
	for i, part := range parts {
		flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= syscall.O_DIRECTORY
		}
		fd, err := syscall.Openat(dir, part, flags, 0)
		if dir != top {
			syscall.Close(dir)
		}
		if err != nil {
			return err
		}
		dir = fd
	}
	defer syscall.Close(dir)
	var st syscall.Stat_t
	if err := syscall.Fstat(dir, &st); err != nil {
		return err
	}
	if st.Uid == owner.UID && st.Gid == owner.GID {
		return nil
	}
	return syscall.Fchown(dir, int(owner.UID), int(owner.GID))
}
