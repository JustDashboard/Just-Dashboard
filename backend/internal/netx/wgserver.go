package netx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Everything that makes, starts, stops, and removes a whole WireGuard
// interface lives here; its peers are in wgpeers.go.

const (
	wgDefaultPort = 51820
	wgDefaultMTU  = 1420
	// wgRemovedDir is where a removed interface's file is moved. The server's
	// private key exists nowhere else, so deleting the file would make a
	// mistaken removal unrecoverable: every client would have to be given a
	// new configuration. Moved aside, it can be put back by hand.
	wgRemovedDir = ".just-dashboard-removed"
)

// wgDefaultDNS is what clients are given when none is chosen: the two
// resolvers most networks already trust. A device on a full tunnel would
// otherwise ask its home router, outside the tunnel, which is the leak a VPN
// is meant to close.
var wgDefaultDNS = []string{"1.1.1.1", "1.0.0.1"}

// WGServerRequest is the one-click server. Every field has a default.
type WGServerRequest struct {
	Name     string         `json:"name"`
	Port     int            `json:"port"`
	Subnet   string         `json:"subnet"`
	Endpoint string         `json:"endpoint"`
	DNS      []string       `json:"dns"`
	ExitNode bool           `json:"exitNode"`
	MTU      int            `json:"mtu"`
	IPv6     *WGIPv6Request `json:"ipv6,omitempty"`
}

// WGServerResult is a created interface and what the caller should know
// that did not stop it being created.
type WGServerResult struct {
	Interface WGInterface `json:"interface"`
	Warnings  []string    `json:"warnings"`
}

// wgHostPrefix is a network the host already has, and where it comes from, so a
// refusal can name it.
type wgHostPrefix struct {
	prefix netip.Prefix
	dev    string
	what   string
}

// wgHostState is the host's addresses and routes, read once for an allocation.
type wgHostState struct {
	prefixes []wgHostPrefix
	// addrs are the addresses by device, host bits kept.
	addrs map[string][]netip.Prefix
	// links is every device by name, addresses or not. A device with none (a
	// bridge port, a tunnel that is down, a veth) is invisible in addrs, and a
	// name picked from addrs alone would collide with it.
	links map[string]bool
	kinds map[string]string
	// uplink is the device the default route leaves through.
	uplink string
}

