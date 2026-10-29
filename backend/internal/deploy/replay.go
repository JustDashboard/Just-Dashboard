package deploy

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

// Traffic replay before the cutover.
//
// A readiness check asks the candidate one question — usually "does /health
// answer 200" — and a candidate that answers it goes live. A release that
// broke one route, renamed an API path, lost a static directory or started
// redirecting every page answers that question as well as the last one did,
// and the first anyone hears of it is a visitor. The ingress already holds
// the answer in its access log: what people actually asked for, and what the
// live release said. This step takes the last distinct GET and HEAD requests
// recorded for the environment's domains, sends them to the candidate — and
// to nothing else: the candidate's own address, never the public route — and
// compares the candidate's status class and p95 with the live release's own
// for the same requests. A candidate that drifts past the plan's lines is
// stopped and the live release is left exactly as it was, through the same
// recovery a failed readiness check takes.
//
// Only reads are replayed, and only as the stranger the proxy sees: no
// cookie, no Authorization, no credential the original request carried, no
// body — the record never held any of those, and this never invents them.
// What comes back is read for its status and its timing and discarded; the
// report lists requests by method, path and status, never an answer's body.
// The work is bounded three ways: how many requests (at most 500), how many
// at once (four) and how long (the plan's timeout).

const (
	// DefaultReplayRequests is how many distinct requests a plan that names
	// no count replays, and MaxReplayRequests the most it may ask for.
	DefaultReplayRequests = 50
	MaxReplayRequests     = 500
	// DefaultReplayStatusDrift is the share of replayed requests, in
	// percent, whose status class may differ from the record before the
	// release stops: one in fifty.
	DefaultReplayStatusDrift = 2.0
	// DefaultReplayLatencyFactor multiplies the recorded p95 of the replayed
	// requests; the candidate's p95 over the same requests may not exceed it.
	DefaultReplayLatencyFactor = 2.0
	// DefaultReplayTimeoutSeconds bounds the whole replay; a request not
	// answered by then counts as drift.
	DefaultReplayTimeoutSeconds = 60
	MaxReplayTimeoutSeconds     = 300
	// maxReplayPaths bounds the allow-list of path prefixes.
	maxReplayPaths = 32
	// replayConcurrency is how many requests are in flight at once. Four is
	// enough to finish fifty requests in a few seconds and few enough that
	// the replay never looks like load to a candidate sized for one proxy.
	replayConcurrency = 4
	// replayRequestTimeout bounds one request within the step's own deadline.
	replayRequestTimeout = 10 * time.Second
	// replayReadWindow is how far back the record is read; replayReadLimit
	// how many of its newest rows selection starts from.
	replayReadWindow = 24 * time.Hour
	replayReadLimit  = 5000
	// replayLatencyMinSamples is the fewest requests timed on both sides a
	// p95 is compared over: a percentile of three requests is one request.
	replayLatencyMinSamples = 10
	// replayDriftListCap bounds the drifted requests the report names.
	replayDriftListCap = 50
	// replayBodyLimit is how much of an answer is read, and discarded, so
	// the measured time covers the answer and not only its headers.
	replayBodyLimit = 256 << 10
	// replayMaxPathBytes is the longest recorded request line replayed.
	replayMaxPathBytes = 2048
)

