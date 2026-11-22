package hostexec

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// A few of the server's files cannot be replaced from inside the shipped
// container. The stack mounts the server's /etc over the container's, and
// Docker then mounts its own hosts, hostname and resolv.conf files on top of
// those three. The kernel refuses to rename anything over a file that is a
// mount point anywhere in the caller's mount namespace, by whichever path the
// caller names it, so /host/etc/hosts is as busy as /etc/hosts here. In the
// server's own namespace nothing is mounted on them.

// OnServerPath runs write against one of the server's files by a path that
// can replace it. real is the file's path on the server (/etc/hosts) and
// mirrored is where this process reaches it (/host/etc/hosts in the shipped
// container, the same path on a plain install). When the two differ, write
// runs in the server's mount namespace with real; where that namespace cannot
// be entered it runs here with mirrored, and RenameOver keeps the write from
// failing on a busy file.
func OnServerPath(real, mirrored string, write func(path string) error) error {
	if real != mirrored {
		var err error
		if ran, _ := IntoHostMounts(func() { err = write(real) }); ran {
			return err
		}
	}
	return write(mirrored)
}

// RenameOver moves tmp, a finished copy of data, over path. When path is a
// mount point in this namespace the rename is refused, and data is written
// into path itself instead: not in one step, but over the old bytes and then
// cut to length, so a reader never finds the file empty.
func RenameOver(tmp, path string, data []byte) error {
	err := os.Rename(tmp, path)
	if !errors.Is(err, unix.EBUSY) {
		return err
	}
	if werr := writeInPlace(path, data); werr != nil {
		return errors.Join(err, werr)
	}
	return nil
}

func writeInPlace(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteAt(data, 0); err != nil {
		f.Close()
		return err
	}
	if err := f.Truncate(int64(len(data))); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
