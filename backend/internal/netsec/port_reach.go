package netsec

import (
	"net"
	"strconv"
	"strings"
)

type ExposedPort struct {
	Port     uint32
	Protocol string
	Address  string
	// Process names the socket's holder: docker-proxy's is a port Docker
	// publishes, which a firewall's inbound default does not hold.
	Process string
	// Published is a port Docker publishes whatever holds it: docker-proxy
	// where this account cannot see the holder, dockerd reserving it, or
	// no socket at all where the userland proxy is off.
	Published bool
	// Exposed is any bind but loopback. How far it reaches — every
	// interface, a public address, a tailnet — is read from Address against
	// AssessInput.Network, by the same HostNetwork.Place the ports page's
	// listing is graded with, so the two cannot disagree.
	Exposed bool
	// Dashboard marks one of the dashboard's own sockets other than its
	// Caddy's: the backend and the web app, which are meant to answer on
	// loopback alone.
	Dashboard bool
}

type PortGrade struct {
	// Level is "critical" or "warning", or empty where the posture raises
	// nothing: a port with no danger in the catalogue, a loopback bind, or a
	// danger only strangers pose on a bind the internet cannot reach.
	Level string
	// InboundDefault is the firewall's inbound default when it is what holds
	// Level to a warning, and empty otherwise.
	InboundDefault string
	// PastFirewall says why a firewall refusing inbound by default does not
	// hold the socket: Docker publishes it, or a rule admits it. Empty when
	// the default holds it, or when there is no such default to hold it.
	PastFirewall PastFirewall
	// FirewallRule is the number of the rule that admits it, for
	// PastFirewallRule.
	FirewallRule int
}

// PastFirewall is how a socket's traffic gets by a firewall's inbound default.
type PastFirewall string

const (
	// PastFirewallDocker is a port Docker publishes, with NAT rules that
	// forward the traffic before the input chain the inbound default
	// belongs to is reached, so the default never sees it.
	PastFirewallDocker PastFirewall = "docker"
	// PastFirewallRule is a port a rule admits from anywhere, which a
	// connection meets before it falls through to the default.
	PastFirewallRule PastFirewall = "rule"
)

// GradePort levels one socket by the rules Assess levels a port's finding
// by. A port bound several times is one finding, at the highest of its
// sockets' levels.
func GradePort(l ExposedPort, network HostNetwork, firewall *FirewallStatus) PortGrade {
	_, reach, ok := network.dangerAt(l)
	if !ok {
		return PortGrade{}
	}
	return portLevel(l, reach, network, firewall)
}

// dangerAt is the catalogue's entry for a socket and how far its address
// reaches, when the posture raises a finding for it.
func (n HostNetwork) dangerAt(l ExposedPort) (ServicePreset, bindReach, bool) {
	if !l.Exposed {
		return ServicePreset{}, bindReach{}, false
	}
	preset, ok := ServiceOf(l)
	if !ok || preset.Danger == "" {
		return ServicePreset{}, bindReach{}, false
	}
	reach := n.reachOf(l.Address)
	if !reach.matters(preset) {
		return ServicePreset{}, bindReach{}, false
	}
	return preset, reach, true
}

// portLevel is a dangerous port's level at a reach. A socket only one
// network can reach is worth knowing about, not an emergency: a database for
// the containers on a bridge, or for the operator's own devices on a
// tailnet, is often the design. A firewall refusing inbound by default may
// be refusing it anyway, and saying so is the difference between a finding
// and a false alarm — but only where the default is what the socket's
// traffic meets. A port Docker publishes is forwarded before it, and a port
// a rule admits from anywhere is let in before it; crediting the default
// there drew amber a database the internet can reach. One whose default
// could not be read is not counted on.
func portLevel(l ExposedPort, reach bindReach, network HostNetwork, firewall *FirewallStatus) PortGrade {
	grade := PortGrade{Level: "warning"}
	if reach.class.InternetFacing() {
		grade.Level = "critical"
	}
	if firewall == nil || !firewall.Enabled || firewall.Policy.Incoming == "" || firewall.Policy.Incoming == "allow" {
		return grade
	}
	if l.Process == "docker-proxy" || l.Published {
		grade.PastFirewall = PastFirewallDocker
		return grade
	}
	if rule, ok := admittingRule(firewall, network, l); ok {
		grade.PastFirewall, grade.FirewallRule = PastFirewallRule, rule
		return grade
	}
	return PortGrade{Level: "warning", InboundDefault: firewall.Policy.Incoming}
}

