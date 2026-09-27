package netsec

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	gnet "github.com/shirou/gopsutil/v4/net"
	"golang.org/x/sys/unix"
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
		"docker0": "bridge", "br-b05f8e098ad7": "bridge", "virbr0": "bridge",
		"lxdbr0": "bridge", "incusbr0": "bridge", "podman1": "bridge", "cni-podman0": "bridge",
		// Only Docker's own br-<network id> says whose bridge it is.
		"br-lan": "physical", "br0": "physical",
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
	}, map[string]bool{"ens3": true}, nil)
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

// A bridge is told by what the kernel says it is and what is enslaved to it,
// not by its name: LXD, Incus and Podman name theirs outside Docker's and
// libvirt's conventions, and a bridge holding a NIC, a bond or a VXLAN is the
// network behind it whatever it is called.
func TestClassifyLinksTellsAGuestBridgeFromOneThatReachesOut(t *testing.T) {
	kinds := classifyLinks([]link{
		{name: "lo", index: 1},
		{name: "ens3", index: 2},
		{name: "docker0", index: 3, kind: "bridge"},
		{name: "veth0de52ac", index: 4, master: 3, kind: "veth"},
		{name: "lxdbr0", index: 5, kind: "bridge"},
		{name: "veth1a2b3c4d", index: 6, master: 5, kind: "veth"},
		{name: "tapa1b2c3d4", index: 7, master: 5, kind: "tun"},
		{name: "incusbr0", index: 8, kind: "bridge"},
		{name: "podman1", index: 9, kind: "bridge"},
		{name: "veth0", index: 10, master: 9, kind: "veth"},
		{name: "br-lan", index: 11, kind: "bridge"},
		{name: "eth1", index: 12, master: 11},
		{name: "br0", index: 13, kind: "bridge"},
		{name: "bond0", index: 14, master: 13, kind: "bond"},
		{name: "eth2", index: 15, master: 14},
		{name: "br-overlay", index: 16, kind: "bridge"},
		{name: "vxlan100", index: 17, master: 16, kind: "vxlan"},
		{name: "virbr0", index: 18, kind: "bridge"},
		{name: "virbr0-nic", index: 19, master: 18, kind: "tun"},
		{name: "vnet0", index: 20, master: 18, kind: "tun"},
		{name: "br-b05f8e098ad7", index: 21, kind: "openvswitch"},
		{name: "tailscale0", index: 22, kind: "tun"},
		{name: "br-guests", index: 23, kind: "bridge"},
		{name: "dummy0", index: 24, master: 23, kind: "dummy"},
	})
	for name, want := range map[string]string{
		"lo": "loopback", "ens3": "physical", "tailscale0": "tunnel",
		"docker0": "bridge", "lxdbr0": "bridge", "incusbr0": "bridge", "podman1": "bridge",
		"virbr0": "bridge", "br-guests": "bridge",
		"br-lan": "physical", "br0": "physical", "br-overlay": "physical",
		"veth0de52ac": "virtual", "vnet0": "virtual", "virbr0-nic": "virtual",
		// Named like Docker's, but an Open vSwitch device whose ports this
		// table does not list.
		"br-b05f8e098ad7": "physical",
	} {
		if got := kinds.of(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	// A device the table did not list is classified by its name.
	if got := kinds.of("lxdbr1"); got != "bridge" {
		t.Errorf("unlisted lxdbr1 = %q, want bridge", got)
	}
}

// netlinkLink encodes one RTM_NEWLINK message as the kernel sends it: the
// header, an ifinfomsg, then the name, the master and the kind nested in
// IFLA_LINKINFO.
func netlinkLink(index, master uint32, name, kind string) []byte {
	attr := func(typ uint16, value []byte) []byte {
		b := make([]byte, syscall.SizeofRtAttr, syscall.SizeofRtAttr+len(value)+3)
		binary.NativeEndian.PutUint16(b[0:2], uint16(syscall.SizeofRtAttr+len(value)))
		binary.NativeEndian.PutUint16(b[2:4], typ)
		b = append(b, value...)
		for len(b)%syscall.RTA_ALIGNTO != 0 {
			b = append(b, 0)
		}
		return b
	}
	info := make([]byte, syscall.SizeofIfInfomsg)
	binary.NativeEndian.PutUint32(info[4:8], index)
	body := append(info, attr(syscall.IFLA_IFNAME, append([]byte(name), 0))...)
	if master != 0 {
		body = append(body, attr(syscall.IFLA_MASTER, binary.NativeEndian.AppendUint32(nil, master))...)
	}
	if kind != "" {
		// The kind follows the kind-specific data in some kernels' order,
		// so the reader has to walk the nest rather than read its head.
		nest := attr(2 /* IFLA_INFO_DATA */, []byte{1, 2, 3})
		nest = append(nest, attr(unix.IFLA_INFO_KIND, append([]byte(kind), 0))...)
		body = append(body, attr(syscall.IFLA_LINKINFO, nest)...)
	}
	header := make([]byte, syscall.NLMSG_HDRLEN)
	binary.NativeEndian.PutUint32(header[0:4], uint32(syscall.NLMSG_HDRLEN+len(body)))
	binary.NativeEndian.PutUint16(header[4:6], syscall.RTM_NEWLINK)
	return append(header, body...)
}

func TestParseLinksReadsTheKernelsLinkTable(t *testing.T) {
	var rib []byte
	rib = append(rib, netlinkLink(1, 0, "lo", "")...)
	rib = append(rib, netlinkLink(5, 0, "lxdbr0", "bridge")...)
	rib = append(rib, netlinkLink(6, 5, "veth1a2b3c4d", "veth")...)
	done := make([]byte, syscall.NLMSG_HDRLEN+4)
	binary.NativeEndian.PutUint32(done[0:4], uint32(len(done)))
	binary.NativeEndian.PutUint16(done[4:6], syscall.NLMSG_DONE)
	rib = append(rib, done...)

	got := parseLinks(rib)
	want := []link{
		{name: "lo", index: 1},
		{name: "lxdbr0", index: 5, kind: "bridge"},
		{name: "veth1a2b3c4d", index: 6, master: 5, kind: "veth"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d links, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The kernel's own table, read the way ReadHostNetwork reads it, agrees with
// sysfs about which devices are bridges and what is enslaved to them: the
// dump carries each device's kind and master, which the classification
// stands on.
func TestReadInterfaceKindsAgreesWithSysfs(t *testing.T) {
	kinds := readInterfaceKinds()
	if kinds == nil {
		t.Fatal("the kernel's link table could not be read")
	}
	if kinds["lo"] != "loopback" {
		t.Errorf("lo = %q, want loopback", kinds["lo"])
	}
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		t.Skipf("no sysfs: %v", err)
	}
	bridges := 0
	for _, e := range entries {
		dir := filepath.Join("/sys/class/net", e.Name())
		if _, err := os.Stat(filepath.Join(dir, "bridge")); err != nil {
			continue
		}
		bridges++
		ports, _ := filepath.Glob(filepath.Join(dir, "brif", "*"))
		want := "bridge"
		for _, port := range ports {
			// A port that is a NIC has a device behind it; a bond or a VLAN
			// has lower devices. Veths and taps have neither.
			pdir := filepath.Join("/sys/class/net", filepath.Base(port))
			lower, _ := filepath.Glob(filepath.Join(pdir, "lower_*"))
			if _, err := os.Stat(filepath.Join(pdir, "device")); err == nil || len(lower) > 0 {
				want = "physical"
			}
		}
		if got := kinds[e.Name()]; got != want {
			t.Errorf("%s (%d ports) = %q, want %q", e.Name(), len(ports), got, want)
		}
	}
	if bridges == 0 {
		t.Log("no bridges on this host; checked loopback only")
	}
}
