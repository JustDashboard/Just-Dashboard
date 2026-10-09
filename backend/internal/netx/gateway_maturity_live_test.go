package netx

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Live acceptance of the newer gateway and protection forms in namespaces the
// tests create and delete. Like the rest of the live tests, the Service's run
// is confined to `ip netns exec <namespace>` and to the gateway's own tools;
// the connection-table test runs a copy of this test binary as root inside
// its namespace, where netlink reaches only that namespace's table.

// gwLiveTopology is client(198.51.100.2) -- gwc [gateway] gws -- server(10.0.4.25):
// the gateway holds a second public address, 198.51.100.25, for a mapping.
func gwLiveTopology(t *testing.T) (gw, cl, sv string, svc *Service) {
	t.Helper()
	gw, cl, sv = gwLiveNS(t, "mgw"), gwLiveNS(t, "mcl"), gwLiveNS(t, "msv")
	gwMustInNS(t, cl, "ip", "link", "add", "c0", "type", "veth", "peer", "name", "gwc", "netns", gw)
	gwMustInNS(t, sv, "ip", "link", "add", "s0", "type", "veth", "peer", "name", "gws", "netns", gw)
	for _, c := range [][]string{
		{cl, "ip", "addr", "add", "198.51.100.2/24", "dev", "c0"}, {cl, "ip", "link", "set", "c0", "up"}, {cl, "ip", "link", "set", "lo", "up"},
		{sv, "ip", "addr", "add", "10.0.4.25/24", "dev", "s0"}, {sv, "ip", "link", "set", "s0", "up"}, {sv, "ip", "link", "set", "lo", "up"},
		{sv, "ip", "route", "add", "default", "via", "10.0.4.1"},
		{gw, "ip", "addr", "add", "198.51.100.1/24", "dev", "gwc"}, {gw, "ip", "addr", "add", "198.51.100.25/24", "dev", "gwc"},
		{gw, "ip", "link", "set", "gwc", "up"},
		{gw, "ip", "addr", "add", "10.0.4.1/24", "dev", "gws"}, {gw, "ip", "link", "set", "gws", "up"},
		{gw, "ip", "link", "set", "lo", "up"},
		{gw, "sysctl", "-w", "net.ipv4.ip_forward=1"},
		{gw, "iptables", "-P", "FORWARD", "DROP"},
	} {
		gwMustInNS(t, c[0], c[1:]...)
	}
	sys := t.TempDir()
	for _, p := range []string{"net/ipv4/ip_forward", "net/ipv6/conf/all/forwarding"} {
		full := filepath.Join(sys, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("1\n"), 0o644)
	}
	prevSys, prevClass, prevRun, prevHas := gatewaySysRoot, gatewayClassNet, run, has
	gatewaySysRoot, gatewayClassNet, run, has = sys, t.TempDir(), gwLiveRun(gw), func(string) bool { return false }
	t.Cleanup(func() { gatewaySysRoot, gatewayClassNet, run, has = prevSys, prevClass, prevRun, prevHas })
	return gw, cl, sv, testService(t)
}

