package netx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// The keys in the fixtures are made up for them and sign nothing. They are
// named here so the tests can assert that none of the secret ones — a private
// key, a preshared key — ever reaches a view.
const (
	fixPriv0 = "0JUJTfLY5EfB++dKcRZjbd3iXEElyMiTDNOEt2oa3oc="
	fixPub0  = "YTPzuXQ/TLno6BXuakNWIRoDjqGc/NVUgUIz+oECWK8="
	fixPriv1 = "XkwXnIMxSwp5776H/CusAsr2pFRWiRQP0bwCUWnkv7c="
	fixPeerA = "/g/7YnnY0V/8zAiEnqgFFJmDsHfdHrNVilZbxLCcgjQ="
	fixPeerB = "YicDMDytoyQTCDwFpZnM8lW9ToZcS06e6XJIV6YGdo0="
	fixPeerC = "KySM8igZqgddFKV3xW+pudALJgAHWRkPByDTwPlYAg8="
	fixPSKA  = "HZdCz5rXrKPzISpa6LQntV8XrTAwwaTX/ksmVNip0uA="
	fixPSKB  = "gU1LVTJdOXI+ETfQ9Rq4mc3N1SarjfAmMah0hfz9p5A="
	fixPSKC  = "52+QSaRhWS9v9tNVhZ8Tbma0NaAQ73hompm0B9TwKvk="
)

var fixSecrets = []string{fixPriv0, fixPriv1, fixPSKA, fixPSKB, fixPSKC}

// The moment the fixtures' handshakes are measured against: ten seconds after
// peerA's, so it is online, and long after peerC's, so that one is not.
const fixNow = 1790000010

const vpnClientsTable = `CREATE TABLE network_vpn_clients (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  iface         TEXT NOT NULL,
  public_key    TEXT NOT NULL,
  name          TEXT NOT NULL,
  kind          TEXT NOT NULL DEFAULT 'device',
  address       TEXT NOT NULL DEFAULT '',
  config_sealed TEXT NOT NULL DEFAULT '',
  created_by    TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  UNIQUE (iface, public_key)
)`

// vpnService is a Service with a real store: an in-memory SQLite holding only
// the clients table, sealed with the same stand-in the foundation's helper
// uses. Its clock, its /proc and its forwarding switch are the test's.
func vpnService(t *testing.T) *Service {
	t.Helper()
	s := testService(t)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// One connection: a second would open a second, empty in-memory database.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(vpnClientsTable); err != nil {
		t.Fatal(err)
	}
	s.db = db
	s.vpn = newVPNStore(db,
		func(v string) (string, error) { return "sealed:" + v, nil },
		func(v string) (string, error) { return strings.TrimPrefix(v, "sealed:"), nil })

	prevNow, prevNet, prevSys, prevSysRoot, prevLib := wgNow, wgProcNet, wgProcSys, wgSysRoot, wgLibModules
	t.Cleanup(func() {
		wgNow, wgProcNet, wgProcSys, wgSysRoot, wgLibModules = prevNow, prevNet, prevSys, prevSysRoot, prevLib
	})
	wgNow = func() time.Time { return time.Unix(fixNow, 0) }
	root := t.TempDir()
	wgProcNet = filepath.Join(root, "proc", "net")
	wgProcSys = filepath.Join(root, "proc", "sys")
	wgSysRoot = filepath.Join(root, "sys")
	wgLibModules = filepath.Join(root, "lib", "modules")
	wgWriteFile(t, filepath.Join(wgProcNet, "udp"), wgUDPTable(51820, 68))
	wgWriteFile(t, filepath.Join(wgProcNet, "udp6"), wgUDPTable(5353))
	wgSetForwarding(t, true)
	return s
}

// wgUDPTable is /proc/net/udp with a socket bound to each port.
func wgUDPTable(ports ...int) string {
	var b strings.Builder
	b.WriteString("  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops\n")
	for i, p := range ports {
		fmt.Fprintf(&b, "  %d: 00000000:%04X 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 12345 2 0000000000000000 0\n", i, p)
	}
	return b.String()
}

func wgWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func wgSetForwarding(t *testing.T, on bool) {
	t.Helper()
	v := "0\n"
	if on {
		v = "1\n"
	}
	wgWriteFile(t, filepath.Join(wgProcSys, "net", "ipv4", "ip_forward"), v)
	wgWriteFile(t, filepath.Join(wgProcSys, "net", "ipv6", "conf", "all", "forwarding"), v)
}

// wgInstallConf copies a fixture into the WireGuard directory.
func wgInstallConf(t *testing.T, s *Service, name, fixtureName string) string {
	t.Helper()
	path := filepath.Join(s.paths.WireGuard, name+".conf")
	wgWriteFile(t, path, fixture(t, fixtureName))
	return path
}

// wgHostReplies answers the reads of the host's addresses and routes: an
// uplink eth0 with a public address, Docker on 172.17/16, a bridge on
// 10.9.0.0/24, a static route over 10.8.0.0/24 and 10.10.0.0/16, and
// Tailscale's range in its own table.
func wgHostReplies(t *testing.T, rec *recorder) {
	t.Helper()
	rec.on("ip -j addr", fixture(t, "wg-ip-addr.json")).
		on("ip -j route show table all", fixture(t, "wg-ip-route-all.json")).
		on("ip -j -6 route show table all", fixture(t, "wg-ip-route6-all.json")).
		on("ip -j route show default", fixture(t, "wg-ip-route-default.json")).
		on("ip -j -6 route show default", "[]")
}

// wgUnitReplies answers the questions asked of wg0's unit afterwards, to read
// a tunnel back.
func wgUnitReplies(rec *recorder, name string) {
	rec.on("wg show all dump", "").
		on("systemctl is-enabled wg-quick@"+name, "enabled\n").
		on("systemctl is-active wg-quick@"+name, "active\n")
}

// wgCommitReplies answers what s.commit runs for a change to the spec.
func wgCommitReplies(rec *recorder) {
	rec.on("nft -c -f", "").
		on("systemctl daemon-reload", "").
		on("systemctl is-enabled "+UnitName, "enabled\n")
}

