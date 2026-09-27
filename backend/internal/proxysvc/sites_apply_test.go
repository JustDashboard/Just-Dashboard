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
// a Service over a Debian layout in a temporary directory. Its `nginx -T`
// prints what nginx would: nginx.conf, then every file in sites-enabled in
// sorted order, which nginx.conf includes inside http.
func siteNginx(t *testing.T, test string, testExit int, reload string, reloadExit int) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conf := "events {}\nhttp {\n    include " + filepath.Join(root, "sites-enabled") + "/*;\n}\n"
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"-t\" ]; then printf '%s\\n' \"$JD_TEST_OUT\"; exit $JD_TEST_EXIT; fi\n" +
		"if [ \"$1\" = \"-T\" ]; then printf '%s\\n' \"$JD_TEST_OUT\" >&2\n" +
		"  for f in '" + root + "/nginx.conf' '" + root + "'/sites-enabled/*; do\n" +
		"    [ -e \"$f\" ] || continue; printf '# configuration file %s:\\n' \"$f\"; cat \"$f\"; printf '\\n'\n" +
		"  done; exit $JD_TEST_EXIT; fi\n" +
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

// conflictWarning is what nginx -t says about a second claim on name on :80.
func conflictWarning(name string) string {
	return `nginx: [warn] conflicting server name "` + name + `" on 0.0.0.0:80, ignored` + "\n" +
		`nginx: [warn] conflicting server name "` + name + `" on [::]:80, ignored` + "\n" + cleanTest
}

func enableSite(t *testing.T, root string, spec *SiteSpec) {
	t.Helper()
	content, _ := RenderNginx(spec)
	available := filepath.Join(root, "sites-available", spec.Name)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", spec.Name)); err != nil {
		t.Fatal(err)
	}
}

// nginx exits 0 through a second claim on a name and serves the first block
// it reads. A new site that sorts before the one holding the name takes it,
// and the refusal says so rather than that the holder goes on serving it.
func TestSaveSiteRefusesToTakeANameFromTheSiteServingIt(t *testing.T) {
	service, root := siteNginx(t, conflictWarning("app.example.com"), 0, "", 0)
	ctx := context.Background()
	enableSite(t, root, plainSpec("legacy", "app.example.com"))

	res, err := service.SaveSite(ctx, plainSpec("app", "App.Example.com"), SiteSave{Enable: true, Reload: true})
	if !errors.Is(err, ErrServerNameConflict) {
		t.Fatalf("got %v, want a name conflict", err)
	}
	want := []ServerNameConflict{{Domain: "app.example.com", Listen: "0.0.0.0:80", Site: "legacy", Effect: ConflictTakes}}
	if !reflect.DeepEqual(res.Conflicts, want) {
		t.Fatalf("conflicts = %+v, want %+v", res.Conflicts, want)
	}
	if _, err := os.Stat(filepath.Join(root, "sites-available", "app")); !os.IsNotExist(err) || isLinked(t, root, "app") {
		t.Fatal("a refused save left its file or link behind")
	}
	if got := ConflictSummary(res.Conflicts); got != "Saving anyway takes app.example.com on 0.0.0.0:80 from legacy, since nginx reads this site first." {
		t.Fatalf("summary = %q", got)
	}

	res, err = service.SaveSite(ctx, plainSpec("app", "app.example.com"), SiteSave{Enable: true, AllowConflict: true})
	if err != nil {
		t.Fatalf("saving anyway was refused: %v", err)
	}
	if !reflect.DeepEqual(res.Conflicts, want) || !isLinked(t, root, "app") {
		t.Fatalf("saved anyway without saying so: %+v", res)
	}
}

// A site that sorts after the holder is the one nginx ignores, and the
// refusal says the holder keeps the name.
func TestSaveSiteRefusesANameItsHolderKeeps(t *testing.T) {
	service, root := siteNginx(t, conflictWarning("app.example.com"), 0, "", 0)
	enableSite(t, root, plainSpec("legacy", "app.example.com"))

	res, err := service.SaveSite(context.Background(), plainSpec("zz", "app.example.com"), SiteSave{Enable: true})
	if !errors.Is(err, ErrServerNameConflict) {
		t.Fatalf("got %v, want a name conflict", err)
	}
	want := []ServerNameConflict{{Domain: "app.example.com", Listen: "0.0.0.0:80", Site: "legacy", Effect: ConflictIgnored}}
	if !reflect.DeepEqual(res.Conflicts, want) {
		t.Fatalf("conflicts = %+v, want %+v", res.Conflicts, want)
	}
	if got := ConflictSummary(res.Conflicts); got != "nginx answers app.example.com on 0.0.0.0:80 from legacy, which it reads first, and ignores this site's claim." {
		t.Fatalf("summary = %q", got)
	}
}

