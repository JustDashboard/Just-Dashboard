package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// replayLine is one request as the Docker Caddy ingress records it, with the
// credentials a browser sent: the record keeps none of them and the replay
// must never send them.
func replayLine(at time.Time, method, host, uri string, status int, seconds float64, tls bool) string {
	tlsField := ""
	if tls {
		tlsField = `,"tls":{"version":772}`
	}
	return fmt.Sprintf(`{"ts":%q,"msg":"handled request","request":{"method":%q,"host":%q,`+
		`"uri":%q,"remote_ip":"198.51.100.7","headers":{"User-Agent":["Mozilla/5.0"],"Cookie":["session=s3cr3t"],`+
		`"Authorization":["Bearer t0ken"]}%s},"status":%d,"size":120,"duration":%v}`+"\n",
		at.Format(time.RFC3339Nano), method, host, uri, tlsField, status, seconds)
}

func parsedEntry(t *testing.T, line string) accesslog.Entry {
	t.Helper()
	entry, ok := accesslog.Parse(line)
	if !ok {
		t.Fatalf("fixture line did not parse: %s", line)
	}
	return entry
}

func TestReplayPlanIsOffByDefaultAndValidated(t *testing.T) {
	t.Parallel()
	var off *ReplayPlan
	if off.on() || replayLabel(off) != "off" || validateReplayPlan(off) != nil {
		t.Fatal("a plan without a replay must be off and valid")
	}
	on := &ReplayPlan{Enabled: true}
	if on.requests() != 50 || on.statusDrift() != 2 || on.latencyFactor() != 2 || on.timeout() != time.Minute {
		t.Fatalf("defaults = %d, %g, %g, %s", on.requests(), on.statusDrift(), on.latencyFactor(), on.timeout())
	}
	if label := replayLabel(&ReplayPlan{Enabled: true, Requests: 120, Paths: []string{"/docs", "/blog"}}); label !=
		"120 requests · status drift ≤ 2% · p95 ≤ 2× · /docs /blog" {
		t.Fatalf("label = %q", label)
	}
	for _, valid := range []ReplayPlan{
		{Enabled: true, Requests: 500, MaxStatusDrift: 100, LatencyFactor: 10, TimeoutSeconds: 300},
		{Enabled: true, Requests: 1, MaxStatusDrift: 0.1, LatencyFactor: 1, TimeoutSeconds: 1, Paths: []string{"/", "/api/v1/"}},
	} {
		if err := validateReplayPlan(&valid); err != nil {
			t.Fatalf("%+v refused: %v", valid, err)
		}
	}
	for field, invalid := range map[string]ReplayPlan{
		"runtime.replay.requests":       {Requests: 501},
		"runtime.replay.paths[1]":       {Paths: []string{"/ok", "api"}},
		"runtime.replay.maxStatusDrift": {MaxStatusDrift: 101},
		"runtime.replay.latencyFactor":  {LatencyFactor: 0.5},
		"runtime.replay.timeoutSeconds": {TimeoutSeconds: 301},
	} {
		err := validateReplayPlan(&invalid)
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != field {
			t.Fatalf("%+v: error = %v, want a refusal of %s", invalid, err, field)
		}
	}
	for _, prefix := range []string{"/a b", "/a?b", "/a#b", "/a\nb", "/" + strings.Repeat("a", 256)} {
		if validateReplayPlan(&ReplayPlan{Paths: []string{prefix}}) == nil {
			t.Fatalf("prefix %q accepted", prefix)
		}
	}
	tooMany := make([]string, maxReplayPaths+1)
	for index := range tooMany {
		tooMany[index] = "/p" + strconv.Itoa(index)
	}
	if validateReplayPlan(&ReplayPlan{Paths: tooMany}) == nil {
		t.Fatal("an allow-list past its bound was accepted")
	}

	// The plan's own validation holds the replay to the same rules.
	plan := PlanConfiguration{
		Build:   BuildPlanConfig{Method: BuildImage},
		Runtime: RuntimePlanConfig{Image: "alpine:3", Strategy: StrategyStopFirst, Replay: &ReplayPlan{Enabled: true, Requests: 9000}},
	}
	var validation *ValidationError
	if err := plan.Validate(); !errors.As(err, &validation) || validation.Field != "runtime.replay.requests" {
		t.Fatalf("plan validation = %v", err)
	}
	plan.Runtime.Replay.Requests = 50
	if err := plan.Validate(); err != nil {
		t.Fatalf("a valid replay refused: %v", err)
	}

	// Stored as a copy with its prefixes trimmed and each kept once, and as
	// nothing at all when it asks for nothing.
	saved := &ReplayPlan{Enabled: true, Paths: []string{" /docs ", "/docs", "", "/blog"}}
	canonical := canonicalConfiguration(PlanConfiguration{Runtime: RuntimePlanConfig{Replay: saved}})
	if canonical.Runtime.Replay == saved || fmt.Sprint(canonical.Runtime.Replay.Paths) != "[/docs /blog]" ||
		fmt.Sprint(saved.Paths) != "[ /docs  /docs  /blog]" {
		t.Fatalf("canonical = %+v, saved = %+v", canonical.Runtime.Replay, saved)
	}
	if canonicalReplayPlan(&ReplayPlan{}) != nil || canonicalReplayPlan(&ReplayPlan{Requests: 80}) == nil {
		t.Fatal("an empty replay must store as nothing, and terms kept while it is off must stay")
	}
}

