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
	Name     string   `json:"name"`
	Port     int      `json:"port"`
	Subnet   string   `json:"subnet"`
	Endpoint string   `json:"endpoint"`
	DNS      []string `json:"dns"`
	ExitNode bool     `json:"exitNode"`
	MTU      int      `json:"mtu"`
}

// WGServerResult is a created interface and what the caller should know
// that did not stop it being created.
type WGServerResult struct {
	Interface WGInterface `json:"interface"`
	Warnings  []string    `json:"warnings"`
}

// hostPrefix is a network the host already has, and where it comes from, so a
// refusal can name it.
type hostPrefix struct {
	prefix netip.Prefix
	dev    string
	what   string
}

// hostState is the host's addresses and routes, read once for an allocation.
type hostState struct {
	prefixes []hostPrefix
	// addrs are the addresses by device, host bits kept.
	addrs map[string][]netip.Prefix
	// uplink is the device the default route leaves through.
	uplink string
}

type ipAddrJSON struct {
	Ifname   string `json:"ifname"`
	AddrInfo []struct {
		Local     string `json:"local"`
		Prefixlen int    `json:"prefixlen"`
	} `json:"addr_info"`
}

type ipRouteJSON struct {
	Dst    string `json:"dst"`
	Dev    string `json:"dev"`
	Type   string `json:"type"`
	Metric int    `json:"metric"`
}

// readHostState reads every address and every route in every table. A tunnel's
// network must overlap none of them, and "every table" matters: Tailscale's
// routes are in table 52 and a policy rule's in whatever it names, neither
// of which `ip route` alone prints.
func readHostState(ctx context.Context) (hostState, error) {
	st := hostState{addrs: map[string][]netip.Prefix{}}
	out, err := run(ctx, "ip", "-j", "addr")
	if err != nil {
		return st, fmt.Errorf("reading the host's addresses: %w", err)
	}
	var addrs []ipAddrJSON
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
			st.addrs[l.Ifname] = append(st.addrs[l.Ifname], p)
			st.prefixes = append(st.prefixes, hostPrefix{prefix: p.Masked(), dev: l.Ifname, what: "an address on " + l.Ifname})
		}
	}
	for _, family := range [][]string{{"-j", "route", "show", "table", "all"}, {"-j", "-6", "route", "show", "table", "all"}} {
		out, err := run(ctx, "ip", family...)
		if err != nil {
			return st, fmt.Errorf("reading the host's routes: %w", err)
		}
		var routes []ipRouteJSON
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
			st.prefixes = append(st.prefixes, hostPrefix{prefix: p.Masked(), dev: r.Dev, what: "a route on " + firstNonEmpty(r.Dev, "this host")})
		}
	}
	st.uplink = defaultDevice(ctx)
	return st, nil
}

