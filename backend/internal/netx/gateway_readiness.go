package netx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"syscall"
	"time"
)

// Readiness separates what is installed from what is measured.
//
// An entry is ready when every rule it renders is in the loaded table, the
// kernel forwards its family and the owned admission rules of that family are
// present. None of that proves a visitor reaches the target: reachability is
// only "verified" from a measurement of the public port by an external source
// after the entry's last change, and "unverified" otherwise.

// EntryReadiness is one forward's or NAT entry's installed state.
type EntryReadiness struct {
	// Policy is installed, partial, missing, drift (more rules than
	// rendered), not_loaded or disabled.
	Policy   string `json:"policy"`
	Rules    int    `json:"rules"`
	Expected int    `json:"expected"`
	// Forwarding is the kernel's switch for the entry's family.
	Forwarding bool `json:"forwarding"`
	// Admission is present, absent, unreadable, unsupported or not_required.
	Admission string `json:"admission"`
	// Ready is installed policy, forwarding and admission together.
	Ready bool `json:"ready"`
	// Reachability is verified, failed or unverified; only an external
	// measurement after the last change moves it off unverified.
	Reachability string `json:"reachability"`
	Reason       string `json:"reason"`
}

func readinessOf(enabled bool, live liveCounters, comments map[string]int, family string, admission AdmissionState) EntryReadiness {
	r := EntryReadiness{Reachability: "unverified", Admission: "not_required"}
	for comment, expected := range comments {
		r.Expected += expected
		r.Rules += live.Rules[comment]
	}
	r.Forwarding = gatewayForwardingOn(familyDigitOf(family))
	switch {
	case !enabled:
		r.Policy, r.Reason = "disabled", "Switched off; nothing is installed for it."
		r.Rules, r.Expected = 0, 0
		return r
	case !live.Loaded:
		r.Policy = "not_loaded"
	case r.Rules == r.Expected:
		r.Policy = "installed"
	case r.Rules == 0:
		r.Policy = "missing"
	case r.Rules > r.Expected:
		r.Policy = "drift"
	default:
		r.Policy = "partial"
	}
	r.Admission = familyAdmission(admission, family)
	r.Ready = r.Policy == "installed" && r.Forwarding && (r.Admission == "present" || r.Admission == "not_required")
	switch {
	case r.Policy == "not_loaded":
		r.Reason = "The gateway table is not loaded, so nothing of it is in force."
	case r.Policy != "installed":
		r.Reason = fmt.Sprintf("%d of the %d rules it renders are in the loaded table.", r.Rules, r.Expected)
	case !r.Forwarding:
		r.Reason = fmt.Sprintf("IPv%s forwarding is off, so nothing passes through this server.", familyDigitOf(family))
	case !r.Ready:
		r.Reason = "The owned admission rules of its family are not all present; the firewall may refuse it after translation."
	default:
		r.Reason = "Installed and admitted. A visitor reaching the target is still unmeasured."
	}
	return r
}

func familyDigitOf(family string) string {
	if family == "inet6" {
		return "6"
	}
	return "4"
}

// familyAdmission sums the needed chains of one family.
func familyAdmission(a AdmissionState, family string) string {
	status, seen := "present", false
	for _, ch := range a.Chains {
		if ch.Family != family || !ch.Needed {
			continue
		}
		seen = true
		switch ch.Status {
		case "present":
		case "unreadable":
			status = "unreadable"
		case "unsupported":
			if status == "present" {
				status = "unsupported"
			}
		default:
			if status != "unreadable" {
				status = "absent"
			}
		}
	}
	if !seen {
		return "not_required"
	}
	return status
}

// forwardRuleComments are the rule comments a forward renders, with counts.
func forwardRuleComments(f ForwardSpec) map[string]int {
	n := len(gwProtocols(f.Protocol))
	out := map[string]int{"forward:" + strconv.Itoa(f.ID): n}
	if _, masq, _ := splitNAT(f.SourceNAT); masq {
		out["forward-nat:"+strconv.Itoa(f.ID)] = n
	}
	return out
}

func natRuleComments(n NATSpec) map[string]int {
	id := strconv.Itoa(n.ID)
	out := map[string]int{"nat:" + id: 1, "nat-mark:" + id: 1}
	if natMapped(n) {
		out["nat-in:"+id] = 1
	}
	return out
}

