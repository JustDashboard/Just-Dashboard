package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func proxySpec() *SiteSpec {
	return &SiteSpec{
		Name: "app", Kind: "proxy", Domains: []string{"app.example.com"},
		Upstream: "http://127.0.0.1:3000",
		TLS:      true, ForceHTTPS: true, HTTP2: true, HSTS: true, SecurityHeaders: true,
		CertPath:   "/etc/letsencrypt/live/app.example.com/fullchain.pem",
		KeyPath:    "/etc/letsencrypt/live/app.example.com/privkey.pem",
		WebSockets: true, Gzip: true, AccessLog: true,
		ClientMaxBody: "50m", ProxyTimeout: 60,
		AllowFrom: []string{}, DenyFrom: []string{}, Locations: []SiteLocation{},
	}
}

func TestValidateSpecAcceptsATypicalSite(t *testing.T) {
	if err := ValidateSpec(proxySpec()); err != nil {
		t.Fatalf("rejected: %v", err)
	}
}

func TestValidateSpecRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*SiteSpec)
	}{
		{"no domains", func(s *SiteSpec) { s.Domains = nil }},
		{"bad domain", func(s *SiteSpec) { s.Domains = []string{"not a domain"} }},
		{"name with a slash", func(s *SiteSpec) { s.Name = "../etc/passwd" }},
		{"upstream with a semicolon", func(s *SiteSpec) { s.Upstream = "http://127.0.0.1:3000; root /" }},
		{"upstream with no scheme", func(s *SiteSpec) { s.Upstream = "127.0.0.1:3000" }},
		{"TLS with no certificate", func(s *SiteSpec) { s.CertPath = "" }},
		{"relative certificate path", func(s *SiteSpec) { s.CertPath = "certs/fullchain.pem" }},
		{"upload limit that is not a size", func(s *SiteSpec) { s.ClientMaxBody = "lots" }},
		{"timeout out of range", func(s *SiteSpec) { s.ProxyTimeout = 99999 }},
		{"acl entry that is not an address", func(s *SiteSpec) { s.AllowFrom = []string{"office"} }},
		{"unknown kind", func(s *SiteSpec) { s.Kind = "magic" }},
		{"unbalanced braces in the escape hatch", func(s *SiteSpec) { s.Custom = "location / {" }},
		{"location with nowhere to go", func(s *SiteSpec) {
			s.Locations = []SiteLocation{{Path: "/api"}}
		}},
		{"location path that is not a path", func(s *SiteSpec) {
			s.Locations = []SiteLocation{{Path: "api", Upstream: "http://127.0.0.1:4000"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := proxySpec()
			tc.mutate(spec)
			if err := ValidateSpec(spec); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// A newline in a scalar would put a directive of the caller's choosing into
// the file, past the person reading the form.
func TestValidateSpecRefusesNewlinesInScalars(t *testing.T) {
	spec := proxySpec()
	spec.Upstream = "http://127.0.0.1:3000\n    root /;"
	if err := ValidateSpec(spec); err == nil {
		t.Fatal("accepted an upstream carrying a second directive")
	}
}

func TestRenderNginxProxySite(t *testing.T) {
	out, err := RenderNginx(proxySpec())
	if err != nil {
		t.Fatal(err)
	}
	must := []string{
		managedMarker,
		"listen 443 ssl;",
		"http2 on;",
		"server_name app.example.com;",
		"ssl_certificate     /etc/letsencrypt/live/app.example.com/fullchain.pem;",
		"ssl_protocols TLSv1.2 TLSv1.3;",
		"add_header Strict-Transport-Security",
		"add_header X-Content-Type-Options nosniff always;",
		"client_max_body_size 50m;",
		"gzip on;",
		"proxy_pass http://127.0.0.1:3000;",
		"proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;",
		"proxy_set_header Upgrade    $http_upgrade;",
		"proxy_read_timeout    60s;",
		"return 301 https://$host$request_uri;",
		"location /.well-known/acme-challenge/ {",
	}
	for _, want := range must {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from:\n%s", want, out)
		}
	}
	if strings.Count(out, "server {") != 2 {
		t.Errorf("a forced-HTTPS site needs a redirect server and a TLS one:\n%s", out)
	}
}

// nginx 1.25 deprecated the listen parameter and warns on every reload.
func TestRenderUsesTheHTTP2Directive(t *testing.T) {
	out, _ := RenderNginx(proxySpec())
	if strings.Contains(out, "listen 443 ssl http2") {
		t.Error("used the deprecated listen parameter")
	}
}

// Certbot proves control over plain HTTP. A redirect that swallows the
// challenge path stops renewal working, and nobody finds out for sixty days.
func TestRenderKeepsTheACMEChallengeReachable(t *testing.T) {
	out, _ := RenderNginx(proxySpec())
	acme := strings.Index(out, "/.well-known/acme-challenge/")
	redirect := strings.Index(out, "return 301 https://$host$request_uri;")
	if acme < 0 || redirect < 0 || acme > redirect {
		t.Fatalf("the challenge location must come before the catch-all redirect:\n%s", out)
	}
}

func TestRenderStaticSite(t *testing.T) {
	spec := proxySpec()
	spec.Kind, spec.Upstream, spec.Root = "static", "", "/var/www/site"
	out, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "root /var/www/site;") || !strings.Contains(out, "try_files $uri $uri/ =404;") {
		t.Fatalf("static site not rendered:\n%s", out)
	}
	if strings.Contains(out, "proxy_pass") {
		t.Error("a static site should not proxy")
	}
}

// A single-page app's router runs in the browser, so a path with no file of
// its own gets index.html; the switch reads back from the file it wrote, and
// a proxy never takes it from a try_files a hand-written file put beside its
// upstream.
func TestStaticSPAFallbackRoundTrips(t *testing.T) {
	spec := proxySpec()
	spec.Kind, spec.Upstream, spec.Root, spec.SPA = "static", "", "/var/www/app", true
	out, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "try_files $uri $uri/ /index.html;") || strings.Contains(out, "=404") {
		t.Fatalf("the fallback is not rendered:\n%s", out)
	}
	back, _ := ParseSiteSpec("app", out)
	if back.Kind != "static" || !back.SPA {
		t.Fatalf("read back as kind %q, spa %v", back.Kind, back.SPA)
	}
	spec.SPA = false
	out, err = RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	if back, _ := ParseSiteSpec("app", out); back.SPA {
		t.Fatal("a plain folder read back as a single-page app")
	}

	proxy, _ := ParseSiteSpec("mixed", "server {\n listen 80;\n server_name m.example.com;\n location / {\n  try_files $uri /index.html;\n  proxy_pass http://127.0.0.1:3000;\n }\n}\n")
	if proxy.Kind != "proxy" || proxy.SPA {
		t.Fatalf("a proxy read back as kind %q, spa %v", proxy.Kind, proxy.SPA)
	}
}

func TestRenderRedirectSite(t *testing.T) {
	spec := &SiteSpec{
		Name: "old", Kind: "redirect", Domains: []string{"old.example.com"},
		RedirectTo: "https://new.example.com", Permanent: true,
	}
	out, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "return 301 https://new.example.com$request_uri;") {
		t.Fatalf("redirect not rendered:\n%s", out)
	}
}

func TestRenderAccessControls(t *testing.T) {
	spec := proxySpec()
	spec.AllowFrom = []string{"10.0.0.0/8"}
	spec.DenyFrom = []string{"all"}
	spec.BasicAuthFile = "/etc/nginx/.htpasswd"
	spec.BasicAuthRealm = "Staging"
	out, _ := RenderNginx(spec)
	for _, want := range []string{
		"allow 10.0.0.0/8;", "deny all;",
		`auth_basic "Staging";`, "auth_basic_user_file /etc/nginx/.htpasswd;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q from:\n%s", want, out)
		}
	}
	// Order is the whole meaning: nginx stops at the first match, so an allow
	// after a deny all never runs.
	if strings.Index(out, "allow 10.0.0.0/8;") > strings.Index(out, "deny all;") {
		t.Error("allow rules must be rendered before deny all")
	}
}

