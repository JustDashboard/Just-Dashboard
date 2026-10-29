package deploy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func TestSleepPlanValidation(t *testing.T) {
	base := func() PlanConfiguration {
		return PlanConfiguration{
			Build:   BuildPlanConfig{Method: BuildImage},
			Runtime: RuntimePlanConfig{Sleep: &SleepPlan{Enabled: true}},
			Domains: []PlannedDomain{{Hostname: "app.example.test"}},
		}
	}
	if err := validateSleepPlan(base()); err != nil {
		t.Fatalf("a single container behind a domain: %v", err)
	}
	refused := map[string]func(*PlanConfiguration){
		"idle below five minutes":  func(c *PlanConfiguration) { c.Runtime.Sleep.IdleMinutes = 4 },
		"idle above a day":         func(c *PlanConfiguration) { c.Runtime.Sleep.IdleMinutes = 1441 },
		"wake bound below five s":  func(c *PlanConfiguration) { c.Runtime.Sleep.WakeTimeoutSeconds = 4 },
		"wake bound above ten min": func(c *PlanConfiguration) { c.Runtime.Sleep.WakeTimeoutSeconds = 601 },
		"a Compose stack":          func(c *PlanConfiguration) { c.Build.Method = BuildCompose },
		"host networking":          func(c *PlanConfiguration) { c.Runtime.HostNetwork = true },
		"no served domain": func(c *PlanConfiguration) {
			c.Domains = []PlannedDomain{{Hostname: "www.example.test", RedirectTo: "example.test"}}
		},
		"Docker's health verdict": func(c *PlanConfiguration) {
			c.Checks = []PlannedCheck{{Name: "healthy", Kind: string(CheckDockerHealth), Phase: "readiness", Required: true}}
		},
		"the public route": func(c *PlanConfiguration) {
			c.Checks = []PlannedCheck{{Name: "public", Kind: string(CheckPublicRoute), Phase: "readiness", Required: true}}
		},
	}
	for name, change := range refused {
		c := base()
		change(&c)
		var validation *ValidationError
		if err := validateSleepPlan(c); err == nil || !errors.As(err, &validation) || !strings.HasPrefix(validation.Field, "runtime.sleep.") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A Docker health check that is not required, or is not a readiness
	// check, does not stand in the way.
	c := base()
	c.Checks = []PlannedCheck{{Name: "healthy", Kind: string(CheckDockerHealth), Phase: "readiness"}, {Name: "smoke", Kind: string(CheckPublicRoute), Phase: "smoke", Required: true}}
	if err := validateSleepPlan(c); err != nil {
		t.Fatalf("an optional health check: %v", err)
	}
	// Off with no terms is stored as nothing; off with terms keeps them.
	if canonicalSleepPlan(&SleepPlan{}) != nil || canonicalSleepPlan(&SleepPlan{IdleMinutes: 30}) == nil {
		t.Fatal("canonical sleep plan")
	}
	if got := sleepLabel(&SleepPlan{Enabled: true}); got != "after 15 idle minutes, woken within 60 s" {
		t.Fatalf("label = %q", got)
	}
	if hold := (&SleepPlan{Enabled: true, WakeTimeoutSeconds: 120}).holdSeconds(); hold != maxWakeHoldSeconds {
		t.Fatalf("hold = %d", hold)
	}
	if conflict := SleepScheduleConflict([]ScheduleStep{{Action: "backup"}, {Action: "container_command"}}); conflict == "" {
		t.Fatal("a scheduled command in the container is no conflict")
	}
}

// sleepRecord answers the idle question from a list of request times.
type sleepRecord struct {
	mu       sync.Mutex
	requests []time.Time
}

func (r *sleepRecord) add(at time.Time) {
	r.mu.Lock()
	r.requests = append(r.requests, at)
	r.mu.Unlock()
}

func (r *sleepRecord) Window(_ context.Context, _ string, filter accesslog.Filter) (accesslog.Window, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := accesslog.Summary{}
	var last time.Time
	for _, at := range r.requests {
		if at.Before(filter.Since) || (!filter.Until.IsZero() && at.After(filter.Until)) {
			continue
		}
		summary.Total++
		if at.After(last) {
			last = at
		}
	}
	if summary.Total > 0 {
		stamp := last.Format(time.RFC3339Nano)
		summary.Last = &stamp
	}
	return accesslog.Window{Result: &accesslog.Result{Summary: summary}, Coverage: accesslog.Coverage{Exists: true}}, nil
}

// sleepingProxy is the routing proxy with a sleeping switch.
type sleepingProxy struct {
	*routingProxy
	mu      sync.Mutex
	on      bool
	ready   bool
	sockets []proxysvc.WakeSocket
}

func (p *sleepingProxy) DeploymentSleeping(context.Context, string) (proxysvc.SleepSwitch, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return proxysvc.SleepSwitch{Driver: "nginx", Ready: p.ready, On: p.on}, nil
}

func (p *sleepingProxy) SetDeploymentSleeping(_ context.Context, _ string, on bool) (proxysvc.SleepSwitch, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if on && !p.ready {
		return proxysvc.SleepSwitch{Driver: "nginx"}, proxysvc.ErrSleepUnready
	}
	p.on = on
	return proxysvc.SleepSwitch{Driver: "nginx", Ready: p.ready, On: on}, nil
}

func (p *sleepingProxy) RefreshDeploymentRoute(context.Context, string) (string, error) {
	return "", nil
}

func (p *sleepingProxy) WakeSockets(context.Context) ([]proxysvc.WakeSocket, error) {
	return p.sockets, nil
}

func (p *sleepingProxy) WakeSocketReached(context.Context, proxysvc.WakeSocket) bool { return true }

func (p *sleepingProxy) switchOn() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.on
}

