package netx

import (
	"context"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Previews run a save's checks without the lock, journal or any host change,
// and name what a save would not refuse but the operator should know.

const gwListeners = `tcp   LISTEN 0      4096   0.0.0.0:8080     0.0.0.0:*    users:(("caddy",pid=900,fd=7))
tcp   LISTEN 0      4096   127.0.0.1:5432   0.0.0.0:*    users:(("postgres",pid=901,fd=5))
udp   UNCONN 0      0      0.0.0.0:53       0.0.0.0:*    users:(("named",pid=902,fd=9))
tcp   LISTEN 0      4096   203.0.113.25:25  0.0.0.0:*    users:(("exim",pid=903,fd=4))
`

func impactKinds(p *GatewayPreview) map[string]Impact {
	out := map[string]Impact{}
	for _, i := range p.Impacts {
		out[i.Kind] = i
	}
	return out
}

func TestForwardPreviewNamesOverlapsListenersAndWhatElseJudgesIt(t *testing.T) {
	h := newGwHost(t)
	h.first("ss -Hlntup", gwListeners, nil)
	sp := emptySpec()
	sp.NextID = 10
	sp.Forwards = []ForwardSpec{
		{ID: 1, Name: "old web", Protocol: "tcp", Interface: "eth0", Ports: "8000-8100", Target: "10.0.0.9", SourceNAT: "never", Enabled: true},
		{ID: 2, Name: "exact", Protocol: "tcp", Interface: "eth0", Ports: "9090", Target: "10.0.0.8", SourceNAT: "never", Enabled: true},
	}
	sp.Limits = []LimitSpec{{ID: 3, Name: "web", Protocol: "tcp", Ports: "8080", Rate: 10, Per: "second", Action: "drop", Enabled: true}}
	sp.Blocklists = []BlocklistSpec{{ID: 4, Name: "scanners", Kind: "manual", Entries: []string{"198.51.100.0/24"}, Enabled: true}}
	h.seed(t, sp)
	before, _ := os.ReadFile(h.specPath())
	p, err := h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "forward", Forward: &ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "172.17.0.2", TargetPort: "80"}}, gwClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Valid || p.Forward == nil || p.Forward.Masquerade {
		t.Fatalf("preview = %+v", p)
	}
	k := impactKinds(p)
	for _, want := range []string{"overlap", "listener", "limit", "blocklist", "nat-decision"} {
		if _, ok := k[want]; !ok {
			t.Errorf("missing %s in %+v", want, p.Impacts)
		}
	}
	if !strings.Contains(k["listener"].Message, "caddy") || k["overlap"].Severity != "warning" {
		t.Errorf("impacts = %+v", p.Impacts)
	}
	if _, ok := k["admission"]; ok {
		t.Error("IPv4 is already admitted for the existing forwards")
	}
	for _, c := range h.rec.commands() {
		if strings.HasPrefix(c, "nft -f") || strings.HasPrefix(c, "iptables -I") || strings.HasPrefix(c, "iptables -D") {
			t.Fatalf("a preview changed the host: %s", c)
		}
	}
	if after, _ := os.ReadFile(h.specPath()); string(after) != string(before) {
		t.Fatal("a preview wrote the spec")
	}
	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "forward", Forward: &ForwardRequest{Name: "v6", Protocol: "tcp", Ports: "7443", Target: "2001:db8::5"}}, gwClient, nil)
	if _, ok := impactKinds(p)["admission"]; !ok {
		t.Errorf("the first IPv6 translation's admission insertion is not named: %+v", p.Impacts)
	}

	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "forward", Forward: &ForwardRequest{Name: "dup", Protocol: "both", Interface: "eth0", Ports: "9090", Target: "10.0.0.7"}}, gwClient, nil)
	if impactKinds(p)["collision"].Severity != "refused" {
		t.Fatalf("collision = %+v", p.Impacts)
	}
	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "forward", Forward: &ForwardRequest{Name: "ssh", Protocol: "tcp", Ports: "22", Target: "10.0.0.7"}}, gwClient, []int{22})
	if impactKinds(p)["guard"].Severity != "refused" {
		t.Fatalf("guard = %+v", p.Impacts)
	}
	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "forward", Forward: &ForwardRequest{Name: "bad", Protocol: "tcp", Ports: "80", Target: "127.0.0.1"}}, gwClient, nil)
	if p.Valid || !strings.Contains(p.Error, "route_localnet") {
		t.Fatalf("invalid = %+v", p)
	}
	h.writeSys("net/ipv4/ip_forward", "0")
	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "forward", Forward: &ForwardRequest{Name: "off", Protocol: "tcp", Ports: "7000", Target: "10.0.0.7"}}, gwClient, nil)
	if impactKinds(p)["forwarding"].Severity != "refused" {
		t.Fatalf("forwarding off = %+v", p.Impacts)
	}
}

