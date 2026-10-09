package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Link is one network device as the kernel reports it, with what the rest of
// the host says about it: who made it, what it is for, which container sits
// behind it and how fast it is moving bytes now.
type Link struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
	// Kind is the kernel's driver kind (bridge, veth, vlan, vxlan, gre,
	// wireguard, tun, dummy, macvlan, bond, …), "physical" for a NIC and
	// "loopback" for lo.
	Kind string `json:"kind"`
	// Role is what it is for: uplink, loopback, tunnel, bridge, container,
	// vlan, virtual or physical. An uplink is any device carrying a default
	// route, whatever its kind.
	Role string `json:"role"`
	// Owner is who manages it: kernel, system (the distribution's network
	// configuration or the provider's), docker, tailscale, wireguard,
	// libvirt, lxd or just-dashboard.
	Owner string `json:"owner"`
	// State is the operational state, lower-cased: up, down, unknown,
	// dormant, lowerlayerdown. A tunnel that is working reads "unknown",
	// which is why AdminUp and Carrier are carried beside it.
	State   string `json:"state"`
	AdminUp bool   `json:"adminUp"`
	Carrier bool   `json:"carrier"`
	MTU     int    `json:"mtu"`
	MAC     string `json:"mac,omitempty"`
	Qdisc   string `json:"qdisc,omitempty"`
	// SpeedMbps is the negotiated speed, zero where the driver does not say
	// (every virtual device, and most cloud NICs).
	SpeedMbps int `json:"speedMbps,omitempty"`
	// Master is the bridge or bond this device is a port of; Members are a
	// bridge's own ports.
	Master  string   `json:"master,omitempty"`
	Members []string `json:"members,omitempty"`
	// Parent is the device a VLAN, macvlan or VXLAN rides on.
	Parent string `json:"parent,omitempty"`
	// Tunnel facts, where the kind has them.
	VLANID int    `json:"vlanId,omitempty"`
	VNI    int    `json:"vni,omitempty"`
	Local  string `json:"local,omitempty"`
	Remote string `json:"remote,omitempty"`
	Port   int    `json:"port,omitempty"`

	Addresses []Address `json:"addresses"`
	// Uplink marks a device carrying a default route.
	Uplink bool `json:"uplink"`
	// ClientPath marks the device the reply to the reader's browser leaves
	// through; it is guarded like the uplink.
	ClientPath bool `json:"clientPath,omitempty"`

	Counters Counters `json:"counters"`
	// RxRate and TxRate are bytes a second over the sampler's last interval.
	RxRate float64 `json:"rxRate"`
	TxRate float64 `json:"txRate"`

	// Container names the container a veth leads into; DockerNetwork the
	// Docker network a bridge carries.
	Container      string `json:"container,omitempty"`
	ContainerImage string `json:"containerImage,omitempty"`
	DockerNetwork  string `json:"dockerNetwork,omitempty"`
	// XDP names the eBPF program attached at the driver, if one is.
	XDP string `json:"xdp,omitempty"`
	// Managed marks a device the dashboard created, and so may remove.
	Managed bool `json:"managed"`
	// Guard is why this device may not be set down, deleted or re-parented,
	// when it may not.
	Guard string `json:"guard,omitempty"`
}

// Address is one address on a device.
type Address struct {
	CIDR    string `json:"cidr"`
	Family  string `json:"family"`
	Scope   string `json:"scope"`
	Dynamic bool   `json:"dynamic,omitempty"`
	// Public is a globally routable address: what the internet reaches.
	Public bool `json:"public,omitempty"`
	// Managed is an address the dashboard added, and so may remove.
	Managed bool `json:"managed,omitempty"`
	// Guard is why it may not be removed, when it may not.
	Guard string `json:"guard,omitempty"`
}

// Counters are a device's totals since it was created.
type Counters struct {
	RxBytes   uint64 `json:"rxBytes"`
	TxBytes   uint64 `json:"txBytes"`
	RxPackets uint64 `json:"rxPackets"`
	TxPackets uint64 `json:"txPackets"`
	RxErrors  uint64 `json:"rxErrors"`
	TxErrors  uint64 `json:"txErrors"`
	RxDropped uint64 `json:"rxDropped"`
	TxDropped uint64 `json:"txDropped"`
}

