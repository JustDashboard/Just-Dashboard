// Package netsec exposes the host's firewall, intrusion prevention and active
// login sessions.
//
// Editing firewall rules from a web UI carries an obvious hazard: a bad rule
// can lock the operator out of the very machine they are administering — and
// out of this dashboard with it. Rules that would drop the caller's own
// connection are therefore refused rather than applied, and that guard lives
// here rather than in any one backend so it cannot be forgotten by the next
// one somebody adds.
package netsec

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var (
	ErrNoFirewall = errors.New("no supported firewall was found on this host")
	ErrLockout    = errors.New("this rule would cut off your own connection to the dashboard")
	// ErrReadOnly is returned when the host's firewall can be read but not
	// safely written from here. Reporting it as a distinct condition is the
	// point: "this dashboard will not edit iptables directly" is information,
	// and a greyed-out button with no reason is not.
	ErrReadOnly = errors.New("this firewall backend is read-only from the dashboard")
)

type Backend string

const (
	BackendUFW       Backend = "ufw"
	BackendFirewalld Backend = "firewalld"
	BackendIPTables  Backend = "iptables"
)

// FirewallCapabilities says what this host's firewall can actually be told to
// do from here.
//
// Every backend answers the same questions and they answer them differently:
// ufw has an on/off switch and firewalld has a service, firewalld has named
// zones and ufw has none, and raw iptables has no persistence story at all.
// Rather than pretend one shape fits, the status carries what is possible and
// the UI hides the rest — with a reason, so a missing control is explained
// rather than merely absent.
type FirewallCapabilities struct {
	// Editable covers adding and deleting rules.
	Editable bool `json:"editable"`
	// Toggle is turning the whole firewall on and off.
	Toggle bool `json:"toggle"`
	// DefaultPolicy is changing what happens to unmatched traffic.
	DefaultPolicy bool `json:"defaultPolicy"`
	Logging       bool `json:"logging"`
	Reset         bool `json:"reset"`
	// Profiles reports whether the host defines named service bundles.
	Profiles bool `json:"profiles"`
	// ReadOnlyReason explains a backend that can only be read.
	ReadOnlyReason string `json:"readOnlyReason,omitempty"`
}

type FirewallStatus struct {
	Backend   Backend `json:"backend"`
	Available bool    `json:"available"`
	Enabled   bool    `json:"enabled"`
	// Default is the policy line verbatim, kept because it is what the tool
	// itself prints and an operator may want to read it unmediated.
	Default string `json:"defaultPolicy,omitempty"`
	// Policy is the same thing split into the three directions, so the UI can
	// show "inbound: deny" as a control rather than as prose to be re-read.
	Policy DefaultPolicy `json:"policy"`
	// Logging is the tool's own level ("on (low)", "off"). A firewall that
	// drops silently leaves nothing to look at after an incident, which is
	// why it is worth a line of its own rather than being buried in the raw
	// output.
	Logging string `json:"logging,omitempty"`
	// Zone is firewalld's active zone. Empty for backends with no such idea.
	Zone         string               `json:"zone,omitempty"`
	Capabilities FirewallCapabilities `json:"capabilities"`
	Rules        []Rule               `json:"rules"`
	// RulesFrom is "configured" when the rules were read from the tool's
	// configuration because it is not enforcing them (an inactive ufw).
	RulesFrom string `json:"rulesFrom,omitempty"`
	Raw       string `json:"raw,omitempty"`
	Error     string `json:"error,omitempty"`
	// Detection is every firewall this host could be run by and whether it
	// is, which is how Backend was chosen.
	Detection []BackendDetection `json:"detection"`
	// Zones are firewalld's active zones with what is bound to them.
	Zones []FirewallZone `json:"zones,omitempty"`
	// Effective is the policy per family and per interface as enforced.
	Effective EffectivePolicy `json:"effective"`
	// Findings are rules the evaluation order makes unreachable or redundant.
	Findings []RuleFinding `json:"findings"`
	// Analysis says whether Findings could be computed for this backend.
	Analysis string `json:"analysis,omitempty"`
	// Foreign names the other nftables tables beside the owned one, which
	// keep enforcing whatever this page says.
	Foreign []string `json:"foreign,omitempty"`
}