type wgIPAddrJSON struct {
	Ifname   string `json:"ifname"`
	AddrInfo []struct {
		Local     string `json:"local"`
		Prefixlen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

type wgIPRouteJSON struct {
	Dst    string `json:"dst"`
	Dev    string `json:"dev"`
	Type   string `json:"type"`
	Metric int    `json:"metric"`
}

// wgReadHostState reads every address and every route in every table. A tunnel's
// network must overlap none of them, and "every table" matters: Tailscale's
// routes are in table 52 and a policy rule's in whatever it names, neither
// of which `ip route` alone prints.
func wgReadHostState(ctx context.Context) (wgHostState, error) {
	st := wgHostState{addrs: map[string][]netip.Prefix{}, links: map[string]bool{}, kinds: map[string]string{}}
	linkOut, err := run(ctx, "ip", "-j", "-d", "link", "show")
	if err != nil {
		return st, fmt.Errorf("reading the host's devices: %w", err)
	}
	var links []struct {
		Ifname   string `json:"ifname"`
		Linkinfo struct {
			Kind string `json:"info_kind"`
		} `json:"linkinfo"`
	}
	if err := json.Unmarshal([]byte(linkOut), &links); err != nil {
		return st, fmt.Errorf("ip link printed something unreadable")
	}
	for _, l := range links {
		st.links[l.Ifname] = true
		st.kinds[l.Ifname] = l.Linkinfo.Kind
	}
	out, err := run(ctx, "ip", "-j", "addr")
	if err != nil {
		return st, fmt.Errorf("reading the host's addresses: %w", err)
	}
	var addrs []wgIPAddrJSON
	if err := json.Unmarshal([]byte(out), &addrs); err != nil {
		return st, fmt.Errorf("ip addr printed something unreadable")
	}
	for _, l := range addrs {
		for _, a := range l.AddrInfo {
			addr, err := ParseAddr(a.Local)
			if err != nil {
				continue
			}
			p := netip.PrefixFrom(addr, a.Prefixlen)
			st.links[l.Ifname] = true
			st.addrs[l.Ifname] = append(st.addrs[l.Ifname], p)
			st.prefixes = append(st.prefixes, wgHostPrefix{prefix: p.Masked(), dev: l.Ifname, what: "an address on " + l.Ifname})
		}
	}
	for _, family := range [][]string{{"-j", "route", "show", "table", "all"}, {"-j", "-6", "route", "show", "table", "all"}} {
		out, err := run(ctx, "ip", family...)
		if err != nil {
			return st, fmt.Errorf("reading the host's routes: %w", err)
		}
		var routes []wgIPRouteJSON
		if err := json.Unmarshal([]byte(out), &routes); err != nil {
			return st, fmt.Errorf("ip route printed something unreadable")
		}
		for _, r := range routes {
			if r.Dst == "" || r.Dst == "default" {
				continue
			}
			p, err := ParsePrefix(r.Dst)
			if err != nil {
				continue
			}
			st.prefixes = append(st.prefixes, wgHostPrefix{prefix: p.Masked(), dev: r.Dev, what: "a route on " + firstNonEmpty(r.Dev, "this host")})
		}
	}
	st.uplink = wgDefaultDevice(ctx)
	return st, nil
}

// wgDefaultDevice is the device the preferred default route leaves through: the
// lowest metric, IPv4 before IPv6.
func wgDefaultDevice(ctx context.Context) string {
	for _, family := range [][]string{{"-j", "route", "show", "default"}, {"-j", "-6", "route", "show", "default"}} {
		out, err := run(ctx, "ip", family...)
		if err != nil {
			continue
		}
		var routes []wgIPRouteJSON
		if json.Unmarshal([]byte(out), &routes) != nil {
			continue
		}
		sort.SliceStable(routes, func(i, j int) bool { return routes[i].Metric < routes[j].Metric })
		for _, r := range routes {
			if r.Dev != "" {
				return r.Dev
			}
		}
	}
	return ""
}

// overlap names the first host network p overlaps, skipping those that are
// on skipDev: a tunnel's own address and routes are not a conflict with
// itself.
func (h wgHostState) overlap(p netip.Prefix, skipDev string) (wgHostPrefix, bool) {
	for _, hp := range h.prefixes {
		if skipDev != "" && hp.dev == skipDev {
			continue
		}
		if hp.prefix.Overlaps(p) {
			return hp, true
		}
	}
	return wgHostPrefix{}, false
}

// publicV4 is the first public IPv4 address of the uplink: what a client on
// the internet dials.
func (h wgHostState) publicV4() string {
	for _, p := range h.addrs[h.uplink] {
		if p.Addr().Is4() && isPublic(p.Addr()) {
			return p.Addr().String()
		}
	}
	return ""
}

// wgUDPListening is every UDP port a socket is bound to, from /proc/net/udp and
// udp6. A port another program holds cannot be listened on, and a tunnel that
// fails to bind at boot is a tunnel that is down when it is needed.
func wgUDPListening() map[int]bool {
	ports := map[int]bool{}
	for _, name := range []string{"udp", "udp6"} {
		b, err := os.ReadFile(filepath.Join(wgProcNet, name))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if i == 0 {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			_, hexPort, ok := strings.Cut(f[1], ":")
			if !ok {
				continue
			}
			if n, err := strconv.ParseUint(hexPort, 16, 16); err == nil {
				ports[int(n)] = true
			}
		}
	}
	return ports
}

// wgPortsInConfs are the listen ports of tunnels that exist as files, running
// or not: a tunnel that is down holds no socket but will want its port back.
func wgPortsInConfs(confs map[string]*wgConf) map[int]string {
	out := map[int]string{}
	for name, c := range confs {
		if sec := c.iface(); sec != nil {
			if n, err := strconv.Atoi(sec.get("listenport")); err == nil {
				out[n] = name
			}
		}
	}
	return out
}

// wgSubnetsInConfs are the networks of tunnels that exist as files.
func wgSubnetsInConfs(confs map[string]*wgConf) []wgHostPrefix {
	var out []wgHostPrefix
	for name, c := range confs {
		if sec := c.iface(); sec != nil {
			for _, a := range sec.list("address") {
				if p, err := ParsePrefix(a); err == nil {
					out = append(out, wgHostPrefix{prefix: p.Masked(), dev: name, what: "the WireGuard tunnel " + name})
				}
			}
		}
		for _, peer := range c.peers() {
			for _, raw := range peer.list("allowedips") {
				if p, err := ParsePrefix(raw); err == nil && p.Bits() > 0 {
					out = append(out, wgHostPrefix{prefix: p.Masked(), dev: name, what: "a configured WireGuard peer of " + name})
				}
			}
		}
	}
	return out
}

// hasDevice reports whether a network device of that name exists, with or
// without addresses.
func (h wgHostState) hasDevice(name string) bool {
	if h.links[name] {
		return true
	}
	_, ok := h.addrs[name]
	return ok
}

// pickWGName is the first of wg0..wg9 that is neither a file nor a device.
func pickWGName(confs map[string]*wgConf, host wgHostState) (string, error) {
	for i := 0; i < 10; i++ {
		name := "wg" + strconv.Itoa(i)
		if _, ok := confs[name]; ok {
			continue
		}
		if host.hasDevice(name) {
			continue
		}
		return name, nil
	}
	return "", fmt.Errorf("wg0 to wg9 are all in use; give the new interface a name")
}

// pickWGPort is the first port from 51820 up that nothing listens on and no
// tunnel's file claims.
func pickWGPort(listening map[int]bool, claimed map[int]string) (int, error) {
	for p := wgDefaultPort; p <= 65535; p++ {
		if !listening[p] && claimed[p] == "" {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free UDP port was found above %d", wgDefaultPort)
}

// pickWGSubnet is the first 10.N.0.0/24 from 10.8 to 10.250 that overlaps no
// address or route on the host and no other tunnel. The starting point is
// WireGuard's own documentation's, so the first tunnel on a clean host gets
// the network its manual shows.
func pickWGSubnet(host wgHostState, other []wgHostPrefix) (netip.Prefix, error) {
	for n := 8; n <= 250; n++ {
		cand := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(n), 0, 0}), 24)
		if _, hit := host.overlap(cand, ""); hit {
			continue
		}
		clash := false
		for _, o := range other {
			if o.prefix.Overlaps(cand) {
				clash = true
				break
			}
		}
		if !clash {
			return cand, nil
		}
	}
	return netip.Prefix{}, fmt.Errorf("every 10.N.0.0/24 network from 10.8 to 10.250 overlaps something on this host; give a subnet")
}

// wgFirstHost is the network's first usable address, the server's.
func wgFirstHost(p netip.Prefix) netip.Addr { return p.Masked().Addr().Next() }

// parseWGEndpoint reads the address clients dial: a host name or address, with
// an optional port that defaults to the tunnel's. Brackets around an IPv6
// address are written back because wg-quick needs them.
func parseWGEndpoint(raw string, defaultPort int) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("an endpoint is required")
	}
	host, portStr := raw, ""
	if strings.HasPrefix(raw, "[") {
		end := strings.Index(raw, "]")
		if end < 0 {
			return "", fmt.Errorf("%q is not an endpoint", raw)
		}
		host = raw[1:end]
		suffix := raw[end+1:]
		if suffix != "" && !strings.HasPrefix(suffix, ":") {
			return "", fmt.Errorf("%q is not an endpoint: a port follows a colon", raw)
		}
		portStr = strings.TrimPrefix(suffix, ":")
	} else if strings.Count(raw, ":") == 1 {
		host, portStr, _ = strings.Cut(raw, ":")
	}
	port := defaultPort
	if portStr != "" {
		n, err := strconv.Atoi(portStr)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("%q is not a port between 1 and 65535", portStr)
		}
		port = n
	}
	if port == 0 {
		return "", fmt.Errorf("the endpoint needs a port")
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" {
			// netip accepts arbitrary zone text, while this endpoint is also
			// written inside a wg-quick file that can contain shell directives.
			if err := ValidIfName(a.Zone()); err != nil {
				return "", fmt.Errorf("the endpoint's IPv6 interface: %w", err)
			}
		}
		if a.IsUnspecified() || a.IsMulticast() {
			return "", fmt.Errorf("%s cannot be a remote endpoint", a)
		}
		if a.Is6() {
			return "[" + a.String() + "]:" + strconv.Itoa(port), nil
		}
		return a.String() + ":" + strconv.Itoa(port), nil
	}
	if !wgValidHostname(host) {
		return "", fmt.Errorf("%q is not a host name or an address", host)
	}
	return host + ":" + strconv.Itoa(port), nil
}

