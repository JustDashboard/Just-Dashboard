package proxysvc

import (
	"context"
	"fmt"
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
// directory, and a renewal timer firing in that second fails, so the probe
// points certbot at directories of its own. They are made for each probe,
// private, and removed after it: one fixed name under the shared /tmp could be
// made first by any local user, with a symlink inside that certbot, as root,
// follows and writes through.
func TestCertbotRuntimeProbesInAPrivateDirectoryOfItsOwn(t *testing.T) {
	useLetsencryptDir(t, t.TempDir())
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	// What another user could have left under the old fixed name.
	victim := t.TempDir()
	planted := filepath.Join(tmp, "just-dashboard-certbot-probe")
	if err := os.Mkdir(planted, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(planted, "logs")); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "argv.log")
	// certbot writes its log into --logs-dir even to list plugins.
	writeExecutable(t, filepath.Join(bin, "certbot"), fmt.Sprintf(`#!/bin/sh
case "$1" in
--version) echo "certbot 9.9.9"; exit 0 ;;
plugins)
  echo "$(stat -c %%a "$3") $*" >> %q
  mkdir -p "$7" && echo written > "$7/letsencrypt.log"
  printf '* webroot\nInterfaces: Authenticator, Plugin\n\n'
  exit 0 ;;
esac
exit 1
`, log))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	forgetCertbotRuntime()
	t.Cleanup(forgetCertbotRuntime)

	for _, fresh := range []bool{true, false, true} {
		if fresh {
			forgetCertbotRuntime()
		}
		rt, err := loadCertbotRuntime(context.Background())
		if err != nil || !rt.authenticators["webroot"] {
			t.Fatalf("runtime = %+v, %v", rt, err)
		}
	}
	raw, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	// The answer is kept: the look inside the cache window ran nothing.
	if len(lines) != 2 {
		t.Fatalf("probed %d times, want 2:\n%s", len(lines), raw)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		// <mode> plugins --config-dir <dir> --work-dir <dir>/work --logs-dir <dir>/logs
		f := strings.Fields(line)
		if len(f) != 8 {
			t.Fatalf("plugins ran as %q", line)
		}
		mode, dir := f[0], f[3]
		if filepath.Dir(dir) != tmp || !strings.HasPrefix(filepath.Base(dir), certbotProbePrefix) || seen[dir] {
			t.Fatalf("probed in %s, want a new directory of its own under %s", dir, tmp)
		}
		seen[dir] = true
		if mode != "700" {
			t.Fatalf("%s had mode %s while certbot ran", dir, mode)
		}
		if f[5] != dir+"/work" || f[7] != dir+"/logs" {
			t.Fatalf("plugins ran as %q", line)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("%s is still there after the probe: %v", dir, err)
		}
	}
	if written, _ := os.ReadDir(victim); len(written) != 0 {
		t.Fatalf("certbot wrote through the planted directory: %v", written)
	}
}

// Without a directory of its own the probe does not run certbot anywhere
// else, and says why.
func TestCertbotRuntimeRefusesToProbeWithoutAPrivateDirectory(t *testing.T) {
	useLetsencryptDir(t, t.TempDir())
	log := fakeCertbot(t, "webroot")
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "mktemp"), `#!/bin/sh
echo "mktemp: failed to create directory via template '$4': No space left on device" >&2
exit 1
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := loadCertbotRuntime(context.Background())
	if err == nil || !strings.Contains(err.Error(), "No space left on device") {
		t.Fatalf("probe without a directory = %v", err)
	}
	if raw, _ := os.ReadFile(log); strings.Contains(string(raw), "plugins") {
		t.Fatalf("certbot ran without a directory of its own:\n%s", raw)
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
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	forgetCertbotRuntime()
	t.Cleanup(forgetCertbotRuntime)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rt, err := loadCertbotRuntime(ctx)
	if err != nil || rt == nil {
		t.Fatalf("runtime = %+v, %v", rt, err)
	}
	// The real certbot ran in a directory of its own, and it is gone.
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("the probe left %v behind", left)
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
