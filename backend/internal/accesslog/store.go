package accesslog

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// The record, kept rather than re-read.
//
// An access log has no index. It is append-only and time-ordered, which means
// the only way to answer "the last hour" from the file is to read from
// somewhere before the last hour to the end — and with nothing to say where
// that is, the honest read was the last 24 MB every time. Two pollers on one
// page made that two scans a minute of the same bytes to produce the same
// figures, and a busy deployment's hour would not fit in the window anyway.
//
// The fix is the one the metrics recorder and the Docker event log already
// made: this process is long-running and already attached to the thing
// producing the record, so it should read each byte once and keep what it
// parsed. A route's record is seeded from what is on disk, then advanced by
// reading only what was appended since — a stat and a short read, not a scan
// — and every question is answered from memory: a time-ordered slice with a
// running high-water mark that a window is found in by binary search. The
// live tail continues from a sequence number rather than a timestamp, which
// is exact where "newer than the last one I saw" is not.
//
// What is kept is bounded by count and by age, and a route nobody has asked
// about for half an hour is dropped whole. The memory is the cost of the
// page being instant, and it goes away when nobody is looking.

// FileStat describes one generation of a record on disk. Identity is the
// inode: it changes when the ingress rolls the file, and it is what tells a
// reader that the bytes at a path are no longer the bytes it was following.
type FileStat struct {
	Exists   bool
	Identity string
	Size     int64
	Modified time.Time
}

// Reader is one route's record as bytes, however it is stored. Rotation is
// the reason it is not an io.Reader: the file at the path is replaced under
// the reader, and the tail of the old generation has to be found again by
// what it is rather than where it was.
type Reader interface {
	// Read hands whole lines from `offset` of the generation carrying
	// `identity` — the live file when identity is empty — to fn, reading at
	// most `limit` bytes; limit zero reads nothing and only reports. It
	// returns the generation it read (Exists false when no file carries that
	// identity any more), the live file as it stood, and the offset just past
	// the last whole line handed over — a line still being written is left for
	// the next read.
	Read(ctx context.Context, identity string, offset, limit int64, fn func(string)) (read, live FileStat, next int64, err error)
	// Rolled lists the rolled generations still on disk, oldest first.
	Rolled(ctx context.Context) ([]FileStat, error)
}

// Facts are what the page needs to know about where a record comes from,
// because the two ingress drivers do not record the same things.
type Facts struct {
	Driver  string `json:"driver,omitempty"`
	Format  Format `json:"format,omitempty"`
	Latency bool   `json:"latency"`
	Path    string `json:"path,omitempty"`
}

// Opener resolves a route to its record. It is called once per route the
// store is asked about, and again only after a read failed — discovery costs
// a Docker inspection and is not something to repeat per poll.
type Opener func(ctx context.Context, route string) (Reader, Facts, error)

// Coverage says what the held record is, so an empty answer can explain
// itself: whether a record exists at all, how far back what is held reaches,
// whether that is the whole retained record or a tail of it, and how fresh
// it is.
type Coverage struct {
	Exists   bool       `json:"exists"`
	From     *time.Time `json:"from,omitempty"`
	To       *time.Time `json:"to,omitempty"`
	Held     int        `json:"held"`
	Complete bool       `json:"complete"`
	// Cursor is the newest request's sequence, for a live tail to continue
	// from exactly.
	Cursor      uint64    `json:"cursor"`
	RefreshedAt time.Time `json:"refreshedAt"`
	// Stale reports that this answer is from the last successful read because
	// the current one failed — served rather than refused, since the figures
	// are still true of the moment they describe.
	Stale bool `json:"stale,omitempty"`
}

// Window is one answer: the rows and readings, what they cover, and where the
// record came from.
type Window struct {
	Result   *Result
	Coverage Coverage
	Facts    Facts
}

// FromNow asks After for only what arrives from here on.
const FromNow = ^uint64(0)

