package proxysvc

import (
	"strings"
	"testing"
)

// Skew protection on both proxies: a visitor whose cookie names a release
// the route serves is answered by it; everyone else gets the live release,
// or a canary's draw, and a cookie saying which.
func TestSkewProtectionRoutesByTheReleaseCookie(t *testing.T) {
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"}, AccessLog: true,
		Upstream: "http://127.0.0.1:32123",
		Skew: &DeploymentSkew{Release: "41", MaxAge: 600,
			Pinned: []DeploymentPinned{{Release: "40", Upstreams: []string{"http://127.0.0.1:32100"}}}},
	}
	if err := validateDeploymentRules(route); err != nil {
		t.Fatal(err)
	}
	content := mustRender(t, deploymentSiteSpec(route, ""))
	ident := NginxIdent(route.Name)
	for _, expected := range []string{
		"upstream " + ident + "_release_40 {\n    least_conn;\n    server 127.0.0.1:32100 max_fails=1 fail_timeout=10s;\n}",
		"map $cookie_jd_release $jd_",
		"    41 41;\n    40 40;\n    default 41;\n}",
		"    40 " + ident + "_release_40;\n    default " + ident + "_pool;\n}",
		"    \"41:41\" \"\";\n    \"40:40\" \"\";\n    default \"jd_release=$jd_",
		"; Path=/; Max-Age=600; HttpOnly; SameSite=Lax$jd_",
		"    https \"; Secure\";",
		"    add_header Set-Cookie $jd_",
		"rel=$jd_",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("nginx skew route is missing %q:\n%s", expected, content)
		}
	}

	// With a canary, the cookie decides first and the draw only for the rest.
	both := route
	both.Canary = &DeploymentCanary{Percent: 10, Upstreams: []string{"http://127.0.0.1:32200"}, Live: "41", Candidate: "42"}
	content = mustRender(t, deploymentSiteSpec(both, ""))
	for _, expected := range []string{
		"split_clients \"${request_id}\" $jd_",
		"_draw {\n    10% 42;\n    * 41;\n}",
		"    41 41;\n    42 42;\n    40 40;\n    default $jd_",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("nginx canary with skew protection is missing %q:\n%s", expected, content)
		}
	}

	caddy, err := renderDockerCaddyRoute(DeploymentRoute{
		Name: route.Name, Domains: route.Domains, AccessLog: true, TLS: true, ForceHTTPS: true,
		Upstream: "http://172.18.0.5:3000", PlainHostnames: nil,
		Skew: &DeploymentSkew{Release: "41", MaxAge: 600,
			Pinned: []DeploymentPinned{{Release: "40", Upstreams: []string{"http://172.18.0.4:3000"}}}},
	}, "http://172.18.0.5:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"@jd_release_40 header_regexp Cookie `(?:^|;\\s*)jd_release=40(?:;|$)`\n",
		"handle @jd_release_40 {\n      log_append release 40\n      reverse_proxy \"http://172.18.0.4:3000\"\n    }",
		"handle @jd_release_41 {\n      log_append release 41\n      reverse_proxy \"http://172.18.0.5:3000\"\n    }",
		"handle {\n      log_append release 41\n      header {\n        +Set-Cookie \"jd_release=41; Path=/; Max-Age=600; HttpOnly; SameSite=Lax; Secure\"\n        defer\n      }\n",
	} {
		if !strings.Contains(caddy, expected) {
			t.Fatalf("Caddy skew route is missing %q:\n%s", expected, caddy)
		}
	}
	// Plain HTTP names get the cookie without Secure, which a browser would
	// refuse there.
	plain, err := renderDockerCaddyRoute(DeploymentRoute{Name: route.Name, Domains: route.Domains, AccessLog: true,
		Upstream: "http://172.18.0.5:3000", Skew: &DeploymentSkew{Release: "41", MaxAge: 600}}, "http://172.18.0.5:3000")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "Secure") || !strings.Contains(plain, "SameSite=Lax\"") {
		t.Fatalf("a plain route's cookie:\n%s", plain)
	}
}

func TestSkewProtectionRefusesWhatEitherProxyWouldWriteWrong(t *testing.T) {
	base := DeploymentRoute{Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"}, Upstream: "http://127.0.0.1:1"}
	for name, skew := range map[string]DeploymentSkew{
		"no lifetime":       {Release: "1"},
		"past an hour":      {Release: "1", MaxAge: 3601},
		"a pin like live":   {Release: "1", MaxAge: 60, Pinned: []DeploymentPinned{{Release: "1", Upstreams: []string{"http://127.0.0.1:2"}}}},
		"a pin on live's":   {Release: "1", MaxAge: 60, Pinned: []DeploymentPinned{{Release: "2", Upstreams: []string{"http://127.0.0.1:1"}}}},
		"a pin of nothing":  {Release: "1", MaxAge: 60, Pinned: []DeploymentPinned{{Release: "2"}}},
		"a name with a gap": {Release: "1 2", MaxAge: 60},
	} {
		route := base
		route.Skew = &skew
		if err := validateDeploymentRules(route); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	mismatched := base
	mismatched.Skew = &DeploymentSkew{Release: "1", MaxAge: 60}
	mismatched.Canary = &DeploymentCanary{Percent: 10, Upstreams: []string{"http://127.0.0.1:2"}, Live: "3", Candidate: "2"}
	if err := validateDeploymentRules(mismatched); err == nil {
		t.Error("a canary and skew protection naming different live releases was accepted")
	}
}
