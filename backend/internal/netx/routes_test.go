package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rtTableNames points the table-name files at a directory with an
// administrator's rt_tables.d entry and the distribution's rt_tables, as a
// Debian host has them.
func rtTableNames(t *testing.T) {
	t.Helper()
	prev := rtTableDirs
	t.Cleanup(func() { rtTableDirs = prev })
	dir := t.TempDir()
	etc, usr := filepath.Join(dir, "etc"), filepath.Join(dir, "usr")
	for path, body := range map[string]string{
		filepath.Join(etc, "rt_tables.d", "10-isp.conf"):  "# the second uplink\n200\tisp2\n",
		filepath.Join(etc, "rt_tables"):                   "100 offices # first definition wins\n",
		filepath.Join(usr, "rt_tables"):                   "#\n255\tlocal\n254\tmain\n253\tdefault\n0\tunspec\n100 shadowed\n",
		filepath.Join(usr, "rt_tables.d", "20-more.conf"): "0x12c hexed\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rtTableDirs = []string{etc, usr}
}

func rtRoutingSpec() *Spec {
	sp := emptySpec()
	sp.Routes = []RouteSpec{
		{ID: 3, Family: "inet", Destination: "172.16.9.0/24", Type: "unicast", Gateway: "192.168.50.2", Device: "jd-lan", Table: 254, Metric: 50},
		{ID: 4, Family: "inet", Destination: "10.88.0.0/24", Type: "unicast", Gateway: "192.168.50.2", Table: 100, Metric: 50},
		{ID: 5, Family: "inet", Destination: "198.51.100.0/24", Type: "blackhole", Table: 254},
		{ID: 6, Family: "inet6", Destination: "2001:db8:88::/64", Type: "unicast", Gateway: "2001:db8:77::2", Table: 100},
		{ID: 7, Family: "inet6", Destination: "2001:db8:98::/64", Type: "unreachable", Table: 254},
		{ID: 14, Family: "inet", Destination: "default", Type: "unicast", Gateway: "198.51.100.1", Device: "eth1", Table: 200},
	}
	sp.Rules = []RuleSpec{
		{ID: 9, Family: "inet", Priority: 10000, From: "192.168.50.0/24", Action: "lookup", Table: 100},
		{ID: 10, Family: "inet", Priority: 10001, To: "198.51.100.0/24", IIF: "jd-lan", Action: "blackhole"},
		{ID: 11, Family: "inet", Priority: 12000, FWMark: "0x10", Action: "lookup", Table: 200},
	}
	return sp
}

func rtBuild(t *testing.T, sp *Spec) *RoutingView {
	t.Helper()
	rtTableNames(t)
	byID, byName := rtTables()
	r4, err := parseIPRoutes(fixture(t, "routing-route4.json"))
	if err != nil {
		t.Fatal(err)
	}
	r6, err := parseIPRoutes(fixture(t, "routing-route6.json"))
	if err != nil {
		t.Fatal(err)
	}
	l4, err := parseIPRules(fixture(t, "routing-rule4.json"))
	if err != nil {
		t.Fatal(err)
	}
	l6, err := parseIPRules(fixture(t, "routing-rule6.json"))
	if err != nil {
		t.Fatal(err)
	}
	return buildRouting(routing{
		routes: map[string][]ipRoute{"inet": r4, "inet6": r6},
		rules:  map[string][]ipRule{"inet": l4, "inet6": l6},
		spec:   sp, byID: byID, byName: byName,
	})
}

func TestRoutingTableNamesComeFromEtcFirstThenUsrAndTheirDropIns(t *testing.T) {
	rtTableNames(t)
	byID, byName := rtTables()
	for id, want := range map[int]string{254: "main", 255: "local", 253: "default", 100: "offices", 200: "isp2", 300: "hexed"} {
		if byID[id] != want {
			t.Errorf("table %d = %q, want %q", id, byID[id], want)
		}
	}
	if byName["isp2"] != 200 || byName["offices"] != 100 {
		t.Errorf("by name = %v", byName)
	}
}

func TestRoutingTableNamesFallBackToTheKernelsOwnWhenNoFileExists(t *testing.T) {
	prev := rtTableDirs
	t.Cleanup(func() { rtTableDirs = prev })
	rtTableDirs = []string{t.TempDir()}
	byID, _ := rtTables()
	if byID[254] != "main" || byID[255] != "local" || byID[253] != "default" {
		t.Fatalf("byID = %v", byID)
	}
}

func TestRoutingGroupsEveryTableAndHidesTheLocalOne(t *testing.T) {
	view := rtBuild(t, emptySpec())
	var ids []int
	for _, tb := range view.Tables {
		ids = append(ids, tb.ID)
	}
	// main first, then ascending: 52 (Tailscale's), 100 (a rule's target and
	// named by number), 200 (isp2).
	if got := ids; len(got) != 4 || got[0] != 254 || got[1] != 52 || got[2] != 100 || got[3] != 200 {
		t.Fatalf("tables = %v", got)
	}
	if view.HiddenLocal != 6 {
		t.Fatalf("hiddenLocal = %d, want the 4 IPv4 and 2 IPv6 entries of the local table", view.HiddenLocal)
	}
	names := map[int]string{}
	for _, tb := range view.Tables {
		names[tb.ID] = tb.Name
	}
	if names[254] != "main" || names[200] != "isp2" || names[100] != "offices" {
		t.Fatalf("names = %v", names)
	}
	if view.RulePriorities.Min != 10000 || view.RulePriorities.Max != 19999 {
		t.Fatalf("priorities = %+v", view.RulePriorities)
	}
}

func TestRoutingNamesWhoOwnsEachRoute(t *testing.T) {
	view := rtBuild(t, emptySpec())
	main := view.Tables[0]
	find := func(tb RoutingTable, dest, dev string) RouteEntry {
		for _, r := range tb.Routes {
			if r.Destination == dest && (dev == "" || r.Device == dev) {
				return r
			}
		}
		t.Fatalf("no route %s dev %s in table %s", dest, dev, tb.Name)
		return RouteEntry{}
	}
	cases := []struct{ dest, dev, owner, family string }{
		{"default", "eth0", "dhcp", "inet"},
		{"10.0.0.0/24", "docker0", "docker", "inet"},
		{"10.0.1.0/24", "br-0123456789ab", "docker", "inet"},
		{"10.8.0.0/24", "wg0", "wireguard", "inet"},
		{"192.168.50.0/24", "jd-lan", "kernel", "inet"},
		{"172.16.9.0/24", "jd-lan", "system", "inet"},
		{"10.5.0.0/16", "", "system", "inet"},
		{"2001:db8::/64", "eth0", "kernel", "inet6"},
	}
	for _, c := range cases {
		r := find(main, c.dest, c.dev)
		if r.Owner != c.owner || r.Family != c.family {
			t.Errorf("%s dev %s: owner %s family %s, want %s %s", c.dest, c.dev, r.Owner, r.Family, c.owner, c.family)
		}
		if r.Guard == "" || r.Managed || r.ID != 0 {
			t.Errorf("%s dev %s: a route made elsewhere must say why it stays: %+v", c.dest, c.dev, r)
		}
	}
	ts := view.Tables[1]
	if ts.ID != 52 || len(ts.Routes) != 3 {
		t.Fatalf("tailscale table = %+v", ts)
	}
	for _, r := range ts.Routes {
		if r.Owner != "tailscale" {
			t.Errorf("%s in table 52 is owned by %s", r.Destination, r.Owner)
		}
	}
	if r := find(main, "10.5.0.0/16", ""); len(r.Nexthops) != 2 || r.Nexthops[1].Gateway != "192.168.50.3" {
		t.Fatalf("multipath = %+v", r.Nexthops)
	}
	if r := find(main, "10.0.0.0/24", "docker0"); len(r.Flags) != 1 || r.Flags[0] != "linkdown" {
		t.Fatalf("flags = %v", r.Flags)
	}
	if r := find(main, "198.51.100.0/24", ""); r.Type != "blackhole" {
		t.Fatalf("type = %s", r.Type)
	}
}

func TestRoutingSortsDefaultFirstAndIPv4BeforeIPv6(t *testing.T) {
	view := rtBuild(t, emptySpec())
	main := view.Tables[0].Routes
	if main[0].Destination != "default" || main[0].Family != "inet" {
		t.Fatalf("first = %+v", main[0])
	}
	seenV6 := false
	for _, r := range main {
		if r.Family == "inet6" {
			seenV6 = true
		} else if seenV6 {
			t.Fatalf("an IPv4 route (%s) after an IPv6 one", r.Destination)
		}
	}
}

func TestRoutingMatchesWhatTheDashboardMadeAndKeepsItsId(t *testing.T) {
	view := rtBuild(t, rtRoutingSpec())
	byID := map[int]RouteEntry{}
	for _, tb := range view.Tables {
		for _, r := range tb.Routes {
			if r.Managed {
				byID[r.ID] = r
			}
		}
	}
	// 3: main, with a device and a metric. 4: table 100 without a device
	// (the kernel picked jd-lan). 5: a blackhole. 6: IPv6 without a metric,
	// which the kernel reads back as 1024. 7: IPv6 unreachable, which the
	// kernel puts on lo.
	for _, id := range []int{3, 4, 5, 6, 7} {
		r, ok := byID[id]
		if !ok {
			t.Errorf("route %d was not matched", id)
			continue
		}
		if r.Owner != "just-dashboard" || r.Guard != "" {
			t.Errorf("route %d: owner %s guard %q", id, r.Owner, r.Guard)
		}
	}
	// 14 is the default route in isp2, a table the kernel prints by name.
	if r, ok := byID[14]; !ok || r.Destination != "default" {
		t.Errorf("a route in a named table was not matched: %+v", r)
	}
	// The same destination elsewhere is not the dashboard's.
	for _, r := range view.Tables[0].Routes {
		if r.Destination == "default" && r.Managed {
			t.Errorf("the provider's default route read as the dashboard's: %+v", r)
		}
	}
}

func TestRoutingRulesAreOwnedByWhoMadeThem(t *testing.T) {
	view := rtBuild(t, rtRoutingSpec())
	byPrio := map[int]RuleEntry{}
	for _, r := range view.Rules {
		if r.Family == "inet" {
			byPrio[r.Priority] = r
		}
	}
	cases := []struct {
		prio    int
		owner   string
		managed bool
	}{
		{0, "system", false}, {32766, "system", false}, {32767, "system", false},
		{5210, "tailscale", false}, {5250, "tailscale", false}, {5270, "tailscale", false},
		{10000, "just-dashboard", true}, {10001, "just-dashboard", true}, {12000, "just-dashboard", true},
	}
	for _, c := range cases {
		r, ok := byPrio[c.prio]
		if !ok {
			t.Fatalf("no rule with priority %d", c.prio)
		}
		if r.Owner != c.owner || r.Managed != c.managed {
			t.Errorf("priority %d: owner %s managed %v", c.prio, r.Owner, r.Managed)
		}
		if (r.Guard == "") != c.managed {
			t.Errorf("priority %d: guard %q", c.prio, r.Guard)
		}
	}
	if r := byPrio[10000]; r.From != "192.168.50.0/24" || r.Table != 100 || r.TableName != "offices" || r.Action != "lookup" || r.ID != 9 {
		t.Errorf("rule 10000 = %+v", r)
	}
	if r := byPrio[10001]; r.To != "198.51.100.0/24" || r.IIF != "jd-lan" || r.Action != "blackhole" || r.Table != 0 {
		t.Errorf("rule 10001 = %+v", r)
	}
	if r := byPrio[12000]; r.FWMark != "0x10" || r.Table != 200 || r.TableName != "isp2" {
		t.Errorf("rule 12000 = %+v", r)
	}
	if r := byPrio[5210]; r.FWMark != "0x80000/0xff0000" || r.TableName != "main" {
		t.Errorf("rule 5210 = %+v", r)
	}
	if r := byPrio[5250]; r.Action != "unreachable" {
		t.Errorf("rule 5250 = %+v", r)
	}
	// The order a packet is decided in.
	for i := 1; i < len(view.Rules); i++ {
		if view.Rules[i-1].Priority > view.Rules[i].Priority {
			t.Fatalf("rules out of order at %d", i)
		}
	}
}

func TestRoutingEmptySlicesAreEmptyNotNull(t *testing.T) {
	view := buildRouting(routing{spec: emptySpec(), byID: map[int]string{254: "main"}, byName: map[string]int{}})
	if view.Tables == nil || view.Rules == nil || view.Tables[0].Routes == nil {
		t.Fatalf("view = %+v", view)
	}
}

func TestRoutingReadsBothFamiliesAndTheClientPath(t *testing.T) {
	rtTableNames(t)
	rec := record(t).
		on("ip -j route show table all", fixture(t, "routing-route4.json")).
		on("ip -j -6 route show table all", fixture(t, "routing-route6.json")).
		on("ip -j rule show", fixture(t, "routing-rule4.json")).
		on("ip -j -6 rule show", fixture(t, "routing-rule6.json")).
		on("ip -j route get", fixture(t, "routing-route-get.json")).
		on("ip -j -d link show type vrf", "[{},{}]").
		on("ss -Htne state established dst", "")
	s := testService(t)
	rtSaveSpec(t, s, rtRoutingSpec())
	view, err := s.Routing(context.Background(), rtClient)
	if err != nil {
		t.Fatal(err)
	}
	if view.ClientPath.Device != "tailscale0" || view.ClientPath.Source != "100.110.34.31" {
		t.Fatalf("client path = %+v", view.ClientPath)
	}
	if len(view.Rules) != 14 {
		t.Fatalf("rules = %d, want 10 IPv4 and 4 IPv6", len(view.Rules))
	}
	if rtMutations(rec) != nil {
		t.Fatalf("a read changed something: %v", rtMutations(rec))
	}
}

func TestRoutingSurvivesAHostWithoutIPv6(t *testing.T) {
	rtTableNames(t)
	record(t).
		on("ip -j route show table all", fixture(t, "routing-route4.json")).
		fail("ip -j -6 route show table all", "RTNETLINK answers: Address family not supported by protocol").
		on("ip -j rule show", fixture(t, "routing-rule4.json")).
		fail("ip -j -6 rule show", "RTNETLINK answers: Address family not supported by protocol").
		on("ip -j route get", fixture(t, "routing-route-get.json")).
		on("ip -j -d link show type vrf", "[]").
		on("ss -Htne state established dst", "")
	s := testService(t)
	if _, err := s.Routing(context.Background(), rtClient); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingRouteRequestValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  RouteRequest
		want string
		code func(*testing.T, error)
	}{
		{name: "no destination", req: RouteRequest{Gateway: "192.168.50.2"}, want: "destination"},
		{name: "not an address", req: RouteRequest{Destination: "banana", Gateway: "192.168.50.2"}, want: "not an IP address"},
		{name: "unicast with no way out", req: RouteRequest{Destination: "10.9.0.0/24"}, want: "gateway, a device"},
		{name: "gateway of another family", req: RouteRequest{Destination: "10.9.0.0/24", Gateway: "2001:db8::1"}, want: "not an inet"},
		{name: "unspecified gateway", req: RouteRequest{Destination: "10.9.0.0/24", Gateway: "0.0.0.0"}, want: "cannot be a gateway"},
		{name: "source of another family", req: RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Source: "2001:db8::1"}, want: "not an inet"},
		{name: "bad device", req: RouteRequest{Destination: "10.9.0.0/24", Device: "a b"}, want: "interface name"},
		{name: "blackhole with a gateway", req: RouteRequest{Destination: "10.9.0.0/24", Type: "blackhole", Gateway: "192.168.50.2"}, want: "no gateway"},
		{name: "unknown type", req: RouteRequest{Destination: "10.9.0.0/24", Type: "throw", Device: "eth0"}, want: "route type"},
		{name: "table 0 is main, so negative is not", req: RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Table: -3}, want: "table"},
		{name: "table past the range", req: RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Table: 4294967295}, want: "table"},
		{name: "negative metric", req: RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Metric: -1}, want: "metric"},
		{name: "comment with a quote", req: RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Comment: `a"b`}, want: "quotes"},
		{name: "table 52", req: RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Table: 52}, code: func(t *testing.T, err error) {
			if g := rtGuarded(t, err); !strings.Contains(g.Reason, "Tailscale") {
				t.Fatalf("reason = %q", g.Reason)
			}
		}},
		{name: "table 255", req: RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Table: 255}, code: func(t *testing.T, err error) {
			if g := rtGuarded(t, err); !strings.Contains(g.Reason, "local") {
				t.Fatalf("reason = %q", g.Reason)
			}
		}},
		{name: "table 253", req: RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Table: 253}, code: func(t *testing.T, err error) {
			rtGuarded(t, err)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.req.spec()
			if c.code != nil {
				c.code(t, err)
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestRoutingRouteRequestNormalises(t *testing.T) {
	cases := []struct {
		name   string
		req    RouteRequest
		family string
		dest   string
		args   string
	}{
		{"host bits are masked", RouteRequest{Destination: "10.9.0.5/24", Gateway: "192.168.50.2"}, "inet", "10.9.0.0/24", "10.9.0.0/24 via 192.168.50.2"},
		{"an address is a /32", RouteRequest{Destination: "10.9.0.5", Device: "eth0"}, "inet", "10.9.0.5/32", "10.9.0.5/32 dev eth0"},
		{"0.0.0.0/0 is default", RouteRequest{Destination: "0.0.0.0/0", Gateway: "192.168.50.2", Table: 100}, "inet", "default", "default via 192.168.50.2 table 100"},
		{"::/0 is the IPv6 default", RouteRequest{Destination: "::/0", Gateway: "2001:db8::1", Table: 100}, "inet6", "default", "::/0 via 2001:db8::1 table 100"},
		{"default takes its family from the gateway", RouteRequest{Destination: "default", Gateway: "2001:db8::1", Table: 100}, "inet6", "default", "::/0 via 2001:db8::1 table 100"},
		{"every option, in the order ip reads them", RouteRequest{Destination: "10.9.0.0/24", Gateway: "192.168.50.2", Device: "jd-lan", Table: 100, Metric: 5, Source: "192.168.50.1"}, "inet", "10.9.0.0/24", "10.9.0.0/24 via 192.168.50.2 dev jd-lan table 100 metric 5 src 192.168.50.1"},
		{"main is not written out", RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Table: 254}, "inet", "10.9.0.0/24", "10.9.0.0/24 dev eth0"},
		{"a blackhole", RouteRequest{Destination: "10.9.0.0/24", Type: "blackhole"}, "inet", "10.9.0.0/24", "blackhole 10.9.0.0/24"},
		{"an IPv6 prohibit", RouteRequest{Destination: "2001:db8:9::/64", Type: "PROHIBIT", Table: 7}, "inet6", "2001:db8:9::/64", "prohibit 2001:db8:9::/64 table 7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := c.req.spec()
			if err != nil {
				t.Fatal(err)
			}
			if r.Family != c.family || r.Destination != c.dest {
				t.Fatalf("spec = %+v", r)
			}
			args, err := routeArgs(r)
			if err != nil || strings.Join(args, " ") != c.args {
				t.Fatalf("args = %v, %v; want %s", args, err, c.args)
			}
		})
	}
}

