package hublink

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// Connecting records the hub's machine (protocol 2.2 step 8), keeps it with the
// request and pins it in the link state when the link completes; the status
// shows it as hubMachine on both.
func TestConnectingPinsTheHubsMachine(t *testing.T) {
	e := newTailnetEnv(t)
	e.whois.set(func(w *fakeWhoIs) { w.id, w.login, w.tags = "nHub1CNTRL", "wayy01@github", []string{} })
	req, err := e.svc.Connect(t.Context(), ConnectRequest{URL: "hub.tailed39ba.ts.net", Scope: accountlink.ScopeObserve, By: "ion"})
	if err != nil {
		t.Fatal(err)
	}
	want := Machine{ID: "nHub1CNTRL", Name: "hub.tailed39ba.ts.net", Owner: "wayy01@github", Tags: []string{}}
	if req.URL != tailnetOrigin || req.HubNode == nil || mustJSON(t, *req.HubNode) != mustJSON(t, want) {
		t.Fatalf("request = %+v %+v", req, req.HubNode)
	}
	if s, _ := e.svc.Status(t.Context(), true); s.Admin.Request.HubMachine == nil || s.Admin.Request.HubMachine.ID != "nHub1CNTRL" {
		t.Fatalf("admin.request = %+v", s.Admin.Request)
	}
	if note := e.svc.NodeNote(req); mustJSON(t, note) != mustJSON(t, want) {
		t.Fatalf("the begin audit's node = %v", note)
	}
	e.hub.approve(req.ID)
	res, err := e.svc.Poll(t.Context())
	if err != nil || res.State != accountlink.PollLinked || res.Link.HubNode == nil || res.Link.HubNode.ID != "nHub1CNTRL" {
		t.Fatalf("poll = %+v %v", res, err)
	}
	s, _ := e.svc.Status(t.Context(), true)
	if s.Admin.Link.HubMachine == nil || mustJSON(t, *s.Admin.Link.HubMachine) != mustJSON(t, want) {
		t.Fatalf("admin.link.hubMachine = %+v", s.Admin.Link.HubMachine)
	}
	if rows := e.auditRows("hub.link"); len(rows) != 1 || !strings.Contains(rows[0].Detail, "nHub1CNTRL") {
		t.Fatalf("the hub.link audit names no node: %+v", rows)
	}
}

// Another StableID at the hub's address: nothing is sent, the link goes idle
// with hub_url_refused, hub.link.node_changed is audited with both nodes, and
// it stays idle across a restart, whatever WhoIs says then (protocol 2.4).
func TestAnotherMachineAtTheHubsNameIdlesTheLink(t *testing.T) {
	e := newTailnetEnv(t)
	e.link(accountlink.ScopeMaintain)
	e.whois.set(func(w *fakeWhoIs) { w.id, w.node = "nImpostor", "hub.tailed39ba.ts.net." })
	e.svc.Start(t.Context())
	// The audit entry is written once the idle flag has committed.
	waitFor(t, "the change to be audited", func() bool { return len(e.auditRows("hub.link.node_changed")) > 0 })
	st, linked, _ := e.svc.Linked(t.Context())
	if !linked || st.Idle.Code != "hub_url_refused" || !strings.Contains(st.Idle.Reason, "the hub's machine changed") {
		t.Fatalf("idle = %+v", st.Idle)
	}
	if n := len(e.hub.heartbeatBodies()); n != 0 {
		t.Fatalf("%d heartbeats reached another machine", n)
	}
	rows := e.auditRows("hub.link.node_changed")
	if len(rows) != 1 || rows[0].Actor != "system" || !strings.Contains(rows[0].Detail, "nImpostor") ||
		!strings.Contains(rows[0].Detail, "nHub1CNTRL") || !strings.Contains(rows[0].Detail, `"sessionsRevoked":0`) {
		t.Fatalf("audit = %+v", rows)
	}
	if err := e.svc.Sync(t.Context()); !errors.Is(err, ErrURLRefused) {
		t.Fatalf("sync on an idle link = %v", err)
	}
	if s, _ := e.svc.Status(t.Context(), true); *s.Reachable || !strings.Contains(s.Admin.Link.LastError, "the hub's machine changed") {
		t.Fatalf("status = %+v / %+v", s, s.Admin.Link)
	}

	// The real hub back at its name does not undo it: a restart finds the
	// link idle in the store and sends nothing until a person disconnects.
	e.whois.set(func(w *fakeWhoIs) { w.id = "nHub1CNTRL" })
	svc := e.restart()
	svc.Start(t.Context())
	time.Sleep(50 * time.Millisecond)
	if n := len(e.hub.heartbeatBodies()); n != 0 {
		t.Fatalf("%d heartbeats after a restart of an idle link", n)
	}
	if len(e.auditRows("hub.link.node_changed")) != 1 {
		t.Fatal("the change was audited again")
	}
	// A disconnect clears it and tells nobody, since the machine at the
	// name is not the one the link was made with.
	if notified, err := svc.Disconnect(t.Context()); err != nil || notified || e.hub.removed() {
		t.Fatalf("disconnect = %v %v", notified, err)
	}
}

