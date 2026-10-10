package dockerx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

func TestNetworkAddressPoolsRejectAmbiguousOrIncompatibleAllocations(t *testing.T) {
	for _, test := range []struct {
		name string
		spec NetworkSpec
	}{
		{"gateway without pool", NetworkSpec{Gateway: "192.0.2.1"}},
		{"noncanonical subnet", NetworkSpec{Subnet: "192.0.2.1/24"}},
		{"foreign gateway", NetworkSpec{Subnet: "192.0.2.0/24", Gateway: "198.51.100.1"}},
		{"foreign allocation", NetworkSpec{Subnet: "192.0.2.0/24", IPRange: "198.51.100.0/25"}},
		{"larger allocation", NetworkSpec{Subnet: "192.0.2.0/24", IPRange: "192.0.2.0/23"}},
		{"mapped address", NetworkSpec{Subnet: "::ffff:192.0.2.0/120", IPv6: true}},
		{"IPv6 disabled", NetworkSpec{Subnet: "fd00:db::/64"}},
		{"mixed dialects", NetworkSpec{Subnet: "192.0.2.0/24", IPAM: []NetworkIPAM{{Subnet: "198.51.100.0/24"}}}},
		{"overlapping pools", NetworkSpec{IPAM: []NetworkIPAM{{Subnet: "192.0.2.0/24"}, {Subnet: "192.0.2.128/25"}}}},
		{"system network", NetworkSpec{Driver: "host"}},
		{"oversized options", NetworkSpec{Options: map[string]string{"parent": strings.Repeat("x", 4097)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.spec.Name = "fixture"
			if _, err := NormalizeNetworkSpec(test.spec); err == nil {
				t.Fatal("unsafe or ambiguous pool accepted")
			}
		})
	}
	legacy, err := NormalizeNetworkSpec(NetworkSpec{Name: " old-client ", Subnet: " 192.0.2.0/24 ", Gateway: "192.0.2.1", IPRange: "192.0.2.128/25"})
	if err != nil || legacy.Name != "old-client" || legacy.Driver != "bridge" || len(legacy.IPAM) != 1 || legacy.IPAM[0].IPRange != "192.0.2.128/25" {
		t.Fatalf("legacy compatibility = %+v: %v", legacy, err)
	}
	if _, err := NormalizeNetworkSpec(legacy); err != nil {
		t.Fatalf("normalized spec cannot pass the owner boundary again: %v", err)
	}
}

func TestCreateNetworkCarriesBothFamiliesAndAdvancedFieldsToEngine(t *testing.T) {
	var created struct {
		Name string
		network.CreateOptions
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.URL.Path, "/v1.47") {
		case "/networks/create":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Errorf("decode request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"Id":"fixture","Warning":""}`))
		case "/networks/fixture":
			_, _ = w.Write([]byte(`{"Id":"fixture","Name":"dual","Driver":"bridge","EnableIPv6":true,"Attachable":true,"Internal":true,"IPAM":{"Config":[{"Subnet":"192.0.2.0/24"},{"Subnet":"fd00:db::/64"}]}}`))
		default:
			t.Errorf("unexpected Engine request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer engine.Close()
	api, err := client.NewClientWithOpts(client.WithHost(engine.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	c := &Client{cli: api}
	got, err := c.CreateNetwork(t.Context(), NetworkSpec{Name: "dual", IPv6: true, Attachable: true, Internal: true,
		IPAM:    []NetworkIPAM{{Subnet: "192.0.2.0/24", Gateway: "192.0.2.1", IPRange: "192.0.2.128/25"}, {Subnet: "fd00:db::/64", Gateway: "fd00:db::1"}},
		Options: map[string]string{"com.docker.network.driver.mtu": "1400"}, Labels: map[string]string{"purpose": "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "dual" || created.Driver != "bridge" || !created.Internal || !created.Attachable || created.EnableIPv6 == nil || !*created.EnableIPv6 || created.IPAM == nil || len(created.IPAM.Config) != 2 || created.IPAM.Config[0].IPRange != "192.0.2.128/25" || created.IPAM.Config[1].Gateway != "fd00:db::1" || created.Options["com.docker.network.driver.mtu"] != "1400" || created.Labels["purpose"] != "fixture" {
		t.Fatalf("advanced request lost its policy: %+v", created)
	}
	if !got.IPv6 || !got.Internal || !got.Attachable || len(got.Subnets) != 2 {
		t.Fatalf("Engine readback lost a family: %+v", got)
	}
}
