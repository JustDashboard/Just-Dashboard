package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func pagesPlan() PlanConfiguration {
	plan := routingPlan()
	plan.Runtime.Maintenance = &RouteMaintenance{RetryAfter: 300, BypassFrom: []string{"203.0.113.7", "10.0.0.0/8"}}
	plan.Runtime.ErrorPages = []int{502, 404}
	plan.Runtime.InterceptErrors = true
	return plan
}

// The plan refuses maintenance and error pages either proxy could not write,
// by the rules a Proxy site's are held to.
func TestPlanValidationOfRoutePages(t *testing.T) {
	t.Parallel()
	if err := pagesPlan().Validate(); err != nil {
		t.Fatalf("maintenance and error pages were refused: %v", err)
	}
	for _, fixture := range []struct {
		name  string
		edit  func(*PlanConfiguration)
		field string
	}{
		{"a negative wait", func(p *PlanConfiguration) { p.Runtime.Maintenance.RetryAfter = -1 }, "runtime.maintenance.retryAfter"},
		{"a wait over a day", func(p *PlanConfiguration) { p.Runtime.Maintenance.RetryAfter = 86401 }, "runtime.maintenance.retryAfter"},
		{"an address that is not one", func(p *PlanConfiguration) { p.Runtime.Maintenance.BypassFrom[1] = "office" }, "runtime.maintenance.bypassFrom[1]"},
		{"an address twice", func(p *PlanConfiguration) {
			p.Runtime.Maintenance.BypassFrom = []string{"10.0.0.0/8", "10.0.0.0/8"}
		}, "runtime.maintenance.bypassFrom[1]"},
		{"a page for a code it cannot have", func(p *PlanConfiguration) { p.Runtime.ErrorPages = []int{500} }, "runtime.errorPages[0]"},
		{"a page twice", func(p *PlanConfiguration) { p.Runtime.ErrorPages = []int{502, 502} }, "runtime.errorPages[1]"},
		{"replacing the application's pages with none", func(p *PlanConfiguration) { p.Runtime.ErrorPages = nil }, "runtime.interceptErrors"},
		{"replacing them beside a sign-in", func(p *PlanConfiguration) { p.Runtime.ForwardAuth = signInPlan().Runtime.ForwardAuth }, "runtime.interceptErrors"},
	} {
		plan := pagesPlan()
		fixture.edit(&plan)
		var validation *ValidationError
		if err := plan.Validate(); !errors.As(err, &validation) || validation.Field != fixture.field {
			t.Fatalf("%s: %v", fixture.name, err)
		}
	}
}

// The plan keeps the addresses as the proxy writes them and the pages in
// the proxy's order, without writing through to the plan it was given, and
// a plan that never chose any keeps the bytes, and so the digest, it had.
func TestCanonicalRoutePagesKeepTheDigest(t *testing.T) {
	t.Parallel()
	before := `{"image":"nginx:alpine","bindAddress":"127.0.0.1","strategy":"stop_first"}`
	for name, edit := range map[string]func(*PlanConfiguration){
		"none":        func(*PlanConfiguration) {},
		"empty":       func(p *PlanConfiguration) { p.Runtime.Maintenance, p.Runtime.ErrorPages = &RouteMaintenance{}, []int{} },
		"nil entries": func(p *PlanConfiguration) { p.Runtime.Maintenance = &RouteMaintenance{BypassFrom: []string{}} },
	} {
		plan := routingPlan()
		edit(&plan)
		if raw := string(mustJSON(canonicalConfiguration(plan).Runtime)); raw != before {
			t.Fatalf("%s: runtime = %s, want the bytes it had before maintenance existed: %s", name, raw, before)
		}
	}

	plan := pagesPlan()
	plan.Runtime.Maintenance.BypassFrom = []string{" 2001:DB8::1 ", " 10.1.0.0/16"}
	plan.Runtime.ErrorPages = []int{504, 404}
	canonical := canonicalConfiguration(plan)
	if got := strings.Join(canonical.Runtime.Maintenance.BypassFrom, ","); got != "2001:db8::1,10.1.0.0/16" {
		t.Fatalf("let past = %s", got)
	}
	if got := mustJSON(canonical.Runtime.ErrorPages); string(got) != "[404,504]" {
		t.Fatalf("error pages = %s", got)
	}
	if plan.Runtime.Maintenance.BypassFrom[0] != " 2001:DB8::1 " || plan.Runtime.ErrorPages[0] != 504 {
		t.Fatal("canonicalising rewrote the caller's plan")
	}
	raw := string(mustJSON(canonical.Runtime))
	if !strings.Contains(raw, `"maintenance":{"retryAfter":300,"bypassFrom":["2001:db8::1","10.1.0.0/16"]},"errorPages":[404,504],"interceptErrors":true`) {
		t.Fatalf("runtime = %s", raw)
	}
}

