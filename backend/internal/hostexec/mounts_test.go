package hostexec

import (
	"errors"
	"os"
	"testing"
)

// TestInHostMountsRunsInTheHostsView needs what the backend container has —
// root, the host PID namespace and a mount namespace of its own — so it runs
// only there: `docker run --privileged --pid=host` with the test binary.
func TestInHostMountsRunsInTheHostsView(t *testing.T) {
	fd, err := hostMountNamespace()
	if err != nil || fd < 0 {
		t.Skip("no other mount namespace to enter")
	}
	host, err := os.Stat("/proc/1/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	var inside os.FileInfo
	var statErr error
	if err := InHostMounts(func() { inside, statErr = os.Stat("/proc/thread-self/ns/mnt") }); err != nil {
		t.Fatal(err)
	}
	if statErr != nil {
		t.Fatal(statErr)
	}
	if !os.SameFile(inside, host) {
		t.Fatal("fn did not run in the host's mount namespace")
	}
	ours, err := os.Stat("/proc/thread-self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(ours, host) {
		t.Fatal("the caller's thread was moved into the host's mount namespace")
	}
}

func TestInHostMountsRunsFnOnce(t *testing.T) {
	calls := 0
	if err := InHostMounts(func() { calls++ }); err != nil {
		t.Logf("ran in place: %v", err)
	}
	if calls != 1 {
		t.Fatalf("fn ran %d times, want 1", calls)
	}
}

func TestInHostMountsRaisesPanicsOnTheCaller(t *testing.T) {
	sentinel := errors.New("sentinel")
	defer func() {
		if got := recover(); got != sentinel {
			t.Fatalf("recovered %v, want the panic fn raised", got)
		}
	}()
	_ = InHostMounts(func() { panic(sentinel) })
	t.Fatal("InHostMounts returned after fn panicked")
}
