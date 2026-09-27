package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// siteNginx puts an nginx first on PATH that answers `nginx -t` with the
// given lines and exit code and `nginx -s reload` with its own, and returns
// a Service over a Debian layout in a temporary directory.
func siteNginx(t *testing.T, test string, testExit int, reload string, reloadExit int) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-t\" ]; then printf '%s\\n' \"$JD_TEST_OUT\"; exit $JD_TEST_EXIT; fi\n" +
		"if [ \"$1\" = \"-s\" ]; then printf '%s\\n' \"$JD_TEST_RELOAD_OUT\"; exit $JD_TEST_RELOAD_EXIT; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_TEST_OUT", test)
	t.Setenv("JD_TEST_EXIT", strconv.Itoa(testExit))
	t.Setenv("JD_TEST_RELOAD_OUT", reload)
	t.Setenv("JD_TEST_RELOAD_EXIT", strconv.Itoa(reloadExit))
	return New(root, filepath.Join(root, "Caddyfile")), root
}

const cleanTest = "nginx: the configuration file /etc/nginx/nginx.conf syntax is ok\n" +
	"nginx: configuration file /etc/nginx/nginx.conf test is successful"

func plainSpec(name, domain string) *SiteSpec {
	return &SiteSpec{
		Name: name, Kind: "proxy", Domains: []string{domain},
		Upstream: "http://127.0.0.1:3000", WebSockets: true, Gzip: true,
		AllowFrom: []string{}, DenyFrom: []string{}, Locations: []SiteLocation{},
	}
}

func isLinked(t *testing.T, root, name string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(root, "sites-enabled", name))
	return err == nil
}