// DefaultPolicy is the three default verdicts. Routed is "disabled" on a host
// that is not forwarding, which is not the same as "deny" and is worth
// reporting as itself.
type DefaultPolicy struct {
	Incoming string `json:"incoming,omitempty"`
	Outgoing string `json:"outgoing,omitempty"`
	Routed   string `json:"routed,omitempty"`
}

type Rule struct {
	Number    int    `json:"number,omitempty"`
	Action    string `json:"action"`
	Protocol  string `json:"protocol,omitempty"`
	From      string `json:"from"`
	To        string `json:"to"`
	Port      string `json:"port,omitempty"`
	Direction string `json:"direction,omitempty"`
	Comment   string `json:"comment,omitempty"`
	// IPv6 marks a backend's duplicate of a rule for the v6 table. ufw prints
	// both and distinguishes them only by a "(v6)" suffix, so a rule list that
	// does not carry the flag reads as every rule having been added twice.
	IPv6 bool `json:"ipv6,omitempty"`
	// Service names the port from the catalogue, so a list of numbers reads
	// as a list of things.
	Service string `json:"service,omitempty"`
	// Danger is the catalogue's warning for a port that is open to everyone
	// and should not be. Attached to the rule rather than computed in the UI
	// so that "which of my rules are the dangerous ones" has one answer.
	Danger string `json:"danger,omitempty"`
	// ID is the rule's stable identity: a digest of what it says, not of
	// where it sits, so a renumbered list still names the same rule and a
	// stale identity refuses an edit instead of changing a neighbour.
	ID string `json:"id"`
	// Interface is the device a rule is scoped to ("on eth0").
	Interface string `json:"interface,omitempty"`
	// Zone is the firewalld zone holding the rule.
	Zone string `json:"zone,omitempty"`
	// BothFamilies is a rule that applies to IPv4 and IPv6 at once, which
	// ufw prints twice and firewalld and nftables print once.
	BothFamilies bool `json:"-"`
	// Handle is how the owning backend identifies this rule when removing it.
	// ufw deletes by number; firewalld has no numbers at all and needs the
	// exact thing back. Never shown, never accepted from a client — the
	// delete route takes the listed number and the backend resolves it.
	Handle string `json:"-"`
	Raw    string `json:"raw"`
}

// fwBackend is one firewall this dashboard knows how to drive.
//
// Validation and the lockout guards deliberately do *not* live here: they are
// applied by Service before dispatching, so a new backend cannot be added
// without them.
type fwBackend interface {
	Kind() Backend
	Detect() bool
	Status(ctx context.Context) (*FirewallStatus, error)
	AddRule(ctx context.Context, req RuleRequest) (string, error)
	DeleteRule(ctx context.Context, number int) (string, error)
	SetEnabled(ctx context.Context, enabled bool) (string, error)
	SetDefaultPolicy(ctx context.Context, direction, policy string) (string, error)
	SetLogging(ctx context.Context, level string) (string, error)
	Reset(ctx context.Context) (string, error)
	Profiles(ctx context.Context) ([]AppProfile, error)
	Capabilities() FirewallCapabilities
}

type Service struct {
	// owned is the dashboard's own nftables table, offered where no ufw or
	// firewalld runs (firewall_nft.go).
	owned OwnedFirewall
	// history keeps the dashboard's own rule changes (firewall_history.go).
	history *sql.DB

	profilesMu sync.Mutex
	profiles   map[string][]string
	profilesAt time.Time
	// conns remembers the connection table between reads (connections.go),
	// which is how a tuple gets an age and a close is noticed at all.
	conns connTracker
}

func New() *Service { return &Service{} }

// backends are the tool firewalls in preference order. ufw first because a
// host with both installed is almost always a Debian machine where ufw is the
// one in charge; iptables last because it is present everywhere and would
// otherwise mask the others. Which one runs the host is decided by activity
// as well as presence (firewall_detect.go).
func backends() []fwBackend {
	return []fwBackend{ufwBackend{}, firewalldBackend{}, iptablesBackend{}}
}

