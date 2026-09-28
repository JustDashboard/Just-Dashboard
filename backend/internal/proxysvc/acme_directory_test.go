package proxysvc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestRoot(t *testing.T, dir string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "roots.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A configured directory reaches both issuers: Caddy's managed routes name it
// (with the private roots where there are some) and certbot orders from it,
// while staging keeps certbot's own flag. Nothing configured leaves both
// exactly as they were.
func TestConfiguredACMEDirectoryReachesCaddyAndCertbot(t *testing.T) {
	t.Setenv("JD_ACME_DIRECTORY", "")
	t.Setenv("JD_ACME_CA_ROOT", "")
	plain, err := renderDockerCaddyRoute(DeploymentRoute{Domains: []string{"app.example.test"}, TLS: true}, "")
	if err != nil || strings.Contains(plain, "issuer") {
		t.Fatalf("default route = %q, %v", plain, err)
	}
	useLetsencryptDir(t, t.TempDir())
	service := &Service{}
	args, err := service.IssueArgs(context.Background(), IssueRequest{Domains: []string{"app.example.test"}, Email: "ops@example.com", Method: "standalone"})
	if err != nil || strings.Contains(strings.Join(args, " "), "--server") {
		t.Fatalf("default certbot args = %v, %v", args, err)
	}

	root := writeTestRoot(t, t.TempDir())
	t.Setenv("JD_ACME_DIRECTORY", "https://pebble:14000/dir")
	t.Setenv("JD_ACME_CA_ROOT", root)
	directory := acmeDirectory()
	if err := directory.validate(); err != nil || !directory.configured() || !directory.private() {
		t.Fatalf("directory = %+v, %v", directory, err)
	}
	pool, err := directory.roots()
	if err != nil || pool == nil {
		t.Fatalf("roots = %v, %v", pool, err)
	}
	route, err := renderDockerCaddyRoute(DeploymentRoute{Domains: []string{"app.example.test"}, TLS: true}, "http://10.0.0.5:3000")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  tls {\n    issuer acme {\n      dir https://pebble:14000/dir\n      trusted_roots " + dockerCaddyACMERoot + "\n    }\n  }\n", `reverse_proxy "http://10.0.0.5:3000"`} {
		if !strings.Contains(route, want) {
			t.Fatalf("route missing %q:\n%s", want, route)
		}
	}
	// An imported certificate is served as-is; the issuer block belongs only
	// to routes Caddy issues for.
	http, err := renderDockerCaddyRoute(DeploymentRoute{Domains: []string{"app.example.test"}}, "")
	if err != nil || strings.Contains(http, "issuer") {
		t.Fatalf("plain HTTP route = %q, %v", http, err)
	}
	args, err = service.IssueArgs(context.Background(), IssueRequest{Domains: []string{"app.example.test"}, Email: "ops@example.com", Method: "standalone"})
	if err != nil || !strings.Contains(strings.Join(args, " "), "--server https://pebble:14000/dir") {
		t.Fatalf("certbot args = %v, %v", args, err)
	}
	// A test run rehearses against the authority the real order will use:
	// certbot's --dry-run goes to Let's Encrypt's staging endpoint only when
	// no other server is named.
	args, err = service.IssueArgs(context.Background(), IssueRequest{Domains: []string{"app.example.test"}, Email: "ops@example.com", Method: "standalone", Staging: true})
	if joined := strings.Join(args, " "); err != nil || !strings.Contains(joined, "--dry-run --server https://pebble:14000/dir") || strings.Contains(joined, "--staging") {
		t.Fatalf("test-run certbot args = %v, %v", args, err)
	}

	t.Setenv("JD_ACME_CA_ROOT", "")
	if block := acmeDirectory().caddyIssuer(); strings.Contains(block, "trusted_roots") || !strings.Contains(block, "dir https://pebble:14000/dir") {
		t.Fatalf("public directory issuer = %q", block)
	}
	for name, values := range map[string][2]string{
		"bad url":       {"ftp://pebble", ""},
		"relative root": {"https://pebble:14000/dir", "roots.pem"},
		"missing root":  {"https://pebble:14000/dir", filepath.Join(t.TempDir(), "absent.pem")},
	} {
		t.Setenv("JD_ACME_DIRECTORY", values[0])
		t.Setenv("JD_ACME_CA_ROOT", values[1])
		if err := acmeDirectory().validate(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// Let's Encrypt's own directories in JD_ACME_DIRECTORY are Let's Encrypt.
// Its production one is certbot's default and is left unnamed: certbot swaps
// a --dry-run to staging only when the server is its default spelled
// exactly, so a trailing slash made the test run a real order. Its staging
// one signs test certificates, so a real issuance there replaces no test
// certificate with anything better and is not forced.
func TestLetsEncryptsOwnDirectoryIsLetsEncrypt(t *testing.T) {
	t.Setenv("JD_ACME_CA_ROOT", "")
	for _, c := range []struct {
		directory string
		want      CertbotAuthority
		server    string
	}{
		{"", CertbotAuthority{}, ""},
		{productionACME, CertbotAuthority{}, ""},
		{"https://ACME-v02.api.letsencrypt.org/directory/", CertbotAuthority{}, ""},
		{stagingACME, CertbotAuthority{Staging: true}, "--server " + stagingACME},
		{"https://pebble:14000/dir", CertbotAuthority{Directory: "https://pebble:14000/dir"}, "--server https://pebble:14000/dir"},
		{"https://ca.internal/acme/staging/directory", CertbotAuthority{Directory: "https://ca.internal/acme/staging/directory", Staging: true}, "--server https://ca.internal/acme/staging/directory"},
	} {
		t.Setenv("JD_ACME_DIRECTORY", c.directory)
		if got := CertbotAuthorityInUse(); got != c.want {
			t.Errorf("%q: authority = %+v, want %+v", c.directory, got, c.want)
		}
		if got := strings.Join(acmeDirectory().certbotArgs(), " "); got != c.server {
			t.Errorf("%q: certbot args = %q, want %q", c.directory, got, c.server)
		}
	}

	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	leaf, _, _ := newAuthority(t, "(STAGING) Riddling Rhubarb R12", nil).issue(t, []string{"app.example.test"}, time.Now().Add(80*24*time.Hour))
	writeLineage(t, dir, "app.example.test", stagingACME, leaf)
	service := &Service{}
	issue := func() string {
		t.Helper()
		args, err := service.IssueArgs(context.Background(), IssueRequest{Domains: []string{"app.example.test"}, Email: "ops@example.com", Method: "standalone"})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(args, " ")
	}
	t.Setenv("JD_ACME_DIRECTORY", productionACME+"/")
	if got := issue(); !strings.Contains(got, "--force-renewal") || strings.Contains(got, "--server") {
		t.Fatalf("real issuance from Let's Encrypt over a test certificate = %q", got)
	}
	t.Setenv("JD_ACME_DIRECTORY", stagingACME)
	if got := issue(); strings.Contains(got, "--force-renewal") || !strings.Contains(got, "--server "+stagingACME) {
		t.Fatalf("issuance from the staging directory over a test certificate = %q", got)
	}
}
