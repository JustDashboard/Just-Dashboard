package netx

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wgStrp(s string) *string       { return &s }
func wgBoolp(b bool) *bool          { return &b }
func wgListp(v ...string) *[]string { return &v }

func TestEditWireGuardDeviceRoutesRegeneratesItsConfiguration(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	ctx := context.Background()
	made, err := s.AddWireGuardPeer(ctx, "wg0", WGPeerRequest{Name: "Tablet", Kind: "device", FullTunnel: true}, "198.51.100.7", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(made.Peer.ClientRoutes, ",") != "0.0.0.0/0,::/0" {
		t.Fatalf("recorded client routes = %v", made.Peer.ClientRoutes)
	}
	before, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	reloads := strings.Count(strings.Join(rec.commands(), "\n"), "systemctl reload")

	res, err := s.EditWireGuardPeer(ctx, "wg0", made.Peer.ID, WGPeerEdit{FullTunnel: wgBoolp(false), ShareNetworks: wgListp("172.17.0.0/16")}, "198.51.100.7", "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ClientChanged || !strings.Contains(res.Config, "AllowedIPs = 10.8.0.0/24, 172.17.0.0/16\n") || strings.Contains(res.Config, "IPv6 leak") ||
		!strings.HasPrefix(res.QR, "data:image/png;base64,") {
		t.Fatalf("regenerated configuration:\n%s", res.Config)
	}
	oldPriv := parseWGConf(made.Config).iface().get("privatekey")
	if parseWGConf(res.Config).iface().get("privatekey") != oldPriv || parseWGConf(res.Config).peers()[0].get("presharedkey") != parseWGConf(made.Config).peers()[0].get("presharedkey") {
		t.Fatal("an edit must keep the device's keys: it is the same peer")
	}
	if strings.Join(res.Peer.ClientRoutes, ",") != "10.8.0.0/24,172.17.0.0/16" {
		t.Errorf("client routes = %v", res.Peer.ClientRoutes)
	}
	stored, err := s.vpn.Get(ctx, "wg0", int64(made.Peer.ID))
	if err != nil || stored.Config != res.Config {
		t.Fatalf("the sealed copy was not replaced: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	if string(before) != string(after) || strings.Count(strings.Join(rec.commands(), "\n"), "systemctl reload") != reloads {
		t.Error("a device's own routes are not this server's: the file and the interface must not change")
	}
	events, _ := s.wg.events(ctx, "wg0", made.Peer.PublicKey, 5)
	if len(events) == 0 || events[0].Kind != "peer_edited" || !strings.Contains(events[0].Detail, "client routes 10.8.0.0/24, 172.17.0.0/16") {
		t.Fatalf("events = %+v", events)
	}
	if strings.Contains(wgMustString(t, events), oldPriv) {
		t.Fatal("the history must never carry a private key")
	}

	// A forgotten configuration cannot be regenerated, and nothing changes.
	if err := s.ForgetWireGuardPeerConfig(ctx, "wg0", made.Peer.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	_, err = s.EditWireGuardPeer(ctx, "wg0", made.Peer.ID, WGPeerEdit{FullTunnel: wgBoolp(true)}, "", "alice", nil)
	var ro *ReadOnlyError
	if !errors.As(err, &ro) || !strings.Contains(ro.Reason, "forgotten") {
		t.Fatalf("an edit of a forgotten configuration: %v", err)
	}
	if forgotten, err := s.vpn.Get(ctx, "wg0", int64(made.Peer.ID)); !errors.Is(err, ErrForgotten) || forgotten.Config != "" {
		t.Fatal("a refused edit brought a private key back")
	}
}

func TestEditWireGuardSiteNetworksAndName(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	ctx := context.Background()
	asked := 0
	authorize := func() error { asked++; return nil }

	res, err := s.EditWireGuardPeer(ctx, "wg0", 2, WGPeerEdit{
		Name: wgStrp("Head office"), RemoteNetworks: wgListp("192.168.77.0/24", "192.168.78.0/24"),
	}, "198.51.100.7", "alice", authorize)
	if err != nil {
		t.Fatal(err)
	}
	if asked != 0 {
		t.Error("adding a network withdraws nothing and needs no destructive budget")
	}
	text, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	for _, want := range []string{"# jd:name=Head office\n", "AllowedIPs = 10.8.0.3/32, 192.168.77.0/24, 192.168.78.0/24\n", "# a note somebody wrote by hand\n"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("file lacks %q:\n%s", want, text)
		}
	}
	if res.Peer.Name != "Head office" || res.ClientChanged || !res.Reloaded {
		t.Errorf("result = %+v", res)
	}
	wgAssertOrder(t, rec.commands(), "systemctl reload wg-quick@wg0", "ip route replace 192.168.78.0/24 dev wg0")
	if rec.ran("ip route replace 192.168.77.0/24") {
		t.Error("a network already routed is not routed again")
	}

	// Taking a network away asks for the destructive budget first.
	refused := errors.New("budget spent")
	_, err = s.EditWireGuardPeer(ctx, "wg0", 2, WGPeerEdit{RemoteNetworks: wgListp("192.168.78.0/24")}, "", "alice", func() error { return refused })
	if !errors.Is(err, refused) {
		t.Fatalf("withdrawal was not authorized first: %v", err)
	}
	if again, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf")); string(again) != string(text) {
		t.Fatal("a refused withdrawal changed the file")
	}
	if _, err := s.EditWireGuardPeer(ctx, "wg0", 2, WGPeerEdit{RemoteNetworks: wgListp("192.168.78.0/24")}, "", "alice", authorize); err != nil {
		t.Fatal(err)
	}
	if asked != 1 || !rec.ran("ip route del 192.168.77.0/24 dev wg0") {
		t.Errorf("withdrawal asked %d times; route removed %v", asked, rec.ran("ip route del 192.168.77.0/24"))
	}
}

func TestEditWireGuardPeerRefusals(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		id      int
		edit    WGPeerEdit
		client  string
		want    string
		guarded bool
	}{
		{"nothing", 2, WGPeerEdit{}, "", "nothing to change", false},
		{"unknown peer", 9, WGPeerEdit{Name: wgStrp("x")}, "", "not found", false},
		{"duplicate name", 2, WGPeerEdit{Name: wgStrp("phone")}, "", "already exists", false},
		{"device endpoint", 1, WGPeerEdit{Endpoint: wgStrp("1.2.3.4:5")}, "", "belong to a site", false},
		{"site full tunnel", 2, WGPeerEdit{FullTunnel: wgBoolp(true)}, "", "device's setting", false},
		{"bad keepalive", 2, WGPeerEdit{Keepalive: wgIntp(-1)}, "", "keepalive", false},
		{"overlaps the tunnel", 2, WGPeerEdit{RemoteNetworks: wgListp("10.8.0.0/16")}, "", "tunnel's own network", false},
		{"overlaps docker", 2, WGPeerEdit{RemoteNetworks: wgListp("192.168.77.0/24", "172.17.0.0/24")}, "", "docker0", true},
		{"holds the operator", 2, WGPeerEdit{RemoteNetworks: wgListp("192.168.77.0/24", "198.51.100.0/24")}, "198.51.100.7", "your own address", true},
		// The phone is dialling from 198.51.100.7 now; a site network over it
		// would route the phone's own transport into the tunnel.
		{"captures a roaming peer", 2, WGPeerEdit{RemoteNetworks: wgListp("192.168.77.0/24", "198.51.100.4/30")}, "", "WireGuard endpoint 198.51.100.7", true},
		{"unresolvable endpoint", 2, WGPeerEdit{Endpoint: wgStrp("office.invalid:51820")}, "", "does not resolve", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := wgAddPeerHost(t)
			before, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
			_, err := s.EditWireGuardPeer(ctx, "wg0", tc.id, tc.edit, tc.client, "a", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if errors.Is(err, ErrGuarded) != tc.guarded {
				t.Errorf("guarded = %v (%v)", errors.Is(err, ErrGuarded), err)
			}
			after, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
			if string(before) != string(after) || rec.ran("systemctl reload") || rec.ran("ip route replace") {
				t.Error("a refused edit changed the host")
			}
		})
	}
}

func TestEditWireGuardSiteEndpointResolvesNamesAgainstItsNetworks(t *testing.T) {
	s, _ := wgAddPeerHost(t)
	wgLookupHost = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "office.example.net" {
			return []netip.Addr{netip.MustParseAddr("192.168.77.1")}, nil
		}
		return nil, fmt.Errorf("no such host")
	}
	_, err := s.EditWireGuardPeer(context.Background(), "wg0", 2, WGPeerEdit{Endpoint: wgStrp("office.example.net:51820")}, "", "a", func() error { return nil })
	if !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "WireGuard endpoint 192.168.77.1") {
		t.Fatalf("a name resolving inside the site's own network must be refused: %v", err)
	}
}

