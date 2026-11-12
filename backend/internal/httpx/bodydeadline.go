package httpx

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// The time a client is given to send a request body.
//
// BodyGrace is how long the server will wait on the client in all before the
// body's first bytes, and BodyMinRate is the rate, in bytes a second, at which
// what has arrived buys more waiting. A 4 MB JSON body may keep the server
// waiting for a little over a quarter of an hour in the worst case and a
// multi-gigabyte upload for as long as it takes at any speed a real link
// has, while a client that sends a byte a second is cut off after a minute.
const (
	BodyGrace   = 60 * time.Second
	BodyMinRate = 4 << 10
)

// errBodyTimeout is what a body's reader is told when the client used up its
// waiting. It is an APIError, so a handler that returns it as it came answers
// 408 without knowing about any of this.
var errBodyTimeout = &APIError{
	Status: http.StatusRequestTimeout, Code: "body_timeout",
	Message: "the request body did not arrive in time", err: os.ErrDeadlineExceeded,
}

// BodyDeadline bounds how long a request may keep the server waiting for its
// body.
//
// The server sets a header timeout and nothing after it, because a write
// timeout or a whole-request read timeout would sever the log tails, uploads
// and terminals that are long-lived by nature. That left a body unbounded: a
// client could send its headers, then a byte now and then, and hold a handler
// goroutine and its buffer for as long as it liked.
//
// What is bounded here is the waiting, not the request. Only time spent
// blocked on the client counts, so a handler that thinks for five minutes
// between two reads has spent nothing, and what has arrived buys more
// (minRate), so an upload is never cut for being large.
//
// The connection's read deadline is what interrupts a read, and it stands only
// while one of this body's reads is in progress. It is never left on the
// connection, for two reasons. Once a body has been read to its end the
// server keeps reading the connection to notice the client leaving, and a
// deadline set after that would read as exactly that and cancel a handler
// that is working. And before the end, net/http reads what is left of a small
// body itself before it answers: a deadline that had passed while the handler
// worked would fail that read for bytes that had long arrived, and the
// connection would be closed over them, which can cost the client the answer.
//
// A request with no body, and one that upgrades the connection, is left alone.
func BodyDeadline(grace time.Duration, minRate int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 ||
				strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
				next.ServeHTTP(w, r)
				return
			}
			r.Body = &timedBody{
				ReadCloser: r.Body, deadlines: http.NewResponseController(w),
				grace: grace, minRate: max(minRate, 1), declared: r.ContentLength,
			}
			next.ServeHTTP(w, r)
		})
	}
}

// timedBody is a request body that charges the client for the time the server
// spends waiting on it.
type timedBody struct {
	io.ReadCloser
	deadlines *http.ResponseController
	grace     time.Duration
	minRate   int64
	// declared is the request's Content-Length, -1 when it has none.
	declared int64

	waited time.Duration
	read   int64
	ended  bool
	failed error
	// unsupported is a connection that takes no read deadline: a recorder in
	// a test, or a request the dashboard made of itself. Nothing is waited on
	// there, so nothing is bounded.
	unsupported bool
}

// remaining is the waiting the client has left: the grace, plus a second for
// every minRate bytes that have arrived, less what it has used.
func (b *timedBody) remaining() time.Duration {
	earned := time.Duration(float64(b.read) / float64(b.minRate) * float64(time.Second))
	return b.grace + earned - b.waited
}

func (b *timedBody) setDeadline(at time.Time) {
	if b.unsupported {
		return
	}
	if err := b.deadlines.SetReadDeadline(at); err != nil {
		b.unsupported = true
	}
}

func (b *timedBody) Read(p []byte) (int, error) {
	if b.failed != nil {
		return 0, b.failed
	}
	if b.ended {
		// Nothing is waited for after the end, and no deadline is set there.
		return b.ReadCloser.Read(p)
	}
	remaining := b.remaining()
	if remaining <= 0 {
		return 0, b.timeOut()
	}
	start := time.Now()
	b.setDeadline(start.Add(remaining))
	n, err := b.ReadCloser.Read(p)
	b.waited += time.Since(start)
	b.read += int64(n)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return n, b.timeOut()
	}
	b.setDeadline(time.Time{})
	if err != nil {
		b.ended = true
	}
	return n, err
}

// timeOut ends the body. This is the one case the deadline is left standing,
// in the past: net/http reads what remains of a small body before it writes
// the answer, and that read has to fail at once rather than wait on the same
// client again. Nothing is cancelled by it, because the body never reached
// its end and the server is not yet watching the connection, and nothing that
// had arrived is lost, because the client is the one that stopped sending.
func (b *timedBody) timeOut() error {
	b.failed, b.ended = errBodyTimeout, true
	b.setDeadline(time.Now())
	return b.failed
}

// postHandlerRead is how much of an unread body net/http reads before it
// writes a response, so that the connection can be used again.
const postHandlerRead = 256 << 10

// drainBody reads what is left of a small request body, within the client's
// allowance, before a refusal is written.
//
// net/http does this read itself when a handler answers without having read
// the body, and waits on the client for as long as it takes: a request that
// declares a body, never sends it and is refused (no credential is enough)
// held its goroutine there. Reading it here first, through the body that
// keeps time, leaves net/http nothing to wait for. A body net/http would not
// read (more than its limit still to come) is not read here either.
func drainBody(r *http.Request) {
	body, ok := r.Body.(*timedBody)
	if !ok || body.ended || body.unsupported {
		return
	}
	if body.declared >= 0 && body.declared-body.read >= postHandlerRead {
		return
	}
	_, _ = io.CopyN(io.Discard, body, postHandlerRead+1)
}