func TestReplaySelectionTakesTheNewestDistinctReadsTheApplicationAnswered(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	at := func(seconds int) time.Time { return now.Add(-time.Duration(seconds) * time.Second) }
	domains := []PlannedDomain{
		{Hostname: "app.example.test", HTTPS: true},
		{Hostname: "*.tenants.example.test", HTTPS: true},
		{Hostname: "www.app.example.test", HTTPS: true, RedirectTo: "app.example.test"},
	}
	lines := []string{
		// Newest first, as the record answers. The newest /pricing decides
		// what the candidate is held to.
		replayLine(at(1), "GET", "app.example.test", "/pricing", 200, 0.020, true),
		replayLine(at(2), "GET", "app.example.test", "/pricing", 404, 0.010, true),
		replayLine(at(3), "HEAD", "app.example.test", "/pricing", 200, 0.005, true),
		replayLine(at(4), "POST", "app.example.test", "/api/save", 200, 0.050, true),
		replayLine(at(5), "GET", "app.example.test", "/broken", 502, 0.050, true),
		replayLine(at(6), "GET", "app.example.test", "/account", 401, 0.010, true),
		replayLine(at(7), "GET", "app.example.test", "/admin", 403, 0.010, true),
		replayLine(at(8), "GET", "app.example.test", "/wp-login.php", 404, 0.002, true),
		replayLine(at(9), "GET", "app.example.test", "/reset?token=abc", 200, 0.010, true),
		replayLine(at(10), "GET", "app.example.test", "/callback?state=x&code=y", 302, 0.010, true),
		replayLine(at(11), "GET", "another.example.test", "/elsewhere", 200, 0.010, true),
		replayLine(at(12), "GET", "www.app.example.test", "/", 308, 0.001, true),
		replayLine(at(13), "GET", "app.example.test", "/plain", 308, 0.001, false),
		replayLine(at(14), "GET", "acme.tenants.example.test", "/dashboard", 200, 0.030, true),
		replayLine(at(15), "GET", "a.b.tenants.example.test", "/deep", 200, 0.030, true),
		replayLine(at(16), "GET", "app.example.test:443", "/search?q=shoes&page=2", 200, 0.040, true),
		replayLine(at(17), "GET", "app.example.test", "/docs/intro", 200, 0.015, true),
		replayLine(at(18), "GET", "app.example.test", "/missing", 404, 0.003, true),
		replayLine(at(19), "GET", "app.example.test", "/bad%0Aline", 200, 0.003, true),
	}
	entries := make([]accesslog.Entry, 0, len(lines))
	for _, line := range lines {
		entries = append(entries, parsedEntry(t, line))
	}
	plan := &ReplayPlan{Enabled: true}
	chosen, eligible := selectReplayRequests(entries, plan, domains, true)
	got := []string{}
	for _, request := range chosen {
		line := request.Method + " " + request.Host + " " + request.Path
		if request.Query != "" {
			line += "?" + request.Query
		}
		got = append(got, line+" "+strconv.Itoa(request.Status))
	}
	want := []string{
		"GET app.example.test /pricing 200",
		"HEAD app.example.test /pricing 200",
		"GET acme.tenants.example.test /dashboard 200",
		"GET app.example.test /search?q=shoes&page=2 200",
		"GET app.example.test /docs/intro 200",
		"GET app.example.test /missing 404",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") || eligible != len(want) {
		t.Fatalf("chosen (%d eligible):\n%s\nwant:\n%s", eligible, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !chosen[0].HTTPS || !chosen[0].Timed || chosen[0].Duration != 20 {
		t.Fatalf("the newest recording's answer = %+v", chosen[0])
	}

	// The count cuts the newest N; eligible still counts what qualified.
	chosen, eligible = selectReplayRequests(entries, &ReplayPlan{Enabled: true, Requests: 2}, domains, true)
	if len(chosen) != 2 || eligible != 6 || chosen[1].Method != http.MethodHead {
		t.Fatalf("limited = %+v, eligible %d", chosen, eligible)
	}
	// The allow-list keeps only paths under its prefixes.
	chosen, _ = selectReplayRequests(entries, &ReplayPlan{Enabled: true, Paths: []string{"/docs", "/search"}}, domains, true)
	if len(chosen) != 2 || chosen[0].Path != "/search" || chosen[1].Path != "/docs/intro" {
		t.Fatalf("allow-listed = %+v", chosen)
	}

	// nginx's combined format names no host and no TLS: its rows are the
	// first concrete served domain's, on the scheme that domain serves.
	combined := parsedEntry(t, `198.51.100.7 - - [`+now.Format("02/Jan/2006:15:04:05 -0700")+`] "GET /about HTTP/1.1" 200 512 "-" "Mozilla/5.0"`)
	chosen, _ = selectReplayRequests([]accesslog.Entry{combined}, plan, domains, false)
	if len(chosen) != 1 || chosen[0].Host != "app.example.test" || !chosen[0].HTTPS || chosen[0].Timed {
		t.Fatalf("combined = %+v", chosen)
	}
	// A plan whose only served domain is a wildcard has no name to give a
	// hostless row.
	if chosen, _ := selectReplayRequests([]accesslog.Entry{combined}, plan, domains[1:], false); len(chosen) != 0 {
		t.Fatalf("hostless row under a wildcard = %+v", chosen)
	}
}

func TestReplayComparisonJudgesStatusClassesAndTheP95(t *testing.T) {
	t.Parallel()
	answers := func(count, drifted int, recordedMs, candidateMs float64) []replayAnswer {
		out := make([]replayAnswer, 0, count)
		for index := 0; index < count; index++ {
			request := replayRequest{Method: "GET", Path: "/p/" + strconv.Itoa(index), Status: 200, Duration: recordedMs, Timed: true}
			answer := replayAnswer{request: request, Answered: true, Status: 200, Ms: candidateMs}
			if index < drifted {
				answer.Status = 404
			}
			out = append(out, answer)
		}
		return out
	}
	judge := func(plan *ReplayPlan, recorded bool, list []replayAnswer) ReplayReport {
		report := ReplayReport{LatencyRecorded: recorded}
		summarizeReplay(&report, plan, list)
		return report
	}
	plan := &ReplayPlan{Enabled: true}

	// One in fifty is the default line, and is within it.
	report := judge(plan, true, answers(50, 1, 20, 25))
	if report.Verdict.Tripped || report.Verdict.Code != ReplayVerdictWithinDrift || report.DriftPercent != 2 ||
		report.Drifted != 1 || !report.LatencyJudged || len(report.Drifts) != 1 || report.Drifts[0].CandidateStatus != 404 {
		t.Fatalf("1 of 50 = %+v", report)
	}
	// Two in fifty is past it.
	report = judge(plan, true, answers(50, 2, 20, 25))
	if !report.Verdict.Tripped || report.Verdict.Code != ReplayVerdictStatusDrift || report.DriftPercent != 4 ||
		!strings.Contains(report.Verdict.Reason, "2 of 50 replayed requests (4.0%)") {
		t.Fatalf("2 of 50 = %+v", report)
	}
	// A 3xx where the record has a 2xx is drift; a 404 for a recorded 404
	// is the same answer.
	same := []replayAnswer{
		{request: replayRequest{Path: "/gone", Status: 404}, Answered: true, Status: 410},
		{request: replayRequest{Path: "/home", Status: 200}, Answered: true, Status: 301},
	}
	if report := judge(&ReplayPlan{Enabled: true, MaxStatusDrift: 60}, true, same); report.Drifted != 1 || report.Drifts[0].Path != "/home" {
		t.Fatalf("class comparison = %+v", report)
	}
	// Nothing answered is its own verdict, whatever the line.
	unanswered := []replayAnswer{{request: replayRequest{Path: "/", Status: 200}, Error: "connection_failed"}}
	if report := judge(&ReplayPlan{Enabled: true, MaxStatusDrift: 100}, true, unanswered); report.Verdict.Code != ReplayVerdictUnanswered ||
		!strings.Contains(report.Verdict.Reason, "connection_failed") || report.Drifts[0].CandidateMs != nil {
		t.Fatalf("unanswered = %+v", report)
	}
	// The p95 rule is the rollback watch's: past the factor and a quarter of
	// a second slower. 20 ms becoming 200 ms is ten times slower and not a
	// regression; 20 ms becoming 400 ms is.
	if report := judge(plan, true, answers(20, 0, 20, 200)); report.Verdict.Tripped || *report.CandidateP95 != 200 {
		t.Fatalf("under the floor = %+v", report)
	}
	report = judge(plan, true, answers(20, 0, 20, 400))
	if report.Verdict.Code != ReplayVerdictLatency || *report.RecordedP95 != 20 || *report.CandidateP95 != 400 ||
		!strings.Contains(report.Verdict.Reason, "past the 2× the plan allows") {
		t.Fatalf("slow candidate = %+v", report)
	}
	if report := judge(&ReplayPlan{Enabled: true, LatencyFactor: 25}, true, answers(20, 0, 20, 400)); report.Verdict.Tripped {
		t.Fatalf("a factor the candidate is inside = %+v", report)
	}
	// Too few timed requests, or a record with no durations, leaves the p95
	// unjudged and says so.
	if report := judge(plan, true, answers(9, 0, 20, 4000)); report.LatencyJudged || report.Verdict.Tripped ||
		!strings.Contains(report.Verdict.Reason, "fewer than 10 requests were timed") {
		t.Fatalf("nine requests = %+v", report)
	}
	if report := judge(plan, false, answers(30, 0, 20, 4000)); report.LatencyJudged || report.Verdict.Tripped ||
		!strings.Contains(report.Verdict.Reason, "records no durations") {
		t.Fatalf("nginx record = %+v", report)
	}
	// The report names at most fifty drifted requests and counts them all.
	report = judge(plan, true, answers(200, 120, 20, 20))
	if report.Drifted != 120 || len(report.Drifts) != replayDriftListCap || report.DriftPercent != 60 {
		t.Fatalf("many drifted = %d named of %d", len(report.Drifts), report.Drifted)
	}
	// A replay the deadline cut short says so in the reason it fails with.
	cut := ReplayReport{LatencyRecorded: true, TimedOut: true, TimeoutSeconds: 5}
	summarizeReplay(&cut, plan, append(answers(4, 0, 20, 20), replayAnswer{request: replayRequest{Path: "/late", Status: 200}, Error: "out_of_time"}))
	if !cut.Verdict.Tripped || !strings.Contains(cut.Verdict.Reason, "ran out of its 5 s") {
		t.Fatalf("cut short = %+v", cut.Verdict)
	}
}

// replayedRequest is what the candidate's side saw of one replayed request.
type replayedRequest struct {
	Method, Host, Path, Query, Proto, ForwardedHost, Replay string
	Cookie, Authorization                               string
}

// replayCandidate is a candidate on loopback that answers by path and keeps
// what each request carried.
type replayCandidate struct {
	mu       sync.Mutex
	seen     []replayedRequest
	inFlight atomic.Int32
	peak     atomic.Int32
	answer   func(path string) int
}

func (c *replayCandidate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	now := c.inFlight.Add(1)
	defer c.inFlight.Add(-1)
	for {
		peak := c.peak.Load()
		if now <= peak || c.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	c.mu.Lock()
	c.seen = append(c.seen, replayedRequest{
		Method: r.Method, Host: r.Host, Path: r.URL.Path, Query: r.URL.RawQuery,
		Proto: r.Header.Get("X-Forwarded-Proto"), ForwardedHost: r.Header.Get("X-Forwarded-Host"),
		Replay: r.Header.Get("X-JD-Replay"), Cookie: r.Header.Get("Cookie"), Authorization: r.Header.Get("Authorization"),
	})
	c.mu.Unlock()
	status := c.answer(r.URL.Path)
	if status == http.StatusFound {
		// An answer pointing elsewhere is the answer; it is never followed.
		w.Header().Set("Location", "https://elsewhere.example.test/")
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(strings.Repeat("body ", 100)))
}

func (c *replayCandidate) requests() []replayedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]replayedRequest(nil), c.seen...)
}

