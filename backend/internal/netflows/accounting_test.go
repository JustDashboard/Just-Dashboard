package netflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	storedb "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func number(n uint64) *uint64 { return &n }
func cycle(at time.Time, tx, rx uint64) Cycle {
	return Cycle{At: at, FinishedAt: at.Add(time.Millisecond), BootID: "fixture-boot", DockerStatus: "observed", Sources: []Source{{ID: "host", Name: "Host", Namespace: "1:2", Status: "observed", ObservedAt: at, Sockets: 1, Values: []Socket{{Protocol: "tcp", State: "ESTAB", LocalAddress: "127.0.0.1", LocalPort: 123, RemoteAddress: "192.0.2.1", RemotePort: 443, Cookie: "abcd", Inode: "123", Tx: number(tx), Rx: number(rx), Retrans: number(3), Lost: number(1), Owner: Owner{Status: "verified_process", Program: "fixture", PID: 111, StartTicks: "222"}}}}}}
}
func testStore(t *testing.T) (*Store, *storedb.Store) {
	t.Helper()
	db, err := storedb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db.DB), db
}
func TestNativeParserKeepsTuplesAndUnknownUDPBytes(t *testing.T) {
	raw := `tcp ESTAB 0 0 [::1]:123 [2001:db8::9]:443 users:(("worker",pid=12,fd=3)) ino:99 sk:abcd bytes_sent:120 bytes_received:34 retrans:0/7 lost:2
udp ESTAB 0 0 127.0.0.1:1555 192.0.2.3:53 ino:101 sk:abce
udp UNCONN 0 0 0.0.0.0:1556 0.0.0.0:* ino:102 sk:abcf
tcp TIME-WAIT 0 0 127.0.0.1:1557 127.0.0.1:80
`
	s := parseSS(raw, 1024)
	if len(s.Values) != 4 || s.IdentityUnavailable != 1 || s.UnconnectedUDP != 1 || s.Status != "partial" {
		t.Fatalf("source=%+v", s)
	}
	tcp := s.Values[0]
	if tcp.RemoteAddress != "2001:db8::9" || tcp.RemotePort != 443 || tcp.State != "ESTAB" || *tcp.Retrans != 7 || len(tcp.owners) != 1 {
		t.Fatalf("tcp=%+v", tcp)
	}
	if s.Values[1].Tx != nil || s.Values[1].Rx != nil || s.Values[2].RemoteAddress != "" {
		t.Fatalf("UDP fields invent evidence: %+v", s.Values[1:])
	}
	capped := parseSS(raw, 1)
	if !capped.Truncated || len(capped.Values) != 1 {
		t.Fatalf("cap=%+v", capped)
	}
}
func TestDeltasNeverAllocateLifetimeRestartResetGapOrHourCrossing(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Hour).Add(2 * time.Minute)
	d := differencer{}
	a := cycle(at, 1000, 2000)
	rows := d.observe(&a, 30*time.Second)
	if rows[0].TxBytes != nil || a.DiscardedIntervals != 1 {
		t.Fatal("first sight counted lifetime")
	}
	b := cycle(at.Add(30*time.Second), 1100, 2150)
	rows = d.observe(&b, 30*time.Second)
	if *rows[0].TxBytes != 100 || *rows[0].RxBytes != 150 || *rows[0].Retransmissions != 0 || rows[0].MeasuredIntervals != 1 {
		t.Fatalf("delta=%+v", rows[0])
	}
	c := cycle(at.Add(time.Minute), 5, 10)
	rows = d.observe(&c, 30*time.Second)
	if rows[0].TxBytes != nil || rows[0].RxBytes != nil {
		t.Fatal("reset counted bytes")
	}
	gap := cycle(at.Add(5*time.Minute), 9000, 9000)
	rows = d.observe(&gap, 30*time.Second)
	if rows[0].TxBytes != nil {
		t.Fatal("gap counted bytes")
	}
	fail := Cycle{At: at.Add(6 * time.Minute), Sources: []Source{{ID: "host", Status: "unavailable"}}}
	d.observe(&fail, 30*time.Second)
	back := cycle(at.Add(6*time.Minute+time.Second), 10000, 10000)
	rows = d.observe(&back, 30*time.Second)
	if rows[0].TxBytes != nil {
		t.Fatal("failed source preserved baseline")
	}
	d = differencer{}
	late := cycle(at.Truncate(time.Hour).Add(59*time.Minute+50*time.Second), 100, 100)
	d.observe(&late, 30*time.Second)
	early := cycle(late.At.Add(20*time.Second), 200, 200)
	rows = d.observe(&early, 30*time.Second)
	if rows[0].TxBytes != nil {
		t.Fatal("UTC-hour boundary allocated bytes to wrong hour")
	}
	old := cycle(at, 100, 100)
	d = differencer{}
	one := d.observe(&old, 30*time.Second)[0]
	changed := cycle(at.Add(30*time.Second), 200, 200)
	changed.Sources[0].Values[0].Owner.StartTicks = "333"
	two := d.observe(&changed, 30*time.Second)[0]
	if two.TxBytes != nil || one.ID == two.ID {
		t.Fatal("owner replacement relabelled historical bytes")
	}
	fresh := differencer{}
	restart := cycle(at.Add(time.Minute), 999999, 999999)
	if fresh.observe(&restart, 30*time.Second)[0].TxBytes != nil {
		t.Fatal("restart allocated lifetime bytes")
	}
}
func TestDurableHistoryReopenAddressAndIdentityFilters(t *testing.T) {
	st, db := testStore(t)
	at := time.Now().UTC().Truncate(time.Hour).Add(-2*time.Hour + time.Minute)
	state := persistedState{Settings: DefaultSettings}
	d := differencer{}
	a := cycle(at, 1000, 2000)
	rows := d.observe(&a, 30*time.Second)
	var err error
	state, err = st.record(t.Context(), state, a, rows)
	if err != nil {
		t.Fatal(err)
	}
	b := cycle(at.Add(30*time.Second), 1500, 2700)
	rows = d.observe(&b, 30*time.Second)
	state, err = st.record(t.Context(), state, b, rows)
	if err != nil {
		t.Fatal(err)
	}
	reopened := NewStore(db.DB)
	q, _ := ValidateQuery(Query{From: at.Truncate(time.Hour), To: at.Truncate(time.Hour).Add(time.Hour), Address: "192.0.2.1"}, time.Now())
	r, err := reopened.read(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Rows) != 1 || *r.Rows[0].TxBytes != 500 || *r.Rows[0].RxBytes != 700 || r.Rows[0].Samples != 2 || r.Rows[0].SkippedIntervals != 1 || len(r.CoverageHours) != 1 || r.CoverageHours[0].Samples != 2 || r.LastCycle.DroppedEvents != nil {
		t.Fatalf("report=%+v", r)
	}
	q.Address = "192.0.2.2"
	r, err = reopened.read(t.Context(), q)
	if err != nil || len(r.Rows) != 0 {
		t.Fatalf("address query=%+v %v", r, err)
	}
	for _, q := range []Query{{From: at, To: at.Add(time.Hour)}, {Address: "example.test"}, {ContainerID: "short"}, {Limit: 1001}} {
		if _, err := ValidateQuery(q, time.Now()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %+v", q)
		}
	}
}
func TestRetentionAndPayloadCapPruneOldestAndReportIt(t *testing.T) {
	st, _ := testStore(t)
	now := time.Now().UTC().Truncate(time.Hour).Add(time.Minute)
	state := persistedState{Settings: DefaultSettings}
	d := differencer{}
	old := cycle(now.Add(-8*24*time.Hour), 1, 1)
	var err error
	state, err = st.record(t.Context(), state, old, d.observe(&old, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	cur := cycle(now, 2, 2)
	state, err = st.record(t.Context(), state, cur, d.observe(&cur, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if state.Pruned != 1 {
		t.Fatalf("pruned=%d", state.Pruned)
	}
	// The byte-cap path uses actual stored row lengths, not fake byte claims.
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 8192)
	for i := 0; i < MaxStoredBytes/len(raw)+1; i++ {
		_, err = tx.Exec(`INSERT INTO network_flow_buckets(id,hour,last_seen,payload,payload_bytes) VALUES(?,?,?,?,?)`, fmt.Sprint("cap", i), now.Unix(), i, string(raw), len(raw))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	state, err = st.policy(t.Context(), state, now)
	if err != nil {
		t.Fatal(err)
	}
	var bytes int64
	st.db.QueryRow(`SELECT SUM(payload_bytes) FROM network_flow_buckets`).Scan(&bytes)
	if bytes > MaxStoredBytes || state.Pruned <= 1 {
		t.Fatalf("cap bytes=%d pruned=%d", bytes, state.Pruned)
	}
}

type functionCollector func(context.Context) Cycle

func (f functionCollector) Collect(ctx context.Context) Cycle { return f(ctx) }
func TestDefaultOffAndOptOutDiscardInflightSample(t *testing.T) {
	st, _ := testStore(t)
	var calls atomic.Int32
	entered := make(chan struct{})
	s := New(st, functionCollector(func(ctx context.Context) Cycle {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		return cycle(time.Now().UTC(), 9999, 9999)
	}))
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	r, err := s.Report(t.Context(), Query{})
	if err != nil || r.Status != "off" || r.RecordingSince != nil || len(r.Rows) != 0 || calls.Load() != 0 || r.KernelObserver.Status != "unavailable" {
		t.Fatalf("default=%+v err=%v calls=%d", r, err, calls.Load())
	}
	if _, err = s.Recording(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	if _, err = s.Recording(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	st.db.QueryRow(`SELECT COUNT(*) FROM network_flow_buckets`).Scan(&rows)
	if rows != 0 {
		t.Fatal("opted-out inflight sample committed")
	}
}
func TestFailedStorageResetsBaselineAndExportDoesNotInventHistory(t *testing.T) {
	st, _ := testStore(t)
	s := New(st, nil)
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	at := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour + time.Minute)
	a := cycle(at, 1, 1)
	d := differencer{}
	state := persistedState{Settings: DefaultSettings}
	var err error
	state, err = st.record(t.Context(), state, a, d.observe(&a, 30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	data, err := s.Export(t.Context(), Query{From: at.Truncate(time.Hour), To: at.Truncate(time.Hour).Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	if json.Unmarshal(data, &r) != nil || len(data) > MaxExportBytes || r.Rows[0].TxBytes != nil || r.LastCycle.DroppedEvents != nil {
		t.Fatalf("export=%s", data)
	}
	// A readonly transaction failure is reported instead of publishing a
	// successful later read from a stale saved cycle.
	_, err = st.db.Exec(`CREATE TRIGGER fail_flow_insert BEFORE INSERT ON network_flow_cycles BEGIN SELECT RAISE(ABORT,'fixture I/O failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.state.Settings.Enabled = true
	s.collector = functionCollector(func(context.Context) Cycle { return cycle(time.Now().UTC(), 9999, 9999) })
	s.mu.Unlock()
	s.sample(t.Context())
	r, err = s.Report(t.Context(), Query{})
	if err != nil || r.Status != "unavailable" || r.Error == "" || len(s.differ.prev) != 0 {
		t.Fatalf("failed record=%+v %v", r, err)
	}
}

func BenchmarkDurableSocketCycle1024(b *testing.B) {
	db, err := storedb.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	st := NewStore(db.DB)
	state := persistedState{Settings: DefaultSettings}
	at := time.Now().UTC().Truncate(time.Hour).Add(time.Minute)
	base := cycle(at, 1000, 2000)
	base.Sources[0].Values = nil
	for i := 0; i < 1024; i++ {
		sk := cycle(at, 1000, 2000).Sources[0].Values[0]
		sk.Cookie = fmt.Sprintf("%x", i+1)
		sk.Inode = fmt.Sprint(i + 1)
		sk.LocalPort = 10000 + i
		base.Sources[0].Values = append(base.Sources[0].Values, sk)
	}
	d := differencer{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := base
		c.At = at.Add(time.Duration(i) * 30 * time.Second)
		c.FinishedAt = c.At.Add(time.Millisecond)
		c.Sources = append([]Source(nil), base.Sources...)
		c.Sources[0].ObservedAt = c.At
		rows := d.observe(&c, 30*time.Second)
		state, err = st.record(context.Background(), state, c, rows)
		if err != nil {
			b.Fatal(err)
		}
	}
}
func TestCounterExportPreservesExactUnsignedDecimal(t *testing.T) {
	original := Bucket{TxBytes: number(9007199254740993), LostGaugeMax: number(1)}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"txBytes":"9007199254740993"`) {
		t.Fatalf("not exact JSON decimal: %s", raw)
	}
	var restored Bucket
	if err = json.Unmarshal(raw, &restored); err != nil || restored.TxBytes == nil || *restored.TxBytes != *original.TxBytes {
		t.Fatalf("roundtrip=%+v %v", restored, err)
	}
}

func TestRowCapAndBoundedWideCoverageExport(t *testing.T) {
	st, _ := testStore(t)
	now := time.Now().UTC().Truncate(time.Hour)
	state := persistedState{Settings: DefaultSettings}
	state.Settings.RetentionDays = 31
	_, err := st.db.Exec(`WITH RECURSIVE sequence(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM sequence WHERE n<50001) INSERT INTO network_flow_buckets(id,hour,last_seen,payload,payload_bytes) SELECT 'fixture-'||n,?,?,?,2 FROM sequence`, now.Unix(), now.UnixMilli(), `{}`)
	if err != nil {
		t.Fatal(err)
	}
	state, err = st.policy(t.Context(), state, now)
	if err != nil {
		t.Fatal(err)
	}
	var rows int
	st.db.QueryRow(`SELECT COUNT(*) FROM network_flow_buckets`).Scan(&rows)
	if rows > MaxRows || state.Pruned == 0 {
		t.Fatalf("row cap=%d pruned=%d", rows, state.Pruned)
	}
	_, err = st.db.Exec(`DELETE FROM network_flow_buckets`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 31*24; i++ {
		at := now.Add(-time.Duration(i) * time.Hour)
		c := Cycle{At: at, FinishedAt: at, BootID: "fixture", Sources: []Source{}}
		for j := 0; j < MaxSources+1; j++ {
			c.Sources = append(c.Sources, Source{ID: fmt.Sprint(j), Name: strings.Repeat("n", 128), Status: "unavailable", Error: strings.Repeat("e", 512), ObservedAt: at})
		}
		cov := CoverageHour{Hour: at, FirstSampleAt: at, LastSampleAt: at, Samples: 1, LastCycle: c}
		data, _ := json.Marshal(cov)
		if len(data) > maxRowBytes {
			t.Fatal("fixture exceeds per-record native bound")
		}
		_, err = tx.Exec(`INSERT INTO network_flow_cycles(hour,payload,payload_bytes) VALUES(?,?,?)`, at.Unix(), string(data), len(data))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s := New(st, nil)
	if err = s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	q := Query{From: now.Add(-30 * 24 * time.Hour), To: now.Add(time.Hour)}
	raw, err := s.Export(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	if json.Unmarshal(raw, &r) != nil || len(raw) > MaxExportBytes || !r.CoverageTruncated || len(r.CoverageHours) == 0 {
		t.Fatalf("export bytes=%d coverage=%d truncated=%v", len(raw), len(r.CoverageHours), r.CoverageTruncated)
	}
	s.exportMu.Lock()
	_, err = s.Export(t.Context(), q)
	s.exportMu.Unlock()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("overlapping export=%v", err)
	}
	// A corrupt stored payload is read through a bounded SQL prefix and is
	// unavailable, rather than being treated as an empty observation window.
	_, err = st.db.Exec(`UPDATE network_flow_cycles SET payload=? WHERE hour=?`, strings.Repeat("x", maxRowBytes+100000), now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Report(t.Context(), q); err == nil {
		t.Fatal("oversized corrupt coverage accepted")
	}
}
