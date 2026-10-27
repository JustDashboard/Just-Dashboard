package proxysvc

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A real Caddy serving a copy of a lineage certbot holds, and the lineage
// renewed as certbot renews it — a new generation in the archive, live/
// moved to it: the lifecycle pass moves the route to the renewed certificate
// and Caddy serves its serial, with the route exactly what activation would
// write; the next pass leaves it alone; a route written before its source
// was recorded is found by its copy and refreshed in the same pass; a route
// snapshot restored after a renewal still finds the copy it names while the
// grace holds, and is moved on again by the pass after; and the copies
// nothing serves are the ones removed.
func TestLiveDockerCaddyRefreshesRenewedCertificateCopies(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for an isolated Caddy certificate refresh test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
	previousGrace := certificateCopyGrace
	certificateCopyGrace = 0
	t.Cleanup(func() { certificateCopyGrace = previousGrace })

	source := filepath.Join(root, "Caddyfile")
	if err := os.WriteFile(source, []byte(managedIngressConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	network := fmt.Sprintf("jd-copies-%d", time.Now().UnixNano())
	docker("network", "create", network)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", network).Run()
	tlsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsEndpoint := tlsListener.Addr().String()
	tlsListener.Close()
	id := docker("run", "-d", "--network", network, "-p", "127.0.0.1::80", "-p", tlsEndpoint+":443",
		"-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	app := docker("run", "-d", "--network", network, "-p", "127.0.0.1::80", "caddy:2-alpine", "caddy", "respond", "--listen", ":80", "application answer")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", app).Run()
	edge := &dockerCaddy{ID: id, Name: "fixture", Source: source, Identity: "fixture-copies-storage"}
	for i := 0; i < 60; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	service := New(filepath.Join(root, "nginx"), source)
	upstream := "http://127.0.0.1:" + strings.TrimPrefix(docker("port", app, "80/tcp"), "127.0.0.1:")
	served := func(name string) string {
		t.Helper()
		var connection *tls.Conn
		var err error
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			connection, err = tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", tlsEndpoint,
				&tls.Config{ServerName: name, InsecureSkipVerify: true})
			if err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatalf("handshake for %s: %v", name, err)
		}
		defer connection.Close()
		return connection.ConnectionState().PeerCertificates[0].SerialNumber.Text(16)
	}
	serialOf := func(path string) string {
		t.Helper()
		leaf, err := readLeaf(path)
		if err != nil {
			t.Fatal(err)
		}
		return leaf.SerialNumber.Text(16)
	}
	copies := func() string { return docker("exec", id, "sh", "-c", "ls "+dockerCaddyCertsRoot) }
	repaired := map[string]bool{}
	pass := func() []string {
		t.Helper()
		refreshed, err := edge.reconcile(ctx, func(name string, ok bool) { repaired[name] = ok })
		if err != nil {
			t.Fatalf("pass: %v; %s", err, docker("logs", "--tail", "20", id))
		}
		return refreshed
	}

	const name = "app.jd-live.test"
	certPath, keyPath := lineage(t, letsencrypt, name, 1, []string{name})
	// Recorded, as every activated route is; a route without the log block
	// reads to the pass as one written before recording existed.
	route := DeploymentRoute{Name: "just-dashboard-env-5.conf", Domains: []string{name}, TLS: true, CertPath: certPath, KeyPath: keyPath, Upstream: upstream, AccessLog: true}
	if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
		t.Fatalf("apply = %+v, %v", result, err)
	}
	first := serialOf(certPath)
	if got := served(name); got != first {
		t.Fatalf("Caddy serves %s, the copy is of %s", got, first)
	}
	firstHash, _ := dockerCaddyCertificateCopy(certPath)

	// certbot renews. The pass is the timer's path: no job asked.
	lineage(t, letsencrypt, name, 2, []string{name})
	second := serialOf(certPath)
	if refreshed := pass(); strings.Join(refreshed, ",") != route.Name || !repaired[route.Name] {
		t.Fatalf("refreshed = %v, repaired = %v", refreshed, repaired)
	}
	if got := served(name); got != second {
		t.Fatalf("Caddy serves %s after the pass, the lineage holds %s", got, second)
	}
	want, _, err := edge.routeContent(ctx, route)
	if err != nil {
		t.Fatal(err)
	}
	routePath, _ := dockerCaddyRoutePath(route.Name)
	if content, _, _ := edge.read(ctx, routePath); content != want {
		t.Fatalf("the refreshed route is not what activation writes:\n--- refreshed\n%s\n--- want\n%s", content, want)
	}
	secondHash, _ := dockerCaddyCertificateCopy(certPath)
	if listed := copies(); strings.Contains(listed, firstHash) || !strings.Contains(listed, secondHash+".crt") {
		t.Fatalf("copies after the refresh: %q", listed)
	}
	t.Logf("renewed %s → %s: the pass moved %s to the new copy and removed the old one", first, second, route.Name)
	repaired = map[string]bool{}
	if refreshed := pass(); len(refreshed) != 0 || len(repaired) != 0 {
		t.Fatalf("a pass with nothing renewed refreshed %v, repaired %v", refreshed, repaired)
	}

	// A route written before its source was recorded: the pass finds the
	// lineage by the copy's hash and refreshes it the same way.
	const legacyName = "legacy.jd-live.test"
	legacyCert, legacyKey := lineage(t, letsencrypt, legacyName, 1, []string{legacyName})
	legacy := DeploymentRoute{Name: "just-dashboard-env-9.conf", Domains: []string{legacyName}, TLS: true, CertPath: legacyCert, KeyPath: legacyKey, Upstream: upstream, AccessLog: true}
	content, _, err := edge.routeContent(ctx, legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacySource, _ := certificateSourceFor(legacy)
	legacyContent := strings.Replace(content, legacySource.metadata(), "", 1)
	if _, ok := parseDockerCaddyCertificate(legacyContent); ok || legacyContent == content {
		t.Fatal("the fixture must be a route without a recorded source")
	}
	legacyPath, _ := dockerCaddyRoutePath(legacy.Name)
	raw, _ := os.ReadFile(legacyCert)
	rawKey, _ := os.ReadFile(legacyKey)
	legacyCrt, legacyKeyCopy := certificateCopyPaths(certificateCopyHash(raw))
	for path, bytes := range map[string]string{legacyCrt: string(raw), legacyKeyCopy: string(rawKey), legacyPath: legacyContent} {
		if err := edge.write(ctx, path, bytes); err != nil {
			t.Fatal(err)
		}
	}
	if err := edge.reload(ctx); err != nil {
		t.Fatal(err)
	}
	legacyFirst := serialOf(legacyCert)
	if got := served(legacyName); got != legacyFirst {
		t.Fatalf("the legacy route serves %s, want %s", got, legacyFirst)
	}
	lineage(t, letsencrypt, legacyName, 2, []string{legacyName})
	legacySecond := serialOf(legacyCert)
	if refreshed := pass(); strings.Join(refreshed, ",") != legacy.Name {
		t.Fatalf("refreshed = %v", refreshed)
	}
	if got := served(legacyName); got != legacySecond {
		t.Fatalf("the legacy route serves %s after the pass, the lineage holds %s", got, legacySecond)
	}
	if want, _, _ := edge.routeContent(ctx, legacy); func() string { c, _, _ := edge.read(ctx, legacyPath); return c }() != want {
		t.Fatalf("the upgraded route is not what activation writes now")
	}
	t.Logf("a route without a recorded source was found through the lineage's archive and moved %s → %s", legacyFirst, legacySecond)

	// A snapshot taken before a renewal — what a failed activation restores
	// — still finds the copy it names while the grace holds; the pass after
	// moves the restored route on again, and the copy goes once nothing
	// names it.
	certificateCopyGrace = time.Hour
	snapshot, err := edge.snapshot(ctx, route.Name)
	if err != nil {
		t.Fatal(err)
	}
	lineage(t, letsencrypt, name, 3, []string{name})
	third := serialOf(certPath)
	if refreshed := pass(); strings.Join(refreshed, ",") != route.Name {
		t.Fatalf("refreshed = %v", refreshed)
	}
	if got := served(name); got != third {
		t.Fatalf("Caddy serves %s, the lineage holds %s", got, third)
	}
	if listed := copies(); !strings.Contains(listed, secondHash+".crt") {
		t.Fatalf("the copy a snapshot names was pruned within the grace: %q", listed)
	}
	if err := edge.restore(ctx, snapshot); err != nil {
		t.Fatalf("restoring the snapshot taken before the renewal: %v", err)
	}
	if got := served(name); got != second {
		t.Fatalf("the restored route serves %s, want its own %s", got, second)
	}
	if refreshed := pass(); strings.Join(refreshed, ",") != route.Name || served(name) != third {
		t.Fatalf("the pass after a restore: refreshed %v, served %s", refreshed, served(name))
	}
	certificateCopyGrace = 0
	pass()
	thirdHash, _ := dockerCaddyCertificateCopy(certPath)
	legacyHash, _ := dockerCaddyCertificateCopy(legacyCert)
	if listed := strings.Fields(copies()); len(listed) != 4 || strings.Contains(strings.Join(listed, " "), secondHash) ||
		!strings.Contains(strings.Join(listed, " "), thirdHash+".key") || !strings.Contains(strings.Join(listed, " "), legacyHash+".crt") {
		t.Fatalf("copies at the end: %v", listed)
	}
	t.Logf("copies at the end: %s and %s, one pair each for the two routes", thirdHash[:12], legacyHash[:12])
}
