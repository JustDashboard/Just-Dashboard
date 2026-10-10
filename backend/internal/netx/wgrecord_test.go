package netx

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// wgRecordTables mirror the store's WireGuard record schema for the tests'
// in-memory database (store.networkWireGuardSchema).
const wgRecordTables = `
CREATE TABLE network_wg_samples (
  iface TEXT NOT NULL, public_key TEXT NOT NULL, ts INTEGER NOT NULL,
  rx INTEGER NOT NULL DEFAULT 0, tx INTEGER NOT NULL DEFAULT 0,
  rx_delta INTEGER NOT NULL DEFAULT 0, tx_delta INTEGER NOT NULL DEFAULT 0,
  span INTEGER NOT NULL DEFAULT 0, handshake INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (iface, public_key, ts)
);
CREATE TABLE network_wg_usage (
  iface TEXT NOT NULL, public_key TEXT NOT NULL, day INTEGER NOT NULL,
  rx INTEGER NOT NULL DEFAULT 0, tx INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (iface, public_key, day)
);
CREATE TABLE network_wg_endpoints (
  iface TEXT NOT NULL, public_key TEXT NOT NULL, endpoint TEXT NOT NULL,
  first_seen INTEGER NOT NULL DEFAULT 0, last_seen INTEGER NOT NULL DEFAULT 0,
  observations INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (iface, public_key, endpoint)
);
CREATE TABLE network_wg_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, iface TEXT NOT NULL, ts INTEGER NOT NULL DEFAULT 0,
  kind TEXT NOT NULL, outcome TEXT NOT NULL DEFAULT 'ok', peer_key TEXT NOT NULL DEFAULT '',
  peer_name TEXT NOT NULL DEFAULT '', actor TEXT NOT NULL DEFAULT '', detail TEXT NOT NULL DEFAULT ''
);
CREATE TABLE network_wg_quotas (
  iface TEXT NOT NULL, public_key TEXT NOT NULL, period TEXT NOT NULL DEFAULT 'month',
  limit_bytes INTEGER NOT NULL DEFAULT 0, created_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (iface, public_key)
);
`

func wgOneLivePeer(iface, key, endpoint string, rx, tx uint64, handshake int64, keepalive int) map[string]*wgLiveIface {
	return map[string]*wgLiveIface{iface: {name: iface, peers: []wgLivePeer{{
		publicKey: key, endpoint: endpoint, rx: rx, tx: tx, handshake: handshake, keepalive: keepalive,
	}}}}
}

