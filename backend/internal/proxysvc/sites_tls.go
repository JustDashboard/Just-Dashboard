package proxysvc

import (
	"fmt"
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
