package netx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The kill switch against real wg-quick, nftables and WireGuard, all inside
// three disposable namespaces: a server whose generated full-tunnel profile a
// client brings up, and a WAN that answers with the source address it saw.
// The plain profile is run first to show the fixture detects both leaks a
// full tunnel leaves open — a more specific native route, and an interface
// lost without wg-quick's teardown — then the kill-switch variant of the same
// peer is shown to hold through both, and a deliberate `wg-quick down` to
// restore the native path. The exits' measured translation counters are read
// from the server's own rules after real client traffic.
func TestLiveWireGuardKillSwitchHoldsWhenTheTunnelBreaks(t *testing.T) {
	gwLiveRequired(t)
	for _, tool := range []string{"wg", "wg-quick", "ip6tables"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	server, client, wan := gwLiveNS(t, "ks-s"), gwLiveNS(t, "ks-c"), gwLiveNS(t, "ks-w")
	for _, ns := range []string{server, client, wan} {
		gwMustInNS(t, ns, "ip", "link", "set", "lo", "up")
		gwMustInNS(t, ns, "sysctl", "-w", "net.ipv6.conf.all.disable_ipv6=0")
	}
	wgDualLiveLink(t, server, "transport", client, "transport", "172.31.0.1/24", "172.31.0.2/24", "", "")
	wgDualLiveLink(t, server, "wan4", wan, "server4", "192.0.2.2/24", "192.0.2.1/24", "", "")
	wgDualLiveLink(t, server, "wan6", wan, "server6", "", "", "2001:db8:2::2/64", "2001:db8:2::1/64")
	wgDualLiveLink(t, client, "native", wan, "native-peer", "198.51.100.2/24", "198.51.100.1/24", "2001:db8:3::2/64", "2001:db8:3::1/64")
	gwMustInNS(t, server, "ip", "route", "add", "default", "via", "192.0.2.1", "dev", "wan4")
	gwMustInNS(t, server, "ip", "-6", "route", "add", "default", "via", "2001:db8:2::1", "dev", "wan6")
	gwMustInNS(t, client, "ip", "route", "add", "default", "via", "198.51.100.1", "dev", "native")
	gwMustInNS(t, client, "ip", "-6", "route", "add", "default", "via", "2001:db8:3::1", "dev", "native")
	gwMustInNS(t, server, "sysctl", "-w", "net.ipv4.ip_forward=1", "net.ipv6.conf.all.forwarding=1")
	gwMustInNS(t, server, "iptables", "-P", "FORWARD", "DROP")
	gwMustInNS(t, server, "ip6tables", "-P", "FORWARD", "DROP")
	for _, addr := range []string{"1.1.1.1/32", "9.9.9.9/32", "2606:4700:4700::1111/128", "2620:fe::fe/128"} {
		gwMustInNS(t, wan, "ip", "addr", "add", addr, "dev", "lo", "nodad")
		wgDualLiveServe(t, wan, strings.Split(addr, "/")[0])
	}

	s := vpnService(t)
	oldRun, oldHas, oldAnchors, oldClass := run, has, anchorPaths, gatewayClassNet
	t.Cleanup(func() { run, has, anchorPaths, gatewayClassNet = oldRun, oldHas, oldAnchors, oldClass })
	run = wgDualLiveRun(server, s)
	has = func(name string) bool {
		if name == "systemctl" {
			return true
		}
		_, err := exec.LookPath(name)
		return err == nil && name != "firewall-cmd" && name != "ufw"
	}
	anchorPaths = func(context.Context) []anchorPath { return nil }
	gatewayClassNet = t.TempDir()
	ctx := context.Background()
	if _, err := s.CreateWireGuard(ctx, WGServerRequest{Name: "jdks", Port: 51890, Subnet: "10.79.0.0/24", Endpoint: "172.31.0.1:51890",
		DNS: []string{"1.1.1.1"}, ExitNode: true, IPv6: &WGIPv6Request{Subnet: "fd42:79::/64", ExitNode: true}}, "fixture"); err != nil {
		t.Fatal(err)
	}
	peer, err := s.AddWireGuardPeer(ctx, "jdks", WGPeerRequest{Name: "laptop", Kind: wgKindDevice, FullTunnel: true}, "", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	ks, err := s.WireGuardPeerKillSwitch(ctx, "jdks", peer.Peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jdkc.conf")
	env := "PATH=" + os.Getenv("PATH")
	up := func(text string) {
		t.Helper()
		wgWriteFile(t, path, wgDualClientText(text))
		if out, err := gwInNS(t, client, "env", env, "wg-quick", "up", path); err != nil {
			t.Fatalf("wg-quick up: %v\n%s", err, out)
		}
	}
	reach := func(host string) (string, error) {
		return gwInNS(t, client, "curl", "--noproxy", "*", "-fsS", "--max-time", "2", "http://"+host+":8080")
	}
	specific := func(add bool) {
		verb := "add"
		if !add {
			verb = "del"
		}
		gwMustInNS(t, client, "ip", "route", verb, "9.9.9.9/32", "via", "198.51.100.1", "dev", "native")
		gwMustInNS(t, client, "ip", "-6", "route", verb, "2620:fe::fe/128", "via", "2001:db8:3::1", "dev", "native")
	}

	// The plain profile: both leaks are real, so the fixture can see them.
	up(peer.Config)
	wgDualLiveFetch(t, client, "9.9.9.9", "192.0.2.2")
	wgDualLiveFetch(t, client, "2620:fe::fe", "2001:db8:2::2")
	specific(true)
	wgDualLiveFetch(t, client, "9.9.9.9", "198.51.100.2")
	wgDualLiveFetch(t, client, "2620:fe::fe", "2001:db8:3::2")
	specific(false)
	gwMustInNS(t, client, "ip", "link", "del", "jdkc")
	wgDualLiveFetch(t, client, "9.9.9.9", "198.51.100.2")
	wgDualLiveFetch(t, client, "2620:fe::fe", "2001:db8:3::2")
	// What wg-quick would have taken down with the interface.
	for _, family := range []string{"-4", "-6"} {
		for {
			rules := gwMustInNS(t, client, "ip", family, "rule", "show")
			if !strings.Contains(rules, "lookup 51820") && !strings.Contains(rules, "suppress_prefixlength 0") {
				break
			}
			if strings.Contains(rules, "lookup 51820") {
				gwMustInNS(t, client, "ip", family, "rule", "del", "table", "51820")
			} else {
				gwMustInNS(t, client, "ip", family, "rule", "del", "table", "main", "suppress_prefixlength", "0")
			}
		}
	}
	if out, _ := gwInNS(t, client, "nft", "list", "tables"); strings.Contains(out, "wg-quick-jdkc") {
		gwMustInNS(t, client, "nft", "delete", "table", "ip", "wg-quick-jdkc")
		_, _ = gwInNS(t, client, "nft", "delete", "table", "ip6", "wg-quick-jdkc") // only present with IPv6 routes
	}

	// The kill-switch variant of the same peer.
	up(ks.Config)
	wgDualLiveFetch(t, client, "9.9.9.9", "192.0.2.2")
	wgDualLiveFetch(t, client, "2620:fe::fe", "2001:db8:2::2")
	view, err := s.readWireGuard(ctx, "jdks", false)
	if err != nil || len(view.Interfaces) != 1 {
		t.Fatal(err)
	}
	for family, state := range map[string]WGExitState{"IPv4": view.Interfaces[0].Families.IPv4.Exit, "IPv6": view.Interfaces[0].Families.IPv6.Exit} {
		if state.Runtime != "verified" || state.Translated == nil || *state.Translated == 0 {
			t.Fatalf("%s exit after client traffic = %+v", family, state)
		}
		t.Logf("%s exit translated %d client connections", family, *state.Translated)
	}
	specific(true)
	for _, host := range []string{"9.9.9.9", "[2620:fe::fe]"} {
		if out, err := reach(host); err == nil {
			t.Fatalf("a more specific native route leaked %s through the kill switch: %s", host, out)
		}
	}
	if _, err := gwInNS(t, client, "python3", "-c", wgDualDNSQuery, "1.1.1.1"); err != nil {
		t.Fatalf("resolver traffic inside the tunnel must still work: %v", err)
	}
	specific(false)

	// A deliberate disconnect lifts the filter and the native path returns.
	gwMustInNS(t, client, "env", env, "wg-quick", "down", path)
	if out, _ := gwInNS(t, client, "nft", "list", "tables"); strings.Contains(out, wgKillSwitchTable) {
		t.Fatalf("wg-quick down left the kill switch in place:\n%s", out)
	}
	wgDualLiveFetch(t, client, "9.9.9.9", "198.51.100.2")

	// An interface lost without wg-quick's teardown leaves the device offline.
	up(ks.Config)
	wgDualLiveFetch(t, client, "9.9.9.9", "192.0.2.2")
	gwMustInNS(t, client, "ip", "link", "del", "jdkc")
	for _, host := range []string{"9.9.9.9", "[2620:fe::fe]"} {
		if out, err := reach(host); err == nil {
			t.Fatalf("%s leaked natively after the interface was lost: %s", host, out)
		}
	}
	for _, dns := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if _, err := gwInNS(t, client, "python3", "-c", wgDualDNSQuery, dns); err == nil {
			t.Fatalf("resolver traffic to %s leaked after the interface was lost", dns)
		}
	}
	if out := gwMustInNS(t, client, "nft", "list", "table", "inet", wgKillSwitchTable); !strings.Contains(out, "reject") {
		t.Fatalf("the kill switch did not outlive its interface:\n%s", out)
	}
	// The documented way out.
	gwMustInNS(t, client, "nft", "delete", "table", "inet", wgKillSwitchTable)
}
