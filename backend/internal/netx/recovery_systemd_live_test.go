package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// This lane creates only uniquely named transient systemd units. All recovery
// ip commands are confined by NetworkNamespacePath to the fixture namespace;
// the helper's private journal and restored files live under the test directory.
func TestLiveSystemdTimerRecoversExpiredPendingChangeWithoutTheBackend(t *testing.T) {
	liveRequired(t)
	if os.Getenv("JD_SYSTEMD_RECOVERY_LIVE") != "1" {
		t.Skip("set JD_SYSTEMD_RECOVERY_LIVE=1 to exercise the real host systemd timer")
	}
	if out, err := gwLiveCmd(context.Background(), "systemctl", "show", "--property=SystemState", "--value").CombinedOutput(); err != nil {
		t.Skipf("host systemd manager is unavailable: %v %s", err, out)
	}
	ns := gwLiveNS(t, "systemd-recovery")
	s := testService(t)
	helper := filepath.Join(t.TempDir(), recoveryBinary)
	buildCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-trimpath", "-o", helper, "./cmd/server")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOMAXPROCS=2")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the static standalone recovery helper: %v %s", err, out)
	}
	var units []string
	t.Cleanup(func() {
		for _, unit := range units {
			_ = gwLiveCmd(context.Background(), "systemctl", "stop", unit+".timer", unit+".service").Run()
			_ = gwLiveCmd(context.Background(), "systemctl", "reset-failed", unit+".timer", unit+".service").Run()
		}
	})
	arm := func(id string) {
		t.Helper()
		unit := fmt.Sprintf("jd-recovery-acceptance-%d-%d", os.Getpid(), time.Now().UnixNano())
		units = append(units, unit)
		marker := filepath.Join(filepath.Dir(helper), unit+".completed")
		args := []string{"systemd-run", "--quiet", "--collect", "--unit=" + unit, "--on-active=2s", "--timer-property=AccuracySec=100ms", "--timer-property=RemainAfterElapse=no", "--property=Type=oneshot", "--property=TimeoutStartSec=20s", "--property=NetworkNamespacePath=/run/netns/" + ns,
			"--property=ExecStartPost=/usr/bin/touch " + fmt.Sprintf("%q", marker), "--", helper, "--network-recover", s.paths.Dir, id}
		if out, err := gwLiveCmd(context.Background(), args...).CombinedOutput(); err != nil {
			t.Fatalf("arming independent transient recovery timer: %v %s", err, out)
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		out, _ := gwLiveCmd(context.Background(), "journalctl", "--no-pager", "--lines=20", "-u", unit+".service").CombinedOutput()
		t.Fatalf("independent recovery timer did not complete: %s", out)
	}
	readJournal := func() *changeJournal {
		t.Helper()
		out := gwLiveHost(t, "cat", filepath.Join(s.paths.Dir, recoveryFile))
		var j changeJournal
		if err := json.Unmarshal([]byte(out), &j); err != nil {
			t.Fatal(err)
		}
		return &j
	}
	writeJournal := func(j *changeJournal) {
		t.Helper()
		b, err := json.Marshal(j)
		if err != nil {
			t.Fatal(err)
		}
		// A prior root helper owns this private file. Replacing its inode is
		// authorized by the test's owned writable directory, without chmod.
		if err := writeFileAtomic(filepath.Join(s.paths.Dir, recoveryFile), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{
		ID: "expired-test", Phase: "awaiting_confirmation", Generation: strings.Repeat("a", 64), Watchdog: "armed", Runtime: "applied", Persistence: "written", OwnerUserID: 42, AppliedAt: now, ExpiresAt: now.Add(2 * time.Second),
	}, Files: []recoverySnapshot{{Path: s.specPath(), Data: []byte("previous generation\n"), Mode: 0o600, Exists: true}},
		Commands: []recoveryCommand{{Tool: "ip", Args: []string{"link", "del", "timer0"}, AllowGone: true}}}
	if err := writeFileAtomic(s.specPath(), []byte("candidate generation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJournal(j)
	gwInNS(t, ns, "ip", "link", "add", "timer0", "type", "dummy")
	// No dashboard server or applying process is running. Only systemd owns
	// the new timer and launches the fresh static helper after its deadline.
	arm(j.ID)
	got := readJournal()
	if got.Phase != "recovered" || got.Runtime != "restored" || got.Persistence != "restored" || len(got.RecoveryErrors) != 0 || time.Now().Before(j.ExpiresAt) {
		t.Fatalf("expired timer recovery evidence = %+v", got)
	}
	if out := gwLiveHost(t, "cat", s.specPath()); out != "previous generation\n" {
		t.Fatalf("timer did not restore the private snapshot: %q", out)
	}
	if _, err := gwLiveCmd(context.Background(), "ip", "netns", "exec", ns, "ip", "link", "show", "dev", "timer0").CombinedOutput(); err == nil {
		t.Fatal("actual candidate device survived timer recovery")
	}
	// A timer for a terminal generation still executes the helper but leaves
	// subsequently observed runtime alone.
	gwInNS(t, ns, "ip", "link", "add", "timer0", "type", "dummy")
	arm(j.ID)
	gwInNS(t, ns, "ip", "link", "show", "dev", "timer0")
	if got := readJournal(); got.Phase != "recovered" || got.ID != j.ID {
		t.Fatalf("completed generation was touched: %+v", got)
	}
	// A stale timer cannot recover a newer pending journal even though that
	// journal would otherwise be eligible for recovery.
	j.ID, j.Phase = "newer-test", "awaiting_confirmation"
	j.ExpiresAt = time.Now().Add(time.Minute)
	writeJournal(j)
	arm("expired-test")
	gwInNS(t, ns, "ip", "link", "show", "dev", "timer0")
	if got := readJournal(); got.ID != "newer-test" || got.Phase != "awaiting_confirmation" {
		t.Fatalf("old timer changed a newer generation: %+v", got)
	}
}
