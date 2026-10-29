package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

func tailnetOnlyPlan(allow ...string) RuntimePlanConfig {
	plan := tailnetPreviewPlan()
	plan.Tailnet = &RouteTailnet{Allow: allow}
	return plan
}

// gateProxyFake is the proxy with the front's half: it records the fronts it
// was asked for, verifies and removes them, and fails on request. Its
// snapshot says whether a front of that name stood before, as the real one's
// does, and a restore puts that back.
type gateProxyFake struct {
	countingActivationProxy
	mu       sync.Mutex
	applied  []proxysvc.TailnetGateRoute
	verified int
	removed  []string
	restored []proxysvc.DeploymentRouteSnapshot
	applyErr error
	standing map[string]bool
}

func (p *gateProxyFake) ApplyTailnetGate(_ context.Context, route proxysvc.TailnetGateRoute) (proxysvc.DeploymentRouteResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.applied = append(p.applied, route)
	if p.standing == nil {
		p.standing = map[string]bool{}
	}
	result := proxysvc.DeploymentRouteResult{Snapshot: proxysvc.DeploymentRouteSnapshot{
		Name: route.Name, Driver: proxysvc.TailnetGateDriver, Version: 1, Existed: p.standing[route.Name]}}
	if p.applyErr != nil {
		result.Recovered = true
		return result, p.applyErr
	}
	p.standing[route.Name] = true
	result.Applied, result.Verified = true, true
	return result, nil
}

func (p *gateProxyFake) VerifyTailnetGate(context.Context, proxysvc.TailnetGateRoute) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.verified++
	return nil
}

func (p *gateProxyFake) RemoveTailnetGate(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removed = append(p.removed, name)
	delete(p.standing, name)
	return nil
}

func (p *gateProxyFake) RestoreDeploymentRoute(_ context.Context, snapshot proxysvc.DeploymentRouteSnapshot) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.restored = append(p.restored, snapshot)
	if p.standing == nil {
		p.standing = map[string]bool{}
	}
	p.standing[snapshot.Name] = snapshot.Existed
	return nil
}

func TestPlanValidationOfATailnetOnlyEnvironment(t *testing.T) {
	t.Parallel()
	tailnet := func() PlanConfiguration {
		plan := routingPlan()
		plan.Domains = nil
		plan.Runtime.InternalPort = 3000
		plan.Runtime.Tailnet = &RouteTailnet{Allow: []string{"alice@example.com", "tag:ci"}}
		return plan
	}
	if err := tailnet().Validate(); err != nil {
		t.Fatalf("a tailnet-only plan was refused: %v", err)
	}
	open := tailnet()
	open.Runtime.Tailnet.Allow = nil
	if err := open.Validate(); err != nil {
		t.Fatalf("a tailnet-only plan admitting the whole tailnet was refused: %v", err)
	}
	for _, fixture := range []struct {
		name  string
		edit  func(*PlanConfiguration)
		field string
	}{
		{"a public domain as well", func(p *PlanConfiguration) { p.Domains = routingPlan().Domains }, "runtime.tailnet"},
		{"a sign-in", func(p *PlanConfiguration) { p.Runtime.ForwardAuth = signInPlan().Runtime.ForwardAuth }, "runtime.tailnet"},
		{"allowed addresses", func(p *PlanConfiguration) { p.Runtime.AllowFrom = []string{"203.0.113.7"} }, "runtime.tailnet"},
		{"response headers", func(p *PlanConfiguration) { p.Runtime.ResponseHeaders = []RouteHeader{{Name: "X-A", Value: "1"}} }, "runtime.tailnet"},
		{"HSTS", func(p *PlanConfiguration) { p.Runtime.HSTS = &RouteHSTS{} }, "runtime.tailnet"},
		{"the host's network", func(p *PlanConfiguration) { p.Runtime.HostNetwork = true }, "runtime.tailnet"},
		{"a fixed host port", func(p *PlanConfiguration) { p.Runtime.HostPort = 8080 }, "runtime.tailnet"},
		{"a published port", func(p *PlanConfiguration) {
			p.Runtime.Ports = []PublishedPort{{HostPort: 2222, ContainerPort: 22}}
		}, "runtime.tailnet"},
		{"every interface", func(p *PlanConfiguration) { p.Runtime.BindAddress = "0.0.0.0" }, "runtime.tailnet"},
		{"no port to forward to", func(p *PlanConfiguration) { p.Runtime.InternalPort = 0 }, "runtime.tailnet"},
		{"a login that is not one", func(p *PlanConfiguration) { p.Runtime.Tailnet.Allow[1] = "alice" }, "runtime.tailnet.allow[1]"},
		{"a tag that is not one", func(p *PlanConfiguration) { p.Runtime.Tailnet.Allow[1] = "tag:" }, "runtime.tailnet.allow[1]"},
		{"a login twice", func(p *PlanConfiguration) { p.Runtime.Tailnet.Allow[1] = "ALICE@example.com" }, "runtime.tailnet.allow[1]"},
		{"too many", func(p *PlanConfiguration) {
			p.Runtime.Tailnet.Allow = nil
			for i := range tailnetgate.MaxAllow + 1 {
				p.Runtime.Tailnet.Allow = append(p.Runtime.Tailnet.Allow, fmt.Sprintf("user%d@example.com", i))
			}
		}, "runtime.tailnet.allow"},
	} {
		plan := tailnet()
		fixture.edit(&plan)
		var validation *ValidationError
		if err := plan.Validate(); !errors.As(err, &validation) || validation.Field != fixture.field {
			t.Errorf("%s: %v", fixture.name, err)
		}
	}
}

