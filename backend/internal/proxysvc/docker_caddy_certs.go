package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
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
// nothing. CertificateInventory lists what Caddy serves instead, each copy
// folded under the certificate for its domain, and a copy whose domain no
// route and no release names any more can be pruned.

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

// CertificateEvidence is one release copy, as it is folded under the
// certificate Caddy serves for its domain and listed for pruning.
type CertificateEvidence struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Domains  []string  `json:"domains"`
	NotAfter time.Time `json:"notAfter"`
}

// caddyEvidenceFor is the directory a copy for this hostname is kept in, as
// ensureDockerCaddyCertificate names it.
func caddyEvidenceFor(hostname string) string {
	return "caddy-" + strings.TrimPrefix(routeDigest(strings.ToLower(strings.TrimSpace(hostname))), "sha256:")[:24]
}

// splitCaddyEvidence separates the release copies from everything else.
func splitCaddyEvidence(certs []Certificate) ([]Certificate, []CertificateEvidence) {
	out := make([]Certificate, 0, len(certs))
	evidence := []CertificateEvidence{}
	for _, c := range certs {
		if !c.CaddyEvidence() {
			out = append(out, c)
			continue
		}
		evidence = append(evidence, CertificateEvidence{
			Name: filepath.Base(filepath.Dir(c.Path)), Path: filepath.Dir(c.Path),
			Domains: c.Domains, NotAfter: c.NotAfter,
		})
	}
	return out, evidence
}

func withoutCaddyEvidence(certs []Certificate) []Certificate {
	out, _ := splitCaddyEvidence(certs)
	return out
}

// ListCertificateEvidence is every release copy no nginx site names.
func (s *Service) ListCertificateEvidence(ctx context.Context) ([]CertificateEvidence, error) {
	certs, err := s.ListCertificates(ctx)
	if err != nil {
		return nil, err
	}
	_, evidence := splitCaddyEvidence(certs)
	return evidence, nil
}

// caddyCertificatesRoot is where the Docker ingress keeps what it issued:
// <root>/<issuer>/<domain>/<domain>.crt, beside the key and Caddy's metadata.
const caddyCertificatesRoot = "/data/caddy/certificates"

// caddyCertificateMark separates one file from the next in the single read
// certificates makes; no PEM line starts with it.
const caddyCertificateMark = "==> "

// certificates reads every certificate Caddy's storage holds, in one exec: a
// fixed script, the directory its only argument.
func (c *dockerCaddy) certificates(ctx context.Context) ([]Certificate, error) {
	raw, err := c.command(ctx, "", "sh", "-c",
		`[ -d "$1" ] || exit 0; find "$1" -type f -name '*.crt' | while IFS= read -r f; do printf '%s%s\n' "$2" "$f"; cat "$f"; echo; done`,
		"sh", caddyCertificatesRoot, caddyCertificateMark)
	if err != nil {
		return nil, err
	}
	out := []Certificate{}
	for _, chunk := range strings.Split(string(raw), "\n"+caddyCertificateMark) {
		chunk = strings.TrimPrefix(chunk, caddyCertificateMark)
		path, content, _ := strings.Cut(chunk, "\n")
		if !strings.HasPrefix(path, caddyCertificatesRoot+"/") {
			continue
		}
		// Caddy names the directory after the name it obtained the
		// certificate for, which is the name the operator knows it by.
		name := filepath.Base(filepath.Dir(path))
		leaf, _, err := parseCertChain(content)
		if err != nil {
			out = append(out, Certificate{Name: name, Path: path, Domains: []string{}, UsedBy: []string{}, Source: "caddy", Error: err.Error()})
			continue
		}
		cert := summarise(leaf, name, path)
		cert.Source = "caddy"
		// Caddy renews on its own schedule, a third of the term before
		// expiry, so the renewal window says nothing the operator must act
		// on. An expired one is still expired.
		cert.Expiring = false
		out = append(out, *cert)
	}
	return out, nil
}

