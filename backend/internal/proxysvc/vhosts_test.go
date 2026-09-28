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
	"time"
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
	// Served through a numbered link, the way 00-default -> default is: the
	// site is serving, and the link is its, not a site of its own.
	writeFile(t, available("numbered"), fmt.Sprintf(site, "numbered"))
	symlink(t, "../sites-available/numbered", enabled("00-numbered"))
	// A site file kept in an application's repository, outside the proxy's
	// directories: the editor will not open it, the switch still moves its
	// link.
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(repo, "repo.conf")
	writeFile(t, outside, fmt.Sprintf(site, "repo"))
	symlink(t, outside, available("repo"))
	symlink(t, "../sites-available/repo", enabled("repo"))
	// http-level configuration in conf.d is not a site.
	writeFile(t, filepath.Join(root, "conf.d", "log.conf"), "log_format vbfmt '$remote_addr $request';\nmap $http_upgrade $connection_upgrade { default upgrade; '' close; }\n")

	hosts := svc.nginxVHosts()
	if len(hosts) != 13 {
		t.Fatalf("listed %d entries: %s", len(hosts), describeHosts(hosts))
	}

	cases := []struct {
		layout, name string
		want         VHost
	}{
		// sites-enabled/renamed, stale for renamed, serves linked a second time.
		{"sites-available", "linked", VHost{Path: available("linked"), EnabledPath: enabled("linked"), Enabled: true, FormEditable: true, LinkedAs: []string{"renamed"}}},
		{"sites-available", "numbered", VHost{Path: available("numbered"), EnabledPath: enabled("numbered"), Enabled: true, FormEditable: true, LinkedAs: []string{"00-numbered"}}},
		{"sites-available", "repo", VHost{Path: available("repo"), EnabledPath: enabled("repo"), Enabled: true, ResolvesTo: outside}},
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
			LinkedAs: got.LinkedAs, ResolvesTo: got.ResolvesTo,
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
		// Caddy's one-site form: no braces, the address on the first line.
		{"one site without braces", "example.com\nreverse_proxy localhost:8080\n", true},
		{"one plain site without braces", "http://example.com\nreverse_proxy localhost:8080\n", false},
		{"without braces after global options", "{\n\temail ops@example.com\n}\nexample.com\nreverse_proxy app:80\n", true},
		{"without braces, tls on a bare port", ":9443\ntls internal\nrespond ok\n", true},
		{"without braces, a nested block", "example.com\nhandle /api/* {\n\treverse_proxy api:80\n}\nreverse_proxy app:80\n", true},
		// Forcing HTTPS by hand: an http:// block that only redirects.
		{"an http block that only redirects", "http://example.com {\n\tredir https://{host}{uri} permanent\n}\nexample.com {\n\treverse_proxy app:80\n}\n", true},
		{"a one-line redirect block", "http://example.com { redir https://{host}{uri} }\nexample.com {\n\treverse_proxy app:80\n}\n", true},
		{"a redirect behind a matcher", "http://example.com {\n\tredir * https://example.com{uri}\n}\nexample.com {\n\trespond ok\n}\n", true},
		{"an http block that also serves", "http://example.com {\n\tredir /old https://example.com/new\n\treverse_proxy app:80\n}\nexample.com {\n\treverse_proxy app:80\n}\n", false},
		{"a redirect to plain http", "http://a.example.com {\n\tredir http://b.example.com{uri}\n}\n", false},
		{"a one-line plain site", "http://example.com { reverse_proxy app:80 }\n", false},
		// An address list carried onto the next lines by trailing commas.
		{"an address list over two lines", "a.example.com,\nb.example.com {\n\treverse_proxy app:80\n}\n", true},
		{"a plain site after an address list over two lines", "a.example.com,\nb.example.com {\n\treverse_proxy app:80\n}\nhttp://c.example.com {\n\treverse_proxy other:80\n}\n", false},
		{"a plain address on the list's next line", "a.example.com,\n# the old name\n\nhttp://b.example.com {\n\treverse_proxy app:80\n}\n", false},
		{"without braces, an address list over two lines", "a.example.com,\nb.example.com\n\nreverse_proxy app:80\n", true},
		{"without braces, a plain address on the list's next line", "a.example.com,\nhttp://b.example.com\nreverse_proxy app:80\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, tls := parseCaddyfile(tc.content); tls != tc.tls {
				t.Errorf("tls = %v, want %v", tls, tc.tls)
			}
		})
	}
}