func TestReplaySendsOnlyTheRecordedReadsToTheCandidateWithNoCredentials(t *testing.T) {
	t.Parallel()
	candidate := &replayCandidate{answer: func(path string) int {
		if path == "/moved" {
			return http.StatusFound
		}
		return http.StatusOK
	}}
	server := httptest.NewServer(candidate)
	defer server.Close()
	address := server.Listener.Addr().String()
	requests := []replayRequest{}
	for index := 0; index < 16; index++ {
		requests = append(requests, replayRequest{Method: "GET", Path: "/p/" + strconv.Itoa(index), Query: "q=a%20b", Host: "app.example.test", HTTPS: true, Status: 200})
	}
	requests = append(requests,
		replayRequest{Method: "HEAD", Path: "/p/head", Host: "app.example.test", Status: 200},
		replayRequest{Method: "GET", Path: "/moved", Host: "app.example.test", HTTPS: true, Status: 302},
		replayRequest{Method: "GET", Path: "/a b/ü", Host: "app.example.test", HTTPS: true, Status: 200},
	)
	answers, timedOut := replayAgainst(context.Background(), newReplayClient(), address, requests, replayConcurrency)
	if timedOut || len(answers) != len(requests) {
		t.Fatalf("answers = %d, timed out %v", len(answers), timedOut)
	}
	for index, answer := range answers {
		if !answer.Answered || answer.request.Path != requests[index].Path {
			t.Fatalf("answer %d = %+v", index, answer)
		}
	}
	if answers[17].Status != http.StatusFound {
		t.Fatalf("a redirect was followed: %+v", answers[17])
	}
	if peak := candidate.peak.Load(); peak > replayConcurrency {
		t.Fatalf("%d requests were in flight at once, the replay allows %d", peak, replayConcurrency)
	}
	seen := candidate.requests()
	if len(seen) != len(requests) {
		t.Fatalf("the candidate saw %d requests, want %d: nothing else may be fetched", len(seen), len(requests))
	}
	paths := map[string]bool{}
	for _, request := range seen {
		paths[request.Path] = true
		if request.Method != "GET" && request.Method != "HEAD" {
			t.Fatalf("replayed a %s", request.Method)
		}
		if request.Host != "app.example.test" || request.ForwardedHost != "app.example.test" || request.Replay != "1" {
			t.Fatalf("the candidate was not introduced as the domain: %+v", request)
		}
		if request.Cookie != "" || request.Authorization != "" {
			t.Fatalf("a credential was replayed: %+v", request)
		}
		if request.Path == "/p/head" && request.Proto != "http" || request.Path == "/p/0" && (request.Proto != "https" || request.Query != "q=a%20b") {
			t.Fatalf("scheme or query not as recorded: %+v", request)
		}
	}
	if !paths["/a b/ü"] {
		t.Fatalf("a decoded path did not reach the candidate as itself: %v", paths)
	}
}