func TestWireGuardRecordKeepsTrendsUsageEndpointsAndStaleTransitions(t *testing.T) {
	s := vpnService(t)
	ctx := context.Background()
	t0 := time.Unix(fixNow, 0)
	steps := []struct {
		at        time.Duration
		rx, tx    uint64
		handshake int64
		endpoint  string
	}{
		// The first reading has no baseline and counts nothing.
		{0, 1000, 2000, fixNow - 10, "198.51.100.50:51820"},
		{5 * time.Minute, 4000, 2600, fixNow + 290, "198.51.100.50:51820"},
		// No handshake for over three minutes from a peer that keeps its
		// session alive; it is seen from a new address.
		{10 * time.Minute, 4100, 2700, fixNow + 290, "203.0.113.9:4000"},
		// The interface restarted: its counters began again.
		{15 * time.Minute, 500, 100, fixNow + 890, "203.0.113.9:4000"},
	}
	for _, step := range steps {
		if err := s.wg.observe(ctx, t0.Add(step.at), wgOneLivePeer("wg0", fixPeerB, step.endpoint, step.rx, step.tx, step.handshake, 25)); err != nil {
			t.Fatal(err)
		}
	}
	wgNow = func() time.Time { return t0.Add(15 * time.Minute) }

	h, err := s.WireGuardHistory(ctx, "wg0", fixPeerB, "24h")
	if err != nil {
		t.Fatal(err)
	}
	if !h.Recording || h.Bucket != 300 || len(h.Points) != 4 {
		t.Fatalf("history = %+v", h)
	}
	var rx, tx float64
	for _, p := range h.Points {
		rx += p.Rx * float64(h.Bucket)
		tx += p.Tx * float64(h.Bucket)
	}
	if rx != 3000+100+500 || tx != 600+100+100 {
		t.Errorf("trend moved rx %v tx %v, want the deltas with the reset counted from zero", rx, tx)
	}
	if len(h.Usage) != 1 || h.Usage[0].Rx != 3600 || h.Usage[0].Tx != 800 {
		t.Errorf("usage = %+v", h.Usage)
	}
	if len(h.Endpoints) != 2 || h.Endpoints[0].Endpoint != "203.0.113.9:4000" || h.Endpoints[0].Observations != 2 ||
		h.Endpoints[0].FirstSeen != t0.Add(10*time.Minute).Unix() || h.Endpoints[1].Endpoint != "198.51.100.50:51820" {
		t.Errorf("endpoints = %+v", h.Endpoints)
	}
	if len(h.Events) != 2 || h.Events[0].Kind != "handshake_recovered" || h.Events[0].Outcome != wgOutcomeRecovered ||
		h.Events[1].Kind != "handshake_stale" || h.Events[1].Outcome != wgOutcomeDegraded || h.Events[1].Peer != fixPeerB {
		t.Errorf("events = %+v", h.Events)
	}

	// The tunnel's own trend is every peer's together.
	all, err := s.WireGuardHistory(ctx, "wg0", "", "7d")
	if err != nil || all.Bucket != 3600 || len(all.Points) == 0 || len(all.Endpoints) != 0 {
		t.Fatalf("tunnel history = %+v %v", all, err)
	}
	for _, bad := range []struct{ iface, peer, window string }{
		{"wg0", fixPeerB, "1y"}, {"wg0", "not-a-key", "24h"}, {"bad name!", "", "24h"},
	} {
		if _, err := s.WireGuardHistory(ctx, bad.iface, bad.peer, bad.window); err == nil {
			t.Errorf("%+v was read", bad)
		}
	}
}

func TestWireGuardRecordWithoutRetentionKeepsNoTrafficButStillNotices(t *testing.T) {
	s := vpnService(t)
	s.wg = newWGRecord(s.db, s.wg.log, 0)
	ctx := context.Background()
	t0 := time.Unix(fixNow, 0)
	_ = s.wg.observe(ctx, t0, wgOneLivePeer("wg0", fixPeerB, "", 10, 10, fixNow, 25))
	_ = s.wg.observe(ctx, t0.Add(10*time.Minute), wgOneLivePeer("wg0", fixPeerB, "198.51.100.50:51820", 99, 99, fixNow, 25))
	var samples, endpoints int
	_ = s.db.QueryRow(`SELECT count(*) FROM network_wg_samples`).Scan(&samples)
	_ = s.db.QueryRow(`SELECT count(*) FROM network_wg_endpoints`).Scan(&endpoints)
	if samples != 0 || endpoints != 0 {
		t.Fatalf("a zero retention recorded %d samples and %d endpoints", samples, endpoints)
	}
	if events, _ := s.wg.events(ctx, "wg0", "", 10); len(events) != 1 || events[0].Kind != "handshake_stale" {
		t.Fatalf("a stale handshake must still be noticed: %+v", events)
	}
	wgNow = func() time.Time { return t0.Add(10 * time.Minute) }
	if h, err := s.WireGuardHistory(ctx, "wg0", fixPeerB, "6h"); err != nil || h.Recording || len(h.Points) != 0 {
		t.Fatalf("history = %+v %v", h, err)
	}
}

