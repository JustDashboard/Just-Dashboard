package netpath

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

// PublishedRequest names one published port of one container: the inbound
// question "what stands between the outside and this container's port".
type PublishedRequest struct {
	ContainerID string `json:"containerId"`
	HostPort    int    `json:"hostPort"`
	Protocol    string `json:"protocol"`
	Family      string `json:"family"`
}

// Publication is Docker's own record of the binding and where the
// container answers now.
type Publication struct {
	Container     string
	HostIP        string
	HostPort      int
	ContainerPort int
	Protocol      string
	Addresses     []PublishedAddress
}

// PublishedAddress is the container's address on one network.
type PublishedAddress struct {
	Network  string
	IPv4     string
	IPv6     string
	Bridge   string
	Internal bool
}

// ExternalMeasurement is one retained measurement an enrolled source made of
// this host port, as the external-check owner kept it.
type ExternalMeasurement struct {
	Source    string
	Placement string
	Address   string
	State     string
	Basis     string
	At        time.Time
	// Local is whether the measured address is on this host; one that is not
	// reached the host through a translation this dashboard does not see.
	Local bool
}

// PublishedProviders are the owners the inbound path is assembled from.
type PublishedProviders struct {
	Publication func(context.Context) (Publication, error)
	Chains      func(context.Context, string) netsec.DockerChains
	Firewall    func(context.Context) (*netsec.FirewallStatus, error)
	Gateway     func(context.Context) (*netx.GatewayView, error)
	Owners      func(context.Context) (OwnerSnapshot, error)
	External    func(context.Context, int) ([]ExternalMeasurement, error)
	// PublicAddress is an address on one of this host's interfaces that is
	// not private, empty where it has none: a host without one is reached
	// through a provider's translation.
	PublicAddress func(context.Context) (string, error)
}

// ValidatePublished checks the closed request shape.
func ValidatePublished(req PublishedRequest) (PublishedRequest, error) {
	if !containerID.MatchString(req.ContainerID) {
		return req, fmt.Errorf("a container needs its full inventory ID")
	}
	if req.HostPort < 1 || req.HostPort > 65535 || (req.Protocol != "tcp" && req.Protocol != "udp") || (req.Family != "inet" && req.Family != "inet6") {
		return req, fmt.Errorf("provide a published host port from 1 to 65535, TCP or UDP, and IPv4 or IPv6")
	}
	return req, nil
}

