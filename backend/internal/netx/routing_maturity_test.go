package netx

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rtDSField gives the table-name directories the distribution's rt_dsfield,
// whose names ip prints instead of the numbers.
func rtDSField(t *testing.T) {
	t.Helper()
	rtTableNames(t)
	if err := os.WriteFile(filepath.Join(rtTableDirs[1], "rt_dsfield"), []byte("0x0\tdefault\n0x20\tCS1\n0xB8\tEF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func modelTuple(t *testing.T, dst, src string) routeTuple {
	t.Helper()
	d := netip.MustParseAddr(dst)
	out := routeTuple{family: familyOf(d), dst: d, iif: "lo"}
	if src != "" {
		out.src = netip.MustParseAddr(src)
	}
	return out
}

func TestModelFollowsTheRulesToTheTailnetAndTailscalesMarkToMain(t *testing.T) {
	view := rtBuild(t, rtRoutingSpec())
	m := newModel(view, "inet", false)
	d := m.decide(modelTuple(t, "100.64.0.7", "100.110.34.31"))
	if d.Status != "route" || d.RulePriority == nil || *d.RulePriority != 5270 || d.Table != 52 || d.Route.Device != "tailscale0" {
		t.Fatalf("decision = %+v", d)
	}
	marked := modelTuple(t, "100.64.0.7", "")
	marked.mark = 0x80000
	d = m.decide(marked)
	if d.Status != "route" || *d.RulePriority != 5210 || d.Table != 254 || d.Route.Device != "eth0" {
		t.Fatalf("Tailscale's own packets look in main past its rules: %+v", d)
	}
	if d := m.decide(modelTuple(t, "203.0.113.20", "")); d.Status != "local" || *d.RulePriority != 0 {
		t.Fatalf("the host's own address is answered by the local table: %+v", d)
	}
}

func TestModelNeedsTheSourceForASourceRuleAndSendFindsItLikeTheKernel(t *testing.T) {
	view := rtBuild(t, rtRoutingSpec())
	m := newModel(view, "inet", false)
	if d := m.decide(modelTuple(t, "10.88.0.5", "")); d.Status != "unknown" || !strings.Contains(d.Reason, "source") {
		t.Fatalf("a source rule cannot be decided without a source: %+v", d)
	}
	if d := m.decide(modelTuple(t, "10.88.0.5", "192.168.50.1")); d.Status != "route" || d.Table != 100 || d.Route.Destination != "10.88.0.0/24" {
		t.Fatalf("from the bridge's address the office table answers: %+v", d)
	}
	// An unbound socket's first lookup has no source, so the rule cannot
	// match; the default route's preferred source does not either.
	if d := m.send(netip.MustParseAddr("10.88.0.5"), 0); d.Status != "route" || d.Table != 254 || d.Route.Destination != "default" {
		t.Fatalf("send = %+v", d)
	}
}

func TestModelDiscardsWhereARuleOrARouteDoes(t *testing.T) {
	view := rtBuild(t, rtRoutingSpec())
	m := newModel(view, "inet", false)
	if d := m.send(netip.MustParseAddr("198.51.100.7"), 0); d.Status != "discard" || d.Table != 254 || d.Route.Type != "blackhole" {
		t.Fatalf("the blackhole route discards: %+v", d)
	}
	forwarded := modelTuple(t, "198.51.100.7", "192.168.50.20")
	forwarded.iif = "jd-lan"
	if d := m.decide(forwarded); d.Status != "discard" || *d.RulePriority != 10001 {
		t.Fatalf("arriving on jd-lan, rule 10001 discards: %+v", d)
	}
}

func modelView(rules []RuleEntry, tables ...RoutingTable) *RoutingView {
	return &RoutingView{Rules: rules, Tables: tables}
}

func TestModelGotoJumpsAndAnUnresolvedGotoPasses(t *testing.T) {
	office := RoutingTable{ID: 100, Name: "office", Routes: []RouteEntry{{Family: "inet", Destination: "10.0.0.0/8", Type: "unicast", Device: "wg0"}}}
	main := RoutingTable{ID: 254, Name: "main", Routes: []RouteEntry{{Family: "inet", Destination: "default", Type: "unicast", Device: "eth0", Gateway: "203.0.113.1"}}}
	rules := []RuleEntry{
		{Family: "inet", Priority: 10000, To: "10.0.0.0/8", Action: "goto", Goto: 10010},
		{Family: "inet", Priority: 10005, To: "10.0.0.0/8", Action: "blackhole"},
		{Family: "inet", Priority: 10010, To: "10.0.0.0/8", Action: "lookup", Table: 100},
		{Family: "inet", Priority: 32766, Action: "lookup", Table: 254},
	}
	d := newModel(modelView(rules, office, main), "inet", false).send(netip.MustParseAddr("10.1.2.3"), 0)
	if d.Status != "route" || *d.RulePriority != 10010 || d.Route.Device != "wg0" {
		t.Fatalf("the goto skips the blackhole: %+v", d)
	}
	rules[0].Unresolved = true
	d = newModel(modelView(rules, office, main), "inet", false).send(netip.MustParseAddr("10.1.2.3"), 0)
	if d.Status != "discard" || *d.RulePriority != 10005 {
		t.Fatalf("an unresolved goto passes over: %+v", d)
	}
}

func TestModelSuppressedAndThrowAnswersReturnToTheRules(t *testing.T) {
	zero := 0
	main := RoutingTable{ID: 254, Routes: []RouteEntry{{Family: "inet", Destination: "default", Type: "unicast", Device: "eth0"}}}
	vpn := RoutingTable{ID: 100, Routes: []RouteEntry{
		{Family: "inet", Destination: "default", Type: "unicast", Device: "wg0"},
		{Family: "inet", Destination: "192.0.2.0/24", Type: "throw"},
	}}
	rules := []RuleEntry{
		{Family: "inet", Priority: 10000, Action: "lookup", Table: 254, SuppressPrefixLength: &zero},
		{Family: "inet", Priority: 10001, Action: "lookup", Table: 100},
		{Family: "inet", Priority: 32766, Action: "lookup", Table: 254},
	}
	m := newModel(modelView(rules, main, vpn), "inet", false)
	if d := m.send(netip.MustParseAddr("8.8.8.8"), 0); d.Route == nil || d.Route.Device != "wg0" || d.Steps[0].Result != "suppressed" {
		t.Fatalf("main's default is suppressed, the VPN table answers: %+v", d)
	}
	if d := m.send(netip.MustParseAddr("192.0.2.9"), 0); d.Route == nil || d.Route.Device != "eth0" || *d.RulePriority != 32766 {
		t.Fatalf("a throw route returns to the rules: %+v", d)
	}
}

func TestModelLeavesWhatItCannotEvaluateUnknown(t *testing.T) {
	main := RoutingTable{ID: 254, Routes: []RouteEntry{{Family: "inet", Destination: "default", Type: "unicast", Device: "eth0"}}}
	cases := []struct {
		name string
		rule RuleEntry
		vrf  bool
		want string
	}{
		{"a UID rule without a UID", RuleEntry{UIDRange: "1000-1999", Action: "blackhole"}, false, "unknown"},
		{"a port rule", RuleEntry{IPProto: "tcp", DPort: "443", Action: "blackhole"}, false, "unknown"},
		{"a TOS rule without a TOS", RuleEntry{TOS: "0x10", Action: "blackhole"}, false, "unknown"},
		{"a VRF rule on a host without VRFs", RuleEntry{L3MDev: true, Action: "lookup"}, false, "route"},
		{"a VRF rule where VRFs exist", RuleEntry{L3MDev: true, Action: "lookup"}, true, "unknown"},
		{"an inverted mark rule", RuleEntry{Not: true, FWMark: "0x10", Action: "blackhole"}, false, "discard"},
	}
	for _, c := range cases {
		c.rule.Family, c.rule.Priority = "inet", 10000
		rules := []RuleEntry{c.rule, {Family: "inet", Priority: 32766, Action: "lookup", Table: 254}}
		if d := newModel(modelView(rules, main), "inet", c.vrf).send(netip.MustParseAddr("198.51.100.4"), 0); d.Status != c.want {
			t.Errorf("%s: %+v", c.name, d)
		}
	}
	uid := uint32(1500)
	tuple := modelTuple(t, "198.51.100.4", "")
	tuple.uid = &uid
	rules := []RuleEntry{{Family: "inet", Priority: 10000, UIDRange: "1000-1999", Action: "blackhole"}, {Family: "inet", Priority: 32766, Action: "lookup", Table: 254}}
	if d := newModel(modelView(rules, main), "inet", false).decide(tuple); d.Status != "discard" {
		t.Fatalf("a known UID inside the range is selected: %+v", d)
	}
	// Unknown, the UID forks the walk; the answers differ, so both are kept.
	d := newModel(modelView(rules, main), "inet", false).send(netip.MustParseAddr("198.51.100.4"), 0)
	if len(d.Alternatives) != 2 || d.Alternatives[0].Decision.Status != "discard" || d.Alternatives[1].Decision.Status != "route" {
		t.Fatalf("alternatives = %+v", d.Alternatives)
	}
	// A UID rule looking up a table without a route for the packet cannot
	// change its answer, so the fork collapses.
	rules[0] = RuleEntry{Family: "inet", Priority: 10000, UIDRange: "1000-1999", Action: "lookup", Table: 100}
	d = newModel(modelView(rules, main, RoutingTable{ID: 100}), "inet", false).send(netip.MustParseAddr("198.51.100.4"), 0)
	if d.Status != "route" || *d.RulePriority != 32766 || d.Steps[0].Result != "undecided" {
		t.Fatalf("collapsed fork = %+v", d)
	}
}

const rtExtendedRules = `[{"priority":0,"src":"all","table":"local"},{"priority":10000,"src":"all","uid_start":1000,"uid_end":1999,"table":"100"},{"priority":10001,"src":"all","tos":"0x10","table":"100"},{"priority":10002,"src":"10.0.0.0","srclen":24,"goto":10010},{"priority":10003,"not":null,"src":"10.9.0.0","srclen":16,"table":"100"},{"priority":10004,"src":"all","ipproto":"tcp","dport":443,"table":"100"},{"priority":10005,"src":"all","dst":"10.5.0.0","dstlen":16,"table":"main","suppress_prefixlen":0},{"priority":10008,"src":"all","ipproto":"udp","sport_start":1000,"sport_end":2000,"action":"prohibit"},{"priority":10009,"src":"all","goto":10011,"unresolved":null},{"priority":10010,"src":"all","l3mdev":null},{"priority":32766,"src":"all","table":"main"}]`

func TestRuleExtensionsReadFromIPsJSON(t *testing.T) {
	rtDSField(t)
	rules, err := parseIPRules(rtExtendedRules)
	if err != nil {
		t.Fatal(err)
	}
	byID, byName := rtTables()
	got := map[int]RuleEntry{}
	for _, r := range rules {
		got[r.Priority] = ruleEntry(r, "inet", byID, byName, emptySpec())
	}
	if e := got[10000]; e.UIDRange != "1000-1999" || e.Action != "lookup" || e.Table != 100 {
		t.Errorf("uid rule = %+v", e)
	}
	if e := got[10001]; e.TOS != "0x10" {
		t.Errorf("tos rule = %+v", e)
	}
	if e := got[10002]; e.Action != "goto" || e.Goto != 10010 || e.From != "10.0.0.0/24" {
		t.Errorf("goto rule = %+v", e)
	}
	if e := got[10003]; !e.Not {
		t.Errorf("inverted rule = %+v", e)
	}
	if e := got[10004]; e.IPProto != "tcp" || e.DPort != "443" {
		t.Errorf("port rule = %+v", e)
	}
	if e := got[10005]; e.SuppressPrefixLength == nil || *e.SuppressPrefixLength != 0 || e.Table != 254 {
		t.Errorf("suppress rule = %+v", e)
	}
	if e := got[10008]; e.SPort != "1000-2000" || e.Action != "prohibit" {
		t.Errorf("port range rule = %+v", e)
	}
	if e := got[10009]; !e.Unresolved || e.Goto != 10011 {
		t.Errorf("unresolved goto = %+v", e)
	}
	if e := got[10010]; !e.L3MDev || e.Action != "lookup" || e.Table != 0 {
		t.Errorf("vrf rule = %+v", e)
	}
}

func TestRuleExtensionRequestsAreValidatedForTheirFamily(t *testing.T) {
	rtDSField(t)
	ok := []struct {
		req  RuleRequest
		want RuleSpec
	}{
		{RuleRequest{UIDRange: "1000", Table: 100}, RuleSpec{Family: "inet", UIDRange: "1000-1000", Action: "lookup", Table: 100}},
		{RuleRequest{TOS: "0x10", From: "10.0.0.0/24", Table: 100}, RuleSpec{Family: "inet", TOS: "0x10", From: "10.0.0.0/24", Action: "lookup", Table: 100}},
		{RuleRequest{Family: "inet6", TOS: "EF", Table: 100}, RuleSpec{Family: "inet6", TOS: "0xb8", Action: "lookup", Table: 100}},
		{RuleRequest{From: "10.0.0.0/24", Action: "goto", Goto: 12000}, RuleSpec{Family: "inet", From: "10.0.0.0/24", Action: "goto", Goto: 12000}},
		{RuleRequest{L3MDev: true}, RuleSpec{Family: "inet", L3MDev: true, Action: "lookup"}},
	}
	for _, c := range ok {
		got, err := c.req.spec()
		if err != nil {
			t.Errorf("%+v: %v", c.req, err)
			continue
		}
		if got.Family != c.want.Family || got.UIDRange != c.want.UIDRange || got.TOS != c.want.TOS || got.Action != c.want.Action ||
			got.Table != c.want.Table || got.Goto != c.want.Goto || got.L3MDev != c.want.L3MDev {
			t.Errorf("%+v = %+v, want %+v", c.req, got, c.want)
		}
	}
	bad := []struct {
		req  RuleRequest
		want string
	}{
		{RuleRequest{UIDRange: "5-2", Table: 100}, "upward"},
		{RuleRequest{UIDRange: "4294967295", Table: 100}, "upward"},
		{RuleRequest{UIDRange: "alice", Table: 100}, "not a UID"},
		{RuleRequest{TOS: "0x20", Table: 100}, "IPv4 rule selects TOS"},
		{RuleRequest{TOS: "0x12", Table: 100}, "ECN"},
		{RuleRequest{Family: "inet6", TOS: "0xb9", Table: 100}, "ECN"},
		{RuleRequest{TOS: "nonsense", Table: 100}, "not a TOS"},
		{RuleRequest{From: "10.0.0.0/24", Action: "goto", Goto: 5300}, "continues at a priority from 10000"},
		{RuleRequest{From: "10.0.0.0/24", Action: "goto", Goto: 12000, Table: 100}, "names no table"},
		{RuleRequest{From: "10.0.0.0/24", Goto: 12000, Table: 100}, "only a goto"},
		{RuleRequest{L3MDev: true, Table: 100}, "takes its table from the VRF"},
		{RuleRequest{L3MDev: true, Action: "blackhole"}, "no other action"},
		{RuleRequest{Action: "goto", Goto: 12000}, "no selector"},
		{RuleRequest{TOS: "0", Table: 100}, "any TOS"},
		{RuleRequest{Family: "inet6", TOS: "default", Table: 100}, "any TOS"},
		{RuleRequest{UIDRange: "0-4294967294", Table: 100}, "every UID"},
	}
	for _, c := range bad {
		if _, err := c.req.spec(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err = %v, want %q", c.req, err, c.want)
		}
	}
}

func TestRuleExtensionsRenderAndMatchTheKernelsSpelling(t *testing.T) {
	rtDSField(t)
	cases := map[string]RuleSpec{
		"priority 10000 uidrange 1000-1999 tos 0x10 lookup 100": {Family: "inet", Priority: 10000, UIDRange: "1000-1999", TOS: "0x10", Action: "lookup", Table: 100},
		"priority 10002 from 10.0.0.0/24 goto 10010":            {Family: "inet", Priority: 10002, From: "10.0.0.0/24", Action: "goto", Goto: 10010},
		"priority 10010 l3mdev":                                 {Family: "inet", Priority: 10010, L3MDev: true, Action: "lookup"},
		"priority 10020 tos 0xb8 lookup 100":                    {Family: "inet6", Priority: 10020, TOS: "0xb8", Action: "lookup", Table: 100},
	}
	for want, r := range cases {
		args, err := ruleArgs(r)
		if err != nil || strings.Join(args, " ") != want {
			t.Errorf("%+v = %q %v, want %q", r, strings.Join(args, " "), err, want)
		}
	}
	if _, err := ruleArgs(RuleSpec{Family: "inet", Priority: 10010, From: "10.0.0.0/24", Action: "goto", Goto: 10002}); err == nil {
		t.Error("a backward goto renders")
	}
	if _, err := ruleArgs(RuleSpec{Family: "inet", Priority: 10010, UIDRange: "1;reboot", Action: "lookup", Table: 100}); err == nil {
		t.Error("a hand-edited UID range renders")
	}
	// The kernel reads 0xb8 back as EF; the spec still recognises it.
	sp := emptySpec()
	sp.Rules = []RuleSpec{{ID: 7, Family: "inet6", Priority: 10020, TOS: "0xb8", Action: "lookup", Table: 100}, {ID: 8, Family: "inet", Priority: 10002, From: "10.0.0.0/24", Action: "goto", Goto: 10010}}
	byID, byName := rtTables()
	var raw []ipRule
	if err := json.Unmarshal([]byte(`[{"priority":10020,"src":"all","tos":"EF","table":"100"}]`), &raw); err != nil {
		t.Fatal(err)
	}
	if e := ruleEntry(raw[0], "inet6", byID, byName, sp); !e.Managed || e.ID != 7 {
		t.Fatalf("EF is 0xb8: %+v", e)
	}
	rules, _ := parseIPRules(rtExtendedRules)
	for _, r := range rules {
		if e := ruleEntry(r, "inet", byID, byName, sp); r.Priority == 10002 && (!e.Managed || e.ID != 8) {
			t.Fatalf("the goto is recognised: %+v", e)
		}
	}
}

func TestAddRuleKeepsAGotoAmongTheDashboardsOwnRules(t *testing.T) {
	setup := func(t *testing.T, live string) (*recorder, *Service) {
		rec := rtHost(t).on("ip rule add", "")
		rec.replies = append([]reply{{prefix: "ip -j rule show", out: live}}, rec.replies...)
		s := testService(t)
		sp := emptySpec()
		sp.Rules = []RuleSpec{{ID: 1, Family: "inet", Priority: 10010, From: "10.0.0.0/24", Action: "lookup", Table: 100}}
		sp.NextID = 2
		rtSaveSpec(t, s, sp)
		return rec, s
	}
	managedOnly := `[{"priority":10010,"src":"10.0.0.0","srclen":24,"table":"100"}]`
	rec, s := setup(t, managedOnly)
	r, err := s.AddRule(context.Background(), RuleRequest{From: "10.0.0.0/16", Action: "goto", Goto: 10010}, rtClient, "ion")
	if err != nil || r.Priority != 10000 {
		t.Fatalf("goto = %+v, %v", r, err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip rule add priority 10000 from 10.0.0.0/16 goto 10010" {
		t.Fatalf("mutations = %v", got)
	}

	_, s = setup(t, `[{"priority":10005,"src":"all","fwmark":"0x7","table":"7"},`+managedOnly[1:])
	_, err = s.AddRule(context.Background(), RuleRequest{From: "10.0.0.0/16", Action: "goto", Goto: 10010}, rtClient, "ion")
	if g := rtGuarded(t, err); !strings.Contains(g.Reason, "10005") {
		t.Fatalf("a jump over a foreign rule: %v", g)
	}
	_, s = setup(t, managedOnly)
	if _, err := s.AddRule(context.Background(), RuleRequest{From: "10.0.0.0/16", Action: "goto", Goto: 10500}, rtClient, "ion"); err == nil || !strings.Contains(err.Error(), "no inet rule has priority 10500") {
		t.Fatalf("a goto to no managed rule: %v", err)
	}
	_, s = setup(t, managedOnly)
	if _, err := s.AddRule(context.Background(), RuleRequest{Priority: 10020, From: "10.0.0.0/16", Action: "goto", Goto: 10010}, rtClient, "ion"); err == nil || !strings.Contains(err.Error(), "later priority") {
		t.Fatalf("a backward goto: %v", err)
	}
}

func TestDeleteRuleRefusesARuleAGotoJumpsTo(t *testing.T) {
	rtHost(t)
	s := testService(t)
	sp := emptySpec()
	sp.Rules = []RuleSpec{
		{ID: 1, Family: "inet", Priority: 10000, From: "10.0.0.0/16", Action: "goto", Goto: 10010},
		{ID: 2, Family: "inet", Priority: 10010, From: "10.0.0.0/24", Action: "lookup", Table: 100},
	}
	rtSaveSpec(t, s, sp)
	g := rtGuarded(t, s.DeleteRule(context.Background(), 2, rtClient, "ion"))
	if !strings.Contains(g.Reason, "Remove rule 10000 first") {
		t.Fatalf("reason = %q", g.Reason)
	}
}

func TestShadowsRepliesReadsTheAnsweringSocketsOwners(t *testing.T) {
	rule := RuleSpec{Family: "inet", Priority: 10000, UIDRange: "1000-1999", Action: "blackhole"}
	path := Path{Address: "100.110.34.9", Source: "100.110.34.31", Device: "tailscale0"}
	if !shadowsReplies(rule, path) {
		t.Fatal("without socket evidence a UID discard may select the replies")
	}
	path.replyUIDs = []uint32{0}
	if shadowsReplies(rule, path) {
		t.Fatal("replies from root's sockets are outside 1000-1999")
	}
	path.replyUIDs = []uint32{0, 1500}
	if !shadowsReplies(rule, path) {
		t.Fatal("one answering socket inside the range is enough")
	}
	// A goto discards nothing; what its path does is verified after it runs.
	if shadowsReplies(RuleSpec{Family: "inet", Priority: 10000, TOS: "0x10", Action: "goto", Goto: 10050}, Path{Address: "100.110.34.9", Source: "100.110.34.31"}) {
		t.Fatal("a goto was judged a discard")
	}
	if got := parseSocketUIDs("0 0 100.110.34.31:443 100.110.34.9:5123 ino:1 sk:2 <->\n0 0 100.110.34.31:22 100.110.34.9:6000 uid:1500 ino:3 <->\n"); len(got) != 2 || got[0] != 0 || got[1] != 1500 {
		t.Fatalf("uids = %v", got)
	}
}

func TestMultipathRoutesValidateAndRenderWithEveryAttributeFirst(t *testing.T) {
	req := RouteRequest{Destination: "10.6.0.0/24", Table: 100, Metric: 5, Nexthops: []NexthopRequest{
		{Gateway: "192.168.50.2", Device: "jd-lan", Weight: 2}, {Gateway: "192.168.50.3", Weight: 1},
	}}
	r, err := req.spec()
	if err != nil {
		t.Fatal(err)
	}
	args, err := routeArgs(r)
	if err != nil || strings.Join(args, " ") != "10.6.0.0/24 table 100 metric 5 nexthop via 192.168.50.2 dev jd-lan weight 2 nexthop via 192.168.50.3" {
		t.Fatalf("args = %v %v", args, err)
	}
	def, err := RouteRequest{Destination: "default", Table: 200, Nexthops: []NexthopRequest{{Gateway: "2001:db8::1"}, {Gateway: "2001:db8::2"}}}.spec()
	if err != nil || def.Family != "inet6" {
		t.Fatalf("an IPv6 multipath default = %+v %v", def, err)
	}
	if cmd, _ := routeCommand("add", def); strings.Join(cmd, " ") != "-6 route add ::/0 table 200 nexthop via 2001:db8::1 nexthop via 2001:db8::2" {
		t.Fatalf("command = %v", cmd)
	}
	bad := []struct {
		req  RouteRequest
		want string
	}{
		{RouteRequest{Destination: "10.6.0.0/24", Nexthops: []NexthopRequest{{Gateway: "192.168.50.2"}}}, "2 to 16"},
		{RouteRequest{Destination: "10.6.0.0/24", Nexthops: []NexthopRequest{{Gateway: "192.168.50.2"}, {Gateway: "2001:db8::2"}}}, "not an inet"},
		{RouteRequest{Destination: "10.6.0.0/24", Nexthops: []NexthopRequest{{Gateway: "192.168.50.2"}, {Gateway: "192.168.50.2"}}}, "twice"},
		{RouteRequest{Destination: "10.6.0.0/24", Nexthops: []NexthopRequest{{Gateway: "192.168.50.2", Weight: 300}, {Gateway: "192.168.50.3"}}}, "weight"},
		{RouteRequest{Destination: "10.6.0.0/24", Gateway: "192.168.50.9", Nexthops: []NexthopRequest{{Gateway: "192.168.50.2"}, {Gateway: "192.168.50.3"}}}, "in its nexthops"},
		{RouteRequest{Destination: "10.6.0.0/24", Type: "blackhole", Nexthops: []NexthopRequest{{Gateway: "192.168.50.2"}, {Gateway: "192.168.50.3"}}}, "no nexthops"},
		{RouteRequest{Destination: "10.6.0.0/24", Nexthops: []NexthopRequest{{}, {Gateway: "192.168.50.3"}}}, "every nexthop"},
	}
	for _, c := range bad {
		if _, err := c.req.spec(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err = %v, want %q", c.req, err, c.want)
		}
	}
}

func TestMultipathRoutesAreRecognisedLegByLeg(t *testing.T) {
	sp := rtRoutingSpec()
	sp.Routes = append(sp.Routes, RouteSpec{ID: 15, Family: "inet", Destination: "10.5.0.0/16", Type: "unicast", Table: 254, Metric: 20,
		Nexthops: []NexthopSpec{{Gateway: "192.168.50.3", Device: "jd-lan"}, {Gateway: "192.168.50.2"}}})
	view := rtBuild(t, sp)
	found := false
	for _, r := range view.Tables[0].Routes {
		if r.Destination == "10.5.0.0/16" {
			found = r.Managed && r.ID == 15
		}
	}
	if !found {
		t.Fatal("the multipath route is the dashboard's, its legs in any order")
	}
	sp.Routes[len(sp.Routes)-1].Nexthops[0].Weight = 3
	for _, r := range rtBuild(t, sp).Tables[0].Routes {
		if r.Destination == "10.5.0.0/16" && r.Managed {
			t.Fatal("a different weight is a different route")
		}
	}
}

func TestAddMultipathRouteChecksEveryLegsDevice(t *testing.T) {
	rec := rtHost(t).on("ip route add", "")
	s := testService(t)
	_, err := s.AddRoute(context.Background(), RouteRequest{Destination: "10.6.0.0/24", Nexthops: []NexthopRequest{
		{Gateway: "192.168.50.2", Device: "jd-lan"}, {Gateway: "192.168.50.3", Device: "nope0"},
	}}, rtClient, "ion")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("a missing leg device: %v", err)
	}
	r, err := s.AddRoute(context.Background(), RouteRequest{Destination: "10.6.0.0/24", Nexthops: []NexthopRequest{
		{Gateway: "192.168.50.2", Device: "jd-lan", Weight: 2}, {Gateway: "192.168.50.3", Device: "jd-lan"},
	}}, rtClient, "ion")
	if err != nil || len(r.Nexthops) != 2 {
		t.Fatalf("route = %+v %v", r, err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip route add 10.6.0.0/24 nexthop via 192.168.50.2 dev jd-lan weight 2 nexthop via 192.168.50.3 dev jd-lan" {
		t.Fatalf("mutations = %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile)); !strings.Contains(string(b), "route add 10.6.0.0/24 nexthop via 192.168.50.2 dev jd-lan weight 2 nexthop via 192.168.50.3 dev jd-lan") {
		t.Fatalf("the boot file lacks the multipath route:\n%s", b)
	}
}

func TestDriftComparesAMultipathRoutesLegs(t *testing.T) {
	rtTableNames(t)
	want := RouteSpec{ID: 15, Family: "inet", Destination: "10.5.0.0/16", Type: "unicast", Table: 254, Metric: 20,
		Nexthops: []NexthopSpec{{Gateway: "192.168.50.2"}, {Gateway: "192.168.50.3"}}}
	entry := json.RawMessage(`{"dst":"10.5.0.0/16","protocol":"static","metric":20,"flags":[],"nexthops":[{"gateway":"192.168.50.2","dev":"jd-lan","weight":1,"flags":[]},{"gateway":"192.168.50.3","dev":"jd-lan","weight":1,"flags":[]}]}`)
	if o := driftRoute(want, []json.RawMessage{entry}, nil); o.Status != "matching" {
		t.Fatalf("matching multipath = %+v", o)
	}
	want.Nexthops = want.Nexthops[:1]
	want.Gateway = "192.168.50.2"
	want.Nexthops = nil
	if o := driftRoute(want, []json.RawMessage{entry}, nil); o.Status != "conflict" {
		t.Fatalf("a single-path spec against a multipath kernel route = %+v", o)
	}
}

func rtEditSpec() *Spec {
	sp := emptySpec()
	sp.Routes = []RouteSpec{
		{ID: 3, Family: "inet", Destination: "10.88.0.0/24", Type: "unicast", Gateway: "192.168.50.2", Device: "jd-lan", Table: 100, Metric: 50},
		{ID: 4, Family: "inet", Destination: "10.89.0.0/24", Type: "unicast", Gateway: "192.168.50.2", Device: "jd-lan", Table: 100},
	}
	sp.NextID = 5
	return sp
}

func TestRoutePlansReplaceInPlaceOrAddBeforeRemoving(t *testing.T) {
	rec := rtHost(t)
	rec.on("ip -j route show table all", "[]").on("ip -j -6 route show table all", "[]").on("ip -j -6 rule show", "[]").
		on("ip -j -d link show type vrf", "[]").on("ss -Htn", "")
	s := testService(t)
	rtSaveSpec(t, s, rtEditSpec())
	plan, err := s.PlanRouteEdit(context.Background(), 3, RouteRequest{Destination: "10.88.0.0/24", Gateway: "192.168.50.3", Device: "jd-lan", Table: 100, Metric: 50}, rtClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Replace || len(plan.Commands) != 1 || plan.Commands[0] != "ip route replace 10.88.0.0/24 via 192.168.50.3 dev jd-lan table 100 metric 50" ||
		strings.Join(plan.Changes, "|") != "via: 192.168.50.2 → 192.168.50.3" {
		t.Fatalf("in-place plan = %+v", plan)
	}
	plan, err = s.PlanRouteEdit(context.Background(), 3, RouteRequest{Destination: "10.88.0.0/24", Gateway: "192.168.50.2", Device: "jd-lan", Table: 100, Metric: 10}, rtClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Replace || len(plan.Commands) != 2 || !strings.HasPrefix(plan.Commands[0], "ip route add 10.88.0.0/24") || !strings.HasPrefix(plan.Commands[1], "ip route del 10.88.0.0/24") {
		t.Fatalf("identity-changing plan = %+v", plan)
	}
	if _, err := s.PlanRouteEdit(context.Background(), 3, RouteRequest{Destination: "10.88.0.0/24", Gateway: "192.168.50.2", Device: "jd-lan", Table: 100, Metric: 50}, rtClient, nil); err == nil || !strings.Contains(err.Error(), "changes nothing") {
		t.Fatalf("an empty edit: %v", err)
	}
	if _, err := s.PlanRouteEdit(context.Background(), 3, RouteRequest{Destination: "10.89.0.0/24", Gateway: "192.168.50.2", Table: 100}, rtClient, nil); !errors.Is(err, ErrExists) {
		t.Fatalf("an edit onto route 4: %v", err)
	}
	if _, err := s.PlanRouteEdit(context.Background(), 99, RouteRequest{Destination: "10.89.0.0/24", Gateway: "192.168.50.2"}, rtClient, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown route: %v", err)
	}
	if rtMutations(rec) != nil {
		t.Fatalf("a plan changed something: %v", rtMutations(rec))
	}
}

func TestEditRouteReplacesAndTakesItBackWhenTheReplyMoves(t *testing.T) {
	rec := rtHost(t).on("ip route replace", "").
		on("ip -j route show table 100 exact 10.88.0.0/24", `[{"dst":"10.88.0.0/24","gateway":"192.168.50.2","dev":"jd-lan","metric":50,"flags":[]}]`)
	s := testService(t)
	rtSaveSpec(t, s, rtEditSpec())
	r, err := s.EditRoute(context.Background(), 3, RouteRequest{Destination: "10.88.0.0/24", Gateway: "192.168.50.3", Device: "jd-lan", Table: 100, Metric: 50, Comment: "second hop"}, rtClient, "ion")
	if err != nil || r.ID != 3 || r.Gateway != "192.168.50.3" {
		t.Fatalf("edit = %+v %v", r, err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip route replace 10.88.0.0/24 via 192.168.50.3 dev jd-lan table 100 metric 50" {
		t.Fatalf("mutations = %v", got)
	}
	if sp := rtLoad(t, s); sp.Routes[0].Gateway != "192.168.50.3" || sp.Routes[0].Comment != "second hop" {
		t.Fatalf("spec = %+v", sp.Routes)
	}
	journal, err := readChange(s.paths.Dir)
	if err != nil || journal.Validation == nil || !journal.Validation.Client || !journal.Validation.SourceSelected {
		t.Fatalf("validation evidence = %+v %v", journal, err)
	}

	rec = rtHost(t).on("ip route add", "").on("ip route del", "")
	rtAnswerAfter(rec, "ip route add", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
	s = testService(t)
	rtSaveSpec(t, s, rtEditSpec())
	_, err = s.EditRoute(context.Background(), 3, RouteRequest{Destination: "100.110.0.0/16", Gateway: "203.0.113.1", Device: "eth0"}, rtClient, "ion")
	rtGuarded(t, err)
	got := strings.Join(rtMutations(rec), "|")
	if got != "ip route add 100.110.0.0/16 via 203.0.113.1 dev eth0|ip route del 10.88.0.0/24 via 192.168.50.2 dev jd-lan table 100 metric 50|ip route del 100.110.0.0/16 via 203.0.113.1 dev eth0|ip route add 10.88.0.0/24 via 192.168.50.2 dev jd-lan table 100 metric 50" {
		t.Fatalf("add, remove, then the undo in reverse: %s", got)
	}
	if sp := rtLoad(t, s); sp.Routes[0].Destination != "10.88.0.0/24" {
		t.Fatalf("a refused edit left the spec changed: %+v", sp.Routes)
	}
}

func TestEditRouteDoesNotReplaceARouteThatDriftedToAnotherOwner(t *testing.T) {
	rec := rtHost(t).
		on("ip -j route show table 100 exact 10.88.0.0/24", `[{"dst":"10.88.0.0/24","gateway":"192.168.50.9","dev":"jd-lan","metric":50,"protocol":"static","flags":[]},{"dst":"10.88.0.0/24","dev":"jd-lan","metric":700,"flags":[]}]`)
	s := testService(t)
	rtSaveSpec(t, s, rtEditSpec())
	_, err := s.EditRoute(context.Background(), 3, RouteRequest{Destination: "10.88.0.0/24", Gateway: "192.168.50.3", Device: "jd-lan", Table: 100, Metric: 50}, rtClient, "ion")
	if g := rtGuarded(t, err); !strings.Contains(g.Reason, "via 192.168.50.9") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if rtMutations(rec) != nil {
		t.Fatalf("mutations = %v", rtMutations(rec))
	}
}

func TestEditRouteRefusesASecondMainDefault(t *testing.T) {
	rec := rtHost(t)
	s := testService(t)
	rtSaveSpec(t, s, rtEditSpec())
	_, err := s.EditRoute(context.Background(), 3, RouteRequest{Destination: "default", Gateway: "192.168.50.3", Device: "jd-lan"}, rtClient, "ion")
	if g := rtGuarded(t, err); !strings.Contains(g.Reason, "already has a default route via 203.0.113.1") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if rtMutations(rec) != nil {
		t.Fatalf("mutations = %v", rtMutations(rec))
	}
}

func TestRoutingEvidenceListsFlowsTheChangeMoved(t *testing.T) {
	rec := record(t).
		on("ss -Htn state established", "0 0 203.0.113.20:5432 198.51.100.40:51000\n0 0 203.0.113.20:443 [2001:db8::40]:4430\n0 0 127.0.0.1:1 127.0.0.1:2\n").
		on("ip -j route get 198.51.100.40", `[{"dst":"198.51.100.40","gateway":"203.0.113.1","dev":"eth0"}]`).
		on("ip -j -6 route get 2001:db8::40", `[{"dst":"2001:db8::40","dev":"eth0"}]`)
	flows := readFlowAnchors(context.Background())
	if len(flows) != 2 {
		t.Fatalf("flows = %+v", flows)
	}
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "ip -j route get 198.51.100.40", out: `[{"dst":"198.51.100.40","gateway":"10.8.0.10","dev":"wg0"}]`}}, rec.replies...)
	rec.mu.Unlock()
	v := routingEvidence(Path{Address: "100.110.34.9", Source: "100.110.34.31", anchors: []anchorPath{{label: "the internet"}}}, flows)(context.Background())
	if !v.Client || !v.SourceSelected || v.Flows != 2 || len(v.Anchors) != 1 || len(v.Moved) != 1 ||
		v.Moved[0].Address != "198.51.100.40" || v.Moved[0].After != "through wg0 via 10.8.0.10" {
		t.Fatalf("validation = %+v", v)
	}
}

func TestTailscaleDirectPathsAreAnchorsAskedWithTailscaledsMark(t *testing.T) {
	record(t).
		on("wg show all dump", "wg0\tPRIV\tPUB\t51820\t0xca6c\nwg0\tPEERA\t(none)\t203.0.113.77:51820\t10.8.0.2/32\t0\t0\t0\toff\nwg1\tPRIV\tPUB\t51821\toff\nwg1\tPEERB\t(none)\t[2001:db8::77]:51821\t10.9.0.2/32\t0\t0\t0\toff\n").
		on("tailscale status --json", `{"Peer":{"k1":{"HostName":"laptop","CurAddr":"198.51.100.9:41641"},"k2":{"HostName":"relayed","CurAddr":""}}}`)
	targets := readTunnelAnchorTargets(context.Background())
	got := []string{}
	for _, a := range targets {
		got = append(got, strings.Join(a.args, " ")+" = "+a.label)
	}
	// WireGuard transports are wgTransportAnchors' (wgrecord_test.go), so
	// the dump is not read twice and a captured transport stays repairable.
	want := []string{
		"-j route get 198.51.100.9 mark 0x80000 = Tailscale's direct path to laptop (198.51.100.9)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("targets:\n%s", strings.Join(got, "\n"))
	}
}

func TestATunnelEndpointMovedByAChangeIsRefusedAndRoamingIsNot(t *testing.T) {
	// Without wg, wgTransportAnchors adds nothing beside the injected target.
	rec := record(t, "wg")
	anchorPaths = readAnchors
	tunnelAnchorTargets = func(context.Context) []anchorTarget {
		return []anchorTarget{{label: "the WireGuard endpoint 203.0.113.77 of a peer on wg0", args: []string{"-j", "route", "get", "203.0.113.77", "mark", "0xca6c"}}}
	}
	rec.fail("ip -j route get 1.1.1.1", "unreachable").fail("ip -j -6 route get", "unreachable").
		on("ip -j route get 203.0.113.77 mark 0xca6c", `[{"dst":"203.0.113.77","gateway":"203.0.113.1","dev":"eth0"}]`)
	before, _ := clientPath(context.Background(), "127.0.0.1")
	if len(before.anchors) != 1 {
		t.Fatalf("anchors = %+v", before.anchors)
	}
	// The endpoint roams: discovery would now find another address, but the
	// check asks the question it was read with.
	tunnelAnchorTargets = func(context.Context) []anchorTarget { return nil }
	if err := verifyPath(before)(context.Background()); err != nil {
		t.Fatalf("an unchanged endpoint path: %v", err)
	}
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "ip -j route get 203.0.113.77", out: `[{"dst":"203.0.113.77","gateway":"10.8.0.10","dev":"wg9"}]`}}, rec.replies...)
	rec.mu.Unlock()
	if err := verifyPath(before)(context.Background()); err == nil || !strings.Contains(err.Error(), "WireGuard endpoint 203.0.113.77") {
		t.Fatalf("a moved endpoint: %v", err)
	}
}
