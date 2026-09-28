package proxysvc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The file certbot writes, in the shape of this host's own renewal
// configuration: the paths at the top, the authority under [renewalparams].
func TestReadRenewalConfReadsCertbotsOwnFile(t *testing.T) {
	dir := t.TempDir()
	root := newAuthority(t, "Test Root", nil)
	leaf, _, _ := root.issue(t, []string{"betbots.site"}, time.Now().Add(60*24*time.Hour))
	writeLineage(t, dir, "betbots.site", productionACME, leaf)

	conf, err := readRenewalConf(filepath.Join(dir, "renewal", "betbots.site.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if conf.Name != "betbots.site" || conf.Server != productionACME || conf.staging() {
		t.Fatalf("conf = %+v", conf)
	}
	if conf.Cert != filepath.Join(dir, "live", "betbots.site", "cert.pem") ||
		conf.FullChain != filepath.Join(dir, "live", "betbots.site", "fullchain.pem") ||
		conf.PrivKey != filepath.Join(dir, "live", "betbots.site", "privkey.pem") {
		t.Fatalf("paths = %+v", conf)
	}
	// A [renewalparams] key never overrides a top-level one of the same name.
	if err := os.WriteFile(filepath.Join(dir, "odd.conf"), []byte("cert = /a/cert.pem\n[renewalparams]\ncert = /b/cert.pem\nserver = "+stagingACME+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	odd, err := readRenewalConf(filepath.Join(dir, "odd.conf"))
	if err != nil || odd.Cert != "/a/cert.pem" || !odd.staging() {
		t.Fatalf("odd = %+v, %v", odd, err)
	}
	if _, err := readRenewalConf(filepath.Join(dir, "missing.conf")); err == nil {
		t.Fatal("a missing file read as a lineage")
	}
}

// `certbot certificates` takes certbot's lock, so while any certbot ran the
// page said certbot managed nothing and nothing renewed it. The lineages are
// read from files now, and whether a timer renews them is asked first — the
// fake certbot here fails every command but --version and plugins, exactly
// as a locked one does.
func TestCertbotStateAnswersWhileCertbotHoldsItsLock(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	log := fakeCertbot(t, "standalone", "webroot")
	fakeSystemctl(t, "certbot.timer")

	root := newAuthority(t, "Test Root", nil)
	app, _, _ := root.issue(t, []string{"app.example.com", "www.app.example.com"}, time.Now().Add(60*24*time.Hour+time.Hour))
	writeLineage(t, dir, "app.example.com", productionACME, app, root.cert)
	stagingRoot := newAuthority(t, "(STAGING) Pretend Pear X1", nil)
	test, _, _ := stagingRoot.issue(t, []string{"test.example.com"}, time.Now().Add(80*24*time.Hour))
	writeLineage(t, dir, "test.example.com", stagingACME, test)

	state := New(t.TempDir(), "").CertbotState(context.Background())
	if !state.Available || state.Version != "certbot 9.9.9" {
		t.Fatalf("available %v version %q", state.Available, state.Version)
	}
	if !state.AutoRenew || state.RenewSource != "certbot.timer" || state.RenewUnit != "" {
		t.Fatalf("renewal = %v %q %q", state.AutoRenew, state.RenewSource, state.RenewUnit)
	}
	if state.Error != "" || len(state.Certs) != 2 {
		t.Fatalf("error %q, certs %+v", state.Error, state.Certs)
	}
	first := state.Certs[0]
	if first.Name != "app.example.com" || strings.Join(first.Domains, " ") != "app.example.com www.app.example.com" ||
		first.DaysLeft != 60 || !first.Valid || first.Staging || first.Serial != app.SerialNumber.Text(16) ||
		first.CertPath != filepath.Join(dir, "live", "app.example.com", "fullchain.pem") {
		t.Fatalf("app lineage = %+v", first)
	}
	if !state.Certs[1].Staging {
		t.Fatalf("a staging lineage is not flagged: %+v", state.Certs[1])
	}
	raw, _ := os.ReadFile(log)
	if strings.Contains(string(raw), "certificates") {
		t.Fatalf("certbot was asked for its certificates, which takes its lock:\n%s", raw)
	}
}

// Whatever happens to the lineages, the renewal answer is still given: it
// comes from systemd, and a lineage read that returned early used to leave
// it reading "off".
func TestCertbotStateAnswersRenewalWhenLineagesCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode")
	}
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	fakeCertbot(t)
	fakeSystemctl(t, "certbot.timer")
	renewal := filepath.Join(dir, "renewal")
	if err := os.MkdirAll(renewal, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(renewal, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(renewal, 0o755) })

	state := New(t.TempDir(), "").CertbotState(context.Background())
	if !state.AutoRenew || state.RenewSource != "certbot.timer" {
		t.Fatalf("renewal = %v %q", state.AutoRenew, state.RenewSource)
	}
	if !strings.Contains(state.Error, "renewal directory could not be read") || len(state.Certs) != 0 {
		t.Fatalf("error %q certs %+v", state.Error, state.Certs)
	}
}

// A lineage whose certificate is missing is still one certbot will try to
// renew, so it is listed, with the reason.
func TestCertbotLineageWithAMissingCertificateIsListedWithItsReason(t *testing.T) {
	dir := t.TempDir()
	root := newAuthority(t, "Test Root", nil)
	leaf, _, _ := root.issue(t, []string{"gone.example.com"}, time.Now().Add(time.Hour))
	writeLineage(t, dir, "gone.example.com", productionACME, leaf)
	if err := os.Remove(filepath.Join(dir, "live", "gone.example.com", "cert.pem")); err != nil {
		t.Fatal(err)
	}
	certs, err := readCertbotLineages(dir)
	if err != nil || len(certs) != 1 || certs[0].Error == "" || certs[0].Valid {
		t.Fatalf("certs = %+v, %v", certs, err)
	}
}

// certbot treats a request as the same certificate only when the names are
// exactly the same set; a subset or a superset is a different question.
func TestLineageForMatchesExactlyTheSameNames(t *testing.T) {
	dir := t.TempDir()
	root := newAuthority(t, "Test Root", nil)
	leaf, _, _ := root.issue(t, []string{"app.example.com", "www.app.example.com"}, time.Now().Add(time.Hour))
	writeLineage(t, dir, "app.example.com", stagingACME, leaf)

	if conf, _, ok := lineageFor(dir, []string{"WWW.app.example.com", "app.example.com"}); !ok || conf.Name != "app.example.com" {
		t.Fatalf("the same names in another order and case were not matched: %+v %v", conf, ok)
	}
	for _, names := range [][]string{{"app.example.com"}, {"app.example.com", "www.app.example.com", "api.example.com"}} {
		if _, _, ok := lineageFor(dir, names); ok {
			t.Fatalf("%v matched a lineage with different names", names)
		}
	}
	if _, _, ok := lineageFor(filepath.Join(dir, "absent"), []string{"app.example.com"}); ok {
		t.Fatal("a directory with no lineages matched")
	}
}

// A job tells a certificate certbot replaced from one it kept by the serials.
func TestCertbotSerialsChangeOnlyWhenTheCertificateDoes(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	root := newAuthority(t, "Test Root", nil)
	first, _, _ := root.issue(t, []string{"app.example.com"}, time.Now().Add(time.Hour))
	writeLineage(t, dir, "app.example.com", productionACME, first)

	before, err := CertbotSerials()
	if err != nil || before["app.example.com"] != first.SerialNumber.Text(16) {
		t.Fatalf("serials = %v, %v", before, err)
	}
	again, _ := CertbotSerials()
	if again["app.example.com"] != before["app.example.com"] {
		t.Fatal("the serial changed with nothing renewed")
	}
	second, _, _ := root.issue(t, []string{"app.example.com"}, time.Now().Add(2*time.Hour))
	writeLineage(t, dir, "app.example.com", productionACME, second)
	after, _ := CertbotSerials()
	if after["app.example.com"] == before["app.example.com"] {
		t.Fatal("a renewed certificate kept its serial")
	}
}

// With JD_ACME_DIRECTORY set a test run rehearses with that authority, not
// Let's Encrypt's staging one, and the page has to be able to say so.
func TestCertbotStateNamesAConfiguredAuthority(t *testing.T) {
	useLetsencryptDir(t, t.TempDir())
	fakeCertbot(t, "webroot")
	fakeSystemctl(t, "certbot.timer")
	t.Setenv("JD_ACME_DIRECTORY", "")
	if state := New(t.TempDir(), "").CertbotState(context.Background()); state.Directory != "" {
		t.Fatalf("Let's Encrypt reads as %q", state.Directory)
	}
	t.Setenv("JD_ACME_DIRECTORY", "https://ca.internal:9000/acme/acme/directory")
	if state := New(t.TempDir(), "").CertbotState(context.Background()); state.Directory != "https://ca.internal:9000/acme/acme/directory" || state.TestAuthority {
		t.Fatalf("directory = %q, test authority %v", state.Directory, state.TestAuthority)
	}
	// Let's Encrypt's own directories are Let's Encrypt, and its staging one
	// signs test certificates: the page offers no "real" issuance from it.
	for directory, testAuthority := range map[string]bool{
		"https://acme-v02.api.letsencrypt.org/directory":         false,
		"https://acme-staging-v02.api.letsencrypt.org/directory": true,
	} {
		t.Setenv("JD_ACME_DIRECTORY", directory)
		state := New(t.TempDir(), "").CertbotState(context.Background())
		if state.Directory != "" || state.TestAuthority != testAuthority {
			t.Fatalf("%s: directory = %q, test authority %v", directory, state.Directory, state.TestAuthority)
		}
		raw, _ := json.Marshal(state)
		if strings.Contains(string(raw), `"testAuthority"`) != testAuthority {
			t.Fatalf("%s: json %s", directory, raw)
		}
	}
}

// setRenewalParams replaces a lineage's [renewalparams] with params, the
// rest of its renewal configuration kept.
func setRenewalParams(t *testing.T, dir, name, params string) {
	t.Helper()
	path := filepath.Join(dir, "renewal", name+".conf")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(raw), "[renewalparams]")
	if err := os.WriteFile(path, []byte(head+"[renewalparams]\n"+params), 0o644); err != nil {
		t.Fatal(err)
	}
}

