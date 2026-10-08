package netx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The live tests run what the module renders and applies against a real
// kernel, inside network namespaces the test creates and deletes itself. They
// are behind JD_NETNS_LIVE=1 because they need root (or sudo without a
// password) and the nft, tc and iptables binaries.
//
// This server is somebody's production machine. Every command the tests run
// goes through `ip netns exec <namespace>`, where nftables, iptables and tc
// have state of their own; the Service under test has its run variable
// replaced by gwLiveRun, which refuses any program that is not one of those and
// runs the rest only inside the namespace. Nothing here can change the host's
// own network.

func gwLiveRequired(t *testing.T) {
	t.Helper()
	if os.Getenv("JD_NETNS_LIVE") != "1" {
		t.Skip("set JD_NETNS_LIVE=1 to run against a throwaway network namespace")
	}
	for _, tool := range []string{"nft", "tc", "ip", "iptables", "python3", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	if os.Geteuid() != 0 {
		if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
			t.Skip("needs root or passwordless sudo")
		}
	}
}

func gwLiveCmd(ctx context.Context, args ...string) *exec.Cmd {
	if os.Geteuid() != 0 {
		args = append([]string{"sudo", "-n"}, args...)
	}
	return exec.CommandContext(ctx, args[0], args[1:]...)
}

