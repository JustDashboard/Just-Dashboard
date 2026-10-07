package api

import (
	"context"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

// networkOverview is the Overview page's one read: the topology's nodes, the
// readings over it and the attention list under it. One request rather than
// eight because every part is drawn into one picture, and a picture that
// fills in a node at a time redraws its wires a node at a time.
type networkOverview struct {
	Hostname string              `json:"hostname"`
	Client   netx.Path           `json:"client"`
	Defaults []netx.DefaultRoute `json:"defaults"`
	// PublicAddresses are the globally routable addresses on the uplinks:
	// what the internet reaches this server at.
	PublicAddresses []string                `json:"publicAddresses"`
	Links           []netx.Link             `json:"links"`
	DockerNetworks  []overviewDockerNet     `json:"dockerNetworks"`
	Firewall        overviewFirewall        `json:"firewall"`
	Connections     overviewConnections     `json:"connections"`
	Forwarding      netx.ForwardingSwitches `json:"forwarding"`
	Persistence     netx.Persistence        `json:"persistence"`
	VPN             netx.VPNSummary         `json:"vpn"`
	DNS             *netx.DNSSummary        `json:"dns,omitempty"`
	Made            overviewMade            `json:"made"`
	Findings        []netx.Finding          `json:"findings"`
}

// overviewDockerNet is a Docker network as the topology draws it: its bridge
// and the containers on it, each as the image it runs.
type overviewDockerNet struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Bridge     string              `json:"bridge,omitempty"`
	Subnets    []string            `json:"subnets"`
	Containers []overviewContainer `json:"containers"`
}

type overviewContainer struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

type overviewFirewall struct {
	Backend   string `json:"backend,omitempty"`
	Available bool   `json:"available"`
	Enabled   bool   `json:"enabled"`
	Incoming  string `json:"incoming,omitempty"`
	Rules     int    `json:"rules"`
}

type overviewConnections struct {
	Total int `json:"total"`
	// Peers is every remote address; FromInternet the ones that are neither
	// private nor on the tailnet.
	Peers        int `json:"peers"`
	FromInternet int `json:"fromInternet"`
	Listening    int `json:"listening"`
}

// overviewMade counts what the dashboard has made, by kind, for the gateway
// and inside nodes of the topology.
type overviewMade struct {
	Links      int `json:"links"`
	Routes     int `json:"routes"`
	Rules      int `json:"rules"`
	Forwards   int `json:"forwards"`
	NAT        int `json:"nat"`
	Limits     int `json:"limits"`
	Blocklists int `json:"blocklists"`
	Shaping    int `json:"shaping"`
	Namespaces int `json:"namespaces"`
}

