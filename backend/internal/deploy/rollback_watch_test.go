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
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func floatPointer(value float64) *float64 { return &value }

func TestJudgeRollbackDecidesOnlyOverEnoughRequests(t *testing.T) {
	t.Parallel()
	policy := DefaultRollbackPolicy()
	clean := &RollbackReading{Requests: 400, Failures: 2, ErrorRate: 0.005, P95: floatPointer(120)}
	cases := []struct {
		name     string
		baseline *RollbackReading
		observed RollbackReading
		latency  bool
		code     string
		tripped  bool
	}{
		{"three failing requests cannot decide", clean, RollbackReading{Requests: 3, Failures: 3, ErrorRate: 1}, true, RollbackVerdictTooFew, false},
		{"nineteen is still one short", clean, RollbackReading{Requests: 19, Failures: 19, ErrorRate: 1}, true, RollbackVerdictTooFew, false},
		{"a 5xx share past the margin trips", clean, RollbackReading{Requests: 120, Failures: 46, ErrorRate: 46.0 / 120, P95: floatPointer(80)}, true, RollbackVerdictErrorRate, true},
		{"a share inside the margin does not", clean, RollbackReading{Requests: 200, Failures: 10, ErrorRate: 0.05, P95: floatPointer(130)}, true, RollbackVerdictWithinBaseline, false},
		{"four failures are too few to trip even at a high share", clean, RollbackReading{Requests: 20, Failures: 4, ErrorRate: 0.2, P95: floatPointer(130)}, true, RollbackVerdictWithinBaseline, false},
		{"a noisy baseline raises its own line", &RollbackReading{Requests: 400, Failures: 80, ErrorRate: 0.2, P95: floatPointer(120)}, RollbackReading{Requests: 100, Failures: 22, ErrorRate: 0.22, P95: floatPointer(125)}, true, RollbackVerdictWithinBaseline, false},
		{"no baseline judges against a clean share", nil, RollbackReading{Requests: 50, Failures: 10, ErrorRate: 0.2}, true, RollbackVerdictErrorRate, true},
		{"a thin baseline is not trusted for its share", &RollbackReading{Requests: 5, Failures: 3, ErrorRate: 0.6, P95: floatPointer(100)}, RollbackReading{Requests: 50, Failures: 10, ErrorRate: 0.2, P95: floatPointer(900)}, true, RollbackVerdictErrorRate, true},
		{"twice the p95 and a quarter second slower trips", clean, RollbackReading{Requests: 100, Failures: 0, ErrorRate: 0, P95: floatPointer(1900)}, true, RollbackVerdictLatency, true},
		{"twice a tiny p95 is not a regression", &RollbackReading{Requests: 400, ErrorRate: 0, P95: floatPointer(4)}, RollbackReading{Requests: 100, ErrorRate: 0, P95: floatPointer(30)}, true, RollbackVerdictWithinBaseline, false},
		{"a record without durations never judges latency", clean, RollbackReading{Requests: 100, Failures: 0, ErrorRate: 0, P95: floatPointer(9000)}, false, RollbackVerdictWithinBaseline, false},
		{"a thin baseline never judges latency", &RollbackReading{Requests: 5, P95: floatPointer(10)}, RollbackReading{Requests: 100, P95: floatPointer(9000)}, true, RollbackVerdictWithinBaseline, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict := JudgeRollback(policy, tc.baseline, tc.observed, tc.latency)
			if verdict.Code != tc.code || verdict.Tripped != tc.tripped {
				t.Fatalf("verdict = %+v, want code %s tripped %t", verdict, tc.code, tc.tripped)
			}
			if verdict.Reason == "" {
				t.Fatal("a verdict without a reason")
			}
			if verdict.Code == RollbackVerdictTooFew && verdict.Judged {
				t.Fatal("too few requests was reported as judged")
			}
		})
	}
	verdict := JudgeRollback(policy, clean, RollbackReading{Requests: 120, Failures: 46, ErrorRate: 46.0 / 120}, true)
	if verdict.Reason != "5xx answers rose to 38.3% (46 of 120 requests) from 0.5% before." {
		t.Fatalf("reason = %q", verdict.Reason)
	}
	if verdict := JudgeRollback(policy, clean, RollbackReading{Requests: 100, P95: floatPointer(130)}, false); !strings.Contains(verdict.Reason, "records no durations") {
		t.Fatalf("an nginx record's verdict does not say why p95 was not judged: %q", verdict.Reason)
	}
}