func TestRoutingAddRouteRunsTheCommandChecksThePathAndRecordsIt(t *testing.T) {
	rec := rtHost(t).on("ip route add", "")
	s := testService(t)
	r, err := s.AddRoute(context.Background(), RouteRequest{
		Destination: "10.88.0.0/24", Gateway: "192.168.50.2", Device: "jd-lan", Table: 100, Metric: 50,
	}, rtClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == 0 || r.CreatedBy != "ion" || r.Table != 100 || r.Family != "inet" {
		t.Fatalf("route = %+v", r)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip route add 10.88.0.0/24 via 192.168.50.2 dev jd-lan table 100 metric 50" {
		t.Fatalf("mutations = %v", got)
	}
	// The path was resolved before and after.
	routeGets := 0
	for _, c := range rec.commands() {
		if strings.HasPrefix(c, "ip -j route get 100.110.34.9") {
			routeGets++
		}
	}
	if routeGets < 3 {
		t.Fatalf("the client path was read %d times, want before, after, and after from its source", routeGets)
	}
	if b, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile)); !strings.Contains(string(b), "route add 10.88.0.0/24 via 192.168.50.2 dev jd-lan table 100 metric 50") {
		t.Fatalf("the boot file lacks the route:\n%s", b)
	}
	if got := rtLoad(t, s).Routes; len(got) != 1 || got[0].ID != r.ID {
		t.Fatalf("spec = %+v", got)
	}
}