func TestRenderExtraLocations(t *testing.T) {
	spec := proxySpec()
	spec.Locations = []SiteLocation{{Path: "/api", Upstream: "http://127.0.0.1:4000"}}
	out, _ := RenderNginx(spec)
	if !strings.Contains(out, "location /api {") || !strings.Contains(out, "proxy_pass http://127.0.0.1:4000;") {
		t.Fatalf("extra location missing:\n%s", out)
	}
	// nginx matches the longest prefix regardless of order, but a reader
	// expects the specific one first. Measured inside the TLS server block:
	// the redirect block above it has a catch-all of its own.
	tls := out[strings.LastIndex(out, "server {"):]
	if strings.Index(tls, "location /api {") > strings.Index(tls, "location / {") {
		t.Error("the specific location should be rendered before the catch-all")
	}
}

func TestSpecWarnings(t *testing.T) {
	plain := proxySpec()
	plain.TLS, plain.ForceHTTPS, plain.HSTS = false, false, false
	if len(SpecWarnings(plain)) == 0 {
		t.Error("a plain-HTTP site should warn")
	}

	noRedirect := proxySpec()
	noRedirect.ForceHTTPS = false
	if !containsSubstring(SpecWarnings(noRedirect), "Plain HTTP still serves") {
		t.Error("TLS without a redirect should warn")
	}

	// The fence is written for the operator now rather than demanded of them,
	// so what is left to say is what the list does — including that it has to
	// contain however they reach the site themselves.
	restricted := proxySpec()
	restricted.AllowFrom = []string{"10.0.0.0/8"}
	if !containsSubstring(SpecWarnings(restricted), "Only the listed addresses") {
		t.Error("an allow list should say what it now refuses")
	}

	if len(SpecWarnings(proxySpec())) != 0 {
		t.Errorf("a correct spec warned anyway: %v", SpecWarnings(proxySpec()))
	}
}