func wgIndexOf(cmds []string, prefix string) int {
	for i, c := range cmds {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func wgAssertOrder(t *testing.T, cmds []string, prefixes ...string) {
	t.Helper()
	last := -1
	for _, p := range prefixes {
		i := wgIndexOf(cmds, p)
		if i < 0 {
			t.Fatalf("%q was never run; ran:\n%s", p, strings.Join(cmds, "\n"))
		}
		if i < last {
			t.Fatalf("%q ran before an earlier step; ran:\n%s", p, strings.Join(cmds, "\n"))
		}
		last = i
	}
}

func wgMustSpec(t *testing.T, s *Service) *Spec {
	t.Helper()
	sp, err := s.loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestParseWGDumpNeverKeepsASecret(t *testing.T) {
	t.Parallel()
	got := parseWGDump(fixture(t, "wg-dump.txt"))
	if len(got) != 2 {
		t.Fatalf("interfaces = %d, want 2", len(got))
	}
	wg0 := got["wg0"]
	if wg0.publicKey != fixPub0 || wg0.listenPort != 51820 || len(wg0.peers) != 2 {
		t.Fatalf("wg0 = %+v", wg0)
	}
	a, b := wg0.peers[0], wg0.peers[1]
	if a.publicKey != fixPeerA || a.endpoint != "198.51.100.7:40021" || a.handshake != 1790000000 || a.rx != 123456 || a.tx != 654321 || a.keepalive != 25 {
		t.Errorf("peer A = %+v", a)
	}
	if b.endpoint != "" || b.keepalive != 0 || len(b.allowedIPs) != 2 || b.allowedIPs[1] != "192.168.77.0/24" {
		t.Errorf("peer B (none, off) = %+v", b)
	}
	// The dump prints every private and preshared key; none may survive the
	// parse, so none can reach a response whatever is later added to a view.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	dumped := fixture(t, "wg-dump.txt")
	for _, secret := range fixSecrets {
		if !strings.Contains(dumped, secret) {
			t.Fatalf("the fixture lost %s", secret)
		}
		if strings.Contains(string(raw), secret) || strings.Contains(wgSprintAll(got), secret) {
			t.Errorf("a secret key survived the parse: %s", secret)
		}
	}
}

func wgSprintAll(m map[string]*wgLiveIface) string {
	var b strings.Builder
	for _, i := range m {
		b.WriteString(i.name + i.publicKey)
		for _, p := range i.peers {
			b.WriteString(p.publicKey + p.endpoint + strings.Join(p.allowedIPs, ","))
		}
	}
	return b.String()
}

func TestParseWGDumpToleratesGarbage(t *testing.T) {
	t.Parallel()
	got := parseWGDump("\n\nwg0\tonly\ttwo\nwgX\t" + fixPeerA + "\t(none)\t(none)\t(none)\t0\t0\t0\toff\n")
	if len(got) != 0 {
		t.Fatalf("a peer of an interface that was never declared, or a short line, made %v", got)
	}
}

func wgReadView(t *testing.T, s *Service) *WireGuardView {
	t.Helper()
	v, err := s.WireGuard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func wgIfaceByName(t *testing.T, v *WireGuardView, name string) WGInterface {
	t.Helper()
	for _, i := range v.Interfaces {
		if i.Name == name {
			return i
		}
	}
	t.Fatalf("no interface %s in %+v", name, v.Interfaces)
	return WGInterface{}
}

func TestWireGuardViewJoinsFilesKernelStoreAndSpec(t *testing.T) {
	s := vpnService(t)
	wgInstallConf(t, s, "wg0", "wg-managed.conf")
	wgInstallConf(t, s, "wg1", "wg-handwritten.conf")
	wgWriteFile(t, filepath.Join(s.paths.WireGuard, "notes.txt"), "not a tunnel")
	wgWriteFile(t, filepath.Join(s.paths.WireGuard, wgRemovedDir, "wg9.conf.1"), "a removed tunnel is not a tunnel")
	wgWriteFile(t, filepath.Join(s.paths.WireGuard, "bad name.conf"), "[Interface]\n")
	// The kernel's module is on disk, not loaded: still supported.
	wgWriteFile(t, filepath.Join(wgProcSys, "kernel", "osrelease"), "6.14.0-test\n")
	wgWriteFile(t, filepath.Join(wgLibModules, "6.14.0-test", "kernel", "drivers", "net", "wireguard", "wireguard.ko.zst"), "")

	sp := emptySpec()
	vpnUpsertNAT(sp, wgOwner("wg0"), "WireGuard wg0 exit", "10.8.0.0/24", "eth0", "alice")
	if err := s.writeSpecForTest(sp); err != nil {
		t.Fatal(err)
	}
	// Peer A (Phone, id 1) has a stored configuration; Office (id 2) was
	// forgotten.
	ctx := context.Background()
	idA, err := s.vpn.Save(ctx, VPNClient{Iface: "wg0", PublicKey: fixPeerA, Name: "Phone", Kind: "device"}, "config A")
	if err != nil || idA != 1 {
		t.Fatalf("save: %v id=%d", err, idA)
	}
	idB, err := s.vpn.Save(ctx, VPNClient{Iface: "wg0", PublicKey: fixPeerB, Name: "Office", Kind: "site"}, "config B")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.vpn.Forget(ctx, "wg0", idB); err != nil {
		t.Fatal(err)
	}

	rec := record(t)
	rec.on("wg show all dump", fixture(t, "wg-dump.txt")).
		on("systemctl is-enabled wg-quick@wg0", "enabled\n").
		fail("systemctl is-enabled wg-quick@wg1", "disabled\n").
		on("systemctl is-active", "active\n")

	v := wgReadView(t, s)
	if !v.Installed || !v.Tools.Wg || !v.Tools.WgQuick || !v.Systemd || !v.Kernel || v.Package != "wireguard-tools" {
		t.Errorf("tools = %+v kernel=%v", v, v.Kernel)
	}
	if len(v.Interfaces) != 2 {
		t.Fatalf("%d interfaces, want 2 (the removed directory, notes and bad names are not tunnels)", len(v.Interfaces))
	}

	wg0 := wgIfaceByName(t, v, "wg0")
	if !wg0.Managed || !wg0.Configured || !wg0.Up || !wg0.Enabled || !wg0.Active {
		t.Errorf("wg0 state = %+v", wg0)
	}
	if wg0.PublicKey != fixPub0 || wg0.ListenPort != 51820 || wg0.MTU != 1420 || wg0.Subnet != "10.8.0.0/24" {
		t.Errorf("wg0 = %+v", wg0)
	}
	if strings.Join(wg0.Addresses, ",") != "10.8.0.1/24" || strings.Join(wg0.DNS, ",") != "1.1.1.1,1.0.0.1" || wg0.Endpoint != "203.0.113.20:51820" || !wg0.ExitNode {
		t.Errorf("wg0 settings = %+v", wg0)
	}
	if len(wg0.Peers) != 2 {
		t.Fatalf("wg0 peers = %+v", wg0.Peers)
	}
	phone, office := wg0.Peers[0], wg0.Peers[1]
	if phone.ID != 1 || phone.Name != "Phone" || phone.Kind != "device" || phone.Address != "10.8.0.2" || !phone.Online ||
		phone.RxBytes != 123456 || phone.TxBytes != 654321 || phone.Endpoint != "198.51.100.7:40021" || phone.Keepalive != 25 || !phone.HasConfig {
		t.Errorf("phone = %+v", phone)
	}
	if office.ID != 2 || office.Kind != "site" || office.Online || office.LatestHandshake != 0 || office.HasConfig ||
		office.Address != "10.8.0.3" || office.Endpoint != "198.51.100.50:51820" || len(office.AllowedIPs) != 2 {
		t.Errorf("office = %+v", office)
	}

	wg1 := wgIfaceByName(t, v, "wg1")
	if wg1.Managed || !wg1.Configured || !wg1.Up || wg1.Enabled || !wg1.Active || wg1.ExitNode {
		t.Errorf("wg1 state = %+v", wg1)
	}
	if len(wg1.Peers) != 1 {
		t.Fatalf("wg1 peers = %+v", wg1.Peers)
	}
	if p := wg1.Peers[0]; p.ID != 0 || p.Kind != "peer" || p.Online || p.Keepalive != 30 || p.Endpoint != "[2001:db8::5]:51000" {
		t.Errorf("wg1 peer = %+v", p)
	}

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range fixSecrets {
		if strings.Contains(string(raw), secret) {
			t.Errorf("a secret key is in the view: %s", secret)
		}
	}
	if strings.Contains(string(raw), "null") {
		t.Errorf("an empty list must be [], not null: %s", raw)
	}
}

// writeSpecForTest puts a spec on disk as an earlier change would have left
// it, without running a commit.
func (s *Service) writeSpecForTest(sp *Spec) error {
	return writeFileAtomic(s.specPath(), wgMustJSON(sp), 0o600)
}

func wgMustJSON(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return b
}

func TestWireGuardViewOnAHostWithNothing(t *testing.T) {
	s := vpnService(t)
	record(t, "wg", "wg-quick", "systemctl")
	v := wgReadView(t, s)
	if v.Installed || v.Kernel || v.Systemd || len(v.Interfaces) != 0 || v.Interfaces == nil {
		t.Errorf("view = %+v", v)
	}
}

func TestWireGuardKernelSupportAcceptsAModuleLoadedOrOnDisk(t *testing.T) {
	vpnService(t)
	if wgKernelSupported() {
		t.Fatal("no module and no file is not supported")
	}
	wgWriteFile(t, filepath.Join(wgSysRoot, "module", "wireguard", "version"), "1")
	if !wgKernelSupported() {
		t.Fatal("a loaded module is supported")
	}
}

func TestHostAllocation(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	wgHostReplies(t, rec)
	host, err := wgReadHostState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if host.uplink != "eth0" || host.publicV4() != "203.0.113.20" {
		t.Fatalf("uplink %q public %q", host.uplink, host.publicV4())
	}

	t.Run("subnet skips an address, a route, and a route that only contains it", func(t *testing.T) {
		// 10.8 is a route, 10.9 an address on a bridge, 10.10 inside a /16 route.
		got, err := pickWGSubnet(host, nil)
		if err != nil || got.String() != "10.11.0.0/24" {
			t.Fatalf("subnet = %v, %v", got, err)
		}
	})
	t.Run("subnet skips other tunnels that are down", func(t *testing.T) {
		got, err := pickWGSubnet(host, []wgHostPrefix{{prefix: wgMustPrefix("10.11.0.0/24"), what: "the WireGuard tunnel wg3"}})
		if err != nil || got.String() != "10.12.0.0/24" {
			t.Fatalf("subnet = %v, %v", got, err)
		}
	})
	t.Run("subnet refuses an explicit overlap and names it", func(t *testing.T) {
		for _, bad := range []string{"10.9.0.0/25", "10.0.0.0/8", "172.17.4.0/24", "10.10.5.0/24", "192.168.0.0/33", "8.8.8.0/24", "10.20.0.0/8", "2001:db8:1::/64"} {
			if _, err := s.wgSubnetFor(bad, host, nil); err == nil {
				t.Errorf("%s was accepted", bad)
			}
		}
		if _, err := s.wgSubnetFor("10.9.0.0/25", host, nil); err == nil || !strings.Contains(err.Error(), "10.9.0.0/24") {
			t.Errorf("the refusal should name what it overlaps: %v", err)
		}
		got, err := s.wgSubnetFor("10.77.3.9/24", host, nil)
		if err != nil || got.String() != "10.77.3.0/24" {
			t.Errorf("a free network was refused or not masked: %v %v", got, err)
		}
	})
	t.Run("port skips what listens and what a file claims", func(t *testing.T) {
		listening := wgUDPListening()
		if !listening[51820] || !listening[68] || !listening[5353] || listening[51821] {
			t.Fatalf("listening = %v", listening)
		}
		if got, err := pickWGPort(listening, nil); err != nil || got != 51821 {
			t.Fatalf("port = %d, %v", got, err)
		}
		if got, _ := pickWGPort(listening, map[int]string{51821: "wg1"}); got != 51822 {
			t.Fatalf("port = %d, want 51822", got)
		}
	})
	t.Run("name is the first free of wg0 to wg9", func(t *testing.T) {
		confs := map[string]*wgConf{"wg0": {}, "wg1": {}}
		if got, _ := pickWGName(confs, host); got != "wg2" {
			t.Fatalf("name = %s", got)
		}
		host.addrs["wg2"] = nil
		if got, _ := pickWGName(confs, host); got != "wg3" {
			t.Fatalf("name = %s, want a live device to count", got)
		}
		all := map[string]*wgConf{}
		for _, n := range []string{"wg0", "wg1", "wg2", "wg3", "wg4", "wg5", "wg6", "wg7", "wg8", "wg9"} {
			all[n] = &wgConf{}
		}
		if _, err := pickWGName(all, host); err == nil {
			t.Fatal("ten names taken should say so")
		}
	})
}

func TestParseWGEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
		bad      bool
	}{
		{"203.0.113.20", "203.0.113.20:51820", false},
		{"203.0.113.20:443", "203.0.113.20:443", false},
		{"vpn.example.com", "vpn.example.com:51820", false},
		{"vpn.example.com:51000", "vpn.example.com:51000", false},
		{"2001:db8::5", "[2001:db8::5]:51820", false},
		{"[2001:db8::5]:51000", "[2001:db8::5]:51000", false},
		{"", "", true},
		{"vpn.example.com:99999", "", true},
		{"bad host", "", true},
		{"a;b.example.com", "", true},
		{"host\nPostUp = id", "", true},
		{"-x.example.com", "", true},
		{"[2001:db8::5", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := parseWGEndpoint(tc.in, 51820)
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("parseWGEndpoint(%q) = %q, %v", tc.in, got, err)
			}
		})
	}
}

