package netx

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

type nativeVariant struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func nativeNMDNS(ctx context.Context, p *nativeProfile, family int) ([]string, []string, error) {
	property, iface := "Ip4Config", nmService+".IP4Config"
	if family == 1 {
		property, iface = "Ip6Config", nmService+".IP6Config"
	}
	object, err := nativeBusProperty[string](ctx, nmService, p.DeviceObject, nmService+".Device", property, "o")
	if err != nil {
		return nil, nil, err
	}
	if object == "/" {
		return []string{}, []string{}, nil
	}
	var dns []string
	if family == 0 {
		servers, err := nativeBusProperty[[]map[string]nativeVariant](ctx, nmService, object, iface, "NameserverData", "aa{sv}")
		if err != nil {
			return nil, nil, err
		}
		for _, server := range servers {
			var addr string
			if server["address"].Type != "s" || json.Unmarshal(server["address"].Data, &addr) != nil {
				return nil, nil, errors.New("unreadable native DNS evidence")
			}
			dns = append(dns, addr)
		}
	} else {
		servers, err := nativeBusProperty[[][]byte](ctx, nmService, object, iface, "Nameservers", "aay")
		if err != nil {
			return nil, nil, err
		}
		for _, raw := range servers {
			if len(raw) != 16 {
				return nil, nil, errors.New("unreadable native IPv6 DNS evidence")
			}
			var bytes [16]byte
			copy(bytes[:], raw)
			dns = append(dns, netip.AddrFrom16(bytes).String())
		}
	}
	domains, err := nativeBusProperty[[]string](ctx, nmService, object, iface, "Searches", "as")
	if err != nil {
		return nil, nil, err
	}
	return dns, domains, nil
}

func nativeNetworkdDNS(ctx context.Context, p *nativeProfile) ([]string, []string, error) {
	out, err := nativeExecute(ctx, nil, "networkctl", "--no-pager", "--json=short", "status", p.View.Device)
	if err != nil {
		return nil, nil, err
	}
	var state struct {
		Name string `json:"Name"`
		DNS  []struct {
			Family  int    `json:"Family"`
			Address []byte `json:"Address"`
		} `json:"DNS"`
		Search []struct {
			Domain string `json:"Domain"`
		} `json:"SearchDomains"`
		Routes []struct {
			Domain string `json:"Domain"`
		} `json:"RouteDomains"`
	}
	if json.Unmarshal([]byte(out), &state) != nil || state.Name != p.View.Device {
		return nil, nil, errors.New("unreadable networkd DNS evidence")
	}
	dns, domains := []string{}, []string{}
	for _, server := range state.DNS {
		switch {
		case server.Family == 2 && len(server.Address) == 4:
			var bytes [4]byte
			copy(bytes[:], server.Address)
			dns = append(dns, netip.AddrFrom4(bytes).String())
		case server.Family == 10 && len(server.Address) == 16:
			var bytes [16]byte
			copy(bytes[:], server.Address)
			dns = append(dns, netip.AddrFrom16(bytes).String())
		default:
			return nil, nil, errors.New("unreadable networkd DNS address")
		}
	}
	for _, domain := range state.Search {
		domains = append(domains, domain.Domain)
	}
	for _, domain := range state.Routes {
		domains = append(domains, "~"+domain.Domain)
	}
	return dns, domains, nil
}

