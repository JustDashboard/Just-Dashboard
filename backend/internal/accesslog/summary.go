package accesslog

import (
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Filter is one question asked of the request record. It is the same struct
// for the table, the readings and the chart, so narrowing to "5xx on /api in
// the last hour" cannot mean three different things in three places — which is
// how the old log page's filter came to apply to the tail and not the search.
type Filter struct {
	Since   time.Time
	Until   time.Time
	Methods []string
	Classes []string
	Status  []int
	Path    string
	Host    string
	Client  string
	MinMs   float64
	MaxMs   float64
	// PagesOnly keeps the requests a person would call a page view and drops
	// the ones a page load brings with it: framework prefetches, scripts,
	// styles, icons, manifests. A modern front end asks for six things per
	// click, and a log that shows all six is a log nobody can read for what
	// people actually opened.
	PagesOnly bool
	// Limit bounds the rows returned, newest first. The readings above the
	// table are still computed over everything in the window: a page showing
	// the last 200 requests must not report a p95 of only those 200.
	Limit int
}

// assetExtensions are what a page load fetches alongside the document.
var assetExtensions = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".map": true, ".json": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".ico": true,
	".webp": true, ".avif": true, ".woff": true, ".woff2": true, ".ttf": true, ".otf": true,
	".webmanifest": true, ".txt": true, ".xml": true, ".mp4": true, ".webm": true, ".pdf": true,
}

// IsPage reports whether a request is a page view rather than something a
// page view dragged in. Next.js prefetches carry `_rsc=`; every framework's
// build output lives under a handful of prefixes; the rest is decided by the
// extension. HEAD and OPTIONS are never a person.
func IsPage(e Entry) bool {
	switch e.Method {
	case "HEAD", "OPTIONS":
		return false
	}
	if strings.Contains(e.Query, "_rsc=") || strings.Contains(e.Query, "__nextDataReq") {
		return false
	}
	for _, prefix := range []string{"/_next/", "/_nuxt/", "/static/", "/assets/", "/_astro/", "/build/", "/__vite", "/.well-known/"} {
		if strings.HasPrefix(e.Path, prefix) {
			return false
		}
	}
	if i := strings.LastIndexByte(e.Path, '.'); i >= 0 && i > strings.LastIndexByte(e.Path, '/') {
		if assetExtensions[strings.ToLower(e.Path[i:])] {
			return false
		}
	}
	return true
}

// probePaths are what scanners ask every host for. A request for one of these
// that the site refused is not a visitor with a typo; it is somebody trying
// doors, and the client that tries several is worth naming.
var probePaths = []string{
	"/.env", "/.git", "/.aws", "/.ssh", "/.docker", "/.vscode",
	"/wp-login.php", "/wp-admin", "/wp-content", "/wp-includes", "/xmlrpc.php", "/wp-config",
	"/phpmyadmin", "/pma", "/adminer", "/mysql", "/phpinfo",
	"/cgi-bin", "/vendor/phpunit", "/actuator", "/console", "/boaform", "/hnap1",
	"/config.json", "/config.yml", "/config.php", "/backup", "/dump.sql", "/database.sql",
	"/etc/passwd", "/server-status", "/telescope", "/_ignition", "/debug",
}

