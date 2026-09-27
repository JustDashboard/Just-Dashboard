package netsec

import (
	"strings"
	"testing"

	gnet "github.com/shirou/gopsutil/v4/net"
)

func TestParseRoutes(t *testing.T) {
	out := strings.Join([]string{
		"default via 203.0.113.1 dev eth0 proto static metric 100",
		"10.0.0.0/8 dev tailscale0 scope link",
		"172.17.0.0/16 dev docker0 proto kernel scope link src 172.17.0.1",
	}, "\n")
	routes := parseRoutes(out, "ipv4")
	if len(routes) != 3 {
		t.Fatalf("got %d routes", len(routes))
	}
	if routes[0].Destination != "default" || routes[0].Gateway != "203.0.113.1" ||
		routes[0].Interface != "eth0" || routes[0].Metric != "100" {
		t.Fatalf("default route = %+v", routes[0])
	}
	if routes[2].Source != "172.17.0.1" {
		t.Errorf("src not read: %+v", routes[2])
	}
	if routes[1].Family != "ipv4" {
		t.Errorf("family = %q", routes[1].Family)
	}
}

// A host running Docker has a dozen veth pairs. A list that does not separate
// them from eth0 is unreadable, so the classification is what makes the panel
// usable rather than decoration.
func TestClassifyInterface(t *testing.T) {
	cases := map[string]string{
		"lo": "loopback", "eth0": "physical", "ens3": "physical",
		"tailscale0": "tunnel", "wg0": "tunnel", "tun0": "tunnel",
		"docker0": "bridge", "br-1a2b3c": "bridge",
		"veth9f2a": "virtual",
	}
	for name, want := range cases {
		if got := classifyInterface(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// The negative space is what matters: an address in any of these ranges can
// never be reached from the internet, so a public DNS record pointing at one
// is a misconfiguration rather than a match.
func TestIsGloballyRoutable(t *testing.T) {
	for _, private := range []string{
		"127.0.0.1/8", "10.1.2.3/8", "192.168.1.5/24", "172.16.0.9/12",
		"169.254.1.1/16", "100.101.102.103/10", "fd00::1/8",
	} {
		if isGloballyRoutable(private) {
			t.Errorf("%s reported as internet-routable", private)
		}
	}
	for _, public := range []string{"203.0.113.9/24", "2001:db8::1/32"} {
		if !isGloballyRoutable(public) {
			t.Errorf("%s reported as private", public)
		}
	}
}

// This host's main routing tables: IPv4's default leaves by ens3, and so does
// IPv6's, beside the unreachable ::/0 defaults the kernel keeps on lo.
func TestDefaultRouteInterfacesReadsThisHostsTables(t *testing.T) {
	route4 := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"ens3\t00000000\t01158339\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"docker0\t0000000A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
		"br-b17d23d019f2\t0001000A\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"
	route6 := "00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200       lo\n" +
		"00000000000000000000000000000000 00 00000000000000000000000000000000 00 200141d0200501000000000000000000 00000400 00000009 00000000 00000003     ens3\n" +
		"fe800000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001  docker0\n"
	got := defaultRouteInterfaces([]byte(route4), []byte(route6))
	if len(got) != 1 || !got["ens3"] {
		t.Errorf("default-route interfaces = %v, want only ens3", got)
	}
	// A host without IPv6 has no ipv6_route, and the IPv4 answer stands.
	if got := defaultRouteInterfaces([]byte(route4), nil); !got["ens3"] {
		t.Errorf("without IPv6 = %v, want ens3", got)
	}
}

func TestHostNetworkPlacesEachAddressOnItsInterface(t *testing.T) {
	n := hostNetworkFrom(gnet.InterfaceStatList{
		{Name: "lo", Flags: []string{"up", "loopback"}, Addrs: gnet.InterfaceAddrList{{Addr: "127.0.0.1/8"}, {Addr: "::1/128"}}},
		{Name: "ens3", Flags: []string{"up"}, Addrs: gnet.InterfaceAddrList{{Addr: "57.131.21.87/32"}, {Addr: "fe80::f816:3eff:fee3:1a48/64"}}},
		{Name: "docker0", Flags: []string{"up"}, Addrs: gnet.InterfaceAddrList{{Addr: "10.0.0.1/24"}}},
		{Name: "tailscale0", Flags: []string{"up"}, Addrs: gnet.InterfaceAddrList{{Addr: "100.110.34.31/32"}}},
	}, map[string]bool{"ens3": true})
	want := map[string]HostAddress{
		"127.0.0.1":                 {Interface: "lo", Kind: "loopback"},
		"fe80::f816:3eff:fee3:1a48": {Interface: "ens3", Kind: "physical", DefaultRoute: true},
		"10.0.0.1":                  {Interface: "docker0", Kind: "bridge"},
		"100.110.34.31":             {Interface: "tailscale0", Kind: "tunnel"},
	}
	if len(n.Addresses) != 6 {
		t.Fatalf("got %d addresses, want 6: %+v", len(n.Addresses), n.Addresses)
	}
	for address, w := range want {
		a, ok := n.interfaceOf(mustIP(address))
		if !ok || a.Interface != w.Interface || a.Kind != w.Kind || a.DefaultRoute != w.DefaultRoute {
			t.Errorf("%s = %+v (found %v), want %+v", address, a, ok, w)
		}
	}
}
