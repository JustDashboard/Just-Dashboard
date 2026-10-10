package netsec

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Readings about this host: its listening sockets, how it reaches the
// internet per family, and the neighbours its kernel remembers. Each is a
// passive read; none sends a packet.

type listenRow struct {
	Protocol, Address, Port, Process string
}

var ssProcess = regexp.MustCompile(`\("([^"]+)",pid=(\d+)`)

func splitHostPortLoose(value string) (string, string) {
	i := strings.LastIndex(value, ":")
	if i < 0 {
		return value, ""
	}
	host, port := value[:i], value[i+1:]
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if j := strings.Index(host, "%"); j >= 0 {
		host = host[:j]
	}
	return host, port
}

// parseListeners reads `ss -tulnp` or `netstat -tulnp` output.
func parseListeners(out string) []listenRow {
	var rows []listenRow
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		proto := strings.ToLower(fields[0])
		var local, process string
		switch {
		case proto == "tcp" || proto == "udp":
			// ss: Netid State Recv-Q Send-Q Local Peer Process
			if len(fields) >= 6 && (fields[1] == "LISTEN" || fields[1] == "UNCONN") {
				local = fields[4]
				if len(fields) > 6 {
					process = strings.Join(fields[6:], " ")
				}
			} else {
				// netstat: Proto Recv-Q Send-Q Local Foreign [State] PID/Program
				local = fields[3]
				process = fields[len(fields)-1]
			}
		case proto == "tcp6" || proto == "udp6":
			local = fields[3]
			process = fields[len(fields)-1]
			proto = strings.TrimSuffix(proto, "6")
		default:
			continue
		}
		host, port := splitHostPortLoose(local)
		if port == "" || port == "*" {
			continue
		}
		names := []string{}
		for _, m := range ssProcess.FindAllStringSubmatch(process, -1) {
			names = append(names, m[1]+" ("+m[2]+")")
		}
		if len(names) == 0 && strings.Contains(process, "/") {
			pid, name, _ := strings.Cut(process, "/")
			names = append(names, name+" ("+pid+")")
		}
		rows = append(rows, listenRow{Protocol: proto, Address: host, Port: port, Process: strings.Join(sortedUnique(names), ", ")})
	}
	return rows
}

func listenScope(address string) (string, string) {
	if address == "*" || address == "0.0.0.0" || address == "::" {
		family := "ipv4"
		if address != "0.0.0.0" {
			family = "ipv6"
		}
		return "all addresses", family
	}
	a, err := netip.ParseAddr(address)
	if err != nil {
		return "specific address", "ipv4"
	}
	family := "ipv4"
	if a.Is6() && !a.Is4In6() {
		family = "ipv6"
	}
	if a.IsLoopback() {
		return "loopback only", family
	}
	return "this address", family
}

// portsLink opens the Ports page on one socket, keyed the way it keys rows.
func portsLink(proto, family, address, port string) string {
	if address == "*" {
		address = "::"
	}
	return "/proxy/ports?q=" + url.QueryEscape(":"+port) + "&socket=" + url.QueryEscape(proto+"-"+family+"-"+address+"-"+port)
}

func describeListeners(res *ProbeResult, out, tool string) {
	rows := parseListeners(out)
	res.fact("Source", tool+" socket table", BasisObserved)
	res.fact("Exposure", "bound addresses only; the firewall and provider decide who actually reaches each socket", BasisInferred)
	res.link("Ports: ownership and exposure", "/proxy/ports")
	table := ProbeTable{ID: "listeners", Title: "Listening sockets", Columns: []string{"Protocol", "Address", "Port", "Process", "Bound to"}}
	tcp, udp, beyond := 0, 0, 0
	sort.SliceStable(rows, func(i, j int) bool {
		a, _ := strconv.Atoi(rows[i].Port)
		b, _ := strconv.Atoi(rows[j].Port)
		if a != b {
			return a < b
		}
		return rows[i].Protocol < rows[j].Protocol
	})
	for _, r := range rows {
		scope, family := listenScope(r.Address)
		table.Rows = append(table.Rows, []string{r.Protocol, r.Address, r.Port, nonEmptyOr(r.Process, "unknown owner"), scope})
		table.RowLinks = append(table.RowLinks, portsLink(r.Protocol, family, r.Address, r.Port))
		res.Records = append(res.Records, r.Protocol+" "+net.JoinHostPort(r.Address, r.Port)+" "+r.Process)
		if r.Protocol == "tcp" {
			tcp++
		} else {
			udp++
		}
		if scope != "loopback only" {
			beyond++
		}
	}
	res.Tables = append(res.Tables, table)
	res.metric("tcp", "TCP listeners", float64(tcp), "")
	res.metric("udp", "UDP sockets", float64(udp), "")
	res.metric("beyond_loopback", "Bound beyond loopback", float64(beyond), "")
	res.Verdict = ProbeOK
	res.Summary = fmt.Sprintf("%d TCP and %d UDP sockets; %d are bound beyond loopback. Open a row in Ports to see its owner and exposure grade.", tcp, udp, beyond)
}