// caddyInventory is what the Docker ingress serves, each certificate used by
// the routes whose names it covers and carrying the release copies kept for
// its domain. A copy with no certificate to fold under stays out of the list:
// it is either a domain Caddy has let go or evidence to prune.
func (s *Service) caddyInventory(ctx context.Context, evidence []CertificateEvidence) ([]Certificate, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil || edge == nil {
		return nil, err
	}
	live, err := edge.certificates(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not read Caddy's certificates: %w", err)
	}
	if len(live) == 0 {
		return live, nil
	}
	sites, err := edge.vhosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not read which Caddy routes use its certificates: %w", err)
	}
	for i := range live {
		cert := &live[i]
		for _, site := range sites {
			for _, name := range site.ServerNames {
				if covers(cert.Domains, strings.ToLower(name)) && !slices.Contains(cert.UsedBy, site.Name) {
					cert.UsedBy = append(cert.UsedBy, site.Name)
				}
			}
		}
		for _, kept := range evidence {
			for _, domain := range cert.Domains {
				if kept.Name == caddyEvidenceFor(domain) {
					cert.Evidence = append(cert.Evidence, kept)
					break
				}
			}
		}
	}
	return live, nil
}

// caddyInventoryFailure stands in for Caddy's certificates when they could
// not be read, so the page says so rather than listing none.
func caddyInventoryFailure(err error) Certificate {
	return Certificate{
		Name: "Caddy", Path: caddyCertificatesRoot, Domains: []string{}, UsedBy: []string{},
		Source: "caddy", Error: err.Error(),
	}
}

// OrphanedCertificateEvidence is the release copies whose domain no Caddy
// route serves and no release names. released is every hostname a release
// on this host was built to serve, from the deployment store; a release can
// be rolled back to, and the copy is what it activates with.
func (s *Service) OrphanedCertificateEvidence(ctx context.Context, released []string) ([]CertificateEvidence, error) {
	evidence, err := s.ListCertificateEvidence(ctx)
	if err != nil || len(evidence) == 0 {
		return evidence, err
	}
	referenced := map[string]bool{}
	for _, hostname := range released {
		referenced[caddyEvidenceFor(hostname)] = true
	}
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return nil, err
	}
	if edge != nil {
		sites, err := edge.vhosts(ctx)
		if err != nil {
			return nil, fmt.Errorf("could not read Caddy's routes: %w", err)
		}
		for _, site := range sites {
			for _, name := range site.ServerNames {
				referenced[caddyEvidenceFor(name)] = true
			}
		}
	}
	orphans := []CertificateEvidence{}
	for _, kept := range evidence {
		if !referenced[kept.Name] {
			orphans = append(orphans, kept)
		}
	}
	return orphans, nil
}

// EvidencePrune is what a prune did: the copies it removed, and the ones
// asked for that a route or release names again, or that are already gone.
type EvidencePrune struct {
	Removed []string `json:"removed"`
	Kept    []string `json:"kept"`
}

// PruneCertificateEvidence removes the named copies that are still orphans
// now. The operator confirmed a list read a moment ago; a deployment since
// may have claimed one of those domains again, and that copy stays.
func (s *Service) PruneCertificateEvidence(ctx context.Context, released, names []string) (EvidencePrune, error) {
	result := EvidencePrune{Removed: []string{}, Kept: []string{}}
	for _, name := range names {
		if !isCaddyEvidence(name) {
			return result, fmt.Errorf("%q is not a Caddy release copy", name)
		}
	}
	orphans, err := s.OrphanedCertificateEvidence(ctx, released)
	if err != nil {
		return result, err
	}
	orphan := map[string]bool{}
	for _, kept := range orphans {
		orphan[kept.Name] = true
	}
	var errs []error
	for _, name := range names {
		if !orphan[name] {
			result.Kept = append(result.Kept, name)
			continue
		}
		// The name matched caddy-<hex> above, so the join stays inside
		// importedDir.
		if err := os.RemoveAll(filepath.Join(importedDir, name)); err != nil {
			errs = append(errs, err)
			continue
		}
		result.Removed = append(result.Removed, name)
	}
	sort.Strings(result.Removed)
	sort.Strings(result.Kept)
	return result, errors.Join(errs...)
}
