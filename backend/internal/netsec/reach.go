package netsec

import (
	"net"
	"strings"
)

// Who can connect to a socket, judged from the address it is bound to and the
// interface that address is on.
//
// 0.0.0.0 and :: are every interface. A socket on one specific address
// reaches exactly as far as that address does: a public address is the
// internet, a tailnet address is every device on the tailnet, a Docker or
// libvirt bridge address is this host's containers and VMs, a link-local one
// the machines on that link. Treating all of those as "not a wildcard, so
// loopback" is how a database on a public IP went unreported, and treating
// them all as one "private address" is how a Redis for the containers on
// docker0 was said to face the internet through a provider's NAT that can
// never map onto a bridge.

// tailscaleNet6 is Tailscale's IPv6 range, beside the CGNAT one in exposure.go.
var tailscaleNet6 = mustCIDR("fd7a:115c:a1e0::/48")

// Reach is how far a bind reaches, as one grade the ports page and the
// posture share: the page colours a socket by it and the posture levels a
// finding by it, so the two cannot disagree about the same database.
type Reach string

const (
	// ReachLoopback is 127.0.0.0/8 or ::1: this machine only.
	ReachLoopback Reach = "loopback"
	// ReachHost is an address on a bridge, or a link-local one there: only
	// this host's containers and virtual machines can connect.
	ReachHost Reach = "host"
	// ReachNetwork is a tailnet, VPN, LAN or private address on the uplink:
	// one network can connect, and not the internet unless a provider maps a
	// public address onto it.
	ReachNetwork Reach = "network"
	// ReachPublic is a globally routable address: the internet.
	ReachPublic Reach = "public"
	// ReachAll is 0.0.0.0 or ::, every interface the host has or will have.
	ReachAll Reach = "all"
)

// rank orders reaches by how many can connect.
func (r Reach) rank() int {
	switch r {
	case ReachAll:
		return 4
	case ReachPublic:
		return 3
	case ReachNetwork:
		return 2
	case ReachHost:
		return 1
	}
	return 0
}

// InternetFacing is a reach the whole internet shares.
func (r Reach) InternetFacing() bool { return r.rank() >= ReachPublic.rank() }

// HostNetwork is which interface holds each of the host's addresses and
// which interfaces carry a default route: what it takes to tell a bridge
// address from an uplink one. The zero value knows nothing, and every private
// address is then judged as if it were on the uplink.
type HostNetwork struct {
	Addresses []HostAddress
}

// HostAddress is one address the host holds.
type HostAddress struct {
	IP        net.IP
	Interface string
	// Kind is classifyLinks': physical, tunnel, bridge, virtual or loopback.
	// A bridge is one whose every port is a container's or a VM's.
	Kind string
	// DefaultRoute says the interface carries a default route, which makes
	// it the uplink whatever its name.
	DefaultRoute bool
}

// Reach grades a bind address.
func (n HostNetwork) Reach(address string) Reach { return n.reachOf(address).class }

func (n HostNetwork) interfaceOf(ip net.IP) (HostAddress, bool) {
	for _, a := range n.Addresses {
		if a.IP.Equal(ip) {
			return a, true
		}
	}
	return HostAddress{}, false
}

type bindReach struct {
	class Reach
	// where finishes "listening on …".
	where string
	// who names what can connect, for a reach short of the internet.
	who string
	// forwarded says a provider could map a public address onto it: a
	// private address on the uplink, or one whose interface is unknown.
	forwarded bool
}

func (n HostNetwork) reachOf(address string) bindReach {
	ip := net.ParseIP(address)
	switch {
	case address == "" || address == "*" || ip != nil && ip.IsUnspecified():
		return bindReach{class: ReachAll, where: "every interface"}
	case ip == nil || isGloballyRoutable(address):
		return bindReach{class: ReachPublic, where: "a public address"}
	case ip.IsLoopback():
		return bindReach{class: ReachLoopback, where: "loopback", who: "this machine only"}
	}
	iface, known := n.interfaceOf(ip)
	hostOnly := known && !iface.DefaultRoute && (iface.Kind == "bridge" || iface.Kind == "virtual")
	switch {
	case ip.IsLinkLocalUnicast() && hostOnly:
		return bindReach{class: ReachHost, where: "a link-local address",
			who: "the containers and virtual machines on " + iface.Interface}
	case ip.IsLinkLocalUnicast():
		// Link-local is never routed, so no provider can forward to it.
		return bindReach{class: ReachNetwork, where: "a link-local address",
			who: "the machines on the same link"}
	case hostOnly:
		return bindReach{class: ReachHost, where: "a bridge address",
			who: "the containers and virtual machines on " + iface.Interface}
	case known && !iface.DefaultRoute && iface.Kind == "tunnel":
		if isTailnet(ip) || strings.HasPrefix(iface.Interface, "tailscale") {
			return bindReach{class: ReachNetwork, where: "a tailnet address", who: "every device on the tailnet"}
		}
		return bindReach{class: ReachNetwork, where: "a VPN address", who: "the peers on " + iface.Interface}
	case !known && isTailnet(ip):
		return bindReach{class: ReachNetwork, where: "a tailnet address", who: "every device on the tailnet"}
	}
	// A cloud instance sees only its private address on the uplink, and the
	// provider forwards the public one onto it.
	return bindReach{class: ReachNetwork, where: "a private address", forwarded: true,
		who: "the machines on that network, and the internet if the provider maps a public address onto it"}
}

func isTailnet(ip net.IP) bool {
	return tailscaleNet.Contains(ip) || tailscaleNet6.Contains(ip)
}

// matters says whether a port whose danger concerns only strangers on the
// internet — an open resolver, a brute-forced remote desktop — is worth a
// finding at this reach. A bind the internet cannot reach is not: libvirt's
// dnsmasq on virbr0 is how its VMs resolve names, and a remote desktop on a
// tailnet is the VPN its own advice asks for.
func (r bindReach) matters(preset ServicePreset) bool {
	return !preset.InternetOnly || r.class.InternetFacing() || r.forwarded
}

// advice is what to do about a database or control port bound at this reach,
// given the catalogue's note on why the service should not be reachable.
func (r bindReach) advice(danger, address, port string) string {
	switch r.class {
	case ReachAll:
		return danger + " Bind it to 127.0.0.1 instead — for a container, publish it as 127.0.0.1:" + port + ": rather than " + port + ":."
	case ReachPublic:
		return danger + " Bind it to 127.0.0.1 instead — for a container, publish it as 127.0.0.1:" + port + ": rather than " + net.JoinHostPort(address, port) + ":."
	}
	return danger + " Anything that can reach " + address + " can connect to it: " + r.who + ". Bind it to 127.0.0.1 unless something there needs it."
}
