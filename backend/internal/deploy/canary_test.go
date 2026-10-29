package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
)

func TestCanaryTermsAreValidated(t *testing.T) {
	t.Parallel()
	base := func() PlanConfiguration {
		return PlanConfiguration{
			Build: BuildPlanConfig{Method: BuildImage, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
			Runtime: RuntimePlanConfig{
				Image: "nginx:1.27", InternalPort: 80, BindAddress: "127.0.0.1", Strategy: StrategyCanary,
				Canary:  &CanaryTerms{Percent: 10, DurationSeconds: 300, AutoPromote: true},
				Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
			},
			Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
			Domains: []PlannedDomain{{Hostname: "app.example.test", Ownership: OwnershipManaged}},
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("a canary plan was refused: %v", err)
	}
	cases := []struct {
		name   string
		change func(*PlanConfiguration)
		field  string
	}{
		{"no terms", func(p *PlanConfiguration) { p.Runtime.Canary = nil }, "runtime.canary"},
		{"no share", func(p *PlanConfiguration) { p.Runtime.Canary.Percent = 0 }, "runtime.canary.percent"},
		{"more than half", func(p *PlanConfiguration) { p.Runtime.Canary.Percent = 51 }, "runtime.canary.percent"},
		{"under a minute", func(p *PlanConfiguration) { p.Runtime.Canary.DurationSeconds = 59 }, "runtime.canary.durationSeconds"},
		{"over an hour", func(p *PlanConfiguration) { p.Runtime.Canary.DurationSeconds = 3601 }, "runtime.canary.durationSeconds"},
		{"no domain", func(p *PlanConfiguration) { p.Domains = nil }, "runtime.strategy"},
		{"only a redirect", func(p *PlanConfiguration) {
			p.Domains = []PlannedDomain{{Hostname: "www.example.test", RedirectTo: "example.test", Ownership: OwnershipManaged}}
		}, "runtime.strategy"},
		{"terms on blue/green", func(p *PlanConfiguration) { p.Runtime.Strategy = StrategyBlueGreen }, "runtime.canary"},
	}
	for _, tc := range cases {
		plan := base()
		tc.change(&plan)
		var field *ValidationError
		if err := plan.Validate(); !errors.As(err, &field) || field.Field != tc.field {
			t.Errorf("%s: %v, want a refusal on %s", tc.name, err, tc.field)
		}
	}
	if item, ok := canaryFinding(base()); !ok || item.Code != "canary" || !strings.Contains(item.Title, "10% of the requests for 5 minutes") ||
		!strings.Contains(item.Means, "promoted at the end") {
		t.Fatalf("finding = %+v", item)
	}
}

// The canary is judged by the rollback watch's rule, read side by side, and
// says so in its words: nothing came "before".
func TestCanaryVerdictComparesTheReleasesSideBySide(t *testing.T) {
	t.Parallel()
	policy := DefaultRollbackPolicy()
	live := &RollbackReading{Requests: 400, Failures: 2, ErrorRate: 0.005, P95: floatPointer(80)}
	failing := judgeCanary(policy, live, RollbackReading{Requests: 60, Failures: 30, ErrorRate: 0.5, P95: floatPointer(90)}, true)
	if !failing.Tripped || failing.Code != RollbackVerdictErrorRate ||
		failing.Reason != "5xx answers rose to 50.0% (30 of 60 requests) from 0.5% on the live release." {
		t.Fatalf("failing verdict = %+v", failing)
	}
	clean := judgeCanary(policy, live, RollbackReading{Requests: 60, Failures: 0, ErrorRate: 0, P95: floatPointer(85)}, true)
	if clean.Tripped || strings.Contains(clean.Reason, "before") || strings.Contains(clean.Reason, "previous") ||
		!strings.Contains(clean.Reason, "Within the live release's figures over 60 requests") {
		t.Fatalf("clean verdict = %+v", clean)
	}
	few := judgeCanary(policy, live, RollbackReading{Requests: 3}, true)
	if few.Code != RollbackVerdictTooFew || !strings.Contains(few.Reason, "on the candidate") {
		t.Fatalf("too few = %+v", few)
	}
}

// canaryRecord answers windows from requests tagged with the release that
// answered them, as a canary route records them.
type canaryRecord struct {
	mu       sync.Mutex
	requests []canaryRequest
}

type canaryRequest struct {
	at      time.Time
	release string
	status  int
	ms      float64
}

func (r *canaryRecord) add(at time.Time, release string, count, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := 0; index < count; index++ {
		r.requests = append(r.requests, canaryRequest{at: at, release: release, status: status, ms: 40})
	}
}

func (r *canaryRecord) Window(_ context.Context, _ string, filter accesslog.Filter) (accesslog.Window, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := accesslog.Summary{Classes: map[string]int{}}
	durations := []float64{}
	for _, request := range r.requests {
		if request.at.Before(filter.Since) || request.at.After(filter.Until) || (filter.Release != "" && filter.Release != request.release) {
			continue
		}
		summary.Total++
		summary.Classes[fmt.Sprintf("%dxx", request.status/100)]++
		durations = append(durations, request.ms)
	}
	if summary.Total > 0 {
		summary.ErrorRate = float64(summary.Classes["5xx"]) / float64(summary.Total)
		sort.Float64s(durations)
		summary.Latency = &accesslog.Latency{P95: durations[int(math.Ceil(0.95*float64(len(durations))))-1]}
	}
	return accesslog.Window{Result: &accesslog.Result{Summary: summary}, Facts: accesslog.Facts{Latency: true}}, nil
}

type canaryFixture struct {
	*releaseStoreFixture
	clock    atomic.Int64
	mu       sync.Mutex
	leases   []StepExecution
	owner    *warmRuntimeOwner
	proxy    *replicaRouteProxy
	record   *canaryRecord
	executor *NormalizedStepExecutor
}

func (f *canaryFixture) time() time.Time { return time.Unix(0, f.clock.Load()).UTC() }

// advance moves the clock, and keeps the run's lease fresh across it as
// the engine's heartbeat would.
func (f *canaryFixture) advance(by time.Duration) {
	f.clock.Add(int64(by))
	f.mu.Lock()
	leases := append([]StepExecution(nil), f.leases...)
	f.mu.Unlock()
	for _, execution := range leases {
		_ = f.runs.Heartbeat(context.Background(), execution.Run.ID, execution.ClaimToken, time.Hour)
	}
}

func newCanaryFixture(t *testing.T) *canaryFixture {
	t.Helper()
	f := &canaryFixture{releaseStoreFixture: newReleaseStoreFixture(t), owner: newWarmRuntimeOwner(),
		proxy: &replicaRouteProxy{}, record: &canaryRecord{}}
	f.clock.Store(f.now.UnixNano())
	// The step reads the clock while the test moves it.
	f.runs.now = f.time
	f.executor = (&NormalizedStepExecutor{
		store: f.runs, variables: f.variables, runtime: f.owner,
		checks: NewCheckRunner(&checkBackendFake{}), proxy: f.proxy,
		sources: &HostSourceAnalyzer{}, builder: &ArtifactBuilder{},
		hostMemory: func() (int64, int64) { return 8 << 30, 16 << 30 },
	}).WithRequestRecord(f.record).WithCanaryTiming(2*time.Millisecond, 4*time.Millisecond)
	return f
}

var canaryPlan = RuntimePlanConfig{
	InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyCanary,
	Canary:  &CanaryTerms{Percent: 20, DurationSeconds: 120, AutoPromote: true},
	Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
}

// release starts one release and takes it to the canary step, or through to
// live when it is the first.
func (f *canaryFixture) release(t *testing.T, revision int, identity string, plan RuntimePlanConfig) (*ReleaseWithArtifacts, StepExecution) {
	t.Helper()
	f.now = f.time()
	f.addPlanWithRuntime(t, revision, strings.Repeat(identity[:1], 40), plan)
	run, lease := f.claimedRun(t, revision)
	release := createRuntimeCandidateWithDomains(t, f.releaseStoreFixture, *run, lease, plan, identity,
		nil, []PlannedDomain{{Hostname: "app.example.test"}})
	execution := StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}}
	f.mu.Lock()
	f.leases = append(f.leases, execution)
	f.mu.Unlock()
	_ = f.runs.Heartbeat(context.Background(), run.ID, lease.Token, time.Hour)
	for _, key := range []StepKey{StepStartCandidate, StepVerifyReadiness} {
		execution.Step = RunStep{Key: key}
		if result := f.executor.Execute(context.Background(), execution); result.State != StepPassed && result.State != StepSkipped {
			t.Fatalf("%s %s = %+v", identity, key, result)
		}
	}
	return release, execution
}

