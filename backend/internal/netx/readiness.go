package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LinkReadiness is what can be established, from this host alone, about
// whether a tunnel or virtual device can carry traffic: its underlay, its
// ends, its MTU and whether anything has arrived through it. Each check says
// what it read; nothing here sends a packet to the other end, so a passing
// check is never a reachability proof.
type LinkReadiness struct {
	Name      string           `json:"name"`
	Kind      string           `json:"kind"`
	CheckedAt time.Time        `json:"checkedAt"`
	Checks    []ReadinessCheck `json:"checks"`
	// Limits are what these checks cannot see.
	Limits []string `json:"limits"`
}

// ReadinessCheck is one check: ok, warning, failed, unknown, or info for a
// fact the operator should know (macvlan host isolation) that is neither.
type ReadinessCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// Encapsulation overhead per kind and outer family, in bytes: the outer IP
// header plus the tunnel's own (UDP+VXLAN 16, GRE 4, GRETAP's inner Ethernet
// 14 more, a key 4 more).
func tunnelOverhead(kind string, v6 bool, key bool) int {
	outer := 20
	if v6 {
		outer = 40
	}
	switch kind {
	case "vxlan":
		return outer + 8 + 8 + 14
	case "gre", "ip6gre":
		n := outer + 4
		if key {
			n += 4
		}
		return n
	case "gretap", "ip6gretap":
		n := outer + 4 + 14
		if key {
			n += 4
		}
		return n
	}
	return 0
}

// cloudDrivers are NIC drivers of virtual machines whose provider filters
// frames by the MAC address it assigned.
var cloudDrivers = map[string]bool{
	"virtio_net": true, "ena": true, "hv_netvsc": true, "gve": true, "vmxnet3": true,
	"xen-netfront": true, "vif": true,
}

// rawLink is the part of `ip -j -d -s link show` readiness needs beyond Link.
type rawLink struct {
	IfName      string   `json:"ifname"`
	Flags       []string `json:"flags"`
	Promiscuity int      `json:"promiscuity"`
	LinkInfo    struct {
		InfoKind string `json:"info_kind"`
		InfoData struct {
			Mode string `json:"mode"`
			IKey string `json:"ikey"`
			OKey string `json:"okey"`
		} `json:"info_data"`
	} `json:"linkinfo"`
}