func TestRoutingAddRouteIPv6UsesTheFamilyFlag(t *testing.T) {
	rec := rtHost(t).on("ip -6 route add", "")
	s := testService(t)
	if _, err := s.AddRoute(context.Background(), RouteRequest{Destination: "2001:db8:88::/64", Gateway: "2001:db8:50::2", Device: "jd-lan"}, rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip -6 route add 2001:db8:88::/64 via 2001:db8:50::2 dev jd-lan" {
		t.Fatalf("mutations = %v", got)
	}
}

func TestRoutingAddRouteIsTakenBackWhenItMovesTheClientPath(t *testing.T) {
	rec := rtHost(t).on("ip route add", "").on("ip route del", "")
	rtAnswerAfter(rec, "ip route add", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
	s := testService(t)
	_, err := s.AddRoute(context.Background(), RouteRequest{Destination: "100.110.0.0/16", Gateway: "203.0.113.1", Device: "eth0"}, rtClient, "ion")
	g := rtGuarded(t, err)
	if !strings.Contains(g.Reason, "instead of through tailscale0") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip route add 100.110.0.0/16 via 203.0.113.1 dev eth0|ip route del 100.110.0.0/16 via 203.0.113.1 dev eth0" {
		t.Fatalf("mutations = %v", got)
	}
	if rtSpecWritten(s) {
		t.Fatal("a rolled-back route reached the spec")
	}
}

