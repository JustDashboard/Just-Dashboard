package netx

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Editing a peer the dashboard made, and verifying a site end to end. A site's
// networks are routed into the tunnel on this server, so every network it is
// given passes the guards a new site's do; a device's routes live in its own
// configuration, so changing them means a new configuration to import.

// refuseSiteRoutes is every guard a site's networks pass before they are
// routed into a tunnel: no host network, not the operator's own address, and
// no WireGuard transport — the request's endpoint resolved if it is a name,
// every endpoint in a tunnel file, and every endpoint the kernel reports now,
// which is where a roaming peer actually is.
func (s *Service) refuseSiteRoutes(ctx context.Context, iface string, remote []netip.Prefix, endpoint, client string) error {
	endpoints, err := s.transportEndpoints(ctx, endpoint)
	if err != nil {
		return err
	}
	host, err := wgReadHostState(ctx)
	if err != nil {
		return err
	}
	var clientAddr netip.Addr
	if client != "" {
		clientAddr, _ = ParseAddr(client)
	}
	for _, r := range remote {
		if hp, hit := host.overlap(r, iface); hit {
			return guarded("%s overlaps %s, which is %s; routing it into the tunnel would take it away from there", r, hp.prefix, hp.what)
		}
		if clientAddr.IsValid() && r.Contains(clientAddr) {
			return guarded("%s contains your own address (%s), so routing it into the tunnel would cut your connection to the dashboard", r, clientAddr)
		}
		for _, e := range endpoints {
			if r.Contains(e) {
				return guarded("%s contains the WireGuard endpoint %s; routing its transport into %s would disconnect the tunnel", r, e, iface)
			}
		}
	}
	return nil
}

// transportEndpoints are the addresses WireGuard's encrypted packets go to
// now, with endpoint (a site's, being added or edited) resolved when it is a
// name. A name that does not resolve is refused: WireGuard resolves it when
// the tunnel loads its peers, and a reload that cannot is a failed change.
func (s *Service) transportEndpoints(ctx context.Context, endpoint string) ([]netip.Addr, error) {
	var out []netip.Addr
	if endpoint != "" {
		dial, err := parseWGEndpoint(endpoint, wgDefaultPort)
		if err != nil {
			return nil, err
		}
		host, _ := splitHostPort(dial)
		if a, err := netip.ParseAddr(host); err == nil {
			out = append(out, a.WithZone("").Unmap())
		} else {
			resolved, err := wgLookupHost(ctx, host)
			if err != nil || len(resolved) == 0 {
				return nil, fmt.Errorf("the endpoint %s does not resolve here; WireGuard needs it to resolve when the tunnel loads its peers", host)
			}
			out = append(out, resolved...)
		}
	}
	confs, err := s.listWGConfs()
	if err != nil {
		return nil, err
	}
	for _, c := range confs {
		for _, peer := range c.peers() {
			if a := wgEndpointAddress(wgPeerOf(peer).endpoint); a.IsValid() {
				out = append(out, a)
			}
		}
	}
	if has("wg") {
		if raw, err := run(ctx, "wg", "show", "all", "dump"); err == nil {
			for _, ifc := range parseWGDump(raw) {
				for _, p := range ifc.peers {
					if a := wgEndpointAddress(p.endpoint); a.IsValid() {
						out = append(out, a)
					}
				}
			}
		}
	}
	return out, nil
}

// WGPeerEdit changes a peer the dashboard made; a nil field is left as it is.
// Name and, for a site, its endpoint, keepalive and networks change on this
// server. A device's routes and keepalive, and the port a dialled site listens
// on, are in the peer's own configuration, which is regenerated from its
// sealed copy and has to be imported again.
type WGPeerEdit struct {
	Name           *string   `json:"name"`
	Keepalive      *int      `json:"keepalive"`
	Endpoint       *string   `json:"endpoint"`
	RemoteNetworks *[]string `json:"remoteNetworks"`
	FullTunnel     *bool     `json:"fullTunnel"`
	ShareNetworks  *[]string `json:"shareNetworks"`
}

