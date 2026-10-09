package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The owned firewall is one nftables table the dashboard makes and nothing
// else: inet jd_firewall, filtering input for both families. It is offered
// where no ufw or firewalld runs, so a host with plain nftables or iptables
// can still keep rules that survive a reboot — the boot unit loads the same
// rendered file — without the dashboard taking over any other table. Other
// tables keep enforcing what they enforce; an accept here cannot override a
// drop there.
const (
	firewallTable = "jd_firewall"
	firewallFile  = "firewall.nft"
	// firewallApplyFile is where a candidate is written for nft to read.
	firewallApplyFile = ".firewall.apply.nft"
)

// FirewallSpec is the owned table. Incoming is the input chain's policy,
// accept or drop.
type FirewallSpec struct {
	Enabled  bool               `json:"enabled"`
	Incoming string             `json:"incoming"`
	Rules    []FirewallRuleSpec `json:"rules"`
}

// FirewallRuleSpec is one ordered rule: who, to which ports, on which device,
// and the verdict.
type FirewallRuleSpec struct {
	ID        int    `json:"id"`
	Action    string `json:"action"`
	Protocol  string `json:"protocol,omitempty"`
	Ports     string `json:"ports,omitempty"`
	Source    string `json:"source,omitempty"`
	Interface string `json:"interface,omitempty"`
	Comment   string `json:"comment,omitempty"`
	Made
}

// OwnedFirewallView is the owned table as saved and as the kernel holds it.
type OwnedFirewallView struct {
	Available bool               `json:"available"`
	Enabled   bool               `json:"enabled"`
	Incoming  string             `json:"incoming"`
	Rules     []FirewallRuleSpec `json:"rules"`
	// Runtime is present, absent or unreadable: whether the kernel holds the
	// table the spec says it should.
	Runtime string `json:"runtime"`
	// Foreign names the other nftables tables, which keep enforcing.
	Foreign []string `json:"foreign"`
	Raw     string   `json:"raw,omitempty"`
}

// FirewallRuleRequest is a rule to add to the owned table.
type FirewallRuleRequest struct {
	Action    string `json:"action"`
	Protocol  string `json:"protocol,omitempty"`
	Ports     string `json:"ports,omitempty"`
	Source    string `json:"source,omitempty"`
	Interface string `json:"interface,omitempty"`
	Comment   string `json:"comment,omitempty"`
	// Position inserts at a 1-based place in the order; zero appends.
	Position int `json:"position,omitempty"`
}

func (req FirewallRuleRequest) spec() (FirewallRuleSpec, error) {
	r := FirewallRuleSpec{}
	switch a := strings.ToLower(strings.TrimSpace(req.Action)); a {
	case "allow", "accept":
		r.Action = "accept"
	case "deny", "drop":
		r.Action = "drop"
	case "reject":
		r.Action = "reject"
	default:
		return r, fmt.Errorf("an owned firewall rule allows, denies or rejects")
	}
	switch p := strings.ToLower(strings.TrimSpace(req.Protocol)); p {
	case "", "tcp", "udp":
		r.Protocol = p
	default:
		return r, fmt.Errorf("the protocol is tcp or udp")
	}
	if ports := strings.TrimSpace(req.Ports); ports != "" {
		set, err := ownedPorts(ports)
		if err != nil {
			return r, err
		}
		r.Ports = set
	}
	if src := strings.TrimSpace(req.Source); src != "" && !strings.EqualFold(src, "any") {
		p, err := ParsePrefix(src)
		if err != nil {
			return r, err
		}
		r.Source = p.Masked().String()
	}
	if iface := strings.TrimSpace(req.Interface); iface != "" {
		if err := ValidIfName(iface); err != nil {
			return r, err
		}
		r.Interface = iface
	}
	if r.Ports == "" && r.Source == "" && r.Interface == "" {
		return r, fmt.Errorf("a rule needs a port, a source address or an interface")
	}
	if c := strings.TrimSpace(req.Comment); c != "" {
		label, err := CleanLabel(c, 64)
		if err != nil {
			return r, err
		}
		r.Comment = label
	}
	return r, nil
}

