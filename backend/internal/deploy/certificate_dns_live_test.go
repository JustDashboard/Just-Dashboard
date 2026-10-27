package deploy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc/acmetest"
)

// The certificate step of a run whose plan serves *.preview.jd-live.test,
// against an authority that really validates DNS-01 and the real Certificates
// code: refused, naming where to save a DNS credential, while none is saved;
// proved over DNS-01 once one is; and a preview under the wildcard reusing it
// rather than ordering another. The step's evidence is logged as the run
// records it.
func TestLiveCertificateStepProvesAWildcardOverDNS01(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for a DNS-01 wildcard against an isolated Pebble and BIND")
	}
	if output, err := exec.Command("certbot", "plugins").CombinedOutput(); err != nil || !strings.Contains(string(output), "* dns-rfc2136") {
		t.Skip("the certbot here has no dns-rfc2136 plugin (python3-certbot-dns-rfc2136, or its wheel on PYTHONPATH)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	authority := acmetest.StartDNS01(ctx, t, "jd-live.test")
	dir := t.TempDir()
	letsencrypt := filepath.Join(dir, "letsencrypt")
	xdg := filepath.Join(dir, "xdg")
	if err := os.MkdirAll(filepath.Join(xdg, "letsencrypt"), 0o755); err != nil {
		t.Fatal(err)
	}
	// certbot is run by the Certificates code, not by this test, so its
	// directories go in the user configuration file it reads by itself.
	ini := fmt.Sprintf("config-dir = %s\nwork-dir = %s\nlogs-dir = %s\n",
		letsencrypt, filepath.Join(dir, "work"), filepath.Join(dir, "logs"))
	if err := os.WriteFile(filepath.Join(xdg, "letsencrypt", "cli.ini"), []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Cleanup(proxysvc.UseCertificateDirsForTest(letsencrypt, filepath.Join(dir, "imported")))
	t.Setenv("JD_ACME_DIRECTORY", authority.Directory)
	t.Setenv("JD_ACME_CA_ROOT", "")
	t.Setenv("REQUESTS_CA_BUNDLE", authority.Bundle)

	executor := (&NormalizedStepExecutor{}).WithCertificateIssuer(proxysvc.New(filepath.Join(dir, "nginx"), filepath.Join(dir, "Caddyfile")))
	transcript := func(stream, line string) { t.Logf("  %s | %s", stream, line) }
	step := func(name string) StepResult {
		t.Helper()
		result := executor.certificateStep(ctx, []string{name}, transcript)
		t.Logf("provision_certificate for %s: state=%s code=%s\n  message: %s\n  evidence: %s",
			name, result.State, result.ErrorCode, result.ErrorMessage, result.Evidence)
		return result
	}

	refused := step("*.preview.jd-live.test")
	if refused.State != StepUnavailable || refused.ErrorCode != "certificate_wildcard_unavailable" ||
		!strings.Contains(refused.ErrorMessage, "/proxy/certificates") {
		t.Fatalf("without credentials = %#v", refused)
	}
	if _, err := proxysvc.WriteDNSCredentials("rfc2136", authority.Credentials); err != nil {
		t.Fatal(err)
	}
	issued := step("*.preview.jd-live.test")
	if issued.State != StepPassed || !strings.Contains(string(issued.Evidence), `"outcome":"issued"`) ||
		!strings.Contains(string(issued.Evidence), `"method":"dns-rfc2136"`) {
		t.Fatalf("with credentials = %#v", issued)
	}
	preview := step("pr-7.preview.jd-live.test")
	if preview.State != StepPassed || !strings.Contains(string(preview.Evidence), `"outcome":"reused"`) ||
		!strings.Contains(string(preview.Evidence), `"certificate":"preview.jd-live.test"`) {
		t.Fatalf("preview = %#v", preview)
	}

	// On a host whose deployments the Docker Caddy ingress serves, the step
	// answers the same: the lineage is reused for the wildcard and for a
	// preview under it, where it used to be refused outright. The ingress
	// is a real caddy:2-alpine on loopback, used by name.
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	source := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(source, []byte("{\n  admin 127.0.0.1:2019\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ingress := fmt.Sprintf("jd-step-ingress-%d", time.Now().UnixNano())
	id := docker("run", "-d", "--name", ingress, "-p", "127.0.0.1::80", "-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	onCaddy := (&NormalizedStepExecutor{}).WithCertificateIssuer(proxysvc.NewWithIngressContainer(filepath.Join(dir, "nginx"), source, ingress))
	for _, name := range []string{"*.preview.jd-live.test", "pr-8.preview.jd-live.test"} {
		result := onCaddy.certificateStep(ctx, []string{name}, transcript)
		t.Logf("provision_certificate for %s on the Caddy ingress: state=%s code=%s\n  evidence: %s", name, result.State, result.ErrorCode, result.Evidence)
		if result.State != StepPassed || !strings.Contains(string(result.Evidence), `"outcome":"reused"`) ||
			!strings.Contains(string(result.Evidence), `"certificate":"preview.jd-live.test"`) {
			t.Fatalf("%s on the Caddy ingress = %#v", name, result)
		}
	}
}
