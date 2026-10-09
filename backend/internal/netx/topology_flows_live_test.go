package netx

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// TestLiveConntrackDumpReadsAnOwnedNamespace reads a throwaway namespace's
// connection tracking over ctnetlink, as the Overview reads the host's. The
// reader runs in-process, so this test enters the namespace itself and needs
// to run as root:
//
//	sudo -E JD_NETNS_LIVE=1 go test ./internal/netx -run TestLiveConntrack -v
func TestLiveConntrackDumpReadsAnOwnedNamespace(t *testing.T) {
	liveRequired(t)
	if os.Geteuid() != 0 {
		t.Skip("the in-process netlink reader must enter the namespace as root")
	}
	ns := newLiveNS(t)
	// A fresh namespace tracks nothing until a rule needs connection state.
	if _, err := ns.run(context.Background(), []byte("table inet jd_ct_probe {\n chain out {\n  type filter hook output priority 0;\n  ct state new counter accept\n }\n}\n"), "nft", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	ns.must(t, "ip", "addr", "add", "10.99.0.1/24", "dev", "lo")
	ns.must(t, "ping", "-c", "1", "-W", "1", "10.99.0.1")

	runtime.LockOSThread()
	original, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	target, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", ns.pid))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		t.Fatal(err)
	}
	flows, truncated, dumpErr := dumpConntrack(context.Background(), 1000)
	if err := unix.Setns(int(original.Fd()), unix.CLONE_NEWNET); err != nil {
		// The thread stays locked and is discarded when the test goroutine ends.
		t.Fatalf("restoring the test thread's namespace: %v", err)
	}
	runtime.UnlockOSThread()
	if dumpErr != nil || truncated {
		t.Fatalf("dump: %v truncated=%v", dumpErr, truncated)
	}
	found := false
	for _, f := range flows {
		if f.Protocol == 1 && f.OrigSrc.String() == "10.99.0.1" && f.OrigDst.String() == "10.99.0.1" && f.ReplySrc.String() == "10.99.0.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the namespace's ICMP entry was not read: %+v", flows)
	}
}