func TestEditWireGuardPeerFailedReloadIsPutBackAndRecorded(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "systemctl reload wg-quick@wg0", err: fmt.Errorf("Job for wg-quick@wg0.service failed")}}, rec.replies...)
	rec.mu.Unlock()
	before, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	_, err := s.EditWireGuardPeer(context.Background(), "wg0", 1, WGPeerEdit{Name: wgStrp("Old phone")}, "", "alice", nil)
	if err == nil {
		t.Fatal("a failed reload must fail the edit")
	}
	after, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	if string(before) != string(after) {
		t.Fatal("the file was not put back")
	}
	events, _ := s.wg.events(context.Background(), "wg0", "", 5)
	// The reload of the restored file failed too, so the outcome is degraded.
	if len(events) != 1 || events[0].Kind != "peer_edit_failed" || events[0].Outcome != wgOutcomeDegraded || !strings.Contains(events[0].Detail, "running interface") {
		t.Fatalf("events = %+v", events)
	}
}

func wgSiteHost(t *testing.T, handshake int64) (*Service, *recorder) {
	t.Helper()
	s, rec := wgAddPeerHost(t)
	dump := strings.Replace(fixture(t, "wg-dump.txt"),
		"(none)\t10.8.0.3/32,192.168.77.0/24\t0\t0\t0\toff",
		fmt.Sprintf("198.51.100.50:51820\t10.8.0.3/32,192.168.77.0/24\t%d\t10\t10\t25", handshake), 1)
	rec.mu.Lock()
	rec.replies = append([]reply{
		{prefix: "wg show all dump", out: dump},
		{prefix: "ip -j route get 192.168.77.1", out: `[{"dst":"192.168.77.1","dev":"wg0"}]`},
		{prefix: "ip -j route get 198.51.100.50", out: `[{"dst":"198.51.100.50","gateway":"203.0.113.1","dev":"eth0"}]`},
		{prefix: "ping -n -q -c 3 -W 1 -I 10.8.0.1 10.8.0.3", out: "3 packets transmitted, 3 received"},
		{prefix: "ping -n -q -c 3 -W 1 -I 10.8.0.1 192.168.77.10", out: "3 packets transmitted, 3 received"},
		{prefix: "ping", err: fmt.Errorf("3 packets transmitted, 0 received")},
	}, rec.replies...)
	rec.mu.Unlock()
	return s, rec
}