// Inventory is what the rest of the server knows that the kernel does not:
// which container a process is and which Docker network a bridge is. The api
// package fills it from the Docker module, so this package does not depend
// on how containers are discovered.
type Inventory struct {
	Containers []ContainerNet `json:"-"`
	Networks   []DockerNet    `json:"-"`
	// An unreadable Docker inventory is not evidence that forwarding is unused.
	DockerNetworksUnknown bool `json:"-"`
}

// ContainerNet is a running container's identity and its init process.
type ContainerNet struct {
	ID    string
	Name  string
	Image string
	PID   int
}

// DockerNet is a Docker network and the bridge that carries it.
type DockerNet struct {
	ID      string
	Name    string
	Driver  string
	Bridge  string
	IPv6    bool
	Subnets []string
}

// ipLink is `ip -j -d -s link show`'s shape, the fields read here.
type ipLink struct {
	IfIndex   int             `json:"ifindex"`
	IfName    string          `json:"ifname"`
	Flags     []string        `json:"flags"`
	MTU       int             `json:"mtu"`
	Qdisc     string          `json:"qdisc"`
	Master    string          `json:"master"`
	OperState string          `json:"operstate"`
	LinkType  string          `json:"link_type"`
	Address   string          `json:"address"`
	Link      string          `json:"link"`
	LinkIndex int             `json:"link_index"`
	LinkNS    json.RawMessage `json:"link_netnsid,omitempty"`
	LinkInfo  struct {
		InfoKind string          `json:"info_kind"`
		InfoData json.RawMessage `json:"info_data"`
	} `json:"linkinfo"`
	XDP *struct {
		Prog *struct {
			Name string `json:"name"`
			ID   int    `json:"id"`
		} `json:"prog"`
	} `json:"xdp"`
	Stats64 *struct {
		Rx struct {
			Bytes   uint64 `json:"bytes"`
			Packets uint64 `json:"packets"`
			Errors  uint64 `json:"errors"`
			Dropped uint64 `json:"dropped"`
		} `json:"rx"`
		Tx struct {
			Bytes   uint64 `json:"bytes"`
			Packets uint64 `json:"packets"`
			Errors  uint64 `json:"errors"`
			Dropped uint64 `json:"dropped"`
		} `json:"tx"`
	} `json:"stats64"`
}

// ipAddr is `ip -j addr show`'s shape.
type ipAddr struct {
	IfName   string `json:"ifname"`
	AddrInfo []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
		Dynamic   bool   `json:"dynamic"`
	} `json:"addr_info"`
}

// tunnelData is the info_data fields of the tunnel kinds, read loosely: each
// kind names the same idea slightly differently.
type tunnelData struct {
	ID       any    `json:"id"`
	Protocol string `json:"protocol"`
	Remote   string `json:"remote"`
	Group    string `json:"group"`
	Local    string `json:"local"`
	Port     int    `json:"port"`
	Link     string `json:"link"`
	Mode     string `json:"mode"`
}

// ReadLinks reads every device in the host's network namespace.
//
// client is the address the request came from, so the device its reply
// leaves through is marked and guarded; empty marks nothing beyond the
// uplinks.
func (s *Service) ReadLinks(ctx context.Context, inv Inventory, client string) ([]Link, error) {
	linkOut, err := run(ctx, "ip", "-j", "-d", "-s", "link", "show")
	if err != nil {
		return nil, err
	}
	addrOut, err := run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		return nil, err
	}
	uplinks := readUplinks(ctx)
	path, _ := clientPath(ctx, client)
	spec, err := s.loadSpec()
	if err != nil {
		spec = emptySpec()
	}
	links, err := parseLinks(linkOut, addrOut)
	if err != nil {
		return nil, err
	}
	veths := containerVeths(inv.Containers)
	annotate(links, annotation{
		uplinks: uplinks, path: path, spec: spec, inv: inv, veths: veths,
		speed: linkSpeed, rates: s.sampler.Rates(),
	})
	return links, nil
}

// annotation is everything beyond the kernel's own reading that a link list
// is joined with, gathered so annotate stays a pure function.
type annotation struct {
	uplinks map[string]bool
	path    Path
	spec    *Spec
	inv     Inventory
	// veths maps a host-side veth's ifindex to the container behind it.
	veths map[int]ContainerNet
	speed func(name string) int
	rates map[string]Rate
}

