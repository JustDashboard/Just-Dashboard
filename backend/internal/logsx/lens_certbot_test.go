package logsx

import "testing"

// What certbot.service's timer runs print to the journal on this host
// (`journalctl -u certbot.service -o cat`): a renewal failing every run, and
// once the ACME server refusing a stale authorisation.
func TestLensCertbotJournal(t *testing.T) {
	appLensCheck(t, "certbot", []string{
		"Failed to renew certificate betbots.site with error: Some challenges have failed.",
		"All renewals failed. The following certificates could not be renewed:",
		"  /etc/letsencrypt/live/betbots.site/fullchain.pem (failure)",
		"1 renew failure(s), 0 parse failure(s)",
		"Failed to renew certificate betbots.site with error: urn:ietf:params:acme:error:malformed :: The request message was malformed :: No such authorization",
	}, []appLensWant{
		{event: "renew_failed", level: "error", attrs: map[string]string{"domain": "betbots.site", "error": "Some challenges have failed."}},
		{},
		{attrs: map[string]string{"domain": "betbots.site"}},
		{},
		{event: "renew_failed", level: "error", attrs: map[string]string{"domain": "betbots.site", "error": "urn:ietf:params:acme:error:malformed :: The request message was malformed :: No such authorization"}},
	})
}

// letsencrypt.log, in the format certbot writes it
// ("%(asctime)s:%(levelname)s:%(name)s:%(message)s", what it shows the
// operator logged as "Notifying user: …"). This host's
// /var/log/letsencrypt is root's alone, so these follow certbot 2.x's own
// sentences rather than this host's file.
func TestLensCertbotFile(t *testing.T) {
	appLensZone(t)
	appLensCheck(t, "certbot", []string{
		// A renew run on two certificates: one not due, one renewed.
		"2026-03-01 12:00:00,090:DEBUG:certbot._internal.main:certbot version: 2.11.0",
		"2026-03-01 12:00:00,100:DEBUG:certbot._internal.display.obj:Notifying user: Processing /etc/letsencrypt/renewal/example.com.conf",
		"2026-03-01 12:00:00,110:DEBUG:certbot._internal.display.obj:Notifying user: Certificate not yet due for renewal",
		"2026-03-01 12:00:00,120:DEBUG:certbot._internal.display.obj:Notifying user: Processing /etc/letsencrypt/renewal/shop.example.com.conf",
		"2026-03-01 12:00:00,130:DEBUG:certbot._internal.display.obj:Notifying user: Renewing an existing certificate for shop.example.com and www.shop.example.com",
		"2026-03-01 12:00:09,000:DEBUG:certbot._internal.display.obj:Notifying user: ",
		"- - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - -",
		"2026-03-01 12:00:09,002:DEBUG:certbot._internal.display.obj:Notifying user:   /etc/letsencrypt/live/example.com/fullchain.pem expires on 2026-05-01 (skipped)",
		"2026-03-01 12:00:09,003:DEBUG:certbot._internal.display.obj:Notifying user: Congratulations, all renewals succeeded: ",
		"2026-03-01 12:00:09,004:DEBUG:certbot._internal.display.obj:Notifying user:   /etc/letsencrypt/live/shop.example.com/fullchain.pem (success)",
		// The report's second certificate is on a line of its own, unstamped.
		"  /etc/letsencrypt/live/api.example.com/fullchain.pem (success)",
		// A failing renewal: the challenge, the traceback logged at DEBUG, the failure.
		"2026-09-27 07:20:00,101:DEBUG:certbot._internal.main:certbot version: 2.11.0",
		"2026-09-27 07:20:00,150:DEBUG:certbot._internal.display.obj:Notifying user: Processing /etc/letsencrypt/renewal/betbots.site.conf",
		"2026-09-27 07:20:04,900:INFO:certbot._internal.auth_handler:Challenge failed for domain betbots.site",
		"2026-09-27 07:20:04,905:DEBUG:certbot._internal.error_handler:Encountered exception:",
		"Traceback (most recent call last):",
		`  File "/usr/lib/python3/dist-packages/certbot/_internal/auth_handler.py", line 108, in handle_authorizations`,
		"certbot.errors.AuthorizationError: Some challenges have failed.",
		"2026-09-27 07:20:04,910:ERROR:certbot._internal.renewal:Failed to renew certificate betbots.site with error: Some challenges have failed.",
		"2026-09-27 07:20:04,912:ERROR:certbot._internal.renewal:All renewals failed. The following certificates could not be renewed:",
		"2026-09-27 07:20:04,914:ERROR:certbot._internal.log:1 renew failure(s), 0 parse failure(s)",
		// A new certificate, whose message starts with a blank line.
		"2026-01-06 12:30:00,000:DEBUG:certbot._internal.main:certbot version: 2.11.0",
		"2026-01-06 12:30:00,010:DEBUG:certbot._internal.display.obj:Notifying user: Requesting a certificate for new.example.com",
		"2026-01-06 12:30:08,000:DEBUG:certbot._internal.display.obj:Notifying user: ",
		"Successfully received certificate.",
		"Certificate is saved at: /etc/letsencrypt/live/new.example.com/fullchain.pem",
		// Asking again the next day meets the rate limit, which certbot
		// reports on the line after "An unexpected error occurred:".
		"2026-01-07 08:59:59,000:DEBUG:certbot._internal.main:certbot version: 2.11.0",
		"2026-01-07 08:59:59,010:DEBUG:certbot._internal.display.obj:Notifying user: Requesting a certificate for new.example.com",
		"2026-01-07 09:00:00,000:ERROR:certbot._internal.log:An unexpected error occurred:",
		"2026-01-07 09:00:00,001:ERROR:certbot._internal.log:There were too many requests of a given type :: Error creating new order :: too many certificates (5) already issued for this exact set of domains in the last 168 hours: new.example.com",
		// An error of certbot's own that names no certificate.
		"2026-01-07 09:10:00,000:DEBUG:certbot._internal.main:certbot version: 2.11.0",
		"2026-01-07 09:10:00,500:ERROR:certbot._internal.log:Could not bind TCP port 80 because it is already in use by another process on this system (such as a web server). Please stop the program in question and then try again.",
	}, []appLensWant{
		{level: "debug", at: "2026-03-01T09:00:00.09Z"},
		{level: "debug", at: "2026-03-01T09:00:00.1Z"},
		{event: "not_due", level: "info", at: "2026-03-01T09:00:00.11Z", attrs: map[string]string{"domain": "example.com"}},
		{level: "debug", at: "2026-03-01T09:00:00.12Z"},
		{event: "renewing", level: "info", at: "2026-03-01T09:00:00.13Z", attrs: map[string]string{"domain": "shop.example.com"}},
		{level: "debug", at: "2026-03-01T09:00:09Z"},
		{level: "debug", cont: true},
		{level: "debug", at: "2026-03-01T09:00:09.002Z", attrs: map[string]string{"domain": "example.com"}},
		{level: "debug", at: "2026-03-01T09:00:09.003Z"},
		{event: "renewed", level: "info", at: "2026-03-01T09:00:09.004Z", attrs: map[string]string{"domain": "shop.example.com"}},
		{event: "renewed", level: "info", at: "2026-03-01T09:00:09.004Z", attrs: map[string]string{"domain": "api.example.com"}},
		{level: "debug", at: "2026-09-27T04:20:00.101Z"},
		{level: "debug", at: "2026-09-27T04:20:00.15Z"},
		{event: "challenge_failed", level: "error", at: "2026-09-27T04:20:04.9Z", attrs: map[string]string{"domain": "betbots.site"}},
		{level: "debug", at: "2026-09-27T04:20:04.905Z"},
		{level: "debug", cont: true},
		{level: "debug", cont: true},
		{level: "debug", cont: true},
		{event: "renew_failed", level: "error", at: "2026-09-27T04:20:04.91Z", attrs: map[string]string{"domain": "betbots.site", "error": "Some challenges have failed."}},
		{level: "error", at: "2026-09-27T04:20:04.912Z"},
		{level: "error", at: "2026-09-27T04:20:04.914Z"},
		{level: "debug", at: "2026-01-06T09:30:00Z"},
		{level: "debug", at: "2026-01-06T09:30:00.01Z", attrs: map[string]string{"domain": "new.example.com"}},
		{level: "debug", at: "2026-01-06T09:30:08Z"},
		{event: "obtained", level: "info", at: "2026-01-06T09:30:08Z", attrs: map[string]string{"domain": "new.example.com"}},
		{level: "info", cont: true},
		{level: "debug", at: "2026-01-07T05:59:59Z"},
		{level: "debug", at: "2026-01-07T05:59:59.01Z", attrs: map[string]string{"domain": "new.example.com"}},
		{level: "error", at: "2026-01-07T06:00:00Z"},
		{event: "rate_limited", level: "error", at: "2026-01-07T06:00:00.001Z", attrs: map[string]string{
			"domain": "new.example.com",
			"error":  "There were too many requests of a given type :: Error creating new order :: too many certificates (5) already issued for this exact set of domains in the last 168 hours: new.example.com",
		}},
		{level: "debug", at: "2026-01-07T06:10:00Z"},
		{event: "error", level: "error", at: "2026-01-07T06:10:00.5Z", attrs: map[string]string{
			"error": "Could not bind TCP port 80 because it is already in use by another process on this system (such as a web server). Please stop the program in question and then try again.",
		}},
	})
}

func BenchmarkLensCertbot(b *testing.B) {
	appLensBenchmark(b, "certbot", []string{
		"2026-03-01 12:00:00,090:DEBUG:certbot._internal.main:certbot version: 2.11.0",
		"2026-03-01 12:00:00,095:DEBUG:certbot._internal.main:Arguments: ['-q', '--no-random-sleep-on-renew']",
		"2026-03-01 12:00:00,100:DEBUG:certbot._internal.display.obj:Notifying user: Processing /etc/letsencrypt/renewal/example.com.conf",
		"2026-03-01 12:00:00,110:DEBUG:certbot._internal.display.obj:Notifying user: Certificate not yet due for renewal",
		"2026-09-27 07:20:04,910:ERROR:certbot._internal.renewal:Failed to renew certificate betbots.site with error: Some challenges have failed.",
		`  File "/usr/lib/python3/dist-packages/certbot/_internal/auth_handler.py", line 108, in handle_authorizations`,
	})
}
