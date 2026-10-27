// Package clihost keeps the jd command line on the server as current as the
// dashboard. install.sh and `manage.sh install-cli` copy jd out of the
// backend's image to /usr/local/bin/jd; an update from the dashboard rebuilds
// the image, and without this the server would go on running the jd of the
// install it started from. At boot the dashboard replaces the server's copy
// with the one built beside it — but only a copy that is already there and is
// already jd: a missing file was the operator's choice, and a different
// program that happens to be called jd is never touched.
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

// Refresh replaces hostRoot+Path with built when the file there is a jd that
// differs from it. It reports whether it replaced anything. hostRoot is where
// the server's own root is mounted in the backend's container ("/host");
// built is the jd beside the dashboard's binary.
func Refresh(hostRoot, built string) (bool, error) {
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
	target := filepath.Join(hostRoot, Path)
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
