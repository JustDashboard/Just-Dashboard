package netx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// egressHost answers every read an egress change makes, from the shared link
// fixtures: eth0 (203.0.113.20) and jd-lan (192.168.50.1) carry the members,
// and the operator arrives over the tailnet.
func egressHost(t *testing.T, groupRoute string) *recorder {
	t.Helper()
	rec := record(t, "systemctl")
	rec.on("ip -j -d link show", fixture(t, "ip-link.json")).
		on("ip -j link show", fixture(t, "ip-link.json")).
		on("ip -j addr show", fixture(t, "ip-addr.json")).
		on("ip -j route get", fixture(t, "routing-route-get.json")).
		on("ip -j route show default", "[]").
		on("ip -j -6 route show default", "[]").
		on("ip -j route show table 7700 default", groupRoute).
		on("ip -j route show table", "[]").
		on("ip -j -6 route show table", "[]").
		on("ip -j rule show", "[]").
		on("ip -j -6 rule show", "[]").
		on("ip route replace", "").
		on("ip rule add", "").
		on("ip rule del", "").
		on("sysctl -n", "2")
	return rec
}

// stubEgressProbes answers probes without a network: every probe answers in
// ten milliseconds unless fail says otherwise for its path.
func stubEgressProbes(t *testing.T, fail func(egressPath) bool) *[]egressPath {
	t.Helper()
	var seen []egressPath
	prev := egressProbe
	egressProbe = func(_ context.Context, path egressPath, p EgressProbe, _ time.Duration) EgressProbeResult {
		seen = append(seen, path)
		if fail != nil && fail(path) {
			return EgressProbeResult{Kind: p.Kind, Target: p.Target, Error: "no answer before the timeout"}
		}
		return EgressProbeResult{Kind: p.Kind, Target: p.Target, OK: true, RTTMillis: 10}
	}
	t.Cleanup(func() { egressProbe = prev })
	return &seen
}

func egressLabRequest() EgressGroupRequest {
	return EgressGroupRequest{
		Name:   "lab",
		Policy: EgressPolicy{Kind: "all"},
		Members: []EgressMember{
			{Name: "provider", Kind: "gateway", Gateway: "203.0.113.1", Device: "eth0", Priority: 1},
			{Name: "lan", Kind: "gateway", Gateway: "192.168.50.254", Device: "jd-lan", Priority: 2},
		},
		Probes: []EgressProbe{{Kind: "icmp", Target: "1.1.1.1"}},
	}
}

const egressClient = "100.110.34.9"

func TestCreateEgressGroupInstallsMemberPathsWithoutMovingTraffic(t *testing.T) {
	rec := egressHost(t, "[]")
	s := testService(t)
	g, err := s.CreateEgressGroup(context.Background(), egressLabRequest(), egressClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if g.Slot != 0 || !equalIDs(g.Active, []int{1}) || g.Enabled {
		t.Fatalf("group = %+v", g)
	}
	for _, want := range []string{
		"ip route replace default via 203.0.113.1 dev eth0 table 7701",
		"ip rule add priority 19000 fwmark 0x100/0x7f00 lookup 7701",
		"ip route replace default via 192.168.50.254 dev jd-lan table 7702",
		"ip rule add priority 19001 fwmark 0x200/0x7f00 lookup 7702",
		"ip route replace default via 203.0.113.1 dev eth0 table 7700",
	} {
		if !rec.ran(want) {
			t.Errorf("did not run %s:\n%s", want, strings.Join(rec.commands(), "\n"))
		}
	}
	if rec.ran("ip rule add priority 19013") || rec.ran("ip rule add priority 19012") {
		t.Fatal("a disabled group installed its selector")
	}
	sp, _ := s.loadSpec()
	if len(sp.EgressGroups) != 1 || sp.EgressGroups[0].Name != "lab" {
		t.Fatalf("spec = %+v", sp.EgressGroups)
	}
	links, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile))
	if !strings.Contains(string(links), "route replace default via 203.0.113.1 dev eth0 table 7700") {
		t.Fatalf("boot file:\n%s", links)
	}
	events, _ := s.egress.store.list(context.Background(), g.ID, 10)
	if len(events) != 1 || events[0].Kind != "config" {
		t.Fatalf("events = %+v", events)
	}
	// A second group takes the next slot; a third with the same name is refused.
	req := egressLabRequest()
	req.Name = "lab"
	if _, err := s.CreateEgressGroup(context.Background(), req, egressClient, "ion"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate name = %v", err)
	}
}