// backend is the firewall in charge of this host now.
func (s *Service) backend(ctx context.Context) fwBackend {
	b, _ := s.selectBackend(ctx)
	return b
}

func (s *Service) Backend() Backend {
	if b := s.backend(context.Background()); b != nil {
		return b.Kind()
	}
	return ""
}

func (s *Service) Status(ctx context.Context) (*FirewallStatus, error) {
	b, detection := s.selectBackend(ctx)
	if b == nil {
		return &FirewallStatus{Rules: []Rule{}, Detection: detection, Findings: []RuleFinding{}, Error: ErrNoFirewall.Error()}, nil
	}
	st, err := s.statusOf(ctx, b)
	if err != nil {
		return &FirewallStatus{Backend: b.Kind(), Rules: []Rule{}, Detection: detection, Findings: []RuleFinding{}, Error: err.Error()}, nil
	}
	st.Detection = detection
	st.Capabilities = capabilitiesFor(b, detection)
	return st, nil
}

// statusOf reads one backend and completes what every reader relies on:
// positional numbers, annotations, stable identities, findings and the
// effective policy.
func (s *Service) statusOf(ctx context.Context, b fwBackend) (*FirewallStatus, error) {
	st, err := b.Status(ctx)
	if err != nil {
		return nil, err
	}
	st.Capabilities = b.Capabilities()
	// Numbers are positional and assigned here rather than by each backend, so
	// "delete rule 4" means the same thing whichever tool is underneath. ufw
	// supplies its own and they are left alone; the others have no notion of
	// one at all.
	for i := range st.Rules {
		// Configured rules of an inactive ufw stay unnumbered: ufw numbers
		// them only once its IPv6 twins are loaded.
		if st.Rules[i].Number == 0 && st.RulesFrom != "configured" {
			st.Rules[i].Number = i + 1
		}
		annotateRule(&st.Rules[i])
	}
	assignRuleIDs(b.Kind(), st.Rules)
	for i := range st.Zones {
		if st.Zones[i].Default {
			st.Zones[i].Rules = st.Rules
			continue
		}
		assignRuleIDs(b.Kind(), st.Zones[i].Rules)
	}
	st.Findings, st.Analysis = analyzeRules(b.Kind(), st.Rules)
	if st.RulesFrom == "configured" {
		st.Findings, st.Analysis = []RuleFinding{}, "ufw is inactive; ordering findings are computed once it is enforcing its rules."
	}
	st.Effective = effectivePolicy(ctx, b, st)
	if st.Detection == nil {
		st.Detection = []BackendDetection{}
	}
	return st, nil
}

// annotateRule attaches the catalogue's name and warning. Done centrally so
// every backend's rules are read the same way.
func annotateRule(r *Rule) {
	if r.Service != "" && r.Danger != "" {
		return
	}
	preset, ok := presetForRulePort(r.Port, r.Protocol)
	if !ok {
		return
	}
	if r.Service == "" {
		r.Service = preset.Name
	}
	// Only a rule that admits everyone earns the warning. The same port
	// restricted to a private source is the arrangement being recommended,
	// and flagging it would train the operator to ignore the flag.
	if preset.Danger != "" && r.Action == "ALLOW" && isAnywhere(r.From) {
		r.Danger = preset.Danger
	}
}

