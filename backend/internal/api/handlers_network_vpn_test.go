package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// vpnRoutes is the VPN surface and the gate in front of each route. Every
// route is system.admin, reads included (a peer list names who connects, and a
// stored configuration is a private key); a removal, `down` and forgetting a
// configuration are destructive on top.
var vpnRoutes = []struct {
	method, path string
	access       proxyAccess
}{
	{http.MethodGet, "/api/v1/network/vpn/", proxyAdmin},
	{http.MethodPost, "/api/v1/network/vpn/tailscale", proxyAdmin},
	{http.MethodPost, "/api/v1/network/vpn/wireguard/", proxyAdmin},
	{http.MethodPost, "/api/v1/network/vpn/wireguard/{iface}/up", proxyAdmin},
	{http.MethodPost, "/api/v1/network/vpn/wireguard/{iface}/exit", proxyAdmin},
	{http.MethodPost, "/api/v1/network/vpn/wireguard/{iface}/peers", proxyAdmin},
	{http.MethodGet, "/api/v1/network/vpn/wireguard/{iface}/peers/{id}/config", proxyAdmin},
	{http.MethodPost, "/api/v1/network/vpn/wireguard/{iface}/down", proxyDestructive},
	{http.MethodDelete, "/api/v1/network/vpn/wireguard/{iface}", proxyDestructive},
	{http.MethodDelete, "/api/v1/network/vpn/wireguard/{iface}/peers/{id}", proxyDestructive},
	{http.MethodDelete, "/api/v1/network/vpn/wireguard/{iface}/peers/{id}/config", proxyDestructive},
}

func TestVPNRoutesAreAdminOnlyAndRemovalsAreDestructive(t *testing.T) {
	s := testServer(t)
	h := s.Routes()
	gates := routeGates(t, h)
	want := map[proxyAccess][2]int{proxyAdmin: {1, 1}, proxyDestructive: {2, 2}}
	refused := map[auth.Role]*client{
		auth.RoleReadOnly: {t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)},
		auth.RoleLimited:  {t: t, h: h, cookie: signInAs(t, s, "limited", auth.RoleLimited)},
	}
	for _, rt := range vpnRoutes {
		key := rt.method + " " + strings.TrimSuffix(rt.path, "/")
		got, ok := gates[key]
		if !ok {
			t.Errorf("%s %s is not mounted", rt.method, rt.path)
			continue
		}
		if got != want[rt.access] {
			t.Errorf("%s %s: %d capability checks and %d rate budgets, want %d and %d",
				rt.method, rt.path, got[0], got[1], want[rt.access][0], want[rt.access][1])
		}
		for role, c := range refused {
			w := c.do(rt.method, routeParam.ReplaceAllString(rt.path, "1"), "", nil)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"forbidden"`) {
				t.Errorf("%s %s: a %s account got %d %s, want 403 forbidden", rt.method, rt.path, role, w.Code, strings.TrimSpace(w.Body.String()))
			}
		}
	}
	// And nothing under /vpn was left out of the table above.
	listed := map[string]bool{}
	for _, rt := range vpnRoutes {
		listed[rt.method+" "+strings.TrimSuffix(rt.path, "/")] = true
	}
	for key := range gates {
		if strings.Contains(key, "/network/vpn") && !listed[key] {
			t.Errorf("%s is mounted but not in the table: say who may call it", key)
		}
	}
}

// fakeBin puts stand-ins for the VPN tools first on PATH, printing recorded
// output, so the page is read end to end without touching this host's network.
func fakeBin(t *testing.T, scripts map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func netxFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "netx", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestVPNPageReadsEachPartOnItsOwn(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"wg-dump.txt", "tailscale-status.json", "tailscale-prefs-exit.json"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(netxFixture(t, f)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fakeBin(t, map[string]string{
		"wg":        `[ "$1 $2 $3" = "show all dump" ] && cat ` + dir + `/wg-dump.txt`,
		"wg-quick":  `exit 0`,
		"tailscale": `case "$1 $2" in "status --json") cat ` + dir + `/tailscale-status.json;; "debug prefs") cat ` + dir + `/tailscale-prefs-exit.json;; *) exit 9;; esac`,
	})
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/network/vpn", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		WireGuard struct {
			Installed  bool `json:"installed"`
			Interfaces []struct {
				Name  string `json:"name"`
				Up    bool   `json:"up"`
				Peers []struct {
					PublicKey string `json:"publicKey"`
					Online    bool   `json:"online"`
				} `json:"peers"`
			} `json:"interfaces"`
		} `json:"wireguard"`
		Tailscale struct {
			Installed       bool   `json:"installed"`
			Running         bool   `json:"running"`
			ControlServer   string `json:"controlServer"`
			ClientOnTailnet bool   `json:"clientOnTailnet"`
			Peers           []any  `json:"peers"`
			Prefs           struct {
				AdvertisingExitNode bool `json:"advertisingExitNode"`
			} `json:"prefs"`
		} `json:"tailscale"`
		Headscale struct {
			Installed bool  `json:"installed"`
			Nodes     []any `json:"nodes"`
		} `json:"headscale"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.WireGuard.Installed || len(got.WireGuard.Interfaces) < 2 {
		t.Errorf("wireguard = %+v", got.WireGuard)
	}
	if !got.Tailscale.Installed || !got.Tailscale.Running || got.Tailscale.ControlServer != "tailscale" || len(got.Tailscale.Peers) != 4 || !got.Tailscale.Prefs.AdvertisingExitNode {
		t.Errorf("tailscale = %+v", got.Tailscale)
	}
	if got.Headscale.Nodes == nil {
		t.Error("an absent Headscale still has an empty node list, not null")
	}
	// The dump carries private and preshared keys; none may be in the answer.
	body := w.Body.String()
	for _, secret := range []string{"privkey:", "nlpriv:"} {
		if strings.Contains(body, secret) {
			t.Errorf("a key is in the response: %s", secret)
		}
	}
	for _, line := range strings.Split(netxFixture(t, "wg-dump.txt"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 5 && strings.Contains(body, f[1]) {
			t.Errorf("an interface's private key is in the response")
		}
		if len(f) == 9 && strings.Contains(body, f[2]) {
			t.Errorf("a peer's preshared key is in the response")
		}
	}
}

