package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// nginxShim puts an nginx first on PATH that runs script, so the service's
// `nginx -t` and `nginx -s reload` answer however the test needs.
func nginxShim(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nginx"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// debianTree is an nginx directory in the Debian layout — sites-available,
// sites-enabled and conf.d — with its symlinks resolved, since the service
// names files the way allowedPath resolves them.
func debianTree(t *testing.T) (*Service, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"sites-available", "sites-enabled", "conf.d", "snippets"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return New(root, filepath.Join(root, "Caddyfile")), root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// listed is the one entry the listing has for name in layout.
func listed(t *testing.T, hosts []VHost, layout, name string) VHost {
	t.Helper()
	var found []VHost
	for _, h := range hosts {
		if h.Layout == layout && h.Name == name {
			found = append(found, h)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s/%s listed %d times in %s", layout, name, len(found), describeHosts(hosts))
	}
	return found[0]
}

func describeHosts(hosts []VHost) string {
	out := []string{}
	for _, h := range hosts {
		out = append(out, h.Layout+"/"+h.Name)
	}
	return strings.Join(out, ", ")
}

type linkChanges struct{ changes []Change }

func (l *linkChanges) Record(_ context.Context, c Change) error {
	l.changes = append(l.changes, c)
	return nil
}

func (l *linkChanges) actions() []ChangeAction {
	out := []ChangeAction{}
	for _, c := range l.changes {
		out = append(out, c.Action)
	}
	return out
}

// The listing used to read sites-available alone on a Debian host. conf.d,
// which nginx.conf includes there too, was invisible, and so was everything
// only in sites-enabled — among it a link whose file is gone, which makes
// nginx refuse every reload, and which the Lstat check reported as a site
// that was serving whenever it did have a sites-available twin.
func TestListingFindsEverySiteNginxReads(t *testing.T) {
	svc, root := debianTree(t)
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	site := "server {\n    listen 80;\n    server_name %s.test;\n}\n"

	writeFile(t, available("linked"), fmt.Sprintf(site, "linked"))
	symlink(t, "../sites-available/linked", enabled("linked"))
	writeFile(t, available("off"), fmt.Sprintf(site, "off"))
	writeFile(t, available("gone"), fmt.Sprintf(site, "gone"))
	symlink(t, available("missing"), enabled("gone"))
	writeFile(t, available("renamed"), fmt.Sprintf(site, "renamed"))
	symlink(t, available("linked"), enabled("renamed"))
	writeFile(t, available("copied"), fmt.Sprintf(site, "copied"))
	writeFile(t, enabled("copied"), fmt.Sprintf(site, "copied"))
	writeFile(t, available("app.bak"), fmt.Sprintf(site, "backup"))
	writeFile(t, filepath.Join(root, "conf.d", "b.conf"), fmt.Sprintf(site, "b"))
	writeFile(t, filepath.Join(root, "conf.d", "notes"), fmt.Sprintf(site, "notes"))
	writeFile(t, enabled("c"), fmt.Sprintf(site, "c"))
	symlink(t, "/nonexistent/jd-test/d.conf", enabled("d"))
	writeFile(t, filepath.Join(root, "custom", "g.conf"), fmt.Sprintf(site, "g"))
	symlink(t, "../custom/g.conf", enabled("g"))
	writeFile(t, enabled("old.bak"), fmt.Sprintf(site, "old"))
	writeFile(t, enabled(".hidden"), fmt.Sprintf(site, "hidden"))
	if err := os.MkdirAll(enabled("folder"), 0o755); err != nil {
		t.Fatal(err)
	}

	hosts := svc.nginxVHosts()
	if len(hosts) != 11 {
		t.Fatalf("listed %d entries: %s", len(hosts), describeHosts(hosts))
	}

	cases := []struct {
		layout, name string
		want         VHost
	}{
		{"sites-available", "linked", VHost{Path: available("linked"), EnabledPath: enabled("linked"), Enabled: true, FormEditable: true}},
		{"sites-available", "off", VHost{Path: available("off"), EnabledPath: enabled("off"), FormEditable: true}},
		{"sites-available", "gone", VHost{Path: available("gone"), EnabledPath: enabled("gone"), FormEditable: true, Broken: "dangling", LinkTarget: available("missing")}},
		{"sites-available", "renamed", VHost{Path: available("renamed"), EnabledPath: enabled("renamed"), FormEditable: true, Broken: "stale", LinkTarget: available("linked")}},
		{"sites-available", "copied", VHost{Path: available("copied"), EnabledPath: enabled("copied"), FormEditable: true, Broken: "stale"}},
		// conf.d on a Debian host: nginx reads it, and the form would save it
		// to sites-available instead.
		{"conf.d", "b.conf", VHost{Path: filepath.Join(root, "conf.d", "b.conf"), Enabled: true}},
		{"conf.d", "notes", VHost{Path: filepath.Join(root, "conf.d", "notes")}},
		{"sites-enabled", "c", VHost{Path: enabled("c"), Enabled: true}},
		{"sites-enabled", "d", VHost{EnabledPath: enabled("d"), Broken: "dangling", LinkTarget: "/nonexistent/jd-test/d.conf"}},
		{"sites-enabled", "g", VHost{Path: enabled("g"), EnabledPath: enabled("g"), Enabled: true, LinkTarget: filepath.Join(root, "custom", "g.conf")}},
		// sites-enabled/* is read whole, so a backup-looking name there is a
		// served site.
		{"sites-enabled", "old.bak", VHost{Path: enabled("old.bak"), Enabled: true}},
	}
	for _, tc := range cases {
		got := listed(t, hosts, tc.layout, tc.name)
		pick := VHost{
			Path: got.Path, EnabledPath: got.EnabledPath, Enabled: got.Enabled,
			FormEditable: got.FormEditable, Broken: got.Broken, LinkTarget: got.LinkTarget,
		}
		if !reflect.DeepEqual(pick, tc.want) {
			t.Errorf("%s/%s:\n got  %+v\n want %+v", tc.layout, tc.name, pick, tc.want)
		}
	}
	if got := listed(t, hosts, "sites-enabled", "g"); !slices.Equal(got.ServerNames, []string{"g.test"}) {
		t.Errorf("a linked file is read through its link: %v", got.ServerNames)
	}
}

// Every RPM distribution, Alpine and Arch keep their sites in conf.d, where
// the form saves conf.d/<name>: a .conf file is its own, a file without the
// suffix is not — saving it would write <name>.conf beside it, active.
func TestConfDHostOffersTheFormOnlyForWhatItSavesBack(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "conf.d", "app.conf"), "server {}\n")
	writeFile(t, filepath.Join(root, "conf.d", "notes"), "server {}\n")
	hosts := New(root, filepath.Join(root, "Caddyfile")).nginxVHosts()
	if !listed(t, hosts, "conf.d", "app.conf").FormEditable {
		t.Error("app.conf is where the form saves app.conf")
	}
	if listed(t, hosts, "conf.d", "notes").FormEditable {
		t.Error("the form would save notes as notes.conf")
	}
}

// A site that forces HTTPS is two server blocks with the same names, and the
// card listed every domain twice.
func TestListingNamesEachDomainAndListenerOnce(t *testing.T) {
	svc, root := debianTree(t)
	writeFile(t, filepath.Join(root, "sites-available", "hand"), `server {
    listen 80;
    listen [::]:80;
    server_name app.example.com www.app.example.com;
    return 301 https://$host$request_uri;
}
server {
    listen 443   ssl;
    listen [::]:443 ssl;
    server_name "app.example.com" www.app.example.com;
    location / { proxy_pass http://127.0.0.1:3000; }
    location /api/ {
        proxy_pass http://127.0.0.1:3000;
    }
}
`)
	rendered, err := RenderNginx(&SiteSpec{
		Name: "rendered", Kind: "proxy", Domains: []string{"app.example.com", "www.app.example.com"},
		Upstream: "http://127.0.0.1:3000", TLS: true, ForceHTTPS: true,
		CertPath: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
		KeyPath:  "/etc/letsencrypt/live/app.example.com/privkey.pem",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "sites-available", "rendered"), rendered)

	hosts := svc.nginxVHosts()
	hand := listed(t, hosts, "sites-available", "hand")
	if want := []string{"app.example.com", "www.app.example.com"}; !slices.Equal(hand.ServerNames, want) {
		t.Errorf("names = %q, want %q", hand.ServerNames, want)
	}
	if want := []string{"80", "[::]:80", "443 ssl", "[::]:443 ssl"}; !slices.Equal(hand.Listen, want) {
		t.Errorf("listen = %q, want %q", hand.Listen, want)
	}
	if want := []string{"http://127.0.0.1:3000"}; !slices.Equal(hand.Upstreams, want) {
		t.Errorf("upstreams = %q, want %q", hand.Upstreams, want)
	}
	// What the form itself writes for a forced-HTTPS site, whatever else it
	// comes to render: each name once.
	form := listed(t, hosts, "sites-available", "rendered")
	if want := []string{"app.example.com", "www.app.example.com"}; !slices.Equal(form.ServerNames, want) {
		t.Errorf("rendered names = %q, want %q", form.ServerNames, want)
	}
	for i, listen := range form.Listen {
		if slices.Index(form.Listen, listen) != i {
			t.Errorf("rendered listen %q is listed twice: %q", listen, form.Listen)
		}
	}
}

// Only the first ssl_certificate was read, with any quotes kept — so an RSA
// and ECDSA pair showed one certificate, a quoted path reached the
// certificate list as a file that did not exist, and one named in an
// included snippet made the site read as plain HTTP.
func TestListingReadsEveryCertificateASiteNames(t *testing.T) {
	svc, root := debianTree(t)
	writeFile(t, filepath.Join(root, "snippets", "extra.conf"), "ssl_certificate '/etc/ssl/snippet.pem';\nssl_certificate_key /etc/ssl/snippet.key;\n")
	writeFile(t, filepath.Join(root, "snippets", "snakeoil.conf"), "ssl_certificate /etc/ssl/certs/ssl-cert-snakeoil.pem;\nssl_certificate_key /etc/ssl/private/ssl-cert-snakeoil.key;\n")
	writeFile(t, filepath.Join(root, "sites-available", "dual"), `server {
    listen 443 ssl;
    server_name dual.example.com;
    ssl_certificate "/etc/ssl/rsa.pem";
    ssl_certificate_key "/etc/ssl/rsa.key";
    ssl_certificate /etc/ssl/ecdsa.pem;
    ssl_certificate_key /etc/ssl/ecdsa.key;
    include snippets/extra.conf;
    include /etc/letsencrypt/options-ssl-nginx.conf;
}
`)
	writeFile(t, filepath.Join(root, "sites-available", "snakeoil"), "server {\n    listen 443 ssl default_server;\n    include snippets/snakeoil.conf;\n}\n")
	writeFile(t, filepath.Join(root, "sites-available", "inherited"), "server {\n    listen 443 ssl;\n    server_name inherited.example.com;\n}\n")
	writeFile(t, filepath.Join(root, "sites-available", "plain"), "server {\n    listen 80;\n    server_name plain.example.com;\n}\n")

	hosts := svc.nginxVHosts()
	dual := listed(t, hosts, "sites-available", "dual")
	if want := []string{"/etc/ssl/rsa.pem", "/etc/ssl/ecdsa.pem", "/etc/ssl/snippet.pem"}; !slices.Equal(dual.CertPaths, want) {
		t.Errorf("certificates = %q, want %q", dual.CertPaths, want)
	}
	if dual.CertPath != "/etc/ssl/rsa.pem" || !dual.TLS {
		t.Errorf("certPath = %q tls = %v", dual.CertPath, dual.TLS)
	}
	snakeoil := listed(t, hosts, "sites-available", "snakeoil")
	if snakeoil.CertPath != "/etc/ssl/certs/ssl-cert-snakeoil.pem" || !snakeoil.TLS {
		t.Errorf("a certificate in an included snippet: %q tls=%v", snakeoil.CertPath, snakeoil.TLS)
	}
	// A TLS listener whose certificate the http block carries is still TLS.
	if inherited := listed(t, hosts, "sites-available", "inherited"); !inherited.TLS || inherited.CertPath != "" {
		t.Errorf("listen 443 ssl: tls=%v certPath=%q", inherited.TLS, inherited.CertPath)
	}
	if plain := listed(t, hosts, "sites-available", "plain"); plain.TLS || len(plain.CertPaths) != 0 {
		t.Errorf("a plain site read as TLS: %+v", plain)
	}
}

// Caddy serves every address with a host over HTTPS unless told otherwise,
// and the listing said plain HTTP for every Caddyfile. The localhost and IP
// rows were checked against caddy:2 itself, which answers both on HTTPS from
// its local authority.
func TestParseCaddyfileSaysWhetherEverySiteIsOnTLS(t *testing.T) {
	cases := []struct {
		name, content string
		tls           bool
	}{
		{"a domain", "example.com {\n\treverse_proxy 127.0.0.1:3000\n}\n", true},
		{"an https address", "https://example.com {\n\trespond ok\n}\n", true},
		{"an http address", "http://example.com {\n\treverse_proxy 127.0.0.1:3000\n}\n", false},
		{"port 80", "example.org:80 {\n\trespond ok\n}\n", false},
		{"no host", ":8080 {\n\trespond ok\n}\n", false},
		{"localhost", "localhost {\n\trespond ok\n}\n", true},
		{"an IP address", "127.0.0.1:8443 {\n\trespond ok\n}\n", true},
		{"an explicit tls on a bare port", ":9443 {\n\ttls internal\n\trespond ok\n}\n", true},
		{"auto_https off", "{\n\tauto_https off\n}\nexample.com {\n\trespond ok\n}\n", false},
		{"auto_https off with a certificate", "{\n\tauto_https off\n}\nexample.com {\n\ttls /etc/ssl/c.pem /etc/ssl/k.pem\n}\n", true},
		{"one plain site among secure ones", "example.com, http://plain.example.com {\n\trespond ok\n}\n", false},
		{"a snippet is not a site", "(common) {\n\tencode gzip\n}\nexample.com {\n\timport common\n}\n", true},
		{"a tls block is a directive", "example.com {\n\ttls {\n\t\tprotocols tls1.2 tls1.3\n\t}\n}\n", true},
		{"no sites", "{\n\temail ops@example.com\n}\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, tls := parseCaddyfile(tc.content); tls != tc.tls {
				t.Errorf("tls = %v, want %v", tls, tc.tls)
			}
		})
	}
}

