package netsec

import (
	"net"
	"strings"
)

// Exposure describes how the dashboard itself can be reached.
//
// This is the one security property the product actually rests on: the panel
// is root-equivalent, so who can open it matters more than anything inside it.
// The setting lives in an env file nobody re-reads after install day, which is
// exactly why it belongs on screen instead — a machine that quietly became
// reachable from the internet should say so, not wait to be discovered.
type Exposure struct {
	// Grade is the headline: tailscale, tunnel, private, public or open.
	Grade string `json:"grade"`
	// Summary is one sentence a person can act on.
	Summary string `json:"summary"`
	// Allowlist is the configured set of addresses, verbatim.
	Allowlist []string `json:"allowlist"`
	// Interfaces names the private network devices found on the host, so the
	// UI can say "you already have Tailscale" without guessing.
	Interfaces []string `json:"interfaces"`
	// TailscaleIP is this machine's tailnet address, when it has one.
	TailscaleIP string `json:"tailscaleIp,omitempty"`
	// Recommendation is non-empty when there is a better arrangement.
	Recommendation string `json:"recommendation,omitempty"`
	// Client is the address the request arrived from, filled in by the
	// handler rather than here so DescribeExposure stays a pure function. It
	// is the one address an operator must never block, ban or leave off an
	// allowlist, and every guard on the Security pages compares against it —
	// so the pages say what it is.
	Client string `json:"client,omitempty"`
}

// The Tailscale CGNAT range. A dashboard allowlisted to this is reachable from
// the operator's own devices and nothing else.
var tailscaleNet = mustCIDR("100.64.0.0/10")

var privateNets = []*net.IPNet{
	mustCIDR("10.0.0.0/8"),
	mustCIDR("172.16.0.0/12"),
	mustCIDR("192.168.0.0/16"),
	mustCIDR("fc00::/7"),
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("netsec: bad constant CIDR " + s)
	}
	return n
}

// DescribeExposure grades the configured allowlist. It reports the weakest
// entry rather than the strongest: one public range in an otherwise private
// list is what decides how exposed the machine really is.
func DescribeExposure(allowed []*net.IPNet) Exposure {
	e := Exposure{Allowlist: make([]string, 0, len(allowed))}
	for _, n := range allowed {
		e.Allowlist = append(e.Allowlist, n.String())
	}
	e.Interfaces, e.TailscaleIP = privateInterfaces()

	worst := 0 // 0 loopback, 1 tailscale, 2 private, 3 public, 4 everything
	for _, n := range allowed {
		switch {
		case isDefaultRoute(n):
			worst = max(worst, 4)
		case n.IP.IsLoopback():
			worst = max(worst, 0)
		case tailscaleNet.Contains(n.IP):
			worst = max(worst, 1)
		case isPrivate(n.IP):
			worst = max(worst, 2)
		default:
			worst = max(worst, 3)
		}
	}

	switch worst {
	case 0:
		e.Grade = "tunnel"
		e.Summary = "Reachable only over loopback — an SSH tunnel is the only way in."
		if e.TailscaleIP != "" {
			e.Recommendation = "Tailscale is already running on this host. Pointing the dashboard at " +
				e.TailscaleIP + " would let you reach it from your own devices without an SSH tunnel."
		}
	case 1:
		e.Grade = "tailscale"
		e.Summary = "Reachable only from your tailnet. Nothing is exposed to the internet."
	case 2:
		e.Grade = "private"
		e.Summary = "Reachable from a private network. Nothing is exposed to the internet directly."
	case 3:
		e.Grade = "public"
		e.Summary = "Reachable from public addresses. The allowlist is the only thing in front of a root-equivalent panel."
		e.Recommendation = "Move to Tailscale or an SSH tunnel. Either removes this machine from the internet entirely."
	case 4:
		e.Grade = "open"
		e.Summary = "The allowlist admits every address on the internet."
		e.Recommendation = "Narrow JD_ALLOWED_CIDRS immediately, or move to Tailscale. Anyone who finds this host can reach the login page."
	}
	return e
}

// isDefaultRoute reports a CIDR that matches everything, in either family.
func isDefaultRoute(n *net.IPNet) bool {
	ones, bits := n.Mask.Size()
	return ones == 0 && bits > 0
}

func isPrivate(ip net.IP) bool {
	for _, n := range privateNets {
		if n.Contains(ip) {
			return true
		}
	}
	return ip.IsLinkLocalUnicast()
}

// privateInterfaces lists the tunnel-ish devices present on the host, so the
// dashboard can point at one the operator already has rather than telling them
// to go and set something up.
func privateInterfaces() (names []string, tailscaleIP string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		n := ifc.Name
		isTailscale := strings.HasPrefix(n, "tailscale")
		if !isTailscale && !strings.HasPrefix(n, "wg") && !strings.HasPrefix(n, "tun") {
			continue
		}
		names = append(names, n)
		if !isTailscale {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				tailscaleIP = ipn.IP.String()
				break
			}
		}
	}
	return names, tailscaleIP
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
