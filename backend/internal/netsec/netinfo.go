package netsec

import (
	"bufio"
	"context"
	"os"
	"sort"
	"strings"
	"time"

	"net"

	gnet "github.com/shirou/gopsutil/v4/net"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The shape of the machine's network, which the Security page needs in order
// to mean anything by "exposed".
//
// An address on a tailscale0 interface and the same address on eth0 are
// completely different security propositions, and until the interfaces are on
// screen the operator has to take the dashboard's word for which one they
// have. The routing table answers the other half — which interface the
// default route uses is what "the internet reaches this box here" means.

type Interface struct {
	Name string `json:"name"`
	// Addresses are CIDRs as the kernel reports them.
	Addresses []string `json:"addresses"`
	MAC       string   `json:"mac,omitempty"`
	MTU       int      `json:"mtu"`
	Up        bool     `json:"up"`
	Loopback  bool     `json:"loopback"`
	// Kind classifies the device for the UI: loopback, tunnel, bridge,
	// virtual or physical. A host running Docker has a dozen veth pairs, and
	// a list that does not separate them from eth0 is unreadable.
	Kind      string `json:"kind"`
	BytesSent uint64 `json:"bytesSent"`
	BytesRecv uint64 `json:"bytesRecv"`
	// Public marks an interface holding a globally routable address — the
	// one fact that decides whether anything bound to it faces the internet.
	Public bool `json:"public"`
}

type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway,omitempty"`
	Interface   string `json:"interface,omitempty"`
	Source      string `json:"source,omitempty"`
	Metric      string `json:"metric,omitempty"`
	Family      string `json:"family"`
	Raw         string `json:"raw"`
}

type NetworkInfo struct {
	Interfaces []Interface `json:"interfaces"`
	Routes     []Route     `json:"routes"`
	// Resolvers is what /etc/resolv.conf points at. Worth showing next to the
	// rest because a resolver you did not choose is a redirection of every
	// name this machine looks up.
	Resolvers []string `json:"resolvers"`
	Search    []string `json:"search"`
}

func (s *Service) NetworkInfo(ctx context.Context) (*NetworkInfo, error) {
	info := &NetworkInfo{Interfaces: []Interface{}, Routes: []Route{}, Resolvers: []string{}, Search: []string{}}

	ifaces, err := gnet.InterfacesWithContext(ctx)
	if err != nil {
		return nil, err
	}
	counters := map[string]gnet.IOCountersStat{}
	if stats, err := gnet.IOCountersWithContext(ctx, true); err == nil {
		for _, s := range stats {
			counters[s.Name] = s
		}
	}
	for _, ifc := range ifaces {
		item := Interface{
			Name: ifc.Name, MTU: ifc.MTU, MAC: ifc.HardwareAddr,
			Addresses: []string{}, Kind: ClassifyInterface(ifc.Name),
		}
		for _, f := range ifc.Flags {
			switch f {
			case "up":
				item.Up = true
			case "loopback":
				item.Loopback = true
				item.Kind = "loopback"
			}
		}
		for _, a := range ifc.Addrs {
			item.Addresses = append(item.Addresses, a.Addr)
			if isGloballyRoutable(a.Addr) {
				item.Public = true
			}
		}
		if c, ok := counters[ifc.Name]; ok {
			item.BytesSent, item.BytesRecv = c.BytesSent, c.BytesRecv
		}
		info.Interfaces = append(info.Interfaces, item)
	}
	// Physical first, then tunnels, then the virtual clutter — the order
	// somebody reads them in, not the order the kernel enumerates them.
	sort.SliceStable(info.Interfaces, func(i, j int) bool {
		a, b := info.Interfaces[i], info.Interfaces[j]
		if KindRank(a.Kind) != KindRank(b.Kind) {
			return KindRank(a.Kind) < KindRank(b.Kind)
		}
		return a.Name < b.Name
	})

	info.Routes = readRoutes(ctx)
	info.Resolvers, info.Search = readResolvers()
	return info, nil
}

// ClassifyInterface names a device from its name, which is what the kernel and
// every tool on the host agree on and is cheaper than probing sysfs.
func ClassifyInterface(name string) string {
	switch {
	case name == "lo":
		return "loopback"
	case strings.HasPrefix(name, "tailscale"), strings.HasPrefix(name, "wg"),
		strings.HasPrefix(name, "tun"), strings.HasPrefix(name, "tap"),
		strings.HasPrefix(name, "zt"), strings.HasPrefix(name, "nebula"):
		return "tunnel"
	case strings.HasPrefix(name, "docker"), strings.HasPrefix(name, "br-"),
		strings.HasPrefix(name, "virbr"), name == "cni0":
		return "bridge"
	case strings.HasPrefix(name, "veth"), strings.HasPrefix(name, "vnet"),
		strings.HasPrefix(name, "cali"), strings.HasPrefix(name, "flannel"):
		return "virtual"
	}
	return "physical"
}

func KindRank(kind string) int {
	switch kind {
	case "physical":
		return 0
	case "tunnel":
		return 1
	case "bridge":
		return 2
	case "loopback":
		return 3
	}
	return 4
}

// isGloballyRoutable reports an address the internet could route to. The
// negative space is what matters: loopback, RFC1918, link-local, CGNAT (which
// is where Tailscale lives) and unique-local v6 are all "not the internet".
func isGloballyRoutable(cidr string) bool {
	addr := cidr
	if i := strings.Index(cidr, "/"); i >= 0 {
		addr = cidr[:i]
	}
	ip := net.ParseIP(addr)
	if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if isPrivate(ip) || tailscaleNet.Contains(ip) {
		return false
	}
	return ip.IsGlobalUnicast()
}

// readRoutes shells to iproute2, which every Linux host has and whose output
// is stable. Read on the host: a container with its own network namespace
// would otherwise report the bridge it sits behind as the whole world.
func readRoutes(ctx context.Context) []Route {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	routes := []Route{}
	for _, family := range []string{"-4", "-6"} {
		out, err := hostexec.CommandOnHost(ctx, "ip", family, "route", "show").Output()
		if err != nil {
			continue
		}
		name := "ipv4"
		if family == "-6" {
			name = "ipv6"
		}
		routes = append(routes, parseRoutes(string(out), name)...)
	}
	return routes
}

// parseRoutes reads `ip route show`, whose lines are a destination followed by
// key/value pairs in no guaranteed order — so they are read as pairs rather
// than by position.
func parseRoutes(out, family string) []Route {
	routes := []Route{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		r := Route{Destination: fields[0], Family: family, Raw: line}
		for i := 1; i < len(fields)-1; i++ {
			switch fields[i] {
			case "via":
				r.Gateway = fields[i+1]
			case "dev":
				r.Interface = fields[i+1]
			case "src":
				r.Source = fields[i+1]
			case "metric":
				r.Metric = fields[i+1]
			}
		}
		routes = append(routes, r)
	}
	return routes
}

// readResolvers parses /etc/resolv.conf. The host's copy is mounted at the
// same path; systemd-resolved hosts point at 127.0.0.53, which is itself the
// answer to "why does this say loopback".
func readResolvers() (servers, search []string) {
	servers, search = []string{}, []string{}
	for _, path := range []string{"/etc/resolv.conf", "/host/etc/resolv.conf"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			key, value, ok := strings.Cut(line, " ")
			if !ok {
				continue
			}
			switch key {
			case "nameserver":
				servers = append(servers, strings.TrimSpace(value))
			case "search", "domain":
				search = append(search, strings.Fields(value)...)
			}
		}
		f.Close()
		break
	}
	return servers, search
}
