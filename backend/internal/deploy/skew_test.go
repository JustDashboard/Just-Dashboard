package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func TestSkewProtectionIsRefusedWhereNoReleaseIsKeptBeside(t *testing.T) {
	t.Parallel()
	base := func() PlanConfiguration {
		return PlanConfiguration{
			Build: BuildPlanConfig{Method: BuildImage, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
			Runtime: RuntimePlanConfig{
				Image: "nginx:1.27", InternalPort: 80, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen, SkewProtection: true,
				Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
			},
			Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
			Domains: []PlannedDomain{{Hostname: "app.example.test", Ownership: OwnershipManaged}},
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("a blue/green plan with a domain was refused: %v", err)
	}
	cases := []struct {
		name   string
		change func(*PlanConfiguration)
		reason string
	}{
		{"stop first", func(p *PlanConfiguration) { p.Runtime.Strategy = StrategyStopFirst }, "stop-first"},
		{"no domain", func(p *PlanConfiguration) { p.Domains = []PlannedDomain{} }, "no domain"},
		{"compose", func(p *PlanConfiguration) { p.Build.Method = BuildCompose }, "Compose stack"},
	}
	for _, tc := range cases {
		plan := base()
		tc.change(&plan)
		err := validateSkewProtection(plan)
		var field *ValidationError
		if err == nil || !errors.As(err, &field) || field.Field != "runtime.skewProtection" || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: %v, want a runtime.skewProtection refusal naming %q", tc.name, err, tc.reason)
		}
	}
	if item, ok := skewFinding(base()); !ok || item.Code != "skew_protection" || item.FieldID != "runtime.skewProtection" || !strings.Contains(item.Means, "kept running rather than paused") {
		t.Fatalf("finding = %+v", item)
	}
}

// skewProxy keeps every route it was given.
type skewProxy struct {
	mu     sync.Mutex
	routes []proxysvc.DeploymentRoute
}

func (p *skewProxy) ApplyDeploymentRoute(_ context.Context, route proxysvc.DeploymentRoute) (proxysvc.DeploymentRouteResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes = append(p.routes, route)
	return proxysvc.DeploymentRouteResult{Applied: true, Verified: true}, nil
}

func (p *skewProxy) RestoreDeploymentRoute(context.Context, proxysvc.DeploymentRouteSnapshot) error {
	return nil
}

func (p *skewProxy) VerifyDeploymentRoute(context.Context, proxysvc.DeploymentRoute) error {
	return nil
}

func (p *skewProxy) last() proxysvc.DeploymentRoute {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.routes[len(p.routes)-1]
}

// pinnedOf names the releases a route keeps for their visitors.
func pinnedOf(route proxysvc.DeploymentRoute) string {
	if route.Skew == nil {
		return "no skew"
	}
	names := []string{}
	for _, pinned := range route.Skew.Pinned {
		names = append(names, pinned.Release+"="+strings.Join(pinned.Upstreams, ","))
	}
	return strings.Join(names, " ")
}

// labelledRecord answers windows by the release each request names.
type labelledRecord struct {
	mu       sync.Mutex
	requests []labelledRequest
}

type labelledRequest struct {
	at      time.Time
	release string
	status  int
}

func (r *labelledRecord) add(at time.Time, release string, count, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index := 0; index < count; index++ {
		r.requests = append(r.requests, labelledRequest{at: at, release: release, status: status})
	}
}

func (r *labelledRecord) Window(_ context.Context, _ string, filter accesslog.Filter) (accesslog.Window, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := accesslog.Summary{Classes: map[string]int{}}
	for _, request := range r.requests {
		if request.at.Before(filter.Since) || request.at.After(filter.Until) || (filter.Release != "" && request.release != filter.Release) {
			continue
		}
		summary.Total++
		summary.Classes[fmt.Sprintf("%dxx", request.status/100)]++
	}
	if summary.Total > 0 {
		summary.ErrorRate = float64(summary.Classes["5xx"]) / float64(summary.Total)
	}
	return accesslog.Window{Result: &accesslog.Result{Summary: summary}, Coverage: accesslog.Coverage{Exists: true, Held: len(r.requests)}}, nil
}

var skewPlan = func() RuntimePlanConfig {
	plan := warmPlan
	plan.SkewProtection = true
	return plan
}()

func newSkewFixture(t *testing.T) (*warmFixture, *skewProxy, *labelledRecord) {
	t.Helper()
	f := newWarmFixture(t)
	proxy, record := &skewProxy{}, &labelledRecord{}
	f.executor.proxy = proxy
	return f, proxy, record
}

// deploySkew is warmFixture.deploy with skew protection on.
func deploySkew(t *testing.T, f *warmFixture, revision int, identity string) (*ReleaseWithArtifacts, StepResult) {
	t.Helper()
	f.addPlanWithRuntime(t, revision, strings.Repeat(identity[:1], 40), skewPlan)
	run, lease := f.claimedRun(t, revision)
	release := createRuntimeCandidateWithDomains(t, f.releaseStoreFixture, *run, lease, skewPlan, identity,
		nil, []PlannedDomain{{Hostname: "app.example.test"}})
	execution := f.step(*run, lease.Token)
	for _, key := range []StepKey{StepStartCandidate, StepVerifyReadiness, StepActivate, StepRetirePrevious} {
		execution.Step = RunStep{Key: key}
		if result := f.executor.Execute(context.Background(), execution); result.State != StepPassed && result.State != StepSkipped {
			t.Fatalf("%s %s = %+v", identity, key, result)
		} else if key == StepRetirePrevious {
			f.finishActivatedRun(t, run.ID, release.Release.ID, lease.Token)
			return release, result
		}
	}
	return release, StepResult{}
}

func TestSkewProtectionKeepsTheWarmReleaseRunningForItsVisitors(t *testing.T) {
	t.Parallel()
	f, proxy, record := newSkewFixture(t)
	first, _ := deploySkew(t, f, 1, "a-release")
	a := releaseLabel(first.Release.ID)
	if route := proxy.last(); route.Skew == nil || route.Skew.Release != a || route.Skew.MaxAge != 600 || len(route.Skew.Pinned) != 0 {
		t.Fatalf("the first release's route = %+v", route.Skew)
	}
	f.now = f.now.Add(time.Minute)
	second, retired := deploySkew(t, f, 2, "b-release")
	b := releaseLabel(second.Release.ID)
	warmID := fmt.Sprintf("container-r%d", first.Release.Number)

	// The cutover's route is the new release's, keeping the previous one
	// for the visitors whose cookie names it, and nothing paused it.
	if route := proxy.last(); route.Skew == nil || route.Skew.Release != b || pinnedOf(route) != a+"=http://127.0.0.1:32001" {
		t.Fatalf("the cutover route keeps %q for %+v", pinnedOf(route), route.Skew)
	}
	if !strings.Contains(string(retired.Evidence), `"kept":true`) || f.owner.state(warmID) != "running" {
		t.Fatalf("kept warm: %s, state %s", retired.Evidence, f.owner.state(warmID))
	}
	for _, call := range f.owner.calls {
		if call == "pause:"+warmID {
			t.Fatalf("the warm release was paused: %v", f.owner.calls)
		}
	}
	watch, err := f.runs.OpenRollbackWatch(context.Background(), f.envID)
	if err != nil || !watch.WarmRunning {
		t.Fatalf("window = %+v, %v", watch, err)
	}

	// The watcher judges the new release on its own answers and counts the
	// warm release's beside them; a restart that left it paused is undone.
	f.now = f.now.Add(10 * time.Second)
	record.add(f.now, b, 100, 200)
	record.add(f.now, a, 40, 200)
	f.owner.mu.Lock()
	f.owner.states[warmID] = "paused"
	f.owner.mu.Unlock()
	watcher := NewRollbackWatcher(f.executor, record, nil, nil, nil)
	watcher.now = func() time.Time { return f.now }
	watcher.Tick(context.Background())
	watch, _ = f.runs.OpenRollbackWatch(context.Background(), f.envID)
	if watch.Observed == nil || watch.Observed.Requests != 100 || watch.WarmServed == nil || watch.WarmServed.Requests != 40 {
		t.Fatalf("reading = observed %+v, warm served %+v", watch.Observed, watch.WarmServed)
	}
	if f.owner.state(warmID) != "running" {
		t.Fatalf("a running warm release left %s", f.owner.state(warmID))
	}
	if read, err := f.runs.RollbackWatch(context.Background(), watch.ID); err != nil || read.WarmServed == nil || read.WarmServed.Requests != 40 {
		t.Fatalf("the window's read = %+v, %v", read, err)
	}

	// The window's end takes it off the route before it is removed.
	routes := len(proxy.routes)
	f.now = watch.ExpiresAt.Add(time.Second)
	watcher.Tick(context.Background())
	if f.owner.state(warmID) != "missing" || len(proxy.routes) != routes+1 {
		t.Fatalf("after the window: warm %s, %d routes written", f.owner.state(warmID), len(proxy.routes)-routes)
	}
	if route := proxy.last(); route.Skew == nil || route.Skew.Release != b || len(route.Skew.Pinned) != 0 {
		t.Fatalf("the route after the window = %+v", route.Skew)
	}
}

func TestSkewProtectionNeverKeepsVisitorsOnARolledBackRelease(t *testing.T) {
	t.Parallel()
	f, proxy, record := newSkewFixture(t)
	first, _ := deploySkew(t, f, 1, "a-release")
	a := releaseLabel(first.Release.ID)
	f.now = f.now.Add(time.Minute)
	record.add(f.now.Add(-30*time.Second), a, 200, 200)
	second, _ := deploySkew(t, f, 2, "b-release")
	b := releaseLabel(second.Release.ID)
	f.now = f.now.Add(10 * time.Second)
	record.add(f.now, b, 40, 500)
	record.add(f.now, b, 20, 200)
	watcher := f.watcher()
	watcher.record = record
	watcher.Tick(context.Background())
	if len(f.enqueued) != 1 {
		t.Fatalf("enqueued = %+v", f.enqueued)
	}
	_, results := f.runInstantRollback(t)
	for index, result := range results {
		if result.State != StepPassed && result.State != StepSkipped {
			t.Fatalf("instant rollback step %s = %+v", InstantRollbackSteps[index], result)
		}
	}
	// The route goes back to the warm release keeping nobody: a visitor the
	// rolled-back release answered is answered by the warm one and told so.
	if live := f.liveReleaseID(t); live != first.Release.ID {
		t.Fatalf("live release = %d", live)
	}
	if route := proxy.last(); route.Skew == nil || route.Skew.Release != a || len(route.Skew.Pinned) != 0 || route.PrimaryUpstream() != "http://127.0.0.1:32001" {
		t.Fatalf("the route after the rollback = %+v (%s)", route.Skew, route.PrimaryUpstream())
	}
	if f.owner.state(fmt.Sprintf("container-r%d", second.Release.Number)) != "missing" {
		t.Fatal("the rolled-back release was not removed")
	}
}

func TestSkewProtectionTakesAPreviousReleaseNotKeptWarmOffTheRouteFirst(t *testing.T) {
	t.Parallel()
	f, proxy, _ := newSkewFixture(t)
	if _, err := f.runs.PutRollbackPolicy(context.Background(), f.projectID, f.envID, RollbackPolicy{
		KeepWarm: false, WindowSeconds: 300, ErrorRateMargin: 5, LatencyFactor: 2, MinRequests: 20,
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	first, _ := deploySkew(t, f, 1, "a-release")
	f.now = f.now.Add(time.Minute)
	second, retired := deploySkew(t, f, 2, "b-release")
	if !strings.Contains(string(retired.Evidence), "turned off") || f.owner.state(fmt.Sprintf("container-r%d", first.Release.Number)) != "missing" {
		t.Fatalf("retired = %s", retired.Evidence)
	}
	// The cutover kept it for its visitors through the drain; the route was
	// rewritten without it before it was stopped. The cookie lasts the
	// window the terms set.
	cutover := proxy.routes[len(proxy.routes)-2]
	if pinnedOf(cutover) == "" || cutover.Skew.MaxAge != 300 {
		t.Fatalf("the cutover route = %+v", cutover.Skew)
	}
	if route := proxy.last(); route.Skew == nil || route.Skew.Release != releaseLabel(second.Release.ID) || len(route.Skew.Pinned) != 0 {
		t.Fatalf("the route after retirement = %+v", route.Skew)
	}
}
