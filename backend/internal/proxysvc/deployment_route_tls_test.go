package proxysvc

import (
	"strings"
	"testing"
)

// mixedRoute serves app.example.test over HTTPS and old.example.test over
// plain HTTP, each with a redirecting www. on its own scheme, and sends HSTS.
func mixedRoute() DeploymentRoute {
	return DeploymentRoute{
		Name: "just-dashboard-env-9.conf", Domains: []string{"app.example.test", "old.example.test"},
		Upstream: "http://127.0.0.1:32123", TLS: true, ForceHTTPS: true,
		CertPath: "/etc/ssl/app/fullchain.pem", KeyPath: "/etc/ssl/app/privkey.pem",
		Redirects: []HostRedirect{
			{From: "www.app.example.test", To: "app.example.test"},
			{From: "www.old.example.test", To: "old.example.test", Code: 301},
		},
		PlainHostnames:  []string{"old.example.test", "www.old.example.test"},
		HSTS:            &DeploymentHSTS{MaxAge: 31536000, IncludeSubDomains: true, Preload: true},
		ResponseHeaders: []HeaderValue{{Name: "X-Robots-Tag", Value: "noindex"}},
	}
}

// On nginx the HTTPS name keeps the TLS server with the HSTS header and the
// port-80 redirect; the plain name gets a server of its own on port 80
// with neither, marked for the parser; and each redirecting name answers on
// its own scheme and sends visitors on over the scheme its domain is served
// on. The certificate is asked to cover the HTTPS names alone.
func TestDeploymentRouteHSTSAndPlainNamesOnNginx(t *testing.T) {
	route := mixedRoute()
	if got := strings.Join(route.TLSHostnames(), ","); got != "app.example.test,www.app.example.test" {
		t.Fatalf("certificate names = %q", got)
	}
	content := mustRender(t, deploymentSiteSpec(route, ""))
	for _, want := range []string{
		// The plain-HTTP half of the TLS site redirects the HTTPS name alone.
		"server {\n    listen 80;\n    listen [::]:80;\n    server_name app.example.test;\n",
		"        return 301 https://$host$request_uri;\n",
		"    listen 443 ssl;\n    listen [::]:443 ssl;\n    http2 on;\n    server_name app.example.test;\n",
		"    add_header Strict-Transport-Security \"max-age=31536000; includeSubDomains; preload\" always;\n",
		"# Host redirect (308): www.app.example.test is sent to app.example.test.\nserver {\n    listen 80;\n    listen [::]:80;\n    listen 443 ssl;\n",
		"        return 308 https://app.example.test$request_uri;\n",
		"# Host redirect (301): www.old.example.test is sent to old.example.test over plain HTTP.\nserver {\n    listen 80;\n    listen [::]:80;\n    server_name www.old.example.test;\n",
		"        return 301 http://old.example.test$request_uri;\n",
		"# Plain HTTP: old.example.test is served without TLS.\nserver {\n    listen 80;\n    listen [::]:80;\n    server_name old.example.test;\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("nginx route is missing %q:\n%s", want, content)
		}
	}
	// Sent by the HTTPS server alone; the application's own is hidden by
	// both, since the control owns the header on the route.
	if strings.Count(content, "add_header Strict-Transport-Security") != 1 || strings.Count(content, "    proxy_hide_header Strict-Transport-Security;\n") != 2 {
		t.Fatalf("HSTS is sent on more than the HTTPS server, or the application's own is kept:\n%s", content)
	}
	if strings.Count(content, "add_header X-Robots-Tag") != 2 || strings.Count(content, "proxy_pass http://127.0.0.1:32123;") != 2 {
		t.Fatalf("the plain server does not carry the route's rules and application:\n%s", content)
	}
	plain := content[strings.Index(content, "# Plain HTTP:"):]
	for _, absent := range []string{"ssl_certificate", "listen 443", "add_header Strict-Transport-Security", "return 301 https://"} {
		if strings.Contains(plain, absent) {
			t.Fatalf("the plain server carries %q:\n%s", absent, plain)
		}
	}
	if strings.Contains(content, "server_name app.example.test old.example.test") {
		t.Fatalf("the plain name reached the TLS server:\n%s", content)
	}

	// The Proxy page reads the route back like any site: the plain name,
	// the redirects' schemes and the policy survive, and the file it would
	// write is the same file.
	parsed, _ := ParseSiteSpec(route.Name, content)
	if parsed == nil || strings.Join(parsed.Domains, ",") != "app.example.test,old.example.test" ||
		strings.Join(parsed.PlainDomains, ",") != "old.example.test" {
		t.Fatalf("parsed domains = %+v", parsed)
	}
	if len(parsed.HostRedirects) != 2 || parsed.HostRedirects[0].Plain || !parsed.HostRedirects[1].Plain || parsed.HostRedirects[1].Code != 301 {
		t.Fatalf("parsed redirects = %+v", parsed.HostRedirects)
	}
	if !parsed.HSTS || parsed.HSTSMaxAge != 31536000 || parsed.HSTSOwnNameOnly || !parsed.HSTSPreload {
		t.Fatalf("parsed HSTS = %+v", parsed)
	}
	if again := mustRender(t, parsed); again != content {
		t.Fatalf("re-rendering the parsed route changed it:\n--- first\n%s\n--- again\n%s", content, again)
	}

	// Every served name plain, with an HTTPS name only redirecting: the
	// site's own server is the plain one, the redirecting name still
	// answers on 443 with the certificate, and the file round-trips.
	route.PlainHostnames = []string{"app.example.test", "old.example.test", "www.old.example.test"}
	content = mustRender(t, deploymentSiteSpec(route, ""))
	if strings.Contains(content, "add_header Strict-Transport-Security") || strings.Contains(content, "return 301 https://$host") ||
		!strings.Contains(content, "    server_name app.example.test old.example.test;\n") {
		t.Fatalf("a route with no HTTPS served name still renders a TLS server:\n%s", content)
	}
	if !strings.Contains(content, "# Host redirect (308): www.app.example.test is sent to app.example.test over HTTPS.\nserver {\n    listen 80;\n    listen [::]:80;\n    listen 443 ssl;\n") ||
		!strings.Contains(content, "        return 308 http://app.example.test$request_uri;\n") {
		t.Fatalf("the HTTPS redirecting name lost its listen or sends visitors to an HTTPS its domain does not serve:\n%s", content)
	}
	parsed, _ = ParseSiteSpec(route.Name, content)
	if parsed == nil || !parsed.TLS || parsed.CertPath != "/etc/ssl/app/fullchain.pem" || strings.Join(parsed.PlainDomains, ",") != "app.example.test,old.example.test" {
		t.Fatalf("parsed all-plain route = %+v", parsed)
	}
	if again := mustRender(t, parsed); again != content {
		t.Fatalf("re-rendering the parsed all-plain route changed it:\n--- first\n%s\n--- again\n%s", content, again)
	}

	// A route without TLS is what it always was: one server, no marker.
	route = mixedRoute()
	route.TLS, route.ForceHTTPS, route.PlainHostnames, route.HSTS = false, false, nil, nil
	content = mustRender(t, deploymentSiteSpec(route, ""))
	if strings.Count(content, "server {") != 3 || strings.Contains(content, "# Plain HTTP") || !strings.Contains(content, "    server_name app.example.test old.example.test;\n") {
		t.Fatalf("plain route:\n%s", content)
	}
}