// WGPeerEditResult is the peer as it is now and, when its own configuration
// changed, the new one to import.
type WGPeerEditResult struct {
	Peer          WGPeer   `json:"peer"`
	ClientChanged bool     `json:"clientChanged"`
	Config        string   `json:"config,omitempty"`
	QR            string   `json:"qr,omitempty"`
	Reloaded      bool     `json:"reloaded"`
	Warnings      []string `json:"warnings"`
}

// EditWireGuardPeer applies an edit. Taking a network away from a site or
// changing where this server dials it can disconnect what rides on it, so
// those edits call authorizeWithdrawal, evaluated against the peer as it is
// under the mutation lock, before anything changes.
func (s *Service) EditWireGuardPeer(ctx context.Context, iface string, id int, req WGPeerEdit, client, actor string, authorizeWithdrawal func() error) (*WGPeerEditResult, error) {
	if err := validWGName(iface); err != nil {
		return nil, err
	}
	if err := requireWG(); err != nil {
		return nil, err
	}
	if req == (WGPeerEdit{}) {
		return nil, fmt.Errorf("nothing to change")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conf, err := s.managedWG(iface)
	if err != nil {
		return nil, err
	}
	var target *wgPeerConf
	for _, sec := range conf.peers() {
		if p := wgPeerOf(sec); p.id == id && id != 0 {
			target = &p
		}
	}
	if target == nil {
		return nil, fmt.Errorf("peer %d of %s: %w", id, iface, ErrNotFound)
	}
	site := target.kind == wgKindSite
	if !site && (req.Endpoint != nil || req.RemoteNetworks != nil) {
		return nil, fmt.Errorf("an endpoint and remote networks belong to a site; a device roams and is dialled by nobody")
	}
	if site && req.FullTunnel != nil && *req.FullTunnel {
		return nil, fmt.Errorf("a full tunnel is a device's setting; a site shares networks instead")
	}

	v4, v6 := wgInterfacePrefixes(conf)
	subnet := v4.Masked()
	dual := wgIPv6Enabled(conf) && v6.IsValid()
	name := target.name
	if req.Name != nil {
		if name, err = CleanLabel(*req.Name, 64); err != nil {
			return nil, err
		}
		for _, sec := range conf.peers() {
			if p := wgPeerOf(sec); p.id != id && strings.EqualFold(p.name, name) {
				return nil, fmt.Errorf("a peer called %s: %w", name, ErrExists)
			}
		}
	}
	if req.Keepalive != nil && (*req.Keepalive < 0 || *req.Keepalive > 65535) {
		return nil, fmt.Errorf("the keepalive is 0 (off) to 65535 seconds")
	}

	// The peer's own addresses inside the tunnel stay; what follows them are
	// the site's networks.
	var own, oldRemote []netip.Prefix
	for _, raw := range target.allowedIPs {
		p, err := ParsePrefix(raw)
		if err != nil {
			continue
		}
		if p.Bits() == p.Addr().BitLen() && (subnet.Contains(p.Addr()) || (dual && v6.Masked().Contains(p.Addr()))) {
			own = append(own, p)
		} else {
			oldRemote = append(oldRemote, p.Masked())
		}
	}
	remote := oldRemote
	if req.RemoteNetworks != nil {
		if remote, err = wgParseNetworks(*req.RemoteNetworks, "remoteNetworks"); err != nil {
			return nil, err
		}
	}
	endpoint := target.endpoint
	if req.Endpoint != nil {
		endpoint = ""
		if raw := strings.TrimSpace(*req.Endpoint); raw != "" {
			if endpoint, err = parseWGEndpoint(raw, wgDefaultPort); err != nil {
				return nil, err
			}
		}
	}
	var added, removed []netip.Prefix
	for _, r := range remote {
		if !slices.Contains(oldRemote, r) {
			added = append(added, r)
		}
	}
	for _, r := range oldRemote {
		if !slices.Contains(remote, r) {
			removed = append(removed, r)
		}
	}
	if authorizeWithdrawal != nil && (len(removed) > 0 || (req.Endpoint != nil && endpoint != target.endpoint)) {
		if err := authorizeWithdrawal(); err != nil {
			return nil, err
		}
	}
	if len(added) > 0 || (req.Endpoint != nil && endpoint != target.endpoint && len(remote) > 0) {
		for _, r := range remote {
			if r.Overlaps(subnet) || (dual && r.Overlaps(v6.Masked())) {
				return nil, fmt.Errorf("remoteNetworks: %s overlaps the tunnel's own network", r)
			}
			for _, sec := range conf.peers() {
				other := wgPeerOf(sec)
				if other.id == id {
					continue
				}
				for _, a := range other.allowedIPs {
					if t, err := ParsePrefix(a); err == nil && t.Overlaps(r) {
						return nil, fmt.Errorf("remoteNetworks: %s overlaps %s, already routed to another peer of %s", r, t.Masked(), iface)
					}
				}
			}
		}
		if err := s.refuseSiteRoutes(ctx, iface, remote, endpoint, client); err != nil {
			return nil, err
		}
	}

	// The peer's own configuration: regenerated from its sealed copy when an
	// edit reaches it. A forgotten copy cannot be regenerated without its
	// private key, so such an edit is refused rather than half applied.
	clientEdit := req.FullTunnel != nil || req.ShareNetworks != nil || req.Keepalive != nil ||
		(site && req.Endpoint != nil && endpoint != target.endpoint)
	var stored VPNClient
	var newConfig, newRoutes, qr string
	if clientEdit {
		stored, err = s.peerClient(ctx, iface, id)
		if errors.Is(err, ErrForgotten) {
			return nil, &ReadOnlyError{Reason: "this change is in " + name + "'s own configuration, which was forgotten here; remove the peer and add it again to make a new one"}
		}
		if err != nil {
			return nil, err
		}
		cc, err := wgClientFrom(stored.Config)
		if err != nil {
			return nil, err
		}
		if req.FullTunnel != nil || req.ShareNetworks != nil {
			full := cc.fullTunnel
			if req.FullTunnel != nil {
				full = *req.FullTunnel
			}
			share := cc.sharedBeyond(subnet, v6, dual)
			if req.ShareNetworks != nil {
				if share, err = wgParseNetworks(*req.ShareNetworks, "shareNetworks"); err != nil {
					return nil, err
				}
			}
			cc.fullTunnel = full
			cc.allowedIPs = wgClientRoutes(subnet, v6, dual, full, share)
		}
		if req.Keepalive != nil {
			cc.keepalive = *req.Keepalive
		}
		if site && req.Endpoint != nil {
			cc.listenPort = 0
			if endpoint != "" {
				cc.listenPort, _ = strconv.Atoi(endpoint[strings.LastIndex(endpoint, ":")+1:])
			}
		}
		newConfig = cc.render()
		if qr, err = qrDataURL(newConfig); err != nil {
			return nil, err
		}
		if !site {
			newRoutes = strings.Join(cc.allowedIPs, ", ")
		}
	}

	// The server's half: metadata, AllowedIPs, endpoint and keepalive.
	original := conf.render()
	sec := target.sec
	if req.Name != nil {
		wgSetLeadMeta(sec, "name", name)
	}
	if req.RemoteNetworks != nil {
		sec.set("AllowedIPs", strings.Join(append(wgPrefixStrings(own), wgPrefixStrings(remote)...), ", "))
	}
	if site && req.Endpoint != nil {
		if endpoint == "" {
			sec.remove("endpoint")
		} else {
			sec.set("Endpoint", endpoint)
		}
	}
	if site && req.Keepalive != nil {
		if *req.Keepalive == 0 {
			sec.remove("persistentkeepalive")
		} else {
			sec.set("PersistentKeepalive", strconv.Itoa(*req.Keepalive))
		}
	}
	serverChanged := conf.render() != original

	oldName := stored.Name
	if !clientEdit {
		if c, err := s.vpn.Get(ctx, iface, int64(id)); err == nil || errors.Is(err, ErrForgotten) {
			oldName = c.Name
		}
	}
	fail := func(cause error, reload bool) (*WGPeerEditResult, error) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		var left []string
		if path, err := s.wgConfPath(iface); err == nil && serverChanged {
			if err := writeFileAtomic(path, []byte(original), 0o600); err != nil {
				left = append(left, "the tunnel's file: "+err.Error())
			}
		}
		if reload {
			if _, err := s.syncWG(cleanup, iface); err != nil {
				left = append(left, "the running interface: "+err.Error())
			}
		}
		if clientEdit {
			if err := s.vpn.Update(cleanup, iface, int64(id), oldName, stored.Config, stored.ClientRoutes); err != nil {
				left = append(left, "the stored configuration: "+err.Error())
			}
		} else if req.Name != nil && oldName != "" {
			if err := s.vpn.Update(cleanup, iface, int64(id), oldName, "", ""); err != nil && !errors.Is(err, ErrNotFound) {
				left = append(left, "the stored name: "+err.Error())
			}
		}
		e := WGEvent{Iface: iface, Kind: "peer_edit_failed", Outcome: wgOutcomeFailed, Peer: target.publicKey, PeerName: target.name, Actor: actor, Detail: cause.Error() + "; put back"}
		if len(left) > 0 {
			e.Outcome, e.Detail = wgOutcomeDegraded, cause.Error()+"; not restored: "+strings.Join(left, "; ")
		}
		s.wg.note(cleanup, e)
		return nil, cause
	}

	if clientEdit || req.Name != nil {
		if err := s.vpn.Update(ctx, iface, int64(id), name, newConfig, newRoutes); err != nil && !(errors.Is(err, ErrNotFound) && !clientEdit) {
			return nil, err
		}
	}
	live := false
	if serverChanged {
		if err := s.writeWGConf(iface, conf); err != nil {
			return fail(err, false)
		}
		if live, err = s.syncWG(ctx, iface); err != nil {
			return fail(err, true)
		}
		if live && len(added) > 0 {
			if err := s.addPeerRoutes(ctx, iface, added, client); err != nil {
				return fail(err, true)
			}
		}
		if live {
			for _, r := range removed {
				// Absent when the route was never installed; the kernel's
				// answer either way is that the network no longer goes here.
				_, _ = run(ctx, "ip", "route", "del", r.String(), "dev", iface)
			}
		}
	}

	var changed []string
	if req.Name != nil && name != target.name {
		changed = append(changed, "name "+name)
	}
	if len(added) > 0 {
		changed = append(changed, "routes "+strings.Join(wgPrefixStrings(added), ", ")+" added")
	}
	if len(removed) > 0 {
		changed = append(changed, "routes "+strings.Join(wgPrefixStrings(removed), ", ")+" withdrawn")
	}
	if site && req.Endpoint != nil && endpoint != target.endpoint {
		changed = append(changed, "endpoint "+firstNonEmpty(endpoint, "cleared"))
	}
	if req.Keepalive != nil {
		changed = append(changed, "keepalive "+strconv.Itoa(*req.Keepalive))
	}
	if clientEdit && (req.FullTunnel != nil || req.ShareNetworks != nil) {
		changed = append(changed, "client routes "+newRoutes)
	}
	if clientEdit {
		changed = append(changed, "its own configuration was regenerated")
	}
	s.wg.note(ctx, WGEvent{Iface: iface, Kind: "peer_edited", Peer: target.publicKey, PeerName: name, Actor: actor, Detail: strings.Join(changed, "; ")})

	res := &WGPeerEditResult{ClientChanged: clientEdit, Reloaded: live, Warnings: []string{}}
	if clientEdit {
		res.Config, res.QR = newConfig, qr
		res.Warnings = append(res.Warnings, name+" keeps its old configuration until the new one is imported on it.")
	}
	if serverChanged && !live {
		res.Warnings = append(res.Warnings, iface+" is not running, so the change takes effect when it is started.")
	}
	view, err := s.readWireGuard(ctx, iface, true)
	if err != nil {
		return nil, err
	}
	for _, ifc := range view.Interfaces {
		for _, p := range ifc.Peers {
			if p.ID == id {
				res.Peer = p
			}
		}
	}
	return res, nil
}

