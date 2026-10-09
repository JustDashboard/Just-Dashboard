package netx

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func rtReadHost(t *testing.T, routeGet string) *recorder {
	t.Helper()
	rtTableNames(t)
	return record(t).
		on("ip -j route show table all", fixture(t, "routing-route4.json")).
		on("ip -j -6 route show table all", fixture(t, "routing-route6.json")).
		on("ip -j rule show", fixture(t, "routing-rule4.json")).
		on("ip -j -6 rule show", fixture(t, "routing-rule6.json")).
		on("ip -j route get", routeGet).
		on("ip -j -d link show type vrf", "[{}]").
		on("ss -Htne state established dst", "").
		on("ss -Htn state established", "")
}

func TestClientDecisionNamesTheKernelsTableAndTheEvaluatedRule(t *testing.T) {
	rtReadHost(t, `[{"dst":"100.64.0.7","dev":"tailscale0","table":"52","prefsrc":"100.110.34.31","flags":[],"uid":0,"cache":[]}]`)
	s := testService(t)
	rtSaveSpec(t, s, rtRoutingSpec())
	view, err := s.Routing(context.Background(), "100.64.0.7")
	if err != nil {
		t.Fatal(err)
	}
	d := view.ClientDecision
	if d == nil || d.Basis != "kernel_and_model" || d.Table != 52 || d.Device != "tailscale0" || d.RulePriority == nil || *d.RulePriority != 5270 {
		t.Fatalf("decision = %+v", d)
	}
	if len(d.Candidates) != 1 || d.Candidates[0] != 5270 || len(d.Steps) == 0 {
		t.Fatalf("candidates = %v steps = %+v", d.Candidates, d.Steps)
	}
}

func TestClientDecisionShowsTheKernelWhenTheModelDisagreesOrCannotDecide(t *testing.T) {
	// The kernel names no table, so main answered, while the rules lead the
	// model to Tailscale's.
	rtReadHost(t, fixture(t, "routing-route-get.json"))
	s := testService(t)
	view, err := s.Routing(context.Background(), "100.64.0.7")
	if err != nil {
		t.Fatal(err)
	}
	if d := view.ClientDecision; d == nil || d.Basis != "disagree" || d.Table != 254 || d.RulePriority != nil {
		t.Fatalf("decision = %+v", d)
	}

	rec := rtReadHost(t, `[{"dst":"203.0.113.9","gateway":"203.0.113.1","dev":"eth0","prefsrc":"203.0.113.20","flags":[]}]`)
	rec.replies = append([]reply{{prefix: "ip -j rule show", out: `[{"priority":0,"src":"all","table":"local"},{"priority":10000,"src":"all","uid_start":1000,"uid_end":1999,"table":"isp2"},{"priority":32766,"src":"all","table":"main"}]`}}, rec.replies...)
	s = testService(t)
	view, err = s.Routing(context.Background(), "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if d := view.ClientDecision; d == nil || d.Basis != "kernel" || d.Table != 254 || !strings.Contains(d.Reason, "UID") || len(d.Candidates) != 1 || d.Candidates[0] != 32766 {
		t.Fatalf("an undecidable UID rule leaves the kernel's table and its candidates: %+v", d)
	}
	// A UID rule whose table has no route for the client does not matter:
	// either way main answers, so the rule is named.
	rec.replies[0].out = strings.Replace(rec.replies[0].out, `"table":"isp2"`, `"table":"100"`, 1)
	view, err = s.Routing(context.Background(), "203.0.113.9")
	if err != nil {
		t.Fatal(err)
	}
	if d := view.ClientDecision; d == nil || d.Basis != "kernel_and_model" || *d.RulePriority != 32766 {
		t.Fatalf("a UID rule that cannot change the answer: %+v", d)
	}
}

func TestClientDecisionIsAbsentForALocalClient(t *testing.T) {
	rtReadHost(t, fixture(t, "routing-route-get.json"))
	s := testService(t)
	view, err := s.Routing(context.Background(), "127.0.0.1")
	if err != nil || view.ClientDecision != nil {
		t.Fatalf("decision = %+v %v", view.ClientDecision, err)
	}
}