// Logins are kept in lower case and tags as written, without writing
// through to the caller's plan; a plan that is not tailnet-only keeps the
// bytes it had.
func TestCanonicalTailnetAllowList(t *testing.T) {
	t.Parallel()
	plan := routingPlan()
	plan.Runtime.Tailnet = &RouteTailnet{Allow: []string{" Alice@Example.com ", "tag:CI"}}
	canonical := canonicalConfiguration(plan)
	if !slices.Equal(canonical.Runtime.Tailnet.Allow, []string{"alice@example.com", "tag:CI"}) {
		t.Fatalf("allow = %q", canonical.Runtime.Tailnet.Allow)
	}
	if plan.Runtime.Tailnet.Allow[0] != " Alice@Example.com " {
		t.Fatal("canonicalising rewrote the caller's plan")
	}
	if raw, _ := json.Marshal(canonicalConfiguration(routingPlan()).Runtime); strings.Contains(string(raw), "tailnet") {
		t.Fatalf("a plan that is not tailnet-only gained the field: %s", raw)
	}
	open := routingPlan()
	open.Runtime.Tailnet = &RouteTailnet{Allow: []string{}}
	if raw, _ := json.Marshal(canonicalConfiguration(open).Runtime.Tailnet); string(raw) != "{}" {
		t.Fatalf("an empty list = %s", raw)
	}
}

// A tailnet-only environment takes a port from the previews' own range and
// table, so the two never share one; a preview's own address is still made
// with its approval and refused here.
func TestAllocateTailnetAddressSharesThePreviewsRange(t *testing.T) {
	t.Parallel()
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	preview := f.addEnvironment(t, "pr-1", EnvironmentPreview)
	production := f.addEnvironment(t, "production", EnvironmentProduction)
	staging := f.addEnvironment(t, "staging", EnvironmentStaging)
	if address, err := f.runs.AllocatePreviewAddress(ctx, preview, nil); err != nil || address.Port != previewTailnetPortMin {
		t.Fatalf("preview = %#v %v", address, err)
	}
	if address, err := f.runs.AllocateTailnetAddress(ctx, production, map[int]int{previewTailnetPortMin + 1: 0}); err != nil || address.Port != previewTailnetPortMin+2 {
		t.Fatalf("production = %#v %v", address, err)
	}
	if address, err := f.runs.AllocateTailnetAddress(ctx, staging, nil); err != nil || address.Port != previewTailnetPortMin+1 {
		t.Fatalf("staging = %#v %v", address, err)
	}
	other := f.addEnvironment(t, "pr-2", EnvironmentPreview)
	if _, err := f.runs.AllocateTailnetAddress(ctx, other, nil); !errors.Is(err, ErrPreviewIsolation) {
		t.Fatalf("a preview through the tailnet-only allocator = %v", err)
	}
}