// wgValidHostname is a DNS name: labels of letters, digits and hyphens. It is
// written into a configuration file and a comment, so nothing else passes.
func wgValidHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(h, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// wgParseDNSList reads client resolvers. Addresses only: wg-quick also accepts
// search domains in DNS, which is a different feature than "which resolver".
func wgParseDNSList(in []string) ([]string, error) {
	var out []string
	for _, raw := range in {
		a, err := ParseAddr(raw)
		if err != nil {
			return nil, fmt.Errorf("DNS: %w", err)
		}
		out = append(out, a.String())
	}
	return out, nil
}

// requireWG checks the tools a tunnel is made and run with.
func requireWG() error {
	if !has("wg") || !has("wg-quick") {
		tool := "wg"
		if has("wg") {
			tool = "wg-quick"
		}
		return &UnavailableError{Tool: tool, Package: wgPackage}
	}
	if !has("systemctl") {
		return &ReadOnlyError{Reason: "WireGuard tunnels here are wg-quick systemd units, and this host has no systemd"}
	}
	return nil
}

// CreateWireGuard makes a server in one step: a key, a file, a unit that is
// enabled and started, and, if asked, the NAT entry that lets clients use
// this server as their route to the internet. Anything that fails after the
// file is written puts the host back as it was.
func (s *Service) CreateWireGuard(ctx context.Context, req WGServerRequest, actor string) (*WGServerResult, error) {
	if err := requireWG(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	confs, err := s.listWGConfs()
	if err != nil {
		return nil, err
	}
	host, err := wgReadHostState(ctx)
	if err != nil {
		return nil, err
	}

	name := req.Name
	if name == "" {
		if name, err = pickWGName(confs, host); err != nil {
			return nil, err
		}
	} else {
		if err := validWGName(name); err != nil {
			return nil, err
		}
		if _, ok := confs[name]; ok {
			return nil, fmt.Errorf("%s: %w", name, ErrExists)
		}
		if host.hasDevice(name) {
			return nil, fmt.Errorf("a network device called %s already exists: %w", name, ErrExists)
		}
	}

	listening, claimed := wgUDPListening(), wgPortsInConfs(confs)
	port := req.Port
	if port == 0 {
		if port, err = pickWGPort(listening, claimed); err != nil {
			return nil, err
		}
	} else {
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("the port is between 1 and 65535")
		}
		if listening[port] {
			return nil, fmt.Errorf("UDP port %d is already in use on this host", port)
		}
		if owner := claimed[port]; owner != "" {
			return nil, fmt.Errorf("UDP port %d belongs to the WireGuard tunnel %s", port, owner)
		}
	}

	subnet, err := s.wgSubnetFor(req.Subnet, host, wgSubnetsInConfs(confs))
	if err != nil {
		return nil, err
	}
	var subnet6 netip.Prefix
	if req.IPv6 != nil {
		if req.IPv6.ExitNode && !req.ExitNode {
			return nil, fmt.Errorf("IPv6 exit egress accompanies the IPv4 exit; enable exitNode too")
		}
		if req.ExitNode && !wgIPForwarding("ipv4") {
			return nil, fmt.Errorf("IPv4 forwarding must be enabled before creating an opted-in dual-stack exit")
		}
		out, err := run(ctx, "wg", "show", "all", "dump")
		if err != nil {
			return nil, fmt.Errorf("reading native WireGuard peer networks before IPv6 allocation: %w", err)
		}
		inventory, err := wgCheckedDump(out)
		if err != nil {
			return nil, err
		}
		for dev, live := range inventory {
			for _, peer := range live.peers {
				for _, raw := range peer.allowedIPs {
					if p, err := ParsePrefix(raw); err == nil && p.Bits() > 0 {
						host.prefixes = append(host.prefixes, wgHostPrefix{prefix: p.Masked(), dev: dev, what: "a live WireGuard peer of " + dev})
					}
				}
			}
		}
		subnet6, err = wgIPv6Subnet(req.IPv6.Subnet, host, wgSubnetsInConfs(confs))
		if err != nil {
			return nil, err
		}
		if req.IPv6.ExitNode && (!wgIPForwarding("ipv4") || !wgIPForwarding("ipv6")) {
			return nil, fmt.Errorf("dual-stack exit egress requires IPv4 and IPv6 forwarding enabled; use the Routing page first")
		}
		if req.IPv6.ExitNode {
			if err := raForwardingGuard(ctx); err != nil {
				return nil, err
			}
		}
	}

	endpoint := req.Endpoint
	if endpoint == "" {
		ip := host.publicV4()
		if ip == "" {
			return nil, fmt.Errorf("no public IPv4 address was found on %s; give the address or name clients should dial", firstNonEmpty(host.uplink, "the uplink"))
		}
		endpoint = ip
	}
	if endpoint, err = parseWGEndpoint(endpoint, port); err != nil {
		return nil, err
	}

	dns := req.DNS
	if len(dns) == 0 {
		dns = wgDefaultDNS
	}
	if dns, err = wgParseDNSList(dns); err != nil {
		return nil, err
	}
	mtu := req.MTU
	if mtu == 0 {
		mtu = wgDefaultMTU
	}
	if mtu < 1280 || mtu > 9000 {
		return nil, fmt.Errorf("the MTU is between 1280 and 9000")
	}

	var warnings []string
	if req.ExitNode {
		if host.uplink == "" {
			return nil, fmt.Errorf("an exit node needs a default route to send traffic out through, and this host has none")
		}
		if !wgIPForwarding("ipv4") {
			warnings = append(warnings, wgForwardingWarning)
		}
	}

	priv, _, err := newWGKeyPair()
	if err != nil {
		return nil, err
	}
	now := wgNow()
	serverAddr := netip.PrefixFrom(wgFirstHost(subnet), subnet.Bits())
	// No PostUp or PostDown: the NAT that an exit node needs is the gateway
	// table's, restored by the same boot unit as everything else the dashboard
	// owns, and a shell line in a file that systemd runs as root is not a
	// thing this code writes.
	text := fmt.Sprintf(`%s
# Created %s. Peers are managed from the dashboard's VPN page;
# keep the lines starting "# jd:", they hold names the file cannot.
[Interface]
# jd:endpoint=%s
# jd:dns=%s
Address = %s
ListenPort = %d
PrivateKey = %s
MTU = %d
SaveConfig = false
`, wgManagedMarker, now.UTC().Format(time.RFC3339), endpoint, strings.Join(dns, ","), serverAddr, port, priv, mtu)
	if subnet6.IsValid() {
		c := parseWGConf(text)
		c.iface().setBodyMeta("ipv6", "ula64")
		c.iface().set("Address", serverAddr.String()+", "+netip.PrefixFrom(wgFirstHost(subnet6), 64).String())
		text = c.render()
	}

	path, err := s.wgConfPath(name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.paths.WireGuard, 0o700); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, []byte(text), 0o600); err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}

	unit := "wg-quick@" + name
	undo := func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		// Best effort by design: this runs because something already failed,
		// and the unit may never have been enabled or started.
		_, _ = run(cleanup, "systemctl", "disable", "--now", unit)
		_ = os.Remove(path)
	}
	if _, err := run(ctx, "systemctl", "enable", "--now", unit); err != nil {
		undo()
		return nil, fmt.Errorf("starting %s: %w", unit, err)
	}

	if req.ExitNode {
		networks := []wgExitNetwork{{subnet: subnet, uplink: host.uplink}}
		if req.IPv6 != nil {
			if err := wgRefusePeerDrift(ctx, name, parseWGConf(text)); err != nil {
				undo()
				return nil, err
			}
			fresh, err := wgReadHostState(ctx)
			if err != nil {
				undo()
				return nil, err
			}
			v4, err := wgExitUplink(ctx, fresh, name, subnet)
			if err != nil {
				undo()
				return nil, err
			}
			networks = []wgExitNetwork{{subnet: subnet, uplink: v4}}
			if req.IPv6.ExitNode {
				v6, err := wgExitUplink(ctx, fresh, name, subnet6)
				if err != nil {
					undo()
					return nil, err
				}
				networks = append(networks, wgExitNetwork{subnet: subnet6, uplink: v6})
			}
		}
		if err := s.setWGExitNetworks(ctx, name, networks, true, actor); err != nil {
			var saved *persistenceError
			if errors.As(err, &saved) {
				// NAT and its spec already reference this tunnel. Removing its
				// device/file would turn a boot-unit warning into a broken exit.
				warnings = append(warnings, err.Error())
			} else {
				undo()
				return nil, fmt.Errorf("making %s an exit node: %w", name, err)
			}
		}
	}

	view, err := s.readWireGuard(ctx, name, false)
	if err != nil {
		return nil, err
	}
	res := &WGServerResult{Warnings: append([]string{}, warnings...)}
	for _, ifc := range view.Interfaces {
		if ifc.Name == name {
			res.Interface = ifc
		}
	}
	if res.Interface.Name == "" {
		return nil, fmt.Errorf("%s was started but could not be read back", name)
	}
	return res, nil
}

