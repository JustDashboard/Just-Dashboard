package netsec

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// JudgeFirewallFrom is JudgeFirewall for one source address, the question an
// access explanation asks: the first inbound rule, in the firewall's order,
// whose source holds the address and whose target covers the socket decides;
// the inbound default does otherwise.
//
// A rule that may concern the socket but cannot be judged for an address from
// its listing — a source that is not an address or a range, a ufw rule
// limited to an interface or written as a profile the host does not define,
// an iptables rule limited to an input interface, carrying a match an address
// does not decide, or jumping to a chain that is not read — is passed over
// and returned, so the caller can say the verdict was reached past it (see
// FirewallUndecided). JudgeFirewall drops such rules, which reads a ufw rule
// admitting the tailnet's interface as nothing at all.
//
// profile looks up a ufw application profile's ports ("80,443/tcp"); it is
// asked only for a rule written as one.
func JudgeFirewallFrom(l ExposedPort, network HostNetwork, firewall *FirewallStatus, source netip.Addr,
	profile func(name string) ([]string, bool)) (FirewallVerdict, []Rule) {
	if firewall == nil || !firewall.Available || firewall.Error != "" {
		return FirewallVerdict{Verdict: VerdictUnknown}, nil
	}
	v := FirewallVerdict{Backend: firewall.Backend, Default: firewall.Policy.Incoming}
	if !firewall.Enabled {
		v.Verdict = VerdictOff
		return v, nil
	}
	if (l.Process == "docker-proxy" || l.Published) &&
		(firewall.Backend == BackendUFW || firewall.Backend == BackendIPTables) {
		v.Verdict = VerdictDocker
		return v, nil
	}
	source = source.Unmap()
	port := strconv.FormatUint(uint64(l.Port), 10)
	var passed []Rule
	for i := range firewall.Rules {
		r := firewall.Rules[i]
		if firewall.Backend == BackendUFW && r.IPv6 != source.Is6() {
			continue
		}
		if !inboundRule(r, firewall.Backend) || !protocolCovers(r.Protocol, l.Protocol) {
			continue
		}
		holds, known := sourceHolds(r.From, source)
		if known && !holds {
			continue
		}
		covers, judged := ruleCoversFrom(r, firewall.Backend, l, network, port, source, profile)
		if !covers {
			continue
		}
		decides := allows(r.Action) || refusesAction(r.Action)
		if !known || !judged || !decides {
			if decides || firewall.Backend == BackendIPTables && jumps(r.Action) {
				passed = append(passed, r)
			}
			continue
		}
		v.Rule, v.Action, v.From = r.Number, r.Action, r.From
		v.Verdict = VerdictBlocked
		if allows(r.Action) {
			v.Verdict = VerdictAllowed
		}
		return v, passed
	}
	switch {
	case firewall.Policy.Incoming == "":
		v.Verdict = VerdictUnknown
	case refuses(firewall.Policy.Incoming):
		v.Verdict = VerdictBlocked
	default:
		v.Verdict = VerdictAllowed
	}
	return v, passed
}

// FirewallUndecided is the rules passed over ahead of a verdict that would
// act otherwise, any of which may be the one that decides for the address:
// with one of them the verdict is not known.
func FirewallUndecided(v FirewallVerdict, passed []Rule) []Rule {
	var undecided []Rule
	for _, r := range passed {
		switch {
		case v.Verdict == VerdictAllowed && !allows(r.Action),
			v.Verdict == VerdictBlocked && !refusesAction(r.Action):
			undecided = append(undecided, r)
		}
	}
	return undecided
}

// ruleCoversFrom says whether a rule may concern the socket for the source,
// and judged whether its listing says so for certain.
func ruleCoversFrom(r Rule, backend Backend, l ExposedPort, network HostNetwork, port string,
	source netip.Addr, profile func(string) ([]string, bool)) (covers, judged bool) {
	switch backend {
	case BackendIPTables:
		return iptablesCoversFrom(r, l, network, port, source)
	case BackendUFW:
		to := strings.TrimSpace(r.To)
		// The parser keeps "on tailscale0" in Interface; a listing read
		// elsewhere may still carry it in the destination column.
		target, iface, limited := strings.Cut(to, " on ")
		if !limited && r.Interface != "" {
			target, iface, limited = to, r.Interface, true
		}
		if limited {
			// "443/tcp on tailscale0": ufw prints the interface in the
			// destination column, and which interface a packet from an
			// address arrives on is the routing's to say, not the rule's.
			if strings.TrimSpace(iface) == "lo" && !source.IsLoopback() {
				return false, true
			}
			return ufwSpecCovers(target, l.Protocol, port), false
		}
		if profile != nil && !ufwPortSpec(to) && !isAnywhere(to) && !isAddress(to) {
			specs, ok := profile(to)
			if !ok {
				return true, false
			}
			for _, spec := range specs {
				ports, protocol, _ := strings.Cut(spec, "/")
				if protocolCovers(protocol, l.Protocol) && portInSpec(port, ports) {
					return true, true
				}
			}
			return false, true
		}
	}
	destination, ports, ok := ruleTarget(r, backend)
	if !ok {
		return true, false
	}
	if ports != "" && !portInSpec(port, ports) {
		return false, true
	}
	if destination != "" && !destinationHolds(destination, l.Address) &&
		!(isWildcardBind(l.Address) && network.internetReaches(destination)) {
		return false, true
	}
	return true, true
}