func TestCaddyfileEntryCarriesItsTLS(t *testing.T) {
	root := t.TempDir()
	caddyfile := filepath.Join(root, "Caddyfile")
	writeFile(t, caddyfile, "app.example.com {\n\treverse_proxy 127.0.0.1:3000\n}\n")
	sites, err := New(filepath.Join(root, "nginx"), caddyfile).caddySites()
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || !sites[0].TLS {
		t.Fatalf("got %+v", sites)
	}
}

// refusingWhileLinked is an nginx that refuses the configuration whenever
// sites-enabled/<name> is there, naming it the way nginx names an included
// file: by the link.
func refusingWhileLinked(root, name string) string {
	link := filepath.Join(root, "sites-enabled", name)
	return fmt.Sprintf(`if [ -e '%[1]s' ]; then
  echo 'nginx: [emerg] unknown directive "foo" in %[1]s:3' >&2
  echo 'nginx: configuration file %[2]s/nginx.conf test failed' >&2
  exit 1
fi
exit 0`, link, root)
}

// Enabling used to make the link and return. The reload after it was refused,
// the link stayed, and every later reload — a deployment's cutover among
// them — was refused with it until somebody removed the link by hand.
func TestEnablingASiteNginxRefusesTakesTheLinkBackOut(t *testing.T) {
	svc, root := debianTree(t)
	log := &linkChanges{}
	svc.SetRecorder(log)
	nginxShim(t, refusingWhileLinked(root, "broken"))
	site := filepath.Join(root, "sites-available", "broken")
	writeFile(t, site, "server {\n    listen 80;\n    foo bar;\n}\n")

	err := svc.SetVHostEnabled(context.Background(), "broken", true)
	var refused *RefusedError
	if !errors.As(err, &refused) || !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("enable returned %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "sites-enabled", "broken")); !os.IsNotExist(err) {
		t.Fatalf("the refused link is still in sites-enabled: %v", err)
	}
	// nginx names the link it included; the reason names the site's file.
	if want := `unknown directive "foo" in ` + site + ":3"; FailureHeadline(refused.Validation) != want {
		t.Errorf("headline = %q, want %q", FailureHeadline(refused.Validation), want)
	}
	if !strings.Contains(err.Error(), `unknown directive "foo"`) {
		t.Errorf("error = %q", err)
	}
	if len(log.changes) != 0 {
		t.Errorf("a refused enable was recorded: %v", log.actions())
	}
}