// wgSubnetFor is the requested network or a free default, checked against the
// host and every other tunnel.
func (s *Service) wgSubnetFor(requested string, host wgHostState, other []wgHostPrefix) (netip.Prefix, error) {
	if requested == "" {
		return pickWGSubnet(host, other)
	}
	p, err := ParsePrefix(requested)
	if err != nil {
		return netip.Prefix{}, err
	}
	p = p.Masked()
	if !p.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("the tunnel's network is an IPv4 network such as 10.8.0.0/24")
	}
	if p.Bits() < 16 || p.Bits() > 29 {
		return netip.Prefix{}, fmt.Errorf("the tunnel's network is between a /16 and a /29")
	}
	if !p.Addr().IsPrivate() {
		return netip.Prefix{}, fmt.Errorf("the tunnel's network is a private range (10.0.0.0/8, 172.16.0.0/12 or 192.168.0.0/16)")
	}
	if hp, hit := host.overlap(p, ""); hit {
		return netip.Prefix{}, fmt.Errorf("%s overlaps %s, which is %s", p, hp.prefix, hp.what)
	}
	for _, o := range other {
		if o.prefix.Overlaps(p) {
			return netip.Prefix{}, fmt.Errorf("%s overlaps %s, which is %s", p, o.prefix, o.what)
		}
	}
	return p, nil
}

