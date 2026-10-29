package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sleepingRoute() DeploymentRoute {
	route := pagesRoute()
	route.Sleep = &DeploymentSleep{HoldSeconds: 30}
	return route
}

// On nginx the application's address is chosen at server level, after the
// maintenance switch and before every check, and the location forwards
// through it: while the sleeping switch is there nothing is sent to an
// address the stopped container may since have lost. A refusal at once, or
// the application not answering, is handed to @jd_wake, which asks the
// dashboard and then sends the request on as it came.
func TestSleepingRouteOnNginx(t *testing.T) {
	route := sleepingRoute()
	content := mustRender(t, deploymentSiteSpec(route, ""))
	ident := NginxIdent(route.Name)
	if hasOverlongVar(content) {
		ident = NginxVarIdent(route.Name)
	}
	pages := "/etc/nginx/jd-pages/just-dashboard-env-9.conf"
	for _, want := range []string{
		"    set $" + ident + "_up 127.0.0.1:32123;\n" +
			"    if (-f " + pages + "/sleeping.on) {\n" +
			"        set $" + ident + "_up unix:" + pages + "/asleep.sock;\n    }\n",
		"    location / {\n        proxy_pass http://$" + ident + "_up;\n",
		"        recursive_error_pages on;\n        error_page 502 504 = @jd_wake;\n        error_page 404 /__jd/404.html;\n    }\n",
		"    location @jd_wake {\n",
		"        auth_request /__jd/wake;\n",
		"        proxy_pass http://127.0.0.1:32123;\n",
		"        error_page 401 502 504 =503 /__jd/waking.html;\n",
		"        error_page 403 500 = @jd_down;\n",
		"    location @jd_down {\n        return 502;\n    }\n",
		"        proxy_pass http://unix:/etc/nginx/jd-wake/wake.sock:/wake/just-dashboard-env-9.conf;\n",
		"        proxy_pass_request_headers off;\n",
		"        proxy_read_timeout 40s;\n",
		"        alias " + pages + "/waking.html;\n        add_header Retry-After 5 always;\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("sleeping nginx route is missing %q:\n%s", want, content)
		}
	}
	if strings.Index(content, "maintenance.on") > strings.Index(content, "sleeping.on") {
		t.Fatalf("the sleeping switch is read before maintenance:\n%s", content)
	}
	// The Proxy page reads the route back as the site it serves: its own
	// address, its pages, and none of the wake's locations as the
	// operator's.
	parsed, _ := ParseSiteSpec(route.Name, content)
	if parsed == nil || parsed.Upstream != "http://127.0.0.1:32123" || len(parsed.Locations) != 0 ||
		parsed.Maintenance == nil || parsed.Maintenance.RetryAfter != 120 || len(parsed.ErrorPages) != 2 {
		t.Fatalf("parsed = %+v", parsed)
	}
	// A route without sleep has none of it.
	plain := mustRender(t, deploymentSiteSpec(pagesRoute(), ""))
	for _, absent := range []string{"sleeping.on", "@jd_wake", "/__jd/wake", "waking.html", "_up "} {
		if strings.Contains(plain, absent) {
			t.Fatalf("a route that never sleeps carries %q:\n%s", absent, plain)
		}
	}
}

// A route that signs in keeps the sign-in's redirect where the location
// names error pages of its own, and the wake keeps the visitor's identity:
// it sets a variable of its own, so the sign-in's are not read again from
// the wake's answer.
func TestSleepingRouteKeepsTheSignIn(t *testing.T) {
	route := sleepingRoute()
	route.InterceptErrors = false
	route.ForwardAuth = &SiteForwardAuth{Provider: "authelia", Verify: "http://127.0.0.1:9091/api/verify", SignIn: "https://auth.example.test/"}
	content := mustRender(t, deploymentSiteSpec(route, ""))
	main := content[strings.Index(content, "    location / {"):]
	main = main[:strings.Index(main, "\n    }\n")]
	if !strings.Contains(main, "error_page 401 =302 https://auth.example.test/?rd=") {
		t.Fatalf("the application's location lost the sign-in's redirect:\n%s", main)
	}
	wake := content[strings.Index(content, "    location @jd_wake {"):]
	wake = wake[:strings.Index(wake, "\n    }\n")]
	if !strings.Contains(wake, "auth_request_set $") || strings.Contains(wake, "=302") {
		t.Fatalf("the wake location = \n%s", wake)
	}
	if !strings.Contains(content, "proxy_set_header Remote-User") {
		t.Fatalf("the visitor's identity is not sent on:\n%s", content)
	}
}

