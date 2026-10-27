package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pagesRoute() DeploymentRoute {
	return DeploymentRoute{
		Name: "just-dashboard-env-9.conf", Domains: []string{"example.test"},
		Upstream: "http://127.0.0.1:32123", AccessLog: true,
		Maintenance: DeploymentMaintenance{RetryAfter: 120, BypassFrom: []string{"203.0.113.7", "10.0.0.0/8"}},
		ErrorPages:  []int{502, 404}, InterceptErrors: true,
	}
}

// On nginx the switch is a test for a file in the route's pages folder,
// ahead of everything else the server does, and maintenance answers from a
// location of its own, so the server's 503 stays free for the route's own
// 503 page and an application's 503 is never the maintenance page.
func TestDeploymentRouteMaintenanceOnNginx(t *testing.T) {
	route := pagesRoute()
	route.ErrorPages = append(route.ErrorPages, 503)
	content := mustRender(t, deploymentSiteSpec(route, ""))
	ident := NginxIdent(route.Name)
	if hasOverlongVar(content) {
		ident = NginxVarIdent(route.Name)
	}
	for _, want := range []string{
		"geo $" + ident + "_maint_ip {\n    default 1;\n    203.0.113.7 0;\n    10.0.0.0/8 0;\n}\n",
		"    set $" + ident + "_maint 0;\n" +
			"    if (-f /etc/nginx/jd-pages/just-dashboard-env-9.conf/maintenance.on) {\n" +
			"        set $" + ident + "_maint $" + ident + "_maint_ip;\n    }\n",
		"    if ($" + ident + "_maint) {\n        rewrite ^ /__jd/maintenance last;\n    }\n",
		"    error_page 404 /__jd/404.html;\n    error_page 502 /__jd/502.html;\n    error_page 503 /__jd/503.html;\n",
		"    location = /__jd/maintenance {\n        internal;\n        error_page 503 /__jd/maintenance.html;\n        return 503;\n    }\n",
		"        alias /etc/nginx/jd-pages/just-dashboard-env-9.conf/maintenance.html;\n        add_header Retry-After 120 always;\n",
		"        proxy_intercept_errors on;\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("nginx route is missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "    error_page 503 /__jd/maintenance.html;\n\n") || strings.Count(content, "error_page 503") != 2 {
		t.Fatalf("maintenance holds the server's 503:\n%s", content)
	}
	if strings.Index(content, "maintenance.on") > strings.Index(content, "location / {") {
		t.Fatalf("the switch is checked after the application is chosen:\n%s", content)
	}

	// The Proxy page reads the route back like any site: the switch, who is
	// let past and the pages survive, maintenance reads as off whatever the
	// file says, and the file it would write is the same file.
	parsed, _ := ParseSiteSpec(route.Name, content)
	if parsed == nil || parsed.Maintenance == nil || !parsed.Maintenance.Switch || parsed.Maintenance.On ||
		parsed.Maintenance.RetryAfter != 120 || strings.Join(parsed.Maintenance.BypassFrom, ",") != "203.0.113.7,10.0.0.0/8" {
		t.Fatalf("parsed maintenance = %+v", parsed.Maintenance)
	}
	if len(parsed.Locations) != 0 || len(parsed.ErrorPages) != 3 || !parsed.InterceptErrors {
		t.Fatalf("parsed locations = %+v, pages = %v", parsed.Locations, parsed.ErrorPages)
	}
	if again := mustRender(t, parsed); again != content {
		t.Fatalf("re-rendering the parsed route changed it:\n--- first\n%s\n--- again\n%s", content, again)
	}

	// A route with none of it still carries the switch, and no Retry-After.
	bare := DeploymentRoute{Name: "just-dashboard-env-9.conf", Domains: []string{"example.test"}, Upstream: "http://127.0.0.1:32123"}
	content = mustRender(t, deploymentSiteSpec(bare, ""))
	if !strings.Contains(content, "maintenance.on) {") || strings.Contains(content, "Retry-After") ||
		strings.Contains(content, "error_page 404") || strings.Contains(content, "proxy_intercept_errors") {
		t.Fatalf("bare route:\n%s", content)
	}
}

