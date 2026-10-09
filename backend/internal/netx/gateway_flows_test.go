package netx

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

// The per-flow model against rule shapes copied (with documentation
// addresses) from an ordinary Docker and ufw host: nat chains that only
// translate, raw-table drops naming container addresses, mangle marks that
// never drop, plus foreign chains a test adds to make a flow blocked,
// restricted by source or undecidable.

// gwRuleset returns the Docker fixture with extra nft objects appended.
func gwRuleset(t *testing.T, extra ...string) string {
	t.Helper()
	var doc struct {
		Nftables []json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "gateway-ruleset-docker.json")), &doc); err != nil {
		t.Fatal(err)
	}
	for _, e := range extra {
		doc.Nftables = append(doc.Nftables, json.RawMessage(e))
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func gwListing(t *testing.T, raw string) *nftListing {
	t.Helper()
	var l nftListing
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		t.Fatal(err)
	}
	return &l
}

var gwHostAddrs = []hostAddr{
	{Addr: netip.MustParseAddr("127.0.0.1"), Network: netip.MustParsePrefix("127.0.0.0/8"), Dev: "lo"},
	{Addr: netip.MustParseAddr("203.0.113.20"), Network: netip.MustParsePrefix("203.0.113.0/24"), Dev: "eth0"},
	{Addr: netip.MustParseAddr("172.17.0.1"), Network: netip.MustParsePrefix("172.17.0.0/16"), Dev: "docker0"},
	{Addr: netip.MustParseAddr("10.0.4.1"), Network: netip.MustParsePrefix("10.0.4.0/24"), Dev: "br-lab"},
	{Addr: netip.MustParseAddr("100.110.34.31"), Network: netip.MustParsePrefix("100.110.34.31/32"), Dev: "tailscale0"},
}

func gwFlowSpec(f ForwardSpec) *Spec {
	f.Enabled = true
	if f.ID == 0 {
		f.ID = 1
	}
	if f.Protocol == "" {
		f.Protocol = "tcp"
	}
	if f.SourceNAT == "" {
		f.SourceNAT = "never"
	}
	return &Spec{Forwards: []ForwardSpec{f}}
}

func gwOneFlow(t *testing.T, raw string, sp *Spec) EntryFlow {
	t.Helper()
	flows := evaluateGatewayFlows(gwListing(t, raw), modelGatewayFlows(sp, gwHostAddrs))
	if len(flows) == 0 {
		t.Fatal("no flow was modeled")
	}
	return flows[0]
}

func gwLayer(t *testing.T, f EntryFlow, table, chain string) FlowLayer {
	t.Helper()
	for _, l := range f.Layers {
		if l.Table == table && l.Chain == chain {
			return l
		}
	}
	t.Fatalf("no layer %s %s in %+v", table, chain, f.Layers)
	return FlowLayer{}
}

const gwInetForward = `{"chain": {"family": "inet", "table": "local", "name": "forward", "handle": 1, "type": "filter", "hook": "forward", "prio": 0, "policy": "accept"}}`

func gwInetRule(handle int, expr string) string {
	return `{"rule": {"family": "inet", "table": "local", "chain": "forward", "handle": ` + strconv.Itoa(handle) + `, "expr": ` + expr + `}}`
}

func TestDockerHostShapesClearAForwardToAContainer(t *testing.T) {
	raw := gwRuleset(t)
	f := gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "8080", Target: "172.17.0.2", TargetPort: "80"}))
	if f.Verdict != "clear" {
		t.Fatalf("verdict = %s: %+v", f.Verdict, f.Layers)
	}
	// Docker's raw drop names the container's address, but at -300 the
	// visitor's packet is still addressed to this host.
	if l := gwLayer(t, f, "raw", "PREROUTING"); l.Verdict != "clear" {
		t.Errorf("raw = %+v", l)
	}
	if l := gwLayer(t, f, "nat", "PREROUTING"); l.Verdict != "clear" {
		t.Errorf("a translating nat chain cannot drop: %+v", l)
	}
	if l := gwLayer(t, f, "mangle", "PREROUTING"); l.Verdict != "clear" {
		t.Errorf("a mark-only rule cannot drop: %+v", l)
	}
	for _, l := range f.Layers {
		if l.Table == "filter" {
			t.Errorf("the owned admission chain was modeled: %+v", l)
		}
	}
	// A port the loopback-guard rule names is still clear: a visitor cannot
	// address 127.0.0.1 from outside.
	f = gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "40507", Target: "172.17.0.2"}))
	if f.Verdict != "clear" {
		t.Fatalf("loopback guard: %+v", f.Layers)
	}
}

