package api

import (
	"net"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// What a change to a database's reach has to do, worked out before anything
// is done: which host address the port is published on, whether the gateway
// keeps it to a list, which rules the host firewall holds for it, and the
// order those change in. Kept free of the host so the order — which is the
// safety property — is pinned by tests rather than hoped for.

// dbReach is one end of a change to where a server can be reached from.
type dbReach struct {
	Exposure dbExposure `json:"exposure"`
	// HostIP is the address the port is published on: loopback, one of the
	// host's own, 0.0.0.0 for every IPv4 interface, or empty for every
	// interface of both families.
	HostIP string `json:"hostIp"`
	// Allow is what the gateway keeps the port to; empty when it keeps it to
	// nothing.
	Allow []string `json:"allow,omitempty"`
}

// everyInterface is a host address Docker reads as every interface.
func everyInterface(ip string) bool {
	switch ip {
	case "", "0.0.0.0", "::", "[::]", "*":
		return true
	}
	return false
}

func loopbackAddress(ip string) bool {
	a := net.ParseIP(strings.Trim(ip, "[]"))
	return a != nil && a.IsLoopback()
}

func sameAddress(a, b string) bool {
	x, y := net.ParseIP(strings.Trim(a, "[]")), net.ParseIP(strings.Trim(b, "[]"))
	return x != nil && x.Equal(y)
}

// listsIPv6 reports a list naming an IPv6 network.
func listsIPv6(allow []string) bool {
	return slices.ContainsFunc(allow, func(source string) bool { return strings.Contains(source, ":") })
}

// reachBinding is the host address a reach publishes the port on. A list
// with an IPv6 network in it needs the port on both families, which Docker
// spells as no address at all; 0.0.0.0 publishes IPv4 alone.
func reachBinding(exposure dbExposure, address string, allow []string) string {
	switch exposure {
	case exposureLocal:
		return "127.0.0.1"
	case exposurePrivate:
		return address
	case exposureAllowlist:
		if listsIPv6(allow) {
			return ""
		}
	}
	return "0.0.0.0"
}

// bindingServes says whether the port's configured host address already gives
// the reach asked for, so the container is recreated — every session dropped —
// only when it has to be: any every-interface binding serves Anywhere and an
// IPv4 list, and only both families serve a list naming an IPv6 network.
func bindingServes(current string, to dbReach) bool {
	switch to.Exposure {
	case exposureLocal:
		return loopbackAddress(current)
	case exposurePrivate:
		return sameAddress(current, to.HostIP)
	case exposureAllowlist:
		if listsIPv6(to.Allow) {
			return current == ""
		}
	}
	return everyInterface(current)
}

// dbAccessSteps is what a change does, in the order it runs. A list goes
// into the gateway before the binding can widen, and comes out only after
// the binding and the firewall say what replaces it, so the port is never
// open to more than both ends of the change allow.
type dbAccessSteps struct {
	// Keep writes the gateway's list for the port.
	Keep bool
	// Rebind recreates the container on another host address.
	Rebind bool
	// Release takes the gateway's list away.
	Release bool
}

// accessSteps plans a change from what the port has to what was asked for.
// kept says the gateway holds a list for the port now, which may be one a
// change that could not finish left behind a binding that no longer needs it.
func accessSteps(from, to dbReach, kept bool) dbAccessSteps {
	return dbAccessSteps{
		Keep:    to.Exposure == exposureAllowlist && (!kept || !slices.Equal(from.Allow, to.Allow)),
		Rebind:  !bindingServes(from.HostIP, to),
		Release: to.Exposure != exposureAllowlist && kept,
	}
}

// dbFirewallWant is the inbound rules the host firewall should hold for the
// port at a reach: one from anywhere for Anywhere, one per network for a list,
// and none where the binding alone decides. On the path a published port takes
// the gateway is what refuses a source; these are what admit the listed ones
// where the firewall does see the traffic, and what its own listing says.
func dbFirewallWant(to dbReach, port int) []netsec.RuleRequest {
	rule := netsec.RuleRequest{
		Action: "allow", Direction: "in", Port: strconv.Itoa(port), Protocol: "tcp", Comment: dbFirewallComment(port),
	}
	switch to.Exposure {
	case exposurePublic:
		return []netsec.RuleRequest{rule}
	case exposureAllowlist:
		out := make([]netsec.RuleRequest, 0, len(to.Allow))
		for _, source := range to.Allow {
			r := rule
			r.From = source
			out = append(out, r)
		}
		return out
	}
	return nil
}

// portAllowRule is an inbound allow rule for exactly this TCP port on every
// interface and no destination address: the only shape this route writes.
func portAllowRule(r netsec.Rule, port int) bool {
	return strings.EqualFold(r.Action, "allow") &&
		(r.Direction == "" || strings.EqualFold(r.Direction, "in")) &&
		r.Port == strconv.Itoa(port) &&
		(r.Protocol == "" || strings.EqualFold(r.Protocol, "tcp")) &&
		r.Interface == "" && len(strings.Fields(r.To)) <= 1
}

// sourceIs compares a listed rule's source with one this route writes, where
// empty is anywhere. ufw prints a single address bare and firewalld may not,
// so the two are compared as networks.
func sourceIs(from, want string) bool {
	if want == "" {
		return isAnywhere(from)
	}
	a, errA := sourcePrefix(from)
	b, errB := sourcePrefix(want)
	return errA == nil && errB == nil && a == b
}

func sourcePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	p, err := netip.ParsePrefix(s)
	return p.Masked(), err
}