func (f *canaryFixture) finish(t *testing.T, release *ReleaseWithArtifacts, execution StepExecution) {
	t.Helper()
	for _, key := range []StepKey{StepActivate, StepRetirePrevious} {
		execution.Step = RunStep{Key: key}
		if result := f.executor.Execute(context.Background(), execution); result.State != StepPassed && result.State != StepSkipped {
			t.Fatalf("%s = %+v", key, result)
		}
	}
	f.now = f.time()
	f.finishActivatedRun(t, execution.Run.ID, release.Release.ID, execution.ClaimToken)
}

// canaryStep runs the canary step in the background, as the engine would.
func (f *canaryFixture) canaryStep(ctx context.Context, execution StepExecution) <-chan StepResult {
	done := make(chan StepResult, 1)
	execution.Step = RunStep{Key: StepCanary}
	go func() { done <- f.executor.Execute(ctx, execution) }()
	return done
}

func (f *canaryFixture) openCanary(t *testing.T) *CanaryWindow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		window, err := f.runs.OpenCanary(context.Background(), f.envID)
		if err == nil {
			return window
		}
		if time.Now().After(deadline) {
			t.Fatalf("no canary opened: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func waitStep(t *testing.T, done <-chan StepResult) StepResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("the canary step did not end")
	}
	return StepResult{}
}

