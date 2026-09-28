package proxysvc

import (
	"strings"
	"testing"
)

// nginx refuses a request body over 1 MB unless told otherwise, which is a
// phone photo; a deployment route carries its plan's limit, or a default
// that lets ordinary uploads reach the application. Caddy has no limit of
// its own, so its routes carry one only when the plan names it.
func TestDeploymentRouteRequestBodyLimitOnBothProxies(t *testing.T) {
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"},
		Upstream: "http://127.0.0.1:32123",
	}
	if content := mustRender(t, deploymentSiteSpec(route, "")); !strings.Contains(content, "client_max_body_size 64m;") {
		t.Fatalf("nginx default limit missing:\n%s", content)
	}
	caddy, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(caddy, "request_body") {
		t.Fatalf("Caddy route gained a limit nobody asked for:\n%s", caddy)
	}

	route.MaxBodyMB = 512
	if content := mustRender(t, deploymentSiteSpec(route, "")); !strings.Contains(content, "client_max_body_size 512m;") {
		t.Fatalf("nginx explicit limit missing:\n%s", content)
	}
	caddy, err = renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(caddy, "  request_body {\n    max_size 512MB\n  }\n  reverse_proxy \"http://10.0.0.2:3000\"") {
		t.Fatalf("Caddy explicit limit missing:\n%s", caddy)
	}
	// The limit is a route setting the site editor reads back like any other.
	parsed, _ := ParseSiteSpec(route.Name, mustRender(t, deploymentSiteSpec(route, "")))
	if parsed == nil || parsed.ClientMaxBody != "512m" {
		t.Fatalf("parsed = %+v", parsed)
	}
}