// How each lineage renews is in its renewal configuration, as certbot wrote
// it: this host's nginx one, a webroot one with its map (a list of one
// written with configobj's trailing comma), a DNS one naming its credentials
// file, an old one naming its plugin with the package in front, and one with
// the None configobj writes for an option never set.
func TestReadRenewalConfReadsHowCertbotRenews(t *testing.T) {
	dir := t.TempDir()
	root := newAuthority(t, "Test Root", nil)
	leaf, _, _ := root.issue(t, []string{"x.example.com"}, time.Now().Add(60*24*time.Hour))
	for _, name := range []string{"betbots.site", "web.example.com", "dns.example.com", "old.example.com", "none.example.com"} {
		writeLineage(t, dir, name, productionACME, leaf)
	}
	setRenewalParams(t, dir, "web.example.com", `account = 0123456789abcdef0123456789abcdef
authenticator = webroot
webroot_path = /var/www/app,
server = https://acme-v02.api.letsencrypt.org/directory
key_type = ecdsa
[[webroot_map]]
web.example.com = /var/www/app
static.example.com = /srv/static
`)
	setRenewalParams(t, dir, "dns.example.com", `account = 0123456789abcdef0123456789abcdef
authenticator = dns-cloudflare
dns_cloudflare_credentials = /etc/letsencrypt/jd-dns/cloudflare.ini
dns_cloudflare_propagation_seconds = 30
server = https://acme-v02.api.letsencrypt.org/directory
renew_hook = systemctl reload nginx
`)
	setRenewalParams(t, dir, "old.example.com", `authenticator = certbot-dns-cloudflare:dns-cloudflare
certbot_dns_cloudflare:dns_cloudflare_credentials = /root/cloudflare.ini
installer = None
`)
	setRenewalParams(t, dir, "none.example.com", `authenticator = standalone
installer = None
pre_hook = systemctl stop nginx
http01_port = 8080
`)

	read := func(name string) renewalConf {
		t.Helper()
		conf, err := readRenewalConf(filepath.Join(dir, "renewal", name+".conf"))
		if err != nil {
			t.Fatal(err)
		}
		return conf
	}
	if c := read("betbots.site"); c.Authenticator != "nginx" || c.Installer != "nginx" || c.Webroots != nil || c.Credentials != "" {
		t.Fatalf("betbots.site = %+v", c)
	}
	if c := read("web.example.com"); c.Authenticator != "webroot" || strings.Join(c.Webroots, " ") != "/var/www/app /srv/static" {
		t.Fatalf("web = %+v", c)
	}
	if c := read("dns.example.com"); c.Credentials != "/etc/letsencrypt/jd-dns/cloudflare.ini" || c.DeployHook != "systemctl reload nginx" {
		t.Fatalf("dns = %+v", c)
	}
	if c := read("old.example.com"); c.Credentials != "/root/cloudflare.ini" || pluginName(c.Authenticator) != "dns-cloudflare" || c.Installer != "" {
		t.Fatalf("old = %+v", c)
	}
	if c := read("none.example.com"); c.Installer != "" || c.PreHook != "systemctl stop nginx" || c.HTTP01Port != 8080 {
		t.Fatalf("none = %+v", c)
	}

	certs, err := readCertbotLineages(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]CertbotCert{}
	for _, c := range certs {
		byName[c.Name] = c
	}
	if c := byName["dns.example.com"]; c.Authenticator != "dns-cloudflare" || c.DNSProvider != "Cloudflare" || !c.DeployHook ||
		!c.NotBefore.Equal(leaf.NotBefore) {
		t.Fatalf("dns lineage = %+v", c)
	}
	if c := byName["old.example.com"]; c.Authenticator != "dns-cloudflare" || c.DNSProvider != "Cloudflare" {
		t.Fatalf("old lineage = %+v", c)
	}
}