func TestVPNPageSurvivesAHostWithoutTheTools(t *testing.T) {
	// A PATH with nothing on it: no wg, no tailscale, no headscale.
	t.Setenv("PATH", t.TempDir())
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/network/vpn", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["wireguard"]["installed"] != false || got["tailscale"]["installed"] != false || got["headscale"]["installed"] != false {
		t.Errorf("answer = %v", got)
	}
	if got["wireguard"]["package"] != "wireguard-tools" {
		t.Errorf("the page needs the package to offer: %v", got["wireguard"])
	}
}

func TestTailscaleSetRefusesWhatCannotBeAnExitNodeRoute(t *testing.T) {
	fakeBin(t, map[string]string{"tailscale": `exit 9`})
	c, _ := newClient(t)
	for name, body := range map[string]string{
		"the default route": `{"advertiseRoutes":["0.0.0.0/0"]}`,
		"the CGNAT range":   `{"advertiseRoutes":["100.64.0.0/10"]}`,
		"nothing":           `{}`,
		"an unknown field":  `{"exitNode":true}`,
	} {
		w := c.do(http.MethodPost, "/api/v1/network/vpn/tailscale", body, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d %s, want 400", name, w.Code, w.Body.String())
		}
	}
}

func TestWireGuardRoutesRefuseBadInterfaceNamesAndPeerIds(t *testing.T) {
	c, _ := newClient(t)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/network/vpn/wireguard/wg0%3Bid/up", ""},
		{http.MethodGet, "/api/v1/network/vpn/wireguard/wg0/peers/abc/config", ""},
		{http.MethodGet, "/api/v1/network/vpn/wireguard/wg0/peers/0/config", ""},
		{http.MethodDelete, "/api/v1/network/vpn/wireguard/wg0/peers/-3", ""},
		{http.MethodDelete, "/api/v1/network/vpn/wireguard/wg0/peers/x/config", ""},
	} {
		w := c.do(tc.method, tc.path, tc.body, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: got %d %s, want 400", tc.method, tc.path, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

func TestWireGuardPeerConfigRoute(t *testing.T) {
	c, s := newClient(t)
	const config = "[Interface]\nPrivateKey = AAAA\nAddress = 10.8.0.2/32\n"
	sealed, err := s.Sealer.Seal(config)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(pub, sealedConfig string) int64 {
		res, err := s.Store.DB.Exec(`INSERT INTO network_vpn_clients (iface, public_key, name, kind, address, config_sealed, created_by, created_at)
			VALUES ('wgtest', ?, 'Phone', 'device', '10.8.0.2', ?, 'tester', 1)`, pub, sealedConfig)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	kept := insert("pk-kept", sealed)
	forgotten := insert("pk-forgotten", "")
	path := func(id int64, suffix string) string {
		return "/api/v1/network/vpn/wireguard/wgtest/peers/" + strconv.FormatInt(id, 10) + suffix
	}

	w := c.do(http.MethodGet, path(kept, "/config"), "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q: a private key must not be cached", cc)
	}
	var shown struct{ Name, Config, QR string }
	if err := json.Unmarshal(w.Body.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.Config != config || shown.Name != "Phone" || !strings.HasPrefix(shown.QR, "data:image/png;base64,") {
		t.Errorf("shown = %+v", shown)
	}

	w = c.do(http.MethodGet, path(forgotten, "/config"), "", nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"forgotten"`) {
		t.Errorf("a forgotten configuration: %d %s, want 404 forgotten", w.Code, w.Body.String())
	}
	w = c.do(http.MethodGet, "/api/v1/network/vpn/wireguard/wgtest/peers/99999/config", "", nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"not_found"`) {
		t.Errorf("an unknown peer: %d %s", w.Code, w.Body.String())
	}

	// Forgetting empties the sealed text and leaves the peer's row.
	w = c.do(http.MethodDelete, path(kept, "/config"), "", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("forget: %d %s", w.Code, w.Body.String())
	}
	var stored string
	if err := s.Store.DB.QueryRow(`SELECT config_sealed FROM network_vpn_clients WHERE id = ?`, kept).Scan(&stored); err != nil || stored != "" {
		t.Errorf("sealed = %q %v", stored, err)
	}
	w = c.do(http.MethodGet, path(kept, "/config"), "", nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"forgotten"`) {
		t.Errorf("after forgetting: %d %s", w.Code, w.Body.String())
	}
}