// stalledClient never answers: each request waits for its deadline.
type stalledClient struct {
	inFlight, peak atomic.Int32
}

func (c *stalledClient) Do(request *http.Request) (*http.Response, error) {
	now := c.inFlight.Add(1)
	defer c.inFlight.Add(-1)
	for {
		peak := c.peak.Load()
		if now <= peak || c.peak.CompareAndSwap(peak, now) {
			break
		}
	}
	<-request.Context().Done()
	return nil, request.Context().Err()
}

func TestReplayIsBoundedInTimeAndUnansweredRequestsCountAsDrift(t *testing.T) {
	t.Parallel()
	requests := make([]replayRequest, 12)
	for index := range requests {
		requests[index] = replayRequest{Method: "GET", Path: "/p/" + strconv.Itoa(index), Host: "app.example.test", Status: 200}
	}
	client := &stalledClient{}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	answers, timedOut := replayAgainst(ctx, client, "127.0.0.1:9", requests, replayConcurrency)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("the replay took %s past a 150 ms deadline", elapsed)
	}
	if !timedOut || len(answers) != len(requests) || client.peak.Load() > replayConcurrency {
		t.Fatalf("answers %d, timed out %v, peak %d", len(answers), timedOut, client.peak.Load())
	}
	unsent := 0
	for _, answer := range answers {
		if answer.Answered {
			t.Fatalf("a stalled request counted as answered: %+v", answer)
		}
		if answer.Error == "out_of_time" {
			unsent++
		}
	}
	if unsent < len(requests)-replayConcurrency-1 {
		t.Fatalf("only %d of %d requests were left unsent", unsent, len(requests))
	}
	report := ReplayReport{TimedOut: timedOut, TimeoutSeconds: 1}
	summarizeReplay(&report, &ReplayPlan{Enabled: true}, answers)
	if report.Verdict.Code != ReplayVerdictUnanswered || report.Drifted != len(requests) {
		t.Fatalf("verdict = %+v", report)
	}
}