func wgMustPrefix(s string) netip.Prefix {
	p, err := ParsePrefix(s)
	if err != nil {
		panic(err)
	}
	return p
}

func TestCreateWireGuardServer(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	wgHostReplies(t, rec)
	rec.on("systemctl enable --now wg-quick@wg0", "")
	wgUnitReplies(rec, "wg0")
	wgCommitReplies(rec)

	res, err := s.CreateWireGuard(context.Background(), WGServerRequest{ExitNode: true}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	ifc := res.Interface
	if ifc.Name != "wg0" || ifc.ListenPort != 51821 || ifc.Subnet != "10.11.0.0/24" || ifc.MTU != 1420 ||
		ifc.Endpoint != "203.0.113.20:51821" || !ifc.ExitNode || !ifc.Managed || ifc.Peers == nil {
		t.Errorf("interface = %+v", ifc)
	}
	if strings.Join(ifc.DNS, ",") != "1.1.1.1,1.0.0.1" || strings.Join(ifc.Addresses, ",") != "10.11.0.1/24" {
		t.Errorf("interface = %+v", ifc)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v", res.Warnings)
	}

	path := filepath.Join(s.paths.WireGuard, "wg0.conf")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.HasPrefix(text, wgManagedMarker+"\n") {
		t.Errorf("the file must open with the marker:\n%s", text)
	}
	for _, want := range []string{"Address = 10.11.0.1/24\n", "ListenPort = 51821\n", "MTU = 1420\n", "SaveConfig = false\n", "# jd:endpoint=203.0.113.20:51821\n", "# jd:dns=1.1.1.1,1.0.0.1\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("file lacks %q:\n%s", want, text)
		}
	}
	for _, never := range []string{"PostUp", "PostDown", "[Peer]"} {
		if strings.Contains(text, never) {
			t.Errorf("a fresh server must not contain %q", never)
		}
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	conf := parseWGConf(text)
	priv := conf.iface().get("privatekey")
	if pub, err := wgPublicKey(priv); err != nil || pub != ifc.PublicKey {
		t.Errorf("the server key does not match its public key: %v", err)
	}
	if strings.Contains(wgMustString(t, res), priv) {
		t.Error("the private key is in the response")
	}

	sp := wgMustSpec(t, s)
	if len(sp.NAT) != 1 || sp.NAT[0].Owner != "wireguard:wg0" || sp.NAT[0].Source != "10.11.0.0/24" ||
		sp.NAT[0].Interface != "eth0" || !sp.NAT[0].Enabled || sp.NAT[0].CreatedBy != "alice" {
		t.Errorf("nat = %+v", sp.NAT)
	}

	cmds := rec.commands()
	wgAssertOrder(t, cmds,
		"ip -j addr",
		"ip -j route show table all",
		"systemctl enable --now wg-quick@wg0",
		"nft -c -f",
		"systemctl daemon-reload",
		"wg show all dump",
	)
	if rec.ran("systemctl disable") {
		t.Error("a successful create must not undo itself")
	}
}

func wgMustString(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreateWireGuardRollsBackWhenTheUnitWillNotStart(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	wgHostReplies(t, rec)
	rec.fail("systemctl enable --now wg-quick@wg0", "Job for wg-quick@wg0.service failed because the control process exited with error code.").
		on("systemctl disable --now wg-quick@wg0", "")

	_, err := s.CreateWireGuard(context.Background(), WGServerRequest{ExitNode: true}, "alice")
	if err == nil || !strings.Contains(err.Error(), "wg-quick@wg0") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(s.paths.WireGuard, "wg0.conf")); !os.IsNotExist(statErr) {
		t.Error("the file of a tunnel that did not start must be removed")
	}
	cmds := rec.commands()
	wgAssertOrder(t, cmds, "systemctl enable --now wg-quick@wg0", "systemctl disable --now wg-quick@wg0")
	if _, statErr := os.Stat(s.specPath()); !os.IsNotExist(statErr) {
		t.Error("the spec must not have been written for a tunnel that never came up")
	}
}

func TestCreateWireGuardRollsBackWhenTheExitNodeCannotBeCommitted(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	wgHostReplies(t, rec)
	rec.on("systemctl enable --now wg-quick@wg0", "").
		fail("nft -c -f", "syntax error").
		on("systemctl disable --now wg-quick@wg0", "")

	_, err := s.CreateWireGuard(context.Background(), WGServerRequest{ExitNode: true}, "alice")
	if err == nil || !strings.Contains(err.Error(), "exit node") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(s.paths.WireGuard, "wg0.conf")); !os.IsNotExist(statErr) {
		t.Error("the file must be removed when the NAT entry fails")
	}
	wgAssertOrder(t, rec.commands(), "systemctl enable --now wg-quick@wg0", "nft -c -f", "systemctl disable --now wg-quick@wg0")
}

func TestCreateWireGuardRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("without wg or wg-quick", func(t *testing.T) {
		s := vpnService(t)
		record(t, "wg-quick")
		_, err := s.CreateWireGuard(ctx, WGServerRequest{}, "a")
		var missing *UnavailableError
		if !errors.As(err, &missing) || missing.Package != "wireguard-tools" || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
		if missing.Tool != "wg-quick" {
			t.Errorf("tool = %s", missing.Tool)
		}
		record(t, "wg")
		_, err = s.CreateWireGuard(ctx, WGServerRequest{}, "a")
		if !errors.As(err, &missing) || missing.Tool != "wg" || missing.Package != "wireguard-tools" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("without systemd", func(t *testing.T) {
		s := vpnService(t)
		record(t, "systemctl")
		_, err := s.CreateWireGuard(ctx, WGServerRequest{}, "a")
		if !errors.Is(err, ErrReadOnly) {
			t.Fatalf("err = %v", err)
		}
	})
	tests := []struct {
		name string
		req  WGServerRequest
		pre  func(*testing.T, *Service)
		want string
	}{
		{"overlapping subnet", WGServerRequest{Subnet: "10.9.0.0/24", Endpoint: "vpn.example.com"}, nil, "10.9.0.0/24"},
		{"public subnet", WGServerRequest{Subnet: "8.8.8.0/24", Endpoint: "vpn.example.com"}, nil, "private"},
		{"port in use", WGServerRequest{Port: 51820, Endpoint: "vpn.example.com"}, nil, "already in use"},
		{"port claimed by a file", WGServerRequest{Port: 51821, Endpoint: "vpn.example.com"}, func(t *testing.T, s *Service) {
			wgInstallConf(t, s, "wg1", "wg-handwritten.conf")
		}, "wg1"},
		{"name taken by a file", WGServerRequest{Name: "wg1", Endpoint: "vpn.example.com"}, func(t *testing.T, s *Service) {
			wgInstallConf(t, s, "wg1", "wg-handwritten.conf")
		}, "already exists"},
		{"name taken by a device", WGServerRequest{Name: "docker0", Endpoint: "vpn.example.com"}, nil, "already exists"},
		{"bad name", WGServerRequest{Name: "wg0;reboot", Endpoint: "vpn.example.com"}, nil, "interface name"},
		{"bad endpoint", WGServerRequest{Endpoint: "bad host"}, nil, "not a host name"},
		{"bad dns", WGServerRequest{Endpoint: "vpn.example.com", DNS: []string{"resolver.example"}}, nil, "DNS"},
		{"bad mtu", WGServerRequest{Endpoint: "vpn.example.com", MTU: 100}, nil, "MTU"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := vpnService(t)
			rec := record(t)
			wgHostReplies(t, rec)
			if tc.pre != nil {
				tc.pre(t, s)
			}
			_, err := s.CreateWireGuard(ctx, tc.req, "a")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if rec.ran("systemctl") {
				t.Error("a refused request must not touch systemd")
			}
		})
	}
	t.Run("no public address and none given", func(t *testing.T) {
		s := vpnService(t)
		rec := record(t)
		wgHostReplies(t, rec)
		rec.replies = append([]reply{{prefix: "ip -j addr", out: `[{"ifname":"eth0","addr_info":[{"family":"inet","local":"192.168.1.5","prefixlen":24}]}]`}}, rec.replies...)
		_, err := s.CreateWireGuard(ctx, WGServerRequest{}, "a")
		if err == nil || !strings.Contains(err.Error(), "no public IPv4") {
			t.Fatalf("err = %v", err)
		}
	})
}

func wgAddPeerHost(t *testing.T) (*Service, *recorder) {
	t.Helper()
	s := vpnService(t)
	wgInstallConf(t, s, "wg0", "wg-managed.conf")
	rec := record(t)
	wgHostReplies(t, rec)
	rec.on("systemctl is-active wg-quick@wg0", "active\n").
		on("systemctl reload wg-quick@wg0", "").
		on("systemctl is-enabled wg-quick@wg0", "enabled\n").
		on("wg show all dump", fixture(t, "wg-dump.txt")).
		on("ip -j route get 198.51.100.7", fixture(t, "wg-ip-route-get-client.json")).
		on("ip -j route get 10.8.0.2", fixture(t, "wg-ip-route-get-tunnel.json")).
		on("ip route replace", "").
		on("ip route del", "")
	return s, rec
}