// lastRoute is the route the proxy was last given.
func (f *canaryFixture) lastRoute(t *testing.T) (string, string) {
	t.Helper()
	f.proxy.mu.Lock()
	defer f.proxy.mu.Unlock()
	if len(f.proxy.routes) == 0 {
		t.Fatal("no route was applied")
	}
	route := f.proxy.routes[len(f.proxy.routes)-1]
	canary := ""
	if route.Canary != nil {
		canary = fmt.Sprintf("%d%%:%s", route.Canary.Percent, strings.Join(route.Canary.Upstreams, ","))
	}
	return route.PrimaryUpstream(), canary
}

func TestFailingCanaryIsAbortedWithTheLiveReleaseUntouched(t *testing.T) {
	t.Parallel()
	f := newCanaryFixture(t)
	first, firstRun := f.release(t, 1, "a-release", canaryPlan)
	firstRun.Step = RunStep{Key: StepCanary}
	if result := f.executor.Execute(context.Background(), firstRun); result.State != StepSkipped {
		t.Fatalf("the first release's canary = %+v", result)
	}
	f.finish(t, first, firstRun)

	second, secondRun := f.release(t, 2, "b-release", canaryPlan)
	done := f.canaryStep(context.Background(), secondRun)
	window := f.openCanary(t)
	if window.ReleaseID != second.Release.ID || window.LiveReleaseID != first.Release.ID || window.Percent != 20 {
		t.Fatalf("window = %+v", window)
	}
	// The window is recorded before the route is written.
	deadline := time.Now().Add(5 * time.Second)
	live, shared := f.lastRoute(t)
	for shared == "" && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
		live, shared = f.lastRoute(t)
	}
	if live != "http://127.0.0.1:32001" || shared != "20%:http://127.0.0.1:32002" {
		t.Fatalf("canary route = %s + %s", live, shared)
	}
	// The candidate answers half its share with 500s while the live release
	// is clean: aborted before its window ends.
	at := f.time().Add(10 * time.Second)
	f.record.add(at, fmt.Sprint(first.Release.ID), 200, 200)
	f.record.add(at, fmt.Sprint(second.Release.ID), 25, 200)
	f.record.add(at, fmt.Sprint(second.Release.ID), 25, 500)
	f.advance(20 * time.Second)
	result := waitStep(t, done)
	if result.State != StepFailed || result.ErrorCode != "canary_failed" || !result.Recovered ||
		!strings.Contains(result.ErrorMessage, "5xx answers rose to 50.0% (25 of 50 requests) from 0.0% on the live release") {
		t.Fatalf("result = %+v", result)
	}
	if live, shared := f.lastRoute(t); live != "http://127.0.0.1:32001" || shared != "" {
		t.Fatalf("route after the abort = %s + %s", live, shared)
	}
	if f.owner.state("container-r2") != "missing" || f.owner.state("container-r1") != "running" {
		t.Fatalf("after the abort: candidate %s, live %s", f.owner.state("container-r2"), f.owner.state("container-r1"))
	}
	ended, err := f.runs.LatestCanary(context.Background(), f.envID)
	if err != nil || ended.State != CanaryAborted || ended.EndReason != CanaryEndFailed || ended.Candidate == nil || ended.Candidate.Requests != 50 || ended.Live.Requests != 200 {
		t.Fatalf("ended window = %+v, %v", ended, err)
	}
	if liveNow := f.mustLive(t); liveNow != first.Release.ID {
		t.Fatalf("live release after the abort = %d", liveNow)
	}
}

