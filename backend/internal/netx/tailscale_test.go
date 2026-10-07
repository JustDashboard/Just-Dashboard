package netx

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// tsHost answers the two reads a view takes.
func tsHost(t *testing.T, status, prefs string) (*Service, *recorder) {
	t.Helper()
	s := vpnService(t)
	rec := record(t)
	rec.on("tailscale status --json", fixture(t, status)).
		on("tailscale debug prefs", fixture(t, prefs))
	return s, rec
}

func tsPeerByHost(t *testing.T, v *TailscaleView, host string) TSPeer {
	t.Helper()
	for _, p := range v.Peers {
		if p.HostName == host {
			return p
		}
	}
	t.Fatalf("no peer %s in %+v", host, v.Peers)
	return TSPeer{}
}

func TestTailscaleViewOfAnExitNodeAndSubnetRouter(t *testing.T) {
	s, _ := tsHost(t, "tailscale-status.json", "tailscale-prefs-exit.json")
	v, err := s.Tailscale(context.Background(), "198.51.100.7")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Installed || !v.Running || v.BackendState != "Running" || v.Tailnet != "example.org" || v.MagicDNSSuffix != "tail-example.ts.net" {
		t.Errorf("view = %+v", v)
	}
	if self := v.Self; self == nil || self.HostName != "srv" || !self.Online || !self.ExitNodeOption || self.Relay != "fra" ||
		strings.Join(self.PrimaryRoutes, ",") != "192.0.2.0/24" || len(self.TailscaleIPs) != 2 {
		t.Errorf("self = %+v", self)
	}
	if len(v.Health) != 1 {
		t.Errorf("health = %v", v.Health)
	}

	// The exit-node pair is not a subnet route: it is the exit node.
	pr := v.Prefs
	if !pr.AdvertisingExitNode || strings.Join(pr.AdvertiseRoutes, ",") != "192.0.2.0/24" || !pr.AcceptRoutes || !pr.AcceptDNS || pr.UsingExitNode || pr.ShieldsUp {
		t.Errorf("prefs = %+v", pr)
	}
	if v.ControlServer != "tailscale" || v.ControlURL != "https://controlplane.tailscale.com" {
		t.Errorf("control = %q %q", v.ControlServer, v.ControlURL)
	}
	if !v.Forwarding.IPv4 || !v.Forwarding.IPv6 || len(v.Warnings) != 0 || v.ClientOnTailnet {
		t.Errorf("forwarding %+v warnings %v tsClientOnTailnet %v", v.Forwarding, v.Warnings, v.ClientOnTailnet)
	}

	// Online first, then by name.
	var order []string
	for _, p := range v.Peers {
		order = append(order, p.HostName)
	}
	if strings.Join(order, ",") != "laptop,phone,nas,old-tablet" {
		t.Fatalf("order = %v", order)
	}
	laptop := tsPeerByHost(t, v, "laptop")
	if !laptop.Online || !laptop.Active || !laptop.Direct || laptop.CurAddr != "198.51.100.9:41641" || laptop.Relay != "fra" ||
		laptop.RxBytes != 123456 || laptop.TxBytes != 654321 || laptop.UserLoginName != "alice@example.com" ||
		laptop.LastSeen == 0 || laptop.LastHandshake == 0 || laptop.OS != "macOS" {
		t.Errorf("laptop = %+v", laptop)
	}
	phone := tsPeerByHost(t, v, "phone")
	if phone.Direct || phone.Relay != "ams" || phone.UserLoginName != "bob@example.com" || phone.LastHandshake != 0 {
		t.Errorf("phone (relayed, never shook hands) = %+v", phone)
	}
	nas := tsPeerByHost(t, v, "nas")
	if nas.Online || strings.Join(nas.Tags, ",") != "tag:server" || strings.Join(nas.PrimaryRoutes, ",") != "203.0.113.128/25" || !nas.ExitNodeOption {
		t.Errorf("nas = %+v", nas)
	}
	if tab := tsPeerByHost(t, v, "old-tablet"); !tab.Expired || tab.Tags == nil || tab.PrimaryRoutes == nil {
		t.Errorf("tablet = %+v", tab)
	}

	// `debug prefs` prints the node key and the network-lock key; neither is
	// a field of anything returned.
	raw := wgMustString(t, v)
	for _, secret := range []string{"privkey:", "nlpriv:", "PrivateNodeKey"} {
		if strings.Contains(raw, secret) {
			t.Errorf("a key leaked into the view: %s", secret)
		}
	}
	if strings.Contains(raw, "null") {
		t.Errorf("an empty list must be [], not null: %s", raw)
	}
}

