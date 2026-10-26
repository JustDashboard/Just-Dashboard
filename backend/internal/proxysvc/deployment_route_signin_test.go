package proxysvc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func signInRoute() DeploymentRoute {
	route := rulesRoute()
	route.ForwardAuth = &SiteForwardAuth{
		Provider: "authelia", Verify: "http://127.0.0.1:9091/api/authz/auth-request", SignIn: "https://auth.example.test/",
	}
	return route
}

// On nginx a route that signs in is a Proxy site with single sign-on on
// every path: the check before the application, the user handed on in
// Remote-User and Remote-Email, and the redirecting name a server of its own
// that answers before anyone is asked to sign in.
func TestDeploymentRouteSignInOnNginx(t *testing.T) {
	route := signInRoute()
	content := mustRender(t, deploymentSiteSpec(route, ""))
	for _, want := range []string{
		"    # Single sign-on through authelia.\n",
		"    auth_request /__jd/auth;\n",
		"    error_page 401 =302 https://auth.example.test/?rd=$scheme://$http_host$request_uri;\n",
		"    location = /__jd/auth {\n        internal;\n        proxy_pass http://127.0.0.1:9091/api/authz/auth-request;\n",
		"        proxy_set_header Remote-User  $",
		"    allow 203.0.113.7;\n    allow 10.0.0.0/8;\n    deny all;\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("nginx route is missing %q:\n%s", want, content)
		}
	}
	redirect := content[strings.Index(content, "# Host redirect"):]
	redirect = redirect[:strings.Index(redirect, "\n}\n")]
	if strings.Contains(redirect, "auth_request") {
		t.Fatalf("the redirecting name signs in:\n%s", redirect)
	}

	// The Proxy page reads the route back with its sign-in, and the file it
	// would write is the same file.
	parsed, _ := ParseSiteSpec(route.Name, content)
	if parsed == nil || parsed.ForwardAuth == nil || *parsed.ForwardAuth != *route.ForwardAuth {
		t.Fatalf("parsed sign-in = %+v", parsed)
	}
	if again := mustRender(t, parsed); again != content {
		t.Fatalf("re-rendering the parsed route changed it:\n--- first\n%s\n--- again\n%s", content, again)
	}
}

func TestDeploymentRouteSignInOnCaddy(t *testing.T) {
	route := signInRoute()
	route.CertPath, route.KeyPath = "", ""
	route.ForwardAuth.Verify = "http://10.0.0.3:9091/api/authz/auth-request"
	content, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	gate := "    request_header -Remote-User\n" +
		"    request_header -Remote-Email\n" +
		"    forward_auth \"http://10.0.0.3:9091\" {\n" +
		"      uri \"/api/authz/auth-request\"\n" +
		"      header_up Host {upstream_hostport}\n" +
		"      header_up X-Original-URL {scheme}://{hostport}{uri}\n" +
		"      header_up X-Original-Method {method}\n" +
		"      copy_headers Remote-User Remote-Email\n" +
		"      @signin status 401\n" +
		"      handle_response @signin {\n" +
		"        redir \"https://auth.example.test/?rd={scheme}://{hostport}{uri}\" 302\n" +
		"      }\n" +
		"    }\n"
	if !strings.Contains(content, gate) {
		t.Fatalf("Caddy route is missing its sign-in:\n%s", content)
	}
	// In a route block, in the order written: the fence, then the sign-in,
	// then the application.
	fence := strings.Index(content, "    @outside not remote_ip")
	check := strings.Index(content, "    forward_auth ")
	proxy := strings.Index(content, "    reverse_proxy \"http://10.0.0.2:3000\"")
	if !strings.Contains(content, "  route {\n") || fence < 0 || check < fence || proxy < check {
		t.Fatalf("Caddy route is out of order:\n%s", content)
	}
	if redirect := content[strings.Index(content, "\nwww.example.test {"):]; strings.Contains(redirect, "forward_auth") {
		t.Fatalf("the redirecting name signs in:\n%s", redirect)
	}

	// Without an allow list the route block is still there, for the order.
	route.AllowFrom = nil
	content, err = renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil || !strings.Contains(content, "  route {\n    request_header -Remote-User\n") || strings.Contains(content, "remote_ip") {
		t.Fatalf("sign-in without a fence = %v:\n%s", err, content)
	}
}

