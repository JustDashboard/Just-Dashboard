package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLiveSQMBackendDeathRestoresOwnedEffectsAndNativeBaseline(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "sqm-death")
	gwMustInNS(t, ns, "ip", "link", "add", "sqmsrc", "type", "dummy")
	gwMustInNS(t, ns, "ip", "link", "set", "sqmsrc", "up")
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", "sqmsrc", "root", "handle", "5:", "fq_codel", "limit", "1000", "target", "7ms", "noecn")
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", "sqmsrc", "clsact")
	gwMustInNS(t, ns, "tc", "filter", "add", "dev", "sqmsrc", "egress", "protocol", "all", "pref", "7", "matchall", "action", "pass")
	rootBefore := gwMustInNS(t, ns, "tc", "-j", "qdisc", "show", "dev", "sqmsrc")
	egressBefore := gwMustInNS(t, ns, "tc", "-j", "filter", "show", "dev", "sqmsrc", "egress")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "network"), 0o755); err != nil {
		t.Fatal(err)
	}
	readJournal := func() (*changeJournal, error) {
		data, err := gwLiveCmd(context.Background(), "cat", filepath.Join(dir, "network", recoveryFile)).Output()
		if err != nil {
			return nil, err
		}
		var journal changeJournal
		err = json.Unmarshal(data, &journal)
		return &journal, err
	}
	invoke := func(mode string) *exec.Cmd {
		cmd := gwLiveCmd(context.Background(), "ip", "netns", "exec", ns, "env", "JD_SQM_FIXTURE_DIR="+dir, "JD_SQM_FIXTURE_MODE="+mode, os.Args[0], "-test.run=^TestSQMProcessFixture$")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return cmd
	}
	apply := invoke("apply")
	var output bytes.Buffer
	apply.Stdout, apply.Stderr = &output, &output
	if err := apply.Start(); err != nil {
		t.Fatal(err)
	}
	alive := true
	defer func() {
		if alive {
			_ = gwLiveCmd(context.Background(), "kill", "-KILL", "--", fmt.Sprintf("-%d", apply.Process.Pid)).Run()
			_ = apply.Wait()
		}
	}()
	ready := filepath.Join(dir, "runtime-ready")
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = gwLiveCmd(context.Background(), "kill", "-KILL", "--", fmt.Sprintf("-%d", apply.Process.Pid)).Run()
			_ = apply.Wait()
			alive = false
			t.Fatalf("SQM apply never reached its death boundary: %s", output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	j, err := readJournal()
	if err != nil || j.Phase != "prepared" || j.Watchdog != "armed" || len(j.Commands) == 0 || j.Commands[0].Tool != "sqm" {
		t.Fatalf("effects preceded a durable independent plan: %+v %v", j, err)
	}
	if !strings.Contains(gwMustInNS(t, ns, "tc", "-j", "qdisc", "show", "dev", "sqmsrc"), `"kind":"htb"`) {
		t.Fatal("apply did not reach the native root effect")
	}
	_ = gwLiveCmd(context.Background(), "kill", "-KILL", "--", fmt.Sprintf("-%d", apply.Process.Pid)).Run()
	_ = apply.Wait()
	alive = false
	// A separate host-side process uses the durable plan after the API
	// process is gone. Its commands remain inside this disposable namespace.
	if out, err := invoke("recover").CombinedOutput(); err != nil {
		t.Fatalf("independent recovery: %v\n%s", err, out)
	}
	j, err = readJournal()
	if err != nil || j.Phase != "recovered" || j.Runtime != "restored" || len(j.RecoveryErrors) != 0 {
		t.Fatalf("recovery evidence: %+v %v", j, err)
	}
	if got := gwMustInNS(t, ns, "tc", "-j", "qdisc", "show", "dev", "sqmsrc"); !sqmBaselineEquivalent(rootBefore, got) {
		t.Fatalf("native baseline not restored: %s -> %s", rootBefore, got)
	}
	if got := gwMustInNS(t, ns, "tc", "-j", "filter", "show", "dev", "sqmsrc", "egress"); got != egressBefore {
		t.Fatal("foreign clsact egress changed on independent undo")
	}
	if got := gwMustInNS(t, ns, "tc", "-j", "filter", "show", "dev", "sqmsrc", "ingress"); strings.TrimSpace(got) != "[]" {
		t.Fatalf("owned redirect leaked: %s", got)
	}
	if got := gwMustInNS(t, ns, "ip", "-j", "-d", "link", "show"); strings.Contains(got, `"info_kind":"ifb"`) {
		t.Fatalf("owned IFB leaked: %s", got)
	}
}

