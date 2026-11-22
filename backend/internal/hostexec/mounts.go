package hostexec

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"
)

// The host's mount namespace, opened once. A descriptor of -1 with no error
// means this process already lives in it: a plain install, or a container
// that does not share the host PID namespace, where /proc/1 is its own init.
var (
	hostMountsOnce sync.Once
	hostMountsFD   = -1
	hostMountsErr  error
)

func hostMountNamespace() (int, error) {
	hostMountsOnce.Do(func() {
		ours, err := os.Stat("/proc/self/ns/mnt")
		if err != nil {
			return
		}
		theirs, err := os.Stat("/proc/1/ns/mnt")
		if err != nil {
			// Not root, or no /proc/1 to borrow from: there is no other view
			// this process could reach, so the one it has is the answer.
			return
		}
		if os.SameFile(ours, theirs) {
			return
		}
		hostMountsFD, hostMountsErr = unix.Open("/proc/1/ns/mnt", unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if hostMountsErr != nil {
			hostMountsFD = -1
		}
	})
	return hostMountsFD, hostMountsErr
}

// InHostMounts runs fn with the host's mount namespace as its filesystem —
// the one a host shell, and so the web terminal, sees — and waits for it.
//
// The container mirrors a handful of host directories at their own paths, and
// everything else is the image's: /tmp, /usr and /var/lib here are not the
// server's. Resolving through /host instead would mean translating every path
// the kernel hands back (symlink targets, walk results, error text) and
// re-rooting absolute symlinks by hand. Entering the namespace makes the real
// path the real file, so the code inside fn needs no knowledge of any of it.
//
// A mount namespace is a property of a thread, and setns refuses one whose
// filesystem attributes are shared, which every Go thread's are. So fn runs on
// a goroutine locked to a thread of its own that unshares those attributes
// first, and that thread is never unlocked: Go discards it when the goroutine
// returns rather than handing a thread with another root to unrelated code.
// The exception is the process's main thread, which Go parks instead of ending,
// and which is what /proc/<pid>/ns reports. A program using this must keep that
// thread off the scheduler with runtime.LockOSThread in an init function, as
// cmd/server does.
// Only fn itself is moved. A goroutine it starts runs in this container again,
// and so does anything it leaves for later; descriptors it opens are the
// process's and stay valid everywhere.
//
// When this process is already in the host's namespace fn runs in place.
// When the namespace cannot be entered fn still runs, in place, and the error
// says so — the caller decides whether that view is good enough to report.
// A panic in fn is re-raised on the caller's goroutine, so the request's own
// recovery sees it.
func InHostMounts(fn func()) error {
	fd, err := hostMountNamespace()
	if err != nil || fd < 0 {
		fn()
		if err != nil {
			return fmt.Errorf("open host mount namespace: %w", err)
		}
		return nil
	}
	_, entered := onHostThread(fd, fn, true)
	return entered
}

// IntoHostMounts runs fn in the host's mount namespace only by moving into it
// from another one, and otherwise does not run it and reports false: for work
// that would reach something else under the same paths here, such as Docker's
// own /etc/hosts in place of the server's. A process with no other namespace
// to enter — a plain install, or a container without the host's PID namespace
// — gets false and no error, and decides for itself where the work belongs.
func IntoHostMounts(fn func()) (bool, error) {
	fd, err := hostMountNamespace()
	if err != nil {
		return false, fmt.Errorf("open host mount namespace: %w", err)
	}
	if fd < 0 {
		return false, nil
	}
	return onHostThread(fd, fn, false)
}

// onHostThread runs fn on a thread of its own that has entered the namespace
// fd names. When entering fails, fn still runs there if always is set, which
// is then the container's view; ran says whether it ran at all.
func onHostThread(fd int, fn func(), always bool) (ran bool, entered error) {
	type outcome struct {
		ran      bool
		entered  error
		panicked bool
		value    any
	}
	done := make(chan outcome, 1)
	go func() {
		runtime.LockOSThread()
		var result outcome
		finished := false
		defer func() {
			if !finished {
				// runtime.Goexit leaves recover() nil; it is still not a return.
				result.panicked, result.value = true, recover()
			}
			done <- result
		}()
		result.entered = enterHostMounts(fd)
		if result.entered == nil || always {
			result.ran = true
			fn()
		}
		finished = true
	}()
	result := <-done
	if result.panicked && result.value != nil {
		panic(result.value)
	}
	return result.ran, result.entered
}

func enterHostMounts(fd int) error {
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return fmt.Errorf("unshare filesystem attributes: %w", err)
	}
	if err := unix.Setns(fd, unix.CLONE_NEWNS); err != nil {
		return fmt.Errorf("enter host mount namespace: %w", err)
	}
	return nil
}