func TestRoutingAddRouteChecksTheRepliesFromTheServersOwnAddressToo(t *testing.T) {
	// Only the replies from the server's source move: a plain route get for
	// the client reads the same before and after.
	rec := rtHost(t).on("ip route add", "").on("ip route del", "")
	rtAnswerAfter(rec, "ip route add", "ip -j route get 100.110.34.9 from 100.110.34.31", fixture(t, "routing-route-get-moved.json"))
	s := testService(t)
	_, err := s.AddRoute(context.Background(), RouteRequest{Destination: "10.9.0.0/24", Device: "eth0"}, rtClient, "ion")
	g := rtGuarded(t, err)
	if !strings.Contains(g.Reason, "when they come from 100.110.34.31") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if !rec.ran("ip route del 10.9.0.0/24 dev eth0") {
		t.Fatalf("not rolled back: %v", rtMutations(rec))
	}
}

func TestRoutingAddRouteRefusals(t *testing.T) {
	cases := []struct {
		name  string
		req   RouteRequest
		reply func(*recorder)
		check func(*testing.T, error)
	}{
		{
			name: "a second default route in main",
			req:  RouteRequest{Destination: "default", Gateway: "198.51.100.1", Device: "eth0"},
			check: func(t *testing.T, err error) {
				g := rtGuarded(t, err)
				if !strings.Contains(g.Reason, "default route via 203.0.113.1 on eth0") {
					t.Fatalf("reason = %q", g.Reason)
				}
			},
		},
		{
			name:  "a second default route in main written 0.0.0.0/0",
			req:   RouteRequest{Destination: "0.0.0.0/0", Gateway: "198.51.100.1"},
			check: func(t *testing.T, err error) { rtGuarded(t, err) },
		},
		{
			name: "an IPv6 default route in main when one exists",
			req:  RouteRequest{Destination: "::/0", Gateway: "2001:db8::1", Device: "eth0"},
			reply: func(rec *recorder) {
				rec.replies = append([]reply{{prefix: "ip -j -6 route show default", out: `[{"dst":"default","gateway":"fe80::1","dev":"eth0","protocol":"ra"}]`}}, rec.replies...)
			},
			check: func(t *testing.T, err error) { rtGuarded(t, err) },
		},
		{
			name:  "table 52",
			req:   RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Table: 52},
			check: func(t *testing.T, err error) { rtGuarded(t, err) },
		},
		{
			name:  "table 255",
			req:   RouteRequest{Destination: "10.9.0.0/24", Device: "eth0", Table: 255},
			check: func(t *testing.T, err error) { rtGuarded(t, err) },
		},
		{
			name: "a device that is not there",
			req:  RouteRequest{Destination: "10.9.0.0/24", Device: "eth9"},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v", err)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := rtHost(t)
			if c.reply != nil {
				c.reply(rec)
			}
			s := testService(t)
			_, err := s.AddRoute(context.Background(), c.req, rtClient, "ion")
			c.check(t, err)
			if m := rtMutations(rec); len(m) != 0 {
				t.Fatalf("a refused route ran %v", m)
			}
		})
	}
}

