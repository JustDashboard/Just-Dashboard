package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// limited sends one request as p from one address through l.ByPrincipal.
func limited(l *Limiter, p *Principal) *httptest.ResponseRecorder {
	h := l.ByPrincipal(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	r.RemoteAddr = "127.0.0.1:4000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	return w
}

// The person's credentials, all at one address: what an SSH tunnel makes of a
// browser, a CI job and the assistant.
func credentialsOfOnePerson() (session, otherSession, key, otherKey, assistant *Principal) {
	ada := &auth.User{ID: 1, Username: "ada"}
	session = &Principal{User: ada, Kind: "session", SessionID: "s1"}
	otherSession = &Principal{User: ada, Kind: "session", SessionID: "s2"}
	key = &Principal{User: ada, Kind: "token", TokenID: 7, Token: &auth.APIToken{ID: 7}}
	otherKey = &Principal{User: ada, Kind: "token", TokenID: 8, Token: &auth.APIToken{ID: 8}}
	assistant = &Principal{User: ada, Kind: "session", SessionID: "s1", Assistant: true}
	return
}

func TestEachCredentialHasABudgetOfItsOwn(t *testing.T) {
	session, otherSession, key, otherKey, assistant := credentialsOfOnePerson()
	l := NewLimiter(60, 2).PerCredential()

	// A script empties its key's bucket.
	for i := range 2 {
		if w := limited(l, key); w.Code != http.StatusNoContent {
			t.Fatalf("request %d of the key: %d", i+1, w.Code)
		}
	}
	if w := limited(l, key); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the key past its budget: %d, want 429", w.Code)
	}
	// Nothing else of the person's is out of budget for that.
	for name, p := range map[string]*Principal{
		"the browser session": session, "a second session": otherSession, "another key": otherKey,
	} {
		for i := range 2 {
			if w := limited(l, p); w.Code != http.StatusNoContent {
				t.Errorf("%s, request %d: %d; it shares a bucket with the key", name, i+1, w.Code)
			}
		}
		if w := limited(l, p); w.Code != http.StatusTooManyRequests {
			t.Errorf("%s past its own budget: %d, want 429", name, w.Code)
		}
	}
	// The assistant is not one more credential: it works under the session's
	// own principal and that session's budget is the one it spends (security
	// invariant 13), so it is out of budget with the session and not before.
	if w := limited(l, assistant); w.Code != http.StatusTooManyRequests {
		t.Errorf("the assistant in a session that is out of budget: %d, want 429", w.Code)
	}
}

// What the assistant spends is its session's, both ways round: its calls come
// out of the budget the person's page has, and it has no second one to go on
// with when that is gone.
func TestTheAssistantSpendsTheBudgetOfTheSessionItWorksIn(t *testing.T) {
	session, otherSession, _, _, assistant := credentialsOfOnePerson()
	l := NewLimiter(60, 2).PerCredential()
	if w := limited(l, assistant); w.Code != http.StatusNoContent {
		t.Fatalf("the assistant's first call: %d", w.Code)
	}
	if w := limited(l, session); w.Code != http.StatusNoContent {
		t.Fatalf("the session's request after it: %d", w.Code)
	}
	for name, p := range map[string]*Principal{"the assistant": assistant, "the session": session} {
		if w := limited(l, p); w.Code != http.StatusTooManyRequests {
			t.Errorf("%s after the two the session had: %d, want 429", name, w.Code)
		}
	}
	// The person's other session is another credential.
	if w := limited(l, otherSession); w.Code != http.StatusNoContent {
		t.Errorf("the person's other session: %d", w.Code)
	}
}

// The destructive budget is not made PerCredential: a person has one allowance
// of irreversible actions, whichever credential spends it.
func TestALimiterIsThePersonsUnlessMadePerCredential(t *testing.T) {
	session, _, key, _, assistant := credentialsOfOnePerson()
	l := NewLimiter(60, 2)
	limited(l, session)
	limited(l, key)
	for name, p := range map[string]*Principal{"the session": session, "the key": key, "the assistant": assistant} {
		if w := limited(l, p); w.Code != http.StatusTooManyRequests {
			t.Errorf("%s after the person's two: %d, want 429", name, w.Code)
		}
	}
	// A different person at the same address is not the same bucket.
	grace := &Principal{User: &auth.User{ID: 2, Username: "grace"}, Kind: "session", SessionID: "s9"}
	if w := limited(l, grace); w.Code != http.StatusNoContent {
		t.Errorf("another person: %d", w.Code)
	}
}

