package proxysvc

import "regexp"

// Caddy's release evidence.
//
// When the Docker ingress issues a certificate for a deployment, a copy is
// kept beside the imports as caddy-<24 hex digits> — the first 24 digits of
// the route digest of the certificate's first name — as a record of what the
// release was served with. Caddy renews the copy it serves and never these,
// so they are not certificates anybody manages: listed as imports they raised
// an expiry alarm apiece, named by hash and used by nothing.

var caddyEvidenceRe = regexp.MustCompile(`^caddy-[0-9a-f]{24}$`)

// isCaddyEvidence reports an import directory that is one of those copies.
func isCaddyEvidence(name string) bool {
	return caddyEvidenceRe.MatchString(name)
}

// keepCaddyEvidence stores or refreshes a copy. Refreshing is the point: a
// renewal Caddy made is the certificate the next release is served with.
func keepCaddyEvidence(name, certPEM, keyPEM string) (*ImportResult, error) {
	return importCertificate(name, certPEM, keyPEM, importOptions{replace: true})
}
