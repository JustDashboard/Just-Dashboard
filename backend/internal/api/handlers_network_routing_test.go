package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

func TestNetworkForwardingDockerDependenciesCountEachFamily(t *testing.T) {
	for _, test := range []struct {
		name     string
		networks []netx.DockerNet
		ipv4     int
		ipv6     int
	}{
		{name: "known empty"},
		{name: "IPv4 bridge", networks: []netx.DockerNet{{Driver: "bridge", Subnets: []string{"172.18.0.0/16"}}}, ipv4: 1},
		{name: "IPv6 enabled without IPAM", networks: []netx.DockerNet{{Driver: "bridge", IPv6: true}}, ipv4: 1, ipv6: 1},
		{name: "IPv6 subnet evidence", networks: []netx.DockerNet{{Driver: "bridge", Subnets: []string{"fd00:1::/64"}}}, ipv4: 1, ipv6: 1},
		{name: "dual stack counted once", networks: []netx.DockerNet{{Driver: "bridge", IPv6: true, Subnets: []string{"172.18.0.0/16", "fd00:1::/64", "fd00:2::/64"}}}, ipv4: 1, ipv6: 1},
		{name: "IPv4 mapped subnet", networks: []netx.DockerNet{{Driver: "bridge", Subnets: []string{"::ffff:172.18.0.0/112"}}}, ipv4: 1},
		{name: "unknown IPAM remains conservative", networks: []netx.DockerNet{{Driver: "bridge", Subnets: []string{"invalid"}}}, ipv4: 1},
		{name: "other drivers are not forwarded bridges", networks: []netx.DockerNet{{Driver: "host", IPv6: true}, {Driver: "overlay", IPv6: true}, {Driver: "macvlan", Subnets: []string{"fd00:1::/64"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			needs := dockerForwardingNeeds(netx.Inventory{Networks: test.networks})
			if needs.DockerNetworks != test.ipv4 || needs.DockerIPv6Networks != test.ipv6 || needs.DockerNetworksUnknown {
				t.Fatalf("needs = %+v, want IPv4 %d and IPv6 %d", needs, test.ipv4, test.ipv6)
			}
		})
	}
}

// The Engine fixture permits only inventory reads. Core forwarding tests
// prove that the dependencies projected here refuse a change before sysctl.
func networkInventoryEngine(t *testing.T, s *Server, networks string, fail bool) {
	t.Helper()
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/_ping" {
			w.Header().Set("API-Version", "1.47")
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("inventory mutated Docker: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_, _ = w.Write([]byte("[]"))
		case strings.HasSuffix(r.URL.Path, "/networks"):
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "fixture network inventory unavailable"})
				return
			}
			_, _ = w.Write([]byte(networks))
		default:
			t.Errorf("unexpected Docker inventory request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(engine.Close)
	s.modules.docker = dockerx.New(engine.URL)
	t.Cleanup(func() { _ = s.modules.docker.Close() })
}

func TestNetworkInventoryKeepsDockerIPv6ForwardingDependencies(t *testing.T) {
	s := &Server{}
	networkInventoryEngine(t, s, `[{"Id":"0123456789abcdef0123456789abcdef","Name":"dual-stack","Driver":"bridge","EnableIPv6":true,"IPAM":{"Config":[{"Subnet":"172.18.0.0/16"},{"Subnet":"fd00:1::/64"}]}}]`, false)
	inv := s.networkInventory(context.Background())
	if inv.DockerNetworksUnknown || len(inv.Networks) != 1 || !inv.Networks[0].IPv6 || len(inv.Networks[0].Subnets) != 2 {
		t.Fatalf("inventory = %+v", inv)
	}
	needs := dockerForwardingNeeds(inv)
	if needs.DockerNetworks != 1 || needs.DockerIPv6Networks != 1 {
		t.Fatalf("forwarding needs = %+v", needs)
	}
	view := netx.New(netx.Options{Paths: netx.Paths{Dir: t.TempDir()}}).Forwarding(context.Background(), needs)
	if len(view.IPv6.NeededBy) != 1 || view.IPv6.Guard == "" {
		t.Fatalf("an IPv6 Docker bridge has no forwarding guard: %+v", view.IPv6)
	}
}

func TestNetworkInventoryFailureGuardsBothForwardingFamilies(t *testing.T) {
	s := &Server{}
	networkInventoryEngine(t, s, "", true)
	inv := s.networkInventory(context.Background())
	if !inv.DockerNetworksUnknown || len(inv.Networks) != 0 {
		t.Fatalf("a failed Docker read looked like a known empty inventory: %+v", inv)
	}
	needs := dockerForwardingNeeds(inv)
	view := netx.New(netx.Options{Paths: netx.Paths{Dir: t.TempDir()}}).Forwarding(context.Background(), needs)
	for family, state := range map[string]netx.ForwardingState{"ipv4": view.IPv4, "ipv6": view.IPv6} {
		if len(state.NeededBy) != 1 || !strings.Contains(state.Guard, "dependencies are unknown") {
			t.Fatalf("%s has no unknown-inventory guard: %+v", family, state)
		}
	}
}

func TestNetworkInventoryWithoutDockerIsKnownAbsent(t *testing.T) {
	inv := (&Server{}).networkInventory(context.Background())
	needs := dockerForwardingNeeds(inv)
	if inv.DockerNetworksUnknown || needs.DockerNetworksUnknown || needs.DockerNetworks != 0 || needs.DockerIPv6Networks != 0 {
		t.Fatalf("a host without Docker is not known absent: inventory %+v, needs %+v", inv, needs)
	}
}