func TestCreateEgressGroupRefusesForeignObjectsAndUnownedTunnels(t *testing.T) {
	rec := egressHost(t, "[]")
	s := testService(t)
	req := egressLabRequest()
	req.Members[1] = EgressMember{Name: "vpn", Kind: "tunnel", Device: "wg0", Priority: 2}
	if _, err := s.CreateEgressGroup(context.Background(), req, egressClient, "ion"); !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "not a tunnel this dashboard owns") {
		t.Fatalf("hand-written tunnel = %v", err)
	}
	rec.replies = append([]reply{{prefix: "ip -j rule show", out: `[{"priority":19005,"table":"100"}]`}}, rec.replies...)
	if _, err := s.CreateEgressGroup(context.Background(), egressLabRequest(), egressClient, "ion"); !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "19005") {
		t.Fatalf("foreign priority = %v", err)
	}
	rec.replies[0] = reply{prefix: "ip -j rule show", out: `[{"priority":100,"fwmark":"0xa00","fwmask":"0xf00","table":"100"}]`}
	if _, err := s.CreateEgressGroup(context.Background(), egressLabRequest(), egressClient, "ion"); !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "0x7f00") {
		t.Fatalf("foreign mark = %v", err)
	}
	if rec.ran("ip route replace") {
		t.Fatal("a refused group touched the kernel")
	}
}