// Activation hands the proxy what the release's plan says while the route is
// in maintenance and which pages it answers with; whether it is in
// maintenance is the switch's, not the release's.
func TestPublicRouteCarriesTheRoutePages(t *testing.T) {
	t.Parallel()
	plan := pagesPlan()
	route := publicRoute(7, runtimeReleaseSnapshot{Plan: plan.Runtime, Domains: plan.Domains}, ReleaseRuntime{Kind: "container", Host: "127.0.0.1", Port: 3000})
	if route.Maintenance.RetryAfter != 300 || strings.Join(route.Maintenance.BypassFrom, ",") != "203.0.113.7,10.0.0.0/8" ||
		len(route.ErrorPages) != 2 || !route.InterceptErrors {
		t.Fatalf("route = %+v", route)
	}
	bare := routingPlan()
	route = publicRoute(7, runtimeReleaseSnapshot{Plan: bare.Runtime, Domains: bare.Domains}, ReleaseRuntime{Kind: "container", Host: "127.0.0.1", Port: 3000})
	if route.Maintenance.RetryAfter != 0 || route.Maintenance.BypassFrom != nil || route.ErrorPages != nil || route.InterceptErrors {
		t.Fatalf("a plan without pages = %+v", route)
	}
}

// The configuration store keeps them across a save and a read, which is the
// path the Domains page writes through.
func TestConfigurationStoreKeepsRoutePages(t *testing.T) {
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	current, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := current.Runtime
	pages := pagesPlan().Runtime
	runtime.Maintenance, runtime.ErrorPages, runtime.InterceptErrors = pages.Maintenance, pages.ErrorPages, pages.InterceptErrors
	request := ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: runtime,
		Dependencies: current.Dependencies, Checks: current.Checks, Domains: routingPlan().Domains,
	}
	if _, err := fixture.variables.SaveEnvironmentConfiguration(ctx, fixture.projectID, fixture.envID, request); err != nil {
		t.Fatal(err)
	}
	read, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Runtime.Maintenance == nil || read.Runtime.Maintenance.RetryAfter != 300 ||
		strings.Join(read.Runtime.Maintenance.BypassFrom, ",") != "203.0.113.7,10.0.0.0/8" ||
		string(mustJSON(read.Runtime.ErrorPages)) != "[404,502]" || !read.Runtime.InterceptErrors {
		t.Fatalf("runtime read back = %+v", read.Runtime)
	}
	name, err := fixture.variables.EnvironmentRouteName(ctx, fixture.projectID, fixture.envID)
	if err != nil || name != deploymentRouteName(fixture.envID) {
		t.Fatalf("route name = %q, %v", name, err)
	}
	if _, err := fixture.variables.EnvironmentRouteName(ctx, fixture.projectID+1, fixture.envID); !errors.Is(err, ErrEnvironmentNotFound) {
		t.Fatalf("another project's environment = %v", err)
	}
}

type maintenanceActivationProxy struct {
	countingActivationProxy
	on bool
}

func (p *maintenanceActivationProxy) DeploymentMaintenance(_ context.Context, _ string) (proxysvc.MaintenanceSwitch, error) {
	return proxysvc.MaintenanceSwitch{Driver: "nginx", Route: true, Ready: true, On: p.on}, nil
}

// A release activated while the route is in maintenance is not failed by its
// public-route checks, which would read the maintenance page; the candidate
// has passed its own checks, which dial it directly. With maintenance off
// the same check runs, and fails the release it cannot reach.
func TestActivationInMaintenanceSkipsThePublicRouteChecks(t *testing.T) {
	for _, on := range []bool{true, false} {
		fixture := newReleaseStoreFixture(t)
		plan := RuntimePlanConfig{
			InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
		}
		fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
		run, lease := fixture.claimedRun(t, 1)
		public := PlannedCheck{
			Name: "public", Kind: string(CheckPublicRoute), Phase: "smoke", Required: true,
			Config: json.RawMessage(`{"url":"http://127.0.0.1:1/","attempts":1,"timeoutSeconds":1}`),
		}
		release := createRuntimeCandidateWithDomains(t, fixture, *run, lease, plan, "maintenance-candidate",
			[]PlannedCheck{public}, []PlannedDomain{{Hostname: "app.example.test", Ownership: OwnershipManaged}})
		if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *run, lease.Token, ReleaseRuntimeInput{
			ReleaseID: release.Release.ID, Kind: "container", RuntimeID: "maintenance-candidate",
			Host: "127.0.0.1", Port: 32123, Metadata: json.RawMessage(`{"version":1}`),
		}); err != nil {
			t.Fatal(err)
		}
		owner := &orderingRuntimeOwner{running: map[string]bool{"maintenance-candidate": true}, allowConcurrent: true}
		proxy := &maintenanceActivationProxy{on: on}
		executor := &NormalizedStepExecutor{
			store: fixture.runs, variables: fixture.variables, runtime: owner, proxy: proxy,
			checks: NewCheckRunner(&checkBackendFake{}),
		}
		result := executor.activate(context.Background(), StepExecution{
			Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{},
		}, mustExecutionPlan(t, fixture, *run))
		var evidence activationStepEvidence
		_ = json.Unmarshal(result.Evidence, &evidence)
		if on {
			if result.State != StepPassed || len(evidence.PublicChecks) != 1 ||
				evidence.PublicChecks[0].Outcome != HealthDisabled || evidence.PublicChecks[0].Attempts[0].Code != "maintenance" {
				t.Fatalf("activation in maintenance = %#v, evidence %+v", result, evidence.PublicChecks)
			}
		} else if result.State != StepFailed || result.ErrorCode != "public_route_failed" {
			t.Fatalf("activation out of maintenance = %#v", result)
		}
	}
}
