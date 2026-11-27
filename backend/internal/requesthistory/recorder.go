package requesthistory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
)

// The recorder.
//
// It reads each route's files the way the store's record does (accesslog's
// advance and rollover), through the same Reader, but a step at a time and
// with its place written down: one read per route per pass, of at most
// routeBudget bytes, its requests and the cursor past them committed in one
// transaction. A route it has never recorded is read from the oldest
// generation still on disk to the live file; after that each pass reads what
// was appended. A roll is followed by identity — the rest of the generation
// it was reading, then each one rolled after it, then the new live file — and
// a truncated or vanished file is started again, with what may have been lost
// recorded as lost and what may be read twice dropped by its instant.

const (
	// tickEvery is how often the recorder looks at its routes. Every read of
	// the Caddy ingress's record is a docker exec, so a route with nothing new
	// is looked at less often, up to every idleMax.
	tickEvery = 20 * time.Second
	idleMax   = 2 * time.Minute
	// startDelay lets the dashboard finish starting before the first pass.
	startDelay = 10 * time.Second
	// listEvery is how often the routes are listed again, and reopenEvery
	// how often a route's reader is resolved again: an ingress replaced by
	// another keeps writing elsewhere without any read failing.
	listEvery   = time.Minute
	reopenEvery = 10 * time.Minute
	// pruneEvery is how often the retention and the caps are applied, the
	// first time pruneFirst after start.
	pruneEvery = time.Hour
	pruneFirst = 5 * time.Minute
	// routeBudget bounds one read, and tickBudget a pass's reads together, so
	// a backfill of five 16 MiB generations is spread over a few minutes of
	// passes rather than one exec that holds the ingress's disk.
	routeBudget = 8 << 20
	tickBudget  = 32 << 20
)

// RouteLister names the routes to record: every routed deployment
// environment's.
type RouteLister func(ctx context.Context) ([]string, error)

// Recorder follows routes' request records into the archive.
type Recorder struct {
	archive *Archive
	open    accesslog.Opener
	routes  RouteLister
	log     *slog.Logger
	now     func() time.Time

	followed map[string]*follower
	listed   time.Time
	pruned   time.Time
	turn     int
	// perRoute and perPass are routeBudget and tickBudget; the tests lower
	// them to see a backfill spread over passes.
	perRoute, perPass int64
}

// follower is one route as the recorder follows it.
type follower struct {
	route  string
	reader accesslog.Reader
	opened time.Time
	cursor Cursor
	loaded bool
	idle   int
	due    time.Time
}

// NewRecorder records the routes routes names, opening each with open.
func NewRecorder(archive *Archive, open accesslog.Opener, routes RouteLister, log *slog.Logger) *Recorder {
	r := &Recorder{
		archive: archive, open: open, routes: routes, log: log, now: time.Now,
		followed: map[string]*follower{}, perRoute: routeBudget, perPass: tickBudget,
	}
	r.pruned = r.now().Add(pruneFirst - pruneEvery)
	return r
}

// Run records until ctx ends.
func (r *Recorder) Run(ctx context.Context) {
	passes := safego.NewPasses(r.log, "request history")
	timer := time.NewTimer(startDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		passes.Report(ctx, r.Tick(ctx))
		timer.Reset(tickEvery)
	}
}

// Tick is one pass: the routes listed again when it is time, every route that
// is due read once, and the prune when it is time. It returns the first
// failure, having carried on past it.
func (r *Recorder) Tick(ctx context.Context) error {
	now := r.now()
	var failed error
	if r.listed.IsZero() || now.Sub(r.listed) >= listEvery {
		if err := r.list(ctx); err != nil {
			failed = fmt.Errorf("list the routes to record: %w", err)
		} else {
			r.listed = now
		}
	}
	names := make([]string, 0, len(r.followed))
	for name := range r.followed {
		names = append(names, name)
	}
	sort.Strings(names)
	// The pass starts one route further along each time, so a budget spent
	// before the end of the list is not always spent on the same routes.
	r.turn++
	budget := r.perPass
	for i := range names {
		if budget <= 0 || ctx.Err() != nil {
			break
		}
		f := r.followed[names[(i+r.turn)%len(names)]]
		if now.Before(f.due) {
			continue
		}
		read, err := r.step(ctx, f, min(budget, r.perRoute))
		budget -= read
		if err != nil && failed == nil && ctx.Err() == nil {
			failed = fmt.Errorf("record %s: %w", f.route, err)
		}
	}
	if now.Sub(r.pruned) >= pruneEvery && ctx.Err() == nil {
		r.pruned = now
		if err := r.archive.Prune(ctx); err != nil && failed == nil {
			failed = fmt.Errorf("prune the request history: %w", err)
		}
	}
	return failed
}

func (r *Recorder) list(ctx context.Context) error {
	names, err := r.routes(ctx)
	if err != nil {
		return err
	}
	keep := make(map[string]bool, len(names))
	for _, name := range names {
		keep[name] = true
		if r.followed[name] == nil {
			r.followed[name] = &follower{route: name}
		}
	}
	for name := range r.followed {
		if !keep[name] {
			delete(r.followed, name)
		}
	}
	r.archive.follow(names)
	return nil
}