// Readiness checks one device. Kinds without readiness checks answer with
// none and a limit saying so.
func (s *Service) Readiness(ctx context.Context, name string) (*LinkReadiness, error) {
	if err := ValidIfName(name); err != nil {
		return nil, err
	}
	linkOut, err := run(ctx, "ip", "-j", "-d", "-s", "link", "show")
	if err != nil {
		return nil, err
	}
	addrOut, err := run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		return nil, err
	}
	links, err := parseLinks(linkOut, addrOut)
	if err != nil {
		return nil, err
	}
	var raws []rawLink
	if err := json.Unmarshal([]byte(linkOut), &raws); err != nil {
		return nil, fmt.Errorf("ip link printed something unreadable: %w", err)
	}
	by := map[string]*Link{}
	raw := map[string]rawLink{}
	for i := range links {
		by[links[i].Name] = &links[i]
	}
	for _, r := range raws {
		raw[r.IfName] = r
	}
	dev, ok := by[name]
	if !ok {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	sp, err := s.loadSpec()
	if err != nil {
		sp = emptySpec()
	}
	r := &LinkReadiness{Name: name, Kind: dev.Kind, CheckedAt: time.Now().UTC(), Checks: []ReadinessCheck{}, Limits: []string{}}
	check := func(id, label, state, format string, args ...any) {
		r.Checks = append(r.Checks, ReadinessCheck{ID: id, Label: label, State: state, Detail: fmt.Sprintf(format, args...)})
	}
	local := map[netip.Addr]string{}
	for _, l := range links {
		for _, a := range l.Addresses {
			if p, err := netip.ParsePrefix(a.CIDR); err == nil {
				local[p.Addr()] = l.Name
			}
		}
	}

	switch dev.Kind {
	case "vxlan", "gre", "gretap", "ip6gre", "ip6gretap":
		managed, _ := sp.link(name)
		ends := []string{}
		multicast := false
		if dev.Remote != "" {
			if a, err := netip.ParseAddr(dev.Remote); err == nil && a.IsMulticast() {
				multicast = true
			} else {
				ends = append(ends, dev.Remote)
			}
		}
		if managed != nil {
			ends = append(ends, managed.Remotes...)
		}
		if dev.Parent != "" {
			parent, ok := by[dev.Parent]
			switch {
			case !ok:
				check("underlay", "Underlay device", "failed", "%s sends from %s, which does not exist.", name, dev.Parent)
			case !parent.AdminUp || !parent.Carrier:
				check("underlay", "Underlay device", "failed", "%s sends from %s, which is down or has no carrier.", name, dev.Parent)
			case multicast && !hasFlag(raw[dev.Parent].Flags, "MULTICAST"):
				check("underlay", "Underlay device", "failed", "%s floods to a multicast group through %s, which does not do multicast.", name, dev.Parent)
			default:
				check("underlay", "Underlay device", "ok", "%s sends from %s, which is up with a carrier.", name, dev.Parent)
			}
		}
		if dev.Local != "" {
			a, err := netip.ParseAddr(dev.Local)
			if holder, ok := local[a]; err == nil && ok {
				check("local", "Local end", "ok", "%s is an address of this host, on %s.", dev.Local, holder)
			} else {
				check("local", "Local end", "failed", "%s is not an address of this host; packets leave from it anyway and the replies have nowhere to arrive.", dev.Local)
			}
		}
		underlayMTU := 0
		for _, end := range ends {
			args := []string{"-j", "route", "get", end}
			if a, err := netip.ParseAddr(end); err == nil && a.Is6() {
				args = []string{"-j", "-6", "route", "get", end}
			}
			out, err := run(ctx, "ip", args...)
			if err != nil {
				check("route:"+end, "Route to "+end, "failed", "The kernel has no route to %s: %s", end, firstLines(err.Error(), 1))
				continue
			}
			p, err := parseRouteGet(out, Path{Address: end})
			switch {
			case err != nil:
				check("route:"+end, "Route to "+end, "unknown", "The route to %s could not be read.", end)
			case p.Device == name:
				check("route:"+end, "Route to "+end, "failed", "The route to %s leaves through %s itself, so the tunnel would carry its own packets.", end, name)
			case p.Local:
				check("route:"+end, "Route to "+end, "failed", "%s is an address of this host, not the other end.", end)
			default:
				via := ""
				if p.Gateway != "" {
					via = " via " + p.Gateway
				}
				check("route:"+end, "Route to "+end, "ok", "Reached through %s%s, from %s. The kernel's route, not a probe of the other end.", p.Device, via, firstNonEmpty(p.Source, "the kernel's choice of source"))
				if u, ok := by[p.Device]; ok && (underlayMTU == 0 || u.MTU < underlayMTU) {
					underlayMTU = u.MTU
				}
			}
		}
		if multicast {
			check("route:"+dev.Remote, "Multicast group", "info", "Frames are flooded to %s. Whether the underlay network forwards that group is outside this host.", dev.Remote)
			if parent, ok := by[dev.Parent]; ok {
				underlayMTU = parent.MTU
			}
		}
		if len(ends) == 0 && !multicast {
			check("ends", "Other ends", "failed", "%s has no other end to send to.", name)
		}
		if underlayMTU > 0 {
			v6 := strings.HasPrefix(dev.Kind, "ip6")
			if a, err := netip.ParseAddr(firstNonEmpty(dev.Remote, dev.Local)); err == nil {
				v6 = a.Is6()
			}
			keyed := raw[name].LinkInfo.InfoData.IKey != "" || raw[name].LinkInfo.InfoData.OKey != ""
			overhead := tunnelOverhead(dev.Kind, v6, keyed)
			if fits := underlayMTU - overhead; fits >= dev.MTU {
				check("mtu", "MTU headroom", "ok", "%s's MTU %d fits inside the underlay's %d with %d bytes of encapsulation.", name, dev.MTU, underlayMTU, overhead)
			} else {
				check("mtu", "MTU headroom", "warning", "%s's MTU %d does not fit inside the underlay's %d with %d bytes of encapsulation; set it to %d or less, or larger packets are fragmented or dropped.", name, dev.MTU, underlayMTU, overhead, fits)
			}
		}
		if dev.Kind == "vxlan" && dev.Remote != "" && !multicast {
			out, err := run(ctx, "bridge", "-j", "fdb", "show", "dev", name)
			if err != nil {
				check("remotes", "Flood destinations", "unknown", "The forwarding database could not be read: %s", firstLines(err.Error(), 1))
			} else if entries, err := parseFDB(out); err != nil {
				check("remotes", "Flood destinations", "unknown", "The forwarding database could not be read.")
			} else {
				held := map[string]bool{}
				for _, e := range entries {
					if e.MAC == "00:00:00:00:00:00" && e.Dst != "" {
						held[e.Dst] = true
					}
				}
				var missing []string
				for _, end := range ends {
					if !held[end] {
						missing = append(missing, end)
					}
				}
				if len(missing) == 0 {
					check("remotes", "Flood destinations", "ok", "Broadcast and unknown frames are copied to %d end%s: %s.", len(ends), suffixS(len(ends)), strings.Join(ends, ", "))
				} else {
					check("remotes", "Flood destinations", "warning", "The kernel does not flood to %s, which this device should reach.", strings.Join(missing, ", "))
				}
			}
		}
		r.Checks = append(r.Checks, liveness(dev, s.sampler))
		if dev.Kind == "vxlan" {
			port := dev.Port
			if port == 0 {
				port = 8472
			}
			r.Limits = append(r.Limits,
				fmt.Sprintf("VXLAN is neither encrypted nor authenticated: anything that can reach UDP %d here can inject frames into the segment.", port),
				fmt.Sprintf("Whether the firewall and the provider admit UDP %d is not checked here.", port),
			)
		} else {
			r.Limits = append(r.Limits,
				"GRE is not encrypted: carry it over WireGuard or IPsec where the path is not trusted.",
				"GRE is IP protocol 47; whether the firewall and the provider pass it is not checked here.",
			)
		}
		r.Limits = append(r.Limits, "Routes are the kernel's answer; no packet is sent to the other end.")
	case "macvlan":
		mode := raw[name].LinkInfo.InfoData.Mode
		parent, ok := by[dev.Parent]
		switch {
		case !ok:
			check("parent", "Parent device", "failed", "%s rides on %s, which does not exist.", name, firstNonEmpty(dev.Parent, "a parent this host does not list"))
		case !parent.AdminUp || !parent.Carrier:
			check("parent", "Parent device", "failed", "%s rides on %s, which is down or has no carrier.", name, parent.Name)
		default:
			check("parent", "Parent device", "ok", "%s rides on %s, which is up with a carrier.", name, parent.Name)
		}
		siblings := 0
		for _, l := range links {
			if l.Kind == "macvlan" && l.Parent == dev.Parent && l.Name != name && raw[l.Name].LinkInfo.InfoData.Mode == mode {
				siblings++
			}
		}
		switch mode {
		case "bridge":
			check("isolation", "Host isolation", "info", "This host cannot reach %s's addresses through %s, nor %s the host's: frames between a macvlan and its parent are never delivered. Give the host its own macvlan on %s to talk to it.", name, dev.Parent, name, dev.Parent)
			check("siblings", "Siblings", "info", "%d other bridge-mode macvlan%s on %s reach %s directly, without leaving the host.", siblings, suffixS(siblings), dev.Parent, name)
		case "private":
			check("isolation", "Host isolation", "info", "%s reaches neither this host through %s nor any other macvlan on it; everything goes out to the network.", name, dev.Parent)
		case "vepa":
			check("isolation", "Host isolation", "info", "This host cannot reach %s through %s. Traffic between macvlans on %s goes out to the switch and must be reflected back by it (802.1Qbg reflective relay).", name, dev.Parent, dev.Parent)
		case "passthru":
			check("isolation", "Host isolation", "info", "%s takes every frame %s receives; the host's own stack on %s sees none.", name, dev.Parent, dev.Parent)
		default:
			check("isolation", "Mode", "unknown", "The macvlan mode could not be read.")
		}
		if ok {
			driver := ""
			if target, err := os.Readlink(filepath.Join(sysClassNet, parent.Name, "device", "driver")); err == nil {
				driver = filepath.Base(target)
			}
			switch {
			case cloudDrivers[driver]:
				check("mac", "Provider MAC filtering", "warning", "%s uses %s, a virtual machine's NIC. Cloud providers drop frames from MAC addresses they did not assign, so %s (%s) may never reach the network unless the provider allows it.", parent.Name, driver, name, firstNonEmpty(dev.MAC, "its own MAC"))
			case parent.Uplink:
				check("mac", "Provider MAC filtering", "warning", "%s is the uplink. Providers and switch port security often accept only the MAC they assigned; %s (%s) may be dropped upstream.", parent.Name, name, firstNonEmpty(dev.MAC, "its own MAC"))
			default:
				check("mac", "MAC filtering", "info", "%s sends with its own MAC (%s); the switch %s connects to must accept more than one MAC on its port.", name, firstNonEmpty(dev.MAC, "unknown"), parent.Name)
			}
		}
		if !holdsAddress(dev) {
			check("address", "Address", "warning", "%s has no address, so it carries nothing at layer 3 yet.", name)
		} else {
			check("address", "Address", "ok", "%s has %d address%s.", name, len(dev.Addresses), suffixES(len(dev.Addresses)))
		}
		r.Checks = append(r.Checks, liveness(dev, s.sampler))
		r.Limits = append(r.Limits, "Connectivity to the outside network is not probed; these checks read this host only.")
	case "dummy":
		check("address", "Address", stateIf(holdsAddress(dev), "ok", "warning"), "%s", dummyAddressDetail(dev))
		var routes []string
		for _, args := range [][]string{{"-j", "route", "show", "dev", name}, {"-j", "-6", "route", "show", "dev", name}} {
			out, err := run(ctx, "ip", args...)
			if err != nil {
				continue
			}
			var got []struct {
				Dst      string `json:"dst"`
				Protocol string `json:"protocol"`
				Type     string `json:"type"`
			}
			if json.Unmarshal([]byte(out), &got) != nil {
				continue
			}
			for _, g := range got {
				if g.Protocol != "kernel" && !strings.HasPrefix(g.Dst, "fe80::") {
					routes = append(routes, g.Dst)
				}
			}
		}
		if len(routes) == 0 {
			check("routes", "Routes into it", "info", "No route sends traffic into %s. A route with %s as its device makes it a sink for that destination.", name, name)
		} else {
			check("routes", "Routes into it", "ok", "%s receive%s %s.", strings.Join(routes, ", "), verbS(len(routes)), name)
		}
		out, err := run(ctx, "ss", "-H", "-l", "-t", "-u", "-n")
		if err != nil {
			check("services", "Services on its addresses", "unknown", "Listening sockets could not be read: %s", firstLines(err.Error(), 1))
		} else {
			mine := map[string]bool{}
			for _, a := range dev.Addresses {
				if p, err := netip.ParsePrefix(a.CIDR); err == nil {
					mine[p.Addr().String()] = true
				}
			}
			var bound []string
			for _, l := range parseListeners(out) {
				if mine[l.Address] {
					bound = append(bound, fmt.Sprintf("%s %s:%d", l.Protocol, l.Address, l.Port))
				}
			}
			sort.Strings(bound)
			if len(bound) == 0 {
				check("services", "Services on its addresses", "info", "Nothing listens on %s's own addresses; services listening on every address answer there too.", name)
			} else {
				check("services", "Services on its addresses", "ok", "%s listen%s there.", strings.Join(bound, ", "), verbS(len(bound)))
			}
		}
		r.Limits = append(r.Limits, "A dummy device carries nothing off this host; what reaches its addresses from elsewhere depends on routes on other machines.")
	default:
		r.Limits = append(r.Limits, fmt.Sprintf("There are no readiness checks for a %s device.", dev.Kind))
	}
	return r, nil
}

