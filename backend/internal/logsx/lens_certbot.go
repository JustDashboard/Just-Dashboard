package logsx

import (
	"path"
	"strings"
	"time"
)

// The certbot lens reads certbot's own log (/var/log/letsencrypt/
// letsencrypt.log) and what its timer run prints to the journal. Both say
// the same things in the same sentences — certbot writes what it shows the
// operator to its log as "Notifying user: …" — so one reader serves both:
// which certificate a run was processing, whether it renewed, was not due,
// or failed and why.
//
// The log file writes a multi-line message as one record whose later lines
// carry no stamp; those continue it. The journal has no stamps in the text
// at all, so a line there is never a continuation: the reader only treats an
// unstamped line as one once it has seen the file's stamp.

func init() {
	register(&Lens{
		ID: "certbot",
		Events: []string{"renewing", "not_due", "renewed", "obtained", "renew_failed", "challenge_failed",
			"rate_limited", "error"},
		Attrs: []string{"domain", "error"},
		New:   func() Reader { return &certbotReader{} },
	})
}

type certbotReader struct {
	// domain is the certificate the run is on — from "Processing
	// /etc/letsencrypt/renewal/<name>.conf", "Renewing an existing
	// certificate for <name>", "Requesting a certificate for <name>" — which
	// the lines after it do not repeat.
	domain string
	// requesting says the run asked for a new certificate rather than
	// renewing one, which decides what "Successfully received certificate"
	// means.
	requesting bool
	// unexpected is set by "An unexpected error occurred:", whose cause is
	// the line after it.
	unexpected bool
	// file says the stream is letsencrypt.log, whose records start with a
	// stamp; stamp is the last one, which an outcome line inside a record is
	// filed under.
	file      bool
	stamp     time.Time
	headLevel string
}

// certbotLevels are the level names Python's logging writes into the file.
var certbotLevels = map[string]string{
	"DEBUG": "debug", "INFO": "info", "WARNING": "warn", "ERROR": "error", "CRITICAL": "critical",
}

func (r *certbotReader) Read(l *Line) {
	msg, stamped := r.prefix(l)
	summary := certbotSummary(msg)
	// An outcome certbot reports inside a multi-line message is a line of its
	// own: the report of a renew run names each certificate on its own line,
	// and a new certificate's "Successfully received certificate." follows
	// the blank line its message starts with.
	if !stamped && r.file && !summary && !certbotReceived(msg) {
		l.Cont = true
		if r.headLevel != "" {
			l.SetLevel(r.headLevel)
		}
		return
	}
	if !stamped && r.file {
		appSetStamp(l, r.stamp)
	}
	r.read(l, msg, summary)
	r.headLevel = l.Level
}

// prefix reads letsencrypt.log's "2024-01-15 00:00:01,234:DEBUG:certbot._internal.main:"
// and answers the message after it.
func (r *certbotReader) prefix(l *Line) (string, bool) {
	text := l.Text
	if text == "" || text[0] < '0' || text[0] > '9' {
		return text, false
	}
	t, n, ok := appParseStamp(text)
	if !ok || n >= len(text) || text[n] != ':' {
		return text, false
	}
	level, rest, ok := strings.Cut(text[n+1:], ":")
	normal, known := certbotLevels[level]
	if !ok || !known {
		return text, false
	}
	_, msg, ok := strings.Cut(rest, ":")
	if !ok {
		return text, false
	}
	appSetStamp(l, t)
	l.SetLevel(normal)
	r.file, r.stamp = true, t
	return strings.TrimPrefix(msg, "Notifying user: "), true
}