// listeningOwner starts candidates on a port something listens on, so a
// wake's readiness has an application to find.
type listeningOwner struct {
	*warmRuntimeOwner
	port int
}

func (o *listeningOwner) StartCandidate(ctx context.Context, request CandidateRuntimeRequest, emit func(BuildLog) error) (StartedRuntime, error) {
	started, err := o.warmRuntimeOwner.StartCandidate(ctx, request, emit)
	started.Input.Port = o.port
	return started, err
}

type sleepFixture struct {
	*warmFixture
	sleepProxy *sleepingProxy
	sleeper    *Sleeper
	audits     []string
	record     *sleepRecord
	clock      time.Time
	release    *ReleaseWithArtifacts
}

func newSleepFixture(t *testing.T, plan *SleepPlan) *sleepFixture {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		_ = http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "the application") }))
	}()
	warm := newWarmFixture(t)
	owner := &listeningOwner{warmRuntimeOwner: warm.owner, port: listener.Addr().(*net.TCPAddr).Port}
	proxy := &sleepingProxy{routingProxy: warm.proxy, ready: true, sockets: []proxysvc.WakeSocket{{Path: filepath.Join(t.TempDir(), "wake.sock"), Proxy: "nginx"}}}
	warm.executor.runtime = owner
	warm.executor.proxy = proxy
	f := &sleepFixture{warmFixture: warm, sleepProxy: proxy, record: &sleepRecord{}, clock: warm.now}
	f.sleeper = NewSleeper(warm.executor, f.record, func(_ context.Context, action string, _ SleepEnvironment, actor string, _ map[string]any, success bool) {
		f.audits = append(f.audits, action+" "+actor+" "+map[bool]string{true: "ok", false: "failed"}[success])
	}, nil).WithClock(func() time.Time { return f.clock })
	runtime := warmPlan
	runtime.Sleep = plan
	f.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), runtime)
	run, lease := f.claimedRun(t, 1)
	release := createRuntimeCandidateWithDomains(t, f.releaseStoreFixture, *run, lease, runtime, "release-one", nil, []PlannedDomain{{Hostname: "app.example.test"}})
	execution := f.step(*run, lease.Token)
	for _, key := range []StepKey{StepStartCandidate, StepVerifyReadiness, StepActivate} {
		execution.Step = RunStep{Key: key}
		if result := f.executor.Execute(context.Background(), execution); result.State != StepPassed && result.State != StepSkipped {
			t.Fatalf("%s = %+v", key, result)
		}
	}
	f.finishActivatedRun(t, run.ID, release.Release.ID, lease.Token)
	f.release = release
	// The socket is served, as a running dashboard serves it.
	f.sleeper.sockets.ensure(context.Background())
	t.Cleanup(f.sleeper.sockets.close)
	return f
}

