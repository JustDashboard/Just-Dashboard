package netx

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestSetTailscaleCheckedAuthorizesOnlyWithdrawals(t *testing.T) {
	denied := errors.New("withdrawal denied")
	for _, tc := range []struct {
		name     string
		prefs    string
		req      TailscaleSetRequest
		withdraw bool
	}{
		{"exit withdrawal", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(false)}, true},
		{"route withdrawal", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr()}, true},
		{"route replacement", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr("198.51.100.0/24")}, true},
		{"route addition", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr("192.0.2.0/24", "198.51.100.0/24")}, false},
		{"exit addition", "tailscale-prefs-plain.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(true)}, false},
		{"already absent exit", "tailscale-prefs-plain.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(false)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := tsHost(t, "tailscale-status.json", tc.prefs)
			rec.on("tailscale set ", "")
			called := false
			_, err := s.SetTailscaleChecked(context.Background(), tc.req, "", func() error {
				called = true
				if s.mu.TryLock() {
					s.mu.Unlock()
					t.Error("withdrawal authorized outside the mutation lock")
				}
				return denied
			})
			if called != tc.withdraw || errors.Is(err, denied) != tc.withdraw {
				t.Fatalf("authorization called=%t, err=%v; withdrawal=%t", called, err, tc.withdraw)
			}
			if !tc.withdraw && err != nil {
				t.Fatal(err)
			}
			if rec.ran("tailscale set") == tc.withdraw {
				t.Fatalf("mutation ran=%t after authorization called=%t", rec.ran("tailscale set"), called)
			}
		})
	}
}

