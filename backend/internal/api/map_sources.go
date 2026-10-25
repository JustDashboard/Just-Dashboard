package api

import (
	"context"
	"sync"
	"time"
)

// Every source the server map reads, held between requests.
//
// The map is one request for the whole server, and the whole server is
// thirty sources: a socket call to Docker, nginx's files, systemctl, a walk
// of the Git roots, the package manager. Read fresh on every poll that would
// be the most expensive page in the product, and a slow source — a cold
// Docker disk-usage walk, a Git root on a network mount — would hold the
// whole map back. So each source keeps its last answer for as long as that
// kind of thing stays true (seconds for containers, minutes for repositories)
// and is read again behind the map's back: a request waits a short budget for
// a fresh answer and otherwise draws the last one, saying when it was read.
// Only one read of a source is ever in flight, however many maps are open.
//
// A read that fails keeps the last good answer beside the error, so the shelf
// can say "not read" over what was there rather than drawing it empty, which
// is the one thing an unavailable reading must never look like.
type mapSources struct {
	mu      sync.Mutex
	entries map[string]*mapEntry
}

type mapEntry struct {
	value any
	have  bool
	// at is when value was read; err and failedAt are the last attempt's, kept
	// until a read succeeds.
	at       time.Time
	err      error
	failedAt time.Time
	inflight chan struct{}
}

// mapRead is one source's answer as the map draws it.
type mapRead[T any] struct {
	Value T
	// Have is false until the source has answered once.
	Have bool
	At   time.Time
	// Err is the last attempt's failure, or a read still in flight with
	// nothing to show yet.
	Err error
}

// mapSourceSpec is how long a source's answer stays true, how long a request
// waits for a fresh one, and how long a read may take at most.
type mapSourceSpec struct {
	key    string
	ttl    time.Duration
	budget time.Duration
	limit  time.Duration
}

// mapRetryAfter caps how long a failed source waits before it is asked again,
// so a repository walk that timed out once is not left unread for its whole
// five minutes.
const mapRetryAfter = 30 * time.Second

var errMapReading = mapReadingError{}

type mapReadingError struct{}

func (mapReadingError) Error() string { return "still being read" }

// read answers from the held value while it is fresh, and otherwise starts
// (or joins) one read in the background and waits up to the budget for it.
// The read runs on its own context so a request that gives up waiting does
// not cancel the read every later request would have used.
func readMapSource[T any](c *mapSources, spec mapSourceSpec, fn func(context.Context) (T, error)) mapRead[T] {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*mapEntry{}
	}
	e := c.entries[spec.key]
	if e == nil {
		e = &mapEntry{}
		c.entries[spec.key] = e
	}
	now := time.Now()
	fresh := e.have && e.err == nil && now.Sub(e.at) < spec.ttl
	// A failure is not asked again on every poll either: a source that just
	// refused is given the same breathing room a good answer would have had.
	recentlyFailed := e.err != nil && now.Sub(e.failedAt) < min(spec.ttl, mapRetryAfter)
	if !fresh && !recentlyFailed && e.inflight == nil {
		done := make(chan struct{})
		e.inflight = done
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), spec.limit)
			defer cancel()
			value, err := fn(ctx)
			c.mu.Lock()
			if err != nil {
				e.err = err
				e.failedAt = time.Now()
			} else {
				e.value, e.have, e.at, e.err = value, true, time.Now(), nil
			}
			e.inflight = nil
			c.mu.Unlock()
			close(done)
		}()
	}
	wait := e.inflight
	c.mu.Unlock()

	if wait != nil && !fresh {
		select {
		case <-wait:
		case <-time.After(spec.budget):
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	out := mapRead[T]{Have: e.have, At: e.at, Err: e.err}
	if e.have {
		out.Value, _ = e.value.(T)
	}
	if !e.have && out.Err == nil {
		out.Err = errMapReading
	}
	return out
}