// ReplayPlan is the plan's terms for the replay. Zero values take the
// defaults above, so a plan that only turns it on gets the terms that make
// sense for almost everyone.
type ReplayPlan struct {
	Enabled bool `json:"enabled"`
	// Requests is how many distinct GET/HEAD requests are replayed, newest
	// first; 0 is DefaultReplayRequests.
	Requests int `json:"requests,omitempty"`
	// Paths, when any is named, keeps only recorded requests whose path
	// begins with one of these prefixes: the public part of a site whose
	// other pages answer by who asked.
	Paths []string `json:"paths,omitempty"`
	// MaxStatusDrift is the percent of replayed requests whose status class
	// may differ from the record; 0 is DefaultReplayStatusDrift.
	MaxStatusDrift float64 `json:"maxStatusDrift,omitempty"`
	// LatencyFactor is what the candidate's p95 may be of the recorded p95
	// over the same requests; 0 is DefaultReplayLatencyFactor.
	LatencyFactor float64 `json:"latencyFactor,omitempty"`
	// TimeoutSeconds bounds the whole replay; 0 is DefaultReplayTimeoutSeconds.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

func (p *ReplayPlan) on() bool { return p != nil && p.Enabled }

func (p *ReplayPlan) requests() int {
	if p == nil || p.Requests <= 0 {
		return DefaultReplayRequests
	}
	return p.Requests
}

func (p *ReplayPlan) statusDrift() float64 {
	if p == nil || p.MaxStatusDrift <= 0 {
		return DefaultReplayStatusDrift
	}
	return p.MaxStatusDrift
}

func (p *ReplayPlan) latencyFactor() float64 {
	if p == nil || p.LatencyFactor <= 0 {
		return DefaultReplayLatencyFactor
	}
	return p.LatencyFactor
}

func (p *ReplayPlan) timeout() time.Duration {
	if p == nil || p.TimeoutSeconds <= 0 {
		return DefaultReplayTimeoutSeconds * time.Second
	}
	return time.Duration(p.TimeoutSeconds) * time.Second
}

// validateReplayPlan is called from PlanConfiguration.Validate beside the
// other runtime fields. A nil plan is off and always valid.
func validateReplayPlan(plan *ReplayPlan) error {
	if plan == nil {
		return nil
	}
	if plan.Requests < 0 || plan.Requests > MaxReplayRequests {
		return invalidField("runtime.replay.requests", "traffic replay sends between 1 and %d requests, or zero for %d", MaxReplayRequests, DefaultReplayRequests)
	}
	if len(plan.Paths) > maxReplayPaths {
		return invalidField("runtime.replay.paths", "traffic replay keeps at most %d path prefixes", maxReplayPaths)
	}
	for index, prefix := range plan.Paths {
		if !strings.HasPrefix(prefix, "/") || len(prefix) > 256 || strings.ContainsAny(prefix, "\x00\r\n\t ?#") {
			return invalidField(fmt.Sprintf("runtime.replay.paths[%d]", index), "a replayed path prefix begins with / and carries no space, query or fragment")
		}
	}
	if plan.MaxStatusDrift < 0 || plan.MaxStatusDrift > 100 || math.IsNaN(plan.MaxStatusDrift) || math.IsInf(plan.MaxStatusDrift, 0) {
		return invalidField("runtime.replay.maxStatusDrift", "the status drift allowed is between 0 and 100 percent, or zero for %g", DefaultReplayStatusDrift)
	}
	if plan.LatencyFactor != 0 && (plan.LatencyFactor < 1 || plan.LatencyFactor > 10 || math.IsNaN(plan.LatencyFactor) || math.IsInf(plan.LatencyFactor, 0)) {
		return invalidField("runtime.replay.latencyFactor", "the p95 factor is between 1 and 10, or zero for %g", DefaultReplayLatencyFactor)
	}
	if plan.TimeoutSeconds < 0 || plan.TimeoutSeconds > MaxReplayTimeoutSeconds {
		return invalidField("runtime.replay.timeoutSeconds", "the replay's time limit is between 1 and %d seconds, or zero for %d", MaxReplayTimeoutSeconds, DefaultReplayTimeoutSeconds)
	}
	return nil
}

// canonicalReplayPlan is the plan's replay as the store keeps it: a copy, so
// canonicalising never writes through to the caller's plan; its prefixes
// trimmed, each once, in the order given; and nothing at all for terms that
// ask for nothing, so a plan that never chose a replay keeps the bytes and
// the digest it had.
func canonicalReplayPlan(plan *ReplayPlan) *ReplayPlan {
	if plan == nil {
		return nil
	}
	copied := *plan
	copied.Paths = nil
	seen := map[string]bool{}
	for _, prefix := range plan.Paths {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" || seen[prefix] {
			continue
		}
		seen[prefix] = true
		copied.Paths = append(copied.Paths, prefix)
	}
	if !copied.Enabled && copied.Requests == 0 && len(copied.Paths) == 0 && copied.MaxStatusDrift == 0 &&
		copied.LatencyFactor == 0 && copied.TimeoutSeconds == 0 {
		return nil
	}
	return &copied
}

// replayLabel is the plan's terms in one line, for the release comparison
// and the preflight finding.
func replayLabel(plan *ReplayPlan) string {
	if !plan.on() {
		return "off"
	}
	parts := []string{
		fmt.Sprintf("%d requests", plan.requests()),
		"status drift ≤ " + trimFloat(plan.statusDrift()) + "%",
		"p95 ≤ " + trimFloat(plan.latencyFactor()) + "×",
	}
	if len(plan.Paths) > 0 {
		parts = append(parts, strings.Join(plan.Paths, " "))
	}
	return strings.Join(parts, " · ")
}

func trimFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// ReplayReport is the step's evidence: what was read, what was sent, how the
// candidate answered against the record, and the verdict. A step that only
// recorded why it did not replay has Replayed false and a Reason.
type ReplayReport struct {
	Replayed bool   `json:"replayed"`
	Reason   string `json:"reason,omitempty"`
	// Domains are the served hostnames whose record was read; Target the
	// candidate's own address the requests were sent to.
	Domains []string `json:"domains,omitempty"`
	Target  string   `json:"target,omitempty"`
	// Read is how many rows the record answered with, Eligible how many
	// distinct requests qualified before the plan's count cut them,
	// Requests how many were sent, Answered how many the candidate answered.
	Read     int `json:"read"`
	Eligible int `json:"eligible"`
	Requests int `json:"requests"`
	Answered int `json:"answered"`
	// Drifted counts requests the candidate answered in another status
	// class than the record, or not at all; DriftPercent is its share.
	Drifted        int     `json:"drifted"`
	DriftPercent   float64 `json:"driftPercent"`
	MaxStatusDrift float64 `json:"maxStatusDrift"`
	LatencyFactor  float64 `json:"latencyFactor"`
	// LatencyRecorded says the ingress records durations at all;
	// LatencyJudged that enough requests were timed on both sides for the
	// p95 rule to have anything to stand on.
	LatencyRecorded bool          `json:"latencyRecorded"`
	LatencyJudged   bool          `json:"latencyJudged"`
	RecordedP95     *float64      `json:"recordedP95,omitempty"`
	CandidateP95    *float64      `json:"candidateP95,omitempty"`
	Verdict         ReplayVerdict `json:"verdict"`
	Drifts          []ReplayDrift `json:"drifts"`
	Concurrency     int           `json:"concurrency"`
	TimeoutSeconds  int           `json:"timeoutSeconds"`
	TimedOut        bool          `json:"timedOut"`
	DurationMS      int64         `json:"durationMs"`
	// Recovery is what was done about the candidate when the verdict
	// stopped the release: the same account a failed health gate gives.
	Recovery *recoveryEvidence `json:"recovery,omitempty"`
}

// ReplayDrift is one request the candidate answered differently: the
// request line as the record has it, both statuses, and both timings.
// CandidateStatus 0 is no answer, with Error saying why in a word.
type ReplayDrift struct {
	Method          string   `json:"method"`
	Path            string   `json:"path"`
	Query           string   `json:"query,omitempty"`
	RecordedStatus  int      `json:"recordedStatus"`
	CandidateStatus int      `json:"candidateStatus"`
	Error           string   `json:"error,omitempty"`
	RecordedMs      *float64 `json:"recordedMs,omitempty"`
	CandidateMs     *float64 `json:"candidateMs,omitempty"`
}

// ReplayVerdict is what the comparison decided, in the words the run page
// and the step's error carry.
type ReplayVerdict struct {
	Code    string `json:"code"`
	Tripped bool   `json:"tripped"`
	Reason  string `json:"reason"`
}

// Verdict codes. The step's error code is "replay_" + the tripped one.
const (
	ReplayVerdictWithinDrift = "within_drift"
	ReplayVerdictStatusDrift = "status_drift"
	ReplayVerdictLatency     = "latency"
	ReplayVerdictUnanswered  = "unanswered"
)

// replayRequest is one recorded request chosen to be sent again, with what
// the live release answered.
type replayRequest struct {
	Method string
	Path   string
	Query  string
	// Host is the Host header sent: the recorded one, or the plan's first
	// served domain when the record carries none (nginx's combined format).
	Host  string
	HTTPS bool
	// Status and Duration are the live release's recorded answer.
	Status   int
	Duration float64
	Timed    bool
}

// replayAnswer is what the candidate said to one replayed request.
type replayAnswer struct {
	request  replayRequest
	Answered bool
	Status   int
	Error    string
	Ms       float64
}

// servedDomains are the plan's domains that serve the application: a
// redirecting name's record is redirects, which say nothing about either
// release.
func servedDomains(domains []PlannedDomain) []PlannedDomain {
	served := []PlannedDomain{}
	for _, domain := range domains {
		if domain.Redirects() || strings.TrimSpace(domain.Hostname) == "" {
			continue
		}
		served = append(served, domain)
	}
	return served
}