// managedWG loads a tunnel's file and refuses one the dashboard did not write.
func (s *Service) managedWG(name string) (*wgConf, error) {
	c, err := s.readWGConf(name)
	if err != nil {
		return nil, err
	}
	if !c.managed {
		return nil, fmt.Errorf("%s was written by hand and is read-only here: %w", name, ErrNotManaged)
	}
	if c.iface() == nil {
		return nil, fmt.Errorf("%s has no [Interface] section", name)
	}
	return c, nil
}

// wgRefuseClientPath is the guard every change to a whole tunnel runs: the
// device the reply to the operator's browser leaves through cannot be taken
// away. An operator who reaches the dashboard through their own WireGuard is
// the case; Tailscale and the uplink are protected by the same comparison.
func wgRefuseClientPath(ctx context.Context, client, iface, verb string) error {
	if client == "" {
		return nil
	}
	p, err := clientPath(ctx, client)
	if err != nil {
		return err
	}
	if p.Device == iface {
		return guarded("your connection to the dashboard (%s) comes through %s, so it cannot be %s", client, iface, verb)
	}
	return nil
}

// SetWireGuardUp starts or stops a tunnel's unit. Stopping leaves it enabled:
// "down" is for now, and the tunnel is back at the next boot.
func (s *Service) SetWireGuardUp(ctx context.Context, iface string, up bool, client string) error {
	if err := validWGName(iface); err != nil {
		return err
	}
	if err := requireWG(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.managedWG(iface); err != nil {
		return err
	}
	verb, action := "taken down", "stop"
	if up {
		verb, action = "started", "start"
	}
	if !up {
		if err := wgRefuseClientPath(ctx, client, iface, verb); err != nil {
			return err
		}
	}
	if _, err := run(ctx, "systemctl", action, "wg-quick@"+iface); err != nil {
		return fmt.Errorf("%s %s: %w", action, iface, err)
	}
	return nil
}

// SetWireGuardExit turns a tunnel's exit node on or off: per-family NAT entries, owned
// by the tunnel, through the same commit as every other change to the
// network.
func (s *Service) SetWireGuardExit(ctx context.Context, iface string, on bool, actor string) (*WGServerResult, error) {
	return s.SetWireGuardExitFamilies(ctx, iface, on, nil, actor)
}

// A nil IPv6 choice preserves existing family intent. Older API callers cannot
// silently activate IPv6 merely because a newer server supports it.
func (s *Service) SetWireGuardExitFamilies(ctx context.Context, iface string, on bool, ipv6 *bool, actor string) (*WGServerResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conf, err := s.managedWG(iface)
	if err != nil {
		return nil, err
	}
	if wgIPv6Enabled(conf) {
		if err := wgRefusePeerDrift(ctx, iface, conf); err != nil {
			return nil, err
		}
	}
	var warnings []string
	if on {
		v4, v6 := wgInterfacePrefixes(conf)
		if !v4.IsValid() {
			return nil, fmt.Errorf("%s has no address to translate", iface)
		}
		sp, err := s.loadSpec()
		if err != nil {
			return nil, err
		}
		dual := wgExitEnabled(sp, iface, true)
		if ipv6 != nil {
			dual = *ipv6
		}
		if dual && (!wgIPv6Enabled(conf) || !v6.IsValid() || v6.Bits() != 64 || !v6.Addr().IsPrivate()) {
			return nil, fmt.Errorf("%s has no opted-in unique-local IPv6 /64; create a dual-stack server to enable IPv6 egress", iface)
		}
		if dual && (!wgIPForwarding("ipv4") || !wgIPForwarding("ipv6")) {
			return nil, fmt.Errorf("dual-stack exit egress requires IPv4 and IPv6 forwarding enabled; use the Routing page first")
		}
		if dual {
			if err := raForwardingGuard(ctx); err != nil {
				return nil, err
			}
		}
		host, err := wgReadHostState(ctx)
		if err != nil {
			return nil, err
		}
		if host.uplink == "" {
			return nil, fmt.Errorf("an exit node needs a default route to send traffic out through, and this host has none")
		}
		networks := []wgExitNetwork{{subnet: v4.Masked(), uplink: host.uplink}}
		if dual || wgIPv6Enabled(conf) {
			uplink4, err := wgExitUplink(ctx, host, iface, v4.Masked())
			if err != nil {
				return nil, err
			}
			networks = []wgExitNetwork{{subnet: v4.Masked(), uplink: uplink4}}
			if dual {
				uplink6, err := wgExitUplink(ctx, host, iface, v6.Masked())
				if err != nil {
					return nil, err
				}
				networks = append(networks, wgExitNetwork{subnet: v6.Masked(), uplink: uplink6})
			}
		}
		if err := s.setWGExitNetworks(ctx, iface, networks, true, actor); err != nil {
			return nil, err
		}
		if !wgIPForwarding("ipv4") {
			warnings = append(warnings, wgForwardingWarning)
		}
	} else if err := s.setWGExit(ctx, iface, "", "", false, actor); err != nil {
		return nil, err
	}
	view, err := s.readWireGuard(ctx, iface, false)
	if err != nil {
		return nil, err
	}
	res := &WGServerResult{Warnings: append([]string{}, warnings...)}
	if len(view.Interfaces) > 0 {
		res.Interface = view.Interfaces[0]
	}
	return res, nil
}

// wgForwardingWarning is shown, not enforced: the NAT entry is correct and
// harmless with forwarding off, and the Routing page is where it is turned on.
const wgForwardingWarning = "IPv4 forwarding is off on this host, so clients cannot reach anything beyond it. Turn it on from the Routing page."

// setWGExit edits the spec for a tunnel's exit node and loads the gateway
// table with it: the masquerade is the gateway's like any NAT entry, so it is
// applied, admitted past the host's forward filters and taken back the same
// way, and a host whose firewall cannot admit it refuses it with the
// gateway's own reason. The caller holds s.mu.
func (s *Service) setWGExit(ctx context.Context, iface, subnet, uplink string, on bool, actor string) error {
	var networks []wgExitNetwork
	if on {
		p, err := ParsePrefix(subnet)
		if err != nil {
			return err
		}
		networks = []wgExitNetwork{{subnet: p.Masked(), uplink: uplink}}
	}
	return s.setWGExitNetworks(ctx, iface, networks, on, actor)
}

type wgExitNetwork struct {
	subnet netip.Prefix
	uplink string
}

func (s *Service) setWGExitNetworks(ctx context.Context, iface string, networks []wgExitNetwork, on bool, actor string) error {
	old, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := old.clone()
	owner := wgOwner(iface)
	if on {
		for _, network := range networks {
			upsertOwnedNAT(next, owner, "WireGuard "+iface+" "+wgFamilyName(network.subnet)+" exit", network.subnet.String(), network.uplink, actor)
		}
		kept := next.NAT[:0]
		for _, n := range next.NAT {
			keep := n.Owner != owner
			for _, network := range networks {
				keep = keep || n.Source == network.subnet.String()
			}
			if keep {
				kept = append(kept, n)
			}
		}
		next.NAT = kept
		if _, err := s.requireWritable(ctx, next); err != nil {
			return err
		}
	} else {
		had := len(next.NAT)
		removeOwnedNAT(next, owner)
		if len(next.NAT) == had {
			return nil
		}
	}
	st := s.gatewayStep(old, next)
	conf, _ := s.readWGConf(iface)
	strict := conf != nil && conf.iface() != nil && wgIPv6Enabled(conf)
	if len(networks) > 1 || (strict && len(networks) > 0) {
		verify := st.verify
		st.verify = func(ctx context.Context) error {
			if err := verify(ctx); err != nil {
				return err
			}
			host, err := wgReadHostState(ctx)
			if err != nil {
				return err
			}
			for _, network := range networks {
				if !wgFamilyForwarding(network.subnet.Addr().Is6()) {
					return fmt.Errorf("%s forwarding changed while enabling the exit", wgFamilyName(network.subnet))
				}
				dev, err := wgExitUplink(ctx, host, iface, network.subnet)
				if err != nil || dev != network.uplink {
					return fmt.Errorf("%s peer-source egress route changed or could not be verified: %v", wgFamilyName(network.subnet), err)
				}
				for _, n := range next.NAT {
					if n.Owner == owner && n.Source == network.subnet.String() {
						if err := wgExitRules(ctx, n); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}
	}
	return s.commit(ctx, next, st)
}

// RemoveWireGuard stops and removes a tunnel the dashboard made. Its file is
// moved aside, not deleted: the server's private key exists only there.
func (s *Service) RemoveWireGuard(ctx context.Context, iface, client string) error {
	if err := validWGName(iface); err != nil {
		return err
	}
	if err := requireWG(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.managedWG(iface); err != nil {
		return err
	}
	if err := wgRefuseClientPath(ctx, client, iface, "removed"); err != nil {
		return err
	}
	unit := "wg-quick@" + iface
	if _, err := run(ctx, "systemctl", "disable", "--now", unit); err != nil {
		return fmt.Errorf("stopping %s: %w", unit, err)
	}
	path, err := s.wgConfPath(iface)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.paths.WireGuard, wgRemovedDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%s was stopped, but its file could not be moved aside: %w", iface, err)
	}
	dest := filepath.Join(dir, fmt.Sprintf("%s.conf.%d", iface, wgNow().Unix()))
	if err := os.Rename(path, dest); err != nil {
		return fmt.Errorf("%s was stopped, but its file could not be moved aside: %w", iface, err)
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		return fmt.Errorf("securing %s: %w", dest, err)
	}
	if err := s.vpn.DeleteInterface(ctx, iface); err != nil {
		return err
	}
	if err := s.setWGExit(ctx, iface, "", "", false, ""); err != nil {
		return fmt.Errorf("%s was removed, but its NAT entry could not be: %w", iface, err)
	}
	return nil
}

// wgIPForwarding reads a family's forwarding switch.
func wgIPForwarding(family string) bool {
	path := filepath.Join(wgProcSys, "net", "ipv4", "ip_forward")
	if family == "ipv6" {
		path = filepath.Join(wgProcSys, "net", "ipv6", "conf", "all", "forwarding")
	}
	b, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(b)) == "1"
}
