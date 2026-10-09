package netx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWireGuardKillSwitchVariant(t *testing.T) {
	s, _ := wgAddPeerHost(t)
	ctx := context.Background()
	full, err := s.AddWireGuardPeer(ctx, "wg0", WGPeerRequest{Name: "Laptop", Kind: "device", FullTunnel: true}, "", "alice")
	if err != nil {
		t.Fatal(err)
	}
	ks, err := s.WireGuardPeerKillSwitch(ctx, "wg0", full.Peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ks.QR != "" {
		t.Error("the phone apps refuse PostUp: the variant is never a QR code")
	}
	for _, want := range []string{
		"PostUp = nft add table inet jd_killswitch\n",
		"oifname \"%i\" accept",
		"meta mark \"$(wg show %i fwmark)\" accept",
		"nd-neighbor-solicit",
		"udp sport 68 udp dport 67 accept",
		"output reject'\n",
		"PreDown = nft delete table inet jd_killswitch\n",
		"# stays offline until: nft delete table inet jd_killswitch\n[Interface]\n",
		"AllowedIPs = 0.0.0.0/0, ::/0\n",
	} {
		if !strings.Contains(ks.Config, want) {
			t.Errorf("variant lacks %q:\n%s", want, ks.Config)
		}
	}
	// wg-quick strips everything after a '#' in a value.
	for _, line := range strings.Split(ks.Config, "\n") {
		if strings.HasPrefix(line, "PostUp") || strings.HasPrefix(line, "PreDown") {
			if strings.Contains(line, "#") {
				t.Errorf("a hook carries a comment marker: %s", line)
			}
		}
	}
	if !strings.Contains(ks.Config, parseWGConf(full.Config).iface().get("privatekey")) {
		t.Error("the variant is the device's own configuration")
	}

	split, err := s.AddWireGuardPeer(ctx, "wg0", WGPeerRequest{Name: "Desk", Kind: "device"}, "", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WireGuardPeerKillSwitch(ctx, "wg0", split.Peer.ID); err == nil || !strings.Contains(err.Error(), "full tunnel") {
		t.Fatalf("a split tunnel's native traffic is meant to leave natively: %v", err)
	}
	site, err := s.AddWireGuardPeer(ctx, "wg0", WGPeerRequest{Name: "Depot", Kind: "site", RemoteNetworks: []string{"192.168.90.0/24"}}, "", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WireGuardPeerKillSwitch(ctx, "wg0", site.Peer.ID); err == nil {
		t.Fatal("a site has no kill switch")
	}
	_ = s.ForgetWireGuardPeerConfig(ctx, "wg0", full.Peer.ID, "alice")
	if _, err := s.WireGuardPeerKillSwitch(ctx, "wg0", full.Peer.ID); !errors.Is(err, ErrForgotten) {
		t.Fatalf("a forgotten configuration: %v", err)
	}
}

func TestStoredConfigurationOfAnotherPeerIsNeverShown(t *testing.T) {
	s, _ := wgAddPeerHost(t)
	ctx := context.Background()
	// A row left with peer 1's id but another key: a tunnel removed by hand
	// and recreated, or a database restored from elsewhere.
	if _, err := s.db.Exec(`INSERT INTO network_vpn_clients (id, iface, public_key, name, config_sealed, created_at) VALUES (1, 'wg0', ?, 'stale', 'sealed:[Interface]', 1)`, fixPeerC); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WireGuardPeerConfig(ctx, "wg0", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another peer's configuration was shown: %v", err)
	}
	if err := s.ForgetWireGuardPeerConfig(ctx, "wg0", 1, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another peer's configuration was forgotten: %v", err)
	}
}
