package accesslog

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	agentChromeWindows = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	agentEdgeWindows   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0"
	agentSafariIPhone  = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
	agentSafariIPad    = "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
	agentChromePixel   = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	agentChromeTablet  = "Mozilla/5.0 (Linux; Android 13; SM-X700) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	agentFirefoxUbuntu = "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0"
	agentChromeOS      = "Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	agentGooglebot     = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	agentCurl          = "curl/8.5.0"
)

var audienceBase = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

// visit is one request as the proxy would have recorded it, at an offset from
// the start of the test's window.
func visit(offset time.Duration, ip, agent, path string, status int) Entry {
	return Entry{
		Method: "GET", Path: path, Status: status, Size: 100, Host: "site.test",
		RemoteIP: ip, UserAgent: agent, at: audienceBase.Add(offset).UnixNano(),
	}
}

func audienceOf(filter Filter, entries ...Entry) Result {
	filter.Limit = 10
	c := NewCollector(filter)
	for _, e := range entries {
		c.Feed(e)
	}
	return *c.Result()
}

func counts(facets []Facet) map[string]int {
	out := map[string]int{}
	for _, f := range facets {
		out[f.Value] = f.Count
	}
	return out
}

func TestAudienceCountsPeopleNotRequests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                                   string
		entries                                []Entry
		visitors, bots, humanRequests, botReqs int
	}{
		{
			name: "two requests from one address and browser are one visitor",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/", 200),
				visit(time.Second, "198.51.100.1", agentChromeWindows, "/pricing", 200),
			},
			visitors: 1, humanRequests: 2,
		},
		{
			name: "the same address with another browser is another visitor",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/", 200),
				visit(time.Second, "198.51.100.1", agentFirefoxUbuntu, "/", 200),
			},
			visitors: 2, humanRequests: 2,
		},
		{
			name: "the same browser from another address is another visitor",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/", 200),
				visit(time.Second, "198.51.100.2", agentChromeWindows, "/", 200),
			},
			visitors: 2, humanRequests: 2,
		},
		{
			name: "a crawler and a script are bots and not visitors",
			entries: []Entry{
				visit(0, "66.249.66.1", agentGooglebot, "/", 200),
				visit(time.Second, "203.0.113.5", agentCurl, "/", 200),
				visit(2*time.Second, "203.0.113.5", agentCurl, "/about", 200),
			},
			bots: 2, botReqs: 3,
		},
		{
			name: "bots are counted by address, however many agents it sends",
			entries: []Entry{
				visit(0, "203.0.113.5", agentCurl, "/", 200),
				visit(time.Second, "203.0.113.5", agentGooglebot, "/", 200),
			},
			bots: 1, botReqs: 2,
		},
		{
			name: "a browser that only fetched a script is not a visitor but is a browser",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/_next/static/x.js", 200),
			},
			humanRequests: 1,
		},
		{
			name: "a prefetch is not opening the page",
			entries: []Entry{
				func() Entry {
					e := visit(0, "198.51.100.1", agentChromeWindows, "/pricing", 200)
					e.Query = "_rsc=1abc"
					return e
				}(),
			},
			humanRequests: 1,
		},
		{
			name: "a refused probe is not a visit",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/.env", 404),
			},
			humanRequests: 1,
		},
		{
			name: "a HEAD request is never a person",
			entries: []Entry{
				func() Entry {
					e := visit(0, "198.51.100.1", agentChromeWindows, "/", 200)
					e.Method = "HEAD"
					return e
				}(),
			},
			humanRequests: 1,
		},
		{
			name: "files fetched before the page still belong to the visitor who opens it",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/_next/static/x.js", 200),
				visit(time.Second, "198.51.100.1", agentChromeWindows, "/", 200),
			},
			visitors: 1, humanRequests: 2,
		},
		{
			name: "no user agent at all is keyed by address and counts on opening a page",
			entries: []Entry{
				visit(0, "198.51.100.1", "", "/", 200),
				visit(time.Second, "198.51.100.1", "", "/about", 200),
				visit(2*time.Second, "198.51.100.2", "", "/", 200),
			},
			visitors: 2, humanRequests: 3,
		},
		{
			name: "no user agent and no page is still nobody",
			entries: []Entry{
				visit(0, "198.51.100.1", "", "/assets/app.css", 200),
			},
			humanRequests: 1,
		},
		{
			name: "a program that is neither a browser nor a known bot is neither",
			entries: []Entry{
				visit(0, "198.51.100.9", "Dalvik/2.1.0 (Linux; U; Android 12)", "/", 200),
			},
		},
		{
			name: "people and bots are told apart in one window",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/", 200),
				visit(time.Second, "66.249.66.1", agentGooglebot, "/", 200),
				visit(2*time.Second, "198.51.100.1", agentChromeWindows, "/.env", 404),
			},
			visitors: 1, bots: 1, humanRequests: 2, botReqs: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := audienceOf(Filter{}, tc.entries...).Summary.Audience
			if got.Visitors != tc.visitors || got.Bots != tc.bots || got.HumanRequests != tc.humanRequests || got.BotRequests != tc.botReqs {
				t.Fatalf("visitors %d bots %d human %d bot requests %d; want %d %d %d %d",
					got.Visitors, got.Bots, got.HumanRequests, got.BotRequests,
					tc.visitors, tc.bots, tc.humanRequests, tc.botReqs)
			}
			if len(got.Top) != got.Visitors {
				t.Fatalf("top names %d visitors of %d", len(got.Top), got.Visitors)
			}
		})
	}
}