// Without a list the environment is published as a preview is: its own port
// of the tailnet, made at its first publish, mapped to its front — which asks
// nobody who they are, and keeps the dashboard's session cookie from the
// container — and the front to the container.
func TestActivatePublishesATailnetOnlyEnvironmentOnAPortOfItsOwn(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	run, lease, release := tailnetCandidateWithPlan(t, fixture, 1, "tailnet-v1", 32101, tailnetOnlyPlan())
	owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true}, allowConcurrent: true}
	// The operator serves something of their own on the first port.
	publisher := &tailnetPublisherFake{served: map[int]int{21000: 0}}
	proxy := &gateProxyFake{}
	executor := (&NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher, proxy: proxy,
		tailnetProbe: func(context.Context, string) error { return nil },
	}).WithTailnetGate("http://127.0.0.1:8080")
	output := &recordingStepOutput{}
	result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: output}, nil)
	if result.State != StepPassed {
		t.Fatalf("activate = %#v", result)
	}
	want := proxysvc.TailnetGateRoute{Name: fmt.Sprintf("just-dashboard-env-%d.conf", fixture.envID), Listen: 21001, Upstream: "http://127.0.0.1:32101"}
	if len(proxy.applied) != 1 || proxy.applied[0] != want || proxy.verified != 1 {
		t.Fatalf("fronts = %+v verified %d", proxy.applied, proxy.verified)
	}
	if err := proxysvc.ValidateTailnetGate(proxy.applied[0]); err != nil {
		t.Fatalf("the front activation asked for is not one the proxy writes: %v", err)
	}
	if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint([]tailnetPublish{{21001, 21001, 0}}) {
		t.Fatalf("publishes = %v, want the tailnet port mapped to the front", got)
	}
	address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID)
	if err != nil || address == nil || address.Port != 21001 || !address.Published || address.UpstreamPort != 21001 {
		t.Fatalf("address = %#v %v", address, err)
	}
	if proxy.applyCalls != 0 || liveReleaseID(t, fixture) != release.Release.ID {
		t.Fatalf("routes = %d live = %d", proxy.applyCalls, liveReleaseID(t, fixture))
	}
	evidence := decodeActivationEvidence(t, result)
	if evidence.TailnetGate == nil || !evidence.TailnetGate.Verified || evidence.TailnetGate.Allowed != 0 {
		t.Fatalf("evidence = %+v", evidence.TailnetGate)
	}
	if log := output.joined(); !strings.Contains(log, "keeps the dashboard's session cookie from it") {
		t.Fatalf("transcript = %s", log)
	}
	if allow, err := fixture.runs.LiveTailnetAllow(context.Background(), fixture.envID); err != nil || allow != nil {
		t.Fatalf("the whole tailnet is fenced by the tailnet, not a list: %v %v", allow, err)
	}

	// An executor without the proxy's half of a front publishes the
	// address straight to the container, as before every address had one.
	direct := newReleaseStoreFixture(t)
	run, lease, _ = tailnetCandidateWithPlan(t, direct, 1, "tailnet-v1", 32101, tailnetOnlyPlan())
	publisher = &tailnetPublisherFake{}
	result = (&NormalizedStepExecutor{
		store: direct.runs, variables: direct.variables, runtime: &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true}, allowConcurrent: true},
		tailnet: publisher, proxy: &countingActivationProxy{}, tailnetProbe: func(context.Context, string) error { return nil },
	}).activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}}, nil)
	if result.State != StepPassed || fmt.Sprint(publisher.published()) != fmt.Sprint([]tailnetPublish{{21000, 32101, 0}}) {
		t.Fatalf("without a front = %#v %v", result, publisher.published())
	}
}

// With a list, tailscale serve maps to the gate rather than the container,
// and the gate is written before the mapping moves, asking the dashboard's
// own whois route about this environment.
func TestActivatePutsTheGateInFrontOfAnEnvironmentThatNamesItsVisitors(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	run, lease, release := tailnetCandidateWithPlan(t, fixture, 1, "tailnet-v1", 32101, tailnetOnlyPlan("alice@example.com", "tag:ci"))
	owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true}, allowConcurrent: true}
	publisher := &tailnetPublisherFake{}
	proxy := &gateProxyFake{}
	executor := (&NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher, proxy: proxy,
		tailnetProbe: func(context.Context, string) error { return nil },
	}).WithTailnetGate("http://127.0.0.1:8080/")
	output := &recordingStepOutput{}
	result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: output}, nil)
	if result.State != StepPassed {
		t.Fatalf("activate = %#v", result)
	}
	want := proxysvc.TailnetGateRoute{
		Name: fmt.Sprintf("just-dashboard-env-%d.conf", fixture.envID), Listen: 21000, Upstream: "http://127.0.0.1:32101",
		Check: fmt.Sprintf("http://127.0.0.1:8080/api/v1/tailnet/whois?environment=%d", fixture.envID),
	}
	if len(proxy.applied) != 1 || proxy.applied[0] != want || proxy.verified != 1 {
		t.Fatalf("gates = %+v verified %d", proxy.applied, proxy.verified)
	}
	if err := proxysvc.ValidateTailnetGate(proxy.applied[0]); err != nil {
		t.Fatalf("the gate activation asked for is not one the proxy writes: %v", err)
	}
	if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint([]tailnetPublish{{21000, 21000, 0}}) {
		t.Fatalf("publishes = %v, want the tailnet port mapped to the gate", got)
	}
	evidence := decodeActivationEvidence(t, result)
	if evidence.TailnetGate == nil || !evidence.TailnetGate.Verified || evidence.TailnetGate.Allowed != 2 || evidence.Tailnet == nil {
		t.Fatalf("evidence = %+v", evidence)
	}
	if liveReleaseID(t, fixture) != release.Release.ID {
		t.Fatal("the release did not go live")
	}
	allow, err := fixture.runs.LiveTailnetAllow(context.Background(), fixture.envID)
	if err != nil || !slices.Equal(allow, []string{"alice@example.com", "tag:ci"}) {
		t.Fatalf("live allow = %v %v", allow, err)
	}
	if log := output.joined(); !strings.Contains(log, "admits 2 listed logins and tags") {
		t.Fatalf("transcript = %s", log)
	}
}