// ownedPorts canonicalises a port, a range (8000:8010 or 8000-8010) or a
// comma-separated list into nft's spelling.
func ownedPorts(raw string) (string, error) {
	var parts []string
	for _, part := range strings.Split(raw, ",") {
		p, err := ParsePorts(strings.TrimSpace(part))
		if err != nil {
			return "", err
		}
		parts = append(parts, p)
	}
	if len(parts) > 32 {
		return "", fmt.Errorf("a rule names at most 32 ports or ranges")
	}
	return strings.Join(parts, ","), nil
}

// hasOwnedFirewall is whether the spec has ever held the owned table, which
// is when its file belongs to the rendered set.
func hasOwnedFirewall(sp *Spec) bool { return sp.Firewall != nil }

func ownedFirewallOn(sp *Spec) bool { return sp.Firewall != nil && sp.Firewall.Enabled }

// renderFirewall writes the owned table. Disabled, the file only removes
// it: declaring first makes the delete succeed whether or not it exists.
// Enabled, the chain admits, before any rule: established and related
// replies, loopback, ICMP and ICMPv6 (neighbour discovery and path MTU
// depend on them), DHCP client replies, the operator's trusted addresses
// and the gateway's translated connections by their mark. Then the rules in
// order, then the policy.
func renderFirewall(sp *Spec, trusted []netip.Prefix) (string, error) {
	var b strings.Builder
	b.WriteString(generatedHeader)
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\n", firewallTable, firewallTable)
	if !ownedFirewallOn(sp) {
		return b.String(), nil
	}
	policy := sp.Firewall.Incoming
	if policy != "drop" {
		policy = "accept"
	}
	v4, v6 := splitFamilies(mergePrefixes(trusted))
	fmt.Fprintf(&b, "table inet %s {\n", firewallTable)
	if len(v4) > 0 {
		fmt.Fprintf(&b, "\tset operator4 {\n\t\ttype ipv4_addr\n\t\tflags interval\n\t\telements = { %s }\n\t}\n", prefixList(v4))
	}
	if len(v6) > 0 {
		fmt.Fprintf(&b, "\tset operator6 {\n\t\ttype ipv6_addr\n\t\tflags interval\n\t\telements = { %s }\n\t}\n", prefixList(v6))
	}
	fmt.Fprintf(&b, "\tchain input {\n\t\ttype filter hook input priority filter + 10; policy %s;\n", policy)
	b.WriteString("\t\tct state established,related accept comment \"jd-fw-established\"\n")
	b.WriteString("\t\tiifname \"lo\" accept comment \"jd-fw-loopback\"\n")
	b.WriteString("\t\tmeta l4proto { icmp, ipv6-icmp } accept comment \"jd-fw-icmp\"\n")
	b.WriteString("\t\tudp sport 67 udp dport 68 accept comment \"jd-fw-dhcp\"\n")
	b.WriteString("\t\tudp dport 546 accept comment \"jd-fw-dhcp6\"\n")
	fmt.Fprintf(&b, "\t\tct mark and %s == %s accept comment \"jd-fw-gateway\"\n", connMask, connMark)
	if len(v4) > 0 {
		b.WriteString("\t\tip saddr @operator4 accept comment \"jd-fw-operator\"\n")
	}
	if len(v6) > 0 {
		b.WriteString("\t\tip6 saddr @operator6 accept comment \"jd-fw-operator\"\n")
	}
	for _, r := range sp.Firewall.Rules {
		line, err := firewallRuleLine(r)
		if err != nil {
			return "", fmt.Errorf("owned firewall rule %d: %w", r.ID, err)
		}
		b.WriteString("\t\t" + line + "\n")
	}
	b.WriteString("\t}\n}\n")
	return b.String(), nil
}

