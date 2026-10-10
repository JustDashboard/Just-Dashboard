package netx

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// WGIPv6Request is opt-in. A missing object keeps the installed IPv4 profile
// format; an empty object creates a private IPv6 /64 without internet egress.
type WGIPv6Request struct {
	Subnet   string `json:"subnet"`
	ExitNode bool   `json:"exitNode"`
}

type WGFamilyState struct {
	Configured bool        `json:"configured"`
	Subnet     string      `json:"subnet,omitempty"`
	Runtime    string      `json:"runtime"`
	Reason     string      `json:"reason,omitempty"`
	Exit       WGExitState `json:"exit"`
}

type WGExitState struct {
	Configured bool       `json:"configured"`
	Interface  string     `json:"interface,omitempty"`
	Runtime    string     `json:"runtime"`
	Reason     string     `json:"reason,omitempty"`
	Capability Capability `json:"capability"`
	// Translated is the owned masquerade rule's packet counter: a NAT chain
	// sees only a connection's first packet, so it counts the connections
	// clients sent out through this exit since the rules were last loaded.
	// It is measured use of the exit, not proof of reachability beyond it.
	Translated *uint64 `json:"translated,omitempty"`
}

type WGFamilies struct {
	IPv4 WGFamilyState `json:"ipv4"`
	IPv6 WGFamilyState `json:"ipv6"`
}

func wgIPv6Subnet(requested string, host wgHostState, other []wgHostPrefix) (netip.Prefix, error) {
	check := func(p netip.Prefix) error {
		if !p.Addr().Is6() || p.Addr().Is4In6() || p.Bits() != 64 || !p.Addr().IsPrivate() {
			return fmt.Errorf("the IPv6 tunnel network is a unique-local /64, such as fd42:8::/64")
		}
		if hp, hit := host.overlap(p, ""); hit {
			return fmt.Errorf("%s overlaps %s, which is %s", p, hp.prefix, hp.what)
		}
		for _, hp := range other {
			if hp.prefix.Overlaps(p) {
				return fmt.Errorf("%s overlaps %s, which is %s", p, hp.prefix, hp.what)
			}
		}
		return nil
	}
	if requested != "" {
		p, err := ParsePrefix(requested)
		if err != nil {
			return netip.Prefix{}, err
		}
		p = p.Masked()
		return p, check(p)
	}
	for range 32 {
		var raw [16]byte
		if _, err := rand.Read(raw[1:8]); err != nil {
			return netip.Prefix{}, err
		}
		raw[0] = 0xfd
		p := netip.PrefixFrom(netip.AddrFrom16(raw), 64)
		if check(p) == nil {
			return p, nil
		}
	}
	return netip.Prefix{}, fmt.Errorf("no non-overlapping unique-local IPv6 /64 could be allocated; choose a network explicitly")
}

func wgInterfacePrefixes(conf *wgConf) (v4, v6 netip.Prefix) {
	for _, raw := range conf.iface().list("address") {
		p, err := ParsePrefix(raw)
		if err != nil {
			continue
		}
		if p.Addr().Is4() && !v4.IsValid() {
			v4 = p
		} else if p.Addr().Is6() && !v6.IsValid() {
			v6 = p
		}
	}
	return
}

// The query models a forwarded peer packet, including source policy rules.
// Reading only an IPv4 default card can select a different IPv6 uplink.
func wgExitUplink(ctx context.Context, host wgHostState, iface string, subnet netip.Prefix) (string, error) {
	family, target := "inet", "1.1.1.1"
	args := []string{"-j"}
	if subnet.Addr().Is6() {
		family, target = "inet6", "2606:4700:4700::1111"
		args = append(args, "-6")
	}
	args = append(args, "route", "get", target, "from", wgFirstHost(subnet).Next().String(), "iif", iface)
	out, err := run(ctx, "ip", args...)
	if err != nil {
		return "", fmt.Errorf("%s peer-source egress lookup failed: %w", family, err)
	}
	var routes []wgIPRouteJSON
	if json.Unmarshal([]byte(out), &routes) != nil || len(routes) != 1 || routes[0].Dev == "" ||
		(routes[0].Type != "" && routes[0].Type != "unicast") {
		return "", fmt.Errorf("%s peer-source egress lookup did not return one usable route", family)
	}
	dev := routes[0].Dev
	if dev == iface || !host.hasDevice(dev) || wgTunnelKind(host.kinds[dev]) {
		return "", fmt.Errorf("%s peer-source route uses %s; an exit needs a native uplink outside the tunnel", family, dev)
	}
	usable := false
	for _, p := range host.addrs[dev] {
		a := p.Addr()
		if a.Is4() == subnet.Addr().Is4() && a.IsGlobalUnicast() && !a.IsLoopback() &&
			(family == "inet" || !a.IsPrivate()) {
			usable = true
		}
	}
	if !usable {
		return "", fmt.Errorf("%s egress through %s has no usable %s address", family, dev, family)
	}
	return dev, nil
}