func egressSpecWith(t *testing.T, s *Service, g EgressGroupSpec) {
	t.Helper()
	sp := emptySpec()
	sp.NextID = g.ID + 1
	sp.EgressGroups = []EgressGroupSpec{g}
	b, _ := json.MarshalIndent(sp, "", "  ")
	if err := os.MkdirAll(s.paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.specPath(), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func labGroup(t *testing.T) EgressGroupSpec {
	g, err := egressLabRequest().config(nil)
	if err != nil {
		t.Fatal(err)
	}
	g.ID, g.Slot, g.Active = 1, 0, []int{1}
	return g
}

func TestEnableEgressGroupProvesMembersAndKeepsTheOperatorOnMain(t *testing.T) {
	rec := egressHost(t, `[{"dst":"default","gateway":"203.0.113.1","dev":"eth0"}]`)
	s := testService(t)
	egressSpecWith(t, s, labGroup(t))
	failing := true
	stubEgressProbes(t, func(p egressPath) bool { return failing })
	if _, err := s.EnableEgressGroup(context.Background(), 1, egressClient, "ion"); !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("unproven member = %v", err)
	}
	if rec.ran("ip rule add priority 19013") {
		t.Fatal("enabled through an unproven member")
	}
	failing = false
	g, err := s.EnableEgressGroup(context.Background(), 1, egressClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if !g.Enabled || len(g.Protected) != 1 || g.Protected[0] != egressClient+"/32" {
		t.Fatalf("group = %+v", g)
	}
	for _, want := range []string{
		"ip rule add priority 19008 to 100.110.34.9/32 lookup main",
		"ip rule add priority 19012 lookup main suppress_prefixlength 0",
		"ip rule add priority 19013 lookup 7700",
	} {
		if !rec.ran(want) {
			t.Errorf("did not run %s", want)
		}
	}
	events, _ := s.egress.store.list(context.Background(), 1, 10)
	if len(events) == 0 || !strings.Contains(events[0].Reason, "Your address 100.110.34.9/32 is kept on the main table") {
		t.Fatalf("events = %+v", events)
	}
}

func TestSwitchEgressReplacesTheGroupRouteAndRecordsTheDecision(t *testing.T) {
	rec := egressHost(t, `[{"dst":"default","gateway":"192.168.50.254","dev":"jd-lan"}]`)
	s := testService(t)
	g := labGroup(t)
	g.Enabled, g.Protected = true, []string{egressClient + "/32"}
	egressSpecWith(t, s, g)
	stubEgressProbes(t, nil)
	res, err := s.SwitchEgress(context.Background(), 1, EgressSwitchRequest{Action: "failover"}, egressClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if !equalIDs(res.After, []int{2}) || !equalIDs(res.Before, []int{1}) {
		t.Fatalf("result = %+v", res)
	}
	if !rec.ran("ip route replace default via 192.168.50.254 dev jd-lan table 7700") {
		t.Fatal("the group route was not replaced")
	}
	sp, _ := s.loadSpec()
	if !equalIDs(sp.EgressGroups[0].Active, []int{2}) || sp.EgressGroups[0].DecidedBy != "ion" {
		t.Fatalf("spec = %+v", sp.EgressGroups[0])
	}
	links, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile))
	if !strings.Contains(string(links), "route replace default via 192.168.50.254 dev jd-lan table 7700") {
		t.Fatal("the boot file does not restore the decided member")
	}
	events, _ := s.egress.store.list(context.Background(), 1, 10)
	if len(events) != 1 || events[0].Outcome != "applied" || events[0].Evidence == nil || events[0].Evidence.RouteAfter == "" || events[0].Evidence.Change == "" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Evidence.Connections == nil || events[0].Evidence.Connections.Error == "" {
		t.Fatal("the connection-tracking evidence (unavailable here) was not reported")
	}
}

func TestSwitchEgressRefusesAPathNotProvenForTheOperator(t *testing.T) {
	rec := egressHost(t, `[{"dst":"default","gateway":"192.168.50.254","dev":"jd-lan"}]`)
	s := testService(t)
	g := labGroup(t)
	g.Enabled = true
	egressSpecWith(t, s, g)
	// The lan member carries probes from its own address, but drops the
	// operator's reply source, as an upstream filtering foreign sources does.
	stubEgressProbes(t, func(p egressPath) bool { return p.Source.String() == "100.110.34.31" })
	_, err := s.SwitchEgress(context.Background(), 1, EgressSwitchRequest{Action: "members", Members: []int{2}}, egressClient, "ion")
	if !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "a probe from 100.110.34.31 through lan failed") {
		t.Fatalf("switch = %v", err)
	}
	if rec.ran("ip route replace default via 192.168.50.254 dev jd-lan table 7700") {
		t.Fatal("the refused switch touched the group route")
	}
	sp, _ := s.loadSpec()
	if !equalIDs(sp.EgressGroups[0].Active, []int{1}) {
		t.Fatal("the decision changed")
	}
	events, _ := s.egress.store.list(context.Background(), 1, 10)
	if len(events) != 1 || events[0].Outcome != "refused" {
		t.Fatalf("events = %+v", events)
	}
}

func TestEgressEditsAndAutomationAreGated(t *testing.T) {
	egressHost(t, "[]")
	s := testService(t)
	g := labGroup(t)
	g.Enabled = true
	egressSpecWith(t, s, g)
	if _, err := s.UpdateEgressGroup(context.Background(), 1, egressLabRequest(), egressClient, "ion"); !errors.Is(err, ErrGuarded) {
		t.Fatalf("edit of an enabled group = %v", err)
	}
	var confirm *ConfirmationError
	if _, err := s.SetEgressAutomation(context.Background(), 1, true, "ion"); !errors.As(err, &confirm) || !strings.Contains(err.Error(), "simulation") {
		t.Fatalf("automation without a simulation = %v", err)
	}
	s.egress.store.saveSimulation(context.Background(), EgressSimulation{ID: "old", GroupID: 1, Fingerprint: "other", Status: "passed", StartedAt: time.Now()})
	if _, err := s.SetEgressAutomation(context.Background(), 1, true, "ion"); !errors.As(err, &confirm) {
		t.Fatalf("a simulation of another configuration = %v", err)
	}
	s.egress.store.saveSimulation(context.Background(), EgressSimulation{ID: "new", GroupID: 1, Fingerprint: g.Fingerprint(), Status: "passed", StartedAt: time.Now()})
	got, err := s.SetEgressAutomation(context.Background(), 1, true, "ion")
	if err != nil || !got.Automation || got.Simulation == nil || got.Simulation.ID != "new" {
		t.Fatalf("automation = %+v, %v", got, err)
	}
}

