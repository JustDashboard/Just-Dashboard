package netsec

import "strconv"

// InboundRuleFor is the first inbound rule whose port and protocol cover a
// port, read the way the posture reads rules: a rule whose target its
// listing does not say — a ufw application profile, a rule limited to an
// interface, an iptables rule naming no port — is not counted, rather than
// guessed at. The free-port search uses it to pass over a port a rule
// already decides for.
func InboundRuleFor(status *FirewallStatus, protocol string, port int) (Rule, bool) {
	if status == nil {
		return Rule{}, false
	}
	value := strconv.Itoa(port)
	for _, r := range status.Rules {
		if !inboundRule(r, status.Backend) || !protocolCovers(r.Protocol, protocol) {
			continue
		}
		_, ports, ok := ruleTarget(r, status.Backend)
		if ok && ports != "" && portInSpec(value, ports) {
			return r, true
		}
	}
	return Rule{}, false
}
