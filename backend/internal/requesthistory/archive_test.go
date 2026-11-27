package requesthistory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// clock is a time the tests move.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func openTest(t *testing.T) (*Archive, *clock) {
	t.Helper()
	a, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	c := &clock{now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	a.now = c.Now
	return a, c
}

// caddyLine is a request as the Caddy ingress writes it, with every field
// the record reads.
func caddyLine(at time.Time, path string, status int, agent string) string {
	return fmt.Sprintf(`{"ts":%q,"logger":"http.log.access","msg":"handled request",`+
		`"request":{"remote_ip":"10.0.0.1","client_ip":"203.0.113.7","proto":"HTTP/2.0","method":"POST","host":"shop.test",`+
		`"uri":%q,"headers":{"User-Agent":[%q],"Referer":["https://news.test/a"]},"tls":{"version":772}},`+
		`"duration":0.012345678,"size":5120,"status":%d,"release":"canary","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736",`+
		`"span_id":"00f067aa0ba902b7","parent_span_id":"b7ad6b7169203331","tailnet_login":"ada@example.com","edge_rule":"block-bots"}`,
		at.Format(time.RFC3339Nano), path, agent, status)
}

func parse(t *testing.T, line string) accesslog.Entry {
	t.Helper()
	e, ok := accesslog.Parse(line)
	if !ok {
		t.Fatalf("the test's line did not parse: %s", line)
	}
	return e
}

func scanAll(t *testing.T, a *Archive, route string) []accesslog.Entry {
	t.Helper()
	var out []accesslog.Entry
	if err := a.Scan(context.Background(), route, 0, math.MaxInt64, func(e accesslog.Entry) error {
		out = append(out, e)
		return nil
	}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return out
}

func TestAKeptRequestReadsBackAsTheRequestThatWasWritten(t *testing.T) {
	a, c := openTest(t)
	at := c.Now().Add(-time.Minute).Add(123456789 * time.Nanosecond)
	want := []accesslog.Entry{
		parse(t, caddyLine(at, "/checkout/a b?coupon=x&_rsc=1", 201, "Mozilla/5.0 (X11; Linux x86_64) Firefox/127.0")),
		// nginx's stock format: no duration, no TLS, no trace.
		parse(t, `198.51.100.4 - - [20/Nov/2026:11:59:30 +0000] "GET /index.html HTTP/1.1" 404 0 "-" "curl/8.5.0"`),
		// The timed nginx format, with what its log_format appends.
		parse(t, `198.51.100.5 - - [20/Nov/2026:11:59:31 +0000] "GET /x HTTP/1.1" 502 17 "-" "-" rt=0.250 host=a.test rel=blue er=crowdsec`),
	}
	if err := a.Append(context.Background(), "r", want, Cursor{Generation: "g", Offset: 10}); err != nil {
		t.Fatal(err)
	}
	got := scanAll(t, a, "r")
	if len(got) != len(want) {
		t.Fatalf("read back %d requests, want %d", len(got), len(want))
	}
	// Kept in time order, which is not the order they were appended in.
	byTime := map[int64]accesslog.Entry{}
	for _, e := range want {
		byTime[e.UnixNano()] = e
	}
	for _, e := range got {
		w := byTime[e.UnixNano()]
		if !reflect.DeepEqual(e, w) {
			t.Fatalf("read back\n %#v\nwant\n %#v", e, w)
		}
		gotJSON, _ := json.Marshal(e)
		wantJSON, _ := json.Marshal(w)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("wire shape differs:\n %s\n %s", gotJSON, wantJSON)
		}
	}
	caddy := got[0]
	if ms, timed := caddy.Duration(); !timed || math.Abs(ms-12.345678) > 1e-9 || caddy.TraceID == "" || caddy.ParentSpanID == "" || !caddy.TLS {
		t.Fatalf("the Caddy request lost its timing, TLS or trace: %+v", caddy)
	}
}

