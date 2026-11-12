package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The feature routes' rate limit is keyed by who is asking, so it runs after
// authentication and a wrong key never met it: 400 of them got 400 answers of
// 401 in 0.4 s. Failed credentials are now counted per address. The count
// refuses nothing that is valid, because an address is shared wherever an SSH
// tunnel ends.
func TestWrongKeysAreToldToSlowDownAndAValidCredentialStillPasses(t *testing.T) {
	s := testServer(t)
	s.Authn.Failures = httpx.NewFailureBudget(60, 3)
	browser, user := signedIn(t, s, "ada")
	_, key, err := s.Auth.CreateAPIToken(t.Context(), user, "ci", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	const path = "/api/v1/audit/?limit=1"
	for i := range 3 {
		if w := withKey(browser.h, "vpsd_not-a-key", path); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong key %d: %d %s", i+1, w.Code, w.Body)
		}
	}
	w := withKey(browser.h, "vpsd_not-a-key", path)
	if w.Code != http.StatusTooManyRequests || codeOf(t, w) != "rate_limited" || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("a wrong key past the address's budget: %d, Retry-After %q, %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	// The MCP endpoint reads a key by itself and counts the same way.
	mcp := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", nil)
	mcp.RemoteAddr = "127.0.0.1:5555"
	mcp.Header.Set("Authorization", "Bearer vpsd_not-a-key")
	rec := httptest.NewRecorder()
	browser.h.ServeHTTP(rec, mcp)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("a wrong key at the MCP endpoint, same address: %d %s", rec.Code, rec.Body)
	}

	// Same address, valid credentials: neither is held to the failures.
	if w := withKey(browser.h, key, path); w.Code != http.StatusOK {
		t.Errorf("a valid key from the address that kept failing: %d %s", w.Code, w.Body)
	}
	if w := browser.do(http.MethodGet, path, "", nil); w.Code != http.StatusOK {
		t.Errorf("the signed-in browser from the address that kept failing: %d %s", w.Code, w.Body)
	}
	// A session that ended still reads 401, which is how the page knows to
	// offer the sign-in again.
	expired := &client{t: t, h: browser.h, cookie: httpx.SessionCookie + "=gone"}
	if w := expired.do(http.MethodGet, path, "", nil); w.Code != http.StatusUnauthorized || codeOf(t, w) != "unauthorized" {
		t.Errorf("an ended session past the budget: %d %s", w.Code, w.Body)
	}
}
