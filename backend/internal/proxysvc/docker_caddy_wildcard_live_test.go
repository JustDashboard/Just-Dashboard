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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc/acmetest"
)

// What a deployment that serves *.preview.jd-live.test gets on a host whose
// deployments the Docker Caddy ingress serves, from an authority that really
// validates DNS-01: the run proves the wildcard (and the zone's apex) through
// certbot and the RFC 2136 credentials saved for the Certificates page, as
// on nginx; the route is a wildcard site served from an explicit copy of
// that certificate, so Caddy's automatic HTTPS orders nothing; a preview
// under the wildcard reuses the lineage and lives beside the wildcard's
// route; and when certbot renews the lineage, the renewal job's refresh moves
// every route to the renewed certificate without a deploy, pruning the copy
// nothing serves any more.
func TestLiveDockerCaddyServesAWildcardProvedOverDNS01(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for a DNS-01 wildcard served by an isolated Caddy ingress")
	}
	requireRFC2136Certbot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	authority := acmetest.StartDNS01(ctx, t, "jd-live.test")
	dir := t.TempDir()
	useCertbotDirs(t, dir)
	t.Setenv("JD_ACME_DIRECTORY", authority.Directory)
	t.Setenv("JD_ACME_CA_ROOT", "")
	t.Setenv("REQUESTS_CA_BUNDLE", authority.Bundle)
	previous := deploymentNSLookup
	deploymentNSLookup = nameservers(map[string][]string{"jd-live.test": {"ns.jd-live.test"}}, nil)
	t.Cleanup(func() { deploymentNSLookup = previous })
	if _, err := WriteDNSCredentials("rfc2136", authority.Credentials); err != nil {
		t.Fatal(err)
	}
	previousGrace := certificateCopyGrace
	certificateCopyGrace = 0
	t.Cleanup(func() { certificateCopyGrace = previousGrace })

	// The ingress: a caddy:2-alpine published on loopback and used by name,
	// as an operator's single-address Caddy or an acceptance stack is.
	stamp := time.Now().UnixNano()
	network := fmt.Sprintf("jd-wildcard-%d", stamp)
	docker("network", "create", network)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", network).Run()
	source := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(source, []byte(managedIngressConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	tlsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsEndpoint := tlsListener.Addr().String()
	tlsListener.Close()
	ingress := fmt.Sprintf("jd-wildcard-ingress-%d", stamp)
	id := docker("run", "-d", "--name", ingress, "--network", network, "-p", "127.0.0.1::80", "-p", tlsEndpoint+":443",
		"-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	service := NewWithIngressContainer(filepath.Join(dir, "nginx"), source, ingress)
	var edge *dockerCaddy
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(250 * time.Millisecond) {
		edge, err = service.dockerCaddy(ctx)
		if err == nil && edge != nil && edge.configurationSynced(ctx) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ingress did not come up: %v; %s", err, docker("logs", id))
		}
	}
	// Two applications, so the preview's own route is told from the
	// wildcard's.
	app := func(answer string) string {
		return docker("run", "-d", "--network", network, "-p", "127.0.0.1::80", "caddy:2-alpine", "caddy", "respond", "--listen", ":80", answer)
	}
	wildcardApp, previewApp := app("wildcard application"), app("preview application")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", wildcardApp).Run()
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", previewApp).Run()
	upstream := func(id string) string { return "http://127.0.0.1:" + strings.TrimPrefix(docker("port", id, "80/tcp"), "127.0.0.1:") }
	transcript := func(stream, line string) { t.Logf("  %s | %s", stream, line) }

	names := []string{"*.preview.jd-live.test", "preview.jd-live.test"}
	issued, err := service.EnsureDeploymentCertificate(ctx, names, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Outcome != CertificateIssued || issued.Method != "dns-rfc2136" || !strings.HasPrefix(issued.DNSProvider, "RFC 2136") {
		t.Fatalf("issued = %+v", issued)
	}
	t.Logf("issued on the Caddy host: %s", evidenceOf(issued))
	// Recorded, as every activated route is: the pass adds the log block to
	// a route without one, which would then not be what activation writes.
	route := DeploymentRoute{Name: "just-dashboard-env-6.conf", Domains: names, TLS: true, CertPath: issued.CertPath, KeyPath: issued.KeyPath, Upstream: upstream(wildcardApp), AccessLog: true}
	if result, err := service.ApplyDeploymentRoute(ctx, route); err != nil || !result.Verified {
		t.Fatalf("wildcard route: %+v, %v; %s", result, err, docker("logs", "--tail", "20", id))
	}
	preview, err := service.EnsureDeploymentCertificate(ctx, []string{"pr-7.preview.jd-live.test"}, transcript)
	if err != nil || preview.Outcome != CertificateReused || preview.Name != issued.Name {
		t.Fatalf("preview = %+v, %v; want the wildcard reused", preview, err)
	}
	previewRoute := DeploymentRoute{Name: "just-dashboard-env-7.conf", Domains: []string{"pr-7.preview.jd-live.test"}, TLS: true, CertPath: preview.CertPath, KeyPath: preview.KeyPath, Upstream: upstream(previewApp), AccessLog: true}
	if result, err := service.ApplyDeploymentRoute(ctx, previewRoute); err != nil || !result.Verified {
		t.Fatalf("preview route beside the wildcard's: %+v, %v", result, err)
	}
	routePath, _ := dockerCaddyRoutePath(route.Name)
	content, _, err := edge.read(ctx, routePath)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := dockerCaddyCertificateCopy(issued.CertPath)
	for _, want := range []string{"*.preview.jd-live.test, preview.jd-live.test {\n" + tlsCopyLine(hash) + "\n", dockerCaddyCertificatePrefix} {
		if !strings.Contains(content, want) {
			t.Fatalf("the wildcard route lacks %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "issuer") {
		t.Fatalf("the wildcard route asks Caddy to issue:\n%s", content)
	}

	roots := authority.Roots(t)
	served := func(name string) *x509.Certificate {
		t.Helper()
		var connection *tls.Conn
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			connection, err = tls.Dial("tcp", tlsEndpoint, &tls.Config{ServerName: name, RootCAs: roots})
			if err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatalf("handshake for %s: %v", name, err)
		}
		defer connection.Close()
		return connection.ConnectionState().PeerCertificates[0]
	}
	answer := func(name string) string {
		t.Helper()
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots},
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, tlsEndpoint)
			},
		}}
		defer client.CloseIdleConnections()
		response, err := client.Get("https://" + name + "/")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		return fmt.Sprintf("%d %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	before, err := readLeaf(issued.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"tenant-a.preview.jd-live.test": "200 wildcard application",
		"preview.jd-live.test":          "200 wildcard application",
		"pr-7.preview.jd-live.test":     "200 preview application",
	}
	for name, want := range expected {
		leaf := served(name)
		if leaf.SerialNumber.Cmp(before.SerialNumber) != 0 {
			t.Fatalf("%s was served serial %x, not the lineage's %x", name, leaf.SerialNumber, before.SerialNumber)
		}
		if got := answer(name); got != want {
			t.Fatalf("https://%s/ = %q, want %q", name, got, want)
		}
		t.Logf("TLS %s → SANs %v, issuer %q, serial %x, verified against this Pebble's root; answered %q",
			name, leaf.DNSNames, leaf.Issuer.CommonName, leaf.SerialNumber, want)
	}

	// certbot renews the lineage on the host, as its timer does; Caddy goes
	// on serving its copy until the dashboard refreshes it.
	renew, err := CertbotCommand(ctx, nil, "renew", "--non-interactive", "--no-random-sleep-on-renew", "--cert-name", issued.Name, "--force-renewal")
	if err != nil {
		t.Fatal(err)
	}
	if output, err := renew.CombinedOutput(); err != nil {
		t.Fatalf("certbot renew: %v\n%s", err, output)
	}
	after, err := readLeaf(issued.CertPath)
	if err != nil || after.SerialNumber.Cmp(before.SerialNumber) == 0 {
		t.Fatalf("renewal left serial %x: %v", before.SerialNumber, err)
	}
	if leaf := served("tenant-a.preview.jd-live.test"); leaf.SerialNumber.Cmp(before.SerialNumber) != 0 {
		t.Fatalf("Caddy changed certificate before being refreshed: %x", leaf.SerialNumber)
	}
	refreshed, err := service.RefreshIngressCertificates(ctx)
	sort.Strings(refreshed)
	if err != nil || strings.Join(refreshed, ",") != "just-dashboard-env-6.conf,just-dashboard-env-7.conf" {
		t.Fatalf("refreshed = %v, %v", refreshed, err)
	}
	for name := range expected {
		if leaf := served(name); leaf.SerialNumber.Cmp(after.SerialNumber) != 0 {
			t.Fatalf("%s is served serial %x after the refresh, not the renewed %x", name, leaf.SerialNumber, after.SerialNumber)
		}
	}
	t.Logf("certbot renew replaced serial %x with %x; the refresh moved %v to it", before.SerialNumber, after.SerialNumber, refreshed)
	// Both routes are what activation would write now, and the copy
	// nothing serves any more is gone.
	for _, value := range []DeploymentRoute{route, previewRoute} {
		if err := service.VerifyDeploymentRoute(ctx, value); err != nil {
			t.Fatalf("%s after the refresh: %v", value.Name, err)
		}
	}
	renewedHash, _ := dockerCaddyCertificateCopy(issued.CertPath)
	copies := docker("exec", id, "ls", dockerCaddyCertsRoot)
	if strings.Contains(copies, hash) || !strings.Contains(copies, renewedHash+".crt") || !strings.Contains(copies, renewedHash+".key") {
		t.Fatalf("copies after the refresh: %q (old %s, renewed %s)", copies, hash, renewedHash)
	}
	if again, err := service.RefreshIngressCertificates(ctx); err != nil || len(again) != 0 {
		t.Fatalf("a refresh with nothing renewed = %v, %v", again, err)
	}
	// Caddy's own storage holds nothing for these names: automatic HTTPS
	// never ordered for the wildcard.
	if stored, _ := edge.command(ctx, "", "sh", "-c", `[ -d /data/caddy/certificates ] && find /data/caddy/certificates -type f -name '*.crt' || true`); strings.Contains(string(stored), "jd-live.test") {
		t.Fatalf("Caddy ordered for the wildcard itself: %s", stored)
	}
}
