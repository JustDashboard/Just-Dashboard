package proxysvc

import (
	"context"
	"encoding/json"
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