func TestVerifyWireGuardSiteEndToEnd(t *testing.T) {
	s, _ := wgSiteHost(t, fixNow-20)
	ctx := context.Background()
	v, err := s.VerifyWireGuardSite(ctx, "wg0", 2, WGSiteVerifyRequest{Target: "192.168.77.10"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if v.Outcome != "verified" || len(v.Checks) != 5 {
		t.Fatalf("verification = %+v", v)
	}
	for _, c := range v.Checks {
		if c.Status != "pass" {
			t.Errorf("check %+v", c)
		}
	}
	if len(v.RemoteSteps) != 4 || !strings.Contains(v.RemoteSteps[1], "ping -c 3 10.8.0.1") {
		t.Errorf("remote steps = %v", v.RemoteSteps)
	}
	events, _ := s.wg.events(ctx, "wg0", fixPeerB, 1)
	if len(events) != 1 || events[0].Kind != "site_verified" || events[0].Outcome != wgOutcomeOK {
		t.Fatalf("events = %+v", events)
	}

	// Without a host inside the site, its LAN path was not exercised.
	v, _ = s.VerifyWireGuardSite(ctx, "wg0", 2, WGSiteVerifyRequest{}, "alice")
	if v.Outcome != "partial" {
		t.Errorf("without a target = %s", v.Outcome)
	}
	// A host that does not answer is a warning: a filter can cause it too.
	v, _ = s.VerifyWireGuardSite(ctx, "wg0", 2, WGSiteVerifyRequest{Target: "192.168.77.99"}, "alice")
	if v.Outcome != "partial" || v.Checks[len(v.Checks)-1].Status != "warn" {
		t.Errorf("a silent target = %+v", v)
	}
	for _, bad := range []struct {
		id     int
		target string
		want   string
	}{{2, "10.20.0.1", "not inside"}, {1, "", "is a device"}, {9, "", "not found"}, {2, "nope", "target"}} {
		if _, err := s.VerifyWireGuardSite(ctx, "wg0", bad.id, WGSiteVerifyRequest{Target: bad.target}, "a"); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}

func TestVerifyWireGuardSiteThatIsQuietFails(t *testing.T) {
	s, _ := wgSiteHost(t, fixNow-3600)
	v, err := s.VerifyWireGuardSite(context.Background(), "wg0", 2, WGSiteVerifyRequest{}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if v.Outcome != "failed" || v.Checks[0].Name != "handshake" || v.Checks[0].Status != "fail" {
		t.Fatalf("a quiet site = %+v", v)
	}
}