func TestAddWireGuardDevice(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	before, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))

	res, err := s.AddWireGuardPeer(context.Background(), "wg0",
		WGPeerRequest{Name: "Tablet", Kind: "device", FullTunnel: true}, "198.51.100.7", "alice")
	if err != nil {
		t.Fatal(err)
	}
	p := res.Peer
	if p.ID != 3 || p.Name != "Tablet" || p.Kind != "device" || p.Address != "10.8.0.4" || !p.HasConfig {
		t.Errorf("peer = %+v", p)
	}
	if !res.Reloaded {
		t.Error("a running interface is told about the peer")
	}

	// The client's configuration.
	for _, want := range []string{
		"[Interface]\n", "Address = 10.8.0.4/32\n", "DNS = 1.1.1.1, 1.0.0.1\n", "MTU = 1420\n",
		"[Peer]\n", "Endpoint = 203.0.113.20:51820\n", "AllowedIPs = 0.0.0.0/0, ::/0\n", "PersistentKeepalive = 25\n",
	} {
		if !strings.Contains(res.Config, want) {
			t.Errorf("client config lacks %q:\n%s", want, res.Config)
		}
	}
	serverPub, _ := wgPublicKey(fixPriv0)
	if !strings.Contains(res.Config, "PublicKey = "+serverPub+"\n") {
		t.Error("the client must be given the server's public key")
	}
	clientConf := parseWGConf(res.Config)
	clientPriv := clientConf.iface().get("privatekey")
	clientPub, err := wgPublicKey(clientPriv)
	if err != nil {
		t.Fatal(err)
	}
	psk := clientConf.peers()[0].get("presharedkey")
	if !validWGKey(psk) {
		t.Errorf("preshared key = %q", psk)
	}
	if p.PublicKey != clientPub {
		t.Error("the peer's public key must derive from the private key in its configuration")
	}

	// The server's file: the old text intact, the new block appended.
	after, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatalf("the existing file changed:\n%s", after)
	}
	added := strings.TrimPrefix(string(after), string(before))
	for _, want := range []string{"# jd:id=3\n", "# jd:name=Tablet\n", "# jd:kind=device\n", "# jd:created=2026-09-21T", "[Peer]\n",
		"PublicKey = " + clientPub + "\n", "PresharedKey = " + psk + "\n", "AllowedIPs = 10.8.0.4/32\n"} {
		if !strings.Contains(added, want) {
			t.Errorf("appended block lacks %q:\n%s", want, added)
		}
	}
	if strings.Contains(added, clientPriv) || strings.Contains(added, "PersistentKeepalive") || strings.Contains(added, "Endpoint") {
		t.Errorf("the server must not hold the client's private key, a device's keepalive or its endpoint:\n%s", added)
	}
	if st, _ := os.Stat(filepath.Join(s.paths.WireGuard, "wg0.conf")); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}

	// Reloaded live, after the file was written; a device has no routes to add.
	wgAssertOrder(t, rec.commands(), "systemctl is-active wg-quick@wg0", "systemctl reload wg-quick@wg0")
	if rec.ran("ip route") {
		t.Error("a device brings no routes")
	}

	// The store holds the configuration sealed.
	var sealed string
	if err := s.db.QueryRow(`SELECT config_sealed FROM network_vpn_clients WHERE id = 3`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if sealed != "sealed:"+res.Config {
		t.Errorf("stored = %.60q", sealed)
	}

	// The QR is a PNG of a size a phone reads.
	if !strings.HasPrefix(res.QR, "data:image/png;base64,") || len(res.QR) < 1000 {
		t.Errorf("qr = %.60s…", res.QR)
	}
	// A full tunnel on a tunnel that is not an exit node warns.
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not an exit node") {
		t.Errorf("warnings = %v", res.Warnings)
	}
	// Nothing secret is in what the page lists afterwards.
	v := wgReadView(t, s)
	listing := wgMustString(t, v)
	for _, secret := range append([]string{clientPriv, psk}, fixSecrets...) {
		if strings.Contains(listing, secret) {
			t.Errorf("a secret is in the listing: %s", secret)
		}
	}
}

func TestAddWireGuardSite(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	res, err := s.AddWireGuardPeer(context.Background(), "wg0", WGPeerRequest{
		Name: "Branch", Kind: "site",
		RemoteNetworks: []string{"192.168.55.9/24", "192.168.55.0/24"},
		ShareNetworks:  []string{"172.17.0.0/16"},
		Endpoint:       "198.51.100.60",
	}, "198.51.100.7", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if res.Peer.Kind != "site" || res.Peer.Address != "10.8.0.4" {
		t.Errorf("peer = %+v", res.Peer)
	}
	after, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	for _, want := range []string{"AllowedIPs = 10.8.0.4/32, 192.168.55.0/24\n", "Endpoint = 198.51.100.60:51820\n", "PersistentKeepalive = 25\n", "# jd:kind=site\n"} {
		if !strings.Contains(string(after), want) {
			t.Errorf("file lacks %q", want)
		}
	}
	for _, want := range []string{"ListenPort = 51820\n", "AllowedIPs = 10.8.0.0/24, 172.17.0.0/16\n", "Endpoint = 203.0.113.20:51820\n"} {
		if !strings.Contains(res.Config, want) {
			t.Errorf("site config lacks %q:\n%s", want, res.Config)
		}
	}
	if strings.Contains(res.Config, "DNS") {
		t.Error("a site router keeps its own resolver")
	}
	// A syncconf adds a peer and no routes; the networks are routed after the
	// reload, and the client path was read before and after.
	wgAssertOrder(t, rec.commands(), "systemctl reload wg-quick@wg0", "ip route replace 192.168.55.0/24 dev wg0")
	n := 0
	for _, c := range rec.commands() {
		if strings.HasPrefix(c, "ip -j route get 198.51.100.7") {
			n++
		}
	}
	if n != 2 {
		t.Errorf("the client path was read %d times, want before and after", n)
	}
}

func TestAddWireGuardPeerRefusals(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		req     WGPeerRequest
		client  string
		want    string
		guarded bool
	}{
		{"duplicate name", WGPeerRequest{Name: "phone", Kind: "device"}, "", "already exists", false},
		{"bad kind", WGPeerRequest{Name: "x", Kind: "router"}, "", "device or site", false},
		{"unsafe name", WGPeerRequest{Name: "x\nPostUp = id", Kind: "device"}, "", "name", false},
		{"empty name", WGPeerRequest{Name: " ", Kind: "device"}, "", "name", false},
		{"device with remote networks", WGPeerRequest{Name: "x", Kind: "device", RemoteNetworks: []string{"192.168.9.0/24"}}, "", "belong to a site", false},
		{"device with endpoint", WGPeerRequest{Name: "x", Kind: "device", Endpoint: "1.2.3.4:5"}, "", "belong to a site", false},
		{"site full tunnel", WGPeerRequest{Name: "x", Kind: "site", FullTunnel: true}, "", "device's setting", false},
		{"remote default route", WGPeerRequest{Name: "x", Kind: "site", RemoteNetworks: []string{"0.0.0.0/0"}}, "", "full tunnel", false},
		{"remote default route v6", WGPeerRequest{Name: "x", Kind: "site", RemoteNetworks: []string{"::/0"}}, "", "full tunnel", false},
		{"share default route", WGPeerRequest{Name: "x", Kind: "device", ShareNetworks: []string{"0.0.0.0/0"}}, "", "shareNetworks", false},
		{"bad cidr", WGPeerRequest{Name: "x", Kind: "site", RemoteNetworks: []string{"192.168.9.0/40"}}, "", "remoteNetworks", false},
		{"remote overlaps the tunnel", WGPeerRequest{Name: "x", Kind: "site", RemoteNetworks: []string{"10.8.0.128/25"}}, "", "tunnel's own network", false},
		{"remote overlaps another peer", WGPeerRequest{Name: "x", Kind: "site", RemoteNetworks: []string{"192.168.77.0/25"}}, "", "already routed to another peer", false},
		{"remote overlaps a host network", WGPeerRequest{Name: "x", Kind: "site", RemoteNetworks: []string{"172.17.5.0/24"}}, "", "docker0", true},
		{"remote overlaps the tailnet", WGPeerRequest{Name: "x", Kind: "site", RemoteNetworks: []string{"100.64.200.0/24"}}, "", "tailscale0", true},
		{"remote holds the client", WGPeerRequest{Name: "x", Kind: "site", RemoteNetworks: []string{"198.51.100.0/24"}}, "198.51.100.7", "your own address", true},
		{"bad keepalive", WGPeerRequest{Name: "x", Kind: "device", Keepalive: wgIntp(70000)}, "", "keepalive", false},
		{"bad site endpoint", WGPeerRequest{Name: "x", Kind: "site", Endpoint: "bad host"}, "", "not a host name", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, rec := wgAddPeerHost(t)
			before, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
			_, err := s.AddWireGuardPeer(ctx, "wg0", tc.req, tc.client, "a")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if errors.Is(err, ErrGuarded) != tc.guarded {
				t.Errorf("guarded = %v, want %v (%v)", errors.Is(err, ErrGuarded), tc.guarded, err)
			}
			after, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
			if string(before) != string(after) {
				t.Error("a refused peer changed the file")
			}
			if rec.ran("systemctl reload") || rec.ran("ip route replace") {
				t.Error("a refused peer changed the host")
			}
			if list, _ := s.vpn.List(ctx, "wg0"); len(list) != 0 {
				t.Errorf("a refused peer left rows: %v", list)
			}
		})
	}
	t.Run("unmanaged tunnel", func(t *testing.T) {
		s, _ := wgAddPeerHost(t)
		wgInstallConf(t, s, "wg1", "wg-handwritten.conf")
		_, err := s.AddWireGuardPeer(ctx, "wg1", WGPeerRequest{Name: "x", Kind: "device"}, "", "a")
		if !errors.Is(err, ErrNotManaged) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown tunnel", func(t *testing.T) {
		s, _ := wgAddPeerHost(t)
		_, err := s.AddWireGuardPeer(ctx, "wg7", WGPeerRequest{Name: "x", Kind: "device"}, "", "a")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a full network", func(t *testing.T) {
		s, _ := wgAddPeerHost(t)
		path := filepath.Join(s.paths.WireGuard, "wg0.conf")
		text := strings.Replace(fixture(t, "wg-managed.conf"), "Address = 10.8.0.1/24", "Address = 10.8.0.1/30", 1)
		text = strings.Replace(text, "AllowedIPs = 10.8.0.3/32, 192.168.77.0/24", "AllowedIPs = 10.8.0.3/32", 1)
		wgWriteFile(t, path, text)
		// A /30 holds the server, one client (.2, Phone) and .3 is the broadcast.
		_, err := s.AddWireGuardPeer(ctx, "wg0", WGPeerRequest{Name: "x", Kind: "device"}, "", "a")
		if err == nil || !strings.Contains(err.Error(), "taken") {
			t.Fatalf("err = %v", err)
		}
	})
}

