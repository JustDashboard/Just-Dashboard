package netsec

import (
	"net/netip"
	"strconv"
	"strings"
)

// JudgeFirewallFrom is JudgeFirewall for one source address, the question an
// access explanation asks: the first inbound rule, in the firewall's order,
// whose source holds the address and whose target covers the socket decides;
// the inbound default does otherwise. A rule whose source the listing does
// not name as an address or a range (an interface, a profile, a set) cannot
// be judged for an address and is passed over, as JudgeFirewall passes over
// a target it cannot read; Skipped counts them, so the verdict says it was
// decided past them.
func JudgeFirewallFrom(l ExposedPort, network HostNetwork, firewall *FirewallStatus, source netip.Addr) (FirewallVerdict, int) {
	if firewall == nil || !firewall.Available || firewall.Error != "" {
		return FirewallVerdict{Verdict: VerdictUnknown}, 0
	}
	v := FirewallVerdict{Backend: firewall.Backend, Default: firewall.Policy.Incoming}
	if !firewall.Enabled {
		v.Verdict = VerdictOff
		return v, 0
	}
	if (l.Process == "docker-proxy" || l.Published) &&
		(firewall.Backend == BackendUFW || firewall.Backend == BackendIPTables) {
		v.Verdict = VerdictDocker
		return v, 0
	}
	port := strconv.FormatUint(uint64(l.Port), 10)
	v6 := source.Unmap().Is6()
	skipped := 0
	for i := range firewall.Rules {
		r := firewall.Rules[i]
		if firewall.Backend == BackendUFW && r.IPv6 != v6 {
			continue
		}
		if !inboundRule(r, firewall.Backend) || !protocolCovers(r.Protocol, l.Protocol) {
			continue
		}
		destination, ports, ok := ruleTarget(r, firewall.Backend)
		if !ok || ports != "" && !portInSpec(port, ports) {
			continue
		}
		if destination != "" && !destinationHolds(destination, l.Address) &&
			!(isWildcardBind(l.Address) && network.internetReaches(destination)) {
			continue
		}
		holds, known := sourceHolds(r.From, source)
		if !known {
			skipped++
			continue
		}
		if !holds {
			continue
		}
		switch strings.ToUpper(r.Action) {
		case "ALLOW", "LIMIT", "ACCEPT":
			v.Verdict, v.Rule, v.Action, v.From = VerdictAllowed, r.Number, r.Action, r.From
			return v, skipped
		case "DENY", "REJECT", "DROP":
			// iptables' listing leaves out the interface a rule is limited
			// to, so its refusals may be about another link entirely.
			if firewall.Backend == BackendIPTables {
				skipped++
				continue
			}
			v.Verdict, v.Rule, v.Action, v.From = VerdictBlocked, r.Number, r.Action, r.From
			return v, skipped
		}
	}
	switch {
	case firewall.Policy.Incoming == "":
		v.Verdict = VerdictUnknown
	case refuses(firewall.Policy.Incoming):
		v.Verdict = VerdictBlocked
	default:
		v.Verdict = VerdictAllowed
	}
	return v, skipped
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
