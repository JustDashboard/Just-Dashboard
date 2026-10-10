package metrics

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/sysinfo"
)

func tcpAt(out, retrans, fails, opens, drops uint64) sysinfo.TCPStats {
	return sysinfo.TCPStats{Supported: true, Counters: sysinfo.TCPCounters{
		OutSegs: out, RetransSegs: retrans, AttemptFails: fails, ActiveOpens: opens, ListenDrops: drops,
	}}
}

func findingByID(findings []Finding, id string) *Finding {
	for i := range findings {
		if findings[i].ID == id {
			return &findings[i]
		}
	}
	return nil
}

// TCP is judged over a window, like the links: the first check has nothing to
// compare with, a window closes once its baseline is a minute old, and
// counters that went backwards start again.
func TestTCPWindowJudgesRetransmitsDropsAndFailures(t *testing.T) {
	var w tcpWatch
	start := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if d := w.observe(tcpAt(1_000_000, 50_000, 900, 10_000, 3), start); d != nil {
		t.Fatalf("the first check judged a window: %+v", d)
	}
	if d := w.observe(tcpAt(1_010_000, 50_100, 905, 10_010, 3), start.Add(20*time.Second)); d != nil {
		t.Fatalf("a twenty-second window closed: %+v", d)
	}
	d := w.observe(tcpAt(1_100_000, 56_000, 1_000, 10_300, 40), start.Add(70*time.Second))
	if d == nil || d.outSegs != 100_000 || d.retrans != 6_000 || d.attemptFails != 100 || d.opens != 300 || d.listenDrops != 37 {
		t.Fatalf("window=%+v", d)
	}
	snap := &sysinfo.Snapshot{}
	got := tcpFindings(snap, d, nil)
	if f := findingByID(got, "tcp:retransmits"); f == nil || f.Level != "warning" || f.Value != 6 || !strings.Contains(f.Detail, "6,000 of 100,000") {
		t.Fatalf("retransmits=%+v", f)
	}
	if f := findingByID(got, "tcp:listen-drops"); f == nil || f.Level != "warning" || f.Value != 37 {
		t.Fatalf("listen drops=%+v", f)
	}
	if f := findingByID(got, "tcp:attempt-fails"); f == nil || f.Level != "notice" {
		t.Fatalf("attempt fails=%+v", f)
	}
	if reset := w.observe(tcpAt(10, 1, 0, 1, 0), start.Add(140*time.Second)); reset != nil {
		t.Fatalf("counters that went back kept a window: %+v", reset)
	}
}

func TestTCPWindowStaysQuietForNormalTraffic(t *testing.T) {
	quiet := &tcpDelta{outSegs: 2_000_000, retrans: 4_000, attemptFails: 40, opens: 10_000, listenDrops: 2, span: time.Minute}
	if got := tcpFindings(&sysinfo.Snapshot{}, quiet, nil); len(got) != 0 {
		t.Fatalf("0.2%% resent, 40 failed attempts and 2 drops raised %+v", got)
	}
	// A small window with a high share is a handful of segments, not a finding.
	tiny := &tcpDelta{outSegs: 1_000, retrans: 100, span: time.Minute}
	if got := tcpFindings(&sysinfo.Snapshot{}, tiny, nil); len(got) != 0 {
		t.Fatalf("100 resent segments raised %+v", got)
	}
	notice := &tcpDelta{outSegs: 20_000, retrans: 500, span: time.Minute}
	if f := findingByID(tcpFindings(&sysinfo.Snapshot{}, notice, nil), "tcp:retransmits"); f == nil || f.Level != "notice" {
		t.Fatalf("2.5%% of 20,000=%+v", f)
	}
}

func rttSeries(n int, ms float64) *Series {
	s := &Series{}
	for i := 0; i < n; i++ {
		v := ms
		s.Points = append(s.Points, Point{RTT: &v})
	}
	return s
}

func TestLatencyIsANoticeOnlyWhenSlowAndWellAboveTheHour(t *testing.T) {
	slow := &sysinfo.Snapshot{TCP: sysinfo.TCPStats{Latency: sysinfo.TCPLatency{Supported: true, Sockets: 40, MedianMs: 240, P90Ms: 400}}}
	if f := findingByID(tcpFindings(slow, nil, rttSeries(30, 60)), "tcp:latency"); f == nil || f.Level != "notice" || f.Threshold != 180 {
		t.Fatalf("240 ms against an hour of 60 ms=%+v", f)
	}
	if f := findingByID(tcpFindings(slow, nil, rttSeries(30, 200)), "tcp:latency"); f != nil {
		t.Fatalf("a host that is always far away was flagged: %+v", f)
	}
	if f := findingByID(tcpFindings(slow, nil, rttSeries(4, 60)), "tcp:latency"); f != nil {
		t.Fatalf("four buckets were taken as an hour: %+v", f)
	}
	fast := &sysinfo.Snapshot{TCP: sysinfo.TCPStats{Latency: sysinfo.TCPLatency{Supported: true, Sockets: 40, MedianMs: 90}}}
	if f := findingByID(tcpFindings(fast, nil, rttSeries(30, 10)), "tcp:latency"); f != nil {
		t.Fatalf("90 ms was called slow: %+v", f)
	}
}

