package procs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

// ErrOwnTree refuses a signal to a tree the dashboard itself runs inside:
// ending it would end the page the operator pressed it on, half-way through.
var ErrOwnTree = errors.New("the dashboard itself runs under this process")

// Descendants lists every process below pid, nearest first, from one pass
// over the table. A cycle in the parent links — a PID reused mid-read — is
// cut where it closes.
func Descendants(ctx context.Context, pid int32) ([]int32, error) {
	all, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	children := map[int32][]int32{}
	for _, p := range all {
		if ppid, err := p.PpidWithContext(ctx); err == nil {
			children[ppid] = append(children[ppid], p.Pid)
		}
	}
	return descendantsOf(children, pid), nil
}

func descendantsOf(children map[int32][]int32, pid int32) []int32 {
	out := []int32{}
	seen := map[int32]bool{pid: true}
	queue := []int32{pid}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, child := range children[next] {
			if seen[child] {
				continue
			}
			seen[child] = true
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out
}

// SignalTree delivers a signal to a process and then to everything below it,
// and returns the PIDs it reached.
//
// The tree is read before the first signal, and the process goes first: a
// supervisor that is told to stop before its workers does not start new ones
// in their place, and the workers it was already ending are answered by the
// "no such process" that is skipped here. A tree holding the dashboard's own
// process is refused whole, and kernel threads and zombies in it are passed
// over, as Signal refuses them one at a time.
func (t *Table) SignalTree(ctx context.Context, pid int32, sig string) ([]int32, error) {
	if _, ok := allowedSignals[strings.ToUpper(sig)]; !ok {
		return nil, fmt.Errorf("signal %q is not permitted", sig)
	}
	below, err := Descendants(ctx, pid)
	if err != nil {
		return nil, err
	}
	self := int32(os.Getpid())
	started := make(map[int32]int64, len(below))
	for _, child := range below {
		if child == self {
			return nil, ErrOwnTree
		}
		started[child] = createTime(ctx, child)
	}
	if err := t.Signal(ctx, pid, sig); err != nil {
		return nil, err
	}
	reached := []int32{pid}
	for _, child := range below {
		// A child that exited after the tree was read may have left its PID
		// to an unrelated process; its start time says whether it is still
		// the one that was below this process.
		if now := createTime(ctx, child); now == 0 || now != started[child] {
			continue
		}
		if kernel, ok := kernelThread("/proc", child); ok && kernel {
			continue
		}
		if stat, ok := statFields(filepath.Join("/proc", strconv.Itoa(int(child)), "stat")); ok && len(stat) > 0 && stat[0] == "Z" {
			continue
		}
		if err := t.Signal(ctx, child, sig); err == nil {
			reached = append(reached, child)
		}
	}
	return reached, nil
}

// createTime is a process's start in milliseconds, or 0 when it is gone.
func createTime(ctx context.Context, pid int32) int64 {
	p, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return 0
	}
	ms, err := p.CreateTimeWithContext(ctx)
	if err != nil {
		return 0
	}
	return ms
}

// SetOOMScoreAdj sets the bias the kernel's out-of-memory killer applies to a
// process: −1000 exempts it, 1000 makes it the first to go. The file is the
// kernel's, named by the PID alone, so no client path is involved.
func (t *Table) SetOOMScoreAdj(ctx context.Context, pid int32, adj int) error {
	if adj < -1000 || adj > 1000 {
		return fmt.Errorf("the out-of-memory bias must be between -1000 and 1000")
	}
	if pid <= 1 || pid == int32(os.Getpid()) {
		return fmt.Errorf("refusing to change the out-of-memory bias of pid %d", pid)
	}
	if _, err := process.NewProcessWithContext(ctx, pid); err != nil {
		return fmt.Errorf("no process with pid %d", pid)
	}
	path := filepath.Join("/proc", strconv.Itoa(int(pid)), "oom_score_adj")
	if err := os.WriteFile(path, []byte(strconv.Itoa(adj)), 0); err != nil {
		return fmt.Errorf("set the out-of-memory bias of pid %d: %w", pid, err)
	}
	return nil
}

// OOMScoreAdj reads the bias SetOOMScoreAdj sets, for the audit record's
// "previous".
func OOMScoreAdj(pid int32) (int, bool) {
	_, adj, ok := readOOM(filepath.Join("/proc", strconv.Itoa(int(pid))))
	return adj, ok
}