func readNativeRuntime(ctx context.Context, p *nativeProfile, in NativeIntent) NativeEvidence {
	unknown := func(reason string) NativeEvidence { return NativeEvidence{Status: "unknown", Reason: reason} }
	drift := func(reason string) NativeEvidence { return NativeEvidence{Status: "drift", Reason: reason} }
	links, err := nativeLinks(ctx)
	if err != nil {
		return unknown("Native source/relationship evidence could not be read.")
	}
	found := false
	for _, link := range links {
		if link.IfName == p.View.Device {
			found = true
			if link.IfIndex != p.Device.IfIndex || link.Address != p.Device.Address || nativeLinkKind(link) != p.View.Kind || !nativeContractsEqual(nativeContract(link, links), p.View.Contract) {
				return drift("Native source identity or bond/VRF relationships changed.")
			}
			peer, err := nativePeer(link, links)
			if err != nil || !nativePeersEqual(peer, p.Peer) {
				return drift("The native veth reciprocal peer identity changed.")
			}
			break
		}
	}
	if !found {
		return drift("The selected native device is absent.")
	}
	out, err := nativeExecute(ctx, nil, "ip", "-j", "addr", "show", "dev", p.View.Device)
	if err != nil {
		return unknown("Kernel addresses could not be read.")
	}
	var addresses []ipAddr
	if json.Unmarshal([]byte(out), &addresses) != nil || len(addresses) != 1 || addresses[0].IfName != p.View.Device {
		return unknown("Kernel address evidence is unreadable.")
	}
	var allDNS, allDomains []string
	if p.View.Renderer == "networkd" {
		allDNS, allDomains, err = nativeNetworkdDNS(ctx, p)
		if err != nil {
			return unknown("The native owner's per-link DNS/domain evidence could not be read.")
		}
	}
	for i, f := range []NativeFamilyIntent{in.IPv4, in.IPv6} {
		actual := []string{}
		dynamic := false
		for _, a := range addresses[0].AddrInfo {
			if a.Scope != "global" || (a.Family == "inet6") != (i == 1) {
				continue
			}
			addr, err := netip.ParseAddr(a.Local)
			if err != nil {
				return unknown("Native family address evidence is unreadable.")
			}
			actual = append(actual, netip.PrefixFrom(addr, a.PrefixLen).String())
			dynamic = dynamic || a.Dynamic
		}
		for _, required := range f.Addresses {
			if !slices.Contains(actual, required) {
				return drift("A configured native static address is missing from the kernel.")
			}
		}
		if f.Method == "disabled" && len(actual) > 0 {
			return drift("A disabled native family still has global addresses.")
		}
		if f.Method == "manual" && len(actual) != len(f.Addresses) {
			return drift("The native manual family has foreign/additional global addresses; review ownership before editing.")
		}
		if slices.Contains([]string{"auto", "dhcp", "slaac"}, f.Method) && !dynamic {
			return NativeEvidence{Status: "acquiring", Reason: "Automatic addressing is configured, but a current DHCP/SLAAC address has not been observed."}
		}
		flag := "-4"
		if i == 1 {
			flag = "-6"
		}
		out, err := nativeExecute(ctx, nil, "ip", "-j", flag, "route", "show", "table", "all", "dev", p.View.Device)
		if err != nil {
			return unknown("Native family routes could not be read.")
		}
		var routes []ipRoute
		if json.Unmarshal([]byte(out), &routes) != nil || routes == nil {
			return unknown("Native family route evidence is unreadable.")
		}
		for _, r := range routes {
			if f.IgnoreAutoRoutes && (r.Protocol == "dhcp" || r.Protocol == "ra") {
				return drift("The native owner retained a DHCP/RA route while automatic routes are disabled.")
			}
		}
		for _, required := range f.Routes {
			matched := false
			for _, r := range routes {
				dest := r.Dst
				if dest == "default" {
					dest = "0.0.0.0/0"
					if i == 1 {
						dest = "::/0"
					}
				}
				table := 254
				if r.Table != "" && r.Table != "main" {
					table, _ = strconv.Atoi(string(r.Table))
				}
				metric := required.Metric
				if i == 1 && metric == 0 {
					metric = 1024
				}
				if dest == required.Destination && r.Gateway == required.Gateway && table == required.Table && (metric == -1 || metric == r.Metric) && (r.Type == "" || r.Type == "unicast") {
					matched = true
					break
				}
			}
			if !matched {
				return drift("A configured native route's destination/gateway/explicit metric/table is missing or different.")
			}
		}
		dns, domains := allDNS, allDomains
		if p.View.Renderer == "NetworkManager" {
			dns, domains, err = nativeNMDNS(ctx, p, i)
			if err != nil {
				return unknown("The native owner's per-family DNS/domain evidence could not be read.")
			}
		}
		familyDNS := []string{}
		for _, raw := range dns {
			addr, err := netip.ParseAddr(raw)
			if err != nil {
				return unknown("The native DNS address evidence is unreadable.")
			}
			if addr.Is6() == (i == 1) {
				familyDNS = append(familyDNS, addr.String())
			}
		}
		for _, required := range f.DNS {
			if !slices.Contains(familyDNS, required) {
				return drift("A configured native DNS server is absent from its active owner.")
			}
		}
		if (f.IgnoreAutoDNS || f.Method == "manual" || f.Method == "disabled") && len(familyDNS) != len(f.DNS) {
			return drift("The native owner has extra DNS servers outside its configured automatic DNS policy.")
		}
		for _, required := range f.Domains {
			if !slices.Contains(domains, required) {
				return drift("A configured native search/route domain is absent from its active owner.")
			}
		}
	}
	return NativeEvidence{Status: "matching", Reason: "Kernel addresses/routes and the native owner's DNS/domains match supported intent; DHCP/RA acquisition was observed where required. Owner-default metrics remain owner-selected. No resolver/provider connectivity was probed."}
}

