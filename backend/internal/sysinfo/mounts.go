package sysinfo

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// mountStatTimeout is how long one pass waits for a filesystem to answer. A
// local filesystem answers statfs from memory in microseconds; one that has
// not answered in two seconds is a device or a server that went away, and the
// kernel may hold that call for minutes.
const mountStatTimeout = 2 * time.Second

// mountProbe measures mount points where the mount table that names them is
// true: the host's mount namespace.
//
// The table comes from /proc/1/mountinfo, which is the host's because the
// backend shares the host's PID namespace. The paths in it mean something else
// inside this container: /boot here is an empty directory of the image, so a
// statfs of it answers with the container's root filesystem, and /boot/efi or
// a second disk at /data is not here at all. Measured in the container, /boot
// was recorded for weeks at the root disk's size, and a /boot that fills, the
// classic cause of a failed kernel upgrade, could never be seen.
//
// Each mount point is measured on a goroutine of its own, because statfs
// cannot be cancelled: a device that stopped answering holds the call, and the
// thread under it, until it comes back. One call per mount point is ever
// outstanding in the process, whoever asks. A caller waits for an answer until
// mountStatTimeout after the call began and then goes on without that mount,
// so a hung mount costs the recorder one short wait and then nothing, rather
// than every sample from then on.
type mountProbe struct {
	// run is hostexec.InHostMounts. Its error means the namespace could not
	// be entered and fn ran in this one, which is the best answer left.
	run func(fn func()) error
	// measure is one statfs, in whatever namespace it is called from.
	measure func(path string) (*disk.UsageStat, error)
	timeout time.Duration

	mu      sync.Mutex
	flights map[string]*mountFlight
}

// mountFlight is one measurement of one mount point, from the moment it is
// asked for until the kernel answers.
type mountFlight struct {
	started time.Time
	done    chan struct{}
	// Written before done is closed and never after.
	usage *disk.UsageStat
	err   error
}

// hostMounts is the probe every Collector shares. The live socket makes a
// Collector per connection; were the outstanding calls theirs, a hung mount
// would hold a thread for each tab that was ever open.
var hostMounts = newMountProbe(hostexec.InHostMounts, statMount, mountStatTimeout)

func newMountProbe(run func(func()) error, measure func(string) (*disk.UsageStat, error), timeout time.Duration) *mountProbe {
	return &mountProbe{run: run, measure: measure, timeout: timeout, flights: map[string]*mountFlight{}}
}

func statMount(path string) (*disk.UsageStat, error) {
	// gopsutil's Usage is a plain statfs and ignores its context.
	return disk.UsageWithContext(context.Background(), path)
}

// usages measures every path and returns the ones that answered in time. A
// path that failed or has not answered is absent, as a mount point that cannot
// be read always was.
func (p *mountProbe) usages(ctx context.Context, paths []string) map[string]*disk.UsageStat {
	// Every measurement is under way before the first is waited for, so the
	// pass is bounded by one timeout, not one per hung mount.
	flights := make([]*mountFlight, len(paths))
	for i, path := range paths {
		flights[i] = p.flight(path)
	}
	out := make(map[string]*disk.UsageStat, len(paths))
	for i, f := range flights {
		if usage, ok := f.wait(ctx, p.timeout); ok {
			out[paths[i]] = usage
		}
	}
	return out
}

// flight is the measurement of path now under way, started if there is none.
func (p *mountProbe) flight(path string) *mountFlight {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f, ok := p.flights[path]; ok {
		return f
	}
	f := &mountFlight{started: time.Now(), done: make(chan struct{})}
	p.flights[path] = f
	go func() {
		defer func() {
			// A panic under run is re-raised here, on a goroutine nothing
			// else would recover: one unreadable mount is not worth the
			// process.
			if r := recover(); r != nil {
				f.usage, f.err = nil, fmt.Errorf("measure %s: panic: %v", path, r)
			}
			p.mu.Lock()
			delete(p.flights, path)
			p.mu.Unlock()
			close(f.done)
		}()
		_ = p.run(func() { f.usage, f.err = p.measure(path) })
	}()
	return f
}

// wait is the flight's answer, if it arrives before the flight is timeout old.
// A flight already older than that is not waited for at all: it is the hung
// mount an earlier pass gave up on.
func (f *mountFlight) wait(ctx context.Context, timeout time.Duration) (*disk.UsageStat, bool) {
	select {
	case <-f.done:
		return f.usage, f.err == nil && f.usage != nil
	default:
	}
	left := time.Until(f.started.Add(timeout))
	if left <= 0 {
		return nil, false
	}
	timer := time.NewTimer(left)
	defer timer.Stop()
	select {
	case <-f.done:
		return f.usage, f.err == nil && f.usage != nil
	case <-timer.C:
	case <-ctx.Done():
	}
	return nil, false
}