// A switch whose process died is settled from the decision the recovery
// journal left in the spec, in the durable record.
func TestEgressMonitorSettlesSwitchesAStoppedProcessLeft(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(Options{Paths: testService(t).paths, DB: st.DB})
	g := labGroup(t)
	egressSpecWith(t, s, g)
	ctx := context.Background()
	restored := s.egress.store.record(ctx, EgressEvent{GroupID: 1, Kind: "switch", Action: "failover", Outcome: "applying", Reason: "provider is down", Before: []int{1}, After: []int{2}})
	saved := s.egress.store.record(ctx, EgressEvent{GroupID: 1, Kind: "switch", Action: "failback", Outcome: "applying", Reason: "back", Before: []int{2}, After: []int{1}})
	s.egress.store.saveSimulation(ctx, EgressSimulation{ID: "run", GroupID: 1, Status: "running", StartedAt: time.Now()})
	s.egress.reconcile(ctx)
	events, err := s.egress.store.list(ctx, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	outcome := map[int64]EgressEvent{}
	for _, e := range events {
		outcome[e.ID] = e
	}
	if e := outcome[restored]; e.Outcome != "interrupted" || !strings.Contains(e.Reason, "recovery journal restored the members decided before it") {
		t.Fatalf("interrupted switch = %+v", e)
	}
	if e := outcome[saved]; e.Outcome != "applied" || !strings.Contains(e.Reason, "had been saved") {
		t.Fatalf("saved switch = %+v", e)
	}
	if sim, _ := s.egress.store.simulation(ctx, "run"); sim.Status != "interrupted" {
		t.Fatalf("simulation = %+v", sim)
	}
	if len(s.egress.store.applying(ctx)) != 0 {
		t.Fatal("a switch is still applying")
	}
}

// A change applied pending reconnection confirmation is settled by its own
// journal: confirmed, or restored by the host.
func TestEgressPendingChangesSettleFromTheirJournal(t *testing.T) {
	s := testService(t)
	g := labGroup(t)
	egressSpecWith(t, s, g)
	ctx := context.Background()
	journal := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "abc", Phase: "awaiting_confirmation", Generation: strings.Repeat("0", 64)}}
	if err := journal.save(); err != nil {
		t.Fatal(err)
	}
	waiting := s.egress.store.record(ctx, EgressEvent{GroupID: 1, Kind: "switch", Outcome: "pending", Before: []int{2}, After: []int{1}, Evidence: &EgressEvidence{Change: "abc"}})
	older := s.egress.store.record(ctx, EgressEvent{GroupID: 1, Kind: "switch", Outcome: "pending", Before: []int{1}, After: []int{2}, Evidence: &EgressEvidence{Change: "old"}})
	s.egress.settlePending(ctx)
	outcome := func(id int64) EgressEvent {
		events, _ := s.egress.store.list(ctx, 1, 10)
		for _, e := range events {
			if e.ID == id {
				return e
			}
		}
		return EgressEvent{}
	}
	if e := outcome(waiting); e.Outcome != "pending" {
		t.Fatalf("a change still awaiting confirmation was settled: %+v", e)
	}
	// The spec decides [1]: the older switch to [2] was not kept.
	if e := outcome(older); e.Outcome != "recovered" {
		t.Fatalf("older switch = %+v", e)
	}
	journal.Phase = "recovered"
	if err := journal.save(); err != nil {
		t.Fatal(err)
	}
	s.egress.settlePending(ctx)
	if e := outcome(waiting); e.Outcome != "recovered" || !strings.Contains(e.Reason, "Not confirmed") {
		t.Fatalf("recovered switch = %+v", e)
	}
}