// wgClientFrom reads a configuration the dashboard generated back into the
// form it renders from.
func wgClientFrom(text string) (wgClientConfig, error) {
	c := parseWGConf(text)
	sec, peers := c.iface(), c.peers()
	if sec == nil || len(peers) != 1 {
		return wgClientConfig{}, fmt.Errorf("the stored configuration is not one this dashboard generated")
	}
	peer := peers[0]
	cc := wgClientConfig{
		privateKey: sec.get("privatekey"), dns: sec.list("dns"),
		serverKey: peer.get("publickey"), psk: peer.get("presharedkey"), endpoint: peer.get("endpoint"),
		allowedIPs: peer.list("allowedips"),
	}
	cc.mtu, _ = strconv.Atoi(sec.get("mtu"))
	cc.listenPort, _ = strconv.Atoi(sec.get("listenport"))
	cc.keepalive, _ = strconv.Atoi(peer.get("persistentkeepalive"))
	for _, raw := range sec.list("address") {
		p, err := ParsePrefix(raw)
		if err != nil {
			continue
		}
		if p.Addr().Is4() && !cc.address.IsValid() {
			cc.address = p.Addr()
		} else if p.Addr().Is6() && !cc.address6.IsValid() {
			cc.address6 = p.Addr()
		}
	}
	if !validWGKey(cc.privateKey) || !validWGKey(cc.serverKey) || !cc.address.IsValid() {
		return wgClientConfig{}, fmt.Errorf("the stored configuration is not one this dashboard generated")
	}
	cc.fullTunnel = slices.Contains(cc.allowedIPs, "0.0.0.0/0")
	return cc, nil
}