// Caddy serves a and b on :443 and c on :80 from this file (caddy:2-alpine,
// caddy adapt). Read line by line, `a.example.com,` began the one-site form
// and the two blocks under it were counted as its directives.
func TestParseCaddyfileReadsAnAddressListOverSeveralLines(t *testing.T) {
	names, upstreams, tls := parseCaddyfile("a.example.com,\nb.example.com {\n\treverse_proxy app:80\n}\nhttp://c.example.com {\n\treverse_proxy other:80\n}\n")
	if want := []string{"a.example.com", "b.example.com", "c.example.com"}; !slices.Equal(names, want) {
		t.Errorf("names = %q, want %q", names, want)
	}
	if want := []string{"app:80", "other:80"}; !slices.Equal(upstreams, want) {
		t.Errorf("upstreams = %q, want %q", upstreams, want)
	}
	if tls {
		t.Error("a Caddyfile serving c.example.com over plain HTTP reads as TLS")
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
	// Without the link nginx loads, so the error is the site's own and says
	// nothing more.
	if refused.Reason() != FailureHeadline(refused.Validation) {
		t.Errorf("reason = %q", refused.Reason())
	}
	if len(log.changes) != 0 {
		t.Errorf("a refused enable was recorded: %v", log.actions())
	}
}

// nginx stops at its first error, so an enable into a configuration it
// already refuses was turned away with another file's error, which read as
// the site's. The refusal now says the error is there without the site, and
// only when it is: a site whose own error comes first is refused by that.
func TestARefusedEnableSaysWhenNginxAlreadyRefusedTheConfiguration(t *testing.T) {
	svc, root := debianTree(t)
	other := filepath.Join(root, "sites-available", "other")
	link := filepath.Join(root, "sites-enabled", "broken")
	nginxShim(t, fmt.Sprintf(`if [ -e '%[1]s' ]; then echo 'nginx: [emerg] unknown directive "foo" in %[1]s:3' >&2; exit 1; fi
echo 'nginx: [emerg] unknown directive "bar" in %[2]s:7' >&2
exit 1`, link, other))
	writeFile(t, filepath.Join(root, "sites-available", "fine"), "server {}\n")
	writeFile(t, filepath.Join(root, "sites-available", "broken"), "server { foo bar; }\n")
	writeFile(t, other, "server { bar; }\n")
	ctx := context.Background()

	err := svc.SetVHostEnabled(ctx, "fine", true)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("enable returned %v", err)
	}
	want := `nginx already refuses the configuration without fine: unknown directive "bar" in ` + other + ":7"
	if refused.Reason() != want || err.Error() != want {
		t.Errorf("reason = %q\nerror = %q\nwant %q", refused.Reason(), err, want)
	}
	if _, err := os.Lstat(filepath.Join(root, "sites-enabled", "fine")); !os.IsNotExist(err) {
		t.Fatalf("the refused link is still there: %v", err)
	}

	err = svc.SetVHostEnabled(ctx, "broken", true)
	if !errors.As(err, &refused) {
		t.Fatalf("enable returned %v", err)
	}
	if want := `unknown directive "foo" in ` + filepath.Join(root, "sites-available", "broken") + ":3"; refused.Reason() != want {
		t.Errorf("reason = %q, want %q", refused.Reason(), want)
	}
}