func TestRollbackPolicyDefaultsValidationAndPreviewRefusal(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	ctx := context.Background()
	policy, err := fixture.runs.RollbackPolicy(ctx, fixture.envID)
	if err != nil || policy != DefaultRollbackPolicy() {
		t.Fatalf("an unsaved environment's terms = %+v, %v", policy, err)
	}
	for _, bad := range []RollbackPolicy{
		{KeepWarm: true, WindowSeconds: 10, ErrorRateMargin: 5, LatencyFactor: 2, MinRequests: 20},
		{KeepWarm: true, WindowSeconds: 600, ErrorRateMargin: 0, LatencyFactor: 2, MinRequests: 20},
		{KeepWarm: true, WindowSeconds: 600, ErrorRateMargin: 5, LatencyFactor: 1, MinRequests: 20},
		{KeepWarm: true, WindowSeconds: 600, ErrorRateMargin: 5, LatencyFactor: 2, MinRequests: 2},
	} {
		if _, err := fixture.runs.PutRollbackPolicy(ctx, fixture.projectID, fixture.envID, bad, "admin"); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("%+v was accepted: %v", bad, err)
		}
	}
	saved, err := fixture.runs.PutRollbackPolicy(ctx, fixture.projectID, fixture.envID, RollbackPolicy{
		KeepWarm: false, Automatic: true, WindowSeconds: 120, ErrorRateMargin: 2.5, LatencyFactor: 3, MinRequests: 50,
	}, "admin")
	if err != nil || saved.KeepWarm || saved.Automatic || saved.WindowSeconds != 120 || saved.UpdatedBy != "admin" {
		t.Fatalf("saved = %+v, %v (nothing warm cannot be rolled back to automatically)", saved, err)
	}
	if _, err := fixture.base.DB.Exec(`UPDATE deploy_environments SET kind = 'preview' WHERE id = ?`, fixture.envID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runs.PutRollbackPolicy(ctx, fixture.projectID, fixture.envID, DefaultRollbackPolicy(), "admin"); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("a preview accepted warm terms: %v", err)
	}
}

// warmRuntimeOwner is a runtime owner that can pause, with Docker's states.
type warmRuntimeOwner struct {
	mu     sync.Mutex
	states map[string]string
	calls  []string
}

func newWarmRuntimeOwner(running ...string) *warmRuntimeOwner {
	owner := &warmRuntimeOwner{states: map[string]string{}}
	for _, id := range running {
		owner.states[id] = "running"
	}
	return owner
}

func (o *warmRuntimeOwner) record(call string) {
	o.calls = append(o.calls, call)
}

func (o *warmRuntimeOwner) StartCandidate(_ context.Context, request CandidateRuntimeRequest, _ func(BuildLog) error) (StartedRuntime, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	id := fmt.Sprintf("container-r%d", request.Release.Number)
	o.states[id] = "running"
	o.record("start:" + id)
	return StartedRuntime{Input: ReleaseRuntimeInput{ReleaseID: request.Release.ID, Kind: "container", RuntimeID: id, Host: "127.0.0.1", Port: 32000 + int(request.Release.Number), Metadata: json.RawMessage(`{"version":1}`)}}, nil
}

func (o *warmRuntimeOwner) StartExisting(_ context.Context, runtime ReleaseRuntime, _ map[string]string, _ func(BuildLog) error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.states[runtime.RuntimeID] = "running"
	o.record("start-existing:" + runtime.RuntimeID)
	return nil
}

func (o *warmRuntimeOwner) Stop(_ context.Context, runtime ReleaseRuntime, _ RuntimePlanConfig, _ map[string]string, remove bool, _ func(BuildLog) error) (RuntimeStopEvidence, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.states[runtime.RuntimeID] == "paused" {
		return RuntimeStopEvidence{}, fmt.Errorf("%s was stopped while paused", runtime.RuntimeID)
	}
	if remove {
		delete(o.states, runtime.RuntimeID)
	} else {
		o.states[runtime.RuntimeID] = "exited"
	}
	o.record("stop:" + runtime.RuntimeID)
	return RuntimeStopEvidence{RuntimeID: runtime.RuntimeID, Removed: remove}, nil
}