// liveness says whether anything has arrived through a device: since it was
// made, and within the sampler's fifteen-minute ring.
func liveness(dev *Link, sampler *Sampler) ReadinessCheck {
	c := ReadinessCheck{ID: "liveness", Label: "Traffic received"}
	recent := false
	if sampler != nil {
		for _, p := range sampler.Live(0)[dev.Name] {
			if p.Rx > 0 {
				recent = true
			}
		}
	}
	switch {
	case dev.Counters.RxPackets == 0:
		c.State, c.Detail = "warning", fmt.Sprintf("Nothing has arrived through %s since it was made.", dev.Name)
	case recent:
		c.State, c.Detail = "ok", fmt.Sprintf("%d packets have arrived since it was made, some within the last fifteen minutes.", dev.Counters.RxPackets)
	default:
		c.State, c.Detail = "warning", fmt.Sprintf("%d packets have arrived since it was made, none within the last fifteen minutes.", dev.Counters.RxPackets)
	}
	return c
}

func hasFlag(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func stateIf(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

func dummyAddressDetail(dev *Link) string {
	var addrs []string
	for _, a := range dev.Addresses {
		if a.Scope != "link" {
			addrs = append(addrs, a.CIDR)
		}
	}
	if len(addrs) == 0 {
		return dev.Name + " has no address of its own; give it the service address it should hold."
	}
	return dev.Name + " holds " + strings.Join(addrs, ", ") + ", independent of any physical device being up."
}

func suffixS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func suffixES(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

func verbS(n int) string {
	if n == 1 {
		return "s"
	}
	return ""
}