func (f *sleepFixture) container(t *testing.T) string {
	t.Helper()
	runtime, err := f.runs.RuntimeForRelease(context.Background(), f.release.Release.ID)
	if err != nil {
		t.Fatal(err)
	}
	return runtime.RuntimeID
}

func (f *sleepFixture) wake(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/wake/"+deploymentRouteName(f.envID), nil)
	request.Header.Set("X-Forwarded-Host", "app.example.test")
	f.sleeper.ServeHTTP(recorder, request)
	return recorder
}

// The idle decision and the wake, end to end on fakes: a release that has
// served a request stays awake until the idle minutes pass, then its
// container is stopped — not removed — with the switch written first; the
// proxy's wake starts it, waits for it to answer, clears the switch and
// closes the stretch; and a wake with nothing asleep is the 502 it was.
func TestIdleEnvironmentSleepsAndARequestWakesIt(t *testing.T) {
	f := newSleepFixture(t, &SleepPlan{Enabled: true, IdleMinutes: 5, WakeTimeoutSeconds: 5})
	ctx := context.Background()
	id := f.container(t)
	f.record.add(f.clock.Add(time.Minute))
	f.clock = f.clock.Add(3 * time.Minute)
	f.sleeper.Tick(ctx)
	decision, _ := f.sleeper.Decision(f.envID)
	if f.owner.state(id) != "running" || decision.Asleep || decision.SleepsAt == nil || !decision.SleepsAt.Equal(f.now.Add(6*time.Minute)) {
		t.Fatalf("three minutes in: %s %+v", f.owner.state(id), decision)
	}
	f.clock = f.clock.Add(4 * time.Minute)
	f.sleeper.Tick(ctx)
	if f.owner.state(id) != "exited" || !f.sleepProxy.switchOn() {
		t.Fatalf("after the idle minutes: container %s, switch %t", f.owner.state(id), f.sleepProxy.switchOn())
	}
	span, err := f.runs.OpenSleepSpan(ctx, f.envID)
	if err != nil || span == nil || span.ContainerID != id || span.LastRequestAt == nil || !strings.Contains(span.Reason, "No request reached it for 5 minutes") {
		t.Fatalf("open stretch = %+v, %v", span, err)
	}
	if strings.Join(f.audits, ",") != "deploy.environment.idle system ok,deploy.environment.sleep system ok" {
		t.Fatalf("audits = %v", f.audits)
	}
	status, err := f.sleeper.Status(ctx, f.envID)
	if err != nil || status.State != "asleep" || status.Since == nil || !status.Enabled || status.IdleMinutes != 5 {
		t.Fatalf("status = %+v, %v", status, err)
	}
	// A second pass while asleep leaves it so.
	f.sleeper.Tick(ctx)
	if f.owner.state(id) != "exited" {
		t.Fatal("a pass woke a sleeping environment")
	}

	woke := f.wake(t)
	if woke.Code != http.StatusOK || f.owner.state(id) != "running" || f.sleepProxy.switchOn() {
		t.Fatalf("wake = %d %q; container %s, switch %t", woke.Code, woke.Body.String(), f.owner.state(id), f.sleepProxy.switchOn())
	}
	spans, _ := f.runs.SleepSpans(ctx, f.envID, f.now.Add(-time.Hour), 10)
	if len(spans) != 1 || spans[0].WokeAt == nil || spans[0].WakeTrigger != WakeByRequest || spans[0].WakeDetail != "app.example.test" || spans[0].WakeError != "" {
		t.Fatalf("closed stretch = %+v", spans)
	}
	if last := f.audits[len(f.audits)-1]; last != "deploy.environment.wake system ok" {
		t.Fatalf("wake audit = %v", f.audits)
	}
	// The switch an instant behind: nothing asleep, the application up.
	if again := f.wake(t); again.Code != http.StatusOK {
		t.Fatalf("a wake with it awake = %d", again.Code)
	}
	// Stopped by hand, it is not the wake's to start.
	f.owner.mu.Lock()
	f.owner.states[id] = "exited"
	f.owner.mu.Unlock()
	if down := f.wake(t); down.Code != http.StatusForbidden {
		t.Fatalf("a wake for a container stopped by hand = %d", down.Code)
	}
	// And the idle minutes run again from the wake, not from the old request.
	f.owner.mu.Lock()
	f.owner.states[id] = "running"
	f.owner.mu.Unlock()
	f.sleeper.Tick(ctx)
	if f.owner.state(id) != "running" {
		t.Fatal("it went back to sleep at once after waking")
	}
}

