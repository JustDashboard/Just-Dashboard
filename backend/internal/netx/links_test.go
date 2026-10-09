package netx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLinksReadsKindsTunnelsAndCounters(t *testing.T) {
	links, err := parseLinks(fixture(t, "ip-link.json"), fixture(t, "ip-addr.json"))
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Link{}
	for _, l := range links {
		by[l.Name] = l
	}
	if by["lo"].Kind != "loopback" || by["eth0"].Kind != "physical" || by["tailscale0"].Kind != "tun" {
		t.Fatalf("kinds: lo=%s eth0=%s tailscale0=%s", by["lo"].Kind, by["eth0"].Kind, by["tailscale0"].Kind)
	}
	if v := by["eth0.100"]; v.Kind != "vlan" || v.VLANID != 100 || v.Parent != "eth0" || v.Master != "jd-lan" {
		t.Fatalf("vlan read as %+v", v)
	}
	if v := by["vx42"]; v.VNI != 42 || v.Remote != "198.51.100.7" || v.Local != "203.0.113.20" || v.Port != 4789 || v.XDP != "xdp_filter" {
		t.Fatalf("vxlan read as %+v", v)
	}
	if by["vx42"].AdminUp {
		t.Fatal("a device without the UP flag read as administratively up")
	}
	if got := by["jd-lan"].Members; len(got) != 1 || got[0] != "eth0.100" {
		t.Fatalf("bridge members = %v", got)
	}
	if c := by["eth0"].Counters; c.RxBytes != 5000000 || c.RxErrors != 2 || c.RxDropped != 5 {
		t.Fatalf("counters = %+v", c)
	}
	if a := by["eth0"].Addresses; len(a) != 3 || !a[0].Public || !a[0].Dynamic || a[0].CIDR != "203.0.113.20/24" {
		t.Fatalf("eth0 addresses = %+v", a)
	}
	if by["tailscale0"].Addresses[0].Public {
		t.Fatal("a tailnet address read as public")
	}
}

func TestAnnotateNamesOwnersRolesAndGuards(t *testing.T) {
	links, err := parseLinks(fixture(t, "ip-link.json"), fixture(t, "ip-addr.json"))
	if err != nil {
		t.Fatal(err)
	}
	spec := emptySpec()
	spec.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge"}}
	annotate(links, annotation{
		uplinks: map[string]bool{"eth0": true},
		path:    Path{Address: "100.110.34.9", Device: "tailscale0", Source: "100.110.34.31"},
		spec:    spec,
		inv:     Inventory{Networks: []DockerNet{{ID: "0123456789abcdef", Name: "web", Bridge: "br-0123456789ab"}}},
		veths:   map[int]ContainerNet{66193: {Name: "postgres", Image: "postgres:17"}},
		speed:   func(string) int { return 1000 },
		rates:   map[string]Rate{"eth0": {Rx: 1200, Tx: 300}},
	})
	by := map[string]Link{}
	for _, l := range links {
		by[l.Name] = l
	}
	if links[0].Name != "eth0" {
		t.Fatalf("the uplink should sort first, got %s", links[0].Name)
	}
	cases := []struct{ name, role, owner string }{
		{"eth0", "uplink", "system"},
		{"tailscale0", "tunnel", "tailscale"},
		{"wg0", "tunnel", "wireguard"},
		{"br-0123456789ab", "bridge", "docker"},
		{"veth6e4f828", "container", "docker"},
		{"jd-lan", "bridge", "just-dashboard"},
		{"eth0.100", "vlan", "system"},
		{"lo", "loopback", "kernel"},
		{"gre0", "tunnel", "kernel"},
	}
	for _, c := range cases {
		if got := by[c.name]; got.Role != c.role || got.Owner != c.owner {
			t.Errorf("%s: role %s owner %s, want %s %s", c.name, got.Role, got.Owner, c.role, c.owner)
		}
	}
	if by["br-0123456789ab"].DockerNetwork != "web" || by["veth6e4f828"].Container != "postgres" {
		t.Fatal("docker joins missing")
	}
	if !by["tailscale0"].ClientPath || by["tailscale0"].Guard == "" || by["eth0"].Guard == "" {
		t.Fatal("the client path and the uplink must both be guarded")
	}
	if by["jd-lan"].Guard != "" || !by["jd-lan"].Managed {
		t.Fatalf("a managed bridge carries no guard: %+v", by["jd-lan"])
	}
	if by["tailscale0"].Addresses[0].Guard == "" {
		t.Fatal("the address the browser is answered from must be guarded")
	}
	if by["eth0"].SpeedMbps != 1000 || by["eth0"].RxRate != 1200 {
		t.Fatal("speed and rate not joined")
	}
}

