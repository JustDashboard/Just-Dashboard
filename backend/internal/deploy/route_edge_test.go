package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func edgePlan() PlanConfiguration {
	plan := routingPlan()
	plan.Runtime.RateLimit = &RouteRateLimit{Requests: 20, Per: "second", Burst: 40}
	plan.Runtime.EdgeRules = []RouteEdgeRule{
		{Name: "admin", Match: proxysvc.EdgeMatch{PathPrefix: "/admin"}, Action: proxysvc.EdgeAction{Kind: "block", Status: 403}},
		{Name: "bots", Match: proxysvc.EdgeMatch{UserAgent: "curl", Methods: []string{"GET", "POST"}}, Action: proxysvc.EdgeAction{Kind: "status", Status: 429}},
	}
	plan.Runtime.CrowdSec = true
	return plan
}

// The plan holds a route's edge rules and rate limit to the rules the proxy
// renders, and names the field the Domains page shows the refusal under.
func TestPlanValidationOfEdgeRulesAndRateLimit(t *testing.T) {
	t.Parallel()
	if err := edgePlan().Validate(); err != nil {
		t.Fatalf("a plan with edge rules and a rate limit was refused: %v", err)
	}
	for _, fixture := range []struct {
		name  string
		edit  func(*PlanConfiguration)
		field string
	}{
		{"a rule matching nothing", func(p *PlanConfiguration) { p.Runtime.EdgeRules[0].Match = proxysvc.EdgeMatch{} }, "runtime.edgeRules"},
		{"two rules of one name", func(p *PlanConfiguration) { p.Runtime.EdgeRules[1].Name = "admin" }, "runtime.edgeRules"},
		{"a pattern PCRE and RE2 read apart", func(p *PlanConfiguration) {
			p.Runtime.EdgeRules[0].Match = proxysvc.EdgeMatch{PathRegex: "^/(?!public)"}
		}, "runtime.edgeRules"},
		{"a redirect with nowhere to go", func(p *PlanConfiguration) {
			p.Runtime.EdgeRules[0].Action = proxysvc.EdgeAction{Kind: "redirect", Code: 302}
		}, "runtime.edgeRules"},
		{"a rate per hour", func(p *PlanConfiguration) { p.Runtime.RateLimit.Per = "hour" }, "runtime.rateLimit"},
		{"a rate of nothing", func(p *PlanConfiguration) { p.Runtime.RateLimit.Requests = 0 }, "runtime.rateLimit"},
	} {
		plan := edgePlan()
		fixture.edit(&plan)
		err := plan.Validate()
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != fixture.field {
			t.Fatalf("%s: %v", fixture.name, err)
		}
	}
}

// What the Domains page sends is kept in the one spelling the proxy renders,
// so a save of the same rules written another way is the same plan.
func TestCanonicalEdgeRulesAndRateLimit(t *testing.T) {
	t.Parallel()
	plan := edgePlan()
	plan.Runtime.EdgeRules = []RouteEdgeRule{{
		Name:   " office ",
		Match:  proxysvc.EdgeMatch{Methods: []string{"post", " get ", "GET"}, ClientFrom: []string{" 2001:DB8::1 ", "10.0.0.0/8"}},
		Action: proxysvc.EdgeAction{Kind: " Block "},
	}}
	plan.Runtime.RateLimit = &RouteRateLimit{Requests: 5, Per: " Minute "}
	canonical := canonicalConfiguration(plan)
	rule := canonical.Runtime.EdgeRules[0]
	if rule.Name != "office" || strings.Join(rule.Match.Methods, ",") != "GET,POST" ||
		strings.Join(rule.Match.ClientFrom, ",") != "2001:db8::1,10.0.0.0/8" || rule.Action != (proxysvc.EdgeAction{Kind: "block", Status: 403}) {
		t.Fatalf("canonical rule = %+v", rule)
	}
	if canonical.Runtime.RateLimit.Per != "minute" || plan.Runtime.RateLimit.Per != " Minute " {
		t.Fatalf("rate limit = %+v (caller's %+v)", canonical.Runtime.RateLimit, plan.Runtime.RateLimit)
	}
	if err := canonical.Validate(); err != nil {
		t.Fatalf("the canonical plan was refused: %v", err)
	}
}

// A tailnet-only environment has no public route, so it has no edge rules,
// rate limit or CrowdSec list to apply them on.
func TestATailnetEnvironmentRefusesTheEdgeRules(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(*PlanConfiguration){
		"edge rules":   func(p *PlanConfiguration) { p.Runtime.EdgeRules = edgePlan().Runtime.EdgeRules },
		"a rate limit": func(p *PlanConfiguration) { p.Runtime.RateLimit = edgePlan().Runtime.RateLimit },
		"crowdsec":     func(p *PlanConfiguration) { p.Runtime.CrowdSec = true },
	} {
		plan := routingPlan()
		plan.Domains = nil
		plan.Runtime.Tailnet = &RouteTailnet{}
		edit(&plan)
		err := plan.Validate()
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != "runtime.tailnet" {
			t.Fatalf("%s beside a tailnet-only environment: %v", name, err)
		}
	}
}

// The route a release asks the proxy for carries the plan's rules, and a
// release that changes them says so in its comparison.
func TestTheReleaseRouteCarriesTheEdgeRules(t *testing.T) {
	t.Parallel()
	plan := edgePlan()
	route := publicRoute(7, runtimeReleaseSnapshot{Version: 1, Plan: plan.Runtime, Domains: plan.Domains}, "127.0.0.1", 30080)
	if len(route.EdgeRules) != 2 || route.EdgeRules[1].Name != "bots" || route.RateLimit == nil || route.RateLimit.Requests != 20 || !route.CrowdSec {
		t.Fatalf("route = %+v", route)
	}
	if got := edgeRulesLabel(plan.Runtime.EdgeRules); got != "admin (block), bots (status)" {
		t.Fatalf("edge rules label = %q", got)
	}
	if got := rateLimitLabel(plan.Runtime.RateLimit); got != "20 per second, burst 40" {
		t.Fatalf("rate limit label = %q", got)
	}
	if edgeRulesLabel(nil) != "none" || rateLimitLabel(nil) != "off" {
		t.Fatal("an absent rule reads as something")
	}
}

// The configuration store keeps the rules across a save and a read, the
// path the Domains page writes through.
func TestConfigurationStoreKeepsEdgeRules(t *testing.T) {
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("b", 40))
	current, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := current.Runtime
	runtime.EdgeRules = edgePlan().Runtime.EdgeRules
	runtime.RateLimit = &RouteRateLimit{Requests: 3, Per: "Second", Burst: 2}
	runtime.CrowdSec = true
	if _, err := fixture.variables.SaveEnvironmentConfiguration(ctx, fixture.projectID, fixture.envID, ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: runtime,
		Dependencies: current.Dependencies, Checks: current.Checks, Domains: routingPlan().Domains,
	}); err != nil {
		t.Fatal(err)
	}
	read, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Runtime.EdgeRules) != 2 || read.Runtime.RateLimit == nil || read.Runtime.RateLimit.Per != "second" || !read.Runtime.CrowdSec {
		t.Fatalf("runtime read back = %+v", read.Runtime)
	}
}
