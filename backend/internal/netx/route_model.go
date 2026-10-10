package netx

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// The routing model answers "which rule and which route would the kernel
// choose" from the inventory alone. Previews need it for changes the kernel
// has not seen yet, and the decision diagram needs it to name the rule behind
// the table the kernel reports. It is deliberately conservative: a selector
// it cannot evaluate for the packet in question makes the answer unknown
// rather than a guess, and wherever the kernel can be asked about the
// present, callers prefer and compare its answer.

// RouteTuple is the packet a decision is modeled for. An empty IIF is a
// packet this host sends; an unset UID or TOS is unknown, so a rule that
// selects on it cannot be decided.
type RouteTuple struct {
	Target string  `json:"target"`
	Source string  `json:"source,omitempty"`
	IIF    string  `json:"iif,omitempty"`
	OIF    string  `json:"oif,omitempty"`
	Mark   string  `json:"mark,omitempty"`
	UID    *uint32 `json:"uid,omitempty"`
	TOS    string  `json:"tos,omitempty"`
}

// routeTuple is a RouteTuple parsed once.
type routeTuple struct {
	family   string
	dst, src netip.Addr
	iif, oif string
	mark     uint32
	uid      *uint32
	tos      uint8
	tosKnown bool
}

func (t RouteTuple) parse() (routeTuple, error) {
	dst, err := ParseAddr(t.Target)
	if err != nil {
		return routeTuple{}, err
	}
	out := routeTuple{family: familyOf(dst), dst: dst, iif: "lo", uid: t.UID}
	if t.Source != "" {
		if out.src, err = ParseAddr(t.Source); err != nil {
			return routeTuple{}, err
		}
		if familyOf(out.src) != out.family {
			return routeTuple{}, fmt.Errorf("the source and target must use the same address family")
		}
	}
	for _, f := range []struct {
		raw string
		dst *string
	}{{t.IIF, &out.iif}, {t.OIF, &out.oif}} {
		if name := strings.TrimSpace(f.raw); name != "" {
			if err := ValidIfName(name); err != nil {
				return routeTuple{}, err
			}
			*f.dst = name
		}
	}
	if t.Mark != "" {
		mark, err := strconv.ParseUint(strings.TrimSpace(t.Mark), 0, 32)
		if err != nil {
			return routeTuple{}, fmt.Errorf("%q is not a packet mark", t.Mark)
		}
		out.mark = uint32(mark)
	}
	if t.TOS != "" {
		v, ok := dsfieldValue(t.TOS)
		if !ok {
			return routeTuple{}, fmt.Errorf("%q is not a TOS value", t.TOS)
		}
		out.tos, out.tosKnown = v, true
	}
	return out, nil
}

