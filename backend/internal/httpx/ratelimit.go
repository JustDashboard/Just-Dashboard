package httpx

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter keeps a token bucket per key (client IP, or principal for
// authenticated routes) and evicts buckets that fall idle.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    rate.Limit
	burst   int
	ttl     time.Duration
	// perCredential is ByPrincipal keyed by the credential rather than the
	// person (PerCredential).
	perCredential bool
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

func NewLimiter(perMinute, burst int) *Limiter {
	l := &Limiter{
		buckets: map[string]*bucket{},
		rate:    rate.Limit(float64(perMinute) / 60.0),
		burst:   burst,
		ttl:     10 * time.Minute,
	}
	go l.sweep()
	return l
}

// PerCredential makes ByPrincipal give each credential a budget of its own: a
// browser session, each API key, and the assistant working in a session.
//
// The general API budget was one bucket per account and address, so an open
// dashboard's polls, every key the person owned and the assistant's calls all
// drew on it wherever they shared an address (an SSH tunnel puts everything on
// loopback), and a CI job could empty the bucket the page was reading from.
// It is for that budget only. The destructive budget stays the person's own
// (the default), whichever credential spends it: nobody gains a second
// allowance of irreversible actions by minting a key or asking the assistant.
func (l *Limiter) PerCredential() *Limiter {
	l.perCredential = true
	return l
}

func (l *Limiter) sweep() {
	t := time.NewTicker(time.Minute)
	for range t.C {
		cutoff := time.Now().Add(-l.ttl)
		l.mu.Lock()
		for k, b := range l.buckets {
			if b.seen.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

func (l *Limiter) Allow(key string) bool {
	ok, _, _ := l.take(key)
	return ok
}

// take spends one request from key's bucket. When the bucket is empty nothing
// is spent, and wait is how long until it holds one again. left is the whole
// requests the bucket still holds afterwards.
func (l *Limiter) take(key string) (ok bool, wait time.Duration, left int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, found := l.buckets[key]
	if !found {
		b = &bucket{lim: rate.NewLimiter(l.rate, l.burst)}
		l.buckets[key] = b
	}
	now := time.Now()
	b.seen = now
	reservation := b.lim.ReserveN(now, 1)
	if !reservation.OK() {
		// A bucket that can never hold one: nothing to wait for.
		return false, 0, 0
	}
	if wait = reservation.DelayFrom(now); wait > 0 {
		reservation.CancelAt(now)
		return false, wait, 0
	}
	return true, 0, max(int(b.lim.TokensAt(now)), 0)
}

// Spend is Allow for a handler that applies a budget by hand: nil, or the 429
// to return, which carries how long to wait so that WriteError can say it in
// Retry-After like the middleware does.
func (l *Limiter) Spend(key, message string) error {
	ok, wait, _ := l.take(key)
	if ok {
		return nil
	}
	return rateLimited(message, wait)
}

// rateLimited is the refusal of an empty bucket. It is retryable by
// definition, and it was refused before anything ran, so trying again is
// always safe.
func rateLimited(message string, wait time.Duration) *APIError {
	return &APIError{
		Status: http.StatusTooManyRequests, Code: "rate_limited", Message: message,
		Retryable: true, retryAfter: wait,
	}
}

// retryAfterSeconds is a wait as Retry-After says it: whole seconds, rounded
// up, and never zero, which a client would read as "now" and be refused again.
func retryAfterSeconds(wait time.Duration) string {
	return strconv.Itoa(max(int(math.Ceil(wait.Seconds())), 1))
}

// Middleware throttles by client IP. Used on the login surface, where the
// caller is by definition not yet authenticated.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.admit(w, r, ClientIP(r)) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ByPrincipal throttles authenticated callers individually, so one runaway
// script cannot exhaust the budget for every other operator. The bucket is
// the person's at their address, or, for a limiter made PerCredential, the
// credential's.
func (l *Limiter) ByPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := MustPrincipal(r)
		key := p.Username() + "|" + ClientIP(r)
		if l.perCredential {
			key = credentialKey(p, key)
		}
		if !l.admit(w, r, key) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// credentialKey names the bucket of the credential behind a request: the
// assistant working in a session, an API key, or a browser session. A
// principal with none of them (the hub's, on an agent) keeps the person's key.
//
// An MCP key's generated tool is a second request under the same key and
// spends from the same bucket as the call that asked for it.
func credentialKey(p *Principal, person string) string {
	switch {
	case p.Assistant && p.SessionID != "":
		return "assistant|" + p.SessionID
	case p.Token != nil:
		return "key|" + strconv.FormatInt(p.Token.ID, 10)
	case p.SessionID != "":
		return "session|" + p.SessionID
	}
	return person
}

// admit spends one request from key's bucket and says what is left of it, or
// writes the refusal and reports false.
func (l *Limiter) admit(w http.ResponseWriter, r *http.Request, key string) bool {
	ok, wait, left := l.take(key)
	if !ok {
		WriteError(w, r, rateLimited("too many requests, slow down", wait))
		return false
	}
	// What the caller may still send before it is asked to wait, so a script
	// can pace itself instead of finding the limit by hitting it.
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(left))
	return true
}
