package proxysvc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeploymentRouteReloadFailureRestoresExactPriorSpec(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "sites-available")
	enabled := filepath.Join(root, "sites-enabled")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(enabled, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "just-dashboard-env-7.conf"
	path := filepath.Join(available, name)
	prior := "# exact prior route\nserver { listen 80; server_name old.example.test; }\n"
	if err := os.WriteFile(path, []byte(prior), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(enabled, name)); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "reload-attempted")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-t\" ]; then exit 0; fi\n" +
		"if [ ! -e \"$JD_TEST_RELOAD_MARKER\" ]; then : > \"$JD_TEST_RELOAD_MARKER\"; exit 1; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_TEST_RELOAD_MARKER", marker)

	service := New(root, filepath.Join(root, "Caddyfile"))
	result, err := service.ApplyDeploymentRoute(context.Background(), DeploymentRoute{
		Name: name, Domains: []string{"new.example.test"}, Upstream: "http://127.0.0.1:32123",
	})
	if err == nil || result.Applied || !result.Recovered {
		t.Fatalf("failed cutover result = %#v, error=%v", result, err)
	}
	if result.Snapshot.Mode != 0o640 || result.Snapshot.LinkTarget != path {
		t.Fatalf("prior route snapshot = %#v", result.Snapshot)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil || string(raw) != prior {
		t.Fatalf("restored bytes = %q, error=%v", raw, readErr)
	}
	if target, linkErr := os.Readlink(filepath.Join(enabled, name)); linkErr != nil || target != path {
		t.Fatalf("restored link = %q, error=%v", target, linkErr)
	}
	if info, statErr := os.Stat(path); statErr != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("restored mode = %v, error=%v", info.Mode().Perm(), statErr)
	}
}

func TestDeploymentRouteRendersExistingTLSCertificateAndRedirect(t *testing.T) {
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"app.example.test"},
		Upstream: "http://127.0.0.1:32123", TLS: true,
		CertPath: "/srv/certs/app/fullchain.pem", KeyPath: "/srv/certs/app/privkey.pem", ForceHTTPS: true,
	}
	content, err := RenderNginx(deploymentSiteSpec(route, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"listen 443 ssl;", "http2 on;", "ssl_certificate     /srv/certs/app/fullchain.pem;",
		"ssl_certificate_key /srv/certs/app/privkey.pem;", "return 301 https://$host$request_uri;",
		"proxy_pass http://127.0.0.1:32123;",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("TLS deployment route is missing %q:\n%s", expected, content)
		}
	}
}

func TestRemoveDeploymentRouteDeletesOnlyNamedRoute(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "sites-available")
	enabled := filepath.Join(root, "sites-enabled")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{available, enabled, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	service := New(root, filepath.Join(root, "Caddyfile"))
	name := "just-dashboard-env-42.conf"
	if _, err := service.ApplyDeploymentRoute(context.Background(), DeploymentRoute{Name: name, Domains: []string{"pr-42.example.test"}, Upstream: "http://127.0.0.1:32123"}); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(available, "foreign.conf")
	if err := os.WriteFile(foreign, []byte("foreign"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveDeploymentRoute(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(available, name)); !os.IsNotExist(err) {
		t.Fatalf("preview route still exists: %v", err)
	}
	if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "foreign" {
		t.Fatalf("foreign route changed: %q %v", raw, err)
	}
}

func TestResolveDeploymentCertificateRequiresOneExistingPairCoveringEveryDomain(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "sites-available")
	if err := os.MkdirAll(available, 0o755); err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := writeDeploymentCertificate(t, root, []string{"app.example.test", "api.example.test"})
	vhost := "server {\n  listen 443 ssl;\n  server_name app.example.test api.example.test;\n" +
		"  ssl_certificate " + certPath + ";\n  ssl_certificate_key " + keyPath + ";\n}\n"
	if err := os.WriteFile(filepath.Join(available, "existing.conf"), []byte(vhost), 0o640); err != nil {
		t.Fatal(err)
	}
	service := New(root, filepath.Join(root, "Caddyfile"))

	resolvedCert, resolvedKey, err := service.ResolveDeploymentCertificate(
		context.Background(), []string{"API.EXAMPLE.TEST", "app.example.test"},
	)
	if err != nil || resolvedCert != certPath || resolvedKey != keyPath {
		t.Fatalf("resolved certificate = %q, %q, error=%v", resolvedCert, resolvedKey, err)
	}
	if _, _, err := service.ResolveDeploymentCertificate(context.Background(), []string{"other.example.test"}); err == nil {
		t.Fatal("certificate resolver accepted a pair that does not cover every requested domain")
	}
}

func writeDeploymentCertificate(t *testing.T, root string, domains []string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domains[0]}, DNSNames: domains,
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(root, "fullchain.pem")
	keyPath := filepath.Join(root, "privkey.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

// A protected route asks nginx for the credentials file the service wrote
// beside its other password files, removes that file again with the route,
// and renders the same credentials inline for Caddy.
func TestDeploymentRoutePasswordProtectionOnBothProxies(t *testing.T) {
	dir := t.TempDir()
	service := New(dir, filepath.Join(dir, "Caddyfile"))
	hash := "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	route := DeploymentRoute{
		Name: "just-dashboard-env-7.conf", Domains: []string{"staging.example.test"},
		Upstream: "http://127.0.0.1:32123", BasicAuth: []BasicAuthUser{{Username: "team", Hash: hash}},
	}
	authFile := service.deploymentAuthFile(route)
	if authFile != filepath.Join(dir, "jd-auth", "just-dashboard-env-7.htpasswd") {
		t.Fatalf("auth file = %q", authFile)
	}
	content, err := RenderNginx(deploymentSiteSpec(route, authFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`auth_basic "Protected deployment";`, "auth_basic_user_file " + authFile + ";"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("protected route is missing %q:\n%s", expected, content)
		}
	}
	if err := service.writeDeploymentAuthFile(route); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(authFile)
	if err != nil || string(written) != "team:"+hash+"\n" {
		t.Fatalf("credentials file = %q, %v", written, err)
	}
	// An unprotected route removes what an earlier protected one wrote.
	open := route
	open.BasicAuth = nil
	if err := service.writeDeploymentAuthFile(open); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(authFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale credentials file survived: %v", err)
	}
	if service.deploymentAuthFile(open) != "" || strings.Contains(mustRender(t, deploymentSiteSpec(open, "")), "auth_basic") {
		t.Fatal("an open route rendered authentication")
	}
	// Malformed credentials never reach a proxy.
	bad := route
	bad.BasicAuth = []BasicAuthUser{{Username: "team", Hash: "plaintext"}}
	if err := service.writeDeploymentAuthFile(bad); err == nil {
		t.Fatal("accepted a non-bcrypt hash for nginx")
	}
	if _, err := renderDockerCaddyRoute(bad, "http://10.0.0.2:3000"); err == nil {
		t.Fatal("accepted a non-bcrypt hash for Caddy")
	}
	caddy, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(caddy, "  basic_auth {\n    team "+hash+"\n  }\n  reverse_proxy") {
		t.Fatalf("Caddy route lacks basic_auth:\n%s", caddy)
	}
}

func mustRender(t *testing.T, spec *SiteSpec) string {
	t.Helper()
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