// Each provider names the user in its own headers; the application is told
// in Remote-User and Remote-Email whichever it is, as on nginx.
func TestDeploymentRouteSignInCopiesEachProvidersHeaders(t *testing.T) {
	for provider, want := range map[string]string{
		"authelia":     "copy_headers Remote-User Remote-Email\n",
		"custom":       "copy_headers Remote-User Remote-Email\n",
		"authentik":    "copy_headers X-Authentik-Username>Remote-User X-Authentik-Email>Remote-Email\n",
		"oauth2-proxy": "copy_headers X-Auth-Request-User>Remote-User X-Auth-Request-Email>Remote-Email\n",
	} {
		gate := SiteForwardAuth{Provider: provider, Verify: "https://sso.example.test/check", SignIn: "https://sso.example.test/start?app=1"}
		block, err := renderCaddyForwardAuth(gate)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(block, want) {
			t.Fatalf("%s copies:\n%s", provider, block)
		}
		// A sign-in page with a query of its own is given rd= after it.
		if !strings.Contains(block, `redir "https://sso.example.test/start?app=1&rd={scheme}://{hostport}{uri}" 302`) {
			t.Fatalf("%s sign-in redirect:\n%s", provider, block)
		}
		if !strings.Contains(block, "forward_auth \"https://sso.example.test\" {\n    uri \"/check\"\n") {
			t.Fatalf("%s check address:\n%s", provider, block)
		}
	}
	// The nginx variables are the same headers spelled nginx's way, so the
	// two proxies hand on the same user.
	for provider, names := range forwardAuthResponseHeaders {
		for index, name := range names {
			if variable := strings.ToLower(strings.ReplaceAll(name, "-", "_")); variable != forwardAuthSources[provider][index] {
				t.Fatalf("%s: %s is not $upstream_http_%s", provider, name, forwardAuthSources[provider][index])
			}
		}
	}
}

func TestDeploymentRouteSignInRefused(t *testing.T) {
	for name, edit := range map[string]func(*DeploymentRoute){
		"unknown provider":           func(r *DeploymentRoute) { r.ForwardAuth.Provider = "keycloak" },
		"paths of its own":           func(r *DeploymentRoute) { r.ForwardAuth.PathsOnly = true },
		"check address with a query": func(r *DeploymentRoute) { r.ForwardAuth.Verify += "?x=1" },
		"check address that ends quotes": func(r *DeploymentRoute) {
			r.ForwardAuth.Verify = `http://10.0.0.3:9091/a" {`
		},
		"sign-in page with rd=": func(r *DeploymentRoute) { r.ForwardAuth.SignIn += "?rd=x" },
		"sign-in page with a placeholder": func(r *DeploymentRoute) {
			r.ForwardAuth.SignIn = "https://auth.example.test/{http.request.host}"
		},
		"password as well": func(r *DeploymentRoute) {
			r.BasicAuth = []BasicAuthUser{{Username: "team", Hash: "$2a$10$" + strings.Repeat("a", 53)}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			route := signInRoute()
			route.ForwardAuth.Verify = "http://10.0.0.3:9091/api/authz/auth-request"
			edit(&route)
			if _, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000"); err == nil {
				t.Fatal("Caddy rendered a refused sign-in")
			}
			if err := validateDeploymentRules(route); err == nil {
				t.Fatal("validation accepted a refused sign-in")
			}
		})
	}
	// Caddy's loopback is its own container's; activation has to put the
	// publishing container's address in first.
	for _, verify := range []string{"http://127.0.0.1:9091/api/authz/auth-request", "http://localhost:9091/check", "http://[::1]:9091/check"} {
		route := signInRoute()
		route.ForwardAuth.Verify = verify
		if _, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000"); err == nil {
			t.Fatalf("Caddy was pointed at its own loopback for %s", verify)
		}
	}
}