// parseLinks joins the link and address dumps.
func parseLinks(linkOut, addrOut string) ([]Link, error) {
	var raw []ipLink
	if err := json.Unmarshal([]byte(linkOut), &raw); err != nil {
		return nil, fmt.Errorf("ip link printed something unreadable: %w", err)
	}
	var addrs []ipAddr
	if err := json.Unmarshal([]byte(addrOut), &addrs); err != nil {
		return nil, fmt.Errorf("ip addr printed something unreadable: %w", err)
	}
	byName := map[string][]Address{}
	for _, a := range addrs {
		for _, info := range a.AddrInfo {
			addr, err := netip.ParseAddr(info.Local)
			if err != nil {
				continue
			}
			byName[a.IfName] = append(byName[a.IfName], Address{
				CIDR:    netip.PrefixFrom(addr, info.PrefixLen).String(),
				Family:  info.Family,
				Scope:   info.Scope,
				Dynamic: info.Dynamic,
				Public:  isPublic(addr),
			})
		}
	}
	byIndex := map[int]string{}
	for _, l := range raw {
		byIndex[l.IfIndex] = l.IfName
	}
	links := make([]Link, 0, len(raw))
	for _, l := range raw {
		link := Link{
			Name: l.IfName, Index: l.IfIndex, MTU: l.MTU, MAC: l.Address, Qdisc: l.Qdisc,
			Master: l.Master, State: strings.ToLower(l.OperState),
			Addresses: byName[l.IfName],
		}
		if link.Addresses == nil {
			link.Addresses = []Address{}
		}
		for _, f := range l.Flags {
			switch f {
			case "UP":
				link.AdminUp = true
			case "LOWER_UP":
				link.Carrier = true
			}
		}
		switch {
		case l.LinkType == "loopback":
			link.Kind = "loopback"
		case l.LinkInfo.InfoKind == "":
			link.Kind = "physical"
		default:
			link.Kind = l.LinkInfo.InfoKind
		}
		if len(l.LinkInfo.InfoData) > 0 {
			var td tunnelData
			if json.Unmarshal(l.LinkInfo.InfoData, &td) == nil {
				switch v := td.ID.(type) {
				case float64:
					if link.Kind == "vlan" {
						link.VLANID = int(v)
					} else if link.Kind == "vxlan" {
						link.VNI = int(v)
					}
				}
				link.Remote = firstNonEmpty(td.Remote, td.Group)
				link.Local = td.Local
				link.Port = td.Port
			}
		}
		// A VLAN or macvlan names its parent as "link"; ip prints the name
		// when it is in the same namespace and only the index when not.
		if l.Link != "" && (link.Kind == "vlan" || link.Kind == "macvlan" || link.Kind == "ipvlan" || link.Kind == "vxlan") {
			link.Parent = l.Link
		} else if l.LinkIndex > 0 && link.Kind != "veth" {
			if name, ok := byIndex[l.LinkIndex]; ok {
				link.Parent = name
			}
		}
		if l.XDP != nil && l.XDP.Prog != nil {
			link.XDP = firstNonEmpty(l.XDP.Prog.Name, "#"+strconv.Itoa(l.XDP.Prog.ID))
		}
		if l.Stats64 != nil {
			link.Counters = Counters{
				RxBytes: l.Stats64.Rx.Bytes, TxBytes: l.Stats64.Tx.Bytes,
				RxPackets: l.Stats64.Rx.Packets, TxPackets: l.Stats64.Tx.Packets,
				RxErrors: l.Stats64.Rx.Errors, TxErrors: l.Stats64.Tx.Errors,
				RxDropped: l.Stats64.Rx.Dropped, TxDropped: l.Stats64.Tx.Dropped,
			}
		}
		links = append(links, link)
	}
	members := map[string][]string{}
	for _, l := range links {
		if l.Master != "" {
			members[l.Master] = append(members[l.Master], l.Name)
		}
	}
	for i := range links {
		if m := members[links[i].Name]; len(m) > 0 {
			sort.Strings(m)
			links[i].Members = m
		}
	}
	return links, nil
}

