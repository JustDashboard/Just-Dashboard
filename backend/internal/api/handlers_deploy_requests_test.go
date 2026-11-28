package api

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func TestRequestFilterReadsTheAgentAndTheSiteItCameFrom(t *testing.T) {
	// The Agents and Came from lists are filters like every other list on
	// Insights: a press sends the family or the site they rank, and the
	// socket and the export read the same question as the window.
	q := url.Values{"agent": {" Googlebot "}, "referer": {"www.google.com"}, "maxMs": {"250"}}
	filter, err := requestFilterFrom(q)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Agent != "Googlebot" || filter.Referer != "www.google.com" || filter.MaxMs != 250 {
		t.Fatalf("filter = %+v", filter)
	}
	if filter.Since.IsZero() {
		t.Fatal("an unbounded question still reads a default window")
	}
}

// who narrows the window, the export and the tail to one side of the audience,
// and a value that is neither side is refused rather than answered with both.
func TestRequestFilterReadsWhoOrRefusesIt(t *testing.T) {
	for raw, want := range map[string]string{"": "", "people": accesslog.WhoPeople, " Bots ": accesslog.WhoBots} {
		filter, err := requestFilterFrom(url.Values{"who": {raw}})
		if err != nil || filter.Who != want {
			t.Fatalf("who=%q read as %q (%v), want %q", raw, filter.Who, err, want)
		}
	}
	_, err := requestFilterFrom(url.Values{"who": {"humans"}})
	var refusal *httpx.APIError
	if !errors.As(err, &refusal) || refusal.Status != http.StatusBadRequest || refusal.Field != "who" {
		t.Fatalf("who=humans = %v, want a 400 naming who", err)
	}
}