func (o *warmRuntimeOwner) WarmState(_ context.Context, runtime ReleaseRuntime) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if state, ok := o.states[runtime.RuntimeID]; ok {
		return state, nil
	}
	return "missing", nil
}

func (o *warmRuntimeOwner) PauseRuntime(_ context.Context, runtime ReleaseRuntime) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch o.states[runtime.RuntimeID] {
	case "running":
		o.states[runtime.RuntimeID] = "paused"
		o.record("pause:" + runtime.RuntimeID)
		return nil
	case "paused":
		return nil
	}
	return errWarmRuntimeLost
}

func (o *warmRuntimeOwner) ResumeRuntime(_ context.Context, runtime ReleaseRuntime) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	switch o.states[runtime.RuntimeID] {
	case "paused":
		o.states[runtime.RuntimeID] = "running"
		o.record("resume:" + runtime.RuntimeID)
		return nil
	case "running":
		return nil
	}
	return errWarmRuntimeLost
}

func (o *warmRuntimeOwner) state(id string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if state, ok := o.states[id]; ok {
		return state
	}
	return "missing"
}

// routingProxy remembers which upstream the route points at.
type routingProxy struct {
	mu       sync.Mutex
	upstream string
	applied  []string
	failNext error
}

func (p *routingProxy) ApplyDeploymentRoute(_ context.Context, route proxysvc.DeploymentRoute) (proxysvc.DeploymentRouteResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failNext != nil {
		err := p.failNext
		p.failNext = nil
		return proxysvc.DeploymentRouteResult{Recovered: true}, err
	}
	p.upstream = route.PrimaryUpstream()
	p.applied = append(p.applied, route.PrimaryUpstream())
	return proxysvc.DeploymentRouteResult{Applied: true, Verified: true}, nil
}

func (p *routingProxy) RestoreDeploymentRoute(context.Context, proxysvc.DeploymentRouteSnapshot) error {
	return nil
}

func (p *routingProxy) VerifyDeploymentRoute(context.Context, proxysvc.DeploymentRoute) error {
	return nil
}

func (p *routingProxy) current() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.upstream
}

// sampledRecord answers windows from a list of requests.
type sampledRecord struct {
	mu       sync.Mutex
	requests []sampledRequest
	latency  bool
}

type sampledRequest struct {
	at     time.Time
	status int
	ms     float64
}

func (r *sampledRecord) add(at time.Time, count, status int, ms float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := 0; index < count; index++ {
		r.requests = append(r.requests, sampledRequest{at: at, status: status, ms: ms})
	}
}

func (r *sampledRecord) Window(_ context.Context, _ string, filter accesslog.Filter) (accesslog.Window, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := accesslog.Summary{Classes: map[string]int{}}
	durations := []float64{}
	for _, request := range r.requests {
		if request.at.Before(filter.Since) || request.at.After(filter.Until) {
			continue
		}
		summary.Total++
		summary.Classes[fmt.Sprintf("%dxx", request.status/100)]++
		durations = append(durations, request.ms)
	}
	if summary.Total > 0 {
		summary.ErrorRate = float64(summary.Classes["5xx"]) / float64(summary.Total)
		if r.latency {
			sort.Float64s(durations)
			index := int(math.Ceil(0.95*float64(len(durations)))) - 1
			summary.Latency = &accesslog.Latency{P95: durations[index]}
		}
	}
	return accesslog.Window{
		Result:   &accesslog.Result{Summary: summary},
		Coverage: accesslog.Coverage{Exists: len(r.requests) > 0, Held: len(r.requests)},
		Facts:    accesslog.Facts{Latency: r.latency},
	}, nil
}

type warmFixture struct {
	*releaseStoreFixture
	owner    *warmRuntimeOwner
	proxy    *routingProxy
	record   *sampledRecord
	executor *NormalizedStepExecutor
	first    *ReleaseWithArtifacts
	second   *ReleaseWithArtifacts
	enqueued []RollbackVerdict
}

var warmPlan = RuntimePlanConfig{
	InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
	Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
}

func (f *warmFixture) step(run EngineRun, token string) StepExecution {
	return StepExecution{Run: run, ClaimToken: token, Output: discardStepOutput{}}
}

