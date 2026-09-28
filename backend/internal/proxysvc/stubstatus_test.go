package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// canonicalStubStatus is ngx_http_stub_status_module's page byte for byte,
// trailing spaces included, as nginx.org documents it.
const canonicalStubStatus = "Active connections: 291 \n" +
	"server accepts handled requests\n" +
	" 16630948 16630948 31070465 \n" +
	"Reading: 6 Writing: 179 Waiting: 106 \n"

func TestParseStubStatus(t *testing.T) {
	got, err := ParseStubStatus(canonicalStubStatus)
	if err != nil {
		t.Fatal(err)
	}
	want := StubStatus{Active: 291, Accepts: 16630948, Handled: 16630948, Requests: 31070465, Reading: 6, Writing: 179, Waiting: 106}
	if got != want {
		t.Fatalf("parsed %+v, want %+v", got, want)
	}
	if crlf, err := ParseStubStatus(strings.ReplaceAll(canonicalStubStatus, "\n", "\r\n")); err != nil || crlf != want {
		t.Fatalf("CRLF page parsed as %+v, %v", crlf, err)
	}
	for name, page := range map[string]string{
		"empty":         "",
		"welcome page":  "<html><body><h1>Welcome to nginx!</h1></body></html>",
		"no counters":   "Active connections: 1 \nReading: 0 Writing: 1 Waiting: 0 \n",
		"no states":     "Active connections: 1 \nserver accepts handled requests\n 1 1 1 \n",
		"negative":      strings.Replace(canonicalStubStatus, "291", "-291", 1),
		"too large":     strings.Replace(canonicalStubStatus, "31070465", "99999999999999999999", 1),
		"counters torn": "Active connections: 1 \nserver accepts handled requests\n 1 1 \nReading: 0 Writing: 1 Waiting: 0 \n",
	} {
		if _, err := ParseStubStatus(page); err == nil {
			t.Errorf("%s: parsed without an error", name)
		}
	}
}

// The file is the switch's state, so what it renders has to read back as the
// dashboard's own with its port, and anybody else's file must not.
func TestStatusFileReadsBackAsTheDashboards(t *testing.T) {
	content := RenderStatusServer(19081)
	if owned, port := parseStatusFile(content); !owned || port != 19081 {
		t.Fatalf("rendered file reads back as owned=%v port=%d", owned, port)
	}
	// A site listing tells conf.d/jd-* files whose first line says the
	// dashboard owns them from sites.
	first, _, _ := strings.Cut(content, "\n")
	if !strings.HasPrefix(statusFileName, "jd-") || !strings.Contains(first, "Just Dashboard owned") {
		t.Fatalf("status file %s opens with %q", statusFileName, first)
	}
	for _, fence := range []string{"listen 127.0.0.1:19081;", "allow 127.0.0.1;", "deny all;", "access_log off;", "keepalive_timeout 0;"} {
		if !strings.Contains(content, fence) {
			t.Errorf("rendered status server lacks %q:\n%s", fence, content)
		}
	}
	for _, foreign := range []string{
		"server { listen 127.0.0.1:19081; location = /jd-status { stub_status; } }\n",
		"# a note\n" + content,
		"",
	} {
		if owned, _ := parseStatusFile(foreign); owned {
			t.Errorf("%q read as the dashboard's file", foreign)
		}
	}
	// Parameters after the port still leave stub_status on 127.0.0.1 there;
	// another address is not the one the sampler reads.
	for listen, want := range map[string]int{
		"listen 19081;":                                 0,
		"listen localhost:19081;":                       0,
		"listen [::1]:19081;":                           0,
		"listen 127.0.0.1:19081 reuseport;":             19081,
		"listen 127.0.0.1:19081 default_server;":        19081,
		"listen 127.0.0.1:19081  default_server  ;":     19081,
		"listen 127.0.0.1:19081 backlog=64 reuseport ;": 19081,
		"listen 127.0.0.1:190811;":                      0,
	} {
		if owned, port := parseStatusFile(strings.Replace(content, "listen 127.0.0.1:19081;", listen, 1)); !owned || port != want {
			t.Errorf("%q read as owned=%v port %d, want %d", listen, owned, port, want)
		}
	}
}

// fakeStub answers as nginx's stub_status does, counting the reading's own
// connection and request before it writes the page.
type fakeStub struct {
	mu                                sync.Mutex
	accepts, handled, requests        int64
	active, reading, writing, waiting int64
	answer                            func(w http.ResponseWriter) bool
	calls                             int
}

