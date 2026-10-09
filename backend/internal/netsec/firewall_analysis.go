package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
)

// assignRuleIDs gives every rule its stable identity. ufw's handle is its
// number, which a delete changes, so ufw rules are identified by what they
// say; firewalld's handle is already the rule's own text. Identical rules
// are told apart by their order among themselves.
func assignRuleIDs(b Backend, rules []Rule) {
	seen := map[string]int{}
	for i := range rules {
		r := rules[i]
		key := strings.Join([]string{string(b), strconv.FormatBool(r.IPv6), strings.ToUpper(firstNonBlank(r.Direction, "IN")),
			r.Action, r.From, r.To, r.Port, r.Protocol, r.Interface, r.Comment, r.Zone}, "\x00")
		if b != BackendUFW && r.Handle != "" {
			key = string(b) + "\x00" + r.Zone + "\x00" + r.Handle
		}
		seen[key]++
		if seen[key] > 1 {
			key += "\x00" + strconv.Itoa(seen[key])
		}
		sum := sha256.Sum256([]byte(key))
		rules[i].ID = "fw-" + hex.EncodeToString(sum[:6])
	}
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// RuleFinding is a rule the evaluation order makes unreachable for some or
// all of what it selects. Kind is shadowed (an earlier rule with a different
// verdict decides everything it selects) or redundant (an earlier rule with
// the same verdict does).
type RuleFinding struct {
	RuleID   string `json:"ruleId"`
	Number   int    `json:"number"`
	Kind     string `json:"kind"`
	ByID     string `json:"byId"`
	ByNumber int    `json:"byNumber"`
	Reason   string `json:"reason"`
}

// analyzeRules finds rules an earlier rule fully covers. Only first-match
// lists are analysed; firewalld orders deny before allow within a zone
// whatever the listing shows, so its findings would be invented.
func analyzeRules(b Backend, rules []Rule) ([]RuleFinding, string) {
	findings := []RuleFinding{}
	switch b {
	case BackendUFW, BackendNFTOwned:
	case BackendFirewalld:
		return findings, "firewalld evaluates a zone's denials before its allowances regardless of listing order, so ordering findings are not computed."
	default:
		return findings, "Rules are read across several chains; ordering findings are not computed for raw iptables."
	}
	for j := range rules {
		later := rules[j]
		for i := 0; i < j; i++ {
			earlier := rules[i]
			if !ruleCovers(earlier, later) {
				continue
			}
			kind, reason := "shadowed", fmt.Sprintf("Rule %d (%s) is read first and decides everything rule %d selects, so rule %d never applies.", earlier.Number, strings.ToLower(earlier.Action), later.Number, later.Number)
			if verdictOf(earlier.Action) == verdictOf(later.Action) {
				kind, reason = "redundant", fmt.Sprintf("Rule %d already %s everything rule %d selects.", earlier.Number, verdictVerb(earlier.Action), later.Number)
			}
			findings = append(findings, RuleFinding{RuleID: later.ID, Number: later.Number, Kind: kind, ByID: earlier.ID, ByNumber: earlier.Number, Reason: reason})
			break
		}
	}
	return findings, ""
}

func verdictOf(action string) string {
	switch strings.ToUpper(action) {
	case "ALLOW", "LIMIT", "ACCEPT":
		return "allow"
	case "DENY", "REJECT", "DROP":
		return "deny"
	}
	return strings.ToLower(action)
}

func verdictVerb(action string) string {
	if verdictOf(action) == "allow" {
		return "admits"
	}
	return "refuses"
}

// ruleCovers reports whether rule a selects everything rule b does. It
// claims coverage only from fields it can compare; an application profile,
// a source port or an unknown action is never covered.
func ruleCovers(a, b Rule) bool {
	if !a.BothFamilies && (b.BothFamilies || a.IPv6 != b.IPv6) {
		return false
	}
	if !strings.EqualFold(firstNonBlank(a.Direction, "IN"), firstNonBlank(b.Direction, "IN")) {
		return false
	}
	if verdictOf(a.Action) != "allow" && verdictOf(a.Action) != "deny" {
		return false
	}
	if a.Interface != "" && a.Interface != b.Interface {
		return false
	}
	if a.Protocol != "" && a.Protocol != b.Protocol {
		return false
	}
	if !addressCovers(a.From, b.From) || !addressCovers(destinationAddress(a.To), destinationAddress(b.To)) {
		return false
	}
	pa, okA := ruleDestinationPorts(a)
	pb, okB := ruleDestinationPorts(b)
	if !okA || !okB {
		return false
	}
	return portsCover(pa, pb)
}

// addressCovers reports whether address selector a holds b's.
func addressCovers(a, b string) bool {
	if isAnywhere(a) {
		return true
	}
	if isAnywhere(b) {
		return false
	}
	pa, err1 := parseSelector(a)
	pb, err2 := parseSelector(b)
	return err1 == nil && err2 == nil && pa.Bits() <= pb.Bits() && pa.Contains(pb.Addr())
}

func parseSelector(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "(v6)"))
	if !strings.Contains(s, "/") {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return p.Masked(), nil
}

