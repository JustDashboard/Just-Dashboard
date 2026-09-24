package accesslog

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

const caddyLineJSON = `{"level":"info","ts":1758276477.5,"logger":"http.log.access.log0","msg":"handled request","request":{"remote_ip":"203.0.113.9","remote_port":"54321","client_ip":"198.51.100.4","proto":"HTTP/2.0","method":"GET","host":"app.example.com","uri":"/api/items?page=2","headers":{"User-Agent":["Mozilla/5.0 Chrome/140.0"],"Referer":["https://app.example.com/"]},"tls":{"version":772}},"bytes_read":0,"duration":0.0125,"size":2048,"status":200}`

func TestParseCaddyJSON(t *testing.T) {
	entry, ok := Parse(caddyLineJSON)
	if !ok {
		t.Fatal("a Caddy access entry was not recognised")
	}
	if entry.Method != "GET" || entry.Status != 200 || entry.Host != "app.example.com" {
		t.Fatalf("request read wrong: %+v", entry)
	}
	// client_ip wins over remote_ip: behind another proxy the remote address
	// is the proxy, and a table of one repeated address is useless.
	if entry.RemoteIP != "198.51.100.4" {
		t.Fatalf("client address = %q, want the client_ip", entry.RemoteIP)
	}
	if entry.Path != "/api/items" || entry.Query != "page=2" {
		t.Fatalf("path/query split wrong: %q %q", entry.Path, entry.Query)
	}
	if ms, ok := entry.Duration(); !ok || math.Abs(ms-12.5) > 0.001 {
		t.Fatalf("duration = %v %v, want 12.5ms", ms, ok)
	}
	if !entry.TLS {
		t.Fatal("a request with a tls block is an HTTPS request")
	}
	if entry.UserAgent == "" || entry.Referer == "" {
		t.Fatalf("headers were dropped: %+v", entry)
	}
	if got := entry.At().UTC().Format(time.RFC3339); got != "2025-09-19T10:07:57Z" {
		t.Fatalf("timestamp = %s", got)
	}
}

func TestParseCaddyRFC3339Timestamp(t *testing.T) {
	line := `{"ts":"2026-09-19T05:47:57.123Z","msg":"handled request","request":{"method":"POST","host":"h","uri":"/x"},"status":201,"size":3,"duration":0.5}`
	entry, ok := Parse(line)
	if !ok {
		t.Fatal("an operator who set time_format rfc3339 still has an access log")
	}
	if entry.At().Year() != 2026 || entry.Status != 201 {
		t.Fatalf("entry read wrong: %+v", entry)
	}
}

func TestParseCaddyIgnoresNonRequestLines(t *testing.T) {
	// Caddy's own startup notices share the file's shape but describe no
	// request; counting one as a hit reports a status of zero.
	for _, line := range []string{
		`{"level":"info","ts":1758276477.5,"msg":"serving initial configuration"}`,
		`{"level":"error","ts":1758276477.5,"logger":"tls","msg":"job failed"}`,
		``,
		`not json at all`,
	} {
		if _, ok := Parse(line); ok {
			t.Fatalf("%q was read as a served request", line)
		}
	}
}

func TestParseCombined(t *testing.T) {
	line := `198.51.100.4 - - [19/Sep/2026:05:47:57 +0000] "GET /a%20b?x=1 HTTP/1.1" 404 512 "https://ref/" "curl/8.5.0"`
	entry, ok := Parse(line)
	if !ok {
		t.Fatal("nginx's stock combined line was not recognised")
	}
	if entry.Method != "GET" || entry.Status != 404 || entry.Size != 512 {
		t.Fatalf("entry read wrong: %+v", entry)
	}
	if entry.Path != "/a b" {
		t.Fatalf("path = %q, want it percent-decoded so one route is one row", entry.Path)
	}
	if entry.Query != "x=1" || entry.Proto != "HTTP/1.1" {
		t.Fatalf("query/proto wrong: %+v", entry)
	}
	if entry.Referer != "https://ref/" || entry.UserAgent != "curl/8.5.0" {
		t.Fatalf("quoted fields wrong: %+v", entry)
	}
	// combined carries no duration, and inventing a zero would put a
	// measurement on the page that was never measured.
	if ms, ok := entry.Duration(); ok {
		t.Fatalf("duration = %v, want absent for combined", ms)
	}
}

