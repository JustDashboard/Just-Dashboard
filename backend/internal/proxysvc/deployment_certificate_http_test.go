package proxysvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeIssuingCertbot is a certbot with the named plugins whose certonly
// "issues" by putting a certificate for names where certbot would, under the
// first name's lineage (a wildcard's without its "*."), and records its argv.
func fakeIssuingCertbot(t *testing.T, letsencrypt string, names []string, plugins ...string) string {
	t.Helper()
	bin, stage := t.TempDir(), t.TempDir()
	log := filepath.Join(bin, "argv.log")
	var listing strings.Builder
	for _, p := range plugins {
		fmt.Fprintf(&listing, "* %s\nDescription: the %s plugin\nInterfaces: Authenticator, Plugin\n\n", p, p)
	}
	writeExecutable(t, filepath.Join(bin, "certbot"), fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$1" in
--version) echo "certbot 9.9.9"; exit 0 ;;
plugins) cat <<'LIST'
%sLIST
exit 0 ;;
certonly)
  name=""
  while [ $# -gt 0 ]; do
    if [ "$1" = "-d" ] && [ -z "$name" ]; then name="${2#\*.}"; fi
    shift
  done
  mkdir -p %q/live/"$name"
  cp %q/*.pem %q/live/"$name"/
  echo "Successfully received certificate."
  exit 0 ;;
esac
exit 1
`, log, listing.String(), letsencrypt, stage, letsencrypt))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	forgetCertbotRuntime()
	t.Cleanup(forgetCertbotRuntime)
	writeDeploymentCertificate(t, stage, names)
	return log
}

// writeACMEAccount lays an account out as certbot keeps one: under
// accounts/<host><path>/<id>/, by the directory it is with.
func writeACMEAccount(t *testing.T, letsencrypt, server string) {
	t.Helper()
	dir := filepath.Join(letsencrypt, "accounts", filepath.FromSlash(accountsPath(server)), "0123456789abcdef")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	regr := `{"body": {"contact": []}, "uri": "` + server + `/acme/acct/1"}`
	if err := os.WriteFile(filepath.Join(dir, "regr.json"), []byte(regr), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"creation_dt": "2026-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// certonlyLine is the certonly argv a fake certbot recorded.
func certonlyLine(t *testing.T, log string) string {
	t.Helper()
	raw, _ := os.ReadFile(log)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "certonly ") {
			return line
		}
	}
	t.Fatalf("certbot was not asked to order:\n%s", raw)
	return ""
}

const pebbleDirectory = "https://pebble.example.test:14000/dir"

// A deployment's HTTP-01 order names the directory JD_ACME_DIRECTORY
// configures the way the DNS path and the Certificates page do — nothing for
// Let's Encrypt's production directory, certbot's own default, and --server
// for anything else — and registers an account only when certbot has none
// with that directory: an account with Let's Encrypt is not one with a
// private authority, which used to be read as one and left the order refused
// for want of a contact.
func TestDeploymentHTTPOrderNamesTheConfiguredDirectoryAndItsAccount(t *testing.T) {
	names := []string{"app.example.test", "www.example.test"}
	for _, tc := range []struct {
		name       string
		directory  string
		method     string
		accounts   []string
		server     string
		registered bool
	}{
		{name: "Let's Encrypt by default", method: "webroot"},
		{name: "Let's Encrypt's account by default", method: "standalone", accounts: []string{productionACME}, registered: true},
		{name: "Let's Encrypt spelled out", directory: productionACME, method: "webroot", accounts: []string{productionACME}, registered: true},
		{name: "the staging endpoint", directory: stagingACME, method: "webroot", accounts: []string{productionACME}, server: stagingACME},
		{name: "a private authority", directory: pebbleDirectory, method: "webroot", accounts: []string{productionACME}, server: pebbleDirectory},
		{name: "a private authority's own account", directory: pebbleDirectory, method: "standalone",
			accounts: []string{productionACME, pebbleDirectory}, server: pebbleDirectory, registered: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			letsencrypt := filepath.Join(root, "letsencrypt")
			t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
			t.Setenv("JD_ACME_DIRECTORY", tc.directory)
			t.Setenv("JD_ACME_CA_ROOT", "")
			for _, server := range tc.accounts {
				writeACMEAccount(t, letsencrypt, server)
			}
			argv := fakeIssuingCertbot(t, letsencrypt, names, "webroot", "standalone")
			service := New(root, filepath.Join(root, "Caddyfile"))
			var logged []string
			result, err := service.issueDeploymentHTTP(context.Background(), names, tc.method, "/srv/just-dashboard-acme",
				acmeDirectory(), func(_, line string) { logged = append(logged, line) })
			if err != nil {
				t.Fatalf("issue: %v\n%s", err, strings.Join(logged, "\n"))
			}
			if result.Outcome != CertificateIssued || result.Method != tc.method || result.Name != "app.example.test" ||
				result.Registered != tc.registered || strings.Join(result.Domains, " ") != strings.Join(names, " ") {
				t.Fatalf("result = %+v", result)
			}
			order := certonlyLine(t, argv)
			want := "certonly --" + tc.method + " --non-interactive --agree-tos --keep-until-expiring --no-eff-email"
			if tc.method == "webroot" {
				want += " --webroot-path /srv/just-dashboard-acme"
			}
			if !tc.registered {
				want += " --register-unsafely-without-email"
			}
			if tc.server != "" {
				want += " --server " + tc.server
			}
			want += " -d app.example.test -d www.example.test"
			if order != want {
				t.Fatalf("certbot ran\n  %s\nwant\n  %s", order, want)
			}
			if !strings.Contains(strings.Join(logged, "\n"), "Successfully received certificate.") {
				t.Fatalf("certbot's output did not reach the transcript: %q", logged)
			}
		})
	}
}

// Every certbot the dashboard runs is handed the bundle JD_ACME_CA_ROOT
// names, as REQUESTS_CA_BUNDLE: it reads a private authority's directory over
// TLS through python-requests, which trusts nothing but the system's roots
// and that variable. The default authority adds nothing.
func TestCertbotIsHandedAPrivateAuthoritysRoot(t *testing.T) {
	fakeCertbot(t, "webroot")
	bundle := filepath.Join(t.TempDir(), "roots.pem")
	if err := os.WriteFile(bundle, []byte(certPEM(newAuthority(t, "Pebble Root", nil).cert)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JD_ACME_DIRECTORY", pebbleDirectory)
	t.Setenv("JD_ACME_CA_ROOT", bundle)
	cmd, err := CertbotCommand(context.Background(), []string{"AWS_SHARED_CREDENTIALS_FILE=/etc/letsencrypt/jd-dns/route53.ini"}, "renew")
	if err != nil {
		t.Fatal(err)
	}
	environment := strings.Join(cmd.Env, "\n")
	for _, want := range []string{"\nREQUESTS_CA_BUNDLE=" + bundle, "\nAWS_SHARED_CREDENTIALS_FILE=/etc/letsencrypt/jd-dns/route53.ini"} {
		if !strings.Contains("\n"+environment, want) {
			t.Fatalf("certbot's environment lacks %q:\n%s", strings.TrimSpace(want), environment)
		}
	}

	t.Setenv("JD_ACME_CA_ROOT", "")
	plain, err := CertbotCommand(context.Background(), nil, "renew")
	if err != nil {
		t.Fatal(err)
	}
	if plain.Env != nil {
		t.Fatalf("a public authority changed certbot's environment: %q", plain.Env)
	}
}