func TestVRFListingIgnoresTheEmptyObjectsIPPrintsForFilteredLinks(t *testing.T) {
	record(t).on("ip -j -d link show type vrf", `[{},{"ifname":"vrf-blue","linkinfo":{"info_kind":"vrf","info_data":{"table":1001}}},{}]`)
	vrfs, read := readVRFs(context.Background())
	if !read || len(vrfs) != 1 || vrfs[0].Name != "vrf-blue" || vrfs[0].Table != 1001 {
		t.Fatalf("vrfs = %+v %v", vrfs, read)
	}
}

func TestRouteImpactListsWhatADiscardRouteWouldTakeAway(t *testing.T) {
	rec := rtReadHost(t, fixture(t, "routing-route-get.json"))
	rec.replies = append([]reply{{prefix: "ss -Htn state established", out: "0 0 203.0.113.20:5432 10.0.1.40:51000\n0 0 203.0.113.20:22 198.18.0.4:40000\n"}}, rec.replies...)
	s := testService(t)
	sp := rtRoutingSpec()
	sp.Forwards = []ForwardSpec{{ID: 20, Name: "postgres", Protocol: "tcp", Ports: "5432", Target: "10.0.1.40", Enabled: true}}
	rtSaveSpec(t, s, sp)
	impact, err := s.PreviewRoute(context.Background(), RouteRequest{Destination: "10.0.0.0/8", Type: "blackhole"}, rtClient,
		[]ImpactCandidate{{Kind: "docker", Name: "the Docker network db", Address: "10.0.1.0/24"}, {Kind: "docker", Name: "the Docker network edge", Address: "10.200.0.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	affected := map[string]ImpactItem{}
	for _, item := range impact.Affected {
		affected[item.Kind+" "+item.Address] = item
	}
	// The connected 10.0.1.0/24 route is more specific and keeps its
	// traffic; the unrouted 10.200.0.0/24 network would be discarded.
	if _, ok := affected["docker 10.200.0.1"]; !ok {
		t.Fatalf("affected = %+v", impact.Affected)
	}
	if item, ok := affected["docker 10.200.0.1"]; !ok || item.After != "discarded" || !strings.Contains(item.Before, "default") {
		t.Fatalf("the Docker network's change = %+v", item)
	}
	for key := range affected {
		if strings.Contains(key, "10.0.1.") {
			t.Fatalf("a more specific route keeps %s: %+v", key, impact.Affected)
		}
	}
	if impact.ClientAffected || impact.Unaffected == 0 || len(impact.Limits) == 0 {
		t.Fatalf("impact = %+v", impact)
	}
	if rtMutations(rec) != nil {
		t.Fatalf("a preview changed something: %v", rtMutations(rec))
	}
}

func TestRouteImpactFlagsTheClientAndLeavesOtherTablesAlone(t *testing.T) {
	rtReadHost(t, fixture(t, "routing-route-get.json"))
	s := testService(t)
	rtSaveSpec(t, s, rtRoutingSpec())
	impact, err := s.PreviewRoute(context.Background(), RouteRequest{Destination: "100.64.0.0/10", Type: "prohibit", Table: 52 + 48}, rtClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if impact.ClientAffected || len(impact.Affected) != 0 {
		t.Fatalf("a route in table 100, which only the bridge's sources reach, moves nothing sent from here: %+v", impact)
	}
	// Table 52 holds a /32 for the client only through Tailscale's own
	// table; a main-table prohibit never sees it. A blackhole of the
	// client's own address in main does not either, the rules reach 52
	// first. A rule-less table entry is the case that would.
	impact, err = s.PreviewRoute(context.Background(), RouteRequest{Destination: "0.0.0.0/1", Type: "blackhole"}, "45.0.0.5", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !impact.ClientAffected {
		t.Fatalf("a half-default blackhole takes this client's replies: %+v", impact)
	}
}

func TestRulePreviewFindsShadowsAndChecksItsModelAgainstTheKernel(t *testing.T) {
	rec := rtReadHost(t, `[{"dst":"172.16.9.5","gateway":"192.168.50.2","dev":"jd-lan","prefsrc":"192.168.50.1","flags":[]}]`)
	s := testService(t)
	rtSaveSpec(t, s, rtRoutingSpec())
	preview, err := s.PreviewRule(context.Background(), RuleRequest{Priority: 10005, From: "192.168.50.0/25", Action: "prohibit"},
		&RouteTuple{Target: "172.16.9.5", Source: "192.168.50.1"}, rtClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Rule.Priority != 10005 || preview.After == nil || preview.After.Priority != 10001 || preview.Before == nil || preview.Before.Priority != 12000 {
		t.Fatalf("position = %+v / %+v / %+v", preview.Rule, preview.After, preview.Before)
	}
	// Rule 10000 already selects 192.168.50.0/24 but table 100 has no route
	// for everything, so it is not a full shadow; nothing earlier discards.
	if len(preview.ShadowedBy) != 0 {
		t.Fatalf("shadowed by = %+v", preview.ShadowedBy)
	}
	// The office table has no route for it, so main answers now, as the
	// kernel does; with the rule in place the packet is refused.
	if p := preview.Probe; p == nil || p.Agreement != "agrees" || !p.Changes || p.Now.Table != 254 || p.With.Status != "discard" || *p.With.RulePriority != 10005 {
		t.Fatalf("probe = %+v", preview.Probe)
	}
	if rtMutations(rec) != nil {
		t.Fatalf("a preview changed something: %v", rtMutations(rec))
	}

	// An earlier discard selecting everything the candidate does shadows
	// it; the candidate in turn decides a later rule it fully covers.
	preview, err = s.PreviewRule(context.Background(), RuleRequest{Priority: 10002, To: "198.51.100.128/25", IIF: "jd-lan", Action: "lookup", Table: 200}, nil, rtClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.ShadowedBy) != 1 || preview.ShadowedBy[0].Priority != 10001 || !strings.Contains(preview.ShadowedBy[0].Reason, "discards") {
		t.Fatalf("shadowed by = %+v", preview.ShadowedBy)
	}
	preview, err = s.PreviewRule(context.Background(), RuleRequest{Priority: 11000, FWMark: "0x10/0x10", Action: "blackhole"}, nil, rtClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Shadows) != 1 || preview.Shadows[0].Priority != 12000 {
		t.Fatalf("a 0x10/0x10 discard decides the exact-0x10 rule 12000: %+v", preview.Shadows)
	}
}

func TestRulePreviewCarriesTheGuardAnAddWouldAnswer(t *testing.T) {
	rtReadHost(t, fixture(t, "routing-route-get.json"))
	s := testService(t)
	preview, err := s.PreviewRule(context.Background(), RuleRequest{To: "100.110.34.0/24", Action: "blackhole"}, nil, rtClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.Refusal, "discard the replies") || preview.Rule.Priority != 10002 {
		t.Fatalf("preview = %+v", preview)
	}
	if !preview.Impact.ClientAffected {
		t.Fatalf("the model agrees the client is affected: %+v", preview.Impact)
	}
}

func TestRouteHistoryDiffsReadingsAndBoundsEachChange(t *testing.T) {
	before := map[string]historyItem{
		"route|inet|254|10.9.0.0/24|0":  {Object: "route", Family: "inet", Table: 254, Destination: "10.9.0.0/24", Owner: "system", Summary: "10.9.0.0/24 dev eth1"},
		"route|inet|254|10.8.0.0/24|0":  {Object: "route", Family: "inet", Table: 254, Destination: "10.8.0.0/24", Owner: "wireguard", Summary: "10.8.0.0/24 dev wg0"},
		"rule|inet|10000|from 10.0.0.0": {Object: "rule", Family: "inet", Summary: "10000 from 10.0.0.0/24 lookup 100"},
	}
	after := map[string]historyItem{
		"route|inet|254|10.9.0.0/24|0": {Object: "route", Family: "inet", Table: 254, Destination: "10.9.0.0/24", Owner: "system", Summary: "10.9.0.0/24 dev eth1 linkdown"},
		"route|inet|100|default|0":     {Object: "route", Family: "inet", Table: 100, Destination: "default", Owner: "just-dashboard", Managed: true, Summary: "default via 10.8.0.10 dev wg0"},
	}
	t0, t1 := time.Unix(1000, 0).UTC(), time.Unix(1030, 0).UTC()
	events := diffRouting(before, after, t0, t1, false)
	got := []string{}
	for _, e := range events {
		got = append(got, e.Change+" "+e.Object+" "+e.Destination)
		if !e.PreviousAt.Equal(t0) || !e.ObservedAt.Equal(t1) {
			t.Fatalf("bounds = %+v", e)
		}
	}
	if strings.Join(got, "|") != "added route default|removed route 10.8.0.0/24|changed route 10.9.0.0/24|removed rule " {
		t.Fatalf("events = %v", got)
	}
}

func TestRouteSummaryIgnoresAnRARoutesCountdown(t *testing.T) {
	a := routeSummary(RouteEntry{Destination: "default", Type: "unicast", Gateway: "fe80::1", Device: "eth0", Protocol: "ra", Metric: 1024, Flags: []string{}})
	b := routeSummary(RouteEntry{Destination: "default", Type: "unicast", Gateway: "fe80::1", Device: "eth0", Protocol: "ra", Metric: 1024, Flags: []string{"offload"}})
	if a != b || a != "default via fe80::1 dev eth0 proto ra metric 1024" {
		t.Fatalf("%q / %q", a, b)
	}
}

func historyService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := testService(t)
	s.db = st.DB
	return s, st.DB
}

func TestRouteHistoryRecordsReadingsAcrossARestart(t *testing.T) {
	rec := rtReadHost(t, fixture(t, "routing-route-get.json"))
	s, db := historyService(t)
	s.observeRoutes(context.Background())
	h, err := s.RouteHistory(context.Background(), RouteHistoryQuery{})
	if err != nil || len(h.Events) != 0 || !h.Running || h.Since == nil {
		t.Fatalf("the first reading is a baseline: %+v %v", h, err)
	}
	route4 := fixture(t, "routing-route4.json")
	changed := strings.Replace(route4, `{"dst":"10.8.0.0/24","dev":"wg0","protocol":"kernel","scope":"link","prefsrc":"10.8.0.1","flags":[]},`, "", 1)
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "ip -j route show table all", out: changed}}, rec.replies...)
	rec.mu.Unlock()
	s.observeRoutes(context.Background())
	h, err = s.RouteHistory(context.Background(), RouteHistoryQuery{})
	if err != nil || len(h.Events) != 1 || h.Events[0].Change != "removed" || h.Events[0].Destination != "10.8.0.0/24" || h.Events[0].Owner != "wireguard" || h.Events[0].AcrossRestart {
		t.Fatalf("history = %+v %v", h, err)
	}

	// A new process finds the predecessor's snapshot and reports what
	// changed while nobody was reading, bounded by that snapshot's time.
	next := testService(t)
	next.db = db
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "ip -j route show table all", out: route4}}, rec.replies...)
	rec.mu.Unlock()
	next.observeRoutes(context.Background())
	h, err = next.RouteHistory(context.Background(), RouteHistoryQuery{Target: "10.8.0.5"})
	if err != nil || len(h.Events) != 2 {
		t.Fatalf("target-filtered history = %+v %v", h, err)
	}
	if e := h.Events[0]; e.Change != "added" || !e.AcrossRestart || e.PreviousAt.IsZero() {
		t.Fatalf("across restart = %+v", e)
	}
	h, err = next.RouteHistory(context.Background(), RouteHistoryQuery{Target: "192.0.2.1", Object: "route"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range h.Events {
		if e.Destination != "default" {
			t.Fatalf("only covering destinations match a target: %+v", e)
		}
	}
}

func TestForwardingHealthMeasuresTheKernelsCountersAndDevices(t *testing.T) {
	dir := t.TempDir()
	prevSys, prevNet := procSysRoot, procNetRoot
	procSysRoot, procNetRoot = filepath.Join(dir, "sys"), filepath.Join(dir, "net")
	t.Cleanup(func() { procSysRoot, procNetRoot = prevSys, prevNet })
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snmp := func(n string) string {
		return "Ip: Forwarding DefaultTTL InReceives ForwDatagrams OutRequests\nIp: 1 64 100 " + n + " 50\nIcmp: InMsgs\nIcmp: 0\n"
	}
	write(filepath.Join(procSysRoot, "net/ipv4/ip_forward"), "1\n")
	write(filepath.Join(procSysRoot, "net/ipv6/conf/all/forwarding"), "0\n")
	for _, dev := range []string{"all", "default", "lo", "eth0", "docker0"} {
		write(filepath.Join(procSysRoot, "net/ipv4/conf", dev, "forwarding"), "1\n")
	}
	write(filepath.Join(procNetRoot, "snmp"), snmp("1000"))
	write(filepath.Join(procNetRoot, "snmp6"), "Ip6InReceives 10\nIp6OutForwDatagrams 7\n")
	s := testService(t)
	needs := ForwardingNeeds{DockerNetworks: 3, DockerIPv6Networks: 1}
	view := s.Forwarding(context.Background(), needs)
	if h := view.IPv4.Health; h.Status != "measuring" || h.Forwarded == nil || *h.Forwarded != 1000 {
		t.Fatalf("first ipv4 reading = %+v", h)
	}
	if h := view.IPv6.Health; h.Status != "off" || *h.Forwarded != 7 {
		t.Fatalf("ipv6 = %+v", h)
	}
	if !strings.Contains(view.IPv4.DockerBasis, "all 3 Docker bridge networks") || !strings.Contains(view.IPv6.DockerBasis, "1 of 3") {
		t.Fatalf("docker basis = %q / %q", view.IPv4.DockerBasis, view.IPv6.DockerBasis)
	}
	s.forwardingSample.mu.Lock()
	last := s.forwardingSample.last["ipv4"]
	last.at = last.at.Add(-10 * time.Second)
	s.forwardingSample.last["ipv4"] = last
	s.forwardingSample.mu.Unlock()
	write(filepath.Join(procNetRoot, "snmp"), snmp("1500"))
	view = s.Forwarding(context.Background(), needs)
	if h := view.IPv4.Health; h.Status != "forwarding" || h.RatePerSecond == nil || *h.RatePerSecond < 49 || *h.RatePerSecond > 51 {
		t.Fatalf("second reading = %+v", h)
	}
	write(filepath.Join(procSysRoot, "net/ipv4/conf/docker0/forwarding"), "0\n")
	if h := s.Forwarding(context.Background(), needs).IPv4.Health; h.Status != "partial" || len(h.Disabled) != 1 || h.Disabled[0] != "docker0" {
		t.Fatalf("a device that does not forward = %+v", h)
	}
	os.Remove(filepath.Join(procNetRoot, "snmp"))
	if h := s.Forwarding(context.Background(), needs).IPv4.Health; h.Status != "unknown" || h.Forwarded != nil {
		t.Fatalf("an unreadable counter = %+v", h)
	}
}

func TestBGPReadsNeighbourPoliciesAndOSPFAdjacencies(t *testing.T) {
	record(t).
		on("systemctl is-active frr", "active\n").
		on("vtysh -c show bgp summary json", fixture(t, "routing-bgp-summary.json")).
		on("vtysh -c show bgp neighbors json", `{"192.0.2.2":{"addressFamilyInfo":{"ipv4Unicast":{"routeMapForIncomingAdvertisements":"EDGE-IN","outgoingUpdatePrefixFilterList":"OWN"}}}}`).
		on("vtysh -c show ip ospf neighbor json", `{"neighbors":{"10.0.0.2":[{"priority":1,"state":"Full/DR","deadTimeMsecs":35000,"address":"192.0.2.2","ifaceName":"eth1:192.0.2.1"}],"10.0.0.3":[{"nbrPriority":0,"nbrState":"2-Way/DROther","role":"DROther","upTimeInMsec":61000,"routerDeadIntervalTimerDueMsec":31000,"ifaceAddress":"192.0.2.3","ifaceName":"eth1:192.0.2.1"}]}}`).
		on("vtysh -c show ipv6 ospf6 neighbor json", `{"neighbors":[{"neighborId":"10.0.0.4","priority":1,"deadTime":"00:00:36","state":"Full","ifState":"BDR","interfaceName":"eth2","duration":"01:00:00"}]}`)
	s := testService(t)
	v, err := s.BGP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p := v.Families[0].Peers[0].Policy; p == nil || p.RouteMapIn != "EDGE-IN" || p.PrefixListOut != "OWN" {
		t.Fatalf("policy = %+v", p)
	}
	if v.Families[0].Peers[1].Policy != nil || v.ReadOnly == "" {
		t.Fatalf("a neighbour without filters has none: %+v", v.Families[0].Peers[1])
	}
	if len(v.OSPF) != 3 {
		t.Fatalf("ospf = %+v", v.OSPF)
	}
	if n := v.OSPF[0]; n.Version != 2 || n.RouterID != "10.0.0.2" || n.State != "Full" || n.Role != "DR" || n.Interface != "eth1" || n.DeadSeconds != 35 {
		t.Fatalf("older ospfd = %+v", n)
	}
	if n := v.OSPF[1]; n.State != "2-Way" || n.Role != "DROther" || n.Priority != 0 || n.UptimeSeconds != 61 || n.Address != "192.0.2.3" {
		t.Fatalf("newer ospfd = %+v", n)
	}
	if n := v.OSPF[2]; n.Version != 3 || n.State != "Full" || n.Role != "BDR" || n.Interface != "eth2" || n.UptimeSeconds != 3600 {
		t.Fatalf("ospf6d = %+v", n)
	}
}

func TestBGPRoutesListASmallTableAndOnlyAPrefixOfALargeOne(t *testing.T) {
	rec := record(t).
		on("vtysh -c show bgp summary json", fixture(t, "routing-bgp-summary.json")).
		on("vtysh -c show bgp ipv4 unicast json", `{"routerId":"192.0.2.1","localAS":65000,"routes":{"10.10.0.0/24":[{"valid":true,"bestpath":true,"pathFrom":"external","network":"10.10.0.0/24","metric":5,"locPrf":100,"weight":0,"peerId":"192.0.2.2","path":"65001","origin":"IGP","nexthops":[{"ip":"192.0.2.2"}]},{"valid":true,"multipath":true,"network":"10.10.0.0/24","peerId":"192.0.2.3","path":"65002 65001","origin":"IGP","nexthops":[{"ip":"192.0.2.3"}]}]}}`).
		on("vtysh -c show bgp ipv4 unicast 10.10.0.0/24 json", `{"prefix":"10.10.0.0/24","paths":[{"aspath":{"string":"65001","length":1},"origin":"IGP","med":5,"valid":true,"bestpath":{"overall":true},"nexthops":[{"ip":"192.0.2.2"}],"peer":{"peerId":"192.0.2.2","type":"external"}}]}`)
	s := testService(t)
	v, err := s.BGPRoutes(context.Background(), "ipv4Unicast", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.Total != 1 || len(v.Routes) != 2 || !v.Routes[0].Best || v.Routes[0].Path != "65001" || *v.Routes[0].MED != 5 || !v.Routes[1].Multipath || v.LocalAS != 65000 {
		t.Fatalf("table = %+v", v)
	}
	v, err = s.BGPRoutes(context.Background(), "ipv4Unicast", "10.10.0.7/24")
	if err != nil {
		t.Fatal(err)
	}
	if v.Prefix != "10.10.0.0/24" || len(v.Routes) != 1 || !v.Routes[0].Best || v.Routes[0].From != "external" || v.Routes[0].Peer != "192.0.2.2" || *v.Routes[0].MED != 5 {
		t.Fatalf("prefix = %+v", v)
	}
	if _, err := s.BGPRoutes(context.Background(), "l2vpnEvpn", ""); err == nil {
		t.Fatal("an unsupported family")
	}
	if _, err := s.BGPRoutes(context.Background(), "ipv6Unicast", "10.0.0.0/8"); err == nil {
		t.Fatal("a prefix of the other family")
	}
	for _, c := range rec.commands() {
		if !strings.HasPrefix(c, "vtysh -c show ") {
			t.Fatalf("reads only: %v", rec.commands())
		}
	}
	big := strings.Replace(fixture(t, "routing-bgp-summary.json"), `"pfxRcd":4`, `"pfxRcd":900000`, 1)
	record(t).on("vtysh -c show bgp summary json", big)
	v, err = s.BGPRoutes(context.Background(), "ipv4Unicast", "")
	if err != nil || !v.Truncated || len(v.Routes) != 0 || !strings.Contains(v.Error, "Name a prefix") {
		t.Fatalf("a full table is not listed whole: %+v %v", v, err)
	}
}