func TestAudienceFacetsCountVisitorsNotRequests(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		// Each of these opens several pages: counted by request the phone would
		// outnumber everything else.
		visit(0, "198.51.100.1", agentSafariIPhone, "/", 200),
		visit(time.Second, "198.51.100.1", agentSafariIPhone, "/a", 200),
		visit(2*time.Second, "198.51.100.1", agentSafariIPhone, "/b", 200),
		visit(0, "198.51.100.2", agentChromePixel, "/", 200),
		visit(0, "198.51.100.3", agentEdgeWindows, "/", 200),
		visit(0, "198.51.100.4", agentSafariIPad, "/", 200),
		visit(0, "198.51.100.5", agentChromeTablet, "/", 200),
		visit(0, "198.51.100.6", agentFirefoxUbuntu, "/", 200),
		visit(0, "198.51.100.7", agentChromeOS, "/", 200),
		// Not people: a crawler, a script, and a browser that opened no page.
		visit(0, "66.249.66.1", agentGooglebot, "/", 200),
		visit(0, "203.0.113.5", agentCurl, "/", 200),
		visit(0, "198.51.100.8", agentChromeWindows, "/_next/static/x.js", 200),
	}
	got := audienceOf(Filter{}, entries...).Summary.Audience

	if got.Visitors != 7 {
		t.Fatalf("visitors = %d, want 7", got.Visitors)
	}
	for name, tc := range map[string]struct {
		got  []Facet
		want map[string]int
	}{
		"devices":  {got.Devices, map[string]int{"phone": 2, "tablet": 2, "desktop": 3}},
		"browsers": {got.Browsers, map[string]int{"Safari": 2, "Chrome": 3, "Edge": 1, "Firefox": 1}},
		"systems": {got.Systems, map[string]int{
			"iOS": 1, "Android": 2, "iPadOS": 1, "Windows": 1, "Ubuntu": 1, "ChromeOS": 1,
		}},
	} {
		if !reflect.DeepEqual(counts(tc.got), tc.want) {
			t.Errorf("%s = %+v, want %v", name, tc.got, tc.want)
		}
	}
	// Ranked by visitors, busiest first.
	if got.Devices[0].Value != "desktop" {
		t.Errorf("devices lead with %q, want desktop", got.Devices[0].Value)
	}
}

func TestAudienceFacetsCarryVisitorsWhoMetAnError(t *testing.T) {
	t.Parallel()
	got := audienceOf(Filter{},
		// Three failures for one visitor are one visitor who met an error.
		visit(0, "198.51.100.1", agentSafariIPhone, "/", 500),
		visit(time.Second, "198.51.100.1", agentSafariIPhone, "/", 502),
		visit(2*time.Second, "198.51.100.1", agentSafariIPhone, "/", 503),
		visit(0, "198.51.100.2", agentSafariIPhone, "/", 200),
		// A 404 is the caller's, not the site's.
		visit(0, "198.51.100.3", agentSafariIPhone, "/old", 404),
	).Summary.Audience
	if len(got.Devices) != 1 || got.Devices[0].Count != 3 || got.Devices[0].Errors != 1 {
		t.Fatalf("devices = %+v, want 3 phones of which 1 met a 5xx", got.Devices)
	}
}