// replayHost decides which served domain a recorded request belongs to, and
// the Host header to replay it with. A record without a host (nginx's stock
// format) is taken as the first concrete served domain's; a host no served
// domain names is another site's, or a redirecting name's, and is not
// replayed.
func replayHost(recorded string, served []PlannedDomain) (PlannedDomain, string, bool) {
	host := strings.ToLower(strings.TrimSpace(recorded))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		for _, domain := range served {
			if !strings.HasPrefix(domain.Hostname, "*.") {
				return domain, strings.ToLower(domain.Hostname), true
			}
		}
		return PlannedDomain{}, "", false
	}
	for _, domain := range served {
		name := strings.ToLower(domain.Hostname)
		if name == host {
			return domain, host, true
		}
		if strings.HasPrefix(name, "*.") {
			suffix := name[1:]
			if label := strings.TrimSuffix(host, suffix); label != host && label != "" && !strings.Contains(label, ".") {
				return domain, host, true
			}
		}
	}
	return PlannedDomain{}, "", false
}

// replaySecretQueryKeys are the parameter names a query carries a credential
// under. A request with one is never replayed: the record kept the value,
// and sending it again would be exactly the leak this step promises not to
// make. Short names match whole; long ones match wherever they appear.
var (
	replaySecretQueryWords = []string{"token", "secret", "password", "passwd", "signature", "session", "apikey", "api_key", "api-key", "auth", "credential", "bearer"}
	replaySecretQueryNames = map[string]bool{"key": true, "code": true, "sig": true, "sid": true, "jwt": true, "otp": true, "nonce": true, "state": true, "ticket": true, "pwd": true, "pass": true}
)

