package hostname

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A server has to be able to look its own name up without the network: sudo
// says "unable to resolve host" on every command when it cannot, and mail and
// database servers refuse to start. The line that does it is the dashboard's
// own, marked, so the next rename finds and replaces it, and every other line
// of the file stays whoever wrote it.
const (
	hostsMark    = "# Just Dashboard: server name"
	hostsAddress = "127.0.1.1"
	// cloudInitHosts is what cloud-init writes at the head of a hosts file
	// it regenerates on every start, from a template, discarding what was
	// added to the file itself.
	cloudInitHosts = "manage_etc_hosts"
)

// recordInHosts puts the name in /etc/hosts, and in cloud-init's templates
// for that file where cloud-init rewrites it at every start. What it returns
// is what it could not do; the server is renamed either way.
func (s *Service) recordInHosts(name string) []string {
	var warnings []string
	data, err := s.read("/etc/hosts")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("/etc/hosts could not be read, so the server may not find itself by its new name: %v", err)}
	}
	if err := s.rewrite("/etc/hosts", data, name); err != nil {
		warnings = append(warnings, fmt.Sprintf("/etc/hosts could not be updated, so the server may not find itself by its new name: %v", err))
	}
	if !bytes.Contains(data, []byte(cloudInitHosts)) {
		return warnings
	}
	templates, _ := filepath.Glob(filepath.Join(s.root, "/etc/cloud/templates/hosts.*.tmpl"))
	for _, template := range templates {
		path := strings.TrimPrefix(template, s.root)
		content, err := s.read(path)
		if err == nil {
			err = s.rewrite(path, content, name)
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cloud-init rewrites /etc/hosts when the server starts, and its template %s could not be updated: %v", filepath.Base(template), err))
		}
	}
	return warnings
}

// read reads the server's file at path in the view a write of it uses
// (onServerFile), so a link whose target differs between the container and
// the server is read as the server's own programs read it.
func (s *Service) read(path string) ([]byte, error) {
	var data []byte
	err := s.onServerFile(path, func(at string) error {
		var err error
		data, err = os.ReadFile(at)
		return err
	})
	return data, err
}

// rewrite gives the server's file at path, which holds data, the name.
func (s *Service) rewrite(path string, data []byte, name string) error {
	next := withServerName(data, name)
	if bytes.Equal(next, data) {
		return nil
	}
	return s.replace(path, next)
}

// replace writes data to the server's file at path, as the server names it.
func (s *Service) replace(path string, data []byte) error {
	_, _, err := s.swap(path, data)
	return err
}

// swap is replace that also returns what the file held before, and whether
// there was a file, for a change that may have to be undone. The old bytes are
// read by the same path, in the same view, as the write.
func (s *Service) swap(path string, data []byte) (before []byte, existed bool, err error) {
	err = s.onServerFile(path, func(at string) error {
		old, err := os.ReadFile(at)
		switch {
		case err == nil:
			before, existed = old, true
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
		return writeKeeping(at, data)
	})
	return before, existed, err
}

// restore undoes a swap: the old bytes go back, or the file it created goes.
func (s *Service) restore(path string, before []byte, existed bool) error {
	if existed {
		return s.replace(path, before)
	}
	return s.onServerFile(path, func(at string) error {
		if err := os.Remove(at); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	})
}

// onServerFile runs fn against the server's file at path, by a path that can
// replace it.
func (s *Service) onServerFile(path string, fn func(at string) error) error {
	if s.onServer == nil {
		return fn(filepath.Join(s.root, path))
	}
	return s.onServer(path, filepath.Join(s.root, path), fn)
}

// withServerName returns a hosts file in which name resolves to this machine.
// The dashboard's earlier line is dropped; a new one is added unless a line
// somebody else wrote already carries the name. The file is edited as bytes,
// so nothing else in it is reordered or respaced.
func withServerName(data []byte, name string) []byte {
	var lines [][]byte
	named := false
	lastLoopback := -1
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		text := strings.TrimSpace(string(line))
		if strings.HasSuffix(text, hostsMark) {
			continue
		}
		if address, names := hostsRecord(text); address != "" {
			if strings.HasPrefix(address, "127.") {
				lastLoopback = len(lines)
			}
			for _, n := range names {
				named = named || strings.EqualFold(n, name)
			}
		}
		lines = append(lines, line)
	}
	if !named {
		ours := []byte(hostsAddress + " " + name + " " + hostsMark + "\n")
		at := len(lines)
		if lastLoopback >= 0 {
			at = lastLoopback + 1
		}
		// A file whose last line has no terminator would otherwise run
		// into the one placed after it.
		if at > 0 && !bytes.HasSuffix(lines[at-1], []byte("\n")) {
			lines[at-1] = append(bytes.Clone(lines[at-1]), '\n')
		}
		lines = append(lines[:at], append([][]byte{ours}, lines[at:]...)...)
	}
	return bytes.Join(lines, nil)
}

// hostsRecord reads one line as an address and its names. A comment, a blank
// line and a line with no name are not records.
func hostsRecord(text string) (string, []string) {
	if i := strings.Index(text, "#"); i >= 0 {
		text = text[:i]
	}
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return "", nil
	}
	return fields[0], fields[1:]
}

// writeKeeping replaces a file through a temporary one and a rename, so no
// reader sees half of it, and keeps the mode and owner the file had. A file
// that is a mount point here cannot be renamed over and is written in place
// (hostexec.RenameOver). Only a
// regular file is replaced: a hosts file that is a link into a read-only
// store is somebody's arrangement, and renaming over it would undo it.
func writeKeeping(path string, data []byte) error {
	mode := os.FileMode(0o644)
	uid, gid := -1, -1
	switch st, err := os.Lstat(path); {
	case err == nil && !st.Mode().IsRegular():
		return fmt.Errorf("%s is not an ordinary file", filepath.Base(path))
	case err == nil:
		mode = st.Mode().Perm()
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(sys.Uid), int(sys.Gid)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if uid >= 0 {
		if err := tmp.Chown(uid, gid); err != nil && os.Geteuid() == 0 {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return hostexec.RenameOver(tmp.Name(), path, data)
}
