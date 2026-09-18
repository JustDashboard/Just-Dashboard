package proxysvc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The Sites list read every block opener in a Caddyfile as a site address,
// so a file using handle, header or tls blocks listed "handle" and "header"
// beside its real names. Only a block opened at the top level is a site.
func TestParseCaddyfileReadsOnlyTopLevelAddresses(t *testing.T) {
	names, upstreams := parseCaddyfile(`
{
    email ops@example.com
}

# Two names on one block, separated the way Caddy allows.
app.example.com, www.app.example.com {
    encode gzip
    header {
        X-Frame-Options SAMEORIGIN
    }
    handle /api/* {
        reverse_proxy 127.0.0.1:4000
    }
    reverse_proxy 127.0.0.1:3000 {
        lb_policy round_robin
    }
    tls {
        protocols tls1.2 tls1.3
    }
}

status.example.com {
    respond "ok"
}
`)
	want := []string{"app.example.com", "www.app.example.com", "status.example.com"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
	if len(upstreams) != 2 || upstreams[0] != "127.0.0.1:4000" || upstreams[1] != "127.0.0.1:3000" {
		t.Fatalf("upstreams = %v", upstreams)
	}
}

// The config editor's read route is held by every signed-in account, and a
// password file inside the nginx directory is not configuration: its bcrypt
// hashes are crackable offline. Both the dashboard's own files and the
// hand-made .htpasswd are refused, and ordinary sites are still readable.
func TestReadConfigRefusesPasswordFiles(t *testing.T) {
	dir := t.TempDir()
	svc := New(dir, filepath.Join(t.TempDir(), "Caddyfile"))
	if _, err := svc.SetAuthUser("staging", "admin", "correcthorsebattery"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".htpasswd"), []byte("bob:$2y$05$hash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sites-available"), 0o755); err != nil {
		t.Fatal(err)
	}
	site := filepath.Join(dir, "sites-available", "app")
	if err := os.WriteFile(site, []byte("server { listen 80; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(dir, "jd-auth", "staging"),
		filepath.Join(dir, ".htpasswd"),
	} {
		_, err := svc.ReadConfig(path)
		if !errors.Is(err, ErrProtectedFile) {
			t.Fatalf("%s: err = %v, want ErrProtectedFile", path, err)
		}
	}
	if content, err := svc.ReadConfig(site); err != nil || content == "" {
		t.Fatalf("an ordinary site must still be readable: %q %v", content, err)
	}
}

// `nginx -t` cannot see a file outside the include tree, so a validation of
// a disabled site's config is a test of nothing. The result says so, and says
// nothing for a file that is included.
func TestIncludeNoteSaysWhenTheTestCouldNotSeeTheFile(t *testing.T) {
	dir := t.TempDir()
	svc := New(dir, filepath.Join(t.TempDir(), "Caddyfile"))
	for _, sub := range []string{"sites-available", "sites-enabled", "conf.d"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linked := filepath.Join(dir, "sites-available", "live")
	unlinked := filepath.Join(dir, "sites-available", "parked")
	for _, p := range []string{linked, unlinked} {
		if err := os.WriteFile(p, []byte("server {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(linked, filepath.Join(dir, "sites-enabled", "live")); err != nil {
		t.Fatal(err)
	}

	if note := svc.includeNote(linked); note != "" {
		t.Fatalf("an enabled site should carry no note, got %q", note)
	}
	if note := svc.includeNote(unlinked); note == "" {
		t.Fatal("a site with no sites-enabled link must say the test could not see it")
	}
	if note := svc.includeNote(filepath.Join(dir, "conf.d", "app.conf")); note != "" {
		t.Fatalf("a .conf under conf.d is included, got %q", note)
	}
	if note := svc.includeNote(filepath.Join(dir, "conf.d", "app.disabled")); note == "" {
		t.Fatal("a conf.d file without .conf is not read by nginx")
	}
	if note := svc.includeNote(filepath.Join(dir, "somewhere", "else")); note != "" {
		t.Fatalf("an unknown location is not a claim either way, got %q", note)
	}
}

// Enabling a site whose sites-enabled link already exists but points at the
// wrong file used to return success and change nothing, so the switch said
// on while nginx read the other file. The stale link is replaced.
func TestSetVHostEnabledReplacesAStaleLink(t *testing.T) {
	dir := t.TempDir()
	svc := New(dir, filepath.Join(t.TempDir(), "Caddyfile"))
	for _, sub := range []string{"sites-available", "sites-enabled"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	site := filepath.Join(dir, "sites-available", "app")
	if err := os.WriteFile(site, []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "sites-enabled", "app")
	if err := os.Symlink(filepath.Join(dir, "sites-available", "gone"), link); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetVHostEnabled(t.Context(), "app", true); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(link)
	if err != nil || target != site {
		t.Fatalf("link = %q (%v), want %q", target, err, site)
	}
	// And enabling twice is still a no-op rather than an error.
	if err := svc.SetVHostEnabled(t.Context(), "app", true); err != nil {
		t.Fatal(err)
	}
}

func TestCertificateNameForGenericDirectories(t *testing.T) {
	cases := map[string]string{
		"/etc/letsencrypt/live/app.example.com/fullchain.pem": "app.example.com",
		"/etc/ssl/just-dashboard/shop/fullchain.pem":          "shop",
		"/etc/nginx/ssl/example.crt":                          "example",
		"/etc/ssl/certs/wildcard.example.com.pem":             "wildcard.example.com",
		"/etc/pki/tls/site.pem":                               "site",
	}
	for path, want := range cases {
		if got := certificateName(path); got != want {
			t.Errorf("certificateName(%q) = %q, want %q", path, got, want)
		}
	}
}

// The list answers "which sites break when this expires": a certificate
// reached through certbot's directory and again through a site's
// ssl_certificate is one entry that names the site, not two entries.
func TestListCertificatesNamesTheSitesThatUseEach(t *testing.T) {
	live := t.TempDir()
	imported := t.TempDir()
	certPEM, _, _ := selfSigned(t, "app.example.com", timeIn(90))
	lineage := filepath.Join(live, "app.example.com")
	if err := os.MkdirAll(lineage, 0o755); err != nil {
		t.Fatal(err)
	}
	// certbot's live/ holds symlinks into archive/; the site names the
	// symlink and the certificate is one file either way.
	archive := filepath.Join(t.TempDir(), "fullchain1.pem")
	if err := os.WriteFile(archive, []byte(certPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(archive, filepath.Join(lineage, "fullchain.pem")); err != nil {
		t.Fatal(err)
	}
	vhosts := []VHost{
		{Name: "app", CertPath: filepath.Join(lineage, "fullchain.pem")},
		{Name: "app-staging", CertPath: archive},
		{Name: "plain"},
	}
	certs := listCertificates(live, imported, vhosts)
	if len(certs) != 1 {
		t.Fatalf("expected one certificate, got %d: %+v", len(certs), certs)
	}
	if certs[0].Source != "certbot" || len(certs[0].UsedBy) != 2 ||
		certs[0].UsedBy[0] != "app" || certs[0].UsedBy[1] != "app-staging" {
		t.Fatalf("certificate = %+v", certs[0])
	}
}