// admittingRule is the number of the inbound rule that lets a connection
// from anywhere reach the socket's port before the inbound default refuses
// it. ufw and iptables stop at the first rule that matches, so a rule
// refusing the port from anywhere ends the search.
//
// A rule limited to one destination admits the socket when that is the
// socket's own address, or one the internet reaches — a public address, or
// the private uplink a provider maps one onto. One for a tailnet's or a
// bridge's address, like one limited to an interface, still leaves the
// internet to the default, which is how a database on every interface is
// kept to a tailnet.
func admittingRule(firewall *FirewallStatus, network HostNetwork, l ExposedPort) (int, bool) {
	port := strconv.FormatUint(uint64(l.Port), 10)
	for _, r := range firewall.Rules {
		if !inboundRule(r, firewall.Backend) || !isAnywhere(r.From) || !protocolCovers(r.Protocol, l.Protocol) {
			continue
		}
		destination, ports, ok := ruleTarget(r, firewall.Backend)
		if !ok || ports != "" && !portInSpec(port, ports) {
			continue
		}
		toSocket := destination == "" || destinationHolds(destination, l.Address)
		switch strings.ToUpper(r.Action) {
		case "ALLOW", "LIMIT", "ACCEPT":
			if toSocket || network.internetReaches(destination) {
				return r.Number, true
			}
		case "DENY", "REJECT", "DROP":
			// iptables' listing leaves out the interface a rule is limited
			// to, so its refusals end nothing.
			if toSocket && firewall.Backend != BackendIPTables {
				return 0, false
			}
		}
	}
	return 0, false
}

// inboundRule is a rule on traffic coming in to this host: ufw's and
// firewalld's IN, and for iptables the INPUT chain the default belongs to.
// Rules in the chains INPUT jumps to are not followed.
func inboundRule(r Rule, backend Backend) bool {
	if backend == BackendIPTables {
		return r.Direction == "INPUT"
	}
	return r.Direction == "" || strings.EqualFold(r.Direction, "IN")
}

func protocolCovers(ruleProtocol, protocol string) bool {
	return ruleProtocol == "" || strings.EqualFold(ruleProtocol, "all") || strings.EqualFold(ruleProtocol, protocol)
}

// ruleTarget reads what a rule is about: the destination address it names,
// empty for any, and its ports, empty for every port. ok is false for a rule
// whose target cannot be read from its listing: a ufw application profile,
// a rule limited to an interface, and an iptables rule that names no port,
// whose other matches — a state, an input interface — are not in its fields.
func ruleTarget(r Rule, backend Backend) (destination, ports string, ok bool) {
	switch backend {
	case BackendIPTables:
		ports = iptablesPort(r.Raw)
		return anywhereAsEmpty(r.To), ports, ports != ""
	case BackendFirewalld:
		// A zone's ports and services, and the rich rules read here, name
		// no destination.
		if r.Port != "" {
			return "", r.Port, true
		}
		if r.Service != "" {
			ports := []string{}
			for _, preset := range ServiceCatalogue {
				if preset.Firewalld == r.Service {
					ports = append(ports, preset.Port)
				}
			}
			return "", strings.Join(ports, ","), len(ports) > 0
		}
		return "", "", isAnywhere(r.To)
	}
	to := strings.TrimSpace(r.To)
	// A rule scoped to one device does not open the port everywhere.
	if r.Interface != "" || strings.Contains(to, " on ") {
		return "", "", false
	}
	if r.Port != "" {
		// ufw prints a destination address in front of the port.
		if i := strings.LastIndexByte(to, ' '); i >= 0 {
			return anywhereAsEmpty(to[:i]), r.Port, true
		}
		return "", r.Port, true
	}
	if isAnywhere(to) {
		return "", "", true
	}
	return to, "", isAddress(to)
}

func anywhereAsEmpty(address string) string {
	if isAnywhere(address) {
		return ""
	}
	return strings.TrimSpace(address)
}

func isAddress(s string) bool {
	if _, _, err := net.ParseCIDR(s); err == nil {
		return true
	}
	return net.ParseIP(s) != nil
}

// destinationHolds says a rule's destination address or network holds a
// socket's address. A socket on every interface is held by no one address.
func destinationHolds(destination, address string) bool {
	ip := net.ParseIP(address)
	if ip == nil || ip.IsUnspecified() {
		return false
	}
	if _, cidr, err := net.ParseCIDR(destination); err == nil {
		return cidr.Contains(ip)
	}
	return ip.Equal(net.ParseIP(destination))
}

// internetReaches says the internet reaches a rule's destination: a public
// address, or a private one a provider may map a public address onto.
func (n HostNetwork) internetReaches(destination string) bool {
	if destination == "" {
		return true
	}
	address, _, _ := strings.Cut(destination, "/")
	if net.ParseIP(address) == nil {
		return false
	}
	reach := n.reachOf(address)
	return reach.class.InternetFacing() || reach.forwarded
}

// portInSpec says a port is in a rule's port field: one port, a list, or a
// range written with a colon (ufw, iptables) or a dash (firewalld).
func portInSpec(port, spec string) bool {
	n, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, ":")
		if !isRange {
			lo, hi, isRange = strings.Cut(part, "-")
		}
		if !isRange {
			if part == port {
				return true
			}
			continue
		}
		from, err1 := strconv.Atoi(strings.TrimSpace(lo))
		to, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 == nil && err2 == nil && from <= n && n <= to {
			return true
		}
	}
	return false
}

func addressLabel(addr string) string {
	if addr == "" || addr == "0.0.0.0" || addr == "::" || addr == "*" {
		return "every interface"
	}
	return addr
}