func TestWireGuardRecordPrunesAndBoundsWhatItKeeps(t *testing.T) {
	s := vpnService(t)
	ctx := context.Background()
	t0 := time.Unix(fixNow, 0)
	for i := range wgEndpointsPerPeer + 5 {
		at := t0.Add(time.Duration(i) * time.Minute)
		endpoint := fmt.Sprintf("198.51.100.%d:5000", i+1)
		if err := s.wg.observe(ctx, at, wgOneLivePeer("wg0", fixPeerB, endpoint, uint64(i), uint64(i), 0, 0)); err != nil {
			t.Fatal(err)
		}
	}
	var endpoints int
	_ = s.db.QueryRow(`SELECT count(*) FROM network_wg_endpoints`).Scan(&endpoints)
	if endpoints != wgEndpointsPerPeer {
		t.Fatalf("kept %d endpoints, want the newest %d", endpoints, wgEndpointsPerPeer)
	}
	for i := range wgEventsPerTunnel + 3 {
		s.wg.note(ctx, WGEvent{Iface: "wg0", Kind: "up", Detail: strconv.Itoa(i)})
	}
	if events, _ := s.wg.events(ctx, "wg0", "", 1000); len(events) != wgEventsPerTunnel || events[0].Detail != strconv.Itoa(wgEventsPerTunnel+2) {
		t.Fatalf("kept %d events", len(events))
	}
	if err := s.wg.prune(ctx, t0.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var samples int
	_ = s.db.QueryRow(`SELECT count(*) FROM network_wg_samples`).Scan(&samples)
	if samples != 0 {
		t.Fatalf("samples older than the retention remain: %d", samples)
	}
	long := strings.Repeat("x", wgEventDetailMax*2)
	s.wg.note(ctx, WGEvent{Iface: "wg9", Kind: "created", Detail: long})
	if events, _ := s.wg.events(ctx, "wg9", "", 1); len(events) != 1 || len(events[0].Detail) != wgEventDetailMax || events[0].Outcome != wgOutcomeOK {
		t.Fatalf("event = %+v", events)
	}
}

func TestWireGuardHandshakeStates(t *testing.T) {
	for _, tc := range []struct {
		handshake, at int64
		expects       bool
		want          string
	}{
		{0, 100, true, "never"},
		{1000, 1100, true, "online"},
		{1000, 1000 + wgOnlineWithin + 1, true, "stale"},
		{1000, 1000 + wgOnlineWithin + 1, false, "idle"},
	} {
		if got := wgHandshakeState(tc.handshake, tc.at, tc.expects); got != tc.want {
			t.Errorf("%+v = %s", tc, got)
		}
	}
}

func TestWireGuardQuotasMeasureUsageAndOnlyAlert(t *testing.T) {
	s, _ := wgAddPeerHost(t)
	ctx := context.Background()
	if _, err := s.SetWireGuardPeerQuota(ctx, "wg0", 2, WGQuotaRequest{Period: "year", LimitBytes: 1 << 30}, "alice"); err == nil {
		t.Fatal("an unknown period was accepted")
	}
	if _, err := s.SetWireGuardPeerQuota(ctx, "wg0", 2, WGQuotaRequest{Period: "month", LimitBytes: 10}, "alice"); err == nil {
		t.Fatal("a budget below 1 MiB was accepted")
	}
	if _, err := s.SetWireGuardPeerQuota(ctx, "wg0", 99, WGQuotaRequest{Period: "month", LimitBytes: 1 << 20}, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown peer: %v", err)
	}
	q, err := s.SetWireGuardPeerQuota(ctx, "wg0", 2, WGQuotaRequest{Period: "month", LimitBytes: 1 << 20}, "alice")
	if err != nil || q.State != "ok" || q.UsedBytes != 0 || q.Enforced || q.CreatedBy != "alice" {
		t.Fatalf("quota = %+v %v", q, err)
	}
	day := wgNow().UTC().Unix() / 86400
	_, _ = s.db.Exec(`INSERT INTO network_wg_usage (iface, public_key, day, rx, tx) VALUES ('wg0', ?, ?, 600000, 300000)`, fixPeerB, day)
	// Usage from a previous month does not count against this one.
	_, _ = s.db.Exec(`INSERT INTO network_wg_usage (iface, public_key, day, rx, tx) VALUES ('wg0', ?, ?, 9000000, 0)`, fixPeerB, day-40)
	quotas, _ := s.wg.quotas(ctx, "wg0")
	if q := quotas[fixPeerB]; q.State != "warning" || q.UsedBytes != 900000 {
		t.Fatalf("quota at 86%% = %+v", q)
	}
	_, _ = s.db.Exec(`UPDATE network_wg_usage SET rx = rx + 200000 WHERE day = ?`, day)
	v := wgReadView(t, s)
	office := wgPeerByName(t, wgIfaceByName(t, v, "wg0"), "Office")
	if office.Quota == nil || office.Quota.State != "exceeded" {
		t.Fatalf("office quota = %+v", office.Quota)
	}
	alerts := wgIfaceByName(t, v, "wg0").Alerts
	found := false
	for _, a := range alerts {
		found = found || (a.Kind == "quota_exceeded" && a.Peer == fixPeerB && strings.Contains(a.Message, "does not disconnect"))
	}
	if !found {
		t.Fatalf("alerts = %+v", alerts)
	}
	if err := s.ClearWireGuardPeerQuota(ctx, "wg0", 2, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearWireGuardPeerQuota(ctx, "wg0", 2, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clearing twice: %v", err)
	}
	events, _ := s.wg.events(ctx, "wg0", fixPeerB, 10)
	if len(events) != 2 || events[0].Kind != "quota_cleared" || events[1].Kind != "quota_set" {
		t.Fatalf("events = %+v", events)
	}
	s.wg = newWGRecord(s.db, s.wg.log, 0)
	_, _ = s.SetWireGuardPeerQuota(ctx, "wg0", 2, WGQuotaRequest{Period: "day", LimitBytes: 1 << 20}, "alice")
	if quotas, _ := s.wg.quotas(ctx, "wg0"); quotas[fixPeerB].State != "unmeasured" {
		t.Fatalf("a budget without a record = %+v", quotas[fixPeerB])
	}
}

func TestWireGuardPeriodStarts(t *testing.T) {
	// Thursday 2026-10-08 14:13 UTC.
	now := time.Date(2026, 10, 8, 14, 13, 0, 0, time.UTC)
	for period, want := range map[string]string{
		"day": "2026-10-08", "week": "2026-10-05", "month": "2026-10-01",
	} {
		got, err := wgPeriodStart(period, now)
		if err != nil || got.Format("2006-01-02") != want || got.Hour() != 0 {
			t.Errorf("%s = %v %v", period, got, err)
		}
	}
}

func TestWireGuardStaleSitesRaiseAlerts(t *testing.T) {
	s, rec := wgAddPeerHost(t)
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "wg show all dump", out: strings.Replace(fixture(t, "wg-dump.txt"),
		"(none)\t10.8.0.3/32,192.168.77.0/24\t0\t0\t0\toff", "198.51.100.50:51820\t10.8.0.3/32,192.168.77.0/24\t1789999000\t10\t10\t25", 1)}}, rec.replies...)
	rec.mu.Unlock()
	ifc := wgIfaceByName(t, wgReadView(t, s), "wg0")
	office, phone := wgPeerByName(t, ifc, "Office"), wgPeerByName(t, ifc, "Phone")
	if office.HandshakeState != "stale" || phone.HandshakeState != "online" {
		t.Fatalf("states office %s phone %s", office.HandshakeState, phone.HandshakeState)
	}
	if len(ifc.Alerts) != 1 || ifc.Alerts[0].Kind != "stale_handshake" || ifc.Alerts[0].PeerName != "Office" || ifc.Alerts[0].Since != 1789999000 {
		t.Fatalf("alerts = %+v", ifc.Alerts)
	}
}

