package proxysvc

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc/acmetest"
)

// requireRFC2136Certbot skips a live test unless the certbot on PATH has the
// dns-rfc2136 plugin acmetest's BIND answers. The distributions package it
// (python3-certbot-dns-rfc2136); a certbot without it can be given the
// plugin's wheel and dnspython's on PYTHONPATH.
func requireRFC2136Certbot(t *testing.T) {
	t.Helper()
	output, err := exec.Command("certbot", "plugins").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "* dns-rfc2136") {
		t.Skip("the certbot here has no dns-rfc2136 plugin (python3-certbot-dns-rfc2136, or its wheel on PYTHONPATH)")
	}
}

// useCertbotDirs points certbot and this package at a test's own
// directories. certbot is run by the code under test, not by the test, so its
// directories are given the one way certbot reads them without an argument:
// the user configuration file under XDG_CONFIG_HOME.
func useCertbotDirs(t *testing.T, dir string) string {
	t.Helper()
	letsencrypt := filepath.Join(dir, "letsencrypt")
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "letsencrypt"), 0o755); err != nil {
		t.Fatal(err)
	}
	ini := fmt.Sprintf("config-dir = %s\nwork-dir = %s\nlogs-dir = %s\n",
		letsencrypt, filepath.Join(dir, "work"), filepath.Join(dir, "logs"))
	if err := os.WriteFile(filepath.Join(xdg, "letsencrypt", "cli.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(dir, "imported")))
	return letsencrypt
}

// What a deployment that serves *.preview.jd-live.test gets from an authority
// that really validates DNS-01: the run proves the wildcard through the RFC
// 2136 credentials saved for the Certificates page, the lineage it leaves
// renews through certbot's own `renew` with the same plugin and file, a
// preview under the wildcard asks for nothing, and nginx serves the wildcard
// on the wildcard's own route and on the preview's, chaining to the root this
// Pebble signed with.
func TestLiveDeploymentWildcardIsProvedOverDNS01AndServed(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for a DNS-01 wildcard against an isolated Pebble and BIND")
	}
	requireRFC2136Certbot(t)
	http2 := liveNginxHasHTTP2Directive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	authority := acmetest.StartDNS01(ctx, t, "jd-live.test")
	letsencrypt := useCertbotDirs(t, t.TempDir())
	t.Setenv("JD_ACME_DIRECTORY", authority.Directory)
	t.Setenv("JD_ACME_CA_ROOT", "")
	t.Setenv("REQUESTS_CA_BUNDLE", authority.Bundle)
	// The zone's nameservers are its own, as a self-hosted BIND's are: no
	// provider's, so the one saved provider answers.
	previous := deploymentNSLookup
	deploymentNSLookup = nameservers(map[string][]string{"jd-live.test": {"ns.jd-live.test"}}, nil)
	t.Cleanup(func() { deploymentNSLookup = previous })
	if _, err := WriteDNSCredentials("rfc2136", authority.Credentials); err != nil {
		t.Fatal(err)
	}
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	transcript := func(stream, line string) { t.Logf("  %s | %s", stream, line) }

	issued, err := service.EnsureDeploymentCertificate(ctx, []string{"*.preview.jd-live.test"}, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Outcome != CertificateIssued || issued.Method != "dns-rfc2136" || !strings.HasPrefix(issued.DNSProvider, "RFC 2136") {
		t.Fatalf("issued = %+v", issued)
	}
	t.Logf("issued: %s", evidenceOf(issued))

	// The lineage is an ordinary one: its renewal configuration names the
	// plugin, the saved file and the authority, and certbot's own renew —
	// what its timer runs — renews it through them.
	conf, err := os.ReadFile(filepath.Join(letsencrypt, "renewal", issued.Name+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"authenticator = dns-rfc2136",
		"dns_rfc2136_credentials = " + credentialsPath("rfc2136"),
		"server = " + authority.Directory,
	} {
		if !strings.Contains(string(conf), want) {
			t.Fatalf("renewal configuration lacks %q:\n%s", want, conf)
		}
	}
	before, err := readLeaf(issued.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	// Without a terminal, renew first sleeps up to eight minutes to spread
	// the authority's load; that is the timer's business, not this test's.
	renew, err := CertbotCommand(ctx, nil, "renew", "--non-interactive", "--no-random-sleep-on-renew",
		"--cert-name", issued.Name, "--force-renewal")
	if err != nil {
		t.Fatal(err)
	}
	output, err := renew.CombinedOutput()
	if err != nil {
		t.Fatalf("certbot renew: %v\n%s", err, output)
	}
	// The record is written again: the authority keeps no authorization to
	// spare the renewal its challenge.
	if !strings.Contains(string(output), "Waiting 60 seconds for DNS changes to propagate") {
		t.Fatalf("certbot renew did not answer a DNS challenge:\n%s", output)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "- -") {
			t.Logf("  renew | %s", line)
		}
	}
	after, err := readLeaf(issued.CertPath)
	if err != nil || after.SerialNumber.Cmp(before.SerialNumber) == 0 {
		t.Fatalf("renewal left serial %x (was %x): %v", after.SerialNumber, before.SerialNumber, err)
	}
	t.Logf("certbot renew replaced serial %x with %x through the lineage's own configuration", before.SerialNumber, after.SerialNumber)

	preview, err := service.EnsureDeploymentCertificate(ctx, []string{"pr-7.preview.jd-live.test"}, transcript)
	if err != nil || preview.Outcome != CertificateReused || preview.Name != issued.Name {
		t.Fatalf("preview = %+v, %v; want the wildcard reused", preview, err)
	}
	t.Logf("preview: %s", evidenceOf(preview))

	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "served %s", r.Host)
	}))
	t.Cleanup(app.Close)
	tlsPort, httpPort := freePort(t), freePort(t)
	for _, route := range []DeploymentRoute{
		{Name: "just-dashboard-env-6.conf", Domains: []string{"*.preview.jd-live.test"}, CertPath: issued.CertPath, KeyPath: issued.KeyPath},
		{Name: "just-dashboard-env-7.conf", Domains: []string{"pr-7.preview.jd-live.test"}, CertPath: preview.CertPath, KeyPath: preview.KeyPath},
	} {
		route.Upstream, route.TLS = app.URL, true
		installTLSRoute(t, root, route, tlsPort, httpPort, http2)
	}
	if result := runValidator(ctx, "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the routes: %s", result.Output)
	}
	startNginx(t, root)

	roots := authority.Roots(t)
	endpoint := fmt.Sprintf("127.0.0.1:%d", tlsPort)
	for _, name := range []string{"pr-7.preview.jd-live.test", "tenant-a.preview.jd-live.test"} {
		var connection *tls.Conn
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			connection, err = tls.Dial("tcp", endpoint, &tls.Config{ServerName: name, RootCAs: roots})
			if err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatalf("handshake for %s: %v", name, err)
		}
		leaf := connection.ConnectionState().PeerCertificates[0]
		connection.Close()
		if leaf.SerialNumber.Cmp(after.SerialNumber) != 0 {
			t.Fatalf("%s was served serial %x, not the renewed %x", name, leaf.SerialNumber, after.SerialNumber)
		}
		t.Logf("TLS %s → subject %q, SANs %v, issuer %q, serial %x, verified against this Pebble's root",
			name, leaf.Subject.CommonName, leaf.DNSNames, leaf.Issuer.CommonName, leaf.SerialNumber)
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, endpoint)
			},
		}}
		response, err := client.Get("https://" + name + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || string(body) != "served "+name {
			t.Fatalf("https://%s/ = %d %q", name, response.StatusCode, body)
		}
	}
}