func TestParseCombinedEscapedQuoteInAgent(t *testing.T) {
	line := `1.2.3.4 - - [19/Sep/2026:05:47:57 +0000] "GET / HTTP/1.1" 200 - "-" "Mozilla \"weird\" 1.0"`
	entry, ok := Parse(line)
	if !ok {
		t.Fatal("a client sending a quote in its agent still made a request")
	}
	if entry.UserAgent != `Mozilla "weird" 1.0` {
		t.Fatalf("agent = %q", entry.UserAgent)
	}
	if entry.Referer != "" {
		t.Fatalf("referer = %q, want empty for the - placeholder", entry.Referer)
	}
	if entry.Size != 0 {
		t.Fatalf("size = %d, want 0 for the - placeholder", entry.Size)
	}
}

func TestParseCombinedRejectsJunk(t *testing.T) {
	for _, line := range []string{
		"",
		"just some text",
		`1.2.3.4 - - [not a date] "GET / HTTP/1.1" 200 1`,
		`1.2.3.4 - - [19/Sep/2026:05:47:57 +0000] "GET / HTTP/1.1" notastatus 1`,
	} {
		if _, ok := Parse(line); ok {
			t.Fatalf("%q was read as a request", line)
		}
	}
}

func entry(at time.Time, method, path string, status int, ms float64) Entry {
	return Entry{
		Method: method, Path: path, Status: status, Size: 100, RemoteIP: "1.2.3.4",
		at: at.UnixNano(), duration: ms, timed: true,
	}
}

func TestEntryWireShape(t *testing.T) {
	at := time.Date(2026, 9, 19, 12, 0, 0, 500_000_000, time.UTC)
	raw, err := json.Marshal(entry(at, "GET", "/x", 200, 12.5))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["time"] != "2026-09-19T12:00:00.5Z" {
		t.Fatalf("time = %v", wire["time"])
	}
	if wire["durationMs"] != 12.5 {
		t.Fatalf("durationMs = %v", wire["durationMs"])
	}
	// The compact fields never reach the wire under their own names.
	for _, key := range []string{"at", "duration", "timed"} {
		if _, present := wire[key]; present {
			t.Fatalf("%q leaked onto the wire", key)
		}
	}
	untimed, _ := json.Marshal(Entry{Method: "GET", Path: "/", Status: 200, at: at.UnixNano()})
	if strings.Contains(string(untimed), "durationMs") {
		t.Fatalf("an unmeasured duration must be absent, not zero: %s", untimed)
	}
}

func TestCollectorReadings(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{Limit: 10})
	for i := 0; i < 100; i++ {
		status := 200
		if i%10 == 0 {
			status = 500
		}
		c.Feed(entry(base.Add(time.Duration(i)*time.Second), "GET", "/api", status, float64(i)))
	}
	result := c.Result()
	if result.Summary.Total != 100 {
		t.Fatalf("total = %d", result.Summary.Total)
	}
	if result.Summary.Classes["5xx"] != 10 || result.Summary.Classes["2xx"] != 90 {
		t.Fatalf("classes = %v", result.Summary.Classes)
	}
	if math.Abs(result.Summary.ErrorRate-0.10) > 1e-9 {
		t.Fatalf("error rate = %v, want 0.10", result.Summary.ErrorRate)
	}
	// Nearest rank: the p95 must be a duration a request actually took, so an
	// operator reading it can go and find that request in the rows.
	if result.Summary.Latency == nil || result.Summary.Latency.P95 != 94 {
		t.Fatalf("p95 = %+v, want the 95th sample", result.Summary.Latency)
	}
	if len(result.Entries) != 10 {
		t.Fatalf("returned %d rows, want the limit", len(result.Entries))
	}
	// Newest first, and the newest of a hundred rather than the oldest ten.
	if result.Entries[0].At() != base.Add(99*time.Second) {
		t.Fatalf("first row is %v, want the newest request", result.Entries[0].At())
	}
}

func TestCollectorLimitDoesNotCapTheReadings(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{Limit: 5})
	for i := 0; i < 500; i++ {
		c.Feed(entry(base.Add(time.Duration(i)*time.Second), "GET", "/", 200, 1))
	}
	result := c.Result()
	if result.Summary.Total != 500 {
		t.Fatalf("total = %d: a table showing the last five requests must not report a p95 of only those five", result.Summary.Total)
	}
	if len(result.Entries) != 5 {
		t.Fatalf("rows = %d", len(result.Entries))
	}
}

