package proxysvc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// The issuance journey against real certbot and a real ACME authority: a
// Pebble that validates nothing (so no public port is needed), and this
// host's certbot run as an ordinary user in directories of the test's own.
//
// It is the proof for the argv IssueArgs builds, because every step of it is
// certbot's decision rather than ours: a test run saves nothing, a real
// issuance saves a lineage, asking again keeps it ("no action taken", exit
// 0, the serial unchanged), and a lineage renewed from a staging authority
// is replaced by the real issuance rather than kept.
func TestLiveCertbotTestRunSavesNothingAndARealIssuanceReplacesAStagingLineage(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for a certbot issuance against an isolated Pebble")
	}
	for _, tool := range []string{"certbot", "docker"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed here", tool)
		}
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
	dir := t.TempDir()
	// One ninety-day profile, as Let's Encrypt issues: the image also offers
	// a six-day one, and a six-day certificate is always inside certbot's
	// thirty-day window — never the "not due" case this test is about.
	pebbleConfig := filepath.Join(dir, "pebble-config.json")
	if err := os.WriteFile(pebbleConfig, []byte(`{"pebble": {
  "listenAddress": "0.0.0.0:14000", "managementListenAddress": "0.0.0.0:15000",
  "certificate": "test/certs/localhost/cert.pem", "privateKey": "test/certs/localhost/key.pem",
  "httpPort": 5002, "tlsPort": 5001, "ocspResponderURL": "", "externalAccountBindingRequired": false,
  "keyAlgorithm": "ecdsa", "retryAfter": {"authz": 3, "order": 5},
  "profiles": {"default": {"description": "ninety days", "validityPeriod": 7776000}}
}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pebble refuses 5% of nonces by default to exercise a client's retry,
	// and certbot retries once: a run of a dozen requests failed now and then
	// on that alone, which is Pebble testing certbot rather than this test.
	pebble := docker("run", "-d", "-e", "PEBBLE_VA_ALWAYS_VALID=1", "-e", "PEBBLE_VA_NOSLEEP=1",
		"-e", "PEBBLE_WFE_NONCEREJECT=0",
		"-v", pebbleConfig+":/test/config/pebble-config.json:ro",
		"-p", "127.0.0.1::14000", "ghcr.io/letsencrypt/pebble:latest")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", pebble).Run()

	// Pebble's directory is served over TLS from its own test root, which
	// certbot has to trust to read it.
	bundle := filepath.Join(dir, "minica.pem")
	for attempt := 0; ; attempt++ {
		if err := hostexec.Command(ctx, "docker", "cp", pebble+":/test/certs/pebble.minica.pem", bundle).Run(); err == nil {
			if raw, _ := os.ReadFile(bundle); strings.Contains(string(raw), "BEGIN CERTIFICATE") {
				break
			}
		}
		if attempt > 40 {
			t.Fatalf("Pebble's listener root was not readable: %s", docker("logs", pebble))
		}
		time.Sleep(250 * time.Millisecond)
	}
	directory := "https://" + docker("port", pebble, "14000/tcp") + "/dir"

	config := filepath.Join(dir, "letsencrypt")
	work := filepath.Join(dir, "work")
	logs := filepath.Join(dir, "logs")
	webroot := filepath.Join(dir, "webroot")
	for _, d := range []string{config, work, logs, webroot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	useLetsencryptDir(t, config)
	t.Setenv("JD_ACME_DIRECTORY", directory)
	t.Setenv("JD_ACME_CA_ROOT", "")
	t.Setenv("REQUESTS_CA_BUNDLE", bundle)

	service := New(t.TempDir(), "")
	issue := func(test bool) (string, []string) {
		t.Helper()
		args, err := service.IssueArgs(ctx, IssueRequest{
			Domains: []string{"app.jd-live.test", "www.jd-live.test"}, Email: "ops@example.com",
			Method: "webroot", WebRoot: webroot, Staging: test,
		})
		if err != nil {
			t.Fatal(err)
		}
		cmd, err := CertbotCommand(ctx, nil, append(args, "--config-dir", config, "--work-dir", work, "--logs-dir", logs)...)
		if err != nil {
			t.Fatal(err)
		}
		// Pebble answers its first requests before it has finished starting.
		for attempt := 0; ; attempt++ {
			out, err := cmd.CombinedOutput()
			if err == nil {
				return string(out), args
			}
			if attempt > 10 || !strings.Contains(string(out), "connect") {
				t.Fatalf("certbot %s: %v\n%s", strings.Join(args, " "), err, out)
			}
			time.Sleep(time.Second)
			cmd, _ = CertbotCommand(ctx, nil, append(args, "--config-dir", config, "--work-dir", work, "--logs-dir", logs)...)
		}
	}

	// A test run goes through the whole exchange and saves nothing.
	out, args := issue(true)
	if !strings.Contains(out, "dry run was successful") || !strings.Contains(strings.Join(args, " "), "--dry-run --server "+directory) {
		t.Fatalf("test run:\n%s", out)
	}
	if confs, err := readRenewalConfs(config); err != nil || len(confs) != 0 {
		t.Fatalf("a test run saved lineages: %+v %v", confs, err)
	}

	// The real issuance saves one.
	issue(false)
	first, err := CertbotSerials()
	if err != nil || len(first) != 1 || first["app.jd-live.test"] == "" {
		t.Fatalf("after the real issuance: %v %v", first, err)
	}

	// Asking again keeps it, and exits 0: the case a job has to explain.
	out, _ = issue(false)
	if again, _ := CertbotSerials(); again["app.jd-live.test"] != first["app.jd-live.test"] || !strings.Contains(out, "no action taken") {
		t.Fatalf("a second issuance replaced a certificate that was not due:\n%s", out)
	}

	// A lineage renewed from a staging authority — what --staging used to
	// leave behind — is replaced by the real issuance, not kept.
	confPath := filepath.Join(config, "renewal", "app.jd-live.test.conf")
	raw, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatal(err)
	}
	staged := regexp.MustCompile(`(?m)^server = .*$`).ReplaceAllString(string(raw), "server = "+stagingACME)
	if err := os.WriteFile(confPath, []byte(staged), 0o644); err != nil {
		t.Fatal(err)
	}
	if lineages, err := readCertbotLineages(config); err != nil || len(lineages) != 1 || !lineages[0].Staging {
		t.Fatalf("a lineage renewed from staging is not flagged: %+v %v", lineages, err)
	}
	out, args = issue(false)
	if !strings.Contains(strings.Join(args, " "), "--force-renewal") {
		t.Fatalf("no --force-renewal over a staging lineage: %q", args)
	}
	replaced, _ := CertbotSerials()
	if replaced["app.jd-live.test"] == first["app.jd-live.test"] {
		t.Fatalf("the staging lineage was kept:\n%s", out)
	}
	if conf, err := readRenewalConf(confPath); err != nil || conf.staging() {
		t.Fatalf("the lineage still renews from staging: %+v %v", conf, err)
	}
	if lineages, err := readCertbotLineages(config); err != nil || len(lineages) != 1 || lineages[0].Staging {
		t.Fatalf("the replaced lineage still reads as a test certificate: %+v %v", lineages, err)
	}
}