// On Caddy the switch is a file matcher, which Caddy also reads on every
// request, and it sits in the route block after the address list and
// before the password: who is refused is refused either way, and nobody is
// asked to sign in to read the maintenance page.
func TestDeploymentRouteMaintenanceOnCaddy(t *testing.T) {
	route := pagesRoute()
	route.AllowFrom = []string{"192.0.2.0/24"}
	route.BasicAuth = []BasicAuthUser{{Username: "team", Hash: "$2a$10$" + strings.Repeat("a", 53)}}
	content, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	pages := "/config/just-dashboard/pages/just-dashboard-env-9"
	for _, want := range []string{
		"    @jd_maintenance {\n      file {\n        root " + pages + "\n        try_files /maintenance.on\n      }\n" +
			"      not remote_ip 203.0.113.7 10.0.0.0/8\n      not path /.well-known/acme-challenge/*\n    }\n",
		"    route @jd_maintenance {\n      header Retry-After 120\n      root * " + pages + "\n" +
			"      rewrite * /maintenance.html\n      file_server {\n        status 503\n      }\n    }\n",
		"    reverse_proxy \"http://10.0.0.2:3000\" {\n      @jd_error status 404 502\n      handle_response @jd_error {\n",
		"  handle_errors {\n    @jd_page expression `{err.status_code} in [404, 502]`\n    handle @jd_page {\n      root * " + pages + "\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("Caddy route is missing %q:\n%s", want, content)
		}
	}
	fence := strings.Index(content, "    @outside not remote_ip")
	maintenance := strings.Index(content, "    @jd_maintenance {")
	auth := strings.Index(content, "    basic_auth {")
	proxy := strings.Index(content, "    reverse_proxy ")
	if fence < 0 || maintenance < fence || auth < maintenance || proxy < auth {
		t.Fatalf("Caddy route is out of order:\n%s", content)
	}

	// No Retry-After, no error pages and no intercept when none are asked for.
	bare := DeploymentRoute{Name: route.Name, Domains: route.Domains}
	content, err = renderDockerCaddyRoute(bare, "http://10.0.0.2:3000")
	if err != nil || strings.Contains(content, "Retry-After") || strings.Contains(content, "handle_errors") ||
		strings.Contains(content, "@jd_error") || strings.Contains(content, "not remote_ip") {
		t.Fatalf("bare Caddy route = %v:\n%s", err, content)
	}
	// The nameless route a certificate is issued through has no switch.
	content, err = renderDockerCaddyRoute(DeploymentRoute{Domains: route.Domains, TLS: true}, "")
	if err != nil || strings.Contains(content, "jd_maintenance") {
		t.Fatalf("certificate route = %v:\n%s", err, content)
	}
}

func TestDeploymentRoutePagesRefused(t *testing.T) {
	for name, edit := range map[string]func(*DeploymentRoute){
		"a negative wait":                        func(r *DeploymentRoute) { r.Maintenance.RetryAfter = -1 },
		"a wait over a day":                      func(r *DeploymentRoute) { r.Maintenance.RetryAfter = MaxRetryAfter + 1 },
		"a bypass that is not an address":        func(r *DeploymentRoute) { r.Maintenance.BypassFrom[0] = "office" },
		"a bypass not in its canonical spelling": func(r *DeploymentRoute) { r.Maintenance.BypassFrom[0] = "2001:DB8::1" },
		"a bypass that closes the matcher":       func(r *DeploymentRoute) { r.Maintenance.BypassFrom[0] = "10.0.0.1 }" },
		"a page for a code it cannot have":       func(r *DeploymentRoute) { r.ErrorPages = []int{500} },
		"a page twice":                           func(r *DeploymentRoute) { r.ErrorPages = []int{502, 502} },
		"replacing the application's with none":  func(r *DeploymentRoute) { r.ErrorPages = nil },
		"replacing the application's 401 sign-in": func(r *DeploymentRoute) {
			r.ForwardAuth = &SiteForwardAuth{Provider: "authelia", Verify: "http://10.0.0.3:9091/api/authz/auth-request", SignIn: "https://auth.example.test/"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			route := pagesRoute()
			edit(&route)
			if err := validateDeploymentRules(route); err == nil {
				t.Fatal("validation accepted it")
			}
			if _, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000"); err == nil {
				t.Fatal("Caddy rendered it")
			}
		})
	}
}

