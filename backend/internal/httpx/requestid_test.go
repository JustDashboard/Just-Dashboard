package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var generatedRequestID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// captureDefaultLog swaps the default logger, which WriteError writes to, for
// one the test can read.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &out
}

// A 500 hides its cause from the client on purpose. The id is what lets the
// person holding it name the request: it is in the header, in the envelope and
// on the log line that has the cause.
func TestAServerErrorCanBeMatchedToItsLogLine(t *testing.T) {
	logged := captureDefaultLog(t)
	h := RequestID(Handler(func(w http.ResponseWriter, r *http.Request) error {
		return Internal(errors.New("disk on fire"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/proxy/vhosts", nil))

	id := rec.Header().Get(RequestIDHeader)
	if !generatedRequestID.MatchString(id) {
		t.Fatalf("X-Request-Id = %q, want sixteen hex digits", id)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"requestId"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusInternalServerError || body.Error.Code != "internal" || body.Error.Message != "internal error" {
		t.Errorf("the answer = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "disk on fire") {
		t.Error("the cause reached the client")
	}
	if body.Error.RequestID != id {
		t.Errorf("the envelope names request %q, the header %q", body.Error.RequestID, id)
	}
	var line struct {
		Msg, Request, Err string
	}
	if err := json.Unmarshal(logged.Bytes(), &line); err != nil {
		t.Fatalf("the log line: %v in %s", err, logged.String())
	}
	if line.Msg != "request failed" || line.Request != id || line.Err != "disk on fire" {
		t.Errorf("the log line = %+v, want the cause under request %s", line, id)
	}
	// The shared error values are not written to: the next request has its own id.
	if ErrNotFound.RequestID != "" {
		t.Error("a shared error value was given a request id")
	}
}

// A refusal that says what was wrong needs no id in its body; the header has
// it all the same.
func TestARefusalCarriesTheIDInItsHeaderOnly(t *testing.T) {
	h := RequestID(Handler(func(w http.ResponseWriter, r *http.Request) error { return ErrNotFound }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Header().Get(RequestIDHeader) == "" || strings.Contains(rec.Body.String(), "requestId") {
		t.Errorf("header %q, body %s", rec.Header().Get(RequestIDHeader), rec.Body.String())
	}
}

func TestACallersOwnRequestIDIsKeptOnlyWhenItIsPlain(t *testing.T) {
	seen := ""
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = RequestIDFrom(r.Context()) }))
	send := func(sent string) string {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		if sent != "" {
			r.Header.Set(RequestIDHeader, sent)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if got := rec.Header().Get(RequestIDHeader); got != seen {
			t.Errorf("the header says %q and the context %q", got, seen)
		}
		return seen
	}
	for _, kept := range []string{"ci-run-4821.step-3", "0af7651916cd43dd8448eb211c80319c", "3f2b1c9e-8a44-4f0e-9d0a-1b2c3d4e5f60"} {
		if got := send(kept); got != kept {
			t.Errorf("a plain id %q was replaced with %q", kept, got)
		}
	}
	for _, replaced := range []string{"", "short", strings.Repeat("a", 65), "two words here", "id\twith-a-tab", `quote"in-the-id`, "naïve-identifier"} {
		if got := send(replaced); got == replaced || !generatedRequestID.MatchString(got) {
			t.Errorf("%q was kept as %q, want a generated id", replaced, got)
		}
	}
	if first, second := send(""), send(""); first == second {
		t.Errorf("two requests share the id %s", first)
	}
}

// A request the dashboard makes of itself, through its own router, is part of
// the request that caused it and keeps its id.
func TestAnInnerRequestKeepsTheOuterID(t *testing.T) {
	var inner string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { inner = RequestIDFrom(r.Context()) }))
	ctx := context.WithValue(context.Background(), requestIDKey, "outer-request-1")
	r := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
	r.Header.Set(RequestIDHeader, "something-else-entirely")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if inner != "outer-request-1" {
		t.Errorf("the inner request ran as %q", inner)
	}
}

func TestAPanicIsLoggedAndAnsweredUnderItsRequestID(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&out, nil))
	h := RequestID(Recoverer(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	id := rec.Header().Get(RequestIDHeader)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), `"requestId":"`+id+`"`) {
		t.Errorf("the answer = %d %s, want request %s in it", rec.Code, rec.Body.String(), id)
	}
	if !strings.Contains(out.String(), `"request":"`+id+`"`) {
		t.Errorf("the panic's log line does not name request %s: %s", id, out.String())
	}
}
