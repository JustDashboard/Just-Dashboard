package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLastMeaningfulLine(t *testing.T) {
	out := strings.Join([]string{
		"Saving debug log to /var/log/letsencrypt/letsencrypt.log",
		"- - - - - - - - - - - - - - - - -",
		"Some challenges have failed.",
		"- - - - - - - - - - - - - - - - -",
	}, "\n")
	if got := lastMeaningfulLine(out); got != "Some challenges have failed." {
		t.Fatalf("got %q", got)
	}
}

func TestCertbotErrorPreservesCauseBeforeHelpFooter(t *testing.T) {
	const footer = "\nAsk for help or search for solutions at https://community.letsencrypt.org. See the logfile /var/log/letsencrypt/letsencrypt.log or re-run Certbot with -v for more details."
	for _, reason := range []string{
		"Could not bind TCP port 80 because it is already in use by another process.",
		"Error creating new order :: too many certificates already issued",
	} {
		if got := lastMeaningfulLine(reason + footer); got != reason {
			t.Fatalf("got %q, want %q", got, reason)
		}
	}
	const detail = "100.110.34.31: Fetching http://app.sslip.io/.well-known/acme-challenge/token: Timeout during connect"
	out := "Certbot failed to authenticate some domains\n  Domain: app.sslip.io\n  Type: connection\n  Detail: " + detail + "\n\nSome challenges have failed." + footer
	if got := lastMeaningfulLine(out); got != detail {
		t.Fatalf("lost ACME validation cause: %q", got)
	}
}

