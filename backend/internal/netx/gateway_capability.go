package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// gatewaySysRoot and gatewayClassNet are where the gateway reads kernel
// state it does not need a command for. Variables so tests point them at a
// temporary directory.
var (
	gatewaySysRoot  = "/proc/sys"
	gatewayClassNet = "/sys/class/net"
)

// Capability says whether the gateway can be written on this host, and if not
// why not, in a sentence the page shows where the controls would be. It is
// declared per firewall the way netsec's FirewallCapabilities is: a missing
// control that explains itself is information, and one that is merely absent
// is a bug report.
type Capability struct {
	Writable bool   `json:"writable"`
	Reason   string `json:"reason,omitempty"`
	// Firewall is what filters this host's traffic: ufw, firewalld, iptables,
	// nftables (a ruleset of the operator's own) or none.
	Firewall string `json:"firewall"`
	// Docker is whether Docker's chains are present, which is why admission
	// reaches DOCKER-USER.
	Docker bool `json:"docker"`
	// Blocker names the chain that makes the gateway read-only, with the rule
	// that would admit the dashboard's translated connections there.
	Blocker *CapabilityBlocker `json:"blocker,omitempty"`
	// Writable describes compatibility for a mutation, never reachability.
	Reachability  string        `json:"reachability"`
	Layers        []PolicyLayer `json:"layers"`
	UnknownLayers []string      `json:"unknownLayers"`

	// missing is nft itself being absent; mutations answer it as an install
	// hand-off rather than a read-only host.
	missing bool
	// listing is the ruleset read, kept for per-flow evaluation.
	listing *nftListing
}

