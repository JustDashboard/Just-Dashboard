package netx

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func trafficStore(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st.DB
}

// A recorded row carries the exact counter growth of its interval, a reset
// inside it counted from its new value rather than lost, and the seconds the
// steps covered.
func TestRecordedRowsCarryExactBytesPacketsAndSpan(t *testing.T) {
	db := trafficStore(t)
	s := newSampler(db, nil, 6*time.Second, time.Hour)
	t0 := time.Now().Add(-time.Minute).Truncate(time.Second)
	s.observe(t0, map[string]devCounters{"eth0": {rxBytes: 1000, txBytes: 100, rxPackets: 10, txPackets: 1}})
	s.observe(t0.Add(2*time.Second), map[string]devCounters{"eth0": {rxBytes: 3000, txBytes: 600, rxPackets: 30, txPackets: 6}})
	// The device was recreated: its counters start again.
	s.observe(t0.Add(4*time.Second), map[string]devCounters{"eth0": {rxBytes: 400, txBytes: 50, rxPackets: 4, txPackets: 1}})
	s.observe(t0.Add(6*time.Second), map[string]devCounters{"eth0": {rxBytes: 1400, txBytes: 150, rxPackets: 14, txPackets: 2}})
	s.record(context.Background(), t0.Add(6*time.Second))
	var rx, tx, rxp, txp, span int64
	if err := db.QueryRow(`SELECT rx_bytes, tx_bytes, rx_packets, tx_packets, span FROM metric_interface_samples WHERE iface = 'eth0'`).
		Scan(&rx, &tx, &rxp, &txp, &span); err != nil {
		t.Fatal(err)
	}
	if rx != 2000+400+1000 || tx != 500+50+100 || rxp != 20+4+10 || txp != 5+1+1 || span != 6 {
		t.Fatalf("row = rx %d tx %d rxp %d txp %d span %d", rx, tx, rxp, txp, span)
	}
	// The next interval starts from nothing.
	s.record(context.Background(), t0.Add(12*time.Second))
	var rows int
	_ = db.QueryRow(`SELECT count(*) FROM metric_interface_samples`).Scan(&rows)
	if rows != 1 {
		t.Fatalf("an interval with no steps wrote a row: %d rows", rows)
	}
}

func seedInterface(t *testing.T, db *sql.DB, iface string, ts int64, rx, tx float64, rxBytes, txBytes, span any) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO metric_interface_samples (ts, iface, rx_rate, tx_rate, rx_peak, tx_peak, rx_bytes, tx_bytes, span)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, ts, iface, rx, tx, rx, tx, rxBytes, txBytes, span); err != nil {
		t.Fatal(err)
	}
}