// A link the enable replaced comes back exactly as it was, relative target
// and all, when nginx refuses the new one.
func TestARefusedEnablePutsBackTheLinkItReplaced(t *testing.T) {
	svc, root := debianTree(t)
	nginxShim(t, fmt.Sprintf(`if [ "$(readlink '%s')" = '%s' ]; then echo 'nginx: [emerg] unknown directive "foo" in %[1]s:3' >&2; exit 1; fi; exit 0`,
		filepath.Join(root, "sites-enabled", "app"), filepath.Join(root, "sites-available", "app")))
	writeFile(t, filepath.Join(root, "sites-available", "app"), "server { foo bar; }\n")
	writeFile(t, filepath.Join(root, "sites-available", "previous"), "server {}\n")
	link := filepath.Join(root, "sites-enabled", "app")
	symlink(t, "../sites-available/previous", link)

	if err := svc.SetVHostEnabled(context.Background(), "app", true); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("enable returned %v", err)
	}
	if target, err := os.Readlink(link); err != nil || target != "../sites-available/previous" {
		t.Fatalf("link = %q (%v), want the one it replaced", target, err)
	}
}

// An enable of a site already served through a relative link changes nothing
// and records nothing; comparing the link's text made it rewrite the link.
func TestEnablingASiteServedThroughARelativeLinkChangesNothing(t *testing.T) {
	svc, root := debianTree(t)
	log := &linkChanges{}
	svc.SetRecorder(log)
	nginxShim(t, "exit 0")
	writeFile(t, filepath.Join(root, "sites-available", "app"), "server {}\n")
	link := filepath.Join(root, "sites-enabled", "app")
	symlink(t, "../sites-available/app", link)
	if err := svc.SetVHostEnabled(context.Background(), "app", true); err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(link); target != "../sites-available/app" || len(log.changes) != 0 {
		t.Fatalf("link = %q, recorded %v", target, log.actions())
	}
}