func TestFilterNarrows(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{Classes: []string{"5xx"}, Limit: 10})
	c.Feed(entry(base, "GET", "/ok", 200, 5))
	c.Feed(entry(base.Add(time.Second), "POST", "/broken", 503, 900))
	result := c.Result()
	if result.Summary.Total != 1 || result.Entries[0].Path != "/broken" {
		t.Fatalf("filter kept the wrong rows: %+v", result.Entries)
	}
	if result.Summary.Scanned != 2 {
		t.Fatalf("scanned = %d, want both lines counted", result.Summary.Scanned)
	}
}

func TestFilterOnLatencyDropsUntimedEntries(t *testing.T) {
	// A combined-format log has no durations at all. Narrowing by latency must
	// not quietly return everything.
	c := NewCollector(Filter{MinMs: 100, Limit: 10})
	e := Entry{Method: "GET", Path: "/", Status: 200, at: time.Now().UnixNano()}
	c.Feed(e)
	if c.Result().Summary.Total != 0 {
		t.Fatal("a request with no measured duration cannot satisfy a latency filter")
	}
}

func TestFacetsCarryTheirErrors(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{Limit: 10})
	for i := 0; i < 5; i++ {
		c.Feed(entry(base.Add(time.Duration(i)*time.Second), "GET", "/healthy", 200, 1))
	}
	for i := 0; i < 3; i++ {
		c.Feed(entry(base.Add(time.Duration(i)*time.Second), "GET", "/failing", 500, 1))
	}
	paths := c.Result().Summary.Paths
	if len(paths) != 2 || paths[0].Value != "/healthy" || paths[0].Count != 5 {
		t.Fatalf("paths ranked wrong: %+v", paths)
	}
	if paths[1].Value != "/failing" || paths[1].Errors != 3 {
		t.Fatalf("the failing path must carry its own error count: %+v", paths[1])
	}
}

func TestHistogramCoversTheRequestedWindow(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{Since: base, Until: base.Add(time.Hour), Limit: 10})
	for i := 0; i < 60; i++ {
		c.Feed(entry(base.Add(time.Duration(i)*time.Minute), "GET", "/", 200, 1))
	}
	result := c.Result()
	if len(result.Summary.Buckets) == 0 {
		t.Fatal("a window with requests in it has columns")
	}
	total := 0
	for _, b := range result.Summary.Buckets {
		total += b.Total
	}
	if total != 60 {
		t.Fatalf("columns hold %d of 60 requests — a request fell outside every column", total)
	}
	if result.Summary.Buckets[0].Start[:16] != "2026-09-19T12:00" {
		t.Fatalf("the first column starts at %s, not at the window's own start", result.Summary.Buckets[0].Start)
	}
}

func TestHistogramColumnsCarryTheirBytesAndTheirWidth(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	hour := NewCollector(Filter{Since: base, Until: base.Add(time.Hour), Limit: 10})
	for i := 0; i < 3; i++ {
		hour.Feed(entry(base.Add(time.Duration(i)*time.Second), "GET", "/", 200, 1))
	}
	result := hour.Result()
	if result.Summary.BucketSeconds != 60 {
		t.Fatalf("an hour's columns are %ds wide, want 60", result.Summary.BucketSeconds)
	}
	if result.Summary.Buckets[0].Bytes != 300 {
		t.Fatalf("the first column sent %d bytes, want the three answers' 300", result.Summary.Buckets[0].Bytes)
	}

	// A day on the same sixty columns is 24 minutes a column, and the summary
	// has to say so or the chart labels every point "1 min".
	day := NewCollector(Filter{Since: base, Until: base.Add(24 * time.Hour), Limit: 10})
	day.Feed(entry(base, "GET", "/", 200, 1))
	if got := day.Result().Summary.BucketSeconds; got != 24*60 {
		t.Fatalf("a day's columns are %ds wide, want %d", got, 24*60)
	}
}

