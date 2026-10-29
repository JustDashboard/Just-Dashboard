package proxysvc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

func rulesRoute() DeploymentRoute {
	return DeploymentRoute{
		Name: "just-dashboard-env-9.conf", Domains: []string{"example.test"},
		Upstream: "http://127.0.0.1:32123", TLS: true, ForceHTTPS: true,
		CertPath: "/etc/ssl/app/fullchain.pem", KeyPath: "/etc/ssl/app/privkey.pem",
		Redirects:       []HostRedirect{{From: "www.example.test", To: "example.test"}},
		ResponseHeaders: []HeaderValue{{Name: "X-Robots-Tag", Value: "noindex"}},
		AllowFrom:       []string{"203.0.113.7", "10.0.0.0/8"},
	}
}

// On nginx the redirecting name is a server of its own, which still answers
// the ACME challenge so the shared certificate can cover it, and the route's
// own server keeps the name off its server_name.
func TestDeploymentRouteRulesOnNginx(t *testing.T) {
	route := rulesRoute()
	content := mustRender(t, deploymentSiteSpec(route, ""))
	for _, want := range []string{
		"# Host redirect (308): www.example.test is sent to example.test.\nserver {",
		"    server_name www.example.test;\n",
		"        return 308 https://example.test$request_uri;\n",
		"    location ^~ /.well-known/acme-challenge/ {\n        root " + deploymentACMEWebroot + ";\n",
		"    server_name example.test;\n",
		"    allow 203.0.113.7;\n    allow 10.0.0.0/8;\n    deny all;\n",
		"    add_header X-Robots-Tag \"noindex\" always;\n",
		"proxy_hide_header X-Robots-Tag;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("nginx route is missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "server_name example.test www.example.test") {
		t.Fatalf("the redirecting name reached the application's server:\n%s", content)
	}

	// The Proxy page reads a deployment's route back like any site; the
	// redirect survives that, and the file it would write is the same file.
	parsed, _ := ParseSiteSpec(route.Name, content)
	if parsed == nil || len(parsed.HostRedirects) != 1 || parsed.HostRedirects[0] != (HostRedirect{From: "www.example.test", To: "example.test", Code: 308}) {
		t.Fatalf("parsed redirects = %+v", parsed)
	}
	if len(parsed.Domains) != 1 || parsed.Domains[0] != "example.test" {
		t.Fatalf("parsed domains = %v", parsed.Domains)
	}
	if again := mustRender(t, parsed); again != content {
		t.Fatalf("re-rendering the parsed route changed it:\n--- first\n%s\n--- again\n%s", content, again)
	}
}

func TestDeploymentRouteRulesOnCaddy(t *testing.T) {
	route := rulesRoute()
	route.CertPath, route.KeyPath = "", ""
	route.BasicAuth = []BasicAuthUser{{Username: "team", Hash: "$2a$10$" + strings.Repeat("a", 53)}}
	content, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  header {\n    X-Robots-Tag \"noindex\"\n    defer\n  }\n",
		"\nwww.example.test {\n",
		"  redir https://example.test{uri} 308\n}\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("Caddy route is missing %q:\n%s", want, content)
		}
	}
	// The fence runs before the password is asked for: Caddy would order a
	// bare respond after basic_auth, so both sit in a route block.
	fence := strings.Index(content, "    @outside not remote_ip 203.0.113.7 10.0.0.0/8\n    respond @outside \"Forbidden\" 403\n")
	auth := strings.Index(content, "    basic_auth {")
	proxy := strings.Index(content, "    reverse_proxy \"http://10.0.0.2:3000\"")
	if !strings.Contains(content, "  route {\n") || fence < 0 || auth < fence || proxy < auth {
		t.Fatalf("Caddy fence is not ahead of the password and the proxy:\n%s", content)
	}
	// The redirect block is the name and its redirect, nothing else.
	redirect := content[strings.Index(content, "\nwww.example.test {"):]
	if strings.Contains(redirect, "basic_auth") || strings.Contains(redirect, "remote_ip") || strings.Contains(redirect, "log {") {
		t.Fatalf("redirect block carries the route's rules:\n%s", redirect)
	}

	route.TLS = false
	content, err = renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil || !strings.Contains(content, "\nhttp://www.example.test {\n  redir http://example.test{uri} 308\n}\n") {
		t.Fatalf("plain-HTTP redirect = %v:\n%s", err, content)
	}
}

