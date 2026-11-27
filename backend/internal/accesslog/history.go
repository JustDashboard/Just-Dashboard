package accesslog

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// History past what memory holds.
//
// The held record is the ingress's files read back, and the files are what
// the ingress keeps: five generations of 16 MiB on the Caddy ingress, read
// with a 48 MB seed and a 150,000-request cap. A site serving a few thousand
// requests an hour has well under a day of them, so "the last 7 days" used
// to be the last day presented as a week. The request history (package
// requesthistory) records every routed deployment's requests as they arrive,
// whether or not anybody is looking, into a database of its own; the store
// answers a window from it and from memory together.
//
// The two are cut at one instant. Everything served before it is answered
// from the archive, everything at or after it from memory, and both go
// through the one Collector, so every figure is computed over the window that
// was asked for and no request is counted from both. The instant is a little
// way into what memory holds, so the requests the ingress wrote a moment out
// of time order around memory's first one are on the archive's side, which
// holds them.

// Archive is the request history as the store reads it. It is an interface so
// this package does not import the database that keeps it, and the tests can
// stand in for one.
type Archive interface {
	// Span says what is kept for route.
	Span(ctx context.Context, route string) (ArchiveSpan, error)
	// Scan hands fn, oldest first, every kept request of route served at or
	// after from and before until, both unix nanoseconds; from at or below
	// zero has no lower bound and until math.MaxInt64 no upper one. An error
	// fn returns stops the scan and is returned.
	Scan(ctx context.Context, route string, from, until int64, fn func(Entry) error) error
	// Retention is how long a request is kept.
	Retention() time.Duration
}

// ArchiveSpan is what the archive keeps of one route.
type ArchiveSpan struct {
	// From and To are the oldest and newest kept request, zero when nothing
	// is kept.
	From, To time.Time
	// LostAt is the newest instant before which something the route served is
	// known to be missing: requests the retention or a cap deleted, or a part
	// of the record that was gone before it was read. Zero when nothing is.
	LostAt time.Time
	// Recording reports that the recorder follows the route now.
	Recording bool
}

// History is what an answer says about the kept record, so a window that
// begins before the first kept request can say where the history begins
// rather than read as a quiet week.
type History struct {
	// From is the oldest kept request; absent until one is kept.
	From *time.Time `json:"from,omitempty"`
	// Days is how long a request is kept.
	Days int `json:"days"`
	// Recording reports whether the recorder follows this route.
	Recording bool `json:"recording"`
}

// WithArchive answers windows from the archive as well as memory.
func (s *Store) WithArchive(a Archive) *Store {
	s.archive = a
	return s
}

// cutMargin is how far past memory's first request the cut is made. The
// ingress writes a request when it is answered, so two that overlapped can
// land in the file a few milliseconds out of time order; a second is far more
// than that and far less than either side holds.
const cutMargin = int64(time.Second)

// heldSpan is what memory holds, as the plan needs it.
type heldSpan struct {
	has      bool
	from     int64
	complete bool
	exists   bool
}

// answerPlan is how one window is answered: from the archive up to cut and
// from memory after it, or from memory alone.
type answerPlan struct {
	archive bool
	// cut is the instant, unix nanoseconds, at which memory takes over.
	// Memory answers the requests at or after it; zero when it answers all.
	cut      int64
	complete bool
	exists   bool
	from     *time.Time
	history  *History
}

// plan decides how a window is answered. With no archive, or one that keeps
// nothing for the route, it is memory alone exactly as it was before there
// was an archive.
func (s *Store) plan(ctx context.Context, route string, filter Filter, held heldSpan) answerPlan {
	p := answerPlan{complete: held.complete, exists: held.exists}
	if s.archive == nil {
		return p
	}
	span, err := s.archive.Span(ctx, route)
	if err != nil {
		// The history unreadable costs the history and not the answer.
		return p
	}
	p.history = &History{Days: int(s.archive.Retention() / (24 * time.Hour)), Recording: span.Recording}
	if span.From.IsZero() {
		return p
	}
	from := span.From
	p.history.From = &from
	p.exists = true

	// Where the two meet. The archive lags the files by a poll, memory is
	// read at this call: when the archive reaches comfortably past memory's
	// first request, the two overlap and the cut goes just inside memory.
	// When it does not, between the two is a stretch neither holds.
	overlap := true
	switch {
	case !held.has:
		p.cut = math.MaxInt64
	case span.To.UnixNano() >= held.from+2*cutMargin:
		p.cut = held.from + cutMargin
	default:
		p.cut, overlap = held.from, false
	}
	since := int64(math.MinInt64)
	if !filter.Since.IsZero() {
		since = filter.Since.UnixNano()
	}
	p.archive = since < p.cut && span.From.UnixNano() < p.cut
	if !p.archive && held.has {
		// Memory answers alone; the cut only stops it reading the part of a
		// window the archive would have answered.
		p.cut = 0
	}

	earliest := span.From
	if held.has && held.from < earliest.UnixNano() {
		earliest = time.Unix(0, held.from).UTC()
	}
	p.from = &earliest

	// Complete is "nothing the window covers is known to be missing": the
	// window starts inside what is held without a gap, or the archive and
	// memory meet and the archive reaches the start of what was ever read,
	// and no part of the record after the window's start was lost. A window
	// reaching back before the first kept request is complete in the way the
	// held record always was — before it nothing was recorded, which
	// History.From says — rather than a floor.
	inMemory := held.has && since >= held.from
	lost := !span.LostAt.IsZero() && since <= span.LostAt.UnixNano()
	p.complete = (inMemory || overlap) && !lost
	return p
}