// deploy takes one release through the steps that matter here: started,
// ready, activated, and its predecessor retired.
func (f *warmFixture) deploy(t *testing.T, revision int, identity string) (*ReleaseWithArtifacts, StepResult) {
	t.Helper()
	f.addPlanWithRuntime(t, revision, strings.Repeat(identity[:1], 40), warmPlan)
	run, lease := f.claimedRun(t, revision)
	release := createRuntimeCandidateWithDomains(t, f.releaseStoreFixture, *run, lease, warmPlan, identity,
		nil, []PlannedDomain{{Hostname: "app.example.test"}})
	execution := f.step(*run, lease.Token)
	plan := mustExecutionPlan(t, f.releaseStoreFixture, *run)
	for _, key := range []StepKey{StepStartCandidate, StepVerifyReadiness, StepActivate} {
		execution.Step = RunStep{Key: key}
		if result := f.executor.Execute(context.Background(), execution); result.State != StepPassed && result.State != StepSkipped {
			t.Fatalf("%s %s = %+v", identity, key, result)
		}
	}
	_ = plan
	execution.Step = RunStep{Key: StepRetirePrevious}
	retired := f.executor.Execute(context.Background(), execution)
	if retired.State != StepPassed && retired.State != StepSkipped {
		t.Fatalf("%s retire = %+v", identity, retired)
	}
	f.finishActivatedRun(t, run.ID, release.Release.ID, lease.Token)
	return release, retired
}

func newWarmFixture(t *testing.T) *warmFixture {
	t.Helper()
	fixture := &warmFixture{
		releaseStoreFixture: newReleaseStoreFixture(t),
		owner:               newWarmRuntimeOwner(),
		proxy:               &routingProxy{},
		record:              &sampledRecord{latency: true},
	}
	fixture.executor = &NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: fixture.owner,
		checks: NewCheckRunner(&checkBackendFake{}), proxy: fixture.proxy,
		sources: &HostSourceAnalyzer{}, builder: &ArtifactBuilder{},
		hostMemory: func() (int64, int64) { return 8 << 30, 16 << 30 },
	}
	return fixture
}

func (f *warmFixture) watcher() *RollbackWatcher {
	watcher := NewRollbackWatcher(f.executor, f.record, func(ctx context.Context, watch RollbackWatch, verdict RollbackVerdict, attempt int) (*EngineRun, error) {
		f.enqueued = append(f.enqueued, verdict)
		metadata, _ := json.Marshal(map[string]any{
			"targetReleaseId": watch.WarmReleaseID, "instant": true, "watchId": watch.ID,
			"automatic": true, "reason": verdict.Reason,
		})
		run, _, err := f.runs.Enqueue(ctx, RunRequest{
			ProjectID: watch.ProjectID, EnvironmentID: watch.EnvironmentID, Operation: OperationRollback,
			Trigger: TriggerAutoRollback, Actor: "system", IdempotencyKey: fmt.Sprintf("rollback-watch-%d-%d", watch.ID, attempt),
			RequestDigest: fmt.Sprintf("instant-%d-%d", watch.ID, attempt), PlanRevision: 1, Priority: 1000,
			SlotClass: SlotLight, Metadata: metadata, Steps: InstantRollbackSteps,
		})
		return run, err
	}, nil, nil)
	watcher.now = func() time.Time { return f.now }
	return watcher
}