func TestHistogramKeepsTheNewestMinute(t *testing.T) {
	// "The last hour", asked partway into a minute with nothing asked for an
	// end: the page's readings poll exactly this. The hour spans 61 clock
	// minutes, and the one that goes has to be the oldest — the newest holds
	// the 5xx that just happened.
	now := time.Date(2026, 9, 19, 12, 0, 40, 0, time.UTC)
	c := NewCollector(Filter{Since: now.Add(-time.Hour), Limit: 1})
	c.Feed(entry(now.Add(-50*time.Minute), "GET", "/", 200, 1))
	c.Feed(entry(now.Add(-5*time.Second), "GET", "/", 500, 1))
	result := c.Result()
	columns := result.Summary.Buckets
	if len(columns) != buckets {
		t.Fatalf("%d columns, want %d", len(columns), buckets)
	}
	if last := columns[len(columns)-1].Start[:16]; last != "2026-09-19T12:00" {
		t.Fatalf("the last column starts at %s, want the current minute 12:00", last)
	}
	failed, total := 0, 0
	for _, column := range columns {
		failed += column.Counts["5xx"]
		total += column.Total
	}
	if failed != 1 || total != 2 {
		t.Fatalf("columns hold %d requests and %d × 5xx, want both requests and the one 5xx", total, failed)
	}

	// The same window asked with an end on the minute keeps its own start.
	start := time.Date(2026, 9, 19, 11, 0, 0, 0, time.UTC)
	whole := NewCollector(Filter{Since: start, Until: start.Add(time.Hour), Limit: 1})
	whole.Feed(entry(start, "GET", "/", 200, 1))
	if first := whole.Result().Summary.Buckets[0].Start[:16]; first != "2026-09-19T11:00" {
		t.Fatalf("a whole-minute window's first column starts at %s, want 11:00", first)
	}
}

func TestHistogramSurvivesASingleRequest(t *testing.T) {
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{Limit: 10})
	c.Feed(entry(at, "GET", "/", 200, 1))
	result := c.Result()
	if len(result.Summary.Buckets) == 0 {
		t.Fatal("one request is still a chart with one column, not a crash")
	}
}

func TestAgentFamilyGroups(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (X11) Chrome/140.0.0.0 Safari/537.36":       "Chrome",
		"Mozilla/5.0 (Windows) Chrome/140 Safari/537.36 Edg/140": "Edge",
		"curl/8.5.0": "curl",
		"Mozilla/5.0 (compatible; Googlebot/2.1)": "Googlebot",
		"": "",
	}
	for agent, want := range cases {
		if got := agentFamily(agent); got != want {
			t.Errorf("agentFamily(%q) = %q, want %q", agent, got, want)
		}
	}
}

func TestClassBoundaries(t *testing.T) {
	cases := map[int]string{100: "1xx", 200: "2xx", 301: "3xx", 404: "4xx", 500: "5xx", 0: "other"}
	for status, want := range cases {
		if got := (Entry{Status: status}).Class(); got != want {
			t.Errorf("status %d is %q, want %q", status, got, want)
		}
	}
}

func TestSlowestSpansTheWholeWindowNotJustTheRows(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	// The row buffer holds the newest few; the slowest request of the day is
	// almost never among them, which is exactly when it is worth reporting.
	c := NewCollector(Filter{Limit: 3})
	c.Feed(entry(base, "GET", "/very-slow", 200, 9000))
	for i := 1; i < 200; i++ {
		c.Feed(entry(base.Add(time.Duration(i)*time.Second), "GET", "/fast", 200, 5))
	}
	result := c.Result()
	if len(result.Slowest) == 0 || result.Slowest[0].Path != "/very-slow" {
		t.Fatalf("slowest = %+v, want the 9s request from the start of the window", result.Slowest)
	}
	if len(result.Slowest) > 5 {
		t.Fatalf("kept %d slow rows, want at most 5", len(result.Slowest))
	}
	// Ordered slowest first, so the list reads as a ranking.
	for i := 1; i < len(result.Slowest); i++ {
		if result.Slowest[i-1].duration < result.Slowest[i].duration {
			t.Fatalf("slowest list is not ordered: %+v", result.Slowest)
		}
	}
}

func TestIsPageSeparatesViewsFromWhatTheyDragIn(t *testing.T) {
	page := func(method, path, query string) bool {
		return IsPage(Entry{Method: method, Path: path, Query: query, Status: 200})
	}
	if !page("GET", "/ro/contact", "") || !page("POST", "/api/checkout", "") || !page("GET", "/", "") {
		t.Fatal("a document and an API call are what a person means by a request")
	}
	for _, drag := range [][3]string{
		{"GET", "/ro/contact", "_rsc=abc"},
		{"GET", "/_next/static/chunks/main.js", ""},
		{"GET", "/apple-icon.png", ""},
		{"GET", "/manifest.webmanifest", ""},
		{"GET", "/assets/app.css", ""},
		{"HEAD", "/", ""},
		{"OPTIONS", "/api/x", ""},
	} {
		if page(drag[0], drag[1], drag[2]) {
			t.Fatalf("%v is what a page load drags in, not a page view", drag)
		}
	}
	// A path with a dot in a directory name is still a page.
	if !page("GET", "/v1.2/docs", "") {
		t.Fatal("a dot in a directory is not an extension")
	}
}

