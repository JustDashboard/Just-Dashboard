package proxysvc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The deploy hook that reloads nginx after every renewal: installed once,
// executable, recognised as this dashboard's, told apart from a copy changed
// by hand and from somebody else's file, and removed without touching the
// hooks beside it.
func TestRenewalHookInstallsOnceAndIsRecognised(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	hooks := filepath.Join(dir, "renewal-hooks", "deploy")

	hook, err := RenewalHookStatus()
	if err != nil || hook.State != "missing" || hook.Path != filepath.Join(hooks, renewalHookName) || len(hook.Others) != 0 {
		t.Fatalf("before = %+v, %v", hook, err)
	}
	for range 2 {
		if hook, err = InstallRenewalHook(); err != nil || hook.State != "installed" {
			t.Fatalf("install = %+v, %v", hook, err)
		}
	}
	info, err := os.Stat(hook.Path)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v, %v", info.Mode(), err)
	}
	entries, _ := os.ReadDir(hooks)
	if len(entries) != 1 {
		t.Fatalf("the directory holds %d files; the staged copy was left behind", len(entries))
	}
	if out, err := exec.Command("sh", "-n", hook.Path).CombinedOutput(); err != nil {
		t.Fatalf("sh -n: %v: %s", err, out)
	}

	// Somebody's own hooks beside it are listed, and left alone.
	writeExecutable(t, filepath.Join(hooks, "reload-haproxy"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(hooks, "notes.txt"), "not a hook\n")
	writeExecutable(t, filepath.Join(hooks, "backup~"), "#!/bin/sh\n")
	if hook, _ = RenewalHookStatus(); strings.Join(hook.Others, ",") != "reload-haproxy" {
		t.Fatalf("others = %q", hook.Others)
	}
	// So are the hooks certbot runs once a renewal run is over, and those
	// cli.ini sets for every run: any of them may reload nginx already.
	post := filepath.Join(dir, "renewal-hooks", "post")
	if err := os.MkdirAll(post, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(post, "reload-nginx"), "#!/bin/sh\nsystemctl reload nginx\n")
	writeFile(t, filepath.Join(post, "README"), "not a hook\n")
	writeFile(t, filepath.Join(dir, "cli.ini"), `# Because we are using logrotate for greater flexibility, disable the
# internal certbot logrotation.
max-log-backups = 0
# deploy-hook = commented out
post_hook = systemctl reload nginx
renew-hook = /usr/local/bin/notify
deploy-hook =
`)
	if hook, _ = RenewalHookStatus(); strings.Join(hook.Others, ",") != "reload-haproxy,post/reload-nginx,cli.ini's post-hook,cli.ini's deploy-hook" {
		t.Fatalf("others = %q", hook.Others)
	}
	os.Remove(filepath.Join(post, "reload-nginx"))
	os.Remove(filepath.Join(dir, "cli.ini"))

	// Changed by hand, or no longer executable: certbot may not run what
	// the switch says. Installing again restores it.
	writeFile(t, hook.Path, renewalHookScript+"echo extra\n")
	if hook, _ = RenewalHookStatus(); hook.State != "modified" {
		t.Fatalf("edited = %+v", hook)
	}
	if hook, _ = InstallRenewalHook(); hook.State != "installed" {
		t.Fatalf("restored = %+v", hook)
	}
	if err := os.Chmod(hook.Path, 0o644); err != nil {
		t.Fatal(err)
	}
	if hook, _ = RenewalHookStatus(); hook.State != "modified" {
		t.Fatalf("not executable = %+v", hook)
	}

	if hook, err = RemoveRenewalHook(); err != nil || hook.State != "missing" {
		t.Fatalf("remove = %+v, %v", hook, err)
	}
	if _, err := os.Stat(filepath.Join(hooks, "reload-haproxy")); err != nil {
		t.Fatal("removing the hook removed another")
	}
	if _, err := RemoveRenewalHook(); !errors.Is(err, ErrRenewalHookMissing) {
		t.Fatalf("removing twice = %v", err)
	}

	// A file of somebody else's at the same name is neither replaced nor
	// removed.
	writeExecutable(t, filepath.Join(hooks, renewalHookName), "#!/bin/sh\nmy own\n")
	if hook, _ = RenewalHookStatus(); hook.State != "foreign" {
		t.Fatalf("foreign = %+v", hook)
	}
	if _, err := InstallRenewalHook(); !errors.Is(err, ErrRenewalHookForeign) {
		t.Fatalf("install over foreign = %v", err)
	}
	if _, err := RemoveRenewalHook(); !errors.Is(err, ErrRenewalHookForeign) {
		t.Fatalf("remove foreign = %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(hooks, renewalHookName)); string(raw) != "#!/bin/sh\nmy own\n" {
		t.Fatalf("the foreign file changed: %q", raw)
	}
}

// What the hook does when certbot runs it: tests, then reloads through
// systemd, or signals nginx where systemd does not manage it; a failing test
// reloads nothing and says so on stderr, which certbot logs.
func TestRenewalHookTestsThenReloads(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	hook, err := InstallRenewalHook()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name               string
		nginxTest, systemd int
		want               string
		exit               int
	}{
		{"systemd reloads", 0, 0, "nginx -t -q\nsystemctl reload nginx\n", 0},
		{"no systemd unit", 0, 1, "nginx -t -q\nsystemctl reload nginx\nnginx -s reload\n", 0},
		{"a broken configuration", 1, 0, "nginx -t -q\n", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin := t.TempDir()
			calls := filepath.Join(bin, "calls")
			writeExecutable(t, filepath.Join(bin, "nginx"), "#!/bin/sh\necho \"nginx $*\" >> "+calls+"\n[ \"$1\" = -t ] && exit "+strconv.Itoa(c.nginxTest)+"\nexit 0\n")
			writeExecutable(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\necho \"systemctl $*\" >> "+calls+"\nexit "+strconv.Itoa(c.systemd)+"\n")
			cmd := exec.Command(hook.Path)
			cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "RENEWED_LINEAGE="+filepath.Join(dir, "live", "app.example.com"))
			out, err := cmd.CombinedOutput()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(calls)
			if string(raw) != c.want || code != c.exit {
				t.Fatalf("exit %d, calls:\n%s\noutput: %s", code, raw, out)
			}
			if c.exit != 0 && !strings.Contains(string(out), "nginx was not reloaded for "+filepath.Join(dir, "live", "app.example.com")) {
				t.Fatalf("a refused reload said %q", out)
			}
		})
	}
}