// On Caddy the HTTPS names are one site block, with the certificate and
// HSTS in its deferred header block, and the plain names another with
// http:// addresses and neither, the same handling in each; Caddy refuses a
// tls directive beside an http:// address, which is why they are two.
func TestDeploymentRouteHSTSAndPlainNamesOnCaddy(t *testing.T) {
	route := mixedRoute()
	route.CertPath, route.KeyPath = "", ""
	route.AllowFrom = []string{"10.0.0.0/8"}
	content, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Managed by Just Dashboard\napp.example.test {\n",
		"  header {\n    Strict-Transport-Security \"max-age=31536000; includeSubDomains; preload\"\n    X-Robots-Tag \"noindex\"\n    defer\n  }\n",
		"\nhttp://old.example.test {\n",
		"\nwww.app.example.test {\n  redir https://app.example.test{uri} 308\n}\n",
		"\nhttp://www.old.example.test {\n  redir http://old.example.test{uri} 301\n}\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("Caddy route is missing %q:\n%s", want, content)
		}
	}
	if strings.Count(content, "Strict-Transport-Security \"") != 1 || strings.Count(content, "    reverse_proxy \"http://10.0.0.2:3000\"") != 2 ||
		strings.Count(content, "    @outside not remote_ip 10.0.0.0/8") != 2 || strings.Count(content, "X-Robots-Tag") != 2 {
		t.Fatalf("the two blocks do not share the handling, or HSTS reached the plain one:\n%s", content)
	}
	// The plain block drops the application's own policy rather than
	// sending one: the control owns the header on the route.
	plain := content[strings.Index(content, "\nhttp://old.example.test {"):strings.Index(content, "\nwww.app.example.test {")]
	if !strings.Contains(plain, "  header {\n    -Strict-Transport-Security\n    X-Robots-Tag \"noindex\"\n    defer\n  }\n") || strings.Contains(plain, "tls ") {
		t.Fatalf("the plain block carries TLS, or keeps the application's HSTS:\n%s", plain)
	}
	if !caddyRouteServesTLS(content) {
		t.Fatal("the inventory would list a route with an HTTPS name as plain")
	}

	// HSTS alone is a header block of its own; a route without it and
	// without headers has none.
	route.ResponseHeaders, route.AllowFrom = nil, nil
	if content, err = renderDockerCaddyRoute(route, "http://10.0.0.2:3000"); err != nil ||
		!strings.Contains(content, "app.example.test {\n  header {\n    Strict-Transport-Security \"max-age=31536000; includeSubDomains; preload\"\n    defer\n  }\n") {
		t.Fatalf("HSTS alone = %v:\n%s", err, content)
	}
	// Without the control, the application's own policy passes through
	// on either block.
	route.HSTS = nil
	if content, err = renderDockerCaddyRoute(route, "http://10.0.0.2:3000"); err != nil || strings.Contains(content, "header {") || strings.Contains(content, "Strict-Transport-Security") {
		t.Fatalf("no headers = %v:\n%s", err, content)
	}
	// A route without TLS is the plain block alone, as it always was.
	route = mixedRoute()
	route.TLS, route.ForceHTTPS, route.PlainHostnames, route.HSTS, route.CertPath, route.KeyPath = false, false, nil, nil, "", ""
	content, err = renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil || !strings.HasPrefix(content, "# Managed by Just Dashboard\nhttp://app.example.test, http://old.example.test {\n") ||
		strings.Count(content, "reverse_proxy") != 1 || caddyRouteServesTLS(content) {
		t.Fatalf("plain route = %v:\n%s", err, content)
	}
}

