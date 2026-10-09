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

// Investigate assembles bounded owner reads. The tuple is pinned after native
// DNS and shared by route and probe, so DNS changes cannot substitute a
// different destination halfway through the evidence chain.
func Investigate(ctx context.Context, request Request, p Providers) (*Result, error) {
	req, err := Validate(request)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := &Result{Request: req, StartedAt: time.Now().UTC(), Addresses: []string{}, Evidence: []Evidence{}, Comparison: "No connection measurement was requested."}
	defer func() { result.EndedAt = time.Now().UTC() }()
	sourceName := p.SourceName
	if sourceName == "" {
		sourceName = req.SourceKind
	}
	result.Scope = Scope{Vantage: "dashboard_host", Source: sourceName, SourceAddress: req.SourceAddress, Target: req.Target, Family: req.Family, Protocol: req.Protocol, Port: req.Port, Mark: req.Mark, Limitations: []string{"Snapshots are collected in sequence, not one atomic packet trace.", "Kernel route lookup uses the diagnostic UID and unspecified source port; another application's UID, marks or source port may select another route.", "Provider policy and remote service ownership remain unknown. An outbound TCP connection does not prove inbound access or TLS/application health."}}
	if req.SourceKind == "container" {
		result.Scope.Vantage = "container_network_namespace"
		result.Scope.Limitations = append(result.Scope.Limitations, "Container routing is the first leg. Host bridge/FORWARD/NAT and provider legs are not completely traced.")
	}
	var address string
	dns := evidence("dns", "DNS", sourceName, "Native resolver", "/network/dns")
	if literal, err := netip.ParseAddr(req.Target); err == nil {
		address = literal.Unmap().String()
		result.Addresses = append(result.Addresses, address)
		dns.Basis, dns.State, dns.Summary = Observed, "not_applicable", "The destination is a literal address; no DNS traffic was sent."
	} else if p.DNS == nil {
		dns.Summary = "Native resolver evidence is unavailable; no alternate resolver was queried."
	} else {
		answer, err := p.DNS(ctx, req)
		if err != nil {
			dns.failure(err)
		} else {
			dns.Basis, dns.State, dns.Summary, dns.Owner = Measured, "observed", answer.Summary, answer.Owner
			dns.Limitations = answer.Limits
			seen := map[string]bool{}
			for _, value := range answer.Addresses {
				ip, err := netip.ParseAddr(value)
				if err != nil || (req.Family == "inet") != ip.Unmap().Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.Zone() != "" {
					continue
				}
				value = ip.Unmap().String()
				if !seen[value] && len(result.Addresses) < 8 {
					seen[value] = true
					result.Addresses = append(result.Addresses, value)
				}
			}
			if req.Address != "" {
				if seen[req.Address] {
					address = req.Address
				} else {
					dns.State, dns.Summary = "unavailable", "The chosen address is absent from the current native DNS answer; no route or connection probe was attempted."
				}
			} else if len(result.Addresses) > 0 {
				address = result.Addresses[0]
			}
		}
	}
	if address != "" {
		dns.Facts = append(dns.Facts, Fact{"Pinned destination", address})
	}
	result.Evidence = append(result.Evidence, dns)
	if address == "" {
		for _, item := range []struct{ id, title, owner, path string }{{"rules", "Policy rules", "Linux kernel", "/network/routing"}, {"route", "Kernel route", "Linux kernel", "/network/routing"}, {"firewall", "Firewall", "Host firewall", "/network/firewall"}, {"nat", "NAT", "Gateway / Docker", "/network/gateway"}, {"tunnel", "Tunnel / interface", "Interface manager", "/network/interfaces"}, {"proxy", "Proxy", "Proxy owner", "/proxy"}, {"owner", "Destination owner", "Listener owner", "/ports"}, {"probe", "Connection measurement", "Diagnostics", "/network/tools"}} {
			e := evidence(item.id, item.title, sourceName, item.owner, item.path)
			e.State, e.Summary = "skipped", "No selected destination address is available; this step was not queried."
			result.Evidence = append(result.Evidence, e)
		}
		result.Comparison = "No selected address exists; connectivity was not measured."
		return result, nil
	}
	req.Address = address
	result.Scope.Address = address
	rules := evidence("rules", "Policy rules", sourceName, "Linux kernel", "/network/routing")
	if p.Rules != nil {
		values, err := p.Rules(ctx, req.Family)
		if err != nil {
			rules.failure(err)
		} else {
			rules.Basis, rules.State, rules.Summary = Observed, "observed", "Ordered native rules are listed as candidates. The kernel route query decides this tuple; this is not a per-rule packet trace."
			for _, r := range values[:min(32, len(values))] {
				rules.Facts = append(rules.Facts, Fact{fmt.Sprintf("Priority %d", r.Priority), fmt.Sprintf("from %s to %s · %s table %s · mark %s", r.From, r.To, r.Action, r.Table, r.Mark)})
				if len(r.UnknownSelectors) > 0 {
					rules.Limitations = append(rules.Limitations, fmt.Sprintf("Rule %d has additional selectors: %s.", r.Priority, strings.Join(r.UnknownSelectors, ", ")))
				}
			}
			if len(values) > 32 {
				rules.Limitations = append(rules.Limitations, "Only the first 32 ordered rules are displayed; the kernel query still evaluates its full rule set.")
			}
		}
	}
	result.Evidence = append(result.Evidence, rules)
	route := evidence("route", "Kernel route", sourceName, "Linux kernel", "/network/routing")
	var selected netx.RouteLookup
	routeKnown := false
	if p.Route != nil {
		selected, err = p.Route(ctx, req)
		if err != nil {
			route.failure(err)
		} else {
			routeKnown = true
			route.Basis, route.State = Observed, "observed"
			route.Summary = "The kernel selected a route for the pinned destination and requested tuple; no connectivity is implied."
			route.Facts = []Fact{{"Destination", address}, {"Interface", selected.Device}, {"Gateway", selected.Gateway}, {"Source", selected.Source}, {"Table", strconv.Itoa(selected.Table)}, {"Protocol / port", fmt.Sprintf("%s / %d", req.Protocol, req.Port)}}
			result.Scope.SourceAddress = selected.Source
		}
	}
	result.Evidence = append(result.Evidence, route)
	firewall := evidence("firewall", "Firewall", "host adapter", "Host firewall", "/network/firewall")
	policy := netsec.TrafficPolicy{Verdict: "unknown"}
	if p.Firewall != nil {
		status, err := p.Firewall(ctx)
		if err != nil {
			firewall.failure(err)
		} else {
			direction := "out"
			if req.SourceKind == "container" {
				direction = "fwd"
				firewall.Scope = "host forwarding leg; container hooks unknown"
			}
			policy = netsec.ModelTrafficPolicy(status, selected.Source, address, req.Protocol, req.Port, direction)
			firewall.Summary, firewall.Limitations = policy.Summary, policy.Limitations
			if policy.Verdict != "unknown" {
				firewall.Basis, firewall.State = Modeled, "modeled"
			}
			if status != nil {
				firewall.Owner = string(status.Backend)
				firewall.Facts = append(firewall.Facts, Fact{"Adapter", string(status.Backend)}, Fact{"Direction", direction}, Fact{"Adapter prediction", policy.Verdict})
			}
			if selected.Local && req.SourceKind == "host" {
				firewall.Limitations = append(firewall.Limitations, "Local delivery also visits INPUT; its source/interface/state decisions are not covered by the OUTPUT prediction.")
			}
		}
	}
	result.Evidence = append(result.Evidence, firewall)
	nat := evidence("nat", "NAT", "host gateway inventory", "Gateway / Docker", "/network/gateway")
	nat.Summary = "NAT in foreign rules, Docker and upstream devices is unknown."
	if p.Gateway != nil {
		gateway, err := p.Gateway(ctx)
		if err != nil {
			nat.failure(err)
		} else if gateway != nil {
			nat.Basis, nat.State = Observed, "observed"
			nat.Facts = append(nat.Facts, Fact{"Owned gateway table loaded", strconv.FormatBool(gateway.Loaded)})
			for _, n := range gateway.NAT {
				prefix, err := netip.ParsePrefix(n.Source)
				src, e := netip.ParseAddr(selected.Source)
				if err == nil && e == nil && prefix.Contains(src) && n.Enabled {
					nat.Facts = append(nat.Facts, Fact{"Configured source NAT candidate", fmt.Sprintf("%s · source %s · egress %s · to %s", n.Name, n.Source, n.Interface, n.ToAddress)})
				}
			}
			for _, f := range gateway.Forwards {
				if f.Enabled && (f.Protocol == req.Protocol || f.Protocol == "both") && portInRange(req.Port, f.Ports) {
					nat.Facts = append(nat.Facts, Fact{"Configured forward candidate", fmt.Sprintf("%s · ingress %s · to %s:%s", f.Name, f.Interface, f.Target, f.TargetPort)})
				}
			}
			nat.Limitations = []string{"Candidates come from the desired owned spec and table presence. They do not prove this packet's ingress, exact active expression or translation.", "Container first-leg interface names are not host egress interfaces. The host/provider NAT leg remains unknown."}
		}
	}
	result.Evidence = append(result.Evidence, nat)
	tunnel := evidence("tunnel", "Tunnel / interface", sourceName, "Interface manager", "/network/interfaces")
	tunnel.Summary = "The egress interface owner is unknown."
	if req.SourceKind == "container" {
		tunnel.Basis, tunnel.State, tunnel.Owner = Observed, "observed", "Docker"
		tunnel.Summary = "The selected route belongs to this container's network namespace; host tunnel traversal is unknown."
		tunnel.Facts = append(tunnel.Facts, Fact{"Container egress", selected.Device})
	} else if p.Links != nil && routeKnown {
		links, err := p.Links(ctx)
		if err != nil {
			tunnel.failure(err)
		} else {
			for _, link := range links {
				if link.Name == selected.Device {
					tunnel.Basis, tunnel.State, tunnel.Owner = Observed, "observed", link.Owner
					tunnel.Summary = "Native interface metadata identifies this route's egress owner."
					tunnel.Facts = []Fact{{"Interface", link.Name}, {"Kind", link.Kind}, {"Owner", link.Owner}, {"State", link.State}}
					if link.Kind == "wireguard" || link.Kind == "tun" || link.Role == "tunnel" {
						tunnel.Limitations = append(tunnel.Limitations, "Tunnel interface selection does not prove peer handshake, remote exit policy or the underlay transport.")
					}
					break
				}
			}
		}
	}
	result.Evidence = append(result.Evidence, tunnel)
	proxy := evidence("proxy", "Proxy", "destination owner inventory", "Proxy owner", "/proxy")
	// streamName is the native stream that owns the matched listener, whose
	// configuration and backend legs are joined below.
	streamName := ""
	owner := evidence("owner", "Destination owner", "local host sockets", "Listener owner", "/ports")
	proxy.Summary, owner.Summary = "Remote or foreign proxy configuration is unknown.", "Remote service/process ownership is unknown."
	if req.SourceKind == "container" && selected.Local {
		owner.Scope, owner.Summary = sourceName, "The destination is local to this container namespace. Host socket ownership was not substituted for container attribution."
	}
	if routeKnown && selected.Local && req.SourceKind == "host" && p.Owners != nil {
		snapshot, err := p.Owners(ctx)
		if err != nil {
			proxy.failure(err)
			owner.failure(err)
		} else {
			owner.Limitations, proxy.Limitations = snapshot.Limits, snapshot.Limits
			for _, listener := range snapshot.Listeners {
				bound, err := netip.ParseAddr(listener.Address)
				if err == nil && req.Family == "inet" && bound.Is6() && bound.IsUnspecified() && listener.Protocol == req.Protocol && int(listener.Port) == req.Port {
					owner.Basis, owner.State, owner.Summary = Modeled, "unknown", "An IPv6 wildcard listener may accept IPv4 if its socket enables dual stack; that socket setting is unverified."
					owner.Facts = append(owner.Facts, Fact{"Possible dual-stack owner", listener.Process})
					continue
				}
				if err != nil || listener.Protocol != req.Protocol || int(listener.Port) != req.Port || bound.Unmap().Is4() != (req.Family == "inet") || !bound.IsUnspecified() && bound.Unmap().String() != address {
					continue
				}
				owner.Basis, owner.State, owner.Summary = Observed, "observed", "A matching local listener or Docker publication was observed; this is not proof of remote reachability."
				owner.Facts = append(owner.Facts, Fact{"Process", listener.Process}, Fact{"Manager", listener.Manager + " " + listener.ManagerName})
				if listener.Container != nil {
					owner.Facts = append(owner.Facts, Fact{"Container", listener.Container.Name})
					owner.Owner, owner.OwnerPath = "Docker", "/docker"
				}
				if listener.Source != "" {
					owner.Facts = append(owner.Facts, Fact{"Publication source", listener.Source})
				}
				for _, site := range listener.Routes {
					proxy.Basis, proxy.State, proxy.Summary = Modeled, "modeled", "Configured proxy sites reference this listener. Site loading and request handling are not measured here."
					proxy.Facts = append(proxy.Facts, Fact{"Configured site", site.Site + " · " + site.ServerName})
					if policy := sitePolicyFact(ctx, p, site.Site); policy != "" {
						proxy.Facts = append(proxy.Facts, Fact{"Service policy of " + site.Site, policy})
						proxy.Limitations = appendOnce(proxy.Limitations, "A site's service policy applies to requests that reach this port through the site; a direct connection to the port meets none of it.")
					}
				}
				if listener.Stream != "" || listener.ServedSites > 0 {
					proxy.Basis, proxy.State = Modeled, "modeled"
					proxy.Summary = "Native proxy inventory associates this listener with a stream or configured sites; application routing remains unmeasured."
					proxy.Facts = append(proxy.Facts, Fact{"Stream", listener.Stream}, Fact{"Configured sites", strconv.Itoa(listener.ServedSites)})
				}
				if listener.Stream != "" {
					streamName = listener.Stream
				}
			}
			if owner.Basis == Unknown {
				owner.Basis, owner.State, owner.Summary = Observed, "absent", "No matching local listener/publication was found in this snapshot; unreadable owners and later changes remain possible."
			}
		}
	}
	result.Evidence = append(result.Evidence, proxy, owner)
	if streamName != "" && p.Stream != nil {
		result.Evidence = append(result.Evidence, streamEvidence(ctx, streamName, p))
	}
	probe := evidence("probe", "Connection measurement", sourceName, "Diagnostics", "/network/tools")
	// traversed is a connection that reached a stream's listener: nginx logs
	// where it forwarded it when it closes.
	var sent time.Time
	traversed := false
	probe.State, probe.Summary = "not_requested", "No TCP connection was attempted."
	if req.Measure {
		switch {
		case !routeKnown:
			probe.State, probe.Summary = "skipped", "No usable selected route exists; a connection probe was not attempted."
		case req.Protocol != "tcp":
			probe.State, probe.Summary = "unsupported", "UDP reachability requires an application response; a UDP connect/send would not prove a listener. No TCP substitute was sent."
		case req.Mark != "":
			probe.State, probe.Summary = "unsupported", "The bounded TCP adapter cannot reproduce an explicit socket mark. No unmarked substitute was sent."
		case p.Probe == nil:
			probe.State, probe.Summary = "unavailable", "The selected source probe adapter is unavailable."
		default:
			req.SourceAddress = selected.Source
			sent = time.Now()
			measurement, err := p.Probe(ctx, req)
			traversed = err == nil && measurement != nil && measurement.OK && streamName != "" && p.StreamSession != nil
			result.Measurement = measurement
			if err != nil {
				probe.failure(err)
			} else if measurement != nil {
				probe.Basis, probe.State = Measured, "failed"
				probe.Summary = "TCP did not connect from this source at this time; the failure does not identify the blocking layer."
				if measurement.OK {
					probe.State, probe.Summary = "connected", "TCP connected from this source at this time."
				}
				probe.Facts = []Fact{{"Pinned destination", address}, {"Source address", selected.Source}, {"Duration", measurement.Duration}, {"Probe detail", measurement.Error}}
				result.Comparison = fmt.Sprintf("Host firewall adapter prediction: %s. Selected-source TCP measurement: %s. Unknown foreign/provider/application layers remain unknown.", policy.Verdict, probe.State)
				probe.Limitations = []string{"A successful TCP handshake does not prove TLS, authentication, HTTP, UDP or inbound/provider reachability."}
			}
		}
		if probe.Basis != Measured {
			result.Comparison = "The selected-source connection was not measured. " + probe.Summary
		}
	}
	result.Evidence = append(result.Evidence, probe)
	if traversed {
		result.Evidence = append(result.Evidence, streamTraversal(ctx, streamName, selected.Source, sent, p))
	}
	return result, nil
}

func portInRange(port int, raw string) bool {
	first, last, ranged := strings.Cut(raw, "-")
	low, err := strconv.Atoi(first)
	if err != nil {
		return false
	}
	if !ranged {
		return port == low
	}
	high, err := strconv.Atoi(last)
	return err == nil && port >= low && port <= high
}
