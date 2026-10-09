package netx

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Per-flow evaluation of the host's other nftables chains.
//
// The capability check judges each foreign chain on its own: a conditional
// drop it cannot read is uncertainty. That is safe and, on an ordinary Docker
// host, too strict — Docker's raw-table drops name container addresses before
// translation, and its nat chains only translate. Here each enabled entry's
// translated flow is modeled as exactly as the entry defines it (family,
// protocol, sources, arrival device, target and port, before and after this
// table's translation at dstnat-10, and the admission mark) and walked
// through every base chain at the hooks it crosses, following jumps. A rule is
// decided only when every match is a supported form; an unsupported match on
// a rule that could change the outcome leaves that layer unknown. The result
// is per flow and per layer, with the deciding rule's position.
//
// This is a model of supported rule forms, not a packet trace: provider
// filtering, kernel routing decisions and unsupported expressions stay
// unknown, and reachability remains unmeasured.

// gatewayNATPriority is where this table translates a forward's
// destination; chains earlier at prerouting see the visitor's original tuple.
const gatewayNATPriority = -110

// FlowLayer is one foreign chain's verdict for one modeled flow.
type FlowLayer struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Chain  string `json:"chain"`
	Hook   string `json:"hook"`
	// Verdict is clear (accepted, translated or passed to an accept
	// policy), blocked, or unknown.
	Verdict string `json:"verdict"`
	// Rule is the 1-based position of the deciding rule in Path's last
	// chain; zero when the chain's policy decided.
	Rule   int    `json:"rule"`
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
}

// EntryFlow is one entry's modeled flow across the checked layers.
type EntryFlow struct {
	Entry     string `json:"entry"`
	Name      string `json:"name"`
	Direction string `json:"direction"`
	// Verdict is blocked when a layer certainly drops it, unknown when a
	// layer cannot be decided, and clear when every checked layer passes it.
	Verdict string      `json:"verdict"`
	Layers  []FlowLayer `json:"layers"`
}

// modelFlow is one flow as an entry defines it.
type modelFlow struct {
	entry, name, direction string
	family                 string // ip or ip6
	protos                 []string
	saddr                  []netip.Prefix // nil: any source
	daddrPre, daddr        []netip.Prefix // before and after translation; nil: unknown
	// elsewhere are networks an unknown destination cannot be in: a flow
	// leaving through the uplink is not addressed to loopback or to a
	// network another local device is on.
	elsewhere       []netip.Prefix
	dportPre, dport [2]int
	iif, oif        string
	local           bool // the translated destination is this host
	translated      bool // destination translation applies
	markAt          struct {
		hook string
		prio int
	}
}