func TestAnnotateFlagsIncompleteDockerJoins(t *testing.T) {
	annotated := func(inv Inventory, veths map[int]ContainerNet) map[string]Link {
		links, err := parseLinks(fixture(t, "ip-link.json"), fixture(t, "ip-addr.json"))
		if err != nil {
			t.Fatal(err)
		}
		annotate(links, annotation{uplinks: map[string]bool{"eth0": true}, spec: emptySpec(), inv: inv, veths: veths})
		by := map[string]Link{}
		for _, l := range links {
			by[l.Name] = l
		}
		return by
	}
	web := []DockerNet{{ID: "0123456789abcdef", Name: "web", Bridge: "br-0123456789ab"}}

	joined := annotated(Inventory{Networks: web}, map[int]ContainerNet{66193: {Name: "postgres"}})
	if joined["veth6e4f828"].DockerJoin != "" || joined["br-0123456789ab"].DockerJoin != "" {
		t.Fatalf("a complete join carries no flag: %+v", joined["veth6e4f828"])
	}

	listFailed := annotated(Inventory{Networks: web, ContainersError: "Cannot connect to the Docker daemon"}, nil)
	if v := listFailed["veth6e4f828"]; v.DockerJoin != "unresolved" || !strings.Contains(v.DockerJoinReason, "container list could not be read") || !strings.Contains(v.DockerJoinReason, "Cannot connect") {
		t.Fatalf("a failed container list must flag the veth: %+v", v)
	}

	unjoined := annotated(Inventory{Networks: web, UnjoinedContainers: []string{"redis", "api"}}, nil)
	if v := unjoined["veth6e4f828"]; v.DockerJoin != "unresolved" || !strings.Contains(v.DockerJoinReason, "api, redis") {
		t.Fatalf("unreadable containers are named: %+v", v)
	}

	networksFailed := annotated(Inventory{DockerNetworksUnknown: true, DockerNetworksError: "context deadline exceeded"}, map[int]ContainerNet{66193: {Name: "postgres"}})
	if b := networksFailed["br-0123456789ab"]; b.DockerJoin != "unknown" || !strings.Contains(b.DockerJoinReason, "network list could not be read") {
		t.Fatalf("a Docker-named bridge without a network list is unknown: %+v", b)
	}
	if networksFailed["eth0"].DockerJoin != "" || networksFailed["wg0"].DockerJoin != "" {
		t.Fatal("only Docker's devices carry a join flag")
	}
}

func TestContainerVethsNameUnreadableContainers(t *testing.T) {
	root := t.TempDir()
	prev := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = prev })
	dir := filepath.Join(root, "41", "root", "sys", "class", "net", "eth0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "iflink"), []byte("66193\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	veths, unreadable := containerVeths([]ContainerNet{{Name: "postgres", PID: 41}, {Name: "gone", PID: 42}, {Name: "nopid"}})
	if veths[66193].Name != "postgres" || len(unreadable) != 1 || unreadable[0] != "gone" {
		t.Fatalf("veths=%v unreadable=%v", veths, unreadable)
	}
}

func TestParseLinksNamesWhereEachAddressCameFrom(t *testing.T) {
	addrs := `[{"ifname":"ens3","addr_info":[
{"family":"inet","local":"203.0.113.20","prefixlen":24,"scope":"global","dynamic":true,"valid_life_time":83020,"preferred_life_time":83020},
{"family":"inet6","local":"2001:db8::13","prefixlen":128,"scope":"global","valid_life_time":4294967295,"preferred_life_time":4294967295},
{"family":"inet6","local":"2001:db8:1::f816:3eff:fe00:1","prefixlen":64,"scope":"global","dynamic":true,"mngtmpaddr":true,"valid_life_time":86400,"preferred_life_time":14400},
{"family":"inet6","local":"2001:db8:1::a1b2","prefixlen":64,"scope":"global","dynamic":true,"temporary":true,"valid_life_time":600,"preferred_life_time":300},
{"family":"inet6","local":"2001:db8:2::5","prefixlen":128,"scope":"global","dynamic":true,"valid_life_time":7200,"preferred_life_time":3600},
{"family":"inet6","local":"fe80::1","prefixlen":64,"scope":"link","protocol":"kernel_ll","valid_life_time":4294967295,"preferred_life_time":4294967295}]}]`
	links, err := parseLinks(`[{"ifindex":2,"ifname":"ens3","flags":["UP"],"mtu":1500,"operstate":"UP"}]`, addrs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dhcp", "static", "slaac", "temporary", "dynamic", "link-local"}
	for i, a := range links[0].Addresses {
		if a.Origin != want[i] {
			t.Errorf("%s: origin %s, want %s", a.CIDR, a.Origin, want[i])
		}
	}
	if v := links[0].Addresses[0].ValidSeconds; v == nil || *v != 83020 {
		t.Fatalf("a lease's lifetime is kept: %v", v)
	}
	if links[0].Addresses[1].ValidSeconds != nil {
		t.Fatal("a forever lifetime reads as none")
	}
}