// sharedBeyond is what a configuration shares besides the tunnel's networks
// and the default routes: the networks an edit keeps when it only switches
// the full tunnel.
func (c wgClientConfig) sharedBeyond(subnet, v6 netip.Prefix, dual bool) []netip.Prefix {
	var out []netip.Prefix
	for _, raw := range c.allowedIPs {
		p, err := ParsePrefix(raw)
		if err != nil || p.Bits() == 0 || p.Masked() == subnet || (dual && p.Masked() == v6.Masked()) {
			continue
		}
		out = append(out, p.Masked())
	}
	return out
}

// wgClientRoutes are a client's AllowedIPs: the tunnel's networks and what is
// shared beyond them, or everything for a full tunnel.
func wgClientRoutes(subnet, v6 netip.Prefix, dual, full bool, share []netip.Prefix) []string {
	if full {
		return []string{"0.0.0.0/0", "::/0"}
	}
	out := []string{subnet.String()}
	if dual {
		out = append(out, v6.Masked().String())
	}
	for _, n := range share {
		if n != subnet && (!dual || n != v6.Masked()) {
			out = append(out, n.String())
		}
	}
	return out
}

// wgSetLeadMeta replaces one of a peer block's notes above its header.
func wgSetLeadMeta(sec *wgSection, key, value string) {
	prefix := "# " + wgMetaPrefix + key + "="
	for i, l := range sec.lead {
		if strings.HasPrefix(strings.TrimSpace(l.raw), prefix) {
			sec.lead[i] = wgMetaLine(key, value)
			return
		}
	}
	sec.lead = append(sec.lead, wgMetaLine(key, value))
}

