package api

import (
	"context"
	"errors"
	"fmt"
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
	Gateway         overviewGateway         `json:"gateway"`
	Made            overviewMade            `json:"made"`
	Findings        []netx.Finding          `json:"findings"`
	// Identity is each family's way out and what it establishes about the
	// address the internet sees; Flows the topology's edges as connection
	// tracking holds them.
	Identity []netx.EgressIdentity `json:"identity"`
	Flows    netx.TopologyFlows    `json:"flows"`
	// Observations say which readings above arrived, so a failed one is shown
	// as failed rather than as an empty answer.
	Observations []netx.Observation `json:"observations"`
	// Incidents are the findings over time; IncidentsError why they could
	// not be read or recorded.
	Incidents      []netx.Incident `json:"incidents"`
	IncidentsError string          `json:"incidentsError,omitempty"`
	ReadAt         time.Time       `json:"readAt"`
}

// overviewIncidents bounds the history the Overview carries.
const overviewIncidents = 50

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

// overviewGateway is the gateway and protection at a glance: whether the
// table is loaded and may be written, what is in force, what it has refused
// since it was loaded, and how full the connection-tracking table is.
type overviewGateway struct {
	Loaded   bool   `json:"loaded"`
	Writable bool   `json:"writable"`
	Reason   string `json:"reason,omitempty"`
	Forwards int    `json:"forwards"`
	NAT      int    `json:"nat"`
	Limits   int    `json:"limits"`
	Lists    int    `json:"blocklists"`
	// Dropped is every packet the blocklists and limits refused since the
	// table was loaded.
	Dropped   uint64         `json:"dropped"`
	Conntrack netx.Conntrack `json:"conntrack"`
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
	client := s.networkClient(r)
	out := networkOverview{
		Links: []netx.Link{}, DockerNetworks: []overviewDockerNet{},
		PublicAddresses: []string{}, Defaults: []netx.DefaultRoute{},
		Identity: []netx.EgressIdentity{}, Incidents: []netx.Incident{},
	}

	var (
		wg           sync.WaitGroup
		inv          netx.Inventory
		linksErr     error
		errs         map[string]uint64
		observations = map[string]netx.Observation{}
		observedMu   sync.Mutex
	)
	observe := func(source, label, href string, err error) {
		o := netx.Observation{Source: source, Label: label, Href: href, State: "ok"}
		var missing *netx.UnavailableError
		switch {
		case errors.As(err, &missing):
			// A tool this host does not have is absent, not a failed read.
			o.State, o.Error = "unavailable", err.Error()
		case err != nil:
			o.State, o.Error = "failed", err.Error()
		}
		observedMu.Lock()
		observations[source] = o
		observedMu.Unlock()
	}
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
	do(func() {
		var err error
		out.Client, err = s.modules.network.ClientPath(ctx, client)
		observe("client", "The route back to your browser", "/network/routing", err)
	})
	do(func() { out.Defaults = netx.DefaultRoutes(ctx) })
	do(func() { out.Identity = s.modules.network.EgressIdentities(ctx) })
	do(func() {
		var err error
		errs, err = s.modules.network.Sampler().RecentErrors(ctx, time.Hour)
		observe("history", "Recorded interface errors", "/network/traffic", err)
	})
	do(func() {
		inv = s.networkInventory(ctx)
		out.Links, linksErr = s.modules.network.ReadLinks(ctx, inv, client)
	})
	do(func() {
		st, err := s.modules.netsec.Status(ctx)
		observe("firewall", "The host firewall", "/network/firewall", err)
		if err == nil {
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
		c, err := s.modules.netsec.Connections(ctx)
		observe("connections", "Connections", "/network/connections", err)
		if err == nil {
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
	do(func() {
		var gwErr, protErr error
		out.Gateway, gwErr, protErr = s.overviewGateway(ctx, client)
		observe("gateway", "The gateway table", "/network/gateway", gwErr)
		observe("protection", "Blocklists and limits", "/network/protection", protErr)
	})
	wg.Wait()
	if linksErr != nil {
		return mapNetworkError(linksErr)
	}
	out.ReadAt = time.Now().UTC()
	if s.modules.docker != nil {
		var containersErr, networksErr error
		if inv.ContainersError != "" {
			containersErr = errors.New(inv.ContainersError)
		} else if len(inv.UnjoinedContainers) > 0 {
			containersErr = fmt.Errorf("the devices of %s could not be read", strings.Join(inv.UnjoinedContainers, ", "))
		}
		if inv.DockerNetworksError != "" {
			networksErr = errors.New(inv.DockerNetworksError)
		}
		observe("docker.containers", "Docker's containers", "/docker", containersErr)
		observe("docker.networks", "Docker's networks", "/docker/networks", networksErr)
	}
	var forwardingErr error
	for _, id := range out.Identity {
		var err error
		if id.Error != "" {
			err = errors.New(id.Error)
		}
		label := "The IPv4 way out"
		if id.Family == "inet6" {
			label = "The IPv6 way out"
		}
		observe("identity."+id.Family, label, "/network/routing", err)
		if id.ForwardingError != "" && forwardingErr == nil {
			forwardingErr = errors.New(id.ForwardingError)
		}
	}
	observe("forwarding", "The forwarding switches", "/network/routing", forwardingErr)
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
	out.Flows = s.modules.network.TopologyFlows(ctx, out.Links, inv.Networks)
	if out.Flows.State == "failed" {
		observe("flows", "Connection tracking", "/network/connections", errors.New(out.Flows.Error))
	}

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
	out.Observations = make([]netx.Observation, 0, len(observations))
	for _, o := range observations {
		out.Observations = append(out.Observations, o)
	}
	sort.Slice(out.Observations, func(i, j int) bool { return out.Observations[i].Source < out.Observations[j].Source })
	out.Findings = netx.OverviewFindings(netx.OverviewInput{
		Links: out.Links, Spec: spec, Persistence: out.Persistence, Forwarding: out.Forwarding,
		LinkHistoryErrors: errs, FirewallAvailable: out.Firewall.Available,
		FirewallEnabled: out.Firewall.Enabled, ConntrackPercent: conntrackPercent(out.Gateway.Conntrack),
		EncryptedDNS: encrypted, Observations: out.Observations,
	})
	if err := s.modules.network.RecordFindings(ctx, out.ReadAt, out.Findings, out.Observations); err != nil {
		out.IncidentsError = "The incident history could not be recorded: " + err.Error()
	}
	if incidents, err := s.modules.network.Incidents(ctx, overviewIncidents); err != nil {
		out.IncidentsError = "The incident history could not be read: " + err.Error()
	} else {
		out.Incidents = incidents
	}
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

// overviewGateway reads the gateway's and the protections' counters; each
// read is one `nft -j list table`, so the Overview's poll costs two. Each
// read's failure is returned so the page says it, not zero.
func (s *Server) overviewGateway(ctx context.Context, client string) (overviewGateway, error, error) {
	g := overviewGateway{Conntrack: netx.CurrentConntrack()}
	gw, gwErr := s.modules.network.Gateway(ctx)
	if gwErr == nil {
		g.Loaded, g.Writable, g.Reason = gw.Loaded, gw.Capability.Writable, gw.Capability.Reason
		for _, f := range gw.Forwards {
			if f.Enabled {
				g.Forwards++
			}
		}
		for _, n := range gw.NAT {
			if n.Enabled {
				g.NAT++
			}
		}
	}
	p, protErr := s.modules.network.Protection(ctx, client)
	if protErr == nil {
		for _, l := range p.Limits {
			if l.Enabled {
				g.Limits++
			}
			g.Dropped += l.Packets
		}
		for _, b := range p.Blocklists {
			if b.Enabled {
				g.Lists++
			}
			g.Dropped += b.Packets
		}
	}
	return g, gwErr, protErr
}

// conntrackPercent is the table's fullness, negative where it cannot be read.
func conntrackPercent(c netx.Conntrack) float64 {
	if !c.Available {
		return -1
	}
	return c.Percent
}