// gwLiveHost runs a command on the host for the namespace's own setup and
// teardown only: `ip netns add|del|pids` and the kill of what ran inside.
func gwLiveHost(t *testing.T, args ...string) string {
	t.Helper()
	out, err := gwLiveCmd(context.Background(), args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// gwLiveNS creates a namespace and deletes it, and what runs in it, at the end
// of the test.
func gwLiveNS(t *testing.T, role string) string {
	t.Helper()
	ns := fmt.Sprintf("jdt%d-%s", os.Getpid(), role)
	gwLiveHost(t, "ip", "netns", "add", ns)
	t.Cleanup(func() {
		if out, err := gwLiveCmd(context.Background(), "ip", "netns", "pids", ns).Output(); err == nil {
			for _, pid := range strings.Fields(string(out)) {
				_ = gwLiveCmd(context.Background(), "kill", "-9", pid).Run() // a process of this test's own namespace
			}
		}
		_ = gwLiveCmd(context.Background(), "ip", "netns", "del", ns).Run() // the namespace may already be gone
	})
	return ns
}

// gwInNS runs a command inside a namespace.
func gwInNS(t *testing.T, ns string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := gwLiveCmd(ctx, append([]string{"ip", "netns", "exec", ns}, args...)...).CombinedOutput()
	return string(out), err
}

func gwMustInNS(t *testing.T, ns string, args ...string) string {
	t.Helper()
	out, err := gwInNS(t, ns, args...)
	if err != nil {
		t.Fatalf("[%s] %s: %v\n%s", ns, strings.Join(args, " "), err, out)
	}
	return out
}

// gwLiveRun is the Service's run, confined to a namespace and to the three
// tools the gateway uses.
func gwLiveRun(ns string) func(context.Context, string, ...string) (string, error) {
	return func(ctx context.Context, name string, args ...string) (string, error) {
		switch name {
		case "nft", "tc", "ip", "iptables", "ip6tables":
		default:
			// ufw, firewall-cmd, systemctl and sysctl are the host's, and
			// are not here.
			return "", &UnavailableError{Tool: name}
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var buf bytes.Buffer
		cmd := gwLiveCmd(ctx, append([]string{"ip", "netns", "exec", ns, name}, args...)...)
		cmd.Stdout, cmd.Stderr = &buf, &buf
		if err := cmd.Run(); err != nil {
			return buf.String(), fmt.Errorf("%s: %s", name, strings.TrimSpace(firstLines(buf.String(), 6)))
		}
		return buf.String(), nil
	}
}

const gwLiveEchoServer = `
import http.server, socketserver, sys
class S(http.server.ThreadingHTTPServer):
    # HTTPServer.server_bind reverse-resolves the address it bound, which
    # waits on a resolver that a namespace with a DROP policy cannot reach.
    def server_bind(self):
        socketserver.TCPServer.server_bind(self)
        self.server_name, self.server_port = sys.argv[1], self.server_address[1]
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = (self.client_address[0] + (sys.argv[3] if len(sys.argv) > 3 else "")).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a):
        pass
S((sys.argv[1], int(sys.argv[2])), H).serve_forever()
`

const gwLiveHold = `
import socket, sys
ok = 0
keep = []
for i in range(5):
    s = socket.socket()
    s.settimeout(1.5)
    try:
        s.connect((sys.argv[1], int(sys.argv[2])))
        keep.append(s)
        ok += 1
    except Exception:
        pass
print(ok)
`

const gwLiveLate = `
import socket, sys, time, os
s = socket.create_connection((sys.argv[1], int(sys.argv[2])), timeout=15)
print("connected", flush=True)
while not os.path.exists(sys.argv[3]):
    time.sleep(0.05)
s.sendall(b"GET / HTTP/1.0\r\nHost: x\r\n\r\n")
data = b""
while True:
    chunk = s.recv(4096)
    if not chunk:
        break
    data += chunk
print(data.decode().split("\r\n\r\n", 1)[-1], flush=True)
`

// gwLiveServe starts the echo server, which answers every request with the
// address it saw the request come from, and the marker if there is one, in a
// namespace.
func gwLiveServe(t *testing.T, ns, addr string, port int, marker ...string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "echo.py")
	if err := os.WriteFile(script, []byte(gwLiveEchoServer), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := gwLiveCmd(context.Background(), append([]string{"ip", "netns", "exec", ns}, append([]string{"timeout", "300", "python3", script, addr, fmt.Sprint(port)}, marker...)...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() }) // the namespace cleanup kills what sudo leaves
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if out, _ := gwInNS(t, ns, "ss", "-ltn"); strings.Contains(out, fmt.Sprintf("%s:%d", addr, port)) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	var why string
	select {
	case err := <-exited:
		why = fmt.Sprintf("it exited: %v", err)
	default:
		why = "it is still running"
	}
	ss, _ := gwInNS(t, ns, "ss", "-ltnp")
	br, _ := gwInNS(t, ns, "ip", "-br", "addr")
	ss += br
	t.Fatalf("the echo server did not start in %s (%s): %s\n%s", ns, why, stderr.String(), ss)
}

// gwFetchFrom is what a curl in a namespace gets, and whether it got anything.
func gwFetchFrom(t *testing.T, ns, url string) (string, bool) {
	t.Helper()
	out, err := gwInNS(t, ns, "curl", "-s", "-m", "3", url)
	return strings.TrimSpace(out), err == nil
}

func TestLiveGoldenRulesetLoadsAndReadsBack(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "golden")
	file, err := filepath.Abs("testdata/gateway-full.nft")
	if err != nil {
		t.Fatal(err)
	}
	gwMustInNS(t, ns, "nft", "-c", "-f", file)
	gwMustInNS(t, ns, "nft", "-f", file)
	gwMustInNS(t, ns, "nft", "-f", file) // the declare/delete/define idiom: a second load replaces, it does not fail
	out := gwMustInNS(t, ns, "nft", "-j", "list", "table", "inet", gatewayTable)
	counters, err := parseGatewayCounters(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{
		"forward:1", "forward:2", "forward:3", "forward-nat:1", "forward-nat:3", "nat:5", "nat:6", "nat:7",
		"limit:8", "limit:9", "limit:10", "blocklist:12", "blocklist:13",
	} {
		if _, ok := counters[comment]; !ok {
			t.Errorf("no rule with the comment %q came back from nft", comment)
		}
	}
	// A rule that is in the golden file and was disabled must be absent.
	for _, comment := range []string{"forward:4", "limit:11", "blocklist:14"} {
		if _, ok := counters[comment]; ok {
			t.Errorf("a disabled entry's rule %q was loaded", comment)
		}
	}
	// The Capability check reads the same listing a real ruleset produces.
	listing := gwMustInNS(t, ns, "nft", "-j", "list", "ruleset")
	var l nftListing
	if err := json.Unmarshal([]byte(listing), &l); err != nil || len(l.Nftables) == 0 {
		t.Fatalf("nft -t -j list ruleset did not parse: %v", err)
	}
}

func TestLiveTheFullGatewayThroughTheRealServiceCode(t *testing.T) {
	gwLiveRequired(t)
	gw, cl, sv := gwLiveNS(t, "gw"), gwLiveNS(t, "client"), gwLiveNS(t, "server")

	// client(10.77.0.2) -- gwc(10.77.0.1) [gateway] gws(10.88.0.1) -- server(10.88.0.5)
	gwMustInNS(t, cl, "ip", "link", "add", "c0", "type", "veth", "peer", "name", "gwc", "netns", gw)
	gwMustInNS(t, sv, "ip", "link", "add", "s0", "type", "veth", "peer", "name", "gws", "netns", gw)
	for _, c := range [][]string{
		{cl, "ip", "addr", "add", "10.77.0.2/24", "dev", "c0"}, {cl, "ip", "link", "set", "c0", "up"}, {cl, "ip", "link", "set", "lo", "up"},
		{cl, "ip", "route", "add", "default", "via", "10.77.0.1"},
		{sv, "ip", "addr", "add", "10.88.0.5/24", "dev", "s0"}, {sv, "ip", "link", "set", "s0", "up"}, {sv, "ip", "link", "set", "lo", "up"},
		{sv, "ip", "route", "add", "default", "via", "10.88.0.1"},
		{gw, "ip", "addr", "add", "10.77.0.1/24", "dev", "gwc"}, {gw, "ip", "link", "set", "gwc", "up"},
		{gw, "ip", "addr", "add", "10.88.0.1/24", "dev", "gws"}, {gw, "ip", "link", "set", "gws", "up"},
		{gw, "ip", "link", "set", "lo", "up"},
		{gw, "sysctl", "-w", "net.ipv4.ip_forward=1"},
		// The host filter that makes admission necessary: forwarded traffic
		// is dropped unless something accepts it.
		{gw, "iptables", "-P", "FORWARD", "DROP"},
		// And a host that accepts nothing for itself it was not asked to.
		{gw, "iptables", "-A", "INPUT", "-i", "lo", "-j", "ACCEPT"},
		{gw, "iptables", "-A", "INPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"},
		{gw, "iptables", "-P", "INPUT", "DROP"},
		// An ordinary firewall rule that lets clients reach one port on the
		// server directly, replies included: traffic merely passing through.
		{gw, "iptables", "-I", "FORWARD", "-d", "10.88.0.5", "-p", "tcp", "--dport", "8080", "-j", "ACCEPT"},
		{gw, "iptables", "-I", "FORWARD", "-s", "10.88.0.5", "-p", "tcp", "--sport", "8080", "-j", "ACCEPT"},
	} {
		gwMustInNS(t, c[0], c[1:]...)
	}
	gwLiveServe(t, sv, "10.88.0.5", 80)
	gwLiveServe(t, sv, "10.88.0.5", 8080, "|direct")
	gwLiveServe(t, cl, "10.77.0.2", 8000)
	gwLiveServe(t, gw, "10.88.0.1", 8000)

	sys := t.TempDir()
	for _, p := range []string{"net/ipv4/ip_forward", "net/ipv6/conf/all/forwarding"} {
		full := filepath.Join(sys, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("1\n"), 0o644)
	}
	prevSys, prevClass, prevRun, prevHas := gatewaySysRoot, gatewayClassNet, run, has
	gatewaySysRoot, gatewayClassNet, run, has = sys, t.TempDir(), gwLiveRun(gw), func(string) bool { return false }
	t.Cleanup(func() { gatewaySysRoot, gatewayClassNet, run, has = prevSys, prevClass, prevRun, prevHas })

	svc := testService(t)
	ctx := context.Background()
	// Not the namespace's client: a limit trusts the address that makes it,
	// and the point of these checks is that the client is limited.
	const operator = "192.0.2.1"

	if c := svc.GatewayCapability(ctx); !c.Writable || c.Firewall != "iptables" && c.Firewall != "none" {
		t.Fatalf("capability = %+v", c)
	}

	// Port forward: blocked by the filter until it is admitted by the mark.
	if _, ok := gwFetchFrom(t, cl, "http://10.77.0.1:8080/"); ok {
		t.Fatal("reached the server before any forward existed")
	}
	fwd, err := svc.AddForward(ctx, ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.88.0.5", TargetPort: "80", SourceNAT: "always"}, operator, "test", gwProtected)
	if err != nil {
		t.Fatal(err)
	}
	if body, ok := gwFetchFrom(t, cl, "http://10.77.0.1:8080/"); !ok || body != "10.88.0.1" {
		t.Fatalf("a masqueraded forward: got %q, %v (want the gateway's own address)", body, ok)
	}
	// The forward's own packets, counted by comment.
	view, err := svc.Gateway(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Loaded || !view.Admission.Present || len(view.Forwards) != 1 || view.Forwards[0].Packets == 0 {
		t.Fatalf("gateway view = %+v", view)
	}

	// Without source translation the target sees the visitor.
	if _, err := svc.UpdateForward(ctx, fwd.ID, ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.88.0.5", TargetPort: "80", SourceNAT: "never"}, operator, "test", gwProtected); err != nil {
		t.Fatal(err)
	}
	if body, ok := gwFetchFrom(t, cl, "http://10.77.0.1:8080/"); !ok || body != "10.77.0.2" {
		t.Fatalf("an unmasqueraded forward: got %q, %v (want the visitor's address)", body, ok)
	}

	// Traffic passing through to the same port on another address is not
	// captured: the forward is for what is addressed to this host.
	if body, ok := gwFetchFrom(t, cl, "http://10.88.0.5:8080/"); !ok || body != "10.77.0.2|direct" {
		t.Fatalf("a connection through the gateway to port 8080 of another host: got %q, %v (the forward took it)", body, ok)
	}

	// A forward to a port range keeps its numbers.
	if _, err := svc.AddForward(ctx, ForwardRequest{Name: "range", Protocol: "tcp", Ports: "9000-9010", Target: "10.88.0.5", SourceNAT: "never"}, operator, "test", gwProtected); err != nil {
		t.Fatal(err)
	}
	gwLiveServe(t, sv, "10.88.0.5", 9005)
	if body, ok := gwFetchFrom(t, cl, "http://10.77.0.1:9005/"); !ok || body != "10.77.0.2" {
		t.Fatalf("a range forward: got %q, %v", body, ok)
	}

	// A connection limit on the forwarded port: five are tried and two get
	// through, the rest being over the limit.
	limit, err := svc.AddLimit(ctx, LimitRequest{Name: "web", Protocol: "tcp", Ports: "8080", MaxConnections: 2}, operator, "test")
	if err != nil {
		t.Fatal(err)
	}
	hold := filepath.Join(t.TempDir(), "hold.py")
	if err := os.WriteFile(hold, []byte(gwLiveHold), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := strings.TrimSpace(gwMustInNS(t, cl, "python3", hold, "10.77.0.1", "8080")); out != "2" {
		t.Fatalf("five connections against a limit of two: %s connected", out)
	}
	// The same port on another host, merely routed through, is not limited.
	if out := strings.TrimSpace(gwMustInNS(t, cl, "python3", hold, "10.88.0.5", "8080")); out != "5" {
		t.Fatalf("five connections through the gateway to another host's port 8080: %s connected; a limit must not throttle routed traffic", out)
	}
	pv, err := svc.Protection(ctx, operator)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Limits[0].ID != limit.ID || pv.Limits[0].Packets == 0 {
		t.Fatalf("the limit dropped nothing, by its own counter: %+v", pv.Limits[0])
	}
	if err := svc.DeleteLimit(ctx, limit.ID); err != nil {
		t.Fatal(err)
	}

	// A blocklist drops the client; the trusted set takes precedence.
	if _, err := svc.AddBlocklist(ctx, BlocklistRequest{Name: "client", Kind: "manual", Entries: []string{"10.77.0.2"}}, "10.77.0.2", "test"); err == nil {
		t.Fatal("a manual list holding the reader was accepted")
	}
	// A session open before the list takes the address...
	late := filepath.Join(t.TempDir(), "late.py")
	if err := os.WriteFile(late, []byte(gwLiveLate), 0o644); err != nil {
		t.Fatal(err)
	}
	signal := filepath.Join(t.TempDir(), "go")
	lateCmd := gwLiveCmd(context.Background(), "ip", "netns", "exec", cl, "timeout", "60", "python3", late, "10.77.0.1", "8080", signal)
	lateOut, err := lateCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := lateCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lateCmd.Process.Kill() })
	lines := bufio.NewReader(lateOut)
	if line, _ := lines.ReadString('\n'); strings.TrimSpace(line) != "connected" {
		t.Fatalf("the session did not open: %q", line)
	}
	bl, err := svc.AddBlocklist(ctx, BlocklistRequest{Name: "client", Kind: "manual", Entries: []string{"10.77.0.2"}}, operator, "test")
	if err != nil {
		t.Fatal(err)
	}
	// ...a new connection from it is dropped...
	if _, ok := gwFetchFrom(t, cl, "http://10.77.0.1:8080/"); ok {
		t.Fatal("a blocklisted client got through")
	}
	// ...and the open one carries on to its answer.
	if err := os.WriteFile(signal, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if line, _ := lines.ReadString('\n'); strings.TrimSpace(line) != "10.77.0.2" {
		t.Fatalf("an established session was cut by a list taking its address: %q", line)
	}
	// Replies to this server's own outbound connections are not dropped either:
	// the gateway fetches from the listed address, and the answer comes back.
	if body, ok := gwFetchFrom(t, gw, "http://10.77.0.2:8000/"); !ok || body != "10.77.0.1" {
		t.Fatalf("a reply from a listed address to a connection this host opened: got %q, %v", body, ok)
	}
	if pv, _ = svc.Protection(ctx, operator); pv.Blocklists[0].Packets == 0 {
		t.Fatalf("the list dropped nothing, by its own counter: %+v", pv.Blocklists[0])
	}
	// Making a protection entry as that client keeps them out of the drops.
	if _, err := svc.AddLimit(ctx, LimitRequest{Name: "anything", Protocol: "tcp", Ports: "7777", Rate: 1, Per: "hour"}, "10.77.0.2", "test"); err != nil {
		t.Fatal(err)
	}
	if body, ok := gwFetchFrom(t, cl, "http://10.77.0.1:8080/"); !ok || body != "10.77.0.2" {
		t.Fatalf("a trusted client on a blocklist: got %q, %v", body, ok)
	}
	if err := svc.DeleteBlocklist(ctx, bl.ID, "10.77.0.2"); err != nil {
		t.Fatal(err)
	}

	// NAT: the server reaches the client through the gateway, translated.
	if _, ok := gwFetchFrom(t, sv, "http://10.77.0.2:8000/"); ok {
		t.Fatal("the server reached the client through a DROP policy with no NAT entry")
	}
	nat, err := svc.AddNAT(ctx, NATRequest{Name: "lab", Source: "10.88.0.0/24", Interface: "gwc"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if body, ok := gwFetchFrom(t, sv, "http://10.77.0.2:8000/"); !ok || body != "10.77.0.1" {
		t.Fatalf("a NAT entry: got %q, %v (want the gateway's address on the way out)", body, ok)
	}
	// The mark does not admit the tunnel's traffic to this host's own ports:
	// the gateway answers on 8000 and its INPUT policy is DROP.
	if _, ok := gwFetchFrom(t, sv, "http://10.88.0.1:8000/"); ok {
		t.Fatal("NAT admission let a source reach a service on the gateway itself")
	}

	// Copies of the rule that something else left behind go with the rest.
	for _, chain := range []string{"FORWARD", "FORWARD", "INPUT"} {
		gwMustInNS(t, gw, append([]string{"iptables", "-I", chain, "1"}, admissionRule()...)...)
	}

	// Everything removed: nothing is admitted any more, and the rules are gone.
	sp, _ := svc.loadSpec()
	for _, f := range sp.Forwards {
		if err := svc.DeleteForward(ctx, f.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.DeleteNAT(ctx, nat.ID); err != nil {
		t.Fatal(err)
	}
	for _, chain := range []string{"FORWARD", "INPUT"} {
		if out := gwMustInNS(t, gw, "iptables", "-S", chain); strings.Contains(out, "just-dashboard-gateway") {
			t.Fatalf("a copy of the admission rule outlived the last translation in %s:\n%s", chain, out)
		}
	}
	if _, ok := gwFetchFrom(t, cl, "http://10.77.0.1:8080/"); ok {
		t.Fatal("a removed forward still forwards")
	}

	// One end of a veth pair is refused; a device of the gateway's own is not.
	if err := svc.SetShaping(ctx, "gwc", ShapeRequest{EgressKbit: 5000}, operator, "test"); err == nil || !strings.Contains(err.Error(), "veth") {
		t.Fatalf("shaping a veth: err = %v", err)
	}
	gwMustInNS(t, gw, "ip", "link", "add", "dm0", "type", "dummy")
	gwMustInNS(t, gw, "ip", "link", "set", "dm0", "up")
	if err := svc.SetShaping(ctx, "dm0", ShapeRequest{EgressKbit: 5000}, operator, "test"); err != nil {
		t.Fatalf("shaping a dummy device: %v", err)
	}
}

func TestLiveShapingBatchLoadsAndReadsBack(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "shape")
	for _, dev := range []string{"dm0", "dm1", "dm2", "dm3"} {
		gwMustInNS(t, ns, "ip", "link", "add", dev, "type", "dummy")
		gwMustInNS(t, ns, "ip", "link", "set", dev, "up")
	}
	sp := emptySpec()
	sp.Shaping = []ShapeSpec{
		{Device: "dm0", EgressKbit: 50000, IngressKbit: 100000},
		{Device: "dm1", Qdisc: "cake", EgressKbit: 20000},
		{Device: "dm2", Qdisc: "fq"},
		{Device: "dm3", IngressKbit: 8000},
	}
	batch := filepath.Join(t.TempDir(), "shaping.batch")
	if err := os.WriteFile(batch, []byte(renderShaping(sp)), 0o644); err != nil {
		t.Fatal(err)
	}
	// -force, as the boot unit runs it: the deletes of what is not there fail
	// and the rest carries on.
	load := func() string {
		// tc exits non-zero under -force when any line failed, and the deletes
		// of queues that are not there do; the unit's "-" prefix accepts that.
		out, _ := gwInNS(t, ns, "tc", "-force", "-batch", batch)
		return out
	}
	load()
	again := load() // the unit run twice over a shaped device
	if strings.Contains(again, "File exists") || strings.Contains(again, "Change operation not supported") || strings.Contains(again, "Cannot find device") {
		t.Fatalf("the batch is not idempotent:\n%s", again)
	}
	out := gwMustInNS(t, ns, "tc", "-j", "-s", "qdisc", "show")
	qs, err := parseQdiscs(out)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]map[string]bool{}
	for _, q := range qs {
		if kinds[q.Dev] == nil {
			kinds[q.Dev] = map[string]bool{}
		}
		kinds[q.Dev][q.Kind] = true
		if q.Root {
			kinds[q.Dev]["root:"+q.Kind] = true
		}
	}
	for dev, want := range map[string][]string{
		"dm0": {"root:htb", "fq_codel", "ingress"}, "dm1": {"root:cake"}, "dm2": {"root:fq"}, "dm3": {"ingress"},
	} {
		for _, k := range want {
			if !kinds[dev][k] {
				t.Errorf("%s lacks %s; has %v", dev, k, kinds[dev])
			}
		}
	}
	if f := gwMustInNS(t, ns, "tc", "filter", "show", "dev", "dm0", "parent", "ffff:"); !strings.Contains(f, "police") || !strings.Contains(f, "100Mbit") {
		t.Errorf("no police action at 100Mbit:\n%s", f)
	}

	// And through the Service: the same commands, one device, then cleared.
	prevRun, prevHas := run, has
	run, has = gwLiveRun(ns), func(string) bool { return false }
	t.Cleanup(func() { run, has = prevRun, prevHas })
	svc := testService(t)
	ctx := context.Background()
	gwMustInNS(t, ns, "ip", "link", "add", "dm4", "type", "dummy")
	gwMustInNS(t, ns, "ip", "link", "set", "dm4", "up")
	if err := svc.SetShaping(ctx, "dm4", ShapeRequest{Qdisc: "cake", EgressKbit: 30000, IngressKbit: 30000}, "192.0.2.1", "test"); err != nil {
		t.Fatal(err)
	}
	view, err := svc.Shaping(ctx, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range view.Devices {
		if d.Name == "dm4" {
			found = true
			if d.Root == nil || d.Root.Kind != "cake" || !d.Ingress || !d.Managed || d.EgressKbit != 30000 {
				t.Fatalf("dm4 = %+v", d)
			}
		}
	}
	if !found {
		t.Fatal("dm4 missing from the view")
	}
	if err := svc.ClearShaping(ctx, "dm4"); err != nil {
		t.Fatal(err)
	}
	if out := gwMustInNS(t, ns, "tc", "qdisc", "show", "dev", "dm4"); strings.Contains(out, "cake") || strings.Contains(out, "ingress") {
		t.Fatalf("still shaped:\n%s", out)
	}
}

func TestLiveCapabilityReadsRealRulesets(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "cap")
	prevRun, prevHas := run, has
	run, has = gwLiveRun(ns), func(string) bool { return false }
	t.Cleanup(func() { run, has = prevRun, prevHas })
	svc := testService(t)
	ctx := context.Background()
	prevClass := gatewayClassNet
	gatewayClassNet = t.TempDir() // this host's docker0 is not the namespace's
	t.Cleanup(func() { gatewayClassNet = prevClass })

	if c := svc.GatewayCapability(ctx); !c.Writable || c.Firewall != "none" || c.Docker {
		t.Fatalf("an empty namespace = %+v", c)
	}
	// An operator's own nftables firewall that drops forwarded traffic.
	rules := filepath.Join(t.TempDir(), "own.nft")
	if err := os.WriteFile(rules, []byte("table inet filter {\n\tchain forward {\n\t\ttype filter hook forward priority filter; policy drop;\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gwMustInNS(t, ns, "nft", "-f", rules)
	c := svc.GatewayCapability(ctx)
	if c.Writable || c.Blocker == nil || c.Blocker.Table != "filter" || c.Blocker.Chain != "forward" || c.Firewall != "nftables" {
		t.Fatalf("a foreign drop-forward table = %+v", c)
	}
	// The accept the page tells the operator to add is accepted by nft, and
	// then admits a marked connection (the forward chain still drops the rest).
	add := filepath.Join(t.TempDir(), "accept.nft")
	if err := os.WriteFile(add, []byte("add rule inet filter forward "+c.Blocker.Rule+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gwMustInNS(t, ns, "nft", "-f", add)
	if c := svc.GatewayCapability(ctx); !c.Writable || len(c.Layers) != 1 || c.Layers[0].Status != "admitted" {
		t.Fatalf("the documented connmark exemption was not recognized: %+v", c)
	}
	// iptables-nft's own FORWARD policy drop is not a blocker.
	gwMustInNS(t, ns, "nft", "delete", "table", "inet", "filter")
	gwMustInNS(t, ns, "iptables", "-P", "FORWARD", "DROP")
	if c := svc.GatewayCapability(ctx); !c.Writable || c.Firewall != "iptables" {
		t.Fatalf("an iptables-nft host with a drop policy = %+v", c)
	}
}

func TestLiveAdmissionDetectsEveryFamilyAndChainAfterPartialReload(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "admission-health")
	prevRun, prevHas := run, has
	run, has = gwLiveRun(ns), func(string) bool { return false }
	t.Cleanup(func() { run, has = prevRun, prevHas })
	prevClass := gatewayClassNet
	gatewayClassNet = t.TempDir()
	t.Cleanup(func() { gatewayClassNet = prevClass })
	svc := testService(t)
	sp := dualStackAdmissionSpec()
	b, err := json.Marshal(sp)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(svc.specPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"iptables", "ip6tables"} {
		gwMustInNS(t, ns, tool, "-N", "DOCKER-USER")
		gwMustInNS(t, ns, tool, "-P", "FORWARD", "DROP")
		gwMustInNS(t, ns, tool, "-P", "INPUT", "DROP")
	}
	ctx := context.Background()
	if err := svc.RepairGatewayAdmission(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"iptables", "ip6tables"} {
		for _, chain := range admissionChains {
			gwMustInNS(t, ns, append([]string{tool, "-D", chain}, admissionRule()...)...)
			state := svc.admissionState(ctx, sp)
			if state.Present || len(state.Chains) != 6 {
				t.Fatalf("partial removal of %s/%s was hidden: %+v", tool, chain, state)
			}
			missing := 0
			for _, ch := range state.Chains {
				if ch.Status == "absent" {
					missing++
					if ch.Tool != tool || ch.Chain != chain {
						t.Fatalf("unexpected missing rule: %+v", ch)
					}
				}
			}
			if missing != 1 {
				t.Fatalf("missing = %d, want one: %+v", missing, state)
			}
			if err := svc.RepairGatewayAdmission(ctx); err != nil {
				t.Fatal(err)
			}
			if state := svc.admissionState(ctx, sp); !state.Present {
				t.Fatalf("repair failed: %+v", state)
			}
		}
	}
}

func TestLiveCapabilityDetectsExplicitDropInAcceptPolicyChain(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "explicit-drop")
	prevRun, prevHas := run, has
	run, has = gwLiveRun(ns), func(string) bool { return false }
	t.Cleanup(func() { run, has = prevRun, prevHas })
	prevClass := gatewayClassNet
	gatewayClassNet = t.TempDir()
	t.Cleanup(func() { gatewayClassNet = prevClass })
	svc := testService(t)
	rules := filepath.Join(t.TempDir(), "drop.nft")
	if err := os.WriteFile(rules, []byte("table inet foreign {\n chain forward {\n type filter hook forward priority filter; policy accept;\n ip daddr 198.51.100.7 drop\n }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gwMustInNS(t, ns, "nft", "-f", rules)
	c := svc.GatewayCapability(context.Background())
	if c.Writable || c.Reachability != "unknown" || len(c.Layers) != 1 || c.Layers[0].Status != "unknown" || c.Blocker == nil {
		t.Fatalf("explicit matching drop in accept-policy chain was hidden: %+v", c)
	}
}

func TestLiveBlocklistHealthRetainsLoadedDataAfterCacheLossAndDetectsSetDrift(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "cache-health")
	prevRun, prevHas := run, has
	run, has = gwLiveRun(ns), func(string) bool { return false }
	t.Cleanup(func() { run, has = prevRun, prevHas })
	svc := testService(t)
	sp := emptySpec()
	sp.NextID = 2
	sp.Blocklists = []BlocklistSpec{{ID: 1, Name: "live list", Kind: "feed", URL: "https://example.com/list", Count: 2, Enabled: true}}
	nets := []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("2001:db8::/64")}
	if err := writeBlocklistCache(filepath.Join(svc.paths.Dir, "lists"), 1, nets); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(sp)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(svc.specPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	rendered, err := renderGateway(sp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.loadGatewayRules(context.Background(), rendered); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(svc.paths.Dir, gatewayFile), []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := svc.Blocklist(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Enforcement != "verified" || v.Runtime.Count == nil || *v.Runtime.Count != 2 {
		t.Fatalf("live generations did not verify: %+v", v)
	}
	if err := os.Remove(blocklistFile(filepath.Join(svc.paths.Dir, "lists"), 1)); err != nil {
		t.Fatal(err)
	}
	v, err = svc.Blocklist(context.Background(), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Cache.Status != "missing" || v.Count != 0 || v.SavedCount != 2 || v.Runtime.Count == nil || *v.Runtime.Count != 2 || v.Enforcement != "degraded" {
		t.Fatalf("lost cache claimed healthy protection: %+v", v)
	}
	if _, err := svc.AddLimit(context.Background(), LimitRequest{Name: "x", Protocol: "tcp", Ports: "8080", Rate: 5, Per: "second"}, "127.0.0.1", "ops"); err == nil {
		t.Fatal("missing cache allowed a reload")
	}
	if actual := readBlocklistRuntime(context.Background(), 1); actual.Generation != v.Runtime.Generation {
		t.Fatal("failed reload changed the last-good kernel set")
	}
	if err := writeBlocklistCache(filepath.Join(svc.paths.Dir, "lists"), 1, nets); err != nil {
		t.Fatal(err)
	}
	gwMustInNS(t, ns, "nft", "delete", "element", "inet", gatewayTable, "bl_1_6", "{", "2001:db8::/64", "}")
	v, _ = svc.Blocklist(context.Background(), 1, "")
	if v.Enforcement != "degraded" || v.Runtime.Count == nil || *v.Runtime.Count != 1 {
		t.Fatalf("partial family set drift was not detected: %+v", v)
	}
}

func TestLiveShapingLeavesAClsactQueueAlone(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "clsact")
	gwMustInNS(t, ns, "ip", "link", "add", "dm5", "type", "dummy")
	gwMustInNS(t, ns, "ip", "link", "set", "dm5", "up")
	// What tc-BPF tooling does: a clsact queue, which is where its filters hang.
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", "dm5", "clsact")
	hasClsact := func() bool {
		out := gwMustInNS(t, ns, "tc", "-j", "qdisc", "show", "dev", "dm5")
		qs, err := parseQdiscs(out)
		if err != nil {
			t.Fatal(err)
		}
		return ingressKind(qs) == "clsact"
	}
	if !hasClsact() {
		t.Fatal("no clsact queue to protect")
	}

	// The boot batch, run over it: the ingress half fails and the queue stays.
	sp := emptySpec()
	sp.Shaping = []ShapeSpec{{Device: "dm5", EgressKbit: 20000, IngressKbit: 8000}}
	batch := filepath.Join(t.TempDir(), "shaping.batch")
	if err := os.WriteFile(batch, []byte(renderShaping(sp)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = gwInNS(t, ns, "tc", "-force", "-batch", batch) // exits 1 where lines fail, which is the point
	if !hasClsact() {
		t.Fatal("the boot batch deleted a clsact queue")
	}

	// Through the Service: a download limit is refused, an upload limit is not,
	// and clearing it leaves the queue.
	prevRun, prevHas := run, has
	run, has = gwLiveRun(ns), func(string) bool { return false }
	t.Cleanup(func() { run, has = prevRun, prevHas })
	svc := testService(t)
	owned, err := json.Marshal(sp)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(svc.specPath(), owned, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := svc.SetShaping(ctx, "dm5", ShapeRequest{IngressKbit: 8000}, "192.0.2.1", "test"); err == nil || !strings.Contains(err.Error(), "clsact") {
		t.Fatalf("err = %v", err)
	}
	if err := svc.SetShaping(ctx, "dm5", ShapeRequest{EgressKbit: 20000}, "192.0.2.1", "test"); err != nil {
		t.Fatal(err)
	}
	if !hasClsact() {
		t.Fatal("an upload limit removed the clsact queue")
	}
	if err := svc.ClearShaping(ctx, "dm5"); err != nil {
		t.Fatal(err)
	}
	if !hasClsact() {
		t.Fatal("clearing the shaping removed the clsact queue")
	}
}
