package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netipam"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

func TestIPAMWireGuardSelectedCreationPreservesExplicitFamilyTuple(t *testing.T) {
	s, router := gatewayRouter(t, auth.RoleAdmin, false)
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "unit-started")
	t.Setenv("JD_IPAM_WG_STATE", stateFile)
	vpnFakeBin(t, map[string]string{
		"ip":        `case "$*" in "-j route show default") printf '%s\n' '[{"dst":"default","dev":"wan0"}]';; *) printf '%s\n' '[]';; esac`,
		"wg":        `if [ "$*" = "show all dump" ] && [ -e "$JD_IPAM_WG_STATE" ]; then printf 'wg-ipam\t(none)\tAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\t51820\toff\n'; fi`,
		"wg-quick":  "exit 0",
		"systemctl": `case "$*" in "enable --now wg-quick@wg-ipam") touch "$JD_IPAM_WG_STATE";; *) exit 0;; esac`,
		"ufw":       "exit 1", "nft": "exit 1", "iptables": "exit 1", "iptables-save": "exit 1", "ip6tables": "exit 1", "ip6tables-save": "exit 1", "sysctl": "exit 1",
	})
	oldSec := s.modules.netsec
	s.modules.netsec = netsec.New()
	t.Cleanup(func() { s.modules.netsec = oldSec })
	s.modules.network = netx.New(netx.Options{Paths: netx.Paths{Dir: filepath.Join(dir, "network"), WireGuard: filepath.Join(dir, "wireguard"), Unit: filepath.Join(dir, "unit"), Sysctl: filepath.Join(dir, "sysctl")}, DB: s.Store.DB, Seal: s.Sealer.Seal, Open: s.Sealer.Open})
	s.modules.ipam = netipam.New(s.Store, func(context.Context) (netipam.Snapshot, error) {
		return netipam.Snapshot{Coverage: []netipam.Coverage{{Source: "fixture", State: "observed"}}}, nil
	})
	makePlan := func(prefix string, bits int) netipam.Reservation {
		p, e := s.modules.ipam.CreatePool(t.Context(), netipam.PoolRequest{Name: prefix, Prefix: prefix, AllocationBits: bits})
		if e != nil {
			t.Fatal(e)
		}
		r, e := s.modules.ipam.Reserve(t.Context(), netipam.ReserveRequest{PoolID: p.ID, Owner: "wireguard_server", Resource: "wg-ipam"}, "operator")
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	r4, r6 := makePlan("10.244.0.0/16", 24), makePlan("fd48:abcd::/48", 64)
	router.Route("/wg-owner", s.mountNetworkVPNRoutes)
	body := string(netipamTestJSON(map[string]any{"name": "wg-ipam", "endpoint": "vpn.example.com", "exitNode": false, "subnet": r4.Prefix, "ipv6": map[string]any{"subnet": r6.Prefix, "exitNode": false}, "ipamReservationIds": []string{r4.ID, r6.ID}}))
	response := ipamDo(router, "POST", "/wg-owner/vpn/wireguard/", body)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var result wgCreateResponse
	if e := json.Unmarshal(response.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.Interface.Name != "wg-ipam" || !result.Interface.IPv6Enabled || result.Interface.ExitNode {
		t.Fatal("selected addressing changed exit intent", result.Interface.Name, result.Interface.IPv6Enabled, result.Interface.ExitNode)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "wireguard", "wg-ipam.conf"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), "Address = 10.244.0.1/24, fd48:abcd::1/64") {
		t.Fatal("owner received another family tuple")
	}
	for _, r := range []netipam.Reservation{r4, r6} {
		var state, id string
		s.Store.DB.QueryRow(`SELECT state,native_id FROM network_ipam_reservations WHERE id=?`, r.ID).Scan(&state, &id)
		if state != "observed" || id != "wg-ipam" {
			t.Fatal("native identity missing; plan must remain held", state, id)
		}
	}
}

func TestIPAMWireGuardIncompatibleSelectionStopsBeforeOwnerMutation(t *testing.T) {
	for _, scenario := range []string{"name", "non-ula", "width", "missing-name", "missing-id", "random", "wrong-owner"} {
		t.Run(scenario, func(t *testing.T) {
			s, router := gatewayRouter(t, auth.RoleAdmin, false)
			s.modules.ipam = netipam.New(s.Store, func(context.Context) (netipam.Snapshot, error) {
				return netipam.Snapshot{Coverage: []netipam.Coverage{{Source: "fixture", State: "observed"}}}, nil
			})
			p, e := s.modules.ipam.CreatePool(t.Context(), netipam.PoolRequest{Name: "ipv6", Prefix: "fd48:abcd::/48", AllocationBits: 64})
			if e != nil {
				t.Fatal(e)
			}
			owner := "wireguard_server"
			if scenario == "wrong-owner" {
				owner = "docker_network"
			}
			reservation, e := s.modules.ipam.Reserve(t.Context(), netipam.ReserveRequest{PoolID: p.ID, Owner: owner, Resource: "wg-ipam"}, "operator")
			if e != nil {
				t.Fatal(e)
			}
			name, prefix, ids := "wg-ipam", reservation.Prefix, []string{reservation.ID}
			switch scenario {
			case "name":
				name = "wg-other"
			case "non-ula":
				prefix = "2001:db8::/64"
			case "width":
				prefix = "fd48:abcd::/48"
			case "missing-name":
				name = ""
			case "missing-id":
				ids = nil
			case "random":
				prefix = ""
			}
			body := string(netipamTestJSON(map[string]any{"name": name, "exitNode": false, "ipv6": map[string]any{"subnet": prefix, "exitNode": false}, "ipamReservationIds": ids}))
			router.Route("/wg-owner", s.mountNetworkVPNRoutes)
			response := ipamDo(router, http.MethodPost, "/wg-owner/vpn/wireguard/", body)
			if response.Code != 400 && response.Code != 409 {
				t.Fatal("invalid shared tuple reached absent native owner", response.Code, response.Body.String())
			}
			var state string
			s.Store.DB.QueryRow(`SELECT state FROM network_ipam_reservations WHERE id=?`, reservation.ID).Scan(&state)
			if state != "reserved" {
				t.Fatal("invalid selection claimed native handoff", state)
			}
		})
	}
}