func TestARateLimitRefusalSaysTheRealWait(t *testing.T) {
	session, _, _, _, _ := credentialsOfOnePerson()
	for _, tc := range []struct {
		perMinute int
		want      string
	}{
		{6, "10"},  // one every ten seconds
		{30, "2"},  // the destructive budget's pace
		{600, "1"}, // a tenth of a second is never said as 0: that reads as "now"
	} {
		l := NewLimiter(tc.perMinute, 1)
		if w := limited(l, session); w.Code != http.StatusNoContent {
			t.Fatalf("%d a minute, first request: %d", tc.perMinute, w.Code)
		}
		w := limited(l, session)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("%d a minute, second request: %d, want 429", tc.perMinute, w.Code)
		}
		if got := w.Header().Get("Retry-After"); got != tc.want {
			t.Errorf("%d a minute: Retry-After %q, want %q", tc.perMinute, got, tc.want)
		}
		if got := w.Header().Get("RateLimit-Remaining"); got != "0" {
			t.Errorf("%d a minute: RateLimit-Remaining %q on the refusal, want 0", tc.perMinute, got)
		}
		var body struct {
			Error APIError `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("the refusal is not the envelope: %v: %s", err, w.Body)
		}
		if body.Error.Code != "rate_limited" || !body.Error.Retryable || body.Error.Message == "" {
			t.Errorf("%d a minute: envelope %+v, want rate_limited and retryable", tc.perMinute, body.Error)
		}
	}
}

func TestAnAdmittedRequestSaysWhatIsLeft(t *testing.T) {
	session, _, _, _, _ := credentialsOfOnePerson()
	l := NewLimiter(1, 3)
	for _, want := range []string{"2", "1", "0"} {
		w := limited(l, session)
		if got := w.Header().Get("RateLimit-Remaining"); w.Code != http.StatusNoContent || got != want {
			t.Fatalf("status %d, RateLimit-Remaining %q; want 204 and %q", w.Code, got, want)
		}
		if w.Header().Get("Retry-After") != "" {
			t.Errorf("an admitted request carries Retry-After %q", w.Header().Get("Retry-After"))
		}
	}
}

// A refusal spends nothing: the wait a client is told does not grow because it
// asked again before the wait was over.
func TestARefusedRequestDoesNotLengthenTheWait(t *testing.T) {
	l := NewLimiter(6, 1)
	if ok, _, _ := l.take("k"); !ok {
		t.Fatal("the first request was refused")
	}
	for i := range 5 {
		ok, wait, _ := l.take("k")
		if ok || wait <= 0 || wait.Seconds() > 10 {
			t.Fatalf("refusal %d: admitted %v, wait %v; want a refusal of at most 10 s", i+1, ok, wait)
		}
	}
}

// A handler that applies a budget by hand (a destructive statement found in a
// body, a second-factor guess) answers like the middleware does.
func TestABudgetSpentByHandCarriesItsWait(t *testing.T) {
	l := NewLimiter(30, 1)
	if err := l.Spend("ada|dbquery", "too many destructive statements, slow down"); err != nil {
		t.Fatalf("the first spend: %v", err)
	}
	err := l.Spend("ada|dbquery", "too many destructive statements, slow down")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusTooManyRequests || apiErr.Code != "rate_limited" {
		t.Fatalf("the second spend: %v, want 429 rate_limited", err)
	}
	w := httptest.NewRecorder()
	WriteError(w, httptest.NewRequest(http.MethodPost, "/api/v1/databases/1/query", nil), err)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "2" {
		t.Errorf("status %d, Retry-After %q; want 429 and 2", w.Code, w.Header().Get("Retry-After"))
	}
	var body struct {
		Error APIError `json:"error"`
	}
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error.Message != "too many destructive statements, slow down" {
		t.Errorf("the handler's own message is lost: %s", w.Body)
	}
	// Another bucket of the same limiter is untouched.
	if err := l.Spend("ada|redis", "x"); err != nil {
		t.Errorf("a different bucket: %v", err)
	}
}
