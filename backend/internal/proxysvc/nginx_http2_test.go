package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func http2Route() DeploymentRoute {
	return DeploymentRoute{
		Name: "just-dashboard-env-1.conf", Domains: []string{"app.example.test"}, Upstream: "http://127.0.0.1:3000",
		TLS: true, ForceHTTPS: true, CertPath: "/certs/app.pem", KeyPath: "/certs/app.key",
		Redirects: []HostRedirect{{From: "www.app.example.test", To: "app.example.test"}},
	}
}

func serviceWithHTTP2Answer(t *testing.T, legacy bool, asked *int) *Service {
	t.Helper()
	service := New(t.TempDir(), "")
	service.http2.probe = func(context.Context, string) bool {
		if asked != nil {
			*asked++
		}
		return legacy
	}
	return service
}

// A first deployment on Ubuntu 24.04 (nginx 1.24) failed at its cutover:
// every route was written with `http2 on;`, which nginx before 1.25.1 does
// not know, so the whole configuration failed its test. Each HTTPS server
// a route writes — its own, and a redirecting name's — is spelled the way
// the host's nginx reads it.
func TestDeploymentRouteSpellsHTTP2ForHostNginx(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		content, err := RenderNginx(serviceWithHTTP2Answer(t, legacy, nil).deploymentSpec(http2Route()))
		if err != nil {
			t.Fatal(err)
		}
		directive := strings.Count(content, "    http2 on;\n")
		parameter := strings.Count(content, "    listen 443 ssl http2;\n") + strings.Count(content, "    listen [::]:443 ssl http2;\n")
		plain := strings.Count(content, "    listen 443 ssl;\n")
		if legacy && (directive != 0 || plain != 0 || parameter != 4) {
			t.Fatalf("an nginx before 1.25.1 got http2 on %d times, %d plain TLS listens and %d with the parameter:\n%s", directive, plain, parameter, content)
		}
		if !legacy && (directive != 2 || parameter != 0) {
			t.Fatalf("a current nginx got http2 on %d times and the parameter %d times:\n%s", directive, parameter, content)
		}
	}
}

// A site saved on an older nginx reads back as what it is, so renaming it
// re-renders the same bytes and drift finds nothing to report.
func TestListenHTTP2SiteReadsBackUnchanged(t *testing.T) {
	spec := serviceWithHTTP2Answer(t, true, nil).deploymentSpec(http2Route())
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	parsed, managed := ParseSiteSpec(spec.Name, content)
	if !managed || !parsed.HTTP2 || !parsed.listenHTTP2 {
		t.Fatalf("managed=%v http2=%v listenHTTP2=%v", managed, parsed.HTTP2, parsed.listenHTTP2)
	}
	if again, err := rerenderedFor([]byte(content), spec.Name, spec.Name, false); !err || again != content {
		t.Fatalf("re-rendered differently:\n%s", again)
	}
}

// One answer serves every render until it is old: a deployment renders its
// route several times, and asking nginx each time would cost a test apiece.
func TestHTTP2SpellingIsAskedOnce(t *testing.T) {
	asked := 0
	service := serviceWithHTTP2Answer(t, true, &asked)
	for range 3 {
		if !service.nginxNeedsListenHTTP2() {
			t.Fatal("the probe's answer was not kept")
		}
	}
	service.http2.at = time.Now().Add(-2 * http2ProbeTTL)
	service.nginxNeedsListenHTTP2()
	if asked != 2 {
		t.Fatalf("asked nginx %d times, want 2", asked)
	}
}

// The checker's advice to replace `listen … http2` is only right where
// nginx reads the directive it suggests.
func TestLintKeepsListenHTTP2WhereNginxNeedsIt(t *testing.T) {
	tree, err := ParseNginxFile("site", "server {\n    listen 443 ssl http2;\n    server_name a.test;\n}\n", []string{"http"})
	if err != nil {
		t.Fatal(err)
	}
	has := func(findings []LintFinding) bool {
		for _, f := range findings {
			if f.Rule == "listen-http2" {
				return true
			}
		}
		return false
	}
	if !has(serviceWithHTTP2Answer(t, false, nil).LintHost(tree)) {
		t.Fatal("a current nginx was not told the parameter is deprecated")
	}
	if has(serviceWithHTTP2Answer(t, true, nil).LintHost(tree)) {
		t.Fatal("an nginx before 1.25.1 was told to drop the only HTTP/2 spelling it reads")
	}
}

// The real probe against the nginx installed here, and the route it decides
// passing that nginx's own test — whichever version it is. This is the test
// that fails on an Ubuntu 24.04 host if the spelling is ever wrong again.
func TestLiveDeploymentRouteHTTP2PassesHostNginx(t *testing.T) {
	root := liveNginx(t)
	certPEM, keyPEM, _ := selfSigned(t, "app.example.test", time.Now().Add(24*time.Hour))
	route := http2Route()
	route.CertPath, route.KeyPath = filepath.Join(root, "app.pem"), filepath.Join(root, "app.key")
	for path, content := range map[string]string{route.CertPath: certPEM, route.KeyPath: keyPEM} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := New(root, filepath.Join(root, "Caddyfile"))
	spec := service.deploymentSpec(route)
	spec.AccessLog = false
	if err := service.writeMissingPages(spec); err != nil {
		t.Fatal(err)
	}
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	// An unprivileged nginx binds high ports; the HTTPS listens keep the
	// spelling under test.
	plainPort, tlsPort := freePort(t), freePort(t)
	content = strings.ReplaceAll(content, "    listen 80;\n", "    listen 127.0.0.1:"+strconv.Itoa(plainPort)+";\n")
	content = strings.ReplaceAll(content, "    listen 443 ssl", "    listen 127.0.0.1:"+strconv.Itoa(tlsPort)+" ssl")
	for _, v6 := range []string{"    listen [::]:80;\n", "    listen [::]:443 ssl;\n", "    listen [::]:443 ssl http2;\n"} {
		content = strings.ReplaceAll(content, v6, "")
	}
	available := filepath.Join(root, "sites-available", route.Name)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", route.Name)); err != nil {
		t.Fatal(err)
	}
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the route (listen parameter: %v): %s\n%s", spec.listenHTTP2, result.Output, content)
	}
}
