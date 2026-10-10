package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

// vpnRecordNetwork gives the server a network module whose WireGuard
// directory is the test's own, and stand-ins for every host tool it would
// run: the tunnel is down, so nothing is reloaded or routed.
func vpnRecordNetwork(t *testing.T, s *Server) string {
	t.Helper()
	dir := t.TempDir()
	vpnFakeBin(t, map[string]string{
		"wg":        "exit 0",
		"wg-quick":  "exit 0",
		"systemctl": `case "$1" in is-active) echo inactive; exit 3;; is-enabled) echo disabled; exit 1;; *) exit 0;; esac`,
		"ip":        `printf '%s\n' '[]'`,
		"nft":       "exit 1", "iptables": "exit 1", "ip6tables": "exit 1", "ufw": "exit 1", "firewall-cmd": "exit 1",
	})
	s.modules.network = netx.New(netx.Options{
		Paths: netx.Paths{Dir: filepath.Join(dir, "network"), WireGuard: filepath.Join(dir, "wireguard"), Unit: filepath.Join(dir, "unit"), Sysctl: filepath.Join(dir, "sysctl")},
		DB:    s.Store.DB, Log: s.Log, Seal: s.Sealer.Seal, Open: s.Sealer.Open, Retention: 24 * time.Hour,
	})
	return filepath.Join(dir, "wireguard")
}

const vpnServerKey = "0JUJTfLY5EfB++dKcRZjbd3iXEElyMiTDNOEt2oa3oc="

func vpnPeerBlock(id int64, name, kind, key, allowed, extra string) string {
	return fmt.Sprintf("\n# jd:id=%d\n# jd:name=%s\n# jd:kind=%s\n[Peer]\nPublicKey = %s\nAllowedIPs = %s\n%s", id, name, kind, key, allowed, extra)
}