// The maintenance pass reads the auth server's target apart from the
// application's, so repairing one never rewrites the other.
func TestDockerCaddyAuthTargetMetadata(t *testing.T) {
	app := dockerCaddyTarget{ContainerID: strings.Repeat("a", 64), Network: "app_default", Port: "3000", Address: "172.18.0.2"}
	auth := dockerCaddyTarget{ContainerID: strings.Repeat("b", 64), Network: "authelia_default", Port: "9091", Address: "172.19.0.3"}
	content := "# Managed by Just Dashboard\nexample.test {\n}\n" + app.metadata() + auth.authMetadata()
	if got, ok := parseDockerCaddyTarget(content); !ok || got != app {
		t.Fatalf("application target = %+v %v", got, ok)
	}
	if got, ok := parseDockerCaddyAuthTarget(content); !ok || got != auth {
		t.Fatalf("auth target = %+v %v", got, ok)
	}
	if _, ok := parseDockerCaddyAuthTarget("# Managed by Just Dashboard\nexample.test {\n}\n" + app.metadata()); ok {
		t.Fatal("a route without a sign-in has an auth target")
	}

	edge := &dockerCaddy{ID: strings.Repeat("c", 64)}
	containers := []ingressContainer{{ID: edge.ID}, {ID: auth.ContainerID}}
	containers[0].NetworkSettings.Networks = map[string]struct{ IPAddress string }{"app_default": {"172.18.0.9"}}
	containers[1].NetworkSettings.Networks = map[string]struct{ IPAddress string }{"authelia_default": {"172.19.0.7"}}
	next, found, connected := edge.locateTarget(auth, containers)
	if !found || connected || next.Address != "172.19.0.7" || next.ContainerID != auth.ContainerID {
		t.Fatalf("located auth target = %+v found=%v connected=%v", next, found, connected)
	}
}

// What the host's real nginx does with a deployment route that signs in: a
// visitor without a session is sent to sign in, one with a session reaches
// the application as the user the auth server named and never as the one
// they claimed, and the redirecting name answers without asking anyone.
func TestLiveDeploymentRouteSignsInOnNginx(t *testing.T) {
	root := liveNginx(t)
	var checks atomic.Int32
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s|%s", r.Header.Get("Remote-User"), r.Header.Get("Remote-Email"))
	}))
	t.Cleanup(app.Close)
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checks.Add(1)
		if r.URL.Path != "/api/authz/auth-request" || r.Header.Get("X-Original-URL") == "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.Header.Get("Cookie") != "session=alice" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Remote-User", "alice")
		w.Header().Set("Remote-Email", "alice@example.test")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(auth.Close)
	route := DeploymentRoute{
		Name: "just-dashboard-env-5.conf", Domains: []string{"app.example.test"}, Upstream: app.URL,
		Redirects: []HostRedirect{{From: "www.app.example.test", To: "app.example.test"}},
		ForwardAuth: &SiteForwardAuth{
			Provider: "authelia", Verify: auth.URL + "/api/authz/auth-request", SignIn: "https://auth.example.test/",
		},
	}
	port := freePort(t)
	// An unprivileged nginx cannot open the route's request record under
	// /var/log; the route is otherwise installed as rendered.
	spec := deploymentSiteSpec(route, "")
	spec.AccessLog = false
	installSite(t, root, spec, port)
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the route: %s", result.Output)
	}
	startNginx(t, root)

	get := func(host, path string, header ...string) (*http.Response, string) {
		t.Helper()
		client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
		request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
		request.Host = host
		for i := 0; i+1 < len(header); i += 2 {
			request.Header.Set(header[i], header[i+1])
		}
		var response *http.Response
		var err error
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if response, err = client.Do(request); err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response, string(body)
	}

	response, _ := get("app.example.test", "/report?q=1", "Remote-User", "mallory")
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil || response.StatusCode != http.StatusFound || location.Host != "auth.example.test" ||
		location.Query().Get("rd") != "http://app.example.test/report?q=1" {
		t.Fatalf("a visitor without a session was not sent to sign in: %d %q", response.StatusCode, response.Header.Get("Location"))
	}
	response, body := get("app.example.test", "/", "Remote-User", "mallory", "Cookie", "session=alice")
	if response.StatusCode != http.StatusOK || body != "alice|alice@example.test" {
		t.Fatalf("a signed-in visitor reached the application as %q: %d", body, response.StatusCode)
	}
	before := checks.Load()
	response, _ = get("www.app.example.test", "/old")
	if response.StatusCode != http.StatusPermanentRedirect || checks.Load() != before {
		t.Fatalf("the redirecting name = %d after %d checks", response.StatusCode, checks.Load()-before)
	}
}