// NATDecision is what auto source translation decided for a forward, what
// the host's networks say now, and why. Drift means the topology changed
// after the decision: saving the gateway again re-decides it.
type NATDecision struct {
	Stored  bool   `json:"stored"`
	Current *bool  `json:"current"`
	Drift   bool   `json:"drift"`
	Reason  string `json:"reason"`
	Error   string `json:"error,omitempty"`
}

// autoNATDecisions re-evaluates every auto forward against the host as it is
// now, without changing anything.
func autoNATDecisions(ctx context.Context, forwards []ForwardSpec) map[int]NATDecision {
	out := map[int]NATDecision{}
	auto := false
	for _, f := range forwards {
		if c, _, _ := splitNAT(f.SourceNAT); c == natAuto {
			auto = true
		}
	}
	if !auto {
		return out
	}
	raw, err := run(ctx, "ip", "-j", "addr", "show")
	var addrs []ipAddr
	if err == nil {
		err = json.Unmarshal([]byte(raw), &addrs)
	}
	uplinks := map[string]bool{}
	if err == nil {
		uplinks = readUplinks(ctx)
	}
	for _, f := range forwards {
		choice, masq, _ := splitNAT(f.SourceNAT)
		if choice != natAuto {
			continue
		}
		d := NATDecision{Stored: masq}
		target, perr := ParseAddr(f.Target)
		switch {
		case err != nil:
			d.Error = "The host's addresses could not be read, so the decision was not re-checked."
		case perr != nil:
			d.Error = perr.Error()
		default:
			here, reason := autoNATReason(addrs, uplinks, target)
			current := !here
			d.Current, d.Reason, d.Drift = &current, reason, current != masq
		}
		out[f.ID] = d
	}
	return out
}

// ForwardCheck is the last measurement of a forward's target from this
// server: whether something answers there, not whether a visitor can reach it.
type ForwardCheck struct {
	Status    string    `json:"status"`
	Detail    string    `json:"detail"`
	Target    string    `json:"target"`
	Protocol  string    `json:"protocol"`
	CheckedAt time.Time `json:"checkedAt"`
	// Basis says what the measurement covers.
	Basis string `json:"basis"`
	// Current is false once the forward was changed after the check.
	Current bool `json:"current"`
	config  string
}

const forwardCheckBasis = "A TCP connection from this server to the target. It does not pass through the forward's translation or the firewall's forward path, and says nothing about a visitor's route."

// forwardDial connects to a target; a variable for tests.
var forwardDial = func(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, address)
}

// forwardConfig fingerprints the fields a target check depends on.
func forwardConfig(f ForwardSpec) string {
	h := sha256.Sum256([]byte(f.Target + "|" + targetPort(f) + "|" + f.Protocol))
	return hex.EncodeToString(h[:8])
}

func targetPort(f ForwardSpec) string {
	port := f.TargetPort
	if port == "" {
		port = f.Ports
	}
	lo, _ := portBounds(port)
	return strconv.Itoa(lo)
}

