package dockerx

import (
	"net"
	"sort"
	"strings"
)

// What a published port actually means.
//
// `0.0.0.0:5432 → 5432` and `127.0.0.1:5432 → 5432` are one character apart
// and could not be further apart in consequence: the first is a database
// offered to everything that can route to this server, the second is one
// offered to this server only. Every panel in this section used to render them
// the same way, and three separate places computed "is this public" with three
// slightly different rules.
//
// The vocabulary here is deliberately about *binding*, not about reachability.
// Docker publishes a port by writing NAT rules the firewall never sees, so
// "bound to every interface" is a fact and "reachable from the internet" is a
// conclusion that needs the firewall, the routing table and what is in front
// of this machine. The type keeps the two apart so the UI can state the first
// and qualify the second.

// PortScope is where a published port can be reached from, as far as the
// binding alone can say.
type PortScope string

const (
	// ScopeLoopback is bound to 127.0.0.1 or ::1 — this server only.
	ScopeLoopback PortScope = "loopback"
	// ScopePrivate is bound to one specific non-loopback address. Whoever can
	// reach that address can reach the port; usually a LAN or a VPN.
	ScopePrivate PortScope = "private"
	// ScopeAll is bound to 0.0.0.0 or :: — every interface this host has,
	// present and future.
	ScopeAll PortScope = "all"
	// ScopeInternal is a container port with no host binding at all. Other
	// containers on the same network can reach it; nothing outside can.
	ScopeInternal PortScope = "internal"
)

// PortExposure describes one published port.
type PortExposure struct {
	// HostIP is the address Docker bound to, verbatim and possibly empty —
	// which is Docker's own spelling of "every interface".
	HostIP        string    `json:"hostIp,omitempty"`
	HostPort      int       `json:"hostPort,omitempty"`
	ContainerPort int       `json:"containerPort"`
	Protocol      string    `json:"protocol"`
	Scope         PortScope `json:"scope"`
	// IPv6 marks a binding made on the v6 stack. Docker publishes a port on
	// both families as two separate bindings, and a list that does not say
	// which is which reads as every port having been published twice.
	IPv6 bool `json:"ipv6,omitempty"`
	// Summary is the one line a table cell shows. Label is the shorter badge.
	Label   string `json:"label"`
	Summary string `json:"summary"`
}

// URL is the address to open in a browser, when there is exactly one that is
// right. A port on every interface has no single correct host — the answer
// depends on which of this machine's addresses the operator's browser can
// reach — so it deliberately returns nothing rather than guessing one that
// leads somewhere else.
func (p PortExposure) URL() string {
	if p.HostPort == 0 {
		return ""
	}
	switch p.Scope {
	case ScopeLoopback, ScopePrivate:
		host := p.HostIP
		if host == "" {
			host = "127.0.0.1"
		}
		if p.IPv6 {
			host = "[" + host + "]"
		}
		return "http://" + host + ":" + itoa(p.HostPort)
	default:
		return ""
	}
}

// DescribePort classifies one binding.
func DescribePort(hostIP string, hostPort, containerPort int, protocol string) PortExposure {
	if protocol == "" {
		protocol = "tcp"
	}
	out := PortExposure{
		HostIP:        hostIP,
		HostPort:      hostPort,
		ContainerPort: containerPort,
		Protocol:      protocol,
	}
	ip := net.ParseIP(strings.TrimSpace(hostIP))
	out.IPv6 = ip != nil && ip.To4() == nil

	switch {
	case hostPort == 0:
		out.Scope = ScopeInternal
	case hostIP == "" || hostIP == "0.0.0.0" || hostIP == "::" || (ip != nil && ip.IsUnspecified()):
		out.Scope = ScopeAll
	case ip != nil && ip.IsLoopback():
		out.Scope = ScopeLoopback
	default:
		out.Scope = ScopePrivate
	}

	out.Label, out.Summary = describeScope(out)
	return out
}

func describeScope(p PortExposure) (label, summary string) {
	proto := ""
	if p.Protocol != "tcp" {
		proto = "/" + p.Protocol
	}
	switch p.Scope {
	case ScopeInternal:
		return "Not published",
			"Port " + itoa(p.ContainerPort) + proto + " is open inside the container. " +
				"Other containers on the same network can reach it; nothing outside this server can."
	case ScopeLoopback:
		return "This server only",
			"Bound to " + p.HostIP + ":" + itoa(p.HostPort) + proto +
				". Only processes on this server — including the reverse proxy — can reach it."
	case ScopePrivate:
		return "One interface",
			"Bound to " + p.HostIP + ":" + itoa(p.HostPort) + proto +
				". Reachable from wherever that address is, and nowhere else."
	default:
		return "Every interface",
			"Bound to every interface on port " + itoa(p.HostPort) + proto +
				". Docker publishes ports with NAT rules the firewall is not consulted about, " +
				"so external reachability depends on what is in front of this server rather than on the firewall alone."
	}
}

// DescribePorts classifies a container's whole port list, ordered so the most
// exposed comes first — which is the order an operator scanning for a problem
// wants and the opposite of the order Docker returns.
func DescribePorts(ports []Port) []PortExposure {
	out := make([]PortExposure, 0, len(ports))
	for _, p := range ports {
		out = append(out, DescribePort(p.IP, int(p.PublicPort), int(p.PrivatePort), p.Type))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if scopeRank(out[i].Scope) != scopeRank(out[j].Scope) {
			return scopeRank(out[i].Scope) < scopeRank(out[j].Scope)
		}
		return out[i].HostPort < out[j].HostPort
	})
	return out
}

func scopeRank(s PortScope) int {
	switch s {
	case ScopeAll:
		return 0
	case ScopePrivate:
		return 1
	case ScopeLoopback:
		return 2
	default:
		return 3
	}
}

// PublicBindings returns the bindings that reach beyond this server, which is
// the set every exposure finding is about.
func PublicBindings(ports []Port) []PortExposure {
	out := []PortExposure{}
	for _, p := range DescribePorts(ports) {
		if p.Scope == ScopeAll || p.Scope == ScopePrivate {
			out = append(out, p)
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