// presetForRulePort resolves the catalogue for a rule's port, which unlike a
// listening socket's may be a list or a range.
//
// Both forms are ones this dashboard's own rule form writes — `80,443` and
// `8000:8010` — and an exact-string lookup matched neither, so a rule opening
// Redis as part of a list carried no name and, more to the point, no warning.
// The first catalogued port found wins: the warning is about the rule, and one
// dangerous port in a list is enough to earn it.
func presetForRulePort(port, protocol string) (ServicePreset, bool) {
	if port == "" {
		return ServicePreset{}, false
	}
	if preset, ok := PresetFor(port, protocol); ok {
		return preset, true
	}
	for _, part := range strings.Split(port, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, ":")
		if !isRange {
			if preset, ok := PresetFor(part, protocol); ok {
				return preset, true
			}
			continue
		}
		// A range is walked through the catalogue rather than through the
		// range, which may be sixty thousand ports wide.
		from, err1 := strconv.Atoi(strings.TrimSpace(lo))
		to, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil || from > to {
			continue
		}
		for _, candidate := range ServiceCatalogue {
			n, err := strconv.Atoi(candidate.Port)
			if err != nil || n < from || n > to {
				continue
			}
			if protocol == "" || protocol == candidate.Protocol {
				return candidate, true
			}
		}
	}
	return ServicePreset{}, false
}

// isAnywhere reports a source that restricts nothing.
func isAnywhere(from string) bool {
	switch strings.ToLower(strings.TrimSpace(from)) {
	case "", "anywhere", "anywhere (v6)", "0.0.0.0/0", "::/0", "any":
		return true
	}
	return false
}

type RuleRequest struct {
	Action    string `json:"action"`
	Direction string `json:"direction"`
	Port      string `json:"port"`
	Protocol  string `json:"protocol"`
	From      string `json:"from"`
	To        string `json:"to"`
	Comment   string `json:"comment"`
	// Position inserts the rule at a given number instead of appending. ufw
	// evaluates in order and stops at the first match, so a deny added after
	// a broad allow does nothing at all — which looks, from the rule list,
	// exactly like a deny that is working.
	Position int `json:"position,omitempty"`
	// App names an application profile ("Nginx Full", "postgresql") instead
	// of a port. The host's own packages define these, and a rule written in
	// their terms keeps meaning what it says when a package adds a port.
	App string `json:"app,omitempty"`
}