func TestGenericCheckCallsDockerRawDropsUnknownButTheFlowsClear(t *testing.T) {
	h := newGwHost(t)
	h.first("nft -t -j list ruleset", gwRuleset(t), nil)
	c := h.GatewayCapability(context.Background())
	if c.Writable {
		t.Fatal("the generic check should still be conservative about conditional drops")
	}
	for _, l := range c.Layers {
		if l.Table == "nat" && l.Status != "checked" {
			t.Errorf("nat chain %s = %s (%s)", l.Chain, l.Status, l.Reason)
		}
		if l.Table == "mangle" && len(l.Uncertain) == 0 {
			t.Errorf("mangle uncertainty not named: %+v", l)
		}
	}
	sp := gwFlowSpec(ForwardSpec{Name: "web", Ports: "8080", Target: "172.17.0.2", TargetPort: "80"})
	got, err := h.requireWritable(context.Background(), sp)
	if err != nil || !got.Writable {
		t.Fatalf("the modeled flow is clear, so the change is allowed: %v", err)
	}
	if _, err := h.requireWritable(context.Background()); err == nil {
		t.Fatal("without the spec the generic refusal stands")
	}
}

func TestAnExplicitDropOnTheTranslatedPortBlocksAndNamesItsRule(t *testing.T) {
	raw := gwRuleset(t, gwInetForward,
		gwInetRule(1, `[{"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 25}}, {"drop": null}]`),
		gwInetRule(2, `[{"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": {"set": [80, 443]}}}, {"counter": {"packets": 0, "bytes": 0}}, {"drop": null}]`))
	f := gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Name: "web", Ports: "8080", Target: "172.17.0.2", TargetPort: "80"}))
	l := gwLayer(t, f, "local", "forward")
	if f.Verdict != "blocked" || l.Verdict != "blocked" || l.Rule != 2 {
		t.Fatalf("flow %s, layer %+v", f.Verdict, l)
	}
	h := newGwHost(t)
	h.first("nft -t -j list ruleset", raw, nil)
	_, err := h.requireWritable(context.Background(), gwFlowSpec(ForwardSpec{Name: "web", Ports: "8080", Target: "172.17.0.2", TargetPort: "80"}))
	var ro *ReadOnlyError
	if !errors.As(err, &ro) || !strings.Contains(ro.Reason, `"forward"`) || !strings.Contains(ro.Reason, "rule 2") || !strings.Contains(ro.Reason, admitMarkRule) {
		t.Fatalf("err = %v", err)
	}
	// The drop is after translation: the visitor's port 8080 is not what it
	// matches, so a forward to another target port is clear.
	if f := gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "80", Target: "172.17.0.2", TargetPort: "8080"})); f.Verdict != "clear" {
		t.Fatalf("translated port: %+v", f.Layers)
	}
}

func TestSourceDropsAreRestrictionsNotBlockers(t *testing.T) {
	raw := gwRuleset(t, gwInetForward,
		gwInetRule(1, `[{"match": {"op": "==", "left": {"payload": {"protocol": "ip", "field": "saddr"}}, "right": "@crowdsec-blacklists"}}, {"drop": null}]`),
		gwInetRule(2, `[{"match": {"op": "==", "left": {"payload": {"protocol": "ip", "field": "saddr"}}, "right": {"prefix": {"addr": "198.51.100.0", "len": 24}}}}, {"drop": null}]`))
	f := gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "8080", Target: "172.17.0.2"}))
	if l := gwLayer(t, f, "local", "forward"); l.Verdict != "restricted" || f.Verdict != "clear" {
		t.Fatalf("flow %s layer %+v", f.Verdict, l)
	}
	// A forward open only to that network is blocked outright.
	f = gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "8080", Target: "172.17.0.2", Sources: []string{"198.51.100.0/25"}}))
	if f.Verdict != "blocked" {
		t.Fatalf("restricted to the dropped network: %+v", f.Layers)
	}
	// And one open only elsewhere is untouched by rule 2.
	f = gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "8080", Target: "172.17.0.2", Sources: []string{"192.0.2.0/24"}}))
	if l := gwLayer(t, f, "local", "forward"); l.Verdict != "restricted" {
		t.Fatalf("the named set still restricts it: %+v", l)
	}
}