func TestIssueValidation(t *testing.T) {
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	cases := []struct {
		name string
		req  IssueRequest
	}{
		{"no domains", IssueRequest{Email: "a@example.com", Method: "nginx"}},
		{"bad domain", IssueRequest{Domains: []string{"not a domain"}, Email: "a@example.com", Method: "nginx"}},
		{"no email", IssueRequest{Domains: []string{"example.com"}, Method: "nginx"}},
		{"bad email", IssueRequest{Domains: []string{"example.com"}, Email: "nope", Method: "nginx"}},
		{"unknown method", IssueRequest{Domains: []string{"example.com"}, Email: "a@example.com", Method: "dns"}},
		{"webroot with no path", IssueRequest{Domains: []string{"example.com"}, Email: "a@example.com", Method: "webroot"}},
		{"domain carrying an argument", IssueRequest{
			Domains: []string{"example.com --force-renewal"}, Email: "a@example.com", Method: "nginx",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.IssueArgs(context.Background(), tc.req); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestRenewAndRevokeValidateTheName(t *testing.T) {
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	if _, err := s.RenewArgs("../../etc/passwd", false, false); err == nil {
		t.Error("accepted a path as a certificate name")
	}
	if _, err := s.RevokeArgs(""); err == nil {
		t.Error("accepted an empty name")
	}
	if _, err := s.RevokeArgs("app.example.com; rm -rf /"); err == nil {
		t.Error("accepted a name carrying a command")
	}
}

// The argv is the whole contract now that running it belongs to a job, so it
// is worth reading back rather than trusting.
func TestIssueArgsShape(t *testing.T) {
	useLetsencryptDir(t, t.TempDir())
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	args, err := s.IssueArgs(context.Background(), IssueRequest{
		Domains: []string{"app.example.com", "www.app.example.com"},
		Email:   "ops@example.com", Method: "webroot", WebRoot: "/var/www/html",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"certonly", "--webroot", "-w /var/www/html", "--non-interactive", "--agree-tos",
		"-m ops@example.com", "--keep-until-expiring",
		"-d app.example.com", "-d www.app.example.com",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q from %q", want, joined)
		}
	}
	for _, unwanted := range []string{"--staging", "--dry-run", "--force-renewal"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("a real issuance for new names carries %s: %q", unwanted, joined)
		}
	}
}

func TestRenewArgsShape(t *testing.T) {
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	args, err := s.RenewArgs("app.example.com", true, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"renew", "--non-interactive", "--cert-name app.example.com", "--dry-run"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q from %q", want, joined)
		}
	}
	if strings.Contains(joined, "--force-renewal") {
		t.Error("force was not asked for")
	}

	// An empty name renews everything due, which is what certbot does with no
	// --cert-name at all.
	all, err := s.RenewArgs("", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(all, " "), "--cert-name") {
		t.Errorf("renew-all should not name a lineage: %q", all)
	}
}

// The image ships certbot without the nginx plugin, while the host it runs on
// has an nginx binary. Reading the plugin list is what keeps the deployment
// from ordering over a challenge this certbot cannot answer.
func TestCertbotAuthenticatorsReadsOnlyPluginsThatCanAnswerAChallenge(t *testing.T) {
	const containerOutput = `
- - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - -
* standalone
Description: Runs an HTTP server locally
Interfaces: Authenticator, Plugin
Entry point: EntryPoint(name='standalone')

* webroot
Description: Saves the necessary validation files
Interfaces: Authenticator, Plugin
Entry point: EntryPoint(name='webroot')
- - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - -
`
	got := certbotAuthenticators(containerOutput)
	if got["nginx"] {
		t.Fatal("nginx reported as an authenticator without its plugin")
	}
	for _, want := range []string{"standalone", "webroot"} {
		if !got[want] {
			t.Fatalf("%s authenticator was not detected in %#v", want, got)
		}
	}

	const hostOutput = `
* nginx
Description: Nginx Web Server plugin
Interfaces: Authenticator, Installer, Plugin
Entry point: EntryPoint(name='nginx')

* dns-route53
Description: Obtain certificates using a DNS TXT record
Interfaces: Plugin
Entry point: EntryPoint(name='dns-route53')
`
	got = certbotAuthenticators(hostOutput)
	if !got["nginx"] {
		t.Fatal("nginx plugin was not detected as an authenticator")
	}
	// Listed but not an Authenticator: it cannot answer the HTTP-01 challenge
	// a deployment orders over, and must not be selected as though it could.
	if got["dns-route53"] {
		t.Fatal("a non-authenticator plugin was offered as a challenge method")
	}
}

// A test run used to be --staging, which wrote a real lineage holding an
// untrusted certificate and left the real issuance that followed a no-op.
// It is certbot's --dry-run now: the whole exchange, nothing saved.
func TestIssueArgsTestRunIsADryRun(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	fakeCertbot(t, "nginx", "standalone", "webroot")
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	req := IssueRequest{Domains: []string{"app.example.com"}, Email: "ops@example.com", Method: "nginx", Staging: true}
	args, err := s.IssueArgs(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.HasPrefix(joined, "certonly --nginx") || !strings.Contains(joined, "--dry-run") || strings.Contains(joined, "--staging") {
		t.Fatalf("test run = %q", joined)
	}
	// certbot refuses --dry-run outside certonly and renew, so a test of an
	// install is a test of the challenge alone.
	req.Install = true
	args, err = s.IssueArgs(context.Background(), req)
	if err != nil || args[0] != "certonly" || !strings.Contains(strings.Join(args, " "), "--dry-run") {
		t.Fatalf("test run with install = %q, %v", args, err)
	}
	// Even over a staging lineage a test run replaces nothing.
	root := newAuthority(t, "(STAGING) Pretend Pear X1", nil)
	leaf, _, _ := root.issue(t, []string{"app.example.com"}, time.Now().Add(80*24*time.Hour))
	writeLineage(t, dir, "app.example.com", stagingACME, leaf)
	req.Install = false
	args, _ = s.IssueArgs(context.Background(), req)
	if strings.Contains(strings.Join(args, " "), "--force-renewal") {
		t.Fatalf("a test run forces renewal: %q", args)
	}
}

// certbot keeps a lineage until it is due, whatever signed it, so a real
// issuance over a staging lineage printed "no action taken" and left the
// test certificate in place. It forces the renewal; over a real one it does
// not, because that would spend a duplicate from the weekly limit.
func TestIssueArgsReplacesAStagingLineage(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	staging := newAuthority(t, "(STAGING) Pretend Pear X1", nil)
	test, _, _ := staging.issue(t, []string{"app.example.com", "www.app.example.com"}, time.Now().Add(80*24*time.Hour))
	writeLineage(t, dir, "app.example.com", stagingACME, test)
	production := newAuthority(t, "R11", nil)
	issued, _, _ := production.issue(t, []string{"shop.example.com"}, time.Now().Add(80*24*time.Hour))
	writeLineage(t, dir, "shop.example.com", productionACME, issued)

	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	issue := func(domains ...string) string {
		t.Helper()
		args, err := s.IssueArgs(context.Background(), IssueRequest{
			Domains: domains, Email: "ops@example.com", Method: "webroot", WebRoot: "/var/www/html",
		})
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(args, " ")
	}
	if got := issue("www.app.example.com", "app.example.com"); !strings.Contains(got, "--force-renewal") || strings.Contains(got, "--dry-run") {
		t.Fatalf("real issuance over a staging lineage = %q", got)
	}
	if got := issue("shop.example.com"); strings.Contains(got, "--force-renewal") {
		t.Fatalf("real issuance over a real lineage forces renewal: %q", got)
	}
	// A different set of names is a different certificate to certbot.
	if got := issue("app.example.com"); strings.Contains(got, "--force-renewal") {
		t.Fatalf("a subset of a staging lineage forces renewal: %q", got)
	}
}

// The nginx and DNS plugins are packages the certbot that runs the job has or
// has not got, and the refusal names that certbot.
func TestIssueArgsRefusesAPluginTheRuntimeLacks(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	fakeCertbot(t, "standalone", "webroot")
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	_, err := s.IssueArgs(context.Background(), IssueRequest{
		Domains: []string{"app.example.com"}, Email: "ops@example.com", Method: "nginx",
	})
	if err == nil || !strings.Contains(err.Error(), "certbot 9.9.9") || !strings.Contains(err.Error(), "no nginx plugin") {
		t.Fatalf("nginx without its plugin = %v", err)
	}
	_, err = s.IssueArgs(context.Background(), IssueRequest{
		Domains: []string{"*.example.com"}, Email: "ops@example.com", Method: "dns",
		DNSProvider: "cloudflare", Credentials: "dns_cloudflare_api_token = x",
	})
	if err == nil || !strings.Contains(err.Error(), "no dns-cloudflare plugin") {
		t.Fatalf("cloudflare without its plugin = %v", err)
	}

	fakeCertbot(t, "standalone", "webroot", "nginx", "dns-cloudflare")
	for _, req := range []IssueRequest{
		{Domains: []string{"app.example.com"}, Email: "ops@example.com", Method: "nginx"},
		{Domains: []string{"*.example.com"}, Email: "ops@example.com", Method: "dns", DNSProvider: "cloudflare", Credentials: "dns_cloudflare_api_token = x", DNSWait: 120},
	} {
		args, err := s.IssueArgs(context.Background(), req)
		if err != nil {
			t.Fatalf("%s refused with its plugin present: %v", req.Method, err)
		}
		if req.Method == "dns" && !strings.Contains(strings.Join(args, " "), "--dns-cloudflare-propagation-seconds 120") {
			t.Fatalf("the propagation wait was not passed: %q", args)
		}
	}
}

// Credentials sent with the request stand in for saved ones, and IssueArgs
// writes nothing: the token is saved by the job, only once it starts.
func TestIssueArgsTakesCredentialsFromTheRequestWithoutSavingThem(t *testing.T) {
	dir := t.TempDir()
	useLetsencryptDir(t, dir)
	fakeCertbot(t, "dns-cloudflare")
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	req := IssueRequest{Domains: []string{"*.example.com"}, Email: "ops@example.com", Method: "dns", DNSProvider: "cloudflare"}
	if _, err := s.IssueArgs(context.Background(), req); err == nil || !strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("no credentials anywhere = %v", err)
	}
	req.Credentials = "dns_cloudflare_api_token = x"
	if _, err := s.IssueArgs(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "jd-dns")); !os.IsNotExist(err) {
		t.Fatalf("IssueArgs wrote the credentials: %v", err)
	}

	checked, err := CheckDNSCredentials("cloudflare", req.Credentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "jd-dns")); !os.IsNotExist(err) {
		t.Fatalf("checking wrote the credentials: %v", err)
	}
	path, err := checked.Save()
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 || path != filepath.Join(dir, "jd-dns", "cloudflare.ini") {
		t.Fatalf("saved %s: %v %v", path, st, err)
	}
	if !HasDNSCredentials("cloudflare") {
		t.Fatal("saved credentials are not seen")
	}
	if _, err := CheckDNSCredentials("route53", "aws_access_key_id = only"); err == nil {
		t.Fatal("incomplete Route 53 credentials were accepted")
	}
}