func TestEditingAForwardCountsTheConnectionsLeftOnTheOldTranslation(t *testing.T) {
	h := newGwHost(t)
	g := &gwConntrack{entries: []gwCT{
		{src: "198.51.100.7", dst: "203.0.113.20", sport: 40000, dport: 8080, replySrc: "10.0.0.9", replySport: 80, proto: unix.IPPROTO_TCP, tcpState: 3, status: ctStatusDstNAT | ctStatusSeenReply, mark: 0x4a000000},
		{src: "198.51.100.8", dst: "203.0.113.20", sport: 40001, dport: 8080, replySrc: "10.0.0.9", replySport: 80, proto: unix.IPPROTO_TCP, tcpState: 3, status: ctStatusDstNAT | ctStatusSeenReply, mark: 0x4a000000},
		{src: "198.51.100.9", dst: "203.0.113.20", sport: 40002, dport: 443, proto: unix.IPPROTO_TCP, tcpState: 3},
	}}
	g.install(t)
	sp := emptySpec()
	sp.NextID = 2
	sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.9", TargetPort: "80", SourceNAT: "never", Enabled: true}}
	h.seed(t, sp)
	p, err := h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "forward", ID: 1, Forward: &ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.10", TargetPort: "80", SourceNAT: "never"}}, gwClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Connections == nil || p.Connections.Tracked != 2 || p.Connections.Sources != 2 {
		t.Fatalf("connections = %+v", p.Connections)
	}
	if !strings.Contains(impactKinds(p)["connections"].Message, "keep that translation") {
		t.Fatalf("impacts = %+v", p.Impacts)
	}
	off := false
	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "forward", ID: 1, Forward: &ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.9", TargetPort: "80", SourceNAT: "never", Enabled: &off}}, gwClient, nil)
	if impactKinds(p)["disable"].Severity != "warning" || p.Connections.Tracked != 2 {
		t.Fatalf("disable = %+v %+v", p.Impacts, p.Connections)
	}
}

func TestNATPreviewNamesCollisionsMappingsAndPrecedence(t *testing.T) {
	h := newGwHost(t)
	h.first("ss -Hlntup", gwListeners, nil)
	h.first("ip -j addr show dev eth0", `[{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.20","prefixlen":24,"scope":"global"},{"family":"inet","local":"203.0.113.25","prefixlen":24,"scope":"global"}]}]`, nil)
	sp := emptySpec()
	sp.NextID = 10
	sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.9", SourceNAT: "never", Enabled: true}}
	sp.NAT = []NATSpec{{ID: 2, Name: "wg exit", Source: "10.8.0.0/24", Interface: "eth0", Owner: "wireguard:wg0", Enabled: true}}
	h.seed(t, sp)
	p, err := h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "nat", NAT: &NATRequest{Name: "mail", Source: "10.0.4.25", Interface: "eth0", Mode: "one-to-one", Translated: "203.0.113.25"}}, gwClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	k := impactKinds(p)
	for _, want := range []string{"inbound", "forward-precedence", "listener"} {
		if _, ok := k[want]; !ok {
			t.Errorf("missing %s in %+v", want, p.Impacts)
		}
	}
	if !strings.Contains(k["listener"].Message, "exim") {
		t.Errorf("listener = %+v", k["listener"])
	}
	if _, refused := k["host"]; refused {
		t.Errorf("the address is on eth0: %+v", k["host"])
	}
	if len(p.Flows) != 2 {
		t.Errorf("both halves are modeled: %+v", p.Flows)
	}
	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "nat", NAT: &NATRequest{Name: "dup", Source: "10.8.0.0/25", Interface: "eth0"}}, gwClient, nil)
	if c := impactKinds(p)["collision"]; c.Severity != "refused" || !strings.Contains(c.Message, "wg exit") {
		t.Fatalf("collision with a VPN's entry = %+v", p.Impacts)
	}
	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "nat", NAT: &NATRequest{Name: "elsewhere", Source: "10.0.4.26", Interface: "eth0", Mode: "one-to-one", Translated: "198.51.100.26"}}, gwClient, nil)
	if c := impactKinds(p)["host"]; c.Severity != "refused" || !strings.Contains(c.Message, "not an address of eth0") {
		t.Fatalf("an address the device does not hold = %+v", p.Impacts)
	}
	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "nat", NAT: &NATRequest{Name: "v6", Source: "fd00:3::/48", Interface: "eth0", Mode: "nptv6", Translated: "2001:db8:3::/48"}}, gwClient, nil)
	if _, ok := impactKinds(p)["nptv6"]; !ok {
		t.Fatalf("stateful prefix translation is explained: %+v", p.Impacts)
	}
	if _, ok := impactKinds(p)["routing"]; !ok {
		t.Fatalf("a routed prefix needs the provider: %+v", p.Impacts)
	}
	p, _ = h.PreviewGateway(context.Background(), GatewayPreviewRequest{Kind: "nat", ID: 2, NAT: &NATRequest{Name: "x", Source: "10.8.0.0/24", Interface: "eth0"}}, gwClient, nil)
	if p.Valid || !strings.Contains(p.Error, "wireguard:wg0") {
		t.Fatalf("a VPN's entry is changed there: %+v", p)
	}
}

func TestOneToOneCannotTakeTheAddressTheOperatorArrivesOn(t *testing.T) {
	h := newGwHost(t)
	h.first("ip -j addr show dev tailscale0", gwAddrTS, nil)
	sp := emptySpec()
	sp.NextID = 2
	h.seed(t, sp)
	// The client's replies leave from 100.110.34.31 on tailscale0.
	_, err := h.AddNAT(context.Background(), NATRequest{Name: "hijack", Source: "10.0.0.5", Interface: "tailscale0", Mode: "one-to-one", Translated: "100.110.34.31"}, gwClient, "ops")
	var guard *GuardError
	if err == nil || !strings.Contains(err.Error(), "your own connection") {
		t.Fatalf("err = %v", err)
	}
	_ = guard
}
