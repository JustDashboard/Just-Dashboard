package proxysvc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A wildcard route on the Docker Caddy ingress is a site for the wildcard
// (and the zone's apex when the plan has it) served from an explicit copy of
// the certificate certbot proved, so Caddy's automatic HTTPS never orders
// for it; without such a certificate the wildcard is refused, naming the DNS
// challenge. A plain name's route renders as it did.
func TestDockerCaddyWildcardRouteServesAnExplicitCertificate(t *testing.T) {
	root := t.TempDir()
	certPath, keyPath := writeDeploymentCertificate(t, root, []string{"*.preview.example.test", "preview.example.test"})
	hash, err := dockerCaddyCertificateCopy(certPath)
	if err != nil {
		t.Fatal(err)
	}
	route := DeploymentRoute{
		Name: "just-dashboard-env-6.conf", Domains: []string{"*.preview.example.test", "preview.example.test"},
		TLS: true, CertPath: certPath, KeyPath: keyPath,
	}
	content, err := renderDockerCaddyRoute(route, "http://10.0.0.2:3000")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(content, "# Managed by Just Dashboard\n*.preview.example.test, preview.example.test {\n"+tlsCopyLine(hash)+"\n") {
		t.Fatalf("wildcard site:\n%s", content)
	}
	if strings.Contains(content, "issuer") || strings.Contains(content, "tls {") {
		t.Fatalf("a wildcard route asked Caddy to issue:\n%s", content)
	}
	if got, ok := routeCertificateCopy(content); !ok || got != hash {
		t.Fatalf("copy read back = %q, %v; want %s", got, ok, hash)
	}

	// Nothing to serve it from: refused before Caddy would try the order.
	bare := route
	bare.CertPath, bare.KeyPath = "", ""
	if _, err := renderDockerCaddyRoute(bare, "http://10.0.0.2:3000"); err == nil || !strings.Contains(err.Error(), "DNS challenge") {
		t.Fatalf("a wildcard without a certificate = %v", err)
	}
	// One of Caddy's own release copies is not one either: the route never
	// reads it, Caddy holds that certificate, and it holds no wildcard.
	evidence := route
	evidence.CertPath = filepath.Join(root, "caddy-"+strings.Repeat("a", 24), "fullchain.pem")
	if _, err := renderDockerCaddyRoute(evidence, "http://10.0.0.2:3000"); err == nil {
		t.Fatal("a wildcard over a Caddy release copy was rendered")
	}
	if _, ok := certificateSourceFor(evidence); ok {
		t.Fatal("a Caddy release copy is no source to refresh from")
	}
	// Plain HTTP serves the wildcard as any name.
	plainHTTP, err := renderDockerCaddyRoute(DeploymentRoute{Name: "just-dashboard-env-8.conf", Domains: []string{"*.preview.example.test"}}, "http://10.0.0.2:3000")
	if err != nil || !strings.Contains(plainHTTP, "http://*.preview.example.test {\n") {
		t.Fatalf("HTTP wildcard = %q, %v", plainHTTP, err)
	}

	// A name under the wildcard reuses the lineage and serves the same copy
	// from a site of its own, beside the wildcard's.
	preview := DeploymentRoute{Name: "just-dashboard-env-7.conf", Domains: []string{"pr-7.preview.example.test"}, TLS: true, CertPath: certPath, KeyPath: keyPath}
	previewContent, err := renderDockerCaddyRoute(preview, "http://10.0.0.3:3000")
	if err != nil || !strings.HasPrefix(previewContent, "# Managed by Just Dashboard\npr-7.preview.example.test {\n"+tlsCopyLine(hash)+"\n") {
		t.Fatalf("preview route:\n%s\n%v", previewContent, err)
	}

	// The renderer writes no source; routeContent records it beside the
	// target, and the pass reads it back.
	if _, ok := parseDockerCaddyCertificate(content); ok {
		t.Fatal("the renderer wrote a certificate source")
	}
	source, ok := certificateSourceFor(route)
	if !ok || source.CertPath != certPath || source.KeyPath != keyPath {
		t.Fatalf("source = %+v, %v", source, ok)
	}
	full := content + dockerCaddyTarget{ContainerID: strings.Repeat("b", 64), Network: "jd-net", Port: "3000", Address: "10.0.0.2"}.metadata() + source.metadata()
	if parsed, ok := parseDockerCaddyCertificate(full); !ok || parsed != source {
		t.Fatalf("parsed source = %+v, %v", parsed, ok)
	}
	if _, ok := parseDockerCaddyTarget(full); !ok {
		t.Fatal("the target metadata must survive the certificate line")
	}
}

