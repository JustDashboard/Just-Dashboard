package proxysvc

import (
	"strconv"
	"strings"
	"testing"
)

// A replicated release's route shares its requests between the replicas on
// both proxies, and a release of one container keeps the route it had: no
// upstream block on nginx, one address on Caddy.
func TestDeploymentRouteSharesRequestsBetweenReplicas(t *testing.T) {
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"},
		Upstreams: []string{"http://127.0.0.1:32123", "http://127.0.0.1:32124", "http://127.0.0.1:32125"},
	}
	content, err := RenderNginx(deploymentSiteSpec(route, ""))
	if err != nil {
		t.Fatal(err)
	}
	pool := NginxIdent(route.Name) + "_pool"
	for _, expected := range []string{
		"upstream " + pool + " {\n    least_conn;\n" +
			"    server 127.0.0.1:32123 max_fails=1 fail_timeout=10s;\n" +
			"    server 127.0.0.1:32124 max_fails=1 fail_timeout=10s;\n" +
			"    server 127.0.0.1:32125 max_fails=1 fail_timeout=10s;\n}",
		"proxy_pass http://" + pool + ";",
		"proxy_connect_timeout 2s;",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("replicated nginx route is missing %q:\n%s", expected, content)
		}
	}
	// No keepalive, as before: the pool must not change how the route
	// reaches the application, only where.
	if strings.Contains(content, "keepalive") || strings.Contains(content, "proxy_next_upstream") || strings.Contains(content, "proxy_read_timeout") {
		t.Fatalf("replicated nginx route changed its connection handling:\n%s", content)
	}

	single := route
	single.Upstreams = []string{"http://127.0.0.1:32123"}
	legacy := route
	legacy.Upstreams, legacy.Upstream = nil, "http://127.0.0.1:32123"
	one, two := mustRender(t, deploymentSiteSpec(single, "")), mustRender(t, deploymentSiteSpec(legacy, ""))
	if one != two || strings.Contains(one, "upstream "+pool) || !strings.Contains(one, "proxy_pass http://127.0.0.1:32123;") {
		t.Fatalf("a release of one container did not keep its route:\n%s\n---\n%s", one, two)
	}

	caddy, err := renderDockerCaddyRoute(route, "http://172.18.0.5:3000", "http://172.18.0.6:3000")
	if err != nil {
		t.Fatal(err)
	}
	want := `reverse_proxy "http://172.18.0.5:3000" "http://172.18.0.6:3000" {` + "\n" +
		"      lb_policy least_conn\n      lb_try_duration 5s\n      fail_duration 10s\n      max_fails 1\n" +
		"      transport http {\n        dial_timeout 2s\n      }\n    }"
	if !strings.Contains(caddy, want) {
		t.Fatalf("replicated Caddy route lacks the balanced proxy %q:\n%s", want, caddy)
	}
	lone, err := renderDockerCaddyRoute(route, "http://172.18.0.5:3000")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lone, "reverse_proxy \"http://172.18.0.5:3000\"\n") || strings.Contains(lone, "lb_policy") {
		t.Fatalf("a one-container Caddy route changed:\n%s", lone)
	}
}

// The application's error pages ride in the same reverse_proxy block as the
// balancing, since Caddy takes one block per directive.
func TestReplicatedCaddyRouteKeepsInterceptedErrors(t *testing.T) {
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"},
		ErrorPages: []int{502, 404}, InterceptErrors: true,
	}
	content, err := renderDockerCaddyRoute(route, "http://172.18.0.5:3000", "http://172.18.0.6:3000")
	if err != nil {
		t.Fatal(err)
	}
	want := "dial_timeout 2s\n      }\n      @jd_error status 404 502\n      handle_response @jd_error {"
	if !strings.Contains(content, want) || strings.Count(content, "reverse_proxy ") != 1 {
		t.Fatalf("replicated route split its proxy options:\n%s", content)
	}
}

func TestDeploymentRouteRefusesAnInconsistentPool(t *testing.T) {
	base := DeploymentRoute{Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"}}
	cases := map[string]DeploymentRoute{}
	both := base
	both.Upstream, both.Upstreams = "http://127.0.0.1:1", []string{"http://127.0.0.1:2"}
	cases["both spellings"] = both
	twice := base
	twice.Upstreams = []string{"http://127.0.0.1:1", "http://127.0.0.1:1"}
	cases["one server twice"] = twice
	many := base
	for port := 1; port <= MaxDeploymentUpstreams+1; port++ {
		many.Upstreams = append(many.Upstreams, "http://127.0.0.1:"+strconv.Itoa(30000+port))
	}
	cases["more than eight"] = many
	for name, route := range cases {
		if err := validateDeploymentRules(route); err == nil {
			t.Errorf("%s: accepted %v", name, route.Servers())
		}
	}
	if _, err := renderDockerCaddyRoute(base, "http://172.18.0.5:3000", "http://172.18.0.5:3000"); err == nil {
		t.Error("Caddy accepted one replica's address twice")
	}
}

// The maintenance pass reads every replica's target, and when a daemon
// restart hands two replicas each other's addresses the route is rewritten
// to the swap rather than to one address twice.
func TestCaddyRouteRepairSwapsReplicaAddressesInOnePass(t *testing.T) {
	first := dockerCaddyTarget{ContainerID: strings.Repeat("a", 64), Network: "jd-e7", Port: "3000", Address: "172.18.0.5"}
	second := dockerCaddyTarget{ContainerID: strings.Repeat("b", 64), Network: "jd-e7", Port: "3000", Address: "172.18.0.6"}
	route := DeploymentRoute{Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"}}
	content, err := renderDockerCaddyRoute(route, first.upstream(), second.upstream())
	if err != nil {
		t.Fatal(err)
	}
	content += first.metadata() + second.metadata()
	targets, ok := parseDockerCaddyTargets(content)
	if !ok || len(targets) != 2 || targets[0] != first || targets[1] != second {
		t.Fatalf("targets = %+v, %v", targets, ok)
	}
	movedFirst, movedSecond := first, second
	movedFirst.Address, movedSecond.Address = second.Address, first.Address
	repaired := replaceProxiedUpstreams(content, targets, []dockerCaddyTarget{movedFirst, movedSecond})
	if !strings.Contains(repaired, `reverse_proxy "http://172.18.0.6:3000" "http://172.18.0.5:3000" {`) {
		t.Fatalf("swap was not applied as one:\n%s", repaired)
	}
	if _, ok := parseDockerCaddyTargets("# Managed by Just Dashboard\n" + dockerCaddyTargetPrefix + "{broken\n"); ok {
		t.Fatal("a damaged target line read as a route to repair")
	}
}
