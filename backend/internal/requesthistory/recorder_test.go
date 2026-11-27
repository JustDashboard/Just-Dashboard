package requesthistory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// disk is a route's record as the ingress keeps it: a live file that grows,
// is renamed aside on a roll, can be truncated in place, and can disappear.
type disk struct {
	mu     sync.Mutex
	clock  *clock
	live   *generation
	rolled []*generation
	next   int
	reads  int
}

type generation struct {
	identity string
	data     []byte
	modified time.Time
}

func (d *disk) file() *generation {
	d.next++
	return &generation{identity: fmt.Sprintf("ino-%d", d.next), modified: d.clock.Now()}
}

// write appends one request per path, a second apart from at.
func (d *disk) write(at time.Time, paths ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.live == nil {
		d.live = d.file()
	}
	for i, path := range paths {
		d.live.data = append(d.live.data, caddyLine(at.Add(time.Duration(i)*time.Second), path, 200, "x")...)
		d.live.data = append(d.live.data, '\n')
	}
	d.live.modified = d.clock.Now()
}

func (d *disk) roll() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.live != nil {
		d.rolled = append(d.rolled, d.live)
	}
	d.live = d.file()
}

// dropOldest deletes the oldest rolled generation, as the roller's keep does.
func (d *disk) dropOldest() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rolled = d.rolled[1:]
}

func (d *disk) truncate() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.live.data = nil
}

// renumber gives every generation a new identity with its bytes unchanged:
// the volume copied somewhere new, which no cursor can follow.
func (d *disk) renumber() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, g := range append(append([]*generation{}, d.rolled...), d.live) {
		d.next++
		g.identity = fmt.Sprintf("ino-%d", d.next)
	}
}

func (d *disk) stat(g *generation) accesslog.FileStat {
	if g == nil {
		return accesslog.FileStat{}
	}
	return accesslog.FileStat{Exists: true, Identity: g.identity, Size: int64(len(g.data)), Modified: g.modified}
}

func (d *disk) Read(_ context.Context, identity string, offset, limit int64, fn func(string)) (accesslog.FileStat, accesslog.FileStat, int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reads++
	live := d.stat(d.live)
	var target *generation
	if identity == "" || (d.live != nil && d.live.identity == identity) {
		target = d.live
	} else {
		for _, g := range d.rolled {
			if g.identity == identity {
				target = g
			}
		}
	}
	if target == nil {
		return accesslog.FileStat{}, live, offset, nil
	}
	read := d.stat(target)
	if limit <= 0 || offset >= int64(len(target.data)) {
		return read, live, offset, nil
	}
	end := min(int64(len(target.data)), offset+limit)
	return read, live, offset + accesslog.ConsumeLines(target.data[offset:end], end-offset == limit, fn), nil
}

func (d *disk) Rolled(context.Context) ([]accesslog.FileStat, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]accesslog.FileStat, 0, len(d.rolled))
	for _, g := range d.rolled {
		out = append(out, d.stat(g))
	}
	return out, nil
}

func newRecorder(a *Archive, c *clock, d *disk) *Recorder {
	r := NewRecorder(a, func(context.Context, string) (accesslog.Reader, accesslog.Facts, error) {
		return d, accesslog.Facts{}, nil
	}, func(context.Context) ([]string, error) { return []string{"r"}, nil }, nil)
	r.now = c.Now
	return r
}

// passes runs n of the recorder's passes, a tick apart.
func passes(t *testing.T, r *Recorder, c *clock, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := r.Tick(context.Background()); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		c.Advance(tickEvery)
	}
}

// kept is the paths of what the history holds, oldest first, and fails on a
// request kept twice.
func kept(t *testing.T, a *Archive) []string {
	t.Helper()
	var out []string
	seen := map[int64]bool{}
	for _, e := range scanAll(t, a, "r") {
		if seen[e.UnixNano()] {
			t.Fatalf("%s at %s is kept twice", e.Path, e.At())
		}
		seen[e.UnixNano()] = true
		out = append(out, e.Path)
	}
	return out
}

func want(t *testing.T, a *Archive, paths ...string) {
	t.Helper()
	if got := kept(t, a); strings.Join(got, " ") != strings.Join(paths, " ") {
		t.Fatalf("the history holds\n %v\nwant\n %v", got, paths)
	}
}

func setup(t *testing.T) (*Archive, *clock, *disk, time.Time) {
	t.Helper()
	a, c := openTest(t)
	return a, c, &disk{clock: c}, c.Now().Add(-time.Hour)
}

func TestARouteNeverRecordedIsReadFromItsOldestGenerationOnASmallBudget(t *testing.T) {
	a, c, d, at := setup(t)
	d.write(at, "/1", "/2", "/3")
	d.roll()
	d.write(at.Add(time.Minute), "/4", "/5")
	d.roll()
	d.write(at.Add(2*time.Minute), "/6")
	r := newRecorder(a, c, d)
	// Two lines a read: the backfill takes several passes and ends in order.
	r.perRoute = int64(2*len(caddyLine(at, "/1", 200, "x")) + 2)
	passes(t, r, c, 8)
	want(t, a, "/1", "/2", "/3", "/4", "/5", "/6")
	if span, _ := a.Span(context.Background(), "r"); !span.LostAt.IsZero() || !span.Recording {
		t.Fatalf("span = %+v, want nothing lost and the route recording", span)
	}
}

