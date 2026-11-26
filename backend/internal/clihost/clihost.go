// Package clihost keeps the jd command line on the server as current as the
// dashboard. install.sh and `manage.sh install-cli` copy jd out of the
// backend's image to /usr/local/bin/jd; an update from the dashboard rebuilds
// the image, and without this the server would go on running the jd of the
// install it started from. At boot the dashboard replaces the server's copy
// with the one built beside it — but only a copy that is already there and is
// already jd: a missing file was the operator's choice, and a different
// program that happens to be called jd is never touched. The one exception
// is Install, for a feature that cannot work without jd on the server (a
// just-dashboard.com address's certificate, which certbot proves through
// jd): it puts jd where nothing is, and nowhere else.
package clihost

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Marker is a line of jd's usage text, which every build of it carries and no
// other program does. It is how a file at the install path is known to be jd.
const Marker = "jd — deploy to a Just Dashboard from the command line"

// Path is where install.sh puts jd on the server.
const Path = "/usr/local/bin/jd"

// Refresh replaces target with built when the file there is a jd that
// differs from it. It reports whether it replaced anything. The caller names
// target, the server's Path, as it opens the server's filesystem, which is
// not always /host: a backend running on the server itself has no such
// mount. built is the jd beside the dashboard's binary.
func Refresh(target, built string) (bool, error) {
	want, err := os.ReadFile(built)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil // a build without jd, such as a developer's
	}
	if err != nil {
		return false, err
	}
	if !bytes.Contains(want, []byte(Marker)) {
		return false, fmt.Errorf("%s does not look like jd", built)
	}
	have, err := os.ReadFile(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if bytes.Equal(have, want) || !bytes.Contains(have, []byte(Marker)) {
		return false, nil
	}
	// Written beside the target and renamed over it, so a jd running from
	// the old file keeps its inode and nothing ever sees half a binary.
	f, err := os.CreateTemp(filepath.Dir(target), ".jd-")
	if err != nil {
		return false, err
	}
	temp := f.Name()
	if _, err = f.Write(want); err == nil {
		err = f.Chmod(0o755)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp, target)
	}
	if err != nil {
		os.Remove(temp)
		return false, err
	}
	return true, nil
}

// Install puts the jd built beside the dashboard at target when nothing is
// there yet. A feature calls it when it needs jd on the server itself:
// certbot runs `jd app-domain challenge` to prove and renew a
// just-dashboard.com address's wildcard, so that address cannot work
// without it, and the operator who claimed one has asked for what it
// needs. The caller names target as it opens the filesystem certbot runs
// on, which is not always /host: a backend running on the server itself
// has no such mount. Whatever is already at the path, jd or another
// program, is left alone. It reports whether it installed anything.
func Install(target, built string) (bool, error) {
	want, err := os.ReadFile(built)
	if err != nil {
		return false, err
	}
	if !bytes.Contains(want, []byte(Marker)) {
		return false, fmt.Errorf("%s does not look like jd", built)
	}
	if _, err := os.Lstat(target); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".jd-")
	if err != nil {
		return false, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(want); err == nil {
		err = f.Chmod(0o755)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return false, err
	}
	// A link, not a rename: it fails rather than replace a file that
	// appeared at the path meanwhile.
	if err := os.Link(temp, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
