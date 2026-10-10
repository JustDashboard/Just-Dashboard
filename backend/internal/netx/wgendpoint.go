package netx

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// What this host can say about the endpoint its clients are given. A cloud
// server often holds only a private address behind the provider's one-to-one
// NAT, and a DNS name can point somewhere this server is not: both produce a
// configuration that looks right and never connects. None of this crosses the
// provider's network; reachability from the internet stays untested.

// WGEndpointEvidence is the endpoint as this host sees it.
type WGEndpointEvidence struct {
	Endpoint string `json:"endpoint"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	// Kind is address (a literal) or hostname.
	Kind string `json:"kind"`
	// Resolution is literal, resolved or failed; a name is resolved with the
	// dashboard's own resolver, which may not be every client's.
	Resolution      string              `json:"resolution"`
	ResolutionError string              `json:"resolutionError,omitempty"`
	Addresses       []WGEndpointAddress `json:"addresses"`
	// Uplink is the device of the default route; UplinkPublic is it holding a
	// public address of a family the endpoint uses.
	Uplink       string `json:"uplink,omitempty"`
	UplinkPublic bool   `json:"uplinkPublic"`
	// Listening is a UDP socket holding the port on this host now.
	Listening bool `json:"listening"`
	// Verdict is on_host_public, provider_mapped, elsewhere, private or
	// unresolved; Explanation says what it means and what stays untested.
	Verdict     string `json:"verdict"`
	Explanation string `json:"explanation"`
	// Reachability is always not_tested: nothing here dials in from outside.
	Reachability string `json:"reachability"`
	CheckedAt    int64  `json:"checkedAt"`
	// Warning is the sentence a creation passes on when the verdict needs a
	// look before clients are given the configuration.
	Warning string `json:"-"`
}

// WGEndpointAddress is one address the endpoint names or resolves to.
type WGEndpointAddress struct {
	Address string `json:"address"`
	Public  bool   `json:"public"`
	// OnHost is this host holding the address, on Device.
	OnHost bool   `json:"onHost"`
	Device string `json:"device,omitempty"`
}

// wgLookupHost resolves an endpoint name. A variable so tests answer it.
var wgLookupHost = func(ctx context.Context, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.Unmap())
	}
	return out, nil
}

// WireGuardEndpoint reads the evidence for a tunnel's recorded endpoint.
func (s *Service) WireGuardEndpoint(ctx context.Context, iface string) (*WGEndpointEvidence, error) {
	if err := validWGName(iface); err != nil {
		return nil, err
	}
	conf, err := s.readWGConf(iface)
	if err != nil {
		return nil, err
	}
	sec := conf.iface()
	if sec == nil {
		return nil, fmt.Errorf("%s has no [Interface] section", iface)
	}
	endpoint := sec.bodyMeta()["endpoint"]
	if endpoint == "" {
		return nil, fmt.Errorf("%s does not record the address clients dial", iface)
	}
	host, err := wgReadHostState(ctx)
	if err != nil {
		return nil, err
	}
	evidence := wgEndpointEvidenceFor(ctx, endpoint, host)
	return &evidence, nil
}

func wgEndpointEvidenceFor(ctx context.Context, endpoint string, host wgHostState) WGEndpointEvidence {
	e := WGEndpointEvidence{Endpoint: endpoint, Addresses: []WGEndpointAddress{}, Uplink: host.uplink, Reachability: "not_tested", CheckedAt: wgNow().Unix()}
	name, port := splitHostPort(endpoint)
	e.Host, e.Port = strings.Trim(name, "[]"), port
	e.Listening = wgUDPListening()[port]
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(e.Host); err == nil {
		e.Kind, e.Resolution = "address", "literal"
		addrs = []netip.Addr{a.WithZone("").Unmap()}
	} else {
		e.Kind = "hostname"
		resolved, err := wgLookupHost(ctx, e.Host)
		if err != nil || len(resolved) == 0 {
			e.Resolution, e.Verdict = "failed", "unresolved"
			if err != nil {
				e.ResolutionError = err.Error()
			}
			e.Explanation = fmt.Sprintf("%s does not resolve here, so clients given it cannot dial this tunnel until it does.", e.Host)
			e.Warning = "The endpoint " + e.Host + " does not resolve here; clients cannot dial the tunnel until its DNS record exists."
			return e
		}
		e.Resolution = "resolved"
		addrs = resolved
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i].Less(addrs[j]) })
	held := map[netip.Addr]string{}
	for dev, prefixes := range host.addrs {
		for _, p := range prefixes {
			held[p.Addr().WithZone("").Unmap()] = dev
		}
	}
	families := map[bool]bool{}
	anyPublic, onHostPublic := false, false
	for _, a := range addrs {
		dev, on := held[a]
		pub := isPublic(a)
		e.Addresses = append(e.Addresses, WGEndpointAddress{Address: a.String(), Public: pub, OnHost: on, Device: dev})
		families[a.Is4()] = true
		anyPublic = anyPublic || pub
		onHostPublic = onHostPublic || (pub && on)
	}
	var uplinkPublic []string
	for _, p := range host.addrs[host.uplink] {
		if a := p.Addr(); families[a.Is4()] && isPublic(a) {
			uplinkPublic = append(uplinkPublic, a.String())
		}
	}
	e.UplinkPublic = len(uplinkPublic) > 0
	untested := " Whether the provider's network admits UDP " + strconv.Itoa(port) + " from the internet is untested here."
	switch {
	case onHostPublic:
		e.Verdict = "on_host_public"
		e.Explanation = "Clients dial a public address this host holds." + untested
	case !anyPublic:
		e.Verdict = "private"
		e.Explanation = "The endpoint is a private address: only clients on a network that routes to it can dial it." + untested
	case !e.UplinkPublic:
		e.Verdict = "provider_mapped"
		e.Explanation = "This host holds no public address of that family, so the endpoint is presumably the provider's public address mapped to it." + untested
	default:
		e.Verdict = "elsewhere"
		e.Explanation = fmt.Sprintf("The endpoint is %s, which this host does not hold, while %s has %s; clients may be dialling another machine.",
			e.Addresses[0].Address, host.uplink, strings.Join(uplinkPublic, ", "))
		e.Warning = "The endpoint " + endpoint + " is not an address this host holds, while its uplink has " + strings.Join(uplinkPublic, ", ") + "; check that clients will dial this server."
	}
	return e
}