const (
	// refreshEvery is the least time between two reads of the same record.
	// Every poller and every live socket on a route shares one refresh.
	refreshEvery = 400 * time.Millisecond
	// seedBudget bounds the first read: the live file and then as many rolled
	// generations as fit, newest first. A quiet deployment's whole retained
	// record is a few megabytes and fits entirely; a busy one gets its most
	// recent 48 MB and a coverage that says so.
	seedBudget = 48 << 20
	// deltaCap bounds one advance. A record that grew by more than this since
	// the last read is read in successive refreshes rather than in one.
	deltaCap = 32 << 20
	// routeCap bounds one route's held requests; storeCap bounds all of them.
	// At roughly 180 bytes a request with interned strings, a full route is
	// under 30 MB and a full store under 60.
	routeCap = 150_000
	storeCap = 300_000
	// keepFor is the oldest request worth holding — a day past the widest
	// window the page offers.
	keepFor = 8 * 24 * time.Hour
	// idleFor is how long a route goes unasked-about before its record is
	// dropped, and sweepEvery how often that is checked.
	idleFor    = 30 * time.Minute
	sweepEvery = time.Minute
	// internCap bounds the distinct strings a route interns. Past it, new
	// values are kept as they arrive; the common ones are already shared.
	internCap = 20_000
)

// Store keeps the parsed record of every route this process has been asked
// about.
type Store struct {
	open Opener
	// now is injectable so the tests can move time rather than wait for it.
	now func() time.Time

	mu      sync.Mutex
	routes  map[string]*record
	sweptAt time.Time
}

func NewStore(open Opener) *Store {
	return &Store{open: open, now: time.Now, routes: map[string]*record{}}
}

type fileCursor struct {
	identity string
	offset   int64
}

// record is one route's held requests and the position in the file they were
// read up to.
type record struct {
	route  string
	reader Reader
	facts  Facts

	mu      sync.Mutex
	entries []Entry
	// ceil[i] is the largest instant among entries[0..i]. Requests arrive in
	// file order, which is completion order, so two that overlapped can land
	// out of time order by a few milliseconds; a running maximum is monotone
	// regardless, and searching it for a window's start is exact: everything
	// before the index found is older than the window, and the filter decides
	// the rest.
	ceil    []int64
	seqBase uint64
	nextSeq uint64
	cursor  fileCursor
	// seeded says the first read has happened; complete says what is held
	// reaches the start of the retained record — no seed budget cut it, no
	// cap evicted from it, no rotation lost part of it.
	seeded   bool
	complete bool
	exists   bool
	// refreshed is when the record was last successfully advanced; stale is
	// set while the latest attempt failed.
	refreshed time.Time
	stale     bool
	intern    map[string]string

	// used and held are read under the store's lock without taking the
	// record's, for the sweep and the global cap.
	used atomic.Int64
	held atomic.Int64
}

func (s *Store) record(route string, now time.Time) *record {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.sweptAt) >= sweepEvery {
		s.sweptAt = now
		for name, rec := range s.routes {
			if name != route && now.Sub(time.Unix(0, rec.used.Load())) >= idleFor {
				delete(s.routes, name)
			}
		}
	}
	rec := s.routes[route]
	if rec == nil {
		rec = &record{route: route, seqBase: 1, nextSeq: 1, intern: map[string]string{}}
		s.routes[route] = rec
	}
	rec.used.Store(now.UnixNano())
	return rec
}

// enforceCap drops whole records, least recently asked-about first, until
// the store is within its bound. The route being served is never the one
// dropped: it is the one somebody is looking at.
func (s *Store) enforceCap(serving string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		var total int64
		var oldest *record
		var oldestName string
		for name, rec := range s.routes {
			total += rec.held.Load()
			if name == serving {
				continue
			}
			if oldest == nil || rec.used.Load() < oldest.used.Load() {
				oldest, oldestName = rec, name
			}
		}
		if total <= storeCap || oldest == nil {
			return
		}
		delete(s.routes, oldestName)
	}
}

// Forget drops a route's record. A removed deployment's record goes with it
// the next time it is asked for anyway; this is for the caller that knows now.
func (s *Store) Forget(route string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.routes, route)
}

// Window refreshes the route's record — at most once per refreshEvery — and
// answers the filter over it.
func (s *Store) Window(ctx context.Context, route string, filter Filter) (Window, error) {
	return s.window(ctx, route, filter, refreshEvery)
}

