package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// The dashboard's rate limit refuses before anything ran and says how long to
// wait: a job that runs jd several times in a row is slowed, not failed.
func TestARateLimitedCommandWaitsAndAsksAgain(t *testing.T) {
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"too many requests, slow down"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	c := &client{base: srv.URL, key: "k", http: srv.Client()}

	started := time.Now()
	raw, err := c.do(http.MethodPost, "/deploy/3/redeploy", nil, map[string]int{"a": 1})
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("answer %q, error %v", raw, err)
	}
	if calls.Load() != 2 || time.Since(started) < time.Second {
		t.Errorf("%d requests in %v; want two, a second apart", calls.Load(), time.Since(started))
	}
	if len(bodies) != 2 || bodies[0] != `{"a":1}` || bodies[1] != bodies[0] {
		t.Errorf("the second request does not carry the body again: %q", bodies)
	}
}

func TestOnlyAShortRateLimitIsWaitedOut(t *testing.T) {
	for name, tc := range map[string]struct {
		status     int
		retryAfter string
	}{
		"a wait too long to sit through":     {http.StatusTooManyRequests, "120"},
		"a 429 that names no wait":           {http.StatusTooManyRequests, ""},
		"another refusal with a Retry-After": {http.StatusServiceUnavailable, "1"},
	} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			if tc.retryAfter != "" {
				w.Header().Set("Retry-After", tc.retryAfter)
			}
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, `{"error":{"code":"second_factor_locked","message":"locked"}}`)
		}))
		_, err := (&client{base: srv.URL, key: "k", http: srv.Client()}).do(http.MethodGet, "/deploy", nil, nil)
		srv.Close()
		var refused *apiError
		if !errors.As(err, &refused) || refused.Status != tc.status || calls.Load() != 1 {
			t.Errorf("%s: %d requests, error %v; want one request and its refusal", name, calls.Load(), err)
		}
	}
}