func wgTunnelKind(kind string) bool {
	switch kind {
	case "wireguard", "tun", "gre", "gretap", "ip6gre", "ip6gretap", "ipip", "ip6tnl", "sit", "vxlan", "geneve":
		return true
	}
	return false
}

// wgExitRules reads exact source/interface/masquerade and mark evidence. A
// counter or a comment alone is not proof that the current rule translates it.
func wgExitRules(ctx context.Context, n NATSpec) error {
	out, err := run(ctx, "nft", "-j", "list", "table", "inet", gatewayTable)
	if err != nil {
		return fmt.Errorf("reading owned exit rules: %w", err)
	}
	return wgCheckExitRules(out, n)
}

func wgCheckExitRules(out string, n NATSpec) error {
	var listing struct {
		Nftables []struct {
			Chain *struct {
				Family   string `json:"family"`
				Table    string `json:"table"`
				Name     string `json:"name"`
				Type     string `json:"type"`
				Hook     string `json:"hook"`
				Priority int    `json:"prio"`
				Policy   string `json:"policy"`
			} `json:"chain"`
			Rule *struct {
				Family  string                       `json:"family"`
				Table   string                       `json:"table"`
				Chain   string                       `json:"chain"`
				Comment string                       `json:"comment"`
				Expr    []map[string]json.RawMessage `json:"expr"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if json.Unmarshal([]byte(out), &listing) != nil || listing.Nftables == nil {
		return fmt.Errorf("owned exit rules are unreadable")
	}
	nat, mark, natHook, markHook := false, false, false, false
	for _, object := range listing.Nftables {
		if c := object.Chain; c != nil && c.Family == "inet" && c.Table == gatewayTable && c.Policy == "accept" {
			if c.Name == "nat_post" && c.Type == "nat" && c.Hook == "postrouting" && c.Priority == 90 {
				natHook = true
			}
			if c.Name == "forward" && c.Type == "filter" && c.Hook == "forward" && c.Priority == -10 {
				markHook = true
			}
		}
		r := object.Rule
		if r == nil || r.Family != "inet" || r.Table != gatewayTable {
			continue
		}
		if r.Comment == "nat:"+strconv.Itoa(n.ID) && r.Chain == "nat_post" {
			nat = wgRuleSelectors(r.Expr, n, false) && wgExpression(r.Expr, "masquerade")
		}
		if r.Comment == "nat-mark:"+strconv.Itoa(n.ID) && r.Chain == "forward" {
			mark = wgRuleSelectors(r.Expr, n, true) && wgOwnedMark(r.Expr)
		}
	}
	if !nat || !mark || !natHook || !markHook {
		return fmt.Errorf("owned %s exit lacks matching source/interface masquerade or connection-mark rules", n.Source)
	}
	return nil
}

// wgExitCounter reads the packet counter of an exit's owned masquerade rule.
func wgExitCounter(out string, n NATSpec) (uint64, bool) {
	var listing struct {
		Nftables []struct {
			Rule *struct {
				Table   string                       `json:"table"`
				Chain   string                       `json:"chain"`
				Comment string                       `json:"comment"`
				Expr    []map[string]json.RawMessage `json:"expr"`
			} `json:"rule"`
		} `json:"nftables"`
	}
	if json.Unmarshal([]byte(out), &listing) != nil {
		return 0, false
	}
	for _, object := range listing.Nftables {
		r := object.Rule
		if r == nil || r.Table != gatewayTable || r.Chain != "nat_post" || r.Comment != "nat:"+strconv.Itoa(n.ID) {
			continue
		}
		for _, e := range r.Expr {
			var c struct {
				Packets *uint64 `json:"packets"`
			}
			if raw, ok := e["counter"]; ok && json.Unmarshal(raw, &c) == nil && c.Packets != nil {
				return *c.Packets, true
			}
		}
	}
	return 0, false
}

func wgExpression(expr []map[string]json.RawMessage, kind string) bool {
	for _, e := range expr {
		if _, ok := e[kind]; ok {
			return true
		}
	}
	return false
}

func wgRuleSelectors(expr []map[string]json.RawMessage, n NATSpec, marked bool) bool {
	source, device, state := false, false, false
	operations := 0
	p, _ := ParsePrefix(n.Source)
	protocol := "ip"
	if p.Addr().Is6() {
		protocol = "ip6"
	}
	for _, e := range expr {
		for kind := range e {
			if kind != "match" && kind != "counter" && !(kind == "masquerade" && !marked) && !(kind == "mangle" && marked) {
				return false
			}
			if kind == "masquerade" || kind == "mangle" {
				operations++
			}
		}
		if _, ok := e["match"]; !ok {
			continue
		}
		var match struct {
			Op   string `json:"op"`
			Left struct {
				Payload *struct{ Protocol, Field string } `json:"payload"`
				Meta    *struct{ Key string }             `json:"meta"`
				CT      *struct{ Key string }             `json:"ct"`
			} `json:"left"`
			Right json.RawMessage `json:"right"`
		}
		if json.Unmarshal(e["match"], &match) != nil ||
			(match.Op != "==" && !(marked && match.Op == "in" && match.Left.CT != nil && match.Left.CT.Key == "state")) {
			return false
		}
		known := false
		if match.Left.Meta != nil && match.Left.Meta.Key == "oifname" {
			known = true
			var got string
			if device || json.Unmarshal(match.Right, &got) != nil || got != n.Interface {
				return false
			}
			device = true
		}
		if match.Left.Payload != nil && match.Left.Payload.Protocol == protocol && match.Left.Payload.Field == "saddr" {
			known = true
			var got struct {
				Prefix struct {
					Addr string `json:"addr"`
					Len  int    `json:"len"`
				} `json:"prefix"`
			}
			if source || json.Unmarshal(match.Right, &got) != nil {
				return false
			}
			a, err := netip.ParseAddr(got.Prefix.Addr)
			if err != nil || netip.PrefixFrom(a, got.Prefix.Len).Masked() != p.Masked() {
				return false
			}
			source = true
		}
		if marked && match.Left.CT != nil && match.Left.CT.Key == "state" {
			known = true
			var got string
			if state || json.Unmarshal(match.Right, &got) != nil || got != "new" {
				return false
			}
			state = true
		}
		if !known {
			return false
		}
	}
	return source && device && operations == 1 && (!marked || state)
}

func wgCheckedDump(out string) (map[string]*wgLiveIface, error) {
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) != 5 && len(fields) != 9 {
			return nil, fmt.Errorf("native WireGuard peer inventory is unreadable")
		}
		if err := validWGName(fields[0]); err != nil {
			return nil, fmt.Errorf("native WireGuard interface inventory is unreadable")
		}
		if len(fields) == 5 {
			if seen[fields[0]] {
				return nil, fmt.Errorf("native WireGuard interface inventory is duplicated")
			}
			seen[fields[0]] = true
		}
		if len(fields) == 9 {
			if !seen[fields[0]] {
				return nil, fmt.Errorf("native WireGuard peer inventory lacks its interface")
			}
			for _, raw := range strings.Split(fields[4], ",") {
				if raw == "(none)" {
					continue
				}
				if _, err := ParsePrefix(raw); err != nil {
					return nil, fmt.Errorf("native WireGuard peer routes are unreadable")
				}
			}
		}
	}
	return parseWGDump(out), nil
}

// Endpoints, handshakes and counters change as authenticated peers roam. Peer
// membership, routes, PSKs and keepalives are persistent input to whole-file
// syncconf; drift there must be reviewed rather than silently overwritten.
func wgPeerDrift(iface string, conf *wgConf, out string, inventory map[string]*wgLiveIface) error {
	if inventory[iface] == nil {
		return nil
	}
	normalIPs := func(raw []string) string {
		seen := map[string]bool{}
		var values []string
		for _, ip := range raw {
			if p, err := ParsePrefix(ip); err == nil && !seen[p.Masked().String()] {
				seen[p.Masked().String()] = true
				values = append(values, p.Masked().String())
			}
		}
		slices.Sort(values)
		return strings.Join(values, ",")
	}
	type persistentPeer struct {
		ips, psk  string
		keepalive int
	}
	saved := map[string]persistentPeer{}
	for _, sec := range conf.peers() {
		key := sec.get("publickey")
		keepalive, _ := strconv.Atoi(sec.get("persistentkeepalive"))
		if key == "" {
			return &ReadOnlyError{Reason: iface + " has unreadable saved peer ownership; review and synchronize it through its native owner before retrying."}
		}
		if _, duplicate := saved[key]; duplicate {
			return &ReadOnlyError{Reason: iface + " has duplicate saved peers; review and synchronize it through its native owner before retrying."}
		}
		saved[key] = persistentPeer{normalIPs(sec.list("allowedips")), wgNoneIsEmpty(sec.get("presharedkey")), keepalive}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 9 || fields[0] != iface {
			continue
		}
		keepalive, _ := strconv.Atoi(fields[8])
		actual := persistentPeer{normalIPs(wgSplitList(wgNoneIsEmpty(fields[4]))), wgNoneIsEmpty(fields[2]), keepalive}
		want, ok := saved[fields[1]]
		if !ok || seen[fields[1]] || actual != want {
			return &ReadOnlyError{Reason: iface + " native peer state differs from its saved configuration; review and synchronize it through its native owner before retrying. No peer is adopted or replaced."}
		}
		seen[fields[1]] = true
	}
	if len(seen) != len(saved) {
		return &ReadOnlyError{Reason: iface + " is missing a saved peer in native state; review and synchronize it through its native owner before retrying. No peer is adopted or replaced."}
	}
	return nil
}

func wgRefusePeerDrift(ctx context.Context, iface string, conf *wgConf) error {
	out, err := run(ctx, "wg", "show", "all", "dump")
	if err != nil {
		return fmt.Errorf("reading native peer ownership: %w", err)
	}
	inventory, err := wgCheckedDump(out)
	if err != nil {
		return err
	}
	return wgPeerDrift(iface, conf, out, inventory)
}

func wgOwnedMark(expr []map[string]json.RawMessage) bool {
	for _, e := range expr {
		var m struct {
			Key struct {
				CT *struct{ Key string } `json:"ct"`
			} `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(e["mangle"], &m) != nil || m.Key.CT == nil || m.Key.CT.Key != "mark" {
			continue
		}
		// nft normalizes a & 0x00ffffff | 0x4a000000 to a & 0x4affffff
		// | 0x4a000000. Both preserve lower bits and set exactly our top byte.
		var or struct {
			Args []json.RawMessage `json:"|"`
		}
		if json.Unmarshal(m.Value, &or) != nil || len(or.Args) != 2 {
			continue
		}
		var top uint64
		var and struct {
			Args []json.RawMessage `json:"&"`
		}
		if json.Unmarshal(or.Args[1], &top) != nil || top != 0x4a000000 || json.Unmarshal(or.Args[0], &and) != nil || len(and.Args) != 2 {
			continue
		}
		var ct struct {
			CT *struct{ Key string } `json:"ct"`
		}
		var mask uint64
		if json.Unmarshal(and.Args[0], &ct) == nil && ct.CT != nil && ct.CT.Key == "mark" &&
			json.Unmarshal(and.Args[1], &mask) == nil && (mask == 0x00ffffff || mask == 0x4affffff) {
			return true
		}
	}
	return false
}

func wgIPv6Enabled(conf *wgConf) bool { return conf.iface().bodyMeta()["ipv6"] == "ula64" }

func wgFamilyName(p netip.Prefix) string {
	if p.Addr().Is6() {
		return "IPv6"
	}
	return "IPv4"
}

func wgExitSource(n NATSpec, ipv6 bool) bool {
	p, err := ParsePrefix(n.Source)
	return err == nil && p.Addr().Is6() == ipv6
}

func wgFamilyForwarding(ipv6 bool) bool {
	if ipv6 {
		return wgIPForwarding("ipv6")
	}
	return wgIPForwarding("ipv4")
}

func wgExitEnabled(sp *Spec, iface string, ipv6 bool) bool {
	for _, n := range sp.NAT {
		if n.Owner == wgOwner(iface) && n.Enabled && wgExitSource(n, ipv6) {
			return true
		}
	}
	return false
}

func wgIPv6Containment(dual bool) string {
	if dual {
		return "Both IPv4 and IPv6 default routes use this tunnel while it is up. Local routes can remain native; this configuration is not a client kill switch."
	}
	return "This full tunnel carries IPv4. IPv6 is blocked inside the tunnel to prevent a native IPv6 leak; IPv6 internet access needs server-side dual-stack configuration."
}

func wgJoinAddresses(v4, v6 netip.Addr) string {
	addresses := []string{netip.PrefixFrom(v4, v4.BitLen()).String()}
	if v6.IsValid() {
		addresses = append(addresses, netip.PrefixFrom(v6, 128).String())
	}
	return strings.Join(addresses, ", ")
}

func (s *Service) wgFamilyEvidence(ctx context.Context, ifc *WGInterface, conf *wgConf, host wgHostState, hostErr error, sp *Spec, cap Capability, admission AdmissionState) {
	ifc.EndpointReachability = "not_tested"
	var v4, v6 netip.Prefix
	if conf != nil && conf.iface() != nil {
		v4, v6 = wgInterfacePrefixes(conf)
	}
	for i, p := range []netip.Prefix{v4, v6} {
		state := &ifc.Families.IPv4
		family := "inet"
		if i == 1 {
			state, family = &ifc.Families.IPv6, "inet6"
		}
		state.Configured, state.Runtime = p.IsValid(), "not_configured"
		state.Exit.Capability = cap
		state.Exit.Runtime = "disabled"
		if p.IsValid() {
			state.Subnet = p.Masked().String()
			switch {
			case hostErr != nil:
				state.Runtime, state.Reason = "unreadable", hostErr.Error()
			case !ifc.Up:
				state.Runtime = "down"
			default:
				state.Runtime = "missing"
				for _, actual := range host.addrs[ifc.Name] {
					if actual == p {
						state.Runtime = "present"
					}
				}
			}
		}
		if !p.IsValid() || (i == 1 && !ifc.IPv6Enabled) {
			state.Exit.Capability.Writable = false
			state.Exit.Capability.Reason = "No opted-in address allocation exists for this family."
		} else if !wgFamilyForwarding(i == 1) {
			state.Exit.Capability.Writable = false
			state.Exit.Capability.Reason = "This family's forwarding is off or unreadable."
		} else if i == 1 && !wgFamilyForwarding(false) {
			state.Exit.Capability.Writable = false
			state.Exit.Capability.Reason = "IPv6 exit accompanies IPv4 exit, whose forwarding is off or unreadable."
		} else if hostErr != nil {
			state.Exit.Capability.Writable = false
			state.Exit.Capability.Reason = hostErr.Error()
		} else if _, err := wgExitUplink(ctx, host, ifc.Name, p.Masked()); err != nil {
			state.Exit.Capability.Writable = false
			state.Exit.Capability.Reason = err.Error()
		}
		if sp == nil {
			state.Exit.Runtime, state.Exit.Reason = "unknown", "Saved gateway intent could not be read."
			continue
		}
		for _, n := range sp.NAT {
			if n.Owner != wgOwner(ifc.Name) || !n.Enabled || !wgExitSource(n, i == 1) {
				continue
			}
			state.Exit.Configured, state.Exit.Interface = true, n.Interface
			state.Exit.Runtime = "verified"
			switch {
			case state.Runtime == "unreadable":
				state.Exit.Runtime, state.Exit.Reason = "unknown", state.Reason
			case state.Runtime != "present":
				state.Exit.Runtime, state.Exit.Reason = "degraded", "The configured tunnel address is not present on a running interface."
			case n.Source != p.Masked().String():
				state.Exit.Runtime, state.Exit.Reason = "degraded", "The saved exit source differs from this family's tunnel network."
			case !state.Exit.Capability.Writable:
				state.Exit.Runtime, state.Exit.Reason = "degraded", state.Exit.Capability.Reason
			default:
				out, err := run(ctx, "nft", "-j", "list", "table", "inet", gatewayTable)
				if err != nil {
					err = fmt.Errorf("reading owned exit rules: %w", err)
				} else {
					err = wgCheckExitRules(out, n)
				}
				if err != nil {
					state.Exit.Runtime, state.Exit.Reason = "unknown", err.Error()
				} else if count, ok := wgExitCounter(out, n); ok {
					state.Exit.Translated = &count
				}
				for _, chain := range admission.Chains {
					if chain.Family == family && chain.Needed && chain.Status != "present" {
						state.Exit.Runtime, state.Exit.Reason = "degraded", chain.Reason
					}
				}
				dev, err := wgExitUplink(ctx, host, ifc.Name, p.Masked())
				if err != nil || dev != n.Interface {
					state.Exit.Runtime, state.Exit.Reason = "degraded", "The fresh peer-source route does not match the saved exit uplink."
				}
			}
		}
	}
}