// step reads route's next stretch into the archive and returns how many
// bytes it took.
func (r *Recorder) step(ctx context.Context, f *follower, limit int64) (int64, error) {
	now := r.now()
	if f.reader != nil && now.Sub(f.opened) >= reopenEvery {
		f.reader = nil
	}
	if f.reader == nil {
		reader, _, err := r.open(ctx, f.route)
		if err != nil {
			f.rest(now)
			return 0, err
		}
		f.reader, f.opened = reader, now
	}
	if !f.loaded {
		cursor, _, err := r.archive.Cursor(ctx, f.route)
		if err != nil {
			return 0, err
		}
		f.cursor, f.loaded = cursor, true
	}

	cur := f.cursor
	target, fresh := cur.Generation, cur.Generation == ""
	if fresh {
		// Never read, or the files were gone: everything on disk from the
		// oldest generation that can hold a request not kept yet. Anything
		// read that is not newer than what is kept was kept already.
		oldest, err := r.oldestUnread(ctx, f, cur.Newest)
		if err != nil {
			r.broken(f, now, err)
			return 0, err
		}
		target, cur.Offset, cur.Guard = oldest, 0, cur.Newest
	}
	from := cur.Offset
	keepFrom := now.Add(-Retention).UnixNano()
	var batch []accesslog.Entry
	read, live, next, err := f.reader.Read(ctx, target, cur.Offset, limit, func(line string) {
		e, ok := accesslog.Parse(line)
		if !ok || (cur.Guard != 0 && e.UnixNano() <= cur.Guard) || e.UnixNano() < keepFrom {
			return
		}
		batch = append(batch, e)
	})
	if err != nil {
		r.broken(f, now, err)
		return 0, err
	}
	switch {
	case !read.Exists && fresh:
		// Nothing on disk yet: a route that has not served a request.
		f.rest(now)
		return 0, nil
	case !read.Exists:
		// The generation being read went before its end was: rolled and
		// deleted while this process was not reading, or the route removed.
		// What it held past the cursor is lost; the next pass starts from
		// what is on disk.
		cur.Generation, cur.Offset, cur.Guard, cur.LostAt = "", 0, 0, now.UnixNano()
	case read.Size < cur.Offset:
		// Shorter than where the last read ended, under the same identity:
		// truncated in place, as copytruncate does. What was written between
		// the last read and the truncation is gone; what is there now is new,
		// unless the file was put back, which the guard covers.
		cur.Offset, cur.Guard, cur.LostAt = 0, cur.Newest, now.UnixNano()
	default:
		cur.Generation, cur.Offset = read.Identity, next
		for _, e := range batch {
			cur.Newest = max(cur.Newest, e.UnixNano())
		}
		// A rolled generation read to its end is done with: on to the one
		// rolled after it, or the live file.
		finished := read.Size-from <= limit
		if finished && (!live.Exists || read.Identity != live.Identity) {
			nextGen, ok, err := r.after(ctx, f, read.Identity, cur.Newest, live)
			if err != nil {
				// What was read is kept; the move is asked again next pass.
				nextGen, ok = "", false
				r.broken(f, now, err)
			}
			if ok {
				cur.Generation, cur.Offset = nextGen, 0
				// Past the newest kept request, nothing further can repeat.
				if cur.Newest > cur.Guard {
					cur.Guard = 0
				}
			}
		}
	}
	if cur == f.cursor && len(batch) == 0 {
		f.rest(now)
		return max(next-from, 0), nil
	}
	if err := r.archive.Append(ctx, f.route, batch, cur); err != nil {
		// Nothing was kept and the cursor stays: the next pass reads the
		// same bytes again.
		return 0, err
	}
	f.cursor, f.idle, f.due = cur, 0, now
	return max(next-from, 0), nil
}

// oldestUnread is the oldest generation that can hold a request newer than
// newest — every one when newest is zero — or "" for the live file. Rolled
// files' times are whole seconds, so one last written in the second of the
// newest request is read again and its repeats dropped by the guard.
func (r *Recorder) oldestUnread(ctx context.Context, f *follower, newest int64) (string, error) {
	rolled, err := f.reader.Rolled(ctx)
	if err != nil {
		return "", err
	}
	for _, gen := range rolled {
		if newest == 0 || gen.Modified.Add(time.Second).UnixNano() > newest {
			return gen.Identity, nil
		}
	}
	return "", nil
}

// after is the generation to read once identity, a rolled one, has been read
// to its end: the one rolled after it, or the live file.
func (r *Recorder) after(ctx context.Context, f *follower, identity string, newest int64, live accesslog.FileStat) (string, bool, error) {
	rolled, err := f.reader.Rolled(ctx)
	if err != nil {
		return "", false, err
	}
	found := false
	for i, gen := range rolled {
		if gen.Identity == identity {
			found = true
			if i+1 < len(rolled) {
				return rolled[i+1].Identity, true, nil
			}
			break
		}
	}
	if !found {
		// Not among the rolled any more: whichever can hold something newer.
		for _, gen := range rolled {
			if gen.Identity != identity && gen.Modified.Add(time.Second).UnixNano() > newest {
				return gen.Identity, true, nil
			}
		}
	}
	if live.Exists && live.Identity != identity {
		return live.Identity, true, nil
	}
	return "", false, nil
}

// broken drops a reader that failed, so the next pass resolves the ingress
// again: the container it was built on may be gone. A cancelled pass is the
// caller's and says nothing about the reader.
func (r *Recorder) broken(f *follower, now time.Time, err error) {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		f.reader = nil
	}
	f.rest(now)
}

// rest puts a route that had nothing new off for longer each time, up to
// idleMax. A roll cannot happen in that time on a route quiet enough to rest.
func (f *follower) rest(now time.Time) {
	if f.idle < 8 {
		f.idle++
	}
	wait := min(tickEvery<<(f.idle-1), idleMax)
	f.due = now.Add(wait - tickEvery/2)
}
