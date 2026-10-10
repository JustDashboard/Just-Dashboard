package netx

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The independent watchdog preflight is recorded here; firewall observations,
// selected effects and targeted recovery use the real disposable kernel. The
// separate systemd acceptance fixture proves timer execution, not this test.
func TestLiveDriftRepairTouchesOnlySelectedAdmissionAndRestoresItAfterDeadline(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	ns.must(t, "ip", "link", "add", "native0", "type", "dummy")
	beforeNative := ns.must(t, "ip", "-j", "-d", "link", "show", "dev", "native0")
	ns.must(t, "iptables", "-P", "FORWARD", "DROP")
	ns.must(t, "iptables", "-A", "FORWARD", "-s", "198.51.100.0/24", "-j", "DROP")
	ns.must(t, "iptables", "-A", "INPUT", "-s", "192.0.2.0/24", "-j", "DROP")
	ns.must(t, "iptables", append([]string{"-I", "INPUT", "1"}, admissionRule()...)...)
	ns.must(t, "ip6tables", "-A", "INPUT", "-s", "2001:db8::/32", "-j", "DROP")
	beforeForward := ns.must(t, "iptables", "-S", "FORWARD")
	beforeInput := ns.must(t, "iptables", "-S", "INPUT")
	beforeIPv6 := ns.must(t, "ip6tables", "-S", "INPUT")
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "unrelated0", Kind: "dummy", Up: true}}
	sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.5", TargetPort: "80", SourceNAT: "never", Enabled: true}}
	h := driftRepairHost(t, sp)
	priorRun, priorStdin := run, runStdin
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "nft" || name == "iptables" || name == "ip6tables" || name == "ip" {
			return ns.run(ctx, nil, name, args...)
		}
		return priorRun(ctx, name, args...)
	}
	runStdin = func(ctx context.Context, input []byte, name string, args ...string) (string, error) {
		if name == "nft" || name == "iptables" || name == "ip6tables" {
			return ns.run(ctx, input, name, args...)
		}
		return priorStdin(ctx, input, name, args...)
	}
	t.Cleanup(func() { run, runStdin = priorRun, priorStdin })
	r := h.Drift(context.Background())
	item := driftRepairItem(t, r, "inet/FORWARD")
	status, err := h.RepairDrift(WithPendingConfirmation(context.Background(), 7), driftRepairRequest(r, item), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	after := ns.must(t, "iptables", "-S", "FORWARD")
	lines := strings.Split(after, "\n")
	if len(lines) < 3 || !strings.Contains(lines[1], "just-dashboard-gateway") || !strings.Contains(after, "198.51.100.0/24") {
		t.Fatalf("selected owned rule did not preserve foreign rules: %s", after)
	}
	if ns.must(t, "iptables", "-S", "INPUT") != beforeInput || ns.must(t, "ip6tables", "-S", "INPUT") != beforeIPv6 {
		t.Fatal("selected repair changed unselected chains or families")
	}
	if status.Persistence != "not_applicable" || status.Boot != "not_applicable" || status.Phase != "awaiting_confirmation" {
		t.Fatalf("wrong runtime evidence: %+v", status)
	}
	j, err := readChange(h.paths.Dir)
	if err != nil {
		t.Fatal(err)
	}
	j.ExpiresAt = time.Now().Add(-time.Second)
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if len(j.BootDependencies) != 0 || !j.SelectedDriftRepair {
		t.Fatal("selected admission journal includes unrelated boot replay")
	}
	if err := RecoverNetworkBoot(context.Background(), h.paths.Dir, status.ID); err != nil {
		t.Fatal(err)
	}
	if ns.must(t, "ip", "-j", "-d", "link", "show", "dev", "native0") != beforeNative {
		t.Fatal("boot-context selected undo changed a native device")
	}
	if _, err := ns.run(context.Background(), nil, "ip", "link", "show", "dev", "unrelated0"); err == nil {
		t.Fatal("boot-context selected undo recreated an unselected managed link")
	}
	if ns.must(t, "iptables", "-S", "FORWARD") != beforeForward || ns.must(t, "iptables", "-S", "INPUT") != beforeInput || ns.must(t, "ip6tables", "-S", "INPUT") != beforeIPv6 {
		t.Fatal("targeted recovery did not restore exact owned presence and preserve unselected chains")
	}
}