func TestTheCursorIsKeptWithTheRowsItCovers(t *testing.T) {
	a, c := openTest(t)
	ctx := context.Background()
	if _, ok, err := a.Cursor(ctx, "r"); ok || err != nil {
		t.Fatalf("a route never recorded has a cursor (%v)", err)
	}
	e := parse(t, caddyLine(c.Now(), "/", 200, "x"))
	if err := a.Append(ctx, "r", []accesslog.Entry{e}, Cursor{Generation: "ino-3", Offset: 512, LostAt: 7}); err != nil {
		t.Fatal(err)
	}
	cur, ok, err := a.Cursor(ctx, "r")
	if err != nil || !ok || cur.Generation != "ino-3" || cur.Offset != 512 || cur.Newest != e.UnixNano() || cur.LostAt != 7 {
		t.Fatalf("cursor = %+v (%v, %v)", cur, ok, err)
	}
	// A loss is only ever moved later.
	if err := a.Append(ctx, "r", nil, Cursor{Generation: "ino-3", Offset: 600, Newest: cur.Newest, LostAt: 3}); err != nil {
		t.Fatal(err)
	}
	if cur, _, _ := a.Cursor(ctx, "r"); cur.LostAt != 7 || cur.Offset != 600 {
		t.Fatalf("cursor = %+v, want the loss kept at 7 and the offset moved", cur)
	}
	span, err := a.Span(ctx, "r")
	if err != nil || !span.From.Equal(e.At()) || !span.To.Equal(e.At()) || span.LostAt.UnixNano() != 7 {
		t.Fatalf("span = %+v (%v)", span, err)
	}
	a.follow([]string{"r"})
	if span, _ := a.Span(ctx, "r"); !span.Recording {
		t.Fatal("a followed route is not reported as recording")
	}
}

func TestPruneDropsWhatTheRetentionNoLongerKeepsAndItsStrings(t *testing.T) {
	a, c := openTest(t)
	ctx := context.Background()
	old := []accesslog.Entry{
		parse(t, caddyLine(c.Now().Add(-time.Hour), "/only-old", 200, "OldBrowser/1")),
		parse(t, caddyLine(c.Now().Add(-time.Hour), "/shared", 200, "OldBrowser/1")),
	}
	if err := a.Append(ctx, "r", old, Cursor{}); err != nil {
		t.Fatal(err)
	}
	c.Advance(Retention + 24*time.Hour)
	fresh := parse(t, caddyLine(c.Now().Add(-time.Minute), "/shared", 200, "NewBrowser/2"))
	if err := a.Append(ctx, "r", []accesslog.Entry{fresh}, Cursor{}); err != nil {
		t.Fatal(err)
	}
	if err := a.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	got := scanAll(t, a, "r")
	if len(got) != 1 || !reflect.DeepEqual(got[0], fresh) {
		t.Fatalf("after the prune the history holds %+v, want only the fresh request", got)
	}
	var strings int
	if err := a.db.Reader.QueryRow(`SELECT count(*) FROM strings WHERE value IN ('/only-old', 'OldBrowser/1')`).Scan(&strings); err != nil {
		t.Fatal(err)
	}
	if strings != 0 {
		t.Fatalf("%d strings only the pruned rows named are still kept", strings)
	}
	span, _ := a.Span(ctx, "r")
	if span.LostAt.IsZero() || span.LostAt.UnixNano() < old[0].UnixNano() {
		t.Fatalf("lost_at = %v, want at least the pruned requests' instant", span.LostAt)
	}
	var kept int64
	if err := a.db.Reader.QueryRow(`SELECT kept FROM routes WHERE name = 'r'`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("kept = %d (%v), want 1", kept, err)
	}
}

