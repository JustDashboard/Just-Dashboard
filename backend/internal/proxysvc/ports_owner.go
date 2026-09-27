package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// socketHolders maps each wanted socket inode to every process holding a
// descriptor on it, read from /proc/<pid>/fd.
//
// gopsutil kept only the first holder its walk met, and the walk meets PID 1
// first — so a socket-activated sshd, whose port systemd holds as well, was
// reported as systemd, /sbin/init. Keeping every holder lets ownerOf choose.
//
// A process that exits mid-walk, or whose descriptors this account may not
// read, is skipped: its sockets are listed without an owner rather than the
// whole listing failing.
func socketHolders(ctx context.Context, root string, wanted map[uint64]bool) (map[uint64][]int32, error) {
	holders := map[uint64][]int32{}
	if len(wanted) == 0 {
		return holders, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.ParseInt(entry.Name(), 10, 32)
		if err != nil {
			continue
		}
		dir := filepath.Join(root, entry.Name(), "fd")
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(dir, fd.Name()))
			if err != nil {
				continue
			}
			inode, ok := socketInode(target)
			if !ok || !wanted[inode] {
				continue
			}
			// One socket on two descriptors (a dup) is still one holder.
			if held := holders[inode]; len(held) > 0 && held[len(held)-1] == int32(pid) {
				continue
			}
			holders[inode] = append(holders[inode], int32(pid))
		}
	}
	return holders, nil
}

// socketInode reads a descriptor link of the form "socket:[12345]".
func socketInode(target string) (uint64, bool) {
	rest, ok := strings.CutPrefix(target, "socket:[")
	if !ok {
		return 0, false
	}
	inode, err := strconv.ParseUint(strings.TrimSuffix(rest, "]"), 10, 64)
	return inode, err == nil
}

// ownerOf names the process to show for a socket that several hold. PID 1
// comes last: systemd keeps every socket-activated listener it hands on, and
// naming init sends the reader to the one process not serving the port. Among
// the rest the lowest PID, which for a prefork server is its master rather
// than one of its workers. When systemd is the only holder — a service not
// started yet, or one it spawns per connection — systemd is what answers.
// Nobody (0) means no process this account can see.
func ownerOf(pids []int32) int32 {
	owner := int32(0)
	for _, pid := range pids {
		if owner == 0 || owner == 1 || pid != 1 && pid < owner {
			owner = pid
		}
	}
	return owner
}
