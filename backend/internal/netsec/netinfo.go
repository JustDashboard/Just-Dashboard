package netsec

import (
	"bufio"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"net"

	gnet "github.com/shirou/gopsutil/v4/net"
	"golang.org/x/sys/unix"

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
	// a list that does not separate them from eth0 is unreadable. A bridge
	// that enslaves a NIC is the network behind it and counts as physical
	// (see classifyLinks).
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
	kinds := readInterfaceKinds()
	for _, ifc := range ifaces {
		item := Interface{
			Name: ifc.Name, MTU: ifc.MTU, MAC: ifc.HardwareAddr,
			Addresses: []string{}, Kind: kinds.of(ifc.Name),
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

// ReadHostNetwork places every address the host holds on its interface, and
// marks the interfaces that carry a default route.
//
// It reads the namespace the listening sockets are read from — Go's own
// interface list, the kernel's link table and /proc/net/{route,ipv6_route},
// under HOST_PROC when set — rather than shelling to `ip` on the host as
// NetworkInfo does for its routes, because a socket's reach is only
// meaningful against the interfaces of the namespace it is bound in, and
// because the ports page asks on every poll. An interface list that cannot be
// read leaves the network unknown, which judges every private address as the
// uplink's: the conservative reading.
func ReadHostNetwork(ctx context.Context) HostNetwork {
	ifaces, err := gnet.InterfacesWithContext(ctx)
	if err != nil {
		return HostNetwork{}
	}
	root := "/proc"
	if hostProc := os.Getenv("HOST_PROC"); hostProc != "" {
		root = hostProc
	}
	route4, _ := os.ReadFile(filepath.Join(root, "net", "route"))
	route6, _ := os.ReadFile(filepath.Join(root, "net", "ipv6_route"))
	return hostNetworkFrom(ifaces, defaultRouteInterfaces(route4, route6), readInterfaceKinds())
}

func hostNetworkFrom(ifaces gnet.InterfaceStatList, uplinks map[string]bool, kinds interfaceKinds) HostNetwork {
	n := HostNetwork{}
	for _, ifc := range ifaces {
		kind := kinds.of(ifc.Name)
		for _, f := range ifc.Flags {
			if f == "loopback" {
				kind = "loopback"
			}
		}
		for _, a := range ifc.Addrs {
			ip, _, err := net.ParseCIDR(a.Addr)
			if err != nil {
				continue
			}
			n.Addresses = append(n.Addresses, HostAddress{
				IP: ip, Interface: ifc.Name, Kind: kind, DefaultRoute: uplinks[ifc.Name],
			})
		}
	}
	return n
}

// defaultRouteInterfaces reads the kernel's main routing tables for the
// interfaces a default route leaves by. IPv4's lists a destination and mask
// of zero; IPv6's lists ::/0, where the kernel also keeps unreachable
// defaults on lo, which carry RTF_REJECT and lead nowhere.
func defaultRouteInterfaces(route4, route6 []byte) map[string]bool {
	const rtfUp, rtfReject = 0x1, 0x200
	up := func(hexFlags string) bool {
		flags, err := strconv.ParseUint(hexFlags, 16, 32)
		return err == nil && flags&rtfUp != 0 && flags&rtfReject == 0
	}
	out := map[string]bool{}
	for i, line := range strings.Split(string(route4), "\n") {
		f := strings.Fields(line)
		// Iface Destination Gateway Flags RefCnt Use Metric Mask …
		if i == 0 || len(f) < 8 {
			continue
		}
		if f[1] == "00000000" && f[7] == "00000000" && up(f[3]) {
			out[f[0]] = true
		}
	}
	for _, line := range strings.Split(string(route6), "\n") {
		f := strings.Fields(line)
		// dest destlen src srclen nexthop metric refcnt use flags iface
		if len(f) < 10 {
			continue
		}
		if strings.Trim(f[0], "0") == "" && f[1] == "00" && up(f[8]) && f[9] != "lo" {
			out[f[9]] = true
		}
	}
	return out
}

// interfaceKinds is each device's class, keyed by name. A device missing from
// it — the kernel's link table could not be read, or the device appeared
// after it was — is classified by its name.
type interfaceKinds map[string]string

func (k interfaceKinds) of(name string) string {
	if kind, ok := k[name]; ok {
		return kind
	}
	return ClassifyInterface(name)
}

// link is one device as the kernel's link table describes it.
type link struct {
	name          string
	index, master uint32
	// kind is the driver's rtnetlink kind, which `ip -d link` prints:
	// "bridge", "veth", "tun" (a tap too), "bond", "vlan", "vxlan", … and
	// empty for a NIC.
	kind string
}

// readInterfaceKinds classifies the devices of this process's network
// namespace — the one Go's interface list reads — from the kernel's link
// table, which answers per device what the name only suggests.
func readInterfaceKinds() interfaceKinds {
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETLINK, syscall.AF_UNSPEC)
	if err != nil {
		return nil
	}
	return classifyLinks(parseLinks(rib))
}

// parseLinks reads an RTM_GETLINK dump.
func parseLinks(rib []byte) []link {
	msgs, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return nil
	}
	var links []link
	for i := range msgs {
		m := &msgs[i]
		if m.Header.Type != syscall.RTM_NEWLINK || len(m.Data) < syscall.SizeofIfInfomsg {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(m)
		if err != nil {
			continue
		}
		// ifinfomsg: family, pad, type (16 bits), then the index.
		l := link{index: binary.NativeEndian.Uint32(m.Data[4:8])}
		for _, a := range attrs {
			switch a.Attr.Type {
			case syscall.IFLA_IFNAME:
				l.name = strings.TrimRight(string(a.Value), "\x00")
			case syscall.IFLA_MASTER:
				if len(a.Value) >= 4 {
					l.master = binary.NativeEndian.Uint32(a.Value)
				}
			case syscall.IFLA_LINKINFO:
				l.kind = linkInfoKind(a.Value)
			}
		}
		links = append(links, l)
	}
	return links
}

// linkInfoKind reads IFLA_INFO_KIND out of an IFLA_LINKINFO nest.
func linkInfoKind(nest []byte) string {
	for len(nest) >= syscall.SizeofRtAttr {
		size := int(binary.NativeEndian.Uint16(nest[0:2]))
		if size < syscall.SizeofRtAttr || size > len(nest) {
			return ""
		}
		if binary.NativeEndian.Uint16(nest[2:4]) == unix.IFLA_INFO_KIND {
			return strings.TrimRight(string(nest[syscall.SizeofRtAttr:size]), "\x00")
		}
		aligned := (size + syscall.RTA_ALIGNTO - 1) &^ (syscall.RTA_ALIGNTO - 1)
		nest = nest[min(aligned, len(nest)):]
	}
	return ""
}

// guestPortKinds are the bridge ports that lead only to this host's own
// guests: a container's veth, a virtual machine's tap, and the dummy some
// setups enslave to keep an empty bridge up.
var guestPortKinds = map[string]bool{"veth": true, "tun": true, "dummy": true}

// classifyLinks grades a bridge by what the kernel says it is rather than by
// its name. Names do not tell: LXD's lxdbr0, Incus's incusbr0 and Podman's
// podman1 follow no convention Docker's or libvirt's do, and a br-lan may
// enslave a second NIC. A Linux bridge is a "bridge" — this host's containers
// and VMs — while every port on it is a guest's; one that enslaves a NIC, a
// bond, a VLAN or a tunnel to other hosts is that network, and physical. So
// is a device named like a bridge that the kernel does not call one, such as
// an Open vSwitch bridge, whose ports are not in this table — unless it is a
// guest port itself, as libvirt's virbr0-nic tap is. Every other device keeps
// its name's class.
func classifyLinks(links []link) interfaceKinds {
	reachesOut := map[uint32]bool{}
	for _, l := range links {
		if l.master != 0 && !guestPortKinds[l.kind] {
			reachesOut[l.master] = true
		}
	}
	kinds := interfaceKinds{}
	for _, l := range links {
		named := ClassifyInterface(l.name)
		switch {
		case l.kind == "bridge" && reachesOut[l.index]:
			kinds[l.name] = "physical"
		case l.kind == "bridge":
			kinds[l.name] = "bridge"
		case named == "bridge" && guestPortKinds[l.kind]:
			kinds[l.name] = "virtual"
		case named == "bridge":
			kinds[l.name] = "physical"
		default:
			kinds[l.name] = named
		}
	}
	return kinds
}

// ClassifyInterface names a device from its name alone, which is what the
// kernel and every tool on the host agree on, for when the kernel's link
// table is not to hand.
func ClassifyInterface(name string) string {
	switch {
	case name == "lo":
		return "loopback"
	case strings.HasPrefix(name, "tailscale"), strings.HasPrefix(name, "wg"),
		strings.HasPrefix(name, "tun"), strings.HasPrefix(name, "tap"),
		strings.HasPrefix(name, "zt"), strings.HasPrefix(name, "nebula"):
		return "tunnel"
	case strings.HasPrefix(name, "docker"), isDockerNetworkBridge(name),
		strings.HasPrefix(name, "virbr"), strings.HasPrefix(name, "lxdbr"),
		strings.HasPrefix(name, "incusbr"), strings.HasPrefix(name, "podman"),
		strings.HasPrefix(name, "cni-podman"), name == "cni0":
		return "bridge"
	case strings.HasPrefix(name, "veth"), strings.HasPrefix(name, "vnet"),
		strings.HasPrefix(name, "cali"), strings.HasPrefix(name, "flannel"):
		return "virtual"
	}
	return "physical"
}

// isDockerNetworkBridge matches the br-<12 hex> Docker names a user-defined
// network's bridge, and not a br-lan somebody made for a second NIC.
func isDockerNetworkBridge(name string) bool {
	id, ok := strings.CutPrefix(name, "br-")
	if !ok || len(id) != 12 {
		return false
	}
	return strings.Trim(id, "0123456789abcdef") == ""
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