func TestIsPublicUpstream(t *testing.T) {
	if isPublicUpstream("http://127.0.0.1:3000") || isPublicUpstream("http://10.0.0.4:8080") {
		t.Error("a local upstream is not public")
	}
	if isPublicUpstream("http://api-container:8080") {
		t.Error("a container name is the common case and should not warn")
	}
	if !isPublicUpstream("http://203.0.113.9:8080") {
		t.Error("a public address should warn")
	}
}

// The form reads a file back. A round trip has to survive, or opening a site
// to change one field would silently drop the rest.
func TestParseSiteSpecRoundTrip(t *testing.T) {
	original := proxySpec()
	original.Locations = []SiteLocation{{Path: "/api", Upstream: "http://127.0.0.1:4000"}}
	out, err := RenderNginx(original)
	if err != nil {
		t.Fatal(err)
	}
	parsed, managed := ParseSiteSpec("app", out)
	if !managed {
		t.Error("a file we wrote should be recognised as ours")
	}
	if parsed.Kind != "proxy" {
		t.Errorf("kind = %q", parsed.Kind)
	}
	if len(parsed.Domains) != 1 || parsed.Domains[0] != "app.example.com" {
		t.Errorf("domains = %v", parsed.Domains)
	}
	if parsed.Upstream != "http://127.0.0.1:3000" {
		t.Errorf("upstream = %q", parsed.Upstream)
	}
	if !parsed.TLS || !parsed.HTTP2 || !parsed.HSTS || !parsed.ForceHTTPS {
		t.Errorf("TLS options lost: %+v", parsed)
	}
	if parsed.CertPath != original.CertPath || parsed.KeyPath != original.KeyPath {
		t.Errorf("certificate paths lost: %q %q", parsed.CertPath, parsed.KeyPath)
	}
	if !parsed.WebSockets || !parsed.Gzip || !parsed.AccessLog || !parsed.SecurityHeaders {
		t.Errorf("switches lost: %+v", parsed)
	}
	if parsed.ClientMaxBody != "50m" || parsed.ProxyTimeout != 60 {
		t.Errorf("limits lost: %q %d", parsed.ClientMaxBody, parsed.ProxyTimeout)
	}
	if len(parsed.Locations) != 1 || parsed.Locations[0].Path != "/api" {
		t.Errorf("locations lost: %+v", parsed.Locations)
	}
	if err := ValidateSpec(parsed); err != nil {
		t.Fatalf("a parsed spec should still be valid: %v", err)
	}
}

