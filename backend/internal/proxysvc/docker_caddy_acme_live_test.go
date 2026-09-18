package proxysvc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The public-certificate journey against a real ACME authority: an isolated
// Caddy is pointed at a Pebble that validates nothing (so no public port is
// needed), asks it for a certificate for a deployment hostname, the issued
// chain is read back out of Caddy's storage and verified against Pebble's own
// roots, a second ask reuses it, and a route served with it answers HTTPS
// with a chain that verifies the same way. Everything Let's Encrypt would do
// differently is the validation Pebble skipped and the roots browsers trust.
func TestLiveDockerCaddyIssuesThroughAConfiguredACMEDirectory(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for an isolated Caddy and Pebble issuance test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	stamp := time.Now().UnixNano()
	network := fmt.Sprintf("jd-acme-test-%d", stamp)
	docker("network", "create", network)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", network).Run()
	pebble := docker("run", "-d", "--network", network, "--network-alias", "pebble", "-e", "PEBBLE_VA_ALWAYS_VALID=1", "-e", "PEBBLE_VA_NOSLEEP=1",
		"-p", "127.0.0.1::15000", "ghcr.io/letsencrypt/pebble:latest")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", pebble).Run()
	// Two roots to trust: the one behind Pebble's own TLS listener, which
	// Caddy must accept to read the directory, and the one Pebble signs
	// certificates with, which is minted fresh on every start. The daemon
	// can answer "page not found" for a container it created a moment ago,
	// so the first read is retried.
	dir := t.TempDir()
	minica := ""
	for attempt := 0; attempt < 40; attempt++ {
		// The image ships no shell, so the file is copied out rather than read.
		if err := hostexec.Command(ctx, "docker", "cp", pebble+":/test/certs/pebble.minica.pem", filepath.Join(dir, "minica.pem")).Run(); err == nil {
			if raw, err := os.ReadFile(filepath.Join(dir, "minica.pem")); err == nil && strings.Contains(string(raw), "BEGIN CERTIFICATE") {
				minica = string(raw)
				break
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if minica == "" {
		t.Fatalf("Pebble's listener root was not readable: %s", docker("logs", pebble))
	}
	management := "https://" + docker("port", pebble, "15000/tcp")
	insecure := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 5 * time.Second}
	defer insecure.CloseIdleConnections()
	var issuingRoot []byte
	for deadline := time.Now().Add(time.Minute); ; {
		response, err := insecure.Get(management + "/roots/0")
		if err == nil {
			issuingRoot, _ = io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && len(issuingRoot) > 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Pebble did not publish its root: %v", err)
		}
		time.Sleep(time.Second)
	}
	bundle := filepath.Join(dir, "acme-roots.pem")
	if err := os.WriteFile(bundle, []byte(minica+"\n"+string(issuingRoot)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JD_ACME_DIRECTORY", "https://pebble:14000/dir")
	t.Setenv("JD_ACME_CA_ROOT", bundle)
	// Release evidence lands where an ordinary user may write during the test.
	previousImportedDir := importedDir
	importedDir = filepath.Join(dir, "imported")
	t.Cleanup(func() { importedDir = previousImportedDir })

	source := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(source, []byte(managedIngressConfig), 0644); err != nil {
		t.Fatal(err)
	}
	tlsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsEndpoint := tlsListener.Addr().String()
	tlsListener.Close()
	id := docker("run", "-d", "--network", network, "-p", "127.0.0.1::80", "-p", tlsEndpoint+":443", "-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	edge := &dockerCaddy{ID: id, Name: "fixture", Source: source, Identity: "fixture-acme-storage"}
	for i := 0; i < 60; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	service := New(dir, source)
	logged := []string{}
	certificate, err := service.ensureDockerCaddyCertificate(ctx, edge, []string{"acme.example.test"}, func(_, line string) { logged = append(logged, line) })
	if err != nil {
		// The issuance budget may have used the test's own deadline; read the
		// logs on a fresh one so the failure explains itself.
		logCtx, cancelLogs := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelLogs()
		caddyLog, _ := hostexec.Command(logCtx, "docker", "logs", "--tail", "40", id).CombinedOutput()
		pebbleLog, _ := hostexec.Command(logCtx, "docker", "logs", "--tail", "20", pebble).CombinedOutput()
		t.Fatalf("issuance: %v\ncaddy log: %s\npebble log: %s", err, caddyLog, pebbleLog)
	}
	if certificate.Outcome != CertificateIssued || certificate.Method != "caddy" || len(certificate.Domains) != 1 || certificate.Domains[0] != "acme.example.test" {
		t.Fatalf("certificate = %+v", certificate)
	}
	trust := x509.NewCertPool()
	if !trust.AppendCertsFromPEM([]byte(issuingRoot)) {
		t.Fatal("Pebble root did not parse")
	}
	issued, err := os.ReadFile(certificate.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _, err := parseCertChain(string(issued))
	if err != nil {
		t.Fatal(err)
	}
	intermediates := x509.NewCertPool()
	intermediates.AppendCertsFromPEM(issued)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "acme.example.test", Roots: trust, Intermediates: intermediates}); err != nil {
		t.Fatalf("issued chain does not verify against Pebble's root: %v", err)
	}
	if !strings.Contains(strings.Join(logged, "\n"), "Requesting HTTPS through the existing public Caddy container") {
		t.Fatalf("issuance did not narrate: %q", logged)
	}
	// The preparation route is gone once the certificate exists, and a second
	// request reuses what Caddy already holds instead of ordering again.
	if routes := docker("exec", id, "sh", "-c", "ls "+dockerCaddyRoot+"/routes"); strings.Contains(routes, "just-dashboard-certificate-") {
		t.Fatalf("preparation route left behind: %s", routes)
	}
	again, err := service.ensureDockerCaddyCertificate(ctx, edge, []string{"acme.example.test"}, nil)
	if err != nil || again.Outcome != CertificateReused {
		t.Fatalf("second request = %+v, %v", again, err)
	}
	saved, _ := os.ReadFile(source)
	if !strings.HasPrefix(string(saved), managedIngressConfig) || !strings.Contains(string(saved), dockerCaddyImport) {
		t.Fatalf("Caddyfile on disk: %q", saved)
	}

	// A real route served with the issued certificate answers HTTPS with a
	// chain that verifies against the same roots.
	app := docker("run", "-d", "-p", "127.0.0.1::80", "nginx:alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", app).Run()
	upstream := "http://127.0.0.1:" + strings.TrimPrefix(docker("port", app, "80/tcp"), "127.0.0.1:")
	route := DeploymentRoute{Name: "just-dashboard-env-7.conf", Domains: []string{"acme.example.test"}, TLS: true, CertPath: certificate.CertPath, KeyPath: certificate.KeyPath, Upstream: upstream}
	result, err := service.applyDockerCaddyRoute(ctx, edge, route)
	if err != nil || !result.Verified {
		t.Fatalf("apply=%+v error=%v", result, err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: trust, ServerName: "acme.example.test", MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://"+tlsEndpoint, nil)
	request.Host = "acme.example.test"
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("verified TLS handshake with the issued certificate: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "Welcome to nginx") {
		t.Fatalf("route behind the issued certificate answered %d %q", response.StatusCode, body)
	}
}