func TestAudienceFacetsSkipWhatNothingNames(t *testing.T) {
	t.Parallel()
	got := audienceOf(Filter{}, visit(0, "198.51.100.1", "", "/", 200)).Summary.Audience
	if got.Visitors != 1 {
		t.Fatalf("visitors = %d, want 1", got.Visitors)
	}
	if len(got.Devices)+len(got.Browsers)+len(got.Systems) != 0 {
		t.Fatalf("a record with no agent was given a device, browser or system: %+v", got)
	}
	if got.Top[0].Agent != "" {
		t.Fatalf("agent = %q", got.Top[0].Agent)
	}
}

func TestAudienceFacetsAreRankedAndBounded(t *testing.T) {
	t.Parallel()
	var entries []Entry
	for i := 0; i < 3; i++ {
		entries = append(entries, visit(0, fmt.Sprintf("198.51.100.%d", i), agentFirefoxUbuntu, "/", 200))
	}
	entries = append(entries, visit(0, "198.51.100.10", agentChromeWindows, "/", 200))
	entries = append(entries, visit(0, "198.51.100.11", agentChromeWindows, "/", 200))
	entries = append(entries, visit(0, "198.51.100.12", agentSafariIPhone, "/", 200))
	got := audienceOf(Filter{}, entries...).Summary.Audience.Browsers
	var order []string
	for _, f := range got {
		order = append(order, fmt.Sprintf("%s=%d", f.Value, f.Count))
	}
	if want := "Firefox=3 Chrome=2 Safari=1"; strings.Join(order, " ") != want {
		t.Fatalf("browsers = %v, want %s", order, want)
	}
}

func TestVisitorReadsWhatTheyDid(t *testing.T) {
	t.Parallel()
	const ip = "198.51.100.1"
	first := visit(0, ip, agentChromeWindows, "/_next/static/a.js", 200)
	first.Size = 1000
	page := visit(2*time.Second, ip, agentChromeWindows, "/pricing", 200)
	page.Size = 2000
	gone := visit(3*time.Second, ip, agentChromeWindows, "/old", 404)
	gone.Size = 10
	broke := visit(4*time.Second+500*time.Millisecond, ip, agentChromeWindows, "/api/x", 500)
	broke.Size = 5

	got := audienceOf(Filter{}, first, page, gone, broke).Summary.Audience.Top
	if len(got) != 1 {
		t.Fatalf("top = %+v", got)
	}
	want := Visitor{
		Client: ip, Agent: agentChromeWindows,
		Requests: 4, Pages: 3, Errors: 1, Refused: 1, Bytes: 3015,
		First: "2026-09-19T12:00:00Z", Last: "2026-09-19T12:00:04.5Z",
		Landing: "/pricing", Journey: []string{"/pricing", "/old", "/api/x"},
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("visitor = %+v\nwant      %+v", got[0], want)
	}
}

func TestVisitorJourneyIsOrderedDistinctAndBounded(t *testing.T) {
	t.Parallel()
	const ip = "198.51.100.1"
	paths := []string{"/a", "/b", "/a", "/c", "/_next/static/x.js", "/d", "/b", "/e", "/f", "/g", "/h", "/.env"}
	var entries []Entry
	for i, p := range paths {
		status := 200
		if p == "/.env" {
			status = 404
		}
		entries = append(entries, visit(time.Duration(i)*time.Second, ip, agentChromeWindows, p, status))
	}
	top := audienceOf(Filter{}, entries...).Summary.Audience.Top[0]
	if want := []string{"/a", "/b", "/c", "/d", "/e", "/f"}; !reflect.DeepEqual(top.Journey, want) {
		t.Fatalf("journey = %v, want %v", top.Journey, want)
	}
	// The bound is on what is remembered, not on what is counted: the pages
	// past the sixth were still opened.
	if top.Pages != 10 {
		t.Fatalf("pages = %d, want all 10 opened, whatever the journey keeps", top.Pages)
	}
	if top.Landing != "/a" {
		t.Fatalf("landing = %q", top.Landing)
	}
}

