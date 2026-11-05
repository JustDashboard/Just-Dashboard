package hublink

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// The hub's machine (protocol section 2.4). The certificate proves only that
// the answering machine holds the hub's MagicDNS name, and on a tailnet that
// name goes to whichever machine registers it next. So the dashboard records
// the hub node's StableNodeID at connect and sends nothing to another machine
// at that name: before any request on a stored link, and before every poll of
// a pending request, it asks tailscaled's WhoIs about the address it is about
// to dial.

// Machine is the hub node as the local API shows it: GET /dashboard/hub/check's
// machine, and admin.request.hubMachine and admin.link.hubMachine.
type Machine = accountlink.PeerNode

// nodeCheckEvery is the longest a passed check is relied on for one address.
const nodeCheckEvery = 5 * time.Minute

// skipLogEvery is how often an unanswered WhoIs is logged.
const skipLogEvery = time.Hour

// errMachineNotOnTailnet is WhoIs saying no machine holds the hub's address:
// nothing is sent, the exchange counts as failed, and the link does not go
// idle, since the machine may be offline or the address may move.
var errMachineNotOnTailnet = errors.New("the hub's machine is not on this tailnet")

// nodeGate remembers, per hub host, the address whose check last passed (or
// was skipped because tailscaled could not be asked) and when.
type nodeGate struct {
	mu      sync.Mutex
	checked map[string]checkedAddr
	// skipLogged is when an unanswered WhoIs was last logged.
	skipLogged time.Time
}

type checkedAddr struct {
	addr netip.Addr
	at   time.Time
}

// fresh reports whether addr is the address last checked for host, less than
// nodeCheckEvery ago.
func (g *nodeGate) fresh(host string, addr netip.Addr, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.checked[host]
	return ok && c.addr == addr && now.Sub(c.at) < nodeCheckEvery && !now.Before(c.at)
}

// due reports whether host has no check younger than nodeCheckEvery.
func (g *nodeGate) due(host string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.checked[host]
	return !ok || now.Sub(c.at) >= nodeCheckEvery || now.Before(c.at)
}

func (g *nodeGate) pass(host string, addr netip.Addr, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.checked == nil {
		g.checked = map[string]checkedAddr{}
	}
	g.checked[host] = checkedAddr{addr, now}
}

func (g *nodeGate) forget() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.checked = nil
}

// logSkip reports whether an unanswered WhoIs should be logged now.
func (g *nodeGate) logSkip(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.skipLogged.IsZero() && now.Sub(g.skipLogged) < skipLogEvery && !now.Before(g.skipLogged) {
		return false
	}
	g.skipLogged = now
	return true
}

// machineOf is what WhoIs says about a peer, as it is pinned and shown.
func machineOf(p tailnetgate.Peer) Machine {
	m := Machine{ID: p.StableID, Name: strings.ToLower(strings.TrimSuffix(p.Node, ".")), Tags: []string{}}
	if len(p.Tags) > 0 {
		m.Tags = slices.Clone(p.Tags)
	} else {
		m.Owner = p.Login
	}
	return m
}

// DescribeMachine says whose machine it is, the way the Settings page and the
// terminal say it: "hub.tailed39ba.ts.net, a machine of wayy01@github", or
// "hub.tailed39ba.ts.net, tagged tag:server".
func DescribeMachine(m Machine) string {
	switch {
	case len(m.Tags) > 0:
		return m.Name + ", tagged " + strings.Join(m.Tags, ", ")
	case m.Owner != "":
		return m.Name + ", a machine of " + m.Owner
	}
	return m.Name
}

// pin is what a check compares against: the stored link's hub node, or the
// pending request's. Neither is set before anything is stored (Connect's own
// request), and then the check is step 8's: the machine must be the host.
type pin struct {
	link *accountlink.State
	req  *accountlink.Request
}

// pinFor finds what an address dialled for host must belong to now.
func (s *Service) pinFor(ctx context.Context, host string) (pin, error) {
	if st, linked, err := s.engine.Linked(ctx); err != nil {
		return pin{}, err
	} else if linked && hostOf(st.URL) == host {
		return pin{link: &st}, nil
	}
	if req, pending, err := s.engine.Pending(ctx); err != nil {
		return pin{}, err
	} else if pending && hostOf(req.URL) == host {
		return pin{req: &req}, nil
	}
	return pin{}, nil
}