func TestWireGuardTransportChecksFollowTheSocketAndRecordCapture(t *testing.T) {
	s := vpnService(t)
	rec := record(t)
	ctx := context.Background()
	live := wgOneLivePeer("wg0", fixPeerB, "198.51.100.50:51820", 0, 0, 0, 25)
	live["wg0"].fwmark = "0xca6c"
	live["wg1"] = &wgLiveIface{name: "wg1"}
	rec.on("ip -j route get 198.51.100.50 mark 0xca6c", `[{"dst":"198.51.100.50","dev":"wg1"}]`)
	s.wg.checkTransports(ctx, time.Unix(fixNow, 0), live)
	got, ok := s.wg.transport("wg0", fixPeerB)
	if !ok || got.State != "captured" || got.Device != "wg1" {
		t.Fatalf("transport = %+v", got)
	}
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "ip -j route get 198.51.100.50 mark 0xca6c", out: `[{"dst":"198.51.100.50","gateway":"203.0.113.1","dev":"eth0"}]`}}, rec.replies...)
	rec.mu.Unlock()
	s.wg.checkTransports(ctx, time.Unix(fixNow+300, 0), live)
	if got, _ := s.wg.transport("wg0", fixPeerB); got.State != "native" {
		t.Fatalf("transport = %+v", got)
	}
	events, _ := s.wg.events(ctx, "wg0", fixPeerB, 10)
	if len(events) != 2 || events[0].Kind != "transport_restored" || events[1].Kind != "transport_captured" || events[1].Outcome != wgOutcomeDegraded {
		t.Fatalf("events = %+v", events)
	}
}