// Another site may use an upstream or a zone this one defines, so taking its
// link out can break the configuration. The link goes back as it was.
func TestADisableThatBreaksAnotherSiteIsUndone(t *testing.T) {
	svc, root := debianTree(t)
	log := &linkChanges{}
	svc.SetRecorder(log)
	link := filepath.Join(root, "sites-enabled", "pool")
	nginxShim(t, fmt.Sprintf(`if [ ! -e '%s' ]; then echo 'nginx: [emerg] host not found in upstream "backend" in %s:7' >&2; exit 1; fi; exit 0`,
		link, filepath.Join(root, "sites-available", "app")))
	writeFile(t, filepath.Join(root, "sites-available", "pool"), "upstream backend { server 127.0.0.1:3000; }\n")
	symlink(t, "../sites-available/pool", link)

	err := svc.SetVHostEnabled(context.Background(), "pool", false)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("disable returned %v", err)
	}
	if !strings.Contains(FailureHeadline(refused.Validation), `host not found in upstream "backend"`) {
		t.Errorf("headline = %q", FailureHeadline(refused.Validation))
	}
	if target, err := os.Readlink(link); err != nil || target != "../sites-available/pool" {
		t.Fatalf("link = %q (%v), want it back as it was", target, err)
	}
	if len(log.changes) != 0 {
		t.Errorf("a refused disable was recorded: %v", log.actions())
	}
}