func sqmBaselineEquivalent(before, after string) bool {
	a, errA := parseQdiscs(before)
	b, errB := parseQdiscs(after)
	if errA != nil || errB != nil || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind == "fq_codel" && b[i].Kind == "fq_codel" {
			// tc converts kernel ticks to integer microseconds and back;
			// one round trip may lose one microsecond. All other baseline
			// parameters and ownership must be exactly preserved.
			for _, key := range []string{"target", "interval"} {
				var x, y int64
				if json.Unmarshal(a[i].Options[key], &x) != nil || json.Unmarshal(b[i].Options[key], &y) != nil || x-y < -1 || x-y > 1 {
					return false
				}
				b[i].Options[key] = a[i].Options[key]
			}
		}
	}
	return reflect.DeepEqual(a, b)
}

func TestSQMProcessFixture(t *testing.T) {
	dir, mode := os.Getenv("JD_SQM_FIXTURE_DIR"), os.Getenv("JD_SQM_FIXTURE_MODE")
	if dir == "" {
		t.Skip("subprocess fixture")
	}
	s := New(Options{Paths: Paths{Dir: filepath.Join(dir, "network"), Unit: filepath.Join(dir, UnitName), Sysctl: filepath.Join(dir, "sysctl.conf")}})
	if mode == "recover" {
		// No host service calls escape this namespace fixture. The typed
		// SQM recovery itself runs real ip/tc in the separate process.
		has = func(name string) bool { return name != "systemctl" }
		j, err := readChange(s.paths.Dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := RecoverNetworkStandalone(t.Context(), s.paths.Dir, j.ID); err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode != "apply" {
		t.Fatal("unknown fixture mode")
	}
	s.independentRecovery, s.recoveryInstalled = true, true
	has = func(string) bool { return true }
	anchorPaths = func(context.Context) []anchorPath { return nil }
	redirectWritten := false
	run = func(ctx context.Context, tool string, args ...string) (string, error) {
		if tool == "systemd-run" {
			return "fixture timer armed", nil
		}
		if tool == filepath.Join(s.paths.Dir, recoveryBinary) {
			if len(args) == 1 && args[0] == "--network-sqm-check" {
				return "sqm-v1\n", nil
			}
			return "", nil
		}
		if tool == "systemctl" {
			if len(args) > 0 && args[0] == "is-enabled" {
				return "enabled\n", nil
			}
			return "", nil
		}
		if !sqmOneOf(tool, "ip", "tc", "nft", "iptables", "ip6tables") {
			return "", &UnavailableError{Tool: tool}
		}
		if redirectWritten && tool == "tc" && strings.Join(args, " ") == "-j filter show dev sqmsrc ingress" {
			if err := os.WriteFile(filepath.Join(dir, "runtime-ready"), []byte("prepared-before-effects"), 0o600); err != nil {
				return "", err
			}
			<-ctx.Done()
			return "", ctx.Err()
		}
		out, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("%s: %s: %w", tool, out, err)
		}
		if tool == "tc" && len(args) > 1 && args[0] == "filter" && args[1] == "add" {
			redirectWritten = true
		}
		return string(out), nil
	}
	runStdin = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		if len(input) != 0 {
			return "", fmt.Errorf("unexpected fixture stdin")
		}
		return run(ctx, tool, args...)
	}
	ctx := WithPendingConfirmation(t.Context(), 7)
	if err := s.SetShaping(ctx, "sqmsrc", ShapeRequest{EgressKbit: 12000, IngressKbit: 9000, SQM: &SQMProfile{}}, "127.0.0.1", "fixture"); err != nil {
		t.Fatal(err)
	}
	t.Fatal("fixture failed to stop at the backend death boundary")
}
