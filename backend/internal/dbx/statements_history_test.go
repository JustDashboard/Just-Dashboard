package dbx

import (
	"reflect"
	"testing"
	"time"
)

// A quantile is the upper edge of the bucket its call falls in, so it is
// never under the truth; the calls are counted, not the buckets.
func TestDurationsQuantileIsTheEdgeOfTheBucketItFallsIn(t *testing.T) {
	d := Durations{1: 50, 2: 40, 8: 9, 120: 1}
	for _, c := range []struct {
		q    float64
		want float64
	}{{0.5, 1}, {0.51, 2}, {0.9, 2}, {0.95, 8}, {0.99, 8}, {0.995, 120}, {1, 120}, {0, 1}} {
		if got, ok := d.Quantile(c.q); !ok || got != c.want {
			t.Errorf("p%v = %v %v, want %v", c.q*100, got, ok, c.want)
		}
	}
	if _, ok := (Durations{}).Quantile(0.5); ok {
		t.Error("no calls have a quantile")
	}
	if _, ok := Durations(nil).Quantile(0.5); ok {
		t.Error("an engine with no durations has a quantile")
	}
}

func TestDurationsAreKeptAsTextAndReadBack(t *testing.T) {
	d := Durations{0.01: 3, 21.7: 1, 1.5: 12, 4: 0}
	text := d.String()
	if text != "0.01:3 1.5:12 21.7:1" {
		t.Errorf("String = %q", text)
	}
	if back := ParseDurations(text); !reflect.DeepEqual(back, Durations{0.01: 3, 1.5: 12, 21.7: 1}) {
		t.Errorf("read back %v", back)
	}
	if ParseDurations("") != nil || Durations(nil).String() != "" {
		t.Error("nothing is not an empty histogram")
	}
	if got := ParseDurations("2:1 junk 3:x 4:-2 5:2"); !reflect.DeepEqual(got, Durations{2: 1, 5: 2}) {
		t.Errorf("a pair that cannot be read was kept: %v", got)
	}
}

// A histogram read twice gives what ran in between, bucket by bucket; one
// that went down, or lost a bucket, was started again.
func TestDurationsSinceTheReadingBefore(t *testing.T) {
	before := Durations{1: 10, 2: 5}
	if got, ok := (Durations{1: 12, 2: 5, 8: 1}).since(before); !ok || !reflect.DeepEqual(got, Durations{1: 2, 8: 1}) {
		t.Errorf("since = %v %v", got, ok)
	}
	if _, ok := (Durations{1: 9, 2: 6}).since(before); ok {
		t.Error("a bucket that went down was differenced")
	}
	if _, ok := (Durations{1: 11}).since(before); ok {
		t.Error("a bucket that went away was differenced")
	}
}

func figures(calls int64, totalMs float64, rows, read int64, d Durations) StatementFigures {
	return StatementFigures{Calls: calls, TotalMs: totalMs, Rows: rows, RowsRead: read, Durations: d}
}

func TestStatementFiguresSinceTheReadingBefore(t *testing.T) {
	before := figures(10, 100, 20, 400, Durations{1: 10})
	got, ok := figures(15, 160, 30, 500, Durations{1: 12, 4: 3}).since(before)
	if !ok || !reflect.DeepEqual(got, figures(5, 60, 10, 100, Durations{1: 2, 4: 3})) {
		t.Errorf("since = %+v %v", got, ok)
	}
	// Any running total that went down is an entry made again.
	for name, after := range map[string]StatementFigures{
		"calls":     figures(9, 160, 30, 500, Durations{1: 12}),
		"time":      figures(15, 99, 30, 500, Durations{1: 12}),
		"rows":      figures(15, 160, 19, 500, Durations{1: 12}),
		"rows read": figures(15, 160, 30, 399, Durations{1: 12}),
		"durations": figures(15, 160, 30, 500, Durations{1: 9, 2: 6}),
	} {
		if got, ok := after.since(before); ok || !reflect.DeepEqual(got, after) {
			t.Errorf("%s went down and since = %+v %v", name, got, ok)
		}
	}
	// What was not counted before is not counted for the interval.
	got, ok = figures(15, 160, 30, 500, Durations{1: 12}).since(figures(10, 100, 20, -1, nil))
	if !ok || got.RowsRead != -1 || got.Durations != nil {
		t.Errorf("figures first read now were given to the interval: %+v", got)
	}
}

func snapshot(at time.Time, since *time.Time, total float64, f map[string]StatementFigures) *StatementSnapshot {
	return &StatementSnapshot{At: at, Supported: true, Since: since, TotalMs: total, Figures: f}
}

func runIDs(in *StatementsInterval) []string {
	ids := []string{}
	for _, run := range in.Statements {
		ids = append(ids, run.ID)
	}
	return ids
}

