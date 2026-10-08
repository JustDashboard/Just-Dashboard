package netx

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// A peer is one block in a tunnel's file, one row in the VPN store holding the
// client's configuration sealed, and one entry in the kernel. The three are
// changed in an order that leaves the host sane when a step fails: the row
// first (it can be deleted), then the file (it can be restored), then the
// kernel (a reload that fails is retried against the restored file).

const (
	wgKindDevice = "device"
	wgKindSite   = "site"
	// wgDefaultKeepalive keeps a client behind a NAT reachable: the NAT forgets
	// an idle UDP flow after about half a minute, and a server cannot reach a
	// client whose mapping is gone.
	wgDefaultKeepalive = 25
)

// WGPeerRequest adds a device or a site to a tunnel.
type WGPeerRequest struct {
	Name string `json:"name"`
	// Kind is device (a phone or laptop) or site (another network's router).
	Kind string `json:"kind"`
	// FullTunnel sends all of a device's traffic through this server. It is the
	// client's own setting: this server never routes 0.0.0.0/0 into a tunnel.
	FullTunnel bool `json:"fullTunnel"`
	// ShareNetworks are networks on this server's side the client may reach,
	// added to the client's AllowedIPs: a Docker network, a bridge.
	ShareNetworks []string `json:"shareNetworks"`
	// RemoteNetworks (sites) are the LANs behind the peer; they are routed
	// into the tunnel on this server.
	RemoteNetworks []string `json:"remoteNetworks"`
	// Endpoint (sites) is the remote's public address, so this server dials it.
	Endpoint  string `json:"endpoint"`
	Keepalive *int   `json:"keepalive"`
}

// WGPeerResult is a created peer with the one copy of its configuration the
// server will ever show without being asked again.
type WGPeerResult struct {
	Peer   WGPeer `json:"peer"`
	Config string `json:"config"`
	// QR is a PNG data: URL of Config.
	QR string `json:"qr"`
	// Reloaded is the running interface having been told about the peer.
	Reloaded bool     `json:"reloaded"`
	Warnings []string `json:"warnings"`
}

// WGPeerConfig is a stored client configuration shown again.
type WGPeerConfig struct {
	Name   string `json:"name"`
	Config string `json:"config"`
	QR     string `json:"qr"`
}