// defaultDevice is the device the preferred default route leaves through: the
// lowest metric, IPv4 before IPv6.
func defaultDevice(ctx context.Context) string {
	for _, family := range [][]string{{"-j", "route", "show", "default"}, {"-j", "-6", "route", "show", "default"}} {
		out, err := run(ctx, "ip", family...)
		if err != nil {
			continue
		}
		var routes []ipRouteJSON
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
func (h hostState) overlap(p netip.Prefix, skipDev string) (hostPrefix, bool) {
	for _, hp := range h.prefixes {
		if skipDev != "" && hp.dev == skipDev {
			continue
		}
		if hp.prefix.Overlaps(p) {
			return hp, true
		}
	}
	return hostPrefix{}, false
}

// publicV4 is the first public IPv4 address of the uplink: what a client on
// the internet dials.
func (h hostState) publicV4() string {
	for _, p := range h.addrs[h.uplink] {
		if p.Addr().Is4() && isPublic(p.Addr()) {
			return p.Addr().String()
		}
	}
	return ""
}

// udpListening is every UDP port a socket is bound to, from /proc/net/udp and
// udp6. A port another program holds cannot be listened on, and a tunnel that
// fails to bind at boot is a tunnel that is down when it is needed.
func udpListening() map[int]bool {
	ports := map[int]bool{}
	for _, name := range []string{"udp", "udp6"} {
		b, err := os.ReadFile(filepath.Join(procNetRoot, name))
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
func wgSubnetsInConfs(confs map[string]*wgConf) []hostPrefix {
	var out []hostPrefix
	for name, c := range confs {
		if sec := c.iface(); sec != nil {
			for _, a := range sec.list("address") {
				if p, err := ParsePrefix(a); err == nil {
					out = append(out, hostPrefix{prefix: p.Masked(), dev: name, what: "the WireGuard tunnel " + name})
				}
			}
		}
	}
	return out
}

// pickWGName is the first of wg0..wg9 that is neither a file nor a device.
func pickWGName(confs map[string]*wgConf, host hostState) (string, error) {
	for i := 0; i < 10; i++ {
		name := "wg" + strconv.Itoa(i)
		if _, ok := confs[name]; ok {
			continue
		}
		if _, ok := host.addrs[name]; ok {
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
func pickWGSubnet(host hostState, other []hostPrefix) (netip.Prefix, error) {
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

// firstHost is the network's first usable address, the server's.
func firstHost(p netip.Prefix) netip.Addr { return p.Masked().Addr().Next() }

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
		portStr = strings.TrimPrefix(raw[end+1:], ":")
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
		if a.Is6() {
			return "[" + a.String() + "]:" + strconv.Itoa(port), nil
		}
		return a.String() + ":" + strconv.Itoa(port), nil
	}
	if !validHostname(host) {
		return "", fmt.Errorf("%q is not a host name or an address", host)
	}
	return host + ":" + strconv.Itoa(port), nil
}

// validHostname is a DNS name: labels of letters, digits and hyphens. It is
// written into a configuration file and a comment, so nothing else passes.
func validHostname(h string) bool {
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

// parseDNSList reads client resolvers. Addresses only: wg-quick also accepts
// search domains in DNS, which is a different feature than "which resolver".
func parseDNSList(in []string) ([]string, error) {
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
	host, err := readHostState(ctx)
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
		if _, ok := host.addrs[name]; ok {
			return nil, fmt.Errorf("a network device called %s already exists: %w", name, ErrExists)
		}
	}

	listening, claimed := udpListening(), wgPortsInConfs(confs)
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
	if dns, err = parseDNSList(dns); err != nil {
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
		if !ipForwarding("ipv4") {
			warnings = append(warnings, forwardingWarning)
		}
	}

	priv, _, err := newWGKeyPair()
	if err != nil {
		return nil, err
	}
	now := wgNow()
	serverAddr := netip.PrefixFrom(firstHost(subnet), subnet.Bits())
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
		if err := s.setWGExit(ctx, name, subnet.String(), host.uplink, true, actor); err != nil {
			undo()
			return nil, fmt.Errorf("making %s an exit node: %w", name, err)
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
func (s *Service) wgSubnetFor(requested string, host hostState, other []hostPrefix) (netip.Prefix, error) {
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

// refuseClientPath is the guard every change to a whole tunnel runs: the
// device the reply to the operator's browser leaves through cannot be taken
// away. An operator who reaches the dashboard through their own WireGuard is
// the case; Tailscale and the uplink are protected by the same comparison.
func refuseClientPath(ctx context.Context, client, iface, verb string) error {
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
		if err := refuseClientPath(ctx, client, iface, verb); err != nil {
			return err
		}
	}
	if _, err := run(ctx, "systemctl", action, "wg-quick@"+iface); err != nil {
		return fmt.Errorf("%s %s: %w", action, iface, err)
	}
	return nil
}

// SetWireGuardExit turns a tunnel's exit node on or off: one NAT entry, owned
// by the tunnel, through the same commit as every other change to the
// network.
func (s *Service) SetWireGuardExit(ctx context.Context, iface string, on bool, actor string) (*WGServerResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conf, err := s.managedWG(iface)
	if err != nil {
		return nil, err
	}
	var warnings []string
	if on {
		subnet := ""
		for _, a := range conf.iface().list("address") {
			if p, err := ParsePrefix(a); err == nil {
				subnet = p.Masked().String()
				break
			}
		}
		if subnet == "" {
			return nil, fmt.Errorf("%s has no address to translate", iface)
		}
		host, err := readHostState(ctx)
		if err != nil {
			return nil, err
		}
		if host.uplink == "" {
			return nil, fmt.Errorf("an exit node needs a default route to send traffic out through, and this host has none")
		}
		if err := s.setWGExit(ctx, iface, subnet, host.uplink, true, actor); err != nil {
			return nil, err
		}
		if !ipForwarding("ipv4") {
			warnings = append(warnings, forwardingWarning)
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

// forwardingWarning is shown, not enforced: the NAT entry is correct and
// harmless with forwarding off, and the Routing page is where it is turned on.
const forwardingWarning = "IPv4 forwarding is off on this host, so clients cannot reach anything beyond it. Turn it on from the Routing page."

// setWGExit edits the spec for a tunnel's exit node. The caller holds s.mu.
func (s *Service) setWGExit(ctx context.Context, iface, subnet, uplink string, on bool, actor string) error {
	sp, err := s.loadSpec()
	if err != nil {
		return err
	}
	next := sp.clone()
	if on {
		vpnUpsertNAT(next, wgOwner(iface), "WireGuard "+iface+" exit", subnet, uplink, actor)
	} else if !vpnRemoveNAT(next, wgOwner(iface)) {
		return nil
	}
	return s.commit(ctx, next, step{})
}

// vpnUpsertNAT adds or updates the NAT entry a tunnel's exit node owns.
// Pure: an edit of next.NAT keyed by owner, so the gateway renders it like
// any other entry.
func vpnUpsertNAT(next *Spec, owner, name, source, iface, actor string) {
	for i := range next.NAT {
		if next.NAT[i].Owner == owner {
			next.NAT[i].Name, next.NAT[i].Source, next.NAT[i].Interface = name, source, iface
			next.NAT[i].Enabled = true
			return
		}
	}
	next.NAT = append(next.NAT, NATSpec{
		ID: next.takeID(), Name: name, Source: source, Interface: iface,
		Owner: owner, Enabled: true,
		Made: Made{CreatedAt: wgNow().UTC(), CreatedBy: actor},
	})
}

// vpnRemoveNAT removes every entry an owner made and reports whether there
// was one.
func vpnRemoveNAT(next *Spec, owner string) bool {
	kept := next.NAT[:0]
	removed := false
	for _, n := range next.NAT {
		if n.Owner == owner {
			removed = true
			continue
		}
		kept = append(kept, n)
	}
	next.NAT = kept
	return removed
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
	if err := refuseClientPath(ctx, client, iface, "removed"); err != nil {
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

// ipForwarding reads a family's forwarding switch.
func ipForwarding(family string) bool {
	path := filepath.Join(procSysRoot, "net", "ipv4", "ip_forward")
	if family == "ipv6" {
		path = filepath.Join(procSysRoot, "net", "ipv6", "conf", "all", "forwarding")
	}
	b, err := os.ReadFile(path)
	return err == nil && strings.TrimSpace(string(b)) == "1"
}