// firewallRuleLine renders one rule from its parsed parts, never from text a
// client sent: every value is parsed again here.
func firewallRuleLine(r FirewallRuleSpec) (string, error) {
	var parts []string
	if r.Interface != "" {
		if err := ValidIfName(r.Interface); err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("iifname %q", r.Interface))
	}
	if r.Source != "" {
		p, err := ParsePrefix(r.Source)
		if err != nil {
			return "", err
		}
		key := "ip saddr"
		if p.Addr().Is6() {
			key = "ip6 saddr"
		}
		parts = append(parts, key+" "+prefixList([]netip.Prefix{p.Masked()}))
	}
	if r.Ports != "" {
		set, err := ownedPorts(r.Ports)
		if err != nil {
			return "", err
		}
		elements := "{ " + strings.ReplaceAll(set, ",", ", ") + " }"
		switch r.Protocol {
		case "tcp", "udp":
			parts = append(parts, r.Protocol+" dport "+elements)
		case "":
			parts = append(parts, "meta l4proto { tcp, udp } th dport "+elements)
		default:
			return "", fmt.Errorf("%q is not a protocol", r.Protocol)
		}
	} else if r.Protocol != "" {
		if r.Protocol != "tcp" && r.Protocol != "udp" {
			return "", fmt.Errorf("%q is not a protocol", r.Protocol)
		}
		parts = append(parts, "meta l4proto "+r.Protocol)
	}
	switch r.Action {
	case "accept", "drop", "reject":
	default:
		return "", fmt.Errorf("%q is not a verdict", r.Action)
	}
	parts = append(parts, "counter", r.Action, fmt.Sprintf("comment \"jd-fw-%d\"", r.ID))
	return strings.Join(parts, " "), nil
}

// OwnedFirewallAvailable is whether nft is there to hold the table.
func (s *Service) OwnedFirewallAvailable() bool { return has("nft") }

// OwnedFirewall reads the saved table and whether the kernel holds it.
func (s *Service) OwnedFirewall(ctx context.Context) (*OwnedFirewallView, error) {
	sp, err := s.loadSpec()
	if err != nil {
		return nil, err
	}
	v := &OwnedFirewallView{Available: has("nft"), Incoming: "accept", Rules: []FirewallRuleSpec{}, Foreign: []string{}, Runtime: "absent"}
	if sp.Firewall != nil {
		v.Enabled, v.Rules = sp.Firewall.Enabled, append(v.Rules, sp.Firewall.Rules...)
		if sp.Firewall.Incoming == "drop" {
			v.Incoming = "drop"
		}
	}
	if !v.Available {
		v.Runtime = "unreadable"
		return v, nil
	}
	out, err := run(ctx, "nft", "-j", "list", "tables")
	if err != nil {
		v.Runtime = "unreadable"
		return v, nil
	}
	var listing struct {
		Nftables []struct {
			Table *struct {
				Family string `json:"family"`
				Name   string `json:"name"`
			} `json:"table"`
		} `json:"nftables"`
	}
	if json.Unmarshal([]byte(out), &listing) != nil {
		v.Runtime = "unreadable"
		return v, nil
	}
	for _, o := range listing.Nftables {
		if o.Table == nil {
			continue
		}
		if o.Table.Family == "inet" && o.Table.Name == firewallTable {
			v.Runtime = "present"
			continue
		}
		v.Foreign = append(v.Foreign, o.Table.Family+" "+o.Table.Name)
	}
	if v.Runtime == "present" {
		if raw, err := run(ctx, "nft", "list", "table", "inet", firewallTable); err == nil {
			v.Raw = raw
		}
	}
	return v, nil
}

// OwnedFirewallChange is one edit of the owned table. Op is add, delete,
// enable, disable, policy or reset.
type OwnedFirewallChange struct {
	Op      string               `json:"op"`
	Rule    *FirewallRuleRequest `json:"rule,omitempty"`
	ID      int                  `json:"id,omitempty"`
	Enabled bool                 `json:"enabled,omitempty"`
	Policy  string               `json:"policy,omitempty"`
}

