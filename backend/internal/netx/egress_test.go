package netx

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func egressRequest() EgressGroupRequest {
	return EgressGroupRequest{
		Name:   "uplinks",
		Policy: EgressPolicy{Kind: "all"},
		Members: []EgressMember{
			{Name: "primary", Kind: "gateway", Gateway: "192.0.2.1", Device: "eth0", Priority: 1},
			{Name: "backup", Kind: "gateway", Gateway: "198.51.100.1", Device: "eth1", Priority: 2},
		},
		Probes: []EgressProbe{{Kind: "icmp", Target: "1.1.1.1"}, {Kind: "tcp", Target: "9.9.9.9"}},
	}
}

func egressGroup(t *testing.T, mutate func(*EgressGroupRequest)) EgressGroupSpec {
	t.Helper()
	req := egressRequest()
	if mutate != nil {
		mutate(&req)
	}
	g, err := req.config(nil)
	if err != nil {
		t.Fatal(err)
	}
	g.ID, g.Slot = 7, 1
	g.Active = g.firstTier()
	return g
}

func TestEgressRequestValidation(t *testing.T) {
	g := egressGroup(t, nil)
	if g.Family != "inet" || g.Connections != "flush" || g.Failback != "automatic" || g.Thresholds != defaultEgressThresholds {
		t.Fatalf("defaults = %+v", g)
	}
	if g.Members[0].ID != 1 || g.Members[1].ID != 2 || g.Members[0].Weight != 1 || g.Probes[1].Port != 443 {
		t.Fatalf("members/probes = %+v %+v", g.Members, g.Probes)
	}
	for name, mutate := range map[string]func(*EgressGroupRequest){
		"one member":          func(r *EgressGroupRequest) { r.Members = r.Members[:1] },
		"same next hop":       func(r *EgressGroupRequest) { r.Members[1].Gateway, r.Members[1].Device = "192.0.2.1", "eth0" },
		"gateway family":      func(r *EgressGroupRequest) { r.Members[1].Gateway = "2001:db8::1" },
		"tunnel with gateway": func(r *EgressGroupRequest) { r.Members[1].Kind = "tunnel" },
		"all with selector":   func(r *EgressGroupRequest) { r.Policy.From = "10.0.0.0/8" },
		"selector without":    func(r *EgressGroupRequest) { r.Policy = EgressPolicy{Kind: "selector"} },
		"selector of all":     func(r *EgressGroupRequest) { r.Policy = EgressPolicy{Kind: "selector", To: "0.0.0.0/0"} },
		"mark in member bits": func(r *EgressGroupRequest) { r.Policy = EgressPolicy{Kind: "rule", FWMark: "0x100"} },
		"sticky keep":         func(r *EgressGroupRequest) { r.Sticky, r.Connections = true, "keep" },
		"sticky mark":         func(r *EgressGroupRequest) { r.Sticky, r.Policy = true, EgressPolicy{Kind: "rule", FWMark: "0x10000"} },
		"latency over timeout": func(r *EgressGroupRequest) {
			r.Thresholds = defaultEgressThresholds
			r.Thresholds.LatencyMillis = 1200
		},
		"timeout over interval": func(r *EgressGroupRequest) {
			r.Thresholds = defaultEgressThresholds
			r.Thresholds.IntervalSeconds, r.Thresholds.TimeoutMillis = 2, 2500
		},
		"partial thresholds": func(r *EgressGroupRequest) { r.Thresholds = EgressThresholds{HoldSeconds: 30} },
		"protect everything": func(r *EgressGroupRequest) { r.Protected = []string{"0.0.0.0/0"} },
		"probe of a name":    func(r *EgressGroupRequest) { r.Probes = []EgressProbe{{Kind: "icmp", Target: "one.one.one.one"}} },
		"probe on loopback":  func(r *EgressGroupRequest) { r.Probes = []EgressProbe{{Kind: "icmp", Target: "127.0.0.1"}} },
		"too many probes": func(r *EgressGroupRequest) {
			r.Probes = []EgressProbe{{Kind: "icmp", Target: "1.1.1.1"}, {Kind: "icmp", Target: "1.0.0.1"}, {Kind: "icmp", Target: "9.9.9.9"}, {Kind: "icmp", Target: "8.8.8.8"}, {Kind: "icmp", Target: "8.8.4.4"}}
		},
		"newline in name": func(r *EgressGroupRequest) { r.Name = "a\nb" },
	} {
		t.Run(name, func(t *testing.T) {
			req := egressRequest()
			mutate(&req)
			if _, err := req.config(nil); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	// A mark outside the member space selects traffic without colliding.
	req := egressRequest()
	req.Policy = EgressPolicy{Kind: "rule", FWMark: "0x10000/0xff0000"}
	if _, err := req.config(nil); err != nil {
		t.Fatal(err)
	}
	// Member ids survive an edit; a new member takes the first free id.
	prev := egressGroup(t, nil)
	edit := egressRequest()
	edit.Members = []EgressMember{
		{ID: 2, Name: "backup", Kind: "gateway", Gateway: "198.51.100.1", Device: "eth1", Priority: 2},
		{Name: "tunnel", Kind: "tunnel", Device: "wg0", Priority: 3},
	}
	g2, err := edit.config(&prev)
	if err != nil {
		t.Fatal(err)
	}
	if g2.Members[0].ID != 1 || g2.Members[0].Name != "tunnel" || g2.Members[1].ID != 2 {
		t.Fatalf("ids = %+v", g2.Members)
	}
}

func TestEgressObjectsOwnReservedTablesRulesAndMarks(t *testing.T) {
	g := egressGroup(t, func(r *EgressGroupRequest) {
		r.Members[1] = EgressMember{Name: "tunnel", Kind: "tunnel", Device: "wg0", Priority: 2}
		r.Protected = []string{"203.0.113.9/32"}
	})
	disabled := egressObjects(g)
	var got []string
	for _, o := range disabled {
		got = append(got, strings.Join(o.add, " "))
	}
	want := []string{
		"route replace default via 192.0.2.1 dev eth0 table 7711",
		"rule add priority 19016 fwmark 0x900/0x7f00 lookup 7711",
		"route replace default dev wg0 table 7712",
		"rule add priority 19017 fwmark 0xa00/0x7f00 lookup 7712",
		"route replace default via 192.0.2.1 dev eth0 table 7710",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("disabled objects:\n%s", strings.Join(got, "\n"))
	}
	g.Enabled = true
	got = nil
	for _, o := range egressObjects(g)[len(disabled):] {
		got = append(got, strings.Join(o.add, " "))
	}
	want = []string{
		"rule add priority 19024 to 203.0.113.9/32 lookup main",
		"rule add priority 19028 lookup main suppress_prefixlength 0",
		"rule add priority 19029 lookup 7710",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("enabled objects:\n%s", strings.Join(got, "\n"))
	}
	// A selector policy selects; it needs no suppress rule.
	g.Policy = EgressPolicy{Kind: "selector", From: "10.8.0.0/24"}
	g.Protected = nil
	last := egressObjects(g)
	if s := strings.Join(last[len(last)-1].add, " "); s != "rule add priority 19029 from 10.8.0.0/24 lookup 7710" {
		t.Fatal(s)
	}
	// Two members of one tier share the group route by weight.
	g.Members[1].Priority, g.Members[1].Weight = 1, 3
	g.Active = []int{1, 2}
	route, _ := egressGroupRoute(g)
	if s := strings.Join(route, " "); s != "nexthop via 192.0.2.1 dev eth0 weight 1 nexthop dev wg0 weight 3" {
		t.Fatal(s)
	}
	// IPv6 groups name the family on every runtime command.
	g6 := egressGroup(t, func(r *EgressGroupRequest) {
		r.Family = "inet6"
		r.Members[0].Gateway, r.Members[1].Gateway = "fe80::1", "fe80::2"
		r.Probes = []EgressProbe{{Kind: "icmp", Target: "2606:4700:4700::1111"}}
	})
	o := egressObjects(g6)[0]
	if s := strings.Join(o.argv(o.add), " "); s != "-6 route replace ::/0 via fe80::1 dev eth0 table 7711" {
		t.Fatal(s)
	}
}

func TestEgressBootFilesRestoreTheDecidedMember(t *testing.T) {
	g := egressGroup(t, nil)
	g.Enabled, g.Active = true, []int{2}
	sp := emptySpec()
	sp.EgressGroups = []EgressGroupSpec{g}
	links := renderLinks(sp)
	for _, line := range []string{
		"route replace default via 198.51.100.1 dev eth1 table 7710\n",
		"route replace default via 192.0.2.1 dev eth0 table 7711\n",
		"rule add priority 19029 lookup 7710\n",
	} {
		if !strings.Contains(links, line) {
			t.Fatalf("links.batch lacks %q:\n%s", line, links)
		}
	}
	if strings.Contains(renderIPv6Rules(sp), "7710") {
		t.Fatal("an IPv4 group is in the IPv6 batch")
	}
	s := testService(t)
	unit := s.unitFor(sp)
	if !strings.Contains(unit, "ExecStart=-nft -f "+s.paths.Dir+"/egress.nft") || !strings.Contains(unit, "ExecStop=-nft delete table inet jd_egress") {
		t.Fatalf("unit:\n%s", unit)
	}
	// A spec that never had a group renders exactly what it always did.
	files, err := s.renderAll(emptySpec())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files[s.paths.Dir+"/egress.nft"]; ok {
		t.Fatal("egress.nft rendered for a host without groups")
	}
	if strings.Contains(s.unitFor(emptySpec()), "egress.nft") || strings.Contains(s.unitFor(emptySpec()), "jd_egress") {
		t.Fatal("unit mentions egress without groups")
	}
}

func TestEgressSwitchIsOneRouteReplaceAndUndoesItself(t *testing.T) {
	from := egressGroup(t, nil)
	from.Enabled = true
	to := from
	to.Active = []int{2}
	forward := egressCommands(&from, &to)
	if len(forward) != 1 || strings.Join(forward[0].Args, " ") != "route replace default via 198.51.100.1 dev eth1 table 7710" {
		t.Fatalf("switch = %+v", forward)
	}
	back := egressCommands(&to, &from)
	if len(back) != 1 || strings.Join(back[0].Args, " ") != "route replace default via 192.0.2.1 dev eth0 table 7710" {
		t.Fatalf("undo = %+v", back)
	}
	// Disabling removes the traffic-moving rules, selector first.
	off := from
	off.Enabled = false
	cmds := egressCommands(&from, &off)
	var lines []string
	for _, c := range cmds {
		lines = append(lines, strings.Join(c.Args, " "))
		if !c.AllowGone {
			t.Fatalf("a removal must accept absence: %+v", c)
		}
	}
	if !slices.Equal(lines, []string{"rule del priority 19029 lookup 7710", "rule del priority 19028 lookup main suppress_prefixlength 0"}) {
		t.Fatalf("disable = %v", lines)
	}
	// Deleting the group removes every object it owns.
	gone := egressCommands(&from, nil)
	if len(gone) != len(egressObjects(from)) {
		t.Fatalf("delete = %+v", gone)
	}
}

func TestEgressRecoveryPlanReturnsToThePreviousDecision(t *testing.T) {
	record(t)
	s := testService(t)
	old := emptySpec()
	g := egressGroup(t, nil)
	g.Enabled = true
	old.EgressGroups = []EgressGroupSpec{g}
	next := old.clone()
	next.EgressGroups[0].Active = []int{2}
	cmds, err := s.recoveryPlan(context.Background(), old, next)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, c := range cmds {
		lines = append(lines, c.Tool+" "+strings.Join(c.Args, " "))
	}
	if !slices.Equal(lines, []string{"ip route replace default via 192.0.2.1 dev eth0 table 7710"}) {
		t.Fatalf("plan = %v", lines)
	}
	// A new sticky group's pinning is removed with it.
	created := old.clone()
	sticky := egressGroup(t, func(r *EgressGroupRequest) { r.Sticky = true })
	sticky.ID, sticky.Slot, sticky.Enabled = 9, 2, true
	created.EgressGroups = append(created.EgressGroups, sticky)
	cmds, err = s.recoveryPlan(context.Background(), old, created)
	if err != nil {
		t.Fatal(err)
	}
	last := cmds[len(cmds)-1]
	if last.Tool != "nft" || strings.Join(last.Args, " ") != "delete table inet jd_egress" || !last.AllowGone {
		t.Fatalf("plan ends with %+v", last)
	}
	if len(cmds)-1 != len(egressObjects(sticky)) {
		t.Fatalf("plan = %+v", cmds)
	}
}

func TestEgressPinningRuleset(t *testing.T) {
	sp := emptySpec()
	g := egressGroup(t, func(r *EgressGroupRequest) {
		r.Sticky = true
		r.Members[1] = EgressMember{Name: "tunnel", Kind: "tunnel", Device: "wg0", Priority: 2}
	})
	sp.EgressGroups = []EgressGroupSpec{g}
	if out := renderEgressNFT(sp); strings.Contains(out, "chain") {
		t.Fatalf("a disabled sticky group pins nothing:\n%s", out)
	}
	sp.EgressGroups[0].Enabled = true
	out := renderEgressNFT(sp)
	for _, want := range []string{
		"table inet jd_egress\ndelete table inet jd_egress\n",
		"type route hook output priority mangle",
		"meta mark 0x0 ct direction original ct mark and 0x7f00 != 0x0 meta mark set ct mark and 0x7f00",
		"meta nfproto ipv4 oifname \"eth0\" rt ip nexthop 192.0.2.1 ct mark set ct mark and 0xffff80ff or 0x900 comment \"egress-pin:7:1\"",
		"meta nfproto ipv4 oifname \"wg0\" ct mark set ct mark and 0xffff80ff or 0xa00 comment \"egress-pin:7:2\"",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ruleset lacks %q:\n%s", want, out)
		}
	}
}

func TestEgressSelectsReply(t *testing.T) {
	g := egressGroup(t, nil)
	path := Path{Address: "203.0.113.9", Source: "192.0.2.10", Device: "eth0", Gateway: "192.0.2.1"}
	if egressSelectsReply(g, path) {
		t.Fatal("a disabled group carries nothing")
	}
	g.Enabled = true
	if !egressSelectsReply(g, path) {
		t.Fatal("a whole-host group carries the operator's replies")
	}
	g.Protected = []string{"203.0.113.0/24"}
	if egressSelectsReply(g, path) {
		t.Fatal("a protected operator stays on main")
	}
	g.Protected = nil
	g.Policy = EgressPolicy{Kind: "selector", From: "10.8.0.0/24"}
	if egressSelectsReply(g, path) {
		t.Fatal("replies from the host's own address are not from the VPN")
	}
	g.Policy = EgressPolicy{Kind: "selector", To: "203.0.113.0/24"}
	if !egressSelectsReply(g, path) {
		t.Fatal("a destination selector covering the operator carries the replies")
	}
	g.Policy = EgressPolicy{Kind: "rule", FWMark: "0x10000/0xff0000"}
	if egressSelectsReply(g, path) {
		t.Fatal("replies carry no mark")
	}
	if egressSelectsReply(g, Path{Address: "127.0.0.1", Local: true}) {
		t.Fatal("a local client has no path to take")
	}
}

func TestSameEgressRouteReadsBackMultipath(t *testing.T) {
	got := egressRoute{{Gateway: "192.0.2.1", Device: "eth0", Weight: 1}, {Device: "wg0", Weight: 3}}
	if !sameEgressRoute(got, []string{"nexthop", "dev", "wg0", "weight", "3", "nexthop", "via", "192.0.2.1", "dev", "eth0", "weight", "1"}) {
		t.Fatal("multipath mismatch")
	}
	if sameEgressRoute(got, []string{"via", "192.0.2.1", "dev", "eth0"}) {
		t.Fatal("a single hop is not the multipath route")
	}
	if !sameEgressRoute(egressRoute{{Gateway: "192.0.2.1", Device: "eth0", Weight: 1}}, []string{"via", "192.0.2.1", "dev", "eth0"}) {
		t.Fatal("single hop mismatch")
	}
}

func TestEgressMemberStateHysteresis(t *testing.T) {
	th := defaultEgressThresholds
	th.FailAfter, th.RecoverAfter, th.Window = 3, 3, 1
	st := newEgressMemberState("k")
	at := time.Unix(1000, 0)
	good := func() EgressSample {
		at = at.Add(10 * time.Second)
		return EgressSample{At: at, OK: 2, Total: 2, LatencyMillis: 20}
	}
	bad := func() EgressSample { at = at.Add(10 * time.Second); return EgressSample{At: at, OK: 0, Total: 2} }
	for i := 0; i < 2; i++ {
		if changed, _ := st.observe(good(), th); changed != "" {
			t.Fatal("up before three good samples")
		}
	}
	if changed, _ := st.observe(good(), th); changed != "up" || st.State != "up" {
		t.Fatal("not up after three good samples")
	}
	st.observe(bad(), th)
	st.observe(bad(), th)
	if st.State != "up" {
		t.Fatal("down before three bad samples")
	}
	if changed, s := st.observe(bad(), th); changed != "down" || s.Why != "no probe answered" {
		t.Fatalf("not down: %q %+v", changed, s)
	}
	// Flapping never strings three good samples together.
	for i := 0; i < 10; i++ {
		st.observe(good(), th)
		st.observe(bad(), th)
	}
	if st.State != "down" {
		t.Fatal("a flapping member recovered")
	}
	// Latency over the threshold is a bad sample even when every probe answers.
	slow := EgressSample{At: at, OK: 2, Total: 2, LatencyMillis: 900}
	judgeSample(&slow, nil, th)
	if slow.Good || !strings.Contains(slow.Why, "median round trip") {
		t.Fatalf("slow = %+v", slow)
	}
	// Loss is judged over the window.
	th.Window, th.LossPercent = 4, 30
	window := []EgressSample{{OK: 2, Total: 2}, {OK: 1, Total: 2}, {OK: 1, Total: 2}}
	s := EgressSample{OK: 2, Total: 2, LatencyMillis: 10}
	judgeSample(&s, window, th)
	if s.Loss != 25 || !s.Good {
		t.Fatalf("25%% loss under a 30%% threshold is good: %+v", s)
	}
	th.LossPercent = 20
	judgeSample(&s, window, th)
	if s.Good || !strings.Contains(s.Why, "loss 25%") {
		t.Fatalf("25%% loss over a 20%% threshold is bad: %+v", s)
	}
}

func TestDecideEgress(t *testing.T) {
	g := egressGroup(t, nil)
	g.Thresholds.StableSeconds, g.Thresholds.HoldSeconds = 120, 60
	now := time.Unix(10000, 0)
	up := func(since time.Duration) egressMemberView {
		return egressMemberView{State: "up", Since: now.Add(-since), Fresh: true}
	}
	down := egressMemberView{State: "down", Since: now.Add(-time.Minute), Fresh: true, Why: "no probe answered"}
	cases := []struct {
		name     string
		active   []int
		views    map[int]egressMemberView
		decided  time.Duration
		failback string
		action   string
		target   []int
		reason   string
	}{
		{"steady", []int{1}, map[int]egressMemberView{1: up(time.Hour), 2: up(time.Hour)}, time.Hour, "", "none", nil, ""},
		{"failover at once", []int{1}, map[int]egressMemberView{1: down, 2: up(time.Hour)}, time.Second, "", "failover", []int{2}, "primary is down (no probe answered)"},
		{"no proven member", []int{1}, map[int]egressMemberView{1: down, 2: down}, time.Hour, "", "hold", nil, "keeps the route"},
		{"unproven backup", []int{1}, map[int]egressMemberView{1: down, 2: {State: "up", Fresh: false}}, time.Hour, "", "hold", nil, "keeps the route"},
		{"not down stays", []int{1}, map[int]egressMemberView{1: {State: "unknown"}, 2: up(time.Hour)}, time.Hour, "", "none", nil, ""},
		{"failback waits stable", []int{2}, map[int]egressMemberView{1: up(40 * time.Second), 2: up(time.Hour)}, time.Hour, "", "hold", []int{1}, "40s of the 2m0s stable recovery"},
		{"failback waits hold", []int{2}, map[int]egressMemberView{1: up(time.Hour), 2: up(time.Hour)}, 20 * time.Second, "", "hold", []int{1}, "hold time"},
		{"failback", []int{2}, map[int]egressMemberView{1: up(3 * time.Minute), 2: up(time.Hour)}, time.Hour, "", "failback", []int{1}, "back and stable"},
		{"manual failback", []int{2}, map[int]egressMemberView{1: up(time.Hour), 2: up(time.Hour)}, time.Hour, "manual", "hold", []int{1}, "by hand"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gg := g
			gg.Active = c.active
			gg.DecidedAt = now.Add(-c.decided)
			if c.failback != "" {
				gg.Failback = c.failback
			}
			d := decideEgress(gg, c.views, now)
			if d.Action != c.action || !equalIDs(d.Target, c.target) && c.target != nil || !strings.Contains(d.Reason, c.reason) {
				t.Fatalf("decision = %+v", d)
			}
		})
	}
	// One member of a multipath tier failing leaves the rest of the tier.
	mp := g
	mp.Members = append(append([]EgressMember{}, g.Members...), EgressMember{ID: 3, Name: "second", Kind: "gateway", Gateway: "192.0.2.2", Device: "eth2", Priority: 1, Weight: 1})
	mp.Active = []int{1, 3}
	d := decideEgress(mp, map[int]egressMemberView{1: down, 2: up(time.Hour), 3: up(time.Hour)}, now)
	if d.Action != "rebalance" || !equalIDs(d.Target, []int{3}) {
		t.Fatalf("multipath = %+v", d)
	}
}

// simulateDecisions drives the simulation's phases with samples computed from
// its faults instead of a kernel: a dropped link answers nothing, a delayed one
// answers late, a clean one at once.
func simulateDecisions(g EgressGroupSpec) *EgressSimulationResult {
	plan := planEgressSimulation(g)
	sim := g
	sim.Enabled, sim.Policy, sim.Active = true, EgressPolicy{Kind: "all"}, g.firstTier()
	states := map[int]*egressMemberState{}
	for _, m := range sim.Members {
		states[m.ID] = newEgressMemberState("")
	}
	result := &EgressSimulationResult{}
	epoch := time.Unix(0, 0)
	interval := time.Duration(g.Thresholds.IntervalSeconds) * time.Second
	index := 0
	for _, ph := range plan.phases {
		phase := EgressSimPhase{Name: ph.name, Start: index, End: index + ph.steps - 1}
		for step := 0; step < ph.steps; step++ {
			now := epoch.Add(time.Duration(index) * interval)
			samples, faults := map[int]EgressSample{}, map[int]string{}
			for _, m := range sim.Members {
				args := ph.fault(m.ID, step)
				faults[m.ID] = strings.Join(args, " ")
				s := EgressSample{At: now, Total: len(g.Probes)}
				switch {
				case len(args) == 2 && args[0] == "loss":
				case len(args) == 2 && args[0] == "delay":
					ms := 0
					for _, c := range strings.TrimSuffix(args[1], "ms") {
						ms = ms*10 + int(c-'0')
					}
					if ms < g.Thresholds.TimeoutMillis {
						s.OK, s.LatencyMillis = s.Total, float64(ms)
					}
				default:
					s.OK, s.LatencyMillis = s.Total, 1
				}
				samples[m.ID] = s
			}
			st := egressSimStep(&sim, states, samples, faults, now, epoch, index, ph.name)
			st.DataPath = "did not reach"
			healthy := true
			for _, id := range st.Active {
				if faults[id] != "" {
					healthy = false
				}
			}
			if healthy {
				st.DataPath = "reached"
			}
			result.Steps = append(result.Steps, st)
			index++
		}
		result.Phases = append(result.Phases, phase)
	}
	result.Expectations = judgeEgressSimulation(g, sim, plan, result)
	return result
}

func TestEgressSimulationJudgesTheRules(t *testing.T) {
	g := egressGroup(t, nil)
	r := simulateDecisions(g)
	for _, e := range r.Expectations {
		if !e.Passed {
			t.Errorf("%s: %s", e.Name, e.Detail)
		}
	}
	if len(r.Expectations) < 9 {
		t.Fatalf("expectations = %+v", r.Expectations)
	}
	// Without hysteresis, stability or hold, a flapping primary is failed
	// back to: the simulation fails and automation stays off.
	loose := egressGroup(t, func(req *EgressGroupRequest) {
		req.Thresholds = EgressThresholds{IntervalSeconds: 10, TimeoutMillis: 1000, LatencyMillis: 300, LossPercent: 20, Window: 1, FailAfter: 1, RecoverAfter: 1, HoldSeconds: 0, StableSeconds: 0}
	})
	r = simulateDecisions(loose)
	failed := map[string]bool{}
	for _, e := range r.Expectations {
		if !e.Passed {
			failed[e.Name] = true
		}
	}
	if !failed["A flapping member is not failed back to"] || egressExpectationsPass(r.Expectations) {
		t.Fatalf("flap suppression was not judged: %+v", r.Expectations)
	}
	// A manual group never fails back on its own.
	manual := egressGroup(t, func(req *EgressGroupRequest) { req.Failback = "manual" })
	r = simulateDecisions(manual)
	if !egressExpectationsPass(r.Expectations) {
		t.Fatalf("manual = %+v", r.Expectations)
	}
	for _, st := range r.Steps {
		if st.Phase == "recovery" && st.Switched {
			t.Fatal("a manual group failed back")
		}
	}
}

func TestEgressAutomationNeedsAPassedSimulationOfThisConfiguration(t *testing.T) {
	g := egressGroup(t, nil)
	if egressAutomationBlock(g, nil) == "" {
		t.Fatal("a disabled group was automatable")
	}
	g.Enabled = true
	if !strings.Contains(egressAutomationBlock(g, nil), "simulation") {
		t.Fatal("no simulation was required")
	}
	if egressAutomationBlock(g, &EgressSimulation{Status: "passed", Fingerprint: g.Fingerprint()}) != "" {
		t.Fatal("a passed simulation did not authorise automation")
	}
	edited := g
	edited.Thresholds.HoldSeconds = 5
	if edited.Fingerprint() == g.Fingerprint() {
		t.Fatal("thresholds are part of the fingerprint")
	}
	renamed := g
	renamed.Name, renamed.Active = "other", []int{2}
	if renamed.Fingerprint() != g.Fingerprint() {
		t.Fatal("the name and decision are not configuration")
	}
}

func TestReservedEgressTablesAndPrioritiesRefuseManualRoutes(t *testing.T) {
	if _, err := (RouteRequest{Destination: "10.0.0.0/8", Device: "eth0", Table: 7712}).spec(); !errors.Is(err, ErrGuarded) {
		t.Fatalf("route into 7712 = %v", err)
	}
	if _, err := (RuleRequest{From: "10.0.0.0/8", Table: 7710}).spec(); !errors.Is(err, ErrGuarded) {
		t.Fatalf("rule to 7710 = %v", err)
	}
	if _, err := (RuleRequest{From: "10.0.0.0/8", Table: 100, Priority: 19003}).spec(); !errors.Is(err, ErrGuarded) {
		t.Fatalf("priority 19003 = %v", err)
	}
}
