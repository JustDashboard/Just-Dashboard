package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// ErrRuleChanged is an edit naming a rule identity the firewall no longer
// holds: someone else changed the list since it was read.
var ErrRuleChanged = errors.New("the rule has changed since the list was read")

// ErrUnreadable is a change refused because the firewall could not be read:
// nothing then shows what the change would do to the operator's access.
var ErrUnreadable = errors.New("the firewall could not be read, so the change cannot be checked against your access")

// ErrBackendChanged is a change prepared for one firewall finding another in
// charge when it runs.
var ErrBackendChanged = errors.New("the firewall in charge changed while the change was being made")

// ErrChecked ends the first pass of a scoped change where the host would be
// changed: everything before it, validation and the access guard, passed.
var ErrChecked = errors.New("the change was checked and not applied")

// A host firewall change made under the network journal runs twice under
// its lock: once only as far as its validation and access guard, so a
// refusal never opens a journal or touches the host, then for real. Both
// passes name the firewall the journal was prepared for, so one that changed
// hands in between is refused rather than written through another path.
type scopeKey struct{}

type changeScope struct {
	backend   Backend
	checkOnly bool
}

// Scoped binds a change to the firewall its journal was prepared for and,
// with checkOnly, stops it before the host is touched with ErrChecked.
func Scoped(ctx context.Context, backend Backend, checkOnly bool) context.Context {
	return context.WithValue(ctx, scopeKey{}, changeScope{backend: backend, checkOnly: checkOnly})
}

func checking(ctx context.Context) bool {
	scope, ok := ctx.Value(scopeKey{}).(changeScope)
	return ok && scope.checkOnly
}

// AccessRefusal is a change that would refuse a required way in that the
// firewall admits now. It is a lockout, carried with its evidence.
type AccessRefusal struct {
	Check  AccessCheck
	Before AccessCheck
}

func (e *AccessRefusal) Error() string {
	c := e.Check
	if c.Verdict == "unknown" {
		return fmt.Sprintf("%s: %s (%d/%s, %s from %s) is %s now, and afterwards it could not be shown to be: %s Make the rule specific enough to judge, or add a rule admitting it first.",
			ErrLockout.Error(), c.Name, c.Port, c.Protocol, c.Family, c.Source, e.Before.Verdict, c.Reason)
	}
	return fmt.Sprintf("%s: %s (%d/%s, %s from %s) is %s now and would be refused: %s Add a rule admitting it first.",
		ErrLockout.Error(), c.Name, c.Port, c.Protocol, c.Family, c.Source, e.Before.Verdict, c.Reason)
}

func (e *AccessRefusal) Unwrap() error { return ErrLockout }

// admits is whether a verdict lets the connection in.
func admits(verdict string) bool {
	return verdict == "admitted" || verdict == "limited" || verdict == "unfiltered"
}

// cloneStatus is a status a simulation can change without touching the
// reading it came from.
func cloneStatus(st *FirewallStatus) *FirewallStatus {
	c := *st
	c.Rules = append([]Rule(nil), st.Rules...)
	c.Zones = append([]FirewallZone(nil), st.Zones...)
	for i := range c.Zones {
		if c.Zones[i].Default {
			c.Zones[i].Rules = c.Rules
		}
	}
	return &c
}

// guardChange compares the required access checks before and after a
// simulated change. Without an access context there is nothing to compare
// against. An unreadable status refuses the change: what it would do to the
// operator's access is then unknown.
func (s *Service) guardChange(ctx context.Context, b fwBackend, mutate func(*FirewallStatus) error) error {
	a, ok := accessFrom(ctx)
	if !ok {
		return nil
	}
	before, err := s.statusOf(ctx, b)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	if before.Error != "" {
		return fmt.Errorf("%w: %s", ErrUnreadable, before.Error)
	}
	after := cloneStatus(before)
	if err := mutate(after); err != nil {
		return err
	}
	return compareAccess(before, after, a)
}