func TestStatementsBetweenTwoReadingsOfRunningTotals(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Minute)
	started := t0.Add(-time.Hour)
	prev := snapshot(t0, &started, 1000, map[string]StatementFigures{
		"a":    figures(100, 500, 100, -1, nil),
		"b":    figures(10, 300, 10, -1, nil),
		"idle": figures(5, 50, 5, -1, nil),
		"made": figures(50, 100, 50, -1, nil),
	})
	cur := snapshot(t1, &started, 1300, map[string]StatementFigures{
		"a":    figures(130, 650, 130, -1, nil),
		"b":    figures(12, 400, 12, -1, nil),
		"idle": figures(5, 50, 5, -1, nil),
		// Dropped to make room and made again: all of it ran since.
		"made": figures(3, 30, 3, -1, nil),
		// New to the list: its totals may run from long before.
		"new": figures(900, 9000, 9, -1, nil),
	})
	in := StatementsBetween(prev, cur, false)
	if in.Reset || !in.From.Equal(t0) || !in.To.Equal(t1) {
		t.Errorf("interval %+v", in)
	}
	if got := runIDs(in); !reflect.DeepEqual(got, []string{"a", "b", "made"}) {
		t.Fatalf("ran %v, the most time first", got)
	}
	if !reflect.DeepEqual(in.Statements[0].StatementFigures, figures(30, 150, 30, -1, nil)) ||
		!reflect.DeepEqual(in.Statements[2].StatementFigures, figures(3, 30, 3, -1, nil)) {
		t.Errorf("figures %+v", in.Statements)
	}
	// The engine's own total, which counts what the list does not hold.
	if in.TotalMs != 300 {
		t.Errorf("total %v, want the engine's 300", in.TotalMs)
	}
	// An engine total that went down — a heavy entry dropped — is floored at
	// what the listed statements ran.
	cur.TotalMs = 900
	if in := StatementsBetween(prev, cur, false); in.TotalMs != 280 {
		t.Errorf("total %v, want the listed 280", in.TotalMs)
	}
}

// A reset between two readings makes every total one of what ran since,
// statements new to the list included; it is told by the engine's own time
// of its last reset or by the dashboard having made it.
func TestStatementsBetweenAReset(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	started, again := t0.Add(-time.Hour), t0.Add(2*time.Minute)
	prev := snapshot(t0, &started, 1000, map[string]StatementFigures{"a": figures(5, 50, 5, -1, nil)})
	// More calls than before: without the reset this reads as 3 calls.
	cur := snapshot(t0.Add(5*time.Minute), &again, 120, map[string]StatementFigures{
		"a": figures(8, 80, 8, -1, nil), "new": figures(2, 40, 2, -1, nil),
	})
	in := StatementsBetween(prev, cur, false)
	if !in.Reset || in.TotalMs != 120 || !reflect.DeepEqual(runIDs(in), []string{"a", "new"}) ||
		in.Statements[0].Calls != 8 {
		t.Errorf("after the engine's reset: %+v", in)
	}
	// MySQL keeps no time of a reset: the dashboard's own says so.
	prev.Since, cur.Since = nil, nil
	if in := StatementsBetween(prev, cur, false); in.Reset || in.Statements[0].Calls != 3 {
		t.Errorf("without a reset known: %+v", in)
	}
	if in := StatementsBetween(prev, cur, true); !in.Reset || in.Statements[0].Calls != 8 || in.TotalMs != 120 {
		t.Errorf("after the dashboard's reset: %+v", in)
	}
	// An engine that once did not say when, and now does, is not a reset.
	cur.Since = &again
	if in := StatementsBetween(prev, cur, false); in.Reset {
		t.Error("a reset time first read now was taken for a reset")
	}
}

// A window's figures are already what ran in it.
func TestStatementsBetweenWindows(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	prev := &StatementSnapshot{At: t0, Window: true, Through: t0.Add(-15 * time.Second)}
	cur := &StatementSnapshot{At: t0.Add(5 * time.Minute), Window: true, Through: t0.Add(285 * time.Second), TotalMs: 75,
		Figures: map[string]StatementFigures{
			"x": figures(3, 60, 3, 3000, Durations{21.7: 3}), "y": figures(1, 15, 1, 10, Durations{16: 1}),
			"none": figures(0, 0, 0, 0, nil),
		}}
	in := StatementsBetween(prev, cur, false)
	if in.Reset || in.TotalMs != 75 || !reflect.DeepEqual(runIDs(in), []string{"x", "y"}) ||
		!reflect.DeepEqual(in.Statements[0].StatementFigures, cur.Figures["x"]) {
		t.Errorf("window: %+v", in)
	}
}