// Nothing is put to sleep while a run is queued, while the route does not
// carry the wake, or while the wake is not listening; and a run starting on
// a sleeping environment wakes it first, while a stop only ends the
// stretch.
func TestSleepWaitsForRunsAndRunsWakeIt(t *testing.T) {
	f := newSleepFixture(t, &SleepPlan{Enabled: true, IdleMinutes: 5, WakeTimeoutSeconds: 5})
	ctx := context.Background()
	id := f.container(t)
	f.clock = f.clock.Add(10 * time.Minute)

	f.sleepProxy.ready = false
	f.sleeper.Tick(ctx)
	if decision, _ := f.sleeper.Decision(f.envID); f.owner.state(id) != "running" || !strings.Contains(decision.Reason, "does not wake") {
		t.Fatalf("a route without the wake: %s %+v", f.owner.state(id), decision)
	}
	f.sleepProxy.ready = true

	f.sleeper.sockets.close()
	f.sleeper.Tick(ctx)
	if decision, _ := f.sleeper.Decision(f.envID); f.owner.state(id) != "running" || !strings.Contains(decision.Reason, "not listening") {
		t.Fatalf("no wake listening: %s %+v", f.owner.state(id), decision)
	}
	f.sleeper.sockets.ensure(ctx)

	run, _, err := f.runs.Enqueue(ctx, RunRequest{ProjectID: f.projectID, EnvironmentID: f.envID, Operation: OperationRestart,
		Trigger: TriggerManual, Actor: "admin", RequestDigest: "queued-restart", PlanRevision: 1, SlotClass: SlotLight})
	if err != nil {
		t.Fatal(err)
	}
	f.sleeper.Tick(ctx)
	if decision, _ := f.sleeper.Decision(f.envID); f.owner.state(id) != "running" || !strings.Contains(decision.Reason, "run is queued") {
		t.Fatalf("a queued run: %s %+v", f.owner.state(id), decision)
	}
	if _, err := f.base.DB.Exec(`UPDATE deploy_runs SET state = 'succeeded', status = 'success' WHERE id = ?`, run.ID); err != nil {
		t.Fatal(err)
	}
	f.sleeper.Tick(ctx)
	if f.owner.state(id) != "exited" {
		t.Fatalf("idle with nothing in the way: %s", f.owner.state(id))
	}

	// A run starting on it wakes it before its first step.
	f.executor.endSleep(ctx, StepExecution{Run: EngineRun{ID: 99, RunNumber: 7, EnvironmentID: f.envID, Operation: OperationDeploy}, Output: discardStepOutput{}})
	if f.owner.state(id) != "running" || f.sleepProxy.switchOn() {
		t.Fatalf("a run on a sleeping environment: %s, switch %t", f.owner.state(id), f.sleepProxy.switchOn())
	}
	spans, _ := f.runs.SleepSpans(ctx, f.envID, f.now.Add(-time.Hour), 10)
	if len(spans) != 1 || spans[0].WakeTrigger != WakeByRun || spans[0].WakeDetail != "run #7" {
		t.Fatalf("stretch woken by a run = %+v", spans)
	}

	// A stop ends the stretch and leaves the container to the stop.
	f.clock = f.clock.Add(time.Hour)
	f.sleeper.Tick(ctx)
	if f.owner.state(id) != "exited" {
		t.Fatal("it did not sleep again")
	}
	f.executor.endSleep(ctx, StepExecution{Run: EngineRun{ID: 100, RunNumber: 8, EnvironmentID: f.envID, Operation: OperationStop}, Output: discardStepOutput{}})
	if open, _ := f.runs.OpenSleepSpan(ctx, f.envID); open != nil || f.owner.state(id) != "exited" || f.sleepProxy.switchOn() {
		t.Fatalf("a stop on a sleeping environment: %+v, %s", open, f.owner.state(id))
	}
}