// errEnough stops a scan that has handed over as many requests as were asked.
var errEnough = errors.New("enough")

// scanArchive feeds the archive's part of a window to fn. until bounds it as
// well as the cut, so a window in the past does not read the history after
// it.
func (s *Store) scanArchive(ctx context.Context, route string, filter Filter, p answerPlan, fn func(Entry) error) error {
	until := p.cut
	if !filter.Until.IsZero() {
		if end := filter.Until.UnixNano(); end < math.MaxInt64 && end+1 < until {
			until = end + 1
		}
	}
	from := int64(0)
	if !filter.Since.IsZero() {
		from = filter.Since.UnixNano()
	}
	return s.archive.Scan(ctx, route, from, until, fn)
}

// longWindow is the span from which an answer that read the archive is kept
// for a while: a week is half a million requests on a busy site, and the page
// asks again every ten seconds.
const longWindow = 6 * time.Hour

// windowCacheFor is how long such an answer stands, and windowCacheCap how
// many are kept at once.
const (
	windowCacheFor = 30 * time.Second
	windowCacheCap = 64
)

// windowCache keeps recent answers to long windows, keyed by the route and
// the question. The window's ends are rounded to windowCacheFor in the key:
// the page's "last 7 days" moves its start with every poll, and three polls
// asking a week that starts ten seconds apart are one question.
type windowCache struct {
	mu      sync.Mutex
	answers map[string]cachedWindow
}

type cachedWindow struct {
	window Window
	at     time.Time
}

func (c *windowCache) get(key string, now time.Time) (Window, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	hit, ok := c.answers[key]
	if !ok || now.Sub(hit.at) >= windowCacheFor || now.Before(hit.at) {
		return Window{}, false
	}
	return hit.window, true
}

func (c *windowCache) put(key string, window Window, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.answers == nil {
		c.answers = map[string]cachedWindow{}
	}
	for k, hit := range c.answers {
		if now.Sub(hit.at) >= windowCacheFor {
			delete(c.answers, k)
		}
	}
	for len(c.answers) >= windowCacheCap {
		oldest := ""
		for k, hit := range c.answers {
			if oldest == "" || hit.at.Before(c.answers[oldest].at) {
				oldest = k
			}
		}
		delete(c.answers, oldest)
	}
	c.answers[key] = cachedWindow{window: window, at: now}
}

// forget drops a route's answers.
func (c *windowCache) forget(route string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.answers {
		if strings.HasPrefix(k, route+"\x00") {
			delete(c.answers, k)
		}
	}
}

// cacheKey is the key a long window is cached under, or "" for a window too
// short to be worth it.
func cacheKey(route string, f Filter, now time.Time) string {
	if f.Since.IsZero() {
		return ""
	}
	end := now
	if !f.Until.IsZero() {
		end = f.Until
	}
	if end.Sub(f.Since) < longWindow {
		return ""
	}
	round := func(t time.Time) int64 {
		if t.IsZero() {
			return 0
		}
		return t.UnixNano() / int64(windowCacheFor)
	}
	sorted := func(values []string) []string {
		out := append([]string(nil), values...)
		for i := range out {
			out[i] = strings.ToLower(out[i])
		}
		sort.Strings(out)
		return out
	}
	status := append([]int(nil), f.Status...)
	sort.Ints(status)
	return fmt.Sprintf("%s\x00%d|%d|%q|%q|%v|%q|%q|%q|%q|%q|%q|%g|%g|%t|%q|%d",
		route, round(f.Since), round(f.Until), sorted(f.Methods), sorted(f.Classes), status,
		f.Path, f.Host, f.Client, f.Agent, f.Referer, f.Release, f.MinMs, f.MaxMs, f.PagesOnly, f.Who, f.Limit)
}