// IsProbe reports whether a refused request looks like a scanner's probe.
func IsProbe(e Entry) bool {
	if e.Status < 400 {
		return false
	}
	lower := strings.ToLower(e.Path)
	for _, probe := range probePaths {
		if strings.HasPrefix(lower, probe) {
			return true
		}
	}
	// A refused request for a PHP or ASP page on a site that serves neither
	// is the commonest probe of all.
	for _, ext := range []string{".php", ".asp", ".aspx", ".jsp", ".cgi", ".sh", ".bak", ".sql"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// Match reports whether one request answers the question.
func (f Filter) Match(e Entry) bool {
	if !f.Since.IsZero() && e.at < f.Since.UnixNano() {
		return false
	}
	if !f.Until.IsZero() && e.at > f.Until.UnixNano() {
		return false
	}
	if len(f.Methods) > 0 && !containsFold(f.Methods, e.Method) {
		return false
	}
	if len(f.Classes) > 0 && !containsFold(f.Classes, e.Class()) {
		return false
	}
	if len(f.Status) > 0 {
		found := false
		for _, want := range f.Status {
			if want == e.Status {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.Path != "" && !strings.Contains(strings.ToLower(e.Path), strings.ToLower(f.Path)) {
		return false
	}
	if f.Host != "" && !strings.EqualFold(f.Host, e.Host) {
		return false
	}
	if f.Client != "" && !strings.HasPrefix(e.RemoteIP, f.Client) {
		return false
	}
	// "Pages" is what people opened: a document, and not a scanner's refused
	// probe for one — a script asking for /wp-login.php on a site that has
	// none is not a visit.
	if f.PagesOnly && (!IsPage(e) || IsProbe(e)) {
		return false
	}
	if f.MinMs > 0 || f.MaxMs > 0 {
		if !e.timed {
			return false
		}
		if f.MinMs > 0 && e.duration < f.MinMs {
			return false
		}
		if f.MaxMs > 0 && e.duration > f.MaxMs {
			return false
		}
	}
	return true
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

// Facet is one value of a dimension with its share of the window. Errors rides
// along with the count because "the busiest path" and "the path that is
// failing" are the same table read two ways, and splitting them into two
// panels made the reader compare rows across a gap.
type Facet struct {
	Value  string   `json:"value"`
	Count  int      `json:"count"`
	Errors int      `json:"errors"`
	Bytes  int64    `json:"bytes,omitempty"`
	P95    *float64 `json:"p95,omitempty"`
	// Refused is how many were answered 4xx, and Probes how many of those
	// looked like a scanner's. On a client both together are the difference
	// between a visitor who followed a dead link and a script trying doors.
	Refused int `json:"refused,omitempty"`
	Probes  int `json:"probes,omitempty"`
}

// Bucket is one column of the request chart, counted by status family so a
// wall of red is visible without reading a single row.
type Bucket struct {
	Start  string         `json:"start"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
	P95    *float64       `json:"p95,omitempty"`
}

// Latency is the distribution, not an average. A mean response time is the one
// number that hides the problem: a p50 of 30ms with a p99 of 9s is a service
// that feels broken to one user in a hundred and looks perfect in the mean.
type Latency struct {
	P50  float64 `json:"p50"`
	P75  float64 `json:"p75"`
	P90  float64 `json:"p90"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
}

// Summary is everything the page draws above and beside the rows.
type Summary struct {
	Total   int            `json:"total"`
	Scanned int            `json:"scanned"`
	Classes map[string]int `json:"classes"`
	// ErrorRate and ClientErrorRate are shares of the window, 0–1. Two
	// separate readings because they are two different people's problem: 5xx
	// is the deployment's, 4xx is usually the caller's.
	ErrorRate       float64  `json:"errorRate"`
	ClientErrorRate float64  `json:"clientErrorRate"`
	Bytes           int64    `json:"bytes"`
	Latency         *Latency `json:"latency,omitempty"`
	// PerMinute is the arrival rate across the window rather than a count, so
	// the figure means the same thing whichever range is chosen.
	PerMinute float64 `json:"perMinute"`

	// Pages is how many of the requests were page views rather than what a
	// page view dragged in — the number a person means by "visits".
	Pages int `json:"pages"`

	Methods  []Facet `json:"methods"`
	Statuses []Facet `json:"statuses"`
	Paths    []Facet `json:"paths"`
	Hosts    []Facet `json:"hosts"`
	Clients  []Facet `json:"clients"`
	Agents   []Facet `json:"agents"`
	// Referers are the sites traffic arrived from, by host; a page's own
	// links to itself are not a source.
	Referers []Facet `json:"referers"`
	// Probes are the refused paths that look like scanning, and Scanners the
	// clients that asked for several of them.
	Probes   []Facet `json:"probes"`
	Scanners []Facet `json:"scanners"`

	Buckets       []Bucket `json:"buckets"`
	BucketSeconds int      `json:"bucketSeconds"`

	First *string `json:"first,omitempty"`
	Last  *string `json:"last,omitempty"`
	// Truncated reports that the scan hit its own bound rather than the end of
	// the record, so a figure drawn from it can say it is a floor.
	Truncated bool `json:"truncated"`
}

// Result is one answer: the rows to render and the readings over the whole
// window they were taken from.
type Result struct {
	Entries []Entry `json:"entries"`
	Summary Summary `json:"summary"`
	Format  Format  `json:"format,omitempty"`
	Slowest []Entry `json:"slowest,omitempty"`
	Recent  []Entry `json:"-"`
}

// buckets is the column count of the request chart, matching the log
// histogram beside it so the two read as one instrument.
const buckets = 60

// latencyCap bounds what the percentile estimate remembers. Every sample is
// eight bytes, and an unbounded window over a busy month would otherwise be
// held in memory to answer one number.
const latencyCap = 250_000

// facetCap bounds the distinct values of a dimension. A path with a request id
// in it produces a new key per request; without a bound, the top-paths table is
// a memory leak that happens to render.
const facetCap = 20_000

// Collector folds a stream of requests into one answer in a single pass. It is
// fed rather than handed a slice because the caller is reading a file it must
// not hold in memory twice.
type Collector struct {
	filter  Filter
	entries []Entry
	total   int
	scanned int

	classes  map[string]int
	methods  map[string]*Facet
	statuses map[int]*Facet
	paths    map[string]*Facet
	hosts    map[string]*Facet
	clients  map[string]*Facet
	agents   map[string]*Facet
	referers map[string]*Facet
	probes   map[string]*Facet
	// pathLatencies is a bounded sample per path, so the top-paths table can
	// say which route is slow rather than only that one is. Bounded twice:
	// per path, and in how many paths are sampled at all, because a path
	// with an id in it is a new key per request.
	pathLatencies map[string][]float64
	pages         int

	latencies []float64
	sum       float64
	max       float64
	bytes     int64
	// slowRows is the running top-N by duration, ordered slowest first.
	slowRows []Entry

	// first and last are unix nanoseconds; zero means nothing seen yet.
	first, last int64
	byMinute    map[int64]*minuteBucket
	truncated   bool
	format      Format
}

type minuteBucket struct {
	counts    map[string]int
	total     int
	latencies []float64
}

func NewCollector(f Filter) *Collector {
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 500
	}
	return &Collector{
		filter: f, classes: map[string]int{},
		methods: map[string]*Facet{}, statuses: map[int]*Facet{},
		paths: map[string]*Facet{}, hosts: map[string]*Facet{},
		clients: map[string]*Facet{}, agents: map[string]*Facet{},
		referers: map[string]*Facet{}, probes: map[string]*Facet{},
		pathLatencies: map[string][]float64{},
		byMinute:      map[int64]*minuteBucket{},
	}
}

// Format records which spelling the file turned out to be in, so the page can
// explain an absent latency column instead of drawing an empty one.
func (c *Collector) Format(f Format) { c.format = f }

// Truncated marks that the scan stopped early.
func (c *Collector) Truncated() { c.truncated = true }

func (c *Collector) Feed(e Entry) {
	c.scanned++
	if !c.filter.Match(e) {
		return
	}
	c.total++
	class := e.Class()
	c.classes[class]++
	c.bytes += e.Size

	if c.first == 0 || e.at < c.first {
		c.first = e.at
	}
	if e.at > c.last {
		c.last = e.at
	}

	failed := e.Status >= 500
	refused := e.Status >= 400 && e.Status < 500
	probe := IsProbe(e)
	if IsPage(e) && !probe {
		c.pages++
	}
	c.bump(c.methods, e.Method, e, failed, refused, probe)
	c.bump(c.paths, e.Path, e, failed, refused, probe)
	c.bump(c.hosts, e.Host, e, failed, refused, probe)
	c.bump(c.clients, e.RemoteIP, e, failed, refused, probe)
	c.bump(c.agents, agentFamily(e.UserAgent), e, failed, refused, probe)
	c.bump(c.referers, refererHost(e.Referer, e.Host), e, failed, refused, probe)
	if probe {
		c.bump(c.probes, e.Path, e, failed, refused, probe)
	}
	if e.timed {
		if samples, ok := c.pathLatencies[e.Path]; ok || len(c.pathLatencies) < pathSampleKeys {
			if len(samples) < pathSamplesEach {
				c.pathLatencies[e.Path] = append(samples, e.duration)
			}
		}
	}
	if facet, ok := c.statuses[e.Status]; ok {
		facet.Count++
		if failed {
			facet.Errors++
		}
	} else if len(c.statuses) < facetCap {
		c.statuses[e.Status] = &Facet{Value: strconv.Itoa(e.Status), Count: 1, Errors: boolInt(failed)}
	}

	if e.timed {
		c.sum += e.duration
		if e.duration > c.max {
			c.max = e.duration
		}
		c.keepSlowest(e)
		if len(c.latencies) < latencyCap {
			c.latencies = append(c.latencies, e.duration)
		} else {
			c.truncated = true
		}
	}

	minute := e.at / int64(time.Minute)
	bucket := c.byMinute[minute]
	if bucket == nil {
		bucket = &minuteBucket{counts: map[string]int{}}
		c.byMinute[minute] = bucket
	}
	bucket.total++
	bucket.counts[class]++
	if e.timed && len(bucket.latencies) < 4096 {
		bucket.latencies = append(bucket.latencies, e.duration)
	}

	// The rows are the newest N, so the window slides rather than filling up
	// and then ignoring everything after it — a table that stopped at the
	// oldest 500 requests of a day is the opposite of what a log is for.
	c.entries = append(c.entries, e)
	if len(c.entries) > c.filter.Limit*2 {
		c.entries = append(c.entries[:0], c.entries[len(c.entries)-c.filter.Limit:]...)
	}
}

func (c *Collector) bump(into map[string]*Facet, key string, e Entry, failed, refused, probe bool) {
	if key == "" {
		return
	}
	facet, ok := into[key]
	if !ok {
		if len(into) >= facetCap {
			c.truncated = true
			return
		}
		facet = &Facet{Value: key}
		into[key] = facet
	}
	facet.Count++
	facet.Bytes += e.Size
	facet.Errors += boolInt(failed)
	facet.Refused += boolInt(refused)
	facet.Probes += boolInt(probe)
}

const (
	pathSampleKeys  = 2000
	pathSamplesEach = 128
)

// refererHost reduces a referer to the site it names, and drops the site's
// own — a link followed within the site is navigation, not a source.
func refererHost(referer, own string) string {
	if referer == "" || referer == "-" {
		return ""
	}
	parsed, err := url.Parse(referer)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if own != "" && strings.EqualFold(host, hostOnly(own)) {
		return ""
	}
	return host
}

func hostOnly(host string) string {
	if i := strings.LastIndexByte(host, ':'); i > 0 && !strings.Contains(host, "]") {
		return host[:i]
	}
	return host
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// agentFamily reduces a user agent to something a column can hold. The full
// string is kept on the row; grouping by it would give a table where every
// Chrome point release is its own client.
func agentFamily(agent string) string {
	if agent == "" {
		return ""
	}
	switch {
	case strings.Contains(agent, "Googlebot"):
		return "Googlebot"
	case strings.Contains(agent, "bingbot"):
		return "bingbot"
	case strings.Contains(agent, "Edg/"):
		return "Edge"
	case strings.Contains(agent, "Chrome/") && !strings.Contains(agent, "Chromium"):
		return "Chrome"
	case strings.Contains(agent, "Firefox/"):
		return "Firefox"
	case strings.Contains(agent, "Safari/"):
		return "Safari"
	case strings.HasPrefix(agent, "curl/"):
		return "curl"
	case strings.Contains(agent, "bot") || strings.Contains(agent, "Bot") || strings.Contains(agent, "spider"):
		return "Other bots"
	}
	if i := strings.IndexAny(agent, "/ "); i > 0 {
		return agent[:i]
	}
	return agent
}

func (c *Collector) Result() *Result {
	out := &Result{Summary: Summary{
		Total: c.total, Scanned: c.scanned, Classes: c.classes,
		Bytes: c.bytes, Truncated: c.truncated, BucketSeconds: 60,
	}, Format: c.format}

	entries := c.entries
	if len(entries) > c.filter.Limit {
		entries = entries[len(entries)-c.filter.Limit:]
	}
	// Newest first: a request log is read from the top, and the row the reader
	// wants is almost always the most recent one.
	out.Entries = make([]Entry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		out.Entries = append(out.Entries, entries[i])
	}

	if c.total > 0 {
		out.Summary.ErrorRate = float64(c.classes["5xx"]) / float64(c.total)
		out.Summary.ClientErrorRate = float64(c.classes["4xx"]) / float64(c.total)
	}
	if c.first != 0 {
		first := time.Unix(0, c.first).UTC().Format(time.RFC3339Nano)
		last := time.Unix(0, c.last).UTC().Format(time.RFC3339Nano)
		out.Summary.First, out.Summary.Last = &first, &last
		if minutes := time.Duration(c.last - c.first).Minutes(); minutes >= 1 {
			out.Summary.PerMinute = float64(c.total) / minutes
		} else if c.total > 0 {
			out.Summary.PerMinute = float64(c.total)
		}
	}
	if len(c.latencies) > 0 {
		sort.Float64s(c.latencies)
		out.Summary.Latency = &Latency{
			P50: percentile(c.latencies, 0.50), P75: percentile(c.latencies, 0.75),
			P90: percentile(c.latencies, 0.90), P95: percentile(c.latencies, 0.95),
			P99: percentile(c.latencies, 0.99), Max: c.max,
			Mean: c.sum / float64(len(c.latencies)),
		}
	}

	out.Summary.Pages = c.pages
	out.Summary.Methods = rank(c.methods, 8)
	out.Summary.Paths = rank(c.paths, 12)
	for i := range out.Summary.Paths {
		if samples := c.pathLatencies[out.Summary.Paths[i].Value]; len(samples) > 0 {
			sorted := append([]float64(nil), samples...)
			sort.Float64s(sorted)
			p95 := percentile(sorted, 0.95)
			out.Summary.Paths[i].P95 = &p95
		}
	}
	out.Summary.Hosts = rank(c.hosts, 8)
	out.Summary.Clients = rank(c.clients, 10)
	out.Summary.Agents = rank(c.agents, 8)
	out.Summary.Referers = rank(c.referers, 10)
	out.Summary.Probes = rank(c.probes, 10)
	out.Summary.Scanners = rankScanners(c.clients)
	out.Summary.Statuses = rankStatuses(c.statuses)
	out.Summary.Buckets = c.histogram()
	out.Slowest = c.slowest()
	return out
}

// slowest is the tail of the distribution as rows rather than a number. A p99
// of nine seconds says something is slow; this says which route it was.
//
// Kept as a running top-N during the scan rather than sorted out of the row
// buffer at the end: the buffer holds only the newest `Limit` requests, so
// reading it would have answered "the slowest of the last 500" — and the
// slowest request of the day is almost never in the last 500.
func (c *Collector) slowest() []Entry {
	out := make([]Entry, len(c.slowRows))
	copy(out, c.slowRows)
	return out
}

const slowestKept = 5

// keepSlowest inserts into an already-ordered short list. At five entries an
// insertion is cheaper than a heap and reads as what it is.
func (c *Collector) keepSlowest(e Entry) {
	if !e.timed {
		return
	}
	at := sort.Search(len(c.slowRows), func(i int) bool {
		return c.slowRows[i].duration < e.duration
	})
	if at >= slowestKept {
		return
	}
	if len(c.slowRows) < slowestKept {
		c.slowRows = append(c.slowRows, Entry{})
	}
	copy(c.slowRows[at+1:], c.slowRows[at:])
	c.slowRows[at] = e
}

// histogram lays the per-minute counts onto a fixed column count over the
// window, widening the column rather than adding columns so a day and an hour
// draw the same instrument at different resolutions.
func (c *Collector) histogram() []Bucket {
	if len(c.byMinute) == 0 {
		return []Bucket{}
	}
	from := time.Unix(0, c.first).UTC().Truncate(time.Minute)
	to := time.Unix(0, c.last).UTC().Truncate(time.Minute)
	if !c.filter.Since.IsZero() {
		from = c.filter.Since.Truncate(time.Minute)
	}
	if !c.filter.Until.IsZero() && c.filter.Until.After(from) {
		to = c.filter.Until.Truncate(time.Minute)
	}
	if to.Before(from) {
		from, to = to, from
	}
	span := to.Sub(from)
	if span < time.Minute {
		span = time.Minute
	}
	// A whole number of minutes wide, so a column boundary is a clock instant
	// the reader can go and find in the rows below it.
	width := time.Duration(math.Ceil(span.Minutes()/buckets)) * time.Minute
	if width < time.Minute {
		width = time.Minute
	}
	count := int(span/width) + 1
	if count > buckets {
		count = buckets
	}
	out := make([]Bucket, count)
	// Samples live beside the columns for the length of this pass only: a
	// bucket travels to the browser as its p95, and shipping the raw samples
	// would be a megabyte per chart.
	samples := make([][]float64, count)
	for i := range out {
		out[i] = Bucket{Start: from.Add(time.Duration(i) * width).Format(time.RFC3339), Counts: map[string]int{}}
	}
	for minute, bucket := range c.byMinute {
		at := time.Unix(minute*60, 0).UTC()
		offset := int(at.Sub(from) / width)
		if offset < 0 || offset >= count {
			continue
		}
		out[offset].Total += bucket.total
		for class, n := range bucket.counts {
			out[offset].Counts[class] += n
		}
		samples[offset] = append(samples[offset], bucket.latencies...)
	}
	for i := range out {
		if len(samples[i]) == 0 {
			continue
		}
		sort.Float64s(samples[i])
		p95 := percentile(samples[i], 0.95)
		out[i].P95 = &p95
	}
	return out
}

// percentile reads the nearest-rank value of an already-sorted sample. Nearest
// rank rather than interpolation because a latency percentile that reports a
// duration no request actually took is a number an operator cannot go and find
// in the rows.
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func rank(from map[string]*Facet, limit int) []Facet {
	out := make([]Facet, 0, len(from))
	for _, facet := range from {
		out = append(out, *facet)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// rankScanners names the clients that tried several doors. Three probes is
// the bar: one is a typo and two a curious person; three is a list.
func rankScanners(clients map[string]*Facet) []Facet {
	out := make([]Facet, 0)
	for _, facet := range clients {
		if facet.Probes >= 3 {
			out = append(out, *facet)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Probes != out[j].Probes {
			return out[i].Probes > out[j].Probes
		}
		return out[i].Value < out[j].Value
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func rankStatuses(from map[int]*Facet) []Facet {
	out := make([]Facet, 0, len(from))
	for _, facet := range from {
		out = append(out, *facet)
	}
	// By code rather than by count: a status column is read as a scale, and
	// sorting it by popularity puts 500 above 200 on a bad afternoon and below
	// it on a good one.
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}