func TestVisitorLandingAndReferer(t *testing.T) {
	t.Parallel()
	withReferer := func(e Entry, referer string) Entry {
		e.Referer = referer
		return e
	}
	cases := []struct {
		name             string
		entries          []Entry
		landing, referer string
	}{
		{
			name: "the first outside referer is the source, though it came after the first page",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/", 200),
				withReferer(visit(time.Second, "198.51.100.1", agentChromeWindows, "/pricing", 200), "https://www.google.com/search?q=x"),
			},
			landing: "/", referer: "www.google.com",
		},
		{
			name: "a later outside referer does not replace the first",
			entries: []Entry{
				withReferer(visit(0, "198.51.100.1", agentChromeWindows, "/", 200), "https://news.ycombinator.com/item?id=1"),
				withReferer(visit(time.Second, "198.51.100.1", agentChromeWindows, "/pricing", 200), "https://github.com/x"),
			},
			landing: "/", referer: "news.ycombinator.com",
		},
		{
			name: "the site's own pages are not a source",
			entries: []Entry{
				withReferer(visit(0, "198.51.100.1", agentChromeWindows, "/", 200), "https://site.test/"),
				withReferer(visit(time.Second, "198.51.100.1", agentChromeWindows, "/pricing", 200), "https://site.test/"),
			},
			landing: "/",
		},
		{
			name: "no referer at all",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/start", 200),
			},
			landing: "/start",
		},
		{
			name: "the landing page is the first page, not the first file",
			entries: []Entry{
				visit(0, "198.51.100.1", agentChromeWindows, "/favicon.ico", 200),
				visit(time.Second, "198.51.100.1", agentChromeWindows, "/docs", 200),
			},
			landing: "/docs",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			top := audienceOf(Filter{}, tc.entries...).Summary.Audience.Top
			if len(top) != 1 || top[0].Landing != tc.landing || top[0].Referer != tc.referer {
				t.Fatalf("top = %+v, want landing %q referer %q", top, tc.landing, tc.referer)
			}
		})
	}
}

func TestAudienceTopIsRankedAndBounded(t *testing.T) {
	t.Parallel()
	var entries []Entry
	// Fourteen visitors, each opening a different number of pages, so the
	// twelve most active are the ones with the most.
	for v := 1; v <= 14; v++ {
		for p := 0; p < v; p++ {
			entries = append(entries, visit(time.Duration(p)*time.Second, fmt.Sprintf("198.51.100.%d", v), agentChromeWindows, fmt.Sprintf("/p%d", p), 200))
		}
	}
	got := audienceOf(Filter{}, entries...).Summary.Audience
	if got.Visitors != 14 || len(got.Top) != 12 {
		t.Fatalf("visitors %d, top %d; want 14 and 12", got.Visitors, len(got.Top))
	}
	if got.Top[0].Client != "198.51.100.14" || got.Top[0].Pages != 14 || got.Top[11].Pages != 3 {
		t.Fatalf("top runs from %+v to %+v", got.Top[0], got.Top[11])
	}
}

func TestAudienceTopBreaksTiesByRequestsThenRecency(t *testing.T) {
	t.Parallel()
	got := audienceOf(Filter{},
		// Same pages, same requests: the one seen last leads.
		visit(0, "198.51.100.1", agentChromeWindows, "/", 200),
		visit(10*time.Second, "198.51.100.2", agentChromeWindows, "/", 200),
		// Same pages, more requests: ahead of both.
		visit(0, "198.51.100.3", agentChromeWindows, "/", 200),
		visit(time.Second, "198.51.100.3", agentChromeWindows, "/app.js", 200),
		// Fewer requests than anyone but more pages: ahead of everyone.
		visit(0, "198.51.100.4", agentChromeWindows, "/a", 200),
		visit(time.Second, "198.51.100.4", agentChromeWindows, "/b", 200),
	).Summary.Audience.Top
	var order []string
	for _, v := range got {
		order = append(order, v.Client)
	}
	if want := "198.51.100.4 198.51.100.3 198.51.100.2 198.51.100.1"; strings.Join(order, " ") != want {
		t.Fatalf("order = %v, want %s", order, want)
	}
}