// The switch turns maintenance on and off with one file beside the route:
// the route's bytes never change, and a route written before the switch
// existed is refused rather than told it is in maintenance.
func TestDeploymentMaintenanceSwitchOnNginx(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	service := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	route := pagesRoute()
	content, err := RenderNginx(service.deploymentSpec(route))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sites-available", route.Name)

	// No route yet: nothing to read the switch.
	state, err := service.SetDeploymentMaintenance(ctx, route.Name, true)
	if !errors.Is(err, ErrMaintenanceUnready) || state.Route {
		t.Fatalf("switch without a route = %+v, %v", state, err)
	}
	// A route from before the switch: there, but it would not read it.
	if err := os.WriteFile(path, []byte("server {\n    listen 80;\n    server_name example.test;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := service.SetDeploymentMaintenance(ctx, route.Name, true); !errors.Is(err, ErrMaintenanceUnready) || !state.Route || state.Ready {
		t.Fatalf("switch on an old route = %+v, %v", state, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := service.DeploymentMaintenance(ctx, route.Name); err != nil || !state.Ready || state.On || state.Driver != "nginx" {
		t.Fatalf("off = %+v, %v", state, err)
	}
	state, err = service.SetDeploymentMaintenance(ctx, route.Name, true)
	if err != nil || !state.On || state.Since == nil {
		t.Fatalf("on = %+v, %v", state, err)
	}
	flag := filepath.Join(service.SitePagesDir(route.Name), "maintenance.on")
	page := filepath.Join(service.SitePagesDir(route.Name), "maintenance.html")
	if !strings.Contains(content, "if (-f "+flag+") {") {
		t.Fatalf("the route tests another file than the switch writes:\n%s", content)
	}
	for _, file := range []string{flag, page} {
		if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("%s = %v, %v", file, info, err)
		}
	}
	if after, _ := os.ReadFile(path); string(after) != content {
		t.Fatal("turning maintenance on rewrote the route")
	}
	if state, err := service.SetDeploymentMaintenance(ctx, route.Name, false); err != nil || state.On {
		t.Fatalf("off again = %+v, %v", state, err)
	}
	if _, err := os.Stat(flag); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the switch file stayed: %v", err)
	}
	// Off is always allowed, so a switch left behind can be cleared.
	if err := os.WriteFile(path, []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(flag, []byte("by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, err := service.SetDeploymentMaintenance(ctx, route.Name, false); err != nil || state.On {
		t.Fatalf("clearing a switch an old route does not read = %+v, %v", state, err)
	}

	service.mu.Lock()
	err = service.removeDeploymentPagesLocked(route.Name)
	service.mu.Unlock()
	if _, statErr := os.Stat(service.SitePagesDir(route.Name)); err != nil || !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("pages after removal = %v, %v", err, statErr)
	}
	// A route that never had pages is removed all the same.
	service.mu.Lock()
	err = service.removeDeploymentPagesLocked("just-dashboard-env-10.conf")
	service.mu.Unlock()
	if err != nil {
		t.Fatalf("removing a route without pages = %v", err)
	}
	// A link put in the pages folder's place is removed, never followed.
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "maintenance.on"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, service.SitePagesDir(route.Name)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetDeploymentMaintenance(ctx, route.Name, false); err == nil {
		t.Fatal("switched off through a link out of the pages folder")
	}
	service.mu.Lock()
	err = service.removeDeploymentPagesLocked(route.Name)
	service.mu.Unlock()
	if _, statErr := os.Stat(filepath.Join(elsewhere, "maintenance.on")); err != nil || statErr != nil {
		t.Fatalf("removal followed the link: %v, %v", err, statErr)
	}
}

// A route's pages are the shipped ones until the operator writes their own,
// and only the route's pages can be written.
func TestDeploymentPagesOnNginx(t *testing.T) {
	root := t.TempDir()
	service := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	name := "just-dashboard-env-9.conf"
	page, err := service.ReadDeploymentPage(ctx, name, "502")
	shipped, _ := ShippedSitePage("502")
	if err != nil || page.Custom || page.Content != shipped {
		t.Fatalf("default 502 = %+v, %v", page, err)
	}
	if _, err := service.WriteDeploymentPage(ctx, name, "502", "<h1>Back soon</h1>"); err != nil {
		t.Fatal(err)
	}
	page, err = service.ReadDeploymentPage(ctx, name, "502")
	if err != nil || !page.Custom || page.Content != "<h1>Back soon</h1>" {
		t.Fatalf("own 502 = %+v, %v", page, err)
	}
	if info, err := os.Stat(filepath.Join(service.SitePagesDir(name), "502.html")); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("502 file = %v, %v", info, err)
	}
	for _, refused := range []struct{ name, page, content string }{
		{name, "security", "x"},
		{name, "500", "x"},
		{name, "502", strings.Repeat("x", MaxPageSize+1)},
		{"../escape", "502", "x"},
	} {
		if _, err := service.WriteDeploymentPage(ctx, refused.name, refused.page, refused.content); err == nil {
			t.Fatalf("wrote %s/%s", refused.name, refused.page)
		}
	}
	// Activation gives a route the pages it serves, keeping one written.
	route := pagesRoute()
	spec := service.deploymentSpec(route)
	if err := service.writeMissingPages(spec); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"maintenance.html", "404.html"} {
		if _, err := os.Stat(filepath.Join(service.SitePagesDir(name), file)); err != nil {
			t.Fatalf("%s not written: %v", file, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(service.SitePagesDir(name), "502.html")); string(b) != "<h1>Back soon</h1>" {
		t.Fatalf("activation replaced the operator's page: %q", b)
	}
}

// The Proxy page's reading of who reaches a URL follows the switch: no
// maintenance layer while it is off, the site's own judgement while on.
func TestExplainAccessFollowsTheMaintenanceSwitch(t *testing.T) {
	maint := plainSpec("maint", "maint.example.com")
	maint.Maintenance = &SiteMaintenance{Switch: true, BypassFrom: []string{"192.0.2.0/24"}}
	tree := accessTree(t, map[string]string{"maint": render(t, maint)})
	on := false
	saved := switchFileExists
	switchFileExists = func(path string) bool {
		return on && path == "/etc/nginx/jd-pages/maint/maintenance.on"
	}
	t.Cleanup(func() { switchFileExists = saved })

	explain := func(source string) *AccessExplanation {
		t.Helper()
		a, err := ExplainAccess(tree, "http://maint.example.com/", source)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := explain("198.51.100.1")
	for _, layer := range a.Layers {
		if layer.ID == "maintenance" || layer.ID == "server-if" {
			t.Fatalf("an off switch reads as a layer: %+v", layer)
		}
	}
	if a.Verdict != AccessAdmitted {
		t.Fatalf("off = %s (%s)", a.Verdict, a.Summary)
	}
	on = true
	if layer := layerOf(t, explain("198.51.100.1"), "maintenance"); layer.Verdict != LayerRefuses || layer.Owner != "Maintenance switch" {
		t.Fatalf("on, from outside = %+v", layer)
	}
	if layer := layerOf(t, explain("192.0.2.9"), "maintenance"); layer.Verdict != LayerAdmits {
		t.Fatalf("on, from a bypass address = %+v", layer)
	}
}

// The Proxy page marks a deployment's route as in maintenance by its
// switch, not by the check every route carries.
func TestListingMarksADeploymentRouteByItsSwitch(t *testing.T) {
	svc, root := debianTree(t)
	route := pagesRoute()
	content, err := RenderNginx(svc.deploymentSpec(route))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "sites-available", route.Name), content)
	if listed(t, svc.nginxVHosts(), "sites-available", route.Name).Maintenance {
		t.Fatal("a route whose switch is off is marked as in maintenance")
	}
	if _, err := svc.SetDeploymentMaintenance(context.Background(), route.Name, true); err != nil {
		t.Fatal(err)
	}
	if !listed(t, svc.nginxVHosts(), "sites-available", route.Name).Maintenance {
		t.Fatal("a route whose switch is on is not marked")
	}
}