// modelGatewayFlows builds the flows of every enabled translating entry.
func modelGatewayFlows(sp *Spec, hostAddrs []hostAddr) []modelFlow {
	var out []modelFlow
	var local []netip.Prefix
	for _, a := range hostAddrs {
		if !a.Addr.IsLoopback() {
			local = append(local, netip.PrefixFrom(a.Addr, a.Addr.BitLen()))
		}
	}
	elsewhere := func(oif string) []netip.Prefix {
		out := append([]netip.Prefix{}, loopbackRanges...)
		for _, a := range hostAddrs {
			if a.Dev != oif && a.Dev != "lo" && a.Network.IsValid() && a.Network.Bits() > 0 {
				out = append(out, a.Network)
			}
		}
		return out
	}
	isLocal := func(a netip.Addr) bool {
		for _, l := range local {
			if l.Addr() == a {
				return true
			}
		}
		return false
	}
	for _, f := range sp.Forwards {
		if !f.Enabled {
			continue
		}
		target, err := ParseAddr(f.Target)
		if err != nil {
			continue
		}
		fam := "ip"
		if target.Is6() {
			fam = "ip6"
		}
		m := modelFlow{
			entry: "forward:" + strconv.Itoa(f.ID), name: f.Name, direction: "inbound", family: fam,
			protos: gwProtocols(f.Protocol), iif: f.Interface, translated: true, local: isLocal(target),
			daddr: []netip.Prefix{netip.PrefixFrom(target, target.BitLen())},
		}
		for _, l := range local {
			if l.Addr().Is4() == target.Is4() {
				m.daddrPre = append(m.daddrPre, l)
			}
		}
		lo, hi := portBounds(f.Ports)
		m.dportPre = [2]int{lo, hi}
		tp := f.TargetPort
		if tp == "" {
			tp = f.Ports
		}
		lo, hi = portBounds(tp)
		m.dport = [2]int{lo, hi}
		for _, s := range f.Sources {
			if p, err := ParsePrefix(s); err == nil {
				m.saddr = append(m.saddr, p.Masked())
			}
		}
		m.markAt.hook, m.markAt.prio = "prerouting", gatewayNATPriority
		out = append(out, m)
	}
	for _, n := range sp.NAT {
		if !n.Enabled {
			continue
		}
		src, err := ParsePrefix(n.Source)
		if err != nil {
			continue
		}
		fam := "ip"
		if src.Addr().Is6() {
			fam = "ip6"
		}
		m := modelFlow{entry: "nat:" + strconv.Itoa(n.ID), name: n.Name, direction: "outbound", family: fam,
			saddr: []netip.Prefix{src.Masked()}, oif: n.Interface, elsewhere: elsewhere(n.Interface)}
		for _, d := range n.Destinations {
			if p, err := ParsePrefix(d); err == nil {
				m.daddr = append(m.daddr, p.Masked())
			}
		}
		m.daddrPre = m.daddr
		m.markAt.hook, m.markAt.prio = "forward", -10
		out = append(out, m)
		if natMapped(n) {
			to, err := ParsePrefix(n.Translated)
			if err != nil {
				continue
			}
			in := modelFlow{entry: "nat-in:" + strconv.Itoa(n.ID), name: n.Name, direction: "inbound", family: fam,
				iif: n.Interface, translated: true, daddrPre: []netip.Prefix{to.Masked()}, daddr: []netip.Prefix{src.Masked()}}
			in.markAt.hook, in.markAt.prio = "prerouting", gatewayNATPriority
			out = append(out, in)
		}
	}
	return out
}

// hooksOf is the hooks a flow crosses after it arrives.
func (m modelFlow) hooks() []string {
	if m.direction == "inbound" && m.local {
		return []string{"prerouting", "input"}
	}
	return []string{"prerouting", "forward", "postrouting"}
}

// stage is the flow as one chain sees it.
type flowStage struct {
	m        modelFlow
	pre      bool // before this table's destination translation
	marked   bool
	chainNAT bool
}

// evaluator walks one flow through one base chain and what it jumps to.
type flowEvaluator struct {
	rules  map[string][][]map[string]json.RawMessage
	budget int
}

type flowOutcome struct {
	verdict string // clear, blocked or unknown
	rule    int
	path    string
	reason  string
	// restrictedBy is set when the flow passes only for some sources.
	restrictedBy string
}

const flowBudget = 4096