// The redirect block this renderer writes carries an ACME challenge location.
// Reading it back as one of the operator's own would emit it twice on the next
// save — once where it works and once where it does nothing.
func TestParseSiteSpecIgnoresTheGeneratedACMELocation(t *testing.T) {
	out, err := RenderNginx(proxySpec())
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := ParseSiteSpec("app", out)
	for _, loc := range parsed.Locations {
		if strings.Contains(loc.Path, "acme-challenge") {
			t.Fatalf("read back the generated challenge location: %+v", parsed.Locations)
		}
	}
}

func TestParseSiteSpecOnAHandWrittenFile(t *testing.T) {
	content := `
server {
    listen 80;
    server_name legacy.example.com;
    location / {
        proxy_pass http://127.0.0.1:9000;
    }
}
`
	spec, managed := ParseSiteSpec("legacy", content)
	if managed {
		t.Error("a hand-written file must not claim to be ours")
	}
	if spec.Upstream != "http://127.0.0.1:9000" || spec.TLS {
		t.Fatalf("got %+v", spec)
	}
}

func TestParseSiteSpecStaticSite(t *testing.T) {
	spec, _ := ParseSiteSpec("site", "server {\n listen 80;\n server_name a.example.com;\n root /var/www/a;\n}\n")
	if spec.Kind != "static" || spec.Root != "/var/www/a" {
		t.Fatalf("got %+v", spec)
	}
}

func containsSubstring(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

// An allow list with nothing after it allows everybody: nginx falls through to
// its default, which is to permit. The operator used to have to know that and
// write the fence into a box labelled "deny from" — and 0.0.0.0/0, the address
// they would reach for, lets in every IPv6 client on the internet.
func TestAllowListIsFencedWithDenyAll(t *testing.T) {
	spec := proxySpec()
	spec.AllowFrom = []string{"10.0.0.0/8", "192.168.1.5"}
	out, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deny all;") {
		t.Fatalf("an allow list with no fence after it:\n%s", out)
	}
	// Order is the whole of nginx's access model: first match wins, so the
	// fence has to come after everything it is fencing.
	if strings.Index(out, "allow 10.0.0.0/8;") > strings.Index(out, "\n    deny all;") {
		t.Error("the fence is above the allow rules, so nothing gets in at all")
	}
}

// A named denial has to beat a range that would otherwise admit it, which
// under first-match means the denials go first.
func TestExplicitDenialsComeBeforeTheAllowList(t *testing.T) {
	spec := proxySpec()
	spec.AllowFrom = []string{"10.0.0.0/8"}
	spec.DenyFrom = []string{"10.0.0.5"}
	out, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	deny, allow := strings.Index(out, "deny 10.0.0.5;"), strings.Index(out, "allow 10.0.0.0/8;")
	if deny < 0 || allow < 0 {
		t.Fatalf("entries missing:\n%s", out)
	}
	if deny > allow {
		t.Error("the range answers first, so the denied address is let in")
	}
}

// A file that already carries the fence reads back and renders again as one
// fence, not two.
func TestDenyAllRoundTripsWithoutDoubling(t *testing.T) {
	spec := proxySpec()
	spec.AllowFrom = []string{"10.0.0.0/8"}
	first, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := ParseSiteSpec(spec.Name, first)
	if !ok {
		t.Fatal("the rendered file did not parse back")
	}
	second, err := RenderNginx(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(second, "\n    deny all;"); n != 1 {
		t.Fatalf("%d fences after a round trip:\n%s", n, second)
	}
}

// Only the site's own access block is fenced. The exploit-shape locations have
// their own deny and are not access control for the site.
func TestNoFenceWithoutAnAllowList(t *testing.T) {
	spec := proxySpec()
	spec.DenyFrom = []string{"203.0.113.0/24"}
	out, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\n    deny all;") {
		t.Fatalf("a blocklist became an allowlist:\n%s", out)
	}
	if !strings.Contains(out, "deny 203.0.113.0/24;") {
		t.Error("the blocklist entry is missing")
	}
}