func TestPruneHoldsEachRouteAndAllOfThemToTheirCaps(t *testing.T) {
	a, c := openTest(t)
	ctx := context.Background()
	add := func(route string, n int) {
		batch := make([]accesslog.Entry, n)
		for i := range batch {
			batch[i] = parse(t, caddyLine(c.Now().Add(-time.Hour).Add(time.Duration(i)*time.Second), fmt.Sprintf("/%s/%d", route, i), 200, "x"))
		}
		if err := a.Append(ctx, route, batch, Cursor{}); err != nil {
			t.Fatal(err)
		}
	}
	add("busy", 200)
	add("quiet", 80)
	if err := a.prune(ctx, caps{route: 100, total: 150}); err != nil {
		t.Fatal(err)
	}
	busy, quiet := scanAll(t, a, "busy"), scanAll(t, a, "quiet")
	// busy: over its cap by 100, cut by that and a tenth of the cap more to 90;
	// then the two together are 170, over the total by 20, and the busiest
	// gives up that and a twentieth of the total more: 63.
	if len(busy) != 63 || len(quiet) != 80 {
		t.Fatalf("busy keeps %d and quiet %d, want 63 and 80", len(busy), len(quiet))
	}
	if busy[len(busy)-1].Path != "/busy/199" || busy[0].Path != "/busy/137" {
		t.Fatalf("busy kept %s..%s, want its newest", busy[0].Path, busy[len(busy)-1].Path)
	}
	span, _ := a.Span(ctx, "busy")
	if span.LostAt.IsZero() || !span.LostAt.Before(span.From) {
		t.Fatalf("busy's lost_at = %v, want just before its oldest kept %v", span.LostAt, span.From)
	}
	if span, _ := a.Span(ctx, "quiet"); !span.LostAt.IsZero() {
		t.Fatal("the quiet route lost nothing and says it did")
	}
}

// What a kept request costs on disk, measured on a mix like a public site's:
// a few hundred paths, thousands of clients, a query string on one request in
// ten that is never seen twice, and the ingress's trace context on every one.
// The caps in archive.go are chosen from this figure.
func TestAKeptRequestIsAboutEightyBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 60,000 requests")
	}
	a, c := openTest(t)
	ctx := context.Background()
	const n = 60_000
	agents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
	}
	start := c.Now().Add(-24 * time.Hour)
	// Small batches, the two routes taking turns, as a recorder's passes
	// write them: an append in the middle of the table splits pages.
	const each = 25
	for batch := 0; batch < n/(2*each); batch++ {
		for r, route := range []string{"a", "b"} {
			entries := make([]accesslog.Entry, 0, each)
			for i := 0; i < each; i++ {
				k := (batch*2+r)*each + i
				uri := fmt.Sprintf("/p/%d", k%400)
				if k%10 == 0 {
					uri += fmt.Sprintf("?t=%d", k)
				}
				line := fmt.Sprintf(`{"ts":%q,"msg":"handled request","request":{"client_ip":"198.51.%d.%d","proto":"HTTP/2.0",`+
					`"method":"GET","host":"shop.test","uri":%q,"headers":{"User-Agent":[%q]},"tls":{}},"duration":0.0%04d,`+
					`"size":%d,"status":200,"trace_id":"%032x","span_id":"%016x"}`,
					start.Add(time.Duration(k)*time.Second).Format(time.RFC3339Nano), k%20, k%250, uri, agents[k%3],
					k%9999+1, 1000+k%5000, k+1, k+1)
				entries = append(entries, parse(t, line))
			}
			if err := a.Append(ctx, route, entries, Cursor{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var pages, size, free int64
	_ = a.db.Reader.QueryRow(`PRAGMA page_count`).Scan(&pages)
	_ = a.db.Reader.QueryRow(`PRAGMA page_size`).Scan(&size)
	_ = a.db.Reader.QueryRow(`PRAGMA freelist_count`).Scan(&free)
	perRow := float64((pages-free)*size) / n
	t.Logf("%d requests in %d KiB: %.0f bytes a request", n, (pages-free)*size/1024, perRow)
	if perRow > 140 {
		t.Fatalf("a kept request costs %.0f bytes; the caps assume about eighty", perRow)
	}
}