// checkNode is the check itself, for the address addr about to be dialled for
// host, against p. It is what every caller below runs, so the three moments
// the protocol names (the connector starting, the address changing, five
// minutes passing) and the request polls all decide the same way.
func (s *Service) checkNode(ctx context.Context, host string, addr netip.Addr, p pin) error {
	if p.link != nil && p.link.Idle != nil {
		return refused(errors.New(p.link.Idle.Reason))
	}
	if s.whois == nil {
		return nil
	}
	peer, err := s.whois.WhoIs(ctx, addr)
	switch {
	case errors.Is(err, tailnetgate.ErrNotAPeer):
		return fmt.Errorf("%w: no machine on it holds %s, the address of %s", errMachineNotOnTailnet, addr, host)
	case err == nil && strings.TrimSpace(peer.StableID) == "":
		err = fmt.Errorf("%w: its answer names no StableID", tailnetgate.ErrUnavailable)
		fallthrough
	case err != nil:
		// tailscaled cannot be asked: the request is sent, as protocol 2.4
		// says; the certificate still binds the name.
		if s.gate.logSkip(s.now()) {
			s.log.Warn("the hub's machine was not checked; tailscaled could not be asked", "hub", host, "err", err)
		}
		s.gate.pass(host, addr, s.now())
		return nil
	}
	got := machineOf(peer)
	var want *Machine
	switch {
	case p.link != nil:
		want = p.link.HubNode
	case p.req != nil:
		want = p.req.HubNode
	default:
		// Nothing pinned: Connect's own request, which may carry the
		// one-time token. Step 8 of the rule holds whenever tailscaled
		// answers, here as in confirmNode: the machine must be the host.
		if got.Name != host {
			return urlError(CodeNotOnTailnet, "%s is held by %s on this tailnet, not by %s", addr, clip(got.Name, 100), host)
		}
		s.gate.pass(host, addr, s.now())
		return nil
	}
	if want == nil {
		// Nothing pinned yet: tailscaled could not be asked at connect. The
		// first answer that names the stored host is recorded, after the
		// same name check as step 8 of the rule.
		if got.Name != host {
			return fmt.Errorf("%s, the address of %s, is held by %s on this tailnet", addr, host, clip(got.Name, 100))
		}
		if err := s.recordNode(ctx, p, got); err != nil {
			return err
		}
		s.gate.pass(host, addr, s.now())
		return nil
	}
	if got.ID == want.ID {
		s.gate.pass(host, addr, s.now())
		return nil
	}
	reason := fmt.Sprintf("the hub's machine changed: %s is now held by %s (%s), not by %s (%s)",
		host, clip(got.Name, 100), clip(got.ID, 64), want.Name, want.ID)
	detail := map[string]any{"old": map[string]string{"id": want.ID, "name": want.Name}, "new": map[string]string{"id": got.ID, "name": got.Name}}
	if p.link != nil {
		s.goIdle(ctx, *p.link, reason, "hub.link.node_changed", detail)
		return refused(errors.New(reason))
	}
	detail["requestId"] = p.req.ID
	s.auditSystem(ctx, "hub.link.node_changed", host, detail)
	s.log.Warn("a link request ended: another machine answers at the hub's name", "hub", host, "old", want.ID, "new", got.ID)
	return &accountlink.RequestEndedError{Message: reason}
}

// recordNode pins the first node a later check finds (2.4, "Recorded later").
func (s *Service) recordNode(ctx context.Context, p pin, m Machine) error {
	if p.req != nil {
		_, _, err := s.engine.RecordRequestNode(ctx, p.req.ID, m)
		return err
	}
	set, err := s.engine.RecordNode(ctx, p.link.ServerID, m)
	if err != nil || !set {
		return err
	}
	s.auditSystem(ctx, "hub.link.node_recorded", p.link.Owner, map[string]any{
		"serverId": p.link.ServerID, "new": map[string]string{"id": m.ID, "name": m.Name},
	})
	return nil
}

