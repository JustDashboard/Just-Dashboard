package netx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These run the owned nftables table and a real ufw inside throwaway
// namespaces. The ufw sandbox binds copies of ufw's configuration over
// /etc/ufw and /etc/default/ufw and a private tmpfs over /run inside the
// namespace's own mount table, so neither the host's rules nor its lock
// file are touched; module loading and sysctl application are switched off
// in the copied defaults.
//
//	JD_NETNS_LIVE=1 go test ./internal/netx/ -run 'TestLiveOwnedFirewall|TestLiveUFW' -v

// liveClient makes a second namespace inside the holder, joined by a veth
// pair: the server end 10.77.0.1, the client end 10.77.0.2.
func liveClient(t *testing.T, ns *liveNS) {
	t.Helper()
	ns.must(t, "ip", "netns", "add", "client")
	ns.must(t, "ip", "link", "add", "fw0", "type", "veth", "peer", "name", "fw1")
	ns.must(t, "ip", "link", "set", "fw1", "netns", "client")
	ns.must(t, "ip", "addr", "add", "10.77.0.1/24", "dev", "fw0")
	ns.must(t, "ip", "link", "set", "fw0", "up")
	ns.must(t, "ip", "-n", "client", "addr", "add", "10.77.0.2/24", "dev", "fw1")
	ns.must(t, "ip", "-n", "client", "link", "set", "fw1", "up")
	ns.must(t, "ip", "-n", "client", "link", "set", "lo", "up")
}

// liveListen starts a TCP listener in the server namespace for the test.
func liveListen(t *testing.T, ns *liveNS, port int) {
	t.Helper()
	cmd := ns.command(context.Background(), "nc", "-lk", "10.77.0.1", strconv.Itoa(port))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = liveSudo("pkill", "-f", "nc -lk 10.77.0.1 "+strconv.Itoa(port)).Run()
		_ = cmd.Wait()
	})
	time.Sleep(200 * time.Millisecond)
}

// liveConnects reports whether the client namespace completes a TCP
// handshake with the server's port within a second.
func liveConnects(ns *liveNS, port int) bool {
	_, err := ns.run(context.Background(), nil, "ip", "netns", "exec", "client", "nc", "-z", "-w", "1", "10.77.0.1", strconv.Itoa(port))
	return err == nil
}