// A site that clashes with another — a second default server, an upstream
// both define — is refused in whichever of the two nginx reads second, which
// can be the other one, and read as that file's fault. Without the site nginx
// loads, so the refusal says the site is what nginx will not take, and says
// it too of an error that names no file.
func TestARefusedEnableSaysTheSiteIsWhatNginxWillNotTake(t *testing.T) {
	svc, root := debianTree(t)
	other := filepath.Join(root, "sites-available", "zz")
	clash := filepath.Join(root, "sites-enabled", "aa")
	long := filepath.Join(root, "sites-enabled", "long")
	nginxShim(t, fmt.Sprintf(`if [ -e '%[1]s' ]; then echo 'nginx: [emerg] a duplicate default server for 0.0.0.0:80 in %[2]s:2' >&2; exit 1; fi
if [ -e '%[3]s' ]; then echo 'nginx: [emerg] could not build server_names_hash, you should increase server_names_hash_bucket_size: 64' >&2; exit 1; fi
exit 0`, clash, filepath.Join(root, "sites-enabled", "zz"), long))
	writeFile(t, other, "server {\n    listen 80 default_server;\n}\n")
	symlink(t, "../sites-available/zz", filepath.Join(root, "sites-enabled", "zz"))
	writeFile(t, filepath.Join(root, "sites-available", "aa"), "server {\n    listen 80 default_server;\n}\n")
	writeFile(t, filepath.Join(root, "sites-available", "long"), "server { server_name a-very-long-name.example.com; }\n")
	ctx := context.Background()

	cases := []struct{ name, link, want string }{
		{"aa", clash, "nginx refuses the configuration with aa: a duplicate default server for 0.0.0.0:80 in " + other + ":2"},
		{"long", long, "nginx refuses the configuration with long: could not build server_names_hash, you should increase server_names_hash_bucket_size: 64"},
	}
	for _, c := range cases {
		err := svc.SetVHostEnabled(ctx, c.name, true)
		var refused *RefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("enabling %s returned %v", c.name, err)
		}
		if refused.Reason() != c.want || err.Error() != c.want {
			t.Errorf("reason = %q\nerror = %q\nwant %q", refused.Reason(), err, c.want)
		}
		if _, err := os.Lstat(c.link); !os.IsNotExist(err) {
			t.Errorf("the refused link %s is still there: %v", c.link, err)
		}
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
	// nginx's error is in the file that needed pool; the reason says the
	// configuration was refused without it.
	if want := `nginx refuses the configuration without pool: host not found in upstream "backend" in ` +
		filepath.Join(root, "sites-available", "app") + ":7"; refused.Reason() != want {
		t.Errorf("reason = %q\nwant %q", refused.Reason(), want)
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
	// A numbered link to a site is that site's too: its Disable takes it out.
	symlink(t, "../sites-available/app", enabled("00-app"))
	ctx := context.Background()

	if _, err := svc.RemoveVHostLink(ctx, "ghost", false); err != nil {
		t.Fatalf("a dangling link: %v", err)
	}
	if _, err := svc.RemoveVHostLink(ctx, "g", false); err != nil {
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
		"00-app":  "is how the site app is enabled",
		"nothing": "nothing called",
		"../app":  "invalid",
		"":        "invalid",
	} {
		if _, err := svc.RemoveVHostLink(ctx, name, false); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("RemoveVHostLink(%q) = %v, want %q", name, err, why)
		}
	}
	if _, err := os.Stat(enabled("copy")); err != nil {
		t.Error("the copy was removed")
	}
	for _, name := range []string{"app", "00-app"} {
		if _, err := os.Lstat(enabled(name)); err != nil {
			t.Errorf("the site's link %s was removed", name)
		}
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
	_, err := svc.RemoveVHostLink(context.Background(), "g", false)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("got %v", err)
	}
	if want := `nginx refuses the configuration without sites-enabled/g: zone "api" is unknown in ` +
		filepath.Join(root, "sites-available", "app") + ":4"; refused.Reason() != want {
		t.Errorf("reason = %q\nwant %q", refused.Reason(), want)
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
	err = svc.SetVHostEnabled(ctx, "good", true)
	if !errors.As(err, &refused) {
		t.Fatalf("an enable into a configuration nginx refuses: %v", err)
	}
	if want := "nginx already refuses the configuration without good: open() \"" + filepath.Join(root, "sites-enabled", "ghost") + "\" failed"; !strings.HasPrefix(refused.Reason(), want) {
		t.Errorf("reason = %q, want it to start %q", refused.Reason(), want)
	}
	if _, err := svc.RemoveVHostLink(ctx, "ghost", false); err != nil {
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

// Against the real nginx: a site whose upstream another site proxies to
// cannot be disabled, the refusal names the file that needed it, and both
// links stay as they were.
func TestLiveADisableAnotherSiteNeedsIsRefusedWithItsReason(t *testing.T) {
	root := liveNginx(t)
	svc := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	writeFile(t, available("pool"), "upstream jd_lane_b_pool {\n    server 127.0.0.1:18096;\n}\n")
	writeFile(t, available("app"), "server {\n    listen 127.0.0.1:18095;\n    location / {\n        proxy_pass http://jd_lane_b_pool;\n    }\n}\n")
	for _, name := range []string{"pool", "app"} {
		if err := svc.SetVHostEnabled(ctx, name, true); err != nil {
			t.Fatalf("enabling %s: %v", name, err)
		}
	}

	err := svc.SetVHostEnabled(ctx, "pool", false)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("disabling the pool: %v", err)
	}
	want := `nginx refuses the configuration without pool: host not found in upstream "jd_lane_b_pool" in ` + available("app") + ":4"
	if refused.Reason() != want {
		t.Errorf("reason = %q\nwant %q", refused.Reason(), want)
	}
	if pool := listed(t, svc.nginxVHosts(), "sites-available", "pool"); !pool.Enabled {
		t.Error("the refused disable left pool out")
	}
	if res := svc.Test(ctx, KindNginx); !res.Valid {
		t.Fatalf("nginx no longer loads after a refused disable:\n%s", res.Output)
	}

	if err := svc.SetVHostEnabled(ctx, "app", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetVHostEnabled(ctx, "pool", false); err != nil {
		t.Fatalf("with nothing proxying to it the pool comes out: %v", err)
	}
}

// Against the real nginx: a site that clashes with an enabled one — a second
// default server on its address, an upstream name both define — is refused
// in the enabled site's file, which nginx reads second. The refusal says the
// site is what nginx will not take, nothing stays linked, and an enable
// refused in the site's own file carries no lead.
func TestLiveAnEnableThatClashesWithAnotherSiteSaysSo(t *testing.T) {
	root := liveNginx(t)
	svc := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	// The enabled halves sort after the ones being switched, so nginx
	// finds each clash in the file that was there first.
	writeFile(t, available("zz"), "server {\n    listen 127.0.0.1:18097 default_server;\n}\n")
	writeFile(t, available("aa"), "server {\n    listen 127.0.0.1:18097 default_server;\n}\n")
	writeFile(t, available("pool-b"), "upstream jd_lane_b_dup {\n    server 127.0.0.1:18098;\n}\n")
	writeFile(t, available("pool-a"), "upstream jd_lane_b_dup {\n    server 127.0.0.1:18099;\n}\n")
	writeFile(t, available("own"), "server {\n    listen 127.0.0.1:18097;\n    foo bar;\n}\n")
	for _, name := range []string{"zz", "pool-b"} {
		if err := svc.SetVHostEnabled(ctx, name, true); err != nil {
			t.Fatalf("enabling %s: %v", name, err)
		}
	}

	cases := []struct{ name, want string }{
		{"aa", "nginx refuses the configuration with aa: a duplicate default server for 127.0.0.1:18097 in " + available("zz") + ":2"},
		{"pool-a", `nginx refuses the configuration with pool-a: duplicate upstream "jd_lane_b_dup" in ` + available("pool-b") + ":1"},
		{"own", `unknown directive "foo" in ` + available("own") + ":3"},
	}
	for _, c := range cases {
		err := svc.SetVHostEnabled(ctx, c.name, true)
		var refused *RefusedError
		if !errors.As(err, &refused) {
			t.Fatalf("enabling %s: %v", c.name, err)
		}
		if refused.Reason() != c.want {
			t.Errorf("reason = %q\nwant %q", refused.Reason(), c.want)
		}
		if _, err := os.Lstat(enabled(c.name)); !os.IsNotExist(err) {
			t.Errorf("the refused %s is still linked: %v", c.name, err)
		}
	}
	if res := svc.Test(ctx, KindNginx); !res.Valid {
		t.Fatalf("nginx no longer loads after the refused enables:\n%s", res.Output)
	}
}

// The one-site form and a redirect block beside its site name each domain
// once, without a scheme the listing's TLS flag already says: "Open site"
// opened https://http://example.com.
func TestParseCaddyfileNamesEachSiteOnce(t *testing.T) {
	cases := []struct {
		name, content    string
		names, upstreams []string
	}{
		{"one site without braces", "example.com\nreverse_proxy localhost:8080\n",
			[]string{"example.com"}, []string{"localhost:8080"}},
		{"without braces, a nested block",
			"{\n\temail ops@example.com\n}\nexample.com, www.example.com\nhandle /api/* {\n\treverse_proxy api:9000\n}\nreverse_proxy app:3000\n",
			[]string{"example.com", "www.example.com"}, []string{"api:9000", "app:3000"}},
		{"a redirect block and its site",
			"http://example.com {\n\tredir https://{host}{uri} permanent\n}\nexample.com {\n\treverse_proxy app:80\n}\n",
			[]string{"example.com"}, []string{"app:80"}},
		{"a snippet before the one site", "(common) {\n\tencode gzip\n}\nexample.com\nimport common\n",
			[]string{"example.com"}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			names, upstreams, _ := parseCaddyfile(tc.content)
			if !slices.Equal(names, tc.names) || !slices.Equal(upstreams, tc.upstreams) {
				t.Errorf("names = %q upstreams = %q, want %q %q", names, upstreams, tc.names, tc.upstreams)
			}
		})
	}
}

// A conf.d file that declares no server — a log_format, a map, a zone — is
// configuration other sites lean on, not a site. It was listed as "Default
// host", always on, with a Delete that broke the next reload.
func TestConfDListsOnlyFilesThatDeclareAServer(t *testing.T) {
	for _, debian := range []bool{true, false} {
		t.Run(fmt.Sprintf("debian=%v", debian), func(t *testing.T) {
			root := t.TempDir()
			if debian {
				for _, dir := range []string{"sites-available", "sites-enabled"} {
					if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			confd := func(name string) string { return filepath.Join(root, "conf.d", name) }
			writeFile(t, confd("site.conf"), "# log_format only\nserver {\n    listen 80;\n}\n")
			writeFile(t, confd("compact.conf"), "server{listen 81;}\n")
			writeFile(t, confd("log.conf"), "log_format main '$remote_addr';\n")
			writeFile(t, confd("zones.conf"), "limit_req_zone $binary_remote_addr zone=api:10m rate=5r/s;\nupstream backend {\n    server 127.0.0.1:3000;\n}\n")
			writeFile(t, confd("commented.conf"), "# server {\n#     listen 80;\n# }\n")
			// nginx refuses a file it cannot parse, and the operator has to
			// see which one.
			writeFile(t, confd("broken.conf"), "server {\n    listen 80;\n")
			hosts := New(root, filepath.Join(root, "Caddyfile")).nginxVHosts()
			names := []string{}
			for _, h := range hosts {
				names = append(names, h.Name)
			}
			slices.Sort(names)
			if want := []string{"broken.conf", "compact.conf", "site.conf"}; !slices.Equal(names, want) {
				t.Errorf("listed %q, want %q", names, want)
			}
		})
	}
}

// A site served through a link under another name — 00-default ->
// ../sites-available/default — read "disabled", its Enable loaded it a
// second time, and its Disable removed nothing.
func TestTheSwitchActsOnEveryLinkServingTheSite(t *testing.T) {
	svc, root := debianTree(t)
	log := &linkChanges{}
	svc.SetRecorder(log)
	nginxShim(t, "exit 0")
	ctx := context.Background()
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	writeFile(t, filepath.Join(root, "sites-available", "default"), "server {}\n")
	symlink(t, "../sites-available/default", enabled("00-default"))

	if got := listed(t, svc.nginxVHosts(), "sites-available", "default"); !got.Enabled || !slices.Equal(got.LinkedAs, []string{"00-default"}) {
		t.Fatalf("listed as %+v", got)
	}
	if err := svc.SetVHostEnabled(ctx, "default", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(enabled("default")); !os.IsNotExist(err) {
		t.Fatalf("the enable linked a site already served, loading it twice: %v", err)
	}
	if len(log.changes) != 0 {
		t.Fatalf("an enable that changed nothing was recorded: %v", log.actions())
	}

	// Served both ways: the disable takes out both.
	symlink(t, "../sites-available/default", enabled("default"))
	if err := svc.SetVHostEnabled(ctx, "default", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "00-default"} {
		if _, err := os.Lstat(enabled(name)); !os.IsNotExist(err) {
			t.Errorf("sites-enabled/%s is still there: %v", name, err)
		}
	}
	if want := []ChangeAction{ChangeDisable}; !slices.Equal(log.actions(), want) {
		t.Errorf("recorded %v, want %v", log.actions(), want)
	}
	if got := listed(t, svc.nginxVHosts(), "sites-available", "default"); got.Enabled || len(got.LinkedAs) != 0 {
		t.Errorf("after the disable, listed as %+v", got)
	}
}

// Enabling a site whose name in sites-enabled links to another file points
// that link at the site's own. The page warns when that takes the other file
// out of nginx, so the listing says whether anything else still reads it.
func TestAStaleLinkSaysWhetherItsTargetHasAnotherWayIn(t *testing.T) {
	svc, root := debianTree(t)
	nginxShim(t, "exit 0")
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alone", "other", "kept", "both", "confd", "repo", "via"} {
		writeFile(t, available(name), "server {}\n")
	}
	// Only the stale link serves other: enabling alone takes it out.
	symlink(t, "../sites-available/other", enabled("alone"))
	// both has its own link as well.
	symlink(t, "../sites-available/both", enabled("kept"))
	symlink(t, "../sites-available/both", enabled("both"))
	// nginx reads conf.d/*.conf whatever sites-enabled holds.
	writeFile(t, filepath.Join(root, "conf.d", "shared.conf"), "server {}\n")
	symlink(t, "../conf.d/shared.conf", enabled("confd"))
	// A file outside the nginx directory, reached only through the link, and
	// one conf.d links in too.
	writeFile(t, filepath.Join(repo, "app.conf"), "server {}\n")
	symlink(t, filepath.Join(repo, "app.conf"), enabled("repo"))
	writeFile(t, filepath.Join(repo, "shared.conf"), "server {}\n")
	symlink(t, filepath.Join(repo, "shared.conf"), filepath.Join(root, "conf.d", "repo.conf"))
	symlink(t, filepath.Join(repo, "shared.conf"), enabled("via"))

	hosts := svc.nginxVHosts()
	for name, want := range map[string]bool{"alone": false, "kept": true, "confd": true, "repo": false, "via": true} {
		got := listed(t, hosts, "sites-available", name)
		if got.Broken != "stale" || got.TargetServedElsewhere != want {
			t.Errorf("%s: broken %q, targetServedElsewhere %v, want stale and %v", name, got.Broken, got.TargetServedElsewhere, want)
		}
	}
	if got := listed(t, hosts, "sites-available", "other"); !got.Enabled {
		t.Fatalf("other is served through sites-enabled/alone but listed as %+v", got)
	}

	// What the warning is about: the enable leaves other with no way in.
	if err := svc.SetVHostEnabled(context.Background(), "alone", true); err != nil {
		t.Fatal(err)
	}
	if got := listed(t, svc.nginxVHosts(), "sites-available", "other"); got.Enabled || len(got.LinkedAs) != 0 {
		t.Errorf("after enabling alone, other is listed as %+v", got)
	}
}

// When nginx refuses the configuration without them, every link the disable
// took out comes back as it was.
func TestARefusedDisablePutsBackEveryLink(t *testing.T) {
	svc, root := debianTree(t)
	own := filepath.Join(root, "sites-enabled", "pool")
	alias := filepath.Join(root, "sites-enabled", "00-pool")
	site := filepath.Join(root, "sites-available", "pool")
	nginxShim(t, fmt.Sprintf(`if [ ! -e '%s' ] || [ ! -e '%s' ]; then echo 'nginx: [emerg] host not found in upstream "backend" in %s:7' >&2; exit 1; fi; exit 0`,
		own, alias, filepath.Join(root, "sites-available", "app")))
	writeFile(t, site, "upstream backend { server 127.0.0.1:3000; }\n")
	symlink(t, "../sites-available/pool", own)
	symlink(t, site, alias)

	err := svc.SetVHostEnabled(context.Background(), "pool", false)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("disable returned %v", err)
	}
	for link, want := range map[string]string{own: "../sites-available/pool", alias: site} {
		if target, err := os.Readlink(link); err != nil || target != want {
			t.Errorf("%s = %q (%v), want %q", link, target, err, want)
		}
	}
}

// Delete removes sites-enabled/<name> by name, file or link, before its
// backup of the site. A copy there — the configuration nginx was really
// serving — went with no copy kept, a stale link took out the other site it
// served, and a numbered link to the file was left pointing at nothing,
// which stops every reload.
func TestDeleteSiteTakesOutOnlyWhatIsTheSites(t *testing.T) {
	svc, root := debianTree(t)
	available := func(name string) string { return filepath.Join(root, "sites-available", name) }
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	ctx := context.Background()
	served := "server { return 200 'served copy'; }\n"
	writeFile(t, available("copied.test"), "server { return 204; }\n")
	writeFile(t, enabled("copied.test"), served)
	writeFile(t, available("stale.test"), "server {}\n")
	writeFile(t, available("other.test"), "server {}\n")
	symlink(t, "../sites-available/other.test", enabled("stale.test"))
	writeFile(t, available("numbered.test"), "server {}\n")
	symlink(t, "../sites-available/numbered.test", enabled("00-numbered.test"))
	writeFile(t, enabled("only.test"), "server {}\n")
	writeFile(t, filepath.Join(root, "custom", "elsewhere.conf"), "server {}\n")
	symlink(t, "../custom/elsewhere.conf", enabled("elsewhere.test"))

	for name, why := range map[string]string{
		"copied.test":    "sites-enabled/copied.test is a file of its own",
		"stale.test":     "points at " + available("other.test"),
		"numbered.test":  "also enabled as sites-enabled/00-numbered.test",
		"only.test":      "sites-enabled/only.test is a file of its own",
		"elsewhere.test": "not at this site's file",
	} {
		if err := svc.DeleteSite(ctx, name); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("DeleteSite(%q) = %v, want %q", name, err, why)
		}
	}
	if b, err := os.ReadFile(enabled("copied.test")); err != nil || string(b) != served {
		t.Errorf("the served copy is gone or changed: %q %v", b, err)
	}
	for _, file := range []string{available("copied.test"), available("stale.test"), available("numbered.test"), enabled("only.test")} {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("a refused delete removed %s", file)
		}
	}
	for _, link := range []string{enabled("stale.test"), enabled("00-numbered.test"), enabled("elsewhere.test")} {
		if _, err := os.Lstat(link); err != nil {
			t.Errorf("a refused delete removed %s", link)
		}
	}

	// The site's own link goes with it, and so does a link to nothing.
	writeFile(t, available("own.test"), "server {}\n")
	symlink(t, "../sites-available/own.test", enabled("own.test"))
	writeFile(t, available("dangling.test"), "server {}\n")
	symlink(t, available("gone.test"), enabled("dangling.test"))
	for _, name := range []string{"own.test", "dangling.test"} {
		if err := svc.DeleteSite(ctx, name); err != nil {
			t.Errorf("DeleteSite(%q) = %v", name, err)
			continue
		}
		if _, err := os.Lstat(enabled(name)); !os.IsNotExist(err) {
			t.Errorf("sites-enabled/%s is still there", name)
		}
		if _, err := os.Stat(available(name) + ".bak"); err != nil {
			t.Errorf("%s was deleted without its backup", name)
		}
	}
}

// waitForFile waits for path to exist, for up to ten seconds.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	for range 400 {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

// The reload a switch or a link removal asks for runs before the service
// lock is let go. Run after it, its test could see the candidate link of an
// enable being tested at that moment, and call a change that had worked
// "not reloaded" over a site nobody had enabled.
func TestLinkChangesReloadInsideTheServiceLock(t *testing.T) {
	svc, root := debianTree(t)
	gate := t.TempDir()
	nginxShim(t, fmt.Sprintf(`if [ "$1" = "-s" ]; then
  touch '%[1]s/reloading'
  i=0
  while [ ! -e '%[1]s/release' ] && [ $i -lt 400 ]; do sleep 0.025; i=$((i+1)); done
  rm -f '%[1]s/reloading' '%[1]s/release'
fi
exit 0`, gate))
	ctx := context.Background()
	writeFile(t, filepath.Join(root, "sites-available", "app"), "server {}\n")
	symlink(t, "/nonexistent/jd-test/ghost", filepath.Join(root, "sites-enabled", "ghost"))

	for _, change := range []struct {
		name string
		run  func() (*LinkReload, error)
	}{
		{"enable", func() (*LinkReload, error) { return svc.ToggleVHost(ctx, "app", true, true) }},
		{"disable", func() (*LinkReload, error) { return svc.ToggleVHost(ctx, "app", false, true) }},
		{"unlink", func() (*LinkReload, error) { return svc.RemoveVHostLink(ctx, "ghost", true) }},
	} {
		type answer struct {
			reload *LinkReload
			err    error
		}
		done := make(chan answer, 1)
		go func() {
			reload, err := change.run()
			done <- answer{reload, err}
		}()
		waitForFile(t, filepath.Join(gate, "reloading"))
		if svc.mu.TryLock() {
			svc.mu.Unlock()
			t.Errorf("%s: nginx reloaded with the service lock let go", change.name)
		}
		writeFile(t, filepath.Join(gate, "release"), "")
		got := <-done
		if got.err != nil || got.reload == nil || got.reload.Err != nil || !got.reload.Result.Reloaded {
			t.Errorf("%s: %+v %v", change.name, got.reload, got.err)
		}
	}
	// No reload asked for, none run.
	if reload, err := svc.ToggleVHost(ctx, "app", true, false); err != nil || reload != nil {
		t.Errorf("a switch without reload: %+v %v", reload, err)
	}
}