// admitNode is the check before a request on a stored link: at the
// connector's start (the gate is empty then) and whenever the last check is
// five minutes old. The dialer runs it too whenever the address it dials is
// not the one last checked.
func (s *Service) admitNode(ctx context.Context, st accountlink.State) error {
	if s.testOrigin != "" && st.URL == s.testOrigin {
		return nil
	}
	host := hostOf(st.URL)
	if !s.gate.due(host, s.now()) {
		return nil
	}
	addr, err := s.dial.resolve(ctx, host)
	if err != nil {
		return noAddress(err)
	}
	return s.checkNode(ctx, host, addr, pin{link: &st})
}

// noAddress is a check that could not run because the hub's name has no
// tailnet address now. Nothing is sent: the transport could still carry the
// request on a connection it keeps open, without dialling, so without the
// dialer's check. It counts as a failed exchange and does not idle the link.
func noAddress(err error) error {
	return fmt.Errorf("the hub cannot be reached, and its machine was not checked: %w", err)
}

// beforePoll is the engine's hook before every poll of a pending request
// (2.4, "While a request is pending"): always a fresh WhoIs, in whichever
// process polls. A node it records is the request's from then on.
func (s *Service) beforePoll(ctx context.Context, req accountlink.Request) (accountlink.Request, error) {
	if s.testOrigin != "" && req.URL == s.testOrigin {
		return req, nil
	}
	host := hostOf(req.URL)
	addr, err := s.dial.resolve(ctx, host)
	if err != nil {
		// A failed poll: an approval is stored only from a poll whose check
		// passed or found tailscaled unavailable.
		return req, noAddress(err)
	}
	if err := s.checkNode(ctx, host, addr, pin{req: &req}); err != nil {
		return req, err
	}
	if cur, ok, err := s.engine.Pending(ctx); err == nil && ok && cur.ID == req.ID {
		req = cur
	}
	return req, nil
}

// dialGate is the dialer's check: an address that is not the one last checked
// for this host, or was checked more than five minutes ago, is checked before
// anything is sent to it.
func (s *Service) dialGate(ctx context.Context, host string, addr netip.Addr) error {
	if s.gate.fresh(host, addr, s.now()) {
		return nil
	}
	p, err := s.pinFor(ctx, host)
	if err != nil {
		return err
	}
	return s.checkNode(ctx, host, addr, p)
}

// goIdle stops using the stored link st and says so (2.2 "When the rule
// runs", 2.4): idle is stored in the link state, every hub session is revoked
// in the same write transaction, and action is audited by system with detail,
// the reason and how many sessions were revoked. A link already idle is left
// as it is and not audited again.
func (s *Service) goIdle(ctx context.Context, st accountlink.State, reason, action string, detail map[string]any) {
	ctx = context.WithoutCancel(ctx)
	var revoked int64
	set, err := s.engine.SetIdle(ctx, st.ServerID, accountlink.Idle{Code: CodeURLRefused, Reason: reason, At: s.now()},
		func(ctx context.Context, tx *sql.Tx) error {
			n, err := s.revokeHubSessionsTx(ctx, tx)
			revoked = n
			return err
		})
	if err != nil {
		s.log.Warn("could not set the hub link idle", "err", err)
		return
	}
	if !set {
		return
	}
	s.log.Warn("the hub link is idle", "hub", st.URL, "reason", reason)
	if detail == nil {
		detail = map[string]any{}
	}
	detail["serverId"], detail["reason"], detail["sessionsRevoked"] = st.ServerID, reason, revoked
	s.auditSystem(ctx, action, st.Owner, detail)
}

// revokeHubSessionsTx ends every session hub sign-in opened, inside the
// transaction that makes the link unusable. This release has no hub sign-in,
// so there are none; it is the one place that revocation belongs.
func (s *Service) revokeHubSessionsTx(context.Context, *sql.Tx) (int64, error) { return 0, nil }

func (s *Service) auditSystem(ctx context.Context, action, target string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	s.audit.Record(context.WithoutCancel(ctx), audit.Entry{
		Username: "system", Actor: "system", Action: action, Target: target, Method: "LINK", Status: 200, Success: true,
		Detail: audit.Detail(detail),
	})
}

// NodeNote is what the hub.link.begin audit entry says about the hub node: the
// node, or why it was not checked.
func (s *Service) NodeNote(req accountlink.Request) any {
	switch {
	case req.HubNode != nil:
		return req.HubNode
	case s.testOrigin != "" && req.URL == s.testOrigin:
		return "not checked: the test origin"
	}
	return "not checked: tailscaled could not be asked"
}