func wgIntp(n int) *int { return &n }

func TestAddWireGuardPeerRestoresTheFileWhenReloadFails(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	rec.replies = append([]reply{{prefix: "systemctl reload wg-quick@wg0", err: errors.New("wg: Unable to modify interface"), out: "wg: Unable to modify interface"}}, rec.replies...)
	before, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	_, err := s.AddWireGuardPeer(context.Background(), "wg0", WGPeerRequest{Name: "Tablet", Kind: "device"}, "", "alice")
	if err == nil || !strings.Contains(err.Error(), "reloading wg-quick@wg0") {
		t.Fatalf("err = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	if string(before) != string(after) {
		t.Errorf("the file was not restored:\n%s", after)
	}
	if list, _ := s.vpn.List(context.Background(), "wg0"); len(list) != 0 {
		t.Errorf("the row was not removed: %v", list)
	}
}

func TestAddWireGuardPeerToAStoppedTunnelOnlyWritesTheFile(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	rec.replies = append([]reply{{prefix: "systemctl is-active wg-quick@wg0", out: "inactive\n", err: errors.New("inactive")}}, rec.replies...)
	res, err := s.AddWireGuardPeer(context.Background(), "wg0", WGPeerRequest{Name: "Tablet", Kind: "device"}, "", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if res.Reloaded || rec.ran("systemctl reload") {
		t.Error("a stopped tunnel is not reloaded")
	}
	found := false
	for _, w := range res.Warnings {
		found = found || strings.Contains(w, "not running")
	}
	if !found {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestAddWireGuardPeerUndoesRoutesThatMoveTheClientPath(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	// After the route is added the client's reply would leave through wg0.
	calls := 0
	prev := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		if strings.HasPrefix(line, "ip -j route get 198.51.100.7") {
			calls++
			if calls == 2 {
				rec.mu.Lock()
				rec.calls = append(rec.calls, line)
				rec.mu.Unlock()
				return fixture(t, "wg-ip-route-get-tunnel.json"), nil
			}
		}
		return prev(ctx, name, args...)
	}
	t.Cleanup(func() { run = prev })
	before, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	_, err := s.AddWireGuardPeer(context.Background(), "wg0",
		WGPeerRequest{Name: "Branch", Kind: "site", RemoteNetworks: []string{"192.168.55.0/24"}}, "198.51.100.7", "alice")
	if !errors.Is(err, ErrGuarded) {
		t.Fatalf("err = %v, want the guard's refusal", err)
	}
	wgAssertOrder(t, rec.commands(), "ip route replace 192.168.55.0/24 dev wg0", "ip route del 192.168.55.0/24 dev wg0")
	after, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	if string(before) != string(after) {
		t.Error("the file was not restored")
	}
}

func TestWireGuardPeerConfigShowForgetAndRemove(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	ctx := context.Background()
	res, err := s.AddWireGuardPeer(ctx, "wg0", WGPeerRequest{Name: "Tablet", Kind: "device"}, "", "alice")
	if err != nil {
		t.Fatal(err)
	}

	shown, err := s.WireGuardPeerConfig(ctx, "wg0", res.Peer.ID)
	if err != nil || shown.Config != res.Config || shown.Name != "Tablet" || !strings.HasPrefix(shown.QR, "data:image/png;base64,") {
		t.Fatalf("shown = %+v, %v", shown, err)
	}
	if _, err := s.WireGuardPeerConfig(ctx, "wg0", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown peer: %v", err)
	}
	if _, err := s.WireGuardPeerConfig(ctx, "wg1", res.Peer.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a peer is shown only under its own tunnel: %v", err)
	}

	if err := s.ForgetWireGuardPeerConfig(ctx, "wg0", res.Peer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WireGuardPeerConfig(ctx, "wg0", res.Peer.ID); !errors.Is(err, ErrForgotten) {
		t.Fatalf("a forgotten configuration: %v", err)
	}
	var sealed string
	if err := s.db.QueryRow(`SELECT config_sealed FROM network_vpn_clients WHERE id = ?`, res.Peer.ID).Scan(&sealed); err != nil || sealed != "" {
		t.Errorf("the sealed text is still stored: %q %v", sealed, err)
	}
	v := wgReadView(t, s)
	for _, p := range wgIfaceByName(t, v, "wg0").Peers {
		if p.ID == res.Peer.ID && (p.HasConfig || p.Name != "Tablet") {
			t.Errorf("forgotten peer = %+v: it stays on the server, named, without a configuration", p)
		}
	}
	if err := s.ForgetWireGuardPeerConfig(ctx, "wg0", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("forgetting nothing: %v", err)
	}

	// Removing the peer takes the block, reloads, and deletes the row.
	n := len(rec.commands())
	if err := s.RemoveWireGuardPeer(ctx, "wg0", res.Peer.ID, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
	if strings.Contains(string(b), "Tablet") || strings.Contains(string(b), res.Peer.PublicKey) {
		t.Errorf("the peer is still in the file:\n%s", b)
	}
	if string(b) != fixture(t, "wg-managed.conf") {
		t.Errorf("removing the peer should leave the file as it was:\n%s", b)
	}
	if !wgContains(rec.commands()[n:], "systemctl reload wg-quick@wg0") {
		t.Errorf("no reload after removal: %v", rec.commands()[n:])
	}
	if list, _ := s.vpn.List(ctx, "wg0"); len(list) != 0 {
		t.Errorf("rows = %v", list)
	}
}

func wgContains(cmds []string, want string) bool {
	for _, c := range cmds {
		if c == want {
			return true
		}
	}
	return false
}

func TestRemoveWireGuardPeerRoutesAndGuards(t *testing.T) {
	ctx := context.Background()
	t.Run("a site's routes are taken out with it", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		if err := s.RemoveWireGuardPeer(ctx, "wg0", 2, "198.51.100.7"); err != nil {
			t.Fatal(err)
		}
		wgAssertOrder(t, rec.commands(), "systemctl reload wg-quick@wg0", "ip route del 192.168.77.0/24 dev wg0")
		if rec.ran("ip route del 10.8.0.3") {
			t.Error("a host address has no route of its own to remove")
		}
		b, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf"))
		if strings.Contains(string(b), "Office") || !strings.Contains(string(b), "Phone") {
			t.Errorf("the wrong peer was removed:\n%s", b)
		}
	})
	t.Run("the peer that carries your own connection stays", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		err := s.RemoveWireGuardPeer(ctx, "wg0", 1, "10.8.0.2")
		if !errors.Is(err, ErrGuarded) {
			t.Fatalf("err = %v", err)
		}
		if rec.ran("systemctl reload") {
			t.Error("a refused removal must not reload")
		}
		if b, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf")); string(b) != fixture(t, "wg-managed.conf") {
			t.Error("a refused removal changed the file")
		}
	})
	t.Run("a site whose network holds your address stays", func(t *testing.T) {
		s, _ := wgAddPeerHost(t)
		if err := s.RemoveWireGuardPeer(ctx, "wg0", 2, "192.168.77.20"); !errors.Is(err, ErrGuarded) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a peer somebody else added is not addressable", func(t *testing.T) {
		s, _ := wgAddPeerHost(t)
		for _, id := range []int{0, 77} {
			if err := s.RemoveWireGuardPeer(ctx, "wg0", id, ""); !errors.Is(err, ErrNotFound) {
				t.Errorf("id %d: %v", id, err)
			}
		}
	})
	t.Run("a hand-written tunnel is not edited", func(t *testing.T) {
		s, _ := wgAddPeerHost(t)
		wgInstallConf(t, s, "wg1", "wg-handwritten.conf")
		if err := s.RemoveWireGuardPeer(ctx, "wg1", 1, ""); !errors.Is(err, ErrNotManaged) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a failed reload restores the file", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		rec.replies = append([]reply{{prefix: "systemctl reload", out: "boom", err: errors.New("boom")}}, rec.replies...)
		if err := s.RemoveWireGuardPeer(ctx, "wg0", 1, ""); err == nil {
			t.Fatal("want an error")
		}
		if b, _ := os.ReadFile(filepath.Join(s.paths.WireGuard, "wg0.conf")); string(b) != fixture(t, "wg-managed.conf") {
			t.Error("the file was not restored")
		}
	})
	t.Run("without wg-quick, wg removes the peer from the running interface", func(t *testing.T) {
		s, _ := wgAddPeerHost(t)
		rec := record(t, "wg-quick")
		rec.on("wg set wg0 peer "+fixPeerA+" remove", "").on("ip route del", "")
		if err := s.RemoveWireGuardPeer(ctx, "wg0", 1, ""); err != nil {
			t.Fatal(err)
		}
		if !rec.ran("wg set wg0 peer " + fixPeerA + " remove") {
			t.Errorf("ran %v", rec.commands())
		}
	})
}

func TestRemoveWireGuardMovesTheFileAside(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	ctx := context.Background()
	rec.on("systemctl disable --now wg-quick@wg0", "")
	wgCommitReplies(rec)
	// An exit node and two clients exist.
	sp := emptySpec()
	vpnUpsertNAT(sp, wgOwner("wg0"), "WireGuard wg0 exit", "10.8.0.0/24", "eth0", "alice")
	vpnUpsertNAT(sp, "wireguard:wg1", "WireGuard wg1 exit", "10.9.9.0/24", "eth0", "alice")
	if err := s.writeSpecForTest(sp); err != nil {
		t.Fatal(err)
	}
	for _, c := range []VPNClient{{Iface: "wg0", PublicKey: fixPeerA, Name: "Phone"}, {Iface: "wg1", PublicKey: fixPeerC, Name: "Laptop"}} {
		if _, err := s.vpn.Save(ctx, c, "x"); err != nil {
			t.Fatal(err)
		}
	}
	original := fixture(t, "wg-managed.conf")

	if err := s.RemoveWireGuard(ctx, "wg0", "198.51.100.7"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.paths.WireGuard, "wg0.conf")); !os.IsNotExist(err) {
		t.Error("the file must not stay where wg-quick reads it")
	}
	matches, _ := filepath.Glob(filepath.Join(s.paths.WireGuard, wgRemovedDir, "wg0.conf.*"))
	if len(matches) != 1 || !strings.HasSuffix(matches[0], "wg0.conf.1790000010") {
		t.Fatalf("moved to %v", matches)
	}
	if b, _ := os.ReadFile(matches[0]); string(b) != original {
		t.Error("the moved file is not the original: the server's key would be lost")
	}
	if st, _ := os.Stat(matches[0]); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	if list, _ := s.vpn.List(ctx, "wg0"); len(list) != 0 {
		t.Errorf("wg0's rows remain: %v", list)
	}
	if list, _ := s.vpn.List(ctx, "wg1"); len(list) != 1 {
		t.Errorf("another tunnel's rows were removed: %v", list)
	}
	sp = wgMustSpec(t, s)
	if len(sp.NAT) != 1 || sp.NAT[0].Owner != "wireguard:wg1" {
		t.Errorf("only wg0's NAT entry goes: %+v", sp.NAT)
	}
	wgAssertOrder(t, rec.commands(), "systemctl disable --now wg-quick@wg0", "nft -c -f")

	// And a tunnel that was removed once can be made again under its name.
	if _, err := s.readWGConf("wg0"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestRemoveWireGuardGuards(t *testing.T) {
	ctx := context.Background()
	t.Run("the tunnel that carries your connection stays", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		err := s.RemoveWireGuard(ctx, "wg0", "10.8.0.2")
		if !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "wg0") {
			t.Fatalf("err = %v", err)
		}
		if rec.ran("systemctl") {
			t.Error("nothing is stopped before the guard has answered")
		}
		if _, statErr := os.Stat(filepath.Join(s.paths.WireGuard, "wg0.conf")); statErr != nil {
			t.Error("the file must still be there")
		}
	})
	t.Run("a hand-written tunnel is not removed", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		wgInstallConf(t, s, "wg1", "wg-handwritten.conf")
		if err := s.RemoveWireGuard(ctx, "wg1", ""); !errors.Is(err, ErrNotManaged) {
			t.Fatalf("err = %v", err)
		}
		if rec.ran("systemctl disable") {
			t.Error("a hand-written tunnel was stopped")
		}
	})
	t.Run("a unit that will not stop keeps its file", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		rec.fail("systemctl disable --now wg-quick@wg0", "Failed")
		if err := s.RemoveWireGuard(ctx, "wg0", ""); err == nil {
			t.Fatal("want an error")
		}
		if _, statErr := os.Stat(filepath.Join(s.paths.WireGuard, "wg0.conf")); statErr != nil {
			t.Error("the file must still be there")
		}
	})
}

