package proxysvc

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// The HSTS preload list, measured.
//
// The report showed a "preload" tag whenever the header carried the word, and
// said six months was what the list expects. hstspreload.org asks for a year,
// includeSubDomains and preload, a plain-HTTP redirect whose first hop is
// HTTPS on the same host, a valid certificate, HTTPS on www when www exists,
// and it takes whole registrable domains only. Each of those is a rule here,
// with what was seen, so "not eligible" comes with the reason and "eligible"
// is not a guess.

// preloadMaxAge is the list's minimum max-age: one year.
const preloadMaxAge = 31536000

// PreloadCheck is a domain against hstspreload.org's submission rules.
type PreloadCheck struct {
	// Domain is the registrable domain the list would take. For a
	// subdomain it is the parent to scan instead, and the only rule is the
	// one that says so.
	Domain   string        `json:"domain"`
	Eligible bool          `json:"eligible"`
	Rules    []PreloadRule `json:"rules"`
}

// PreloadRule is one submission requirement and what was seen of it.
type PreloadRule struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// registrableDomain is the name the preload list would take for domain: its
// public suffix plus one label, by the Public Suffix List. It is "" for a
// public suffix itself or a name the list cannot place.
func registrableDomain(domain string) string {
	base, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil {
		return ""
	}
	return base
}

// preloadCheck measures a scan of domain on port 443 that got an HTTP answer.
// www is the outcome of checkWWW, run beside the rest of the scan; it is nil
// for a domain that is not registrable, which gets the one rule that says so.
func preloadCheck(domain string, scan *TLSScan, www *PreloadRule) *PreloadCheck {
	base := registrableDomain(domain)
	check := &PreloadCheck{Domain: base, Rules: []PreloadRule{}}
	if base != domain {
		rule := PreloadRule{ID: "registrable", Title: "A registrable domain"}
		if base == "" {
			rule.Detail = domain + " is a public suffix, or a name the Public Suffix List cannot place, so nothing can be submitted for it."
		} else {
			rule.Detail = "The list takes whole registrable domains: " + domain + " is covered by submitting " + base + ", which preloads every name under it."
		}
		check.Rules = append(check.Rules, rule)
		return check
	}
	check.Rules = append(check.Rules, PreloadRule{ID: "registrable", Title: "A registrable domain", Passed: true,
		Detail: domain + " is a registrable domain, the only kind the list takes."})

	cert := PreloadRule{ID: "certificate", Title: "A valid certificate", Passed: scan.Trusted,
		Detail: "The certificate is trusted and covers " + domain + "."}
	if !scan.Trusted {
		cert.Detail = "The certificate is not trusted: " + scan.TrustError
	}
	check.Rules = append(check.Rules, cert, preloadRedirect(scan.HTTP))
	if rule, applies := preloadHTTPSRedirect(scan.HTTP); applies {
		check.Rules = append(check.Rules, rule)
	}
	check.Rules = append(check.Rules, preloadHeader(scan.HTTP.HSTS)...)
	if www != nil {
		check.Rules = append(check.Rules, *www)
	}

	check.Eligible = true
	for _, rule := range check.Rules {
		check.Eligible = check.Eligible && rule.Passed
	}
	return check
}

// preloadRedirect is the plain-HTTP rule: a port 80 that refuses connections
// passes (the list asks for the redirect "if you are listening on port 80"),
// and one that answers must send its first redirect to HTTPS on the same
// host — a visitor sent to http://www first never receives the header on the
// name being preloaded.
func preloadRedirect(http *HTTPScan) PreloadRule {
	rule := PreloadRule{ID: "redirect", Title: "Plain HTTP redirects to HTTPS on the same host first"}
	switch {
	case http.PlainErrorKind == "refused":
		rule.Passed, rule.Detail = true, "Port 80 refuses connections, which the list accepts."
		return rule
	case http.PlainError != "":
		rule.Detail = "Port 80 did not answer (" + http.PlainError + "), so the redirect could not be checked. If nothing is meant to listen there, it has to refuse the connection."
		return rule
	case len(http.RedirectChain) == 0:
		rule.Detail = "The plain-HTTP request was not made."
		return rule
	}
	first := http.RedirectChain[0]
	if first.Status < 300 || first.Status >= 400 || first.Location == "" {
		rule.Detail = fmt.Sprintf("%s answered %d without redirecting.", first.URL, first.Status)
		return rule
	}
	from, err := url.Parse(first.URL)
	if err != nil {
		rule.Detail = "The first request's address could not be read."
		return rule
	}
	to, err := from.Parse(first.Location)
	if err != nil {
		rule.Detail = fmt.Sprintf("%s redirects to %q, which is not an address.", first.URL, first.Location)
		return rule
	}
	switch {
	case to.Scheme != "https":
		rule.Detail = fmt.Sprintf("%s redirects to %s first. The first redirect has to go to https://%s.", first.URL, to, from.Hostname())
	case !strings.EqualFold(to.Hostname(), from.Hostname()):
		rule.Detail = fmt.Sprintf("%s redirects to %s, another host. The first redirect has to stay on %s, so the header for it is set before the visitor moves on.", first.URL, to, from.Hostname())
	default:
		rule.Passed, rule.Detail = true, fmt.Sprintf("%s redirects straight to %s.", first.URL, to)
	}
	return rule
}