// lineage lays out a certbot lineage as certbot keeps it: numbered
// generations in archive/ and live/ symlinks to the current one. Each
// generation is a fresh key and a certificate whose serial is its number,
// so what a server serves says which generation it is.
func lineage(t *testing.T, letsencrypt, name string, generation int, domains []string) (string, string) {
	t.Helper()
	archive := filepath.Join(letsencrypt, "archive", name)
	live := filepath.Join(letsencrypt, "live", name)
	for _, dir := range []string{archive, live} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(int64(generation)), Subject: pkix.Name{CommonName: domains[0]}, DNSNames: domains,
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"fullchain": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"privkey":   pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
	for base, raw := range files {
		target := filepath.Join(archive, base+strconv.Itoa(generation)+".pem")
		if err := os.WriteFile(target, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(live, base+".pem")
		os.Remove(link)
		if err := os.Symlink(filepath.Join("..", "..", "archive", name, filepath.Base(target)), link); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(live, "fullchain.pem"), filepath.Join(live, "privkey.pem")
}

// When certbot renews the lineage a route serves, the pass copies the new
// certificate under its own hash and moves every tls line — the route's and
// its redirecting name's — to it, leaving the rest of the route as written;
// the copy the retained snapshot names stays what it was, a retained
// release's route rendered again serves the current certificate, and the
// copies nothing names any more are the ones pruned.
func TestCertificateCopiesAreRefreshedWhenTheLineageRenews(t *testing.T) {
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
	names := []string{"*.preview.example.test", "preview.example.test"}
	certPath, keyPath := lineage(t, letsencrypt, "preview.example.test", 1, names)
	first, _ := dockerCaddyCertificateCopy(certPath)
	route := DeploymentRoute{
		Name: "just-dashboard-env-6.conf", Domains: names, TLS: true, CertPath: certPath, KeyPath: keyPath,
		Redirects: []HostRedirect{{From: "www.preview.example.test", To: "preview.example.test"}},
	}
	target := dockerCaddyTarget{ContainerID: strings.Repeat("c", 64), Network: "jd-net", Port: "3000", Address: "10.0.0.2"}
	rendered, err := renderDockerCaddyRoute(route, target.upstream())
	if err != nil {
		t.Fatal(err)
	}
	source, _ := certificateSourceFor(route)
	before := rendered + target.metadata() + source.metadata()
	if strings.Count(before, tlsCopyLine(first)+"\n") != 2 {
		t.Fatalf("the route and its redirect must share the copy:\n%s", before)
	}
	// Nothing renewed: nothing to do, and the copy is still in use.
	plan, serves := planCertificateRefresh(before)
	if !serves || plan.changed() || plan.hash != first {
		t.Fatalf("plan before renewal = %+v, %v", plan, serves)
	}

	// certbot renews: a new generation in the archive, live/ moved to it.
	lineage(t, letsencrypt, "preview.example.test", 2, names)
	second, _ := dockerCaddyCertificateCopy(certPath)
	if second == first {
		t.Fatal("the fixture's renewal kept the certificate")
	}
	plan, serves = planCertificateRefresh(before)
	if !serves || !plan.changed() || plan.recordSource || plan.served != first || plan.hash != second || plan.cert == nil || plan.key == nil {
		t.Fatalf("plan after renewal = %+v, %v", plan, serves)
	}
	after := plan.apply(before)
	fresh, _ := renderDockerCaddyRoute(route, target.upstream())
	if want := fresh + target.metadata() + source.metadata(); after != want {
		t.Fatalf("a refreshed route must be byte-identical to one activated after the renewal:\n--- refreshed\n%s\n--- want\n%s", after, want)
	}
	if strings.Count(after, tlsCopyLine(second)+"\n") != 2 || strings.Contains(after, first) {
		t.Fatalf("tls lines after the refresh:\n%s", after)
	}
	if again, _ := planCertificateRefresh(after); again.changed() {
		t.Fatalf("refreshing twice must change nothing: %+v", again)
	}
	// The snapshot taken before the renewal still names its own copy, so
	// restoring it serves exactly what it did; the retained release's route
	// value, rendered again by a rollback, serves the current certificate.
	if withCertificateCopy(before, first) != before {
		t.Fatal("a snapshot's copy must be left alone")
	}
	if got, _ := routeCertificateCopy(fresh); got != second {
		t.Fatalf("a rollback's render serves %s, want the renewed %s", got, second)
	}

	// Prune: the old copy once nothing names it, never the one in use or a
	// file that is not a copy.
	referenced := map[string]bool{second: true}
	listed := []string{
		dockerCaddyCertsRoot + "/" + first + ".crt", dockerCaddyCertsRoot + "/" + first + ".key",
		dockerCaddyCertsRoot + "/" + second + ".crt", dockerCaddyCertsRoot + "/" + second + ".key",
		dockerCaddyCertsRoot + "/notes.txt", dockerCaddyRoot + "/routes/" + first + ".crt",
	}
	if stale := staleCertificateCopies(listed, referenced); strings.Join(stale, " ") != dockerCaddyCertsRoot+"/"+first+".crt "+dockerCaddyCertsRoot+"/"+first+".key" {
		t.Fatalf("stale = %v", stale)
	}
}

// A route written before its source was recorded is found by the copy it
// serves: the lineage's current certificate, or an archived generation when
// the copy predates a renewal. The source goes in after the other metadata,
// where activation writes it, and a source outside certbot's live directory
// or the imports is never read from.
func TestCertificateSourceIsFoundForARouteWrittenBeforeRefreshing(t *testing.T) {
	root := t.TempDir()
	letsencrypt := filepath.Join(root, "letsencrypt")
	imported := filepath.Join(root, "imported")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, imported))
	names := []string{"app.example.test"}
	certPath, keyPath := lineage(t, letsencrypt, "app.example.test", 1, names)
	route := DeploymentRoute{Name: "just-dashboard-env-3.conf", Domains: names, TLS: true, CertPath: certPath, KeyPath: keyPath}
	target := dockerCaddyTarget{ContainerID: strings.Repeat("d", 64), Network: "jd-net", Port: "80", Address: "10.0.0.9"}
	rendered, _ := renderDockerCaddyRoute(route, target.upstream())
	legacy := rendered + target.metadata()

	// The copy is the current certificate: the source is recorded, nothing
	// is copied.
	plan, serves := planCertificateRefresh(legacy)
	want := dockerCaddyCertificateSource{CertPath: certPath, KeyPath: keyPath}
	if !serves || !plan.recordSource || plan.source != want || plan.cert != nil {
		t.Fatalf("plan = %+v, %v", plan, serves)
	}
	upgraded := plan.apply(legacy)
	if source, _ := certificateSourceFor(route); upgraded != legacy+source.metadata() {
		t.Fatalf("upgraded:\n%s", upgraded)
	}
	if parsed, ok := parseDockerCaddyCertificate(upgraded); !ok || parsed != want {
		t.Fatalf("parsed = %+v, %v", parsed, ok)
	}

	// The copy predates a renewal: the archive says whose it was, and the
	// renewed certificate is copied in the same pass.
	lineage(t, letsencrypt, "app.example.test", 2, names)
	current, _ := dockerCaddyCertificateCopy(certPath)
	plan, _ = planCertificateRefresh(legacy)
	if !plan.recordSource || plan.source != want || plan.cert == nil || plan.hash != current {
		t.Fatalf("plan from the archive = %+v", plan)
	}
	refreshed := plan.apply(legacy)
	if got, _ := routeCertificateCopy(refreshed); got != current || !strings.Contains(refreshed, want.metadata()) {
		t.Fatalf("refreshed:\n%s", refreshed)
	}

	// A copy of nothing this host holds is left as it is.
	unknown := withCertificateCopy(legacy, strings.Repeat("e", 64))
	if plan, serves := planCertificateRefresh(unknown); !serves || plan.changed() || plan.hash != strings.Repeat("e", 64) {
		t.Fatalf("unknown copy = %+v, %v", plan, serves)
	}
	// A source the metadata points elsewhere is not read.
	elsewhere := legacy + dockerCaddyCertificateSource{CertPath: filepath.Join(root, "fullchain.pem"), KeyPath: filepath.Join(root, "privkey.pem")}.metadata()
	if plan, _ := planCertificateRefresh(elsewhere); plan.changed() {
		t.Fatalf("a source outside the trusted directories was read: %+v", plan)
	}
	for _, source := range []dockerCaddyCertificateSource{
		want,
		{CertPath: filepath.Join(imported, "site", "fullchain.pem"), KeyPath: filepath.Join(imported, "site", "privkey.pem")},
	} {
		if !trustedCertificateSource(source) {
			t.Fatalf("%+v must be trusted", source)
		}
	}
	for _, source := range []dockerCaddyCertificateSource{
		{CertPath: filepath.Join(letsencrypt, "live", "..", "x.pem"), KeyPath: keyPath},
		{CertPath: certPath, KeyPath: "/etc/shadow"},
		{CertPath: "live/app/fullchain.pem", KeyPath: keyPath},
	} {
		if trustedCertificateSource(source) {
			t.Fatalf("%+v must not be trusted", source)
		}
	}
	// A route that serves no copy — Caddy issued for it — plans nothing.
	issued, _ := renderDockerCaddyRoute(DeploymentRoute{Name: "just-dashboard-env-4.conf", Domains: names, TLS: true}, target.upstream())
	if _, serves := planCertificateRefresh(issued + target.metadata()); serves {
		t.Fatal("a route Caddy issues for serves no copy")
	}
}