// The sites a renewal reaches through a reload: enabled ones naming a file
// of a renewed lineage, by its live link or its archive version.
func TestSitesServingLineagesFindsEveryPathToTheCertificate(t *testing.T) {
	le := t.TempDir()
	useLetsencryptDir(t, le)
	root := newAuthority(t, "Test Root", nil)
	leaf, _, _ := root.issue(t, []string{"app.example.com"}, time.Now().Add(60*24*time.Hour))
	writeLineage(t, le, "app.example.com", productionACME, leaf)
	writeLineage(t, le, "other.example.com", productionACME, leaf)
	archive := filepath.Join(le, "archive", "app.example.com")
	if err := os.MkdirAll(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(archive, "fullchain3.pem"), certPEM(leaf))

	nginx := t.TempDir()
	for _, sub := range []string{"sites-available", "sites-enabled"} {
		if err := os.MkdirAll(filepath.Join(nginx, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	site := func(name, cert string, enabled bool) {
		available := filepath.Join(nginx, "sites-available", name)
		writeFile(t, available, "server {\n    listen 443 ssl;\n    server_name "+name+";\n    ssl_certificate "+cert+";\n}\n")
		if enabled {
			if err := os.Symlink(available, filepath.Join(nginx, "sites-enabled", name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	site("live", filepath.Join(le, "live", "app.example.com", "fullchain.pem"), true)
	site("cert-only", filepath.Join(le, "live", "app.example.com", "cert.pem"), true)
	site("archive", filepath.Join(archive, "fullchain3.pem"), true)
	site("disabled", filepath.Join(le, "live", "app.example.com", "fullchain.pem"), false)
	site("other", filepath.Join(le, "live", "other.example.com", "fullchain.pem"), true)
	// A lineage whose name is the other's with more on the end.
	site("longer", filepath.Join(le, "live", "app.example.com-0001", "fullchain.pem"), true)

	service := New(nginx, "")
	if got := strings.Join(service.SitesServingLineages([]string{"app.example.com"}), ","); got != "archive,cert-only,live" {
		t.Fatalf("sites = %s", got)
	}
	if got := service.SitesServingLineages(nil); len(got) != 0 {
		t.Fatalf("nothing renewed, but %v", got)
	}
	if got := ChangedLineages(map[string]string{"a": "1", "b": "2", "gone": "3"}, map[string]string{"a": "1", "b": "9", "new": "4"}); strings.Join(got, ",") != "b,new" {
		t.Fatalf("changed = %v", got)
	}
}