// annotate fills in roles, owners, guards, containers and rates.
func annotate(links []Link, a annotation) {
	bridges := map[string]DockerNet{}
	for _, n := range a.inv.Networks {
		if n.Bridge != "" {
			bridges[n.Bridge] = n
		}
	}
	dockerBridge := func(name string) bool {
		_, ok := bridges[name]
		return ok || name == "docker0" || isDockerBridgeName(name)
	}
	if a.spec == nil {
		a.spec = emptySpec()
	}
	extra := map[string]map[string]bool{}
	for _, as := range a.spec.Addresses {
		if extra[as.Link] == nil {
			extra[as.Link] = map[string]bool{}
		}
		extra[as.Link][as.CIDR] = true
	}
	for i := range links {
		l := &links[i]
		l.Uplink = a.uplinks[l.Name]
		l.ClientPath = a.path.Device != "" && !a.path.Local && a.path.Device == l.Name
		if n, ok := bridges[l.Name]; ok {
			l.DockerNetwork = n.Name
		}
		if l.Kind == "veth" {
			if c, ok := a.veths[l.Index]; ok {
				l.Container, l.ContainerImage = c.Name, c.Image
			}
		}
		if a.speed != nil && (l.Kind == "physical" || l.Kind == "bond") {
			l.SpeedMbps = a.speed(l.Name)
		}
		if r, ok := a.rates[l.Name]; ok {
			l.RxRate, l.TxRate = r.Rx, r.Tx
		}
		_, made := a.spec.link(l.Name)
		l.Managed = made
		l.Owner = ownerOf(*l, made, dockerBridge)
		l.Role = roleOf(*l)
		l.Guard = linkGuard(*l)
		for j := range l.Addresses {
			addr := &l.Addresses[j]
			addr.Managed = made || extra[l.Name][addr.CIDR]
			addr.Guard = addressGuard(*l, *addr, a.path)
		}
	}
	sort.SliceStable(links, func(i, j int) bool {
		ri, rj := roleRank(links[i].Role), roleRank(links[j].Role)
		if ri != rj {
			return ri < rj
		}
		return links[i].Name < links[j].Name
	})
}

// fallbackDevices are the devices a tunnel module makes for itself the moment
// it loads: the catch-all for packets no configured tunnel claims. They are
// always down, carry nothing, and cannot be deleted while the module is
// loaded, so they are the kernel's rather than this system's.
var fallbackDevices = map[string]bool{
	"gre0": true, "gretap0": true, "erspan0": true, "ip6gre0": true, "ip6tnl0": true,
	"sit0": true, "tunl0": true, "ip_vti0": true, "ip6_vti0": true,
}

func ownerOf(l Link, managed bool, dockerBridge func(string) bool) string {
	switch {
	case managed:
		return "just-dashboard"
	case l.Kind == "loopback", fallbackDevices[l.Name]:
		return "kernel"
	case strings.HasPrefix(l.Name, "tailscale"):
		return "tailscale"
	case l.Kind == "wireguard":
		return "wireguard"
	case dockerBridge(l.Name), l.Container != "", l.Kind == "veth" && l.Master != "" && dockerBridge(l.Master):
		return "docker"
	case strings.HasPrefix(l.Name, "virbr") || strings.HasPrefix(l.Name, "vnet"):
		return "libvirt"
	case strings.HasPrefix(l.Name, "lxdbr") || strings.HasPrefix(l.Name, "incusbr"):
		return "lxd"
	}
	return "system"
}

func roleOf(l Link) string {
	switch l.Kind {
	case "loopback":
		return "loopback"
	}
	if l.Uplink {
		return "uplink"
	}
	switch l.Kind {
	case "tun", "tap", "wireguard", "gre", "gretap", "ip6gre", "ip6gretap", "vxlan", "ipip", "sit", "ip6tnl", "geneve":
		return "tunnel"
	case "bridge":
		return "bridge"
	case "veth":
		if l.Container != "" || l.Owner == "docker" {
			return "container"
		}
		return "virtual"
	case "vlan", "macvlan", "ipvlan":
		return "vlan"
	case "physical", "bond", "team":
		return "physical"
	}
	return "virtual"
}

