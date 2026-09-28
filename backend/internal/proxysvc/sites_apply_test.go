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

// shimPrelude starts a fake nginx the way the real one reads its
// configuration: $conf is the file after -c (the trial copy a disabled site
// is tested in) or nginx.conf, and $include the pattern of its first
// include, in whichever directory that points. Every call is logged to
// bin/calls.log with what the live sites-enabled held at the time.
func shimPrelude(root string) string {
	return "#!/bin/sh\n" +
		"conf='" + root + "/nginx.conf'\n" +
		"if [ \"$2\" = \"-c\" ]; then conf=\"$3\"; fi\n" +
		"include=$(sed -n 's/^ *include \\(.*\\);$/\\1/p' \"$conf\" | head -n 1)\n" +
		"echo \"$* :: $(ls '" + root + "/sites-enabled' 2>/dev/null | tr '\\n' ' ')\" >> '" + root + "/bin/calls.log'\n"
}

// siteNginx puts an nginx first on PATH that answers `nginx -t` with the
// given lines and exit code and `nginx -s reload` with its own, and returns
// a Service over a Debian layout in a temporary directory. Its `nginx -T`
// prints what nginx would: the configuration it was given, then every file
// its include matches in sorted order, which nginx.conf includes inside http.
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
	script := shimPrelude(root) +
		"if [ \"$1\" = \"-t\" ]; then printf '%s\\n' \"$JD_TEST_OUT\"; exit $JD_TEST_EXIT; fi\n" +
		"if [ \"$1\" = \"-T\" ]; then printf '%s\\n' \"$JD_TEST_OUT\" >&2\n" +
		"  for f in \"$conf\" $include; do\n" +
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