func nativeContractsEqual(a, b NativeContract) bool {
	return a.Master == b.Master && a.BondMode == b.BondMode && a.VRFTable == b.VRFTable && slices.Equal(a.Members, b.Members)
}

func nativeL3Contract(p *nativeProfile, in NativeIntent) error {
	if p.View.Contract.Master != "" {
		return &ReadOnlyError{Reason: "This device is a native port; edit the master's L3 profile instead of competing with its membership."}
	}
	if p.View.Kind == "vrf" {
		if p.View.Contract.VRFTable < 1 || p.View.Contract.VRFTable == 52 || p.View.Contract.VRFTable >= 253 && p.View.Contract.VRFTable <= 255 {
			return errors.New("this native VRF table is protected or unreadable")
		}
		for _, f := range []NativeFamilyIntent{in.IPv4, in.IPv6} {
			for _, route := range f.Routes {
				if route.Table != p.View.Contract.VRFTable {
					return errors.New("supported native VRF routes must stay in its observed table")
				}
			}
		}
	}
	if p.View.Owner != "NetworkManager" {
		for _, f := range []NativeFamilyIntent{in.IPv4, in.IPv6} {
			for _, r := range f.Routes {
				if r.Metric < 0 {
					return errors.New("this native owner needs an explicit nonnegative route metric")
				}
			}
		}
	}
	if p.View.Owner == "NetworkManager" || p.View.Renderer == "NetworkManager" {
		for _, f := range []NativeFamilyIntent{in.IPv4, in.IPv6} {
			for _, r := range f.Routes {
				if r.Metric == 0 {
					return errors.New("NetworkManager treats zero route metrics as owner defaults; choose -1 for owner default or a positive explicit metric")
				}
			}
		}
	}
	return nil
}

func nativeRetainsSource(before Path, device string, in NativeIntent) error {
	if before.Device != device || before.Local || before.Source == "" {
		return nil
	}
	family := in.IPv4
	if strings.Contains(before.Source, ":") {
		family = in.IPv6
	}
	if family.Method == "disabled" {
		return guarded("the native family answers your current connection and cannot be disabled")
	}
	if family.Method == "manual" {
		for _, raw := range family.Addresses {
			prefix, _ := netip.ParsePrefix(raw)
			if prefix.Addr().String() == before.Source {
				return nil
			}
		}
		return guarded("the native address answering your current connection must be retained")
	}
	return nil
}