// walk evaluates a chain from rule from; cont is what happens when it falls
// off its end or returns: the caller's remaining rules for a jump, the base
// chain's policy at the top.
func (e *flowEvaluator) walk(family, table, name string, st flowStage, cont func() flowOutcome, depth, from int) flowOutcome {
	rules := e.rules[family+"/"+table+"/"+name]
	for i := from; i < len(rules); i++ {
		e.budget--
		if e.budget < 0 {
			return flowOutcome{verdict: "unknown", rule: i + 1, path: name, reason: "The chains are too large to model within the evaluation bound."}
		}
		match, sourceOnly, why := matchRule(rules[i], st)
		if match == triFalse {
			continue
		}
		act, target, actWhy := ruleAction(rules[i])
		if act == "none" {
			continue
		}
		next := i + 1
		rest := func() flowOutcome { return e.walk(family, table, name, st, cont, depth, next) }
		var taken flowOutcome
		switch act {
		case "accept", "translate":
			taken = flowOutcome{verdict: "clear", rule: i + 1, path: name, reason: actWhy}
		case "drop":
			taken = flowOutcome{verdict: "blocked", rule: i + 1, path: name, reason: actWhy}
		case "return":
			taken = cont()
		case "jump", "goto":
			if depth >= 8 {
				taken = flowOutcome{verdict: "unknown", rule: i + 1, path: name, reason: "Jumps nest deeper than the model follows."}
				break
			}
			after := rest
			if act == "goto" {
				after = cont
			}
			taken = e.walk(family, table, target, st, after, depth+1, 0)
			if taken.path == target || strings.HasPrefix(taken.path, target+" → ") {
				taken.path = name + " → " + taken.path
			}
		default:
			taken = flowOutcome{verdict: "unknown", rule: i + 1, path: name, reason: actWhy}
		}
		if match == triTrue {
			return taken
		}
		// The rule may or may not match: both futures must agree.
		other := rest()
		switch {
		case taken.verdict == other.verdict && taken.verdict != "unknown":
			if other.restrictedBy == "" {
				other.restrictedBy = taken.restrictedBy
			}
			return other
		case sourceOnly && taken.verdict != "unknown" && other.verdict != "unknown":
			// One branch blocked, the other clear, and only the visitor's
			// address decides which: a source restriction, not a blocker.
			out := taken
			if out.verdict != "clear" {
				out = other
			}
			out.restrictedBy = fmt.Sprintf("%s rule %d", name, i+1)
			return out
		case taken.verdict == "unknown":
			return taken
		case other.verdict == "unknown":
			return other
		}
		return flowOutcome{verdict: "unknown", rule: i + 1, path: name, reason: why}
	}
	return cont()
}

func policyOutcome(policy, chain string) flowOutcome {
	switch policy {
	case "accept", "":
		return flowOutcome{verdict: "clear", path: chain, reason: "No rule of a supported form drops it, and the chain's policy accepts."}
	case "drop":
		return flowOutcome{verdict: "blocked", path: chain, reason: "No rule accepts it before the chain's drop policy."}
	}
	return flowOutcome{verdict: "unknown", path: chain, reason: "The chain's policy could not be read."}
}

// evaluateGatewayFlows judges every modeled flow against the checked base
// chains of the listing. Owned chains (this table and the admission chains)
// are reported by the admission evidence instead.
func evaluateGatewayFlows(listing *nftListing, flows []modelFlow) []EntryFlow {
	if listing == nil {
		return nil
	}
	rules := map[string][][]map[string]json.RawMessage{}
	for _, o := range listing.Nftables {
		if r := o.Rule; r != nil {
			key := r.Family + "/" + r.Table + "/" + r.Chain
			rules[key] = append(rules[key], r.Expr)
		}
	}
	out := make([]EntryFlow, 0, len(flows))
	for _, m := range flows {
		ef := EntryFlow{Entry: m.entry, Name: m.name, Direction: m.direction, Verdict: "clear", Layers: []FlowLayer{}}
		hooks := map[string]bool{}
		for _, h := range m.hooks() {
			hooks[h] = true
		}
		for _, o := range listing.Nftables {
			ch := o.Chain
			if ch == nil || !hooks[ch.Hook] || !familyApplies(ch.Family, m.family) || ownedLayer(ch.Family, ch.Table, ch.Name) {
				continue
			}
			prio := chainPriority(ch.Prio)
			st := flowStage{m: m, chainNAT: ch.Type == "nat"}
			st.pre = m.translated && ch.Hook == "prerouting" && prio < gatewayNATPriority
			st.marked = markVisible(m, ch.Hook, prio)
			ev := &flowEvaluator{rules: rules, budget: flowBudget}
			policy, name := ch.Policy, ch.Name
			res := ev.walk(ch.Family, ch.Table, ch.Name, st, func() flowOutcome { return policyOutcome(policy, name) }, 0, 0)
			layer := FlowLayer{Family: ch.Family, Table: ch.Table, Chain: ch.Name, Hook: ch.Hook, Verdict: res.verdict, Rule: res.rule, Path: res.path, Reason: res.reason}
			if res.verdict == "clear" && res.restrictedBy != "" {
				layer.Verdict = "restricted"
				layer.Reason = "Some sources are dropped (" + res.restrictedBy + "); the rest pass."
			}
			if layer.Path == ch.Name {
				layer.Path = ""
			}
			ef.Layers = append(ef.Layers, layer)
			switch {
			case res.verdict == "blocked":
				ef.Verdict = "blocked"
			case res.verdict == "unknown" && ef.Verdict != "blocked":
				ef.Verdict = "unknown"
			}
		}
		out = append(out, ef)
	}
	return out
}