func TestTheRecorderKeepsWhatIsAppendedAndNothingTwice(t *testing.T) {
	a, c, d, at := setup(t)
	d.write(at, "/1")
	r := newRecorder(a, c, d)
	passes(t, r, c, 1)
	d.write(at.Add(time.Minute), "/2", "/3")
	passes(t, r, c, 1)
	passes(t, r, c, 3)
	want(t, a, "/1", "/2", "/3")
}

func TestARollIsFollowedThroughTheOldTailAndEveryNewerGeneration(t *testing.T) {
	a, c, d, at := setup(t)
	d.write(at, "/1")
	r := newRecorder(a, c, d)
	passes(t, r, c, 1)
	// Between two passes: more written, rolled, written, rolled again.
	d.write(at.Add(time.Minute), "/2")
	d.roll()
	d.write(at.Add(2*time.Minute), "/3")
	d.roll()
	d.write(at.Add(3*time.Minute), "/4")
	passes(t, r, c, 4)
	want(t, a, "/1", "/2", "/3", "/4")
	if span, _ := a.Span(context.Background(), "r"); !span.LostAt.IsZero() {
		t.Fatalf("a roll followed to its end says it lost something at %v", span.LostAt)
	}
}

func TestATruncationIsReadFromItsStartAndRecordedAsALoss(t *testing.T) {
	a, c, d, at := setup(t)
	d.write(at, "/1", "/2")
	r := newRecorder(a, c, d)
	passes(t, r, c, 1)
	d.write(at.Add(time.Minute), "/lost")
	d.truncate()
	d.write(at.Add(2*time.Minute), "/3")
	passes(t, r, c, 3)
	want(t, a, "/1", "/2", "/3")
	if span, _ := a.Span(context.Background(), "r"); span.LostAt.IsZero() {
		t.Fatal("a truncation that took an unread request says nothing was lost")
	}
}

func TestAGenerationGoneBeforeItsEndWasReadIsALossAndTheRestIsRead(t *testing.T) {
	a, c, d, at := setup(t)
	d.write(at, "/1")
	r := newRecorder(a, c, d)
	passes(t, r, c, 1)
	// While the recorder was not looking: written, rolled, and the rolled
	// generation deleted before its tail was read.
	d.write(at.Add(time.Minute), "/lost")
	d.roll()
	d.write(at.Add(2*time.Minute), "/2")
	d.roll()
	d.write(at.Add(3*time.Minute), "/3")
	d.dropOldest()
	passes(t, r, c, 4)
	want(t, a, "/1", "/2", "/3")
	if span, _ := a.Span(context.Background(), "r"); span.LostAt.IsZero() {
		t.Fatal("a generation deleted before it was read says nothing was lost")
	}
}

func TestBytesReadAgainUnderANewIdentityAreNotKeptTwice(t *testing.T) {
	a, c, d, at := setup(t)
	d.write(at, "/1", "/2")
	r := newRecorder(a, c, d)
	passes(t, r, c, 1)
	d.write(at.Add(time.Minute), "/3")
	d.renumber()
	passes(t, r, c, 3)
	want(t, a, "/1", "/2", "/3")
}

func TestARestartContinuesFromTheKeptCursor(t *testing.T) {
	dir := t.TempDir()
	c := &clock{now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	d := &disk{clock: c}
	at := c.Now().Add(-time.Hour)
	open := func() *Archive {
		a, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		a.now = c.Now
		return a
	}
	d.write(at, "/1", "/2", "/3", "/4")
	first := open()
	r := newRecorder(first, c, d)
	// Halfway through the file when the process stops.
	r.perRoute = int64(2*len(caddyLine(at, "/1", 200, "x")) + 2)
	passes(t, r, c, 1)
	first.Close()

	d.write(at.Add(time.Minute), "/5")
	second := open()
	defer second.Close()
	passes(t, newRecorder(second, c, d), c, 3)
	want(t, second, "/1", "/2", "/3", "/4", "/5")
	if span, _ := second.Span(context.Background(), "r"); !span.LostAt.IsZero() {
		t.Fatal("a restart says it lost something")
	}
}

func TestAQuietRouteIsReadLessOften(t *testing.T) {
	a, c, d, at := setup(t)
	d.write(at, "/1")
	r := newRecorder(a, c, d)
	passes(t, r, c, 12)
	// Twelve passes over four minutes; a route with nothing new backs off to
	// one read every two minutes, so far fewer than twelve reads.
	if d.reads > 7 {
		t.Fatalf("a quiet route was read %d times in twelve passes", d.reads)
	}
	d.write(at.Add(time.Minute), "/2")
	passes(t, r, c, 7)
	want(t, a, "/1", "/2")
}

func TestARouteNoLongerListedIsNotRecording(t *testing.T) {
	a, c, d, at := setup(t)
	d.write(at, "/1")
	routes := []string{"r"}
	r := NewRecorder(a, func(context.Context, string) (accesslog.Reader, accesslog.Facts, error) {
		return d, accesslog.Facts{}, nil
	}, func(context.Context) ([]string, error) { return routes, nil }, nil)
	r.now = c.Now
	passes(t, r, c, 1)
	routes = nil
	c.Advance(listEvery)
	passes(t, r, c, 1)
	if span, _ := a.Span(context.Background(), "r"); span.Recording || span.From.IsZero() {
		t.Fatalf("span = %+v, want the history kept and the route not recording", span)
	}
}
