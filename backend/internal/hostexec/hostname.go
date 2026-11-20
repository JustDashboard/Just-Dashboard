package hostexec

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"
)

// The host's UTS namespace, opened once. A descriptor of -1 means this
// process already lives in it: a plain install, or a container that does not
// share the host PID namespace, where /proc/1 is its own init.
var (
	hostUTSOnce sync.Once
	hostUTSFD   = -1
)

func hostUTSNamespace() int {
	hostUTSOnce.Do(func() {
		ours, err := os.Stat("/proc/self/ns/uts")
		if err != nil {
			return
		}
		theirs, err := os.Stat("/proc/1/ns/uts")
		if err != nil || os.SameFile(ours, theirs) {
			return
		}
		fd, err := unix.Open("/proc/1/ns/uts", unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err == nil {
			hostUTSFD = fd
		}
	})
	return hostUTSFD
}

// Hostname is the server's name as its kernel has it now.
//
// os.Hostname is not that. A container on the host's network is given a copy
// of the host's name in a UTS namespace of its own, and Docker records that
// name in the container's configuration when it creates it. A rename of the
// server never reaches the copy of a running container, so this process would
// go on calling the server by the name it had when the container was made.
//
// The name is read in PID 1's namespace, and this container's copy is then
// set to it, which is what puts every other reader in the process right as
// well: os.Hostname, and the libraries that call it. Setting the copy needs
// the privilege the shipped stack runs with; without it the name returned is
// still the host's.
//
// The thread that enters the namespace is locked and never unlocked, so Go
// discards it rather than handing a thread with another hostname to unrelated
// code. See InHostMounts for the one thread that rule cannot cover.
func Hostname() (string, error) {
	fd := hostUTSNamespace()
	if fd < 0 {
		return os.Hostname()
	}
	type outcome struct {
		name string
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Setns(fd, unix.CLONE_NEWUTS); err != nil {
			done <- outcome{err: fmt.Errorf("enter host UTS namespace: %w", err)}
			return
		}
		var uts unix.Utsname
		if err := unix.Uname(&uts); err != nil {
			done <- outcome{err: fmt.Errorf("read host name: %w", err)}
			return
		}
		done <- outcome{name: unix.ByteSliceToString(uts.Nodename[:])}
	}()
	result := <-done
	if result.err != nil || result.name == "" {
		// The copy is the best answer left, and it is right on every server
		// that was never renamed.
		return os.Hostname()
	}
	if own, err := os.Hostname(); err != nil || own != result.name {
		_ = unix.Sethostname([]byte(result.name))
	}
	return result.name, nil
}