func TestTailscaleViewOfAHeadscaleNodeUsingAnExitNode(t *testing.T) {
	s, _ := tsHost(t, "tailscale-status.json", "tailscale-prefs-headscale.json")
	v, err := s.Tailscale(context.Background(), "100.64.10.2")
	if err != nil {
		t.Fatal(err)
	}
	if v.ControlServer != "self-hosted" || v.ControlURL != "https://hs.example.com" {
		t.Errorf("control = %q %q", v.ControlServer, v.ControlURL)
	}
	if !v.Prefs.UsingExitNode || v.Prefs.ExitNodeID != "n12345" || !v.Prefs.ShieldsUp || v.Prefs.AdvertisingExitNode || len(v.Prefs.AdvertiseRoutes) != 0 {
		t.Errorf("prefs = %+v", v.Prefs)
	}
	if len(v.Warnings) != 2 {
		t.Errorf("warnings = %v: an exit node in use and shields up each deserve one", v.Warnings)
	}
	if !v.ClientOnTailnet {
		t.Error("a peer's address is on the tailnet")
	}
}

func TestControlServerOf(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"":                                   "tailscale",
		"https://controlplane.tailscale.com": "tailscale",
		"https://login.tailscale.com":        "tailscale",
		"https://hs.example.com":             "self-hosted",
		"https://hs.example.com:8080":        "self-hosted",
		"http://192.0.2.7:8080":              "self-hosted",
		"https://tailscale.com.evil.example": "self-hosted",
		"https://nottailscale.com":           "self-hosted",
		"::not a url":                        "self-hosted",
	} {
		if got := tsControlServerOf(in); got != want {
			t.Errorf("tsControlServerOf(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestClientOnTailnet(t *testing.T) {
	s, _ := tsHost(t, "tailscale-status.json", "tailscale-prefs-plain.json")
	v, err := s.Tailscale(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for client, want := range map[string]bool{
		"198.51.100.7":       false,
		"":                   false,
		"not an address":     false,
		"100.64.10.4":        true,  // a peer
		"100.127.255.254":    true,  // the CGNAT range, a peer this server has not heard of
		"100.128.0.1":        false, // just outside it
		"fd7a:115c:a1e0::77": true,
		"fd7a:115c:a1e1::77": false,
		"fd7a:115c:a1e0::1":  true, // this server's own
		"2001:db8::1":        false,
	} {
		if got := tsClientOnTailnet(client, v); got != want {
			t.Errorf("tsClientOnTailnet(%q) = %v, want %v", client, got, want)
		}
	}
}

func TestTailscaleNotInstalledAndStatusFailing(t *testing.T) {
	s := vpnService(t)
	rec := record(t, "tailscale")
	v, err := s.Tailscale(context.Background(), "")
	if err != nil || v.Installed || v.Peers == nil || v.Prefs.AdvertiseRoutes == nil || len(rec.commands()) != 0 {
		t.Fatalf("v = %+v err = %v ran %v", v, err, rec.commands())
	}
	if exit, routes := s.TailscaleNeedsForwarding(context.Background()); exit || routes {
		t.Error("an absent Tailscale needs nothing")
	}

	rec = record(t)
	rec.fail("tailscale status --json", "failed to connect to local tailscaled")
	v, err = s.Tailscale(context.Background(), "")
	if err == nil || !v.Installed || v.Running {
		t.Fatalf("v = %+v err = %v", v, err)
	}
	rec = record(t)
	rec.on("tailscale status --json", "not json")
	if _, err = s.Tailscale(context.Background(), ""); err == nil {
		t.Fatal("unreadable status should be an error")
	}
	// A prefs failure costs the prefs, not the peers.
	rec = record(t)
	rec.on("tailscale status --json", fixture(t, "tailscale-status.json")).fail("tailscale debug prefs", "unknown subcommand")
	v, err = s.Tailscale(context.Background(), "")
	if err != nil || len(v.Peers) != 4 || len(v.Warnings) != 1 {
		t.Fatalf("v.Peers=%d warnings=%v err=%v", len(v.Peers), v.Warnings, err)
	}
}

func TestTailscaleNeedsForwarding(t *testing.T) {
	s, _ := tsHost(t, "tailscale-status.json", "tailscale-prefs-exit.json")
	if exit, routes := s.TailscaleNeedsForwarding(context.Background()); !exit || !routes {
		t.Errorf("exit=%v routes=%v", exit, routes)
	}
	s, _ = tsHost(t, "tailscale-status.json", "tailscale-prefs-plain.json")
	if exit, routes := s.TailscaleNeedsForwarding(context.Background()); exit || routes {
		t.Errorf("exit=%v routes=%v", exit, routes)
	}
}

func tsBptr(b bool) *bool { return &b }

func tsSptr(s ...string) *[]string {
	if s == nil {
		s = []string{}
	}
	return &s
}

// The one command ever run to change Tailscale is `tailscale set` with both
// of its advertising flags, spelled exactly.
func TestSetTailscaleArgv(t *testing.T) {
	tests := []struct {
		name  string
		prefs string
		req   TailscaleSetRequest
		want  string
	}{
		{"exit node on", "tailscale-prefs-plain.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(true)},
			"tailscale set --advertise-exit-node=true --advertise-routes="},
		{"exit node off keeps the routes", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(false)},
			"tailscale set --advertise-exit-node=false --advertise-routes=192.0.2.0/24"},
		{"routes keep the exit node", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr("198.51.100.0/24", "192.0.2.77/24", "198.51.100.0/24", "203.0.113.9")},
			"tailscale set --advertise-exit-node=true --advertise-routes=192.0.2.0/24,198.51.100.0/24,203.0.113.9/32"},
		{"empty list withdraws every route", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr()},
			"tailscale set --advertise-exit-node=true --advertise-routes="},
		{"both at once", "tailscale-prefs-plain.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(true), AdvertiseRoutes: tsSptr("192.0.2.0/24")},
			"tailscale set --advertise-exit-node=true --advertise-routes=192.0.2.0/24"},
		{"a v6 route", "tailscale-prefs-plain.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr("2001:db8:5::/48", "192.0.2.0/24")},
			"tailscale set --advertise-exit-node=false --advertise-routes=192.0.2.0/24,2001:db8:5::/48"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := tsHost(t, "tailscale-status.json", tc.prefs)
			rec.on("tailscale set ", "")
			res, err := s.SetTailscale(context.Background(), tc.req, "198.51.100.7")
			if err != nil {
				t.Fatal(err)
			}
			var sets []string
			for _, c := range rec.commands() {
				if strings.HasPrefix(c, "tailscale set") {
					sets = append(sets, c)
				}
				for _, banned := range []string{"tailscale up", "tailscale down", "tailscale logout", "tailscale login", "tailscale serve", "tailscale funnel", "--exit-node=", "--shields-up"} {
					if strings.Contains(c, banned) {
						t.Errorf("ran %q", c)
					}
				}
			}
			if len(sets) != 1 || sets[0] != tc.want {
				t.Fatalf("ran %q, want exactly %q", sets, tc.want)
			}
			if res.Tailscale == nil || !strings.Contains(res.Note, "approve") {
				t.Errorf("result = %+v", res)
			}
			wgAssertOrder(t, rec.commands(), "tailscale debug prefs", "tailscale set", "tailscale status --json")
		})
	}
}

