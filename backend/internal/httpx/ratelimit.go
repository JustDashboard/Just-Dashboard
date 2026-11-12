package httpx

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
)

// Limiter keeps a token bucket per key (client IP, or principal for
// authenticated routes) and evicts buckets that fall idle.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    rate.Limit
	burst   int
	ttl     time.Duration
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
	safego.Go(context.Background(), nil, "rate limit sweep", func(context.Context) { l.sweep() })
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
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(l.rate, l.burst)}
		l.buckets[key] = b
	}
	b.seen = time.Now()
	return b.lim.Allow()
}

// Middleware throttles by client IP. Used on the login surface, where the
// caller is by definition not yet authenticated.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(ClientIP(r)) {
			l.reject(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ByPrincipal throttles authenticated callers individually, so one runaway
// script cannot exhaust the budget for every other operator.
func (l *Limiter) ByPrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := MustPrincipal(r)
		key := p.Username() + "|" + ClientIP(r)
		if !l.Allow(key) {
			l.reject(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *Limiter) reject(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(30))
	JSON(w, http.StatusTooManyRequests, map[string]any{
		"error": map[string]string{"code": "rate_limited", "message": "too many requests, slow down"},
	})
}