// dbFirewallOwned are the port's rules this route wrote: by their comment, or
// — on firewalld, which keeps no comments — by being exactly the rule written
// for a network the gateway's list names. A rule from anywhere without a
// comment is never taken for this route's: it could as well be the operator's.
// ufw's IPv6 copy of a rule is left out, because removing a rule removes its
// copy with it and a second removal would find nothing.
func dbFirewallOwned(st *netsec.FirewallStatus, port int, known []string) []netsec.Rule {
	comment := dbFirewallComment(port)
	var owned []netsec.Rule
	for _, r := range st.Rules {
		if !portAllowRule(r, port) {
			continue
		}
		byShape := st.Backend == netsec.BackendFirewalld && r.Comment == "" &&
			slices.ContainsFunc(known, func(source string) bool { return sourceIs(r.From, source) })
		if r.Comment != comment && !byShape {
			continue
		}
		owned = append(owned, r)
	}
	return slices.DeleteFunc(owned, func(r netsec.Rule) bool {
		return r.IPv6 && slices.ContainsFunc(owned, func(twin netsec.Rule) bool {
			return !twin.IPv6 && twin.From == r.From && twin.To == r.To && twin.Comment == r.Comment
		})
	})
}

// dbFirewallPlan is the operations that take the host firewall from the rules
// it holds for the port to the ones wanted: an add for each wanted source no
// rule admits yet, then a removal for each rule of this route's nothing wants.
// A source an operator's own rule already admits is left to that rule, the
// way opening the port always has been.
func dbFirewallPlan(st *netsec.FirewallStatus, port int, want []netsec.RuleRequest, known []string) []netsec.PlanOperation {
	owned := dbFirewallOwned(st, port, known)
	keep := map[string]bool{}
	var adds, removes []netsec.PlanOperation
	for _, w := range want {
		admitted := false
		for _, r := range st.Rules {
			// From anywhere, ufw's IPv6 copy says what the rule beside it
			// does; only the rule itself is kept.
			if portAllowRule(r, port) && sourceIs(r.From, w.From) && (w.From != "" || !r.IPv6) {
				admitted = true
				keep[r.ID] = true
			}
		}
		if !admitted {
			rule := w
			adds = append(adds, netsec.PlanOperation{Op: "add", Rule: &rule})
		}
	}
	for _, r := range owned {
		if !keep[r.ID] {
			removes = append(removes, netsec.PlanOperation{Op: "delete", RuleID: r.ID})
		}
	}
	return append(adds, removes...)
}

// planChunks splits the operations into plans of the size netsec reviews and
// takes back as one. The order is kept, so every add runs before any removal
// whichever plan it lands in.
func planChunks(ops []netsec.PlanOperation) [][]netsec.PlanOperation {
	var out [][]netsec.PlanOperation
	for len(ops) > 0 {
		n := min(len(ops), netsec.MaxPlanOperations)
		out = append(out, ops[:n])
		ops = ops[n:]
	}
	return out
}

// dbFirewallWord is what a plan does to the firewall, in the one word the
// response and the audit entry carry.
func dbFirewallWord(ops []netsec.PlanOperation) string {
	adds := slices.ContainsFunc(ops, func(op netsec.PlanOperation) bool { return op.Op == "add" })
	removes := slices.ContainsFunc(ops, func(op netsec.PlanOperation) bool { return op.Op == "delete" })
	switch {
	case adds && removes:
		return "updated"
	case adds:
		return "opened"
	case removes:
		return "closed"
	}
	return "already"
}

// dbHostAddress is one address of this machine a port can be published on
// alone.
type dbHostAddress struct {
	Address   string `json:"address"`
	Interface string `json:"interface,omitempty"`
	// Network is who the address faces: tailnet, vpn, private (a LAN or a
	// second interface) or uplink (the private address a provider maps a
	// public one onto).
	Network string `json:"network"`
}

var bindableRank = map[netsec.Network]int{
	netsec.NetworkTailnet: 0, netsec.NetworkVPN: 1, netsec.NetworkPrivate: 2, netsec.NetworkUplink: 3,
}

// bindableAddresses are the host's addresses a database can be kept to one
// network by — a tailnet's, a VPN's, a LAN's — read by the grading the ports
// page uses. Loopback is This server only, and a public address is Anywhere by
// another name; a bridge's address is the containers', which reach the server
// over its own network already; a link-local address cannot be published
// without its zone.
func bindableAddresses(n netsec.HostNetwork) []dbHostAddress {
	out := []dbHostAddress{}
	seen := map[string]bool{}
	for _, a := range n.Addresses {
		address := a.IP.String()
		place := n.Place(address)
		if _, ok := bindableRank[place.Network]; !ok || place.Reach != netsec.ReachNetwork || seen[address] {
			continue
		}
		seen[address] = true
		out = append(out, dbHostAddress{Address: address, Interface: a.Interface, Network: string(place.Network)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := bindableRank[netsec.Network(a.Network)], bindableRank[netsec.Network(b.Network)]; ra != rb {
			return ra < rb
		}
		if a4, b4 := !strings.Contains(a.Address, ":"), !strings.Contains(b.Address, ":"); a4 != b4 {
			return a4
		}
		return a.Address < b.Address
	})
	return out
}

// bindableAddress finds an address among the host's, written as Docker will
// be handed it.
func bindableAddress(addresses []dbHostAddress, want string) (dbHostAddress, bool) {
	for _, a := range addresses {
		if sameAddress(a.Address, want) {
			return a, true
		}
	}
	return dbHostAddress{}, false
}