func TestRoutingAddRouteAllowsADefaultRouteInATableOfItsOwn(t *testing.T) {
	rec := rtHost(t).on("ip route add", "")
	s := testService(t)
	if _, err := s.AddRoute(context.Background(), RouteRequest{Destination: "default", Gateway: "198.51.100.1", Device: "eth0", Table: 200}, rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip route add default via 198.51.100.1 dev eth0 table 200" {
		t.Fatalf("mutations = %v", got)
	}
}

func TestRoutingAddRouteAcceptsADefaultWhenTheHostHasNone(t *testing.T) {
	rec := rtHost(t).on("ip route add", "")
	rec.replies = append([]reply{{prefix: "ip -j route show default", out: `[]`}}, rec.replies...)
	s := testService(t)
	if _, err := s.AddRoute(context.Background(), RouteRequest{Destination: "default", Gateway: "203.0.113.1", Device: "eth0"}, rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingAddRouteRefusesADuplicate(t *testing.T) {
	rec := rtHost(t).on("ip route add", "")
	s := testService(t)
	req := RouteRequest{Destination: "10.88.0.0/24", Gateway: "192.168.50.2", Table: 100}
	if _, err := s.AddRoute(context.Background(), req, rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	before := len(rtMutations(rec))
	if _, err := s.AddRoute(context.Background(), req, rtClient, "ion"); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v", err)
	}
	if len(rtMutations(rec)) != before {
		t.Fatal("a duplicate ran a command")
	}
}

func TestRoutingAddRouteFailureLeavesNoTrace(t *testing.T) {
	rtHost(t).fail("ip route add", "ip: RTNETLINK answers: Network is unreachable")
	s := testService(t)
	_, err := s.AddRoute(context.Background(), RouteRequest{Destination: "10.88.0.0/24", Gateway: "10.200.0.9"}, rtClient, "ion")
	if err == nil || !strings.Contains(err.Error(), "Network is unreachable") {
		t.Fatalf("err = %v", err)
	}
	if rtSpecWritten(s) {
		t.Fatal("a failed route reached the spec")
	}
}

func TestRoutingDeleteRoute(t *testing.T) {
	spec := func() *Spec {
		sp := emptySpec()
		sp.Routes = []RouteSpec{
			{ID: 3, Family: "inet", Destination: "10.88.0.0/24", Type: "unicast", Gateway: "192.168.50.2", Device: "jd-lan", Table: 100, Metric: 50, Source: "192.168.50.1"},
			{ID: 4, Family: "inet6", Destination: "2001:db8:88::/64", Type: "unicast", Gateway: "2001:db8:50::2", Table: 254},
		}
		return sp
	}
	t.Run("removes the route and its entry, leaving src out of the delete", func(t *testing.T) {
		rec := rtHost(t).on("ip route del", "")
		s := testService(t)
		rtSaveSpec(t, s, spec())
		if err := s.DeleteRoute(context.Background(), 3, rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip route del 10.88.0.0/24 via 192.168.50.2 dev jd-lan table 100 metric 50" {
			t.Fatalf("mutations = %v", got)
		}
		if got := rtLoad(t, s).Routes; len(got) != 1 || got[0].ID != 4 {
			t.Fatalf("spec = %+v", got)
		}
	})
	t.Run("an IPv6 route", func(t *testing.T) {
		rec := rtHost(t).on("ip -6 route del", "")
		s := testService(t)
		rtSaveSpec(t, s, spec())
		if err := s.DeleteRoute(context.Background(), 4, rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip -6 route del 2001:db8:88::/64 via 2001:db8:50::2" {
			t.Fatalf("mutations = %v", got)
		}
	})
	t.Run("a route that was never the dashboard's", func(t *testing.T) {
		rec := rtHost(t)
		s := testService(t)
		rtSaveSpec(t, s, spec())
		if err := s.DeleteRoute(context.Background(), 99, rtClient, "ion"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatal("a refused delete ran")
		}
	})
	t.Run("a route the kernel already lost still leaves the spec", func(t *testing.T) {
		rtHost(t).fail("ip route del", "RTNETLINK answers: No such process")
		s := testService(t)
		rtSaveSpec(t, s, spec())
		if err := s.DeleteRoute(context.Background(), 3, rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if len(rtLoad(t, s).Routes) != 1 {
			t.Fatal("the entry outlived its route")
		}
	})
	t.Run("is put back when the client path moves", func(t *testing.T) {
		rec := rtHost(t).on("ip route del", "").on("ip route add", "")
		rtAnswerAfter(rec, "ip route del", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
		s := testService(t)
		rtSaveSpec(t, s, spec())
		rtGuarded(t, s.DeleteRoute(context.Background(), 3, rtClient, "ion"))
		got := rtMutations(rec)
		if len(got) != 2 || !strings.HasPrefix(got[1], "ip route add 10.88.0.0/24 via 192.168.50.2 dev jd-lan table 100 metric 50 src 192.168.50.1") {
			t.Fatalf("mutations = %v", got)
		}
		if len(rtLoad(t, s).Routes) != 2 {
			t.Fatal("a rolled-back delete dropped the entry")
		}
	})
}

func TestRoutingRuleRequestValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  RuleRequest
		want string
	}{
		{name: "no selector at all", req: RuleRequest{Table: 100}, want: "no selector"},
		{name: "from all is no selector", req: RuleRequest{From: "0.0.0.0/0", Table: 100}, want: "no selector"},
		{name: "from all, spelled out", req: RuleRequest{From: "all", To: "0.0.0.0/0", Table: 100}, want: ""},
		{name: "lookup without a table", req: RuleRequest{From: "10.9.0.0/24"}, want: "needs a table"},
		{name: "a blackhole naming a table", req: RuleRequest{From: "10.9.0.0/24", Action: "blackhole", Table: 100}, want: "names no table"},
		{name: "unknown action", req: RuleRequest{From: "10.9.0.0/24", Action: "nat", Table: 100}, want: "action"},
		{name: "a goto naming a table", req: RuleRequest{From: "10.9.0.0/24", Action: "goto", Goto: 12000, Table: 100}, want: "names no table"},
		{name: "table 52", req: RuleRequest{From: "10.9.0.0/24", Table: 52}, want: "Tailscale"},
		{name: "table 255", req: RuleRequest{From: "10.9.0.0/24", Table: 255}, want: "local"},
		{name: "priority below the range", req: RuleRequest{From: "10.9.0.0/24", Table: 100, Priority: 5270}, want: "priority from 10000 to 19999"},
		{name: "priority above the range", req: RuleRequest{From: "10.9.0.0/24", Table: 100, Priority: 20000}, want: "priority from 10000 to 19999"},
		{name: "bad mark", req: RuleRequest{FWMark: "blue", Table: 100}, want: "firewall mark"},
		{name: "mark too large", req: RuleRequest{FWMark: "0x1ffffffff", Table: 100}, want: "firewall mark"},
		{name: "bad interface", req: RuleRequest{IIF: "a b", Table: 100}, want: "interface name"},
		{name: "bad from", req: RuleRequest{From: "x", Table: 100}, want: "not an IP address"},
		{name: "mixed families", req: RuleRequest{From: "2001:db8:88::/64", To: "10.0.0.0/8", Table: 100}, want: "must match"},
		{name: "explicit family mismatch", req: RuleRequest{Family: "inet", To: "2001:db8:88::/64", Action: "blackhole"}, want: "must match"},
		{name: "unknown family", req: RuleRequest{Family: "banana", FWMark: "0x10", Table: 100}, want: "family"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.req.spec()
			if c.want == "" {
				if err == nil || !strings.Contains(err.Error(), "no selector") {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestRoutingRuleRefusalsAreTheRightKind(t *testing.T) {
	_, err := RuleRequest{Table: 100}.spec()
	rtGuarded(t, err)
	_, err = RuleRequest{Family: "inet", From: "2001:db8::/32", Table: 100}.spec()
	var g *GuardError
	if err == nil || errors.As(err, &g) || errors.Is(err, ErrReadOnly) {
		t.Fatalf("err = %v, want a plain refusal", err)
	}
}

func TestRoutingRuleRequestNormalises(t *testing.T) {
	r, err := RuleRequest{From: "192.168.50.7/24", To: "10.0.0.0/8", IIF: "jd-lan", FWMark: "16", Table: 100}.spec()
	if err != nil {
		t.Fatal(err)
	}
	r.Priority = 10000
	args, err := ruleArgs(r)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); got != "priority 10000 from 192.168.50.0/24 to 10.0.0.0/8 iif jd-lan fwmark 0x10 lookup 100" {
		t.Fatalf("args = %s", got)
	}
	m, err := RuleRequest{FWMark: "0x10/0xffffffff", Action: "UNREACHABLE"}.spec()
	if err != nil || m.FWMark != "0x10" || m.Action != "unreachable" {
		t.Fatalf("mark rule = %+v, %v", m, err)
	}
}

func TestRoutingAddRuleChoosesTheFirstFreePriorityAndChecksThePath(t *testing.T) {
	rec := rtHost(t).on("ip rule add", "")
	s := testService(t)
	sp := emptySpec()
	sp.Rules = []RuleSpec{
		{ID: 1, Family: "inet", Priority: 10000, From: "10.1.0.0/24", Action: "lookup", Table: 100},
		{ID: 2, Family: "inet", Priority: 10001, From: "10.2.0.0/24", Action: "lookup", Table: 100},
		{ID: 3, Family: "inet", Priority: 10003, From: "10.3.0.0/24", Action: "lookup", Table: 100},
	}
	sp.NextID = 4
	rtSaveSpec(t, s, sp)
	r, err := s.AddRule(context.Background(), RuleRequest{From: "192.168.50.0/24", Table: 200}, rtClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if r.Priority != 10002 || r.ID != 4 || r.CreatedBy != "ion" || r.Family != "inet" {
		t.Fatalf("rule = %+v", r)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip rule add priority 10002 from 192.168.50.0/24 lookup 200" {
		t.Fatalf("mutations = %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile)); !strings.Contains(string(b), "rule add priority 10002 from 192.168.50.0/24 lookup 200") {
		t.Fatalf("the boot file lacks the rule:\n%s", b)
	}
}

func TestRoutingAddRuleLeavesAPriorityAnotherProgramUses(t *testing.T) {
	rec := rtHost(t).on("ip rule add", "")
	rec.replies = append([]reply{{prefix: "ip -j rule show", out: `[{"priority":10000,"src":"all","fwmark":"0x5","table":"7"},{"priority":10001,"src":"all","table":"8"}]`}}, rec.replies...)
	s := testService(t)
	r, err := s.AddRule(context.Background(), RuleRequest{From: "192.168.50.0/24", Table: 200}, rtClient, "ion")
	if err != nil || r.Priority != 10002 {
		t.Fatalf("rule = %+v, %v", r, err)
	}
}

func TestRoutingAddRuleGivenPriority(t *testing.T) {
	rec := rtHost(t).on("ip rule add", "")
	s := testService(t)
	if _, err := s.AddRule(context.Background(), RuleRequest{Priority: 15000, FWMark: "0x20", Table: 200}, rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip rule add priority 15000 fwmark 0x20 lookup 200" {
		t.Fatalf("mutations = %v", got)
	}
	if _, err := s.AddRule(context.Background(), RuleRequest{Priority: 15000, FWMark: "0x21", Table: 200}, rtClient, "ion"); !errors.Is(err, ErrExists) {
		t.Fatalf("a taken priority: err = %v", err)
	}
}

func TestRoutingExplicitRulePriorityCannotCollideWithAnotherProgram(t *testing.T) {
	rec := rtHost(t)
	rec.replies = append([]reply{{prefix: "ip -j rule show", out: `[{"priority":15000,"src":"all","fwmark":"0x5","table":7}]`}}, rec.replies...)
	s := testService(t)
	_, err := s.AddRule(context.Background(), RuleRequest{Priority: 15000, FWMark: "0x20", Table: 200}, rtClient, "ion")
	if !errors.Is(err, ErrExists) || len(rtMutations(rec)) != 0 {
		t.Fatalf("err = %v, mutations = %v", err, rtMutations(rec))
	}
}

func TestRoutingIPv6PolicyRulesPersistAndUseTheFamilyOnAddAndDelete(t *testing.T) {
	rec := rtHost(t).on("ip -6 rule add", "").on("ip -6 rule del", "")
	rec.replies = append([]reply{{prefix: "ip -j -6 rule show", out: `[{"priority":10000,"src":"all","table":7}]`}}, rec.replies...)
	s := testService(t)
	r, err := s.AddRule(context.Background(), RuleRequest{From: "2001:db8:88::7/64", Table: 100}, rtClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if r.Family != "inet6" || r.Priority != 10001 || r.From != "2001:db8:88::/64" {
		t.Fatalf("rule = %+v", r)
	}
	want := "rule add priority 10001 from 2001:db8:88::/64 lookup 100"
	if b, err := os.ReadFile(filepath.Join(s.paths.Dir, rules6File)); err != nil || !strings.Contains(string(b), want) {
		t.Fatalf("IPv6 boot file = %q, %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile)); strings.Contains(string(b), "2001:db8:88") {
		t.Fatalf("IPv6 rule reached IPv4 batch: %s", b)
	}
	if err := s.DeleteRule(context.Background(), r.ID, rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	if !rec.ran("ip -6 rule add priority 10001") || !rec.ran("ip -6 rule del priority 10001") || len(rtLoad(t, s).Rules) != 0 {
		t.Fatalf("commands = %v", rec.commands())
	}
}

func TestRoutingIPv6InterfaceAndMarkRulesHaveAnExplicitFamily(t *testing.T) {
	for _, req := range []RuleRequest{
		{Family: "inet6", OIF: "tailscale0", Action: "blackhole"},
		{Family: "ipv6", FWMark: "0x10", Table: 100},
		{From: "::/0", IIF: "eth0", Table: 100},
	} {
		r, err := req.spec()
		if err != nil || r.Family != "inet6" {
			t.Fatalf("request %+v -> %+v, %v", req, r, err)
		}
		if shadowsReplies(r, Path{Address: "100.110.34.9", Source: "100.110.34.31", Device: "tailscale0"}) {
			t.Fatalf("IPv6 rule shadows IPv4 replies: %+v", r)
		}
	}
	if _, err := ruleArgs(RuleSpec{Family: "inet6", Priority: 10000, From: "10.0.0.0/8", Action: "lookup", Table: 100}); err == nil {
		t.Fatal("mismatched family in edited spec reached the boot file")
	}
}

func TestRoutingAddRuleRefusals(t *testing.T) {
	cases := []struct {
		name string
		req  RuleRequest
		want string
	}{
		{"a rule that selects every packet", RuleRequest{Table: 200}, "no selector"},
		{"a blackhole of everything", RuleRequest{Action: "blackhole"}, "no selector"},
		{"a prohibit of the client's address", RuleRequest{To: "100.110.34.9/32", Action: "prohibit"}, "discard the replies to your connection"},
		{"an unreachable of the client's network", RuleRequest{To: "100.64.0.0/10", Action: "unreachable"}, "discard the replies to your connection"},
		{"a blackhole of the server's own source", RuleRequest{From: "100.110.34.31/32", Action: "blackhole"}, "discard the replies to your connection"},
		{"a blackhole of the device the replies leave by", RuleRequest{OIF: "tailscale0", Action: "blackhole"}, "discard the replies to your connection"},
		{"a blackhole of unmarked replies", RuleRequest{FWMark: "0", Action: "blackhole"}, "discard the replies to your connection"},
		{"a zero mark mask", RuleRequest{FWMark: "0x10/0", Table: 100}, "mask of zero"},
		{"table 52", RuleRequest{From: "10.9.0.0/24", Table: 52}, "Tailscale"},
		{"table 255", RuleRequest{From: "10.9.0.0/24", Table: 255}, "local"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := rtHost(t)
			s := testService(t)
			_, err := s.AddRule(context.Background(), c.req, rtClient, "ion")
			g := rtGuarded(t, err)
			if !strings.Contains(g.Reason, c.want) {
				t.Fatalf("reason = %q, want %q", g.Reason, c.want)
			}
			if m := rtMutations(rec); len(m) != 0 {
				t.Fatalf("a refused rule ran %v", m)
			}
		})
	}
}

func TestRoutingAddRuleAllowsDiscardingWhatIsNotTheClients(t *testing.T) {
	rec := rtHost(t).on("ip rule add", "")
	s := testService(t)
	for _, req := range []RuleRequest{
		{To: "198.51.100.0/24", Action: "blackhole"},
		{From: "10.9.0.0/16", Action: "prohibit"},
		{IIF: "jd-lan", To: "100.110.34.9/32", Action: "blackhole"}, // forwarded traffic, not the replies
		{FWMark: "0x7", Action: "unreachable"},
		{OIF: "eth0", Action: "blackhole"},
	} {
		if _, err := s.AddRule(context.Background(), req, rtClient, "ion"); err != nil {
			t.Errorf("%+v refused: %v", req, err)
		}
	}
	if len(rtMutations(rec)) != 5 {
		t.Fatalf("mutations = %v", rtMutations(rec))
	}
}

func TestRoutingAddRuleIsTakenBackWhenItMovesTheRepliesFromTheServersSource(t *testing.T) {
	rec := rtHost(t).on("ip rule add", "").on("ip rule del", "")
	rtAnswerAfter(rec, "ip rule add", "ip -j route get 100.110.34.9 from", fixture(t, "routing-route-get-moved.json"))
	s := testService(t)
	_, err := s.AddRule(context.Background(), RuleRequest{From: "100.110.34.0/24", Table: 200}, rtClient, "ion")
	g := rtGuarded(t, err)
	if !strings.Contains(g.Reason, "when they come from 100.110.34.31") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip rule add priority 10000 from 100.110.34.0/24 lookup 200|ip rule del priority 10000 from 100.110.34.0/24 lookup 200" {
		t.Fatalf("mutations = %v", got)
	}
	if rtSpecWritten(s) {
		t.Fatal("a rolled-back rule reached the spec")
	}
}

func TestRoutingAddRuleIsTakenBackWhenTheKernelHasNoRouteLeft(t *testing.T) {
	rec := rtHost(t).on("ip rule add", "").on("ip rule del", "")
	prev := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		if strings.HasPrefix(line, "ip -j route get 100.110.34.9 from") && rec.ran("ip rule add") {
			rec.mu.Lock()
			rec.calls = append(rec.calls, line)
			rec.mu.Unlock()
			return "", errors.New("ip: RTNETLINK answers: Network is unreachable")
		}
		return prev(ctx, name, args...)
	}
	t.Cleanup(func() { run = prev })
	s := testService(t)
	_, err := s.AddRule(context.Background(), RuleRequest{From: "100.110.34.0/24", Table: 200}, rtClient, "ion")
	g := rtGuarded(t, err)
	if !strings.Contains(g.Reason, "no route for the replies") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if !rec.ran("ip rule del") {
		t.Fatal("not rolled back")
	}
}

func TestRoutingDeleteRule(t *testing.T) {
	spec := func() *Spec {
		sp := emptySpec()
		sp.Rules = []RuleSpec{{ID: 5, Family: "inet", Priority: 10000, From: "192.168.50.0/24", Action: "lookup", Table: 200}}
		return sp
	}
	t.Run("removes the rule and its entry", func(t *testing.T) {
		rec := rtHost(t).on("ip rule del", "")
		s := testService(t)
		rtSaveSpec(t, s, spec())
		if err := s.DeleteRule(context.Background(), 5, rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip rule del priority 10000 from 192.168.50.0/24 lookup 200" {
			t.Fatalf("mutations = %v", got)
		}
		if len(rtLoad(t, s).Rules) != 0 {
			t.Fatal("the entry stayed")
		}
	})
	t.Run("a rule that is not the dashboard's", func(t *testing.T) {
		rec := rtHost(t)
		s := testService(t)
		rtSaveSpec(t, s, spec())
		if err := s.DeleteRule(context.Background(), 5270, rtClient, "ion"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatal("a refused delete ran")
		}
	})
	t.Run("is put back when the path moves", func(t *testing.T) {
		rec := rtHost(t).on("ip rule del", "").on("ip rule add", "")
		rtAnswerAfter(rec, "ip rule del", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
		s := testService(t)
		rtSaveSpec(t, s, spec())
		rtGuarded(t, s.DeleteRule(context.Background(), 5, rtClient, "ion"))
		if got := rtMutations(rec); strings.Join(got, "|") != "ip rule del priority 10000 from 192.168.50.0/24 lookup 200|ip rule add priority 10000 from 192.168.50.0/24 lookup 200" {
			t.Fatalf("mutations = %v", got)
		}
	})
	t.Run("one the kernel already lost", func(t *testing.T) {
		rtHost(t).fail("ip rule del", "RTNETLINK answers: No such file or directory")
		s := testService(t)
		rtSaveSpec(t, s, spec())
		if err := s.DeleteRule(context.Background(), 5, rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRoutingCanonicalFWMark(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"": "", "16": "0x10", "0x10": "0x10", "0X10": "0x10", "0x10/0xff": "0x10/0xff",
		"0x10/0xffffffff": "0x10", "0x80000/0xff0000": "0x80000/0xff0000",
	}
	for in, want := range cases {
		got, err := canonicalFWMark(in)
		if err != nil || got != want {
			t.Errorf("canonicalFWMark(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"x", "0x1ffffffff", "1/zz", "-1", "1/0"} {
		if _, err := canonicalFWMark(bad); err == nil {
			t.Errorf("canonicalFWMark(%q) accepted", bad)
		}
	}
}

// A request that arrives over an SSH tunnel to loopback carries the SSH
// peer's address as its client, so the sentences must read right for a client
// that is not a browser, and a client that really is loopback has no path to
// protect.
func TestRoutingRefusalsReadWellForAnSSHPeerClient(t *testing.T) {
	const peer = "198.51.100.77"
	rec := rtHost(t)
	rec.replies = append([]reply{{prefix: "ip -j route get 198.51.100.77", out: `[{"dst":"198.51.100.77","gateway":"203.0.113.1","dev":"eth0","prefsrc":"203.0.113.20","flags":[]}]`}}, rec.replies...)
	s := testService(t)
	_, err := s.AddRule(context.Background(), RuleRequest{To: peer + "/32", Action: "prohibit"}, peer, "ion")
	g := rtGuarded(t, err)
	if strings.Contains(strings.ToLower(g.Reason), "browser") || !strings.Contains(g.Reason, peer) {
		t.Fatalf("reason = %q", g.Reason)
	}
	_, err = s.RemoveAddress(context.Background(), "eth0", "203.0.113.20/24", peer, "ion")
	if !errors.Is(err, ErrNotManaged) {
		t.Fatalf("an address nobody added: err = %v", err)
	}
	sp := emptySpec()
	sp.Addresses = []AddressSpec{{ID: 1, Link: "eth0", CIDR: "203.0.113.20/24"}}
	rtSaveSpec(t, s, sp)
	_, err = s.RemoveAddress(context.Background(), "eth0", "203.0.113.20/24", peer, "ion")
	if g := rtGuarded(t, err); strings.Contains(strings.ToLower(g.Reason), "browser") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if len(rtMutations(rec)) != 0 {
		t.Fatalf("a refused change ran %v", rtMutations(rec))
	}
}

func TestRoutingALoopbackClientHasNoPathToProtect(t *testing.T) {
	rec := rtHost(t).on("ip rule add", "")
	s := testService(t)
	if _, err := s.AddRule(context.Background(), RuleRequest{To: "100.110.34.9/32", Action: "blackhole"}, "127.0.0.1", "ion"); err != nil {
		t.Fatal(err)
	}
	if got := rtMutations(rec); len(got) != 1 {
		t.Fatalf("mutations = %v", got)
	}
}