// The form used to post enable:true for every save, so opening a disabled
// site to change one field put it back on the internet.
func TestSaveSiteLeavesTheLinkAsItWas(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	ctx := context.Background()
	spec := plainSpec("app", "app.example.com")

	res, err := service.SaveSite(ctx, spec, SiteSave{Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Enabled || !isLinked(t, root, "app") {
		t.Fatalf("a new site asked to be enabled was not: %+v", res)
	}

	if err := os.Remove(filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	spec.Upstream = "http://127.0.0.1:4000"
	res, err = service.SaveSite(ctx, spec, SiteSave{Overwrite: true, Reload: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Enabled || isLinked(t, root, "app") {
		t.Fatalf("saving a disabled site enabled it: %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "sites-available", "app")); !strings.Contains(string(b), "127.0.0.1:4000") {
		t.Fatalf("the edit was not written:\n%s", b)
	}

	// An enabled site stays enabled, and says so: the old result read false
	// whenever the request had not asked for the link.
	if err := os.Symlink(filepath.Join(root, "sites-available", "app"), filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	res, err = service.SaveSite(ctx, spec, SiteSave{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Enabled || !isLinked(t, root, "app") {
		t.Fatalf("saving an enabled site lost its link: %+v", res)
	}
}

// A reload that fails after a clean test is the running process's problem,
// not the file's: the site is saved and in place, and the result says what
// nginx did not do instead of reporting that nothing was applied.
func TestSaveSiteReportsAFailedReloadAsAResult(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0,
		`nginx: [error] open() "/run/nginx.pid" failed (2: No such file or directory)`, 1)
	res, err := service.SaveSite(context.Background(), plainSpec("app", "app.example.com"),
		SiteSave{Enable: true, Reload: true})
	if err != nil {
		t.Fatalf("a clean test with a failed reload was refused: %v", err)
	}
	if res.Reloaded || !strings.Contains(res.ReloadError, "nginx.pid") {
		t.Fatalf("the failed reload is not reported: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "sites-available", "app")); err != nil || !isLinked(t, root, "app") {
		t.Fatal("the saved site was taken back after a reload failure")
	}

	// The deployment cutovers recover on this error, so their form keeps it.
	service.mu.Lock()
	content, _ := RenderNginx(plainSpec("deploy", "deploy.example.com"))
	_, err = service.applySiteLocked(context.Background(), plainSpec("deploy", "deploy.example.com"), content, true, true, false)
	service.mu.Unlock()
	if err == nil || !strings.HasPrefix(err.Error(), "reload failed: ") {
		t.Fatalf("a deployment cutover must still see the reload failure, got %v", err)
	}
}

// nginx exits 0 through a second claim on a name and serves one of the two.
// The save is refused and taken back, naming who holds the name, unless the
// operator says to save anyway.
func TestSaveSiteRefusesANameAnotherSiteServes(t *testing.T) {
	warn := `nginx: [warn] conflicting server name "app.example.com" on 0.0.0.0:80, ignored` + "\n" +
		`nginx: [warn] conflicting server name "app.example.com" on [::]:80, ignored` + "\n" + cleanTest
	service, root := siteNginx(t, warn, 0, "", 0)
	ctx := context.Background()
	legacy := plainSpec("legacy", "app.example.com")
	content, _ := RenderNginx(legacy)
	if err := os.WriteFile(filepath.Join(root, "sites-available", "legacy"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "sites-available", "legacy"), filepath.Join(root, "sites-enabled", "legacy")); err != nil {
		t.Fatal(err)
	}

	res, err := service.SaveSite(ctx, plainSpec("app", "App.Example.com"), SiteSave{Enable: true, Reload: true})
	if !errors.Is(err, ErrServerNameConflict) {
		t.Fatalf("got %v, want a name conflict", err)
	}
	want := []ServerNameConflict{{Domain: "app.example.com", Listen: "0.0.0.0:80", Site: "legacy"}}
	if !reflect.DeepEqual(res.Conflicts, want) {
		t.Fatalf("conflicts = %+v, want %+v", res.Conflicts, want)
	}
	if _, err := os.Stat(filepath.Join(root, "sites-available", "app")); !os.IsNotExist(err) || isLinked(t, root, "app") {
		t.Fatal("a refused save left its file or link behind")
	}
	if got := ConflictSummary(res.Conflicts); got != "app.example.com is already served by legacy on 0.0.0.0:80. nginx answers a name from one server block and ignores the other." {
		t.Fatalf("summary = %q", got)
	}

	res, err = service.SaveSite(ctx, plainSpec("app", "app.example.com"), SiteSave{Enable: true, AllowConflict: true})
	if err != nil {
		t.Fatalf("saving anyway was refused: %v", err)
	}
	if len(res.Conflicts) != 1 || !isLinked(t, root, "app") {
		t.Fatalf("saved anyway without saying so: %+v", res)
	}
}

// Only this site's names on the addresses its file listens on are its
// conflicts: the test reports every warning in the configuration, including
// two other sites fighting over a name on an address this one does not bind.
// A wildcard :80 and 127.0.0.1:80 are separate to nginx, which answers
// 127.0.0.1 from the blocks bound there and never from the wildcard's.
func TestServerNameConflictsAreOnlyThisSites(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	content, _ := RenderNginx(plainSpec("app", "app.example.com"))
	full := filepath.Join(root, "sites-available", "app")
	v := &ValidationResult{Output: strings.Join([]string{
		`nginx: [warn] conflicting server name "other.example.com" on 0.0.0.0:80, ignored`,
		`nginx: [warn] conflicting server name "app.example.com" on 0.0.0.0:8443, ignored`,
		`nginx: [warn] conflicting server name "app.example.com" on 127.0.0.1:80, ignored`,
		`nginx: [warn] conflicting server name "app.example.com" on [::]:80, ignored`,
	}, "\n")}
	v.diagnose("nginx")
	got := service.serverNameConflicts(v, []string{"app.example.com"}, content, full)
	want := []ServerNameConflict{{Domain: "app.example.com", Listen: "[::]:80"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want only the name on the site's own address", got)
	}
	if got := ConflictSummary(got); !strings.HasPrefix(got, "app.example.com is already served by another server block on [::]:80.") {
		t.Fatalf("summary = %q", got)
	}
}

func TestConflictSummaryGroupsTheAddressesOfOneOwner(t *testing.T) {
	got := ConflictSummary([]ServerNameConflict{
		{Domain: "app.example.com", Listen: "0.0.0.0:80", Site: "legacy"},
		{Domain: "app.example.com", Listen: "0.0.0.0:443", Site: "legacy"},
		{Domain: "www.example.com", Listen: "0.0.0.0:80"},
	})
	want := "app.example.com is already served by legacy on 0.0.0.0:80 and 0.0.0.0:443; " +
		"www.example.com is already served by another server block on 0.0.0.0:80. " +
		"nginx answers a name from one server block and ignores the other."
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestListenAddressIsNginxsName(t *testing.T) {
	for value, want := range map[string]string{
		"80": "0.0.0.0:80", "443 ssl": "0.0.0.0:443", "[::]:443 ssl": "[::]:443",
		"*:8080": "0.0.0.0:8080", "127.0.0.1:8080 default_server": "127.0.0.1:8080",
		"127.0.0.1": "127.0.0.1:80", "[::]": "[::]:80",
	} {
		if got := listenAddress(value); got != want {
			t.Errorf("listenAddress(%q) = %q, want %q", value, got, want)
		}
	}
}

// A warning nginx places in this site's file is the site's to report; one in
// another file is not, and saying "saved with warnings" over it is noise.
func TestSaveSiteReportsTheTestsWarningsAboutItsOwnFile(t *testing.T) {
	service, root := siteNginx(t, "", 0, "", 0)
	full := filepath.Join(root, "sites-available", "app")
	t.Setenv("JD_TEST_OUT", strings.Join([]string{
		`nginx: [warn] protocol options redefined for 0.0.0.0:443 in ` + full + `:12`,
		`nginx: [warn] "ssl_stapling" ignored, no OCSP responder URL in /etc/nginx/sites-enabled/other:9`,
		cleanTest,
	}, "\n"))
	res, err := service.SaveSite(context.Background(), plainSpec("app", "app.example.com"), SiteSave{Enable: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []Diagnostic{{Level: "warn", Message: "protocol options redefined for 0.0.0.0:443", File: res.Path, Line: 12}}
	if !reflect.DeepEqual(res.TestWarnings, want) {
		t.Fatalf("testWarnings = %+v, want %+v", res.TestWarnings, want)
	}
}

// On a conf.d host the listing names a site app.conf. Its spec used to keep
// that name, so the next render wrote app.conf.access.log and the site's
// logs moved to a file nothing else reads.
func TestReadSiteSpecNamesAConfDSiteWithoutItsSuffix(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	if err := os.RemoveAll(filepath.Join(root, "sites-available")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := plainSpec("app", "app.example.com")
	spec.AccessLog = true
	if _, err := service.SaveSite(context.Background(), spec, SiteSave{Enable: true}); err != nil {
		t.Fatal(err)
	}
	written := filepath.Join(root, "conf.d", "app.conf")
	first, err := os.ReadFile(written)
	if err != nil {
		t.Fatal(err)
	}

	read, managed, content, err := service.ReadSiteSpec("app.conf")
	if err != nil {
		t.Fatal(err)
	}
	if read.Name != "app" || !managed || content != string(first) {
		t.Fatalf("read back as %q (managed %v)", read.Name, managed)
	}
	if _, err := service.SaveSite(context.Background(), read, SiteSave{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(written)
	if string(again) != string(first) || !strings.Contains(string(again), "/var/log/nginx/app.access.log") {
		t.Fatalf("an unchanged edit rewrote the file:\n%s", again)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "conf.d")); len(entries) != 1 {
		t.Fatalf("saving the edit made a second file: %v", entries)
	}

	if _, _, _, err := service.ReadSiteSpec("../nginx.conf"); err == nil {
		t.Fatal("read a name that is a path")
	}
}