func (r *certbotReader) read(l *Line, msg string, summary bool) {
	if strings.Contains(msg, "certbot version: ") || strings.Contains(msg, "Saving debug log to ") {
		// The first line of a run, in the file and on the terminal; what the
		// last run was working on is not this one's.
		r.domain, r.requesting, r.unexpected = "", false, false
		return
	}
	if r.unexpected {
		// The cause of "An unexpected error occurred:" is the line after it.
		r.unexpected = false
		if event := certbotFailure(msg); event != "" {
			r.fail(l, event, r.domain, msg)
			return
		}
		if strings.TrimSpace(msg) != "" {
			r.fail(l, "error", r.domain, msg)
			return
		}
	}
	switch {
	case summary:
		// "  /etc/letsencrypt/live/example.com/fullchain.pem (success)" — one
		// line per certificate at the end of a renew run. Only a success is
		// an event here: a failure or a skip was already named, with its
		// cause, by the line that said so.
		domain, outcome := certbotSummaryParts(msg)
		l.SetAttr("domain", domain)
		if outcome == "success" {
			r.succeed(l, "renewed", domain)
		}
	case strings.Contains(msg, "Processing ") && strings.HasSuffix(strings.TrimSpace(msg), ".conf"):
		r.domain = strings.Clone(strings.TrimSuffix(path.Base(strings.TrimSpace(msg)), ".conf"))
		r.requesting = false
	case strings.Contains(msg, "Renewing an existing certificate"):
		r.domain, r.requesting = certbotFor(msg, r.domain), false
		r.succeed(l, "renewing", r.domain)
	case strings.Contains(msg, "Requesting a certificate for "):
		r.domain, r.requesting = certbotFor(msg, r.domain), true
		l.SetAttr("domain", r.domain)
	case strings.Contains(msg, "Obtaining a new certificate"):
		r.requesting = true
	case strings.Contains(msg, "not yet due for renewal"):
		r.succeed(l, "not_due", r.domain)
	case certbotReceived(msg):
		event := "renewed"
		if r.requesting {
			event = "obtained"
		}
		r.succeed(l, event, r.domain)
	case strings.Contains(msg, "Failed to renew certificate "):
		// "Failed to renew certificate example.com with error: Some challenges have failed."
		rest := msg[strings.Index(msg, "Failed to renew certificate ")+len("Failed to renew certificate "):]
		domain, cause, _ := strings.Cut(rest, " with error: ")
		event := "renew_failed"
		if certbotRateLimited(cause) {
			event = "rate_limited"
		}
		r.fail(l, event, strings.TrimSpace(domain), cause)
	case strings.Contains(msg, "Challenge failed for domain "):
		domain := strings.Fields(msg[strings.Index(msg, "Challenge failed for domain ")+len("Challenge failed for domain "):])
		if len(domain) > 0 {
			r.fail(l, "challenge_failed", domain[0], "")
		}
	case strings.Contains(msg, "An unexpected error occurred"):
		_, cause, _ := strings.Cut(msg, "An unexpected error occurred:")
		if strings.TrimSpace(cause) == "" {
			r.unexpected = true
			l.SetLevel("error")
			return
		}
		event := certbotFailure(cause)
		if event == "" {
			event = "error"
		}
		r.fail(l, event, r.domain, cause)
	default:
		if event := certbotFailure(msg); event != "" {
			r.fail(l, event, r.domain, msg)
			return
		}
		if (l.Level == "error" || l.Level == "critical") && r.file && !certbotRunSummary(msg) {
			r.fail(l, "error", r.domain, msg)
		}
	}
}

// succeed and fail name a line's outcome. A renewal reported to the operator
// is written to the file at DEBUG ("Notifying user: …") because that is the
// logger certbot's display uses, not because it is debugging detail; the
// outcome is what the line is for, so it is filed at info. A failure is an
// error whatever level certbot chose — "Challenge failed for domain" is INFO.
func (r *certbotReader) succeed(l *Line, event, domain string) {
	l.Event = event
	l.SetAttr("domain", domain)
	l.SetLevel("info")
}

func (r *certbotReader) fail(l *Line, event, domain, cause string) {
	l.Event = event
	l.SetAttr("domain", domain)
	l.SetAttr("error", appCap(appFirstLine(cause), appErrorCap))
	l.SetLevel("error")
}

// certbotFailure names a failure a line states on its own: the ACME server's
// rate limit, however it is worded.
func certbotFailure(msg string) string {
	if certbotRateLimited(msg) {
		return "rate_limited"
	}
	return ""
}

func certbotRateLimited(msg string) bool {
	return strings.Contains(msg, "rateLimited") || strings.Contains(msg, "too many certificates") ||
		strings.Contains(msg, "too many failed authorizations") || strings.Contains(msg, "too many new orders") ||
		strings.Contains(msg, "There were too many requests")
}

// certbotRunSummary is the error-level wrap-up of a run whose failures were
// each already named: "All renewals failed…", "1 renew failure(s), 0 parse
// failure(s)", the pointer to the community forum.
func certbotRunSummary(msg string) bool {
	return strings.Contains(msg, "renewals failed") || strings.Contains(msg, "renewals succeeded") ||
		strings.Contains(msg, "renew failure(s)") || strings.Contains(msg, "Some challenges have failed") ||
		strings.Contains(msg, "Ask for help or search for solutions") || strings.Contains(msg, "following certificates")
}

// certbotSummary is one certificate's line in a run's closing report:
// "  /etc/letsencrypt/live/example.com/fullchain.pem (success)", or
// "… expires on 2024-03-10 (skipped)".
func certbotSummary(msg string) bool {
	return strings.Contains(msg, "/live/") && strings.Contains(msg, "/fullchain.pem") && strings.HasSuffix(msg, ")")
}

func certbotSummaryParts(msg string) (domain, outcome string) {
	live := strings.Index(msg, "/live/")
	rest := msg[live+len("/live/"):]
	domain, _, _ = strings.Cut(rest, "/")
	if open := strings.LastIndexByte(msg, '('); open >= 0 {
		outcome = msg[open+1 : len(msg)-1]
	}
	return domain, outcome
}

func certbotReceived(msg string) bool {
	return strings.Contains(msg, "Successfully received certificate") ||
		strings.Contains(msg, "Congratulations! Your certificate and chain have been saved")
}

// certbotFor is the first name after " for " — certbot summarises a
// certificate's names as "example.com and www.example.com" or "example.com
// and 3 more domains".
func certbotFor(msg, fallback string) string {
	_, rest, ok := strings.Cut(msg, " for ")
	if !ok {
		return fallback
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return fallback
	}
	// Kept past this line as the run's certificate: a copy, not a slice of it.
	return strings.Clone(strings.TrimRight(fields[0], ".,"))
}