func (s *Store) window(ctx context.Context, route string, filter Filter, minGap time.Duration) (Window, error) {
	now := s.now()
	rec := s.record(route, now)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if err := rec.refresh(ctx, s, now, minGap); err != nil && len(rec.entries) == 0 && !rec.seeded {
		return Window{}, err
	}
	s.enforceCap(route)

	c := NewCollector(filter)
	for i := rec.lowerBound(filter.Since); i < len(rec.entries); i++ {
		c.Feed(rec.entries[i])
	}
	c.Format(rec.facts.Format)
	if !rec.complete {
		c.Truncated()
	}
	return Window{Result: c.Result(), Coverage: rec.coverage(), Facts: rec.facts}, nil
}

// Export hands every request in the window that matches the filter to fn,
// oldest first, up to limit. It is the whole answer rather than the newest
// rows: a download is for taking the record somewhere else, not for reading
// it here.
func (s *Store) Export(ctx context.Context, route string, filter Filter, limit int, fn func(Entry)) (int, error) {
	now := s.now()
	rec := s.record(route, now)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if err := rec.refresh(ctx, s, now, refreshEvery); err != nil && len(rec.entries) == 0 && !rec.seeded {
		return 0, err
	}
	if limit <= 0 {
		limit = 50_000
	}
	n := 0
	for i := rec.lowerBound(filter.Since); i < len(rec.entries) && n < limit; i++ {
		if filter.Match(rec.entries[i]) {
			fn(rec.entries[i])
			n++
		}
	}
	return n, nil
}

// Cached answers like Window but is content with a record refreshed within
// maxAge. It is for the readers that ask about many routes at once — the
// fleet's cards — where "fresh to the minute" is plenty and a read of every
// record on every poll would be a read of every record on every poll.
func (s *Store) Cached(ctx context.Context, route string, filter Filter, maxAge time.Duration) (Window, error) {
	return s.window(ctx, route, filter, maxAge)
}

// After refreshes the record and returns, oldest first, the requests numbered
// past `after` that match the filter, up to limit — and the cursor to continue
// from. FromNow returns nothing and only positions the cursor; a cursor behind
// what is held returns everything held, which is a gap the caller can see by
// comparing sequences.
func (s *Store) After(ctx context.Context, route string, after uint64, filter Filter, limit int) ([]Entry, uint64, error) {
	now := s.now()
	rec := s.record(route, now)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if err := rec.refresh(ctx, s, now, refreshEvery); err != nil && len(rec.entries) == 0 && !rec.seeded {
		return nil, 0, err
	}
	start := 0
	switch {
	case after == FromNow:
		start = len(rec.entries)
	case after+1 > rec.seqBase:
		start = int(after + 1 - rec.seqBase)
		if start > len(rec.entries) {
			start = len(rec.entries)
		}
	}
	if limit <= 0 {
		limit = 500
	}
	out := make([]Entry, 0)
	for i := start; i < len(rec.entries) && len(out) < limit; i++ {
		if filter.Match(rec.entries[i]) {
			out = append(out, rec.entries[i])
		}
	}
	return out, rec.nextSeq - 1, nil
}

// Facts reports where a route's record comes from, opening it if needed.
func (s *Store) Facts(ctx context.Context, route string) (Facts, error) {
	now := s.now()
	rec := s.record(route, now)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.reader == nil {
		reader, facts, err := s.open(ctx, route)
		if err != nil {
			return Facts{}, err
		}
		rec.reader, rec.facts = reader, facts
	}
	return rec.facts, nil
}

func (r *record) coverage() Coverage {
	c := Coverage{
		Exists: r.exists, Held: len(r.entries), Complete: r.complete,
		Cursor: r.nextSeq - 1, RefreshedAt: r.refreshed, Stale: r.stale,
	}
	if n := len(r.entries); n > 0 {
		from, to := r.entries[0].At(), time.Unix(0, r.ceil[n-1]).UTC()
		c.From, c.To = &from, &to
	}
	return c
}

func (r *record) lowerBound(since time.Time) int {
	if since.IsZero() || len(r.ceil) == 0 {
		return 0
	}
	target := since.UnixNano()
	return sort.Search(len(r.ceil), func(i int) bool { return r.ceil[i] >= target })
}

