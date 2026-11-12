package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func queryRequest(rawQuery string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "/x?"+rawQuery, nil)
}

// refusedFor is the parameter a query refusal names, or "" when err is not one.
func refusedFor(t *testing.T, err error) string {
	t.Helper()
	var refused *APIError
	if !errors.As(err, &refused) {
		return ""
	}
	if refused.Status != http.StatusBadRequest || refused.Code != "bad_request" {
		t.Errorf("a query refusal is 400 bad_request, got %d %s", refused.Status, refused.Code)
	}
	return refused.Field
}

// One flag means one thing on every route: the two spellings the routes used
// to disagree about are both read, and nothing else is.
func TestAFlagIsOneOfFourSpellingsOrRefused(t *testing.T) {
	for raw, want := range map[string]bool{
		"preview=1": true, "preview=true": true, "preview=TRUE": true, "preview=True": true,
		"preview=0": false, "preview=false": false, "preview=FALSE": false,
		"": false, "other=1": false,
	} {
		got, err := QueryBool(queryRequest(raw), "preview")
		if err != nil || got != want {
			t.Errorf("?%s read as %v, %v; want %v", raw, got, err, want)
		}
	}
	// Each of these used to read as "not a preview" somewhere, and the request
	// then ran. A flag that cannot be read stops the request instead.
	for _, raw := range []string{
		"preview=yes", "preview=on", "preview=y", "preview=2", "preview=-1", "preview=",
		"preview", "preview=+1", "preview=1%20", "preview=t", "preview=1&preview=0", "preview=1&preview=1",
	} {
		got, err := QueryBool(queryRequest(raw), "preview")
		if field := refusedFor(t, err); field != "preview" || got {
			t.Errorf("?%s read as %v, %v; want a refusal naming preview", raw, got, err)
		}
	}
}

func TestAFlagThatDefaultsToOnIsStillRefusedWhenItCannotBeRead(t *testing.T) {
	for raw, want := range map[string]bool{"": true, "all=0": false, "all=false": false, "all=1": true} {
		got, err := QueryBoolOr(queryRequest(raw), "all", true)
		if err != nil || got != want {
			t.Errorf("?%s read as %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := QueryBoolOr(queryRequest("all=no"), "all", true); refusedFor(t, err) != "all" {
		t.Errorf("?all=no = %v, want a refusal naming all", err)
	}
}

func TestANumberIsInItsRangeOrRefused(t *testing.T) {
	for raw, want := range map[string]int{"": 100, "limit=": 100, "limit=1": 1, "limit=500": 500, "limit=%2042%20": 42} {
		got, err := QueryInt(queryRequest(raw), "limit", 100, 1, 500)
		if err != nil || got != want {
			t.Errorf("?%s read as %d, %v; want %d", raw, got, err, want)
		}
	}
	// limit=abc and limit=-5 used to answer the default page with a 200.
	for _, raw := range []string{"limit=abc", "limit=-5", "limit=0", "limit=501", "limit=1.5", "limit=1e3",
		"limit=99999999999999999999", "limit=1&limit=2"} {
		got, err := QueryInt(queryRequest(raw), "limit", 100, 1, 500)
		if field := refusedFor(t, err); field != "limit" || got != 100 {
			t.Errorf("?%s read as %d, %v; want a refusal naming limit", raw, got, err)
		}
	}
}

func TestATimeIsRFC3339OrRefused(t *testing.T) {
	at, ok, err := QueryTime(queryRequest("since=2026-11-01T09:30:00Z"), "since")
	if err != nil || !ok || !at.Equal(time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("an instant read as %v, %v, %v", at, ok, err)
	}
	// What a browser's toISOString sends.
	if _, ok, err := QueryTime(queryRequest("since=2026-11-01T09:30:00.123Z"), "since"); err != nil || !ok {
		t.Errorf("an instant with milliseconds: %v, %v", ok, err)
	}
	for _, raw := range []string{"", "since=", "other=x"} {
		if at, ok, err := QueryTime(queryRequest(raw), "since"); err != nil || ok || !at.IsZero() {
			t.Errorf("?%s read as %v, %v, %v; want absent", raw, at, ok, err)
		}
	}
	// since=tomorrow, and a date without a time, used to answer everything.
	for _, raw := range []string{"since=tomorrow", "since=2026-11-01", "since=1762000000",
		"since=2026-11-01T09:30:00Z&since=2026-11-02T09:30:00Z"} {
		at, ok, err := QueryTime(queryRequest(raw), "since")
		if field := refusedFor(t, err); field != "since" || ok || !at.IsZero() {
			t.Errorf("?%s read as %v, %v, %v; want a refusal naming since", raw, at, ok, err)
		}
	}
}

// A reader keeps the first refusal and answers defaults after it, so a handler
// that fills a struct and then asks Err never acts on a half-read address.
func TestAReaderKeepsTheFirstRefusal(t *testing.T) {
	clean := Query(queryRequest("failed=1&limit=20&since=2026-11-01T00:00:00Z&name=x"))
	if !clean.Bool("failed") || clean.BoolOr("all", true) != true || clean.Int("limit", 100, 1, 500) != 20 ||
		clean.Time("since").IsZero() || !clean.Time("until").IsZero() || clean.String("name") != "x" {
		t.Error("a readable address was not read")
	}
	if err := clean.Err(); err != nil {
		t.Errorf("a readable address was refused: %v", err)
	}

	bad := Query(queryRequest("failed=maybe&limit=abc&since=tomorrow"))
	if bad.Bool("failed") || bad.Int("limit", 100, 1, 500) != 100 || !bad.Time("since").IsZero() {
		t.Error("an unreadable value was read as something other than its default")
	}
	if field := refusedFor(t, bad.Err()); field != "failed" {
		t.Errorf("the refusal names %q, want the first unreadable parameter", field)
	}
}
