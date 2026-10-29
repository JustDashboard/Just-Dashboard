package proxysvc

import (
	"strings"
	"testing"
)

// A route serving the name the dashboard is reached by is one the browser
// sends the dashboard's session to, whatever port the dashboard is on: the
// route as this host writes it drops the dashboard's cookies. A route on any
// other name — the preview domain's, a sibling's — is written as it was, and
// a name that only redirects serves no application to keep them from.
func TestOnlyARouteOnTheDashboardsNameDropsItsCookies(t *testing.T) {
	service := New(t.TempDir(), t.TempDir()+"/Caddyfile").WithDashboardHost("Dash.Example.test.")
	if service.dashboardHost != "dash.example.test" {
		t.Fatalf("host = %q", service.dashboardHost)
	}
	route := func(domains ...string) DeploymentRoute {
		return DeploymentRoute{Name: "just-dashboard-env-9.conf", Domains: domains, Upstream: "http://127.0.0.1:32123"}
	}
	for name, test := range map[string]struct {
		route DeploymentRoute
		drops bool
	}{
		"the dashboard's name":          {route("dash.example.test"), true},
		"beside another name":           {route("app.example.test", "DASH.example.test"), true},
		"a wildcard covering it":        {route("*.example.test"), true},
		"another name":                  {route("app.example.test"), false},
		"the parent name":               {route("example.test"), false},
		"a wildcard two labels up":      {route("*.test"), false},
		"a preview domain":              {route("pr-12.preview.example.test"), false},
		"a name that only redirects to": {DeploymentRoute{Name: "just-dashboard-env-9.conf", Domains: []string{"app.example.test"}, Upstream: "http://127.0.0.1:32123", Redirects: []HostRedirect{{From: "dash.example.test", To: "app.example.test"}}}, false},
	} {
		got := service.onDashboardName(test.route)
		if drops := len(got.DropCookies) > 0; drops != test.drops {
			t.Errorf("%s: drops = %v (%v)", name, drops, got.DropCookies)
		}
		if test.drops && strings.Join(got.DropCookies, ",") != "__Host-jd_session,jd_session,vpsd_session" {
			t.Errorf("%s: dropped %v", name, got.DropCookies)
		}
	}
	if got := New(t.TempDir(), t.TempDir()+"/Caddyfile").onDashboardName(route("dash.example.test")); len(got.DropCookies) != 0 {
		t.Fatalf("a service told no dashboard name dropped %v", got.DropCookies)
	}
	if again := service.onDashboardName(service.onDashboardName(route("dash.example.test"))); len(again.DropCookies) != 3 {
		t.Fatalf("applied twice = %v", again.DropCookies)
	}
	for site, want := range map[string]string{"box.tail1234.ts.net": "box.tail1234.ts.net", "BOX.example.test:8443": "box.example.test", "[::1]:8443": "::1", " localhost ": "localhost"} {
		if got := DashboardHost(site); got != want {
			t.Errorf("DashboardHost(%q) = %q, want %q", site, got, want)
		}
	}
}

// On nginx the route forwards the Cookie header without the dashboard's
// session: a chain of maps above the servers, one a name, and every
// forwarding location — and the sign-in check — sending what is left. The
// Proxy page reads the route back as the same file.
func TestNginxRouteOnTheDashboardsNameForwardsCookiesWithoutItsSession(t *testing.T) {
	service := New(t.TempDir(), t.TempDir()+"/Caddyfile").WithDashboardHost("example.test")
	route := service.onDashboardName(rulesRoute())
	route.ForwardAuth = &SiteForwardAuth{Provider: "authelia", Verify: "http://127.0.0.1:9091/api/authz/auth-request", SignIn: "https://auth.example.test/"}
	spec := deploymentSiteSpec(route, "")
	content := mustRender(t, spec)
	ident := spec.varIdent()
	for _, want := range []string{
		"map $http_cookie $" + ident + "_ck1 {\n    default $http_cookie;\n    \"~^__Host-jd_session=[^;]*(?:; *)?(?<jd_ckr1>.*)$\" $jd_ckr1;\n",
		"map $" + ident + "_ck1 $" + ident + "_ck2 {\n    default $" + ident + "_ck1;\n    \"~^jd_session=",
		"map $" + ident + "_ck2 $" + ident + "_cookie {\n    default $" + ident + "_ck2;\n    \"~^vpsd_session=",
		"        proxy_set_header Cookie     $" + ident + "_cookie;\n",
		"        proxy_set_header Cookie             $" + ident + "_cookie;\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("the nginx route is missing %q:\n%s", want, content)
		}
	}
	parsed, _ := ParseSiteSpec(route.Name, content)
	if parsed == nil || strings.Join(parsed.dropCookies, ",") != "__Host-jd_session,jd_session,vpsd_session" {
		t.Fatalf("parsed = %+v", parsed)
	}
	if parsed.Headers != nil && len(parsed.Headers.Request) > 0 {
		t.Fatalf("the route's Cookie header was read as one the form sets: %+v", parsed.Headers.Request)
	}
	if again := mustRender(t, parsed); again != content {
		t.Fatalf("re-rendering the parsed route changed it:\n--- first\n%s\n--- again\n%s", content, again)
	}

	// Elsewhere the route is the file it always was.
	other := mustRender(t, deploymentSiteSpec(rulesRoute(), ""))
	if strings.Contains(other, "Cookie") || strings.Contains(other, "_ck1") {
		t.Fatalf("a route on another name drops cookies:\n%s", other)
	}
}

// On the Caddy ingress the same is two header_up replacements in the
// route's reverse_proxy, beside the error interception the route already
// had there, and in its sign-in check.
func TestCaddyRouteOnTheDashboardsNameForwardsCookiesWithoutItsSession(t *testing.T) {
	service := New(t.TempDir(), t.TempDir()+"/Caddyfile").WithDashboardHost("example.test")
	route := service.onDashboardName(rulesRoute())
	route.CertPath, route.KeyPath = "", ""
	route.ErrorPages, route.InterceptErrors = []int{502}, true
	content, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	drop := "header_up Cookie \"(^|; *)(__Host-jd_session|jd_session|vpsd_session)=[^;]*\" \"\"\n"
	if want := "    reverse_proxy \"http://10.0.0.2:3000\" {\n      " + drop + "      header_up Cookie \"^; *\" \"\"\n      @jd_error status 502\n"; !strings.Contains(content, want) {
		t.Fatalf("the Caddy route is missing %q:\n%s", want, content)
	}
	signIn := service.onDashboardName(rulesRoute())
	signIn.CertPath, signIn.KeyPath = "", ""
	signIn.ForwardAuth = &SiteForwardAuth{Provider: "authelia", Verify: "http://authelia:9091/api/authz/forward-auth", SignIn: "https://auth.example.test/"}
	content, err = renderDockerCaddyRoute(signIn, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"    reverse_proxy \"http://10.0.0.2:3000\" {\n      " + drop + "      header_up Cookie \"^; *\" \"\"\n    }\n",
		"      header_up X-Original-Method {method}\n      " + drop,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("the Caddy route with a sign-in is missing %q:\n%s", want, content)
		}
	}
	other := rulesRoute()
	other.CertPath, other.KeyPath = "", ""
	plain, err := renderDockerCaddyRoute(other, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "Cookie") {
		t.Fatalf("a route on another name drops cookies:\n%s", plain)
	}
	bad := route
	bad.DropCookies = []string{"a\" b"}
	if _, err := renderDockerCaddyRoute(bad, "http://10.0.0.2:3000"); err == nil {
		t.Fatal("a cookie name that is not a token was rendered")
	}
}