// A route asking for none of the rules renders the application behind the
// maintenance switch every route carries, and nothing else.
func TestDeploymentRouteWithoutRulesCarriesOnlyTheSwitch(t *testing.T) {
	route := DeploymentRoute{Name: "just-dashboard-env-9.conf", Domains: []string{"example.test"}, Upstream: "http://127.0.0.1:32123"}
	content, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	if want := "# Managed by Just Dashboard\nhttp://example.test {\n  route {\n" + indentCaddy(renderCaddyMaintenance(DeploymentMaintenance{}, "/config/just-dashboard/pages/just-dashboard-env-9")) +
		"    reverse_proxy \"http://10.0.0.2:3000\"\n  }\n}\n"; content != want {
		t.Fatalf("Caddy route = %q, want %q", content, want)
	}
	nginx := mustRender(t, deploymentSiteSpec(route, ""))
	for _, absent := range []string{"Host redirect", "\n    allow ", "deny all", "proxy_hide_header"} {
		if strings.Contains(nginx, absent) {
			t.Fatalf("nginx route without rules has %q:\n%s", absent, nginx)
		}
	}
}

func TestDeploymentRouteRulesRefused(t *testing.T) {
	for name, edit := range map[string]func(*DeploymentRoute){
		"redirect to a name the route does not serve": func(r *DeploymentRoute) { r.Redirects[0].To = "other.test" },
		"redirect from a served name":                 func(r *DeploymentRoute) { r.Redirects[0].From = "example.test" },
		"redirect from a wildcard":                    func(r *DeploymentRoute) { r.Redirects[0].From = "*.example.test" },
		"redirect code":                               func(r *DeploymentRoute) { r.Redirects[0].Code = 303 },
		"header that belongs to the connection":       func(r *DeploymentRoute) { r.ResponseHeaders[0].Name = "Transfer-Encoding" },
		"header the security headers already send":    func(r *DeploymentRoute) { r.ResponseHeaders[0].Name = "X-Content-Type-Options" },
		"header value that ends the quotes":           func(r *DeploymentRoute) { r.ResponseHeaders[0].Value = `a" ; evil` },
		"header value with a Caddy placeholder":       func(r *DeploymentRoute) { r.ResponseHeaders[0].Value = "{http.request.host}" },
		"address that is not one":                     func(r *DeploymentRoute) { r.AllowFrom[0] = "example.test" },
		"range with host bits":                        func(r *DeploymentRoute) { r.AllowFrom[1] = "10.0.0.1/8" },
		"address not in its canonical spelling":       func(r *DeploymentRoute) { r.AllowFrom[0] = "2001:DB8::1" },
	} {
		t.Run(name, func(t *testing.T) {
			route := rulesRoute()
			edit(&route)
			if _, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000"); err == nil {
				t.Fatal("Caddy rendered a refused rule")
			}
			if err := validateDeploymentRules(route); err == nil {
				t.Fatal("validation accepted a refused rule")
			}
		})
	}
}

func TestDeploymentRouteHostnamesIncludeRedirects(t *testing.T) {
	got := rulesRoute().Hostnames()
	if strings.Join(got, ",") != "example.test,www.example.test" {
		t.Fatalf("hostnames = %v", got)
	}
}

