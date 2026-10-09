package netx

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Readiness, re-checked auto source translation, the target check from this
// server and external evidence: installed is not reachable, and only a
// measurement after the last change says a visitor got through.

func TestReadinessSeparatesInstalledPolicyForwardingAndAdmission(t *testing.T) {
	h := newGwHost(t)
	f := ForwardSpec{ID: 2, Protocol: "both", Ports: "27015-27020", Target: "172.17.0.2", SourceNAT: "never", Enabled: true}
	live := gwLive(t, 5, map[string][2]uint64{"forward:2": {1, 60}})
	live.Rules["forward:2"] = 2
	present := AdmissionState{Needed: true, Present: true, Chains: []AdmissionChainState{
		{Family: "inet", Chain: "FORWARD", Needed: true, Status: "present"},
		{Family: "inet", Chain: "INPUT", Needed: true, Status: "present"},
	}}
	r := readinessOf(true, live, forwardRuleComments(f), "inet", present)
	if r.Policy != "installed" || !r.Ready || r.Reachability != "unverified" || r.Expected != 2 {
		t.Fatalf("installed: %+v", r)
	}
	live.Rules["forward:2"] = 1
	if r := readinessOf(true, live, forwardRuleComments(f), "inet", present); r.Policy != "partial" || r.Ready {
		t.Fatalf("partial: %+v", r)
	}
	live.Rules["forward:2"] = 2
	absent := present
	absent.Chains = append([]AdmissionChainState{}, present.Chains...)
	absent.Chains[1].Status = "absent"
	if r := readinessOf(true, live, forwardRuleComments(f), "inet", absent); r.Admission != "absent" || r.Ready {
		t.Fatalf("admission absent: %+v", r)
	}
	h.writeSys("net/ipv4/ip_forward", "0")
	if r := readinessOf(true, live, forwardRuleComments(f), "inet", present); r.Forwarding || r.Ready || !strings.Contains(r.Reason, "forwarding is off") {
		t.Fatalf("forwarding off: %+v", r)
	}
	if r := readinessOf(true, liveCounters{Rules: map[string]int{}}, forwardRuleComments(f), "inet", present); r.Policy != "not_loaded" {
		t.Fatalf("not loaded: %+v", r)
	}
	if r := readinessOf(false, live, forwardRuleComments(f), "inet", present); r.Policy != "disabled" || r.Expected != 0 {
		t.Fatalf("disabled: %+v", r)
	}
	masq := ForwardSpec{ID: 3, Protocol: "tcp", Ports: "443", Target: "2001:db8::5", SourceNAT: "always"}
	if got := forwardRuleComments(masq); got["forward:3"] != 1 || got["forward-nat:3"] != 1 {
		t.Fatalf("masquerading forward renders two rules: %+v", got)
	}
	mapped := NATSpec{ID: 4, Mode: natOneToOne}
	if got := natRuleComments(mapped); len(got) != 3 || got["nat-in:4"] != 1 {
		t.Fatalf("a mapping renders its inbound half: %+v", got)
	}
}

func TestAutoNATDecisionIsReCheckedAgainstTheHostAsItIsNow(t *testing.T) {
	h := newGwHost(t)
	// Stored when 172.17.0.2 was on docker0: keep the visitor's address.
	f := ForwardSpec{ID: 1, Protocol: "tcp", Ports: "80", Target: "172.17.0.2", SourceNAT: "auto:never", Enabled: true}
	d := autoNATDecisions(context.Background(), []ForwardSpec{f})[1]
	if d.Drift || d.Current == nil || *d.Current || !strings.Contains(d.Reason, "docker0") {
		t.Fatalf("docker0 still routes it: %+v", d)
	}
	// docker0 went away: the target is no longer on a network routed here.
	h.first("ip -j addr show", `[{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.20","prefixlen":24,"scope":"global"}]}]`, nil)
	d = autoNATDecisions(context.Background(), []ForwardSpec{f})[1]
	if !d.Drift || d.Current == nil || !*d.Current || d.Stored {
		t.Fatalf("drift not seen: %+v", d)
	}
	if got := autoNATDecisions(context.Background(), []ForwardSpec{{ID: 2, Target: "10.0.0.5", SourceNAT: "always"}}); len(got) != 0 {
		t.Fatalf("a fixed choice has no decision to re-check: %+v", got)
	}
}

func TestAGatewaySaveReDecidesADriftedAutoForward(t *testing.T) {
	h := newGwHost(t)
	sp := emptySpec()
	sp.NextID = 3
	sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "203.0.113.50", SourceNAT: "auto:never", Enabled: true}}
	h.seed(t, sp)
	v, err := h.Gateway(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d := v.Forwards[0].Decision; d == nil || !d.Drift {
		t.Fatalf("the uplink's own network was stored as routed here: %+v", d)
	}
	if _, err := h.UpdateForward(context.Background(), 1, ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "203.0.113.50", SourceNAT: "auto"}, gwClient, "ops", nil); err != nil {
		t.Fatal(err)
	}
	if got := h.spec(t).Forwards[0].SourceNAT; got != "auto:always" {
		t.Fatalf("re-decided = %s", got)
	}
}

type gwConn struct{ net.Conn }

func (gwConn) Close() error { return nil }

