package proxysvc

import (
	"strings"
	"testing"
)

// certbot's output is a labelled block per lineage. Parsed by label rather
// than position: the order of the lines has changed between releases and the
// labels have not.
func TestParseCertbotCertificates(t *testing.T) {
	out := strings.Join([]string{
		"Found the following certs:",
		"  Certificate Name: app.example.com",
		"    Serial Number: 3f2a91c4",
		"    Key Type: ECDSA",
		"    Domains: app.example.com www.app.example.com",
		"    Expiry Date: 2026-11-04 09:12:33+00:00 (VALID: 73 days)",
		"    Certificate Path: /etc/letsencrypt/live/app.example.com/fullchain.pem",
		"    Private Key Path: /etc/letsencrypt/live/app.example.com/privkey.pem",
		"  Certificate Name: old.example.com",
		"    Domains: old.example.com",
		"    Expiry Date: 2026-01-04 09:12:33+00:00 (INVALID: EXPIRED)",
		"    Certificate Path: /etc/letsencrypt/live/old.example.com/fullchain.pem",
	}, "\n")

	certs := ParseCertbotCertificates(out)
	if len(certs) != 2 {
		t.Fatalf("got %d certificates", len(certs))
	}
	first := certs[0]
	if first.Name != "app.example.com" {
		t.Errorf("name = %q", first.Name)
	}
	if len(first.Domains) != 2 {
		t.Errorf("domains = %v", first.Domains)
	}
	if first.DaysLeft != 73 || !first.Valid {
		t.Errorf("expiry = %d days, valid %v", first.DaysLeft, first.Valid)
	}
	if first.Serial != "3f2a91c4" {
		t.Errorf("serial = %q", first.Serial)
	}
	if first.CertPath == "" || first.KeyPath == "" {
		t.Errorf("paths lost: %+v", first)
	}
	if first.Expiry.Year() != 2026 || first.Expiry.Month() != 11 {
		t.Errorf("expiry date = %v", first.Expiry)
	}
	if certs[1].Valid {
		t.Error("an EXPIRED certificate should not be reported as valid")
	}
}

func TestParseCertbotCertificatesOnNothing(t *testing.T) {
	if got := ParseCertbotCertificates("No certificates found.\n"); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestParseDaysLeft(t *testing.T) {
	if got := parseDaysLeft("VALID: 73 days"); got != 73 {
		t.Errorf("got %d", got)
	}
	if got := parseDaysLeft("VALID: 1 day"); got != 1 {
		t.Errorf("got %d", got)
	}
	if got := parseDaysLeft("INVALID: EXPIRED"); got != 0 {
		t.Errorf("got %d", got)
	}
}

// certbot prints a paragraph and buries the reason near the end. The toast
// gets one line, so it had better be the right one.
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
			if _, err := s.IssueArgs(tc.req); err == nil {
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
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	args, err := s.IssueArgs(IssueRequest{
		Domains: []string{"app.example.com", "www.app.example.com"},
		Email:   "ops@example.com", Method: "webroot", WebRoot: "/var/www/html", Staging: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"certonly", "--webroot", "-w /var/www/html", "--non-interactive", "--agree-tos",
		"-m ops@example.com", "--keep-until-expiring", "--staging",
		"-d app.example.com", "-d www.app.example.com",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q from %q", want, joined)
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