// RouteDecision is a modeled answer. Status is route, local, discard,
// unreachable (no table answered) or unknown.
type RouteDecision struct {
	Status       string         `json:"status"`
	RulePriority *int           `json:"rulePriority,omitempty"`
	Table        int            `json:"table,omitempty"`
	TableName    string         `json:"tableName,omitempty"`
	Route        *RouteEntry    `json:"route,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Steps        []DecisionStep `json:"steps"`
	// Alternatives are the answers either way of an undecidable selector,
	// present only when they differ.
	Alternatives []RouteAlternative `json:"alternatives,omitempty"`
}

// RouteAlternative is one branch of an undecidable rule.
type RouteAlternative struct {
	Assumption string        `json:"assumption"`
	Decision   RouteDecision `json:"decision"`
}

// DecisionStep is what one rule did with the packet: no_match, unknown,
// undecided (either way leads to the same answer), no_route, suppressed,
// throw, jumped, passed or decided.
type DecisionStep struct {
	Priority int    `json:"priority"`
	Result   string `json:"result"`
	Detail   string `json:"detail,omitempty"`
}

// modelRouting is the inventory the model reads: the rules of one family in
// priority order and every table, the local one included.
type modelRouting struct {
	rules  []RuleEntry
	tables map[int]RoutingTable
	// vrf is whether any VRF device exists; without one an l3mdev rule
	// cannot match anything.
	vrf bool
}

func newModel(view *RoutingView, family string, vrf bool) modelRouting {
	m := modelRouting{tables: map[int]RoutingTable{}, vrf: vrf}
	for _, t := range view.Tables {
		m.tables[t.ID] = t
	}
	m.tables[tableLocal] = RoutingTable{ID: tableLocal, Name: "local", Routes: view.local}
	for _, r := range view.Rules {
		if r.Family == family {
			m.rules = append(m.rules, r)
		}
	}
	slices.SortStableFunc(m.rules, func(a, b RuleEntry) int { return a.Priority - b.Priority })
	return m
}

// withRule is the model with a candidate rule inserted at its priority, after
// any existing rule of the same priority, which is where the kernel puts it.
func (m modelRouting) withRule(r RuleEntry) modelRouting {
	out := m
	out.rules = append([]RuleEntry(nil), m.rules...)
	i := 0
	for i < len(out.rules) && out.rules[i].Priority <= r.Priority {
		i++
	}
	out.rules = slices.Insert(out.rules, i, r)
	return out
}

// withRoute is the model with a candidate route added to its table.
func (m modelRouting) withRoute(table int, name string, e RouteEntry) modelRouting {
	out := m
	out.tables = make(map[int]RoutingTable, len(m.tables)+1)
	for id, t := range m.tables {
		out.tables[id] = t
	}
	t := out.tables[table]
	t.ID = table
	if t.Name == "" {
		t.Name = name
	}
	t.Routes = append(append([]RouteEntry(nil), t.Routes...), e)
	out.tables[table] = t
	return out
}

// withoutRoute is the model with every route matching drop removed.
func (m modelRouting) withoutRoute(table int, drop func(RouteEntry) bool) modelRouting {
	out := m
	out.tables = make(map[int]RoutingTable, len(m.tables))
	for id, t := range m.tables {
		out.tables[id] = t
	}
	t := out.tables[table]
	var kept []RouteEntry
	for _, r := range t.Routes {
		if !drop(r) {
			kept = append(kept, r)
		}
	}
	t.Routes = kept
	out.tables[table] = t
	return out
}

// maxModelForks bounds how many undecidable selectors one decision explores.
const maxModelForks = 3

// decide walks the rules in priority order as fib_rules_lookup does. A rule
// whose selectors cannot be decided for the packet forks the walk: when both
// continuations reach the same answer the selector does not matter and the
// answer stands; otherwise the decision is unknown and carries both.
func (m modelRouting) decide(t routeTuple) RouteDecision {
	return m.walk(t, 0, -1, []DecisionStep{}, 0, map[int]bool{})
}

func (m modelRouting) walk(t routeTuple, start, jump int, steps []DecisionStep, forks int, forced map[int]bool) RouteDecision {
	d := RouteDecision{Steps: append([]DecisionStep{}, steps...)}
	for i := start; i < len(m.rules); i++ {
		r := m.rules[i]
		if jump >= 0 && r.Priority < jump {
			continue
		}
		jump = -1
		match, why := ruleMatches(r, t, m.vrf)
		if pinned, ok := forced[i]; ok && match == matchUnknown {
			match = matchNo
			if pinned {
				match = matchYes
			}
		}
		switch match {
		case matchNo:
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "no_match"})
			continue
		case matchUnknown:
			if forks >= maxModelForks {
				d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "unknown", Detail: why})
				d.Status, d.Reason = "unknown", fmt.Sprintf("Rule %d %s.", r.Priority, why)
				return d
			}
			yes := m.walk(t, i, -1, d.Steps, forks+1, pin(forced, i, true))
			no := m.walk(t, i, -1, d.Steps, forks+1, pin(forced, i, false))
			if yes.Status != "unknown" && no.Status != "unknown" && sameDecision(yes, no) {
				out := no
				out.Steps = append(append([]DecisionStep{}, d.Steps...), DecisionStep{Priority: r.Priority, Result: "undecided", Detail: why + "; selected or not, the answer is the same"})
				out.Steps = append(out.Steps, no.Steps[len(d.Steps)+1:]...)
				if yes.RulePriority == nil || no.RulePriority == nil || *yes.RulePriority != *no.RulePriority {
					out.RulePriority = nil
					out.Reason = fmt.Sprintf("The answer is the same either way, but whether rule %d decides it depends on what it %s.", r.Priority, strings.TrimPrefix(why, "selects "))
				}
				return out
			}
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "unknown", Detail: why})
			d.Status, d.Reason = "unknown", fmt.Sprintf("Rule %d %s, and the answer depends on it.", r.Priority, why)
			d.Alternatives = []RouteAlternative{
				{Assumption: fmt.Sprintf("if rule %d selects it", r.Priority), Decision: yes},
				{Assumption: fmt.Sprintf("if rule %d does not", r.Priority), Decision: no},
			}
			return d
		}
		switch r.Action {
		case "goto":
			if r.Unresolved || !slices.ContainsFunc(m.rules, func(x RuleEntry) bool { return x.Priority == r.Goto }) {
				d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "passed", Detail: "its goto target holds no rule"})
				continue
			}
			jump = r.Goto
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "jumped", Detail: fmt.Sprintf("continues at %d", r.Goto)})
			continue
		case "nop":
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "passed"})
			continue
		case "blackhole", "unreachable", "prohibit":
			p := r.Priority
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "decided", Detail: r.Action})
			d.Status, d.RulePriority, d.Reason = "discard", &p, fmt.Sprintf("Rule %d discards it (%s).", r.Priority, r.Action)
			return d
		case "lookup":
		default:
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "unknown", Detail: "its action " + r.Action + " is not modeled"})
			d.Status, d.Reason = "unknown", fmt.Sprintf("Rule %d has an action the model does not evaluate.", r.Priority)
			return d
		}
		if r.L3MDev {
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "unknown", Detail: "it looks up a VRF device's table"})
			d.Status, d.Reason = "unknown", fmt.Sprintf("Rule %d looks the packet up in its VRF device's table, which the model does not resolve.", r.Priority)
			return d
		}
		table := m.tables[r.Table]
		route, found := longestMatch(table.Routes, t.family, t.dst)
		if !found {
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "no_route", Detail: fmt.Sprintf("table %s has no route for it", tableLabel(r.Table, r.TableName))})
			continue
		}
		if r.SuppressPrefixLength != nil && prefixBits(route.Destination) <= *r.SuppressPrefixLength {
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "suppressed", Detail: fmt.Sprintf("its /%d answer is suppressed", prefixBits(route.Destination))})
			continue
		}
		if route.Type == "throw" {
			d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "throw", Detail: "a throw route returns it to the rules"})
			continue
		}
		p := r.Priority
		d.RulePriority, d.Table, d.TableName, d.Route = &p, r.Table, firstNonEmpty(table.Name, r.TableName), &route
		d.Steps = append(d.Steps, DecisionStep{Priority: r.Priority, Result: "decided", Detail: "table " + tableLabel(r.Table, d.TableName)})
		switch route.Type {
		case "blackhole", "unreachable", "prohibit":
			d.Status, d.Reason = "discard", fmt.Sprintf("Table %s discards it (%s %s).", tableLabel(r.Table, d.TableName), route.Type, route.Destination)
		case "local", "broadcast", "multicast", "anycast":
			d.Status = "local"
		default:
			d.Status = "route"
		}
		return d
	}
	d.Status, d.Reason = "unreachable", "No rule's table had a route for it."
	return d
}

func pin(forced map[int]bool, i int, match bool) map[int]bool {
	out := make(map[int]bool, len(forced)+1)
	for k, v := range forced {
		out[k] = v
	}
	out[i] = match
	return out
}

// A rule's match is the gateway flow model's three-valued tri
// (gateway_flows.go), named here for the routing decision.
const (
	matchNo      = triFalse
	matchYes     = triTrue
	matchUnknown = triUnknown
)

// ruleMatches evaluates one rule's selectors as fib_rule_match and the
// per-family matchers do.
func ruleMatches(r RuleEntry, t routeTuple, vrf bool) (tri, string) {
	result, why := selectorsMatch(r, t, vrf)
	if r.Not && result != matchUnknown {
		if result == matchYes {
			return matchNo, ""
		}
		return matchYes, ""
	}
	return result, why
}

func selectorsMatch(r RuleEntry, t routeTuple, vrf bool) (tri, string) {
	if r.IIF != "" && r.IIF != t.iif {
		return matchNo, ""
	}
	if r.OIF != "" && r.OIF != t.oif {
		return matchNo, ""
	}
	if r.FWMark != "" {
		val, rawMask, masked := strings.Cut(r.FWMark, "/")
		mark, err := strconv.ParseUint(val, 0, 32)
		mask := uint64(0xffffffff)
		if err == nil && masked {
			mask, err = strconv.ParseUint(rawMask, 0, 32)
		}
		if err != nil {
			return matchUnknown, "has a mark the model cannot read"
		}
		if (mark^uint64(t.mark))&mask != 0 {
			return matchNo, ""
		}
	}
	if r.To != "" {
		p, err := ParsePrefix(r.To)
		if err != nil {
			return matchUnknown, "has a destination the model cannot read"
		}
		if !p.Masked().Contains(t.dst) {
			return matchNo, ""
		}
	}
	unknown := ""
	if r.From != "" {
		p, err := ParsePrefix(r.From)
		switch {
		case err != nil:
			return matchUnknown, "has a source the model cannot read"
		case !t.src.IsValid():
			unknown = "selects a source address and the packet's source is not known"
		case !p.Masked().Contains(t.src):
			return matchNo, ""
		}
	}
	if r.UIDRange != "" {
		lo, hi, _ := strings.Cut(r.UIDRange, "-")
		start, err1 := strconv.ParseUint(lo, 10, 32)
		end, err2 := strconv.ParseUint(hi, 10, 32)
		switch {
		case err1 != nil || err2 != nil:
			return matchUnknown, "has a UID range the model cannot read"
		case t.uid == nil:
			unknown = firstNonEmpty(unknown, "selects a socket owner and the packet's UID is not known")
		case uint64(*t.uid) < start || uint64(*t.uid) > end:
			return matchNo, ""
		}
	}
	if r.TOS != "" {
		v, ok := dsfieldValue(r.TOS)
		mask := uint8(0xfc)
		if r.Family != "inet6" {
			mask = 0x1c
		}
		switch {
		case !ok:
			return matchUnknown, "has a TOS value the model cannot read"
		case !t.tosKnown:
			unknown = firstNonEmpty(unknown, "selects a TOS value and the packet's is not known")
		case t.tos&mask != v:
			return matchNo, ""
		}
	}
	if r.IPProto != "" || r.SPort != "" || r.DPort != "" {
		unknown = firstNonEmpty(unknown, "selects a protocol or port, which this packet does not name")
	}
	if r.L3MDev {
		if !vrf {
			return matchNo, ""
		}
		unknown = firstNonEmpty(unknown, "selects traffic using a VRF device")
	}
	if unknown != "" {
		return matchUnknown, unknown
	}
	return matchYes, ""
}

// longestMatch is a table lookup: the most specific route of the family
// covering the address, the lowest metric between equals.
func longestMatch(routes []RouteEntry, family string, dst netip.Addr) (RouteEntry, bool) {
	best, bits, found := RouteEntry{}, -1, false
	for _, r := range routes {
		if r.Family != family {
			continue
		}
		p, ok := routePrefix(r)
		if !ok || !p.Contains(dst) {
			continue
		}
		if p.Bits() > bits || (p.Bits() == bits && r.Metric < best.Metric) {
			best, bits, found = r, p.Bits(), true
		}
	}
	return best, found
}

// routePrefix is a route's destination as a prefix, default included.
func routePrefix(r RouteEntry) (netip.Prefix, bool) {
	if r.Destination == "default" {
		if r.Family == "inet6" {
			return netip.MustParsePrefix("::/0"), true
		}
		return netip.MustParsePrefix("0.0.0.0/0"), true
	}
	p, err := ParsePrefix(r.Destination)
	if err != nil {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}

func prefixBits(dest string) int {
	if dest == "default" {
		return 0
	}
	if p, err := ParsePrefix(dest); err == nil {
		return p.Bits()
	}
	return -1
}

func tableLabel(id int, name string) string {
	if name != "" {
		return name
	}
	return strconv.Itoa(id)
}

// routeUses reports whether the kernel's chosen device is the route's, or one
// of a multipath route's legs; the kernel answers with the leg it hashed to.
func routeUses(r *RouteEntry, device string) bool {
	if r == nil {
		return false
	}
	if len(r.Nexthops) == 0 {
		return r.Device == "" || r.Device == device
	}
	for _, n := range r.Nexthops {
		if n.Device == device {
			return true
		}
	}
	return false
}

// describeDecision is a decision in the words the guards use.
func describeDecision(d RouteDecision) string {
	switch d.Status {
	case "route":
		via := d.Route.Device
		if d.Route.Gateway != "" {
			via += " via " + d.Route.Gateway
		}
		if len(d.Route.Nexthops) > 0 {
			via = fmt.Sprintf("%d nexthops", len(d.Route.Nexthops))
		}
		return fmt.Sprintf("%s (%s, table %s)", via, d.Route.Destination, tableLabel(d.Table, d.TableName))
	case "local":
		return "this host itself"
	case "discard":
		return "discarded"
	case "unreachable":
		return "no route"
	}
	return "unknown"
}
