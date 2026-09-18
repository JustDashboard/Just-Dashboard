package files

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Root pins the allowed directory while recursive operations run. A pathname
// checked once cannot protect its children from links introduced afterwards.
func (s *Service) openRootEntry(path string) (*os.Root, string, error) {
	for _, allowed := range s.roots {
		rel, err := filepath.Rel(allowed, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		root, err := os.OpenRoot(allowed)
		return root, rel, err
	}
	return nil, "", ErrOutsideRoot
}

func (s *Service) copyPath(src, dst string) error {
	source, sourceName, err := s.openRootEntry(src)
	if err != nil {
		return err
	}
	defer source.Close()
	target, targetName, err := s.openRootEntry(dst)
	if err != nil {
		return err
	}
	defer target.Close()
	st, err := source.Lstat(sourceName)
	if err != nil {
		return err
	}
	if st.IsDir() && (src == dst || strings.HasPrefix(dst, src+string(filepath.Separator))) {
		return fmt.Errorf("cannot copy a directory into itself")
	}
	return copyRootEntry(source, sourceName, target, targetName)
}

func copyRootEntry(source *os.Root, src string, target *os.Root, dst string) error {
	st, err := source.Lstat(src)
	if err != nil {
		return err
	}
	var destination os.FileInfo
	if existing, err := target.Lstat(dst); err == nil {
		destination = existing
		if os.SameFile(st, existing) {
			return fmt.Errorf("source and destination are the same file")
		}
		if existing.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: copy destination is a symlink", ErrOutsideRoot)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		link, err := source.Readlink(src)
		if err != nil {
			return err
		}
		return target.Symlink(link, dst)
	case st.IsDir():
		if err := target.MkdirAll(dst, st.Mode().Perm()); err != nil {
			return err
		}
		dir, err := source.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
		if err != nil {
			return err
		}
		entries, err := dir.ReadDir(-1)
		dir.Close()
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyRootEntry(source, filepath.Join(src, entry.Name()), target, filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	case !st.Mode().IsRegular():
		return fmt.Errorf("only regular files, directories and symlinks can be copied")
	default:
		in, err := source.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer in.Close()
		var destinationACL []byte
		if destination != nil && destination.Mode().IsRegular() {
			existing, err := target.OpenFile(dst, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			current, err := existing.Stat()
			if err == nil && (!current.Mode().IsRegular() || !os.SameFile(destination, current)) {
				err = fmt.Errorf("copy destination changed while opening access controls")
			}
			if err == nil {
				destinationACL, err = readCopyAccessACL(existing)
			}
			existing.Close()
			if err != nil {
				return err
			}
			destination = current
		}
		token := make([]byte, 16)
		if _, err := rand.Read(token); err != nil {
			return err
		}
		tmpName := filepath.Join(filepath.Dir(dst), fmt.Sprintf(".jd-copy-%x", token))
		out, err := target.OpenFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
		if err != nil {
			return err
		}
		defer target.Remove(tmpName)
		if destination != nil && destination.Mode().IsRegular() {
			// Atomic replacement must retain the destination's access controls,
			// just as writing to the existing file did before this change.
			if stat, ok := destination.Sys().(*syscall.Stat_t); ok {
				if err := out.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
					out.Close()
					return err
				}
			}
			if err := out.Chmod(destination.Mode().Perm()); err != nil {
				out.Close()
				return err
			}
			// Mode group bits are an ACL mask, not necessarily the owning
			// group's permissions. Losing the ACL can widen access. Apply it
			// after ownership/mode changes, before any content reaches staging.
			if err := writeCopyAccessACL(out, destinationACL); err != nil {
				out.Close()
				return err
			}
		}
		_, copyErr := io.Copy(out, in)
		if copyErr == nil {
			copyErr = out.Sync()
		}
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return target.Rename(tmpName, dst)
	}
}

const copyAccessACL = "system.posix_acl_access"

func readCopyAccessACL(file *os.File) ([]byte, error) {
	// Linux bounds an extended attribute at 64 KiB. A single bounded read
	// avoids a size-probe race; any read failure leaves the destination intact.
	value := make([]byte, 64*1024)
	n, err := unix.Fgetxattr(int(file.Fd()), copyAccessACL, value)
	if errors.Is(err, unix.ENODATA) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read destination access ACL: %w", err)
	}
	return value[:n], nil
}

func writeCopyAccessACL(file *os.File, value []byte) error {
	var err error
	if value == nil {
		// The temporary file may have inherited a parent default ACL even
		// though the original destination had no extended access ACL.
		err = unix.Fremovexattr(int(file.Fd()), copyAccessACL)
		if errors.Is(err, unix.ENODATA) {
			return nil
		}
	} else {
		err = unix.Fsetxattr(int(file.Fd()), copyAccessACL, value, 0)
	}
	if err != nil {
		return fmt.Errorf("preserve destination access ACL: %w", err)
	}
	return nil
}