// Percentiles rank the raw interval means, not the chart's buckets, and an
// explicit window bounds what is read.
func TestHistoryRangeRanksIntervalMeans(t *testing.T) {
	db := trafficStore(t)
	s := newSampler(db, nil, 15*time.Second, 24*time.Hour)
	now := time.Now().Truncate(time.Second)
	from := now.Add(-100 * 15 * time.Second)
	for i := 0; i < 100; i++ {
		// rx is 1..100 in a shuffled order; tx is flat with one burst.
		rx := float64((i*37)%100 + 1)
		tx := 10.0
		if i == 50 {
			tx = 5000
		}
		seedInterface(t, db, "eth0", from.Unix()+int64(i*15), rx, tx, nil, nil, nil)
	}
	seedInterface(t, db, "wg0", from.Unix()+30, 7, 7, nil, nil, nil)
	h, err := s.HistoryRange(context.Background(), from, now, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	p := h.Percentiles["eth0"]
	if p.Samples != 100 || p.BasisSeconds != 15 || p.RxP50 != 50 || p.RxP95 != 95 || p.RxP99 != 99 || p.TxP95 != 10 || p.TxP99 != 10 {
		t.Fatalf("percentiles = %+v", p)
	}
	if len(h.Interfaces["eth0"]) > 20 || h.Percentiles["wg0"].Samples != 1 || h.RetainedFrom == 0 {
		t.Fatalf("history = %d points, wg0 %+v, retained %d", len(h.Interfaces["eth0"]), h.Percentiles["wg0"], h.RetainedFrom)
	}
	// A narrower explicit window reads only its own rows.
	narrow, err := s.HistoryRange(context.Background(), from, from.Add(10*15*time.Second), 20, "eth0")
	if err != nil || narrow.Percentiles["eth0"].Samples != 11 || len(narrow.Percentiles) != 1 {
		t.Fatalf("narrow = %+v %v", narrow.Percentiles, err)
	}
	if _, err := s.HistoryRange(context.Background(), now, from, 20, ""); err == nil {
		t.Fatal("a window that ends before it starts was read")
	}
	if _, err := s.HistoryRange(context.Background(), now.Add(-32*24*time.Hour), now, 20, ""); err == nil {
		t.Fatal("a window over a month was read")
	}
	// Without retention there is nothing to rank.
	off, err := newSampler(db, nil, 15*time.Second, 0).HistoryRange(context.Background(), from, now, 20, "")
	if err != nil || off.Recording || off.Percentiles != nil {
		t.Fatalf("disabled = %+v %v", off, err)
	}
}

// A budget counts exact bytes, estimates what older rows add apart from
// them, says how much of the period was recorded, and only ever alerts.
func TestInterfaceQuotasMeasureExactAndEstimatedUsage(t *testing.T) {
	db := trafficStore(t)
	s := newSampler(db, nil, 10*time.Second, 90*24*time.Hour)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	start, _ := wgPeriodStart("day", now)
	// Two exact intervals and one recorded before exact counters existed.
	seedInterface(t, db, "eth0", start.Unix()+100, 0, 0, 600, 400, 10)
	seedInterface(t, db, "eth0", start.Unix()+110, 0, 0, 1000, 0, 10)
	seedInterface(t, db, "eth0", start.Unix()+120, 30, 20, nil, nil, nil)
	// Yesterday's traffic is not today's.
	seedInterface(t, db, "eth0", start.Unix()-100, 0, 0, 1<<40, 0, 10)
	if _, err := db.Exec(`INSERT INTO network_interface_quotas (iface, period, direction, limit_bytes, created_by) VALUES
		('eth0', 'day', 'both', 2500, 'ops'), ('wg0', 'month', 'rx', 1048576, 'ops')`); err != nil {
		t.Fatal(err)
	}
	list, err := s.quotas(context.Background(), now)
	if err != nil || len(list) != 2 {
		t.Fatalf("quotas = %+v %v", list, err)
	}
	q := list[0]
	if q.Iface != "eth0" || q.UsedBytes != 2000 || q.EstimatedBytes != 500 || q.State != "exceeded" || q.Enforced ||
		q.PeriodStart != start.Unix() || q.CreatedBy != "ops" || q.RetentionShort {
		t.Fatalf("eth0 = %+v", q)
	}
	// Thirty recorded seconds of a twelve-hour day.
	if q.Coverage <= 0 || q.Coverage > 0.001 {
		t.Fatalf("coverage = %v", q.Coverage)
	}
	if w := list[1]; w.State != "unmeasured" || w.UsedBytes != 0 {
		t.Fatalf("an unrecorded device = %+v", w)
	}

	// One direction counts only itself; 80% is a warning.
	if _, err := db.Exec(`UPDATE network_interface_quotas SET direction = 'rx', limit_bytes = 2000 WHERE iface = 'eth0'`); err != nil {
		t.Fatal(err)
	}
	list, _ = s.quotas(context.Background(), now)
	if q := list[0]; q.UsedBytes != 1600 || q.EstimatedBytes != 300 || q.State != "warning" {
		t.Fatalf("rx only = %+v", q)
	}
	// A retention shorter than the period says its start is gone.
	short := newSampler(db, nil, 10*time.Second, time.Hour)
	list, _ = short.quotas(context.Background(), now)
	if !list[0].RetentionShort {
		t.Fatalf("short retention = %+v", list[0])
	}
	// Nothing recorded at all is unmeasured, never ok.
	list, _ = newSampler(db, nil, 10*time.Second, 0).quotas(context.Background(), now)
	if list[0].State != "unmeasured" {
		t.Fatalf("no retention = %+v", list[0])
	}
}

func TestSetAndClearInterfaceQuota(t *testing.T) {
	db := trafficStore(t)
	rec := record(t)
	rec.on("ip -j -d link show dev eth0", gwLinkEth0)
	rec.fail("ip -j -d link show dev nope", `Device "nope" does not exist.`)
	s := New(Options{DB: db, SampleEvery: 10 * time.Second, Retention: 24 * time.Hour})
	ctx := context.Background()
	for _, c := range []struct {
		iface string
		req   InterfaceQuotaRequest
		want  string
	}{
		{"eth0", InterfaceQuotaRequest{Period: "year", LimitBytes: 1 << 30}, "day, week or month"},
		{"eth0", InterfaceQuotaRequest{Period: "month", LimitBytes: 10}, "between 1 MiB"},
		{"eth0", InterfaceQuotaRequest{Period: "month", Direction: "sideways", LimitBytes: 1 << 30}, "both, rx or tx"},
		{"veth1234", InterfaceQuotaRequest{Period: "month", LimitBytes: 1 << 30}, "not recorded"},
		{"nope", InterfaceQuotaRequest{Period: "month", LimitBytes: 1 << 30}, "no interface"},
		{"eth 0", InterfaceQuotaRequest{Period: "month", LimitBytes: 1 << 30}, "interface name"},
	} {
		if _, err := s.SetInterfaceQuota(ctx, c.iface, c.req, "ops"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s %+v = %v, want %q", c.iface, c.req, err, c.want)
		}
	}
	q, err := s.SetInterfaceQuota(ctx, "eth0", InterfaceQuotaRequest{Period: "week", LimitBytes: 50 << 30}, "ops")
	if err != nil || q.Direction != "both" || q.Period != "week" || q.LimitBytes != 50<<30 || q.CreatedBy != "ops" {
		t.Fatalf("set = %+v %v", q, err)
	}
	if q, err = s.SetInterfaceQuota(ctx, "eth0", InterfaceQuotaRequest{Period: "month", Direction: "tx", LimitBytes: 1 << 30}, "ana"); err != nil || q.Period != "month" || q.Direction != "tx" {
		t.Fatalf("replace = %+v %v", q, err)
	}
	if err := s.ClearInterfaceQuota(ctx, "eth0"); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearInterfaceQuota(ctx, "eth0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clearing twice = %v", err)
	}
}