func TestBucketsCountPeopleOverTime(t *testing.T) {
	t.Parallel()
	const a, b, c = "198.51.100.1", "198.51.100.2", "198.51.100.3"

	t.Run("one column a minute wide", func(t *testing.T) {
		t.Parallel()
		got := audienceOf(Filter{Since: audienceBase, Until: audienceBase.Add(2 * time.Minute)},
			visit(0, a, agentChromeWindows, "/", 200),
			visit(10*time.Second, a, agentChromeWindows, "/pricing", 200),
			visit(20*time.Second, b, agentSafariIPhone, "/", 200),
			visit(30*time.Second, c, agentFirefoxUbuntu, "/", 200),
			// Scripts and a crawler are not people looking at the site.
			visit(40*time.Second, "198.51.100.4", agentChromeWindows, "/_next/static/x.js", 200),
			visit(45*time.Second, "66.249.66.1", agentGooglebot, "/", 200),
			visit(time.Minute, a, agentChromeWindows, "/docs", 200),
			visit(time.Minute+5*time.Second, c, agentFirefoxUbuntu, "/docs", 200),
		).Summary
		if got.BucketSeconds != 60 {
			t.Fatalf("columns are %ds wide, want 60", got.BucketSeconds)
		}
		if got.Buckets[0].Visitors != 3 || got.Buckets[1].Visitors != 2 {
			t.Fatalf("columns hold %d and %d visitors, want 3 and 2", got.Buckets[0].Visitors, got.Buckets[1].Visitors)
		}
		// A and C appear in both columns and are two of three people, not four of five.
		if got.Audience.Visitors != 3 {
			t.Fatalf("visitors = %d, want 3", got.Audience.Visitors)
		}
	})

	t.Run("a person across a wide column is one person in it", func(t *testing.T) {
		t.Parallel()
		got := audienceOf(Filter{Since: audienceBase, Until: audienceBase.Add(2 * time.Hour)},
			visit(0, a, agentChromeWindows, "/", 200),
			visit(time.Minute, a, agentChromeWindows, "/docs", 200),
			visit(time.Minute+time.Second, b, agentChromeWindows, "/", 200),
			visit(2*time.Minute, a, agentChromeWindows, "/more", 200),
		).Summary
		if got.BucketSeconds != 120 {
			t.Fatalf("columns are %ds wide, want 120", got.BucketSeconds)
		}
		if got.Buckets[0].Visitors != 2 || got.Buckets[1].Visitors != 1 {
			t.Fatalf("columns hold %d and %d visitors, want 2 and 1", got.Buckets[0].Visitors, got.Buckets[1].Visitors)
		}
	})
}

func TestAudienceRespectsTheFilter(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		visit(0, "198.51.100.1", agentChromeWindows, "/pricing", 200),
		visit(time.Second, "198.51.100.1", agentChromeWindows, "/docs", 200),
		visit(2*time.Second, "198.51.100.2", agentSafariIPhone, "/docs", 200),
		visit(3*time.Second, "198.51.100.3", agentFirefoxUbuntu, "/docs", 503),
		visit(4*time.Second, "66.249.66.1", agentGooglebot, "/pricing", 200),
	}
	cases := []struct {
		name          string
		filter        Filter
		visitors      int
		bots          int
		topRequests   int
		humanRequests int
	}{
		{"nothing narrowed", Filter{}, 3, 1, 2, 4},
		{"a path", Filter{Path: "/pricing"}, 1, 1, 1, 1},
		{"a server error", Filter{Classes: []string{"5xx"}}, 1, 0, 1, 1},
		{"a browser", Filter{Agent: "Safari"}, 1, 0, 1, 1},
		{"a window that ends early", Filter{Until: audienceBase.Add(1500 * time.Millisecond)}, 1, 0, 2, 2},
		{"a path nobody opened", Filter{Path: "/nowhere"}, 0, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := audienceOf(tc.filter, entries...).Summary.Audience
			if got.Visitors != tc.visitors || got.Bots != tc.bots || got.HumanRequests != tc.humanRequests {
				t.Fatalf("visitors %d bots %d human %d; want %d %d %d", got.Visitors, got.Bots, got.HumanRequests, tc.visitors, tc.bots, tc.humanRequests)
			}
			if tc.visitors > 0 && got.Top[0].Requests != tc.topRequests {
				t.Fatalf("the busiest visitor made %d requests, want only the %d that matched", got.Top[0].Requests, tc.topRequests)
			}
		})
	}
}