// What will make the next renewal fail, each one certain in certbot's code:
// a plugin the certbot here lacks, a DNS credentials file that is gone, the
// manual plugin with no hook to answer it, a configuration with no
// authenticator, and a standalone server whose port something holds with
// nothing freeing it. A webroot folder that is gone is none of these:
// certbot creates it again.
func TestRenewalProblemsAreTheCertainFailures(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	fakeCertbot(t, "standalone", "webroot", "dns-digitalocean")
	root := newAuthority(t, "Test Root", nil)
	leaf, _, _ := root.issue(t, []string{"x.example.com"}, time.Now().Add(60*24*time.Hour))
	saved := filepath.Join(dir, "jd-dns", "digitalocean.ini")
	if err := os.MkdirAll(filepath.Dir(saved), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, saved, "dns_digitalocean_token = x\n")
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	port := held.Addr().(*net.TCPAddr).Port

	params := map[string]string{
		"cloudflare.example.com":   "authenticator = dns-cloudflare\ndns_cloudflare_credentials = " + filepath.Join(dir, "jd-dns", "cloudflare.ini") + "\n",
		"digitalocean.example.com": "authenticator = dns-digitalocean\ndns_digitalocean_credentials = " + saved + "\n",
		"manual.example.com":       "authenticator = manual\n",
		"hooked.example.com":       "authenticator = manual\nmanual_auth_hook = /usr/local/bin/dns-auth\n",
		"web.example.com":          "authenticator = webroot\nwebroot_path = " + filepath.Join(dir, "gone") + ",\n",
		"noauth.example.com":       "server = " + productionACME + "\n",
		"standalone.example.com":   fmt.Sprintf("authenticator = standalone\nhttp01_port = %d\n", port),
		"stops.example.com":        fmt.Sprintf("authenticator = standalone\nhttp01_port = %d\npre_hook = systemctl stop nginx\n", port),
	}
	for name, p := range params {
		writeLineage(t, dir, name, productionACME, leaf)
		setRenewalParams(t, dir, name, p)
	}
	confs, err := readRenewalConfs(dir)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := loadCertbotRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	problems := New(t.TempDir(), "").renewalProblems(context.Background(), rt, confs)

	cloudflare := problems["cloudflare.example.com"]
	if len(cloudflare) != 2 || !strings.Contains(cloudflare[0], "dns-cloudflare plugin, which the host's, certbot 9.9.9 does not have") ||
		cloudflare[1] != "certbot reads the Cloudflare credentials from "+filepath.Join(dir, "jd-dns", "cloudflare.ini")+", which is gone." {
		t.Fatalf("cloudflare = %q", cloudflare)
	}
	if got := problems["manual.example.com"]; len(got) != 1 || !strings.Contains(got[0], "manual plugin") {
		t.Fatalf("manual = %q", got)
	}
	if got := problems["noauth.example.com"]; len(got) != 1 || !strings.Contains(got[0], "names no authenticator") {
		t.Fatalf("noauth = %q", got)
	}
	if got := problems["standalone.example.com"]; len(got) != 1 || !strings.Contains(got[0], fmt.Sprintf("needs port %d, which ", port)) {
		t.Fatalf("standalone = %q", got)
	}
	for _, name := range []string{"digitalocean.example.com", "hooked.example.com", "web.example.com", "stops.example.com"} {
		if got := problems[name]; len(got) != 0 {
			t.Fatalf("%s will not fail, but reads %q", name, got)
		}
	}

	// A pre hook in certbot's hooks directory runs before every renewal.
	pre := filepath.Join(dir, "renewal-hooks", "pre")
	if err := os.MkdirAll(pre, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(pre, "stop-nginx"), "#!/bin/sh\nsystemctl stop nginx\n")
	problems = New(t.TempDir(), "").renewalProblems(context.Background(), rt, confs)
	if got := problems["standalone.example.com"]; len(got) != 0 {
		t.Fatalf("a pre hook frees the port, but standalone reads %q", got)
	}
}