// refresh brings the record up to date, once per refreshEvery. The caller
// holds the record's lock, so two pollers arriving together do one read.
func (r *record) refresh(ctx context.Context, s *Store, now time.Time, minGap time.Duration) error {
	if minGap < refreshEvery {
		minGap = refreshEvery
	}
	if !r.refreshed.IsZero() && now.Sub(r.refreshed) < minGap {
		return nil
	}
	if r.reader == nil {
		reader, facts, err := s.open(ctx, r.route)
		if err != nil {
			r.stale = r.seeded
			return err
		}
		r.reader, r.facts = reader, facts
	}
	var err error
	if r.seeded {
		err = r.advance(ctx)
	} else {
		err = r.seed(ctx)
	}
	if err != nil {
		r.stale = true
		// A cancelled request is the caller's, not the reader's; anything
		// else is reason to resolve the ingress again next time, since the
		// container this reader was built on may be gone.
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			r.reader = nil
		}
		return err
	}
	r.seeded, r.stale, r.refreshed = true, false, now
	r.trim(now)
	return nil
}

// seed is the first read: the live file and then as many rolled generations
// as the budget allows, newest first, read in the order they were written.
func (r *record) seed(ctx context.Context) error {
	_, live, _, err := r.reader.Read(ctx, "", 0, 0, nil)
	if err != nil {
		return err
	}
	rolled, err := r.reader.Rolled(ctx)
	if err != nil {
		return err
	}
	r.exists = live.Exists || len(rolled) > 0
	if !r.exists {
		r.complete, r.cursor = true, fileCursor{}
		return nil
	}

	// Which generations, and from where in each. The newest is worth the
	// most, so the budget is spent from the live file backwards; a generation
	// the budget only reaches part of is read from that point, and its first
	// partial line is dropped by the parser.
	type piece struct {
		identity string
		from     int64
	}
	var plan []piece
	remaining := int64(seedBudget)
	complete := true
	take := func(f FileStat) {
		if remaining <= 0 {
			complete = false
			return
		}
		from := int64(0)
		if f.Size > remaining {
			from = f.Size - remaining
			complete = false
		}
		remaining -= f.Size - from
		plan = append(plan, piece{f.Identity, from})
	}
	if live.Exists {
		take(live)
	}
	for i := len(rolled) - 1; i >= 0; i-- {
		take(rolled[i])
	}

	// Written oldest first, so the held slice is in time order.
	for i := len(plan) - 1; i >= 0; i-- {
		p := plan[i]
		read, _, next, err := r.reader.Read(ctx, p.identity, p.from, seedBudget, r.feed)
		if err != nil {
			return err
		}
		if live.Exists && p.identity == live.Identity && read.Exists {
			r.cursor = fileCursor{read.Identity, next}
		}
	}
	if !live.Exists {
		r.cursor = fileCursor{}
	}
	r.complete = complete
	return nil
}

// advance reads what was appended since the last read, and follows the file
// across a roll.
func (r *record) advance(ctx context.Context) error {
	read, live, next, err := r.reader.Read(ctx, r.cursor.identity, r.cursor.offset, deltaCap, r.feed)
	if err != nil {
		return err
	}
	switch {
	case !read.Exists && r.cursor.identity != "":
		// The generation being followed is gone before its tail was read —
		// rolled and already deleted, or the route removed. Whatever it
		// carried past the cursor is lost.
		r.complete = false
		r.cursor = fileCursor{}
	case read.Exists && read.Size < r.cursor.offset:
		// Shorter than where the last read ended, same identity: truncated in
		// place, which is what logrotate's copytruncate does. What was written
		// between the last read and the truncation is gone, and what is there
		// now is new.
		r.complete = false
		_, _, next, err = r.reader.Read(ctx, read.Identity, 0, deltaCap, r.feed)
		if err != nil {
			return err
		}
		r.cursor = fileCursor{read.Identity, next}
	case read.Exists:
		r.cursor = fileCursor{read.Identity, next}
	}

	r.exists = live.Exists || read.Exists
	if !live.Exists {
		if !read.Exists {
			r.vanish()
		}
		return nil
	}
	if live.Identity == r.cursor.identity {
		return nil
	}
	return r.rollover(ctx, live)
}

