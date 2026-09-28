package accesslog

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeFile is one generation on a fake disk.
type fakeFile struct {
	identity string
	data     []byte
	modified time.Time
}

// fakeReader models a record as the drivers see it: a live file that grows,
// gets renamed aside on a roll, can be truncated in place, and can disappear.
type fakeReader struct {
	mu     sync.Mutex
	live   *fakeFile
	rolled []*fakeFile
	nextID int
	clock  *fakeClock

	reads     int
	bytesRead int64
	fail      error
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newFake() (*fakeReader, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)}
	return &fakeReader{clock: clock}, clock
}

func (f *fakeReader) newFile() *fakeFile {
	f.nextID++
	return &fakeFile{identity: fmt.Sprintf("ino-%d", f.nextID), modified: f.clock.Now()}
}

func (f *fakeReader) append(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live == nil {
		f.live = f.newFile()
	}
	f.live.data = append(f.live.data, line...)
	f.live.data = append(f.live.data, '\n')
	f.live.modified = f.clock.Now()
}

// appendPartial writes a line without its newline, as a proxy mid-write does.
func (f *fakeReader) appendPartial(text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live == nil {
		f.live = f.newFile()
	}
	f.live.data = append(f.live.data, text...)
}

func (f *fakeReader) roll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.live != nil {
		f.rolled = append(f.rolled, f.live)
	}
	f.live = f.newFile()
}

func (f *fakeReader) deleteRolled() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rolled = nil
}

func (f *fakeReader) truncate() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live.data = nil
}

func (f *fakeReader) remove() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live, f.rolled = nil, nil
}

func (f *fakeReader) stat(file *fakeFile) FileStat {
	if file == nil {
		return FileStat{}
	}
	return FileStat{Exists: true, Identity: file.identity, Size: int64(len(file.data)), Modified: file.modified}
}

func (f *fakeReader) Read(_ context.Context, identity string, offset, limit int64, fn func(string)) (FileStat, FileStat, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return FileStat{}, FileStat{}, offset, f.fail
	}
	f.reads++
	live := f.stat(f.live)
	var target *fakeFile
	if identity == "" {
		target = f.live
	} else if f.live != nil && f.live.identity == identity {
		target = f.live
	} else {
		for _, gen := range f.rolled {
			if gen.identity == identity {
				target = gen
			}
		}
	}
	if target == nil {
		return FileStat{}, live, offset, nil
	}
	read := f.stat(target)
	if limit <= 0 || offset >= int64(len(target.data)) {
		return read, live, offset, nil
	}
	end := int64(len(target.data))
	if end-offset > limit {
		end = offset + limit
	}
	chunk := target.data[offset:end]
	f.bytesRead += int64(len(chunk))
	consumed := ConsumeLines(chunk, end-offset == limit, fn)
	return read, live, offset + consumed, nil
}

func (f *fakeReader) Rolled(context.Context) ([]FileStat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	out := make([]FileStat, 0, len(f.rolled))
	for _, gen := range f.rolled {
		out = append(out, f.stat(gen))
	}
	return out, nil
}

func line(at time.Time, path string, status int) string {
	return fmt.Sprintf(`{"ts":%q,"msg":"handled request","request":{"method":"GET","host":"h","uri":%q,"remote_ip":"1.1.1.1"},"status":%d,"size":10,"duration":0.01}`,
		at.Format(time.RFC3339Nano), path, status)
}

func newTestStore(reader *fakeReader, clock *fakeClock) *Store {
	s := NewStore(func(context.Context, string) (Reader, Facts, error) {
		return reader, Facts{Driver: "fake", Format: FormatCaddyJSON, Latency: true}, nil
	})
	s.now = clock.Now
	return s
}