func familyApplies(chainFamily, flowFamily string) bool {
	return chainFamily == flowFamily || chainFamily == "inet"
}

// ownedLayer is this table, or a filter chain the owned admission rule heads.
func ownedLayer(family, table, chain string) bool {
	if family == "inet" && table == gatewayTable {
		return true
	}
	return (family == "ip" || family == "ip6") && table == "filter" && (chain == "FORWARD" || chain == "INPUT")
}

func chainPriority(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	return 0
}

// markVisible reports whether the admission mark is set when this chain
// runs: a forward's at its translation, a NAT entry's in this table's
// forward chain at priority -10.
func markVisible(m modelFlow, hook string, prio int) bool {
	order := map[string]int{"prerouting": 0, "input": 1, "forward": 1, "postrouting": 2}
	if order[hook] != order[m.markAt.hook] {
		return order[hook] > order[m.markAt.hook]
	}
	return prio > m.markAt.prio
}

type tri int

const (
	triFalse tri = iota
	triTrue
	triUnknown
)

func and(a, b tri) tri {
	switch {
	case a == triFalse || b == triFalse:
		return triFalse
	case a == triUnknown || b == triUnknown:
		return triUnknown
	}
	return triTrue
}

func not(t tri) tri {
	switch t {
	case triTrue:
		return triFalse
	case triFalse:
		return triTrue
	}
	return triUnknown
}

// matchRule decides a rule's matches for a stage of a flow. sourceOnly
// reports that every undecided match concerns the visitor's address.
func matchRule(expr []map[string]json.RawMessage, st flowStage) (tri, bool, string) {
	result, why, sourceOnly := triTrue, "", true
	for i, e := range expr {
		for kind, raw := range e {
			var t tri
			source := false
			switch kind {
			case "match":
				t = matchExpr(raw, st)
				source = isSourceMatch(raw)
			case "xt":
				var x struct{ Type, Name string }
				if json.Unmarshal(raw, &x) == nil && x.Type == "target" {
					continue
				}
				t = triUnknown
			case "limit", "quota", "meter", "set", "numgen", "osf", "socket":
				t = triUnknown
			default:
				continue
			}
			if t == triUnknown {
				sourceOnly = sourceOnly && source
				if why == "" {
					why = fmt.Sprintf("Expression %d (%s) of this rule cannot be decided for the flow.", i+1, describeExpr(kind, raw))
				}
			}
			result = and(result, t)
			if result == triFalse {
				return triFalse, false, ""
			}
		}
	}
	return result, sourceOnly && result == triUnknown, why
}

// isSourceMatch is a match on the source address.
func isSourceMatch(raw json.RawMessage) bool {
	var m struct {
		Left struct {
			Payload *struct{ Protocol, Field string } `json:"payload"`
		} `json:"left"`
	}
	return json.Unmarshal(raw, &m) == nil && m.Left.Payload != nil && m.Left.Payload.Field == "saddr"
}

// ruleAction is what a rule does when it matches.
func ruleAction(expr []map[string]json.RawMessage) (act, target, why string) {
	act = "none"
	for _, e := range expr {
		for kind, raw := range e {
			switch kind {
			case "accept":
				return "accept", "", "An accept of a supported form passes it."
			case "drop":
				return "drop", "", "An explicit drop of a supported form matches it."
			case "reject":
				return "drop", "", "An explicit reject of a supported form matches it."
			case "return":
				return "return", "", ""
			case "jump", "goto":
				var j struct{ Target string }
				if json.Unmarshal(raw, &j) != nil {
					return "unknown", "", "A jump whose target could not be read."
				}
				return kind, j.Target, ""
			case "dnat", "snat", "masquerade", "redirect":
				return "translate", "", "A translation rule; it changes addresses, it does not drop."
			case "queue":
				return "unknown", "", "A queue hands the packet to a user-space program whose verdict is not modeled."
			case "xt":
				var x struct{ Type, Name string }
				if json.Unmarshal(raw, &x) != nil || x.Type != "target" {
					continue
				}
				switch x.Name {
				case "ACCEPT":
					return "accept", "", "An iptables ACCEPT passes it."
				case "DROP", "REJECT":
					return "drop", "", "An iptables " + x.Name + " target matches it."
				case "RETURN":
					return "return", "", ""
				case "DNAT", "SNAT", "MASQUERADE", "REDIRECT", "NETMAP":
					return "translate", "", "An iptables " + x.Name + " target translates; it does not drop."
				case "LOG", "NFLOG", "MARK", "CONNMARK", "TCPMSS", "CT", "NOTRACK", "TRACE", "CLASSIFY", "DSCP", "TOS", "SET":
					continue
				}
				return "unknown", "", "The iptables target " + x.Name + " is not modeled."
			}
		}
	}
	return act, "", ""
}

