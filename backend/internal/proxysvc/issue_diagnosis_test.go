package proxysvc

import (
	"net"
	"strings"
	"testing"
)

const failedChallenges = `Saving debug log to /var/log/letsencrypt/letsencrypt.log
Requesting a certificate for app.example.com and 4 more domains

Certbot failed to authenticate some domains (authenticator: webroot). The Certificate Authority reported these problems:
  Domain: app.example.com
  Type:   connection
  Detail: 203.0.113.5: Fetching http://app.example.com/.well-known/acme-challenge/x1: Timeout during connect (likely firewall problem)

  Domain: www.example.com
  Type:   dns
  Detail: DNS problem: NXDOMAIN looking up A for www.example.com - check that a DNS record exists for this domain; DNS problem: NXDOMAIN looking up AAAA for www.example.com - check that a DNS record exists for this domain

  Domain: shop.example.com
  Type:   unauthorized
  Detail: 203.0.113.5: Invalid response from http://shop.example.com/.well-known/acme-challenge/x3: 404

  Domain: old.example.com
  Type:   connection
  Detail: 198.51.100.7: Fetching http://old.example.com/.well-known/acme-challenge/x4: Connection refused

  Domain: blocked.example.com
  Type:   caa
  Detail: CAA record for blocked.example.com prevents issuance

Hint: The Certificate Authority failed to download the temporary challenge files created by Certbot.

Some challenges have failed.
Ask for help or search for solutions at https://community.letsencrypt.org. See the logfile /var/log/letsencrypt/letsencrypt.log or re-run Certbot with -v for more details.`

// hereIs makes 203.0.113.5 this host's address and every other address
// known to be elsewhere.
func hereIs(ip net.IP) (bool, bool) { return ip.String() == "203.0.113.5", true }

// Each domain's problem is read at the stage it failed and that stage's
// owner, with the address the authority reached and whether it is this
// host's.
func TestDiagnoseIssuanceReadsEachDomainsStageAndOwner(t *testing.T) {
	got := DiagnoseIssuance(strings.Split(failedChallenges, "\n"), hereIs)
	if len(got) != 5 {
		t.Fatalf("problems = %+v", got)
	}
	want := []struct {
		domain, stage, owner, link string
		here                       *bool
	}{
		{"app.example.com", StageConnect, "A firewall in front of port 80: this host's or the provider's", "/network/firewall", ptr(true)},
		{"www.example.com", StageDNS, "The DNS provider or registrar", "/network/tools?tool=dns&target=www.example.com", nil},
		{"shop.example.com", StageChallenge, "The web server answering the name on port 80", "/proxy/sites", ptr(true)},
		{"old.example.com", StageConnect, "Whoever holds 198.51.100.7: the name's record points there, or a provider maps it to this host", "/network/tools?tool=dns&target=old.example.com", ptr(false)},
		{"blocked.example.com", StageCAA, "The domain's DNS zone (its CAA records)", "/proxy/tls?domain=blocked.example.com", nil},
	}
	for i, w := range want {
		g := got[i]
		if g.Domain != w.domain || g.Stage != w.stage || g.Owner != w.owner || len(g.Links) == 0 || g.Links[0].Href != w.link {
			t.Errorf("%d = %+v, want %+v", i, g, w)
		}
		if (g.Here == nil) != (w.here == nil) || g.Here != nil && *g.Here != *w.here {
			t.Errorf("%s here = %v, want %v", w.domain, g.Here, w.here)
		}
	}
	if got[0].Address != "203.0.113.5" || !strings.Contains(got[0].Detail, "Timeout during connect") {
		t.Fatalf("first = %+v", got[0])
	}
	// Refused on this host's own address is what listens on port 80 here.
	refusedHere := DiagnoseIssuance([]string{"  Domain: old.example.com", "  Type:   connection",
		"  Detail: 203.0.113.5: Fetching http://old.example.com/.well-known/acme-challenge/x: Connection refused"}, hereIs)
	if refusedHere[0].Owner != "Whatever answers port 80 here" || refusedHere[0].Links[0].Href != "/proxy/ports?q=:80" {
		t.Fatalf("refused here = %+v", refusedHere[0])
	}
	// Where the host has no public address of its own, nothing is said
	// about where an address is: a provider may map it onto this host.
	unknown := DiagnoseIssuance(strings.Split(failedChallenges, "\n"), func(net.IP) (bool, bool) { return false, false })
	if unknown[0].Here != nil || unknown[0].Owner != "A firewall in front of port 80: this host's or the provider's" {
		t.Fatalf("unknown placement = %+v", unknown[0])
	}
}

// A run that failed before or beside validation is read from its words: the
// rate limits, certbot's own port 80, the DNS plugin's credentials, a TXT
// record that did not propagate, the account; anything else is said to be
// unrecognised rather than guessed.
func TestDiagnoseIssuanceReadsRunFailures(t *testing.T) {
	for _, tc := range []struct {
		output, stage string
	}{
		{"An unexpected error occurred:\nError creating new order :: too many certificates (5) already issued for this exact set of domains in the last 168h0m0s", StageRateLimit},
		{"Could not bind TCP port 80 because it is already in use by another process on this system (such as a web server).", StageLocalPort},
		{"Encountered exception during recovery: certbot.errors.PluginError: Unable to find a Route53 hosted zone for _acme-challenge.example.com", StageDNSPlugin},
		{"  Domain: example.com\n  Type:   unauthorized\n  Detail: No TXT record found at _acme-challenge.example.com", StageDNS01},
		{"Error: Cloudflare API error: Invalid request headers (authentication)", StageDNSPlugin},
		{"An unexpected error occurred:\nThe client lacks sufficient authorization :: Account is not valid, has status \"deactivated\"", StageAccount},
		{"The nginx plugin is not working; there may be problems with your existing configuration.", StageInstaller},
		{"Something odd happened.", StageUnknownRun},
		{"Saving debug log to /var/log/letsencrypt/letsencrypt.log\nAccount registered.\nAn unexpected error occurred:\njosepy.errors.DeserializationError: Could not decode 'token'", StageUnknownRun},
	} {
		got := DiagnoseIssuance(strings.Split(tc.output, "\n"), nil)
		if len(got) != 1 || got[0].Stage != tc.stage || got[0].Owner == "" || got[0].Action == "" {
			t.Errorf("%q = %+v, want stage %s", tc.output, got, tc.stage)
		}
	}
	if got := DiagnoseIssuance(nil, nil); got != nil {
		t.Fatalf("nothing printed = %+v", got)
	}
}

func ptr(b bool) *bool { return &b }

// A failed renewal run carries the same problems beside its failures.
func TestFailedRenewalRunCarriesItsProblems(t *testing.T) {
	var lines []RenewalLine
	for _, text := range strings.Split(failedChallenges, "\n") {
		lines = append(lines, RenewalLine{Text: text})
	}
	lines = append(lines, RenewalLine{Text: "Failed to renew certificate app.example.com with error: Some challenges have failed."})
	health := &RenewalHealth{}
	judgeFailedRun(health, &journalRun{lines: lines, failed: true}, nil)
	if health.State != "failed" || len(health.Problems) != 5 || health.Problems[1].Stage != StageDNS {
		t.Fatalf("health = %+v", health)
	}
}
