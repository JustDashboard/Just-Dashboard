package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// memoryRecord is a request record with one live file that only grows, which
// is enough to prove the observer asks for the environment's own route and
// reports what the store says about it.
type memoryRecord struct {
	data  []byte
	asked string
	fail  error
}

func (m *memoryRecord) Read(_ context.Context, identity string, offset, limit int64, fn func(string)) (accesslog.FileStat, accesslog.FileStat, int64, error) {
	if m.fail != nil {
		return accesslog.FileStat{}, accesslog.FileStat{}, offset, m.fail
	}
	if len(m.data) == 0 {
		return accesslog.FileStat{}, accesslog.FileStat{}, offset, nil
	}
	stat := accesslog.FileStat{Exists: true, Identity: "1", Size: int64(len(m.data)), Modified: time.Now()}
	if identity != "" && identity != "1" {
		return accesslog.FileStat{}, stat, offset, nil
	}
	if limit <= 0 || offset >= stat.Size {
		return stat, stat, offset, nil
	}
	end := stat.Size
	if end-offset > limit {
		end = offset + limit
	}
	return stat, stat, offset + accesslog.ConsumeLines(m.data[offset:end], end-offset == limit, fn), nil
}

func (m *memoryRecord) Rolled(context.Context) ([]accesslog.FileStat, error) { return nil, nil }

func storeOver(record *memoryRecord, facts accesslog.Facts) *accesslog.Store {
	return accesslog.NewStore(func(_ context.Context, route string) (accesslog.Reader, accesslog.Facts, error) {
		record.asked = route
		if record.fail != nil {
			return nil, accesslog.Facts{}, record.fail
		}
		return record, facts, nil
	})
}

func caddyLine(at time.Time, method, uri string, status int, seconds float64) string {
	return fmt.Sprintf(`{"ts":%q,"msg":"handled request","request":{"method":%q,"host":"app.example.com",`+
		`"uri":%q,"remote_ip":"198.51.100.7","headers":{"User-Agent":["curl/8.5.0"]}},`+
		`"status":%d,"size":120,"duration":%v}`+"\n",
		at.Format(time.RFC3339Nano), method, uri, status, seconds)
}

var caddyFacts = accesslog.Facts{Driver: "docker-caddy", Format: accesslog.FormatCaddyJSON, Latency: true}

func TestObserveRequestsReadsTheEnvironmentsOwnRoute(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	record := &memoryRecord{data: []byte(
		caddyLine(now, "GET", "/", 200, 0.012) +
			caddyLine(now.Add(time.Second), "POST", "/api/save", 500, 0.900) +
			"a line of something else entirely\n")}
	window := ObserveRequests(context.Background(), storeOver(record, caddyFacts), 42, accesslog.Filter{Limit: 10})
	if window.Status != "available" {
		t.Fatalf("status = %q, reason %q", window.Status, window.Reason)
	}
	if record.asked != "just-dashboard-env-42.conf" {
		t.Fatalf("read %q, want the environment's own route file", record.asked)
	}
	if window.Summary.Total != 2 {
		t.Fatalf("total = %d: the unparseable line must be skipped, not counted", window.Summary.Total)
	}
	if window.Summary.Classes["5xx"] != 1 {
		t.Fatalf("classes = %v", window.Summary.Classes)
	}
	if !window.Latency || window.Driver != "docker-caddy" {
		t.Fatalf("driver facts lost: %+v", window)
	}
	if len(window.Entries) != 2 || window.Entries[0].Path != "/api/save" {
		t.Fatalf("rows wrong (newest first): %+v", window.Entries)
	}
	if !window.Complete || !window.Coverage.Exists || window.Coverage.Held != 2 || window.Coverage.Cursor != 2 {
		t.Fatalf("coverage = %+v", window.Coverage)
	}
}

func TestObserveRequestsExplainsAnAbsentRecord(t *testing.T) {
	window := ObserveRequests(context.Background(), storeOver(&memoryRecord{}, caddyFacts), 1, accesslog.Filter{})
	if window.Status != "unavailable" {
		t.Fatalf("status = %q", window.Status)
	}
	// "Nothing has asked for it" and "the ingress is broken" are different
	// sentences, and a page that renders both as an empty table teaches the
	// operator to distrust it.
	if !strings.Contains(window.Reason, "No request has been recorded") {
		t.Fatalf("reason = %q", window.Reason)
	}
	// The driver is still known, so the page can say what would record it.
	if window.Driver != "docker-caddy" {
		t.Fatalf("driver = %q", window.Driver)
	}
}

func TestObserveRequestsWithoutAProxy(t *testing.T) {
	window := ObserveRequests(context.Background(), nil, 1, accesslog.Filter{})
	if window.Status != "unavailable" || window.Reason == "" {
		t.Fatalf("a missing proxy must explain itself: %+v", window)
	}
	if window.Entries == nil || window.Summary.Classes == nil {
		t.Fatal("the empty answer still has to be renderable without a nil check per field")
	}
}

func TestObserveRequestsReportsAReadFailure(t *testing.T) {
	record := &memoryRecord{fail: errors.New("docker exec: connection refused to /var/run/docker.sock")}
	window := ObserveRequests(context.Background(), storeOver(record, caddyFacts), 3, accesslog.Filter{})
	if window.Status != "unavailable" {
		t.Fatalf("status = %q", window.Status)
	}
	if strings.Contains(window.Reason, "docker.sock") {
		t.Fatalf("reason leaked the transport error: %q", window.Reason)
	}
}

func TestObserveRequestsCarriesTheNginxFacts(t *testing.T) {
	now := time.Now().UTC()
	line := fmt.Sprintf("198.51.100.4 - - [%s] \"GET / HTTP/1.1\" 200 12 \"-\" \"curl\"\n", now.Format("02/Jan/2006:15:04:05 -0700"))
	facts := accesslog.Facts{Driver: "nginx", Format: accesslog.FormatCombined, Latency: false}
	window := ObserveRequests(context.Background(), storeOver(&memoryRecord{data: []byte(line)}, facts), 9, accesslog.Filter{Limit: 5})
	if window.Status != "available" || window.Latency || window.Format != "nginx-combined" {
		t.Fatalf("nginx window: %+v", window)
	}
	if window.Summary.Latency != nil {
		t.Fatal("combined carries no durations; a latency block would be zeros presented as measurements")
	}
}
