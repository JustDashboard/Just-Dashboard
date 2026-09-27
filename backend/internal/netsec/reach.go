package netsec

import "net"

// Who can connect to a socket, judged from the address it is bound to.
//
// 0.0.0.0 and :: are every interface. A socket on one specific address
// reaches exactly as far as that address does: a public address is the
// internet, a tailnet address is every device on the tailnet, a LAN or Docker
// bridge address is whatever sits on that network. Treating all of those as
// "not a wildcard, so loopback" is how a database on a public IP went
// unreported.

// tailscaleNet6 is Tailscale's IPv6 range, beside the CGNAT one in exposure.go.
var tailscaleNet6 = mustCIDR("fd7a:115c:a1e0::/48")

type bindReach struct {
	// where finishes "listening on …".
	where string
	// rank orders reaches by how many can connect: 3 every interface, 2 a
	// public address, 1 a network this host is on.
	rank int
	// who names what can connect, for a reach short of the internet.
	who string
}

func reachOf(address string) bindReach {
	ip := net.ParseIP(address)
	switch {
	case address == "" || address == "*" || ip != nil && ip.IsUnspecified():
		return bindReach{where: "every interface", rank: 3}
	case ip != nil && (tailscaleNet.Contains(ip) || tailscaleNet6.Contains(ip)):
		return bindReach{where: "a tailnet address", rank: 1, who: "every device on the tailnet"}
	case ip != nil && !isGloballyRoutable(address):
		// A cloud instance sees only its private address, and the provider
		// forwards the public one onto it.
		return bindReach{where: "a private address", rank: 1,
			who: "the machines and containers on that network, and the internet if the provider maps a public address onto it"}
	}
	return bindReach{where: "a public address", rank: 2}
}

// internetFacing is a reach the whole internet shares.
func (r bindReach) internetFacing() bool { return r.rank >= 2 }

// advice is what to do about a database or control port bound at this reach,
// given the catalogue's note on why the service should not face the internet.
func (r bindReach) advice(danger, address, port string) string {
	switch r.rank {
	case 3:
		return danger + " Bind it to 127.0.0.1 instead — for a container, publish it as 127.0.0.1:" + port + ": rather than " + port + ":."
	case 2:
		return danger + " Bind it to 127.0.0.1 instead — for a container, publish it as 127.0.0.1:" + port + ": rather than " + net.JoinHostPort(address, port) + ":."
	}
	return "Anything that can reach " + address + " can connect to it: " + r.who + ". Bind it to 127.0.0.1 unless something there needs it."
}