func TestSetTailscaleNoteForASelfHostedControlServer(t *testing.T) {
	s, rec := tsHost(t, "tailscale-status.json", "tailscale-prefs-headscale.json")
	rec.on("tailscale set ", "")
	res, err := s.SetTailscale(context.Background(), TailscaleSetRequest{AdvertiseRoutes: tsSptr("192.0.2.0/24")}, "")
	if err != nil || !strings.Contains(res.Note, "Headscale") {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestSetTailscaleRefusals(t *testing.T) {
	tests := []struct {
		name    string
		req     TailscaleSetRequest
		forward bool
		want    string
		fwdOff  bool
	}{
		{"nothing asked", TailscaleSetRequest{}, true, "nothing to change", false},
		{"v4 default route", TailscaleSetRequest{AdvertiseRoutes: tsSptr("0.0.0.0/0")}, true, "exit node", false},
		{"v6 default route", TailscaleSetRequest{AdvertiseRoutes: tsSptr("::/0")}, true, "exit node", false},
		{"the CGNAT range", TailscaleSetRequest{AdvertiseRoutes: tsSptr("100.64.0.0/10")}, true, "tailnet", false},
		{"inside the CGNAT range", TailscaleSetRequest{AdvertiseRoutes: tsSptr("100.100.0.0/16")}, true, "tailnet", false},
		{"containing the CGNAT range", TailscaleSetRequest{AdvertiseRoutes: tsSptr("100.0.0.0/8")}, true, "tailnet", false},
		{"inside the tailnet's v6 range", TailscaleSetRequest{AdvertiseRoutes: tsSptr("fd7a:115c:a1e0:ab12::/64")}, true, "tailnet", false},
		{"not a network", TailscaleSetRequest{AdvertiseRoutes: tsSptr("lan")}, true, "advertiseRoutes", false},
		{"one bad among good", TailscaleSetRequest{AdvertiseRoutes: tsSptr("192.0.2.0/24", "192.0.2.0/99")}, true, "advertiseRoutes", false},
		{"exit node with forwarding off", TailscaleSetRequest{AdvertiseExitNode: tsBptr(true)}, false, "forwarding is off", true},
		{"routes with forwarding off", TailscaleSetRequest{AdvertiseRoutes: tsSptr("192.0.2.0/24")}, false, "forwarding is off", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := tsHost(t, "tailscale-status.json", "tailscale-prefs-plain.json")
			wgSetForwarding(t, tc.forward)
			_, err := s.SetTailscale(context.Background(), tc.req, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if errors.Is(err, ErrForwardingOff) != tc.fwdOff {
				t.Errorf("forwarding refusal = %v, want %v", errors.Is(err, ErrForwardingOff), tc.fwdOff)
			}
			if rec.ran("tailscale set") {
				t.Error("a refused request ran tailscale set")
			}
		})
	}
}

func TestSetTailscaleWithdrawingNeedsNoForwarding(t *testing.T) {
	s, rec := tsHost(t, "tailscale-status.json", "tailscale-prefs-exit.json")
	rec.on("tailscale set ", "")
	wgSetForwarding(t, false)
	if _, err := s.SetTailscale(context.Background(), TailscaleSetRequest{AdvertiseExitNode: tsBptr(false), AdvertiseRoutes: tsSptr()}, ""); err != nil {
		t.Fatalf("taking an offer back must always be possible: %v", err)
	}
	if !rec.ran("tailscale set --advertise-exit-node=false --advertise-routes= ") && !rec.ran("tailscale set --advertise-exit-node=false --advertise-routes=") {
		t.Errorf("ran %v", rec.commands())
	}
}

func TestSetTailscaleWhenPrefsCannotBeReadChangesNothing(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	rec.fail("tailscale debug prefs", "denied")
	if _, err := s.SetTailscale(context.Background(), TailscaleSetRequest{AdvertiseExitNode: tsBptr(true)}, ""); err == nil {
		t.Fatal("want an error")
	}
	if rec.ran("tailscale set") {
		t.Error("the other flag cannot be sent at its current value when the current value is unknown")
	}
}

func TestSetTailscaleNotInstalled(t *testing.T) {
	s := vpnService(t)
	record(t, "tailscale")
	_, err := s.SetTailscale(context.Background(), TailscaleSetRequest{AdvertiseExitNode: tsBptr(true)}, "")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