// replayStepFixture is an environment with a live first release and a
// started candidate of a second, the candidate answering on loopback.
type replayStepFixture struct {
	*releaseStoreFixture
	owner     *orderingRuntimeOwner
	executor  *NormalizedStepExecutor
	run       *EngineRun
	lease     QueueLease
	first     *ReleaseWithArtifacts
	candidate *ReleaseWithArtifacts
	output    *recordingStepOutput
}

func newReplayStepFixture(t *testing.T, replay *ReplayPlan, domains []PlannedDomain, record RequestRecord, port int) *replayStepFixture {
	t.Helper()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{}, Replay: replay,
	}
	ctx := context.Background()
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	firstRun, firstLease := fixture.claimedRun(t, 1)
	first := createRuntimeCandidateWithDomains(t, fixture, *firstRun, firstLease, plan, "replay-v1", nil, domains)
	if _, err := fixture.runs.RecordCandidateRuntime(ctx, *firstRun, firstLease.Token, ReleaseRuntimeInput{
		ReleaseID: first.Release.ID, Kind: "container", RuntimeID: "replay-live", Host: "127.0.0.1", Port: 32001,
		Metadata: json.RawMessage(`{"version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runs.ActivateCandidate(ctx, firstRun.ID, firstLease.Token, first.Release.ID); err != nil {
		t.Fatal(err)
	}
	fixture.finishActivatedRun(t, firstRun.ID, first.Release.ID, firstLease.Token)

	fixture.now = fixture.now.Add(time.Minute)
	fixture.addPlanWithRuntime(t, 2, strings.Repeat("b", 40), plan)
	run, lease := fixture.claimedRun(t, 2)
	candidate := createRuntimeCandidateWithDomains(t, fixture, *run, lease, plan, "replay-v2", nil, domains)
	if _, err := fixture.runs.RecordCandidateRuntime(ctx, *run, lease.Token, ReleaseRuntimeInput{
		ReleaseID: candidate.Release.ID, Kind: "container", RuntimeID: "replay-candidate", Host: "127.0.0.1", Port: port,
		Metadata: json.RawMessage(`{"version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	owner := &orderingRuntimeOwner{running: map[string]bool{"replay-live": true, "replay-candidate": true}, allowConcurrent: true}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner}
	if record != nil {
		executor.WithRequestRecord(record)
	}
	return &replayStepFixture{
		releaseStoreFixture: fixture, owner: owner, executor: executor, run: run, lease: lease,
		first: first, candidate: candidate, output: &recordingStepOutput{},
	}
}

func (f *replayStepFixture) replay(t *testing.T) (StepResult, ReplayReport) {
	t.Helper()
	result := f.executor.replayTraffic(context.Background(), StepExecution{
		Run: *f.run, ClaimToken: f.lease.Token, Output: f.output, Step: RunStep{Key: StepReplayTraffic},
	}, mustExecutionPlan(t, f.releaseStoreFixture, *f.run))
	var report ReplayReport
	if len(result.Evidence) > 0 {
		if err := json.Unmarshal(result.Evidence, &report); err != nil {
			t.Fatalf("evidence %s: %v", result.Evidence, err)
		}
	}
	return result, report
}

// liveUntouched says the first release is still the live one and still
// running, which is what a stopped replay must leave.
func (f *replayStepFixture) liveUntouched(t *testing.T) {
	t.Helper()
	live, err := f.runs.LiveRelease(context.Background(), f.envID)
	if err != nil || live.Release.ID != f.first.Release.ID {
		t.Fatalf("live release = %#v, %v", live, err)
	}
	f.owner.mu.Lock()
	defer f.owner.mu.Unlock()
	if !f.owner.running["replay-live"] {
		t.Fatal("the live release was stopped")
	}
}

func replayRecordOf(lines ...string) *accesslog.Store {
	return storeOver(&memoryRecord{data: []byte(strings.Join(lines, ""))}, caddyFacts)
}

// thirtyRequests is a record of thirty distinct pages the live release
// answered 200 in about 20 ms, /pricing the newest, oldest first in the file
// as a log is written.
func thirtyRequests(host string) []string {
	now := time.Now().UTC()
	lines := []string{}
	for index := 29; index >= 0; index-- {
		path := "/p/" + strconv.Itoa(index)
		if index == 0 {
			path = "/pricing"
		}
		lines = append(lines, replayLine(now.Add(-time.Duration(index+1)*time.Second), "GET", host, path, 200, 0.020, true))
	}
	// Writes and the ingress's own redirect are in the record and are never
	// replayed.
	lines = append(lines,
		replayLine(now.Add(-500*time.Millisecond), "POST", host, "/api/orders", 201, 0.080, true),
		replayLine(now.Add(-400*time.Millisecond), "GET", host, "/plain", 308, 0.001, false))
	return lines
}

func TestReplayStepStopsADriftingCandidateAndLeavesTheLiveReleaseUntouched(t *testing.T) {
	t.Parallel()
	candidate := &replayCandidate{answer: func(path string) int {
		if path == "/pricing" {
			return http.StatusNotFound
		}
		return http.StatusOK
	}}
	server := httptest.NewServer(candidate)
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	portNumber, _ := strconv.Atoi(port)
	domains := []PlannedDomain{{Hostname: "app.example.test", HTTPS: true}}
	f := newReplayStepFixture(t, &ReplayPlan{Enabled: true}, domains, replayRecordOf(thirtyRequests("app.example.test")...), portNumber)

	result, report := f.replay(t)
	if result.State != StepFailed || result.ErrorCode != "replay_status_drift" ||
		!strings.Contains(result.ErrorMessage, "the live release was not touched") {
		t.Fatalf("drifting candidate = %+v", result)
	}
	if !report.Replayed || report.Requests != 30 || report.Answered != 30 || report.Drifted != 1 ||
		report.Target != server.Listener.Addr().String() || fmt.Sprint(report.Domains) != "[app.example.test]" {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Drifts) != 1 || report.Drifts[0].Path != "/pricing" || report.Drifts[0].RecordedStatus != 200 ||
		report.Drifts[0].CandidateStatus != 404 || report.Drifts[0].Method != "GET" {
		t.Fatalf("drifts = %+v", report.Drifts)
	}
	if report.Recovery == nil || !report.Recovery.CandidateStopped || !result.Recovered {
		t.Fatalf("recovery = %+v, recovered %v", report.Recovery, result.Recovered)
	}
	f.liveUntouched(t)
	f.owner.mu.Lock()
	candidateRunning := f.owner.running["replay-candidate"]
	f.owner.mu.Unlock()
	if candidateRunning {
		t.Fatal("the drifting candidate was left running")
	}
	runtime, err := f.runs.RuntimeForRelease(context.Background(), f.candidate.Release.ID)
	if err != nil || runtime.State != "failed" {
		t.Fatalf("candidate runtime = %+v, %v", runtime, err)
	}
	transcript := f.output.joined()
	for _, want := range []string{
		"Replaying the last 30 distinct GET/HEAD requests recorded for app.example.test against the candidate at " + server.Listener.Addr().String(),
		"GET /pricing: recorded 200, candidate 404",
		"1 of 30 replayed requests (3.3%) answered in another status class",
	} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, transcript)
		}
	}
	for _, request := range candidate.requests() {
		if request.Method != "GET" || request.Cookie != "" || request.Authorization != "" || request.Path == "/plain" || request.Path == "/api/orders" {
			t.Fatalf("replayed %+v", request)
		}
	}
	if strings.Contains(string(result.Evidence), "body body") || strings.Contains(string(result.Evidence), "s3cr3t") {
		t.Fatal("an answer's body or a recorded credential reached the evidence")
	}
}

