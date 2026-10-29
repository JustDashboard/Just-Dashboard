package proxysvc

import (
	"regexp"
	"strings"
	"testing"
)

// A canary route shares its requests between the live release and the
// candidate on both proxies, picks one at random per request, and records
// which release answered.
func TestCanaryRouteSplitsBetweenTwoReleases(t *testing.T) {
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"}, AccessLog: true,
		Upstream: "http://127.0.0.1:32123",
		Canary: &DeploymentCanary{Percent: 10, Upstreams: []string{"http://127.0.0.1:32200", "http://127.0.0.1:32201"},
			Live: "41", Candidate: "42"},
	}
	if err := validateDeploymentRules(route); err != nil {
		t.Fatal(err)
	}
	content := mustRender(t, deploymentSiteSpec(route, ""))
	ident := NginxIdent(route.Name)
	for _, expected := range []string{
		"upstream " + ident + "_pool {\n    least_conn;\n    server 127.0.0.1:32123 max_fails=1 fail_timeout=10s;\n}",
		"upstream " + ident + "_canary {\n    least_conn;\n    server 127.0.0.1:32200 max_fails=1 fail_timeout=10s;\n    server 127.0.0.1:32201 max_fails=1 fail_timeout=10s;\n}",
		"split_clients \"${request_id}\" $jd_",
		"    10% 42;\n    * 41;\n}",
		"    42 " + ident + "_canary;\n    default " + ident + "_pool;\n}",
		"proxy_pass http://$jd_",
		"rel=$jd_",
		"proxy_connect_timeout 2s;",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("nginx canary route is missing %q:\n%s", expected, content)
		}
	}

	caddy, err := renderDockerCaddyRoute(DeploymentRoute{
		Name: route.Name, Domains: route.Domains, AccessLog: true, Upstream: "http://172.18.0.5:3000",
		Canary: &DeploymentCanary{Percent: 10, Upstreams: []string{"http://172.18.0.8:3000", "http://172.18.0.9:3000"}, Live: "41", Candidate: "42"},
	}, "http://172.18.0.5:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"@jd_canary vars_regexp {http.request.uuid} ^(?:[0][0-9a-f]|1[0123456789])\n",
		"handle @jd_canary {\n      log_append release 42\n      reverse_proxy \"http://172.18.0.8:3000\" \"http://172.18.0.9:3000\" {\n        lb_policy least_conn\n",
		"handle {\n      log_append release 41\n      reverse_proxy \"http://172.18.0.5:3000\"\n    }",
	} {
		if !strings.Contains(caddy, expected) {
			t.Fatalf("Caddy canary route is missing %q:\n%s", expected, caddy)
		}
	}
}

// The candidate's share is the run of the 256 two-hex-digit prefixes the
// pattern names, which is the percent asked for to within one bucket.
func TestCanaryUUIDPatternTakesItsShare(t *testing.T) {
	const hex = "0123456789abcdef"
	for percent := 1; percent <= 50; percent++ {
		pattern := regexp.MustCompile(canaryUUIDPattern(percent))
		matched := 0
		for _, first := range hex {
			for _, second := range hex {
				if pattern.MatchString(string(first) + string(second) + "000000-0000-4000-8000-000000000000") {
					matched++
				}
			}
		}
		if want := (percent*256 + 50) / 100; matched != max(1, want) {
			t.Errorf("%d%%: %d buckets, want %d (%s)", percent, matched, want, canaryUUIDPattern(percent))
		}
	}
}

func TestCanaryRouteRefusesWhatEitherProxyWouldWriteWrong(t *testing.T) {
	base := DeploymentRoute{Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"}, Upstream: "http://127.0.0.1:1"}
	for name, canary := range map[string]DeploymentCanary{
		"no share":          {Percent: 0, Upstreams: []string{"http://127.0.0.1:2"}, Live: "1", Candidate: "2"},
		"all of it":         {Percent: 100, Upstreams: []string{"http://127.0.0.1:2"}, Live: "1", Candidate: "2"},
		"no servers":        {Percent: 10, Live: "1", Candidate: "2"},
		"the live server":   {Percent: 10, Upstreams: []string{"http://127.0.0.1:1"}, Live: "1", Candidate: "2"},
		"one name":          {Percent: 10, Upstreams: []string{"http://127.0.0.1:2"}, Live: "1", Candidate: "1"},
		"a name with a gap": {Percent: 10, Upstreams: []string{"http://127.0.0.1:2"}, Live: "1 2", Candidate: "3"},
	} {
		route := base
		route.Canary = &canary
		if err := validateDeploymentRules(route); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