// What Caddy actually answers for a route's rules: the redirecting name
// sends the path and query on, an address off the list is refused, and the
// route's header replaces the application's own rather than joining it.
// Then a route serving one name over HTTPS and another over plain HTTP, with
// HSTS: the HTTPS name carries the policy and sends its plain-HTTP visitors
// to HTTPS, the plain name is served as asked with neither, and the
// inventory lists every name under the route.
func TestLiveDockerCaddyServesRouteRules(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for isolated Docker ingress test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker fixture command failed: %v: %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(source, []byte("# fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	network := fmt.Sprintf("jd-ingress-rules-%d", time.Now().UnixNano())
	docker("network", "create", network)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", network).Run()
	bindings := []string{}
	for _, port := range []string{"80", "443"} {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, "-p", fmt.Sprintf("127.0.0.1:%d:%s", listener.Addr().(*net.TCPAddr).Port, port))
		listener.Close()
	}
	id := docker(append(append([]string{"run", "-d", "--network", network}, bindings...), "-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")...)
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	// The application sends the header the route is about to set, so the
	// test can tell replaced from added.
	app := docker("run", "-d", "-p", "127.0.0.1::80", "caddy:2-alpine", "caddy", "respond", "--listen", ":80", "--header", "X-Robots-Tag: all", "application answer")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", app).Run()
	published := func(id string, port string) string {
		return strings.TrimPrefix(docker("port", id, port+"/tcp"), "127.0.0.1:")
	}
	edge := &dockerCaddy{ID: id, Name: "fixture", Source: source, Identity: "fixture-persistent-storage"}
	endpoint := "http://127.0.0.1:" + published(id, "80")
	// The HTTPS name's certificate is the test's own, so the client trusts
	// nothing and names the site; the name is what Caddy serves by.
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(host, path string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint+path, nil)
		req.Host = host
		res, err := client.Do(req)
		if err == nil {
			res.Body.Close()
		}
		return res, err
	}
	getTLS := func(host, path string) (*http.Response, error) {
		secure := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: host}, DisableKeepAlives: true,
		}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://127.0.0.1:"+published(id, "443")+path, nil)
		req.Host = host
		res, err := secure.Do(req)
		if err == nil {
			res.Body.Close()
		}
		return res, err
	}
	for i := 0; i < 40; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	service := New(dir, source)
	route := DeploymentRoute{
		Name: "just-dashboard-env-43.conf", Domains: []string{"rules.example.test"},
		Upstream:        "http://127.0.0.1:" + published(app, "80"),
		Redirects:       []HostRedirect{{From: "www.rules.example.test", To: "rules.example.test"}},
		ResponseHeaders: []HeaderValue{{Name: "X-Robots-Tag", Value: "noindex"}},
		AllowFrom:       []string{"203.0.113.7"},
		// Traced as activation renders it, so the trace's directives are
		// checked inside the route block a fence puts the handling in.
		AccessLog: true, Trace: true,
	}
	if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
		t.Fatalf("apply=%+v error=%v; fixture log: %s", result, err, docker("logs", id))
	}
	res, err := get("www.rules.example.test", "/pricing?plan=team")
	if err != nil || res.StatusCode != 308 || res.Header.Get("Location") != "http://rules.example.test/pricing?plan=team" {
		t.Fatalf("redirect = %v %v", res, err)
	}
	if res, err := get("rules.example.test", "/"); err != nil || res.StatusCode != 403 {
		t.Fatalf("an address off the list was answered: %v %v", res, err)
	}

	// The test reaches the ingress through Docker's published port, from
	// whatever address the bridge gives it; every IPv4 address is on this list.
	route.AllowFrom = []string{"0.0.0.0/0"}
	if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
		t.Fatalf("reapply=%+v error=%v", result, err)
	}
	res, err = get("rules.example.test", "/")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("allowed address = %v %v", res, err)
	}
	if got := res.Header.Values("X-Robots-Tag"); len(got) != 1 || got[0] != "noindex" {
		t.Fatalf("X-Robots-Tag = %q, want the route's alone", got)
	}
	listed := func(want string) {
		t.Helper()
		inventory, err := edge.vhosts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, site := range inventory {
			names := append([]string(nil), site.ServerNames...)
			sort.Strings(names)
			if site.Name == route.Name && strings.Join(names, ",") == want {
				return
			}
		}
		t.Fatalf("the route's inventory does not carry %s: %+v", want, inventory)
	}
	listed("rules.example.test,www.rules.example.test")

	// One name over HTTPS with the test's own certificate and HSTS, the
	// other over plain HTTP, each with a redirecting www. of its own scheme.
	// The application sends an HSTS of its own, which the route's replaces.
	certPEM, keyPEM, _ := selfSigned(t, "tls.rules.example.test", time.Now().Add(24*time.Hour))
	certPath, keyPath := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	for path, content := range map[string]string{certPath: certPEM, keyPath: keyPEM} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hstsApp := docker("run", "-d", "-p", "127.0.0.1::80", "caddy:2-alpine", "caddy", "respond", "--listen", ":80", "--header", "Strict-Transport-Security: max-age=1", "application answer")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", hstsApp).Run()
	route = DeploymentRoute{
		Name: route.Name, Domains: []string{"tls.rules.example.test", "rules.example.test"},
		Upstream: "http://127.0.0.1:" + published(hstsApp, "80"), TLS: true, ForceHTTPS: true,
		CertPath: certPath, KeyPath: keyPath,
		Redirects: []HostRedirect{
			{From: "www.tls.rules.example.test", To: "tls.rules.example.test"},
			{From: "www.rules.example.test", To: "rules.example.test"},
		},
		PlainHostnames: []string{"rules.example.test", "www.rules.example.test"},
		HSTS:           &DeploymentHSTS{MaxAge: 31536000, IncludeSubDomains: true},
		AllowFrom:      []string{"0.0.0.0/0"},
	}
	if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
		t.Fatalf("mixed apply=%+v error=%v; fixture log: %s", result, err, docker("logs", id))
	}
	res, err = getTLS("tls.rules.example.test", "/")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("the HTTPS name over TLS = %v %v", res, err)
	}
	if got := res.Header.Values("Strict-Transport-Security"); len(got) != 1 || got[0] != "max-age=31536000; includeSubDomains" {
		t.Fatalf("HSTS on the HTTPS name = %q, want the route's alone", got)
	}
	res, err = get("tls.rules.example.test", "/pricing?plan=team")
	if err != nil || res.StatusCode != 308 || res.Header.Get("Location") != "https://tls.rules.example.test/pricing?plan=team" {
		t.Fatalf("the HTTPS name over plain HTTP = %v %v", res, err)
	}
	res, err = get("rules.example.test", "/")
	if err != nil || res.StatusCode != 200 || res.Header.Get("Location") != "" {
		t.Fatalf("the plain name was not served as asked: %v %v", res, err)
	}
	if got := res.Header.Values("Strict-Transport-Security"); len(got) != 0 {
		t.Fatalf("the plain name was sent HSTS: %q", got)
	}
	res, err = get("www.rules.example.test", "/pricing?plan=team")
	if err != nil || res.StatusCode != 308 || res.Header.Get("Location") != "http://rules.example.test/pricing?plan=team" {
		t.Fatalf("the plain redirecting name = %v %v", res, err)
	}
	res, err = get("www.tls.rules.example.test", "/pricing?plan=team")
	if err != nil || res.StatusCode != 308 || res.Header.Get("Location") != "https://www.tls.rules.example.test/pricing?plan=team" {
		t.Fatalf("the HTTPS redirecting name over plain HTTP = %v %v", res, err)
	}
	listed("rules.example.test,tls.rules.example.test,www.rules.example.test,www.tls.rules.example.test")

	// Edge rules: a blocked path refused 403, a user agent answered with
	// the rule's status, an allow rule letting its path through ahead of a
	// broader block, and the request record naming the rule that decided.
	// Then CrowdSec's list, banning every address the bridge can give the
	// test, refused at the route, and lifted by a refresh with no release.
	route = DeploymentRoute{
		Name: route.Name, Domains: []string{"rules.example.test"}, AccessLog: true, CrowdSec: true,
		Upstream:  "http://127.0.0.1:" + published(app, "80"),
		AllowFrom: []string{"0.0.0.0/0"},
		EdgeRules: CanonicalEdgeRules([]EdgeRule{
			{Name: "health", Match: EdgeMatch{PathPrefix: "/admin/health"}, Action: EdgeAction{Kind: "allow"}},
			{Name: "admin", Match: EdgeMatch{PathPrefix: "/admin"}, Action: EdgeAction{Kind: "block"}},
			{Name: "bots", Match: EdgeMatch{UserAgent: "evilbot", Methods: []string{"GET"}}, Action: EdgeAction{Kind: "status", Status: 418}},
		}),
	}
	if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
		t.Fatalf("edge apply=%+v error=%v; fixture log: %s", result, err, docker("logs", id))
	}
	asked := func(path, agent string) int {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint+path, nil)
		req.Host = "rules.example.test"
		if agent != "" {
			req.Header.Set("User-Agent", agent)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for _, testCase := range []struct {
		path, agent string
		status      int
	}{
		{"/", "", 200},
		{"/admin/users", "", 403},
		{"/ADMIN/users", "", 200},
		{"/admin/health", "", 200},
		{"/", "Mozilla/5.0 EvilBot/2", 418},
	} {
		if got := asked(testCase.path, testCase.agent); got != testCase.status {
			t.Fatalf("GET %s (%q) = %d, want %d", testCase.path, testCase.agent, got, testCase.status)
		}
	}
	if refreshed, err := service.refreshDockerCaddyCrowdSec(ctx, edge, []string{"0.0.0.0/0"}); err != nil || !refreshed.Reloaded {
		t.Fatalf("ban refresh = %+v, %v", refreshed, err)
	}
	if got := asked("/", ""); got != 403 {
		t.Fatalf("a banned address = %d", got)
	}
	if refreshed, err := service.refreshDockerCaddyCrowdSec(ctx, edge, nil); err != nil || !refreshed.Reloaded {
		t.Fatalf("lifting refresh = %+v, %v", refreshed, err)
	}
	if got := asked("/", ""); got != 200 {
		t.Fatalf("a lifted ban = %d", got)
	}
	logPath, _ := dockerCaddyAccessLogPath(route.Name)
	decided := map[string]int{}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		raw, _ := hostexec.Command(ctx, "docker", "exec", id, "cat", logPath).Output()
		decided = map[string]int{}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if entry, ok := accesslog.Parse(line); ok && entry.EdgeRule != "" {
				decided[entry.EdgeRule]++
			}
		}
		if decided["admin"] > 0 && decided["bots"] > 0 && decided["health"] > 0 && decided["crowdsec"] > 0 || time.Now().After(deadline) {
			break
		}
	}
	for _, name := range []string{"admin", "bots", "health", "crowdsec"} {
		if decided[name] == 0 {
			t.Fatalf("the record does not name %s: %v", name, decided)
		}
	}
}