// Switching sites off is how a broken configuration is fixed, so a disable
// out of a configuration nginx was already refusing stands. An enable into
// one does not: nginx could not load it.
func TestADisableOutOfABrokenConfigurationStands(t *testing.T) {
	svc, root := debianTree(t)
	log := &linkChanges{}
	svc.SetRecorder(log)
	nginxShim(t, fmt.Sprintf(`echo 'nginx: [emerg] open() "%s" failed (2: No such file or directory) in %s:20' >&2; exit 1`,
		filepath.Join(root, "sites-enabled", "ghost"), filepath.Join(root, "nginx.conf")))
	writeFile(t, filepath.Join(root, "sites-available", "app"), "server {}\n")
	writeFile(t, filepath.Join(root, "sites-available", "other"), "server {}\n")
	link := filepath.Join(root, "sites-enabled", "app")
	symlink(t, "../sites-available/app", link)

	if err := svc.SetVHostEnabled(context.Background(), "app", false); err != nil {
		t.Fatalf("disable returned %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("the link is back: %v", err)
	}
	if err := svc.SetVHostEnabled(context.Background(), "other", true); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("an enable into a broken configuration returned %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "sites-enabled", "other")); !os.IsNotExist(err) {
		t.Fatal("the refused enable left its link")
	}
	if want := []ChangeAction{ChangeDisable}; !slices.Equal(log.actions(), want) {
		t.Errorf("recorded %v, want %v", log.actions(), want)
	}
}

// A copy in sites-enabled is a configuration of its own; removing it to
// "disable" the site deleted it.
func TestDisablingLeavesAFileInSitesEnabledAlone(t *testing.T) {
	svc, root := debianTree(t)
	nginxShim(t, "exit 0")
	writeFile(t, filepath.Join(root, "sites-available", "app"), "server {}\n")
	copied := filepath.Join(root, "sites-enabled", "app")
	writeFile(t, copied, "server { listen 8080; }\n")
	if err := svc.SetVHostEnabled(context.Background(), "app", false); err == nil || !strings.Contains(err.Error(), "is a file") {
		t.Fatalf("disable returned %v", err)
	}
	if b, err := os.ReadFile(copied); err != nil || string(b) != "server { listen 8080; }\n" {
		t.Fatalf("the copy is gone or changed: %q %v", b, err)
	}
}

func TestRemoveVHostLinkTakesOutOnlyLinksNoSiteOwns(t *testing.T) {
	svc, root := debianTree(t)
	log := &linkChanges{}
	svc.SetRecorder(log)
	nginxShim(t, "exit 0")
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	symlink(t, "/nonexistent/jd-test/ghost", enabled("ghost"))
	writeFile(t, filepath.Join(root, "custom", "g.conf"), "server { listen 81; }\n")
	symlink(t, "../custom/g.conf", enabled("g"))
	writeFile(t, enabled("copy"), "server {}\n")
	writeFile(t, filepath.Join(root, "sites-available", "app"), "server {}\n")
	symlink(t, "../sites-available/app", enabled("app"))
	ctx := context.Background()

	if err := svc.RemoveVHostLink(ctx, "ghost"); err != nil {
		t.Fatalf("a dangling link: %v", err)
	}
	if err := svc.RemoveVHostLink(ctx, "g"); err != nil {
		t.Fatalf("a link to a file elsewhere: %v", err)
	}
	for _, name := range []string{"ghost", "g"} {
		if _, err := os.Lstat(enabled(name)); !os.IsNotExist(err) {
			t.Errorf("%s is still there", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "custom", "g.conf")); err != nil {
		t.Errorf("the file behind the link went with it: %v", err)
	}
	for name, why := range map[string]string{
		"copy":    "is a file",
		"app":     "disable the site",
		"nothing": "nothing called",
		"../app":  "invalid",
		"":        "invalid",
	} {
		if err := svc.RemoveVHostLink(ctx, name); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("RemoveVHostLink(%q) = %v, want %q", name, err, why)
		}
	}
	if _, err := os.Stat(enabled("copy")); err != nil {
		t.Error("the copy was removed")
	}
	if _, err := os.Lstat(enabled("app")); err != nil {
		t.Error("the site's own link was removed")
	}
	want := []Change{
		{Path: enabled("ghost"), Action: ChangeDisable, BeforeExisted: true},
		{Path: filepath.Join(root, "custom", "g.conf"), Action: ChangeDisable, BeforeExisted: true,
			Before: []byte("server { listen 81; }\n"), After: []byte("server { listen 81; }\n")},
	}
	if !reflect.DeepEqual(log.changes, want) {
		t.Errorf("recorded %+v\nwant %+v", log.changes, want)
	}
}

func TestRemoveVHostLinkIsUndoneWhenNginxRefuses(t *testing.T) {
	svc, root := debianTree(t)
	link := filepath.Join(root, "sites-enabled", "g")
	nginxShim(t, fmt.Sprintf(`if [ ! -e '%s' ]; then echo 'nginx: [emerg] zone "api" is unknown in %s:4' >&2; exit 1; fi; exit 0`,
		link, filepath.Join(root, "sites-available", "app")))
	writeFile(t, filepath.Join(root, "custom", "g.conf"), "limit_req_zone $binary_remote_addr zone=api:1m rate=1r/s;\n")
	symlink(t, "../custom/g.conf", link)
	if err := svc.RemoveVHostLink(context.Background(), "g"); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("got %v", err)
	}
	if target, _ := os.Readlink(link); target != "../custom/g.conf" {
		t.Fatalf("link = %q", target)
	}
}