// ChangeOwnedFirewall applies one edit through the ordinary commit: the
// candidate file is checked, loaded and verified before the spec and boot
// unit are written, the journal can restore the previous table without the
// backend, and a pending apply waits for reconnection like any covered
// change. Access guards are the firewall layer's, which calls this.
func (s *Service) ChangeOwnedFirewall(ctx context.Context, change OwnedFirewallChange, actor string) error {
	if !has("nft") {
		return &UnavailableError{Tool: "nft", Package: "nftables"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := sp.clone()
	if next.Firewall == nil {
		next.Firewall = &FirewallSpec{Incoming: "accept", Rules: []FirewallRuleSpec{}}
	}
	fw := next.Firewall
	switch change.Op {
	case "add":
		if change.Rule == nil {
			return fmt.Errorf("an add names a rule")
		}
		r, err := change.Rule.spec()
		if err != nil {
			return err
		}
		r.ID, r.Made = next.takeID(), stamp(actor)
		pos := change.Rule.Position
		if pos < 0 || pos > len(fw.Rules)+1 {
			return fmt.Errorf("position %d is outside the %d rules", pos, len(fw.Rules))
		}
		if pos == 0 {
			fw.Rules = append(fw.Rules, r)
		} else {
			fw.Rules = append(fw.Rules[:pos-1], append([]FirewallRuleSpec{r}, fw.Rules[pos-1:]...)...)
		}
	case "delete":
		kept := fw.Rules[:0]
		found := false
		for _, r := range fw.Rules {
			if r.ID == change.ID {
				found = true
				continue
			}
			kept = append(kept, r)
		}
		if !found {
			return fmt.Errorf("owned firewall rule %d: %w", change.ID, ErrNotFound)
		}
		fw.Rules = kept
	case "enable", "disable":
		fw.Enabled = change.Op == "enable"
	case "policy":
		switch change.Policy {
		case "allow", "accept":
			fw.Incoming = "accept"
		case "deny", "drop", "reject":
			fw.Incoming = "drop"
		default:
			return fmt.Errorf("the owned table's inbound default is allow or deny")
		}
	case "reset":
		next.Firewall = &FirewallSpec{Incoming: "accept", Rules: []FirewallRuleSpec{}}
	default:
		return fmt.Errorf("%q is not an owned firewall change", change.Op)
	}
	candidate, err := renderFirewall(next, s.trustedFor(next))
	if err != nil {
		return err
	}
	previous, _ := renderFirewall(sp, s.trustedFor(sp))
	if sp.Firewall == nil {
		previous = ""
	}
	return s.commit(ctx, next, step{
		apply: func(ctx context.Context) error { return s.loadFirewall(ctx, candidate) },
		undo: func(ctx context.Context) {
			if previous == "" || !ownedFirewallOn(sp) {
				if _, err := run(ctx, "nft", "delete", "table", "inet", firewallTable); err != nil && !isGone(err) {
					recordRecoveryError(ctx, err)
				}
				return
			}
			recordRecoveryError(ctx, s.loadFirewall(ctx, previous))
		},
		verify: func(ctx context.Context) error { return verifyOwnedFirewall(ctx, next) },
	})
}

func (s *Service) loadFirewall(ctx context.Context, ruleset string) error {
	if err := os.MkdirAll(s.paths.Dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.paths.Dir, firewallApplyFile)
	if err := os.WriteFile(path, []byte(ruleset), 0o600); err != nil {
		return err
	}
	defer os.Remove(path)
	if _, err := run(ctx, "nft", "-f", path); err != nil {
		return fmt.Errorf("loading the owned firewall table: %w", err)
	}
	return nil
}

// verifyOwnedFirewall reads the table back: absent when disabled; present,
// with the saved policy and one counted rule per saved rule, when enabled.
func verifyOwnedFirewall(ctx context.Context, sp *Spec) error {
	out, err := run(ctx, "nft", "-j", "list", "table", "inet", firewallTable)
	if !ownedFirewallOn(sp) {
		if err == nil {
			return fmt.Errorf("the owned firewall table is still loaded after it was switched off")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("the owned firewall table is not loaded: %w", err)
	}
	var listing struct {
		Nftables []struct {
			Chain *struct {
				Name   string `json:"name"`
				Policy string `json:"policy"`
			} `json:"chain"`
			Rule *struct {
				Comment string `json:"comment"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		return fmt.Errorf("the owned firewall table could not be read back: %w", err)
	}
	want := map[string]bool{}
	for _, r := range sp.Firewall.Rules {
		want["jd-fw-"+strconv.Itoa(r.ID)] = true
	}
	policy := ""
	for _, o := range listing.Nftables {
		if o.Chain != nil && o.Chain.Name == "input" {
			policy = o.Chain.Policy
		}
		if o.Rule != nil {
			delete(want, o.Rule.Comment)
		}
	}
	if wantPolicy := map[bool]string{true: "drop", false: "accept"}[sp.Firewall.Incoming == "drop"]; policy != wantPolicy {
		return fmt.Errorf("the owned firewall's input policy reads %q, not %q", policy, wantPolicy)
	}
	if len(want) > 0 {
		return fmt.Errorf("the kernel is missing %d of the owned firewall's rules", len(want))
	}
	return nil
}