func vpnWriteTunnel(t *testing.T, dir, name, peers string) {
	t.Helper()
	text := "# Managed by Just Dashboard\n[Interface]\n# jd:endpoint=203.0.113.20:51820\nAddress = 10.8.0.1/24\nListenPort = 51820\nPrivateKey = " + vpnServerKey + "\n" + peers
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".conf"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWireGuardSiteEditSpendsTheBudgetOnlyToWithdraw(t *testing.T) {
	c, s := newClient(t)
	dir := vpnRecordNetwork(t, s)
	vpnWriteTunnel(t, dir, "wgsite", vpnPeerBlock(7, "Office", "site", "YicDMDytoyQTCDwFpZnM8lW9ToZcS06e6XJIV6YGdo0=", "10.8.0.3/32, 198.18.1.0/24, 198.18.2.0/24", "PersistentKeepalive = 25\n"))
	const path = "/api/v1/network/vpn/wireguard/wgsite/peers/7"
	for i := 1; i <= 30; i++ {
		w := c.do(http.MethodPatch, path, `{"name":"Office `+strconv.Itoa(i)+`"}`, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("a rename: %d %s", w.Code, w.Body.String())
		}
	}
	limited := 0
	for i := 1; i <= 40 && limited == 0; i++ {
		w := c.do(http.MethodPatch, path, `{"remoteNetworks":["198.18.1.0/24"]}`, nil)
		switch w.Code {
		case http.StatusTooManyRequests:
			limited = i
		case http.StatusOK:
		default:
			t.Fatalf("a withdrawal: %d %s", w.Code, w.Body.String())
		}
		if w := c.do(http.MethodPatch, path, `{"remoteNetworks":["198.18.1.0/24","198.18.2.0/24"]}`, nil); w.Code != http.StatusOK {
			t.Fatalf("adding it back: %d %s", w.Code, w.Body.String())
		}
	}
	if limited == 0 || limited > 15 {
		t.Fatalf("withdrawing a site's network was limited after %d calls, want it to spend the destructive budget", limited)
	}
	text, _ := os.ReadFile(filepath.Join(dir, "wgsite.conf"))
	if !strings.Contains(string(text), "# jd:name=Office 30\n") || !strings.Contains(string(text), "AllowedIPs = 10.8.0.3/32, 198.18.1.0/24, 198.18.2.0/24\n") {
		t.Fatalf("file:\n%s", text)
	}
	var action, detail string
	if err := s.Store.DB.QueryRow(`SELECT action, detail FROM audit_log WHERE action = 'network.vpn.peer.edit' ORDER BY id DESC LIMIT 1`).Scan(&action, &detail); err != nil || !strings.Contains(detail, "remoteNetworks") {
		t.Fatalf("audit = %q %q %v", action, detail, err)
	}
}

func TestWireGuardRecordRoutes(t *testing.T) {
	c, s := newClient(t)
	dir := vpnRecordNetwork(t, s)
	vpnWriteTunnel(t, dir, "wgrec", vpnPeerBlock(3, "Office", "site", "YicDMDytoyQTCDwFpZnM8lW9ToZcS06e6XJIV6YGdo0=", "10.8.0.3/32, 198.18.1.0/24", ""))
	s.modules.network.NoteWireGuard(t.Context(), "wgrec", "created", "ok", "udp 51820", "alice")

	w := c.do(http.MethodGet, "/api/v1/network/vpn/wireguard/wgrec/history?window=7d", "", nil)
	var h netx.WGHistory
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &h) != nil || !h.Recording || len(h.Events) != 1 || h.Events[0].Kind != "created" {
		t.Fatalf("history: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodGet, "/api/v1/network/vpn/wireguard/wgrec/history?window=forever", "", nil); w.Code != http.StatusBadRequest {
		t.Errorf("a bad window: %d", w.Code)
	}

	w = c.do(http.MethodPut, "/api/v1/network/vpn/wireguard/wgrec/peers/3/quota", `{"period":"month","limitBytes":10737418240}`, nil)
	var q netx.WGQuota
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &q) != nil || q.State != "ok" || q.Enforced {
		t.Fatalf("quota: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPut, "/api/v1/network/vpn/wireguard/wgrec/peers/3/quota", `{"period":"century","limitBytes":10737418240}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("a bad period: %d", w.Code)
	}
	if w := c.do(http.MethodDelete, "/api/v1/network/vpn/wireguard/wgrec/peers/3/quota", "", nil); w.Code != http.StatusNoContent {
		t.Errorf("clearing: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodDelete, "/api/v1/network/vpn/wireguard/wgrec/peers/3/quota", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("clearing what is not there: %d", w.Code)
	}

	w = c.do(http.MethodGet, "/api/v1/network/vpn/archive", "", nil)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("an empty archive: %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []string{"wg0.conf", "..%2Fwg0.conf.1", "wg0.conf.1790000000"} {
		if w := c.do(http.MethodPost, "/api/v1/network/vpn/archive/"+bad+"/restore", "", nil); w.Code != http.StatusNotFound {
			t.Errorf("restoring %q: %d %s", bad, w.Code, w.Body.String())
		}
	}
}

func TestWireGuardKillSwitchVariantIsANoStoreAuditedText(t *testing.T) {
	c, s := newClient(t)
	dir := vpnRecordNetwork(t, s)
	full := "[Interface]\nPrivateKey = " + vpnServerKey + "\nAddress = 10.8.0.2/32\n\n[Peer]\nPublicKey = YTPzuXQ/TLno6BXuakNWIRoDjqGc/NVUgUIz+oECWK8=\nEndpoint = 203.0.113.20:51820\nAllowedIPs = 0.0.0.0/0, ::/0\n"
	sealed, err := s.Sealer.Seal(full)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(`INSERT INTO network_vpn_clients (iface, public_key, name, kind, address, config_sealed, created_by, created_at) VALUES ('wgks', 'pk-laptop', 'Laptop', 'device', '10.8.0.2', ?, 'tester', 1)`, sealed)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	vpnWriteTunnel(t, dir, "wgks", vpnPeerBlock(id, "Laptop", "device", "pk-laptop", "10.8.0.2/32", ""))
	path := fmt.Sprintf("/api/v1/network/vpn/wireguard/wgks/peers/%d/config", id)

	w := c.do(http.MethodGet, path+"?variant=linux-killswitch", "", nil)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("variant: %d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
	var shown struct{ Config, QR string }
	_ = json.Unmarshal(w.Body.Bytes(), &shown)
	if shown.QR != "" || !strings.Contains(shown.Config, "PreDown = nft delete table inet jd_killswitch") {
		t.Fatalf("variant = %+v", shown)
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action = 'network.vpn.peer.config.view' ORDER BY id DESC LIMIT 1`).Scan(&detail); err != nil || !strings.Contains(detail, "linux-killswitch") {
		t.Fatalf("the variant shown is not recorded: %q %v", detail, err)
	}
	if w := c.do(http.MethodGet, path+"?variant=macos", "", nil); w.Code != http.StatusBadRequest {
		t.Errorf("an unknown variant: %d", w.Code)
	}
}