// An environment that names its visitors goes live behind its gate or not at
// all: with no proxy that can gate, or a gate that cannot be written, the
// candidate stops, nothing is published, and a gate that was half written
// is put back.
func TestAnEnvironmentThatNamesItsVisitorsNeverGoesLiveWithoutItsGate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		proxy ActivationProxy
		check string
		code  string
	}{
		{"no proxy that gates", &countingActivationProxy{}, "http://127.0.0.1:8080", "tailnet_gate_unavailable"},
		{"no API to ask", &gateProxyFake{}, "", "tailnet_gate_unavailable"},
		{"a gate that cannot be written", &gateProxyFake{applyErr: errors.New("127.0.0.1:21000 is already in use")}, "http://127.0.0.1:8080", "tailnet_gate_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseStoreFixture(t)
			run, lease, _ := tailnetCandidateWithPlan(t, fixture, 1, "tailnet-v1", 32101, tailnetOnlyPlan("alice@example.com"))
			owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true}, allowConcurrent: true}
			publisher := &tailnetPublisherFake{}
			executor := (&NormalizedStepExecutor{
				store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher, proxy: test.proxy,
			}).WithTailnetGate(test.check)
			result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}}, nil)
			if result.ErrorCode != test.code || result.State == StepPassed {
				t.Fatalf("activate = %#v", result)
			}
			if got := publisher.published(); len(got) != 0 {
				t.Fatalf("published without a gate: %v", got)
			}
			owner.mu.Lock()
			running := owner.running["tailnet-v1"]
			owner.mu.Unlock()
			if running || liveReleaseID(t, fixture) != 0 {
				t.Fatalf("running=%v live=%d", running, liveReleaseID(t, fixture))
			}
			if gate, ok := test.proxy.(*gateProxyFake); ok && gate.applyErr != nil {
				if len(gate.restored) != 1 || gate.restored[0].Driver != proxysvc.TailnetGateDriver {
					t.Fatalf("the gate's previous state was not put back: %+v", gate.restored)
				}
			}
		})
	}
}