// runInstantRollback claims the queued instant rollback and takes it through
// its steps, as the engine would.
func (f *warmFixture) runInstantRollback(t *testing.T) (EngineRun, []StepResult) {
	t.Helper()
	lease, err := f.runs.ClaimNext(context.Background(), "rollback-worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
	if err != nil || lease == nil {
		t.Fatalf("instant rollback claim = %#v, %v", lease, err)
	}
	run, err := f.runs.Run(context.Background(), lease.RunID)
	if err != nil {
		t.Fatal(err)
	}
	results := []StepResult{}
	for _, key := range InstantRollbackSteps {
		result := f.executor.Execute(context.Background(), StepExecution{Run: *run, Step: RunStep{Key: key}, ClaimToken: lease.Token, Output: discardStepOutput{}})
		results = append(results, result)
		if result.State == StepFailed || result.State == StepUnavailable {
			break
		}
	}
	if _, err := f.base.DB.Exec(`UPDATE deploy_runs SET state = 'succeeded', status = 'success', ended_at = ? WHERE id = ?`, f.now.Unix(), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.base.DB.Exec(`DELETE FROM deploy_queue_leases WHERE run_id = ?`, run.ID); err != nil {
		t.Fatal(err)
	}
	return *run, results
}

func (f *warmFixture) liveReleaseID(t *testing.T) int64 {
	t.Helper()
	live, err := f.runs.LiveRelease(context.Background(), f.envID)
	if err != nil {
		t.Fatal(err)
	}
	return live.Release.ID
}

func TestFailingReleaseIsRolledBackToTheSameWarmContainer(t *testing.T) {
	t.Parallel()
	f := newWarmFixture(t)
	first, _ := f.deploy(t, 1, "a-release")
	f.record.add(f.now.Add(30*time.Second), 300, 200, 90)
	f.record.add(f.now.Add(30*time.Second), 1, 500, 90)
	f.now = f.now.Add(time.Minute)
	second, retired := f.deploy(t, 2, "b-release")
	warmID := fmt.Sprintf("container-r%d", first.Release.Number)
	if !strings.Contains(string(retired.Evidence), `"kept":true`) || f.owner.state(warmID) != "paused" {
		t.Fatalf("the previous release was not kept warm: %s, state %s", retired.Evidence, f.owner.state(warmID))
	}
	runtime, _ := f.runs.RuntimeForRelease(context.Background(), first.Release.ID)
	if runtime.State != "warm" {
		t.Fatalf("warm runtime state = %s", runtime.State)
	}
	watch, err := f.runs.OpenRollbackWatch(context.Background(), f.envID)
	if err != nil || watch.ReleaseID != second.Release.ID || watch.WarmReleaseID != first.Release.ID || watch.WarmRuntimeID != warmID {
		t.Fatalf("window = %+v, %v", watch, err)
	}

	// Three failing requests: the window reads them and decides nothing.
	f.now = f.now.Add(10 * time.Second)
	f.record.add(f.now, 3, 502, 40)
	watcher := f.watcher()
	watcher.Tick(context.Background())
	if len(f.enqueued) != 0 {
		t.Fatalf("three requests decided a rollback: %+v", f.enqueued)
	}
	watch, _ = f.runs.OpenRollbackWatch(context.Background(), f.envID)
	if watch.Verdict == nil || watch.Verdict.Code != RollbackVerdictTooFew || watch.Baseline == nil || watch.Baseline.Requests != 301 {
		t.Fatalf("first reading = %+v / baseline %+v", watch.Verdict, watch.Baseline)
	}

	f.now = f.now.Add(10 * time.Second)
	f.record.add(f.now, 40, 500, 40)
	f.record.add(f.now, 20, 200, 40)
	watcher.Tick(context.Background())
	if len(f.enqueued) != 1 || f.enqueued[0].Code != RollbackVerdictErrorRate {
		t.Fatalf("enqueued = %+v", f.enqueued)
	}
	// Asked once; a second pass before the run claims the window asks nothing more.
	watcher.Tick(context.Background())
	if len(f.enqueued) != 1 {
		t.Fatalf("a second pass asked again: %+v", f.enqueued)
	}

	run, results := f.runInstantRollback(t)
	for index, result := range results {
		if result.State != StepPassed && result.State != StepSkipped {
			t.Fatalf("instant rollback step %s = %+v", InstantRollbackSteps[index], result)
		}
	}
	if len(results) != len(InstantRollbackSteps) {
		t.Fatalf("instant rollback ran %d steps", len(results))
	}
	if live := f.liveReleaseID(t); live != first.Release.ID {
		t.Fatalf("live release = %d, want the warm %d", live, first.Release.ID)
	}
	if f.owner.state(warmID) != "running" {
		t.Fatalf("warm container state after rollback = %s", f.owner.state(warmID))
	}
	if got := f.proxy.current(); got != "http://127.0.0.1:32001" {
		t.Fatalf("route upstream = %q, want the warm release's own port", got)
	}
	if f.owner.state(fmt.Sprintf("container-r%d", second.Release.Number)) != "missing" {
		t.Fatal("the rolled-back release's container was not removed")
	}
	starts := 0
	for _, call := range f.owner.calls {
		if call == "start:"+warmID || call == "start-existing:"+warmID {
			starts++
		}
	}
	if starts != 1 {
		t.Fatalf("the warm release was started %d times; it is resumed, never started anew: %v", starts, f.owner.calls)
	}
	rolledBack, _ := f.runs.Release(context.Background(), second.Release.ID)
	restored, _ := f.runs.Release(context.Background(), first.Release.ID)
	if rolledBack.Release.State != "retained" || rolledBack.Release.RolledBackAt == nil || !strings.Contains(rolledBack.Release.RollbackReason, "5xx answers rose") {
		t.Fatalf("rolled back release = %+v", rolledBack.Release)
	}
	if restored.Release.State != "live" || restored.Release.RetiredAt != nil {
		t.Fatalf("restored release = %+v", restored.Release)
	}
	closed, _ := f.runs.RollbackWatch(context.Background(), watch.ID)
	if closed.State != RollbackWatchRolledBack || closed.RollbackRunID != run.ID || closed.EndedAt == nil {
		t.Fatalf("closed window = %+v", closed)
	}
	runRecord, _ := f.runs.Run(context.Background(), run.ID)
	if runRecord.ReleaseID != first.Release.ID {
		t.Fatalf("the rollback run names release %d", runRecord.ReleaseID)
	}
	if event := notificationEventForRun(EngineRun{Operation: OperationRollback, State: RunSucceeded, Metadata: run.Metadata}); event != NotificationEventRolledBack {
		t.Fatalf("an automatic rollback raises %q", event)
	}
	if !notificationEventSelected([]string{NotificationEventFailed}, NotificationEventRolledBack) ||
		notificationEventSelected([]string{NotificationEventSucceeded}, NotificationEventRolledBack) {
		t.Fatal("an automatic rollback does not reach the channels that hear about failures alone")
	}
}

func TestCleanWindowRemovesTheWarmContainerAndSurvivesARestart(t *testing.T) {
	t.Parallel()
	f := newWarmFixture(t)
	first, _ := f.deploy(t, 1, "a-release")
	f.record.add(f.now.Add(30*time.Second), 200, 200, 100)
	f.now = f.now.Add(time.Minute)
	f.deploy(t, 2, "b-release")
	warmID := fmt.Sprintf("container-r%d", first.Release.Number)
	f.now = f.now.Add(time.Minute)
	f.record.add(f.now, 200, 200, 110)
	f.watcher().Tick(context.Background())
	watch, _ := f.runs.OpenRollbackWatch(context.Background(), f.envID)
	if watch.Verdict == nil || watch.Verdict.Code != RollbackVerdictWithinBaseline || f.owner.state(warmID) != "paused" {
		t.Fatalf("mid-window reading = %+v, warm %s", watch.Verdict, f.owner.state(warmID))
	}

	// A restart: a new executor and watcher over the same store, after a
	// Docker restart brought the paused container back running.
	f.owner.mu.Lock()
	f.owner.states[warmID] = "running"
	f.owner.mu.Unlock()
	restarted := &NormalizedStepExecutor{store: f.runs, variables: f.variables, runtime: f.owner, proxy: f.proxy,
		hostMemory: func() (int64, int64) { return 8 << 30, 16 << 30 }}
	f.executor = restarted
	watcher := f.watcher()
	watcher.Tick(context.Background())
	if f.owner.state(warmID) != "paused" {
		t.Fatalf("a restart left the warm container %s", f.owner.state(warmID))
	}
	f.now = watch.ExpiresAt.Add(time.Second)
	watcher.Tick(context.Background())
	if f.owner.state(warmID) != "missing" || len(f.enqueued) != 0 {
		t.Fatalf("after a clean window: warm %s, enqueued %+v", f.owner.state(warmID), f.enqueued)
	}
	closed, _ := f.runs.RollbackWatch(context.Background(), watch.ID)
	if closed.State != RollbackWatchClosed || closed.EndReason != RollbackEndWindowPassed || !strings.Contains(closed.EndMessage, "stayed within") {
		t.Fatalf("closed window = %+v", closed)
	}
	runtime, _ := f.runs.RuntimeForRelease(context.Background(), first.Release.ID)
	retired, _ := f.runs.Release(context.Background(), first.Release.ID)
	if runtime.State != "retired" || retired.Release.RetiredAt == nil {
		t.Fatalf("warm release after its window: runtime %s, release %+v", runtime.State, retired.Release)
	}
}

func TestAnotherRunEndsTheWindowAndNothingIsJudgedWithoutADomain(t *testing.T) {
	t.Parallel()
	f := newWarmFixture(t)
	first, _ := f.deploy(t, 1, "a-release")
	f.now = f.now.Add(time.Minute)
	f.deploy(t, 2, "b-release")
	warmID := fmt.Sprintf("container-r%d", first.Release.Number)

	// A deploy that starts ends the window at its first step.
	f.addPlanWithRuntime(t, 3, strings.Repeat("c", 40), warmPlan)
	run, lease := f.claimedRun(t, 3)
	f.executor.endWarmWindow(context.Background(), f.step(*run, lease.Token))
	if f.owner.state(warmID) != "missing" {
		t.Fatalf("a new deploy left the warm container %s", f.owner.state(warmID))
	}
	latest, _ := f.runs.LatestRollbackWatch(context.Background(), f.envID)
	if latest.State != RollbackWatchClosed || latest.EndReason != RollbackEndSuperseded {
		t.Fatalf("window after another run = %+v", latest)
	}
	if _, err := f.runs.OpenRollbackWatch(context.Background(), f.envID); !errors.Is(err, ErrNoRollbackWatch) {
		t.Fatalf("an open window survived: %v", err)
	}

	// Without a domain a release is kept warm but never judged.
	g := newWarmFixture(t)
	g.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), warmPlan)
	firstRun, firstLease := g.claimedRun(t, 1)
	bare := createRuntimeCandidate(t, g.releaseStoreFixture, *firstRun, firstLease, warmPlan, "bare-a")
	for _, key := range []StepKey{StepStartCandidate, StepVerifyReadiness, StepActivate, StepRetirePrevious} {
		g.executor.Execute(context.Background(), StepExecution{Run: *firstRun, Step: RunStep{Key: key}, ClaimToken: firstLease.Token, Output: discardStepOutput{}})
	}
	g.finishActivatedRun(t, firstRun.ID, bare.Release.ID, firstLease.Token)
	g.now = g.now.Add(time.Minute)
	g.addPlanWithRuntime(t, 2, strings.Repeat("b", 40), warmPlan)
	secondRun, secondLease := g.claimedRun(t, 2)
	bareSecond := createRuntimeCandidate(t, g.releaseStoreFixture, *secondRun, secondLease, warmPlan, "bare-b")
	for _, key := range []StepKey{StepStartCandidate, StepVerifyReadiness, StepActivate, StepRetirePrevious} {
		g.executor.Execute(context.Background(), StepExecution{Run: *secondRun, Step: RunStep{Key: key}, ClaimToken: secondLease.Token, Output: discardStepOutput{}})
	}
	g.finishActivatedRun(t, secondRun.ID, bareSecond.Release.ID, secondLease.Token)
	g.record.add(g.now, 100, 500, 10)
	g.watcher().Tick(context.Background())
	watch, err := g.runs.OpenRollbackWatch(context.Background(), g.envID)
	if err != nil || watch.Verdict == nil || watch.Verdict.Code != RollbackVerdictNoRoute || len(g.enqueued) != 0 {
		t.Fatalf("a release without a domain = %+v, %v, enqueued %+v", watch, err, g.enqueued)
	}
}

