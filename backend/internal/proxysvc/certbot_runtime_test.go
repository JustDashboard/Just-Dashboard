package proxysvc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The plugins a DNS provider needs are read from the certbot that runs the
// jobs. Globbing this process's Python paths reported a plugin installed on
// the host as missing, and the refusal pointed at an install that changed
// nothing.
func TestListDNSProvidersReadsTheRuntimesPlugins(t *testing.T) {
	useLetsencryptDir(t, t.TempDir())
	fakeCertbot(t, "standalone", "webroot", "dns-cloudflare", "dns-route53")
	got, err := New(t.TempDir(), "").ListDNSProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	installed := map[string]bool{}
	seenMissing := false
	for _, p := range got {
		installed[p.Key] = p.Installed
		if p.Credentials == "" {
			t.Errorf("%s does not say what its credentials look like", p.Name)
		}
		// The list is a choice, and the entries that will work are the ones
		// worth reading first.
		if !p.Installed {
			seenMissing = true
		} else if seenMissing {
			t.Error("an installed provider was sorted below a missing one")
		}
	}
	if len(got) != len(dnsProviders) || !installed["cloudflare"] || !installed["route53"] || installed["digitalocean"] {
		t.Fatalf("installed = %v", installed)
	}
}

// With no certbot anywhere nothing is installed, and that is an answer rather
// than an error.
func TestListDNSProvidersWithNoCertbot(t *testing.T) {
	useLetsencryptDir(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	forgetCertbotRuntime()
	got, err := New(t.TempDir(), "").ListDNSProviders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		if p.Installed {
			t.Fatalf("%s reported installed with no certbot", p.Name)
		}
	}
}

// Listing plugins as root takes the lock on certbot's configuration
// directory, and a renewal timer firing in that second fails. The probe
// points certbot at directories of its own.
func TestCertbotRuntimeProbesInItsOwnDirectories(t *testing.T) {
	useLetsencryptDir(t, t.TempDir())
	log := fakeCertbot(t, "webroot")
	if _, err := loadCertbotRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	probe := filepath.Join(os.TempDir(), "just-dashboard-certbot-probe")
	var plugins string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "plugins") {
			plugins = line
		}
	}
	for _, want := range []string{"--config-dir " + probe, "--work-dir " + probe + "/work", "--logs-dir " + probe + "/logs"} {
		if !strings.Contains(plugins, want) {
			t.Fatalf("plugins ran as %q, missing %q", plugins, want)
		}
	}
	// And the answer is kept: a second look runs nothing.
	before := strings.Count(string(raw), "\n")
	if _, err := loadCertbotRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(log)
	if strings.Count(string(after), "\n") != before {
		t.Fatal("the runtime was probed again inside its cache window")
	}
}

// A job runs the certbot the page read: the same binary, with the
// environment it was given.
func TestCertbotCommandRunsTheRuntimesCertbot(t *testing.T) {
	useLetsencryptDir(t, t.TempDir())
	fakeCertbot(t, "webroot")
	cmd, err := CertbotCommand(context.Background(), []string{"AWS_PROFILE=default"}, "--version")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "certbot 9.9.9" {
		t.Fatalf("ran %v: %q %v", cmd.Args, out, err)
	}
	if !strings.Contains(strings.Join(cmd.Env, "\n"), "AWS_PROFILE=default") {
		t.Fatal("the environment was not passed")
	}

	t.Setenv("PATH", t.TempDir())
	forgetCertbotRuntime()
	if _, err := CertbotCommand(context.Background(), nil, "renew"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("no certbot gave %v", err)
	}
}

// On this machine the host's certbot is what the timer runs; the page reports
// that one, and what it can do.
func TestLiveCertbotRuntimeIsTheHostsCertbot(t *testing.T) {
	path, err := exec.LookPath("certbot")
	if err != nil {
		t.Skip("certbot is not installed here")
	}
	forgetCertbotRuntime()
	t.Cleanup(forgetCertbotRuntime)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rt, err := loadCertbotRuntime(ctx)
	if err != nil || rt == nil {
		t.Fatalf("runtime = %+v, %v", rt, err)
	}
	want, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if rt.version != strings.TrimSpace(string(want)) {
		t.Fatalf("reported %q, %s says %q", rt.version, path, want)
	}
	if !rt.authenticators["standalone"] || !rt.authenticators["webroot"] {
		t.Fatalf("authenticators = %v", rt.authenticators)
	}
}