func TestAudienceIsBoundedLikeTheFacets(t *testing.T) {
	t.Parallel()

	t.Run("visitors past the bound are not admitted and the held ones keep counting", func(t *testing.T) {
		t.Parallel()
		// One address and a new build string each time, so the Clients and Agents
		// tables stay small and it is the audience's own bound that is reached.
		build := func(i int) string { return fmt.Sprintf("Mozilla/5.0 (X11; Linux x86_64) build-%d", i) }
		c := NewCollector(Filter{Limit: 1})
		for i := 0; i <= facetCap; i++ {
			c.Feed(visit(time.Duration(i)*time.Millisecond, "198.51.100.1", build(i), "/", 200))
		}
		c.Feed(visit(time.Hour, "198.51.100.1", build(0), "/again", 200))
		got := c.Result()
		if got.Summary.Audience.Visitors != facetCap || !got.Summary.Truncated {
			t.Fatalf("visitors = %d truncated = %v, want %d and a floor", got.Summary.Audience.Visitors, got.Summary.Truncated, facetCap)
		}
		var first *Visitor
		for i := range got.Summary.Audience.Top {
			if got.Summary.Audience.Top[i].Agent == build(0) {
				first = &got.Summary.Audience.Top[i]
			}
		}
		if first == nil || first.Pages != 2 {
			t.Fatalf("a visitor admitted before the bound stopped counting: %+v", first)
		}
		// Every browser request is still a browser's request.
		if got.Summary.Audience.HumanRequests != facetCap+2 {
			t.Fatalf("human requests = %d, want %d", got.Summary.Audience.HumanRequests, facetCap+2)
		}
	})

	t.Run("browsers that opened nothing give their place to a person", func(t *testing.T) {
		t.Parallel()
		// The bound filled by hotlinked images, then a person opening a page:
		// the person is counted, and the chart's column says so.
		build := func(i int) string { return fmt.Sprintf("Mozilla/5.0 (X11; Linux x86_64) build-%d", i) }
		c := NewCollector(Filter{Limit: 1})
		for i := 0; i < facetCap; i++ {
			c.Feed(visit(time.Duration(i)*time.Millisecond, "198.51.100.1", build(i), "/logo.png", 200))
		}
		c.Feed(visit(time.Minute, "203.0.113.9", "Mozilla/5.0 (Macintosh) Chrome/140.0", "/", 200))
		got := c.Result().Summary
		if got.Audience.Visitors != 1 {
			t.Fatalf("visitors = %d, want the one person past the bound", got.Audience.Visitors)
		}
		if got.Truncated {
			t.Fatal("a page view that took an empty place made the reading a floor")
		}
		last := got.Buckets[len(got.Buckets)-1]
		if last.Visitors != 1 {
			t.Fatalf("the person's column counts %d visitors, want 1", last.Visitors)
		}
	})

	t.Run("bot addresses are bounded", func(t *testing.T) {
		t.Parallel()
		c := NewCollector(Filter{Limit: 1})
		for i := 0; i <= facetCap; i++ {
			c.Feed(visit(0, fmt.Sprintf("10.%d.%d.%d", i>>16, (i>>8)&255, i&255), agentCurl, "/", 200))
		}
		got := c.Result().Summary.Audience
		if got.Bots != facetCap || got.BotRequests != facetCap+1 {
			t.Fatalf("bots %d requests %d, want %d addresses and every one of the %d requests", got.Bots, got.BotRequests, facetCap, facetCap+1)
		}
	})

	t.Run("a minute remembers a bounded number of people", func(t *testing.T) {
		t.Parallel()
		c := NewCollector(Filter{Limit: 1})
		for i := 0; i <= minuteVisitorsCap; i++ {
			c.Feed(visit(0, fmt.Sprintf("10.0.%d.%d", i>>8, i&255), agentChromeWindows, "/", 200))
		}
		got := c.Result()
		if got.Summary.Buckets[0].Visitors != minuteVisitorsCap || !got.Summary.Truncated {
			t.Fatalf("column holds %d, truncated %v; want %d and a floor", got.Summary.Buckets[0].Visitors, got.Summary.Truncated, minuteVisitorsCap)
		}
		if got.Summary.Audience.Visitors != minuteVisitorsCap+1 {
			t.Fatalf("visitors = %d, want every one of the %d", got.Summary.Audience.Visitors, minuteVisitorsCap+1)
		}
	})
}