func addressScope(a netip.Addr) string {
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return "loopback"
	case a.IsLinkLocalUnicast():
		return "link-local"
	case a.Is4() && netip.MustParsePrefix("100.64.0.0/10").Contains(a):
		return "shared address space (carrier-grade NAT)"
	case a.IsPrivate() && a.Is4():
		return "private (RFC 1918)"
	case a.IsPrivate():
		return "unique local (ULA)"
	case a.IsGlobalUnicast():
		return "global"
	}
	return "other"
}

type egressFamily struct {
	Label     string
	Route     routeGet
	RouteOK   bool
	RouteErr  string
	Defaults  []string
	Scope     string
	SourceVal netip.Addr
}

func describeEgress(res *ProbeResult, families []egressFamily) {
	table := ProbeTable{ID: "egress", Title: "Egress by family", Columns: []string{"Family", "Route", "Next hop", "Interface", "Source", "Source scope", "Default routes"}}
	working := []string{}
	for _, f := range families {
		if !f.RouteOK {
			table.Rows = append(table.Rows, []string{f.Label, "no route", "", "", "", "", strconv.Itoa(len(f.Defaults))})
			res.fact(f.Label+" egress", "no route ("+nonEmptyOr(f.RouteErr, "the kernel selected none")+")", BasisObserved)
			continue
		}
		working = append(working, f.Label)
		next := nonEmptyOr(f.Route.Gateway, "directly connected")
		table.Rows = append(table.Rows, []string{f.Label, f.Route.Type, next, f.Route.Device, f.Route.Source, f.Scope, strconv.Itoa(len(f.Defaults))})
		res.fact(f.Label+" egress", fmt.Sprintf("dev %s via %s from %s (%s)", nonEmptyOr(f.Route.Device, "?"), next, nonEmptyOr(f.Route.Source, "?"), nonEmptyOr(f.Scope, "unknown scope")), BasisObserved)
		res.Records = append(res.Records, strings.ToLower(f.Label)+" dev "+f.Route.Device+" via "+next)
		switch {
		case strings.HasPrefix(f.Scope, "private"), strings.HasPrefix(f.Scope, "shared"), strings.HasPrefix(f.Scope, "unique local"):
			res.finding(strings.ToLower(f.Label)+"-nat", "notice", f.Label+" leaves from a "+f.Scope+" address",
				"An upstream NAT almost certainly translates "+f.Route.Source+". The public address other hosts see is not visible from this host; measure it from an external vantage.", "Upstream router or provider")
		case f.Scope == "global":
			res.fact(f.Label+" public address", f.Route.Source+" is globally routable; other hosts see it unless an upstream device rewrites it, which this host cannot observe", BasisInferred)
		}
	}
	res.Tables = append(res.Tables, table)
	res.link("Measure the public address from an external vantage", "/network/external")
	res.Limitations = append(res.Limitations, "Only the kernel routing table was queried; no internet request was sent. A selected source address is not proof of the address a provider's NAT presents.")
	switch len(working) {
	case 0:
		res.OK, res.Verdict = false, ProbeFailed
		res.Summary = "Neither IPv4 nor IPv6 has a route to the internet."
	case 1:
		res.OK, res.Verdict = true, ProbeOK
		res.Summary = working[0] + " only: the other family has no route from this host."
	default:
		res.OK, res.Verdict = true, ProbeOK
		res.Summary = "IPv4 and IPv6 both have independent routes to the internet."
	}
	if len(res.Findings) > 0 && res.OK {
		res.Verdict = ProbeFindings
	}
}

type neighbour struct {
	Address, Family, Device, MAC, State string
	Router                              bool
}

var neighbourStates = map[string]string{
	"REACHABLE":  "confirmed reachable recently",
	"STALE":      "known, not confirmed recently; checked again on next use",
	"DELAY":      "being re-confirmed",
	"PROBE":      "being re-confirmed",
	"FAILED":     "nothing answered resolution for this address",
	"INCOMPLETE": "resolution started, no answer yet",
	"PERMANENT":  "static entry set by an administrator",
	"NOARP":      "the interface needs no address resolution",
	"NONE":       "placeholder entry",
}