// On Caddy the switch sits in the route block after the fence, maintenance
// and the sign-in, and the application's errors are woken ahead of its
// pages — but only an error from the application itself, which is what the
// jd_app mark says: an auth server that does not answer is a 502 too.
func TestSleepingRouteOnCaddy(t *testing.T) {
	route := sleepingRoute()
	route.InterceptErrors = false
	route.AllowFrom = []string{"203.0.113.0/24"}
	route.ForwardAuth = &SiteForwardAuth{Provider: "authelia", Verify: "http://172.18.0.9:9091/api/verify", SignIn: "https://auth.example.test/"}
	content, err := renderDockerCaddyRoute(route, "http://172.18.0.5:8080")
	if err != nil {
		t.Fatal(err)
	}
	pages := "/config/just-dashboard/pages/just-dashboard-env-9"
	order := []string{
		"@outside not remote_ip 203.0.113.0/24",
		"@jd_maintenance {",
		"forward_auth \"http://172.18.0.9:9091\" {",
		"@jd_asleep file {\n      root " + pages + "\n      try_files /sleeping.on\n    }\n",
		"      reverse_proxy unix//config/just-dashboard/wake/wake.sock {\n",
		"        rewrite \"/wake/just-dashboard-env-9.conf\"\n",
		"          vars jd_wake_upstream {rp.header.X-Jd-Upstream}\n",
		"      reverse_proxy {vars.jd_wake_upstream} {\n        lb_try_duration 5s\n",
		"    vars jd_app 1\n    reverse_proxy \"http://172.18.0.5:8080\" {\n      transport http {\n        dial_timeout 1s\n      }\n    }\n",
		"  handle_errors {\n    @jd_wake {\n      expression `{err.status_code} in [502, 504]`\n      vars jd_app 1\n    }\n",
		"    @jd_page expression `{err.status_code} in [404, 502]`\n",
	}
	at := 0
	for _, want := range order {
		index := strings.Index(content[at:], want)
		if index < 0 {
			t.Fatalf("Caddy route is missing %q after byte %d:\n%s", want, at, content)
		}
		at += index + len(want)
	}
	for _, want := range []string{
		"          rewrite * /waking.html\n",
		"          rewrite * /502.html\n          file_server {\n            status 502\n",
		"        header_up -Cookie\n        header_up -Authorization\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("Caddy route is missing %q:\n%s", want, content)
		}
	}
	// No 502 page of its own: Caddy's 502, as the route would have given.
	route.ErrorPages = nil
	content, err = renderDockerCaddyRoute(route, "http://172.18.0.5:8080")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "respond \"Bad Gateway\" 502") || strings.Contains(content, "@jd_page") ||
		!strings.Contains(content, "  handle_errors {\n    @jd_wake {") {
		t.Fatalf("Caddy route without pages:\n%s", content)
	}
	// And a route that never sleeps is what it was.
	plain, err := renderDockerCaddyRoute(pagesRoute(), "http://172.18.0.5:8080")
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"sleeping.on", "jd_app", "wake.sock", "waking"} {
		if strings.Contains(plain, absent) {
			t.Fatalf("a route that never sleeps carries %q:\n%s", absent, plain)
		}
	}
}

func TestSleepingRouteValidation(t *testing.T) {
	route := sleepingRoute()
	for _, hold := range []int{0, -1, MaxWakeHold + 1} {
		route.Sleep.HoldSeconds = hold
		if err := validateDeploymentRules(route); err == nil {
			t.Errorf("hold %d was accepted", hold)
		}
	}
	route.Sleep.HoldSeconds = 30
	route.Upstream = "http://app.internal:8080"
	if err := validateDeploymentRules(route); err == nil {
		t.Error("a sleeping route forwarding to a name was accepted")
	}
	route = sleepingRoute()
	route.Name = "elsewhere.conf"
	if err := validateDeploymentRules(route); err == nil {
		t.Error("a sleeping route under another name was accepted")
	}
}

// The switch on nginx: refused for a route that would not wake the
// application, written with the waking page beside it, read back with when
// it was turned on, and cleared whatever the route is.
func TestSleepingSwitchOnNginx(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	service := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	route := sleepingRoute()
	if _, err := service.SetDeploymentSleeping(ctx, route.Name, true); err != ErrSleepUnready {
		t.Fatalf("switching on a route that is not there = %v", err)
	}
	plain := mustRender(t, service.deploymentSpec(pagesRoute()))
	if err := os.WriteFile(filepath.Join(root, "sites-available", route.Name), []byte(plain), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetDeploymentSleeping(ctx, route.Name, true); err != ErrSleepUnready {
		t.Fatalf("switching on a route without the wake = %v", err)
	}
	content := mustRender(t, service.deploymentSpec(route))
	if !strings.Contains(content, "proxy_pass http://unix:"+filepath.Join(root, "jd-wake", "wake.sock")+":/wake/") {
		t.Fatalf("the route does not ask this host's socket:\n%s", content)
	}
	if err := os.WriteFile(filepath.Join(root, "sites-available", route.Name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := service.SetDeploymentSleeping(ctx, route.Name, true)
	if err != nil || !state.Ready || !state.On || state.Since == nil || state.Driver != "nginx" {
		t.Fatalf("switched on = %+v, %v", state, err)
	}
	waking, _ := ShippedSitePage("waking")
	if got, err := os.ReadFile(filepath.Join(service.SitePagesDir(route.Name), "waking.html")); err != nil || string(got) != waking {
		t.Fatalf("waking page = %q, %v", got, err)
	}
	if !strings.Contains(waking, `http-equiv="refresh"`) {
		t.Fatal("the shipped waking page does not try again by itself")
	}
	state, err = service.SetDeploymentSleeping(ctx, route.Name, false)
	if err != nil || state.On {
		t.Fatalf("switched off = %+v, %v", state, err)
	}
	if _, err := os.Stat(filepath.Join(service.SitePagesDir(route.Name), "sleeping.on")); !os.IsNotExist(err) {
		t.Fatalf("the switch file is still there: %v", err)
	}
	sockets, err := service.WakeSockets(ctx)
	if err != nil || len(sockets) != 1 || sockets[0].Proxy != "nginx" || sockets[0].Path != filepath.Join(root, "jd-wake", "wake.sock") {
		t.Fatalf("wake sockets = %+v, %v", sockets, err)
	}
}