// The site nginx answers a name from is not refused an edit because a later
// site claims the same name: the edit changes nothing about who answers it,
// and the other site is the one being ignored. Adding a name it did not
// answer before is taking it, and is refused.
func TestSaveSiteLetsTheSiteAnsweringANameBeEdited(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	ctx := context.Background()
	first := plainSpec("c1", "conf.test")
	if _, err := service.SaveSite(ctx, first, SiteSave{Enable: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JD_TEST_OUT", conflictWarning("conf.test"))
	second := plainSpec("c2", "conf.test")
	second.Domains = []string{"conf.test", "b.test"}
	if _, err := service.SaveSite(ctx, second, SiteSave{Enable: true, AllowConflict: true}); err != nil {
		t.Fatal(err)
	}

	first.ClientMaxBody = "10m"
	res, err := service.SaveSite(ctx, first, SiteSave{Overwrite: true, Reload: true})
	if err != nil {
		t.Fatalf("an edit to the site answering the name was refused: %v", err)
	}
	want := []ServerNameConflict{{Domain: "conf.test", Listen: "0.0.0.0:80", Site: "c2", Effect: ConflictKeeps}}
	if !reflect.DeepEqual(res.Conflicts, want) {
		t.Fatalf("conflicts = %+v, want %+v", res.Conflicts, want)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "sites-available", "c1")); !strings.Contains(string(b), "client_max_body_size 10m;") {
		t.Fatalf("the edit was not written:\n%s", b)
	}

	t.Setenv("JD_TEST_OUT", conflictWarning("b.test"))
	first.Domains = []string{"conf.test", "b.test"}
	res, err = service.SaveSite(ctx, first, SiteSave{Overwrite: true})
	if !errors.Is(err, ErrServerNameConflict) {
		t.Fatalf("taking b.test from c2 was not refused: %v", err)
	}
	if got := ConflictSummary(res.Conflicts); got != "Saving anyway takes b.test on 0.0.0.0:80 from c2, since nginx reads this site first." {
		t.Fatalf("summary = %q", got)
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
	if got := ConflictSummary(got); got != "app.example.com on [::]:80 is also claimed by another server block, and nginx answers it from only one of the two." {
		t.Fatalf("summary = %q", got)
	}
}

func TestConflictSummaryGroupsTheAddressesOfOneOwner(t *testing.T) {
	got := ConflictSummary([]ServerNameConflict{
		{Domain: "app.example.com", Listen: "0.0.0.0:80", Site: "legacy", Effect: ConflictIgnored},
		{Domain: "app.example.com", Listen: "0.0.0.0:443", Site: "legacy", Effect: ConflictIgnored},
		{Domain: "www.example.com", Listen: "0.0.0.0:80", Effect: ConflictTakes},
		{Domain: "old.example.com", Listen: "0.0.0.0:80", Site: "c2", Effect: ConflictKeeps},
	})
	want := "nginx answers app.example.com on 0.0.0.0:80 and 0.0.0.0:443 from legacy, which it reads first, and ignores this site's claim. " +
		"Saving anyway takes www.example.com on 0.0.0.0:80 from another server block, since nginx reads this site first. " +
		"This site goes on answering old.example.com on 0.0.0.0:80; nginx ignores c2's claim to it, as before."
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// The first block nginx reads that claims a name on an address answers it,
// wherever the include put it: a block in nginx.conf after the include of
// sites-enabled comes after every site, and one before it before them.
func TestOrderConflictsReadsTheOrderNginxReadsIn(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	sites := filepath.Join(root, "sites-enabled")
	inline := "server {\n    listen 80;\n    server_name inline.test;\n}\n"
	for _, tc := range []struct {
		conf   string
		effect string
		site   string
	}{
		{"events {}\nhttp {\n    include " + sites + "/*;\n" + inline + "}\n", ConflictTakes, ""},
		{"events {}\nhttp {\n" + inline + "    include " + sites + "/*;\n}\n", ConflictIgnored, ""},
	} {
		if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(tc.conf), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("JD_TEST_OUT", conflictWarning("inline.test"))
		res, err := service.SaveSite(context.Background(), plainSpec("app", "inline.test"), SiteSave{Enable: true})
		if !errors.Is(err, ErrServerNameConflict) {
			t.Fatalf("got %v, want a name conflict", err)
		}
		want := []ServerNameConflict{{Domain: "inline.test", Listen: "0.0.0.0:80", Site: tc.site, Effect: tc.effect}}
		if !reflect.DeepEqual(res.Conflicts, want) {
			t.Fatalf("%s: conflicts = %+v, want %+v", tc.effect, res.Conflicts, want)
		}
	}
}

func TestClaimsNameAsNginxMatchesIt(t *testing.T) {
	block := func(body string) Directive {
		parsed, err := ParseNginxFile("/x", "server {\n"+body+"}\n", []string{"http"})
		if err != nil {
			t.Fatal(err)
		}
		return parsed[0]
	}
	for _, tc := range []struct {
		body, name, address string
		want                bool
	}{
		{"listen 80; server_name App.Test;", "app.test", "0.0.0.0:80", true},
		{"listen 80; server_name app.test;", "app.test", "[::]:80", false},
		{"listen [::]:80; server_name app.test;", "app.test", "[::]:80", true},
		{"listen 127.0.0.1:8080; server_name app.test;", "app.test", "0.0.0.0:8080", false},
		{"server_name app.test;", "app.test", "0.0.0.0:80", true},
		{"listen 80; server_name .app.test;", "app.test", "0.0.0.0:80", true},
		{"listen 80; server_name .app.test;", "*.app.test", "0.0.0.0:80", true},
		{"listen 80; server_name www.app.test;", "app.test", "0.0.0.0:80", false},
	} {
		if got := claimsName(block(tc.body), tc.name, tc.address); got != tc.want {
			t.Errorf("%q claims %s on %s = %v, want %v", tc.body, tc.name, tc.address, got, tc.want)
		}
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
