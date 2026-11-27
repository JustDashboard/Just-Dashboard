package blueprint

import (
	"regexp"
	"strings"
)

// addressLabelRE is a stack service's address label: one DNS label of at
// most 20 characters, so the name it makes beside a domain stays short
// enough to read and to fit a certificate.
var addressLabelRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,18}[a-z0-9])?$`)

// ServiceHostname is the address a stack's service is given beside the
// deployment's domain. A name with three labels or more gets a sibling,
// media.example.com and "requests" making media-requests.example.com; an
// apex gets a subdomain, example.com making requests.example.com.
//
// Siblings rather than sub-labels, because a wildcard certificate for
// *.example.com covers them, a just-dashboard.com app domain's wildcard
// covers <app>-requests.<handle>.<zone> as another app name, and sslip.io
// resolves them; none of those covers requests.media.example.com.
func ServiceHostname(domain, label string) string {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	label = strings.ToLower(strings.TrimSpace(label))
	if domain == "" || label == "" {
		return ""
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 3 {
		return label + "." + domain
	}
	// A DNS label holds 63 characters: a long first label gives way at its
	// end, so the service's own label is never the part that is cut.
	first := labels[0]
	if room := 63 - len(label) - 1; len(first) > room {
		first = strings.TrimRight(first[:max(room, 1)], "-")
	}
	return first + "-" + label + "." + strings.Join(labels[1:], ".")
}