func TestReplayStepPassesACandidateThatAnswersLikeTheLiveRelease(t *testing.T) {
	t.Parallel()
	candidate := &replayCandidate{answer: func(string) int { return http.StatusOK }}
	server := httptest.NewServer(candidate)
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	portNumber, _ := strconv.Atoi(port)
	domains := []PlannedDomain{{Hostname: "app.example.test", HTTPS: true}}
	f := newReplayStepFixture(t, &ReplayPlan{Enabled: true, Requests: 10}, domains, replayRecordOf(thirtyRequests("app.example.test")...), portNumber)
	result, report := f.replay(t)
	if result.State != StepPassed || report.Verdict.Tripped || report.Requests != 10 || report.Eligible != 30 ||
		report.Verdict.Code != ReplayVerdictWithinDrift || !report.LatencyJudged || report.Recovery != nil {
		t.Fatalf("passing candidate = %+v, %+v", result, report)
	}
	if len(candidate.requests()) != 10 {
		t.Fatalf("the candidate saw %d requests, the plan asked for 10", len(candidate.requests()))
	}
	f.owner.mu.Lock()
	running := f.owner.running["replay-candidate"]
	f.owner.mu.Unlock()
	if !running {
		t.Fatal("a passing replay stopped the candidate")
	}
}