func TestVerifyForwardMeasuresTheTargetFromThisServer(t *testing.T) {
	h := newGwHost(t)
	sp := emptySpec()
	sp.NextID = 4
	sp.Forwards = []ForwardSpec{
		{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.5", TargetPort: "80", SourceNAT: "never", Enabled: true},
		{ID: 2, Name: "game", Protocol: "udp", Ports: "27015", Target: "10.0.0.6", SourceNAT: "never", Enabled: true},
		{ID: 3, Name: "v6", Protocol: "both", Ports: "443", Target: "2001:db8::5", SourceNAT: "never", Enabled: true},
	}
	h.seed(t, sp)
	prev := forwardDial
	t.Cleanup(func() { forwardDial = prev })
	var dialed []string
	results := map[string]error{"10.0.0.5:80": nil, "[2001:db8::5]:443": syscall.ECONNREFUSED}
	forwardDial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		if err := results[address]; err != nil {
			return nil, &net.OpError{Op: "dial", Err: err}
		}
		return gwConn{}, nil
	}
	c, err := h.VerifyForward(context.Background(), 1)
	if err != nil || c.Status != "answering" || c.Target != "10.0.0.5:80" || !c.Current {
		t.Fatalf("answering: %+v %v", c, err)
	}
	if c, _ := h.VerifyForward(context.Background(), 2); c.Status != "not_measurable" || len(dialed) != 1 {
		t.Fatalf("UDP is not dialled: %+v %v", c, dialed)
	}
	if c, _ := h.VerifyForward(context.Background(), 3); c.Status != "refused" {
		t.Fatalf("refused: %+v", c)
	}
	results["10.0.0.5:80"] = context.DeadlineExceeded
	if c, _ := h.VerifyForward(context.Background(), 1); c.Status != "timeout" {
		t.Fatalf("timeout: %+v", c)
	}
	if _, err := h.VerifyForward(context.Background(), 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	// The check is retained for the page and goes stale when the target moves.
	f := sp.Forwards[0]
	if got := h.lastForwardCheck(f); got == nil || !got.Current {
		t.Fatalf("retained: %+v", got)
	}
	f.Target = "10.0.0.9"
	if got := h.lastForwardCheck(f); got == nil || got.Current {
		t.Fatalf("a moved target's check is stale: %+v", got)
	}
}

func TestExternalEvidenceVerifiesOnlyAfterTheLastChange(t *testing.T) {
	changed := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	v := &GatewayView{
		hostAddrs: gwHostAddrs,
		Forwards: []ForwardView{
			{ID: 1, Protocol: "tcp", Ports: "8080", Enabled: true, ChangedAt: &changed, Readiness: &EntryReadiness{Reachability: "unverified"}},
			{ID: 2, Protocol: "udp", Ports: "27015", Enabled: true, Readiness: &EntryReadiness{Reachability: "unverified"}},
			{ID: 3, Protocol: "both", Ports: "9000-9010", Enabled: true, ChangedAt: &changed, Readiness: &EntryReadiness{Reachability: "unverified"}},
		},
	}
	obs := []ExternalObservation{
		{CheckID: "old", Vantage: "A", Address: "203.0.113.20", Port: 8080, TCP: "connected", CompletedAt: changed.Add(-time.Hour)},
		{CheckID: "new", Vantage: "B", Address: "203.0.113.20", Port: 8080, TCP: "connected", CompletedAt: changed.Add(time.Hour)},
		{CheckID: "elsewhere", Vantage: "C", Address: "198.51.100.1", Port: 8080, TCP: "failed", CompletedAt: changed.Add(2 * time.Hour)},
		{CheckID: "udp", Vantage: "D", Address: "203.0.113.20", Port: 27015, TCP: "connected", CompletedAt: changed.Add(time.Hour)},
		{CheckID: "range", Vantage: "E", Address: "203.0.113.20", Port: 9005, TCP: "failed", CompletedAt: changed.Add(-time.Minute)},
	}
	AttachExternalEvidence(v, obs)
	if e := v.Forwards[0].External; e == nil || e.CheckID != "new" || !e.Current || v.Forwards[0].Readiness.Reachability != "verified" {
		t.Fatalf("forward 1: %+v %+v", e, v.Forwards[0].Readiness)
	}
	if v.Forwards[1].External != nil {
		t.Fatal("a UDP forward cannot be verified by a TCP check")
	}
	if e := v.Forwards[2].External; e == nil || e.Current || v.Forwards[2].Readiness.Reachability != "unverified" {
		t.Fatalf("evidence from before the change does not count: %+v", e)
	}
	v.Forwards[2].ChangedAt = nil
	v.Forwards[2].External = nil
	AttachExternalEvidence(v, obs)
	if v.Forwards[2].Readiness.Reachability != "failed" {
		t.Fatalf("a current failure is reported: %+v", v.Forwards[2].Readiness)
	}
}

func TestGatewayViewCarriesReadinessTotalsAndFlows(t *testing.T) {
	h := newGwHost(t)
	sp := emptySpec()
	sp.NextID = 4
	sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.5", TargetPort: "80", SourceNAT: "auto:always", Enabled: true}}
	sp.NAT = []NATSpec{{ID: 3, Name: "lab", Source: "10.9.0.0/24", Interface: "eth0", Enabled: true}}
	h.seed(t, sp)
	v, err := h.Gateway(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v.Counters.Generation != 5 || v.Counters.Gap == "" {
		t.Fatalf("counters = %+v", v.Counters)
	}
	fv := v.Forwards[0]
	if fv.Readiness == nil || fv.Readiness.Expected != 2 || fv.Readiness.Reachability != "unverified" {
		t.Fatalf("readiness = %+v", fv.Readiness)
	}
	if nv := v.NAT[0]; nv.Mode != "masquerade" || nv.Readiness == nil || nv.Readiness.Expected != 2 {
		t.Fatalf("nat = %+v", nv)
	}
	if len(v.Flows) != 2 {
		t.Fatalf("flows = %+v", v.Flows)
	}
}
