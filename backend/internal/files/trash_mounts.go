package files

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// mountPoint is one line of the mount table, as much of it as the Trash and
// the index need: where a filesystem is attached and what kind it is.
type mountPoint struct {
	ID       int
	Path     string
	FSType   string
	ReadOnly bool
}

// readMountTable reads the mount table of the namespace the calling thread is
// in. The Files operations run on a thread that has entered the host's mount
// namespace (hostexec.InHostMounts), and /proc/self names the process, whose
// first thread never moved: thread-self is the table those operations see.
var readMountTable = func() ([]mountPoint, error) {
	f, err := os.Open("/proc/thread-self/mountinfo")
	if err != nil {
		f, err = os.Open("/proc/self/mountinfo")
		if err != nil {
			return nil, err
		}
	}
	defer f.Close()
	var out []mountPoint
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if m, ok := parseMountInfoLine(scanner.Text()); ok {
			out = append(out, m)
		}
	}
	return out, scanner.Err()
}

// parseMountInfoLine reads one line of proc(5)'s mountinfo:
//
//	36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
//
// The optional fields between the options and the "-" vary in number, so the
// filesystem type is found after the separator rather than by position.
func parseMountInfoLine(line string) (mountPoint, bool) {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return mountPoint{}, false
	}
	id, err := strconv.Atoi(fields[0])
	if err != nil {
		return mountPoint{}, false
	}
	sep := -1
	for i := 6; i < len(fields); i++ {
		if fields[i] == "-" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+1 >= len(fields) {
		return mountPoint{}, false
	}
	m := mountPoint{ID: id, Path: unescapeMountPath(fields[4]), FSType: fields[sep+1]}
	for _, opt := range strings.Split(fields[5], ",") {
		if opt == "ro" {
			m.ReadOnly = true
		}
	}
	return m, true
}

// unescapeMountPath undoes the octal escapes the kernel writes for a space,
// a tab, a newline and a backslash in a mount point.
func unescapeMountPath(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mountOf is the mount a path lives on: the longest mount point that is the
// path or one of its ancestors, the later of two at the same place (the one
// mounted over the other) winning. It reads paths, not device numbers, because
// a bind mount shares its source's device and rename(2) still refuses to
// cross from one to the other.
func mountOf(mounts []mountPoint, path string) (mountPoint, bool) {
	best, found := mountPoint{}, false
	for _, m := range mounts {
		if !pathWithin(path, m.Path) {
			continue
		}
		if !found || len(m.Path) >= len(best.Path) {
			best, found = m, true
		}
	}
	return best, found
}

// isMountPoint reports whether a filesystem is attached at path itself.
func isMountPoint(mounts []mountPoint, path string) bool {
	for _, m := range mounts {
		if m.Path == path {
			return true
		}
	}
	return false
}

func pathWithin(path, dir string) bool {
	return dir == "/" || path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// pseudoFS are the filesystems that are not a tree of anybody's files: the
// kernel's views of itself, and the plumbing of containers and devices. The
// Trash refuses them and the name index does not walk them.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "cgroup": true,
	"cgroup2": true, "securityfs": true, "debugfs": true, "tracefs": true, "pstore": true,
	"bpf": true, "configfs": true, "fusectl": true, "mqueue": true, "hugetlbfs": true,
	"autofs": true, "binfmt_misc": true, "efivarfs": true, "rpc_pipefs": true, "nsfs": true,
	"selinuxfs": true, "ramfs": true, "squashfs": true, "iso9660": true, "nfsd": true,
}

// networkFS are filesystems another machine answers for. The name index does
// not walk them: a walk over a share is a walk over somebody else's server,
// and a share that stops answering holds the walk with it.
var networkFS = map[string]bool{
	"nfs": true, "nfs4": true, "cifs": true, "smb3": true, "smbfs": true, "9p": true,
	"fuse.sshfs": true, "fuse.rclone": true, "fuse.s3fs": true, "ceph": true, "glusterfs": true,
	"fuse.glusterfs": true, "afs": true, "davfs": true, "fuse.davfs2": true, "lustre": true,
}
