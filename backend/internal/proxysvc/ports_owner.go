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

// parentsOf reads each holder's parent PID from /proc/<pid>/stat. A process
// that exited since the walk is left out, and counts as nobody's child.
func parentsOf(root string, holders map[uint64][]int32) map[int32]int32 {
	parents := map[int32]int32{}
	for _, pids := range holders {
		for _, pid := range pids {
			if _, done := parents[pid]; done {
				continue
			}
			stat, err := os.ReadFile(filepath.Join(root, strconv.Itoa(int(pid)), "stat"))
			if err != nil {
				continue
			}
			if ppid, ok := parentFromStat(string(stat)); ok {
				parents[pid] = ppid
			}
		}
	}
	return parents
}

// parentFromStat reads the fourth field of "pid (comm) state ppid …". The
// command name may hold spaces and parentheses of its own, so the fields are
// counted from the last ")".
func parentFromStat(stat string) (int32, bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.ParseInt(fields[1], 10, 32)
	return int32(ppid), err == nil
}

// ownerOf names the process to show for a socket that several hold.
//
// PID 1 is set aside: systemd keeps every socket-activated listener it hands
// on, and naming init sends the reader to the one process not serving the
// port. Of the rest, the owner is the holder whose parent is not itself a
// holder — for a prefork server its master, whose workers inherited the
// socket from it. Not the lowest PID: once the PID counter wraps, workers
// respawned on a reload are numbered below the master that forked them.
// Only when several holders are unrelated (or a parent could not be read)
// does the lowest of them decide. When systemd is the only holder — a service
// not started yet, or one it spawns per connection — systemd is what
// answers. Nobody (0) means no process this account can see.
func ownerOf(pids []int32, parents map[int32]int32) int32 {
	held := map[int32]bool{}
	for _, pid := range pids {
		if pid != 1 {
			held[pid] = true
		}
	}
	if len(held) == 0 {
		if len(pids) > 0 {
			return 1
		}
		return 0
	}
	lowest := func(keep func(int32) bool) int32 {
		owner := int32(0)
		for pid := range held {
			if keep(pid) && (owner == 0 || pid < owner) {
				owner = pid
			}
		}
		return owner
	}
	root := lowest(func(pid int32) bool {
		parent, known := parents[pid]
		return !known || !held[parent]
	})
	if root != 0 {
		return root
	}
	return lowest(func(int32) bool { return true })
}