func paths(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func TestStoreReadsOnlyWhatWasAppended(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	reader.append(line(now.Add(-2*time.Minute), "/a", 200))
	reader.append(line(now.Add(-time.Minute), "/b", 200))
	store := newTestStore(reader, clock)

	w, err := store.Window(context.Background(), "r", Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if w.Result.Summary.Total != 2 || !w.Coverage.Complete || !w.Coverage.Exists {
		t.Fatalf("seed: %+v %+v", w.Result.Summary, w.Coverage)
	}
	seedBytes := reader.bytesRead

	// Asked again within the refresh interval: no read at all.
	reads := reader.reads
	if _, err := store.Window(context.Background(), "r", Filter{Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if reader.reads != reads {
		t.Fatalf("a second poller inside the interval read the file again (%d → %d)", reads, reader.reads)
	}

	// Appended, and past the interval: only the new line's bytes are read.
	clock.Advance(time.Second)
	reader.append(line(now, "/c", 500))
	w, err = store.Window(context.Background(), "r", Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := reader.bytesRead - seedBytes; got != int64(len(line(now, "/c", 500))+1) {
		t.Fatalf("advance read %d bytes, want exactly the appended line", got)
	}
	if w.Result.Summary.Total != 3 || w.Result.Entries[0].Path != "/c" {
		t.Fatalf("advance: %v", paths(w.Result.Entries))
	}
	if w.Coverage.Cursor != 3 {
		t.Fatalf("cursor = %d, want the third request's sequence", w.Coverage.Cursor)
	}
}

func TestStoreLeavesAHalfWrittenLineForTheNextRead(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	reader.append(line(now, "/a", 200))
	reader.appendPartial(`{"ts":"2026-09-19T12:00:01Z","msg":"handled request","request":{"method":"GET","uri":"/half"`)
	store := newTestStore(reader, clock)

	w, _ := store.Window(context.Background(), "r", Filter{Limit: 10})
	if w.Result.Summary.Total != 1 {
		t.Fatalf("a line still being written must not be parsed: %v", paths(w.Result.Entries))
	}
	// The proxy finishes the line; the next read starts exactly where the
	// whole lines ended, so the finished line arrives intact.
	clock.Advance(time.Second)
	reader.appendPartial(`,"remote_ip":"1.1.1.1"},"status":200,"size":1,"duration":0.001}` + "\n")
	w, _ = store.Window(context.Background(), "r", Filter{Limit: 10})
	if w.Result.Summary.Total != 2 || w.Result.Entries[0].Path != "/half" {
		t.Fatalf("the finished line did not arrive whole: %v", paths(w.Result.Entries))
	}
}

func TestStoreFollowsARollWithoutAGap(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	reader.append(line(now.Add(-3*time.Minute), "/1", 200))
	store := newTestStore(reader, clock)
	if _, err := store.Window(context.Background(), "r", Filter{Limit: 10}); err != nil {
		t.Fatal(err)
	}

	// Between two reads: more written to the old generation, then a roll,
	// then writes to the new one. The old generation's tail is the classic
	// gap — a follower bound to the path misses it entirely.
	clock.Advance(time.Second)
	reader.append(line(now.Add(-2*time.Minute), "/2-old-tail", 200))
	reader.roll()
	clock.Advance(time.Second)
	reader.append(line(now.Add(-time.Minute), "/3-new", 200))

	w, err := store.Window(context.Background(), "r", Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(w.Result.Entries); strings.Join(got, ",") != "/3-new,/2-old-tail,/1" {
		t.Fatalf("after the roll: %v", got)
	}
	if !w.Coverage.Complete {
		t.Fatal("nothing was lost across the roll, so the record is still complete")
	}
	for i, e := range w.Result.Entries {
		if e.Seq != uint64(3-i) {
			t.Fatalf("sequences must keep rising across a roll: %v", w.Result.Entries)
		}
	}

	// And the new generation keeps advancing.
	clock.Advance(time.Second)
	reader.append(line(now, "/4", 200))
	w, _ = store.Window(context.Background(), "r", Filter{Limit: 10})
	if w.Result.Summary.Total != 4 || w.Result.Entries[0].Path != "/4" {
		t.Fatalf("after following onto the new file: %v", paths(w.Result.Entries))
	}
}

func TestStoreRollWhoseOldGenerationWasDeleted(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	reader.append(line(now.Add(-3*time.Minute), "/1", 200))
	store := newTestStore(reader, clock)
	store.Window(context.Background(), "r", Filter{Limit: 10})

	clock.Advance(time.Second)
	reader.append(line(now.Add(-2*time.Minute), "/lost", 200))
	reader.roll()
	reader.deleteRolled()
	reader.append(line(now.Add(-time.Minute), "/new", 200))

	w, err := store.Window(context.Background(), "r", Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(w.Result.Entries); strings.Join(got, ",") != "/new,/1" {
		t.Fatalf("got %v", got)
	}
	if w.Coverage.Complete {
		t.Fatal("the old generation's tail is gone, and the record must say so")
	}
}

func TestStoreSurvivesTruncationInPlace(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	reader.append(line(now.Add(-2*time.Minute), "/before", 200))
	store := newTestStore(reader, clock)
	store.Window(context.Background(), "r", Filter{Limit: 10})

	// copytruncate: same identity, the file shrinks to nothing, then grows.
	clock.Advance(time.Second)
	reader.truncate()
	reader.append(line(now.Add(-time.Minute), "/after", 200))

	w, err := store.Window(context.Background(), "r", Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(w.Result.Entries); strings.Join(got, ",") != "/after,/before" {
		t.Fatalf("got %v", got)
	}
	if w.Coverage.Complete {
		t.Fatal("a truncation can lose what was written just before it; the record must not claim completeness")
	}
}

func TestStoreSeedsRolledGenerationsInOrder(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	reader.append(line(now.Add(-3*time.Hour), "/oldest", 200))
	reader.roll()
	clock.Advance(time.Hour)
	reader.append(line(now.Add(-2*time.Hour), "/middle", 200))
	reader.roll()
	clock.Advance(time.Hour)
	reader.append(line(now.Add(-time.Hour), "/live", 200))
	store := newTestStore(reader, clock)

	w, err := store.Window(context.Background(), "r", Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got := paths(w.Result.Entries); strings.Join(got, ",") != "/live,/middle,/oldest" {
		t.Fatalf("rolled generations must be read oldest first, then the live file: %v", got)
	}
	if !w.Coverage.Complete || w.Coverage.From == nil || w.Coverage.From.Sub(now.Add(-3*time.Hour)) != 0 {
		t.Fatalf("coverage = %+v", w.Coverage)
	}
}

func TestStoreSeedBudgetKeepsTheNewest(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	// One line is about 150 bytes; make the record much larger than the budget
	// by writing a filler path.
	filler := strings.Repeat("x", 4096)
	perLine := len(line(now, "/"+filler, 200)) + 1
	count := seedBudget/perLine + 50
	for i := 0; i < count; i++ {
		reader.append(line(now.Add(time.Duration(i)*time.Millisecond), "/"+filler, 200))
	}
	reader.append(line(now.Add(time.Hour), "/newest", 200))
	store := newTestStore(reader, clock)

	w, err := store.Window(context.Background(), "r", Filter{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if w.Coverage.Complete {
		t.Fatal("a seed that could not afford the whole record must say so")
	}
	if w.Result.Entries[0].Path != "/newest" {
		t.Fatalf("the newest request must survive the budget: %v", paths(w.Result.Entries[:1]))
	}
	if reader.bytesRead > int64(seedBudget+perLine) {
		t.Fatalf("seed read %d bytes against a budget of %d", reader.bytesRead, seedBudget)
	}
}

func TestStoreEvictsPastTheRouteCap(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	store := newTestStore(reader, clock)
	for i := 0; i < routeCap+10; i++ {
		reader.append(line(now.Add(time.Duration(i)*time.Millisecond), "/p", 200))
	}
	w, err := store.Window(context.Background(), "r", Filter{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if w.Coverage.Held > routeCap || w.Coverage.Held < routeCap-routeCap/10-1 {
		t.Fatalf("held %d, want the cap minus one tenth", w.Coverage.Held)
	}
	if w.Coverage.Complete {
		t.Fatal("evicting from the start of the record makes it incomplete")
	}
	if w.Coverage.Cursor != uint64(routeCap+10) {
		t.Fatalf("cursor = %d: sequences must not be renumbered by eviction", w.Coverage.Cursor)
	}
	// The oldest held has the sequence the eviction left it with.
	after, _, _ := store.After(context.Background(), "r", 0, Filter{}, 1)
	if len(after) != 1 || after[0].Seq != uint64(routeCap+10-w.Coverage.Held+1) {
		t.Fatalf("oldest held = %+v", after)
	}
}

func TestStoreAfterContinuesFromASequence(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	reader.append(line(now, "/1", 200))
	reader.append(line(now, "/2", 500))
	store := newTestStore(reader, clock)

	w, _ := store.Window(context.Background(), "r", Filter{Limit: 10})
	cursor := w.Coverage.Cursor
	got, next, err := store.After(context.Background(), "r", cursor, Filter{}, 100)
	if err != nil || len(got) != 0 || next != cursor {
		t.Fatalf("nothing new yet: %v %d %v", got, next, err)
	}

	clock.Advance(time.Second)
	reader.append(line(now, "/3", 200))
	reader.append(line(now, "/4", 503))
	got, next, _ = store.After(context.Background(), "r", cursor, Filter{}, 100)
	if strings.Join(paths(got), ",") != "/3,/4" || next != cursor+2 {
		t.Fatalf("after %d: %v (next %d)", cursor, paths(got), next)
	}
	// The filter applies to the tail as it does to the window.
	only5xx, _, _ := store.After(context.Background(), "r", cursor, Filter{Classes: []string{"5xx"}}, 100)
	if strings.Join(paths(only5xx), ",") != "/4" {
		t.Fatalf("filtered tail: %v", paths(only5xx))
	}
	// FromNow positions without replaying.
	fresh, next, _ := store.After(context.Background(), "r", FromNow, Filter{}, 100)
	if len(fresh) != 0 || next != cursor+2 {
		t.Fatalf("FromNow replayed: %v", paths(fresh))
	}
	// A cursor from before what is held — an earlier process, say — gets
	// everything held rather than nothing.
	all, _, _ := store.After(context.Background(), "r", 0, Filter{}, 100)
	if len(all) != 4 {
		t.Fatalf("a stale cursor should yield everything held, got %d", len(all))
	}
}

func TestStoreWindowFindsOutOfOrderRequests(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	// Completion order is not time order: a slow request that started earlier
	// lands in the file after a fast one that started later.
	reader.append(line(now.Add(-10*time.Minute), "/old", 200))
	reader.append(line(now.Add(-1*time.Minute), "/fast", 200))
	reader.append(line(now.Add(-2*time.Minute), "/slow-but-earlier", 200))
	reader.append(line(now, "/now", 200))
	store := newTestStore(reader, clock)

	w, _ := store.Window(context.Background(), "r", Filter{Since: now.Add(-5 * time.Minute), Limit: 10})
	got := paths(w.Result.Entries)
	if strings.Join(got, ",") != "/now,/slow-but-earlier,/fast" {
		t.Fatalf("window = %v: the out-of-order request must be found and /old must not", got)
	}
}

func TestStoreDropsIdleRoutes(t *testing.T) {
	reader, clock := newFake()
	reader.append(line(clock.Now(), "/a", 200))
	store := newTestStore(reader, clock)
	store.Window(context.Background(), "idle", Filter{Limit: 10})
	store.Window(context.Background(), "busy", Filter{Limit: 10})

	clock.Advance(idleFor + time.Minute)
	store.Window(context.Background(), "busy", Filter{Limit: 10})
	store.mu.Lock()
	_, idleHeld := store.routes["idle"]
	_, busyHeld := store.routes["busy"]
	store.mu.Unlock()
	if idleHeld || !busyHeld {
		t.Fatalf("idle=%v busy=%v: a route nobody asked about for half an hour is dropped, the one being read is not", idleHeld, busyHeld)
	}
}

func TestStoreExplainsAnAbsentRecord(t *testing.T) {
	reader, clock := newFake()
	store := newTestStore(reader, clock)
	w, err := store.Window(context.Background(), "r", Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if w.Coverage.Exists || w.Coverage.Held != 0 || w.Result.Summary.Total != 0 {
		t.Fatalf("absent record: %+v", w.Coverage)
	}
	// The record appears later — the first request — and is picked up.
	clock.Advance(time.Second)
	reader.append(line(clock.Now(), "/first", 200))
	w, _ = store.Window(context.Background(), "r", Filter{Limit: 10})
	if !w.Coverage.Exists || w.Result.Summary.Total != 1 {
		t.Fatalf("a record that appeared was not picked up: %+v", w.Coverage)
	}
}

func TestStoreServesStaleOnAFailedRefresh(t *testing.T) {
	reader, clock := newFake()
	reader.append(line(clock.Now(), "/a", 200))
	store := newTestStore(reader, clock)
	store.Window(context.Background(), "r", Filter{Limit: 10})

	clock.Advance(time.Second)
	reader.fail = errors.New("docker exec: broken pipe")
	w, err := store.Window(context.Background(), "r", Filter{Limit: 10})
	if err != nil {
		t.Fatalf("held data must be served, not refused: %v", err)
	}
	if !w.Coverage.Stale || w.Result.Summary.Total != 1 {
		t.Fatalf("stale answer: %+v total %d", w.Coverage, w.Result.Summary.Total)
	}
	// And the reader is resolved again once the failure clears.
	reader.fail = nil
	clock.Advance(time.Second)
	reader.append(line(clock.Now(), "/b", 200))
	w, _ = store.Window(context.Background(), "r", Filter{Limit: 10})
	if w.Coverage.Stale || w.Result.Summary.Total != 2 {
		t.Fatalf("after recovery: %+v total %d", w.Coverage, w.Result.Summary.Total)
	}
}

func TestStoreRefusesWhenNothingWasEverRead(t *testing.T) {
	reader, clock := newFake()
	reader.fail = errors.New("no ingress")
	store := newTestStore(reader, clock)
	if _, err := store.Window(context.Background(), "r", Filter{Limit: 10}); err == nil {
		t.Fatal("with nothing held and the read failing, the answer is an error, not an empty page")
	}
}

func TestStoreEmptiesARemovedRoute(t *testing.T) {
	reader, clock := newFake()
	reader.append(line(clock.Now(), "/a", 200))
	store := newTestStore(reader, clock)
	store.Window(context.Background(), "r", Filter{Limit: 10})

	clock.Advance(time.Second)
	reader.remove()
	w, err := store.Window(context.Background(), "r", Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if w.Coverage.Exists || w.Coverage.Held != 0 {
		t.Fatalf("a removed route's record must not linger: %+v", w.Coverage)
	}
}

func TestConsumeLines(t *testing.T) {
	var got []string
	n := ConsumeLines([]byte("a\r\nb\nc"), false, func(s string) { got = append(got, s) })
	if strings.Join(got, "|") != "a|b" || n != 5 {
		t.Fatalf("got %v, consumed %d", got, n)
	}
	// A single line that fills the whole read is skipped rather than waited on.
	got = nil
	n = ConsumeLines([]byte("no newline at all"), true, func(s string) { got = append(got, s) })
	if len(got) != 0 || n != int64(len("no newline at all")) {
		t.Fatalf("overlong line: got %v, consumed %d", got, n)
	}
	// But a partial line in a read that was not full is left alone.
	if n := ConsumeLines([]byte("partial"), false, nil); n != 0 {
		t.Fatalf("partial line consumed %d bytes", n)
	}
}

func TestStoreExportHandsTheWholeWindowOldestFirst(t *testing.T) {
	reader, clock := newFake()
	now := clock.Now()
	for i := 0; i < 700; i++ {
		reader.append(line(now.Add(time.Duration(i)*time.Second), fmt.Sprintf("/p%d", i), 200))
	}
	store := newTestStore(reader, clock)
	var got []string
	n, err := store.Export(context.Background(), "r", Filter{}, 0, func(e Entry) { got = append(got, e.Path) })
	if err != nil || n != 700 || len(got) != 700 {
		t.Fatalf("export n=%d len=%d err=%v", n, len(got), err)
	}
	if got[0] != "/p0" || got[699] != "/p699" {
		t.Fatalf("export order wrong: first %s last %s", got[0], got[699])
	}
	// A filter and a limit both apply.
	got = nil
	n, _ = store.Export(context.Background(), "r", Filter{Path: "/p1"}, 3, func(e Entry) { got = append(got, e.Path) })
	if n != 3 || len(got) != 3 {
		t.Fatalf("limited export: %v", got)
	}
}

func TestStoreCachedSkipsTheReadInsideMaxAge(t *testing.T) {
	reader, clock := newFake()
	reader.append(line(clock.Now(), "/a", 200))
	store := newTestStore(reader, clock)
	if _, err := store.Window(context.Background(), "r", Filter{Limit: 5}); err != nil {
		t.Fatal(err)
	}
	reads := reader.reads
	clock.Advance(20 * time.Second)
	reader.append(line(clock.Now(), "/b", 200))
	w, err := store.Cached(context.Background(), "r", Filter{Limit: 5}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if reader.reads != reads {
		t.Fatal("a cached read inside its max age must not touch the file")
	}
	if w.Result.Summary.Total != 1 {
		t.Fatalf("cached answer should be the held one: total %d", w.Result.Summary.Total)
	}
	clock.Advance(time.Minute)
	w, _ = store.Cached(context.Background(), "r", Filter{Limit: 5}, time.Minute)
	if w.Result.Summary.Total != 2 {
		t.Fatalf("past its max age the cached read refreshes: total %d", w.Result.Summary.Total)
	}
}