func parseNeighbours(out string) []neighbour {
	var rows []neighbour
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		a, err := netip.ParseAddr(fields[0])
		if err != nil {
			continue
		}
		n := neighbour{Address: a.String(), Family: "IPv4"}
		if a.Is6() {
			n.Family = "IPv6"
		}
		for i := 1; i < len(fields); i++ {
			switch fields[i] {
			case "dev":
				if i+1 < len(fields) {
					n.Device, i = fields[i+1], i+1
				}
			case "lladdr":
				if i+1 < len(fields) {
					n.MAC, i = strings.ToLower(fields[i+1]), i+1
				}
			case "router":
				n.Router = true
			default:
				if _, ok := neighbourStates[fields[i]]; ok {
					n.State = fields[i]
				}
			}
		}
		rows = append(rows, n)
	}
	return rows
}

// lanInterfaceAddrs names an interface's addresses so a neighbour row can say
// which local network it sits on.
var lanInterfaceAddrs = func(name string) []string {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}

func interfaceRole(name string) string {
	switch {
	case strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-"):
		return "Docker bridge"
	case strings.HasPrefix(name, "veth"):
		return "container link"
	case strings.HasPrefix(name, "wg") || strings.HasPrefix(name, "tailscale") || strings.HasPrefix(name, "tun"):
		return "tunnel"
	case name == "lo":
		return "loopback"
	}
	return ""
}

func describeNeighbours(res *ProbeResult, out string) {
	rows := parseNeighbours(out)
	res.fact("Method", "passive read of the kernel's ARP/NDP cache; no packets were sent", BasisObserved)
	res.fact("Not shown", "devices that have not exchanged traffic with this host recently; this is not a scan of the subnet", BasisConfigured)
	table := ProbeTable{ID: "neighbours", Title: "Neighbour cache", Columns: []string{"Address", "Family", "Interface", "MAC", "State", "Meaning"}}
	type ifaceCount struct{ total, reachable, stale, failed int }
	perIface := map[string]*ifaceCount{}
	counts := map[string]int{}
	for _, n := range rows {
		state := nonEmptyOr(n.State, "NONE")
		meaning := neighbourStates[state]
		if n.Router {
			meaning += "; advertises itself as a router"
		}
		table.Rows = append(table.Rows, []string{n.Address, n.Family, n.Device, nonEmptyOr(n.MAC, "none"), state, meaning})
		res.Records = append(res.Records, n.Address+" "+n.Device+" "+n.MAC+" "+state)
		c := perIface[n.Device]
		if c == nil {
			c = &ifaceCount{}
			perIface[n.Device] = c
		}
		c.total++
		switch state {
		case "REACHABLE", "PERMANENT", "NOARP":
			c.reachable++
			counts["reachable"]++
		case "STALE", "DELAY", "PROBE":
			c.stale++
			counts["stale"]++
		case "FAILED", "INCOMPLETE":
			c.failed++
			counts["failed"]++
		}
	}
	res.Tables = append(res.Tables, table)
	names := make([]string, 0, len(perIface))
	for name := range perIface {
		names = append(names, name)
	}
	sort.Strings(names)
	ifaces := ProbeTable{ID: "interfaces", Title: "By interface", Columns: []string{"Interface", "Role", "Local addresses", "Entries", "Confirmed", "Stale", "Failed"}}
	for _, name := range names {
		c := perIface[name]
		ifaces.Rows = append(ifaces.Rows, []string{name, interfaceRole(name), strings.Join(lanInterfaceAddrs(name), ", "), strconv.Itoa(c.total), strconv.Itoa(c.reachable), strconv.Itoa(c.stale), strconv.Itoa(c.failed)})
	}
	if len(ifaces.Rows) > 0 {
		res.Tables = append(res.Tables, ifaces)
	}
	res.metric("entries", "Cache entries", float64(len(rows)), "")
	res.metric("confirmed", "Confirmed", float64(counts["reachable"]), "")
	res.metric("stale", "Stale", float64(counts["stale"]), "")
	res.metric("failed", "Failed", float64(counts["failed"]), "")
	res.Verdict = ProbeOK
	res.Summary = fmt.Sprintf("%d cached neighbours on %d interfaces: %d confirmed, %d stale, %d failed.", len(rows), len(perIface), counts["reachable"], counts["stale"], counts["failed"])
	if len(rows) == 0 {
		res.Summary = "The neighbour cache is empty; this host has not resolved any neighbour recently."
	}
}