func TestSetTailscaleCheckedRechecksAfterConcurrentAddition(t *testing.T) {
	s, _ := tsHost(t, "tailscale-status.json", "tailscale-prefs-plain.json")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstInSet, releaseFirst := make(chan struct{}), make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(releaseFirst) }) })
	var stateMu sync.Mutex
	prefs := tsPrefsJSON{AdvertiseRoutes: []string{"192.0.2.0/24"}}
	sets := 0
	fallback := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "tailscale" && strings.Join(args, " ") == "debug prefs" {
			stateMu.Lock()
			defer stateMu.Unlock()
			out, err := json.Marshal(prefs)
			return string(out), err
		}
		if name == "tailscale" && len(args) > 0 && args[0] == "set" {
			stateMu.Lock()
			sets++
			n := sets
			stateMu.Unlock()
			if n == 1 {
				close(firstInSet)
				select {
				case <-releaseFirst:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			stateMu.Lock()
			prefs.AdvertiseRoutes = strings.Split(strings.TrimPrefix(args[2], "--advertise-routes="), ",")
			stateMu.Unlock()
			return "", nil
		}
		return fallback(ctx, name, args...)
	}
	replace := TailscaleSetRequest{AdvertiseRoutes: tsSptr("192.0.2.0/24")}
	if s.TailscaleWithdraws(ctx, replace) {
		t.Fatal("replacing the initial routes should not withdraw an offer yet")
	}
	firstResult := make(chan error, 1)
	go func() {
		_, err := s.SetTailscaleChecked(ctx, TailscaleSetRequest{AdvertiseRoutes: tsSptr("192.0.2.0/24", "198.51.100.0/24")}, "", func() error {
			return errors.New("addition incorrectly requires withdrawal authorization")
		})
		firstResult <- err
	}()
	select {
	case <-firstInSet:
	case <-ctx.Done():
		t.Fatal("addition did not reach tailscale set")
	}
	denied := errors.New("withdrawal denied")
	secondStarted, secondResult := make(chan struct{}), make(chan error, 1)
	go func() {
		close(secondStarted)
		_, err := s.SetTailscaleChecked(ctx, replace, "", func() error { return denied })
		secondResult <- err
	}()
	<-secondStarted
	release.Do(func() { close(releaseFirst) })
	for _, result := range []struct {
		ch   <-chan error
		want error
	}{{firstResult, nil}, {secondResult, denied}} {
		select {
		case err := <-result.ch:
			if !errors.Is(err, result.want) {
				t.Fatalf("mutation returned %v, want %v", err, result.want)
			}
		case <-ctx.Done():
			t.Fatal("concurrent mutation did not finish")
		}
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	if sets != 1 || strings.Join(prefs.AdvertiseRoutes, ",") != "192.0.2.0/24,198.51.100.0/24" {
		t.Fatalf("denied replacement changed offers: sets=%d, routes=%v", sets, prefs.AdvertiseRoutes)
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

// While the daemon is up and its preferences cannot be read, what this server
// offers is unknown, and forwarding must not be allowed off on a guess.
func TestTailscaleNeedsForwardingFailsClosedWhenPrefsAreUnreadable(t *testing.T) {
	tests := []struct {
		name       string
		status     func(*recorder)
		wantExit   bool
		wantSubnet bool
	}{
		{"running", func(r *recorder) { r.on("tailscale status --json", `{"BackendState":"Running"}`) }, true, true},
		{"starting", func(r *recorder) { r.on("tailscale status --json", `{"BackendState":"Starting"}`) }, true, true},
		{"status unreadable", func(r *recorder) { r.on("tailscale status --json", "garbled") }, true, true},
		{"stopped", func(r *recorder) { r.on("tailscale status --json", `{"BackendState":"Stopped"}`) }, false, false},
		{"needs login", func(r *recorder) { r.on("tailscale status --json", `{"BackendState":"NeedsLogin"}`) }, false, false},
		{"both reads fail", func(r *recorder) { r.fail("tailscale status --json", "permission denied") }, true, true},
		{"state absent", func(r *recorder) { r.on("tailscale status --json", `{}`) }, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := vpnService(t)
			rec := record(t)
			rec.fail("tailscale debug prefs", "permission denied")
			tc.status(rec)
			exit, routes := s.TailscaleNeedsForwarding(context.Background())
			if exit != tc.wantExit || routes != tc.wantSubnet {
				t.Errorf("exit=%v routes=%v, want %v %v", exit, routes, tc.wantExit, tc.wantSubnet)
			}
		})
	}
}

func TestTailscaleWithdraws(t *testing.T) {
	tests := []struct {
		name  string
		prefs string
		req   TailscaleSetRequest
		want  bool
	}{
		{"turn the exit node off", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(false)}, true},
		{"keep the exit node on", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(true)}, false},
		{"turn off what was never on", "tailscale-prefs-plain.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(false)}, false},
		{"turn the exit node on", "tailscale-prefs-plain.json", TailscaleSetRequest{AdvertiseExitNode: tsBptr(true)}, false},
		{"withdraw every route", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr()}, true},
		{"withdraw the only route by naming another", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr("198.51.100.0/24")}, true},
		{"add a route and keep the one there", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr("198.51.100.0/24", "192.0.2.9/24")}, false},
		{"the same routes again", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr("192.0.2.0/24")}, false},
		{"empty list when there are none", "tailscale-prefs-plain.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr()}, false},
		{"routes added to none", "tailscale-prefs-plain.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr("192.0.2.0/24")}, false},
		{"nothing asked", "tailscale-prefs-exit.json", TailscaleSetRequest{}, false},
		{"a malformed list changes nothing, so it withdraws nothing", "tailscale-prefs-exit.json", TailscaleSetRequest{AdvertiseRoutes: tsSptr("lan")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := tsHost(t, "tailscale-status.json", tc.prefs)
			if got := s.TailscaleWithdraws(context.Background(), tc.req); got != tc.want {
				t.Errorf("withdraws = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("preferences that cannot be read count as withdrawing", func(t *testing.T) {
		s := vpnService(t)
		rec := record(t)
		rec.fail("tailscale debug prefs", "denied")
		if !s.TailscaleWithdraws(context.Background(), TailscaleSetRequest{AdvertiseRoutes: tsSptr("192.0.2.0/24")}) {
			t.Error("an unknown current state must fail closed")
		}
	})
}

// The page is written from these keys; a rename pass once turned
// "clientOnTailnet" into "tsClientOnTailnet" inside a tag. Every tag of every
// view is lower camel case and none starts with one of this package's own
// identifier prefixes.
func TestVPNJSONKeysAreTheContract(t *testing.T) {
	t.Parallel()
	keys := func(v any) []string {
		var out []string
		var walk func(rt reflect.Type)
		walk = func(rt reflect.Type) {
			for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map {
				rt = rt.Elem()
			}
			if rt.Kind() != reflect.Struct {
				return
			}
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
				if tag == "" || tag == "-" {
					t.Errorf("%s.%s has no json tag", rt.Name(), f.Name)
					continue
				}
				out = append(out, tag)
				walk(f.Type)
			}
		}
		walk(reflect.TypeOf(v))
		return out
	}
	camel := regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)
	prefixed := regexp.MustCompile(`^(wg|ts|vpn|headscale)[A-Z]`)
	for _, v := range []any{
		WireGuardView{}, TailscaleView{}, HeadscaleView{}, VPNSummary{},
		WGServerRequest{}, WGServerResult{}, WGPeerRequest{}, WGPeerResult{}, WGPeerConfig{}, TailscaleSetRequest{}, TailscaleSetResult{},
	} {
		for _, k := range keys(v) {
			if !camel.MatchString(k) {
				t.Errorf("%T: %q is not lower camel case", v, k)
			}
			if prefixed.MatchString(k) && k != "wgQuick" {
				t.Errorf("%T: %q looks like an identifier the rename pass reached", v, k)
			}
		}
	}
	want := map[string][]string{
		"TailscaleView": {"clientOnTailnet", "magicDnsSuffix", "controlServer", "controlUrl", "backendState", "authUrl", "forwarding", "advertisingExitNode", "usingExitNode", "routeAll", "corpDns", "shieldsUp", "tailscaleIps", "exitNodeOption", "primaryRoutes", "userLoginName", "lastHandshake"},
		"VPNSummary":    {"selfIps", "subnetRoutes", "exitNode", "wireguard", "tailscale"},
		"WireGuardView": {"wgQuick", "latestHandshake", "allowedIps", "publicKey", "listenPort", "hasConfig", "exitNode", "configured"},
	}
	for name, list := range want {
		var v any
		switch name {
		case "TailscaleView":
			v = TailscaleView{}
		case "VPNSummary":
			v = VPNSummary{}
		default:
			v = WireGuardView{}
		}
		have := map[string]bool{}
		for _, k := range keys(v) {
			have[k] = true
		}
		for _, k := range list {
			if !have[k] {
				t.Errorf("%s lost the key %q", name, k)
			}
		}
	}
}