// No machine holds the hub's address (ErrNotAPeer): nothing is sent, the
// exchange fails and backs off, and the link does not go idle.
// tailscaled not answering (ErrUnavailable): the request is sent.
func TestNotAPeerBacksOffAndUnavailableSends(t *testing.T) {
	e := newTailnetEnv(t)
	e.link(accountlink.ScopeObserve)
	e.whois.set(func(w *fakeWhoIs) { w.err = tailnetgate.ErrNotAPeer })
	e.svc.Start(t.Context())
	waitFor(t, "the error on the status", func() bool {
		s, _ := e.svc.Status(t.Context(), true)
		return strings.Contains(s.Admin.Link.LastError, "the hub's machine is not on this tailnet")
	})
	if n := len(e.hub.heartbeatBodies()); n != 0 {
		t.Fatalf("%d heartbeats with no machine at the address", n)
	}
	if st, _, _ := e.svc.Linked(t.Context()); st.Idle != nil {
		t.Fatalf("ErrNotAPeer set the link idle: %+v", st.Idle)
	}
	e.whois.set(func(w *fakeWhoIs) { w.err = tailnetgate.ErrUnavailable })
	waitFor(t, "a heartbeat with tailscaled unavailable", func() bool { return len(e.hub.heartbeatBodies()) > 0 })
	if st, _, _ := e.svc.Linked(t.Context()); st.Idle != nil {
		t.Fatalf("ErrUnavailable set the link idle: %+v", st.Idle)
	}
}