func TestAMarkAcceptInADropPolicyChainAdmitsOnlyWhenTheMarkIsAlreadySet(t *testing.T) {
	mark := `[{"match": {"op": "==", "left": {"&": [{"ct": {"key": "mark"}}, 4278190080]}, "right": 1241513984}}, {"accept": null}]`
	late := `{"chain": {"family": "inet", "table": "local", "name": "forward", "handle": 1, "type": "filter", "hook": "forward", "prio": 0, "policy": "drop"}}`
	raw := gwRuleset(t, late, gwInetRule(1, mark))
	if f := gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "8080", Target: "172.17.0.2"})); f.Verdict != "clear" {
		t.Fatalf("forward after its mark: %+v", f.Layers)
	}
	nat := &Spec{NAT: []NATSpec{{ID: 3, Name: "lab", Source: "10.9.0.0/24", Interface: "eth0", Enabled: true}}}
	if f := gwOneFlow(t, raw, nat); f.Verdict != "clear" {
		t.Fatalf("NAT after its mark at -10: %+v", f.Layers)
	}
	early := `{"chain": {"family": "inet", "table": "local", "name": "forward", "handle": 1, "type": "filter", "hook": "forward", "prio": -50, "policy": "drop"}}`
	raw = gwRuleset(t, early, gwInetRule(1, mark))
	if f := gwOneFlow(t, raw, nat); f.Verdict == "clear" {
		t.Fatalf("a chain before the NAT mark cannot be admitted by it: %+v", f.Layers)
	}
}

func TestJumpsAreFollowedAndUnsupportedFormsStayUnknown(t *testing.T) {
	sub := `{"chain": {"family": "inet", "table": "local", "name": "screen", "handle": 2}}`
	subDrop := `{"rule": {"family": "inet", "table": "local", "chain": "screen", "handle": 9, "expr": [{"match": {"op": "==", "left": {"payload": {"protocol": "th", "field": "dport"}}, "right": {"range": [70, 90]}}}, {"drop": null}]}}`
	raw := gwRuleset(t, gwInetForward, sub, subDrop, gwInetRule(1, `[{"jump": {"target": "screen"}}]`))
	f := gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "8080", Target: "172.17.0.2", TargetPort: "80"}))
	l := gwLayer(t, f, "local", "forward")
	if l.Verdict != "blocked" || !strings.Contains(l.Path, "screen") {
		t.Fatalf("jumped drop: %+v", l)
	}
	raw = gwRuleset(t, gwInetForward, gwInetRule(1, `[{"limit": {"rate": 10, "per": "second"}}, {"drop": null}]`))
	if f := gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "8080", Target: "172.17.0.2"})); f.Verdict != "unknown" {
		t.Fatalf("a rate-limited drop depends on traffic: %+v", f.Layers)
	}
	raw = gwRuleset(t, gwInetForward, gwInetRule(1, `[{"xt": {"type": "match", "name": "recent"}}, {"xt": {"type": "target", "name": "REJECT"}}]`))
	f = gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "8080", Target: "172.17.0.2"}))
	if l := gwLayer(t, f, "local", "forward"); l.Verdict != "unknown" || !strings.Contains(l.Reason, "iptables match recent") {
		t.Fatalf("xt match reason: %+v", l)
	}
}

func TestALocalTargetCrossesInputNotForward(t *testing.T) {
	inputChain := `{"chain": {"family": "inet", "table": "local", "name": "in", "handle": 3, "type": "filter", "hook": "input", "prio": 0, "policy": "accept"}}`
	inputDrop := `{"rule": {"family": "inet", "table": "local", "chain": "in", "handle": 4, "expr": [{"match": {"op": "==", "left": {"payload": {"protocol": "tcp", "field": "dport"}}, "right": 2222}}, {"drop": null}]}}`
	raw := gwRuleset(t, gwInetForward, inputChain, inputDrop, gwInetRule(1, `[{"drop": null}]`))
	f := gwOneFlow(t, raw, gwFlowSpec(ForwardSpec{Ports: "22022", Target: "203.0.113.20", TargetPort: "2222"}))
	if f.Verdict != "blocked" {
		t.Fatalf("local target: %+v", f.Layers)
	}
	for _, l := range f.Layers {
		if l.Hook == "forward" {
			t.Errorf("a local target never crosses the forward hook: %+v", l)
		}
	}
}

func TestMappedNATModelsItsInboundHalf(t *testing.T) {
	raw := gwRuleset(t, gwInetForward, gwInetRule(1, `[{"match": {"op": "==", "left": {"payload": {"protocol": "ip", "field": "daddr"}}, "right": "10.0.4.25"}}, {"drop": null}]`))
	sp := &Spec{NAT: []NATSpec{{ID: 1, Name: "mail", Source: "10.0.4.25/32", Interface: "eth0", Mode: natOneToOne, Translated: "203.0.113.25/32", Enabled: true}}}
	flows := evaluateGatewayFlows(gwListing(t, raw), modelGatewayFlows(sp, gwHostAddrs))
	if len(flows) != 2 || flows[1].Entry != "nat-in:1" || flows[1].Verdict != "blocked" {
		t.Fatalf("flows = %+v", flows)
	}
	if flows[0].Verdict != "clear" {
		t.Fatalf("the outbound half is not addressed to the private host: %+v", flows[0].Layers)
	}
}