// What neither proxy is handed: a plain name the route does not have, a
// policy outside the bounds a site's is held to, and the header the control
// writes set by hand — refused with the control named.
func TestDeploymentRouteSchemesAndHSTSRefused(t *testing.T) {
	for name, edit := range map[string]func(*DeploymentRoute){
		"plain name the route does not have": func(r *DeploymentRoute) { r.PlainHostnames = []string{"other.example.test"} },
		"plain name twice":                   func(r *DeploymentRoute) { r.PlainHostnames = []string{"old.example.test", "old.example.test"} },
		"plain name on a route without TLS":  func(r *DeploymentRoute) { r.TLS, r.PlainHostnames = false, []string{"old.example.test"} },
		"HSTS on a route without TLS":        func(r *DeploymentRoute) { r.TLS, r.PlainHostnames = false, nil },
		"max-age under five minutes":         func(r *DeploymentRoute) { r.HSTS = &DeploymentHSTS{MaxAge: 60} },
		"max-age over two years":             func(r *DeploymentRoute) { r.HSTS = &DeploymentHSTS{MaxAge: 63072001} },
		"preload under a year": func(r *DeploymentRoute) {
			r.HSTS = &DeploymentHSTS{MaxAge: 2592000, IncludeSubDomains: true, Preload: true}
		},
		"preload without subdomains": func(r *DeploymentRoute) { r.HSTS = &DeploymentHSTS{MaxAge: 31536000, Preload: true} },
	} {
		t.Run(name, func(t *testing.T) {
			route := mixedRoute()
			edit(&route)
			if err := validateDeploymentRules(route); err == nil {
				t.Fatal("validation accepted it")
			}
			if _, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000"); err == nil {
				t.Fatal("Caddy rendered it")
			}
		})
	}
	err := ValidateDeploymentHeaders([]HeaderValue{{Name: "Strict-Transport-Security", Value: "max-age=300"}})
	if err == nil || !strings.Contains(err.Error(), "HSTS control under Access and headers") {
		t.Fatalf("a hand-set HSTS header was refused with %v", err)
	}
	// The policy's defaults: six months, the name alone.
	if got := (DeploymentHSTS{}).Value(); got != "max-age=15552000" {
		t.Fatalf("default policy = %q", got)
	}
	if err := ValidateDeploymentHSTS(DeploymentHSTS{}); err != nil {
		t.Fatalf("the default policy was refused: %v", err)
	}
}
