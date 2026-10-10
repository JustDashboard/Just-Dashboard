package netsec

import (
	"fmt"
	"slices"
	"strings"
)

// PostureUnknown is a layer of the host's exposure the checks did not see.
// It is neither a finding nor a pass: a verdict that leaves out what it
// could not look at reads as having looked.
type PostureUnknown struct {
	ID       string   `json:"id"`
	Layer    string   `json:"layer"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Subjects []string `json:"subjects,omitempty"`
}

// PolicyLayer is a base chain at a filtering hook, as the gateway's reading
// of the nftables ruleset classified it.
type PolicyLayer struct {
	Family string
	Table  string
	Chain  string
	Hook   string
	Policy string
	// Status is the gateway's own verdict on the chain: owned, checked,
	// admitted, blocked or unknown.
	Status string
}

// PolicyCoverage is what could be seen of the host's packet filtering past
// the firewall adapter. Error is the ruleset being unreadable.
type PolicyCoverage struct {
	Error  string
	Layers []PolicyLayer
}

// iptablesTables are the tables iptables-nft writes for ufw, Docker and
// plain iptables; the firewall adapter models those, not another table.
var iptablesTables = []string{"filter", "nat", "raw", "mangle", "security"}

// foreignLayer is a base chain no adapter of this dashboard models: not an
// iptables-nft table, not firewalld's, not the dashboard's own gateway.
func foreignLayer(l PolicyLayer) bool {
	switch {
	case l.Status == "owned":
		return false
	case (l.Family == "ip" || l.Family == "ip6") && slices.Contains(iptablesTables, l.Table):
		return false
	case l.Family == "inet" && l.Table == "firewalld":
		return false
	}
	return l.Hook == "input" || l.Hook == "forward" || l.Hook == "prerouting"
}

// assessUnknowns names the layers the exposure checks could not see:
// provider policy always, since no provider adapter exists, and any foreign
// nftables chain whose decision is more than an unconditional accept.
func assessUnknowns(in AssessInput) []PostureUnknown {
	provider := PostureUnknown{ID: "unknown.provider", Layer: "provider", Title: "Provider policy is not visible",
		Detail: "No provider adapter is configured. Security groups, provider firewalls, upstream NAT and load balancers in front of this host are unseen: a port graded reachable here may be filtered upstream, and one graded closed may still be forwarded to by a provider's mapping."}
	switch {
	case !in.PublicAddressRead:
	case in.PublicAddress != "":
		provider.Detail += " This host has a public address on an interface (" + in.PublicAddress + "), so the internet can reach it without a provider translation."
	default:
		provider.Detail += " This host has no public address on any interface, so traffic from the internet reaches it only through a provider's translation."
	}
	out := []PostureUnknown{provider}
	switch {
	case in.Policy == nil:
		out = append(out, PostureUnknown{ID: "unknown.nftables", Layer: "nftables", Title: "Other nftables tables were not read",
			Detail: "The host's nftables ruleset was not read, so a table besides the firewall adapter's could decide inbound traffic unseen."})
	case in.Policy.Error != "":
		out = append(out, PostureUnknown{ID: "unknown.nftables", Layer: "nftables", Title: "The nftables ruleset could not be read",
			Detail: "A table besides the firewall adapter's could decide inbound traffic unseen: " + in.Policy.Error})
	default:
		subjects := []string{}
		for _, l := range in.Policy.Layers {
			if foreignLayer(l) && (l.Status == "blocked" || l.Status == "unknown") {
				policy := l.Policy
				if policy == "" {
					policy = "unknown"
				}
				subjects = append(subjects, fmt.Sprintf("%s %s %s (%s hook, policy %s)", l.Family, l.Table, l.Chain, l.Hook, policy))
			}
		}
		if len(subjects) > 0 {
			out = append(out, PostureUnknown{ID: "unknown.nftables", Layer: "nftables", Title: "Foreign nftables decisions are not modeled", Subjects: subjects,
				Detail: fmt.Sprintf("%d chain%s outside the firewall adapter can drop or redirect inbound traffic with rules this check does not evaluate: %s. A port's grade here does not include them.", len(subjects), map[bool]string{true: "", false: "s"}[len(subjects) == 1], strings.Join(subjects, "; "))})
		}
	}
	return out
}