func roleRank(role string) int {
	switch role {
	case "uplink":
		return 0
	case "physical":
		return 1
	case "tunnel":
		return 2
	case "vlan":
		return 3
	case "bridge":
		return 4
	case "virtual":
		return 5
	case "container":
		return 6
	}
	return 7
}

// linkGuard says why a device may not be set down, deleted or re-parented.
func linkGuard(l Link) string {
	switch {
	case l.Kind == "loopback":
		return "Loopback carries this machine's own traffic, the dashboard's included."
	case fallbackDevices[l.Name]:
		return "The kernel made this when the tunnel module loaded; it carries nothing and goes when the module does."
	case l.ClientPath:
		return fmt.Sprintf("Your browser is reached through %s.", l.Name)
	case l.Uplink:
		return fmt.Sprintf("%s carries the default route: it is how this server reaches the internet.", l.Name)
	case l.Owner == "docker":
		return "Docker owns this device and recreates it as it needs; change it through the Docker pages."
	case l.Owner == "tailscale":
		return "tailscaled owns this device; change it through Tailscale on the VPN page."
	}
	return ""
}

// addressGuard says why an address may not be removed.
func addressGuard(l Link, a Address, path Path) string {
	p, err := netip.ParsePrefix(a.CIDR)
	if err != nil {
		return ""
	}
	if path.Source != "" && p.Addr().String() == path.Source {
		return "Your browser is answered from this address."
	}
	if l.Uplink && a.Scope == "global" && !a.Managed {
		return "The uplink's own address: the provider assigned it and the internet reaches this server at it."
	}
	if l.Owner == "docker" || l.Owner == "tailscale" {
		return linkGuard(l)
	}
	return ""
}

// readUplinks names the devices carrying a default route, either family.
func readUplinks(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	for _, family := range [][]string{{"-j", "route", "show", "default"}, {"-j", "-6", "route", "show", "default"}} {
		raw, err := run(ctx, "ip", family...)
		if err != nil {
			continue
		}
		var routes []struct {
			Dev string `json:"dev"`
		}
		if json.Unmarshal([]byte(raw), &routes) != nil {
			continue
		}
		for _, r := range routes {
			if r.Dev != "" {
				out[r.Dev] = true
			}
		}
	}
	return out
}

// procRoot is where a process's root is reached from; a variable for tests.
var procRoot = "/proc"

// containerVeths maps each host-side veth to the container behind it. A
// container's own devices name, in iflink, the index of their peer in the
// host's namespace; reading that through /proc/<pid>/root/sys needs no
// subprocess and no entry into the container's namespace.
func containerVeths(containers []ContainerNet) map[int]ContainerNet {
	out := map[int]ContainerNet{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, c := range containers {
		if c.PID <= 0 {
			continue
		}
		wg.Add(1)
		go func(c ContainerNet) {
			defer wg.Done()
			dir := filepath.Join(procRoot, strconv.Itoa(c.PID), "root", "sys", "class", "net")
			entries, err := os.ReadDir(dir)
			if err != nil {
				return
			}
			for _, e := range entries {
				if e.Name() == "lo" {
					continue
				}
				b, err := os.ReadFile(filepath.Join(dir, e.Name(), "iflink"))
				if err != nil {
					continue
				}
				idx, err := strconv.Atoi(strings.TrimSpace(string(b)))
				if err != nil {
					continue
				}
				mu.Lock()
				out[idx] = c
				mu.Unlock()
			}
		}(c)
	}
	wg.Wait()
	return out
}

// linkSpeed reads a NIC's negotiated speed. -1 (no link, or a driver that
// does not say) reads as zero.
func linkSpeed(name string) int {
	b, err := os.ReadFile(filepath.Join("/sys/class/net", name, "speed"))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// isDockerBridgeName matches the br-<12 hex> name Docker gives a
// user-defined network's bridge.
func isDockerBridgeName(name string) bool {
	id, ok := strings.CutPrefix(name, "br-")
	return ok && len(id) == 12 && strings.Trim(id, "0123456789abcdef") == ""
}

// cgnat is the shared address space carriers and Tailscale use; routable
// nowhere but inside the network that assigned it.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// isPublic is a globally routable address: what the internet can reach.
func isPublic(a netip.Addr) bool {
	return a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