// PolicyLayer records the coverage of a local base chain. Unknown rules are
// retained as uncertainty rather than inferred to permit translated traffic.
type PolicyLayer struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Chain  string `json:"chain"`
	Hook   string `json:"hook"`
	Policy string `json:"policy"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Type is the chain's type (filter, nat, route): a nat chain only
	// translates unless it holds an explicit drop or reject.
	Type string `json:"type,omitempty"`
	// Rules counts the chain's own rules; Uncertain names, by position, the
	// forms the generic check could not read (at most a handful).
	Rules     int      `json:"rules"`
	Uncertain []string `json:"uncertain,omitempty"`
}

// CapabilityBlocker is a base chain at the forward hook that drops by default.
type CapabilityBlocker struct {
	Family string `json:"family"`
	Table  string `json:"table"`
	Chain  string `json:"chain"`
	// Rule is what to add to that chain so connections the gateway
	// translated are accepted by it.
	Rule string `json:"rule"`
}

// admitMarkRule is the accept an operator adds to a foreign forward chain.
const admitMarkRule = "ct mark and " + connMask + " == " + connMark + " accept"

// iptablesCompat are the tables iptables-nft creates. Their forward chain is
// named FORWARD, and it is the one the admission rule is inserted into; a
// chain by any other name in one of these tables is somebody's own nftables
// ruleset that happens to be called filter.
var iptablesCompat = map[string]bool{"filter": true, "nat": true, "raw": true, "mangle": true, "security": true}

// nftListing is `nft -t -j list ruleset` (terse: no set elements, which on a
// host with a blocklist loaded are most of the output) read for the two kinds of object that
// matter here. Everything else it prints is left unparsed.
type nftListing struct {
	Nftables []struct {
		Table *struct {
			Family string `json:"family"`
			Name   string `json:"name"`
		} `json:"table"`
		Chain *struct {
			Family string          `json:"family"`
			Table  string          `json:"table"`
			Name   string          `json:"name"`
			Type   string          `json:"type"`
			Hook   string          `json:"hook"`
			Prio   json.RawMessage `json:"prio"`
			Policy string          `json:"policy"`
		} `json:"chain"`
		Rule *struct {
			Family string                       `json:"family"`
			Table  string                       `json:"table"`
			Chain  string                       `json:"chain"`
			Expr   []map[string]json.RawMessage `json:"expr"`
		} `json:"rule"`
	} `json:"nftables"`
}

// GatewayCapability reads what is filtering this host and says whether the
// gateway's translations can be admitted through it.
//
// The gateway's table never accepts: an accept in one nftables table cannot
// override a drop in another, so a forward is let through by the connection
// mark the iptables chains accept (§4 of the plan). That works wherever the
// host's filtering is iptables — ufw, Docker, plain iptables — and nowhere the
// filtering is a table of its own that drops forwarded traffic, because the
// accept would have to be written into that table.
func (s *Service) GatewayCapability(ctx context.Context) Capability {
	c := Capability{Firewall: "none", Reachability: "unknown", Layers: []PolicyLayer{},
		UnknownLayers: []string{"provider and upstream policy", "measured end-to-end connectivity"}}
	out, err := run(ctx, "nft", "-t", "-j", "list", "ruleset")
	if err != nil {
		var missing *UnavailableError
		if errors.As(err, &missing) {
			c.Reason = "nftables is not installed on this host; the gateway needs the nft command."
			c.missing = true
			return c
		}
		c.Reason = fmt.Sprintf("the host's nftables ruleset could not be read (%v), so whether the gateway can be admitted is not known.", err)
		return c
	}
	var listing nftListing
	if err := json.Unmarshal([]byte(out), &listing); err != nil || listing.Nftables == nil {
		c.Reason = "nft printed its ruleset in a form this dashboard could not read, so whether the gateway can be admitted is not known."
		return c
	}
	c.listing = &listing

	firewalld := false
	if state, ferr := run(ctx, "firewall-cmd", "--state"); ferr == nil && strings.TrimSpace(state) == "running" {
		firewalld = true
	}
	var blocker *CapabilityBlocker
	hasIPTables := false
	rules := map[string][][]map[string]json.RawMessage{}
	for _, o := range listing.Nftables {
		if r := o.Rule; r != nil {
			key := r.Family + "/" + r.Table + "/" + r.Chain
			rules[key] = append(rules[key], r.Expr)
		}
	}
	for _, o := range listing.Nftables {
		if o.Table != nil && o.Table.Family == "inet" && o.Table.Name == "firewalld" {
			firewalld = true
		}
		ch := o.Chain
		if ch == nil {
			continue
		}
		if (ch.Family == "ip" || ch.Family == "ip6") && iptablesCompat[ch.Table] {
			hasIPTables = true
		}
		if ch.Hook != "forward" && ch.Hook != "input" && ch.Hook != "prerouting" && ch.Hook != "postrouting" {
			continue
		}
		key := ch.Family + "/" + ch.Table + "/" + ch.Name
		layer := PolicyLayer{Family: ch.Family, Table: ch.Table, Chain: ch.Name, Hook: ch.Hook, Policy: ch.Policy, Status: "checked", Type: ch.Type, Rules: len(rules[key])}
		if ch.Family == "inet" && ch.Table == gatewayTable {
			layer.Status = "owned"
			c.Layers = append(c.Layers, layer)
			continue
		}
		// The owned firewall admits translated connections by their mark
		// before any of its own rules or its policy.
		if ch.Family == "inet" && ch.Table == firewallTable {
			layer.Status = "owned"
			layer.Reason = "The dashboard's firewall table admits translated connections by their connection mark before its own rules."
			c.Layers = append(c.Layers, layer)
			continue
		}
		// Admission is inserted only into the filter table. A security or
		// mangle chain called FORWARD remains an independent policy layer.
		if (ch.Family == "ip" || ch.Family == "ip6") && ch.Table == "filter" && (ch.Name == "FORWARD" || ch.Name == "INPUT") {
			layer.Status = "owned"
			layer.Reason = "Translated connections require the owned admission rule at the start of this chain."
			c.Layers = append(c.Layers, layer)
			continue
		}
		if ch.Type == "nat" {
			layer.Status, layer.Reason = assessNATChain(rules, key, 0)
		} else {
			layer.Status, layer.Reason = assessPolicyRules(ch.Policy, rules[key], ch.Hook != "prerouting")
		}
		layer.Uncertain = uncertainForms(rules[key])
		c.Layers = append(c.Layers, layer)
		if blocker == nil && (layer.Status == "blocked" || layer.Status == "unknown") {
			blocker = &CapabilityBlocker{Family: ch.Family, Table: ch.Table, Chain: ch.Name, Rule: admitMarkRule}
		}
	}

	c.Docker = dockerPresent(ctx)
	switch {
	case firewalld:
		c.Firewall = "firewalld"
		c.Reason = "firewalld filters forwarded traffic in its own nftables table, and an accept in another table cannot override its drop, so this dashboard cannot let a port forward or NAT through it. Open the forward in a firewalld zone instead (the Firewall page lists them)."
		return c
	case blocker != nil:
		c.Firewall = "nftables"
		c.Blocker = blocker
		c.Reason = fmt.Sprintf("The %s table %q can drop translated traffic in its chain %q, and an accept in the gateway's table cannot override that. Review the checked policy layers and add this rule before blocking rules to admit the gateway's translated connections: %s",
			blocker.Family, blocker.Table, blocker.Chain, blocker.Rule)
		return c
	}
	c.Writable = true
	switch {
	case ufwActive(ctx):
		c.Firewall = "ufw"
	case hasIPTables:
		c.Firewall = "iptables"
	}
	return c
}

// assessPolicyRules supports unconditional verdicts and the exact masked
// connmark exemption we document. Selectors, jumps and maps need a packet
// trace or probe; treating them as unconditional accepts would hide blockers.
func assessPolicyRules(policy string, rules [][]map[string]json.RawMessage, markAvailable bool) (string, string) {
	for _, expr := range rules {
		verdict, markOnly, unconditional := "", true, true
		for _, e := range expr {
			for kind, raw := range e {
				switch kind {
				case "counter", "comment":
				case "accept", "drop", "reject", "return":
					verdict = kind
				case "match":
					unconditional = false
					if !markAvailable || !isAdmissionMarkMatch(raw) {
						markOnly = false
					}
				default:
					return "unknown", "A jump, map or unsupported expression needs bounded trace or probe evidence; admission is not proven."
				}
			}
		}
		if (verdict == "accept" || (verdict == "return" && policy == "accept")) && (unconditional || markOnly) {
			return "admitted", "Supported rule forms permit connections carrying the dashboard's translation mark through this chain."
		}
		if verdict == "drop" || verdict == "reject" {
			if unconditional {
				return "blocked", "An explicit unconditional drop or reject blocks translated traffic."
			}
			return "unknown", "An explicit conditional drop or reject can match translated traffic; a flow-specific trace or probe is required."
		}
		if verdict == "return" && policy == "drop" {
			return "blocked", "A return applies this base chain's drop policy."
		}
		if !unconditional {
			return "unknown", "A conditional rule could change admission; a flow-specific trace or probe is required."
		}
	}
	if policy == "drop" {
		return "blocked", "The chain drops forwarded traffic by default."
	}
	if policy != "accept" {
		return "unknown", "The base-chain policy could not be determined."
	}
	return "checked", "No blocking verdict exists in the supported rule forms of this local chain."
}

// assessNATChain judges a nat chain, following its jumps: translation
// statements and returns do not drop, so such a chain is checked unless it
// (or a chain it reaches) holds an explicit drop or reject.
func assessNATChain(rules map[string][][]map[string]json.RawMessage, key string, depth int) (string, string) {
	if depth > 8 {
		return "unknown", "Jumps from this nat chain nest deeper than the check follows."
	}
	family, rest, _ := strings.Cut(key, "/")
	table, _, _ := strings.Cut(rest, "/")
	for _, expr := range rules[key] {
		act, target, _ := ruleAction(expr)
		switch act {
		case "drop":
			return "unknown", "This nat chain holds an explicit drop or reject; a flow-specific evaluation decides whether it applies."
		case "unknown":
			return "unknown", "This nat chain holds a statement whose effect is not modeled."
		case "jump", "goto":
			if status, reason := assessNATChain(rules, family+"/"+table+"/"+target, depth+1); status != "checked" {
				return status, reason
			}
		}
	}
	return "checked", "A nat chain that only translates and returns; it cannot drop translated traffic."
}

// uncertainForms names the rules whose forms the generic check cannot read.
func uncertainForms(rules [][]map[string]json.RawMessage) []string {
	var out []string
	for i, expr := range rules {
		for _, e := range expr {
			for kind, raw := range e {
				switch kind {
				case "match", "counter", "comment", "accept", "drop", "reject", "return", "log", "mangle":
					continue
				case "xt":
					var x struct{ Type, Name string }
					_ = json.Unmarshal(raw, &x)
					out = append(out, fmt.Sprintf("rule %d: iptables %s %s", i+1, x.Type, x.Name))
				default:
					out = append(out, fmt.Sprintf("rule %d: %s", i+1, kind))
				}
			}
		}
		if len(out) >= 6 {
			return append(out[:6], fmt.Sprintf("and later rules of %d", len(rules)))
		}
	}
	return out
}

func isAdmissionMarkMatch(raw json.RawMessage) bool {
	var m struct {
		Op    string          `json:"op"`
		Left  json.RawMessage `json:"left"`
		Right uint64          `json:"right"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Op != "==" || m.Right != 0x4a000000 {
		return false
	}
	var left struct {
		And []json.RawMessage `json:"&"`
	}
	if json.Unmarshal(m.Left, &left) != nil || len(left.And) != 2 {
		return false
	}
	var ct struct {
		CT struct {
			Key string `json:"key"`
		} `json:"ct"`
	}
	var mask uint64
	return json.Unmarshal(left.And[0], &ct) == nil && ct.CT.Key == "mark" && json.Unmarshal(left.And[1], &mask) == nil && mask == 0xff000000
}