// handleNetworkOverview gathers every part concurrently — each is a
// subprocess, a socket or a file on a host that may be busy — and judges the
// attention list from what came back.
func (s *Server) handleNetworkOverview(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	client := httpx.ClientIP(r)
	out := networkOverview{
		Links: []netx.Link{}, DockerNetworks: []overviewDockerNet{},
		PublicAddresses: []string{}, Defaults: []netx.DefaultRoute{},
	}

	var (
		wg        sync.WaitGroup
		inv       netx.Inventory
		linksErr  error
		errs      map[string]uint64
		conntrack = -1.0
	)
	do := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
	do(func() {
		if host, err := s.modules.sys.Host(ctx); err == nil {
			out.Hostname = host.Hostname
		}
	})
	do(func() { out.Client, _ = s.modules.network.ClientPath(ctx, client) })
	do(func() { out.Defaults = netx.DefaultRoutes(ctx) })
	do(func() { errs = s.modules.network.Sampler().RecentErrors(ctx, time.Hour) })
	do(func() {
		inv = s.networkInventory(ctx)
		out.Links, linksErr = s.modules.network.ReadLinks(ctx, inv, client)
	})
	do(func() {
		if st, err := s.modules.netsec.Status(ctx); err == nil {
			out.Firewall = overviewFirewall{
				Backend: string(st.Backend), Available: st.Available, Enabled: st.Enabled,
				Incoming: st.Policy.Incoming,
			}
			for _, rule := range st.Rules {
				if !rule.IPv6 {
					out.Firewall.Rules++
				}
			}
		}
	})
	do(func() {
		if c, err := s.modules.netsec.Connections(ctx); err == nil {
			out.Connections = overviewConnections{Total: c.Total, Peers: len(c.Peers), Listening: c.Listening}
			for _, p := range c.Peers {
				if !p.Private {
					out.Connections.FromInternet++
				}
			}
		}
	})
	do(func() { out.Persistence = s.modules.network.PersistenceStatus(ctx) })
	do(func() { out.VPN = s.modules.network.VPNSummary(ctx) })
	do(func() { out.DNS = s.cachedDNSSummary(ctx) })
	wg.Wait()
	if linksErr != nil {
		return mapNetworkError(linksErr)
	}
	if out.Defaults == nil {
		out.Defaults = []netx.DefaultRoute{}
	}

	out.Forwarding = netx.CurrentForwarding()
	for _, l := range out.Links {
		if !l.Uplink {
			continue
		}
		for _, a := range l.Addresses {
			if a.Public {
				out.PublicAddresses = append(out.PublicAddresses, a.CIDR)
			}
		}
	}
	out.DockerNetworks = dockerTopology(inv, out.Links)

	spec, err := s.modules.network.Spec()
	if err == nil {
		out.Made = overviewMade{
			Links: len(spec.Links), Routes: len(spec.Routes), Rules: len(spec.Rules),
			Forwards: len(spec.Forwards), NAT: len(spec.NAT), Limits: len(spec.Limits),
			Blocklists: len(spec.Blocklists), Shaping: len(spec.Shaping), Namespaces: len(spec.Namespaces),
		}
	}
	var encrypted *bool
	if out.DNS != nil && out.DNS.Resolver == "systemd-resolved" {
		on := out.DNS.DNSOverTLS == "yes" || out.DNS.DNSOverTLS == "opportunistic"
		encrypted = &on
	}
	out.Findings = netx.OverviewFindings(netx.OverviewInput{
		Links: out.Links, Spec: spec, Persistence: out.Persistence, Forwarding: out.Forwarding,
		LinkHistoryErrors: errs, FirewallAvailable: out.Firewall.Available,
		FirewallEnabled: out.Firewall.Enabled, ConntrackPercent: conntrack,
		EncryptedDNS: encrypted,
	})
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// dockerTopology puts each running container on the networks its veths lead
// into, by the bridge each veth is a port of. Docker's own listing says which
// networks a container joined; this says which bridge its traffic crosses,
// which is the thing the picture draws a wire along.
func dockerTopology(inv netx.Inventory, links []netx.Link) []overviewDockerNet {
	images := map[string]string{}
	for _, c := range inv.Containers {
		images[c.Name] = c.Image
	}
	onBridge := map[string][]overviewContainer{}
	for _, l := range links {
		if l.Container == "" || l.Master == "" {
			continue
		}
		onBridge[l.Master] = append(onBridge[l.Master], overviewContainer{Name: l.Container, Image: images[l.Container]})
	}
	out := []overviewDockerNet{}
	for _, n := range inv.Networks {
		if n.Driver != "bridge" || n.Bridge == "" {
			continue
		}
		cs := onBridge[n.Bridge]
		sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
		if cs == nil {
			cs = []overviewContainer{}
		}
		subnets := []string{}
		for _, sn := range n.Subnets {
			if _, err := netip.ParsePrefix(sn); err == nil {
				subnets = append(subnets, sn)
			}
		}
		out = append(out, overviewDockerNet{
			ID: n.ID, Name: n.Name, Bridge: n.Bridge, Subnets: subnets, Containers: cs,
		})
	}
	// Busiest first: the networks with containers on them are the ones the
	// picture is about; an empty default bridge goes last.
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Containers) != len(out[j].Containers) {
			return len(out[i].Containers) > len(out[j].Containers)
		}
		return strings.Compare(out[i].Name, out[j].Name) < 0
	})
	return out
}

// dnsSummaryCache holds the resolver's summary for a minute. Reading it walks
// every socket on port 53 to its process, and the resolver chain changes when
// somebody changes it — which the DNS page does through its own read, not
// the Overview's fifteen-second poll.
var dnsSummaryCache = struct {
	sync.Mutex
	at  time.Time
	sum *netx.DNSSummary
}{}

func (s *Server) cachedDNSSummary(ctx context.Context) *netx.DNSSummary {
	dnsSummaryCache.Lock()
	defer dnsSummaryCache.Unlock()
	if dnsSummaryCache.sum != nil && time.Since(dnsSummaryCache.at) < time.Minute {
		return dnsSummaryCache.sum
	}
	sum := s.modules.network.DNSSummary(ctx, s.dnsContainers(ctx))
	dnsSummaryCache.sum, dnsSummaryCache.at = &sum, time.Now()
	return dnsSummaryCache.sum
}