func compareAccess(before, after *FirewallStatus, a AccessContext) error {
	checks := accessChecks(a)
	was := evaluateAccess(before, checks, a.Profiles)
	now := evaluateAccess(after, checks, a.Profiles)
	// A way in admitted now must still be shown admitted: one the result can
	// no longer be judged on is refused as surely as one it refuses, since
	// nothing then says SSH or the dashboard survives.
	for i := range checks {
		if checks[i].Required && admits(was[i].Verdict) && (now[i].Verdict == "refused" || now[i].Verdict == "unknown") {
			return &AccessRefusal{Check: now[i], Before: was[i]}
		}
	}
	return nil
}

// AccessComparison is one check before and after a proposed change.
type AccessComparison struct {
	AccessCheck
	Before string `json:"before"`
}

// Preflight is a proposed change judged before it is made: every access
// check before and after, the findings the result would have, and the
// refusal an apply would answer with.
type Preflight struct {
	Backend  Backend            `json:"backend"`
	Checks   []AccessComparison `json:"checks"`
	Findings []RuleFinding      `json:"findings"`
	Refusal  string             `json:"refusal,omitempty"`
}

// ProposedChange names one change for a preflight. Op is enable, disable,
// policy, add, replace, delete or reset.
type ProposedChange struct {
	Op        string       `json:"op"`
	Direction string       `json:"direction,omitempty"`
	Policy    string       `json:"policy,omitempty"`
	RuleID    string       `json:"ruleId,omitempty"`
	Rule      *RuleRequest `json:"rule,omitempty"`
}

// Preflight evaluates a proposed change without making it.
func (s *Service) Preflight(ctx context.Context, change ProposedChange) (*Preflight, error) {
	b, detection := s.selectBackend(ctx)
	if b == nil {
		return nil, ErrNoFirewall
	}
	before, err := s.statusOf(ctx, b)
	if err != nil {
		return nil, err
	}
	before.Detection = detection
	after := cloneStatus(before)
	mutate, err := s.simulation(ctx, before, change)
	if err != nil {
		return nil, err
	}
	if err := mutate(after); err != nil {
		return nil, err
	}
	return s.preflightOf(ctx, b.Kind(), before, after), nil
}

func (s *Service) preflightOf(ctx context.Context, kind Backend, before, after *FirewallStatus) *Preflight {
	p := &Preflight{Backend: kind, Checks: []AccessComparison{}}
	p.Findings, _ = analyzeRules(kind, after.Rules)
	a, ok := accessFrom(ctx)
	if !ok {
		return p
	}
	checks := accessChecks(a)
	was := evaluateAccess(before, checks, a.Profiles)
	now := evaluateAccess(after, checks, a.Profiles)
	for i := range checks {
		p.Checks = append(p.Checks, AccessComparison{AccessCheck: now[i], Before: was[i].Verdict})
	}
	if err := compareAccess(before, after, a); err != nil {
		p.Refusal = err.Error()
	}
	return p
}

// simulation turns a proposed change into the mutation of a status.
func (s *Service) simulation(ctx context.Context, st *FirewallStatus, change ProposedChange) (func(*FirewallStatus) error, error) {
	switch change.Op {
	case "enable", "disable":
		return func(st *FirewallStatus) error { st.Enabled = change.Op == "enable"; return nil }, nil
	case "policy":
		return func(st *FirewallStatus) error {
			return simulatePolicy(st, strings.ToLower(change.Direction), strings.ToLower(change.Policy))
		}, nil
	case "reset":
		return func(st *FirewallStatus) error { return simulateReset(ctx, st) }, nil
	case "add", "replace":
		if change.Rule == nil {
			return nil, fmt.Errorf("an %s names a rule", change.Op)
		}
		clean, err := normaliseRule(*change.Rule)
		if err != nil {
			return nil, err
		}
		if change.Op == "add" {
			return func(st *FirewallStatus) error { return simulateAdd(st, clean) }, nil
		}
		old, ok := ruleWithID(st.Rules, change.RuleID)
		if !ok {
			return nil, ErrRuleChanged
		}
		return func(st *FirewallStatus) error { return simulateReplace(st, old.Number, clean) }, nil
	case "delete":
		old, ok := ruleWithID(st.Rules, change.RuleID)
		if !ok {
			return nil, ErrRuleChanged
		}
		return func(st *FirewallStatus) error { return simulateDelete(st, old.Number) }, nil
	}
	return nil, fmt.Errorf("%q is not a firewall change", change.Op)
}