func TestNetworkAreaCarriesTheTCPReading(t *testing.T) {
	r := testRecorder(t, DefaultInterval, DefaultRetention)
	snap := &sysinfo.Snapshot{
		Net: []sysinfo.NetStats{{Interface: "eth0", Kind: "physical", PacketsRecv: 1000, PacketsSent: 1000}},
		TCP: sysinfo.TCPStats{Supported: true, Counters: sysinfo.TCPCounters{OutSegs: 1000, RetransSegs: 1},
			Latency: sysinfo.TCPLatency{Supported: true, Sockets: 3, MedianMs: 42}},
	}
	r.Assess(context.Background(), snap)
	r.links.base["eth0"] = linkCounters{packets: 2000, at: time.Now().Add(-2 * time.Minute)}
	r.tcp.base.at = time.Now().Add(-2 * time.Minute)
	snap.TCP.Counters = sysinfo.TCPCounters{OutSegs: 101_000, RetransSegs: 101}
	h := r.Assess(context.Background(), snap)
	for _, a := range h.Areas {
		if a.ID == AreaNetwork && (!strings.Contains(a.Summary, "0.1% resent") || !strings.Contains(a.Summary, "RTT 42 ms")) {
			t.Fatalf("network area=%+v", a)
		}
	}
}

// Recorded rows from before TCP was sampled are NULL and read as unmeasured;
// the rows after carry the rates and the RTT.
func TestRecordedTCPSeriesAreNullBeforeTheyWereSampled(t *testing.T) {
	r := testRecorder(t, 15*time.Second, DefaultRetention)
	ctx := context.Background()
	// Hour-aligned, so the two samples fifteen seconds apart share a bucket
	// whatever step the window rounds to.
	base := time.Now().Add(-2 * time.Hour).Truncate(time.Hour)
	if err := r.record(ctx, Sample{TS: base}); err != nil {
		t.Fatal(err)
	}
	for i, retrans := range []float64{2, 8} {
		s := Sample{TS: base.Add(10*time.Minute + time.Duration(i)*15*time.Second), TCPOutSegs: ptrf(200), TCPRetrans: ptrf(retrans),
			TCPAttemptFails: ptrf(0.5), TCPEstabResets: ptrf(0.1), TCPListenDrops: ptrf(float64(i)), TCPRTT: ptrf(40), TCPRTTP90: ptrf(90 + float64(i))}
		if err := r.record(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	old, err := r.Range(ctx, base, base.Add(time.Minute), 1)
	if err != nil || len(old.Points) != 1 {
		t.Fatalf("old=%+v %v", old, err)
	}
	if p := old.Points[0]; p.RetransPct != nil || p.RTT != nil || p.ListenDrops != nil {
		t.Fatalf("an unmeasured bucket reads as measured: %+v", p)
	}
	sampled, err := r.Range(ctx, base.Add(10*time.Minute), base.Add(11*time.Minute), 1)
	if err != nil || len(sampled.Points) != 1 {
		t.Fatalf("sampled=%+v %v", sampled, err)
	}
	p := sampled.Points[0]
	if p.Samples != 2 || *p.RetransPct != 2.5 || *p.RetransPctPeak != 4 || *p.RTT != 40 || *p.RTTPeak != 91 || *p.ListenDropsPeak != 1 {
		t.Fatalf("sampled bucket=%+v", p)
	}
}

func TestProbesAreCitedBesideNetworkFindingsAndAloneSayTheTroubleIsBeyond(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	runs := []ProbeEvidence{
		{ID: "a", Name: "ping upstream", Tool: "ping", Target: "198.51.100.1", Outcome: "timed_out", EndedAt: now.Add(-5 * time.Minute)},
		{ID: "b", Name: "web", Tool: "http", Target: "example.net", Outcome: "refused", EndedAt: now.Add(-10 * time.Minute)},
		{ID: "c", Name: "fine", Tool: "dns", Outcome: "completed", EndedAt: now.Add(-2 * time.Minute)},
		{ID: "d", Name: "old", Tool: "ping", Outcome: "timed_out", EndedAt: now.Add(-2 * time.Hour)},
		{ID: "e", Name: "cancelled", Tool: "ping", Outcome: "cancelled", EndedAt: now.Add(-time.Minute)},
	}
	h := Health{Findings: []Finding{{ID: "tcp:retransmits", Area: AreaNetwork, Level: "warning"}, {ID: "files", Area: AreaHardware, Level: "warning"}}}
	CorrelateProbes(&h, runs, now)
	f := findingByID(h.Findings, "tcp:retransmits")
	if len(f.Correlated) != 2 || f.Correlated[0].ID != "a" || f.Correlated[1].ID != "b" {
		t.Fatalf("correlated=%+v", f.Correlated)
	}
	if len(findingByID(h.Findings, "files").Correlated) != 0 {
		t.Fatal("a hardware finding cited network probes")
	}

	clean := Health{Findings: []Finding{}}
	CorrelateProbes(&clean, runs, now)
	beyond := findingByID(clean.Findings, "probes:beyond-host")
	if beyond == nil || beyond.Level != "notice" || len(beyond.Correlated) != 2 {
		t.Fatalf("beyond=%+v", beyond)
	}
	one := Health{Findings: []Finding{}}
	CorrelateProbes(&one, runs[:1], now)
	if len(one.Findings) != 0 {
		t.Fatalf("one failed probe made a finding: %+v", one.Findings)
	}
}