// wgParseNetworks reads CIDRs for a field, masked, de-duplicated, without the
// default route: a full tunnel is a setting of the client's, never a network
// somebody types into this server's routing.
func wgParseNetworks(in []string, field string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	seen := map[netip.Prefix]bool{}
	for _, raw := range in {
		p, err := ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}
		p = p.Masked()
		if p.Bits() == 0 {
			return nil, fmt.Errorf("%s: %s is every address; a device that sends everything through this server is the full tunnel option", field, p)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

func wgPrefixStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func wgEndpointAddress(endpoint string) netip.Addr {
	dial, err := parseWGEndpoint(endpoint, wgDefaultPort)
	if err != nil {
		return netip.Addr{}
	}
	host, _ := splitHostPort(dial)
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.WithZone("").Unmap()
}

// wgLastAddr is a network's last address, its broadcast.
func wgLastAddr(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().AsSlice()
	for bit := p.Bits(); bit < len(b)*8; bit++ {
		b[bit/8] |= 0x80 >> (bit % 8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

// wgNextPeerAddress is the first address in the tunnel's network no peer holds,
// starting after the server's.
func wgNextPeerAddress(subnet netip.Prefix, server netip.Addr, taken []netip.Prefix) (netip.Addr, error) {
	last := wgLastAddr(subnet)
	for a := server.Next(); subnet.Contains(a) && a != last; a = a.Next() {
		free := true
		for _, t := range taken {
			if t.Contains(a) {
				free = false
				break
			}
		}
		if free {
			return a, nil
		}
	}
	return netip.Addr{}, fmt.Errorf("every address in %s is taken", subnet)
}

// wgClientConfig is what the client imports, in the form `wg-quick` and the
// phone apps both read.
type wgClientConfig struct {
	privateKey string
	address    netip.Addr
	dns        []string
	mtu        int
	listenPort int
	serverKey  string
	psk        string
	endpoint   string
	allowedIPs []string
	keepalive  int
	fullTunnel bool
}

func (c wgClientConfig) render() string {
	var b strings.Builder
	b.WriteString("[Interface]\n")
	if c.fullTunnel {
		b.WriteString("# This tunnel carries IPv4. IPv6 is routed into it to prevent a native\n# IPv6 leak; IPv6 internet access needs server-side dual-stack configuration.\n")
	}
	fmt.Fprintf(&b, "PrivateKey = %s\n", c.privateKey)
	fmt.Fprintf(&b, "Address = %s/%d\n", c.address, c.address.BitLen())
	if c.listenPort > 0 {
		fmt.Fprintf(&b, "ListenPort = %d\n", c.listenPort)
	}
	if len(c.dns) > 0 {
		fmt.Fprintf(&b, "DNS = %s\n", strings.Join(c.dns, ", "))
	}
	if c.mtu > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", c.mtu)
	}
	b.WriteString("\n[Peer]\n")
	fmt.Fprintf(&b, "PublicKey = %s\n", c.serverKey)
	fmt.Fprintf(&b, "PresharedKey = %s\n", c.psk)
	fmt.Fprintf(&b, "Endpoint = %s\n", c.endpoint)
	fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(c.allowedIPs, ", "))
	if c.keepalive > 0 {
		fmt.Fprintf(&b, "PersistentKeepalive = %d\n", c.keepalive)
	}
	return b.String()
}

// AddWireGuardPeer adds a device or a site to a tunnel the dashboard made.
func (s *Service) AddWireGuardPeer(ctx context.Context, iface string, req WGPeerRequest, client, actor string) (*WGPeerResult, error) {
	if err := validWGName(iface); err != nil {
		return nil, err
	}
	if err := requireWG(); err != nil {
		return nil, err
	}
	name, err := CleanLabel(req.Name, 64)
	if err != nil {
		return nil, err
	}
	if req.Kind != wgKindDevice && req.Kind != wgKindSite {
		return nil, fmt.Errorf("the kind is device or site")
	}
	if req.Kind == wgKindDevice && (len(req.RemoteNetworks) > 0 || req.Endpoint != "") {
		return nil, fmt.Errorf("remote networks and an endpoint belong to a site; a device roams and is dialled by nobody")
	}
	if req.Kind == wgKindSite && req.FullTunnel {
		return nil, fmt.Errorf("a full tunnel is a device's setting; a site shares networks instead")
	}
	keepalive := wgDefaultKeepalive
	if req.Keepalive != nil {
		keepalive = *req.Keepalive
	}
	if keepalive < 0 || keepalive > 65535 {
		return nil, fmt.Errorf("the keepalive is 0 (off) to 65535 seconds")
	}
	share, err := wgParseNetworks(req.ShareNetworks, "shareNetworks")
	if err != nil {
		return nil, err
	}
	remote, err := wgParseNetworks(req.RemoteNetworks, "remoteNetworks")
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	conf, err := s.managedWG(iface)
	if err != nil {
		return nil, err
	}
	isec := conf.iface()

	var subnet netip.Prefix
	var serverAddr netip.Addr
	for _, a := range isec.list("address") {
		if p, err := ParsePrefix(a); err == nil && p.Addr().Is4() {
			subnet, serverAddr = p.Masked(), p.Addr()
			break
		}
	}
	if !subnet.IsValid() {
		return nil, fmt.Errorf("%s has no IPv4 address to hand clients one from", iface)
	}
	meta := isec.bodyMeta()
	endpoint := meta["endpoint"]
	if endpoint == "" {
		return nil, fmt.Errorf("%s does not record the address clients dial; add a line \"# jd:endpoint=host:port\" under [Interface]", iface)
	}
	serverPub, err := wgPublicKey(isec.get("privatekey"))
	if err != nil {
		return nil, fmt.Errorf("%s has no usable PrivateKey: %w", iface, err)
	}
	mtu, _ := strconv.Atoi(isec.get("mtu"))

	var taken []netip.Prefix
	for _, sec := range conf.peers() {
		p := wgPeerOf(sec)
		if strings.EqualFold(p.name, name) {
			return nil, fmt.Errorf("a peer called %s: %w", name, ErrExists)
		}
		for _, a := range p.allowedIPs {
			if pp, err := ParsePrefix(a); err == nil {
				taken = append(taken, pp.Masked())
			}
		}
	}
	taken = append(taken, netip.PrefixFrom(serverAddr, serverAddr.BitLen()))

	for _, r := range remote {
		if r.Overlaps(subnet) {
			return nil, fmt.Errorf("remoteNetworks: %s overlaps the tunnel's own network %s", r, subnet)
		}
		for _, t := range taken {
			if t.Overlaps(r) {
				return nil, fmt.Errorf("remoteNetworks: %s overlaps %s, already routed to another peer of %s", r, t, iface)
			}
		}
	}
	if len(remote) > 0 {
		// The route to a tunnel's public endpoint must stay outside it.
		// Default routes do not appear in host.overlap, so a remote LAN can
		// otherwise capture the very UDP packets that carry that LAN.
		endpoints := []netip.Addr{}
		if a := wgEndpointAddress(req.Endpoint); a.IsValid() {
			endpoints = append(endpoints, a)
		}
		confs, err := s.listWGConfs()
		if err != nil {
			return nil, err
		}
		for _, other := range confs {
			for _, peer := range other.peers() {
				if a := wgEndpointAddress(wgPeerOf(peer).endpoint); a.IsValid() {
					endpoints = append(endpoints, a)
				}
			}
		}
		host, err := wgReadHostState(ctx)
		if err != nil {
			return nil, err
		}
		var clientAddr netip.Addr
		if client != "" {
			clientAddr, _ = ParseAddr(client)
		}
		for _, r := range remote {
			if hp, hit := host.overlap(r, iface); hit {
				return nil, guarded("%s overlaps %s, which is %s; routing it into the tunnel would take it away from there", r, hp.prefix, hp.what)
			}
			if clientAddr.IsValid() && r.Contains(clientAddr) {
				return nil, guarded("%s contains your own address (%s), so routing it into the tunnel would cut your connection to the dashboard", r, clientAddr)
			}
			for _, endpoint := range endpoints {
				if r.Contains(endpoint) {
					return nil, guarded("%s contains the WireGuard endpoint %s; routing its transport into %s would disconnect the tunnel", r, endpoint, iface)
				}
			}
		}
	}

	addr, err := wgNextPeerAddress(subnet, serverAddr, taken)
	if err != nil {
		return nil, err
	}
	priv, pub, err := newWGKeyPair()
	if err != nil {
		return nil, err
	}
	psk, err := newWGPresharedKey()
	if err != nil {
		return nil, err
	}

	cc := wgClientConfig{
		privateKey: priv, address: addr, mtu: mtu,
		serverKey: serverPub, psk: psk, endpoint: endpoint, keepalive: keepalive,
		fullTunnel: req.FullTunnel,
	}
	cc.allowedIPs = []string{subnet.String()}
	if req.FullTunnel {
		cc.allowedIPs = []string{"0.0.0.0/0", "::/0"}
	}
	for _, n := range share {
		if n != subnet && !req.FullTunnel {
			cc.allowedIPs = append(cc.allowedIPs, n.String())
		}
	}
	settings := [][2]string{{"PublicKey", pub}, {"PresharedKey", psk}}
	serverAllowed := []string{netip.PrefixFrom(addr, addr.BitLen()).String()}
	serverAllowed = append(serverAllowed, wgPrefixStrings(remote)...)
	settings = append(settings, [2]string{"AllowedIPs", strings.Join(serverAllowed, ", ")})

	if req.Kind == wgKindDevice {
		cc.dns = wgSplitList(meta["dns"])
	} else {
		if req.Endpoint != "" {
			dial, err := parseWGEndpoint(req.Endpoint, wgDefaultPort)
			if err != nil {
				return nil, err
			}
			settings = append(settings, [2]string{"Endpoint", dial})
			// The remote listens on the port this server dials, or the dial
			// would reach nothing.
			cc.listenPort, _ = strconv.Atoi(dial[strings.LastIndex(dial, ":")+1:])
		}
		if keepalive > 0 {
			settings = append(settings, [2]string{"PersistentKeepalive", strconv.Itoa(keepalive)})
		}
	}
	config := cc.render()
	// Before anything is written: a configuration that cannot be a QR code is
	// refused while nothing has changed.
	qr, err := qrDataURL(config)
	if err != nil {
		return nil, err
	}

	var warnings []string
	if !wgIPForwarding("ipv4") && (req.FullTunnel || len(share) > 0 || len(remote) > 0) {
		warnings = append(warnings, wgForwardingWarning)
	}
	if req.FullTunnel {
		warnings = append(warnings, "This full tunnel carries IPv4. IPv6 is blocked inside the tunnel to prevent a native IPv6 leak; IPv6 internet access needs server-side dual-stack configuration.")
		if sp, err := s.loadSpec(); err == nil {
			exit := false
			for _, n := range sp.NAT {
				exit = exit || (n.Owner == wgOwner(iface) && n.Enabled)
			}
			if !exit {
				warnings = append(warnings, "This device sends all its traffic through "+iface+", which is not an exit node, so it will have no internet until it is made one.")
			}
		}
	}

	original := conf.render()
	now := wgNow()
	id, err := s.saveClient(ctx, conf, VPNClient{
		Iface: iface, PublicKey: pub, Name: name, Kind: req.Kind,
		Address: addr.String(), CreatedBy: actor, CreatedAt: now,
	}, config)
	if err != nil {
		return nil, err
	}
	rollback := func(reload bool) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = s.vpn.Delete(cleanup, iface, id) // the row may already be gone; either way nothing refers to it
		if path, err := s.wgConfPath(iface); err == nil {
			_ = writeFileAtomic(path, []byte(original), 0o600) // best effort; the failure being reported is the first one
		}
		if reload {
			_, _ = s.syncWG(cleanup, iface)
		}
	}

	conf.appendPeer(wgNewPeerSection(int(id), name, req.Kind, now, settings))
	if err := s.writeWGConf(iface, conf); err != nil {
		rollback(false)
		return nil, err
	}
	live, err := s.syncWG(ctx, iface)
	if err != nil {
		rollback(true)
		return nil, err
	}
	if live && len(remote) > 0 {
		if err := s.addPeerRoutes(ctx, iface, remote, client); err != nil {
			rollback(true)
			return nil, err
		}
	}
	if !live {
		warnings = append(warnings, iface+" is not running, so the peer takes effect when it is started.")
	}

	view, err := s.readWireGuard(ctx, iface, true)
	if err != nil {
		return nil, err
	}
	res := &WGPeerResult{Config: config, QR: qr, Reloaded: live, Warnings: append([]string{}, warnings...)}
	for _, ifc := range view.Interfaces {
		for _, p := range ifc.Peers {
			if p.ID == int(id) {
				res.Peer = p
			}
		}
	}
	if res.Peer.ID == 0 {
		return nil, fmt.Errorf("the peer was added but could not be read back")
	}
	return res, nil
}

// saveClient stores a client and returns the id its peer block will carry. The
// id is the row's, so a number a peer already has in the file — the database
// was restored from an older copy, or reset — is never handed out twice: a
// row that lands on one is removed and the insert tried again, which the
// store's ever-increasing sequence makes come out differently.
func (s *Service) saveClient(ctx context.Context, conf *wgConf, c VPNClient, config string) (int64, error) {
	used := map[int]bool{}
	for _, sec := range conf.peers() {
		used[wgPeerOf(sec).id] = true
	}
	for range len(used) + 1 {
		id, err := s.vpn.Save(ctx, c, config)
		if err != nil {
			return 0, err
		}
		if !used[int(id)] {
			return id, nil
		}
		if err := s.vpn.Delete(ctx, c.Iface, id); err != nil {
			return 0, err
		}
	}
	return 0, fmt.Errorf("no unused peer number could be found")
}

// syncWG makes the running interface match its file, by the unit's own
// reload (`wg syncconf` over the stripped configuration), which changes
// peers without dropping the tunnel's other sessions. It reports whether the
// interface was running; a stopped one has nothing to sync and will read the
// file when it starts.
func (s *Service) syncWG(ctx context.Context, iface string) (bool, error) {
	unit := "wg-quick@" + iface
	state, err := run(ctx, "systemctl", "is-active", unit)
	if err != nil && (ctx.Err() != nil || strings.TrimSpace(state) == "") {
		return false, fmt.Errorf("reading %s state: %w", unit, err)
	}
	if strings.TrimSpace(state) != "active" {
		return false, nil
	}
	if _, err := run(ctx, "systemctl", "reload", unit); err != nil {
		return true, fmt.Errorf("reloading %s: %w", unit, err)
	}
	return true, nil
}

// addPeerRoutes routes a site's networks into the tunnel. `wg syncconf`
// changes the interface's peers and nothing else, while `wg-quick up` is what
// installs a route for every allowed address, so a peer added to a running
// tunnel has its keys and no road until its networks are routed here. The
// change is tested against the client path like every other route, and
// undone if it moved.
func (s *Service) addPeerRoutes(ctx context.Context, iface string, nets []netip.Prefix, client string) error {
	var before Path
	if client != "" {
		var err error
		if before, err = clientPath(ctx, client); err != nil {
			return err
		}
	}
	var added []netip.Prefix
	undo := func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for _, n := range added {
			_, _ = run(cleanup, "ip", "route", "del", n.String(), "dev", iface) // the route being undone may not have been added
		}
	}
	for _, n := range nets {
		if _, err := run(ctx, "ip", "route", "replace", n.String(), "dev", iface); err != nil {
			undo()
			return fmt.Errorf("routing %s into %s: %w", n, iface, err)
		}
		added = append(added, n)
	}
	if client != "" {
		if err := verifyPath(before)(ctx); err != nil {
			undo()
			return err
		}
	}
	return nil
}

// RemoveWireGuardPeer removes a peer's block from its tunnel, the tunnel's
// knowledge of it, and its stored configuration.
func (s *Service) RemoveWireGuardPeer(ctx context.Context, iface string, id int, client string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	conf, err := s.managedWG(iface)
	if err != nil {
		return err
	}
	var target *wgPeerConf
	for _, sec := range conf.peers() {
		if p := wgPeerOf(sec); p.id == id && id != 0 {
			p := p
			target = &p
		}
	}
	if target == nil {
		return fmt.Errorf("peer %d of %s: %w", id, iface, ErrNotFound)
	}
	if addr, err := ParseAddr(client); err == nil {
		for _, a := range target.allowedIPs {
			if p, err := ParsePrefix(a); err == nil && p.Bits() > 0 && p.Contains(addr) {
				return guarded("%s is how your own connection (%s) reaches the dashboard, so it cannot be removed", target.name, addr)
			}
		}
	}

	original := conf.render()
	conf.removePeer(target.sec)
	if err := s.writeWGConf(iface, conf); err != nil {
		return err
	}
	restore := func() {
		if path, err := s.wgConfPath(iface); err == nil {
			_ = writeFileAtomic(path, []byte(original), 0o600) // best effort; the failure being reported is the first one
		}
	}
	if has("wg-quick") && has("systemctl") {
		if _, err := s.syncWG(ctx, iface); err != nil {
			restore()
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_, _ = s.syncWG(cleanup, iface)
			return err
		}
	} else if has("wg") {
		// Without wg-quick there is no reload to run; `wg set … remove` takes
		// the peer out of a running interface directly, and fails harmlessly
		// when the interface is down.
		_, _ = run(ctx, "wg", "set", iface, "peer", target.publicKey, "remove")
	}
	// A reload takes the peer out of the interface but leaves the routes that
	// carried its networks into the tunnel; they would go on blackholing them.
	for _, a := range target.allowedIPs {
		if p, err := ParsePrefix(a); err == nil {
			peerAddress := false
			for _, address := range conf.iface().list("address") {
				if subnet, err := ParsePrefix(address); err == nil && p.Bits() == p.Addr().BitLen() && subnet.Contains(p.Addr()) {
					peerAddress = true
				}
			}
			if peerAddress {
				continue
			}
			_, _ = run(ctx, "ip", "route", "del", p.Masked().String(), "dev", iface) // absent when the tunnel was down
		}
	}
	if err := s.vpn.Delete(ctx, iface, int64(id)); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// WireGuardPeerConfig shows a stored client configuration again, for an
// administrator who has to re-import it. ErrForgotten is its being gone.
func (s *Service) WireGuardPeerConfig(ctx context.Context, iface string, id int) (*WGPeerConfig, error) {
	if err := validWGName(iface); err != nil {
		return nil, err
	}
	c, err := s.vpn.Get(ctx, iface, int64(id))
	if err != nil {
		return nil, err
	}
	qr, err := qrDataURL(c.Config)
	if err != nil {
		return nil, err
	}
	return &WGPeerConfig{Name: c.Name, Config: c.Config, QR: qr}, nil
}

// ForgetWireGuardPeerConfig destroys the stored copy of a client's private
// key. The peer keeps working; it can no longer be shown again.
func (s *Service) ForgetWireGuardPeerConfig(ctx context.Context, iface string, id int) error {
	if err := validWGName(iface); err != nil {
		return err
	}
	return s.vpn.Forget(ctx, iface, int64(id))
}