// destinationAddress is the address half of ufw's destination column, or
// Anywhere when the column holds only a port or a profile.
func destinationAddress(to string) string {
	fields := strings.Fields(strings.TrimSuffix(strings.TrimSpace(to), "(v6)"))
	if len(fields) == 2 {
		if _, err := parseSelector(fields[0]); err == nil {
			return fields[0]
		}
	}
	if len(fields) == 1 {
		if _, err := parseSelector(fields[0]); err == nil {
			return fields[0]
		}
	}
	return "Anywhere"
}

type portRange struct{ lo, hi int }

// ruleDestinationPorts is the set of destination ports a rule selects; nil
// is every port. An application profile is not resolved here.
func ruleDestinationPorts(r Rule) ([]portRange, bool) {
	if r.Port == "" {
		last := lastField(r.To)
		if last == "" || isAnywhere(last) || last == "any" {
			return nil, true
		}
		if _, err := parseSelector(last); err == nil {
			return nil, true
		}
		return nil, false
	}
	return parsePortSpec(r.Port)
}

func parsePortSpec(spec string) ([]portRange, bool) {
	var out []portRange
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		lo, hi, ranged := strings.Cut(part, ":")
		if !ranged {
			lo, hi, ranged = strings.Cut(part, "-")
		}
		a, err := strconv.Atoi(lo)
		if err != nil {
			return nil, false
		}
		b := a
		if ranged {
			if b, err = strconv.Atoi(hi); err != nil {
				return nil, false
			}
		}
		out = append(out, portRange{a, b})
	}
	return out, true
}

// portsCover reports whether set a holds every port of b; nil is all.
func portsCover(a, b []portRange) bool {
	if a == nil {
		return true
	}
	if b == nil {
		return false
	}
	for _, r := range b {
		if !rangeCovered(a, r) {
			return false
		}
	}
	return true
}