// EvaluateAccess is the current verdict for each access check, for the page.
func (s *Service) EvaluateAccess(ctx context.Context, st *FirewallStatus) []AccessCheck {
	a, ok := accessFrom(ctx)
	if !ok || st == nil {
		return nil
	}
	return evaluateAccess(st, accessChecks(a), a.Profiles)
}

func ruleWithID(rules []Rule, id string) (Rule, bool) {
	for _, r := range rules {
		if r.ID == id && id != "" {
			return r, true
		}
	}
	return Rule{}, false
}

// RuleByID resolves a stable identity to the rule as it stands now.
func (s *Service) RuleByID(ctx context.Context, id string) (Rule, error) {
	st, err := s.Status(ctx)
	if err != nil {
		return Rule{}, err
	}
	if st.Error != "" {
		return Rule{}, errors.New(st.Error)
	}
	r, ok := ruleWithID(st.Rules, id)
	if !ok {
		return Rule{}, ErrRuleChanged
	}
	return r, nil
}

// requestRules is what a validated request becomes in a listing: ufw writes
// a twin for each family unless an address picks one; firewalld and the owned
// table write one rule for both.
func requestRules(b Backend, req RuleRequest) []Rule {
	r := Rule{Action: strings.ToUpper(req.Action), Direction: strings.ToUpper(req.Direction), From: firstNonBlank(req.From, "Anywhere"),
		Port: req.Port, Protocol: req.Protocol, Comment: req.Comment}
	if strings.EqualFold(r.From, "any") {
		r.From = "Anywhere"
	}
	dest := ""
	if req.To != "" && !strings.EqualFold(req.To, "any") {
		dest = req.To
	}
	switch {
	case req.App != "":
		r.To = strings.TrimSpace(dest + " " + req.App)
	case req.Port != "":
		r.To = req.Port
		if req.Protocol != "" {
			r.To += "/" + req.Protocol
		}
		if dest != "" {
			r.To = dest + " " + r.To
		}
	default:
		r.To = firstNonBlank(dest, "Anywhere")
	}
	family := ""
	for _, addr := range []string{req.From, dest} {
		if p, err := parseSelector(addr); err == nil {
			family = map[bool]string{true: "ipv6", false: "ipv4"}[p.Addr().Is6()]
		}
	}
	if b != BackendUFW {
		r.IPv6 = family == "ipv6"
		r.BothFamilies = family == ""
		return []Rule{r}
	}
	switch family {
	case "ipv4":
		return []Rule{r}
	case "ipv6":
		r.IPv6 = true
		return []Rule{r}
	}
	twin := r
	twin.IPv6 = true
	return []Rule{r, twin}
}

// renumberRules assigns positional numbers and identities after a change.
func renumberRules(st *FirewallStatus) {
	for i := range st.Rules {
		st.Rules[i].Number = i + 1
	}
	assignRuleIDs(st.Backend, st.Rules)
	for i := range st.Zones {
		if st.Zones[i].Default {
			st.Zones[i].Rules = st.Rules
		}
	}
}

// simulateAdd places the request's rules where the backend would: at a
// chosen position, a source block in front of its family, otherwise at the
// end of its family's block.
func simulateAdd(st *FirewallStatus, req RuleRequest) error {
	for _, r := range requestRules(st.Backend, req) {
		r.Zone = st.Zone
		pos := -1
		switch {
		case req.Position > 0 && st.Backend != BackendFirewalld:
			pos = familyIndex(st.Rules, r, req.Position)
		case st.Backend == BackendUFW && blocksASource(req):
			pos = firstOfFamily(st.Rules, r.IPv6)
		}
		if pos < 0 || pos > len(st.Rules) {
			pos = endOfFamily(st.Rules, r.IPv6, st.Backend)
		}
		st.Rules = append(st.Rules[:pos], append([]Rule{r}, st.Rules[pos:]...)...)
	}
	renumberRules(st)
	return nil
}