// A deleted site must not come back as a site called <name>.bak.
//
// The listing showed every file in sites-available, and the delete keeps the
// previous content beside it — so deleting `app` produced `app.bak`, deleting
// that produced `app.bak.bak`, and a host where apt had ever touched a config
// showed a duplicate of every site as .dpkg-old. nginx reads none of these:
// sites-enabled is a directory of symlinks and conf.d is included as *.conf.
func TestBackupsAndEditorLeftoversAreNotSites(t *testing.T) {
	dir := t.TempDir()
	available := filepath.Join(dir, "sites-available")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"app", "app.bak", "app.bak.bak", "app~", "app.dpkg-old", "app.conf.rpmsave", "app.orig",
	} {
		if err := os.WriteFile(filepath.Join(available, name), []byte("server {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hosts := New(dir, filepath.Join(t.TempDir(), "Caddyfile")).nginxVHosts()
	if len(hosts) != 1 || hosts[0].Name != "app" {
		names := []string{}
		for _, h := range hosts {
			names = append(names, h.Name)
		}
		t.Fatalf("expected only app, got %v", names)
	}
}

// The listing says where each nginx site writes, as its page reads it, so the
// logs page can offer the sites that keep a request record of their own
// without reading every site's file a second time.
func TestVHostListingSaysWhereASiteLogs(t *testing.T) {
	dir := t.TempDir()
	available := filepath.Join(dir, "sites-available")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"shop": "server {\n  server_name shop.example.com;\n" +
			"  access_log /var/log/nginx/shop.access.log;\n  error_log /var/log/nginx/shop.error.log warn;\n" +
			"  location / {\n    access_log off;\n  }\n}\n",
		"quiet": "server {\n  server_name quiet.example.com;\n  access_log off;\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(available, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]VHost{}
	for _, h := range New(dir, filepath.Join(t.TempDir(), "Caddyfile")).nginxVHosts() {
		got[h.Name] = h
	}
	if shop := got["shop"]; shop.AccessLogPath != "/var/log/nginx/shop.access.log" ||
		shop.ErrorLogPath != "/var/log/nginx/shop.error.log" {
		t.Errorf("shop: access %q, error %q", shop.AccessLogPath, shop.ErrorLogPath)
	}
	if quiet := got["quiet"]; quiet.AccessLogPath != "" || quiet.ErrorLogPath != "" {
		t.Errorf("a site that logs nothing of its own named %q and %q", quiet.AccessLogPath, quiet.ErrorLogPath)
	}
}

// Deleting a site keeps the previous content, and the copy must not be
// deletable as if it were a site of its own.
func TestDeleteSiteKeepsABackupAndRefusesToBackUpABackup(t *testing.T) {
	dir := t.TempDir()
	available := filepath.Join(dir, "sites-available")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(available, "app"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(dir, filepath.Join(t.TempDir(), "Caddyfile"))
	if err := svc.DeleteSite(context.Background(), "app"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(available, "app.bak")); err != nil {
		t.Fatalf("the previous content was not kept: %v", err)
	}
	if err := svc.DeleteSite(context.Background(), "app.bak"); err == nil {
		t.Fatal("deleting a backup was accepted, which is how .bak.bak happened")
	}
	if _, err := os.Stat(filepath.Join(available, "app.bak.bak")); err == nil {
		t.Fatal("a backup of a backup was written")
	}
}

// Every RPM distribution, Alpine and Arch keep their sites in conf.d and have
// no sites-available at all — most of the servers this runs on. There the
// listing reports a name that already ends in .conf, and both delete and save
// used to append a second one and act on app.conf.conf, which exists nowhere.
func TestConfDLayoutSitesCanBeDeleted(t *testing.T) {
	dir := t.TempDir()
	confd := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confd, "app.conf"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := New(dir, filepath.Join(t.TempDir(), "Caddyfile"))
	hosts := svc.nginxVHosts()
	if len(hosts) != 1 || hosts[0].Name != "app.conf" {
		t.Fatalf("unexpected listing: %+v", hosts)
	}
	if hosts[0].EnabledPath != "" {
		t.Error("conf.d has no symlink to toggle, and an empty EnabledPath is what says so")
	}
	if err := svc.DeleteSite(context.Background(), hosts[0].Name); err != nil {
		t.Fatalf("delete under conf.d: %v", err)
	}
	if _, err := os.Stat(filepath.Join(confd, "app.conf")); err == nil {
		t.Fatal("the file is still there")
	}
	if _, err := os.Stat(filepath.Join(confd, "app.conf.conf")); err == nil {
		t.Fatal("a second suffix was appended")
	}
}

// A file in conf.d that does not end in .conf is not included by nginx, so
// reporting it as enabled is a site the page says is serving and is not.
func TestConfDOnlyCountsDotConfAsActive(t *testing.T) {
	dir := t.TempDir()
	confd := filepath.Join(dir, "conf.d")
	if err := os.MkdirAll(confd, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.conf", "old-site"} {
		if err := os.WriteFile(filepath.Join(confd, name), []byte("server {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, h := range New(dir, filepath.Join(t.TempDir(), "Caddyfile")).nginxVHosts() {
		want := h.Name == "app.conf"
		if h.Enabled != want {
			t.Errorf("%s: enabled=%v, want %v", h.Name, h.Enabled, want)
		}
	}
}

// There is no enable/disable on a conf.d host, and saying which of the two
// layouts this is beats "no such vhost" about a site plainly on the page.
func TestVHostToggleExplainsItselfOnAConfDHost(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := New(dir, filepath.Join(t.TempDir(), "Caddyfile")).
		SetVHostEnabled(context.Background(), "app.conf", false)
	if err == nil || !strings.Contains(err.Error(), "conf.d") {
		t.Fatalf("unhelpful error: %v", err)
	}
}

// The escape hatch was the one field an edit destroyed: extra configuration
// was written into the file and dropped the next time the form read it back,
// so opening a site and pressing save deleted directives nobody was shown.
func TestCustomConfigurationSurvivesARoundTrip(t *testing.T) {
	spec := proxySpec()
	spec.BlockExploits = true
	spec.Custom = "location /metrics {\n    deny all;\n}"
	rendered, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	back, managed := ParseSiteSpec("app", rendered)
	if !managed {
		t.Fatal("the marker was not recognised")
	}
	if back.Custom != spec.Custom {
		t.Fatalf("custom block lost:\ngot  %q\nwant %q", back.Custom, spec.Custom)
	}
	if !back.BlockExploits {
		t.Fatal("the probe blocks were dropped, so saving an edited site removes them")
	}
	again, err := RenderNginx(back)
	if err != nil {
		t.Fatal(err)
	}
	if again != rendered {
		t.Fatalf("second render differs:\n%s", again)
	}
}

// An allow inside a location restricts that path. Hoisting it into the form's
// site-wide list applied it to the whole site on the next save.
func TestLocationAllowIsNotHoistedToTheServer(t *testing.T) {
	spec, _ := ParseSiteSpec("app", `server {
    server_name app.example.com;
    location /admin {
        allow 10.0.0.0/8;
        deny all;
        proxy_pass http://127.0.0.1:3000;
    }
    location / {
        proxy_pass http://127.0.0.1:3000;
    }
}
`)
	if len(spec.AllowFrom) != 0 {
		t.Fatalf("a location's allow became site-wide: %v", spec.AllowFrom)
	}
	if len(spec.DenyFrom) != 0 {
		t.Fatalf("a location's deny became site-wide: %v", spec.DenyFrom)
	}
}

// nginx takes the first matching location and ignores the rest, so a second
// block for the same path is a site serving something other than the form
// shows. The catch-all the renderer always writes counts as one of them.
func TestDuplicateLocationsAreRefused(t *testing.T) {
	spec := proxySpec()
	spec.Locations = []SiteLocation{{Path: "/", Upstream: "http://127.0.0.1:4000"}}
	if err := ValidateSpec(spec); err == nil {
		t.Error("a second location for / was accepted")
	}
	spec.Locations = []SiteLocation{
		{Path: "/api", Upstream: "http://127.0.0.1:4000"},
		{Path: "/api", Upstream: "http://127.0.0.1:5000"},
	}
	if err := ValidateSpec(spec); err == nil {
		t.Error("two locations for /api were accepted")
	}
}
