package netx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These tests run the real `ip` against a real kernel, so they are behind
// JD_NETNS_LIVE=1 and never touch this host's own network: each builds its
// own network namespace, as a process nobody else can see, and sends every
// command into it through nsenter.
//
//	JD_NETNS_LIVE=1 go test ./internal/netx/ -run Live -v
//
// The namespace is held open by a `sleep` started under `unshare --net
// --mount`, with a private tmpfs on /run/netns so the `ip netns add` a batch
// does leaves nothing in the host's. Killing the process removes the
// namespace, its devices and its named namespaces with it. Needs root, or
// sudo without a password.

func liveRequired(t *testing.T) {
	t.Helper()
	if os.Getenv("JD_NETNS_LIVE") != "1" {
		t.Skip("set JD_NETNS_LIVE=1 to run against a throwaway network namespace")
	}
	if os.Geteuid() != 0 {
		if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
			t.Skip("needs root or passwordless sudo")
		}
	}
}

func liveSudo(args ...string) *exec.Cmd {
	if os.Geteuid() == 0 {
		return exec.Command(args[0], args[1:]...)
	}
	return exec.Command("sudo", append([]string{"-n"}, args...)...)
}

// liveNS is a throwaway network and mount namespace.
type liveNS struct {
	pid int
}

// newLiveNS starts the holder and returns once the namespace is ready. The
// holder prints its own pid first: sh, unshare and sleep are one process
// image after another, so the pid is the namespace's.
func newLiveNS(t *testing.T) *liveNS {
	t.Helper()
	script := `echo $$; exec unshare --net --mount --propagation private -- sh -c 'mkdir -p /run/netns && mount -t tmpfs tmpfs /run/netns && ip link set lo up && echo ready && exec sleep 900'`
	cmd := liveSudo("sh", "-c", script)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ns := &liveNS{}
	t.Cleanup(func() {
		if ns.pid > 0 {
			// The holder is root's; so is the kill.
			_ = liveSudo("kill", strconv.Itoa(ns.pid)).Run() // best effort: it exits by itself after 15 minutes
		}
		_ = cmd.Wait() // reaps the killed holder; its exit status is the signal
	})
	r := bufio.NewReader(out)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("holder did not start: %v %s", err, stderr.String())
	}
	if ns.pid, err = strconv.Atoi(strings.TrimSpace(line)); err != nil || ns.pid < 2 {
		t.Fatalf("holder pid %q", line)
	}
	if line, err = r.ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("holder said %q, %v: %s", line, err, stderr.String())
	}
	return ns
}

// command is the one place a command is aimed: always at the holder's
// namespaces. A command that could not be aimed would not be run at all.
func (ns *liveNS) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	full := append([]string{"nsenter", "--target", strconv.Itoa(ns.pid), "--net", "--mount", "--", name}, args...)
	if os.Geteuid() == 0 {
		return exec.CommandContext(ctx, full[0], full[1:]...)
	}
	return exec.CommandContext(ctx, "sudo", append([]string{"-n"}, full...)...)
}

func (ns *liveNS) run(ctx context.Context, stdin []byte, name string, args ...string) (string, error) {
	cmd := ns.command(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", &UnavailableError{Tool: name}
		}
		return buf.String(), fmt.Errorf("%s: %s", name, strings.TrimSpace(firstLines(buf.String(), 6)))
	}
	return buf.String(), nil
}

// must runs a command in the namespace and fails the test if it fails.
func (ns *liveNS) must(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := ns.run(context.Background(), nil, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return out
}

// useLive points the package's command hooks at a namespace. Nothing else in
// the package reaches the host while it is installed: run, runStdin and has
// are the only doors, and has says no to the tools whose absence makes the
// commit skip what only a booted host has (systemd).
func useLive(t *testing.T, ns *liveNS) {
	t.Helper()
	prevRun, prevStdin, prevHas := run, runStdin, has
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		return ns.run(ctx, nil, name, args...)
	}
	runStdin = func(ctx context.Context, stdin []byte, name string, args ...string) (string, error) {
		return ns.run(ctx, stdin, name, args...)
	}
	has = func(name string) bool { return name != "systemctl" }
	t.Cleanup(func() { run, runStdin, has = prevRun, prevStdin, prevHas })
}