func TestAudienceWireShape(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(audienceOf(Filter{}, visit(0, "198.51.100.1", agentChromeWindows, "/", 200)).Summary)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Audience map[string]json.RawMessage `json:"audience"`
		Buckets  []map[string]json.RawMessage
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"visitors", "bots", "humanRequests", "botRequests", "devices", "browsers", "systems", "top"} {
		if _, ok := wire.Audience[key]; !ok {
			t.Errorf("audience has no %q: %s", key, raw)
		}
	}
	if _, ok := wire.Buckets[0]["visitors"]; !ok {
		t.Errorf("a column has no visitors: %s", raw)
	}
	var top []map[string]json.RawMessage
	if err := json.Unmarshal(wire.Audience["top"], &top); err != nil || len(top) != 1 {
		t.Fatalf("top = %s (%v)", wire.Audience["top"], err)
	}
	for _, key := range []string{"client", "agent", "requests", "pages", "errors", "refused", "bytes", "first", "last", "landing", "journey"} {
		if _, ok := top[0][key]; !ok {
			t.Errorf("a visitor has no %q: %s", key, wire.Audience["top"])
		}
	}

	// A window nobody came in must still be lists, not nulls the page has to
	// guard against.
	empty, err := json.Marshal(audienceOf(Filter{}).Summary.Audience)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"devices", "browsers", "systems", "top"} {
		if !strings.Contains(string(empty), `"`+key+`":[]`) {
			t.Errorf("empty %s is not a list: %s", key, empty)
		}
	}
}

func TestDeviceAndSystemReadTheAgentLikeThePage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, agent, device, system string
	}{
		{"windows chrome", agentChromeWindows, "desktop", "Windows"},
		{"windows edge", agentEdgeWindows, "desktop", "Windows"},
		{"iphone", agentSafariIPhone, "phone", "iOS"},
		{"ipad says mobile and is still a tablet", agentSafariIPad, "tablet", "iPadOS"},
		{"android phone", agentChromePixel, "phone", "Android"},
		{"android without mobi is a tablet", agentChromeTablet, "tablet", "Android"},
		{"a tablet that says so", "Mozilla/5.0 (X11; Linux x86_64) Tablet Firefox/127.0", "tablet", "Linux"},
		{"ubuntu before linux", agentFirefoxUbuntu, "desktop", "Ubuntu"},
		{"fedora before linux", "Mozilla/5.0 (X11; Fedora; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0", "desktop", "Fedora"},
		{"chromeos before linux", agentChromeOS, "desktop", "ChromeOS"},
		{"a mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "desktop", "macOS"},
		{"an ipod", "Mozilla/5.0 (iPod touch; CPU iPhone OS 15_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148", "phone", "iOS"},
		{"bare linux", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36", "desktop", "Linux"},
		{"a browser that names no system", "Mozilla/5.0 Gecko/20100101 Firefox/127.0", "desktop", ""},
		{"a script is no device", agentCurl, "", ""},
		{"no agent", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := deviceOf(tc.agent); got != tc.device {
				t.Errorf("device = %q, want %q", got, tc.device)
			}
			if got := systemOf(tc.agent); got != tc.system {
				t.Errorf("system = %q, want %q", got, tc.system)
			}
		})
	}
}