// The check runs at the connector's start and then at least every five
// minutes, not before every heartbeat. The poll that collected the approval
// checked the address a moment ago, so the heartbeats after it rely on that.
func TestTheHubsMachineIsCheckedEveryFiveMinutes(t *testing.T) {
	e := newTailnetEnv(t)
	e.link(accountlink.ScopeObserve)
	base := e.whois.asked()
	for i := 0; i < 3; i++ {
		if err := e.svc.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if e.whois.asked() != base {
		t.Fatalf("three heartbeats asked WhoIs %d times, want none", e.whois.asked()-base)
	}
	e.advance(4 * time.Minute)
	_ = e.svc.Sync(t.Context())
	if e.whois.asked() != base {
		t.Fatal("WhoIs was asked again within five minutes")
	}
	e.advance(time.Minute)
	_ = e.svc.Sync(t.Context())
	if e.whois.asked() != base+1 {
		t.Fatalf("WhoIs was asked %d times after five minutes, want once", e.whois.asked()-base)
	}
	// A start of the connector checks again before its first request.
	e.svc.Start(t.Context())
	waitFor(t, "the check at start", func() bool { return e.whois.asked() > base+1 })
}

// A link stored without a hub node (tailscaled could not be asked at connect)
// records the first node a later check returns for the stored host, after the
// node-name check of step 8, and audits hub.link.node_recorded.
func TestANodeNotRecordedAtConnectIsRecordedLater(t *testing.T) {
	e := newTailnetEnv(t)
	e.whois.set(func(w *fakeWhoIs) { w.err = tailnetgate.ErrUnavailable })
	st := e.link(accountlink.ScopeObserve)
	if st.HubNode != nil {
		t.Fatalf("a node was pinned with tailscaled unavailable: %+v", st.HubNode)
	}
	// A machine of another name at the address is not recorded, and nothing
	// is sent to it.
	e.whois.set(func(w *fakeWhoIs) { w.err, w.node, w.id = nil, "elsewhere.tailed39ba.ts.net.", "nElsewhere" })
	e.svc.gate.forget()
	if err := e.svc.Sync(t.Context()); err == nil || !strings.Contains(err.Error(), "elsewhere") {
		t.Fatalf("a machine of another name = %v", err)
	}
	if st, _, _ := e.svc.Linked(t.Context()); st.HubNode != nil || st.Idle != nil {
		t.Fatalf("a machine of another name was pinned or idled the link: %+v %+v", st.HubNode, st.Idle)
	}
	if n := len(e.hub.heartbeatBodies()); n != 0 {
		t.Fatalf("%d heartbeats to a machine of another name", n)
	}
	e.whois.set(func(w *fakeWhoIs) { w.node, w.id = "hub.tailed39ba.ts.net.", "nHub1CNTRL" })
	if err := e.svc.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	cur, _, _ := e.svc.Linked(t.Context())
	if cur.HubNode == nil || cur.HubNode.ID != "nHub1CNTRL" || len(e.hub.heartbeatBodies()) != 1 {
		t.Fatalf("node = %+v, heartbeats = %d", cur.HubNode, len(e.hub.heartbeatBodies()))
	}
	if rows := e.auditRows("hub.link.node_recorded"); len(rows) != 1 || !strings.Contains(rows[0].Detail, "nHub1CNTRL") {
		t.Fatalf("audit = %+v", rows)
	}
	// From then on it is pinned: another machine idles the link.
	e.whois.set(func(w *fakeWhoIs) { w.id = "nImpostor" })
	e.advance(nodeCheckEvery)
	if err := e.svc.Sync(t.Context()); !errors.Is(err, ErrURLRefused) {
		t.Fatalf("sync after the machine changed = %v", err)
	}
}

// While a request is pending the check runs before every poll, in whichever
// process polls. Another StableID before the poll that would get "approved"
// sends nothing, ends the request as failed and stores no hub key, serverId or
// name (protocol 2.4, "While a request is pending").
func TestAnotherMachineWhileARequestIsPendingEndsIt(t *testing.T) {
	for _, by := range []string{"ion", "cli"} {
		t.Run(by, func(t *testing.T) {
			e := newTailnetEnv(t)
			req, err := e.svc.Connect(t.Context(), ConnectRequest{URL: tailnetOrigin, Scope: accountlink.ScopeObserve, By: by})
			if err != nil || req.HubNode == nil {
				t.Fatalf("connect = %+v %v", req, err)
			}
			e.hub.approve(req.ID)
			e.whois.set(func(w *fakeWhoIs) { w.id = "nImpostor" })
			polls := e.hub.pollCount()
			var res accountlink.PollResult
			if by == "cli" {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				res, err = e.svc.Await(ctx)
			} else {
				e.svc.Start(t.Context())
				waitFor(t, "the request to end", func() bool { _, pending, _ := e.svc.Pending(t.Context()); return !pending })
				var es accountlink.Status
				es, err = e.svc.Engine().Status(t.Context())
				if es.LastOutcome != nil {
					res = accountlink.PollResult{State: es.LastOutcome.Result, Message: es.LastOutcome.Message}
				}
			}
			if err != nil || res.State != accountlink.PollFailed || !strings.Contains(res.Message, "the hub's machine changed") {
				t.Fatalf("poll = %+v %v", res, err)
			}
			if e.hub.pollCount() != polls {
				t.Fatal("a poll was sent to another machine")
			}
			if _, linked, _ := e.svc.Linked(t.Context()); linked {
				t.Fatal("a link was stored from another machine")
			}
			if rows := e.auditRows("hub.link.node_changed"); len(rows) != 1 || !strings.Contains(rows[0].Detail, req.ID) {
				t.Fatalf("audit = %+v", rows)
			}
		})
	}
}

// A request kept without a node (tailscaled unavailable at connect) records
// the first node a poll's check returns, and the link pins the request's node
// as it stood at the poll that collected the approval.
func TestARequestWithoutANodeRecordsItAtThePoll(t *testing.T) {
	e := newTailnetEnv(t)
	e.whois.set(func(w *fakeWhoIs) { w.err = tailnetgate.ErrUnavailable })
	req, err := e.svc.Connect(t.Context(), ConnectRequest{URL: tailnetOrigin, Scope: accountlink.ScopeObserve, By: "ion"})
	if err != nil || req.HubNode != nil {
		t.Fatalf("connect = %+v %v", req, err)
	}
	if note := e.svc.NodeNote(req); note != "not checked: tailscaled could not be asked" {
		t.Fatalf("the begin audit's node = %v", note)
	}
	e.whois.set(func(w *fakeWhoIs) { w.err = nil })
	if res, err := e.svc.Poll(t.Context()); err != nil || res.State != accountlink.PollPending {
		t.Fatalf("poll = %+v %v", res, err)
	}
	if cur, _, _ := e.svc.Pending(t.Context()); cur.HubNode == nil || cur.HubNode.ID != "nHub1CNTRL" {
		t.Fatalf("the request's node = %+v", cur.HubNode)
	}
	e.hub.approve(req.ID)
	res, err := e.svc.Poll(t.Context())
	if err != nil || res.State != accountlink.PollLinked || res.Link.HubNode == nil || res.Link.HubNode.ID != "nHub1CNTRL" {
		t.Fatalf("link = %+v %v", res, err)
	}
}

// At the test origin no WhoIs is asked and no hub node is recorded.
func TestTheTestOriginAsksNoWhoIs(t *testing.T) {
	e := newEnv(t)
	st := e.link(accountlink.ScopeObserve)
	if err := e.svc.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if st.HubNode != nil || e.whois.asked() != 0 {
		t.Fatalf("node = %+v, WhoIs asked %d times", st.HubNode, e.whois.asked())
	}
}

// A stored test-origin link with the variable removed goes idle at the next
// start, on any machine, with every hub session revoked and hub.link.idle
// audited; putting the variable back does not revive it (protocol 2.2, 2.3).
func TestATestOriginLinkIdlesOnceTheVariableIsRemoved(t *testing.T) {
	e := newEnv(t)
	e.link(accountlink.ScopeObserve)
	// No tailnet at all: steps 1 to 4 decide alone.
	e.tailnet.err = errors.New("tailscale is not installed")
	svc := e.restart(func(o *Options) { o.TestOrigin = "" })
	svc.Start(t.Context())
	waitFor(t, "the idle link to be audited", func() bool { return len(e.auditRows("hub.link.idle")) > 0 })
	if st, _, _ := svc.Linked(t.Context()); st.Idle == nil {
		t.Fatal("the link is not idle in the store")
	}
	if n := len(e.hub.heartbeatBodies()); n != 0 {
		t.Fatalf("%d heartbeats to a removed test origin", n)
	}
	rows := e.auditRows("hub.link.idle")
	if len(rows) != 1 || !strings.Contains(rows[0].Detail, "not an https address") || !strings.Contains(rows[0].Detail, `"sessionsRevoked":0`) {
		t.Fatalf("audit = %+v", rows)
	}
	svc = e.restart()
	svc.Start(t.Context())
	time.Sleep(50 * time.Millisecond)
	if n := len(e.hub.heartbeatBodies()); n != 0 {
		t.Fatalf("%d heartbeats once the variable was back", n)
	}
	if s, _ := svc.Status(t.Context(), true); !strings.Contains(s.Admin.Link.LastError, "no longer accepts the hub's address") {
		t.Fatalf("status = %+v", s.Admin.Link)
	}
}

// GET /dashboard/hub/check's answer: the stored spelling, the machine when
// WhoIs answers, nothing when it cannot be asked, and the test origin flag.
func TestCheckShowsTheMachineBeforeConnect(t *testing.T) {
	e := newTailnetEnv(t)
	res, err := e.svc.Check(t.Context(), "HUB.tailed39ba.ts.net.:443")
	if err != nil || res.HubURL != tailnetOrigin || res.Machine == nil || res.Machine.ID != "nHub1CNTRL" || res.TestOrigin {
		t.Fatalf("check = %+v %v", res, err)
	}
	if got := DescribeMachine(*res.Machine); got != "hub.tailed39ba.ts.net, tagged tag:hub" {
		t.Fatalf("described as %q", got)
	}
	if got := DescribeMachine(Machine{Name: "hub.tailed39ba.ts.net", Owner: "wayy01@github"}); got != "hub.tailed39ba.ts.net, a machine of wayy01@github" {
		t.Fatalf("described as %q", got)
	}
	e.whois.set(func(w *fakeWhoIs) { w.err = tailnetgate.ErrUnavailable })
	if res, err := e.svc.Check(t.Context(), "hub.tailed39ba.ts.net"); err != nil || res.Machine != nil {
		t.Fatalf("check with tailscaled unavailable = %+v %v", res, err)
	}
	e.whois.set(func(w *fakeWhoIs) { w.err = nil; w.node = "impostor.tailed39ba.ts.net." })
	if _, err := e.svc.Check(t.Context(), "hub.tailed39ba.ts.net"); codeOf(err) != CodeNotOnTailnet {
		t.Fatalf("check of a name another machine answers = %v", err)
	}
	if e.hub.requestCount() != 0 {
		t.Fatal("check sent something to the hub")
	}
	off := newEnv(t, func(o *Options) { o.Enabled = false })
	if _, err := off.svc.Check(t.Context(), "hub.tailed39ba.ts.net"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("check switched off = %v", err)
	}
	test := newEnv(t)
	if res, err := test.svc.Check(t.Context(), test.hub.URL()); err != nil || !res.TestOrigin || res.Machine != nil || res.HubURL != test.hub.URL() {
		t.Fatalf("check of the test origin = %+v %v", res, err)
	}
}

// The dialer checks an address it has not checked, or checked more than five
// minutes ago, before dialling it; a refusal dials nothing.
func TestTheDialerChecksAnAddressItHasNotChecked(t *testing.T) {
	d := newDialer(newFakeTailnet(), "")
	d.lookup = func(context.Context, string) ([]netip.Addr, error) { return nil, errors.New("no such host") }
	var gated []netip.Addr
	refuse := errors.New("not the hub's machine")
	d.gate = func(_ context.Context, host string, addr netip.Addr) error {
		if host != "hub.tailed39ba.ts.net" {
			t.Errorf("gate asked about %q", host)
		}
		gated = append(gated, addr)
		return refuse
	}
	if _, err := d.dial(t.Context(), "tcp", "hub.tailed39ba.ts.net:443"); !errors.Is(err, refuse) {
		t.Fatalf("dial = %v", err)
	}
	if len(gated) != 1 || gated[0] != netip.MustParseAddr("100.101.102.103") {
		t.Fatalf("gated = %v", gated)
	}

	// The Service's gate: a fresh pass for that address lets it through
	// without asking again; another address is asked about.
	e := newTailnetEnv(t)
	e.link(accountlink.ScopeObserve)
	asked := e.whois.asked()
	addr := netip.MustParseAddr("100.101.102.103")
	if err := e.svc.dialGate(t.Context(), "hub.tailed39ba.ts.net", addr); err != nil || e.whois.asked() != asked {
		t.Fatalf("a checked address = %v, asked %d more", err, e.whois.asked()-asked)
	}
	e.whois.set(func(w *fakeWhoIs) { w.id = "nImpostor" })
	if err := e.svc.dialGate(t.Context(), "hub.tailed39ba.ts.net", netip.MustParseAddr("100.101.102.104")); !errors.Is(err, ErrURLRefused) {
		t.Fatalf("a new address held by another machine = %v", err)
	}
	if st, _, _ := e.svc.Linked(t.Context()); st.Idle == nil {
		t.Fatal("the dialer's refusal did not idle the link")
	}
}