// familyIndex is where ufw's `insert N` lands a rule: the IPv6 twin goes at
// the same offset within the IPv6 block.
func familyIndex(rules []Rule, r Rule, position int) int {
	if !r.IPv6 {
		return min(position-1, len(rules))
	}
	v4 := 0
	for _, x := range rules {
		if !x.IPv6 {
			v4++
		}
	}
	return min(v4+position-1, len(rules))
}

func firstOfFamily(rules []Rule, ipv6 bool) int {
	for i, x := range rules {
		if x.IPv6 == ipv6 {
			return i
		}
	}
	return endOfFamily(rules, ipv6, BackendUFW)
}

func endOfFamily(rules []Rule, ipv6 bool, b Backend) int {
	if b != BackendUFW || ipv6 {
		return len(rules)
	}
	for i, x := range rules {
		if x.IPv6 {
			return i
		}
	}
	return len(rules)
}

// simulateDelete removes a numbered rule and, on ufw, its IPv6 twin.
func simulateDelete(st *FirewallStatus, number int) error {
	idx := -1
	for i, r := range st.Rules {
		if r.Number == number {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("no rule %d", number)
	}
	gone := st.Rules[idx]
	st.Rules = append(st.Rules[:idx], st.Rules[idx+1:]...)
	if st.Backend == BackendUFW && !gone.IPv6 {
		for i, r := range st.Rules {
			if r.IPv6 && sameRule(r, gone) {
				st.Rules = append(st.Rules[:i], st.Rules[i+1:]...)
				break
			}
		}
	}
	renumberRules(st)
	return nil
}

// simulateReplace puts the request where the rule it replaces was.
func simulateReplace(st *FirewallStatus, number int, req RuleRequest) error {
	var old Rule
	found := false
	for _, r := range st.Rules {
		if r.Number == number {
			old, found = r, true
		}
	}
	if !found {
		return fmt.Errorf("no rule %d", number)
	}
	if st.Backend != BackendFirewalld {
		req.Position = number
		if old.IPv6 {
			v4 := 0
			for _, x := range st.Rules {
				if !x.IPv6 {
					v4++
				}
			}
			req.Position = number - v4
		}
	}
	if err := simulateDelete(st, number); err != nil {
		return err
	}
	return simulateAdd(st, req)
}

// simulatePolicy changes a default; on firewalld the inbound default is the
// default zone's target.
func simulatePolicy(st *FirewallStatus, direction, policy string) error {
	switch direction {
	case "incoming":
		st.Policy.Incoming = policy
		if st.Backend == BackendFirewalld {
			target := map[string]string{"allow": "ACCEPT", "deny": "DROP", "reject": "%%REJECT%%"}[policy]
			for i := range st.Zones {
				if st.Zones[i].Default {
					st.Zones[i].Target = target
				}
			}
			st.Default = target
		}
	case "outgoing":
		st.Policy.Outgoing = policy
	case "routed":
		st.Policy.Routed = policy
	default:
		return fmt.Errorf("direction must be incoming, outgoing or routed")
	}
	return nil
}

// simulateReset is what each reset leaves: ufw and the owned table end
// switched off with no rules; firewalld's default zone returns to its
// shipped settings.
func simulateReset(ctx context.Context, st *FirewallStatus) error {
	switch st.Backend {
	case BackendFirewalld:
		defaults, err := firewalldZoneDefaults(st.Zone)
		if err != nil {
			return err
		}
		st.Rules = defaults.rules
		for i := range st.Rules {
			st.Rules[i].Zone = st.Zone
		}
		st.Policy = firewalldPolicy(defaults.target)
		st.Default = defaults.target
		for i := range st.Zones {
			if st.Zones[i].Default {
				st.Zones[i].Target = defaults.target
			}
		}
	default:
		st.Enabled = false
		st.Rules = []Rule{}
		st.Policy.Incoming = "deny"
	}
	renumberRules(st)
	return nil
}

// ruleRequestFor turns a listed rule back into the request that writes it,
// for compensating a failed plan.
func ruleRequestFor(r Rule) RuleRequest {
	req := RuleRequest{Action: strings.ToLower(r.Action), Direction: strings.ToLower(firstNonBlank(r.Direction, "in")),
		Port: r.Port, Protocol: r.Protocol, Comment: r.Comment}
	if !isAnywhere(r.From) {
		req.From = strings.TrimSpace(strings.TrimSuffix(r.From, "(v6)"))
	}
	if dest := destinationAddress(r.To); !isAnywhere(dest) {
		req.To = dest
	}
	if r.Port == "" {
		if name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.To), req.To)); name != "" && !isAnywhere(name) {
			if _, err := netip.ParseAddr(name); err != nil {
				req.App = name
			}
		}
		if r.Service != "" && r.Handle != "" && strings.HasPrefix(r.Handle, "service:") {
			req.App = r.Service
		}
	}
	return req
}

