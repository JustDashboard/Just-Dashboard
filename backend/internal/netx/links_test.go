package netx

import (
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