// ufwActive reports whether ufw is managing the host's filtering.
func ufwActive(ctx context.Context) bool {
	out, err := run(ctx, "ufw", "status")
	return err == nil && strings.Contains(out, "Status: active")
}

// dockerPresent reports whether Docker's chains exist: DOCKER-USER is where
// admission goes, and docker0 is the fallback for a host whose iptables
// cannot be listed.
func dockerPresent(ctx context.Context) bool {
	if _, err := run(ctx, "iptables", "-S", "DOCKER-USER"); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(gatewayClassNet, "docker0"))
	return err == nil
}

// requireWritable is the guard in front of every gateway mutation that adds
// or changes a translation. With the spec about to be applied, a foreign
// layer the generic check could not clear is judged per translated flow: the
// change goes ahead only when every enabled flow is certainly admitted or
// passed (or restricted only by source) at every checked layer.
func (s *Service) requireWritable(ctx context.Context, specs ...*Spec) (Capability, error) {
	c := s.GatewayCapability(ctx)
	switch {
	case c.Writable:
		return c, nil
	case c.missing:
		return c, &UnavailableError{Tool: "nft", Package: "nftables"}
	case c.Firewall == "firewalld" || c.listing == nil || len(specs) == 0 || specs[0] == nil:
		return c, &ReadOnlyError{Reason: c.Reason}
	}
	flows := evaluateGatewayFlows(c.listing, modelGatewayFlows(specs[0], hostAddresses(ctx)))
	if len(flows) == 0 {
		return c, &ReadOnlyError{Reason: c.Reason}
	}
	for _, f := range flows {
		if f.Verdict == "clear" {
			continue
		}
		for _, l := range f.Layers {
			if l.Verdict != "blocked" && l.Verdict != "unknown" {
				continue
			}
			at := "its policy"
			if l.Rule > 0 {
				at = fmt.Sprintf("rule %d", l.Rule)
			}
			if l.Path != "" {
				at += " of " + l.Path
			}
			word := "can drop"
			if l.Verdict == "blocked" {
				word = "drops"
			}
			return c, &ReadOnlyError{Reason: fmt.Sprintf("The %s table %q %s %q's translated connections in its chain %q (%s): %s Add this rule before it to admit the gateway's translated connections: %s",
				l.Family, l.Table, word, f.Name, l.Chain, at, l.Reason, admitMarkRule)}
		}
	}
	c.Writable = true
	c.Reason = "Every enabled translated flow passes the checked layers in their supported rule forms."
	return c, nil
}

// hostAddr is one address this host holds and the network it is on.
type hostAddr struct {
	Addr    netip.Addr
	Network netip.Prefix
	Dev     string
}

// hostAddresses are every address this host holds, for modeling where a
// visitor's packet is addressed before translation and which networks a
// flow leaving through the uplink cannot be addressed to.
func hostAddresses(ctx context.Context) []hostAddr {
	raw, err := run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		return nil
	}
	var addrs []ipAddr
	if json.Unmarshal([]byte(raw), &addrs) != nil {
		return nil
	}
	var out []hostAddr
	for _, a := range addrs {
		for _, info := range a.AddrInfo {
			if addr, err := netip.ParseAddr(info.Local); err == nil {
				out = append(out, hostAddr{Addr: addr, Network: netip.PrefixFrom(addr, info.PrefixLen).Masked(), Dev: a.IfName})
			}
		}
	}
	return out
}