func (f *canaryFixture) mustLive(t *testing.T) int64 {
	t.Helper()
	live, err := f.runs.LiveRelease(context.Background(), f.envID)
	if err != nil {
		t.Fatal(err)
	}
	return live.Release.ID
}

func TestHealthyCanaryIsPromotedAtTheEndOfItsWindow(t *testing.T) {
	t.Parallel()
	f := newCanaryFixture(t)
	first, firstRun := f.release(t, 1, "a-release", canaryPlan)
	f.finish(t, first, firstRun)

	second, secondRun := f.release(t, 2, "b-release", canaryPlan)
	done := f.canaryStep(context.Background(), secondRun)
	f.openCanary(t)
	at := f.time().Add(10 * time.Second)
	f.record.add(at, fmt.Sprint(first.Release.ID), 300, 200)
	f.record.add(at, fmt.Sprint(second.Release.ID), 80, 200)
	f.advance(121 * time.Second)
	result := waitStep(t, done)
	var evidence map[string]any
	_ = json.Unmarshal(result.Evidence, &evidence)
	if result.State != StepPassed || evidence["endReason"] != CanaryEndPassed {
		t.Fatalf("result = %+v %s", result, result.Evidence)
	}
	// The route is the live release's alone again, for the ordinary cutover.
	if live, shared := f.lastRoute(t); live != "http://127.0.0.1:32001" || shared != "" {
		t.Fatalf("route after the promotion = %s + %s", live, shared)
	}
	f.finish(t, second, secondRun)
	if f.mustLive(t) != second.Release.ID {
		t.Fatal("the promoted release did not go live")
	}
	if live, _ := f.lastRoute(t); live != "http://127.0.0.1:32002" {
		t.Fatalf("route after the cutover = %s", live)
	}
	// The release it replaced is kept warm, as after any blue/green cutover.
	if f.owner.state("container-r1") != "paused" {
		t.Fatalf("the previous release is %s, want paused", f.owner.state("container-r1"))
	}
}

