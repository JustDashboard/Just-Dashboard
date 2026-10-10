package netx

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// Totals that survive table replacement. The table's kernel handle names a
// generation; a new handle or a counter that falls carries the last reading
// into the total, and the dashboard's own reloads read the old table first.

// gwTableJSON is `nft -t -j list table` for a handle and rule counters.
func gwTableJSON(handle int, counters map[string][2]uint64) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"nftables":[{"metainfo":{"json_schema_version":1}},{"table":{"family":"inet","name":"jd_gateway","handle":%d}}`, handle)
	for comment, c := range counters {
		fmt.Fprintf(&b, `,{"rule":{"family":"inet","table":"jd_gateway","chain":"nat_pre","comment":%q,"expr":[{"counter":{"packets":%d,"bytes":%d}},{"accept":null}]}}`, comment, c[0], c[1])
	}
	b.WriteString("]}")
	return b.String()
}

func gwLive(t *testing.T, handle int, counters map[string][2]uint64) liveCounters {
	t.Helper()
	live, err := parseLiveCounters(gwTableJSON(handle, counters))
	if err != nil {
		t.Fatal(err)
	}
	return live
}

func quietTelemetry(db *sql.DB) *gatewayTelemetry {
	return newGatewayTelemetry(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestCounterTotalsCarryAcrossGenerations(t *testing.T) {
	tel := quietTelemetry(nil)
	tel.loaded = true
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

	d := tel.fold(gwLive(t, 5, map[string][2]uint64{"forward:1": {100, 1000}}), t0)
	if d["forward:1"].Packets != 100 {
		t.Fatalf("first reading's delta = %+v", d)
	}
	d = tel.fold(gwLive(t, 5, map[string][2]uint64{"forward:1": {130, 1300}}), t0.Add(time.Minute))
	if d["forward:1"].Packets != 30 || d["forward:1"].Bytes != 300 {
		t.Fatalf("same generation delta = %+v", d)
	}
	// The table was replaced: a new handle, counting from zero again.
	d = tel.fold(gwLive(t, 6, map[string][2]uint64{"forward:1": {4, 40}}), t0.Add(2*time.Minute))
	if d["forward:1"].Packets != 4 {
		t.Fatalf("new generation delta = %+v", d)
	}
	r := tel.rows["forward:1"]
	if r.Packets != 130 || r.LivePackets != 4 || r.Generation != 6 || r.Resets != 1 {
		t.Fatalf("row = %+v", r)
	}
	total := tel.total("forward:1", gwLive(t, 6, map[string][2]uint64{"forward:1": {10, 100}}))
	if total.Packets != 140 || total.Bytes != 1400 || total.Resets != 1 || total.Since == nil || !total.Since.Equal(t0) {
		t.Fatalf("total = %+v", total)
	}
	// A counter that falls under the same handle is a reset too.
	tel.fold(gwLive(t, 6, map[string][2]uint64{"forward:1": {2, 20}}), t0.Add(3*time.Minute))
	if r := tel.rows["forward:1"]; r.Packets != 134 || r.Resets != 2 {
		t.Fatalf("in-place reset row = %+v", r)
	}
}

func TestCounterTotalsBetweenReadingsIncludeAnUnfoldedReplacement(t *testing.T) {
	tel := quietTelemetry(nil)
	tel.loaded = true
	tel.fold(gwLive(t, 5, map[string][2]uint64{"nat:2": {50, 500}}), time.Now())
	// The page reads a newer table before the recorder has seen it.
	total := tel.total("nat:2", gwLive(t, 9, map[string][2]uint64{"nat:2": {7, 70}}))
	if total.Packets != 57 || total.Resets != 1 {
		t.Fatalf("total = %+v", total)
	}
}

func TestAVanishedRuleOrTableIsCarriedIntoItsTotal(t *testing.T) {
	tel := quietTelemetry(nil)
	tel.loaded = true
	tel.fold(gwLive(t, 5, map[string][2]uint64{"forward:1": {10, 100}, "forward:2": {20, 200}}), time.Now())
	tel.fold(liveCounters{Counters: map[string]RuleCounter{}, Rules: map[string]int{}}, time.Now())
	for key, want := range map[string]uint64{"forward:1": 10, "forward:2": 20} {
		if r := tel.rows[key]; r.Packets != want || r.LivePackets != 0 || r.Generation != 0 {
			t.Errorf("%s = %+v", key, r)
		}
	}
	// Loaded again later, counting from zero.
	tel.fold(gwLive(t, 7, map[string][2]uint64{"forward:1": {3, 30}}), time.Now())
	if total := tel.total("forward:1", gwLive(t, 7, map[string][2]uint64{"forward:1": {3, 30}})); total.Packets != 13 {
		t.Fatalf("total = %+v", total)
	}
}

func TestCounterTotalsPersistAndSeriesRecordDeltasAndGauges(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec := record(t)
	sys := t.TempDir()
	prevSys := gatewaySysRoot
	gatewaySysRoot = sys
	t.Cleanup(func() { gatewaySysRoot = prevSys })
	h := &gwHost{sys: sys}
	h.writeSys("net/netfilter/nf_conntrack_count", "300")
	h.writeSys("net/netfilter/nf_conntrack_max", "1000")
	prevNow := gatewayNow
	now := time.Date(2026, 10, 9, 12, 0, 30, 0, time.UTC)
	gatewayNow = func() time.Time { return now }
	t.Cleanup(func() { gatewayNow = prevNow })

	table := gwTableJSON(5, map[string][2]uint64{"limit:3": {10, 600}})
	rec.on("nft -t -j list table inet jd_gateway", table)
	stats := []ctStats{{Drop: 4, InsertFailed: 1}, {Drop: 9, InsertFailed: 1}}
	conntrackTransport = func(ctx context.Context, req []byte, fn func(uint16, []byte) (bool, error)) error {
		s := stats[0]
		stats = stats[1:]
		_, err := fn(0, gwStatsMessage(s))
		return err
	}

	tel := quietTelemetry(st.DB)
	tel.observe(context.Background())
	rec.mu.Lock()
	rec.replies = nil
	rec.mu.Unlock()
	rec.on("nft -t -j list table inet jd_gateway", gwTableJSON(5, map[string][2]uint64{"limit:3": {25, 1500}}))
	now = now.Add(time.Minute)
	tel.observe(context.Background())

	again := quietTelemetry(st.DB)
	total := again.totals(context.Background(), gwLive(t, 6, map[string][2]uint64{"limit:3": {2, 120}}), []string{"limit:3"})["limit:3"]
	if total.Packets != 27 || total.Bytes != 1620 {
		t.Fatalf("a restarted recorder lost the total: %+v", total)
	}
	series, err := again.series(context.Background(), []string{"limit:3", "conntrack:count", "conntrack:drop", "conntrack:insert_failed"}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := series["limit:3"]; len(got) != 2 || got[0].Value != 10 || got[1].Value != 15 || got[1].Bytes != 900 {
		t.Fatalf("limit series = %+v", got)
	}
	if got := series["conntrack:count"]; len(got) != 2 || got[1].Value != 300 {
		t.Fatalf("count series = %+v", got)
	}
	if got := series["conntrack:drop"]; len(got) != 1 || got[0].Value != 5 {
		t.Fatalf("drops are recorded as the interval's increase: %+v", got)
	}
	if got := series["conntrack:insert_failed"]; len(got) != 0 {
		t.Fatalf("an unchanged counter records nothing: %+v", got)
	}
}

func TestADashboardReloadReadsTheOldTableBeforeReplacingIt(t *testing.T) {
	h := newGwHost(t)
	sp := emptySpec()
	sp.NextID = 2
	sp.Forwards = []ForwardSpec{{ID: 1, Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.5", SourceNAT: "never", Enabled: true}}
	h.seed(t, sp)
	h.first("nft -t -j list table inet jd_gateway", gwTableJSON(5, map[string][2]uint64{"forward:1": {500, 50000}}), nil)
	if _, err := h.UpdateForward(context.Background(), 1, ForwardRequest{Name: "web", Protocol: "tcp", Ports: "8080", Target: "10.0.0.6", SourceNAT: "never"}, gwClient, "ops", nil); err != nil {
		t.Fatal(err)
	}
	r, ok := h.telemetry.rows["forward:1"]
	if !ok || r.LivePackets != 500 || r.Generation != 5 {
		t.Fatalf("the old generation was not read before the reload: %+v", r)
	}
	total := h.telemetry.total("forward:1", gwLive(t, 6, map[string][2]uint64{"forward:1": {1, 100}}))
	if total.Packets != 501 {
		t.Fatalf("total = %+v", total)
	}
}

// gwStatsMessage encodes one CPU's statistics as ctnetlink sends them.
func gwStatsMessage(s ctStats) []byte {
	payload := []byte{0, 0, 0, 0}
	add := func(typ uint16, v uint64) {
		b := []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		payload = append(payload, nlAttr(typ, b)...)
	}
	add(ctaStatsFound, s.Found)
	add(ctaStatsInsertFailed, s.InsertFailed)
	add(ctaStatsDrop, s.Drop)
	add(ctaStatsEarlyDrop, s.EarlyDrop)
	add(ctaStatsError, s.Error)
	return payload
}
