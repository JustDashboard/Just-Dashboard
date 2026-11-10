package procs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/sys/unix"
)

// UnitFile is one file systemd reads a unit from: its fragment, or a drop-in
// that overrides part of it.
type UnitFile struct {
	Path      string `json:"path"`
	DropIn    bool   `json:"dropIn"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
	Error     string `json:"error,omitempty"`
}

// UnitFiles is a unit's files, fragment first and then its drop-ins in the
// order systemd applies them, and whether systemd's copy of them is stale.
type UnitFiles struct {
	Files            []UnitFile `json:"files"`
	NeedDaemonReload bool       `json:"needDaemonReload"`
}

// unitFileLimit caps each file read. A unit file is a few kilobytes; one this
// large is not a unit file anybody wrote, and the page shows what fits.
const unitFileLimit = 256 << 10

// Files reads the unit's fragment and drop-ins from the host, as systemd
// names them, through PID 1's root when the dashboard runs in a container and
// / on a bare-metal install.
func (s *Systemd) Files(ctx context.Context, name string) (*UnitFiles, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	res, err := run(ctx, 20*time.Second, "systemctl", "show", "--no-pager",
		"-p", "FragmentPath,DropInPaths,NeedDaemonReload", "--", name)
	if err != nil {
		return nil, err
	}
	props := map[string]string{}
	if records := parseShowRecords(res.Stdout); len(records) > 0 {
		props = records[0]
	}
	return &UnitFiles{
		Files:            readUnitFiles(hostexec.HostPath("/"), props["FragmentPath"], SplitEnvironment(props["DropInPaths"])),
		NeedDaemonReload: props["NeedDaemonReload"] == "yes",
	}, nil
}

// readUnitFiles reads the fragment and drop-ins under root, the directory
// the host's / is opened through. A file that cannot be read is listed with
// why, rather than failing the others.
func readUnitFiles(root, fragment string, dropIns []string) []UnitFile {
	out := []UnitFile{}
	type wanted struct {
		path   string
		dropIn bool
	}
	var paths []wanted
	if fragment != "" {
		paths = append(paths, wanted{fragment, false})
	}
	for _, path := range dropIns {
		paths = append(paths, wanted{path, true})
	}
	if len(paths) == 0 {
		return out
	}
	tree, err := openHostTree(root)
	for _, p := range paths {
		file := UnitFile{Path: p.path, DropIn: p.dropIn}
		if err != nil {
			file.Error = "the host's files could not be opened: " + err.Error()
		} else {
			readUnitFile(tree, &file)
		}
		out = append(out, file)
	}
	if tree != nil {
		_ = tree.Close()
	}
	return out
}

func readUnitFile(tree hostTree, file *UnitFile) {
	if !filepath.IsAbs(file.Path) || filepath.Clean(file.Path) != file.Path {
		file.Error = "not an absolute path"
		return
	}
	rel := strings.TrimPrefix(file.Path, "/")
	// A masked unit's fragment is a link to /dev/null. Opening it would read
	// nothing, or refuse a character device; saying "masked" is the answer.
	if target, err := tree.readlink(rel); err == nil && target == "/dev/null" {
		file.Error = "masked: linked to /dev/null"
		return
	}
	f, err := tree.open(rel)
	if err != nil {
		file.Error = describeOpenError(err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		file.Error = describeOpenError(err)
		return
	}
	if !info.Mode().IsRegular() {
		file.Error = "not a regular file"
		return
	}
	file.Size = info.Size()
	data, err := io.ReadAll(io.LimitReader(f, unitFileLimit+1))
	if err != nil {
		file.Error = describeOpenError(err)
		return
	}
	if len(data) > unitFileLimit {
		data = data[:unitFileLimit]
		// Do not end on half a character.
		for range utf8.UTFMax - 1 {
			if r, size := utf8.DecodeLastRune(data); r != utf8.RuneError || size != 1 {
				break
			}
			data = data[:len(data)-1]
		}
		file.Truncated = true
	}
	file.Content = string(data)
}

func describeOpenError(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "no such file on the host"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	}
	return err.Error()
}

// hostTree opens files below the host's root, resolving every symbolic link
// against that root rather than this process's. The difference matters in a
// container: /proc/1/root/etc/systemd/system/app.service linked to
// /opt/app/app.service would otherwise be followed to the container's /opt,
// which is a different file or none.
type hostTree interface {
	readlink(rel string) (string, error)
	open(rel string) (*os.File, error)
	Close() error
}

func openHostTree(root string) (hostTree, error) {
	fd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: root, Err: err}
	}
	tree := &inRootTree{fd: fd}
	// openat2 arrived in Linux 5.6. Before it, os.Root keeps every link
	// inside the root by refusing the ones that would leave it, absolute
	// ones included.
	probe, err := tree.openHow(".", unix.O_PATH|unix.O_DIRECTORY)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EPERM) {
		_ = unix.Close(fd)
		r, err := os.OpenRoot(root)
		if err != nil {
			return nil, err
		}
		return beneathTree{r}, nil
	}
	if err == nil {
		_ = unix.Close(probe)
	}
	return tree, nil
}

// inRootTree resolves with RESOLVE_IN_ROOT, which reads an absolute link as
// starting at the root, as the host itself would. Magic links (/proc/self/…)
// are refused: a host file linked to one would otherwise reach this process.
type inRootTree struct{ fd int }

func (t *inRootTree) openHow(rel string, flags int) (int, error) {
	how := &unix.OpenHow{
		Flags:   uint64(flags | unix.O_CLOEXEC),
		Resolve: unix.RESOLVE_IN_ROOT | unix.RESOLVE_NO_MAGICLINKS,
	}
	for {
		fd, err := unix.Openat2(t.fd, rel, how)
		// EAGAIN is openat2 declining to resolve across a concurrent rename.
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
			continue
		}
		return fd, err
	}
}

func (t *inRootTree) readlink(rel string) (string, error) {
	dir, base := filepath.Split(rel)
	if dir == "" {
		dir = "."
	}
	dfd, err := t.openHow(dir, unix.O_PATH|unix.O_DIRECTORY)
	if err != nil {
		return "", err
	}
	defer unix.Close(dfd)
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(dfd, base, buf)
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

func (t *inRootTree) open(rel string) (*os.File, error) {
	// O_NONBLOCK so a FIFO where a unit file should be cannot hold the open.
	fd, err := t.openHow(rel, unix.O_RDONLY|unix.O_NOCTTY|unix.O_NONBLOCK)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: rel, Err: err}
	}
	return os.NewFile(uintptr(fd), rel), nil
}

func (t *inRootTree) Close() error { return unix.Close(t.fd) }

type beneathTree struct{ root *os.Root }

func (t beneathTree) readlink(rel string) (string, error) { return t.root.Readlink(rel) }

func (t beneathTree) open(rel string) (*os.File, error) {
	return t.root.OpenFile(rel, os.O_RDONLY|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
}

func (t beneathTree) Close() error { return t.root.Close() }
