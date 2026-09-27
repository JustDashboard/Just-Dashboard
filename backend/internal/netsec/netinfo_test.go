package netsec

import (
	"strings"
	"testing"
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
		if got := ClassifyInterface(name); got != want {
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