// evidenceOf is a result as the run's step records it: no paths.
func evidenceOf(result DeploymentCertificate) string {
	result.CertPath, result.KeyPath = "", ""
	raw, _ := json.Marshal(result)
	return string(raw)
}

// installTLSRoute writes a deployment route as activation renders it, on
// loopback ports instead of 80 and 443, which the host's own server holds.
func installTLSRoute(t *testing.T, root string, route DeploymentRoute, tlsPort, httpPort int, http2 bool) {
	t.Helper()
	spec := deploymentSiteSpec(route, "")
	// The route's request record goes under /var/log, which is not this
	// test's to write.
	spec.AccessLog = false
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	for old, replacement := range map[string]string{
		"    listen 443 ssl;\n":      fmt.Sprintf("    listen 127.0.0.1:%d ssl;\n", tlsPort),
		"    listen [::]:443 ssl;\n": "",
		"    listen 80;\n":           fmt.Sprintf("    listen 127.0.0.1:%d;\n", httpPort),
		"    listen [::]:80;\n":      "",
	} {
		content = strings.ReplaceAll(content, old, replacement)
	}
	if !http2 {
		content = strings.ReplaceAll(content, "    http2 on;\n", "")
	}
	available := filepath.Join(root, "sites-available", route.Name)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", route.Name)); err != nil {
		t.Fatal(err)
	}
}
