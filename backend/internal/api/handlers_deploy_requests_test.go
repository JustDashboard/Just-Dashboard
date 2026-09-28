package api

import (
	"net/url"
	"testing"
)

func TestRequestFilterReadsTheAgentAndTheSiteItCameFrom(t *testing.T) {
	// The Agents and Came from lists are filters like every other list on
	// Insights: a press sends the family or the site they rank, and the
	// socket and the export read the same question as the window.
	q := url.Values{"agent": {" Googlebot "}, "referer": {"www.google.com"}, "maxMs": {"250"}}
	filter := requestFilterFrom(q)
	if filter.Agent != "Googlebot" || filter.Referer != "www.google.com" || filter.MaxMs != 250 {
		t.Fatalf("filter = %+v", filter)
	}
	if filter.Since.IsZero() {
		t.Fatal("an unbounded question still reads a default window")
	}
}