func (f *fakeStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if r.URL.Path != statusLocation {
		http.NotFound(w, r)
		return
	}
	if f.answer != nil && f.answer(w) {
		return
	}
	f.accepts++
	f.handled++
	f.requests++
	fmt.Fprintf(w, "Active connections: %d \nserver accepts handled requests\n %d %d %d \nReading: %d Writing: %d Waiting: %d \n",
		f.active+1, f.accepts, f.handled, f.requests, f.reading, f.writing+1, f.waiting)
}

// visit counts n requests from visitors, each on a connection of its own.
func (f *fakeStub) visit(n int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accepts += n
	f.handled += n
	f.requests += n
}

func (f *fakeStub) set(change func(f *fakeStub)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

type sampledHost struct {
	root    string
	stub    *fakeStub
	server  *httptest.Server
	port    int
	sampler *StatusSampler
	clock   time.Time
}

// sampledHost is a proxy directory whose status file points at a fake
// stub_status, read by a sampler on a clock the test moves.
func newSampledHost(t *testing.T) *sampledHost {
	t.Helper()
	h := &sampledHost{root: t.TempDir(), stub: &fakeStub{}, clock: time.Unix(1_800_000_000, 0)}
	if err := os.MkdirAll(filepath.Join(h.root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(h.stub)
	t.Cleanup(h.server.Close)
	h.port = h.server.Listener.Addr().(*net.TCPAddr).Port
	h.writeStatusFile(t, RenderStatusServer(h.port))
	h.sampler = NewStatusSampler(New(h.root, ""))
	h.sampler.now = func() time.Time { return h.clock }
	return h
}

func (h *sampledHost) writeStatusFile(t *testing.T, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.root, "conf.d", statusFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// poll moves the clock on by gap and takes a reading.
func (h *sampledHost) poll(t *testing.T, gap time.Duration) StatusSample {
	t.Helper()
	h.clock = h.clock.Add(gap)
	if err := h.sampler.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := h.sampler.Report(0, 0)
	if report.Current == nil {
		t.Fatal("a reading that succeeded is not the current one")
	}
	return *report.Current
}

func rateOf(s StatusSample) string {
	if s.Requests == nil {
		return "none"
	}
	return fmt.Sprintf("%.2f", *s.Requests)
}

func TestSamplerCountsVisitorsAndLeavesItsOwnRequestOut(t *testing.T) {
	h := newSampledHost(t)
	h.stub.set(func(f *fakeStub) { f.active, f.reading, f.writing, f.waiting = 7, 1, 2, 4 })
	first := h.poll(t, 0)
	if first.Requests != nil {
		t.Fatalf("the first reading has a rate of %s with nothing to take it from", rateOf(first))
	}
	if first.Active != 7 || first.Reading != 1 || first.Writing != 2 || first.Waiting != 4 {
		t.Fatalf("first reading %+v counts the sampler's own connection", first)
	}
	h.stub.visit(50)
	second := h.poll(t, StatusInterval)
	if rateOf(second) != "10.00" {
		t.Fatalf("50 requests over 5s read as %s a second", rateOf(second))
	}
	h.poll(t, StatusInterval)
	report := h.sampler.Report(0, 0)
	if report.HourRequests != 50 {
		t.Fatalf("the hour holds %d requests, want the visitors' 50", report.HourRequests)
	}
	if !report.Enabled || report.Endpoint != statusEndpoint(h.port) || report.Error != "" {
		t.Fatalf("report %+v", report)
	}
	if report.Totals == nil || report.Totals.Requests != 53 {
		t.Fatalf("totals %+v, want nginx's own count of 53", report.Totals)
	}
}

// nginx starts its counters again from zero when it restarts; a delta across
// that is not traffic, and never a negative rate.
func TestSamplerNeverDrawsARestartAsANegativeRate(t *testing.T) {
	h := newSampledHost(t)
	h.stub.visit(1000)
	h.poll(t, 0)
	h.stub.visit(10)
	h.poll(t, StatusInterval)
	h.stub.set(func(f *fakeStub) { f.accepts, f.handled, f.requests = 2, 2, 3 })
	across := h.poll(t, StatusInterval)
	if across.Requests != nil || across.Dropped != 0 {
		t.Fatalf("a restart drew a rate of %s and %d dropped", rateOf(across), across.Dropped)
	}
	h.stub.visit(25)
	after := h.poll(t, StatusInterval)
	if rateOf(after) != "5.00" {
		t.Fatalf("after the restart 25 requests in 5s read as %s", rateOf(after))
	}
	for _, s := range h.sampler.Report(0, 0).Samples {
		if s.Requests != nil && *s.Requests < 0 {
			t.Fatalf("negative rate in %+v", s)
		}
	}
	if got := h.sampler.Report(0, 0).HourRequests; got != 35 {
		t.Fatalf("the hour holds %d requests, want 10 + 25", got)
	}
}

// One average across minutes without a reading would stand for all of them;
// the requests still count toward the hour.
func TestSamplerDrawsNoRateAcrossAGap(t *testing.T) {
	h := newSampledHost(t)
	h.poll(t, 0)
	h.stub.visit(600)
	late := h.poll(t, 2*time.Minute)
	if late.Requests != nil {
		t.Fatalf("a reading two minutes late drew a rate of %s", rateOf(late))
	}
	if got := h.sampler.Report(0, 0).HourRequests; got != 600 {
		t.Fatalf("the hour holds %d requests, want 600", got)
	}
}

func TestSamplerCountsDroppedConnections(t *testing.T) {
	h := newSampledHost(t)
	h.poll(t, 0)
	h.stub.set(func(f *fakeStub) { f.accepts += 9; f.handled += 4; f.requests += 4 })
	s := h.poll(t, StatusInterval)
	if s.Dropped != 5 {
		t.Fatalf("dropped %d, want the 5 accepted and not handled", s.Dropped)
	}
	h.poll(t, StatusInterval)
	if got := h.sampler.Report(0, 0).HourDropped; got != 5 {
		t.Fatalf("the hour dropped %d, want 5", got)
	}
}

func TestSamplerKeepsAnHour(t *testing.T) {
	h := newSampledHost(t)
	for range 800 {
		h.stub.visit(5)
		h.poll(t, StatusInterval)
	}
	report := h.sampler.Report(0, 0)
	if n := len(report.Samples); n < 700 || n > 721 {
		t.Fatalf("kept %d readings, want an hour's 720", n)
	}
	if oldest := h.clock.Sub(report.Samples[0].At); oldest > statusWindow {
		t.Fatalf("kept a reading %s old", oldest)
	}
	if report.HourRequests != int64(len(report.Samples))*5 {
		t.Fatalf("the hour holds %d requests over %d readings", report.HourRequests, len(report.Samples))
	}
}

// A caller that holds the series is sent what is new; one holding another
// series, or none, the whole hour.
func TestReportSendsOnlyWhatTheCallerLacks(t *testing.T) {
	h := newSampledHost(t)
	h.poll(t, 0)
	h.poll(t, StatusInterval)
	whole := h.sampler.Report(0, 0)
	if len(whole.Samples) != 2 || whole.Epoch == 0 {
		t.Fatalf("whole report %+v", whole)
	}
	last := whole.Samples[1].Seq
	if held := h.sampler.Report(whole.Epoch, last); len(held.Samples) != 0 || held.Current == nil {
		t.Fatalf("a caller up to date was sent %d readings, current %v", len(held.Samples), held.Current)
	}
	h.poll(t, StatusInterval)
	next := h.sampler.Report(whole.Epoch, last)
	if len(next.Samples) != 1 || next.Samples[0].Seq != last+1 {
		t.Fatalf("an update after seq %d sent %+v", last, next.Samples)
	}
	if other := h.sampler.Report(whole.Epoch+1, last); len(other.Samples) != 3 {
		t.Fatalf("a caller holding another series was sent %d readings, want all 3", len(other.Samples))
	}
}

// Removing the file by hand switches the metrics off, and a moved port is a
// new series: readings of one address are not readings of another.
func TestSeriesStartsAgainWhenTheFileChanges(t *testing.T) {
	h := newSampledHost(t)
	h.poll(t, 0)
	h.poll(t, StatusInterval)
	before := h.sampler.Report(0, 0)

	moved := httptest.NewServer(h.stub)
	defer moved.Close()
	h.writeStatusFile(t, RenderStatusServer(moved.Listener.Addr().(*net.TCPAddr).Port))
	if stale := h.sampler.Report(0, 0); len(stale.Samples) != 0 || stale.Current != nil {
		t.Fatalf("the old port's readings were reported for the new one: %+v", stale.Samples)
	}
	h.poll(t, StatusInterval)
	after := h.sampler.Report(before.Epoch, before.Samples[1].Seq)
	if after.Epoch == before.Epoch || len(after.Samples) != 1 {
		t.Fatalf("a moved port kept the series: epoch %d -> %d, %d readings", before.Epoch, after.Epoch, len(after.Samples))
	}

	if err := os.Remove(filepath.Join(h.root, "conf.d", statusFileName)); err != nil {
		t.Fatal(err)
	}
	calls := h.stub.calls
	h.clock = h.clock.Add(StatusInterval)
	if err := h.sampler.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	off := h.sampler.Report(0, 0)
	if off.Enabled || off.Endpoint != "" || len(off.Samples) != 0 || off.Epoch == after.Epoch {
		t.Fatalf("switched off by hand, the report says %+v", off)
	}
	if h.stub.calls != calls {
		t.Fatal("the sampler read an address with no status file naming it")
	}
}

func TestSamplerSaysWhyAReadingFailed(t *testing.T) {
	h := newSampledHost(t)
	h.poll(t, 0)
	failing := h.clock.Add(StatusInterval)
	for _, c := range []struct {
		answer func(w http.ResponseWriter) bool
		want   string
	}{
		{func(w http.ResponseWriter) bool { w.WriteHeader(http.StatusForbidden); return true }, "answered 403"},
		{func(w http.ResponseWriter) bool { fmt.Fprint(w, "<h1>Welcome to nginx!</h1>"); return true }, "not nginx's stub_status page"},
	} {
		h.stub.set(func(f *fakeStub) { f.answer = c.answer })
		h.clock = h.clock.Add(StatusInterval)
		if err := h.sampler.Poll(context.Background()); err == nil {
			t.Fatal("a failed reading returned no error")
		}
		report := h.sampler.Report(0, 0)
		if !strings.Contains(report.Error, c.want) || report.FailingSince == nil || !report.FailingSince.Equal(failing) {
			t.Fatalf("error %q since %v, want %q since %v", report.Error, report.FailingSince, c.want, failing)
		}
		if len(report.Samples) != 1 {
			t.Fatalf("a failure dropped the readings: %d left", len(report.Samples))
		}
	}
	h.stub.set(func(f *fakeStub) { f.answer = nil })
	h.poll(t, StatusInterval)
	if report := h.sampler.Report(0, 0); report.Error != "" || report.FailingSince != nil {
		t.Fatalf("an answer did not clear the failure: %+v", report)
	}

	h.server.Close()
	h.clock = h.clock.Add(StatusInterval)
	if err := h.sampler.Poll(context.Background()); err == nil {
		t.Fatal("a closed port returned no error")
	}
	if report := h.sampler.Report(0, 0); report.Error != fmt.Sprintf("nothing is listening on 127.0.0.1:%d", h.port) {
		t.Fatalf("a closed port reads %q", report.Error)
	}
}

// The hour is the hour before now. Readings that fail for longer than it
// leave nothing behind to be read as the last hour — no requests, and no
// connections turned away — and the first reading after them counts nothing
// from before it.
func TestSamplerForgetsAnHourOfFailedReadings(t *testing.T) {
	h := newSampledHost(t)
	h.poll(t, 0)
	for range 10 {
		h.stub.visit(90)
		h.poll(t, StatusInterval)
	}
	h.stub.set(func(f *fakeStub) { f.accepts += 5 })
	h.poll(t, StatusInterval)
	if report := h.sampler.Report(0, 0); report.HourRequests != 900 || report.HourDropped != 5 || len(report.Samples) != 12 {
		t.Fatalf("before the failure the hour holds %d requests, %d dropped over %d readings", report.HourRequests, report.HourDropped, len(report.Samples))
	}
	last := h.clock

	// Asked between polls, a report counts nothing older than the hour.
	h.clock = last.Add(statusWindow + time.Second)
	if report := h.sampler.Report(0, 0); report.HourRequests != 0 || report.HourDropped != 0 || len(report.Samples) != 0 || report.Current != nil || report.Totals != nil {
		t.Fatalf("an hour after the last reading the report holds %+v", report)
	}

	h.stub.set(func(f *fakeStub) {
		f.answer = func(w http.ResponseWriter) bool { w.WriteHeader(http.StatusServiceUnavailable); return true }
	})
	h.clock = last
	for range 18 {
		h.clock = h.clock.Add(10 * time.Minute)
		if err := h.sampler.Poll(context.Background()); err == nil {
			t.Fatal("a failed reading returned no error")
		}
	}
	report := h.sampler.Report(0, 0)
	if report.HourRequests != 0 || report.HourDropped != 0 || len(report.Samples) != 0 || report.Current != nil {
		t.Fatalf("after 3h of failed readings the report holds %d requests, %d dropped, %d readings, current %v",
			report.HourRequests, report.HourDropped, len(report.Samples), report.Current)
	}
	if report.Error == "" || report.FailingSince == nil || !report.At.Equal(h.clock) {
		t.Fatalf("the failure reads %q since %v at %v", report.Error, report.FailingSince, report.At)
	}
	h.sampler.mu.Lock()
	held, previous := len(h.sampler.samples), h.sampler.previous
	h.sampler.mu.Unlock()
	if held != 0 || previous != nil {
		t.Fatalf("the sampler still holds %d readings and a base of %v", held, previous)
	}

	h.stub.set(func(f *fakeStub) { f.answer = nil })
	h.stub.visit(5000)
	back := h.poll(t, StatusInterval)
	if back.Requests != nil {
		t.Fatalf("the first reading after 3h drew a rate of %s", rateOf(back))
	}
	if got := h.sampler.Report(0, 0).HourRequests; got != 0 {
		t.Fatalf("the first reading after 3h counts %d requests from before it into the hour", got)
	}
}

// A report says when it was made, so a client drops readings on the clock
// the readings were taken by.
func TestReportSaysWhenItWasMade(t *testing.T) {
	h := newSampledHost(t)
	h.poll(t, 0)
	h.clock = h.clock.Add(2 * time.Second)
	if at := h.sampler.Report(0, 0).At; !at.Equal(h.clock) {
		t.Fatalf("report made at %v, the clock reads %v", at, h.clock)
	}
}

// The dashboard's own file whose listen was edited past what the sampler
// reads is switched on and never read; the report says so rather than
// standing on "On" with no reading and no reason.
func TestReportSaysWhyTheDashboardsFileIsNotRead(t *testing.T) {
	h := newSampledHost(t)
	for _, listen := range []string{fmt.Sprintf("listen [::1]:%d;", h.port), fmt.Sprintf("listen localhost:%d;", h.port)} {
		h.writeStatusFile(t, strings.Replace(RenderStatusServer(h.port), fmt.Sprintf("listen 127.0.0.1:%d;", h.port), listen, 1))
		if err := h.sampler.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		report := h.sampler.Report(0, 0)
		if !report.Enabled || report.Endpoint != "" || len(report.Samples) != 0 || h.stub.calls != 0 {
			t.Fatalf("%s: report %+v after %d reads", listen, report, h.stub.calls)
		}
		if !strings.Contains(report.Error, "names no 127.0.0.1 port the dashboard can read") || !strings.Contains(report.Error, statusFileName) {
			t.Fatalf("%s: the report says %q", listen, report.Error)
		}
	}
	// Parameters after a loopback port are read as that port.
	h.writeStatusFile(t, strings.Replace(RenderStatusServer(h.port), ";\n    access_log", " reuseport;\n    access_log", 1))
	h.poll(t, 0)
	if report := h.sampler.Report(0, 0); report.Error != "" || report.Endpoint != statusEndpoint(h.port) || len(report.Samples) != 1 {
		t.Fatalf("a listen with reuseport reads as %+v", report)
	}
}

func TestSamplerLeavesAFileItDidNotWriteAlone(t *testing.T) {
	h := newSampledHost(t)
	h.writeStatusFile(t, fmt.Sprintf("server { listen 127.0.0.1:%d; location = /jd-status { stub_status; } }\n", h.port))
	if err := h.sampler.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := h.sampler.Report(0, 0)
	if report.Enabled || !report.Foreign || h.stub.calls != 0 {
		t.Fatalf("somebody's file was read as the switch: %+v, %d calls", report, h.stub.calls)
	}
}

// statusHost is a Debian-layout proxy directory behind a scripted nginx that
// logs its arguments and prints conf.d in its dump, as nginx does when
// nginx.conf includes it.
type statusHost struct {
	root    string
	service *Service
	log     *changeLog
}

const statusNginxScript = `#!/bin/sh
echo "$*" >> "$JD_TEST_NGINX_ROOT/nginx.log"
case "$1" in
-t)
	if [ -n "$JD_TEST_NGINX_FAIL_TEST" ]; then
		echo "nginx: [emerg] unknown directive \"stub_status\" in $JD_TEST_NGINX_ROOT/conf.d/jd-status.conf:7" >&2
		echo "nginx: configuration file $JD_TEST_NGINX_ROOT/nginx.conf test failed" >&2
		exit 1
	fi
	echo "nginx: configuration file $JD_TEST_NGINX_ROOT/nginx.conf test is successful" >&2
	;;
-T)
	echo "# configuration file $JD_TEST_NGINX_ROOT/nginx.conf:"
	echo "events {}"
	if [ -z "$JD_TEST_NGINX_SKIP_CONFD" ]; then
		for f in "$JD_TEST_NGINX_ROOT"/conf.d/*.conf; do
			[ -e "$f" ] || continue
			echo
			echo "# configuration file $f:"
			cat "$f"
		done
	fi
	;;
-s)
	if [ -n "$JD_TEST_NGINX_FAIL_RELOAD" ]; then
		echo 'nginx: [error] invalid PID number "" in "/run/nginx.pid"' >&2
		exit 1
	fi
	;;
esac
exit 0
`

func newStatusHost(t *testing.T) *statusHost {
	t.Helper()
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	root = resolved
	for _, dir := range []string{"sites-available", "sites-enabled", "conf.d", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte("events {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(statusNginxScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_TEST_NGINX_ROOT", root)
	t.Setenv("JD_TEST_NGINX_FAIL_TEST", "")
	t.Setenv("JD_TEST_NGINX_SKIP_CONFD", "")
	t.Setenv("JD_TEST_NGINX_FAIL_RELOAD", "")
	h := &statusHost{root: root, service: New(root, filepath.Join(root, "Caddyfile")), log: &changeLog{}}
	h.service.SetRecorder(h.log)
	return h
}

func (h *statusHost) file() string { return filepath.Join(h.root, "conf.d", statusFileName) }

// calls is every nginx invocation so far, one per line.
func (h *statusHost) calls(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.root, "nginx.log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// answers is a verify that records where it was pointed and answers with the
// next of its results, then with the last one.
type answers struct {
	results   []error
	endpoints []string
}

func (a *answers) verify(_ context.Context, endpoint string) error {
	a.endpoints = append(a.endpoints, endpoint)
	if len(a.results) == 0 {
		return nil
	}
	err := a.results[0]
	if len(a.results) > 1 {
		a.results = a.results[1:]
	}
	return err
}

func (h *statusHost) assertNoFile(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(h.file()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the status file was left behind: %v", err)
	}
}

func TestSwitchingOnWritesTestsReloadsAndWaitsForTheFirstReading(t *testing.T) {
	h := newStatusHost(t)
	// The preferred port held by somebody else is passed over.
	if held, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", statusPortPreferred)); err == nil {
		defer held.Close()
	}
	a := &answers{}
	change, err := h.service.EnableStatusServer(context.Background(), a.verify)
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed || !change.Reloaded || change.Port == 0 || change.Port == statusPortPreferred {
		t.Fatalf("change %+v", change)
	}
	raw, err := os.ReadFile(h.file())
	if err != nil || string(raw) != RenderStatusServer(change.Port) {
		t.Fatalf("status file %q, %v", raw, err)
	}
	if want := []string{"-t", "-T", "-s reload"}; strings.Join(h.calls(t), "|") != strings.Join(want, "|") {
		t.Fatalf("nginx ran %q, want %q", h.calls(t), want)
	}
	if len(a.endpoints) != 1 || a.endpoints[0] != statusEndpoint(change.Port) {
		t.Fatalf("verified %q", a.endpoints)
	}
	if len(h.log.changes) != 1 || h.log.changes[0].Action != ChangeWrite || h.log.changes[0].BeforeExisted {
		t.Fatalf("recorded %+v", h.log.changes)
	}
	if last, ok := h.service.LastTest(KindNginx); !ok || !last.Validation.Valid {
		t.Fatalf("the passing test was not kept as the engine's last: %+v", last)
	}

	// On again: in place and answering, so nothing is written or reloaded.
	again, err := h.service.EnableStatusServer(context.Background(), a.verify)
	if err != nil || again.Changed || again.Port != change.Port || len(h.calls(t)) != 3 {
		t.Fatalf("switching on twice gave %+v, %v and ran nginx %q", again, err, h.calls(t))
	}
}

// A status file already in place keeps its port, even when it has stopped
// answering and has to be tested and reloaded again.
func TestSwitchingOnKeepsTheFilesPort(t *testing.T) {
	h := newStatusHost(t)
	if err := os.WriteFile(h.file(), []byte(RenderStatusServer(23456)), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &answers{results: []error{errors.New("nothing is listening"), nil}}
	change, err := h.service.EnableStatusServer(context.Background(), a.verify)
	if err != nil || !change.Changed || change.Port != 23456 || !change.Reloaded {
		t.Fatalf("change %+v, %v", change, err)
	}
	if len(h.log.changes) != 1 || !h.log.changes[0].BeforeExisted {
		t.Fatalf("recorded %+v", h.log.changes)
	}
}

func TestSwitchingOnPutsTheHostBackWhenAnyStepFails(t *testing.T) {
	edited := strings.Replace(RenderStatusServer(23456), "keepalive_timeout 0;", "keepalive_timeout 0; # tuned", 1)
	for _, c := range []struct {
		name    string
		env     string
		prior   string
		verify  []error
		want    error
		calls   []string
		message string
	}{
		{name: "test fails", env: "JD_TEST_NGINX_FAIL_TEST", want: ErrInvalidConf, calls: []string{"-t"}},
		{name: "test fails over an edited file", env: "JD_TEST_NGINX_FAIL_TEST", prior: edited, verify: []error{errors.New("no")}, want: ErrInvalidConf, calls: []string{"-t"}},
		{name: "conf.d not read", env: "JD_TEST_NGINX_SKIP_CONFD", want: ErrStatusNotIncluded, calls: []string{"-t", "-T"}},
		{name: "reload refused", env: "JD_TEST_NGINX_FAIL_RELOAD", want: ErrStatusReload, calls: []string{"-t", "-T", "-s reload"}, message: "invalid PID number"},
		{name: "no answer", verify: []error{errors.New("nothing is listening on 127.0.0.1:1")}, want: ErrStatusNoAnswer,
			calls: []string{"-t", "-T", "-s reload", "-t", "-s reload"}, message: "nothing is listening on 127.0.0.1:1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newStatusHost(t)
			if c.env != "" {
				t.Setenv(c.env, "1")
			}
			if c.prior != "" {
				if err := os.WriteFile(h.file(), []byte(c.prior), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			a := &answers{results: c.verify}
			change, err := h.service.EnableStatusServer(context.Background(), a.verify)
			if !errors.Is(err, c.want) {
				t.Fatalf("error %v, want %v", err, c.want)
			}
			if !strings.Contains(err.Error(), c.message) {
				t.Fatalf("error %q does not say %q", err, c.message)
			}
			if c.prior == "" {
				h.assertNoFile(t)
			} else if raw, _ := os.ReadFile(h.file()); string(raw) != c.prior {
				t.Fatalf("the edited file became %q", raw)
			}
			calls := h.calls(t)
			if c.prior != "" {
				calls = calls[len(calls)-len(c.calls):]
			}
			if strings.Join(calls, "|") != strings.Join(c.calls, "|") {
				t.Fatalf("nginx ran %q, want %q", calls, c.calls)
			}
			if len(h.log.changes) != 0 {
				t.Fatalf("a change that was undone was recorded: %+v", h.log.changes)
			}
			if errors.Is(err, ErrInvalidConf) && (change == nil || change.Validation == nil || len(change.Validation.Diagnostics) == 0) {
				t.Fatalf("a failed test came back without its diagnostics: %+v", change)
			}
		})
	}
}

func TestSwitchingLeavesSomebodyElsesFileAlone(t *testing.T) {
	h := newStatusHost(t)
	theirs := "server { listen 127.0.0.1:8080; location = /jd-status { stub_status; } }\n"
	if err := os.WriteFile(h.file(), []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.EnableStatusServer(context.Background(), (&answers{}).verify); !errors.Is(err, ErrStatusForeign) {
		t.Fatalf("switching on over their file: %v", err)
	}
	if _, err := h.service.DisableStatusServer(context.Background(), (&answers{}).verify); !errors.Is(err, ErrStatusForeign) {
		t.Fatalf("switching off over their file: %v", err)
	}
	if raw, _ := os.ReadFile(h.file()); string(raw) != theirs || len(h.calls(t)) != 0 {
		t.Fatalf("their file became %q after nginx ran %q", raw, h.calls(t))
	}
}

func TestSwitchingOnNeedsConfD(t *testing.T) {
	h := newStatusHost(t)
	if err := os.Remove(filepath.Join(h.root, "conf.d")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.EnableStatusServer(context.Background(), (&answers{}).verify); !errors.Is(err, ErrStatusUnsupported) {
		t.Fatalf("switching on with no conf.d: %v", err)
	}
	if ok, reason := h.service.StatusSupported(); ok || !strings.Contains(reason, "conf.d") {
		t.Fatalf("supported %v: %q", ok, reason)
	}
}

func TestSwitchingOffRemovesTestsAndReloads(t *testing.T) {
	h := newStatusHost(t)
	content := RenderStatusServer(23456)
	if err := os.WriteFile(h.file(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	change, err := h.service.DisableStatusServer(context.Background(), (&answers{}).verify)
	if err != nil || !change.Changed || !change.Reloaded || change.Port != 23456 {
		t.Fatalf("change %+v, %v", change, err)
	}
	h.assertNoFile(t)
	if strings.Join(h.calls(t), "|") != "-t|-s reload" {
		t.Fatalf("nginx ran %q", h.calls(t))
	}
	if len(h.log.changes) != 1 || h.log.changes[0].Action != ChangeDelete || string(h.log.changes[0].Before) != content {
		t.Fatalf("recorded %+v", h.log.changes)
	}
	again, err := h.service.DisableStatusServer(context.Background(), (&answers{}).verify)
	if err != nil || again.Changed || len(h.calls(t)) != 2 {
		t.Fatalf("switching off twice gave %+v, %v and ran nginx %q", again, err, h.calls(t))
	}
}

func TestSwitchingOffKeepsTheFileWhenTheTestFailsWithoutIt(t *testing.T) {
	h := newStatusHost(t)
	t.Setenv("JD_TEST_NGINX_FAIL_TEST", "1")
	content := RenderStatusServer(23456)
	if err := os.WriteFile(h.file(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.DisableStatusServer(context.Background(), (&answers{}).verify); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("error %v", err)
	}
	if raw, _ := os.ReadFile(h.file()); string(raw) != content {
		t.Fatalf("the file became %q", raw)
	}
	if len(h.log.changes) != 0 {
		t.Fatalf("recorded %+v", h.log.changes)
	}
}

// Once the file is out and the configuration tests clean, a reload that
// fails does not bring the file back: nginx drops the status server at its
// next reload whatever happens now. The error says whether it still answers
// until then — with nginx stopped, nothing does.
func TestSwitchingOffSaysWhenNginxDidNotReload(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer error
		says   string
		never  string
		port   string
		probed bool
	}{
		{name: "nginx running", says: "still answers on 127.0.0.1:23456 until nginx next reloads or restarts", never: "nothing answers", port: "listen 127.0.0.1:23456;", probed: true},
		{name: "nginx stopped", answer: errors.New("nothing is listening on 127.0.0.1:23456"), says: "nothing answers on 127.0.0.1:23456. nginx did not reload", never: "still answers", port: "listen 127.0.0.1:23456;", probed: true},
		{name: "no port read", says: "was removed, but nginx did not reload.", never: "answers", port: "listen [::1]:23456;"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newStatusHost(t)
			t.Setenv("JD_TEST_NGINX_FAIL_RELOAD", "1")
			content := strings.Replace(RenderStatusServer(23456), "listen 127.0.0.1:23456;", c.port, 1)
			if err := os.WriteFile(h.file(), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			a := &answers{results: []error{c.answer}}
			change, err := h.service.DisableStatusServer(context.Background(), a.verify)
			if !errors.Is(err, ErrStatusReload) || !change.Changed || change.Reloaded {
				t.Fatalf("change %+v, error %v", change, err)
			}
			if !strings.Contains(err.Error(), c.says) || strings.Contains(err.Error(), c.never) || !strings.Contains(err.Error(), "invalid PID number") {
				t.Fatalf("error %q, want it to say %q and never %q, with nginx's words", err, c.says, c.never)
			}
			if probed := strings.Join(a.endpoints, " "); (probed == statusEndpoint(23456)) != c.probed {
				t.Fatalf("asked %q", probed)
			}
			h.assertNoFile(t)
		})
	}
}

func TestSamplerForgetsTheSeriesWhenSwitchedOff(t *testing.T) {
	h := newStatusHost(t)
	stub := &fakeStub{}
	server := httptest.NewServer(stub)
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(h.file(), []byte(RenderStatusServer(port)), 0o644); err != nil {
		t.Fatal(err)
	}
	sampler := NewStatusSampler(h.service)
	// Switching on over a file in place and answering takes the first
	// reading as its proof.
	change, err := sampler.Enable(context.Background())
	if err != nil || change.Changed || change.Port != port {
		t.Fatalf("change %+v, %v", change, err)
	}
	on := sampler.Report(0, 0)
	if len(on.Samples) != 1 || !on.Enabled {
		t.Fatalf("switched on, the report says %+v", on)
	}
	if _, err := sampler.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	off := sampler.Report(0, 0)
	if off.Enabled || len(off.Samples) != 0 || off.Current != nil || off.Epoch == on.Epoch {
		t.Fatalf("switched off, the report says %+v", off)
	}
}
