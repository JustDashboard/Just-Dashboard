package proxysvc

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Keep staging, metadata, publication and cleanup on the directory originally
// approved, even if a native writer renames it or replaces its path with a link.
type configTarget struct {
	parent            *os.Root
	info              os.FileInfo
	full, name, alias string
	service           *Service
}

func (s *Service) openConfigTarget(full string) (*configTarget, error) {
	for _, configured := range []string{s.nginxDir, filepath.Dir(s.caddyFile)} {
		rootPath, err := filepath.Abs(configured)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(rootPath, full)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			continue
		}
		allowed, err := os.OpenRoot(rootPath)
		if err != nil {
			return nil, err
		}
		parent, err := allowed.OpenRoot(filepath.Dir(relative))
		allowed.Close()
		if err != nil {
			return nil, err
		}
		return newConfigTarget(parent, full, s)
	}
	return nil, ErrUnsafePath
}

func newConfigTarget(parent *os.Root, full string, service *Service) (*configTarget, error) {
	info, err := parent.Stat(".")
	if err != nil {
		parent.Close()
		return nil, err
	}
	target := &configTarget{parent: parent, info: info, full: full, name: filepath.Base(full), service: service}
	if err := target.location(); err != nil {
		parent.Close()
		return nil, err
	}
	return target, nil
}

func (t *configTarget) location() error {
	parentPath := filepath.Dir(t.full)
	if t.service != nil {
		current, err := t.service.allowedPath(t.full)
		if err != nil || current != t.full {
			return fmt.Errorf("%w: the file's path changed", ErrConfigChanged)
		}
	} else {
		if t.alias != "" {
			parentPath = filepath.Dir(t.alias)
		}
		if current, err := filepath.EvalSymlinks(parentPath); err != nil || current != filepath.Dir(t.full) {
			return fmt.Errorf("%w: the file's directory changed", ErrConfigChanged)
		}
	}
	current, err := os.Stat(parentPath)
	if err != nil || !os.SameFile(t.info, current) {
		return fmt.Errorf("%w: the file's directory was replaced", ErrConfigChanged)
	}
	return nil
}

func (t *configTarget) read() (string, bool, error) {
	file, err := t.parent.OpenFile(t.name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if errors.Is(err, syscall.ELOOP) {
		return "", false, ErrConfigChanged
	}
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%w: the configuration is not a regular file", ErrConfigChanged)
	}
	content, err := io.ReadAll(file)
	return string(content), err == nil, err
}

func (t *configTarget) check(expected string) (string, error) {
	content, existed, err := t.read()
	if err != nil {
		return "", err
	}
	if (expected == "" && existed) || (expected != "" && (!existed || ContentDigest(content) != expected)) {
		return "", ErrConfigChanged
	}
	return content, nil
}

func (t *configTarget) mode() os.FileMode {
	if info, err := t.parent.Stat(t.name); err == nil {
		return info.Mode().Perm()
	}
	return 0o644
}

func (t *configTarget) write(content string, mode os.FileMode, expected *string, checkLocation bool) error {
	if checkLocation {
		if err := t.location(); err != nil {
			return err
		}
	}
	name := ".vpsd-" + rand.Text()
	tmp, err := t.parent.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer t.parent.Remove(name)
	defer tmp.Close()
	if _, err := tmp.WriteString(content); err != nil {
		return err
	}
	// Path-based chmod could follow a replacement ancestor into an unrelated file.
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if checkLocation {
		if err := t.location(); err != nil {
			return err
		}
	}
	if expected != nil {
		if _, err := t.check(*expected); err != nil {
			return err
		}
	}
	return t.parent.Rename(name, t.name)
}

func (t *configTarget) restore(original string, existed bool, mode os.FileMode, candidate string) error {
	if _, err := t.check(candidate); err != nil {
		return err
	}
	if existed {
		return t.write(original, mode, &candidate, false)
	}
	return t.parent.Remove(t.name)
}