func TestCanaryWaitsForTheOperatorWithoutAutoPromote(t *testing.T) {
	t.Parallel()
	f := newCanaryFixture(t)
	manual := canaryPlan
	manual.Canary = &CanaryTerms{Percent: 10, DurationSeconds: 60}
	first, firstRun := f.release(t, 1, "a-release", manual)
	f.finish(t, first, firstRun)

	_, secondRun := f.release(t, 2, "b-release", manual)
	done := f.canaryStep(context.Background(), secondRun)
	window := f.openCanary(t)
	f.advance(61 * time.Second)
	deadline := time.Now().Add(5 * time.Second)
	for window.State != CanaryHolding {
		if time.Now().After(deadline) {
			t.Fatalf("the window did not hold: %+v", window)
		}
		time.Sleep(2 * time.Millisecond)
		window, _ = f.runs.Canary(context.Background(), window.ID)
	}
	select {
	case result := <-done:
		t.Fatalf("a canary without automatic promotion ended on its own: %+v", result)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := f.runs.RequestCanaryDecision(context.Background(), f.projectID+1, f.envID, CanaryPromote, "mallory"); !errors.Is(err, ErrNoCanary) {
		t.Fatalf("another project's decision = %v", err)
	}
	decided, err := f.runs.RequestCanaryDecision(context.Background(), f.projectID, f.envID, CanaryPromote, "alice")
	if err != nil || decided.Decision != CanaryPromote {
		t.Fatalf("decision = %+v, %v", decided, err)
	}
	if _, err := f.runs.RequestCanaryDecision(context.Background(), f.projectID, f.envID, CanaryAbort, "bob"); !errors.Is(err, ErrCanaryDecided) {
		t.Fatalf("a second decision = %v", err)
	}
	result := waitStep(t, done)
	ended, _ := f.runs.LatestCanary(context.Background(), f.envID)
	if result.State != StepPassed || ended.EndReason != CanaryEndOperatorPromoted || ended.DecidedBy != "alice" ||
		!strings.Contains(ended.EndMessage, "alice promoted release #2") {
		t.Fatalf("result = %+v, window %+v", result, ended)
	}
}

func TestOperatorAbortsACanaryMidWindow(t *testing.T) {
	t.Parallel()
	f := newCanaryFixture(t)
	first, firstRun := f.release(t, 1, "a-release", canaryPlan)
	f.finish(t, first, firstRun)
	_, secondRun := f.release(t, 2, "b-release", canaryPlan)
	done := f.canaryStep(context.Background(), secondRun)
	f.openCanary(t)
	if _, err := f.runs.RequestCanaryDecision(context.Background(), f.projectID, f.envID, CanaryAbort, "alice"); err != nil {
		t.Fatal(err)
	}
	result := waitStep(t, done)
	if result.State != StepFailed || result.ErrorCode != "canary_aborted" || !strings.Contains(result.ErrorMessage, "alice aborted release #2's canary") {
		t.Fatalf("result = %+v", result)
	}
	if f.owner.state("container-r2") != "missing" || f.mustLive(t) != first.Release.ID {
		t.Fatal("the aborted candidate was not removed, or the live release moved")
	}
}

// A process that stops mid-window leaves the trial open, and the next one
// resumes it with its own start and end rather than a new window.
func TestARestartMidWindowResumesTheCanary(t *testing.T) {
	t.Parallel()
	f := newCanaryFixture(t)
	first, firstRun := f.release(t, 1, "a-release", canaryPlan)
	f.finish(t, first, firstRun)
	second, secondRun := f.release(t, 2, "b-release", canaryPlan)
	stopping, stop := context.WithCancel(context.Background())
	done := f.canaryStep(stopping, secondRun)
	opened := f.openCanary(t)
	stop()
	if result := waitStep(t, done); result.State != StepCancelled {
		t.Fatalf("interrupted step = %+v", result)
	}
	still, err := f.runs.OpenCanary(context.Background(), f.envID)
	if err != nil || still.ID != opened.ID {
		t.Fatalf("the window did not stay open: %+v, %v", still, err)
	}
	f.advance(60 * time.Second)
	resumed := f.canaryStep(context.Background(), secondRun)
	at := f.time()
	f.record.add(at, fmt.Sprint(first.Release.ID), 100, 200)
	f.record.add(at, fmt.Sprint(second.Release.ID), 30, 200)
	time.Sleep(20 * time.Millisecond)
	window, _ := f.runs.Canary(context.Background(), opened.ID)
	if !window.StartedAt.Equal(opened.StartedAt) || !window.EndsAt.Equal(opened.EndsAt) {
		t.Fatalf("the resumed window moved: %+v vs %+v", window, opened)
	}
	f.advance(61 * time.Second)
	if result := waitStep(t, resumed); result.State != StepPassed {
		t.Fatalf("resumed step = %+v", result)
	}
	if count := countCanaries(t, f); count != 1 {
		t.Fatalf("%d windows were opened for one run", count)
	}
	reconciled := (&NormalizedStepReconciler{store: f.runs}).Reconcile(context.Background(), ReconcileRequest{Step: &RunStep{Key: StepCanary}})
	if reconciled.Action != ReconcileResume {
		t.Fatalf("an interrupted canary is reconciled with %s", reconciled.Action)
	}
}

func countCanaries(t *testing.T, f *canaryFixture) int {
	t.Helper()
	var count int
	if err := f.base.DB.QueryRow(`SELECT COUNT(*) FROM deploy_canaries WHERE environment_id = ?`, f.envID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// A cancelled run takes its candidate off the route before removing it.
func TestCancellingACanaryPutsTheRouteBackFirst(t *testing.T) {
	t.Parallel()
	f := newCanaryFixture(t)
	first, firstRun := f.release(t, 1, "a-release", canaryPlan)
	f.finish(t, first, firstRun)
	_, secondRun := f.release(t, 2, "b-release", canaryPlan)
	cancelled, cancel := context.WithCancel(context.Background())
	done := f.canaryStep(cancelled, secondRun)
	f.openCanary(t)
	cancel()
	waitStep(t, done)
	if _, err := f.executor.CleanupCancelledRun(context.Background(), secondRun.Run, secondRun.ClaimToken); err != nil {
		t.Fatal(err)
	}
	if live, shared := f.lastRoute(t); live != "http://127.0.0.1:32001" || shared != "" {
		t.Fatalf("route after the cancellation = %s + %s", live, shared)
	}
	ended, _ := f.runs.LatestCanary(context.Background(), f.envID)
	if ended.State != CanaryAborted || ended.EndReason != CanaryEndCancelled || f.owner.state("container-r2") != "missing" {
		t.Fatalf("after the cancellation: window %+v, candidate %s", ended, f.owner.state("container-r2"))
	}
}

// Waiting out a window builds nothing, so the run gives the host's build
// slot back for it.
func TestACanaryGivesTheBuildSlotBackWhileItWaits(t *testing.T) {
	t.Parallel()
	f := newCanaryFixture(t)
	first, firstRun := f.release(t, 1, "a-release", canaryPlan)
	f.finish(t, first, firstRun)
	_, secondRun := f.release(t, 2, "b-release", canaryPlan)
	slot := func() string {
		var class string
		if err := f.base.DB.QueryRow(`SELECT slot_class FROM deploy_queue_leases WHERE run_id = ?`, secondRun.Run.ID).Scan(&class); err != nil {
			t.Fatal(err)
		}
		return class
	}
	before := slot()
	done := f.canaryStep(context.Background(), secondRun)
	f.openCanary(t)
	deadline := time.Now().Add(5 * time.Second)
	for slot() != string(canaryHoldSlot) {
		if time.Now().After(deadline) {
			t.Fatalf("the lease kept its %s slot through the window", slot())
		}
		time.Sleep(2 * time.Millisecond)
	}
	f.advance(121 * time.Second)
	waitStep(t, done)
	if slot() != before {
		t.Fatalf("after the window the lease is %s, want %s", slot(), before)
	}
}

// Shutdown does not wait out a canary's window: the step is stopped where it
// is and its lease given up, and the next process reclaims the run at once
// and resumes the step.
func TestShutdownHandsACanaryWaitToTheNextProcess(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) { req.Steps = []StepKey{StepCanary, StepNotify} })
	first := &recordingExecutor{block: make(chan struct{}), started: make(chan StepKey, 4)}
	engine := NewEngine(f.runs, first, NewDeploymentReconciler(f.runs), EngineConfig{
		WorkerID: "stopping", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute,
	}, nil)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case key := <-first.started:
		if key != StepCanary {
			t.Fatalf("started %s", key)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the canary step never started")
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := engine.Shutdown(shutdown); err != nil {
		t.Fatalf("shutdown waited on the canary: %v", err)
	}
	expired, err := f.runs.ExpiredLeases(context.Background())
	if err != nil || len(expired) != 1 || expired[0].RunID != run.ID {
		t.Fatalf("the lease was not given up: %+v, %v", expired, err)
	}
	if current, _ := f.runs.Run(context.Background(), run.ID); current.State.Terminal() {
		t.Fatalf("the run ended at shutdown: %s", current.State)
	}

	next := &recordingExecutor{}
	resumed := NewEngine(f.runs, next, NewDeploymentReconciler(f.runs), EngineConfig{
		WorkerID: "next", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute,
	}, nil)
	if err := resumed.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = resumed.Shutdown(stop)
	}()
	waitForRunState(t, f.runs, run.ID, RunSucceeded)
	if keys := next.executed(); len(keys) != 2 || keys[0] != StepCanary {
		t.Fatalf("the next process executed %v, want the canary resumed", keys)
	}
}
