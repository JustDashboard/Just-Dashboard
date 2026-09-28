package proxysvc

import (
	"path/filepath"
	"regexp"
)

// Caddy's release evidence.
//
// When the Docker ingress issues a certificate for a deployment, a copy is
// kept beside the imports as caddy-<24 hex digits> — the first 24 digits of
// the route digest of the certificate's first name — as a record of what the
// release was served with. Deployments resolve their certificate to that copy,
// so ListCertificates keeps it. Caddy renews the copy it serves and never
// these, so they are not certificates anybody manages: in the operator's
// inventory they raised an expiry alarm apiece, named by hash and used by
// nothing, and CertificateInventory leaves them out.

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

// CaddyEvidence reports one of those copies that no nginx site names: what a
// deployment resolves to on a Docker Caddy host, and nothing the Certificates
// page lists. A copy a site does name is a certificate nginx serves and
// nothing renews, so it is listed, alarm and all.
func (c Certificate) CaddyEvidence() bool {
	return c.Source == "imported" && len(c.UsedBy) == 0 && isCaddyEvidence(filepath.Base(filepath.Dir(c.Path)))
}

func withoutCaddyEvidence(certs []Certificate) []Certificate {
	out := make([]Certificate, 0, len(certs))
	for _, c := range certs {
		if !c.CaddyEvidence() {
			out = append(out, c)
		}
	}
	return out
}