func TestIsProbeNamesScannersNotTypos(t *testing.T) {
	probe := func(path string, status int) bool { return IsProbe(Entry{Path: path, Status: status}) }
	for _, p := range []string{"/.env", "/wp-login.php", "/.git/config", "/phpmyadmin/index.php", "/vendor/phpunit/x", "/backup.sql", "/admin.php"} {
		if !probe(p, 404) {
			t.Fatalf("%s refused is a probe", p)
		}
	}
	// The same paths served are the site's own business.
	if probe("/wp-login.php", 200) {
		t.Fatal("a WordPress site serving its own login is not being scanned")
	}
	// A refused ordinary page is a dead link, not a scanner.
	if probe("/ro/old-page", 404) {
		t.Fatal("a 404 on an ordinary path is a typo or a stale link")
	}
}

func TestCollectorCountsPagesAndNamesScanners(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{Limit: 10})
	visitor := func(path, query string, status int) {
		e := entry(base, "GET", path, status, 5)
		e.Query, e.RemoteIP, e.Host = query, "198.51.100.7", "site.test"
		e.Referer = "https://www.google.com/search?q=site"
		c.Feed(e)
	}
	visitor("/ro/contact", "", 200)
	visitor("/ro/contact", "_rsc=x", 200)
	visitor("/_next/static/a.js", "", 200)
	for _, p := range []string{"/.env", "/wp-login.php", "/xmlrpc.php", "/.git/HEAD"} {
		e := entry(base, "GET", p, 404, 1)
		e.RemoteIP, e.Host = "203.0.113.9", "site.test"
		c.Feed(e)
	}
	result := c.Result()
	// One document view: the prefetch and the script are what it dragged in,
	// and the four refused probes are a scanner, not visits.
	if result.Summary.Pages != 1 {
		t.Fatalf("pages = %d, want the one document view", result.Summary.Pages)
	}
	if len(result.Summary.Scanners) != 1 || result.Summary.Scanners[0].Value != "203.0.113.9" || result.Summary.Scanners[0].Probes != 4 {
		t.Fatalf("scanners = %+v", result.Summary.Scanners)
	}
	if len(result.Summary.Probes) != 4 {
		t.Fatalf("probes = %+v", result.Summary.Probes)
	}
	// The visitor is not a scanner, however many things their page load asked for.
	for _, client := range result.Summary.Clients {
		if client.Value == "198.51.100.7" && client.Probes != 0 {
			t.Fatalf("a visitor was counted as probing: %+v", client)
		}
	}
	if len(result.Summary.Referers) != 1 || result.Summary.Referers[0].Value != "www.google.com" || result.Summary.Referers[0].Count != 3 {
		t.Fatalf("referers = %+v", result.Summary.Referers)
	}
}

func TestCollectorReferersDropTheSitesOwnLinks(t *testing.T) {
	c := NewCollector(Filter{Limit: 10})
	e := entry(time.Now(), "GET", "/b", 200, 1)
	e.Host, e.Referer = "site.test", "https://site.test/a"
	c.Feed(e)
	if len(c.Result().Summary.Referers) != 0 {
		t.Fatal("a link followed within the site is navigation, not a source")
	}
}

func TestPathsCarryTheirOwnP95(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{Limit: 10})
	for i := 0; i < 20; i++ {
		c.Feed(entry(base, "GET", "/fast", 200, 5))
		c.Feed(entry(base, "GET", "/slow", 200, 900))
	}
	paths := c.Result().Summary.Paths
	byValue := map[string]Facet{}
	for _, f := range paths {
		byValue[f.Value] = f
	}
	if byValue["/slow"].P95 == nil || *byValue["/slow"].P95 != 900 || byValue["/fast"].P95 == nil || *byValue["/fast"].P95 != 5 {
		t.Fatalf("per-path p95 wrong: %+v", paths)
	}
}

func TestPagesOnlyFilterKeepsTheReadingsHonest(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	c := NewCollector(Filter{PagesOnly: true, Limit: 10})
	c.Feed(entry(base, "GET", "/ro", 200, 5))
	asset := entry(base, "GET", "/_next/static/x.js", 200, 1)
	c.Feed(asset)
	result := c.Result()
	if result.Summary.Total != 1 || result.Summary.Scanned != 2 || len(result.Entries) != 1 {
		t.Fatalf("pages-only: total %d scanned %d rows %d", result.Summary.Total, result.Summary.Scanned, len(result.Entries))
	}
}
