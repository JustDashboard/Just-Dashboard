package accesslog

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeArchive is a request history held in a slice.
type fakeArchive struct {
	mu        sync.Mutex
	entries   []Entry
	lostAt    time.Time
	recording bool
	scans     int
	fail      error
}

func (a *fakeArchive) keep(entries ...Entry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entries = append(a.entries, entries...)
	sort.SliceStable(a.entries, func(i, j int) bool { return a.entries[i].at < a.entries[j].at })
}

func (a *fakeArchive) Span(context.Context, string) (ArchiveSpan, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	span := ArchiveSpan{LostAt: a.lostAt, Recording: a.recording}
	if n := len(a.entries); n > 0 {
		span.From, span.To = a.entries[0].At(), a.entries[n-1].At()
	}
	return span, nil
}

func (a *fakeArchive) Scan(ctx context.Context, _ string, from, until int64, fn func(Entry) error) error {
	a.mu.Lock()
	a.scans++
	entries, fail := append([]Entry(nil), a.entries...), a.fail
	a.mu.Unlock()
	for i, e := range entries {
		if fail != nil && i == len(entries)/2 {
			return fail
		}
		if e.at < from || e.at >= until {
			continue
		}
		e.Seq = 0
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func (a *fakeArchive) Retention() time.Duration { return 30 * 24 * time.Hour }

func (a *fakeArchive) scanCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.scans
}

func parsed(t *testing.T, at time.Time, path string) Entry {
	t.Helper()
	e, ok := Parse(line(at, path, 200))
	if !ok {
		t.Fatal("the test's line did not parse")
	}
	return e
}

// historyFixture is a route whose files hold its last hour and whose history
// holds three days before that and most of the hour too: the shape of a busy
// site whose ingress has rolled its older files away.
//
//	archive: 100 requests from now-3d, then the hour's first 51 requests
//	disk:    the hour, 60 requests a minute apart from now-1h
func historyFixture(t *testing.T) (*Store, *fakeReader, *fakeClock, *fakeArchive, time.Time) {
	t.Helper()
	reader, clock := newFake()
	now := clock.Now()
	archive := &fakeArchive{recording: true}
	for i := 0; i < 100; i++ {
		archive.keep(parsed(t, now.Add(-72*time.Hour).Add(time.Duration(i)*time.Minute), "/old"))
	}
	for i := 0; i < 60; i++ {
		at := now.Add(-time.Hour).Add(time.Duration(i) * time.Minute)
		reader.append(line(at, "/recent", 200))
		if i <= 50 {
			archive.keep(parsed(t, at, "/recent"))
		}
	}
	store := newTestStore(reader, clock).WithArchive(archive)
	return store, reader, clock, archive, now
}

func TestWindowSpanningHistoryAndMemoryCountsEachRequestOnce(t *testing.T) {
	store, _, _, archive, now := historyFixture(t)
	window, err := store.Window(context.Background(), "r", Filter{Since: now.Add(-7 * 24 * time.Hour), Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	summary := window.Result.Summary
	// 100 from the history alone and 60 from the hour, of which the history
	// also holds 51: counted from both, the week would be 211.
	if summary.Total != 160 {
		t.Fatalf("the week counts %d requests, want 160", summary.Total)
	}
	byPath := counts(summary.Paths)
	if byPath["/old"] != 100 || byPath["/recent"] != 60 {
		t.Fatalf("paths = %v, want /old 100 and /recent 60", byPath)
	}
	columns := 0
	for _, b := range summary.Buckets {
		columns += b.Total
	}
	if columns != 160 {
		t.Fatalf("the chart's columns hold %d requests, want the 160 the readings count", columns)
	}
	if summary.BucketSeconds != 3*3600 {
		t.Fatalf("a week's columns are %ds wide, want three hours", summary.BucketSeconds)
	}
	if !window.Coverage.Complete || summary.Truncated {
		t.Fatalf("complete = %v, truncated = %v; the history and memory meet and nothing is lost",
			window.Coverage.Complete, summary.Truncated)
	}
	h := window.Coverage.History
	if h == nil || h.From == nil || !h.From.Equal(now.Add(-72*time.Hour)) || h.Days != 30 || !h.Recording {
		t.Fatalf("history = %+v, want from now-3d, 30 days, recording", h)
	}
	if window.Coverage.From == nil || !window.Coverage.From.Equal(now.Add(-72*time.Hour)) {
		t.Fatalf("coverage from = %v, want the history's first request", window.Coverage.From)
	}
	if archive.scanCount() != 1 {
		t.Fatalf("the history was scanned %d times, want once", archive.scanCount())
	}
	// Newest first, and the newest is memory's, with its sequence.
	if first := window.Result.Entries[0]; first.Path != "/recent" || first.Seq == 0 {
		t.Fatalf("the newest row is %+v, want memory's newest", first)
	}
}

func TestWindowInsideMemoryDoesNotReadTheHistory(t *testing.T) {
	store, _, _, archive, now := historyFixture(t)
	window, err := store.Window(context.Background(), "r", Filter{Since: now.Add(-30 * time.Minute), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if archive.scanCount() != 0 {
		t.Fatal("a window memory holds read the history")
	}
	if got := window.Result.Summary.Total; got != 30 {
		t.Fatalf("the half hour counts %d, want 30", got)
	}
	if !window.Coverage.Complete || window.Coverage.History == nil {
		t.Fatalf("coverage = %+v, want complete with the history reported", window.Coverage)
	}
}

func TestWindowSaysWhereHistoryAndMemoryDoNotMeet(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	archive := &fakeArchive{recording: true}
	for i := 0; i < 10; i++ {
		archive.keep(parsed(t, now.Add(-72*time.Hour).Add(time.Duration(i)*time.Minute), "/old"))
	}
	for i := 0; i < 5; i++ {
		reader.append(line(now.Add(-time.Hour).Add(time.Duration(i)*time.Minute), "/recent", 200))
	}
	store := newTestStore(reader, clock).WithArchive(archive)
	window, err := store.Window(context.Background(), "r", Filter{Since: now.Add(-7 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got := window.Result.Summary.Total; got != 15 {
		t.Fatalf("the week counts %d, want both sides' 15", got)
	}
	// Between the history's last request and memory's first nothing is held,
	// and whatever was served then is not in the figures.
	if window.Coverage.Complete || !window.Result.Summary.Truncated {
		t.Fatal("a window across a gap between the history and memory reads as complete")
	}
}

func TestWindowAcrossALossIsAFloor(t *testing.T) {
	store, _, _, archive, now := historyFixture(t)
	archive.lostAt = now.Add(-48 * time.Hour)
	week, err := store.Window(context.Background(), "r", Filter{Since: now.Add(-7 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if week.Coverage.Complete {
		t.Fatal("a week across a part of the record known to be missing reads as complete")
	}
	day, err := store.Window(context.Background(), "r", Filter{Since: now.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !day.Coverage.Complete {
		t.Fatal("a day that starts after the loss reads as a floor")
	}
}

func TestWindowWithAnUnreadableHistoryIsMemorysAlone(t *testing.T) {
	store, _, _, archive, now := historyFixture(t)
	archive.fail = errors.New("disk I/O error")
	window, err := store.Window(context.Background(), "r", Filter{Since: now.Add(-7 * 24 * time.Hour)})
	if err != nil {
		t.Fatalf("an unreadable history failed the window: %v", err)
	}
	// Half the history was fed before the scan failed; none of it may count.
	if got := window.Result.Summary.Total; got != 60 {
		t.Fatalf("the week counts %d, want memory's 60", got)
	}
}

func TestExportSpansHistoryAndMemoryOldestFirstOnce(t *testing.T) {
	store, _, _, _, now := historyFixture(t)
	var got []Entry
	n, err := store.Export(context.Background(), "r", Filter{Since: now.Add(-7 * 24 * time.Hour)}, 0, func(e Entry) {
		got = append(got, e)
	})
	if err != nil || n != 160 || len(got) != 160 {
		t.Fatalf("exported %d (%d handed over, %v), want 160", n, len(got), err)
	}
	for i := 1; i < len(got); i++ {
		if got[i].at < got[i-1].at {
			t.Fatalf("row %d is older than the one before it", i)
		}
	}
	capped, err := store.Export(context.Background(), "r", Filter{Since: now.Add(-7 * 24 * time.Hour)}, 50, func(Entry) {})
	if err != nil || capped != 50 {
		t.Fatalf("a limit of 50 exported %d (%v)", capped, err)
	}
}

func TestLongWindowsAreAnsweredFromACacheForHalfAMinute(t *testing.T) {
	store, reader, clock, archive, now := historyFixture(t)
	ask := func() Window {
		t.Helper()
		// The page's "last 7 days", its start moving with the clock.
		w, err := store.Window(context.Background(), "r", Filter{Since: clock.Now().Add(-7 * 24 * time.Hour), Limit: 500})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	first := ask()
	clock.Advance(10 * time.Second)
	reader.append(line(clock.Now(), "/new", 200))
	if again := ask(); again.Result.Summary.Total != first.Result.Summary.Total || archive.scanCount() != 1 {
		t.Fatalf("a second week ten seconds later scanned the history again (%d scans)", archive.scanCount())
	}
	clock.Advance(windowCacheFor)
	if later := ask(); later.Result.Summary.Total != 161 || archive.scanCount() != 2 {
		t.Fatalf("after the cache's half minute the week counts %d with %d scans, want 161 and 2",
			later.Result.Summary.Total, archive.scanCount())
	}
	// A short window is never cached: it is memory's, and memory is instant.
	if _, err := store.Window(context.Background(), "r", Filter{Since: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.cache.get(cacheKey("r", Filter{Since: now.Add(-2 * time.Hour)}, clock.Now()), clock.Now()); ok {
		t.Fatal("a two-hour window was cached")
	}
}

func TestWhoNarrowsToPeopleOrEverythingElse(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		visit(0, "198.51.100.1", agentChromeWindows, "/", 200),
		visit(time.Second, "198.51.100.2", agentSafariIPhone, "/app.js", 200),
		// Lower-case is still a browser's agent.
		visit(2*time.Second, "198.51.100.3", "mozilla/5.0 (X11; Linux x86_64) Firefox/127.0", "/", 200),
		visit(3*time.Second, "66.249.66.1", agentGooglebot, "/", 200),
		visit(4*time.Second, "203.0.113.9", agentCurl, "/", 200),
		// A scanner wearing a browser's agent is not a person.
		visit(5*time.Second, "203.0.113.10", agentChromeWindows, "/wp-login.php", 404),
		visit(6*time.Second, "203.0.113.11", "", "/", 200),
	}
	people := audienceOf(Filter{Who: WhoPeople}, entries...).Summary.Total
	bots := audienceOf(Filter{Who: WhoBots}, entries...).Summary.Total
	if people != 3 || bots != 4 {
		t.Fatalf("people = %d, bots = %d; want 3 and 4", people, bots)
	}
	if people+bots != len(entries) {
		t.Fatal("the two sides do not add up to the window")
	}
}

func TestHistogramColumnsForAWeekAndAMonth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 19, 12, 34, 56, 0, time.UTC)
	for _, tc := range []struct {
		span  time.Duration
		width int
	}{
		{7 * 24 * time.Hour, 3 * 3600},
		{30 * 24 * time.Hour, 24 * 3600},
		// A day keeps its sixty columns of 24 minutes.
		{24 * time.Hour, 24 * 60},
	} {
		c := NewCollector(Filter{Since: now.Add(-tc.span), Limit: 1})
		c.Feed(entry(now.Add(-tc.span).Add(time.Minute), "GET", "/", 200, 1))
		c.Feed(entry(now, "GET", "/", 200, 1))
		got := c.Result().Summary
		if got.BucketSeconds != tc.width {
			t.Fatalf("%v: columns are %ds wide, want %d", tc.span, got.BucketSeconds, tc.width)
		}
		if len(got.Buckets) > buckets {
			t.Fatalf("%v: %d columns, more than the chart's %d", tc.span, len(got.Buckets), buckets)
		}
		total := 0
		for _, b := range got.Buckets {
			total += b.Total
		}
		if total != 2 {
			t.Fatalf("%v: the columns hold %d of 2 requests", tc.span, total)
		}
		if tc.width >= 3600 {
			start, _ := time.Parse(time.RFC3339, got.Buckets[0].Start)
			if start.Unix()%int64(tc.width) != 0 {
				t.Fatalf("%v: the first column starts at %s, not on a %ds boundary", tc.span, got.Buckets[0].Start, tc.width)
			}
		}
	}
}

// Past the sample's bound the percentiles are of the whole window: a week
// whose second half is slow has a slow p75, where keeping only the first
// samples read the week as fast throughout.
func TestLatencyPercentilesSampleTheWholeWindow(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{Limit: 1})
	for i := 0; i < 2*latencyCap; i++ {
		ms := 1.0
		if i >= latencyCap {
			ms = 100
		}
		c.Feed(entry(base.Add(time.Duration(i)*time.Second), "GET", "/", 200, ms))
	}
	got := c.Result().Summary
	if got.Latency == nil || got.Latency.P75 != 100 {
		t.Fatalf("latency = %+v, want a p75 of the slow half", got.Latency)
	}
	if mean := got.Latency.Mean; mean < 50.4 || mean > 50.6 {
		t.Fatalf("mean = %v, want 50.5 over every request", mean)
	}
	if got.Truncated {
		t.Fatal("a sampled percentile marked the window a floor")
	}
}