func enabledLinkTarget(t *testing.T, root, name string) string {
	t.Helper()
	target, err := os.Readlink(filepath.Join(root, "sites-enabled", name))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

// A hand-written site can be enabled under a link named for its domain
// rather than for its file. A new site derived from that domain used to
// replace the link before nginx -t ran: the test never saw the two claim one
// name, and the hand-written site silently stopped being served.
func TestSaveSiteLeavesAnotherSitesLinkAlone(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	ctx := context.Background()
	handWritten := filepath.Join(root, "sites-available", "shopfront.conf")
	if err := os.WriteFile(handWritten, []byte("server { server_name shop.example.com; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../sites-available/shopfront.conf", filepath.Join(root, "sites-enabled", "shop.example.com")); err != nil {
		t.Fatal(err)
	}
	spec := plainSpec("shop.example.com", "shop.example.com")
	want := "sites-enabled/shop.example.com already enables " + handWritten

	file, err := service.SiteFile("shop.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if file.Exists || file.EnabledElsewhere != want {
		t.Fatalf("the preview's view of the name = %+v, want it held by %s", file, handWritten)
	}
	for _, opts := range []SiteSave{{Enable: true, Reload: true}, {Reload: true}} {
		_, err := service.SaveSite(ctx, spec, opts)
		if err == nil || !strings.HasPrefix(err.Error(), want+" — ") {
			t.Fatalf("a new site over another's link (%+v): %v", opts, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "sites-available", "shop.example.com")); !os.IsNotExist(err) {
		t.Fatal("a refused new site left its file behind")
	}
	if got := enabledLinkTarget(t, root, "shop.example.com"); got != "../sites-available/shopfront.conf" {
		t.Fatalf("the hand-written site's link now names %s", got)
	}

	// An existing file of that name, edited: keeping its link state leaves
	// the other site's link where it is and says this one is not enabled;
	// asking to enable it is refused rather than unlinking the other.
	if err := os.WriteFile(filepath.Join(root, "sites-available", "shop.example.com"), []byte("# old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true, Reload: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Enabled || enabledLinkTarget(t, root, "shop.example.com") != "../sites-available/shopfront.conf" {
		t.Fatalf("an edit kept as it was took the other site's link: %+v", res)
	}
	if _, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true, Enable: true}); err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("enabling over another site's link: %v", err)
	}
	if got := enabledLinkTarget(t, root, "shop.example.com"); got != "../sites-available/shopfront.conf" {
		t.Fatalf("the hand-written site's link now names %s", got)
	}
}

// What holds a name's link, for each shape a sites-enabled entry takes.
func TestSiteFileSaysWhatHoldsTheNamesLink(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	if err := os.Symlink("../sites-available/stale", enabled("stale")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available("gone"), enabled("orphan")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enabled("inline"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(available("app"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available("app"), enabled("app")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(available("off"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, want      string
		exists, enabled bool
	}{
		{"free", "", false, false},
		{"app", "", true, true},
		{"off", "", true, false},
		// Its own link, left from a file deleted by hand: saving writes the
		// file it already names.
		{"stale", "", false, false},
		{"orphan", "sites-enabled/orphan links to " + available("gone") + ", which is not there", false, false},
		{"inline", "sites-enabled/inline is a file of its own, not a link", false, false},
	} {
		file, err := service.SiteFile(c.name)
		if err != nil {
			t.Fatal(err)
		}
		if file.Path != available(c.name) || file.Exists != c.exists || file.EnabledElsewhere != c.want || file.Enabled != c.enabled {
			t.Errorf("%s: %+v, want exists=%v enabled=%v held by %q", c.name, file, c.exists, c.enabled, c.want)
		}
	}

	// The stale link is this name's own, so a new site is saved through it.
	res, err := service.SaveSite(context.Background(), plainSpec("stale", "stale.example.com"), SiteSave{Enable: true})
	if err != nil || !res.Enabled {
		t.Fatalf("a new site over its own stale link: %+v %v", res, err)
	}

	// On a conf.d host there are no links to hold a name.
	if err := os.RemoveAll(filepath.Join(root, "sites-available")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if file, err := service.SiteFile("inline"); err != nil || file.EnabledElsewhere != "" || file.Enabled {
		t.Fatalf("conf.d: %+v %v", file, err)
	}
	// And every file there is read.
	if err := os.WriteFile(filepath.Join(root, "conf.d", "app.conf"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if file, err := service.SiteFile("app"); err != nil || !file.Exists || !file.Enabled {
		t.Fatalf("conf.d app: %+v %v", file, err)
	}
}

// Deleting a site whose name another site's link holds used to remove that
// link wherever it pointed: the hand-written site linked under its domain
// stopped being served, and the delete reported success.
func TestDeleteSiteLeavesAnotherSitesLinkAlone(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	ctx := context.Background()
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("server {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		t.Helper()
		if err := os.Symlink(target, enabled(name)); err != nil {
			t.Fatal(err)
		}
	}
	gone := func(path string) bool {
		_, err := os.Lstat(path)
		return os.IsNotExist(err)
	}

	// A file of the name, not enabled, beside a link of that name to the
	// hand-written site: the file goes, the link stays.
	write(available("shopfront.conf"))
	link("../sites-available/shopfront.conf", "shop.example.com")
	write(available("shop.example.com"))
	if err := service.DeleteSite(ctx, "shop.example.com"); err != nil {
		t.Fatal(err)
	}
	if !gone(available("shop.example.com")) {
		t.Fatal("the site's file is still there")
	}
	if _, err := os.Stat(available("shop.example.com.bak")); err != nil {
		t.Fatalf("the previous content was not kept: %v", err)
	}
	if got := enabledLinkTarget(t, root, "shop.example.com"); got != "../sites-available/shopfront.conf" {
		t.Fatalf("the hand-written site's link now names %q", got)
	}

	// The link alone, with no file of the name: nothing of this site's to
	// delete, and the other site's link is not taken instead.
	err := service.DeleteSite(ctx, "shop.example.com")
	want := "no such site: shop.example.com — sites-enabled/shop.example.com already enables " +
		available("shopfront.conf") + ", and stays"
	if err == nil || err.Error() != want {
		t.Fatalf("deleting a name only another site's link holds: %v", err)
	}
	if got := enabledLinkTarget(t, root, "shop.example.com"); got != "../sites-available/shopfront.conf" {
		t.Fatalf("the hand-written site's link now names %q", got)
	}

	// A file of its own in sites-enabled is a site too, with no backup if it
	// were removed here.
	write(enabled("inline.example.com"))
	write(available("inline.example.com"))
	if err := service.DeleteSite(ctx, "inline.example.com"); err != nil {
		t.Fatal(err)
	}
	if gone(enabled("inline.example.com")) || !gone(available("inline.example.com")) {
		t.Fatal("the delete took the file in sites-enabled, or left its own")
	}

	// The site's own link goes with it, spelled either way.
	for _, target := range []string{"../sites-available/app", available("app")} {
		write(available("app"))
		link(target, "app")
		if err := service.DeleteSite(ctx, "app"); err != nil {
			t.Fatal(err)
		}
		if !gone(enabled("app")) || !gone(available("app")) {
			t.Fatalf("the site's own link %s or its file survived the delete", target)
		}
	}

	// A link to nothing enables nothing and fails the next reload, whoever
	// it named: its own file deleted by hand, or another that is gone.
	link("../sites-available/stale", "stale")
	link(available("elsewhere-gone"), "orphan")
	for _, name := range []string{"stale", "orphan"} {
		if err := service.DeleteSite(ctx, name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !gone(enabled(name)) {
			t.Fatalf("the dangling link %s stayed", name)
		}
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

// linkTestedNginx is siteNginx with a test that fails, the way nginx fails
// on a broken site, only while the directory its configuration includes has
// <name> in it, and a reload that leaves a mark in the prefix.
func linkTestedNginx(t *testing.T, name string) (*Service, string) {
	t.Helper()
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	script := shimPrelude(root) +
		"if [ \"$1\" = \"-t\" ]; then\n" +
		"  site=\"$(dirname \"$include\")/" + name + "\"\n" +
		"  if [ -e \"$site\" ]; then\n" +
		"    echo \"nginx: [emerg] unknown directive \\\"frobnicate\\\" in $site:3\"\n" +
		"    echo \"nginx: configuration file $conf test failed\"; exit 1\n" +
		"  fi\n" +
		"  printf '%s\\n' \"$JD_TEST_OUT\"; exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"-s\" ]; then touch '" + root + "/reloaded'; exit 0; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return service, root
}

// A site saved disabled used to be written untested: nginx -t never read a
// file nothing linked, so "saved" said nothing about the day somebody
// enabled it. It is now tested in a copy of the configuration that links it,
// and what that test says is reported without refusing the save, in the
// words of the real files rather than the copy's.
func TestSaveSiteTestsADisabledSiteAsEnabled(t *testing.T) {
	service, root := linkTestedNginx(t, "app")
	ctx := context.Background()
	spec := plainSpec("app", "app.example.com")

	res, err := service.SaveSite(ctx, spec, SiteSave{Reload: true})
	if err != nil {
		t.Fatalf("a disabled site that fails its test enabled was refused: %v", err)
	}
	if res.Enabled || !res.TestedAsEnabled || res.Validation.Valid {
		t.Fatalf("result = %+v, want disabled, tested as enabled and failing", res)
	}
	want := Diagnostic{Level: "emerg", Message: `unknown directive "frobnicate"`, File: res.Path, Line: 3}
	if len(res.Validation.Diagnostics) != 1 || !reflect.DeepEqual(res.Validation.Diagnostics[0], want) {
		t.Fatalf("diagnostics = %+v, want %+v: the copy must still be there when nginx's file is resolved", res.Validation.Diagnostics, want)
	}
	wantOutput := `nginx: [emerg] unknown directive "frobnicate" in ` + filepath.Join(root, "sites-enabled", "app") + ":3\n" +
		"nginx: configuration file " + filepath.Join(root, "nginx.conf") + " test failed"
	if res.Validation.Output != wantOutput {
		t.Fatalf("output = %q, want %q", res.Validation.Output, wantOutput)
	}
	if isLinked(t, root, "app") {
		t.Fatal("the site was linked into the live configuration")
	}
	noTrialLeft(t, root)
	if b, _ := os.ReadFile(filepath.Join(root, "sites-available", "app")); !strings.Contains(string(b), "server_name app.example.com") {
		t.Fatalf("the disabled site was not written:\n%s", b)
	}
	// A reload follows a passing test only, and this one did not pass.
	if _, err := os.Stat(filepath.Join(root, "reloaded")); !os.IsNotExist(err) || res.Reloaded {
		t.Fatal("nginx was reloaded after a failing test")
	}

	// Enabling it is refused over the same test, and puts back the file it
	// replaced rather than deleting the disabled site.
	spec.Upstream = "http://127.0.0.1:4000"
	res, err = service.SaveSite(ctx, spec, SiteSave{Enable: true, Overwrite: true, Reload: true})
	if !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("enabling a site that fails its test: %v", err)
	}
	if isLinked(t, root, "app") || res.Enabled {
		t.Fatal("a refused enable left the site linked")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "sites-available", "app")); !strings.Contains(string(b), "127.0.0.1:3000") {
		t.Fatalf("a refused enable did not put the disabled file back:\n%s", b)
	}

	// A disabled site that passes is reloaded when asked and still not linked.
	other := plainSpec("fine", "fine.example.com")
	res, err = service.SaveSite(ctx, other, SiteSave{Reload: true})
	if err != nil || !res.TestedAsEnabled || !res.Validation.Valid || res.Enabled || !res.Reloaded || isLinked(t, root, "fine") {
		t.Fatalf("a passing disabled save: %+v %v", res, err)
	}
}

// A name a disabled site shares with an enabled one is what enabling it
// would meet: reported with which of the two nginx would answer it from,
// and no reason to refuse a file nginx does not read.
func TestSaveSiteReportsADisabledSitesConflictsWithoutRefusing(t *testing.T) {
	service, root := siteNginx(t, conflictWarning("app.example.com"), 0, "", 0)
	ctx := context.Background()
	enableSite(t, root, plainSpec("legacy", "app.example.com"))

	res, err := service.SaveSite(ctx, plainSpec("app", "app.example.com"), SiteSave{})
	if err != nil {
		t.Fatalf("a disabled site sharing a name was refused: %v", err)
	}
	want := []ServerNameConflict{{Domain: "app.example.com", Listen: "0.0.0.0:80", Site: "legacy", Effect: ConflictTakes}}
	if !reflect.DeepEqual(res.Conflicts, want) || res.Enabled || !res.TestedAsEnabled || isLinked(t, root, "app") {
		t.Fatalf("result = %+v, want the conflict enabling it would meet", res)
	}

	res, err = service.SaveSite(ctx, plainSpec("zz", "app.example.com"), SiteSave{})
	if err != nil || len(res.Conflicts) != 1 || res.Conflicts[0].Effect != ConflictIgnored || isLinked(t, root, "zz") {
		t.Fatalf("a disabled site nginx would ignore: %+v %v", res, err)
	}
}

// A disabled site whose name another site's link holds cannot be tested
// under its name, and the result says it was not tested.
func TestSaveSiteSaysWhenADisabledSiteCouldNotBeTested(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	handWritten := filepath.Join(root, "sites-available", "shopfront.conf")
	if err := os.WriteFile(handWritten, []byte("server { server_name shop.example.com; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(handWritten, filepath.Join(root, "sites-enabled", "shop.example.com")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sites-available", "shop.example.com"), []byte("# old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := service.SaveSite(context.Background(), plainSpec("shop.example.com", "shop.example.com"), SiteSave{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "sites-enabled/shop.example.com already enables " + handWritten + ", so nginx could not test this site as enabled."
	if res.TestedAsEnabled || res.Enabled || res.Validation.Note != want {
		t.Fatalf("result = %+v, note %q; want untested with %q", res, res.Validation.Note, want)
	}
	if got := enabledLinkTarget(t, root, "shop.example.com"); got != handWritten {
		t.Fatalf("the other site's link now names %s", got)
	}
}

// noTrialLeft fails when a trial configuration outlived its test.
func noTrialLeft(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".jd-trial-") {
			t.Fatalf("%s was left in %s", e.Name(), root)
		}
	}
}

// liveCalls are the fake nginx's calls, each with what the live
// sites-enabled held while it ran.
func liveCalls(t *testing.T, root string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "bin", "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// A disabled site used to be linked into the live sites-enabled for the
// length of its test and of the dump that orders a conflict. Nothing that
// runs nginx outside the service lock waits for that — the reload and test
// endpoints, a toggle, certbot's hook — so a reload in the window served the
// disabled site, and one that was broken failed the reload. Every nginx the
// save runs now reads a copy, and the live tree never holds the site.
func TestADisabledSaveNeverPutsTheSiteInTheLiveConfiguration(t *testing.T) {
	service, root := siteNginx(t, conflictWarning("app.example.com"), 0, "", 0)
	enableSite(t, root, plainSpec("legacy", "app.example.com"))
	conf, _ := os.ReadFile(filepath.Join(root, "nginx.conf"))

	res, err := service.SaveSite(context.Background(), plainSpec("app", "app.example.com"), SiteSave{})
	if err != nil || !res.TestedAsEnabled || len(res.Conflicts) != 1 || res.Conflicts[0].Effect != ConflictTakes {
		t.Fatalf("result = %+v %v, want tested as enabled and taking the name from legacy", res, err)
	}
	calls := liveCalls(t, root)
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "-t -c ") || !strings.HasPrefix(calls[1], "-T -c ") {
		t.Fatalf("nginx was run as %q, want a test and a dump of the copy", calls)
	}
	for _, call := range calls {
		if held := strings.Fields(strings.SplitN(call, "::", 2)[1]); !reflect.DeepEqual(held, []string{"legacy"}) {
			t.Fatalf("the live sites-enabled held %v while nginx ran %q", held, call)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(root, "nginx.conf")); string(after) != string(conf) {
		t.Fatalf("nginx.conf changed:\n%s", after)
	}
	noTrialLeft(t, root)
}

// The copy is nginx.conf with only the include of the site directory
// pointed elsewhere, so every line nginx names in it is nginx.conf's line.
func TestTrialConfigurationKeepsNginxConfAsItIs(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	sites := filepath.Join(root, "sites-enabled")
	conf := "events {}\n" +
		"http {\n" +
		"    include mime.types; # sites-enabled/* comes last\n" +
		"    include \"" + sites + "/*\";\n" +
		"    include sites-enabled/*.conf;\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sites, "legacy"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(root, "sites-available", "app")
	trial, note := service.stageTrial(sites, "app", site)
	if note != "" {
		t.Fatal(note)
	}
	b, err := os.ReadFile(trial.main)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(conf, "\""+sites+"/*\"", "\""+trial.dir+"/*\"")
	want = strings.Replace(want, "include sites-enabled/*.conf", "include "+trial.dir+"/*.conf", 1)
	if string(b) != want {
		t.Fatalf("copy:\n%s\nwant:\n%s", b, want)
	}
	for name, target := range map[string]string{"legacy": filepath.Join(sites, "legacy"), "app": site} {
		if got, err := os.Readlink(filepath.Join(trial.dir, name)); err != nil || got != target {
			t.Fatalf("%s in the copy links %q (%v), want %s", name, got, err, target)
		}
	}
	trial.remove()
	noTrialLeft(t, root)

	// A site nginx.conf's include would not read enabled is not tested as
	// if it were, and neither is one when nginx.conf includes no sites.
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte("http {\n    include "+sites+"/*.conf;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, note := service.stageTrial(sites, "app", site); note != "nginx.conf includes sites-enabled as "+sites+"/*.conf, which does not match app"+trialUntested {
		t.Fatalf("note = %q", note)
	}
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte("http {\n    include conf.d/*.conf;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, note := service.stageTrial(sites, "app", site); note != "nginx.conf does not include sites-enabled itself"+trialUntested {
		t.Fatalf("note = %q", note)
	}
	noTrialLeft(t, root)
}

// confdNginx is linkTestedNginx on a host that keeps its sites in conf.d,
// whose test fails while conf.d/<failing> is read.
func confdNginx(t *testing.T, failing string) (*Service, string) {
	t.Helper()
	service, root := linkTestedNginx(t, failing)
	if err := os.RemoveAll(filepath.Join(root, "sites-available")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	conf := "events {}\nhttp {\n    include " + filepath.Join(root, "conf.d") + "/*.conf;\n}\n"
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	return service, root
}

func confdEntries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "conf.d"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A conf.d site is switched off by renaming app.conf to app.conf.disabled,
// which nginx's conf.d/*.conf no longer reads. The form read it back as
// app.conf.disabled, said it would stay disabled, and saved it to
// app.conf.disabled.conf — a second copy of the site that nginx reads. The
// save now writes the file that was read, tests it as if it ended in .conf,
// and leaves it off.
func TestSaveSiteKeepsAConfDSiteThatIsOffWhereItIs(t *testing.T) {
	service, root := confdNginx(t, "app.conf.disabled.conf")
	ctx := context.Background()
	off := filepath.Join(root, "conf.d", "app.conf.disabled")
	written, err := RenderNginx(plainSpec("app.conf.disabled", "app.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(off, []byte(written), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, _, _, err := service.ReadSiteSpec("app.conf.disabled")
	if err != nil || spec.Name != "app.conf.disabled" {
		t.Fatalf("read back as %+v %v", spec, err)
	}
	file, err := service.SiteFile(spec.Name)
	if err != nil || file != (SiteFileInfo{Path: off, Exists: true, Confd: true}) {
		t.Fatalf("site file = %+v %v, want the file that was read, off", file, err)
	}

	spec.Upstream = "http://127.0.0.1:4000"
	res, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true})
	if err != nil {
		t.Fatalf("keeping a conf.d site off: %v", err)
	}
	if res.Enabled || !res.TestedAsEnabled || res.Validation.Valid || res.Path != off {
		t.Fatalf("result = %+v, want off, tested as app.conf.disabled.conf and failing", res)
	}
	want := Diagnostic{Level: "emerg", Message: `unknown directive "frobnicate"`, File: off, Line: 3}
	if len(res.Validation.Diagnostics) != 1 || !reflect.DeepEqual(res.Validation.Diagnostics[0], want) {
		t.Fatalf("diagnostics = %+v, want %+v", res.Validation.Diagnostics, want)
	}
	if got := confdEntries(t, root); !reflect.DeepEqual(got, []string{"app.conf.disabled"}) {
		t.Fatalf("conf.d holds %v after the save, want the one file nginx does not read", got)
	}
	if b, _ := os.ReadFile(off); !strings.Contains(string(b), "127.0.0.1:4000") {
		t.Fatalf("the edit was not written in place:\n%s", b)
	}
	noTrialLeft(t, root)

	// There is no link to make: enabling it is renaming it, which a save
	// does not do.
	if _, err := service.SaveSite(ctx, spec, SiteSave{Enable: true, Overwrite: true, Reload: true}); err == nil ||
		!strings.Contains(err.Error(), "renaming") {
		t.Fatalf("enabling a conf.d site that is off: %v", err)
	}
	if got := confdEntries(t, root); !reflect.DeepEqual(got, []string{"app.conf.disabled"}) {
		t.Fatalf("conf.d holds %v after a refused enable", got)
	}

	// Deleting it deletes the file the listing names, where it looked for
	// app.conf.disabled.conf and found no such site.
	if err := service.DeleteSite(ctx, "app.conf.disabled"); err != nil {
		t.Fatal(err)
	}
	if got := confdEntries(t, root); !reflect.DeepEqual(got, []string{"app.conf.disabled.bak"}) {
		t.Fatalf("conf.d holds %v after the delete", got)
	}
}

// app and app.conf side by side are two sites; each is read back under a
// name that saves and deletes that one, never the other.
func TestConfDSitesOfOneNameWithAndWithoutTheSuffixStayApart(t *testing.T) {
	service, root := confdNginx(t, "nothing")
	for _, name := range []string{"app", "app.conf"} {
		if err := os.WriteFile(filepath.Join(root, "conf.d", name), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for listed, wantEnabled := range map[string]bool{"app": false, "app.conf": true} {
		spec, _, _, err := service.ReadSiteSpec(listed)
		if err != nil {
			t.Fatal(err)
		}
		file, err := service.SiteFile(spec.Name)
		if err != nil || file.Path != filepath.Join(root, "conf.d", listed) || file.Enabled != wantEnabled {
			t.Fatalf("%s reads back as %q, saving to %+v %v", listed, spec.Name, file, err)
		}
	}
	if err := service.DeleteSite(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "conf.d", "app.conf")); err != nil || string(b) != "# app.conf\n" {
		t.Fatalf("deleting app touched app.conf: %q %v", b, err)
	}
}

// A site linked under a name of its own choosing — sites-enabled/010-app —
// is enabled. It was called disabled, so a save put a second link beside
// the first for its test: nginx read the file twice, reported the site
// conflicting with itself, and the edit was never reloaded.
func TestSaveSiteTakesALinkOfAnotherNameForEnabled(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	ctx := context.Background()
	spec := plainSpec("app", "app.example.com")
	full := filepath.Join(root, "sites-available", "app")
	if err := os.WriteFile(full, []byte("server { server_name app.example.com; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../sites-available/app", filepath.Join(root, "sites-enabled", "010-app")); err != nil {
		t.Fatal(err)
	}
	// A dotted entry is not one nginx's include reads.
	if err := os.Symlink(full, filepath.Join(root, "sites-enabled", ".app.old")); err != nil {
		t.Fatal(err)
	}
	if file, err := service.SiteFile("app"); err != nil || !file.Enabled {
		t.Fatalf("site file = %+v %v, want enabled", file, err)
	}
	for _, opts := range []SiteSave{{Overwrite: true, Reload: true}, {Enable: true, Overwrite: true, Reload: true}} {
		res, err := service.SaveSite(ctx, spec, opts)
		if err != nil || !res.Enabled || res.TestedAsEnabled || !res.Reloaded || len(res.Conflicts) != 0 {
			t.Fatalf("%+v: result = %+v %v, want enabled and reloaded", opts, res, err)
		}
		if isLinked(t, root, "app") {
			t.Fatalf("%+v: a second link was made beside 010-app", opts)
		}
	}
	for _, call := range liveCalls(t, root) {
		if strings.Contains(call, "-c") {
			t.Fatalf("an enabled site was tested in a copy: %q", call)
		}
	}

	// Without 010-app it is disabled again, and the dotted entry does not
	// change that.
	if err := os.Remove(filepath.Join(root, "sites-enabled", "010-app")); err != nil {
		t.Fatal(err)
	}
	if file, err := service.SiteFile("app"); err != nil || file.Enabled {
		t.Fatalf("site file = %+v %v, want disabled", file, err)
	}
}

// The listing called a site serving whenever a link of its name was there,
// wherever it pointed, while the form said the site could be neither
// enabled nor tested: its link enables another file.
func TestTheListingAndTheFormAgreeOnALinkThatEnablesAnotherFile(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	for _, name := range []string{"held.example.com", "other", "own", "off", "copy"} {
		if err := os.WriteFile(available(name), []byte("server {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"held.example.com": available("other"), "own": available("own")} {
		if err := os.Symlink(target, filepath.Join(root, "sites-enabled", link)); err != nil {
			t.Fatal(err)
		}
	}
	// A copy made with cp where a link was meant: nginx serves it.
	if err := os.WriteFile(filepath.Join(root, "sites-enabled", "copy"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, v := range service.nginxVHosts() {
		listed[v.Name] = v.Enabled
	}
	for _, name := range []string{"held.example.com", "own", "off", "copy"} {
		file, err := service.SiteFile(name)
		if err != nil {
			t.Fatal(err)
		}
		// The listing says whether nginx serves the name; the form tells a
		// file it reads from a file of its own that it reads instead.
		if listed[name] != (file.Enabled || file.ServedCopy) {
			t.Errorf("%s: listed enabled=%v, the form says enabled=%v, served from a copy=%v",
				name, listed[name], file.Enabled, file.ServedCopy)
		}
	}
	if listed["held.example.com"] {
		t.Error("a site whose name's link enables another file is listed as serving")
	}
	if !listed["copy"] {
		t.Error("a site nginx serves from a file of its own in sites-enabled is listed as not serving")
	}
	if file, _ := service.SiteFile("copy"); file.Enabled || !file.ServedCopy {
		t.Errorf("copy: %+v, want served from the copy and this file not read", file)
	}
}

// A site whose sites-enabled entry is a file of its own is served from that
// file. Its save was reported as "It stays disabled", which was not true of
// a name nginx answers; it says now that the save is not what nginx serves,
// and leaves the file nginx serves alone.
func TestSaveSiteSaysNginxServesAFileOfItsOwn(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	ctx := context.Background()
	spec := plainSpec("copy", "copy.example.com")
	copied := filepath.Join(root, "sites-enabled", "copy")
	if _, err := service.SaveSite(ctx, spec, SiteSave{}); err != nil {
		t.Fatal(err)
	}
	served := "server { server_name copy.example.com; } # the copy\n"
	if err := os.WriteFile(copied, []byte(served), 0o644); err != nil {
		t.Fatal(err)
	}
	spec.Upstream = "http://127.0.0.1:4000"
	res, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	want := "nginx serves sites-enabled/copy, a file of its own, and not this one, so it did not test this file and the save changes nothing it serves."
	if !res.ServedCopy || res.Enabled || res.TestedAsEnabled || res.Validation.Note != want {
		t.Fatalf("result = %+v, note %q", res, res.Validation.Note)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "sites-available", "copy")); !strings.Contains(string(b), "127.0.0.1:4000") {
		t.Fatalf("the edit was not written:\n%s", b)
	}
	if b, _ := os.ReadFile(copied); string(b) != served {
		t.Fatalf("the file nginx serves was changed:\n%s", b)
	}
	// Enabling this file would take the place of the one nginx serves.
	if _, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true, Enable: true}); err == nil {
		t.Fatal("enabling over a file of its own in sites-enabled was not refused")
	}
	if b, _ := os.ReadFile(copied); string(b) != served {
		t.Fatalf("a refused enable changed the file nginx serves:\n%s", b)
	}
}

// The form says a hand-written site's previous version is kept as .bak when
// it is saved. It was not: the save replaced the file with what the form
// read of it, and a comment or a directive the form has no field for was
// gone for good.
func TestSaveSiteKeepsAHandWrittenFileAsBak(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	ctx := context.Background()
	full := filepath.Join(root, "sites-available", "hand")
	backup := full + ".bak"
	hand := "# written by hand\nserver {\n    server_name hand.example.com;\n    location / {\n" +
		"        proxy_pass http://127.0.0.1:3000;\n        proxy_buffering off;\n        sub_filter 'foo' 'bar';\n    }\n}\n"
	if err := os.WriteFile(full, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(full, filepath.Join(root, "sites-enabled", "hand")); err != nil {
		t.Fatal(err)
	}
	spec, managed, _, err := service.ReadSiteSpec("hand")
	if err != nil || managed {
		t.Fatalf("read back as %+v managed=%v %v", spec, managed, err)
	}
	res, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true, Reload: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup != backup {
		t.Fatalf("backup = %q, want %q", res.Backup, backup)
	}
	if b, err := os.ReadFile(backup); err != nil || string(b) != hand {
		t.Fatalf("the .bak holds %q %v, want the hand-written file", b, err)
	}
	if b, _ := os.ReadFile(full); !strings.Contains(string(b), managedMarker) {
		t.Fatalf("the save did not replace the file:\n%s", b)
	}
	// The listing does not offer the copy as a site.
	for _, v := range service.nginxVHosts() {
		if v.Name == "hand.bak" {
			t.Fatal("the .bak is listed as a site")
		}
	}

	// The file is the dashboard's now: saving it again keeps the .bak as the
	// hand-written version rather than overwriting it with the first save.
	spec.Upstream = "http://127.0.0.1:4000"
	res, err = service.SaveSite(ctx, spec, SiteSave{Overwrite: true, Reload: true})
	if err != nil || res.Backup != "" {
		t.Fatalf("a save over the dashboard's own file: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(backup); string(b) != hand {
		t.Fatalf("the .bak was overwritten by a save of the dashboard's own file:\n%s", b)
	}

	// A save that is refused leaves the file and the .bak as they were: the
	// older .bak comes back, and one that was not there is not left behind.
	t.Setenv("JD_TEST_OUT", `nginx: [emerg] unknown directive "frobnicate" in `+full+`:3`)
	t.Setenv("JD_TEST_EXIT", "1")
	if err := os.WriteFile(full, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("# older\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true}); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("err = %v, want the test's refusal", err)
	}
	if b, _ := os.ReadFile(full); string(b) != hand {
		t.Fatalf("a refused save left:\n%s", b)
	}
	if b, _ := os.ReadFile(backup); string(b) != "# older\n" {
		t.Fatalf("a refused save left the .bak holding:\n%s", b)
	}
	if err := os.Remove(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveSite(ctx, spec, SiteSave{Overwrite: true}); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("err = %v, want the test's refusal", err)
	}
	if _, err := os.Lstat(backup); !os.IsNotExist(err) {
		t.Fatalf("a refused save left a .bak behind: %v", err)
	}
}

// Deleting a site linked only under another name, such as 010-app, removed
// sites-enabled/app, which was not there, and left 010-app naming a file that
// was gone: every nginx test and reload failed from then on.
func TestDeleteSiteRemovesALinkOfAnotherName(t *testing.T) {
	service, root := siteNginx(t, cleanTest, 0, "", 0)
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	for _, name := range []string{"app", "other"} {
		if err := os.WriteFile(available(name), []byte("server {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		"010-app": "../sites-available/app", "020-app": available("app"),
		"other": available("other"), ".app.old": available("app"),
	} {
		if err := os.Symlink(target, enabled(link)); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.DeleteSite(context.Background(), "app"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"010-app", "020-app"} {
		if _, err := os.Lstat(enabled(name)); !os.IsNotExist(err) {
			t.Errorf("%s still links the deleted site: %v", name, err)
		}
	}
	if _, err := os.Stat(available("app")); !os.IsNotExist(err) {
		t.Fatal("the site's file is still there")
	}
	if _, err := os.Stat(available("app.bak")); err != nil {
		t.Fatalf("the previous content was not kept: %v", err)
	}
	// Another site's link stays, and so does a dotted entry nginx never read.
	if target := enabledLinkTarget(t, root, "other"); target != available("other") {
		t.Fatalf("the other site's link now names %q", target)
	}
	if _, err := os.Lstat(enabled(".app.old")); err != nil {
		t.Fatalf("the dotted entry was removed: %v", err)
	}
}
