package netsec

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

type TrafficPolicy struct {
	Verdict     string   `json:"verdict"`
	Direction   string   `json:"direction"`
	Summary     string   `json:"summary"`
	Rule        int      `json:"rule,omitempty"`
	Limitations []string `json:"limitations"`
}

// ModelTrafficPolicy explains the simple UFW adapter's ordered selectors.
// It never decides foreign chains, zones, Docker bypass, state or provider
// policy. Those remain unknown even if this adapter would admit an attempt.
func ModelTrafficPolicy(status *FirewallStatus, source, target, protocol string, port int, direction string) TrafficPolicy {
	p := TrafficPolicy{Verdict: "unknown", Direction: direction, Summary: "The effective firewall path is unknown.", Limitations: []string{"Foreign nftables/iptables, conntrack, interface-specific rules, Docker forwarding and provider policy are not fully modeled."}}
	if status == nil || !status.Available || status.Error != "" {
		return p
	}
	if !status.Enabled {
		p.Verdict, p.Summary = "disabled", "The selected host firewall adapter is disabled; other policy may still enforce."
		return p
	}
	if status.Backend != BackendUFW {
		p.Summary = "The " + string(status.Backend) + " adapter does not expose enough source/direction selectors for a complete prediction."
		return p
	}
	ip, err := netip.ParseAddr(target)
	if err != nil {
		return p
	}
	for _, rule := range status.Rules {
		d := strings.ToLower(rule.Direction)
		if d == "" {
			d = "in"
		}
		if d != direction || rule.IPv6 != ip.Is6() {
			continue
		}
		switch strings.ToLower(rule.Protocol) {
		case "", "all", "tcp", "udp", "icmp", "ipv6-icmp":
		default:
			p.Summary = "A rule has an opaque protocol selector; its match remains unknown."
			return p
		}
		if !protocolCovers(rule.Protocol, protocol) {
			continue
		}
		destination, ports, understood := ruleTarget(rule, status.Backend)
		if !understood {
			p.Summary = "UFW rule " + strconv.Itoa(rule.Number) + " has selectors this model cannot interpret."
			return p
		}
		if ports != "" && !portInSpec(strconv.Itoa(port), ports) || destination != "" && !destinationHolds(destination, target) {
			continue
		}
		if !isAnywhere(rule.From) {
			if source == "" {
				p.Summary = "A source-sensitive rule cannot be resolved without the selected source address."
				return p
			}
			prefix, err := netip.ParsePrefix(rule.From)
			if err != nil {
				addr, e := netip.ParseAddr(rule.From)
				if e != nil {
					return p
				}
				prefix = netip.PrefixFrom(addr, addr.BitLen())
			}
			src, err := netip.ParseAddr(source)
			if err != nil || !prefix.Contains(src) {
				continue
			}
		}
		if strings.Contains(strings.ToLower(rule.Raw), " on ") {
			p.Summary = "A matching rule has an interface selector; its effective hook remains unknown."
			return p
		}
		switch strings.ToUpper(rule.Action) {
		case "ALLOW", "ACCEPT":
			p.Verdict = "allow"
		case "DENY", "DROP":
			p.Verdict = "deny"
		case "REJECT":
			p.Verdict = "reject"
		default:
			p.Summary = "A matching stateful/rate-limited action cannot be predicted for one new connection."
			return p
		}
		p.Rule = rule.Number
		p.Summary = fmt.Sprintf("UFW %s rule %d models %s for this tuple.", strings.ToUpper(direction), rule.Number, p.Verdict)
		return p
	}
	value := status.Policy.Outgoing
	if direction == "in" {
		value = status.Policy.Incoming
	} else if direction == "fwd" {
		value = status.Policy.Routed
	}
	switch strings.ToLower(value) {
	case "allow", "accept":
		p.Verdict = "allow"
	case "deny", "drop":
		p.Verdict = "deny"
	case "reject":
		p.Verdict = "reject"
	}
	if p.Verdict != "unknown" {
		p.Summary = fmt.Sprintf("UFW %s default models %s for this tuple.", strings.ToUpper(direction), p.Verdict)
	}
	return p
}