// InvestigatePublished joins Docker's publication, its NAT translation, the
// forwarded leg's filters, the dashboard's gateway, the proxy, provider
// policy and any retained external measurement into one inbound path. Every
// step is a read; nothing is sent to the port.
func InvestigatePublished(ctx context.Context, request PublishedRequest, p PublishedProviders) (*Result, error) {
	req, err := ValidatePublished(request)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := &Result{
		Request:   Request{SourceKind: "external", ContainerID: req.ContainerID, Family: req.Family, Protocol: req.Protocol, Port: req.HostPort},
		StartedAt: time.Now().UTC(), Addresses: []string{}, Evidence: []Evidence{},
	}
	defer func() { result.EndedAt = time.Now().UTC() }()
	result.Scope = Scope{Vantage: "published_port", Source: "Outside this host", Family: req.Family, Protocol: req.Protocol, Port: req.HostPort,
		Limitations: []string{
			"Each layer is a snapshot read in sequence; no packet was sent or traced.",
			"Connection tracking, rule counters, policy routing and the order of other nftables tables at the same hooks are not evaluated.",
			"Provider firewalls, security groups, upstream NAT and load balancers are not visible to this dashboard.",
		}}

	publication := evidence("publication", "Docker publication", "Docker Engine", "Docker", "/docker")
	var pub Publication
	known := false
	if p.Publication == nil {
		publication.Summary = "The Docker owner is unavailable."
	} else if pub, err = p.Publication(ctx); err != nil {
		publication.failure(err)
	} else {
		known = true
		publication.Basis, publication.State = Observed, "observed"
		publication.Summary = fmt.Sprintf("Docker publishes %s port %d of %s on host port %d.", strings.ToUpper(pub.Protocol), pub.ContainerPort, pub.Container, pub.HostPort)
		publication.Facts = append(publication.Facts, Fact{"Container", pub.Container}, Fact{"Host binding", bindingOf(pub.HostIP, pub.HostPort)}, Fact{"Container port", fmt.Sprintf("%d/%s", pub.ContainerPort, pub.Protocol)})
		for _, a := range pub.Addresses {
			address := a.IPv4
			if req.Family == "inet6" {
				address = a.IPv6
			}
			publication.Facts = append(publication.Facts, Fact{"Address on " + a.Network, nonEmpty(address, "none in this family")})
			if address != "" && len(result.Addresses) < 8 {
				result.Addresses = append(result.Addresses, address)
			}
		}
		if bindingScope(pub.HostIP) == "loopback" {
			publication.Limitations = append(publication.Limitations, "A loopback binding is reachable only from this host; the layers after NAT describe local traffic and the proxy in front of it.")
		}
		result.Scope.Target = pub.Container
	}
	result.Evidence = append(result.Evidence, publication)
	if !known {
		for _, item := range []struct{ id, title, owner, path string }{{"listener", "Host socket", "Listener owner", "/proxy/ports"}, {"dnat", "Docker NAT", "Docker", "/docker"}, {"docker-user", "DOCKER-USER", "Operator rules", "/network/firewall"}, {"firewall", "Host firewall (forwarded leg)", "Host firewall", "/network/firewall"}, {"gateway", "Dashboard gateway", "Gateway", "/network/gateway"}, {"proxy", "Proxy", "Proxy owner", "/proxy"}, {"provider", "Provider policy", "Provider", ""}, {"external", "External measurement", "External checks", "/network/external"}} {
			e := evidence(item.id, item.title, "published port", item.owner, item.path)
			e.State, e.Summary = "skipped", "Docker's record of this publication is unavailable; this layer was not read."
			result.Evidence = append(result.Evidence, e)
		}
		result.Comparison = "The publication could not be read, so no layer of its path was assembled."
		return result, nil
	}

	listener := evidence("listener", "Host socket", "host sockets", "Listener owner", "/proxy/ports")
	proxy := evidence("proxy", "Proxy", "configured proxy", "Proxy owner", "/proxy")
	proxy.Summary = "No configured proxy site or stream was associated with this host port."
	if p.Owners != nil {
		snapshot, err := p.Owners(ctx)
		if err != nil {
			listener.failure(err)
			proxy.failure(err)
		} else {
			listener.Limitations = snapshot.Limits
			for _, l := range snapshot.Listeners {
				if int(l.Port) != req.HostPort || l.Protocol != req.Protocol || l.Container == nil || !strings.HasPrefix(req.ContainerID, l.Container.ID) {
					continue
				}
				listener.Basis, listener.State = Observed, "observed"
				if l.Source == "docker-nat" {
					listener.Summary = "No process holds this port: Docker publishes it through NAT alone, so nothing answers it on the host before translation."
				} else {
					listener.Summary = fmt.Sprintf("%s holds the host socket for Docker's publication.", nonEmpty(l.Process, "A process this account cannot see"))
				}
				listener.Facts = append(listener.Facts, Fact{"Address", bindingOf(l.Address, int(l.Port))}, Fact{"Holder", nonEmpty(l.Process, "unknown")}, Fact{"Reach", nonEmpty(l.Reach, "unknown")})
				for _, site := range l.Routes {
					proxy.Basis, proxy.State = Modeled, "modeled"
					proxy.Summary = "Configured proxy sites forward to this host port. Whether a request reaches them is not measured here."
					proxy.Facts = append(proxy.Facts, Fact{"Configured site", strings.TrimSpace(site.Site + " " + site.ServerName)})
				}
				if l.Stream != "" {
					proxy.Basis, proxy.State = Modeled, "modeled"
					proxy.Facts = append(proxy.Facts, Fact{"Stream", l.Stream})
				}
			}
			if listener.Basis == Unknown {
				listener.Basis, listener.State, listener.Summary = Observed, "absent", "No socket or NAT-only publication for this binding was in the listing; the container may have restarted since."
			}
			if proxy.Basis == Unknown {
				proxy.Basis, proxy.State = Observed, "none"
			}
		}
	}
	result.Evidence = append(result.Evidence, listener)

	dnat := evidence("dnat", "Docker NAT", "nat table, DOCKER chain", "Docker", "/docker")
	user := evidence("docker-user", "DOCKER-USER", "filter table, DOCKER-USER chain", "Operator rules", "/network/firewall")
	var target netip.Addr
	var chains netsec.DockerChains
	chainsRead := false
	if p.Chains == nil {
		dnat.Summary, user.Summary = "Docker's chains could not be read.", "Docker's chains could not be read."
	} else {
		chains, chainsRead = p.Chains(ctx, req.Family), true
		switch {
		case chains.NATError != "":
			dnat.failure(fmt.Errorf("the DOCKER nat chain could not be listed (%s); an Engine using its nftables backend keeps its rules in its own table, which this adapter does not read", strings.TrimSpace(chains.NATError)))
		default:
			hostIP := pub.HostIP
			if hostIP == "0.0.0.0" || hostIP == "::" {
				hostIP = ""
			}
			found := false
			for _, rule := range chains.NAT {
				if !rule.Covers(req.Protocol, hostIP, req.HostPort) {
					continue
				}
				found = true
				dnat.Basis, dnat.State = Observed, "observed"
				destination := rule.To
				addr, err := netip.ParseAddrPort(destination)
				if err != nil {
					if a, e := netip.ParseAddr(strings.Trim(strings.Split(destination, ":")[0], "[]")); e == nil {
						addr = netip.AddrPortFrom(a, 0)
					}
				}
				target = addr.Addr()
				dnat.Summary = fmt.Sprintf("Docker's rule translates host port %d to %s.", req.HostPort, destination)
				dnat.Facts = append(dnat.Facts, Fact{"Translates to", destination}, Fact{"Matches address", nonEmpty(rule.HostIP, "every address")}, Fact{"Except from", nonEmpty(rule.ExceptInterface, "none")}, Fact{"Rule", rule.Raw})
				if target.IsValid() && !containsAddress(result.Addresses, target) {
					dnat.State = "mismatch"
					dnat.Summary = fmt.Sprintf("Docker's rule translates host port %d to %s, which is not the container's current address; the Engine may still be reconciling, or the rule belongs to an older container.", req.HostPort, destination)
				}
				break
			}
			if !found {
				dnat.Basis, dnat.State = Observed, "absent"
				dnat.Summary = "No DNAT rule in the DOCKER chain matches this binding. With the userland proxy, local traffic can still be relayed by docker-proxy; inbound traffic from other hosts is not translated."
			}
		}
		switch {
		case chains.UserError != "":
			user.failure(fmt.Errorf("DOCKER-USER could not be listed: %s", strings.TrimSpace(chains.UserError)))
		case len(operatorRules(chains.User)) == 0:
			user.Basis, user.State = Observed, "empty"
			user.Summary = "DOCKER-USER holds no rule of the operator's, so nothing there filters connections forwarded to containers."
		default:
			rules := operatorRules(chains.User)
			user.Basis, user.State = Unknown, "rules_present"
			user.Summary = fmt.Sprintf("DOCKER-USER holds %d rule(s) that see every connection forwarded to a container before Docker's own; whether they admit this one is not evaluated.", len(rules))
			for _, rule := range rules[:min(16, len(rules))] {
				user.Facts = append(user.Facts, Fact{"Rule", rule})
			}
			if len(rules) > 16 {
				user.Limitations = append(user.Limitations, "Only the first 16 rules are listed.")
			}
		}
		if chains.ForwardError == "" && chains.ForwardPolicy != "" {
			user.Facts = append(user.Facts, Fact{"FORWARD policy", chains.ForwardPolicy})
		}
	}
	result.Evidence = append(result.Evidence, dnat, user)
	if target.IsValid() {
		result.Scope.Address = target.String()
	} else if len(result.Addresses) > 0 {
		result.Scope.Address = result.Addresses[0]
	}

	firewall := evidence("firewall", "Host firewall (forwarded leg)", "host adapter, after translation", "Host firewall", "/network/firewall")
	policy := netsec.TrafficPolicy{Verdict: "unknown"}
	forwarded := ""
	if p.Firewall != nil {
		status, err := p.Firewall(ctx)
		if err != nil {
			firewall.failure(err)
		} else if status != nil {
			destination := result.Scope.Address
			policy = netsec.ModelTrafficPolicy(status, "", destination, req.Protocol, pub.ContainerPort, "fwd")
			firewall.Summary, firewall.Limitations = policy.Summary, policy.Limitations
			if policy.Verdict != "unknown" {
				firewall.Basis, firewall.State = Modeled, "modeled"
			}
			forwarded = policy.Verdict
			firewall.Owner = string(status.Backend)
			firewall.Facts = append(firewall.Facts, Fact{"Adapter", string(status.Backend)}, Fact{"Direction", "forwarded, to the container's address and port"}, Fact{"Adapter prediction", policy.Verdict}, Fact{"Inbound default", nonEmpty(status.Policy.Incoming, "unknown")})
			if status.Backend == netsec.BackendUFW || status.Backend == netsec.BackendIPTables {
				firewall.Limitations = append(firewall.Limitations, "Docker translates the port before INPUT, so inbound rules and the inbound default do not apply to it; only forwarded-traffic (route) rules do.")
			}
		}
	}
	// FORWARD decides the translated connection in its own order. Where
	// Docker's chains come before ufw's and Docker's rule admits this
	// container's port, ufw's route rules and routed default are never
	// reached for it, whatever the adapter alone would predict.
	if chainsRead && chains.ForwardError == "" && chains.FilterError == "" {
		dockerFirst, ahead := chains.ForwardOrder()
		accept := chains.DockerAccept(req.Protocol, result.Scope.Address, pub.ContainerPort)
		firewall.Facts = append(firewall.Facts, Fact{"FORWARD order", forwardJumps(chains.Forward)})
		switch {
		case dockerFirst && accept != "":
			firewall.Basis, firewall.State = Modeled, "docker_admits"
			firewall.Summary = "Docker's chains come before the firewall adapter's in FORWARD, and Docker's own rule admits this container's port, so the adapter's route rules and routed default are not reached for it."
			firewall.Facts = append(firewall.Facts, Fact{"Docker's accept", accept})
			forwarded = "admitted by Docker's rule ahead of the firewall adapter"
			// Rules FORWARD meets before Docker's accept can still drop the
			// connection; they are listed, not evaluated, so the verdict
			// says it depends on them.
			earlier := append([]string{}, ahead...)
			if len(operatorRules(chains.User)) > 0 {
				earlier = append(earlier, "DOCKER-USER")
			}
			if len(earlier) > 0 {
				firewall.State = "docker_admits_unless_earlier"
				verb := "comes first and could still drop it; it is not evaluated."
				if len(earlier) > 1 {
					verb = "come first and could still drop it; they are not evaluated."
				}
				firewall.Summary += " " + strings.Join(earlier, " and ") + " " + verb
				forwarded += ", unless " + strings.Join(earlier, " or ") + " drops it first (not evaluated)"
			}
		case dockerFirst:
			firewall.Limitations = append(firewall.Limitations, "Docker's chains come first in FORWARD, but no Docker accept for this container's port was listed; the connection falls through to the chains after them.")
		}
		for _, chain := range ahead {
			firewall.Limitations = append(firewall.Limitations, fmt.Sprintf("FORWARD consults %s before Docker's chains; its rules are not evaluated here.", chain))
		}
	} else if chainsRead {
		firewall.Limitations = append(firewall.Limitations, "FORWARD or Docker's filter chain could not be listed, so whether Docker's accept comes before the adapter's chains is unknown.")
	}
	result.Evidence = append(result.Evidence, firewall)

	gateway := evidence("gateway", "Dashboard gateway", "owned gateway and forward-hook layers", "Gateway", "/network/gateway")
	if p.Gateway != nil {
		view, err := p.Gateway(ctx)
		if err != nil {
			gateway.failure(err)
		} else if view != nil {
			gateway.Basis, gateway.State = Observed, "observed"
			gateway.Summary = "The dashboard's gateway translates nothing on this port, and no other forward-hook table was seen that could drop it."
			for _, f := range view.Forwards {
				if f.Enabled && (f.Protocol == req.Protocol || f.Protocol == "both") && portInRange(req.HostPort, f.Ports) {
					gateway.State = "competing"
					gateway.Summary = "A gateway forward of the dashboard's also translates this host port. Which translation a connection meets depends on hook priorities this adapter does not order."
					gateway.Facts = append(gateway.Facts, Fact{"Gateway forward", fmt.Sprintf("%s · ingress %s · to %s:%s", f.Name, f.Interface, f.Target, f.TargetPort)})
				}
			}
			for _, layer := range view.Capability.Layers {
				if layer.Hook != "forward" || layer.Status == "owned" || layer.Status == "checked" || layer.Status == "admitted" {
					continue
				}
				gateway.Basis = Unknown
				if gateway.State == "observed" {
					gateway.State = "unknown"
					gateway.Summary = "Another nftables table filters forwarded traffic with rules this adapter does not evaluate; it can drop this connection."
				}
				gateway.Facts = append(gateway.Facts, Fact{"Forward-hook layer", fmt.Sprintf("%s %s %s · policy %s · %s", layer.Family, layer.Table, layer.Chain, nonEmpty(layer.Policy, "unknown"), layer.Status)})
			}
			gateway.Limitations = append(gateway.Limitations, view.Capability.UnknownLayers...)
		}
	}
	result.Evidence = append(result.Evidence, gateway, proxy)

	provider := evidence("provider", "Provider policy", "upstream of this host", "Provider", "")
	provider.Summary = "No provider adapter is configured. Security groups, provider firewalls, upstream NAT and load balancers are not visible, so whether the outside reaches this host port is not known from here."
	if p.PublicAddress != nil {
		address, err := p.PublicAddress(ctx)
		switch {
		case err != nil:
			provider.Facts = append(provider.Facts, Fact{"Public address on this host", "unreadable: " + err.Error()})
		case address == "":
			provider.Facts = append(provider.Facts, Fact{"Public address on this host", "none — inbound traffic reaches it through a provider's translation"})
		default:
			provider.Facts = append(provider.Facts, Fact{"Public address on this host", address})
		}
	}
	result.Evidence = append(result.Evidence, provider)

	external := evidence("external", "External measurement", "enrolled external sources", "External checks", "/network/external")
	external.Summary = "No enrolled source has a retained measurement of this host port."
	measured := ""
	if p.External != nil {
		measurements, err := p.External(ctx, req.HostPort)
		if err != nil {
			external.failure(err)
		}
		for _, m := range measurements {
			if m.Basis != "measured" {
				continue
			}
			external.Basis, external.State = Measured, m.State
			where := "an address on this host"
			if !m.Local {
				where = "an address not on this host, through a translation not traced here"
			}
			external.Facts = append(external.Facts, Fact{m.Source + " (" + m.Placement + ")", fmt.Sprintf("%s to %s, %s, at %s", m.State, m.Address, where, m.At.UTC().Format(time.RFC3339))})
			if measured == "" {
				measured = fmt.Sprintf("%s from %s", m.State, m.Source)
			}
		}
		if external.Basis == Measured {
			external.Summary = "Enrolled sources measured a TCP connection to this host port. A connection proves that one source reached it at that time, not that every source can."
		}
	}
	result.Evidence = append(result.Evidence, external)

	result.Comparison = fmt.Sprintf("Docker NAT: %s. Forwarded leg: %s. Provider policy: unknown. External measurement: %s.",
		dnat.State, nonEmpty(forwarded, "unknown"), nonEmpty(measured, "none retained"))
	return result, nil
}