func TestGuardedChangesAnchorWireGuardTransports(t *testing.T) {
	rec := record(t)
	anchorPaths = readAnchors
	rec.on("wg show all dump", fixture(t, "wg-dump.txt")).
		on("ip -j route get 1.1.1.1", `[{"dst":"1.1.1.1","gateway":"203.0.113.1","dev":"eth0"}]`).
		fail("ip -j -6 route get 2606", "unreachable").
		on("ip -j route get 198.51.100.7", `[{"dst":"198.51.100.7","gateway":"203.0.113.1","dev":"eth0"}]`).
		// wg1's peer is already routed into wg0: it is not anchored, so the
		// change that moves it back out is not refused.
		on("ip -j -6 route get 2001:db8::5", `[{"dst":"2001:db8::5","dev":"wg0"}]`)
	before, err := clientPath(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{}
	for _, a := range before.anchors {
		labels = append(labels, a.label)
	}
	if strings.Join(labels, "|") != "the internet|the internet for Tailscale's own packets|the WireGuard transport of wg0 to 198.51.100.7" {
		t.Fatalf("anchors = %v", labels)
	}
	// The roaming phone moved to another tunnel's route: refused.
	rec.mu.Lock()
	rec.replies = append([]reply{{prefix: "ip -j route get 198.51.100.7", out: `[{"dst":"198.51.100.7","dev":"wg1"}]`}}, rec.replies...)
	rec.mu.Unlock()
	err = verifyPath(before)(context.Background())
	if !errors.Is(err, ErrGuarded) || !strings.Contains(err.Error(), "into wg1") {
		t.Fatalf("a captured transport must be refused: %v", err)
	}
	// Moving to another native device is the uplink anchors' business, not
	// the transport's.
	rec.mu.Lock()
	rec.replies[0] = reply{prefix: "ip -j route get 198.51.100.7", out: `[{"dst":"198.51.100.7","gateway":"192.0.2.1","dev":"eth1"}]`}
	rec.mu.Unlock()
	if err := verifyPath(before)(context.Background()); err != nil {
		t.Fatalf("a native move of a transport is not a capture: %v", err)
	}
	rec.mu.Lock()
	rec.replies[0] = reply{prefix: "ip -j route get 198.51.100.7", err: fmt.Errorf("RTNETLINK answers: Network is unreachable")}
	rec.mu.Unlock()
	if err := verifyPath(before)(context.Background()); !errors.Is(err, ErrGuarded) {
		t.Fatalf("a transport left with no route must be refused: %v", err)
	}
}

func TestWireGuardExitCounterIsTheOwnedMasqueradeRule(t *testing.T) {
	listing := `{"nftables":[
		{"rule":{"family":"inet","table":"` + gatewayTable + `","chain":"nat_post","comment":"nat:3","expr":[{"match":{}},{"counter":{"packets":42,"bytes":3360}},{"masquerade":null}]}},
		{"rule":{"family":"inet","table":"` + gatewayTable + `","chain":"nat_post","comment":"nat:4","expr":[{"counter":{"packets":7,"bytes":1}}]}}
	]}`
	if n, ok := wgExitCounter(listing, NATSpec{ID: 3}); !ok || n != 42 {
		t.Fatalf("counter = %d %v", n, ok)
	}
	if _, ok := wgExitCounter(listing, NATSpec{ID: 9}); ok {
		t.Fatal("a rule that is not there has no counter")
	}
}

func wgPeerByName(t *testing.T, ifc WGInterface, name string) WGPeer {
	t.Helper()
	for _, p := range ifc.Peers {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no peer %s in %s", name, ifc.Name)
	return WGPeer{}
}