func queryCarriesCredential(query string) bool {
	if query == "" {
		return false
	}
	for _, pair := range strings.Split(query, "&") {
		name, _, _ := strings.Cut(pair, "=")
		if decoded, err := url.QueryUnescape(name); err == nil {
			name = decoded
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if replaySecretQueryNames[name] {
			return true
		}
		for _, word := range replaySecretQueryWords {
			if strings.Contains(name, word) {
				return true
			}
		}
	}
	return false
}

// replayable says whether one recorded request may be sent again at all:
// a read, answered by the application rather than by who asked, and not a
// scanner's probe or a credential-carrying query.
func replayable(entry accesslog.Entry) bool {
	if entry.Method != http.MethodGet && entry.Method != http.MethodHead {
		return false
	}
	// 5xx is the live release failing, not an answer to hold a candidate
	// to; 401, 403 and 407 were decided by who asked, and the replay asks
	// as nobody.
	if entry.Status < 200 || entry.Status >= 500 || entry.Status == 401 || entry.Status == 403 || entry.Status == 407 {
		return false
	}
	if entry.Path == "" || !strings.HasPrefix(entry.Path, "/") || len(entry.Path)+len(entry.Query) > replayMaxPathBytes ||
		!replayablePath(entry.Path) || !replayableQuery(entry.Query) {
		return false
	}
	if accesslog.IsProbe(entry) || queryCarriesCredential(entry.Query) {
		return false
	}
	return true
}

// replayablePath refuses a decoded path with a control character in it: the
// record decodes the path for its tables, and a byte that cannot travel in a
// request line is a request nobody's browser sent.
func replayablePath(path string) bool {
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// replayableQuery takes the query as the record kept it, still encoded, and
// refuses one that would not travel as it is: a space, a fragment, a control
// or a non-ASCII byte means the line was not what a client sent, and
// re-encoding it would replay a different request.
func replayableQuery(query string) bool {
	for index := 0; index < len(query); index++ {
		if b := query[index]; b <= 0x20 || b >= 0x7f || b == '#' {
			return false
		}
	}
	return true
}

// selectReplayRequests takes the newest distinct requests worth replaying
// from a record read newest first: the method, host, path and query together
// name a request, and its newest recording decides the status and duration
// the candidate is held to. recordsTLS says the record knows which requests
// arrived over TLS (Caddy's does; nginx's combined format does not): a plain
// request for an HTTPS domain was then answered by the ingress's redirect,
// not by the application, and is not the application's to answer again. It
// returns the chosen requests and how many distinct requests qualified before
// the plan's count cut them.
func selectReplayRequests(entries []accesslog.Entry, plan *ReplayPlan, domains []PlannedDomain, recordsTLS bool) ([]replayRequest, int) {
	served := servedDomains(domains)
	limit := plan.requests()
	seen := map[string]bool{}
	chosen := []replayRequest{}
	eligible := 0
	for _, entry := range entries {
		if !replayable(entry) {
			continue
		}
		domain, host, ok := replayHost(entry.Host, served)
		if !ok {
			continue
		}
		if len(plan.Paths) > 0 && !pathAllowed(entry.Path, plan.Paths) {
			continue
		}
		https := domain.HTTPS
		if recordsTLS {
			if domain.HTTPS && !entry.TLS {
				continue
			}
			https = entry.TLS
		}
		key := entry.Method + " " + host + " " + entry.Path + "?" + entry.Query
		if seen[key] {
			continue
		}
		seen[key] = true
		eligible++
		if len(chosen) >= limit {
			continue
		}
		duration, timed := entry.Duration()
		chosen = append(chosen, replayRequest{
			Method: entry.Method, Path: entry.Path, Query: entry.Query, Host: host, HTTPS: https,
			Status: entry.Status, Duration: duration, Timed: timed,
		})
	}
	return chosen, eligible
}

func pathAllowed(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// replayClient is the one method of an http.Client the replay uses, so a
// test can stand in for the network.
type replayClient interface {
	Do(*http.Request) (*http.Response, error)
}

// newReplayClient dials the candidate's address and nothing else: no proxy
// from the environment, and no redirect followed — the record holds the
// first answer the live release gave, so the first answer is what the
// candidate is compared on, and a redirect to another origin is never
// fetched.
func newReplayClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: nil, DialContext: dialer.DialContext,
			MaxConnsPerHost: replayConcurrency, MaxIdleConnsPerHost: replayConcurrency,
			IdleConnTimeout: 10 * time.Second, ResponseHeaderTimeout: replayRequestTimeout,
			DisableCompression: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (e *NormalizedStepExecutor) replayHTTPClient() replayClient {
	e.replayOnce.Do(func() {
		if e.replayClient == nil {
			e.replayClient = newReplayClient()
		}
	})
	return e.replayClient
}

// replayAgainst sends the requests to the candidate at address, at most
// `concurrency` at once, until all are answered or ctx ends. Each request is
// built from the record alone — method, path, query and Host — with the
// headers the managed proxy would add and the replay's own mark, and nothing
// a client might have sent. Answers are read for their status and timing and
// discarded. It returns one answer per request, in request order — a request
// the deadline left unsent is unanswered, with "out_of_time", since a
// candidate too slow to be asked is not one that passed — and whether the
// deadline cut the replay short.
func replayAgainst(ctx context.Context, client replayClient, address string, requests []replayRequest, concurrency int) ([]replayAnswer, bool) {
	if concurrency <= 0 {
		concurrency = replayConcurrency
	}
	answers := make([]replayAnswer, len(requests))
	sent := make([]bool, len(requests))
	var wg sync.WaitGroup
	slots := make(chan struct{}, concurrency)
	timedOut := false
	for index, request := range requests {
		if ctx.Err() != nil {
			timedOut = true
			break
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			timedOut = true
		}
		if timedOut {
			break
		}
		sent[index] = true
		wg.Add(1)
		go func(index int, request replayRequest) {
			defer wg.Done()
			defer func() { <-slots }()
			answers[index] = replayOne(ctx, client, address, request)
		}(index, request)
	}
	wg.Wait()
	for index, request := range requests {
		switch {
		case !sent[index]:
			answers[index] = replayAnswer{request: request, Error: "out_of_time"}
			timedOut = true
		case !answers[index].Answered && ctx.Err() != nil:
			// Cut off by the deadline rather than refused by the candidate.
			timedOut = true
		}
	}
	return answers, timedOut
}

func replayOne(ctx context.Context, client replayClient, address string, request replayRequest) replayAnswer {
	answer := replayAnswer{request: request}
	target := url.URL{Scheme: "http", Host: address, Path: request.Path, RawQuery: request.Query}
	requestCtx, cancel := context.WithTimeout(ctx, replayRequestTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, request.Method, target.String(), nil)
	if err != nil {
		answer.Error = "invalid_request"
		return answer
	}
	scheme := "http"
	if request.HTTPS {
		scheme = "https"
	}
	// The candidate is introduced as the domain, as the proxy introduces
	// it and as the readiness probe does (checks_http.go), so a host
	// allowlist or an HTTPS redirect answers the replay as it answers a
	// visitor. X-JD-Replay marks the request for an application that wants
	// to keep replays out of its own analytics.
	httpRequest.Host = request.Host
	httpRequest.Header.Set("X-Forwarded-Host", request.Host)
	httpRequest.Header.Set("X-Forwarded-Proto", scheme)
	httpRequest.Header.Set("X-Forwarded-For", "127.0.0.1")
	httpRequest.Header.Set("X-JD-Replay", "1")
	httpRequest.Header.Set("User-Agent", "JustDashboard-Replay/1")
	httpRequest.Header.Set("Accept", readinessAccept)
	started := time.Now()
	response, err := client.Do(httpRequest)
	if err != nil {
		answer.Error = checkNetworkError(requestCtx, err)
		return answer
	}
	// Read enough of the body for the timing to be the answer's, then let
	// it go: nothing of it is kept.
	_, _ = io.CopyN(io.Discard, response.Body, replayBodyLimit)
	_ = response.Body.Close()
	answer.Answered, answer.Status = true, response.StatusCode
	answer.Ms = float64(time.Since(started)) / float64(time.Millisecond)
	return answer
}

func statusClass(status int) string {
	return accesslog.Entry{Status: status}.Class()
}

// summarizeReplay reads the answers into the report: the counts, the
// drifted requests, both p95s over the requests timed on both sides, and
// the verdict. The latency rule is the rollback watch's own
// (latencyRegressed): the candidate's p95 past the factor and at least a
// quarter of a second slower, so 4 ms becoming 9 ms is not a regression.
func summarizeReplay(report *ReplayReport, plan *ReplayPlan, answers []replayAnswer) {
	report.Requests = len(answers)
	report.MaxStatusDrift, report.LatencyFactor = plan.statusDrift(), plan.latencyFactor()
	report.Drifts = []ReplayDrift{}
	recorded, candidate := []float64{}, []float64{}
	for _, answer := range answers {
		if answer.Answered {
			report.Answered++
		}
		drifted := !answer.Answered || statusClass(answer.Status) != statusClass(answer.request.Status)
		if drifted {
			report.Drifted++
			if len(report.Drifts) < replayDriftListCap {
				report.Drifts = append(report.Drifts, replayDrift(answer))
			}
		}
		if answer.Answered && answer.request.Timed && report.LatencyRecorded {
			recorded = append(recorded, answer.request.Duration)
			candidate = append(candidate, answer.Ms)
		}
	}
	if report.Requests > 0 {
		report.DriftPercent = math.Round(1000*float64(report.Drifted)/float64(report.Requests)) / 10
	}
	if len(recorded) >= replayLatencyMinSamples {
		was, is := accesslog.Percentile(recorded, 0.95), accesslog.Percentile(candidate, 0.95)
		report.RecordedP95, report.CandidateP95, report.LatencyJudged = &was, &is, true
	}

	verdict := ReplayVerdict{}
	switch {
	case report.Requests == 0:
		verdict.Code = ReplayVerdictWithinDrift
		verdict.Reason = "Nothing was replayed."
	case report.Answered == 0:
		verdict.Code, verdict.Tripped = ReplayVerdictUnanswered, true
		verdict.Reason = fmt.Sprintf("None of the %d replayed requests was answered by the candidate (%s).", report.Requests, report.Drifts[0].Error)
	case report.DriftPercent > report.MaxStatusDrift:
		verdict.Code, verdict.Tripped = ReplayVerdictStatusDrift, true
		verdict.Reason = fmt.Sprintf("%d of %d replayed requests (%s) answered in another status class than the live release recorded; the plan allows %s.",
			report.Drifted, report.Requests, percent(report.DriftPercent), percent(report.MaxStatusDrift))
	case report.LatencyJudged && latencyRegressed(*report.RecordedP95, *report.CandidateP95, report.LatencyFactor):
		verdict.Code, verdict.Tripped = ReplayVerdictLatency, true
		verdict.Reason = fmt.Sprintf("The candidate's p95 over the replayed requests was %s against %s recorded, past the %s× the plan allows.",
			humanMillis(*report.CandidateP95), humanMillis(*report.RecordedP95), trimFloat(report.LatencyFactor))
	default:
		verdict.Code = ReplayVerdictWithinDrift
		parts := []string{fmt.Sprintf("%d drifted (%s)", report.Drifted, percent(report.DriftPercent))}
		switch {
		case report.LatencyJudged:
			parts = append(parts, fmt.Sprintf("p95 %s (%s recorded)", humanMillis(*report.CandidateP95), humanMillis(*report.RecordedP95)))
		case !report.LatencyRecorded:
			parts = append(parts, "p95 not judged: this ingress records no durations")
		default:
			parts = append(parts, fmt.Sprintf("p95 not judged: fewer than %d requests were timed on both sides", replayLatencyMinSamples))
		}
		verdict.Reason = fmt.Sprintf("Within the live release's record over %d requests: %s.", report.Requests, strings.Join(parts, ", "))
	}
	if verdict.Tripped && report.TimedOut {
		verdict.Reason += fmt.Sprintf(" The replay ran out of its %d s before every request was answered.", report.TimeoutSeconds)
	}
	report.Verdict = verdict
}

func replayDrift(answer replayAnswer) ReplayDrift {
	drift := ReplayDrift{
		Method: answer.request.Method, Path: answer.request.Path, Query: answer.request.Query,
		RecordedStatus: answer.request.Status, CandidateStatus: answer.Status, Error: answer.Error,
	}
	if answer.request.Timed {
		recorded := answer.request.Duration
		drift.RecordedMs = &recorded
	}
	if answer.Answered {
		candidate := answer.Ms
		drift.CandidateMs = &candidate
	}
	return drift
}

// replayTraffic is the replay_traffic step. It skips, with the reason in its
// transcript and evidence, when the plan has it off or the record holds
// nothing to replay; it warns when the record cannot be read; and when the
// candidate drifts past the plan's lines it stops the candidate and restores
// the predecessor exactly as a failed health gate does, so the live release
// is never touched.
func (e *NormalizedStepExecutor) replayTraffic(ctx context.Context, execution StepExecution, _ *StoredExecutionPlan) StepResult {
	skip := func(reason string) StepResult {
		_ = stepLog(execution, "status", "Not replayed: "+reason+".")
		return StepResult{State: StepSkipped, Evidence: mustJSON(ReplayReport{Reason: reason, Drifts: []ReplayDrift{}})}
	}
	if operationTargetsLiveRelease(execution.Run.Operation) {
		return skip(fmt.Sprintf("a %s has no candidate release to compare with the live one", execution.Run.Operation))
	}
	release, snapshot, err := e.releaseSnapshotForRun(ctx, execution.Run.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	plan := snapshot.Plan.Replay
	if !plan.on() {
		return skip("traffic replay is turned off for this plan")
	}
	runtime, err := e.store.RuntimeForRelease(ctx, release.Release.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	if e.requests == nil {
		return skip("this host keeps no request record to replay from")
	}
	served := servedDomains(snapshot.Domains)
	if len(served) == 0 {
		return skip("the plan serves no domain, so the ingress recorded nothing to replay")
	}
	hostnames := make([]string, 0, len(served))
	for _, domain := range served {
		hostnames = append(hostnames, domain.Hostname)
	}
	target := targetForRuntime(*runtime, snapshot)
	if target.Host == "" || target.Port == 0 {
		return skip("the candidate has no HTTP address of its own to replay against")
	}
	address := net.JoinHostPort(target.Host, strconv.Itoa(target.Port))

	started := time.Now()
	readCtx, cancelRead := context.WithTimeout(ctx, 30*time.Second)
	window, err := e.requests.Window(readCtx, deploymentRouteName(execution.Run.EnvironmentID), accesslog.Filter{
		Since: started.Add(-replayReadWindow), Until: started,
		Methods: []string{http.MethodGet, http.MethodHead}, Limit: replayReadLimit,
	})
	cancelRead()
	if ctx.Err() != nil {
		return StepResult{State: StepCancelled, ErrorCode: "cancelled", ErrorMessage: "the traffic replay was cancelled"}
	}
	if err != nil {
		// Unreadable is not empty: the release goes on, and the run says
		// the gate did not run rather than that there was nothing to check.
		reason := "the request record could not be read; open Proxy to check the ingress is running"
		_ = stepLog(execution, "stderr", "Not replayed: "+reason+". The release continues without the replay.")
		return StepResult{State: StepWarning, Evidence: mustJSON(ReplayReport{Reason: reason, Domains: hostnames, Drifts: []ReplayDrift{}})}
	}
	if !window.Coverage.Exists || window.Result == nil {
		return skip("no request has been recorded for this deployment yet")
	}
	chosen, eligible := selectReplayRequests(window.Result.Entries, plan, snapshot.Domains, window.Facts.Format == accesslog.FormatCaddyJSON)
	report := ReplayReport{
		Domains: hostnames, Target: address, Read: len(window.Result.Entries), Eligible: eligible,
		MaxStatusDrift: plan.statusDrift(), LatencyFactor: plan.latencyFactor(), LatencyRecorded: window.Facts.Latency,
		Concurrency: replayConcurrency, TimeoutSeconds: int(plan.timeout().Seconds()), Drifts: []ReplayDrift{},
	}
	if len(chosen) == 0 {
		report.Reason = fmt.Sprintf("the record holds no eligible GET or HEAD request for %s in the last 24 hours (%d requests read)",
			strings.Join(hostnames, ", "), report.Read)
		_ = stepLog(execution, "status", "Not replayed: "+report.Reason+".")
		return StepResult{State: StepSkipped, Evidence: mustJSON(report)}
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Replaying the last %d distinct GET/HEAD requests recorded for %s against the candidate at %s, %d at a time, within %s",
		len(chosen), strings.Join(hostnames, ", "), address, replayConcurrency, plan.timeout()))

	replayCtx, cancelReplay := context.WithTimeout(ctx, plan.timeout())
	answers, timedOut := replayAgainst(replayCtx, e.replayHTTPClient(), address, chosen, replayConcurrency)
	cancelReplay()
	if ctx.Err() != nil {
		return StepResult{State: StepCancelled, ErrorCode: "cancelled", ErrorMessage: "the traffic replay was cancelled"}
	}
	report.Replayed, report.TimedOut = true, timedOut
	summarizeReplay(&report, plan, answers)
	report.DurationMS = time.Since(started).Milliseconds()
	if timedOut {
		_ = stepLog(execution, "stderr", fmt.Sprintf("The replay ran out of time: %d of %d requests answered within %s. A request not answered in time counts as drifted",
			report.Answered, len(chosen), plan.timeout()))
	}
	for _, drift := range report.Drifts {
		line := drift.Path
		if drift.Query != "" {
			line += "?" + drift.Query
		}
		candidate := strconv.Itoa(drift.CandidateStatus)
		if drift.CandidateStatus == 0 {
			candidate = "no answer (" + drift.Error + ")"
		}
		_ = stepLog(execution, "stderr", fmt.Sprintf("%s %s: recorded %d, candidate %s", drift.Method, line, drift.RecordedStatus, candidate))
	}
	_ = stepLog(execution, "status", fmt.Sprintf("Replayed %d requests in %s: %s", report.Requests,
		(time.Duration(report.DurationMS)*time.Millisecond).Round(100*time.Millisecond), report.Verdict.Reason))
	if !report.Verdict.Tripped {
		return StepResult{State: StepPassed, Evidence: mustJSON(report)}
	}
	// The same recovery a failed readiness check takes: the candidate is
	// stopped, a stop-first predecessor started again, and the route — which
	// nothing before activation has touched — left as it is.
	recovery := e.stopCandidateAndRestore(ctx, execution, release.Release, *runtime, snapshot.Plan, nil)
	report.Recovery = &recovery
	return StepResult{
		State: StepFailed, ErrorCode: "replay_" + report.Verdict.Code,
		ErrorMessage: report.Verdict.Reason + " The release stopped before the cutover and the live release was not touched.",
		Evidence:     mustJSON(report), Recovered: recoveryComplete(recovery, snapshot.Plan, release.Release),
	}
}

// replayFindings is preflight's word on the replay: that it is on and what
// it will hold the candidate to, or — when nothing records the route's
// requests — that it will have nothing to replay on every deployment.
func replayFindings(configuration PlanConfiguration, observation HostObservation) []PreflightFinding {
	plan := configuration.Runtime.Replay
	if !plan.on() {
		return nil
	}
	served := servedDomains(configuration.Domains)
	if len(served) == 0 {
		return []PreflightFinding{finding("replay_without_record", PreflightWarning,
			"Traffic replay is on, but no served domain records requests to replay", replayLabel(plan),
			"The request record is written by the public route's proxy. A plan with no served domain has no route and no record, so the replay step will skip on every deployment.",
			"Add a domain the application serves, or turn traffic replay off.", "proxy", "runtime.replay")}
	}
	hostnames := make([]string, 0, len(served))
	for _, domain := range served {
		hostnames = append(hostnames, domain.Hostname)
		for _, observed := range observation.Domains {
			if strings.EqualFold(observed.Hostname, domain.Hostname) && !observed.ProxyAvailable {
				return []PreflightFinding{finding("replay_without_record", PreflightWarning,
					"Traffic replay is on, but no proxy records "+domain.Hostname+"'s requests", replayLabel(plan),
					"The request record is written by the public route's proxy, and none is available for this name, so the replay step will have nothing to replay.",
					"Open Proxy to check the ingress, or turn traffic replay off.", "proxy", "runtime.replay")}
			}
		}
	}
	return []PreflightFinding{finding("replay_before_cutover", PreflightPass,
		fmt.Sprintf("The last %d recorded requests are replayed against the candidate before the cutover", plan.requests()), replayLabel(plan),
		"GET and HEAD requests the proxy recorded for "+strings.Join(hostnames, ", ")+" are sent to the candidate's own address without cookies, credentials or bodies, and a candidate whose status classes or p95 drift past these lines is stopped with the live release untouched.",
		"", "deploy", "runtime.replay")}
}
