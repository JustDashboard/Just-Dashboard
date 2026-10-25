package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

func routingPlan() PlanConfiguration {
	return PlanConfiguration{
		Build:     BuildPlanConfig{Method: BuildImage, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
		Runtime:   RuntimePlanConfig{Image: "nginx:alpine", Strategy: StrategyStopFirst, BindAddress: "127.0.0.1"},
		Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{},
		Domains: []PlannedDomain{
			{Hostname: "example.test", HTTPS: true, Ownership: OwnershipManaged},
			{Hostname: "www.example.test", HTTPS: true, Ownership: OwnershipManaged, RedirectTo: "example.test"},
		},
	}
}

// A redirect lands on a name that serves the application, so a visitor is
// never sent round a chain or off to a host this deployment does not answer.
func TestPlanValidationOfDomainRedirectsAndRouteRules(t *testing.T) {
	t.Parallel()
	if err := routingPlan().Validate(); err != nil {
		t.Fatalf("a www. redirect to the bare domain was refused: %v", err)
	}
	for _, fixture := range []struct {
		name  string
		edit  func(*PlanConfiguration)
		field string
	}{
		{"to itself", func(p *PlanConfiguration) { p.Domains[1].RedirectTo = "www.example.test" }, "domains[1].redirectTo"},
		{"to a name it does not have", func(p *PlanConfiguration) { p.Domains[1].RedirectTo = "other.test" }, "domains[1].redirectTo"},
		{"to another redirect", func(p *PlanConfiguration) {
			p.Domains = append(p.Domains, PlannedDomain{Hostname: "old.example.test", Ownership: OwnershipManaged, RedirectTo: "www.example.test"})
		}, "domains[2].redirectTo"},
		{"every name redirecting", func(p *PlanConfiguration) { p.Domains[0].RedirectTo = "www.example.test" }, "domains[0].redirectTo"},
		{"from a wildcard", func(p *PlanConfiguration) { p.Domains[1].Hostname = "*.example.test" }, "domains[1].redirectTo"},
		{"an unknown code", func(p *PlanConfiguration) { p.Domains[1].RedirectCode = 303 }, "domains[1].redirectCode"},
		{"a code with nowhere to go", func(p *PlanConfiguration) { p.Domains[0].RedirectCode = 301 }, "domains[0].redirectCode"},
		{"an address that is not one", func(p *PlanConfiguration) { p.Runtime.AllowFrom = []string{"office"} }, "runtime.allowFrom[0]"},
		{"an address twice", func(p *PlanConfiguration) { p.Runtime.AllowFrom = []string{"10.0.0.0/8", "10.0.0.0/8"} }, "runtime.allowFrom[1]"},
		{"a connection header", func(p *PlanConfiguration) {
			p.Runtime.ResponseHeaders = []RouteHeader{{Name: "Content-Length", Value: "1"}}
		}, "runtime.responseHeaders"},
		{"a header value that ends the quotes", func(p *PlanConfiguration) {
			p.Runtime.ResponseHeaders = []RouteHeader{{Name: "X-Note", Value: `a"b`}}
		}, "runtime.responseHeaders"},
	} {
		plan := routingPlan()
		fixture.edit(&plan)
		err := plan.Validate()
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Field != fixture.field {
			t.Fatalf("%s: %v", fixture.name, err)
		}
	}
	// "every name redirecting" is a chain before it is a plan with no
	// served name; a lone redirect to nothing is the second.
	plan := routingPlan()
	plan.Domains = plan.Domains[1:]
	plan.Domains[0].RedirectTo = "www.example.test"
	var validation *ValidationError
	if err := plan.Validate(); !errors.As(err, &validation) {
		t.Fatalf("a plan serving nothing validated: %v", err)
	}
}

// The plan keeps addresses as the proxy writes them, and a domain's row
// keeps the bytes it had before redirects existed.
func TestCanonicalRouteRulesAndDomainRowBytes(t *testing.T) {
	t.Parallel()
	plan := routingPlan()
	plan.Domains[1].RedirectTo = " Example.TEST "
	plan.Runtime.AllowFrom = []string{" 2001:DB8::1 ", "10.0.0.0/8"}
	plan.Runtime.ResponseHeaders = []RouteHeader{{Name: " X-Robots-Tag ", Value: " noindex "}}
	canonical := canonicalConfiguration(plan)
	if canonical.Domains[1].RedirectTo != "example.test" {
		t.Fatalf("redirect target = %q", canonical.Domains[1].RedirectTo)
	}
	if strings.Join(canonical.Runtime.AllowFrom, ",") != "2001:db8::1,10.0.0.0/8" {
		t.Fatalf("allowed = %v", canonical.Runtime.AllowFrom)
	}
	if canonical.Runtime.ResponseHeaders[0] != (RouteHeader{Name: "X-Robots-Tag", Value: "noindex"}) {
		t.Fatalf("headers = %+v", canonical.Runtime.ResponseHeaders)
	}
	empty := routingPlan()
	empty.Runtime.AllowFrom, empty.Runtime.ResponseHeaders = []string{}, []RouteHeader{}
	if raw, _ := json.Marshal(canonicalConfiguration(empty).Runtime); strings.Contains(string(raw), "allowFrom") || strings.Contains(string(raw), "responseHeaders") {
		t.Fatalf("empty rules were kept: %s", raw)
	}

	if raw := string(mustJSON(plan.Domains[0].domainConfig())); raw != `{"hostname":"example.test","https":true}` {
		t.Fatalf("a served domain's row changed: %s", raw)
	}
	redirect := PlannedDomain{Hostname: "www.example.test", HTTPS: true, RedirectTo: "example.test", RedirectCode: 301}
	if raw := string(mustJSON(redirect.domainConfig())); raw != `{"hostname":"www.example.test","https":true,"redirectCode":301,"redirectTo":"example.test"}` {
		t.Fatalf("redirect row = %s", raw)
	}
}

// Activation hands the proxy the redirecting names apart from the served
// ones, and public-route checks ask only the served ones.
func TestDeploymentRouteSeparatesRedirectsFromServedNames(t *testing.T) {
	t.Parallel()
	domains := routingPlan().Domains
	domains[1].RedirectCode = 301
	route := deploymentRoute(7, domains, "127.0.0.1", 3000)
	if strings.Join(route.Domains, ",") != "example.test" {
		t.Fatalf("served = %v", route.Domains)
	}
	if len(route.Redirects) != 1 || route.Redirects[0] != (proxysvc.HostRedirect{From: "www.example.test", To: "example.test", Code: 301}) {
		t.Fatalf("redirects = %+v", route.Redirects)
	}
	if strings.Join(route.Hostnames(), ",") != "example.test,www.example.test" {
		t.Fatalf("certificate names = %v", route.Hostnames())
	}
	if urls := publicRouteURLs(domains); strings.Join(urls, ",") != "https://example.test/" {
		t.Fatalf("public checks ask %v", urls)
	}
}

// The configuration store keeps a redirect and the route's rules across a
// save and a read, which is the path the Domains page writes through.
func TestConfigurationStoreKeepsRedirectsAndRouteRules(t *testing.T) {
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	current, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	runtime := current.Runtime
	runtime.AllowFrom = []string{"203.0.113.7"}
	runtime.ResponseHeaders = []RouteHeader{{Name: "X-Robots-Tag", Value: "noindex"}}
	request := ConfigurationWriteRequest{
		Revision: current.Revision, Build: current.Build, Runtime: runtime,
		Dependencies: current.Dependencies, Checks: current.Checks,
		Domains: routingPlan().Domains,
	}
	if _, err := fixture.variables.SaveEnvironmentConfiguration(ctx, fixture.projectID, fixture.envID, request); err != nil {
		t.Fatal(err)
	}
	read, err := fixture.variables.EnvironmentConfiguration(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	var redirect *PlannedDomain
	for index := range read.Domains {
		if read.Domains[index].Hostname == "www.example.test" {
			redirect = &read.Domains[index]
		}
	}
	if redirect == nil || redirect.RedirectTo != "example.test" || redirect.Ownership != OwnershipManaged {
		t.Fatalf("domains read back = %+v", read.Domains)
	}
	if strings.Join(read.Runtime.AllowFrom, ",") != "203.0.113.7" || len(read.Runtime.ResponseHeaders) != 1 {
		t.Fatalf("runtime read back = %+v", read.Runtime)
	}
}