// failingRecord is a record the ingress could not be asked for.
type failingRecord struct{}

func (failingRecord) Window(context.Context, string, accesslog.Filter) (accesslog.Window, error) {
	return accesslog.Window{}, errors.New("dial unix /var/run/docker.sock: permission denied")
}

func TestReplayStepSkipsWithItsReasonAndNeverSendsWhenItCannotCompare(t *testing.T) {
	t.Parallel()
	candidate := &replayCandidate{answer: func(string) int { return http.StatusOK }}
	server := httptest.NewServer(candidate)
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	portNumber, _ := strconv.Atoi(port)
	served := []PlannedDomain{{Hostname: "app.example.test", HTTPS: true}}
	record := replayRecordOf(thirtyRequests("app.example.test")...)

	cases := []struct {
		name    string
		replay  *ReplayPlan
		domains []PlannedDomain
		record  RequestRecord
		state   StepState
		reason  string
	}{
		{"off", nil, served, record, StepSkipped, "traffic replay is turned off for this plan"},
		{"turned off", &ReplayPlan{Requests: 80}, served, record, StepSkipped, "turned off"},
		{"no record", &ReplayPlan{Enabled: true}, served, nil, StepSkipped, "keeps no request record"},
		{"no domain", &ReplayPlan{Enabled: true}, nil, record, StepSkipped, "serves no domain"},
		{"only a redirect", &ReplayPlan{Enabled: true}, []PlannedDomain{{Hostname: "www.app.example.test", RedirectTo: "app.example.test"}}, record, StepSkipped, "serves no domain"},
		{"empty record", &ReplayPlan{Enabled: true}, served, replayRecordOf(), StepSkipped, "no request has been recorded"},
		{"unreadable record", &ReplayPlan{Enabled: true}, served, failingRecord{}, StepWarning, "could not be read"},
		{"nothing eligible", &ReplayPlan{Enabled: true, Paths: []string{"/docs"}}, served, record, StepSkipped, "no eligible GET or HEAD request"},
		{"another site's record", &ReplayPlan{Enabled: true}, []PlannedDomain{{Hostname: "other.example.test", HTTPS: true}}, record, StepSkipped, "no eligible GET or HEAD request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newReplayStepFixture(t, tc.replay, tc.domains, tc.record, portNumber)
			result, report := f.replay(t)
			if result.State != tc.state || !strings.Contains(report.Reason, tc.reason) || report.Replayed {
				t.Fatalf("%s = %+v, %+v", tc.name, result, report)
			}
			if !strings.Contains(f.output.joined(), "Not replayed: ") {
				t.Fatalf("the transcript does not say why: %s", f.output.joined())
			}
			if strings.Contains(string(result.Evidence), "permission denied") {
				t.Fatal("a transport error reached the evidence")
			}
			f.owner.mu.Lock()
			running := f.owner.running["replay-candidate"]
			f.owner.mu.Unlock()
			if !running {
				t.Fatal("a replay that did not run stopped the candidate")
			}
		})
	}
	if len(candidate.requests()) != 0 {
		t.Fatalf("a replay that skipped sent %d requests", len(candidate.requests()))
	}

	// A restart has no candidate to compare with the live release.
	f := newReplayStepFixture(t, &ReplayPlan{Enabled: true}, served, record, portNumber)
	f.run.Operation = OperationRestart
	if result, report := f.replay(t); result.State != StepSkipped || !strings.Contains(report.Reason, "no candidate release") {
		t.Fatalf("restart = %+v", result)
	}
}