func TestLiveOwnedFirewallFiltersRecoversAndLeavesOtherTables(t *testing.T) {
	liveRequired(t)
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("nc is needed to measure admitted connections")
	}
	ns := newLiveNS(t)
	liveClient(t, ns)
	// A foreign table the owned one must never touch.
	ns.must(t, "nft", "add", "table", "inet", "foreign")
	ns.must(t, "nft", "add", "chain", "inet", "foreign", "input", "{ type filter hook input priority 0; policy accept; }")
	liveListen(t, ns, 8080)
	liveListen(t, ns, 9090)
	useLive(t, ns)
	s := testService(t)
	ctx := context.Background()
	if !liveConnects(ns, 8080) || !liveConnects(ns, 9090) {
		t.Fatal("the fixture's listeners are not reachable before any rule")
	}
	steps := []OwnedFirewallChange{
		{Op: "add", Rule: &FirewallRuleRequest{Action: "allow", Protocol: "tcp", Ports: "8080", Source: "10.77.0.0/24"}},
		{Op: "policy", Policy: "deny"},
		{Op: "enable"},
	}
	for _, step := range steps {
		if err := s.ChangeOwnedFirewall(ctx, step, "live"); err != nil {
			t.Fatalf("%s: %v", step.Op, err)
		}
	}
	if !liveConnects(ns, 8080) {
		t.Fatal("the admitted port is refused")
	}
	if liveConnects(ns, 9090) {
		t.Fatal("the drop policy admits a port no rule names")
	}
	if !strings.Contains(ns.must(t, "nft", "list", "tables"), "table inet foreign") {
		t.Fatal("a foreign table disappeared")
	}
	saved, err := readChange(s.paths.Dir)
	if err != nil || saved.Phase != "saved" {
		t.Fatalf("journal = %+v %v", saved, err)
	}

	// Removing the rule, then recovering it as the host helper would for
	// an unconfirmed temporary apply.
	sp := rtLoad(t, s)
	if err := s.ChangeOwnedFirewall(ctx, OwnedFirewallChange{Op: "delete", ID: sp.Firewall.Rules[0].ID}, "live"); err != nil {
		t.Fatal(err)
	}
	if liveConnects(ns, 8080) {
		t.Fatal("the removed rule still admits")
	}
	pending, err := readChange(s.paths.Dir)
	if err != nil {
		t.Fatal(err)
	}
	pending.Phase, pending.OwnerUserID = "awaiting_confirmation", 1
	if err := pending.save(); err != nil {
		t.Fatal(err)
	}
	recoverCtx := context.WithValue(ctx, recoveryExecutorKey{}, recoveryExecutor(func(ctx context.Context, input []byte, name string, args ...string) (string, error) {
		return ns.run(ctx, input, name, args...)
	}))
	if err := recoverNetwork(recoverCtx, s.paths.Dir, pending.ID, false); err != nil {
		t.Fatal(err)
	}
	if !liveConnects(ns, 8080) || liveConnects(ns, 9090) {
		t.Fatal("recovery did not restore the previous table")
	}
	if got := rtLoad(t, s); len(got.Firewall.Rules) != 1 {
		t.Fatalf("recovery did not restore the saved spec: %+v", got.Firewall)
	}

	// Switched off, the table is gone and every port answers; the foreign
	// table is still there.
	if err := s.ChangeOwnedFirewall(ctx, OwnedFirewallChange{Op: "disable"}, "live"); err != nil {
		t.Fatal(err)
	}
	if !liveConnects(ns, 9090) {
		t.Fatal("a switched-off table still filters")
	}
	tables := ns.must(t, "nft", "list", "tables")
	if strings.Contains(tables, "jd_firewall") || !strings.Contains(tables, "table inet foreign") {
		t.Fatalf("tables = %s", tables)
	}
	// The boot file of the switched-on state loads in the kernel as-is.
	if err := s.ChangeOwnedFirewall(ctx, OwnedFirewallChange{Op: "enable"}, "live"); err != nil {
		t.Fatal(err)
	}
	ns.must(t, "nft", "delete", "table", "inet", "jd_firewall")
	ns.must(t, "nft", "-f", filepath.Join(s.paths.Dir, firewallFile))
	if !liveConnects(ns, 8080) || liveConnects(ns, 9090) {
		t.Fatal("the boot file does not restore the table")
	}
}