// rangeCovered walks a range through the set's ranges, in any order.
func rangeCovered(a []portRange, r portRange) bool {
	for p := r.lo; p <= r.hi; {
		found := false
		for _, x := range a {
			if x.lo <= p && p <= x.hi {
				p, found = x.hi+1, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func portsContain(set []portRange, port int) bool {
	if set == nil {
		return true
	}
	for _, r := range set {
		if r.lo <= port && port <= r.hi {
			return true
		}
	}
	return false
}

// AccessContext is what the request knows about the operator's own way in.
// The API puts it on the context; writes without it keep only the source
// lockout guard.
type AccessContext struct {
	// Client is the operator's address, and Interface the device their
	// connection arrives on.
	Client    string
	Interface string
	// Dashboard are the ports the dashboard is served on through Caddy.
	Dashboard []int
	// SSH are the ports sshd listens on.
	SSH []int
	// Ingress are the ports Caddy serves every site on, whose public
	// reachability a change must not take away, and Uplink the device that
	// public traffic arrives on.
	Ingress []int
	Uplink  string
	// Profiles resolve application profile names to their ports.
	Profiles map[string][]string
}

type accessKey struct{}

// WithAccess carries the operator's access context into a firewall write.
func WithAccess(ctx context.Context, a AccessContext) context.Context {
	return context.WithValue(ctx, accessKey{}, a)
}

func accessFrom(ctx context.Context) (AccessContext, bool) {
	a, ok := ctx.Value(accessKey{}).(AccessContext)
	return a, ok
}

// AccessCheck is one way in and whether the firewall admits it. Verdict is
// admitted, limited (admitted at a rate), refused, unfiltered (the firewall
// is off) or unknown. Required checks are the ones a change may not take
// away.
type AccessCheck struct {
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Family    string `json:"family"`
	Source    string `json:"source"`
	Interface string `json:"interface,omitempty"`
	Required  bool   `json:"required"`
	Verdict   string `json:"verdict"`
	Rule      int    `json:"rule,omitempty"`
	RuleID    string `json:"ruleId,omitempty"`
	Reason    string `json:"reason"`
}

// accessChecks are the tuples a change is judged against: the operator's
// connection to the dashboard and to SSH from their own address, and public
// ingress to Caddy in both families.
func accessChecks(a AccessContext) []AccessCheck {
	var out []AccessCheck
	client, err := netip.ParseAddr(a.Client)
	if err == nil && !client.IsLoopback() {
		family := "ipv4"
		if client.Unmap().Is6() {
			family = "ipv6"
		}
		for _, port := range uniquePorts(a.Dashboard) {
			out = append(out, AccessCheck{Name: "Your connection to the dashboard", Port: port, Protocol: "tcp", Family: family, Source: client.Unmap().String(), Interface: a.Interface, Required: true})
		}
		for _, port := range uniquePorts(a.SSH) {
			out = append(out, AccessCheck{Name: "SSH from your address", Port: port, Protocol: "tcp", Family: family, Source: client.Unmap().String(), Interface: a.Interface, Required: true})
		}
	}
	for _, port := range uniquePorts(a.Ingress) {
		for _, family := range []string{"ipv4", "ipv6"} {
			name := fmt.Sprintf("Public ingress to Caddy on %d", port)
			switch port {
			case 80:
				name = "Public HTTP ingress through Caddy"
			case 443:
				name = "Public HTTPS ingress through Caddy"
			}
			out = append(out, AccessCheck{Name: name, Port: port, Protocol: "tcp", Family: family, Source: "Anywhere", Interface: a.Uplink, Required: true})
		}
	}
	return out
}

func uniquePorts(in []int) []int {
	var out []int
	for _, p := range in {
		if p < 1 || p > 65535 {
			continue
		}
		dup := false
		for _, q := range out {
			dup = dup || q == p
		}
		if !dup {
			out = append(out, p)
		}
	}
	return out
}

// evaluateAccess judges each check against a status: first match for an
// ordered list, zone semantics for firewalld, and the incoming default when
// nothing matches. A rule the evaluator cannot read before the decision
// makes the verdict unknown rather than a guess.
func evaluateAccess(st *FirewallStatus, checks []AccessCheck, profiles map[string][]string) []AccessCheck {
	out := make([]AccessCheck, len(checks))
	for i, c := range checks {
		out[i] = c
		switch {
		case !st.Available:
			out[i].Verdict, out[i].Reason = "unknown", "No firewall could be read."
		case !st.Enabled:
			out[i].Verdict, out[i].Reason = "unfiltered", string(st.Backend)+" is not enforcing, so nothing is refused."
		case st.Backend == BackendFirewalld:
			evaluateZoneAccess(st, &out[i], profiles)
		case st.Backend == BackendUFW || st.Backend == BackendNFTOwned:
			evaluateOrderedAccess(st, &out[i], profiles)
		default:
			out[i].Verdict, out[i].Reason = "unknown", "Raw iptables chains are not evaluated here."
		}
	}
	return out
}

// maxAccessForks bounds how many unreadable rules the ordered evaluation
// follows both ways before it settles for unknown.
const maxAccessForks = 4

func evaluateOrderedAccess(st *FirewallStatus, c *AccessCheck, profiles map[string][]string) {
	*c = orderedVerdict(st, st.Rules, *c, profiles, 0)
}

// orderedVerdict is first match over the rules. A rule the evaluator cannot
// read is followed both ways, selecting the connection and not, and when
// both reach the same decision that is the verdict: an allow that may or may
// not apply in front of a rule that admits anyway changes nothing.
func orderedVerdict(st *FirewallStatus, rules []Rule, c AccessCheck, profiles map[string][]string, forks int) AccessCheck {
	for i, r := range rules {
		match, why := ruleSelects(r, c, profiles)
		if match == selectNo {
			continue
		}
		if match == selectUnknown {
			if forks < maxAccessForks {
				taken := ruleVerdict(r, c)
				passed := orderedVerdict(st, rules[i+1:], c, profiles, forks+1)
				if (admits(taken.Verdict) && admits(passed.Verdict)) || (taken.Verdict == "refused" && passed.Verdict == "refused") {
					passed.Reason = fmt.Sprintf("Rule %d may or may not apply, and the verdict is the same either way: %s", r.Number, passed.Reason)
					return passed
				}
			}
			c.Verdict, c.Rule, c.RuleID = "unknown", r.Number, r.ID
			c.Reason = fmt.Sprintf("Rule %d is read first and %s.", r.Number, why)
			return c
		}
		return ruleVerdict(r, c)
	}
	defaultVerdict(&c, st.Policy.Incoming)
	if c.Family == "ipv6" && st.Effective.ipv6Unfiltered {
		c.Verdict, c.Reason = "unfiltered", "ufw is configured not to filter IPv6 (IPV6=no)."
	}
	return c
}

// ruleVerdict is a rule's decision for a connection it selects.
func ruleVerdict(r Rule, c AccessCheck) AccessCheck {
	c.Rule, c.RuleID = r.Number, r.ID
	switch verdictOf(r.Action) {
	case "allow":
		c.Verdict, c.Reason = "admitted", fmt.Sprintf("Rule %d admits it.", r.Number)
		if strings.EqualFold(r.Action, "LIMIT") {
			c.Verdict, c.Reason = "limited", fmt.Sprintf("Rule %d admits it at a limited rate.", r.Number)
		}
	case "deny":
		c.Verdict, c.Reason = "refused", fmt.Sprintf("Rule %d refuses it.", r.Number)
	default:
		c.Verdict, c.Reason = "unknown", fmt.Sprintf("Rule %d has an action this page cannot read.", r.Number)
	}
	return c
}

func defaultVerdict(c *AccessCheck, policy string) {
	switch strings.ToLower(policy) {
	case "allow", "accept":
		c.Verdict, c.Reason = "admitted", "No rule matches; the inbound default admits it."
	case "deny", "reject", "drop":
		c.Verdict, c.Reason = "refused", fmt.Sprintf("No rule matches, and the inbound default is %s.", strings.ToLower(policy))
	default:
		c.Verdict, c.Reason = "unknown", "No rule matches, and the inbound default could not be read."
	}
}

type selection int

const (
	selectNo selection = iota
	selectYes
	selectUnknown
)

// ruleSelects is whether an inbound rule selects a check's connection. The
// port and protocol are compared first: a rule about another port says
// nothing, whatever else about it is unknown.
func ruleSelects(r Rule, c AccessCheck, profiles map[string][]string) (selection, string) {
	if !strings.EqualFold(firstNonBlank(r.Direction, "IN"), "IN") {
		return selectNo, ""
	}
	if !r.BothFamilies && r.IPv6 != (c.Family == "ipv6") {
		return selectNo, ""
	}
	if r.Protocol != "" && r.Protocol != c.Protocol {
		return selectNo, ""
	}
	ports, ok := ruleDestinationPorts(r)
	if ok && !portsContain(ports, c.Port) {
		return selectNo, ""
	}
	if !ok {
		name := strings.TrimSpace(strings.TrimSuffix(r.To, "(v6)"))
		resolved, found := profiles[name]
		if !found {
			return selectUnknown, "names the profile " + name + ", whose ports are not known"
		}
		matched := false
		for _, spec := range resolved {
			port, proto, _ := strings.Cut(spec, "/")
			if proto != "" && proto != c.Protocol {
				continue
			}
			if set, ok := parsePortSpec(port); ok && portsContain(set, c.Port) {
				matched = true
			}
		}
		if !matched {
			return selectNo, ""
		}
	}
	if !isAnywhere(r.From) {
		if c.Source == "Anywhere" {
			// A rule naming a source does not decide for everyone.
			return selectNo, ""
		}
		p, err := parseSelector(r.From)
		if err != nil {
			return selectUnknown, "names a source this page cannot read"
		}
		addr, err := netip.ParseAddr(c.Source)
		if err != nil || !p.Contains(addr) {
			return selectNo, ""
		}
	}
	if dest := destinationAddress(r.To); !isAnywhere(dest) {
		return selectUnknown, "applies to the destination " + dest + ", which may or may not be the address used"
	}
	if r.Interface != "" {
		if c.Interface == "" {
			return selectUnknown, "applies on " + r.Interface + ", and the connection's device is not known"
		}
		if r.Interface != c.Interface {
			return selectNo, ""
		}
	}
	return selectYes, ""
}

// firewalldServicePorts are the predefined services whose ports the access
// checks need; anything else is resolved by firewalld only.
var firewalldServicePorts = func() map[string][]string {
	out := map[string][]string{
		"ssh": {"22/tcp"}, "http": {"80/tcp"}, "https": {"443/tcp"}, "http3": {"443/udp"},
		"dhcpv6-client": {"546/udp"}, "cockpit": {"9090/tcp"}, "mdns": {"5353/udp"},
	}
	for _, preset := range ServiceCatalogue {
		if preset.Firewalld != "" {
			out[preset.Firewalld] = append(out[preset.Firewalld], preset.Port+"/"+preset.Protocol)
		}
	}
	return out
}()

// evaluateZoneAccess applies firewalld's order within the zone the
// connection lands in: a source binding before an interface binding before
// the default zone, then rich-rule denials, then any allowance, then the
// zone's target.
func evaluateZoneAccess(st *FirewallStatus, c *AccessCheck, profiles map[string][]string) {
	zone := st.Zone
	if c.Source != "Anywhere" {
		if addr, err := netip.ParseAddr(c.Source); err == nil {
			for _, z := range st.Zones {
				for _, src := range z.Sources {
					if p, err := parseSelector(src); err == nil && p.Contains(addr) {
						zone = z.Name
					}
				}
			}
		}
	}
	if zone == st.Zone && c.Interface != "" {
		for _, z := range st.Zones {
			for _, iface := range z.Interfaces {
				if iface == c.Interface {
					zone = z.Name
				}
			}
		}
	}
	target, rules := "", st.Rules
	for _, z := range st.Zones {
		if z.Name == zone {
			target = z.Target
			if !z.Default {
				rules = z.Rules
			}
		}
	}
	if zone != st.Zone && target == "" {
		c.Verdict, c.Reason = "unknown", "The connection lands in zone "+zone+", whose rules could not be read."
		return
	}
	if target == "" {
		target = st.Default
	}
	merged := map[string][]string{}
	for k, v := range firewalldServicePorts {
		merged[k] = v
	}
	for k, v := range profiles {
		merged[k] = v
	}
	var allow *Rule
	for i := range rules {
		r := rules[i]
		probe := r
		if r.Service != "" && r.Port == "" {
			probe.To = r.Service
		}
		match, why := ruleSelects(probe, *c, merged)
		if match == selectUnknown {
			c.Verdict, c.Rule, c.RuleID, c.Reason = "unknown", r.Number, r.ID, fmt.Sprintf("Rule %d %s.", r.Number, why)
			return
		}
		if match != selectYes {
			continue
		}
		switch verdictOf(r.Action) {
		case "deny":
			c.Verdict, c.Rule, c.RuleID, c.Reason = "refused", r.Number, r.ID, fmt.Sprintf("Rule %d refuses it in zone %s.", r.Number, zone)
			return
		case "allow":
			if allow == nil {
				allow = &rules[i]
			}
		default:
			c.Verdict, c.Rule, c.RuleID, c.Reason = "unknown", r.Number, r.ID, fmt.Sprintf("Rule %d in zone %s has a form this page cannot evaluate.", r.Number, zone)
			return
		}
	}
	if allow != nil {
		c.Verdict, c.Rule, c.RuleID, c.Reason = "admitted", allow.Number, allow.ID, fmt.Sprintf("Rule %d admits it in zone %s.", allow.Number, zone)
		return
	}
	word := ""
	if fields := strings.Fields(target); len(fields) > 0 {
		word = fields[0]
	}
	defaultVerdict(c, firewalldPolicy(word).Incoming)
	c.Reason += " (zone " + zone + ")"
}

// EffectivePolicy is what each family and each interface is actually held
// to, beside the configured defaults.
type EffectivePolicy struct {
	Families   []FamilyPolicy    `json:"families"`
	Interfaces []InterfacePolicy `json:"interfaces"`
	// ipv6Unfiltered is ufw's IPV6=no.
	ipv6Unfiltered bool
}

// FamilyPolicy is one address family's defaults. Filtered is false where
// the firewall does not apply to the family at all.
type FamilyPolicy struct {
	Family   string `json:"family"`
	Filtered bool   `json:"filtered"`
	Incoming string `json:"incoming,omitempty"`
	Outgoing string `json:"outgoing,omitempty"`
	Routed   string `json:"routed,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// InterfacePolicy is the policy traffic arriving on one device meets.
type InterfacePolicy struct {
	Interface string `json:"interface"`
	Zone      string `json:"zone,omitempty"`
	Incoming  string `json:"incoming"`
	Rules     int    `json:"rules"`
	Reason    string `json:"reason,omitempty"`
}

// ufwDefaultsFile is ufw's defaults file; a variable so tests can point it
// at a fixture.
var ufwDefaultsFile = "/etc/default/ufw"

func effectivePolicy(ctx context.Context, b fwBackend, st *FirewallStatus) EffectivePolicy {
	e := EffectivePolicy{Families: []FamilyPolicy{}, Interfaces: []InterfacePolicy{}}
	switch st.Backend {
	case BackendUFW:
		v4 := FamilyPolicy{Family: "ipv4", Filtered: st.Enabled, Incoming: st.Policy.Incoming, Outgoing: st.Policy.Outgoing, Routed: st.Policy.Routed}
		v6 := v4
		v6.Family = "ipv6"
		if raw, err := os.ReadFile(ufwDefaultsFile); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "IPV6="); ok && strings.Trim(v, `"' `) == "no" {
					v6 = FamilyPolicy{Family: "ipv6", Reason: "ufw is configured with IPV6=no, so IPv6 traffic is not filtered by it."}
					e.ipv6Unfiltered = true
				}
			}
		} else {
			v6.Reason = "ufw's defaults file could not be read; IPv6 is assumed filtered like IPv4."
		}
		if !st.Enabled {
			v4.Reason, v6.Reason = "ufw is inactive; nothing is filtered.", firstNonBlank(v6.Reason, "ufw is inactive; nothing is filtered.")
			v6.Filtered = false
		}
		e.Families = append(e.Families, v4, v6)
		counts := map[string]int{}
		for _, r := range st.Rules {
			if r.Interface != "" {
				counts[r.Interface]++
			}
		}
		ifaces := make([]string, 0, len(counts))
		for iface := range counts {
			ifaces = append(ifaces, iface)
		}
		sort.Strings(ifaces)
		for _, iface := range ifaces {
			e.Interfaces = append(e.Interfaces, InterfacePolicy{Interface: iface, Incoming: st.Policy.Incoming, Rules: counts[iface],
				Reason: "Rules scoped to this device apply before the global default."})
		}
	case BackendFirewalld:
		for _, family := range []string{"ipv4", "ipv6"} {
			e.Families = append(e.Families, FamilyPolicy{Family: family, Filtered: st.Enabled, Incoming: st.Policy.Incoming, Outgoing: st.Policy.Outgoing, Routed: st.Policy.Routed,
				Reason: "firewalld applies each zone to both families."})
		}
		for _, z := range st.Zones {
			for _, iface := range z.Interfaces {
				rules := 0
				for _, r := range st.Rules {
					if r.Zone == z.Name {
						rules++
					}
				}
				e.Interfaces = append(e.Interfaces, InterfacePolicy{Interface: iface, Zone: z.Name, Incoming: firewalldPolicy(z.Target).Incoming, Rules: rules})
			}
		}
	case BackendIPTables:
		for _, f := range []struct{ family, tool string }{{"ipv4", "iptables"}, {"ipv6", "ip6tables"}} {
			p := FamilyPolicy{Family: f.family, Filtered: true}
			out, err := run(ctx, f.tool, "-S")
			if err != nil {
				p.Filtered, p.Reason = false, f.tool+" could not be read: "+err.Error()
			}
			for _, line := range strings.Split(out, "\n") {
				fields := strings.Fields(line)
				if len(fields) < 3 || fields[0] != "-P" {
					continue
				}
				switch fields[1] {
				case "INPUT":
					p.Incoming = iptablesPolicyWord(fields[2])
				case "OUTPUT":
					p.Outgoing = iptablesPolicyWord(fields[2])
				case "FORWARD":
					p.Routed = iptablesPolicyWord(fields[2])
				}
			}
			e.Families = append(e.Families, p)
		}
	case BackendNFTOwned:
		for _, family := range []string{"ipv4", "ipv6"} {
			e.Families = append(e.Families, FamilyPolicy{Family: family, Filtered: st.Enabled, Incoming: st.Policy.Incoming,
				Reason: "The dashboard's inet table filters both families; other tables can still refuse what it admits."})
		}
	}
	return e
}