var (
	// A single port, a range, or a comma-separated list — the last only
	// together with a protocol, which is enforced below rather than in the
	// pattern so the error can say why.
	portRe    = regexp.MustCompile(`^\d{1,5}(:\d{1,5})?(,\d{1,5}(:\d{1,5})?)*$`)
	commentRe = regexp.MustCompile(`^[A-Za-z0-9 ._:\-]{0,64}$`)
	// A profile name is free text in a package's own file, so it may contain
	// spaces — but not the characters that would make it something other than
	// a name.
	appNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._/+-]{0,63}$`)
)

// AddRule validates, guards, and hands the request to whichever firewall this
// host runs. Every component is checked against a strict pattern and passed as
// a separate argument, so nothing the operator types can become part of a
// different command.
func (s *Service) AddRule(ctx context.Context, req RuleRequest, callerIP string) (string, error) {
	b, err := s.writable(ctx, func(c FirewallCapabilities) bool { return c.Editable })
	if err != nil {
		return "", err
	}
	clean, err := normaliseRule(req)
	if err != nil {
		return "", err
	}
	if err := guardLockout(clean.Action, clean.Direction, clean.From, callerIP); err != nil {
		return "", err
	}
	if err := s.guardChange(ctx, b, func(st *FirewallStatus) error { return simulateAdd(st, clean) }); err != nil {
		return "", err
	}
	if checking(ctx) {
		return "", ErrChecked
	}
	return b.AddRule(ctx, clean)
}

// normaliseRule validates a request and returns it in canonical form.
//
// Separate from AddRule so the rules are one thing to read and one thing to
// test, and so every backend receives the same already-checked shape.
func normaliseRule(req RuleRequest) (RuleRequest, error) {
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	switch req.Action {
	case "allow", "deny", "reject", "limit":
	default:
		return req, fmt.Errorf("action must be allow, deny, reject or limit")
	}
	req.Direction = strings.ToLower(strings.TrimSpace(req.Direction))
	if req.Direction == "" {
		req.Direction = "in"
	}
	if req.Direction != "in" && req.Direction != "out" {
		return req, fmt.Errorf("direction must be in or out")
	}
	if req.Port != "" {
		if !portRe.MatchString(req.Port) {
			return req, fmt.Errorf("port must be a number, a range like 8000:8010, or a list like 80,443")
		}
		// The pattern bounds the digits, not the numbers. 0 and 99999 both
		// match five digits or fewer and neither is a port, and a range
		// written backwards is accepted by the pattern and rejected by every
		// tool underneath — with an error that says nothing about which of
		// the four fields on the form was wrong.
		if err := boundPorts(req.Port); err != nil {
			return req, err
		}
	}
	req.Protocol = strings.ToLower(strings.TrimSpace(req.Protocol))
	if req.Protocol != "" && req.Protocol != "tcp" && req.Protocol != "udp" {
		return req, fmt.Errorf("protocol must be tcp or udp")
	}
	// A list becomes a multiport match, and iptables has no multiport without
	// a protocol. Saying so beats letting the tool reject it three layers down.
	if strings.Contains(req.Port, ",") && req.Protocol == "" {
		return req, fmt.Errorf("a list of ports needs a protocol")
	}
	if req.App != "" {
		if !appNameRe.MatchString(req.App) {
			return req, fmt.Errorf("invalid application profile name")
		}
		if req.Port != "" {
			return req, fmt.Errorf("an application profile already names its ports")
		}
	}
	if req.Port == "" && req.App == "" && req.From == "" {
		return req, fmt.Errorf("a rule needs a port, a profile or a source address")
	}
	if !commentRe.MatchString(req.Comment) {
		return req, fmt.Errorf("comment may only contain letters, digits, spaces and . _ : -")
	}
	if err := validAddress(req.From, "from"); err != nil {
		return req, err
	}
	if err := validAddress(req.To, "to"); err != nil {
		return req, err
	}
	if req.Position < 0 || req.Position > 9999 {
		return req, fmt.Errorf("position must be a rule number")
	}
	return req, nil
}

// boundPorts checks every number in a port specification is a real port and
// every range runs the right way round.
func boundPorts(spec string) error {
	for _, part := range strings.Split(spec, ",") {
		lo, hi, isRange := strings.Cut(part, ":")
		from, err := portNumber(lo)
		if err != nil {
			return err
		}
		if !isRange {
			continue
		}
		to, err := portNumber(hi)
		if err != nil {
			return err
		}
		if from > to {
			return fmt.Errorf("the port range %s starts above where it ends", part)
		}
	}
	return nil
}

func portNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%q is not a port; ports run from 1 to 65535", strings.TrimSpace(s))
	}
	return n, nil
}

// validAddress accepts an IP, a CIDR, or the word for "no restriction".
func validAddress(addr, field string) error {
	if addr == "" || strings.EqualFold(addr, "any") {
		return nil
	}
	if _, _, err := net.ParseCIDR(addr); err == nil {
		return nil
	}
	if net.ParseIP(addr) != nil {
		return nil
	}
	return fmt.Errorf("%s must be an IP address or CIDR", field)
}

// guardLockout refuses a deny rule that covers the address the operator is
// connecting from. Applying it would sever the session mid-request and leave
// the machine reachable only through the provider's console.
func guardLockout(action, direction, from, callerIP string) error {
	if direction != "in" || (action != "deny" && action != "reject") {
		return nil
	}
	caller := net.ParseIP(callerIP)
	if caller == nil {
		return nil
	}
	if from == "" {
		return fmt.Errorf("%w: a blanket inbound %s has no source restriction", ErrLockout, action)
	}
	if ip := net.ParseIP(from); ip != nil {
		if ip.Equal(caller) {
			return fmt.Errorf("%w: %s is the address you are connected from", ErrLockout, from)
		}
		return nil
	}
	if _, network, err := net.ParseCIDR(from); err == nil && network.Contains(caller) {
		return fmt.Errorf("%w: %s covers %s, the address you are connected from", ErrLockout, from, callerIP)
	}
	return nil
}

// ReplaceRule edits a rule, which on a firewall means replacing it.
//
// Neither ufw nor firewalld has an edit: a rule is a line, and changing one
// means removing it and putting another in its place. The order here is the
// part worth getting right — the replacement goes in *first*, so a failure
// leaves the original rule doing its job. Deleting first and then failing to
// add would leave a hole in the firewall, which is the one outcome an edit
// must never produce.
func (s *Service) ReplaceRule(ctx context.Context, number int, req RuleRequest, callerIP string) (string, error) {
	b, err := s.writable(ctx, func(c FirewallCapabilities) bool { return c.Editable })
	if err != nil {
		return "", err
	}
	if clean, err := normaliseRule(req); err == nil {
		if err := s.guardChange(ctx, b, func(st *FirewallStatus) error { return simulateReplace(st, number, clean) }); err != nil {
			return "", err
		}
	}
	return replaceRule(ctx, b, number, req, callerIP)
}

// replaceRule is the ordering itself, separated from backend detection so it
// can be driven against a backend that records what it was asked to do. The
// sequence is the whole point of the function and a host with no firewall
// installed is no place to find out it is wrong.
func replaceRule(ctx context.Context, b fwBackend, number int, req RuleRequest, callerIP string) (string, error) {
	if !b.Capabilities().Editable {
		return "", fmt.Errorf("%w: %s", ErrReadOnly, b.Capabilities().ReadOnlyReason)
	}
	if number <= 0 {
		return "", fmt.Errorf("rule number must be positive")
	}
	clean, err := normaliseRule(req)
	if err != nil {
		return "", err
	}
	if err := guardLockout(clean.Action, clean.Direction, clean.From, callerIP); err != nil {
		return "", err
	}

	// The rule being replaced is read *before* anything is added, whichever
	// backend this is: adding changes both the positions firewalld's listing
	// implies and the numbers ufw prints, so a rule identified afterwards is
	// not necessarily the rule that was asked for.
	st, err := b.Status(ctx)
	if err != nil {
		return "", err
	}
	old, ok := ruleNumbered(st.Rules, number)
	if !ok {
		return "", fmt.Errorf("no rule %d", number)
	}
	// A forwarding rule — `ufw route`, which is what ufw-docker writes — is
	// neither inbound nor outbound, and this form has no way to say so. An
	// edit would therefore rewrite it as an inbound rule and quietly change
	// what it does, which is worse than declining to edit it.
	if old.Direction != "" && !strings.EqualFold(old.Direction, "in") && !strings.EqualFold(old.Direction, "out") {
		return "", fmt.Errorf("rule %d is a %s rule, which this form cannot express — edit it with ufw directly", number, old.Direction)
	}
	// The form has no device field either; saving it would widen the rule
	// from one interface to all of them.
	if old.Interface != "" {
		return "", fmt.Errorf("rule %d applies only on %s, which this form cannot express — edit it with %s directly", number, old.Interface, b.Kind())
	}
	// firewalld has no ordering and no numbers of its own, so the handle it
	// was given is what removes it. ufw is ordered, so the replacement is
	// inserted where the original sits and the original is found again by what
	// it says — never by arithmetic on the number, which is wrong the moment
	// ufw declines to insert anything.
	oldHandle := old.Handle
	switch b.Kind() {
	case BackendUFW:
		clean.Position = number
		oldHandle = ""
	case BackendNFTOwned:
		// Ordered too, but its handle is a stable id, so the original is
		// removed by that rather than found again.
		clean.Position = number
	}
	if checking(ctx) {
		return "", ErrChecked
	}

	added, err := b.AddRule(ctx, clean)
	if err != nil {
		// An add that changed nothing must not be followed by a delete. ufw
		// answers a duplicate with "Skipping adding existing rule" and exit 0,
		// and the delete that used to follow removed the rule *below* the one
		// being edited.
		return "", err
	}

	var removed string
	if oldHandle != "" {
		remover, ok := b.(handleRemover)
		if !ok {
			return added, fmt.Errorf("the new rule was added, but this backend cannot remove the old one")
		}
		removed, err = remover.removeHandle(ctx, oldHandle)
	} else {
		locator, ok := b.(ruleLocator)
		if !ok {
			return added, fmt.Errorf("the new rule was added, but this backend cannot find the old one to remove it")
		}
		target, found := locator.findRule(ctx, old, old.IPv6)
		if !found {
			// Already gone. Nothing to undo and nothing to report as a
			// half-finished edit.
			return added, nil
		}
		removed, err = b.DeleteRule(ctx, target)
	}
	if err != nil {
		// Both rules exist now. That is visible in the list and harmless — the
		// firewall is at least as strict as it was — which is why this is the
		// order to fail in.
		return added, fmt.Errorf("the replacement was added but the original rule could not be removed: %w", err)
	}
	return strings.TrimSpace(added + "\n" + removed), nil
}

// ruleNumbered finds the rule a client's number refers to. Backends that print
// their own numbers are matched on those; the rest are numbered by position,
// exactly as Service.Status hands them out.
func ruleNumbered(rules []Rule, number int) (Rule, bool) {
	for _, r := range rules {
		if r.Number == number {
			return r, true
		}
	}
	if number >= 1 && number <= len(rules) {
		return rules[number-1], true
	}
	return Rule{}, false
}

// sameRule reports whether two listed rules say the same thing.
//
// Everything an operator chose is compared and everything the listing computed
// — the number, the raw line, the catalogue's annotations — is not, so the
// answer survives the renumbering a delete causes.
func sameRule(a, b Rule) bool {
	return a.Action == b.Action &&
		strings.EqualFold(a.Direction, b.Direction) &&
		a.To == b.To && a.From == b.From &&
		a.Port == b.Port && a.Protocol == b.Protocol &&
		a.Comment == b.Comment && a.Interface == b.Interface
}

// ruleLocator is a backend that can find a rule again after the list beneath it
// has moved. Discovered by assertion for the same reason handleRemover is: only
// the numbered backend needs it.
type ruleLocator interface {
	findRule(ctx context.Context, want Rule, ipv6 bool) (int, bool)
}

// handleRemover is a backend that identifies rules by something other than
// their position. Discovered by assertion rather than added to fwBackend,
// because only firewalld needs it and ufw's answer would be a lie.
type handleRemover interface {
	removeHandle(ctx context.Context, handle string) (string, error)
}

func (s *Service) DeleteRule(ctx context.Context, number int) (string, error) {
	b, err := s.writable(ctx, func(c FirewallCapabilities) bool { return c.Editable })
	if err != nil {
		return "", err
	}
	if number <= 0 {
		return "", fmt.Errorf("rule number must be positive")
	}
	if err := s.guardChange(ctx, b, func(st *FirewallStatus) error { return simulateDelete(st, number) }); err != nil {
		return "", err
	}
	if checking(ctx) {
		return "", ErrChecked
	}
	return b.DeleteRule(ctx, number)
}

func (s *Service) SetEnabled(ctx context.Context, enabled bool) (string, error) {
	b, err := s.writable(ctx, func(c FirewallCapabilities) bool { return c.Toggle })
	if err != nil {
		return "", err
	}
	if err := s.guardChange(ctx, b, func(st *FirewallStatus) error { st.Enabled = enabled; return nil }); err != nil {
		return "", err
	}
	if checking(ctx) {
		return "", ErrChecked
	}
	return b.SetEnabled(ctx, enabled)
}

// SetDefaultPolicy changes what happens to a packet no rule matched.
//
// This is the single most consequential control on the page: switching the
// inbound default to deny on a host whose rule list admits nobody takes the
// machine off the network, this dashboard included, in one command. The
// access guard refuses a default that would refuse the operator's own
// connection, SSH or Caddy's public ingress where they are admitted now; the
// no-allow-rule guard below remains for callers without that context.
func (s *Service) SetDefaultPolicy(ctx context.Context, direction, policy string) (string, error) {
	b, err := s.writable(ctx, func(c FirewallCapabilities) bool { return c.DefaultPolicy })
	if err != nil {
		return "", err
	}
	direction = strings.ToLower(strings.TrimSpace(direction))
	policy = strings.ToLower(strings.TrimSpace(policy))
	switch direction {
	case "incoming", "outgoing", "routed":
	default:
		return "", fmt.Errorf("direction must be incoming, outgoing or routed")
	}
	switch policy {
	case "allow", "deny", "reject":
	default:
		return "", fmt.Errorf("policy must be allow, deny or reject")
	}
	if direction == "incoming" && policy != "allow" {
		st, err := s.statusOf(ctx, b)
		if err == nil && st.Enabled && !admitsAnything(st.Rules) {
			return "", fmt.Errorf("%w: no inbound allow rule exists, so a default of %s would refuse every connection to this host",
				ErrLockout, policy)
		}
	}
	if err := s.guardChange(ctx, b, func(st *FirewallStatus) error { return simulatePolicy(st, direction, policy) }); err != nil {
		return "", err
	}
	if checking(ctx) {
		return "", ErrChecked
	}
	return b.SetDefaultPolicy(ctx, direction, policy)
}

// admitsAnything reports whether any inbound rule lets a connection in. A rule
// printed without a direction is inbound — the listings only mark exceptions.
func admitsAnything(rules []Rule) bool {
	for _, r := range rules {
		if r.Action == "ALLOW" || r.Action == "LIMIT" {
			if r.Direction == "" || strings.EqualFold(r.Direction, "in") {
				return true
			}
		}
	}
	return false
}

// SetLogging changes how much the firewall writes about what it dropped.
//
// Worth exposing because the default is quiet and the difference matters after
// the fact: a firewall that logs nothing leaves an incident with no record of
// what was refused, and "off" is a choice somebody should have made on purpose
// rather than inherited.
func (s *Service) SetLogging(ctx context.Context, level string) (string, error) {
	b, err := s.writable(ctx, func(c FirewallCapabilities) bool { return c.Logging })
	if err != nil {
		return "", err
	}
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "off", "on", "low", "medium", "high", "full":
	default:
		return "", fmt.Errorf("logging level must be off, on, low, medium, high or full")
	}
	if checking(ctx) {
		return "", ErrChecked
	}
	return b.SetLogging(ctx, level)
}

// Reset removes every rule and returns the firewall to its installed state.
// firewalld's equivalent reloads the default zone's shipped settings and is
// judged against the access guard like any other change (firewall_reset.go).
func (s *Service) Reset(ctx context.Context) (string, error) {
	b, err := s.writable(ctx, func(c FirewallCapabilities) bool { return c.Reset })
	if err != nil {
		if errors.Is(err, ErrReadOnly) {
			return "", fmt.Errorf("%w: this firewall has no reset that is safe to offer", ErrReadOnly)
		}
		return "", err
	}
	if err := s.guardChange(ctx, b, func(st *FirewallStatus) error { return simulateReset(ctx, st) }); err != nil {
		return "", err
	}
	if checking(ctx) {
		return "", ErrChecked
	}
	return b.Reset(ctx)
}

// AppProfiles lists the named service bundles this host defines — ufw's
// application profiles, firewalld's services.
//
// They are worth surfacing because they are the form the host's own packages
// speak: a rule added as "Nginx Full" keeps meaning what it says if the
// package later adds a port, and reads better in the rule list than 80,443.
func (s *Service) AppProfiles(ctx context.Context) ([]AppProfile, error) {
	b := s.backend(ctx)
	if b == nil {
		return []AppProfile{}, nil
	}
	return b.Profiles(ctx)
}

// run invokes a firewall tool on the host. These manage host services, so they
// run there: a copy of ufw or firewall-cmd inside this image would report on
// the container.
//
// A variable rather than a function so a test can put a recorded transcript
// behind it. The parsers in this package read output whose exact shape — ufw's
// "(v6)" suffix, firewalld's tab-indented rich rules, the column order of
// `iptables -L -n -v --line-numbers` — is the thing most likely to be wrong,
// and a host that happens to run one of the three is no way to check the other
// two.
var run = runOnHost

func runOnHost(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := hostexec.CommandOnHost(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", fmt.Errorf("%s is not installed on this host", name)
		}
		return buf.String(), fmt.Errorf("%s: %s", name, strings.TrimSpace(buf.String()))
	}
	return buf.String(), nil
}
