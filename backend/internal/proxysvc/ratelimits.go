package proxysvc

import (
	"crypto/x509"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Let's Encrypt's production limits that one host can run into by itself:
// five certificates for the same set of names a week, fifty new certificates
// per registered domain a week, and five failed validations per name an hour
// for one account. They are the authority's to change; these are the figures
// it publishes, and the meter says where they come from.
const (
	duplicateLimit       = 5
	registeredLimit      = 50
	failedValidationRate = 5
	rateWeek             = 7 * 24 * time.Hour
)

// RateLimits is how close a real issuance for a set of names is to Let's
// Encrypt's limits, counted from what this host can see: the certificates in
// certbot's archive and the issuance runs this dashboard started. Other hosts
// issuing for the same domains count against the same limits and are not
// seen here, which Scope says in words.
type RateLimits struct {
	// Applies is false when certbot orders from another authority, whose
	// limits these figures say nothing about, or from Let's Encrypt's
	// staging one, whose limits are far higher.
	Applies    bool         `json:"applies"`
	Duplicates RateCount    `json:"duplicates"`
	Registered []DomainRate `json:"registered"`
	Failures   []DomainRate `json:"failures"`
	Scope      string       `json:"scope"`
}

// RateCount is a used figure against its limit. Frees is when the oldest
// counted certificate leaves the window, set only when the limit is reached.
type RateCount struct {
	Used  int        `json:"used"`
	Limit int        `json:"limit"`
	Frees *time.Time `json:"frees,omitempty"`
}

type DomainRate struct {
	Domain string `json:"domain"`
	RateCount
}

// FailedIssue is a real issuance run that failed, for the failed-validation
// count: the names it asked for and when it ended.
type FailedIssue struct {
	Domains []string
	At      time.Time
}

// archivedCert is one certificate certbot saved: every version of every
// lineage stays in archive/<name>/certN.pem until the lineage is deleted.
type archivedCert struct {
	names     []string
	notBefore time.Time
}

// readArchive is every production certificate in certbot's archive issued
// since a moment. A staging one counts against nothing real.
func readArchive(dir string, since time.Time) []archivedCert {
	paths, _ := filepath.Glob(filepath.Join(dir, "archive", "*", "cert*.pem"))
	var out []archivedCert
	for _, path := range paths {
		leaf, err := readLeaf(path)
		if err != nil || stagingIssuer(leaf) || leaf.NotBefore.Before(since) {
			continue
		}
		out = append(out, archivedCert{names: leafNames(leaf), notBefore: leaf.NotBefore})
	}
	return out
}

func leafNames(leaf *x509.Certificate) []string {
	names := leaf.DNSNames
	if len(names) == 0 && leaf.Subject.CommonName != "" {
		names = []string{leaf.Subject.CommonName}
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.ToLower(n))
	}
	return out
}

func sameNameSet(a, b []string) bool {
	set := map[string]bool{}
	for _, n := range a {
		set[strings.ToLower(n)] = true
	}
	other := map[string]bool{}
	for _, n := range b {
		n = strings.ToLower(n)
		if !set[n] {
			return false
		}
		other[n] = true
	}
	return len(set) == len(other)
}

// registeredDomain is the name a registrar sells, by the public suffix list:
// example.co.uk for www.example.co.uk.
func registeredDomain(name string) string {
	name = strings.TrimPrefix(strings.ToLower(name), "*.")
	if d, err := publicsuffix.EffectiveTLDPlusOne(name); err == nil {
		return d
	}
	return name
}

// CertificateRateLimits counts the certificates and failures that weigh on a
// real issuance for these names.
func CertificateRateLimits(domains []string, failed []FailedIssue) RateLimits {
	return rateLimitsAt(letsencryptDir, domains, failed, time.Now())
}

func rateLimitsAt(dir string, domains []string, failed []FailedIssue, now time.Time) RateLimits {
	d := acmeDirectory()
	limits := RateLimits{
		Applies:    !d.configured() || (d.letsEncrypt() && !d.staging()),
		Duplicates: RateCount{Limit: duplicateLimit},
		Registered: []DomainRate{},
		Failures:   []DomainRate{},
		Scope:      "Counted from certbot's archive on this host and the issuance runs this dashboard started since it last restarted. Certificates other hosts obtain for the same domains count too and are not seen here.",
	}
	archive := readArchive(dir, now.Add(-rateWeek))
	var duplicates []time.Time
	for _, cert := range archive {
		if sameNameSet(cert.names, domains) {
			duplicates = append(duplicates, cert.notBefore)
		}
	}
	limits.Duplicates.Used = len(duplicates)
	limits.Duplicates.Frees = freesAt(duplicates, duplicateLimit, rateWeek)

	var registered []string
	seen := map[string]bool{}
	for _, name := range domains {
		if r := registeredDomain(name); !seen[r] {
			seen[r] = true
			registered = append(registered, r)
		}
	}
	sort.Strings(registered)
	for _, r := range registered {
		var issued []time.Time
		for _, cert := range archive {
			for _, n := range cert.names {
				if registeredDomain(n) == r {
					issued = append(issued, cert.notBefore)
					break
				}
			}
		}
		limits.Registered = append(limits.Registered, DomainRate{Domain: r, RateCount: RateCount{
			Used: len(issued), Limit: registeredLimit, Frees: freesAt(issued, registeredLimit, rateWeek),
		}})
	}

	for _, name := range domains {
		name = strings.ToLower(name)
		var at []time.Time
		for _, f := range failed {
			if !f.At.After(now.Add(-time.Hour)) {
				continue
			}
			for _, n := range f.Domains {
				if strings.EqualFold(n, name) {
					at = append(at, f.At)
					break
				}
			}
		}
		if len(at) > 0 {
			limits.Failures = append(limits.Failures, DomainRate{Domain: name, RateCount: RateCount{
				Used: len(at), Limit: failedValidationRate, Frees: freesAt(at, failedValidationRate, time.Hour),
			}})
		}
	}
	return limits
}

// freesAt is when a full window gets a slot back: the moment that has to
// leave it for the count to drop under the limit, plus the window.
func freesAt(times []time.Time, limit int, window time.Duration) *time.Time {
	if len(times) < limit {
		return nil
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	at := times[len(times)-limit].Add(window)
	return &at
}