func TestSetWireGuardUpAndDown(t *testing.T) {
	ctx := context.Background()
	t.Run("down is refused on the path your connection arrives by", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		err := s.SetWireGuardUp(ctx, "wg0", false, "10.8.0.2")
		if !errors.Is(err, ErrGuarded) {
			t.Fatalf("err = %v", err)
		}
		if rec.ran("systemctl stop") {
			t.Error("the tunnel was stopped")
		}
	})
	t.Run("down stops the unit and leaves it enabled", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		rec.on("systemctl stop wg-quick@wg0", "")
		if err := s.SetWireGuardUp(ctx, "wg0", false, "198.51.100.7"); err != nil {
			t.Fatal(err)
		}
		if !rec.ran("systemctl stop wg-quick@wg0") || rec.ran("systemctl disable") {
			t.Errorf("ran %v", rec.commands())
		}
	})
	t.Run("up needs no guard", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		rec.on("systemctl start wg-quick@wg0", "")
		if err := s.SetWireGuardUp(ctx, "wg0", true, "10.8.0.2"); err != nil {
			t.Fatal(err)
		}
		if !rec.ran("systemctl start wg-quick@wg0") {
			t.Errorf("ran %v", rec.commands())
		}
	})
	t.Run("a hand-written tunnel is not started or stopped", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		wgInstallConf(t, s, "wg1", "wg-handwritten.conf")
		for _, up := range []bool{true, false} {
			if err := s.SetWireGuardUp(ctx, "wg1", up, ""); !errors.Is(err, ErrNotManaged) {
				t.Errorf("up=%v: %v", up, err)
			}
		}
		if rec.ran("systemctl start") || rec.ran("systemctl stop") {
			t.Error("a hand-written tunnel was started or stopped")
		}
	})
	t.Run("a failing stop is reported", func(t *testing.T) {
		s, rec := wgAddPeerHost(t)
		rec.fail("systemctl stop", "Failed to stop")
		if err := s.SetWireGuardUp(ctx, "wg0", false, ""); err == nil || !strings.Contains(err.Error(), "stop wg0") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSetWireGuardExit(t *testing.T) {
	ctx := context.Background()
	s, rec := wgAddPeerHost(t)
	wgCommitReplies(rec)

	res, err := s.SetWireGuardExit(ctx, "wg0", true, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Interface.ExitNode {
		t.Error("the interface should read back as an exit node")
	}
	sp := wgMustSpec(t, s)
	if len(sp.NAT) != 1 || sp.NAT[0].Source != "10.8.0.0/24" || sp.NAT[0].Interface != "eth0" || sp.NAT[0].Owner != "wireguard:wg0" {
		t.Fatalf("nat = %+v", sp.NAT)
	}
	// Turning it on twice is one entry.
	if _, err := s.SetWireGuardExit(ctx, "wg0", true, "alice"); err != nil {
		t.Fatal(err)
	}
	if sp := wgMustSpec(t, s); len(sp.NAT) != 1 {
		t.Fatalf("nat = %+v", sp.NAT)
	}
	res, err = s.SetWireGuardExit(ctx, "wg0", false, "alice")
	if err != nil || res.Interface.ExitNode {
		t.Fatalf("off: %v %+v", err, res)
	}
	if sp := wgMustSpec(t, s); len(sp.NAT) != 0 {
		t.Fatalf("nat = %+v", sp.NAT)
	}
	// Off when it was never on changes nothing and is not an error.
	if _, err := s.SetWireGuardExit(ctx, "wg0", false, "alice"); err != nil {
		t.Fatal(err)
	}

	t.Run("forwarding off is a warning", func(t *testing.T) {
		wgSetForwarding(t, false)
		res, err := s.SetWireGuardExit(ctx, "wg0", true, "alice")
		if err != nil || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Routing") {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
	t.Run("hand-written tunnels have no exit switch", func(t *testing.T) {
		wgInstallConf(t, s, "wg1", "wg-handwritten.conf")
		if _, err := s.SetWireGuardExit(ctx, "wg1", true, "a"); !errors.Is(err, ErrNotManaged) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestVPNSummaryCountsAndNamesNobody(t *testing.T) {
	s := vpnService(t)
	wgInstallConf(t, s, "wg0", "wg-managed.conf")
	wgInstallConf(t, s, "wg1", "wg-handwritten.conf")
	rec := record(t)
	rec.on("wg show all dump", fixture(t, "wg-dump.txt")).
		on("systemctl", "enabled\n").
		on("tailscale status --json", fixture(t, "tailscale-status.json")).
		on("tailscale debug prefs", fixture(t, "tailscale-prefs-exit.json"))

	sum := s.VPNSummary(context.Background())
	if w := sum.WireGuard; !w.Installed || w.Interfaces != 2 || w.Peers != 3 || w.Online != 1 || w.Rx != 123456+42 || w.Tx != 654321+43 {
		t.Errorf("wireguard = %+v", w)
	}
	if ts := sum.Tailscale; !ts.Installed || !ts.Running || ts.Peers != 4 || ts.Online != 2 || !ts.ExitNode || ts.SubnetRoutes != 1 ||
		strings.Join(ts.SelfIPs, ",") != "100.64.10.1,fd7a:115c:a1e0::1" {
		t.Errorf("tailscale = %+v", ts)
	}
	raw := wgMustString(t, sum)
	for _, leak := range append([]string{"Phone", "Office", "laptop", "alice", "srv", "example"}, fixSecrets...) {
		if strings.Contains(raw, leak) {
			t.Errorf("the summary names %q: %s", leak, raw)
		}
	}
}

func TestVPNSummaryOnAHostWithNeither(t *testing.T) {
	s := vpnService(t)
	record(t, "wg", "wg-quick", "tailscale", "systemctl")
	sum := s.VPNSummary(context.Background())
	if sum.WireGuard.Installed || sum.Tailscale.Installed || sum.Tailscale.SelfIPs == nil {
		t.Errorf("summary = %+v", sum)
	}
}