// A stretch the environment is no longer in is closed by the next pass:
// its container started by hand.
func TestSleepIsRepairedWhenTheContainerRunsAgain(t *testing.T) {
	f := newSleepFixture(t, &SleepPlan{Enabled: true, IdleMinutes: 5})
	ctx := context.Background()
	id := f.container(t)
	f.clock = f.clock.Add(10 * time.Minute)
	f.sleeper.Tick(ctx)
	if f.owner.state(id) != "exited" {
		t.Fatal("not asleep")
	}
	f.owner.mu.Lock()
	f.owner.states[id] = "running"
	f.owner.mu.Unlock()
	f.sleeper.Tick(ctx)
	spans, _ := f.runs.SleepSpans(ctx, f.envID, f.now.Add(-time.Hour), 10)
	if len(spans) != 1 || spans[0].WakeTrigger != WakeByRepair || f.sleepProxy.switchOn() {
		t.Fatalf("repaired stretch = %+v, switch %t", spans, f.sleepProxy.switchOn())
	}
}

// The socket answers the proxy and nothing else: this test's own process is
// not nginx or Caddy, so it is refused before anything is woken.
func TestWakeSocketAnswersOnlyTheProxy(t *testing.T) {
	f := newSleepFixture(t, &SleepPlan{Enabled: true, IdleMinutes: 5})
	path := f.sleepProxy.sockets[0].Path
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", path)
	}}}
	response, err := client.Get("http://jd-wake/wake/" + deploymentRouteName(f.envID))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "only the proxy") {
		t.Fatalf("a wake from another process = %d %q", response.StatusCode, body)
	}
	if !wakePeerAllowed("nginx") || !wakePeerAllowed("caddy") || wakePeerAllowed("") || wakePeerAllowed("curl") {
		t.Fatal("peer names")
	}
	// A request that is not a wake is not one.
	recorder := httptest.NewRecorder()
	f.sleeper.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/wake/just-dashboard-env-1.conf.bak", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("a malformed wake = %d", recorder.Code)
	}
}

// Asleep by design is not downtime: the stretch is cut out of the down
// intervals, and an environment asleep now is not down now.
func TestAvailabilityLeavesSleepOut(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	spans := []LiveSpan{{ReleaseID: 1, ReleaseNumber: 1, From: now.Add(-48 * time.Hour)}}
	owner := map[string]string{"release-id": "1", "environment-id": "7"}
	events := []dockerx.Event{
		{Time: now.Add(-3 * time.Hour), Type: "container", Action: "die", ID: "c1", Owner: owner},
		{Time: now.Add(-2 * time.Hour), Type: "container", Action: "start", ID: "c1", Owner: owner},
		{Time: now.Add(-time.Hour), Type: "container", Action: "die", ID: "c1", Owner: owner},
	}
	woke := now.Add(-2 * time.Hour)
	asleep := []SleepSpan{
		{SleptAt: now.Add(-3 * time.Hour), WokeAt: &woke},
		{SleptAt: now.Add(-time.Hour)},
	}
	without := ComputeAvailability(now, now.Add(-72*time.Hour), spans, events)
	with := ComputeAvailability(now, now.Add(-72*time.Hour), spans, events, asleep...)
	if !without.Down || without.Windows[0].DownSeconds != 2*3600 {
		t.Fatalf("without sleep = %+v", without.Windows[0])
	}
	if with.Down || with.Windows[0].DownSeconds != 0 {
		t.Fatalf("with sleep = down %t %+v", with.Down, with.Windows[0])
	}
}
