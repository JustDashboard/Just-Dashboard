package netx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rtProc stands a /proc/sys behind readSysctl, with the forwarding switches
// at the values given, and makes `sysctl -w` write to it the way the kernel
// would.
func rtProc(t *testing.T, rec *recorder, v4, v6 string) string {
	t.Helper()
	root := t.TempDir()
	prev := procSysRoot
	procSysRoot = root
	t.Cleanup(func() { procSysRoot = prev })
	write := func(key, val string) {
		path := procPath(key)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(val+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if v4 != "" {
		write(sysctlForwardV4, v4)
	}
	if v6 != "" {
		write(sysctlForwardV6, v6)
	}
	if rec != nil {
		inner := run
		run = func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := inner(ctx, name, args...)
			if err == nil && name == "sysctl" && len(args) == 2 && args[0] == "-w" {
				key, val, _ := strings.Cut(args[1], "=")
				write(key, val)
			}
			return out, err
		}
	}
	return root
}

func rtCommit(rec *recorder) *recorder {
	return rec.on("nft -c -f", "").on("systemctl daemon-reload", "").on("systemctl is-enabled", "disabled").on("systemctl enable", "")
}

func TestForwardingReadsBothFamiliesAndWhatNeedsThem(t *testing.T) {
	rtProc(t, nil, "1", "0")
	s := testService(t)
	sp := emptySpec()
	sp.Sysctls[sysctlForwardV4] = "1"
	sp.Forwards = []ForwardSpec{
		{ID: 1, Name: "web", Target: "10.0.1.5", Enabled: true},
		{ID: 2, Name: "off", Target: "10.0.1.6", Enabled: false},
		{ID: 3, Name: "v6web", Target: "2001:db8::5", Enabled: true},
	}
	sp.NAT = []NATSpec{
		{ID: 4, Name: "lan", Source: "192.168.50.0/24", Enabled: true},
		{ID: 5, Name: "wg0 exit", Source: "10.8.0.0/24", Owner: "wireguard:wg0", Enabled: true},
		{ID: 6, Name: "disabled", Source: "10.9.0.0/24", Enabled: false},
	}
	rtSaveSpec(t, s, sp)
	v := s.Forwarding(context.Background(), ForwardingNeeds{DockerNetworks: 3, TailscaleExitNode: true})
	if !v.IPv4.Available || !v.IPv4.Enabled || !v.IPv4.Persisted || v.IPv6.Enabled || v.IPv6.Persisted {
		t.Fatalf("view = %+v", v)
	}
	want4 := `3 Docker networks|Tailscale's exit node|the port forward "web"|the NAT entry "lan"|the WireGuard exit through wg0`
	if got := strings.Join(v.IPv4.NeededBy, "|"); got != want4 {
		t.Fatalf("ipv4 needed by %s, want %s", got, want4)
	}
	want6 := `Tailscale's exit node|the port forward "v6web"`
	if got := strings.Join(v.IPv6.NeededBy, "|"); got != want6 {
		t.Fatalf("ipv6 needed by %s, want %s", got, want6)
	}
	if !strings.Contains(v.IPv4.Guard, "3 Docker networks") {
		t.Fatalf("guard = %q", v.IPv4.Guard)
	}
}

func TestForwardingNothingNeedsItEmptyNotNull(t *testing.T) {
	rtProc(t, nil, "0", "")
	s := testService(t)
	v := s.Forwarding(context.Background(), ForwardingNeeds{})
	if v.IPv4.NeededBy == nil || v.IPv6.NeededBy == nil || v.IPv4.Guard != "" {
		t.Fatalf("view = %+v", v)
	}
	if v.IPv6.Available {
		t.Fatal("a host without IPv6 forwarding reads as having it")
	}
}

func TestForwardingTurningItOffIsRefusedWhileAnythingNeedsIt(t *testing.T) {
	cases := []struct {
		name   string
		family string
		needs  ForwardingNeeds
		spec   func(*Spec)
		want   string
	}{
		{"Docker's networks", "ipv4", ForwardingNeeds{DockerNetworks: 2}, nil, "2 Docker networks"},
		{"one Docker network", "ipv4", ForwardingNeeds{DockerNetworks: 1}, nil, "1 Docker network would"},
		{"Tailscale's exit node", "ipv4", ForwardingNeeds{TailscaleExitNode: true}, nil, "Tailscale's exit node"},
		{"Tailscale's subnet routes on IPv6", "ipv6", ForwardingNeeds{TailscaleSubnetRoutes: true}, nil, "Tailscale's subnet routes"},
		{"a NAT entry", "ipv4", ForwardingNeeds{}, func(sp *Spec) {
			sp.NAT = []NATSpec{{ID: 1, Name: "lan", Source: "192.168.50.0/24", Enabled: true}}
		}, `the NAT entry "lan"`},
		{"a WireGuard exit", "ipv4", ForwardingNeeds{}, func(sp *Spec) {
			sp.NAT = []NATSpec{{ID: 1, Name: "x", Source: "10.8.0.0/24", Owner: "wireguard:wg0", Enabled: true}}
		}, "the WireGuard exit through wg0"},
		{"a port forward", "ipv4", ForwardingNeeds{}, func(sp *Spec) {
			sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Target: "10.0.1.5", Enabled: true}}
		}, `the port forward "web"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := rtCommit(record(t))
			rtProc(t, rec, "1", "1")
			s := testService(t)
			if c.spec != nil {
				sp := emptySpec()
				c.spec(sp)
				rtSaveSpec(t, s, sp)
			}
			_, err := s.SetForwarding(context.Background(), c.family, false, c.needs, "ion")
			g := rtGuarded(t, err)
			if !strings.Contains(g.Reason, c.want) || !strings.Contains(g.Reason, "cannot be turned off") {
				t.Fatalf("reason = %q, want it to name %q", g.Reason, c.want)
			}
			if len(rtMutations(rec)) != 0 {
				t.Fatalf("a refused change ran %v", rtMutations(rec))
			}
		})
	}
}

func TestForwardingDisabledEntriesAndTheOtherFamilyDoNotBlock(t *testing.T) {
	rec := rtCommit(record(t)).on("sysctl -w", "")
	rtProc(t, rec, "1", "1")
	s := testService(t)
	sp := emptySpec()
	sp.NAT = []NATSpec{{ID: 1, Name: "lan", Source: "192.168.50.0/24", Enabled: false}}
	sp.Forwards = []ForwardSpec{{ID: 2, Name: "web", Target: "10.0.1.5", Enabled: true}}
	rtSaveSpec(t, s, sp)
	// The only live forward is IPv4; IPv6 may go off.
	v, err := s.SetForwarding(context.Background(), "ipv6", false, ForwardingNeeds{DockerNetworks: 4}, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if v.IPv6.Enabled || !v.IPv4.Enabled {
		t.Fatalf("view = %+v", v)
	}
}

func TestForwardingOffWritesTheSpecTheKernelAndTheBootFile(t *testing.T) {
	rec := rtCommit(record(t)).on("sysctl -w", "")
	rtProc(t, rec, "1", "1")
	s := testService(t)
	v, err := s.SetForwarding(context.Background(), "ipv4", false, ForwardingNeeds{}, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if v.IPv4.Enabled || !v.IPv4.Persisted {
		t.Fatalf("view = %+v", v)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "sysctl -w net.ipv4.ip_forward=0" {
		t.Fatalf("mutations = %v", got)
	}
	if got := rtLoad(t, s).Sysctls[sysctlForwardV4]; got != "0" {
		t.Fatalf("spec sysctl = %q", got)
	}
	if b, _ := os.ReadFile(s.paths.Sysctl); !strings.Contains(string(b), "net.ipv4.ip_forward = 0") {
		t.Fatalf("sysctl drop-in = %q", b)
	}
}

func TestForwardingOnWritesTheKey(t *testing.T) {
	rec := rtCommit(record(t)).on("sysctl -w", "").on("ip -j -6 route show default", `[{"dst":"default","gateway":"2001:db8::1","dev":"eth0","protocol":"static"}]`)
	rtProc(t, rec, "0", "0")
	s := testService(t)
	for _, c := range []struct{ family, cmd, key string }{
		{"ipv4", "sysctl -w net.ipv4.ip_forward=1", "net.ipv4.ip_forward"},
		{"ipv6", "sysctl -w net.ipv6.conf.all.forwarding=1", "net.ipv6.conf.all.forwarding"},
	} {
		if _, err := s.SetForwarding(context.Background(), c.family, true, ForwardingNeeds{}, "ion"); err != nil {
			t.Fatal(err)
		}
		if !rec.ran(c.cmd) {
			t.Fatalf("%s did not run: %v", c.cmd, rec.commands())
		}
		if rtLoad(t, s).Sysctls[c.key] != "1" {
			t.Fatalf("%s not in the spec", c.key)
		}
	}
}

func TestForwardingIPv6OnIsRefusedWhenTheDefaultRouteCameFromARouterAdvertisement(t *testing.T) {
	rec := rtCommit(record(t)).on("ip -j -6 route show default", `[{"dst":"default","gateway":"fe80::1","dev":"eth0","protocol":"ra"}]`)
	rtProc(t, rec, "1", "0")
	s := testService(t)
	_, err := s.SetForwarding(context.Background(), "ipv6", true, ForwardingNeeds{}, "ion")
	g := rtGuarded(t, err)
	if !strings.Contains(g.Reason, "router advertisement") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if len(rtMutations(rec)) != 0 {
		t.Fatalf("a refused change ran %v", rtMutations(rec))
	}
}

func TestForwardingPutsTheOldValueBackWhenTheKernelDoesNotTakeIt(t *testing.T) {
	// No write hook: `sysctl -w` "succeeds" and the file never changes, which
	// is what a read-only /proc/sys looks like from here.
	rec := rtCommit(record(t)).on("sysctl -w", "")
	rtProc(t, nil, "1", "1")
	s := testService(t)
	_, err := s.SetForwarding(context.Background(), "ipv4", false, ForwardingNeeds{}, "ion")
	if err == nil || !strings.Contains(err.Error(), "still reports") {
		t.Fatalf("err = %v", err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "sysctl -w net.ipv4.ip_forward=0|sysctl -w net.ipv4.ip_forward=1" {
		t.Fatalf("mutations = %v", got)
	}
	if rtSpecWritten(s) {
		t.Fatal("a rolled-back change reached the spec")
	}
}

func TestForwardingFailureOfSysctlLeavesNoTrace(t *testing.T) {
	rec := rtCommit(record(t)).fail("sysctl -w", "sysctl: permission denied")
	rtProc(t, nil, "1", "1")
	s := testService(t)
	if _, err := s.SetForwarding(context.Background(), "ipv4", false, ForwardingNeeds{}, "ion"); err == nil {
		t.Fatal("a failed sysctl was reported as done")
	}
	if rtSpecWritten(s) || rec.ran("systemctl") {
		t.Fatal("a failed change wrote files")
	}
}

func TestForwardingFamilyAndAvailability(t *testing.T) {
	rec := rtCommit(record(t))
	rtProc(t, rec, "1", "")
	s := testService(t)
	if _, err := s.SetForwarding(context.Background(), "ipx", true, ForwardingNeeds{}, "ion"); err == nil {
		t.Fatal("a family nobody has was accepted")
	}
	if _, err := s.SetForwarding(context.Background(), "ipv6", true, ForwardingNeeds{}, "ion"); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("a host without IPv6 forwarding: err = %v", err)
	}
	for in, want := range map[string]string{"ipv4": "ipv4", "IPv6": "ipv6", "inet": "ipv4", "inet6": "ipv6"} {
		if _, canon, err := forwardingKey(in); err != nil || canon != want {
			t.Errorf("forwardingKey(%q) = %q, %v", in, canon, err)
		}
	}
}

func TestForwardingEnsureWritesTheSpecForOtherSlices(t *testing.T) {
	sp := emptySpec()
	sp.Sysctls = nil
	ensureForwarding(sp, "ipv4")
	ensureForwarding(sp, "ipv6")
	ensureForwarding(sp, "nonsense")
	if len(sp.Sysctls) != 2 || sp.Sysctls[sysctlForwardV4] != "1" || sp.Sysctls[sysctlForwardV6] != "1" {
		t.Fatalf("sysctls = %v", sp.Sysctls)
	}
	if got := renderSysctl(sp); !strings.Contains(got, "net.ipv4.ip_forward = 1\n") || !strings.Contains(got, "net.ipv6.conf.all.forwarding = 1\n") {
		t.Fatalf("boot file = %q", got)
	}
}

func TestForwardingEnableRuntimeOnlyWhenOff(t *testing.T) {
	rec := record(t).on("sysctl -w", "")
	rtProc(t, rec, "0", "1")
	if err := enableForwarding(context.Background(), "ipv4"); err != nil {
		t.Fatal(err)
	}
	if err := enableForwarding(context.Background(), "ipv6"); err != nil {
		t.Fatal(err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "sysctl -w net.ipv4.ip_forward=1" {
		t.Fatalf("mutations = %v", got)
	}
}
