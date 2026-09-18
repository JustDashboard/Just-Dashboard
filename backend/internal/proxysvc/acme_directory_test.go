package proxysvc

import (
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
	service := &Service{}
	args, err := service.IssueArgs(IssueRequest{Domains: []string{"app.example.test"}, Email: "ops@example.com", Method: "standalone"})
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
	args, err = service.IssueArgs(IssueRequest{Domains: []string{"app.example.test"}, Email: "ops@example.com", Method: "standalone"})
	if err != nil || !strings.Contains(strings.Join(args, " "), "--server https://pebble:14000/dir") {
		t.Fatalf("certbot args = %v, %v", args, err)
	}
	args, err = service.IssueArgs(IssueRequest{Domains: []string{"app.example.test"}, Email: "ops@example.com", Method: "standalone", Staging: true})
	if err != nil || strings.Contains(strings.Join(args, " "), "--server") || !strings.Contains(strings.Join(args, " "), "--staging") {
		t.Fatalf("staging certbot args = %v, %v", args, err)
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