// preloadHTTPSRedirect applies when the HTTPS answer is itself a redirect,
// which the list requires to stay on HTTPS.
func preloadHTTPSRedirect(http *HTTPScan) (PreloadRule, bool) {
	if http.StatusCode < 300 || http.StatusCode >= 400 || http.Location == "" {
		return PreloadRule{}, false
	}
	rule := PreloadRule{ID: "https-redirect", Title: "HTTPS redirects only to HTTPS"}
	to, err := url.Parse(http.Location)
	switch {
	case err != nil:
		rule.Detail = fmt.Sprintf("The HTTPS answer redirects to %q, which is not an address.", http.Location)
	case to.Scheme == "http":
		rule.Detail = "The HTTPS answer redirects to " + http.Location + ", an insecure page."
	default:
		rule.Passed, rule.Detail = true, "The HTTPS answer redirects to "+http.Location+", and the header has to be on that redirect itself, which is the answer checked below."
	}
	return rule, true
}

// preloadHeader is the three rules the Strict-Transport-Security header has
// to meet on the HTTPS answer for the domain itself.
func preloadHeader(hsts *HSTS) []PreloadRule {
	maxAge := PreloadRule{ID: "max-age", Title: "max-age of at least a year (31536000)"}
	subdomains := PreloadRule{ID: "include-subdomains", Title: "includeSubDomains"}
	preload := PreloadRule{ID: "preload", Title: "The preload directive"}
	if hsts == nil {
		missing := "No Strict-Transport-Security header on the HTTPS answer."
		maxAge.Detail, subdomains.Detail, preload.Detail = missing, missing, missing
		return []PreloadRule{maxAge, subdomains, preload}
	}
	switch {
	case hsts.MaxAge < 0:
		maxAge.Detail = "The header has no max-age."
	case hsts.MaxAge < preloadMaxAge:
		maxAge.Detail = fmt.Sprintf("max-age is %d seconds (%d days); the list asks for a year.", hsts.MaxAge, hsts.MaxAge/86400)
	default:
		maxAge.Passed, maxAge.Detail = true, fmt.Sprintf("max-age is %d seconds (%d days).", hsts.MaxAge, hsts.MaxAge/86400)
	}
	subdomains.Passed = hsts.IncludeSubDomains
	subdomains.Detail = "The header leaves it out. The list requires it, because a preloaded domain holds every name under it to HTTPS."
	if hsts.IncludeSubDomains {
		subdomains.Detail = "Set: every subdomain is held to HTTPS too."
	}
	preload.Passed = hsts.Preload
	preload.Detail = "The header does not ask to be preloaded."
	if hsts.Preload {
		preload.Detail = "Set."
	}
	return []PreloadRule{maxAge, subdomains, preload}
}

// lookupFunc resolves a name, as net.DefaultResolver.LookupIPAddr does.
type lookupFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

// checkWWW is the list's www rule: if www.<domain> has a DNS record, it must
// serve HTTPS with a certificate browsers trust. It handshakes with the first
// address the name resolves to, on port.
func checkWWW(ctx context.Context, domain, port string, lookup lookupFunc) PreloadRule {
	name := "www." + domain
	rule := PreloadRule{ID: "www", Title: name + " serves HTTPS, if it exists"}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addrs, err := lookup(ctx, name)
	var dns *net.DNSError
	switch {
	case errors.As(err, &dns) && dns.IsNotFound:
		rule.Passed, rule.Detail = true, name+" has no DNS record, so there is nothing to serve."
		return rule
	case err != nil:
		rule.Detail = "Its lookup failed (" + err.Error() + "), so it could not be checked."
		return rule
	case len(addrs) == 0:
		rule.Passed, rule.Detail = true, name+" has no address, so there is nothing to serve."
		return rule
	}
	address := net.JoinHostPort(addrs[0].IP.String(), port)
	conn, err := dialTLS(ctx, address, name, "", 0, 0)
	if err != nil {
		rule.Detail = "It resolves to " + addrs[0].IP.String() + " and no TLS handshake completed there: " + err.Error() + "."
		return rule
	}
	state := conn.ConnectionState()
	conn.Close()
	if len(state.PeerCertificates) == 0 {
		rule.Detail = "Its handshake completed without a certificate."
		return rule
	}
	roots, _ := x509.SystemCertPool()
	if _, err := state.PeerCertificates[0].Verify(x509.VerifyOptions{
		DNSName: name, Roots: roots, Intermediates: intermediates(state.PeerCertificates),
	}); err != nil {
		rule.Detail = "Its certificate is not trusted: " + err.Error() + "."
		return rule
	}
	rule.Passed, rule.Detail = true, name+" serves a trusted certificate."
	return rule
}