func describeExpr(kind string, raw json.RawMessage) string {
	if kind == "xt" {
		var x struct{ Type, Name string }
		_ = json.Unmarshal(raw, &x)
		return "iptables " + x.Type + " " + x.Name
	}
	if kind != "match" {
		return kind
	}
	var m struct {
		Left map[string]json.RawMessage `json:"left"`
	}
	_ = json.Unmarshal(raw, &m)
	for k, v := range m.Left {
		s := strings.Trim(string(v), "{}")
		if len(s) > 60 {
			s = s[:60]
		}
		return k + " " + s
	}
	return "match"
}

// matchExpr decides one match expression.
func matchExpr(raw json.RawMessage, st flowStage) tri {
	var m struct {
		Op    string                     `json:"op"`
		Left  map[string]json.RawMessage `json:"left"`
		Right json.RawMessage            `json:"right"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return triUnknown
	}
	t := matchLeft(m.Left, m.Right, st)
	switch m.Op {
	case "==", "in", "":
		return t
	case "!=":
		return not(t)
	}
	return triUnknown
}

func matchLeft(left map[string]json.RawMessage, right json.RawMessage, st flowStage) tri {
	if raw, ok := left["payload"]; ok {
		var p struct{ Protocol, Field string }
		if json.Unmarshal(raw, &p) != nil {
			return triUnknown
		}
		switch {
		case (p.Protocol == "ip" || p.Protocol == "ip6") && (p.Field == "saddr" || p.Field == "daddr"):
			if p.Protocol != st.m.family {
				return triFalse
			}
			if p.Field == "saddr" {
				return addressesIn(st.m.saddr, right)
			}
			have := st.m.daddr
			if st.pre {
				have = st.m.daddrPre
			}
			if len(have) == 0 && len(st.m.elsewhere) > 0 {
				return outsideAll(st.m.elsewhere, right)
			}
			return addressesIn(have, right)
		case (p.Protocol == "tcp" || p.Protocol == "udp" || p.Protocol == "th") && p.Field == "dport":
			if p.Protocol != "th" {
				if t := protoIn(st.m.protos, []string{p.Protocol}); t != triTrue {
					return t
				}
			}
			if st.pre {
				return portsIn(st.m.dportPre, right)
			}
			return portsIn(st.m.dport, right)
		}
		return triUnknown
	}
	if raw, ok := left["meta"]; ok {
		var k struct{ Key string }
		if json.Unmarshal(raw, &k) != nil {
			return triUnknown
		}
		switch k.Key {
		case "l4proto":
			var names []string
			if !stringsOf(right, &names) {
				return triUnknown
			}
			return protoIn(st.m.protos, names)
		case "nfproto":
			var names []string
			if !stringsOf(right, &names) {
				return triUnknown
			}
			want := "ipv4"
			if st.m.family == "ip6" {
				want = "ipv6"
			}
			return boolTri(contains(names, want))
		case "iifname":
			return nameIn(st.m.iif, right)
		case "oifname":
			return nameIn(st.m.oif, right)
		}
		return triUnknown
	}
	if raw, ok := left["ct"]; ok {
		var k struct{ Key string }
		if json.Unmarshal(raw, &k) != nil {
			return triUnknown
		}
		var names []string
		switch k.Key {
		case "state":
			if !stringsOf(right, &names) {
				return triUnknown
			}
			return boolTri(contains(names, "new"))
		case "status":
			if !stringsOf(right, &names) {
				return triUnknown
			}
			if len(names) == 1 && names[0] == "dnat" {
				if st.pre {
					return triFalse
				}
				return boolTri(st.m.translated)
			}
			return triUnknown
		}
		return triUnknown
	}
	if raw, ok := left["fib"]; ok {
		var f struct {
			Result string   `json:"result"`
			Flags  []string `json:"flags"`
		}
		var want string
		if json.Unmarshal(raw, &f) != nil || f.Result != "type" || !contains(f.Flags, "daddr") || json.Unmarshal(right, &want) != nil || want != "local" {
			return triUnknown
		}
		switch {
		case st.m.direction == "outbound":
			return triFalse
		case st.pre && strings.HasPrefix(st.m.entry, "forward:"):
			return triTrue
		case st.pre:
			return triUnknown
		}
		return boolTri(st.m.local)
	}
	if raw, ok := left["&"]; ok {
		// The admission mark, masked: ct mark & 0xff000000 == 0x4a000000.
		var parts []json.RawMessage
		var ct struct {
			CT struct{ Key string } `json:"ct"`
		}
		var mask, want uint64
		if json.Unmarshal(raw, &parts) == nil && len(parts) == 2 && json.Unmarshal(parts[0], &ct) == nil && ct.CT.Key == "mark" &&
			json.Unmarshal(parts[1], &mask) == nil && json.Unmarshal(right, &want) == nil {
			if !st.marked {
				return triUnknown
			}
			return boolTri(0x4a000000&mask == want)
		}
		return triUnknown
	}
	return triUnknown
}

func boolTri(b bool) tri {
	if b {
		return triTrue
	}
	return triFalse
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// stringsOf reads a string, a list of strings or an anonymous set of them.
func stringsOf(raw json.RawMessage, out *[]string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		*out = []string{one}
		return true
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		*out = list
		return true
	}
	var set struct {
		Set []string `json:"set"`
	}
	if json.Unmarshal(raw, &set) == nil && set.Set != nil {
		*out = set.Set
		return true
	}
	return false
}

// protoIn decides whether the flow's protocols all are, or none are, in a set.
func protoIn(have, want []string) tri {
	if len(have) == 0 {
		return triUnknown
	}
	in := 0
	for _, h := range have {
		if contains(want, h) || contains(want, map[string]string{"tcp": "6", "udp": "17"}[h]) {
			in++
		}
	}
	switch in {
	case 0:
		return triFalse
	case len(have):
		return triTrue
	}
	return triUnknown
}

// nameIn decides an interface-name match; an unknown device cannot be.
func nameIn(have string, right json.RawMessage) tri {
	if have == "" {
		return triUnknown
	}
	var names []string
	if !stringsOf(right, &names) {
		return triUnknown
	}
	for _, n := range names {
		if strings.HasSuffix(n, "*") {
			if strings.HasPrefix(have, strings.TrimSuffix(n, "*")) {
				return triTrue
			}
			continue
		}
		if n == have {
			return triTrue
		}
	}
	return triFalse
}

// prefixesOf reads an address, a prefix, a range or an anonymous set of them.
// A named set (@name) is not read: its elements are another table's.
func prefixesOf(raw json.RawMessage) ([]netip.Prefix, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.HasPrefix(s, "@") {
			return nil, false
		}
		p, err := ParsePrefix(s)
		return []netip.Prefix{p.Masked()}, err == nil
	}
	var pfx struct {
		Prefix *struct {
			Addr string `json:"addr"`
			Len  int    `json:"len"`
		} `json:"prefix"`
		Set   []json.RawMessage `json:"set"`
		Range []string          `json:"range"`
	}
	if json.Unmarshal(raw, &pfx) != nil {
		return nil, false
	}
	switch {
	case pfx.Prefix != nil:
		p, err := ParsePrefix(fmt.Sprintf("%s/%d", pfx.Prefix.Addr, pfx.Prefix.Len))
		return []netip.Prefix{p.Masked()}, err == nil
	case pfx.Set != nil:
		var out []netip.Prefix
		for _, item := range pfx.Set {
			ps, ok := prefixesOf(item)
			if !ok {
				return nil, false
			}
			out = append(out, ps...)
		}
		return out, true
	case len(pfx.Range) == 2:
		lo, e1 := ParseAddr(pfx.Range[0])
		hi, e2 := ParseAddr(pfx.Range[1])
		if e1 != nil || e2 != nil {
			return nil, false
		}
		return rangePrefixes(lo, hi), true
	}
	return nil, false
}

// rangePrefixes covers [lo, hi] with prefixes.
func rangePrefixes(lo, hi netip.Addr) []netip.Prefix {
	var out []netip.Prefix
	for lo.IsValid() && lo.Compare(hi) <= 0 && len(out) < 256 {
		bits := lo.BitLen()
		for bits > 0 {
			p := netip.PrefixFrom(lo, bits-1).Masked()
			if p.Addr() != lo || prefixLast(p).Compare(hi) > 0 {
				break
			}
			bits--
		}
		p := netip.PrefixFrom(lo, bits)
		out = append(out, p)
		next := prefixLast(p).Next()
		if !next.IsValid() {
			break
		}
		lo = next
	}
	return out
}

// addressesIn decides whether every address of the flow is inside a match's
// networks (true), none is (false), or some are (unknown). An unknown or
// unrestricted side cannot be decided against a restriction.
func addressesIn(have []netip.Prefix, right json.RawMessage) tri {
	want, ok := prefixesOf(right)
	if !ok || len(have) == 0 {
		return triUnknown
	}
	all, any := true, false
	for _, h := range have {
		inside, touches := false, false
		for _, w := range want {
			if w.Bits() <= h.Bits() && w.Contains(h.Addr()) {
				inside = true
			}
			if w.Overlaps(h) {
				touches = true
			}
		}
		all = all && inside
		any = any || touches
	}
	switch {
	case all:
		return triTrue
	case !any:
		return triFalse
	}
	return triUnknown
}

// outsideAll decides a destination match for a flow whose destination is
// only known not to be in the given networks: a match entirely inside them
// cannot apply.
func outsideAll(elsewhere []netip.Prefix, right json.RawMessage) tri {
	want, ok := prefixesOf(right)
	if !ok || len(want) == 0 {
		return triUnknown
	}
	for _, w := range want {
		inside := false
		for _, e := range elsewhere {
			if e.Bits() <= w.Bits() && e.Contains(w.Addr()) {
				inside = true
			}
		}
		if !inside {
			return triUnknown
		}
	}
	return triFalse
}

// portsIn decides a port match against the flow's port span.
func portsIn(have [2]int, right json.RawMessage) tri {
	if have[0] == 0 {
		return triUnknown
	}
	var ranges [][2]int
	var one int
	var set struct {
		Set []json.RawMessage `json:"set"`
	}
	var r struct {
		Range []int `json:"range"`
	}
	switch {
	case json.Unmarshal(right, &one) == nil:
		ranges = append(ranges, [2]int{one, one})
	case json.Unmarshal(right, &r) == nil && len(r.Range) == 2:
		ranges = append(ranges, [2]int{r.Range[0], r.Range[1]})
	case json.Unmarshal(right, &set) == nil && set.Set != nil:
		for _, item := range set.Set {
			if json.Unmarshal(item, &one) == nil {
				ranges = append(ranges, [2]int{one, one})
				continue
			}
			var ir struct {
				Range []int `json:"range"`
			}
			if json.Unmarshal(item, &ir) == nil && len(ir.Range) == 2 {
				ranges = append(ranges, [2]int{ir.Range[0], ir.Range[1]})
				continue
			}
			return triUnknown
		}
	default:
		return triUnknown
	}
	covered, touched := false, false
	for _, w := range ranges {
		if w[0] <= have[0] && have[1] <= w[1] {
			covered = true
		}
		if w[0] <= have[1] && have[0] <= w[1] {
			touched = true
		}
	}
	switch {
	case covered:
		return triTrue
	case !touched:
		return triFalse
	}
	return triUnknown
}
