package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func signInPlan() PlanConfiguration {
	plan := routingPlan()
	plan.Runtime.ForwardAuth = &RouteForwardAuth{
		Provider: "authelia", Verify: "http://127.0.0.1:9091/api/authz/auth-request", SignIn: "https://auth.example.test/",
	}
	return plan
}

// The plan refuses a sign-in either proxy could not write, and, on a route
// it gates, a password beside it or a name served over plain HTTP.
func TestPlanValidationOfRouteSignIn(t *testing.T) {
	t.Parallel()
	if err := signInPlan().Validate(); err != nil {
		t.Fatalf("an Authelia sign-in was refused: %v", err)
	}
	protection := &DomainProtection{Username: "team", Hash: "$2a$10$" + strings.Repeat("a", 53)}
	for _, fixture := range []struct {
		name  string
		edit  func(*PlanConfiguration)
		field string
	}{
		{"an unknown provider", func(p *PlanConfiguration) { p.Runtime.ForwardAuth.Provider = "keycloak" }, "runtime.forwardAuth.provider"},
		{"a check address that is not one", func(p *PlanConfiguration) { p.Runtime.ForwardAuth.Verify = "authelia:9091" }, "runtime.forwardAuth.verify"},
		{"a check address with a query", func(p *PlanConfiguration) { p.Runtime.ForwardAuth.Verify += "?x=1" }, "runtime.forwardAuth.verify"},
		{"a sign-in page given rd=", func(p *PlanConfiguration) { p.Runtime.ForwardAuth.SignIn += "?rd=back" }, "runtime.forwardAuth.signIn"},
		{"a sign-in page with a quote", func(p *PlanConfiguration) { p.Runtime.ForwardAuth.SignIn = `https://auth.example.test/"` }, "runtime.forwardAuth.signIn"},
		{"a password as well", func(p *PlanConfiguration) { p.Domains[0].Protection = protection }, "runtime.forwardAuth"},
		{"a name served over HTTP", func(p *PlanConfiguration) { p.Domains[0].HTTPS = false }, "runtime.forwardAuth"},
	} {
		plan := signInPlan()
		fixture.edit(&plan)
		var validation *ValidationError
		if err := plan.Validate(); !errors.As(err, &validation) || validation.Field != fixture.field {
			t.Fatalf("%s: %v", fixture.name, err)
		}
	}

	// A name that only redirects serves nothing to protect.
	plan := signInPlan()
	plan.Domains[1].HTTPS = false
	if err := plan.Validate(); err != nil {
		t.Fatalf("a plain-HTTP redirecting name was refused: %v", err)
	}

	// Signing in on previews alone leaves this route as it is, password and
	// plain HTTP included — until the plan is a preview's.
	plan = signInPlan()
	plan.Runtime.ForwardAuth.PreviewsOnly = true
	plan.Domains[0].Protection, plan.Domains[0].HTTPS = protection, false
	if err := plan.Validate(); err != nil {
		t.Fatalf("a production route was held to its previews' sign-in: %v", err)
	}
	plan.Runtime.PreviewIsolation = true
	var validation *ValidationError
	if err := plan.Validate(); !errors.As(err, &validation) || validation.Field != "runtime.forwardAuth" {
		t.Fatalf("a preview's route kept a password beside its sign-in: %v", err)
	}
}

func TestRouteSignInGate(t *testing.T) {
	t.Parallel()
	gate := &RouteForwardAuth{Provider: "authelia", Verify: "http://127.0.0.1:9091/check", SignIn: "https://auth.example.test/"}
	production, preview := RuntimePlanConfig{ForwardAuth: gate}, RuntimePlanConfig{ForwardAuth: gate, PreviewIsolation: true}
	if production.SignInGate() != gate || preview.SignInGate() != gate {
		t.Fatal("a sign-in on every environment is missing from one")
	}
	previewsOnly := *gate
	previewsOnly.PreviewsOnly = true
	production.ForwardAuth, preview.ForwardAuth = &previewsOnly, &previewsOnly
	if production.SignInGate() != nil || preview.SignInGate() != &previewsOnly {
		t.Fatal("a sign-in for previews only gated production, or no preview")
	}
	if (RuntimePlanConfig{}).SignInGate() != nil {
		t.Fatal("a plan without a sign-in has one")
	}
}

// The plan keeps the sign-in trimmed, without writing through to the plan
// it was given, and a plan without one keeps the bytes it had.
func TestCanonicalRouteSignIn(t *testing.T) {
	t.Parallel()
	plan := signInPlan()
	plan.Runtime.ForwardAuth.Verify = " http://127.0.0.1:9091/check "
	canonical := canonicalConfiguration(plan)
	if canonical.Runtime.ForwardAuth.Verify != "http://127.0.0.1:9091/check" {
		t.Fatalf("verify = %q", canonical.Runtime.ForwardAuth.Verify)
	}
	if plan.Runtime.ForwardAuth.Verify != " http://127.0.0.1:9091/check " {
		t.Fatal("canonicalising rewrote the caller's plan")
	}
	if raw, _ := json.Marshal(canonicalConfiguration(routingPlan()).Runtime); strings.Contains(string(raw), "forwardAuth") {
		t.Fatalf("a plan without a sign-in gained the field: %s", raw)
	}
}

// Activation hands the proxy the sign-in its release's plan carries, on a
// preview's route alone when it is for previews.
func TestPublicRouteCarriesTheSignIn(t *testing.T) {
	t.Parallel()
	plan := signInPlan()
	snapshot := runtimeReleaseSnapshot{Plan: plan.Runtime, Domains: plan.Domains}
	route := publicRoute(7, snapshot, ReleaseRuntime{Kind: "container", Host: "127.0.0.1", Port: 3000})
	want := proxysvc.SiteForwardAuth{Provider: "authelia", Verify: "http://127.0.0.1:9091/api/authz/auth-request", SignIn: "https://auth.example.test/"}
	if route.ForwardAuth == nil || *route.ForwardAuth != want {
		t.Fatalf("route sign-in = %+v", route.ForwardAuth)
	}
	snapshot.Plan.ForwardAuth.PreviewsOnly = true
	if route := publicRoute(7, snapshot, ReleaseRuntime{Kind: "container", Host: "127.0.0.1", Port: 3000}); route.ForwardAuth != nil {
		t.Fatalf("production signed in for its previews: %+v", route.ForwardAuth)
	}
	snapshot.Plan.PreviewIsolation = true
	if route := publicRoute(7, snapshot, ReleaseRuntime{Kind: "container", Host: "127.0.0.1", Port: 3000}); route.ForwardAuth == nil || *route.ForwardAuth != want {
		t.Fatalf("preview route sign-in = %+v", route.ForwardAuth)
	}
}

// The configuration store keeps the sign-in across a save and a read, which
// is the path the Domains page writes through.
func TestConfigurationStoreKeepsRouteSignIn(t *testing.T) {
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	current, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := current.Runtime
	runtime.ForwardAuth = signInPlan().Runtime.ForwardAuth
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
	if read.Runtime.ForwardAuth == nil || *read.Runtime.ForwardAuth != *signInPlan().Runtime.ForwardAuth {
		t.Fatalf("sign-in read back = %+v", read.Runtime.ForwardAuth)
	}
}
