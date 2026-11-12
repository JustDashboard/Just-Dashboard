package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// withKey sends a read with an API key from the address the test's browser
// uses, which is what an SSH tunnel makes of a CI job and the operator's page.
func withKey(h http.Handler, key, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// A script that empties its key's budget does not take the open page's with
// it, and is told how long to wait in seconds that are true.
func TestAKeyOutOfBudgetLeavesThePersonsBrowserItsOwn(t *testing.T) {
	s := testServer(t)
	s.apiLim = httpx.NewLimiter(60, 3).PerCredential()
	browser, user := signedIn(t, s, "ada")
	_, key, err := s.Auth.CreateAPIToken(t.Context(), user, "ci", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	const path = "/api/v1/audit/?limit=1"
	for i := range 3 {
		if w := withKey(browser.h, key, path); w.Code != http.StatusOK {
			t.Fatalf("the key's request %d: %d %s", i+1, w.Code, w.Body)
		}
	}
	w := withKey(browser.h, key, path)
	if w.Code != http.StatusTooManyRequests || codeOf(t, w) != "rate_limited" {
		t.Fatalf("the key past its budget: %d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After %q for a bucket that refills every second, want 1", got)
	}

	page := browser.do(http.MethodGet, path, "", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("the browser, same person and address, after the key ran out: %d %s", page.Code, page.Body)
	}
	if got := page.Header().Get("RateLimit-Remaining"); got != "2" {
		t.Errorf("RateLimit-Remaining %q on the browser's first request, want 2", got)
	}
}
