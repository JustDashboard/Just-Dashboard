package requesthistory

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// nothingOnDisk is an ingress whose files have all rolled away.
type nothingOnDisk struct{}

func (nothingOnDisk) Read(_ context.Context, _ string, offset, _ int64, _ func(string)) (accesslog.FileStat, accesslog.FileStat, int64, error) {
	return accesslog.FileStat{}, accesslog.FileStat{}, offset, nil
}

func (nothingOnDisk) Rolled(context.Context) ([]accesslog.FileStat, error) { return nil, nil }

// A week of a site serving 3,000 requests an hour, answered from the history
// alone: what the page's "Last 7 days" costs once the ingress's files have
// rolled past it. It writes half a million requests, so it runs only when
// asked: JD_REQUEST_HISTORY_SCALE=1.
func TestAWeekOfHistoryIsAnsweredQuickly(t *testing.T) {
	if os.Getenv("JD_REQUEST_HISTORY_SCALE") == "" {
		t.Skip("set JD_REQUEST_HISTORY_SCALE=1 to write and read a week of half a million requests")
	}
	a, c := openTest(t)
	ctx := context.Background()
	const n = 504_000
	week := 7 * 24 * time.Hour
	start := c.Now().Add(-week)
	step := week / n
	agents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"curl/8.5.0",
	}
	began := time.Now()
	for k := 0; k < n; {
		batch := make([]accesslog.Entry, 0, 5000)
		for ; len(batch) < cap(batch) && k < n; k++ {
			uri := fmt.Sprintf("/p/%d", k%600)
			if k%8 == 0 {
				uri = fmt.Sprintf("/_next/static/chunk-%d.js", k%50)
			}
			if k%10 == 0 {
				uri += fmt.Sprintf("?t=%d", k)
			}
			line := fmt.Sprintf(`{"ts":%q,"msg":"handled request","request":{"client_ip":"198.51.%d.%d","proto":"HTTP/2.0",`+
				`"method":"GET","host":"shop.test","uri":%q,"headers":{"User-Agent":[%q],"Referer":["https://ref%d.test/"]},"tls":{}},`+
				`"duration":0.0%04d,"size":%d,"status":%d,"trace_id":"%032x","span_id":"%016x"}`,
				start.Add(time.Duration(k)*step).Format(time.RFC3339Nano), k%40, k%250, uri, agents[k%len(agents)], k%30,
				k%9999+1, 1000+k%5000, []int{200, 200, 200, 304, 404, 500}[k%6], k+1, k+1)
			batch = append(batch, parse(t, line))
		}
		if err := a.Append(ctx, "r", batch, Cursor{}); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("wrote %d requests in %v", n, time.Since(began).Round(time.Millisecond))

	store := accesslog.NewStore(func(context.Context, string) (accesslog.Reader, accesslog.Facts, error) {
		return nothingOnDisk{}, accesslog.Facts{Format: accesslog.FormatCaddyJSON, Latency: true}, nil
	}).WithArchive(a)
	for i, label := range []string{"cold", "warm dictionary"} {
		began, cpu := time.Now(), cpuTime()
		window, err := store.Window(ctx, "r", accesslog.Filter{Since: c.Now().Add(-week).Add(time.Duration(i) * windowCacheMiss), Limit: 500})
		took, used := time.Since(began), cpuTime()-cpu
		if err != nil {
			t.Fatal(err)
		}
		// Wall time depends on what else the host is running; the CPU the
		// process spent says what the window itself costs.
		t.Logf("a week (%s): %d requests in %v (%v of CPU), %d columns of %ds", label, window.Result.Summary.Total,
			took.Round(time.Millisecond), used.Round(time.Millisecond), len(window.Result.Summary.Buckets), window.Result.Summary.BucketSeconds)
		if window.Result.Summary.Total != n-i*int(windowCacheMiss/step) && i == 0 {
			t.Fatalf("the week counts %d of %d requests", window.Result.Summary.Total, n)
		}
	}
}

func cpuTime() time.Duration {
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// windowCacheMiss moves the second window's start past the store's cache, so
// it is read again rather than handed back.
const windowCacheMiss = time.Minute