// The annotations are the network's own changes and the incidents saved
// in the window, oldest first; probes and other sections' actions are not.
func TestTrafficAnnotationsAreChangesAndIncidents(t *testing.T) {
	db := trafficStore(t)
	now := time.Now().Truncate(time.Second)
	for _, e := range []struct {
		ts      int64
		action  string
		success int
	}{
		{now.Add(-50 * time.Minute).Unix(), "network.shaping.set", 1},
		{now.Add(-40 * time.Minute).Unix(), "firewall.block.add", 1},
		{now.Add(-30 * time.Minute).Unix(), "network.probe", 1},
		{now.Add(-20 * time.Minute).Unix(), "docker.container.restart", 1},
		{now.Add(-10 * time.Minute).Unix(), "network.links.delete", 0},
		{now.Add(-3 * time.Hour).Unix(), "network.shaping.clear", 1},
	} {
		if _, err := db.Exec(`INSERT INTO audit_log (ts, username, action, target, success) VALUES (?, 'ops', ?, 'eth0', ?)`, e.ts, e.action, e.success); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO network_diagnostic_runs (id, name, status, created_at, updated_at, payload) VALUES (?, ?, 'succeeded', ?, ?, '{}')`,
		"0123456789abcdef0123456789abcdef", "Uplink loss at night", now.Add(-35*time.Minute).UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	s := New(Options{DB: db})
	list, err := s.TrafficAnnotations(context.Background(), now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, a := range list {
		titles = append(titles, a.Source+": "+a.Title+" ["+a.Severity+"]")
	}
	want := []string{
		"change: network.shaping.set eth0 [info]",
		"change: firewall.block.add eth0 [info]",
		"incident: Incident: Uplink loss at night [warning]",
		"change: network.links.delete eth0 [error]",
	}
	if strings.Join(titles, "\n") != strings.Join(want, "\n") {
		t.Fatalf("annotations:\n%s", strings.Join(titles, "\n"))
	}
	if list[2].Ref != "0123456789abcdef0123456789abcdef" || list[3].Detail != "failed by ops" {
		t.Fatalf("incident %+v, failure %+v", list[2], list[3])
	}
	if _, err := s.TrafficAnnotations(context.Background(), now, now.Add(-time.Hour)); err == nil {
		t.Fatal("a backwards window was read")
	}
}

// One container's chart is finer than the list's sparkline and names only it.
func TestContainerDetailIsOneContainerAtAFinerGrain(t *testing.T) {
	db := containerDB(t)
	now := time.Now().Unix()
	for i := int64(0); i < 240; i++ {
		addSample(t, db, "web", now-3590+i*15, i*1000, i*500, 1)
		addSample(t, db, "db", now-3590+i*15, i*10, i*10, 1)
	}
	s := New(Options{DB: db})
	d, err := s.ContainerDetail(context.Background(), "web", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "web" || d.RxBytes != 239*1000 || d.TxBytes != 239*500 || len(d.Series) <= seriesPoints || len(d.Series) > detailPoints {
		t.Fatalf("detail = %s rx %d tx %d, %d points", d.Name, d.RxBytes, d.TxBytes, len(d.Series))
	}
	if _, err := s.ContainerDetail(context.Background(), "gone", time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a container with no samples = %v", err)
	}
}
