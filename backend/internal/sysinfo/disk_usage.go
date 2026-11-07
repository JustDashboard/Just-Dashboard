package sysinfo

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
)

// DirEntry is one row of the per-directory size breakdown.
type DirEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"isDir"`
	Entries int    `json:"entries"`
	// Partial says the scan stopped before it had counted all of this entry —
	// its time or its work ran out, or part of it could not be read — so Size and
	// Entries are floors: it holds at least that much.
	Partial bool `json:"partial,omitempty"`
}

// ErrDirScanLimit is a directory with more entries directly inside it than a
// breakdown ranks.
var ErrDirScanLimit = errors.New("directory scan exceeded its work limit")

const (
	// maxDirBreakdownChildren bounds the entries directly inside the scanned
	// directory, each of which is a row to rank.
	maxDirBreakdownChildren = 10_000
	// maxDirBreakdownVisits bounds one scan's work. A server's / with Docker on it
	// holds a few million entries — 2.5 million on a busy one, which four workers
	// walk in about ten seconds once the disk's cache is warm — so this lets a
	// scan of / finish; past it the answer is what was counted, marked partial.
	maxDirBreakdownVisits = 5_000_000
	// dirBreakdownWorkers walk at once. A disk answers several reads at a time
	// faster than one after another, a VPS's network-backed disk most of all.
	dirBreakdownWorkers = 4
	// dirReadBatch is how many entries of a directory are read at once, so a
	// directory of a million files is never held whole.
	dirReadBatch = 512
)

type dirScanLimits struct{ children, visits, workers int }

// DirBreakdown answers "what is eating this mount": the entries directly inside
// root, each directory with everything under it summed, largest first. It stays
// on root's filesystem: a child that is a mount of its own (/proc, /sys, a second
// disk) is left out, as is anything mounted deeper, since its space is not this
// mount's.
//
// Several workers walk at once. run, when set, is how each runs, and how root is
// first read: the dashboard runs them in the host's mount namespace, which a
// goroutine does not inherit (hostexec.InHostMounts), so nothing here reads the
// filesystem from a goroutine of its own outside run.
//
// When ctx ends or the work runs out, the answer is what was counted, with each
// entry it did not finish marked Partial; so is an entry part of which could not
// be read. It fails only when root cannot be read, or holds more than
// maxDirBreakdownChildren entries (ErrDirScanLimit).
func DirBreakdown(ctx context.Context, root string, limit int, run func(func())) ([]DirEntry, error) {
	return dirBreakdown(ctx, root, limit, dirScanLimits{
		children: maxDirBreakdownChildren, visits: maxDirBreakdownVisits, workers: dirBreakdownWorkers,
	}, run)
}

func dirBreakdown(ctx context.Context, root string, limit int, limits dirScanLimits, run func(func())) ([]DirEntry, error) {
	if run == nil {
		run = func(fn func()) { fn() }
	}
	scan := &dirScan{ctx: ctx}
	scan.ready = sync.NewCond(&scan.mu)
	scan.left.Store(int64(limits.visits))
	var (
		out []DirEntry
		err error
	)
	run(func() { out, err = scan.begin(root, limits.children) })
	if err != nil {
		return nil, err
	}
	stopWatching := context.AfterFunc(ctx, scan.halt)
	defer stopWatching()
	var workers sync.WaitGroup
	for range max(limits.workers, 1) {
		workers.Go(func() { run(scan.work) })
	}
	workers.Wait()
	for i := range out {
		if !out[i].IsDir {
			continue
		}
		tally := &scan.tops[i]
		out[i].Size, out[i].Entries = tally.size.Load(), int(tally.files.Load())
		out[i].Partial = tally.cut.Load() || tally.open.Load() > 0
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// dirScan is one breakdown's shared work: the directories still to read, and
// what has been counted under each entry of the root.
type dirScan struct {
	ctx context.Context
	dev uint64
	// left is how many more entries may be looked at.
	left atomic.Int64
	tops []dirTally

	mu    sync.Mutex
	ready *sync.Cond
	jobs  []dirJob
	busy  int
	// stopped is set once the scan is to end where it is.
	stopped atomic.Bool
}

// dirTally is what has been counted under one entry of the root.
type dirTally struct {
	size, files atomic.Int64
	// open is how many of its directories are waiting or being read.
	open atomic.Int64
	// cut is set when part of it was not counted: unreadable, or left halfway.
	cut atomic.Bool
}

type dirJob struct {
	path string
	top  int
}

// begin reads root's own entries, each a row, and queues its directories.
func (s *dirScan) begin(root string, maxChildren int) ([]DirEntry, error) {
	dir, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxChildren + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > maxChildren {
		return nil, ErrDirScanLimit
	}
	if s.dev, err = deviceOf(root); err != nil {
		return nil, err
	}
	out := make([]DirEntry, 0, len(entries))
	s.tops = make([]dirTally, len(entries))
	for _, e := range entries {
		full := filepath.Join(root, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !e.IsDir() {
			out = append(out, DirEntry{Name: e.Name(), Path: full, Size: info.Size()})
			continue
		}
		if dev, err := deviceOf(full); err != nil || dev != s.dev {
			continue
		}
		s.push(dirJob{path: full, top: len(out)})
		out = append(out, DirEntry{Name: e.Name(), Path: full, IsDir: true})
	}
	return out, nil
}

func (s *dirScan) push(job dirJob) {
	s.tops[job.top].open.Add(1)
	s.mu.Lock()
	s.jobs = append(s.jobs, job)
	s.mu.Unlock()
	s.ready.Signal()
}

// next is the next directory to read, or false once there is none left and
// none being read that could add one, or the scan has stopped.
func (s *dirScan) next() (dirJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.stopped.Load() {
			return dirJob{}, false
		}
		if n := len(s.jobs); n > 0 {
			job := s.jobs[n-1]
			s.jobs = s.jobs[:n-1]
			s.busy++
			return job, true
		}
		if s.busy == 0 {
			return dirJob{}, false
		}
		s.ready.Wait()
	}
}

func (s *dirScan) finished(job dirJob) {
	s.tops[job.top].open.Add(-1)
	s.mu.Lock()
	s.busy--
	idle := s.busy == 0 && len(s.jobs) == 0
	s.mu.Unlock()
	if idle {
		s.ready.Broadcast()
	}
}

// halt ends the scan where it is: its time or its work has run out.
func (s *dirScan) halt() {
	s.mu.Lock()
	s.stopped.Store(true)
	s.mu.Unlock()
	s.ready.Broadcast()
}

func (s *dirScan) work() {
	for {
		job, ok := s.next()
		if !ok {
			return
		}
		s.read(job)
		s.finished(job)
	}
}

// read counts one directory's files and queues its directories.
func (s *dirScan) read(job dirJob) {
	tally := &s.tops[job.top]
	// What vanished between being listed and being looked at is not there to
	// count; anything else not read leaves the figure a floor.
	failed := func(err error) {
		if !errors.Is(err, fs.ErrNotExist) {
			tally.cut.Store(true)
		}
	}
	dir, err := os.Open(job.path)
	if err != nil {
		failed(err)
		return
	}
	defer dir.Close()
	for {
		if s.stopped.Load() || s.ctx.Err() != nil {
			tally.cut.Store(true)
			return
		}
		batch, err := dir.ReadDir(dirReadBatch)
		for _, e := range batch {
			if s.left.Add(-1) < 0 {
				tally.cut.Store(true)
				s.halt()
				return
			}
			if e.IsDir() {
				full := filepath.Join(job.path, e.Name())
				// Not into another filesystem: the question is this mount's space.
				if dev, err := deviceOf(full); err != nil {
					failed(err)
				} else if dev == s.dev {
					s.push(dirJob{path: full, top: job.top})
				}
				continue
			}
			info, err := e.Info()
			if err != nil {
				failed(err)
				continue
			}
			// A symlink counts as nothing, or a tree with links into /usr would
			// report the size of the operating system.
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			tally.size.Add(info.Size())
			tally.files.Add(1)
		}
		switch {
		case err == io.EOF:
			return
		case err != nil:
			failed(err)
			return
		}
	}
}
