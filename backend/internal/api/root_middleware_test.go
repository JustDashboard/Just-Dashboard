package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The request id and the body's deadline are each one line at the top of
// Routes. httpx holds the two middlewares to what they do, and nothing held
// the router to having them: these two tests fail when either line goes.

func TestEveryAnswerOfTheRouterCarriesARequestID(t *testing.T) {
	h := testServer(t).Routes()
	for _, path := range []string{"/api/v1/nope", "/api/v1/audit/", "/api/v1/auth/session"} {
		rec := anonymous(h, http.MethodGet, path)
		if id := rec.Header().Get(httpx.RequestIDHeader); len(id) != 16 {
			t.Errorf("GET %s = %d with %s %q, want sixteen hex digits", path, rec.Code, httpx.RequestIDHeader, id)
		}
	}
	// A caller's own plain id is the one that comes back.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil)
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set(httpx.RequestIDHeader, "ci-run-4711.step-2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if id := rec.Header().Get(httpx.RequestIDHeader); id != "ci-run-4711.step-2" {
		t.Errorf("the caller's own id came back as %q", id)
	}
}

// deadlineRecorder is a response whose connection takes a read deadline, as a
// real one does and a plain recorder does not. It counts the deadlines set.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	set, cleared int
}

func (d *deadlineRecorder) SetReadDeadline(at time.Time) error {
	if at.IsZero() {
		d.cleared++
	} else {
		d.set++
	}
	return nil
}

// A body read through the router is read against a deadline, and the deadline
// is taken off the connection again when the read is over: left standing, it
// would end a handler that is still working once the body has been read.
func TestABodyIsReadAgainstADeadlineThroughTheRouter(t *testing.T) {
	h := testServer(t).Routes()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"nobody","password":"not-the-password"}`))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(rec, req)

	if rec.set == 0 {
		t.Fatalf("POST /auth/login = %d and its body was read with no deadline: BodyDeadline is not on the router", rec.Code)
	}
	if rec.cleared == 0 {
		t.Errorf("%d deadlines were set for the body and none was taken off the connection", rec.set)
	}
}