func TestLowMemoryAndTurnedOffTermsRetireAsBefore(t *testing.T) {
	t.Parallel()
	f := newWarmFixture(t)
	f.executor.hostMemory = func() (int64, int64) { return 100 << 20, 1 << 30 }
	first, _ := f.deploy(t, 1, "a-release")
	f.now = f.now.Add(time.Minute)
	_, retired := f.deploy(t, 2, "b-release")
	if !strings.Contains(string(retired.Evidence), "below the 256 MB") || f.owner.state(fmt.Sprintf("container-r%d", first.Release.Number)) != "missing" {
		t.Fatalf("a tight host kept a release warm: %s", retired.Evidence)
	}

	g := newWarmFixture(t)
	if _, err := g.runs.PutRollbackPolicy(context.Background(), g.projectID, g.envID, RollbackPolicy{
		KeepWarm: false, WindowSeconds: 600, ErrorRateMargin: 5, LatencyFactor: 2, MinRequests: 20,
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	g.deploy(t, 1, "a-release")
	g.now = g.now.Add(time.Minute)
	_, retired = g.deploy(t, 2, "b-release")
	if !strings.Contains(string(retired.Evidence), "turned off") {
		t.Fatalf("turned-off terms kept a release warm: %s", retired.Evidence)
	}
}

func TestWarmLostAndManualRefusalsCloseTheWindow(t *testing.T) {
	t.Parallel()
	f := newWarmFixture(t)
	first, _ := f.deploy(t, 1, "a-release")
	f.now = f.now.Add(time.Minute)
	f.deploy(t, 2, "b-release")
	warmID := fmt.Sprintf("container-r%d", first.Release.Number)
	f.owner.mu.Lock()
	f.owner.states[warmID] = "exited"
	f.owner.mu.Unlock()
	f.watcher().Tick(context.Background())
	latest, _ := f.runs.LatestRollbackWatch(context.Background(), f.envID)
	if latest.State != RollbackWatchClosed || latest.EndReason != RollbackEndWarmLost {
		t.Fatalf("a vanished warm container left the window %+v", latest)
	}
}

func TestInterruptedRollbackIsSettledFromThePointer(t *testing.T) {
	t.Parallel()
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed=%t", committed), func(t *testing.T) {
			f := newWarmFixture(t)
			first, _ := f.deploy(t, 1, "a-release")
			f.now = f.now.Add(time.Minute)
			second, _ := f.deploy(t, 2, "b-release")
			f.record.add(f.now, 100, 500, 10)
			f.watcher().Tick(context.Background())
			lease, err := f.runs.ClaimNext(context.Background(), "killed-worker", QueueBudget{Heavy: 1, Light: 1}, time.Minute)
			if err != nil || lease == nil {
				t.Fatal(err)
			}
			run, _ := f.runs.Run(context.Background(), lease.RunID)
			steps := []StepKey{StepStartCandidate, StepVerifyReadiness}
			if committed {
				steps = append(steps, StepActivate)
			}
			for _, key := range steps {
				if result := f.executor.Execute(context.Background(), StepExecution{Run: *run, Step: RunStep{Key: key}, ClaimToken: lease.Token, Output: discardStepOutput{}}); result.State == StepFailed {
					t.Fatalf("%s = %+v", key, result)
				}
			}
			if !committed {
				// The route moved before the kill; the pointer did not.
				f.proxy.upstream = "http://127.0.0.1:32001"
			}
			// The engine ends a killed run with restart_evidence_missing.
			if _, err := f.base.DB.Exec(`UPDATE deploy_runs SET state = 'failed', status = 'failed', terminal_code = 'restart_evidence_missing', ended_at = ? WHERE id = ?`, f.now.Unix(), run.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.base.DB.Exec(`DELETE FROM deploy_queue_leases WHERE run_id = ?`, run.ID); err != nil {
				t.Fatal(err)
			}
			f.executor = &NormalizedStepExecutor{store: f.runs, variables: f.variables, runtime: f.owner, proxy: f.proxy,
				hostMemory: func() (int64, int64) { return 8 << 30, 16 << 30 }}
			f.watcher().Tick(context.Background())
			watch, _ := f.runs.LatestRollbackWatch(context.Background(), f.envID)
			warmID := fmt.Sprintf("container-r%d", first.Release.Number)
			if committed {
				if f.liveReleaseID(t) != first.Release.ID || watch.State != RollbackWatchRolledBack ||
					f.owner.state(fmt.Sprintf("container-r%d", second.Release.Number)) != "missing" {
					t.Fatalf("a committed rollback was not finished: live %d, window %+v", f.liveReleaseID(t), watch)
				}
				return
			}
			if f.liveReleaseID(t) != second.Release.ID || watch.State != RollbackWatchWatching ||
				f.proxy.current() != "http://127.0.0.1:32002" || f.owner.state(warmID) != "paused" {
				t.Fatalf("an uncommitted rollback was not put back: live %d, route %s, warm %s, window %+v",
					f.liveReleaseID(t), f.proxy.current(), f.owner.state(warmID), watch)
			}
		})
	}
}