// ufwSpecCovers reads the target half of a ufw rule limited to an interface
// — "443/tcp", "Anywhere", "10.0.0.5 5432/tcp" — and says whether it may
// cover the port. A profile there is not read and may.
func ufwSpecCovers(target, protocol, port string) bool {
	target = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(target), "(v6)"))
	if isAnywhere(target) {
		return true
	}
	ports, proto, _ := strings.Cut(lastField(target), "/")
	if !bareUFWPortRe.MatchString(ports) {
		return true
	}
	return protocolCovers(proto, protocol) && portInSpec(port, ports)
}

func ufwPortSpec(to string) bool {
	ports, _, _ := strings.Cut(lastField(to), "/")
	return bareUFWPortRe.MatchString(ports)
}

// iptablesCoversFrom reads a line of `iptables -L -n -v`: its input
// interface (the seventh column, "*" for any), its destination, and the
// match text after the columns. A rule limited to loopback concerns only a
// loopback source; one limited to another interface, or carrying a match
// other than its protocol, destination ports, a connection state, a comment
// and a REJECT's answer, cannot be judged for an address; a state match
// without NEW never meets a new connection.
func iptablesCoversFrom(r Rule, l ExposedPort, network HostNetwork, port string, source netip.Addr) (covers, judged bool) {
	fields := strings.Fields(r.Raw)
	in := "*"
	if len(fields) > 6 {
		in = fields[6]
	}
	var matches []string
	if len(fields) > 10 {
		matches = fields[10:]
	}
	judged = true
	comment := false
	for i := 0; i < len(matches); i++ {
		m := matches[i]
		switch {
		case comment:
			comment = m != "*/"
		case m == "/*":
			comment = true
		case m == "tcp" || m == "udp" || m == "multiport" ||
			strings.HasPrefix(m, "dpt:") || strings.HasPrefix(m, "dpts:"):
		case (m == "dports" || m == "reject-with") && i+1 < len(matches):
			i++
		case (m == "state" || m == "ctstate") && i+1 < len(matches):
			if !slices.Contains(strings.Split(matches[i+1], ","), "NEW") {
				return false, true
			}
			i++
		default:
			judged = false
		}
	}
	if ports := iptablesPort(r.Raw); ports != "" && !portInSpec(port, ports) {
		return false, true
	}
	if destination := anywhereAsEmpty(r.To); destination != "" && !destinationHolds(destination, l.Address) &&
		!(isWildcardBind(l.Address) && network.internetReaches(destination)) {
		return false, true
	}
	switch in {
	case "*", "":
	case "lo":
		if !source.IsLoopback() {
			return false, true
		}
	default:
		judged = false
	}
	return true, judged
}

func allows(action string) bool {
	switch strings.ToUpper(action) {
	case "ALLOW", "LIMIT", "ACCEPT":
		return true
	}
	return false
}

func refusesAction(action string) bool {
	switch strings.ToUpper(action) {
	case "DENY", "REJECT", "DROP":
		return true
	}
	return false
}

// jumps says an iptables target is another chain, whose rules are not read:
// anything but a built-in target that lets the packet go on through INPUT.
func jumps(target string) bool {
	switch strings.ToUpper(target) {
	case "", "LOG", "NFLOG", "ULOG", "RETURN", "MARK", "CONNMARK", "CT", "NOTRACK", "TRACE", "AUDIT":
		return false
	}
	return true
}

// sourceHolds says whether a rule's source admits an address: anywhere, the
// address itself, or a range holding it. known is false for a source written
// as something else.
func sourceHolds(from string, source netip.Addr) (holds, known bool) {
	if isAnywhere(from) {
		return true, true
	}
	raw := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(from), "(v6)"))
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		return prefix.Contains(source.Unmap()), true
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		return addr.Unmap() == source.Unmap(), true
	}
	return false, false
}
