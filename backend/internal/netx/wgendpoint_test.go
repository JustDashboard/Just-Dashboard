package netx

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"
)

func TestWireGuardEndpointEvidenceVerdicts(t *testing.T) {
	vpnService(t)
	wgLookupHost = func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "vpn.example.org":
			return []netip.Addr{netip.MustParseAddr("198.51.100.99")}, nil
		case "here.example.org":
			return []netip.Addr{netip.MustParseAddr("203.0.113.20")}, nil
		}
		return nil, fmt.Errorf("lookup %s: no such host", host)
	}
	public := wgHostState{uplink: "eth0", addrs: map[string][]netip.Prefix{"eth0": {netip.MustParsePrefix("203.0.113.20/24")}}}
	behindNAT := wgHostState{uplink: "ens5", addrs: map[string][]netip.Prefix{"ens5": {netip.MustParsePrefix("10.0.1.5/24")}}}
	for _, tc := range []struct {
		endpoint string
		host     wgHostState
		verdict  string
		warns    bool
	}{
		{"203.0.113.20:51820", public, "on_host_public", false},
		{"here.example.org:51820", public, "on_host_public", false},
		{"vpn.example.org:51820", public, "elsewhere", true},
		{"gone.example.org:51820", public, "unresolved", true},
		{"10.0.1.5:51820", behindNAT, "private", false},
		{"198.51.100.7:51820", behindNAT, "provider_mapped", false},
	} {
		e := wgEndpointEvidenceFor(context.Background(), tc.endpoint, tc.host)
		if e.Verdict != tc.verdict || (e.Warning != "") != tc.warns || e.Reachability != "not_tested" || e.Port != 51820 {
			t.Errorf("%s = %+v", tc.endpoint, e)
		}
		if e.Explanation == "" {
			t.Errorf("%s has no explanation", tc.endpoint)
		}
	}
	e := wgEndpointEvidenceFor(context.Background(), "vpn.example.org:51820", public)
	if e.Kind != "hostname" || e.Resolution != "resolved" || len(e.Addresses) != 1 || e.Addresses[0].OnHost || !e.UplinkPublic ||
		!strings.Contains(e.Explanation, "203.0.113.20") {
		t.Errorf("elsewhere = %+v", e)
	}
	// The UDP port is read from this host's sockets, never dialled.
	if e := wgEndpointEvidenceFor(context.Background(), "203.0.113.20:51820", public); !e.Listening || e.Addresses[0].Device != "eth0" {
		t.Errorf("listening = %+v", e)
	}
}

func TestWireGuardEndpointOfATunnel(t *testing.T) {
	s, _ := wgAddPeerHost(t)
	e, err := s.WireGuardEndpoint(context.Background(), "wg0")
	if err != nil || e.Endpoint != "203.0.113.20:51820" || e.Verdict != "on_host_public" {
		t.Fatalf("evidence = %+v %v", e, err)
	}
	if _, err := s.WireGuardEndpoint(context.Background(), "wg7"); err == nil {
		t.Fatal("a tunnel that does not exist has no endpoint")
	}
}

// An automatic allocation steers around the shared IPAM plan's held
// reservations, which the API layer passes in as Avoid, and says which
// endpoint evidence it found once the tunnel exists.
func TestCreateWireGuardAvoidsHeldPlanningSpace(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	wgHostReplies(t, rec)
	rec.on("systemctl enable --now wg-quick@wg0", "")
	wgUnitReplies(rec, "wg0")
	wgCommitReplies(rec)
	res, err := s.CreateWireGuard(context.Background(), WGServerRequest{
		Avoid: []netip.Prefix{netip.MustParsePrefix("10.11.0.0/24"), netip.MustParsePrefix("10.12.0.0/23")},
	}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if res.Interface.Subnet != "10.13.0.0/24" {
		t.Fatalf("subnet = %s, want the first /24 outside the host and the held plan", res.Interface.Subnet)
	}
	if res.EndpointEvidence == nil || res.EndpointEvidence.Verdict != "on_host_public" {
		t.Fatalf("evidence = %+v", res.EndpointEvidence)
	}
	events, _ := s.wg.events(context.Background(), "wg0", "", 1)
	if len(events) != 1 || events[0].Kind != "created" || !strings.Contains(events[0].Detail, "10.13.0.0/24") ||
		!strings.Contains(events[0].Detail, "on_host_public") {
		t.Fatalf("events = %+v", events)
	}
	// An explicit network inside the held plan is refused here too.
	s2 := vpnService(t)
	rec2 := record(t)
	wgHostReplies(t, rec2)
	_, err = s2.CreateWireGuard(context.Background(), WGServerRequest{Subnet: "10.12.1.0/24", Avoid: []netip.Prefix{netip.MustParsePrefix("10.12.0.0/23")}}, "alice")
	if err == nil || !strings.Contains(err.Error(), "held in the shared IPAM plan") {
		t.Fatalf("err = %v", err)
	}
}
