package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// UnixListener is one listening unix socket bound to a path, joined to the
// process holding it.
//
// The ports page has no use for these — a unix socket has no port — but a
// database server that listens on one and on nothing else is invisible in
// every table that page reads. The database inventory asks for them here
// because the join to a process is the same one, made over the same walk.
type UnixListener struct {
	Path        string
	PID         int32
	Process     string
	Cmdline     string
	User        string
	Manager     string
	ManagerName string
}

// acceptingFlag is __SO_ACCEPTCON in the flags column of /proc/net/unix: set
// on a socket that has been listened on.
const acceptingFlag = 0x00010000

// ListUnixListeners lists the listening unix sockets whose path want accepts,
// each with its owner.
//
// The filter is applied before the walk over every process's descriptors,
// which is what takes the time: a machine has hundreds of unix sockets and a
// caller is interested in a handful, and when none match there is no walk.
func ListUnixListeners(ctx context.Context, want func(path string) bool) ([]UnixListener, error) {
	root := procRoot()
	content, err := os.ReadFile(filepath.Join(root, "net", "unix"))
	if err != nil {
		return nil, err
	}
	rows := parseUnixSocketTable(content)
	paths := map[uint64]string{}
	wanted := map[uint64]bool{}
	order := []uint64{}
	for _, row := range rows {
		if !want(row.path) || wanted[row.inode] {
			continue
		}
		wanted[row.inode] = true
		paths[row.inode] = row.path
		order = append(order, row.inode)
	}
	holders, err := socketHolders(ctx, root, wanted)
	if err != nil {
		return nil, err
	}
	parents := parentsOf(root, holders)
	cache := map[int32]*ownerDetails{}
	out := make([]UnixListener, 0, len(order))
	for _, inode := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		l := UnixListener{Path: paths[inode], PID: ownerOf(holders[inode], parents)}
		if l.PID > 0 {
			d, ok := cache[l.PID]
			if !ok {
				d = readOwnerDetails(ctx, root, l.PID)
				cache[l.PID] = d
			}
			if d != nil {
				l.Process, l.Cmdline, l.User = d.name, d.cmdline, d.user
				l.Manager, l.ManagerName = d.manager, d.managerName
			}
		}
		out = append(out, l)
	}
	return out, nil
}

type unixSocketRow struct {
	inode uint64
	path  string
}

// parseUnixSocketTable reads /proc/net/unix, keeping the listening sockets
// that are bound to a filesystem path. An abstract socket (its name begins
// with "@") and one with no name at all are not files anybody can connect to
// by path.
func parseUnixSocketTable(content []byte) []unixSocketRow {
	out := []unixSocketRow{}
	lines := strings.Split(string(content), "\n")
	if len(lines) == 0 {
		return out
	}
	for _, line := range lines[1:] {
		// Num RefCount Protocol Flags Type St Inode Path
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 64)
		if err != nil || flags&acceptingFlag == 0 {
			continue
		}
		inode, err := strconv.ParseUint(fields[6], 10, 64)
		if err != nil {
			continue
		}
		// A path may hold spaces; it is everything after the inode column.
		at := strings.Index(line, " "+fields[6]+" ")
		if at < 0 {
			continue
		}
		path := strings.TrimSpace(line[at+len(fields[6])+2:])
		if !strings.HasPrefix(path, "/") {
			continue
		}
		out = append(out, unixSocketRow{inode: inode, path: path})
	}
	return out
}