// ufwSandbox prepares copies of ufw's shipped configuration and binds them
// over the namespace's /etc/ufw and /etc/default/ufw.
func ufwSandbox(t *testing.T, ns *liveNS) string {
	t.Helper()
	root := t.TempDir()
	etc := filepath.Join(root, "etc", "ufw")
	for _, dir := range []string{etc, filepath.Join(etc, "applications.d"), filepath.Join(root, "etc", "default")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"before.rules", "before6.rules", "after.rules", "after6.rules", "user.rules", "user6.rules", "ufw.conf"} {
		src := filepath.Join("/usr/share/ufw", name)
		if name == "user6.rules" {
			src = filepath.Join("/usr/share/ufw/iptables", name)
		}
		b, err := os.ReadFile(src)
		if err != nil {
			t.Skipf("ufw's shipped templates are needed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(etc, name), b, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(etc, "sysctl.conf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defaults := "IPV6=yes\nDEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=\"ACCEPT\"\nDEFAULT_FORWARD_POLICY=\"DROP\"\nDEFAULT_APPLICATION_POLICY=\"SKIP\"\nMANAGE_BUILTINS=no\nIPT_SYSCTL=/etc/ufw/sysctl.conf\nIPT_MODULES=\"\"\n"
	if err := os.WriteFile(filepath.Join(root, "etc", "default", "ufw"), []byte(defaults), 0o644); err != nil {
		t.Fatal(err)
	}
	ns.must(t, "mount", "-t", "tmpfs", "tmpfs", "/run")
	ns.must(t, "mount", "--bind", etc, "/etc/ufw")
	ns.must(t, "mount", "--bind", filepath.Join(root, "etc", "default", "ufw"), "/etc/default/ufw")
	prev := firewallRecoveryRoot
	firewallRecoveryRoot = root
	t.Cleanup(func() { firewallRecoveryRoot = prev })
	return root
}

// handBack returns the sandbox's files to the unprivileged test process,
// which reads and restores them where the root backend would in production.
func handBack(t *testing.T, ns *liveNS) {
	t.Helper()
	ns.must(t, "chown", "-R", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()), "/etc/ufw", "/etc/default/ufw")
}

func TestLiveUFWChangesAreRestoredByTheJournal(t *testing.T) {
	liveRequired(t)
	if _, err := exec.LookPath("ufw"); err != nil {
		if _, err := os.Stat("/usr/sbin/ufw"); err != nil {
			t.Skip("ufw is not installed")
		}
	}
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("nc is needed to measure admitted connections")
	}
	ns := newLiveNS(t)
	// The private /run comes first: the client namespace is named under it.
	root := ufwSandbox(t, ns)
	liveClient(t, ns)
	liveListen(t, ns, 8080)
	useLive(t, ns)
	s := testService(t)
	ctx := context.Background()
	ufw := func(args ...string) func(context.Context) error {
		return func(ctx context.Context) error {
			_, err := run(ctx, "ufw", args...)
			handBack(t, ns)
			return err
		}
	}
	if out := ns.must(t, "ufw", "status"); !strings.Contains(out, "Status: inactive") {
		t.Fatalf("sandbox ufw = %s", out)
	}
	handBack(t, ns)

	// A failed verification after enabling puts ufw back off at once.
	err := s.ProtectFirewallChange(ctx, FirewallState{Backend: "ufw"}, ufw("--force", "enable"),
		func(context.Context) error { return errors.New("SSH from your address would be refused") })
	if err == nil || !strings.Contains(err.Error(), "would be refused") {
		t.Fatalf("err = %v", err)
	}
	handBack(t, ns)
	if out := ns.must(t, "ufw", "status"); !strings.Contains(out, "Status: inactive") || !liveConnects(ns, 8080) {
		t.Fatalf("after recovery: %s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc", "ufw", "ufw.conf")); !strings.Contains(string(b), "ENABLED=no") {
		t.Fatalf("ufw.conf = %s", b)
	}

	// An admitted port, then enabling with a deny default: the port stays
	// reachable and others are dropped.
	if err := s.ProtectFirewallChange(ctx, FirewallState{Backend: "ufw"}, ufw("allow", "8080/tcp"), nil); err != nil {
		t.Fatal(err)
	}
	handBack(t, ns)
	if err := s.ProtectFirewallChange(ctx, FirewallState{Backend: "ufw"}, ufw("--force", "enable"), nil); err != nil {
		t.Fatal(err)
	}
	handBack(t, ns)
	if out := ns.must(t, "ufw", "status"); !strings.Contains(out, "Status: active") || !strings.Contains(out, "8080/tcp") || !liveConnects(ns, 8080) {
		t.Fatalf("enabled: %s", out)
	}
	liveListen(t, ns, 9090)
	if liveConnects(ns, 9090) {
		t.Fatal("ufw's deny default admits an unlisted port")
	}

	// Removing the rule as a temporary apply nobody confirms: the host
	// helper restores ufw's files and reloads them.
	if err := s.ProtectFirewallChange(ctx, FirewallState{Backend: "ufw", Enabled: true}, ufw("--force", "delete", "allow", "8080/tcp"), nil); err != nil {
		t.Fatal(err)
	}
	handBack(t, ns)
	if liveConnects(ns, 8080) {
		t.Fatal("the deleted rule still admits")
	}
	pending, err := readChange(s.paths.Dir)
	if err != nil || pending.Firewall != "ufw" {
		t.Fatalf("journal = %+v %v", pending, err)
	}
	pending.Phase, pending.OwnerUserID = "awaiting_confirmation", 1
	if err := pending.save(); err != nil {
		t.Fatal(err)
	}
	recoverCtx := context.WithValue(ctx, recoveryExecutorKey{}, recoveryExecutor(func(ctx context.Context, input []byte, name string, args ...string) (string, error) {
		out, err := ns.run(ctx, input, name, args...)
		handBack(t, ns)
		return out, err
	}))
	if err := recoverNetwork(recoverCtx, s.paths.Dir, pending.ID, false); err != nil {
		t.Fatal(err)
	}
	if out := ns.must(t, "ufw", "status"); !strings.Contains(out, "8080/tcp") || !liveConnects(ns, 8080) || liveConnects(ns, 9090) {
		t.Fatalf("after the helper's recovery: %s", out)
	}
	if after, _ := readChange(s.paths.Dir); after.Phase != "recovered" {
		t.Fatalf("journal = %+v", after.ChangeStatus)
	}
}