// liveSnapshot is the namespace's devices, addresses, routes and rules as
// `ip` prints them, for assertions on what really exists.
type liveSnapshot struct {
	links, addrs, routes4, routes6, rules4, netns string
}

func (ns *liveNS) snapshot(t *testing.T) liveSnapshot {
	t.Helper()
	return liveSnapshot{
		links:   ns.must(t, "ip", "-j", "-d", "link", "show"),
		addrs:   ns.must(t, "ip", "-j", "addr", "show"),
		routes4: ns.must(t, "ip", "route", "show", "table", "all"),
		routes6: ns.must(t, "ip", "-6", "route", "show", "table", "all"),
		rules4:  ns.must(t, "ip", "rule", "show"),
		netns:   ns.must(t, "ip", "netns", "list"),
	}
}

func liveHas(t *testing.T, where, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("%s lacks %q:\n%s", where, n, haystack)
		}
	}
}

func liveLacks(t *testing.T, where, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			t.Errorf("%s still has %q:\n%s", where, n, haystack)
		}
	}
}

// TestLiveBootFileLoadsWithoutOneError runs the rendered batch file for every
// kind of entry through a real `ip -force -batch`, in a throwaway namespace,
// the way the boot unit does, and checks each thing it describes exists.
func TestLiveBootFileLoadsWithoutOneError(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	// What the provider's configuration would already have made.
	ns.must(t, "ip", "link", "add", "eth0", "type", "dummy")
	ns.must(t, "ip", "link", "set", "eth0", "up")
	ns.must(t, "ip", "addr", "add", "203.0.113.20/24", "dev", "eth0")

	sp := emptySpec()
	sp.Namespaces = []NamespaceSpec{{Name: "tenant-a"}}
	sp.Links = []LinkSpec{
		{Name: "jd-lan", Kind: "bridge", STP: true, MTU: 1450, Addresses: []string{"192.168.50.1/24", "2001:db8:50::1/64"}, Up: true},
		{Name: "jd-d0", Kind: "dummy", Up: true},
		{Name: "jd-v100", Kind: "vlan", Parent: "jd-d0", VLANID: 100, Master: "jd-lan", Up: true},
		{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7", Local: "203.0.113.20", Parent: "eth0", Port: 4789, Up: true},
		{Name: "vxg", Kind: "vxlan", VNI: 43, Group: "239.1.1.1", Parent: "jd-d0", Port: 4789},
		{Name: "gre1", Kind: "gre", Local: "203.0.113.20", Remote: "198.51.100.8", TTL: 64, Key: 5, Addresses: []string{"10.200.0.1/30"}, Up: true},
		{Name: "gt1", Kind: "gretap", Remote: "198.51.100.9"},
		{Name: "g61", Kind: "ip6gre", Local: "2001:db8::20", Remote: "2001:db8::2", TTL: 64},
		{Name: "g6t", Kind: "ip6gretap", Remote: "2001:db8::3"},
		{Name: "mv0", Kind: "macvlan", Parent: "eth0", Mode: "bridge", Up: true},
		{Name: "jd-v0", Kind: "veth", Peer: "jd-v1", PeerNamespace: "tenant-a", Master: "jd-lan", MTU: 1400, Up: true},
		{Name: "pair0", Kind: "veth", Peer: "pair1", Addresses: []string{"10.201.0.1/30"}, Up: true},
	}
	sp.Addresses = []AddressSpec{
		{ID: 1, Link: "jd-v1", CIDR: "192.168.50.2/24"},
		{ID: 2, Link: "eth0", CIDR: "203.0.113.50/24"},
	}
	sp.Routes = []RouteSpec{
		{ID: 3, Family: "inet", Destination: "172.16.9.0/24", Type: "unicast", Gateway: "192.168.50.2", Device: "jd-lan", Table: 254, Metric: 50},
		{ID: 4, Family: "inet", Destination: "default", Type: "unicast", Gateway: "203.0.113.1", Device: "eth0", Table: 200, Source: "203.0.113.50"},
		{ID: 5, Family: "inet6", Destination: "2001:db8:88::/64", Type: "unicast", Gateway: "2001:db8:50::2", Table: 100},
		{ID: 6, Family: "inet6", Destination: "default", Type: "unicast", Device: "jd-d0", Table: 200},
		{ID: 7, Family: "inet", Destination: "198.51.100.0/24", Type: "blackhole", Table: 254},
		{ID: 8, Family: "inet6", Destination: "2001:db8:98::/64", Type: "unreachable", Table: 254, Metric: 10},
	}
	sp.Rules = []RuleSpec{
		{ID: 9, Family: "inet", Priority: 10000, From: "192.168.50.0/24", Action: "lookup", Table: 200},
		{ID: 10, Family: "inet", Priority: 10001, To: "198.51.100.0/24", IIF: "jd-lan", Action: "blackhole"},
		{ID: 11, Family: "inet", Priority: 10002, FWMark: "0x10/0xff", OIF: "eth0", Action: "lookup", Table: 100},
		{ID: 12, Family: "inet", Priority: 10003, From: "10.9.0.0/16", Action: "prohibit"},
		{ID: 13, Family: "inet6", Priority: 10004, From: "2001:db8:88::/64", Action: "lookup", Table: 100},
	}
	file := filepath.Join(t.TempDir(), "links.batch")
	if err := os.WriteFile(file, []byte(renderLinks(sp)), 0o644); err != nil {
		t.Fatal(err)
	}
	// The same invocation the boot unit makes. A line that fails prints an
	// error and makes the exit status 1, so a clean run means every line took.
	if out, err := ns.run(context.Background(), nil, "ip", "-force", "-batch", file); err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("ip -force -batch: %v\n%s", err, out)
	}
	// Running it a second time is what happens when the unit is started over
	// a network that is already up: every line reports "exists" and none
	// breaks the ones after it.
	if out, err := ns.run(context.Background(), nil, "ip", "-force", "-batch", file); err == nil {
		t.Fatalf("a second run reported no errors, which a network already made cannot: %s", out)
	} else if !strings.Contains(out, "exist") {
		t.Fatalf("second run: %v\n%s", err, out)
	}

	snap := ns.snapshot(t)
	for _, name := range []string{"jd-lan", "jd-d0", "jd-v100", "vx42", "vxg", "gre1", "gt1", "g61", "g6t", "mv0", "jd-v0", "pair0", "pair1"} {
		liveHas(t, "links", snap.links, `"ifname":"`+name+`"`)
	}
	liveHas(t, "netns", snap.netns, "tenant-a")
	liveHas(t, "addresses", snap.addrs, "192.168.50.1", "2001:db8:50::1", "10.200.0.1", "10.201.0.1", "203.0.113.50")
	liveHas(t, "ipv4 routes", snap.routes4,
		"172.16.9.0/24 via 192.168.50.2 dev jd-lan metric 50",
		"default via 203.0.113.1 dev eth0 table 200",
		"blackhole 198.51.100.0/24")
	liveHas(t, "ipv6 routes", snap.routes6,
		"2001:db8:88::/64 via 2001:db8:50::2 dev jd-lan table 100",
		"default dev jd-d0 table 200",
		"unreachable 2001:db8:98::/64")
	liveHas(t, "rules", snap.rules4,
		"10000:\tfrom 192.168.50.0/24 lookup 200",
		"10001:\tfrom all to 198.51.100.0/24 iif jd-lan blackhole",
		"10002:\tfrom all fwmark 0x10/0xff oif eth0 lookup 100",
		"10003:\tfrom 10.9.0.0/16 prohibit")
	// The far end of the veth is inside the namespace, up, and addressed.
	peer := ns.must(t, "ip", "-n", "tenant-a", "-j", "addr", "show")
	liveHas(t, "tenant-a", peer, `"ifname":"jd-v1"`, "192.168.50.2")
	liveHas(t, "tenant-a", ns.must(t, "ip", "-n", "tenant-a", "link", "show", "jd-v1"), "mtu 1400", "UP")
	liveHas(t, "bridge", ns.must(t, "ip", "-d", "link", "show", "jd-lan"), "stp_state 1", "mtu 1450")
	liveHas(t, "bridge ports", ns.must(t, "ip", "link", "show", "master", "jd-lan"), "jd-v100", "jd-v0")
	liveHas(t, "vxlan", ns.must(t, "ip", "-d", "link", "show", "vx42"), "vxlan id 42", "remote 198.51.100.7", "dstport 4789")

	// And what `Routing` reads back of it, against the real output shapes.
	useLive(t, ns)
	rtTableNames(t)
	s := testService(t)
	rtSaveSpec(t, s, sp)
	view, err := s.Routing(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	managedRoutes, managedRules := map[int]bool{}, map[int]bool{}
	for _, tb := range view.Tables {
		for _, r := range tb.Routes {
			if r.Managed {
				managedRoutes[r.ID] = true
			}
		}
	}
	for _, r := range view.Rules {
		if r.Managed {
			managedRules[r.ID] = true
		}
	}
	for _, id := range []int{3, 4, 5, 6, 7, 8} {
		if !managedRoutes[id] {
			t.Errorf("route %d was not recognised in the kernel's own output", id)
		}
	}
	for _, id := range []int{9, 10, 11, 12} {
		if !managedRules[id] {
			t.Errorf("rule %d was not recognised in the kernel's own output", id)
		}
	}
	if managedRules[13] {
		t.Error("the IPv6 rule, which the boot file cannot hold, reads as live")
	}
}

// TestLiveMutationsAgainstARealKernel drives the service's own changes —
// devices, a namespace, addresses, routes and rules, and the refusals — in a
// throwaway namespace, then loads the boot file they wrote into a second one
// and checks it rebuilds the same network.
func TestLiveMutationsAgainstARealKernel(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	useLive(t, ns)
	rtTableNames(t)
	ctx := context.Background()
	// The provider's configuration: a connected network the browser is on,
	// and a default route out of it.
	ns.must(t, "ip", "link", "add", "up0", "type", "dummy")
	ns.must(t, "ip", "addr", "add", "10.99.0.1/24", "dev", "up0")
	ns.must(t, "ip", "link", "set", "up0", "up")
	ns.must(t, "ip", "route", "add", "default", "via", "10.99.0.254", "dev", "up0")
	const client = "10.99.0.50"

	s := testService(t)
	mk := func(req LinkRequest) {
		t.Helper()
		if _, err := s.CreateLink(ctx, req, client, "live"); err != nil {
			t.Fatalf("create %s %s: %v", req.Kind, req.Name, err)
		}
	}
	mk(LinkRequest{Name: "jdbr0", Kind: "bridge", Addresses: []string{"10.77.0.1/24", "2001:db8:77::1/64"}, MTU: 1450, Up: true})
	mk(LinkRequest{Name: "jdd0", Kind: "dummy", Up: true})
	mk(LinkRequest{Name: "jdd0.100", Kind: "vlan", Parent: "jdd0", VLANID: 100, Master: "jdbr0", Up: true})
	mk(LinkRequest{Name: "jdvx", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7", Local: "10.99.0.1", Parent: "up0", Up: true})
	mk(LinkRequest{Name: "jdgre", Kind: "gre", Local: "10.99.0.1", Remote: "198.51.100.8", TTL: 64, Key: 5, Addresses: []string{"10.200.0.1/30"}, Up: true})
	mk(LinkRequest{Name: "jdmv", Kind: "macvlan", Parent: "jdd0", Mode: "private", Up: true})
	mk(LinkRequest{Name: "jdp0", Kind: "veth", Peer: "jdp1", Addresses: []string{"10.201.0.1/30"}, Up: true})

	if _, err := s.CreateNamespace(ctx, NamespaceRequest{Name: "jdns", Veth: &VethRequest{
		HostName: "jdv0", PeerName: "eth0", Bridge: "jdbr0", PeerAddress: "10.77.0.2/24",
	}}, client, "live"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	snap := ns.snapshot(t)
	liveHas(t, "netns", snap.netns, "jdns")
	liveHas(t, "namespace devices", ns.must(t, "ip", "-n", "jdns", "-j", "addr", "show"), `"ifname":"eth0"`, "10.77.0.2")
	namespaces, err := s.Namespaces(ctx, Inventory{})
	if err != nil || len(namespaces) != 1 || !namespaces[0].Managed || len(namespaces[0].Devices) != 1 || namespaces[0].Devices[0].Addresses[0] != "10.77.0.2/24" {
		t.Fatalf("Namespaces() = %+v, %v", namespaces, err)
	}

	// Addresses: onto a managed device, and onto the provider's.
	if _, err := s.AddAddress(ctx, "jdd0", "10.66.0.1/24", client, "live"); err != nil {
		t.Fatal(err)
	}
	if change, err := s.AddAddress(ctx, "up0", "10.99.0.9/24", client, "live"); err != nil || !change.Persisted {
		t.Fatalf("address on an unmanaged device: %+v, %v", change, err)
	}
	if _, err := s.SetLinkMTU(ctx, "jdd0", 1400, client, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetLinkState(ctx, "jdd0", false, client, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetLinkState(ctx, "jdd0", true, client, "live"); err != nil {
		t.Fatal(err)
	}

	// Routes and rules.
	route4, err := s.AddRoute(ctx, RouteRequest{Destination: "10.88.0.0/24", Gateway: "10.77.0.2", Device: "jdbr0", Table: 100, Metric: 50}, client, "live")
	if err != nil {
		t.Fatal(err)
	}
	route6, err := s.AddRoute(ctx, RouteRequest{Destination: "2001:db8:88::/64", Gateway: "2001:db8:77::2", Table: 100}, client, "live")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRoute(ctx, RouteRequest{Destination: "::/0", Device: "jdd0", Table: 200}, client, "live"); err != nil {
		t.Fatal(err)
	}
	hole, err := s.AddRoute(ctx, RouteRequest{Destination: "198.51.100.0/24", Type: "blackhole"}, client, "live")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.AddRule(ctx, RuleRequest{From: "10.77.0.0/24", Table: 100}, client, "live")
	if err != nil || rule.Priority != 10000 {
		t.Fatalf("rule = %+v, %v", rule, err)
	}
	if _, err := s.AddRule(ctx, RuleRequest{FWMark: "0x10", Table: 100}, client, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRule(ctx, RuleRequest{To: "198.51.100.0/24", IIF: "jdbr0", Action: "blackhole"}, client, "live"); err != nil {
		t.Fatal(err)
	}
	snap = ns.snapshot(t)
	liveHas(t, "routes", snap.routes4, "10.88.0.0/24 via 10.77.0.2 dev jdbr0 table 100 metric 50", "blackhole 198.51.100.0/24")
	liveHas(t, "ipv6 routes", snap.routes6, "2001:db8:88::/64 via 2001:db8:77::2 dev jdbr0 table 100", "default dev jdd0 table 200")
	liveHas(t, "rules", snap.rules4, "10000:\tfrom 10.77.0.0/24 lookup 100", "10001:\tfrom all fwmark 0x10 lookup 100", "10002:\tfrom all to 198.51.100.0/24 iif jdbr0 blackhole")

	view, err := s.Routing(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if view.ClientPath.Device != "up0" || view.ClientPath.Source != "10.99.0.1" {
		t.Fatalf("client path = %+v", view.ClientPath)
	}
	found := 0
	for _, tb := range view.Tables {
		for _, r := range tb.Routes {
			if r.Managed && (r.ID == route4.ID || r.ID == route6.ID || r.ID == hole.ID) {
				found++
			}
		}
	}
	if found != 3 {
		t.Fatalf("Routing() recognised %d of the 3 routes just made", found)
	}

	// The refusals, against the kernel's own answers.
	t.Run("refusals", func(t *testing.T) {
		before := ns.snapshot(t)
		refused := func(name string, err error) {
			t.Helper()
			if _, ok := err.(*GuardError); !ok {
				t.Errorf("%s: err = %v, want a guard refusal", name, err)
			}
		}
		_, err := s.AddRoute(ctx, RouteRequest{Destination: "default", Gateway: "10.99.0.253", Device: "up0"}, client, "live")
		refused("a second default route in main", err)
		_, err = s.AddRoute(ctx, RouteRequest{Destination: "10.99.0.0/25", Device: "jdd0"}, client, "live")
		refused("a more specific route that takes the client's reply elsewhere", err)
		_, err = s.AddRule(ctx, RuleRequest{Table: 100}, client, "live")
		refused("a rule with no selector", err)
		_, err = s.AddRule(ctx, RuleRequest{To: "10.99.0.50/32", Action: "prohibit"}, client, "live")
		refused("a prohibit of the client", err)
		_, err = s.SetLinkState(ctx, "up0", false, client, "live")
		refused("the client path down", err)
		_, err = s.SetLinkMTU(ctx, "up0", 1200, client, "live")
		refused("the client path below 1280", err)
		_, err = s.SetLinkMaster(ctx, "up0", "jdbr0", client, "live")
		refused("the client path into a bridge", err)
		_, err = s.SetLinkMaster(ctx, "jdp0", "jdbr0", client, "live")
		refused("an addressed device into a bridge", err)
		// The provider's device is not the dashboard's to delete, which is
		// refused before the client-path guard is reached.
		if err := s.DeleteLink(ctx, "up0", client, "live"); !errors.Is(err, ErrNotManaged) {
			t.Errorf("deleting the provider's device: err = %v", err)
		}
		_, err = s.AddRoute(ctx, RouteRequest{Destination: "10.9.0.0/24", Device: "up0", Table: 52}, client, "live")
		refused("Tailscale's table", err)
		_, err = s.RemoveAddress(ctx, "up0", "10.99.0.1/24", client, "live")
		if err == nil {
			t.Error("the client's own address was removed")
		}
		// A rule on the server's own source, to a table that would answer
		// from another device, is the case a plain `route get` misses.
		if _, err := s.AddRoute(ctx, RouteRequest{Destination: "10.99.0.0/24", Device: "jdd0", Table: 300}, client, "live"); err != nil {
			t.Fatal(err)
		}
		_, err = s.AddRule(ctx, RuleRequest{From: "10.99.0.1/32", Table: 300}, client, "live")
		refused("a rule moving the replies sent from the server's address", err)
		if _, err := s.AddRoute(ctx, RouteRequest{Destination: "10.99.0.0/24", Device: "jdd0", Table: 300}, client, "live"); err == nil {
			t.Error("a duplicate route was accepted")
		}
		after := ns.snapshot(t)
		// Table 300 was added on purpose; nothing else may have changed.
		liveLacks(t, "rules after the refusals", after.rules4, "10.99.0.1", "prohibit")
		liveLacks(t, "routes after the refusals", after.routes4, "10.99.0.0/25", "10.99.0.253")
		if after.rules4 != before.rules4 {
			t.Errorf("rules changed:\n%s\n--\n%s", before.rules4, after.rules4)
		}
		if got := ns.must(t, "ip", "-j", "-d", "link", "show", "up0"); !strings.Contains(got, `"mtu":1500`) {
			t.Errorf("up0's MTU changed: %s", got)
		}
	})

	// What the changes wrote is what a reboot rebuilds: a second namespace
	// with only what the provider would have made loads the boot file.
	boot, err := os.ReadFile(filepath.Join(s.paths.Dir, linksFile))
	if err != nil {
		t.Fatal(err)
	}
	second := newLiveNS(t)
	second.must(t, "ip", "link", "add", "up0", "type", "dummy")
	second.must(t, "ip", "addr", "add", "10.99.0.1/24", "dev", "up0")
	second.must(t, "ip", "link", "set", "up0", "up")
	file := filepath.Join(t.TempDir(), "links.batch")
	if err := os.WriteFile(file, boot, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := second.run(ctx, nil, "ip", "-force", "-batch", file); err != nil || strings.TrimSpace(out) != "" {
		t.Fatalf("the boot file the changes wrote did not load cleanly: %v\n%s\n%s", err, out, boot)
	}
	rebuilt := second.snapshot(t)
	liveHas(t, "rebuilt links", rebuilt.links, `"ifname":"jdbr0"`, `"ifname":"jdd0.100"`, `"ifname":"jdvx"`, `"ifname":"jdgre"`, `"ifname":"jdmv"`, `"ifname":"jdp1"`, `"ifname":"jdv0"`)
	liveHas(t, "rebuilt netns", rebuilt.netns, "jdns")
	liveHas(t, "rebuilt addresses", rebuilt.addrs, "10.77.0.1", "10.66.0.1", "10.99.0.9", "10.200.0.1")
	liveHas(t, "rebuilt namespace", second.must(t, "ip", "-n", "jdns", "-j", "addr", "show"), "10.77.0.2")
	liveHas(t, "rebuilt routes", rebuilt.routes4, "10.88.0.0/24 via 10.77.0.2 dev jdbr0 table 100 metric 50", "blackhole 198.51.100.0/24")
	liveHas(t, "rebuilt rules", rebuilt.rules4, "from 10.77.0.0/24 lookup 100", "fwmark 0x10 lookup 100", "iif jdbr0 blackhole")
	if got := ns.must(t, "ip", "-j", "-d", "link", "show", "jdbr0"); !strings.Contains(got, `"mtu":1450`) {
		t.Errorf("jdbr0's MTU: %s", got)
	}
	liveHas(t, "rebuilt bridge", second.must(t, "ip", "-d", "link", "show", "jdbr0"), "mtu 1450")

	// Removals take it all back out.
	if err := s.DeleteRule(ctx, rule.ID, client, "live"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRoute(ctx, route4.ID, client, "live"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRoute(ctx, route6.ID, client, "live"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNamespace(ctx, "jdns", client, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveAddress(ctx, "up0", "10.99.0.9/24", client, "live"); err != nil {
		t.Fatal(err)
	}
	snap = ns.snapshot(t)
	liveLacks(t, "after the removals", snap.routes4, "10.88.0.0/24")
	liveLacks(t, "after the removals", snap.routes6, "2001:db8:88::/64")
	liveLacks(t, "after the removals", snap.rules4, "10.77.0.0/24")
	liveLacks(t, "after the removals", snap.netns, "jdns")
	liveLacks(t, "after the removals", snap.links, `"ifname":"jdv0"`)
	liveLacks(t, "after the removals", snap.addrs, "10.99.0.9")
	// Devices go once nothing the dashboard made refers to them: a route or a
	// rule naming a device keeps it.
	for _, name := range []string{"jdp0", "jdmv", "jdgre", "jdvx", "jdd0.100"} {
		if err := s.DeleteLink(ctx, name, client, "live"); err != nil {
			t.Fatalf("delete %s: %v", name, err)
		}
	}
	if err := s.DeleteLink(ctx, "jdd0", client, "live"); err == nil || !strings.Contains(err.Error(), "remove that first") {
		t.Errorf("a device a managed route leaves through was deleted: %v", err)
	}
	sp, err := s.loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range sp.Routes {
		if err := s.DeleteRoute(ctx, r.ID, client, "live"); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range sp.Rules {
		if err := s.DeleteRule(ctx, r.ID, client, "live"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"jdd0", "jdbr0"} {
		if err := s.DeleteLink(ctx, name, client, "live"); err != nil {
			t.Fatalf("delete %s: %v", name, err)
		}
	}
	sp, err = s.loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	// Only the address the dashboard put on the provider's device is left,
	// and it was removed above.
	if n := len(sp.Links) + len(sp.Routes) + len(sp.Rules) + len(sp.Namespaces) + len(sp.Addresses); n != 0 {
		t.Fatalf("the spec still holds %d entries: %+v", n, sp)
	}
	liveLacks(t, "after everything", ns.snapshot(t).links, `"ifname":"jd`)
	if b, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile)); string(b) != generatedHeader {
		t.Fatalf("the boot file after everything was removed:\n%s", b)
	}
}