// forwardJumps is FORWARD's chain of jumps as one line, policy-ordered.
func forwardJumps(rules []string) string {
	jumps := []string{}
	for _, rule := range rules {
		fields := strings.Fields(rule)
		for i, f := range fields {
			if f == "-j" && i+1 < len(fields) {
				jumps = append(jumps, fields[i+1])
			}
		}
	}
	return nonEmpty(strings.Join(jumps, " → "), "no jumps")
}

// operatorRules drops Docker's own default RETURN from DOCKER-USER.
func operatorRules(rules []string) []string {
	out := []string{}
	for _, rule := range rules {
		if strings.TrimSpace(rule) == "-A DOCKER-USER -j RETURN" {
			continue
		}
		out = append(out, rule)
	}
	return out
}

func containsAddress(addresses []string, target netip.Addr) bool {
	for _, value := range addresses {
		if a, err := netip.ParseAddr(strings.Split(value, "/")[0]); err == nil && a.Unmap() == target.Unmap() {
			return true
		}
	}
	return false
}

func bindingOf(ip string, port int) string {
	if ip == "" {
		ip = "0.0.0.0"
	}
	if strings.Contains(ip, ":") {
		return "[" + ip + "]:" + strconv.Itoa(port)
	}
	return ip + ":" + strconv.Itoa(port)
}

func bindingScope(ip string) string {
	if a, err := netip.ParseAddr(ip); err == nil && a.IsLoopback() {
		return "loopback"
	}
	return "network"
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
