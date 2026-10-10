package netx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A site joined through a real WireGuard tunnel, all inside three disposable
// namespaces: this server, a site router dialled at its endpoint, and a host
// on the site's LAN whose default route is that router. The fixture verifies
// the site end to end, sees a verification degrade when the site stops
// forwarding, refuses a guarded route that would capture the site's transport,
// and removes and restores the tunnel from its archive with the site's
// unchanged configuration reconnecting.
func TestLiveWireGuardSiteVerificationTransportGuardAndRestore(t *testing.T) {
	gwLiveRequired(t)
	for _, tool := range []string{"wg", "wg-quick", "ping"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	server, site, lan := gwLiveNS(t, "st-s"), gwLiveNS(t, "st-r"), gwLiveNS(t, "st-l")
	for _, ns := range []string{server, site, lan} {
		gwMustInNS(t, ns, "ip", "link", "set", "lo", "up")
	}
	wgDualLiveLink(t, server, "transport", site, "transport", "172.32.0.1/24", "172.32.0.2/24", "", "")
	wgDualLiveLink(t, site, "lan", lan, "uplink", "192.168.88.1/24", "192.168.88.10/24", "", "")
	gwMustInNS(t, lan, "ip", "route", "add", "default", "via", "192.168.88.1")
	gwMustInNS(t, site, "sysctl", "-w", "net.ipv4.ip_forward=1")

	s := vpnService(t)
	oldRun, oldHas, oldAnchors, oldClass := run, has, anchorPaths, gatewayClassNet
	t.Cleanup(func() { run, has, anchorPaths, gatewayClassNet = oldRun, oldHas, oldAnchors, oldClass })
	inServer := wgDualLiveRun(server, s)
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "ping" {
			out, err := gwLiveCmd(ctx, append([]string{"ip", "netns", "exec", server, "ping"}, args...)...).CombinedOutput()
			return string(out), err
		}
		return inServer(ctx, name, args...)
	}
	has = func(name string) bool {
		if name == "systemctl" {
			return true
		}
		_, err := exec.LookPath(name)
		return err == nil && name != "firewall-cmd" && name != "ufw"
	}
	// The real anchor reader, run in the server's namespace: it has no
	// default route, so the WireGuard transport is the anchor that counts.
	anchorPaths = readAnchors
	gatewayClassNet = t.TempDir()
	ctx := context.Background()
	if _, err := s.CreateWireGuard(ctx, WGServerRequest{Name: "jdst", Port: 51895, Subnet: "10.80.0.0/24", Endpoint: "172.32.0.1:51895"}, "fixture"); err != nil {
		t.Fatal(err)
	}
	peer, err := s.AddWireGuardPeer(ctx, "jdst", WGPeerRequest{Name: "branch", Kind: wgKindSite, RemoteNetworks: []string{"192.168.88.0/24"}, Endpoint: "172.32.0.2:51820"}, "", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	sitePath := filepath.Join(t.TempDir(), "jdsite.conf")
	wgWriteFile(t, sitePath, peer.Config)
	env := "PATH=" + os.Getenv("PATH")
	if out, err := gwInNS(t, site, "env", env, "wg-quick", "up", sitePath); err != nil {
		t.Fatalf("site wg-quick up: %v\n%s", err, out)
	}
	handshake := func() {
		t.Helper()
		for range 20 {
			if out, _ := gwInNS(t, site, "env", env, "wg", "show", "jdsite", "latest-handshakes"); !strings.Contains(out, "\t0") && strings.TrimSpace(out) != "" {
				return
			}
			_, _ = gwInNS(t, site, "ping", "-c", "1", "-W", "1", "10.80.0.1") // a packet starts the handshake
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("the site never completed a handshake")
	}
	handshake()
	wgNow = time.Now

	v, err := s.VerifyWireGuardSite(ctx, "jdst", peer.Peer.ID, WGSiteVerifyRequest{Target: "192.168.88.10"}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if v.Outcome != "verified" {
		t.Fatalf("a working site = %+v", v)
	}
	t.Logf("verified: %+v", v.Checks)

	// The record reads the same kernel: traffic, the site's endpoint and a
	// native transport, from two passes with LAN traffic between them.
	first := time.Now()
	s.wg.tick(ctx, first)
	gwMustInNS(t, lan, "ping", "-c", "5", "-i", "0.2", "10.80.0.1")
	s.wg.tick(ctx, first.Add(2*time.Second))
	wgNow = func() time.Time { return first.Add(2 * time.Second) }
	h, err := s.WireGuardHistory(ctx, "jdst", peer.Peer.PublicKey, "6h")
	if err != nil || len(h.Endpoints) != 1 || h.Endpoints[0].Endpoint != "172.32.0.2:51820" || len(h.Points) == 0 {
		t.Fatalf("record = %+v %v", h, err)
	}
	moved := 0.0
	for _, p := range h.Points {
		moved += p.Rx + p.Tx
	}
	if transport, ok := s.wg.transport("jdst", peer.Peer.PublicKey); moved == 0 || !ok || transport.State != "native" || transport.Device != "transport" {
		t.Fatalf("recorded traffic %v, transport %+v", moved, transport)
	}
	wgNow = time.Now

	// The site stops forwarding: the tunnel answers, its LAN does not.
	gwMustInNS(t, site, "sysctl", "-w", "net.ipv4.ip_forward=0")
	v, err = s.VerifyWireGuardSite(ctx, "jdst", peer.Peer.ID, WGSiteVerifyRequest{Target: "192.168.88.10"}, "fixture")
	if err != nil || v.Outcome != "partial" || v.Checks[len(v.Checks)-1].Status != "warn" || v.Checks[len(v.Checks)-2].Status != "pass" {
		t.Fatalf("a site that stopped forwarding = %+v %v", v, err)
	}
	gwMustInNS(t, site, "sysctl", "-w", "net.ipv4.ip_forward=1")

	// A guarded route that would carry the site's own transport into the
	// tunnel is refused and taken back.
	before := gwMustInNS(t, server, "ip", "route", "show")
	_, err = s.AddRoute(ctx, RouteRequest{Destination: "172.32.0.0/25", Device: "jdst"}, "", "fixture")
	if !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "WireGuard transport of jdst to 172.32.0.2") {
		t.Fatalf("a route capturing the site's transport: %v", err)
	}
	if after := gwMustInNS(t, server, "ip", "route", "show"); after != before {
		t.Fatalf("the refused route was left in place:\n%s", after)
	}
	if sp := wgMustSpec(t, s); len(sp.Routes) != 0 {
		t.Fatalf("the refused route was saved: %+v", sp.Routes)
	}
	handshake()

	// Removed, archived, restored: the same key, so the site's unchanged
	// configuration connects again.
	if err := s.RemoveWireGuard(ctx, "jdst", "", "fixture"); err != nil {
		t.Fatal(err)
	}
	if out, err := gwInNS(t, server, "ip", "link", "show", "jdst"); err == nil {
		t.Fatalf("the removed tunnel's interface remains: %s", out)
	}
	archive, err := s.WireGuardArchive(ctx)
	if err != nil || len(archive) != 1 || !archive[0].Restorable {
		t.Fatalf("archive = %+v %v", archive, err)
	}
	res, err := s.RestoreWireGuard(ctx, archive[0].File, "", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Interface.Up || len(res.Interface.Peers) != 1 {
		t.Fatalf("restored = %+v", res.Interface)
	}
	if out := gwMustInNS(t, server, "ip", "route", "show", "192.168.88.0/24"); !strings.Contains(out, "dev jdst") {
		t.Fatalf("the restored tunnel does not route the site's network: %q", out)
	}
	handshake()
	v, err = s.VerifyWireGuardSite(ctx, "jdst", peer.Peer.ID, WGSiteVerifyRequest{Target: "192.168.88.10"}, "fixture")
	if err != nil || v.Outcome != "verified" {
		t.Fatalf("after restore = %+v %v", v, err)
	}
	events, _ := s.wg.events(ctx, "jdst", "", 20)
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	want := "site_verified restored removed site_verified site_verified peer_added created"
	if strings.Join(kinds, " ") != want {
		t.Fatalf("history = %s, want %s", strings.Join(kinds, " "), want)
	}
	t.Logf("history: %s", strings.Join(kinds, " "))
}