func TestLiveOneToOneMappingTranslatesBothWays(t *testing.T) {
	gwLiveRequired(t)
	gw, cl, sv, svc := gwLiveTopology(t)
	ctx := context.Background()
	gwLiveServe(t, sv, "10.0.4.25", 80)
	gwLiveServe(t, cl, "198.51.100.2", 8000)
	if _, ok := gwFetchFrom(t, cl, "http://198.51.100.25/"); ok {
		t.Fatal("reached the private host before the mapping existed")
	}
	nat, err := svc.AddNAT(ctx, NATRequest{Name: "mail", Source: "10.0.4.25", Interface: "gwc", Mode: "one-to-one", Translated: "198.51.100.25"}, "", "test")
	if err != nil {
		t.Fatal(err)
	}
	// Inbound: the private host sees the visitor, admitted by the mark
	// through a DROP forward policy.
	if body, ok := gwFetchFrom(t, cl, "http://198.51.100.25/"); !ok || body != "198.51.100.2" {
		t.Fatalf("inbound through the mapping: %q %v", body, ok)
	}
	// Outbound: the visitor sees the mapped public address, not the
	// gateway's primary one.
	if body, ok := gwFetchFrom(t, sv, "http://198.51.100.2:8000/"); !ok || body != "198.51.100.25" {
		t.Fatalf("outbound through the mapping: %q %v", body, ok)
	}
	v, err := svc.Gateway(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nv := v.NAT[0]
	if nv.ID != nat.ID || nv.Mode != "one-to-one" || nv.InPackets == 0 || nv.Packets == 0 || nv.Readiness == nil || nv.Readiness.Policy != "installed" {
		t.Fatalf("view = %+v readiness %+v", nv, nv.Readiness)
	}
	if !strings.Contains(gwMustInNS(t, gw, "nft", "list", "chain", "inet", gatewayTable, "nat_pre"), "dnat ip to 10.0.4.25") {
		t.Fatal("the inbound half is not in the kernel")
	}
}

func TestLiveExceptionsExpireInTheKernelAndTotalsSurviveReloads(t *testing.T) {
	gwLiveRequired(t)
	_, cl, sv, svc := gwLiveTopology(t)
	ctx := context.Background()
	const operator = "192.0.2.1"
	gwLiveServe(t, sv, "10.0.4.25", 80)
	fwd, err := svc.AddForward(ctx, ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.4.25", TargetPort: "80", SourceNAT: "always"}, operator, "test", gwProtected)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gwFetchFrom(t, cl, "http://198.51.100.1:8080/"); !ok {
		t.Fatal("the forward does not carry traffic")
	}
	before, err := svc.Gateway(ctx)
	if err != nil {
		t.Fatal(err)
	}
	carried, generation := before.Forwards[0].Packets, before.Counters.Generation
	if carried == 0 || generation == 0 {
		t.Fatalf("nothing counted: %+v %+v", before.Forwards[0], before.Counters)
	}

	bl, err := svc.AddBlocklist(ctx, BlocklistRequest{Name: "visitors", Kind: "manual", Entries: []string{"198.51.100.0/24"}}, operator, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := gwFetchFrom(t, cl, "http://198.51.100.1:8080/"); ok {
		t.Fatal("a listed visitor got through")
	}
	// An exception for one list, ending a few seconds from now.
	until := time.Now().Add(6 * time.Second).UTC().Truncate(time.Second).Add(time.Second)
	if _, err := svc.AddException(ctx, ExceptionRequest{Address: "198.51.100.2", Scope: "blocklist:" + strconv.Itoa(bl.ID), Reason: "monitor", ExpiresAt: until.Format(time.RFC3339)}, "test"); err != nil {
		t.Fatal(err)
	}
	if body, ok := gwFetchFrom(t, cl, "http://198.51.100.1:8080/"); !ok || body == "" {
		t.Fatalf("the excepted visitor is still refused: %q %v", body, ok)
	}
	// The dashboard does nothing more: the kernel's clock ends it.
	time.Sleep(time.Until(until) + 1500*time.Millisecond)
	if _, ok := gwFetchFrom(t, cl, "http://198.51.100.1:8080/"); ok {
		t.Fatal("an expired exception still let the visitor through")
	}

	// Every save above replaced the table; the forward's total carried
	// across each replacement while the live counter restarted.
	after, err := svc.Gateway(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Counters.Generation == generation {
		t.Fatal("the table's handle did not change across replacements")
	}
	f := after.Forwards[0]
	if f.ID != fwd.ID || f.Total.Packets < carried || f.Total.Resets == 0 || f.Total.Packets < f.Packets {
		t.Fatalf("total %+v after carrying %d (live now %d)", f.Total, carried, f.Packets)
	}
	pv, err := svc.Protection(ctx, operator)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Exceptions) != 1 || !pv.Exceptions[0].Expired || pv.Exceptions[0].Packets == 0 {
		t.Fatalf("exception view = %+v", pv.Exceptions)
	}
}

func TestLiveGlobalCeilingRefusesPastItsCount(t *testing.T) {
	gwLiveRequired(t)
	_, cl, sv, svc := gwLiveTopology(t)
	ctx := context.Background()
	const operator = "192.0.2.1"
	gwLiveServe(t, sv, "10.0.4.25", 80)
	if _, err := svc.AddForward(ctx, ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.4.25", TargetPort: "80", SourceNAT: "never"}, operator, "test", gwProtected); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddLimit(ctx, LimitRequest{Name: "web", Protocol: "tcp", Ports: "8080", GlobalConnections: 3}, operator, "test"); err != nil {
		t.Fatal(err)
	}
	hold := filepath.Join(t.TempDir(), "hold.py")
	if err := os.WriteFile(hold, []byte(gwLiveHold), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := strings.TrimSpace(gwMustInNS(t, cl, "python3", hold, "198.51.100.1", "8080")); out != "3" {
		t.Fatalf("five connections against a ceiling of three for everyone: %s connected", out)
	}
	pv, err := svc.Protection(ctx, operator)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Limits[0].GlobalPackets == 0 {
		t.Fatalf("the ceiling's own counter is empty: %+v", pv.Limits[0])
	}
}

func TestLiveFlowModelReadsRealIptablesShapes(t *testing.T) {
	gwLiveRequired(t)
	gw := gwLiveNS(t, "mflow")
	for _, c := range [][]string{
		{"ip", "link", "add", "docker0", "type", "dummy"}, {"ip", "addr", "add", "172.17.0.1/16", "dev", "docker0"}, {"ip", "link", "set", "docker0", "up"},
		{"ip", "link", "add", "eth0", "type", "dummy"}, {"ip", "addr", "add", "203.0.113.20/24", "dev", "eth0"}, {"ip", "link", "set", "eth0", "up"},
		{"iptables", "-P", "FORWARD", "DROP"},
		{"iptables", "-t", "raw", "-A", "PREROUTING", "-d", "172.17.0.2/32", "!", "-i", "docker0", "-j", "DROP"},
		{"iptables", "-t", "nat", "-N", "DOCKER"},
		{"iptables", "-t", "nat", "-A", "PREROUTING", "-m", "addrtype", "--dst-type", "LOCAL", "-j", "DOCKER"},
		{"iptables", "-t", "nat", "-A", "DOCKER", "-i", "docker0", "-j", "RETURN"},
		{"iptables", "-t", "nat", "-A", "DOCKER", "!", "-i", "docker0", "-p", "tcp", "--dport", "8081", "-j", "DNAT", "--to-destination", "172.17.0.3:80"},
		{"iptables", "-t", "nat", "-A", "POSTROUTING", "-s", "172.17.0.0/16", "!", "-o", "docker0", "-j", "MASQUERADE"},
		{"iptables", "-t", "mangle", "-A", "PREROUTING", "-m", "conntrack", "--ctstate", "NEW", "-j", "CONNMARK", "--save-mark"},
	} {
		gwMustInNS(t, gw, c...)
	}
	sys := t.TempDir()
	_ = os.MkdirAll(filepath.Join(sys, "net/ipv4"), 0o755)
	_ = os.WriteFile(filepath.Join(sys, "net/ipv4/ip_forward"), []byte("1\n"), 0o644)
	prevSys, prevClass, prevRun, prevHas := gatewaySysRoot, gatewayClassNet, run, has
	gatewaySysRoot, gatewayClassNet, run, has = sys, t.TempDir(), gwLiveRun(gw), func(string) bool { return false }
	t.Cleanup(func() { gatewaySysRoot, gatewayClassNet, run, has = prevSys, prevClass, prevRun, prevHas })
	svc := testService(t)
	ctx := context.Background()
	c := svc.GatewayCapability(ctx)
	if c.Writable {
		t.Fatalf("the generic check passed conditional raw drops: %+v", c.Layers)
	}
	sp := gwFlowSpec(ForwardSpec{Name: "web", Ports: "8080", Target: "172.17.0.2", TargetPort: "80"})
	if _, err := svc.requireWritable(ctx, sp); err != nil {
		t.Fatalf("real Docker shapes against the modeled flow: %v", err)
	}
	gwMustInNS(t, gw, "iptables", "-t", "raw", "-A", "PREROUTING", "-p", "tcp", "--dport", "8080", "-j", "DROP")
	_, err := svc.requireWritable(ctx, sp)
	if err == nil || !strings.Contains(err.Error(), "raw") {
		t.Fatalf("an explicit drop of the public port must refuse: %v", err)
	}
}

// The connection-table client, inside a namespace. The parent creates two
// namespaces and runs this binary as root in the first; the helper serves a
// TCP connection to a client in the second, reads it back over netlink,
// revokes it through the Service with a blocklist loaded, and checks the
// client's next exchange stalls.
func TestLiveConntrackNetlinkInANamespace(t *testing.T) {
	gwLiveRequired(t)
	a, b := gwLiveNS(t, "cta"), gwLiveNS(t, "ctb")
	gwMustInNS(t, b, "ip", "link", "add", "b0", "type", "veth", "peer", "name", "a0", "netns", a)
	for _, c := range [][]string{
		{a, "ip", "addr", "add", "10.98.0.1/24", "dev", "a0"}, {a, "ip", "link", "set", "a0", "up"}, {a, "ip", "link", "set", "lo", "up"},
		{b, "ip", "addr", "add", "10.98.0.2/24", "dev", "b0"}, {b, "ip", "link", "set", "b0", "up"}, {b, "ip", "link", "set", "lo", "up"},
	} {
		gwMustInNS(t, c[0], c[1:]...)
	}
	work := t.TempDir()
	if err := os.Chmod(work, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := gwLiveCmd(ctx, "ip", "netns", "exec", a, "env", "JD_CT_HELPER=1", "JD_CT_PEER="+b, "JD_CT_WORK="+work,
		os.Args[0], "-test.run", "^TestLiveConntrackHelper$", "-test.v", "-test.count=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestLiveConntrackHelper") {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	t.Logf("helper:\n%s", out)
}

// TestLiveConntrackHelper runs only inside the namespace the test above made.
func TestLiveConntrackHelper(t *testing.T) {
	if os.Getenv("JD_CT_HELPER") != "1" || os.Geteuid() != 0 {
		t.Skip("runs as root inside the namespace TestLiveConntrackNetlinkInANamespace creates")
	}
	peer, work := os.Getenv("JD_CT_PEER"), os.Getenv("JD_CT_WORK")
	// This process is already in its namespace: tools run directly, never
	// through the host wrapper, and only the gateway's own.
	prevRun, prevHas := run, has
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		switch name {
		case "nft", "ip", "iptables", "ip6tables":
		default:
			return "", &UnavailableError{Tool: name}
		}
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("%s: %s", name, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}
	has = func(string) bool { return false }
	t.Cleanup(func() { run, has = prevRun, prevHas })

	// Connection tracking starts in a namespace once a rule needs it.
	if out, err := run(context.Background(), "nft", "-f", writeTemp(t, work, "ct.nft", "table inet cttest {\n\tchain in {\n\t\ttype filter hook input priority 0; policy accept;\n\t\tct state new counter\n\t}\n}\n")); err != nil {
		t.Fatalf("enabling conntrack: %v %s", err, out)
	}
	ln, err := net.Listen("tcp", "10.98.0.1:7000")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	served := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			served <- c
		}
	}()
	script := filepath.Join(work, "client.py")
	_ = os.WriteFile(script, []byte(`
import socket, sys, time
s = socket.create_connection(("10.98.0.1", 7000), timeout=3)
s.sendall(b"one\n"); print(s.recv(16).decode().strip(), flush=True)
while True:
    try:
        line = sys.stdin.readline()
        if not line: break
        s.sendall(b"two\n"); print(s.recv(16).decode().strip(), flush=True)
    except Exception as e:
        print("stalled", flush=True)
`), 0o644)
	client := exec.Command("ip", "netns", "exec", peer, "python3", script)
	stdin, _ := client.StdinPipe()
	stdout, _ := client.StdoutPipe()
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer client.Process.Kill()
	var conn net.Conn
	select {
	case conn = <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("the client did not connect")
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 16)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			_, _ = conn.Write(append([]byte("ack:"), buf[:n]...))
		}
	}()
	readLine := func() string {
		b := make([]byte, 64)
		_ = os.Stdout.Sync()
		n, _ := stdout.Read(b)
		return strings.TrimSpace(string(b[:n]))
	}
	if got := readLine(); got != "ack:one" {
		t.Fatalf("first exchange: %q", got)
	}

	ctx := context.Background()
	var found *ctEntry
	_, _, err = conntrackDump(ctx, 10000, func(e ctEntry) bool {
		if e.Src.String() == "10.98.0.2" && e.DPort == 7000 {
			c := e
			found = &c
		}
		return true
	})
	if err != nil || found == nil || found.state() != "ESTABLISHED" || !found.HasID {
		t.Fatalf("dump: %v %+v", err, found)
	}
	if _, err := conntrackStats(ctx); err != nil {
		t.Fatalf("stats: %v", err)
	}

	svc := testService(t, "192.0.2.0/24")
	sys := t.TempDir()
	prevSys := gatewaySysRoot
	gatewaySysRoot = sys
	t.Cleanup(func() { gatewaySysRoot = prevSys })
	bl, err := svc.AddBlocklist(ctx, BlocklistRequest{Name: "peer", Kind: "manual", Entries: []string{"10.98.0.2"}}, "192.0.2.1", "helper")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewSessions(ctx, SessionRequest{Network: "10.98.0.2/32", BlocklistID: bl.ID}, "192.0.2.1")
	if err != nil || preview.Connections.Tracked != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	// The established session survives the list on its own...
	_, _ = stdin.Write([]byte("\n"))
	if got := readLine(); got != "ack:two" {
		t.Fatalf("a list must not cut an open session by itself: %q", got)
	}
	// ...and stalls once it is explicitly ended.
	res, err := svc.RevokeSessions(ctx, SessionRequest{Network: "10.98.0.2/32", BlocklistID: bl.ID}, "192.0.2.1")
	if err != nil || res.Ended != 1 {
		t.Fatalf("revoke: %+v %v", res, err)
	}
	_, _ = stdin.Write([]byte("\n"))
	if got := readLine(); got != "stalled" {
		t.Fatalf("a revoked session still answered: %q", got)
	}
	still := false
	_, _, _ = conntrackDump(ctx, 10000, func(e ctEntry) bool {
		if e.Src.String() == "10.98.0.2" && e.DPort == 7000 && e.state() == "ESTABLISHED" {
			still = true
		}
		return true
	})
	if still {
		t.Fatal("the session's entry is back as established")
	}
}

func writeTemp(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLiveModesGoldenLoadsAndDriftReadsItAsOwned(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "mgolden")
	file, err := filepath.Abs("testdata/gateway-modes.nft")
	if err != nil {
		t.Fatal(err)
	}
	gwMustInNS(t, ns, "nft", "-c", "-f", file)
	gwMustInNS(t, ns, "nft", "-f", file)
	gwMustInNS(t, ns, "nft", "-f", file)
	prevRun := run
	run = gwLiveRun(ns)
	t.Cleanup(func() { run = prevRun })
	sp, _, _ := gwModesSpec()
	rendered, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	svc := testService(t)
	o := svc.driftGateway(context.Background(), sp, string(rendered), nil)[0]
	if o.Status == "drift" || o.Status == "missing" || o.Status == "unreadable" {
		t.Fatalf("the loaded modes table reads as %s: %s", o.Status, o.Reason)
	}
	live, err := parseLiveCounters(gwMustInNS(t, ns, "nft", "-t", "-j", "list", "table", "inet", gatewayTable))
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{"nat-in:1", "nat-in:2", "nat-in:3", "nat:4", "limit-global:5", "exception:8", "exception:9", "exception:10", "blocklist:6"} {
		if live.Rules[comment] == 0 {
			t.Errorf("no rule %q came back from the kernel", comment)
		}
	}
}