// RecoveryState is what the host firewall's independent recovery restores.
// Unit is whether firewalld starts at boot: enabled, disabled, or empty when
// systemd answered anything else, which the recovery then leaves alone.
type RecoveryState struct {
	Backend Backend
	Enabled bool
	Unit    string
	Zone    string
}

// RecoveryState reads the firewall in charge, for a journal taken before a
// change, together with the status the post-change verification compares.
func (s *Service) RecoveryState(ctx context.Context) (RecoveryState, *FirewallStatus, error) {
	st, err := s.Status(ctx)
	if err != nil {
		return RecoveryState{}, nil, err
	}
	if st.Error != "" {
		return RecoveryState{Backend: st.Backend}, st, errors.New(st.Error)
	}
	rs := RecoveryState{Backend: st.Backend, Enabled: st.Enabled, Zone: st.Zone}
	if st.Backend == BackendFirewalld {
		rs.Unit = unitState(run(ctx, "systemctl", "is-enabled", "firewalld"))
	}
	return rs, st, nil
}

// unitState reads `systemctl is-enabled`, whose exit status is non-zero for
// a disabled unit, by its first line. Only a plain enabled or disabled is a
// state the recovery restores; alias, static, masked, a runtime-only
// enablement or no answer leave the unit as it is.
func unitState(out string, _ error) string {
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	switch first = strings.TrimSpace(first); first {
	case "enabled", "disabled":
		return first
	}
	return ""
}

// VerifyAccessAfter is the staged verification after a change: the firewall
// as it now enforces must still admit every required way in the status read
// before admitted. A firewall that cannot be read back fails it, since
// nothing then shows the operator's access survived.
func (s *Service) VerifyAccessAfter(ctx context.Context, before *FirewallStatus) error {
	a, ok := accessFrom(ctx)
	if !ok || before == nil {
		return nil
	}
	after, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if after.Error != "" {
		return fmt.Errorf("the firewall could not be read back after the change: %s", after.Error)
	}
	return compareAccess(before, after, a)
}

// FindRuleFor is the listed rule a request wrote, found after the write by
// what it says.
func (s *Service) FindRuleFor(ctx context.Context, req RuleRequest) (Rule, bool) {
	clean, err := normaliseRule(req)
	if err != nil {
		return Rule{}, false
	}
	st, err := s.Status(ctx)
	if err != nil || st.Error != "" {
		return Rule{}, false
	}
	want := requestRules(st.Backend, clean)[0]
	for _, r := range st.Rules {
		if r.IPv6 == want.IPv6 && r.Action == want.Action && r.Port == want.Port && r.Protocol == want.Protocol &&
			r.Comment == want.Comment && addressCovers(r.From, want.From) && addressCovers(want.From, r.From) {
			return r, true
		}
	}
	return Rule{}, false
}

// ProfileMap resolves application profile names to their ports, cached
// briefly: ufw answers one subprocess per profile.
func (s *Service) ProfileMap(ctx context.Context) map[string][]string {
	s.profilesMu.Lock()
	defer s.profilesMu.Unlock()
	if s.profiles != nil && time.Since(s.profilesAt) < time.Minute {
		return s.profiles
	}
	out := map[string][]string{}
	if profiles, err := s.AppProfiles(ctx); err == nil {
		for _, p := range profiles {
			if len(p.Ports) > 0 {
				out[p.Name] = p.Ports
			}
		}
	}
	s.profiles, s.profilesAt = out, time.Now()
	return out
}
