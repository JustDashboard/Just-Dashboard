package proxysvc

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// A site's HSTS policy and TLS versions. The zero values write what the form
// always wrote: six months with includeSubDomains, TLS 1.2 and 1.3.
//
// There is no cipher profile. nginx picks the cipher list and the key
// exchange groups from the server that owns the socket before it reads the
// client's SNI, and switching to the named server afterwards carries over its
// protocol options but not its ciphers or curves. A cipher string in any site
// but the socket's default server does nothing, and with a catch-all default
// site that is every site. The protocol versions do follow the name, so
// "modern" is the one profile that changes what a visitor gets.

const (
	// hstsDefaultMaxAge is six months, what the form wrote before the
	// choice existed and the threshold tlsscan grades against.
	hstsDefaultMaxAge = 15552000
	// hstsPreloadMaxAge is the year the preload list requires.
	hstsPreloadMaxAge = 31536000
	hstsMinMaxAge     = 300
	hstsMaxMaxAge     = 63072000

	tlsProfileModern = "modern"
)

func validTLSOptions(spec *SiteSpec) error {
	if spec.HSTSMaxAge != 0 && (spec.HSTSMaxAge < hstsMinMaxAge || spec.HSTSMaxAge > hstsMaxMaxAge) {
		return fmt.Errorf("the HSTS max-age must be between %d seconds and two years", hstsMinMaxAge)
	}
	if spec.HSTSPreload {
		// The list's own rules: a submission that breaks either is refused
		// there, and a header that claims preload without them is a promise
		// the browsers will not keep.
		if spec.hstsMaxAge() < hstsPreloadMaxAge {
			return fmt.Errorf("HSTS preload needs a max-age of at least a year")
		}
		if spec.HSTSOwnNameOnly {
			return fmt.Errorf("HSTS preload needs includeSubDomains, so it cannot be limited to the site's own name")
		}
	}
	switch spec.TLSProfile {
	case "", tlsProfileModern:
	default:
		return fmt.Errorf("the TLS profile must be compatible or modern")
	}
	return nil
}

func (spec *SiteSpec) hstsMaxAge() int {
	if spec.HSTSMaxAge == 0 {
		return hstsDefaultMaxAge
	}
	return spec.HSTSMaxAge
}

func (spec *SiteSpec) hstsValue() string {
	v := "max-age=" + strconv.Itoa(spec.hstsMaxAge())
	if !spec.HSTSOwnNameOnly {
		v += "; includeSubDomains"
	}
	if spec.HSTSPreload {
		v += "; preload"
	}
	return v
}

func renderProtocols(l *lines, spec *SiteSpec) {
	if spec.TLSProfile == tlsProfileModern {
		l.add("    # TLS 1.3 only. Clients without it cannot connect at all.")
		l.add("    ssl_protocols TLSv1.3;")
		return
	}
	l.add("    # TLS 1.0 and 1.1 are retired and no current client needs them.")
	l.add("    ssl_protocols TLSv1.2 TLSv1.3;")
}

// readHSTS takes the policy back out of the header's value, quoted or not,
// with or without `always`. A max-age of six months reads back as the
// default so a file written before the choice saves unchanged.
func readHSTS(spec *SiteSpec, value string) {
	spec.HSTS = true
	_, policy, _ := strings.Cut(value, " ")
	policy = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(policy), " always"))
	policy = strings.Trim(policy, `"'`)
	spec.HSTSOwnNameOnly = true
	for _, part := range strings.Split(policy, ";") {
		key, val, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch strings.ToLower(key) {
		case "max-age":
			if n, err := strconv.Atoi(strings.Trim(val, `"`)); err == nil && n != hstsDefaultMaxAge {
				spec.HSTSMaxAge = n
			}
		case "includesubdomains":
			spec.HSTSOwnNameOnly = false
		case "preload":
			spec.HSTSPreload = true
		}
	}
}

// validPlainDomains checks the names a TLS site serves over plain HTTP:
// each is one of the site's domains, and a site without TLS, which serves
// every name that way, names none.
func validPlainDomains(spec *SiteSpec) error {
	if len(spec.PlainDomains) == 0 && !slices.ContainsFunc(spec.HostRedirects, func(r HostRedirect) bool { return r.Plain }) {
		return nil
	}
	if !spec.TLS {
		return fmt.Errorf("a site without TLS serves every name over plain HTTP, so none can be singled out")
	}
	seen := map[string]bool{}
	for _, name := range spec.PlainDomains {
		if !containsFold(spec.Domains, name) {
			return fmt.Errorf("%s is not one of the site's domains, so it cannot be served over plain HTTP", name)
		}
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("%s is listed twice as a plain-HTTP name", name)
		}
		seen[strings.ToLower(name)] = true
	}
	return nil
}

// servesPlain says whether the site answers the name over plain HTTP alone.
func (spec *SiteSpec) servesPlain(name string) bool {
	return !spec.TLS || containsFold(spec.PlainDomains, name)
}

// servedByScheme splits the site's domains into the names its TLS server
// answers and the names served over plain HTTP alone, each in the order
// the domains are listed.
func (spec *SiteSpec) servedByScheme() (tls, plain []string) {
	for _, name := range spec.Domains {
		if spec.servesPlain(name) {
			plain = append(plain, name)
		} else {
			tls = append(tls, name)
		}
	}
	return tls, plain
}

// plainCopy is the site as its plain-HTTP server renders it for the given
// names: the same everything, on port 80 alone, with nothing a certificate
// gives — no TLS, no redirect to HTTPS, no HSTS and no HTTP/2.
func (spec *SiteSpec) plainCopy(domains []string) *SiteSpec {
	plain := *spec
	plain.Domains = domains
	plain.PlainDomains = nil
	plain.TLS, plain.ForceHTTPS, plain.HSTS, plain.HTTP2 = false, false, false, false
	plain.CertPath, plain.KeyPath = "", ""
	return &plain
}