// A release that is not tailnet-only, replacing one that was, takes the
// environment's address away once it is live, and its front with it; a
// release without the list replaces the gate with a front that asks nobody.
func TestLeavingTheTailnetWithdrawsTheAddressOnceTheNextReleaseIsLive(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true, "tailnet-v2": true, "tailnet-v3": true}, allowConcurrent: true}
	publisher := &tailnetPublisherFake{}
	proxy := &gateProxyFake{}
	executor := (&NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher, proxy: proxy,
		tailnetProbe: func(context.Context, string) error { return nil },
	}).WithTailnetGate("http://127.0.0.1:8080")
	activate := func(revision int, identity string, port int, plan RuntimePlanConfig) {
		t.Helper()
		run, lease, release := tailnetCandidateWithPlan(t, fixture, revision, identity, port, plan)
		if result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}}, nil); result.State != StepPassed {
			t.Fatalf("activate %s = %#v", identity, result)
		}
		fixture.finishActivatedRun(t, run.ID, release.Release.ID, lease.Token)
		fixture.now = fixture.now.Add(time.Minute)
	}
	name := fmt.Sprintf("just-dashboard-env-%d.conf", fixture.envID)

	activate(1, "tailnet-v1", 32101, tailnetOnlyPlan("alice@example.com"))
	activate(2, "tailnet-v2", 32102, tailnetOnlyPlan())
	if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint([]tailnetPublish{{21000, 21000, 0}, {21000, 21000, 21000}}) {
		t.Fatalf("publishes = %v", got)
	}
	if len(proxy.applied) != 2 || proxy.applied[0].Check == "" || proxy.applied[1].Check != "" || proxy.applied[1].Upstream != "http://127.0.0.1:32102" {
		t.Fatalf("fronts = %+v", proxy.applied)
	}
	if len(proxy.removed) != 0 {
		t.Fatalf("the front was taken away from a release that is still on the tailnet: %v", proxy.removed)
	}

	activate(3, "tailnet-v3", 32103, tailnetPreviewPlan())
	if got := publisher.withdrawn(); fmt.Sprint(got) != "[21000]" {
		t.Fatalf("withdraws = %v", got)
	}
	if !slices.Equal(proxy.removed, []string{name}) {
		t.Fatalf("the front of an address that was withdrawn = %v", proxy.removed)
	}
	// The release that left was never published on the tailnet itself.
	if got := publisher.published(); len(got) != 2 {
		t.Fatalf("publishes after leaving = %v", got)
	}
	if address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID); err != nil || address != nil {
		t.Fatalf("address after leaving the tailnet = %#v %v", address, err)
	}
}