// The page's whole reading of certbot, with the renewal record and the hook
// on it: each lineage's method and problems, the last run's failure on the
// lineage it names, and the hook's state.
func TestCertbotStateCarriesTheRenewalRecord(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	fakeCertbot(t, "nginx", "webroot")
	failingHost(t)
	root := newAuthority(t, "Test Root", nil)
	leaf, _, _ := root.issue(t, []string{"betbots.site"}, time.Now().Add(10*24*time.Hour))
	writeLineage(t, dir, "betbots.site", productionACME, leaf)
	other, _, _ := root.issue(t, []string{"app.example.com"}, time.Now().Add(70*24*time.Hour))
	writeLineage(t, dir, "app.example.com", productionACME, other)
	setRenewalParams(t, dir, "app.example.com", "authenticator = dns-cloudflare\n")
	// Saved long before the failed run, as the host's was.
	old := at("2026-06-01T00:00:00Z")
	for _, name := range []string{"betbots.site", "app.example.com"} {
		for _, file := range []string{"cert.pem", "fullchain.pem"} {
			if err := os.Chtimes(filepath.Join(dir, "live", name, file), old, old); err != nil {
				t.Fatal(err)
			}
		}
	}

	state := New(t.TempDir(), "").CertbotState(context.Background())
	if state.Health == nil || state.Health.State != "failed" || state.Health.Service != "certbot.service" {
		t.Fatalf("health = %+v", state.Health)
	}
	if state.ReloadHook == nil || state.ReloadHook.State != "missing" || state.ReloadHook.Path != filepath.Join(dir, "renewal-hooks", "deploy", renewalHookName) {
		t.Fatalf("hook = %+v", state.ReloadHook)
	}
	var betbots, app CertbotCert
	for _, c := range state.Certs {
		switch c.Name {
		case "betbots.site":
			betbots = c
		case "app.example.com":
			app = c
		}
	}
	if betbots.LastFailure == nil || betbots.LastFailure.Reason != "Some challenges have failed." ||
		betbots.Authenticator != "nginx" || betbots.Installer != "nginx" || len(betbots.WillFail) != 0 {
		t.Fatalf("betbots.site = %+v", betbots)
	}
	if app.LastFailure != nil || len(app.WillFail) != 1 || app.DNSProvider != "Cloudflare" {
		t.Fatalf("app.example.com = %+v", app)
	}

	// A certificate that cannot be read is still a lineage, and its failure
	// still stands.
	if os.Geteuid() != 0 {
		live := filepath.Join(dir, "live", "betbots.site")
		if err := os.Chmod(live, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(live, 0o755) })
		state = New(t.TempDir(), "").CertbotState(context.Background())
		if state.Health.State != "failed" || len(state.Health.Failures) != 1 {
			t.Fatalf("health with an unreadable certificate = %+v", state.Health)
		}
		for _, c := range state.Certs {
			if c.Name == "betbots.site" && (c.Error == "" || c.LastFailure == nil) {
				t.Fatalf("unreadable betbots.site = %+v", c)
			}
		}
	}
}