func TestFailureHeadline(t *testing.T) {
	cases := []struct {
		res  ValidationResult
		want string
	}{
		{ValidationResult{Diagnostics: []Diagnostic{
			{Level: "warn", Message: `conflicting server name "a" on 0.0.0.0:80, ignored`},
			{Level: "emerg", Message: `unknown directive "foo"`, File: "/etc/nginx/sites-available/app", Line: 3},
		}}, `unknown directive "foo" in /etc/nginx/sites-available/app:3`},
		{ValidationResult{Diagnostics: []Diagnostic{{Level: "emerg", Message: "no events section"}}}, "no events section"},
		{ValidationResult{Output: "nginx: something unparsed\nsecond line"}, "nginx: something unparsed"},
		{ValidationResult{}, "the configuration test failed"},
	}
	for _, tc := range cases {
		if got := FailureHeadline(&tc.res); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

// The same changes against the real nginx: a site it cannot load is never
// left linked, a link to nothing is what makes it refuse every reload, and
// taking that link out is what makes it load again.
func TestLiveLinkChangesKeepNginxLoadable(t *testing.T) {
	root := liveNginx(t)
	svc := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	writeFile(t, available("broken"), "server {\n    listen 127.0.0.1:18097;\n    foo bar;\n}\n")
	writeFile(t, available("good"), "server {\n    listen 127.0.0.1:18098;\n    server_name good.test;\n    return 204;\n}\n")

	err := svc.SetVHostEnabled(ctx, "broken", true)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("enabling a broken site: %v", err)
	}
	if headline := FailureHeadline(refused.Validation); headline != `unknown directive "foo" in `+available("broken")+":3" {
		t.Errorf("headline = %q", headline)
	}
	if res := svc.Test(ctx, KindNginx); !res.Valid {
		t.Fatalf("nginx no longer loads after a refused enable:\n%s", res.Output)
	}

	symlink(t, available("missing"), filepath.Join(root, "sites-enabled", "ghost"))
	res := svc.Test(ctx, KindNginx)
	if res.Valid || !strings.Contains(res.Output, "No such file or directory") {
		t.Fatalf("a dangling link should fail the test:\n%s", res.Output)
	}
	if ghost := listed(t, svc.nginxVHosts(), "sites-enabled", "ghost"); ghost.Broken != "dangling" || ghost.Enabled {
		t.Errorf("ghost listed as %+v", ghost)
	}
	if err := svc.SetVHostEnabled(ctx, "good", true); !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("an enable into a configuration nginx refuses: %v", err)
	}
	if err := svc.RemoveVHostLink(ctx, "ghost"); err != nil {
		t.Fatal(err)
	}
	if res := svc.Test(ctx, KindNginx); !res.Valid {
		t.Fatalf("removing the dangling link did not make nginx load:\n%s", res.Output)
	}
	if err := svc.SetVHostEnabled(ctx, "good", true); err != nil {
		t.Fatal(err)
	}
	if good := listed(t, svc.nginxVHosts(), "sites-available", "good"); !good.Enabled || good.Broken != "" {
		t.Errorf("good listed as %+v", good)
	}
}