// A preview's plan is a copy of the environment's, so a preview of an
// environment that names its visitors names the same ones.
func TestAPreviewInheritsTheTailnetAllowList(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), tailnetOnlyPlan("alice@example.com", "tag:ci"))
	result, err := fixture.base.DB.Exec(
		"INSERT INTO deploy_environments(project_id, name, slug, kind, desired_revision, strategy, expected_downtime, protected, created_at, updated_at) VALUES(?, 'PR 4', 'pr-4', 'preview', 0, 'blue_green', 0, 0, ?, ?)",
		fixture.projectID, fixture.now.Unix(), fixture.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	previewID, _ := result.LastInsertId()
	tx, err := fixture.base.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := createIsolatedPreviewPlansTx(context.Background(), tx, fixture.envID, 1, previewID, 1, fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := fixture.base.DB.QueryRow(`SELECT config_json FROM deploy_runtime_plans WHERE environment_id=? AND revision=1`, previewID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var runtime RuntimePlanConfig
	if err := json.Unmarshal([]byte(raw), &runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.PreviewIsolation || !runtime.Tailnet.IdentityAware() || !slices.Equal(runtime.Tailnet.Allow, []string{"alice@example.com", "tag:ci"}) {
		t.Fatalf("the preview's plan = %+v", runtime.Tailnet)
	}
}

// A trigger's preview domain under a tailnet-only environment is refused at
// activation rather than published on a public name.
func TestATailnetOnlyPlanIsNeverPublishedOnADomain(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	plan := tailnetOnlyPlan("alice@example.com")
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	run, lease := fixture.claimedRun(t, 1)
	release := createRuntimeCandidateWithDomains(t, fixture, *run, lease, plan, "tailnet-v1", nil,
		[]PlannedDomain{{Hostname: "pr-4.preview.example.test", HTTPS: true, Ownership: OwnershipManaged}})
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *run, lease.Token, ReleaseRuntimeInput{
		ReleaseID: release.Release.ID, Kind: "container", RuntimeID: "tailnet-v1", Host: "127.0.0.1", Port: 32101,
		Metadata: json.RawMessage(`{"version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	proxy := &gateProxyFake{}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables,
		runtime: &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true}, allowConcurrent: true}, proxy: proxy}
	result := executor.activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}}, nil)
	if result.ErrorCode != "tailnet_only_has_domains" || proxy.applyCalls != 0 {
		t.Fatalf("activate = %#v routes=%d", result, proxy.applyCalls)
	}
}

// A gated release whose mapping cannot be moved puts the previous gate back
// and points the address at it again — the gate, not the predecessor's
// container, which the tailnet must never reach without the check.
func TestAFailedGatedPublishPointsTheAddressBackAtTheGate(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true, "tailnet-v2": true}, allowConcurrent: true}
	publisher := &tailnetPublisherFake{}
	proxy := &gateProxyFake{}
	executor := (&NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher, proxy: proxy,
		tailnetProbe: func(context.Context, string) error { return nil },
	}).WithTailnetGate("http://127.0.0.1:8080")
	firstRun, firstLease, first := tailnetCandidateWithPlan(t, fixture, 1, "tailnet-v1", 32101, tailnetOnlyPlan("alice@example.com"))
	if result := executor.activate(context.Background(), StepExecution{Run: *firstRun, ClaimToken: firstLease.Token, Output: discardStepOutput{}}, nil); result.State != StepPassed {
		t.Fatalf("first = %#v", result)
	}
	fixture.finishActivatedRun(t, firstRun.ID, first.Release.ID, firstLease.Token)
	fixture.now = fixture.now.Add(time.Minute)

	secondRun, secondLease, _ := tailnetCandidateWithPlan(t, fixture, 2, "tailnet-v2", 32102, tailnetOnlyPlan("alice@example.com"))
	publisher.failNext = true
	result := executor.activate(context.Background(), StepExecution{Run: *secondRun, ClaimToken: secondLease.Token, Output: discardStepOutput{}}, nil)
	if result.State != StepFailed || result.ErrorCode != "tailnet_publish_failed" || !result.Recovered {
		t.Fatalf("second = %#v", result)
	}
	want := []tailnetPublish{{21000, 21000, 0}, {21000, 21000, 21000}, {21000, 21000, 21000}}
	if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("publishes = %v, want %v", got, want)
	}
	if len(proxy.restored) != 1 || proxy.restored[0].Driver != proxysvc.TailnetGateDriver {
		t.Fatalf("the first release's gate was not put back: %+v", proxy.restored)
	}
	if liveReleaseID(t, fixture) != first.Release.ID {
		t.Fatal("the first release is not live any more")
	}
}

// A release whose mapping cannot be moved points the address back at what
// reached the release before it: the front, which the recovery has put back,
// when that release was published through one, and its container when it was
// published before every address had a front — whose recovery takes the
// candidate's front away again rather than leaving one nothing points at.
func TestAFailedFrontedPublishPointsTheAddressBackAtWhatReachedThePredecessor(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		upgraded bool
		want     []tailnetPublish
	}{
		{"a predecessor behind its front", true, []tailnetPublish{{21000, 21000, 0}, {21000, 21000, 21000}, {21000, 21000, 21000}}},
		{"a predecessor published straight to its container", false, []tailnetPublish{{21000, 32101, 0}, {21000, 21000, 32101}, {21000, 32101, 32101}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseStoreFixture(t)
			owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true, "tailnet-v2": true}, allowConcurrent: true}
			publisher := &tailnetPublisherFake{}
			proxy := &gateProxyFake{}
			executor := func(proxy ActivationProxy) *NormalizedStepExecutor {
				return (&NormalizedStepExecutor{
					store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher, proxy: proxy,
					tailnetProbe: func(context.Context, string) error { return nil },
				}).WithTailnetGate("http://127.0.0.1:8080")
			}
			first := ActivationProxy(&countingActivationProxy{})
			if test.upgraded {
				first = proxy
			}
			firstRun, firstLease, firstRelease := tailnetCandidateWithPlan(t, fixture, 1, "tailnet-v1", 32101, tailnetPreviewPlan())
			insertPreviewAddress(t, fixture, 21000, 0, false)
			if result := executor(first).activate(context.Background(), StepExecution{Run: *firstRun, ClaimToken: firstLease.Token, Output: discardStepOutput{}}, nil); result.State != StepPassed {
				t.Fatalf("first = %#v", result)
			}
			fixture.finishActivatedRun(t, firstRun.ID, firstRelease.Release.ID, firstLease.Token)
			fixture.now = fixture.now.Add(time.Minute)

			secondRun, secondLease, _ := tailnetCandidateWithPlan(t, fixture, 2, "tailnet-v2", 32102, tailnetPreviewPlan())
			publisher.failNext = true
			result := executor(proxy).activate(context.Background(), StepExecution{Run: *secondRun, ClaimToken: secondLease.Token, Output: discardStepOutput{}}, nil)
			if result.State != StepFailed || result.ErrorCode != "tailnet_publish_failed" || !result.Recovered {
				t.Fatalf("second = %#v", result)
			}
			if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint(test.want) {
				t.Fatalf("publishes = %v, want %v", got, test.want)
			}
			if len(proxy.restored) != 1 || proxy.restored[0].Existed != test.upgraded {
				t.Fatalf("the front before the candidate was not put back as it stood: %+v", proxy.restored)
			}
			address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID)
			if err != nil || address == nil || !address.Published || address.UpstreamPort != test.want[2].Upstream {
				t.Fatalf("address = %#v %v", address, err)
			}
			if liveReleaseID(t, fixture) != firstRelease.Release.ID {
				t.Fatal("the first release is not live any more")
			}
		})
	}
}

type gateHostFake struct{ PlanningProxy }

func (gateHostFake) TailnetGateHost(context.Context) (string, bool, error) { return "caddy", true, nil }

// The overview reads the address and who may open it from the live
// release's plan and the address record, and which proxy gates it.
func TestOperationsSummaryNamesTheTailnetAddressAndWhoMayOpenIt(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	ctx := context.Background()
	gated := runtimeReleaseSnapshot{Plan: tailnetOnlyPlan("alice@example.com", "tag:ci")}
	if got := fixture.runs.tailnetSummary(ctx, OperationsOwners{}, runtimeReleaseSnapshot{Plan: tailnetPreviewPlan()}, fixture.envID); got != nil {
		t.Fatalf("an environment that is not on the tailnet = %+v", got)
	}
	insertPreviewAddress(t, fixture, 21004, 21004, true)
	got := fixture.runs.tailnetSummary(ctx, OperationsOwners{Proxy: gateHostFake{}}, gated, fixture.envID)
	if got == nil || got.URL != "https://node.tailnet.ts.net:21004" || got.Port != 21004 || !got.Published ||
		!slices.Equal(got.Allow, []string{"alice@example.com", "tag:ci"}) || got.Gate != "caddy" || !got.GateRunning {
		t.Fatalf("summary = %+v", got)
	}
	open := fixture.runs.tailnetSummary(ctx, OperationsOwners{Proxy: gateHostFake{}}, runtimeReleaseSnapshot{Plan: tailnetOnlyPlan()}, fixture.envID)
	if open == nil || len(open.Allow) != 0 || open.Gate != "" {
		t.Fatalf("the whole tailnet = %+v", open)
	}
}

// Preflight says the address the application will have and over which
// scheme, who may open it and through which proxy — and refuses a host on no
// tailnet, which no deployment could reach.
func TestPreflightExplainsTheTailnetAddress(t *testing.T) {
	t.Parallel()
	plan := routingPlan()
	plan.Domains = nil
	plan.Runtime.Tailnet = &RouteTailnet{Allow: []string{"alice@example.com", "tag:ci"}}
	byCode := func(findings []PreflightFinding) map[string]PreflightFinding {
		out := map[string]PreflightFinding{}
		for _, item := range findings {
			out[item.Code] = item
		}
		return out
	}
	if got := tailnetFindings(routingPlan(), &TailnetObservation{Usable: true}); len(got) != 0 {
		t.Fatalf("a plan that is not tailnet-only = %+v", got)
	}
	off := byCode(tailnetFindings(plan, &TailnetObservation{Detail: "This machine is not connected to a tailnet (Stopped)."}))
	if item := off["tailnet_unavailable"]; item.Severity != PreflightBlocked || !strings.Contains(item.Measured, "Stopped") || item.FieldID != "runtime.tailnet" {
		t.Fatalf("no tailnet = %+v", off)
	}
	if item := byCode(tailnetFindings(plan, nil))["tailnet_unobserved"]; item.Severity != PreflightUnavailable {
		t.Fatalf("an unread tailnet = %+v", item)
	}
	first := byCode(tailnetFindings(plan, &TailnetObservation{Usable: true, Hostname: "box.tail1234.ts.net", HTTPS: true, NextPort: 21004, Gate: "caddy"}))
	if item := first["tailnet_address"]; item.Severity != PreflightPass || item.Measured != "https://box.tail1234.ts.net:21004" ||
		!strings.Contains(item.Means, "no public domain") || !strings.Contains(item.Means, "lowest free") {
		t.Fatalf("a first deployment's address = %+v", item)
	}
	if item := first["tailnet_identity"]; item.Measured != "alice@example.com, tag:ci" || !strings.Contains(item.Means, "which the deployment starts") ||
		!strings.Contains(item.Means, "503") {
		t.Fatalf("who opens it = %+v", item)
	}
	plain := byCode(tailnetFindings(plan, &TailnetObservation{Usable: true, Hostname: "box.tail1234.ts.net", URL: "http://box.tail1234.ts.net:21000", Port: 21000, Gate: "nginx"}))
	if item := plain["tailnet_address"]; item.Measured != "http://box.tail1234.ts.net:21000" || !strings.Contains(item.Means, "plain HTTP") || !strings.Contains(item.Means, "keeps this address") {
		t.Fatalf("an address kept over plain HTTP = %+v", item)
	}
	if item := plain["tailnet_identity"]; !strings.Contains(item.Means, "this host's nginx") {
		t.Fatalf("the nginx gate = %+v", item)
	}
	plan.Runtime.Tailnet.Allow = nil
	if item := byCode(tailnetFindings(plan, &TailnetObservation{Usable: true, Hostname: "box.tail1234.ts.net", HTTPS: true}))["tailnet_identity"]; item.Title != "Everyone on the tailnet can open it" {
		t.Fatalf("the whole tailnet = %+v", item)
	}
	request := preflightObservationRequest(&Draft{Data: DraftData{Source: &DraftSourceConfig{Kind: SourceImage}, Detection: &DetectionResult{}}}, plan)
	if !request.Tailnet {
		t.Fatal("preflight does not ask about the tailnet for a tailnet-only plan")
	}
}

// An address published before every address had a front is moved behind one
// when the dashboard starts: the front is written for the container the
// mapping reaches, the mapping moves to it and is recorded. One whose front
// cannot be written, or whose mapping cannot be moved, is left as it was,
// with the front taken away again; and an address already behind its front
// is not touched.
func TestFrontTailnetAddressesMovesADirectMappingBehindItsFront(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		applyErr error
		failNext bool
	}{
		{name: "moved"},
		{name: "a front that cannot be written", applyErr: errors.New("127.0.0.1:21000 is already in use")},
		{name: "a mapping that cannot be moved", failNext: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseStoreFixture(t)
			owner := &orderingRuntimeOwner{running: map[string]bool{"tailnet-v1": true}, allowConcurrent: true}
			publisher := &tailnetPublisherFake{}
			run, lease, release := tailnetCandidateWithPlan(t, fixture, 1, "tailnet-v1", 32101, tailnetPreviewPlan())
			insertPreviewAddress(t, fixture, 21000, 0, false)
			// Published the way an earlier release published it: straight to
			// the container.
			if result := (&NormalizedStepExecutor{
				store: fixture.runs, variables: fixture.variables, runtime: owner, tailnet: publisher, proxy: &countingActivationProxy{},
				tailnetProbe: func(context.Context, string) error { return nil },
			}).activate(context.Background(), StepExecution{Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{}}, nil); result.State != StepPassed {
				t.Fatalf("activate = %#v", result)
			}
			fixture.finishActivatedRun(t, run.ID, release.Release.ID, lease.Token)

			proxy := &gateProxyFake{applyErr: test.applyErr}
			publisher.failNext = test.failNext
			executor := &NormalizedStepExecutor{store: fixture.runs, tailnet: publisher, proxy: proxy}
			fronting, err := executor.FrontTailnetAddresses(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			address, err := fixture.runs.PreviewAddressFor(context.Background(), fixture.envID)
			if err != nil || address == nil || !address.Published {
				t.Fatalf("address = %#v %v", address, err)
			}
			want := proxysvc.TailnetGateRoute{Name: fmt.Sprintf("just-dashboard-env-%d.conf", fixture.envID), Listen: 21000, Upstream: "http://127.0.0.1:32101"}
			if len(proxy.applied) != 1 || proxy.applied[0] != want {
				t.Fatalf("fronts = %+v", proxy.applied)
			}
			if test.name == "moved" {
				if !slices.Equal(fronting.Fronted, []int64{fixture.envID}) || len(fronting.Failed) != 0 || address.UpstreamPort != 21000 {
					t.Fatalf("fronting = %+v address = %#v", fronting, address)
				}
				if got := publisher.published(); fmt.Sprint(got) != fmt.Sprint([]tailnetPublish{{21000, 32101, 0}, {21000, 21000, 32101}}) {
					t.Fatalf("publishes = %v", got)
				}
				// Behind its front now, so a second start leaves it be.
				again, err := executor.FrontTailnetAddresses(context.Background())
				if err != nil || len(again.Fronted) != 0 || len(again.Failed) != 0 || len(proxy.applied) != 1 {
					t.Fatalf("second pass = %+v %v, fronts %d", again, err, len(proxy.applied))
				}
				return
			}
			if len(fronting.Fronted) != 0 || fronting.Failed[fixture.envID] == "" || address.UpstreamPort != 32101 {
				t.Fatalf("fronting = %+v address = %#v", fronting, address)
			}
			if test.failNext && (len(proxy.restored) != 1 || proxy.restored[0].Existed) {
				t.Fatalf("the front was not taken away again: %+v", proxy.restored)
			}
		})
	}
}