// rollover follows the record onto a new live file. The generation the cursor
// was on has already been read to its end by advance, by identity rather than
// by path, which is what closes the gap a roll would otherwise leave; what
// remains is any generation rolled in between, and the new live file from its
// start.
func (r *record) rollover(ctx context.Context, live FileStat) error {
	rolled, err := r.reader.Rolled(ctx)
	if err != nil {
		return err
	}
	var newest time.Time
	if n := len(r.ceil); n > 0 {
		newest = time.Unix(0, r.ceil[n-1]).UTC()
	}
	for _, gen := range rolled {
		// A generation last written before the newest request held cannot
		// hold anything new; the one the cursor was on was read already.
		if gen.Identity == r.cursor.identity || !gen.Modified.After(newest) {
			continue
		}
		if _, _, _, err := r.reader.Read(ctx, gen.Identity, 0, deltaCap, r.feed); err != nil {
			return err
		}
	}
	read, _, next, err := r.reader.Read(ctx, "", 0, deltaCap, r.feed)
	if err != nil {
		return err
	}
	if read.Exists {
		r.cursor = fileCursor{read.Identity, next}
	} else {
		r.cursor = fileCursor{}
	}
	_ = live
	return nil
}

// vanish empties a record whose files are all gone: the route was removed.
// Sequences keep counting, so a tail that was following it sees nothing
// rather than a replay.
func (r *record) vanish() {
	r.entries, r.ceil = nil, nil
	r.seqBase = r.nextSeq
	r.cursor = fileCursor{}
	r.seeded, r.complete, r.exists = false, false, false
	r.held.Store(0)
}

// feed parses one line into the record.
func (r *record) feed(line string) {
	e, ok := Parse(line)
	if !ok {
		return
	}
	e.Method, e.Path, e.Host = r.share(e.Method), r.share(e.Path), r.share(e.Host)
	e.Proto, e.RemoteIP = r.share(e.Proto), r.share(e.RemoteIP)
	e.UserAgent, e.Referer = r.share(e.UserAgent), r.share(e.Referer)
	e.Seq = r.nextSeq
	r.nextSeq++
	top := e.at
	if n := len(r.ceil); n > 0 && r.ceil[n-1] > top {
		top = r.ceil[n-1]
	}
	r.entries = append(r.entries, e)
	r.ceil = append(r.ceil, top)
	r.held.Store(int64(len(r.entries)))
}

// share returns the one copy of a string the record already holds, so a
// hundred thousand requests from one browser carry one user agent between
// them rather than a hundred thousand.
func (r *record) share(s string) string {
	if s == "" {
		return ""
	}
	if have, ok := r.intern[s]; ok {
		return have
	}
	if len(r.intern) < internCap {
		r.intern[s] = s
	}
	return s
}

// trim drops what is too old to be asked for and, past the cap, the oldest
// tenth — in one go, so a busy route is not copying its slice per request.
func (r *record) trim(now time.Time) {
	drop := 0
	if cutoff := now.Add(-keepFor).UnixNano(); len(r.ceil) > 0 && r.ceil[0] < cutoff {
		drop = sort.Search(len(r.ceil), func(i int) bool { return r.ceil[i] >= cutoff })
	}
	if len(r.entries)-drop > routeCap {
		drop = len(r.entries) - routeCap + routeCap/10
		r.complete = false
	}
	if drop == 0 {
		return
	}
	if drop >= len(r.entries) {
		r.entries, r.ceil = nil, nil
	} else {
		// Copied rather than resliced: a reslice keeps the dropped prefix
		// alive underneath, and the point of dropping it is the memory.
		r.entries = append([]Entry(nil), r.entries[drop:]...)
		r.ceil = append([]int64(nil), r.ceil[drop:]...)
	}
	r.seqBase += uint64(drop)
	r.held.Store(int64(len(r.entries)))
}

// ConsumeLines hands every whole line in data to fn and reports how many
// bytes that took — through the last newline, so a line still being written
// is left for the next read to finish. A single line that fills the whole
// read on its own is skipped: nothing a proxy writes is that long, and a
// reader that waited for its newline would wait forever.
func ConsumeLines(data []byte, full bool, fn func(string)) int64 {
	start := 0
	for i, c := range data {
		if c != '\n' {
			continue
		}
		line := data[start:i]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if fn != nil {
			fn(string(line))
		}
		start = i + 1
	}
	if start == 0 && full && len(data) > 0 {
		return int64(len(data))
	}
	return int64(start)
}
