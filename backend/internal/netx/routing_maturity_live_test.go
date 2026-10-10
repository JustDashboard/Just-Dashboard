package netx

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// TestLiveRoutingMaturity drives multipath routes, the extended rule
// selectors, reviewed edits, previews, tunnel anchors and route history
// against a real kernel inside a throwaway namespace.
//
//	JD_NETNS_LIVE=1 go test ./internal/netx/ -run TestLiveRoutingMaturity -v
func TestLiveRoutingMaturity(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	ns.must(t, "ip", "link", "add", "eth0", "type", "dummy")
	ns.must(t, "ip", "link", "set", "eth0", "up")
	ns.must(t, "ip", "addr", "add", "203.0.113.20/24", "dev", "eth0")
	ns.must(t, "ip", "route", "add", "default", "via", "203.0.113.1", "dev", "eth0")
	ns.must(t, "ip", "link", "add", "d1", "type", "dummy")
	ns.must(t, "ip", "link", "set", "d1", "up")
	ns.must(t, "ip", "addr", "add", "192.168.60.1/24", "dev", "d1")
	ns.must(t, "ip", "-6", "addr", "add", "2001:db8:60::1/64", "dev", "d1", "nodad")
	useLive(t, ns)
	rtTableNames(t)
	prevTunnels := tunnelAnchorTargets
	t.Cleanup(func() { tunnelAnchorTargets = prevTunnels })
	tunnelAnchorTargets = func(context.Context) []anchorTarget {
		return []anchorTarget{{label: "the WireGuard endpoint 198.18.0.7 of a peer on wg0", args: []string{"-j", "route", "get", "198.18.0.7"}}}
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := testService(t)
	s.db = st.DB
	ctx := context.Background()
	const client = "198.51.100.9"
	s.observeRoutes(ctx)

	// A multipath route in a table of its own, and the kernel's reading of
	// it recognised as the dashboard's.
	route, err := s.AddRoute(ctx, RouteRequest{Destination: "10.70.0.0/24", Table: 100, Nexthops: []NexthopRequest{
		{Gateway: "192.168.60.2", Device: "d1", Weight: 2}, {Gateway: "192.168.60.3", Device: "d1"},
	}}, client, "live")
	if err != nil {
		t.Fatal(err)
	}
	liveHas(t, "table 100", ns.must(t, "ip", "route", "show", "table", "100"), "nexthop via 192.168.60.2 dev d1 weight 2", "nexthop via 192.168.60.3 dev d1 weight 1")
	v6, err := s.AddRoute(ctx, RouteRequest{Destination: "2001:db8:70::/48", Table: 100, Nexthops: []NexthopRequest{
		{Gateway: "2001:db8:60::2", Device: "d1"}, {Gateway: "2001:db8:60::3", Device: "d1", Weight: 3},
	}}, client, "live")
	if err != nil {
		t.Fatal(err)
	}

	before, err := s.Routing(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if d := before.ClientDecision; d == nil || d.Basis != "kernel_and_model" || d.Table != 254 || d.Device != "eth0" || d.RulePriority == nil || *d.RulePriority != 32766 {
		t.Fatalf("client decision = %+v", d)
	}

	// Rules with every extension, a goto among the dashboard's own rules.
	target, err := s.AddRule(ctx, RuleRequest{Priority: 10050, From: "10.71.0.0/16", Table: 100}, client, "live")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRule(ctx, RuleRequest{UIDRange: "1000-1099", Table: 100}, client, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRule(ctx, RuleRequest{TOS: "0x10", To: "10.70.0.0/24", Table: 100}, client, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRule(ctx, RuleRequest{Family: "inet6", TOS: "EF", Table: 100}, client, "live"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRule(ctx, RuleRequest{L3MDev: true, From: "10.72.0.0/16"}, client, "live"); err != nil {
		t.Fatal(err)
	}
	jump, err := s.AddRule(ctx, RuleRequest{From: "10.71.0.0/24", Action: "goto", Goto: 10050}, client, "live")
	if err != nil {
		t.Fatal(err)
	}
	rules := ns.must(t, "ip", "rule", "show")
	liveHas(t, "rules", rules, "uidrange 1000-1099 lookup 100", "tos 0x10 lookup 100", "l3mdev", "goto 10050")
	liveHas(t, "ipv6 rules", ns.must(t, "ip", "-6", "rule", "show"), "lookup 100")
	if _, err := s.AddRule(ctx, RuleRequest{From: "10.71.0.0/24", Action: "goto", Goto: 10050, Priority: 10060}, client, "live"); err == nil {
		t.Fatal("the kernel's backward-goto refusal is caught before it is asked")
	}
	g := rtGuarded(t, s.DeleteRule(ctx, target.ID, client, "live"))
	if !strings.Contains(g.Reason, "jumps to") {
		t.Fatalf("goto target deletion = %q", g.Reason)
	}

	view, err := s.Routing(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	managedRules := 0
	for _, r := range view.Rules {
		if r.Managed {
			managedRules++
		}
		if r.Priority == jump.Priority && (!r.Managed || r.Action != "goto" || r.Goto != 10050) {
			t.Fatalf("goto rule read back as %+v", r)
		}
	}
	if managedRules != 6 {
		t.Fatalf("recognised %d of the 6 rules made: %+v", managedRules, view.Rules)
	}
	managedRoutes := 0
	for _, table := range view.Tables {
		for _, r := range table.Routes {
			if r.Managed && (r.ID == route.ID || r.ID == v6.ID) {
				managedRoutes++
			}
		}
	}
	if managedRoutes != 2 {
		t.Fatalf("multipath routes recognised = %d", managedRoutes)
	}
	// No socket answers this client inside the namespace, so its UID is
	// unknown; the UID rule's table has no route for it either way, so main
	// still answers and its rule is named.
	if d := view.ClientDecision; d == nil || d.Basis != "kernel_and_model" || d.Table != 254 || d.RulePriority == nil || *d.RulePriority != 32766 {
		t.Fatalf("client decision = %+v", d)
	}

	// The preview's model agrees with the kernel about a UID-selected packet
	// to the multipath route, and about one the UID rule does not select,
	// which the candidate would refuse.
	probe := func(uid uint32) *RuleProbe {
		t.Helper()
		preview, err := s.PreviewRule(ctx, RuleRequest{Priority: 10004, UIDRange: "1500", Action: "prohibit"},
			&RouteTuple{Target: "10.70.0.5", Source: "192.168.60.1", TOS: "0x00", UID: &uid}, client, nil)
		if err != nil {
			t.Fatal(err)
		}
		if preview.Probe == nil || preview.Probe.Kernel == nil {
			t.Fatalf("preview = %+v", preview)
		}
		return preview.Probe
	}
	if p := probe(1050); p.Agreement != "agrees" || p.Now.Table != 100 || p.Kernel.Table != 100 || p.Changes {
		t.Fatalf("multipath probe = %+v", p)
	}
	if p := probe(1500); p.Agreement != "agrees" || p.Now.Table != 254 || p.Kernel.Table != 254 || !p.Changes || p.With.Status != "discard" {
		t.Fatalf("refused probe = %+v", p)
	}

	// Drift reads the same kernel shapes and finds the routes and rules made.
	report := s.Drift(ctx)
	for _, o := range report.Runtime {
		if (o.Domain == "route" || o.Domain == "rule") && o.Status != "matching" {
			t.Errorf("drift %s = %s (%s)", o.ID, o.Status, o.Reason)
		}
	}

	// A reviewed edit: weights in place, then a metric, which changes the
	// kernel's identity of the route.
	if _, err := s.EditRoute(ctx, route.ID, RouteRequest{Destination: "10.70.0.0/24", Table: 100, Nexthops: []NexthopRequest{
		{Gateway: "192.168.60.2", Device: "d1"}, {Gateway: "192.168.60.3", Device: "d1", Weight: 4},
	}}, client, "live"); err != nil {
		t.Fatal(err)
	}
	liveHas(t, "replaced", ns.must(t, "ip", "route", "show", "table", "100"), "nexthop via 192.168.60.3 dev d1 weight 4")
	if _, err := s.EditRoute(ctx, route.ID, RouteRequest{Destination: "10.70.0.0/24", Table: 100, Metric: 30, Gateway: "192.168.60.2", Device: "d1"}, client, "live"); err != nil {
		t.Fatal(err)
	}
	table100 := ns.must(t, "ip", "route", "show", "table", "100")
	liveHas(t, "re-keyed", table100, "10.70.0.0/24 via 192.168.60.2 dev d1 metric 30")
	liveLacks(t, "re-keyed", table100, "nexthop via 192.168.60.3")
	journal, err := readChange(s.paths.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(journal.Validation); journal.Validation == nil || !journal.Validation.Client || !journal.Validation.SourceSelected || len(journal.Validation.Anchors) == 0 {
		t.Fatalf("validation = %s", raw)
	}

	// A discard preview names the anchor it would take; the apply is
	// refused and leaves nothing behind.
	impact, err := s.PreviewRoute(ctx, RouteRequest{Destination: "198.18.0.0/24", Type: "blackhole"}, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	foundTunnel := false
	for _, item := range impact.Affected {
		foundTunnel = foundTunnel || (item.Kind == "tunnel" && item.After == "discarded")
	}
	if !foundTunnel {
		t.Fatalf("impact = %+v", impact)
	}
	_, err = s.AddRoute(ctx, RouteRequest{Destination: "198.18.0.0/24", Gateway: "192.168.60.2", Device: "d1"}, client, "live")
	if g := rtGuarded(t, err); !strings.Contains(g.Reason, "WireGuard endpoint 198.18.0.7") {
		t.Fatalf("anchor refusal = %q", g.Reason)
	}
	liveLacks(t, "main", ns.must(t, "ip", "route", "show"), "198.18.0.0/24")

	// The observer saw every accepted change, and the refused one never
	// reached a reading.
	s.observeRoutes(ctx)
	history, err := s.RouteHistory(ctx, RouteHistoryQuery{Target: "10.70.0.9"})
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]bool{}
	for _, e := range history.Events {
		changes[e.Object+" "+e.Change] = true
	}
	if !changes["route added"] {
		t.Fatalf("history = %+v", history.Events)
	}
	all, _ := s.RouteHistory(ctx, RouteHistoryQuery{Target: "198.18.0.7", Object: "route"})
	for _, e := range all.Events {
		if e.Destination == "198.18.0.0/24" {
			t.Fatalf("a refused route appeared in history: %+v", e)
		}
	}
}
