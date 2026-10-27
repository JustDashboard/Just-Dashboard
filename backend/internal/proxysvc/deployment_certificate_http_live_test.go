package proxysvc

import (
	"context"
	"crypto/x509"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What a deployment that serves app.jd-live.test gets over HTTP-01 from the
// authority JD_ACME_DIRECTORY names: the run orders from that directory and
// not from Let's Encrypt — with an account of Let's Encrypt's already on
// disk, which used to read as this directory's and left the order refused
// for want of a contact — registers with it, leaves a lineage that renews
// from it, and the certificate chains to the root this Pebble signed with.
// Pebble validates nothing here, so no port 80 is needed: the challenge's
// webroot is a folder of the test's own, written to by the host's certbot.
func TestLiveDeploymentHTTP01OrdersFromTheConfiguredDirectory(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for an HTTP-01 deployment order against an isolated Pebble")
	}
	for _, tool := range []string{"certbot", "docker"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed here", tool)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dir := t.TempDir()
	directory, bundle, management := startPebble(ctx, t, dir)
	letsencrypt := useCertbotDirs(t, dir)
	t.Setenv("JD_ACME_DIRECTORY", directory)
	// The root behind Pebble's listener: certbot is handed it as
	// REQUESTS_CA_BUNDLE, and a private authority's names are not checked
	// against public DNS, which knows nothing of jd-live.test.
	t.Setenv("JD_ACME_CA_ROOT", bundle)
	writeACMEAccount(t, letsencrypt, productionACME)
	webroot := filepath.Join(dir, "webroot")
	if err := os.MkdirAll(webroot, 0o755); err != nil {
		t.Fatal(err)
	}
	service := New(t.TempDir(), "")
	var transcript []string
	logger := func(stream, line string) {
		transcript = append(transcript, line)
		t.Logf("  %s | %s", stream, line)
	}

	names := []string{"app.jd-live.test", "www.jd-live.test"}
	issued, err := service.issueDeploymentHTTP(ctx, names, "webroot", webroot, acmeDirectory(), logger)
	if err != nil {
		t.Fatal(err)
	}
	if issued.Outcome != CertificateIssued || issued.Method != "webroot" || issued.Registered || issued.Name != "app.jd-live.test" {
		t.Fatalf("issued = %+v", issued)
	}
	t.Logf("issued: %s", evidenceOf(issued))
	if !strings.Contains(strings.Join(transcript, "\n"), "Requesting a certificate for app.jd-live.test") {
		t.Fatalf("certbot's own output did not reach the transcript: %q", transcript)
	}

	// The lineage renews from this directory, through the plugin the run
	// chose, and the account the order registered is filed under it.
	conf, err := os.ReadFile(filepath.Join(letsencrypt, "renewal", issued.Name+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"authenticator = webroot", "server = " + directory} {
		if !strings.Contains(string(conf), want) {
			t.Fatalf("renewal configuration lacks %q:\n%s", want, conf)
		}
	}
	if !acmeAccountExists(letsencrypt, directory) {
		t.Fatalf("no account with %s was registered under %s", directory, filepath.Join(letsencrypt, "accounts"))
	}

	// The certificate was signed by this Pebble and covers both names.
	leaf, err := readLeaf(issued.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := os.ReadFile(issued.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	intermediates := x509.NewCertPool()
	intermediates.AppendCertsFromPEM(chain)
	roots := pebbleIssuingRoots(t, management, bundle)
	for _, name := range names {
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots, Intermediates: intermediates}); err != nil {
			t.Fatalf("the issued chain does not verify for %s against this Pebble's root: %v", name, err)
		}
	}
	t.Logf("leaf: subject %q, SANs %v, issuer %q, serial %x, verified against this Pebble's root",
		leaf.Subject.CommonName, leaf.DNSNames, leaf.Issuer.CommonName, leaf.SerialNumber)

	// The next order finds the account the first registered — this
	// directory's, told apart from Let's Encrypt's beside it.
	again, err := service.issueDeploymentHTTP(ctx, []string{"api.jd-live.test"}, "webroot", webroot, acmeDirectory(), logger)
	if err != nil {
		t.Fatal(err)
	}
	if again.Outcome != CertificateIssued || !again.Registered || again.Name != "api.jd-live.test" {
		t.Fatalf("second order = %+v", again)
	}
	t.Logf("second order: %s", evidenceOf(again))
	servers := map[string]int{}
	for _, account := range readACMEAccounts(letsencrypt) {
		servers[account.Server]++
	}
	if len(servers) != 2 || servers[productionACME] != 1 || servers[directory] != 1 {
		t.Fatalf("accounts on disk = %v, want Let's Encrypt's and one with %s", servers, directory)
	}
}