// WGSiteCheck is one step of a site's verification.
type WGSiteCheck struct {
	Name string `json:"name"`
	// Status is pass, fail, warn (no answer, which a filter can also cause)
	// or skipped.
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// WGSiteVerification is a site checked from this end, with the steps that
// confirm the other end, which this server cannot run.
type WGSiteVerification struct {
	Peer     string `json:"peer"`
	PeerName string `json:"peerName"`
	At       int64  `json:"at"`
	// Outcome is verified (every check passed, including a round trip to the
	// target when one was given), partial (the tunnel works, an answer was
	// missing) or failed.
	Outcome     string        `json:"outcome"`
	Checks      []WGSiteCheck `json:"checks"`
	RemoteSteps []string      `json:"remoteSteps"`
}

// WGSiteVerifyRequest names an optional host inside the site's networks to
// reach from this server's tunnel address, which proves the site forwards to
// its LAN and its LAN routes the tunnel back.
type WGSiteVerifyRequest struct {
	Target string `json:"target"`
}

// VerifyWireGuardSite checks a site from this end: a fresh handshake, its
// networks routed into the tunnel, its transport outside every tunnel, an
// answer from its tunnel address and, when a target is given, a round trip
// into its LAN sourced from this server's tunnel address. Nothing changes;
// the result is recorded in the tunnel's history.
func (s *Service) VerifyWireGuardSite(ctx context.Context, iface string, id int, req WGSiteVerifyRequest, actor string) (*WGSiteVerification, error) {
	if err := validWGName(iface); err != nil {
		return nil, err
	}
	conf, err := s.managedWG(iface)
	if err != nil {
		return nil, err
	}
	var target *wgPeerConf
	for _, sec := range conf.peers() {
		if p := wgPeerOf(sec); p.id == id && id != 0 {
			target = &p
		}
	}
	if target == nil {
		return nil, fmt.Errorf("peer %d of %s: %w", id, iface, ErrNotFound)
	}
	if target.kind != wgKindSite {
		return nil, fmt.Errorf("%s is a device; a site is what has networks to verify", target.name)
	}
	v4, v6 := wgInterfacePrefixes(conf)
	dual := wgIPv6Enabled(conf) && v6.IsValid()
	var own, remote []netip.Prefix
	for _, raw := range target.allowedIPs {
		p, err := ParsePrefix(raw)
		if err != nil {
			continue
		}
		if p.Bits() == p.Addr().BitLen() && (v4.Masked().Contains(p.Addr()) || (dual && v6.Masked().Contains(p.Addr()))) {
			own = append(own, p)
		} else {
			remote = append(remote, p.Masked())
		}
	}
	var probe netip.Addr
	if t := strings.TrimSpace(req.Target); t != "" {
		if probe, err = ParseAddr(t); err != nil {
			return nil, fmt.Errorf("target: %w", err)
		}
		inside := false
		for _, r := range remote {
			inside = inside || r.Contains(probe)
		}
		if !inside {
			return nil, fmt.Errorf("target: %s is not inside %s's networks", probe, target.name)
		}
	}

	v := &WGSiteVerification{Peer: target.publicKey, PeerName: target.name, At: wgNow().Unix(), Checks: []WGSiteCheck{}}
	add := func(name, status, detail string) { v.Checks = append(v.Checks, WGSiteCheck{name, status, detail}) }

	out, err := run(ctx, "wg", "show", "all", "dump")
	var live map[string]*wgLiveIface
	if err == nil {
		live, err = wgCheckedDump(out)
	}
	var lp *wgLivePeer
	if err == nil && live[iface] != nil {
		for i := range live[iface].peers {
			if live[iface].peers[i].publicKey == target.publicKey {
				lp = &live[iface].peers[i]
			}
		}
	}
	switch {
	case err != nil:
		add("handshake", "fail", "WireGuard could not be read: "+err.Error())
	case live[iface] == nil:
		add("handshake", "fail", iface+" is not running")
	case lp == nil:
		add("handshake", "fail", target.name+" is in the file but not in the running interface")
	case lp.handshake == 0:
		add("handshake", "fail", "no handshake has ever completed")
	case v.At-lp.handshake > wgOnlineWithin:
		add("handshake", "fail", fmt.Sprintf("the last handshake was %s ago", time.Duration(v.At-lp.handshake)*time.Second))
	default:
		add("handshake", "pass", fmt.Sprintf("handshake %ds ago", v.At-lp.handshake))
	}

	for _, r := range remote {
		probeAddr := r.Addr()
		if r.Bits() < r.Addr().BitLen() {
			probeAddr = r.Addr().Next()
		}
		args := []string{"-j"}
		if probeAddr.Is6() {
			args = append(args, "-6")
		}
		raw, err := run(ctx, "ip", append(args, "route", "get", probeAddr.String())...)
		var p Path
		if err == nil {
			p, err = parseRouteGet(raw, Path{Address: probeAddr.String()})
		}
		switch {
		case err != nil:
			add("route "+r.String(), "fail", "no route: "+err.Error())
		case p.Device != iface:
			add("route "+r.String(), "fail", "routed through "+firstNonEmpty(p.Device, "nothing")+", not "+iface)
		default:
			add("route "+r.String(), "pass", "routed into "+iface)
		}
	}

	if lp != nil && lp.endpoint != "" {
		t := WGTransport{State: "unknown"}
		if a := wgEndpointAddress(lp.endpoint); a.IsValid() {
			t = wgTransportOf(ctx, wgTransportTarget{ref: wgPeerRef{iface, target.publicKey}, endpoint: lp.endpoint, addr: a, fwmark: live[iface].fwmark}, live)
		}
		switch t.State {
		case "native":
			add("transport", "pass", lp.endpoint+" leaves through "+t.Device)
		case "captured", "unroutable":
			add("transport", "fail", t.Reason)
		default:
			add("transport", "warn", "the route to "+lp.endpoint+" could not be read")
		}
	} else {
		add("transport", "skipped", "no endpoint has been seen for "+target.name+" yet")
	}

	ping := func(name string, dst, src netip.Addr) {
		if _, err := run(ctx, "ping", "-n", "-q", "-c", "3", "-W", "1", "-I", src.String(), dst.String()); err != nil {
			add(name, "warn", fmt.Sprintf("no answer from %s sourced from %s; ICMP may be filtered there", dst, src))
			return
		}
		add(name, "pass", fmt.Sprintf("%s answered %s through the tunnel", dst, src))
	}
	if len(own) > 0 && lp != nil {
		dst := own[0].Addr()
		src := v4.Addr()
		if dst.Is6() {
			src = v6.Addr()
		}
		ping("tunnel address", dst, src)
	} else {
		add("tunnel address", "skipped", "the site has no handshake to answer over")
	}
	if probe.IsValid() {
		src := v4.Addr()
		if probe.Is6() {
			src = v6.Addr()
		}
		if !src.IsValid() || lp == nil {
			add("target "+probe.String(), "skipped", "no tunnel address of that family or no running peer to source from")
		} else {
			ping("target "+probe.String(), probe, src)
		}
	}

	// Without a host inside the site's networks, the far side's LAN routing
	// was not exercised, only the tunnel itself, so that is partial too.
	v.Outcome = "verified"
	if !probe.IsValid() {
		v.Outcome = "partial"
	}
	for _, c := range v.Checks {
		if c.Status == "fail" {
			v.Outcome = "failed"
			break
		}
		if c.Status == "warn" || (c.Status == "skipped" && strings.HasPrefix(c.Name, "target")) {
			v.Outcome = "partial"
		}
	}
	server := v4.Addr().String()
	v.RemoteSteps = []string{
		"wg show — the peer for this server should show a handshake within the last two minutes",
		"ping -c 3 " + server + " — this server's tunnel address answers through the tunnel",
		"ip route get " + server + " — the route to this server's tunnel network leaves through the WireGuard interface",
	}
	if len(remote) > 0 {
		v.RemoteSteps = append(v.RemoteSteps,
			"From a host on "+remote[0].String()+": ping -c 3 "+server+" — the LAN routes "+v4.Masked().String()+" back through the site's WireGuard router")
	}
	outcome := map[string]string{"verified": wgOutcomeOK, "partial": wgOutcomeDegraded, "failed": wgOutcomeFailed}[v.Outcome]
	var summary []string
	for _, c := range v.Checks {
		summary = append(summary, c.Name+" "+c.Status)
	}
	s.wg.note(ctx, WGEvent{Iface: iface, At: v.At, Kind: "site_verified", Outcome: outcome, Peer: target.publicKey, PeerName: target.name, Actor: actor,
		Detail: v.Outcome + ": " + strings.Join(summary, ", ")})
	return v, nil
}