// VerifyForward measures whether the target answers from this server. UDP
// has no handshake to observe, so a UDP-only forward is reported as not
// measurable rather than guessed.
func (s *Service) VerifyForward(ctx context.Context, id int) (ForwardCheck, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return ForwardCheck{}, err
	}
	var f *ForwardSpec
	for i := range sp.Forwards {
		if sp.Forwards[i].ID == id {
			f = &sp.Forwards[i]
		}
	}
	if f == nil {
		return ForwardCheck{}, fmt.Errorf("forward %d: %w", id, ErrNotFound)
	}
	target, err := ParseAddr(f.Target)
	if err != nil {
		return ForwardCheck{}, err
	}
	address := net.JoinHostPort(target.String(), targetPort(*f))
	c := ForwardCheck{Target: address, Protocol: "tcp", CheckedAt: gatewayNow().UTC(), Basis: forwardCheckBasis, Current: true, config: forwardConfig(*f)}
	if f.Protocol == "udp" {
		c.Protocol, c.Status = "udp", "not_measurable"
		c.Detail = "UDP has no handshake, so an answer cannot be told apart from silence without the service's own protocol."
	} else {
		dialCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		conn, err := forwardDial(dialCtx, "tcp", address)
		cancel()
		switch {
		case err == nil:
			_ = conn.Close()
			c.Status, c.Detail = "answering", "Something accepted a TCP connection at the target."
		case errors.Is(err, syscall.ECONNREFUSED):
			c.Status, c.Detail = "refused", "The target refused the connection: nothing listens on that port, or its firewall rejects it."
		case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
			c.Status, c.Detail = "unreachable", "This server has no route to the target."
		case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
			c.Status, c.Detail = "timeout", "Nothing answered within four seconds; a firewall may be dropping the connection, or the target is down."
		default:
			c.Status, c.Detail = "error", err.Error()
		}
	}
	s.checksMu.Lock()
	if s.forwardChecks == nil {
		s.forwardChecks = map[int]ForwardCheck{}
	}
	s.forwardChecks[id] = c
	s.checksMu.Unlock()
	return c, nil
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// lastForwardCheck is the retained check of a forward, marked stale when
// the forward's target changed since.
func (s *Service) lastForwardCheck(f ForwardSpec) *ForwardCheck {
	s.checksMu.Lock()
	c, ok := s.forwardChecks[f.ID]
	s.checksMu.Unlock()
	if !ok {
		return nil
	}
	c.Current = c.config == forwardConfig(f) && (f.ChangedAt.IsZero() || !c.CheckedAt.Before(f.ChangedAt))
	return &c
}

// ExternalObservation is one retained measurement by an enrolled external
// source, as the API layer hands it over from the external checks module.
type ExternalObservation struct {
	CheckID   string
	Vantage   string
	Location  string
	Placement string
	Address   string
	Port      int
	Family    string
	// TCP is the TCP stage's state: connected or failed; empty when the
	// check never reached it.
	TCP         string
	Detail      string
	CompletedAt time.Time
}

// ExternalEvidence is the external measurement that speaks for a forward's
// public port.
type ExternalEvidence struct {
	Status    string    `json:"status"`
	CheckID   string    `json:"checkId"`
	Vantage   string    `json:"vantage"`
	Location  string    `json:"location"`
	Placement string    `json:"placement"`
	Address   string    `json:"address"`
	Port      int       `json:"port"`
	Family    string    `json:"family"`
	CheckedAt time.Time `json:"checkedAt"`
	Detail    string    `json:"detail"`
	// Current is a measurement taken after the forward's last change.
	Current bool   `json:"current"`
	Basis   string `json:"basis"`
}

const externalBasis = "A TCP connection from an enrolled source to this server's address and the forward's public port, as that source reported it. It covers TCP only, and a local service answering on the same port would look the same."

// AttachExternalEvidence picks, for each TCP forward, the newest external
// measurement of one of this host's addresses on a port the forward
// publishes, and moves its reachability off unverified only when the
// measurement is newer than the forward's last change.
func AttachExternalEvidence(v *GatewayView, observations []ExternalObservation) {
	host := map[netip.Addr]bool{}
	for _, a := range v.hostAddrs {
		host[a.Addr] = true
	}
	for i := range v.Forwards {
		f := &v.Forwards[i]
		if f.Protocol == "udp" {
			continue
		}
		lo, hi := portBounds(f.Ports)
		var best *ExternalObservation
		for j := range observations {
			o := &observations[j]
			addr, err := ParseAddr(o.Address)
			if err != nil || !host[addr] || o.Port < lo || o.Port > hi || o.TCP == "" {
				continue
			}
			if best == nil || o.CompletedAt.After(best.CompletedAt) {
				best = o
			}
		}
		if best == nil {
			continue
		}
		e := &ExternalEvidence{
			Status: best.TCP, CheckID: best.CheckID, Vantage: best.Vantage, Location: best.Location, Placement: best.Placement,
			Address: best.Address, Port: best.Port, Family: best.Family, CheckedAt: best.CompletedAt, Detail: best.Detail, Basis: externalBasis,
		}
		e.Current = f.ChangedAt == nil || !best.CompletedAt.Before(*f.ChangedAt)
		f.External = e
		if f.Readiness == nil || !e.Current || !f.Enabled {
			continue
		}
		switch e.Status {
		case "connected":
			f.Readiness.Reachability = "verified"
		case "failed":
			f.Readiness.Reachability = "failed"
		}
	}
}
