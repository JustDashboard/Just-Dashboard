package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// refusedFrom presents one credential that did not resolve, from ip.
func refusedFrom(a *Authenticator, ip string, bearer bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/system/host", nil)
	r.RemoteAddr = ip + ":4000"
	w := httptest.NewRecorder()
	a.refused(w, r, Err(http.StatusUnauthorized, "unauthorized", "api token invalid, revoked or expired"), bearer)
	return w
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error APIError `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("not the envelope: %v: %s", err, w.Body)
	}
	return body.Error.Code
}

// A script with a wrong key used to hear 401 for as long as it cared to ask,
// and nothing recorded that it had. Past the address's budget of failures it
// is told to slow down, with the wait, and the log gets one line.
func TestAnAddressThatKeepsFailingIsToldToSlowDown(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	a := &Authenticator{Failures: NewFailureBudget(60, 3)}
	for i := range 3 {
		if w := refusedFrom(a, "203.0.113.9", true); w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d, want the 401 it always was", i+1, w.Code)
		}
	}
	if logged.Len() != 0 {
		t.Errorf("failures within the budget were logged: %s", &logged)
	}
	for i := range 4 {
		w := refusedFrom(a, "203.0.113.9", true)
		if w.Code != http.StatusTooManyRequests || errorCode(t, w) != "rate_limited" {
			t.Fatalf("failure %d past the budget: %d %s", i+1, w.Code, w.Body)
		}
		if got := w.Header().Get("Retry-After"); got != "1" {
			t.Errorf("Retry-After %q, want 1", got)
		}
	}
	if got := strings.Count(logged.String(), "repeated failed authentication"); got != 1 {
		t.Errorf("%d log lines for one address in one minute, want 1: %s", got, &logged)
	}
	if !strings.Contains(logged.String(), "ip=203.0.113.9") || !strings.Contains(logged.String(), `credential="api key"`) {
		t.Errorf("the line does not name the address and the credential: %s", &logged)
	}

	// An expired session keeps its 401 whatever the count: that status is how
	// the page learns to offer the sign-in again.
	if w := refusedFrom(a, "203.0.113.9", false); w.Code != http.StatusUnauthorized || errorCode(t, w) != "unauthorized" {
		t.Errorf("a session that failed past the budget: %d %s", w.Code, w.Body)
	}
	// Another address has its own count.
	if w := refusedFrom(a, "203.0.113.10", true); w.Code != http.StatusUnauthorized {
		t.Errorf("a first failure from another address: %d", w.Code)
	}
}

func TestWithoutAFailureBudgetARefusalIsWhatItWas(t *testing.T) {
	a := &Authenticator{}
	for range 100 {
		if w := refusedFrom(a, "203.0.113.9", true); w.Code != http.StatusUnauthorized {
			t.Fatalf("status %d", w.Code)
		}
	}
}