func TestReplayTrafficSitsBetweenTheChecksAndTheCutoverAndResumesAfterARestart(t *testing.T) {
	t.Parallel()
	for name, keys := range map[string][]StepKey{"deploy": DefaultStepKeys, "promotion": PromotionStepKeys} {
		at := -1
		for index, key := range keys {
			if key == StepReplayTraffic {
				at = index
			}
		}
		// The canary comes after it and right before the cutover: a replay
		// is done before anything routes to the candidate, and a canary is
		// the candidate's first real traffic.
		if at <= 0 || at+2 >= len(keys) || keys[at-1] != StepVerifySmoke || keys[at+1] != StepCanary || keys[at+2] != StepActivate {
			t.Fatalf("a %s's replay is not between the smoke checks and the canary before the cutover: %v", name, keys)
		}
	}
	if !validStepKey(StepReplayTraffic) || stepLabel(StepReplayTraffic, OperationDeploy) != "Traffic replay" {
		t.Fatal("the step is not a known step with its label")
	}
	decision := (&NormalizedStepReconciler{}).Reconcile(context.Background(), ReconcileRequest{Step: &RunStep{Key: StepReplayTraffic}})
	if decision.Action != ReconcileResume {
		t.Fatalf("an interrupted replay = %+v", decision)
	}
	if causeTitle("replay_status_drift") == "" || causeTitle("replay_latency") == "" || causeTitle("replay_unanswered") == "" {
		t.Fatal("a replay failure has no title")
	}
}

func TestReplayPreflightSaysWhatItWillHoldTheCandidateTo(t *testing.T) {
	t.Parallel()
	served := []PlannedDomain{{Hostname: "app.example.test", HTTPS: true}}
	configuration := func(replay *ReplayPlan, domains []PlannedDomain) PlanConfiguration {
		return PlanConfiguration{Runtime: RuntimePlanConfig{Replay: replay}, Domains: domains}
	}
	if findings := replayFindings(configuration(nil, served), HostObservation{}); len(findings) != 0 {
		t.Fatalf("off = %+v", findings)
	}
	findings := replayFindings(configuration(&ReplayPlan{Enabled: true, Requests: 80}, served), HostObservation{
		Domains: []DomainObservation{{Hostname: "app.example.test", ProxyAvailable: true}},
	})
	if len(findings) != 1 || findings[0].Code != "replay_before_cutover" || findings[0].Severity != PreflightPass ||
		findings[0].FieldID != "runtime.replay" || !strings.Contains(findings[0].Title, "last 80 recorded requests") {
		t.Fatalf("on = %+v", findings)
	}
	for name, observation := range map[string]HostObservation{
		"no proxy records the name": {Domains: []DomainObservation{{Hostname: "app.example.test", ProxyAvailable: false}}},
	} {
		findings := replayFindings(configuration(&ReplayPlan{Enabled: true}, served), observation)
		if len(findings) != 1 || findings[0].Code != "replay_without_record" || findings[0].Severity != PreflightWarning {
			t.Fatalf("%s = %+v", name, findings)
		}
	}
	findings = replayFindings(configuration(&ReplayPlan{Enabled: true}, []PlannedDomain{{Hostname: "www.app.example.test", RedirectTo: "app.example.test"}}), HostObservation{})
	if len(findings) != 1 || findings[0].Code != "replay_without_record" || findings[0].Severity != PreflightWarning {
		t.Fatalf("redirect only = %+v", findings)
	}
}

func TestReleaseComparisonShowsTheReplayTerms(t *testing.T) {
	t.Parallel()
	from := runtimeReleaseSnapshot{Plan: RuntimePlanConfig{Strategy: StrategyBlueGreen}}
	to := runtimeReleaseSnapshot{Plan: RuntimePlanConfig{Strategy: StrategyBlueGreen, Replay: &ReplayPlan{Enabled: true, Paths: []string{"/docs"}}}}
	detail := compareRuntimeSnapshots(from, to, false, false)
	for _, field := range detail.Fields {
		if field.Field == "traffic replay" {
			if !field.Changed || field.From != "off" || field.To != "50 requests · status drift ≤ 2% · p95 ≤ 2× · /docs" {
				t.Fatalf("replay field = %+v", field)
			}
			return
		}
	}
	t.Fatalf("no traffic replay field in %+v", detail.Fields)
}
