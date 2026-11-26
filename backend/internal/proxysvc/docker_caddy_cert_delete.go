package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Deleting a certificate the Docker ingress holds.
//
// Caddy obtains a certificate when a route names a domain and keeps it in its
// own volume after the route is gone, so a host that has deployed for a while
// lists certificates for domains nothing answers. The page offered Delete for
// certbot's lineages and the imports only, and to the operator whose every
// certificate is Caddy's that read as "I can't remove certificates".
//
// One Caddy still serves is never removed: the routes are read again at the
// moment of the delete, and a route whose name the certificate covers refuses
// it. What goes is the one directory Caddy keeps that certificate in.

// ErrNoCaddyIngress is a delete on a host with no Docker ingress to hold one.
var ErrNoCaddyIngress = errors.New("this server has no Caddy ingress holding certificates")

// ErrCaddyCertificateNotFound is a delete of a certificate Caddy does not hold.
var ErrCaddyCertificateNotFound = errors.New("Caddy holds no certificate at that path")

// ErrCaddyCertificatePath is a path that is not one of Caddy's certificates.
var ErrCaddyCertificatePath = errors.New("that is not the path of a certificate Caddy holds")

// CaddyCertificateInUseError refuses to delete a certificate a route of the
// ingress serves: Caddy would order it again at once, spending one of the
// authority's issuances, and the site would answer without it meanwhile.
type CaddyCertificateInUseError struct {
	Name   string
	Routes []string
}

func (e *CaddyCertificateInUseError) Error() string {
	return fmt.Sprintf("%s is served by %s. Remove the domain from what serves it first.", e.Name, strings.Join(e.Routes, ", "))
}

// CaddyCertificateDelete is what deleting one took with it.
type CaddyCertificateDelete struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Copies are the release copies removed with it, and KeptCopies the ones
	// a release still names and could be rolled back to.
	Copies     []string `json:"copies"`
	KeptCopies []string `json:"keptCopies"`
	// CopiesError says why the copies could not be cleared. The certificate
	// is gone either way.
	CopiesError string `json:"copiesError,omitempty"`
}

// caddyCertificateSegmentRe is one directory name under Caddy's certificates:
// an issuer's (acme-v02.api.letsencrypt.org-directory) or a name's
// (shop.example.com, wildcard_.example.com). It starts with a letter or a
// digit, so neither "." nor ".." is one.
var caddyCertificateSegmentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// caddyCertificateDir is the directory Caddy keeps the certificate at path
// in, for a path that is exactly <root>/<issuer>/<name>/<file>.crt as the
// inventory lists it. Anything else is refused: the answer is handed to rm.
func caddyCertificateDir(path string) (string, error) {
	rest, under := strings.CutPrefix(path, caddyCertificatesRoot+"/")
	if !under || filepath.Clean(path) != path {
		return "", ErrCaddyCertificatePath
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || !strings.HasSuffix(parts[2], ".crt") {
		return "", ErrCaddyCertificatePath
	}
	for _, part := range parts {
		if !caddyCertificateSegmentRe.MatchString(part) {
			return "", ErrCaddyCertificatePath
		}
	}
	return caddyCertificatesRoot + "/" + parts[0] + "/" + parts[1], nil
}

// caddyCertificateNames is the names a held certificate answers for: what it
// says itself, or, for one that could not be parsed, the name Caddy filed it
// under, which is all that is known about it.
func caddyCertificateNames(cert Certificate) []string {
	if len(cert.Domains) > 0 {
		return cert.Domains
	}
	if name, wildcard := strings.CutPrefix(cert.Name, "wildcard_"); wildcard {
		return []string{"*" + name}
	}
	return []string{cert.Name}
}

// routesServing is the routes among sites that answer for a name the
// certificate covers, wildcards included.
func routesServing(names []string, sites []VHost) []string {
	routes := []string{}
	for _, site := range sites {
		for _, host := range site.ServerNames {
			if covers(names, strings.ToLower(strings.TrimSpace(host))) && !slices.Contains(routes, site.Name) {
				routes = append(routes, site.Name)
			}
		}
	}
	sort.Strings(routes)
	return routes
}

// DeleteCaddyCertificate removes one certificate from the Docker ingress's
// storage, by the path the inventory lists it at. It refuses a certificate a
// route serves now, whatever the page showed a moment ago, and a Caddy whose
// routes cannot be read: not knowing is not "unused". If a route asks for the
// name later, Caddy orders a new certificate.
//
// released is every hostname a release on this host was built to serve. The
// release copies kept for the certificate's names go with it unless a release
// names them, since a rollback activates with its copy.
func (s *Service) DeleteCaddyCertificate(ctx context.Context, path string, released []string) (CaddyCertificateDelete, error) {
	out := CaddyCertificateDelete{Path: path, Copies: []string{}, KeptCopies: []string{}}
	dir, err := caddyCertificateDir(path)
	if err != nil {
		return out, err
	}
	out.Name = filepath.Base(dir)
	names, err := s.removeCaddyCertificate(ctx, path, dir)
	if err != nil {
		return out, err
	}
	var copies []string
	for _, name := range names {
		copies = append(copies, caddyEvidenceFor(name))
	}
	out.Copies, out.KeptCopies, err = s.pruneCopiesOf(ctx, released, copies)
	if err != nil {
		out.CopiesError = err.Error()
	}
	return out, nil
}

// removeCaddyCertificate checks and removes under the lock an activation
// writes its route under, so a route cannot claim the name between the two.
// It answers with the names the certificate covered.
func (s *Service) removeCaddyCertificate(ctx context.Context, path, dir string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return nil, err
	}
	if edge == nil {
		return nil, ErrNoCaddyIngress
	}
	held, err := edge.certificates(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not read Caddy's certificates: %w", err)
	}
	at := slices.IndexFunc(held, func(c Certificate) bool { return c.Path == path })
	if at < 0 {
		return nil, ErrCaddyCertificateNotFound
	}
	names := caddyCertificateNames(held[at])
	sites, err := edge.vhosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not read which Caddy routes use its certificates: %w", err)
	}
	if routes := routesServing(names, sites); len(routes) > 0 {
		return nil, &CaddyCertificateInUseError{Name: held[at].Name, Routes: routes}
	}
	// dir is <root>/<issuer>/<name> with every part checked, and the script
	// is fixed: nothing from the request is read as shell.
	if _, err := edge.command(ctx, "", "sh", "-c", `rm -rf -- "$1"`, "sh", dir); err != nil {
		return nil, fmt.Errorf("could not remove the certificate from Caddy's storage: %w", err)
	}
	return names, nil
}

// pruneCopiesOf removes those of the named release copies that exist and
// that no route and no release names, and says which were kept.
func (s *Service) pruneCopiesOf(ctx context.Context, released, names []string) (removed, kept []string, err error) {
	removed, kept = []string{}, []string{}
	evidence, err := s.ListCertificateEvidence(ctx)
	if err != nil {
		return removed, kept, err
	}
	var present []string
	for _, copy := range evidence {
		if slices.Contains(names, copy.Name) {
			present = append(present, copy.Name)
		}
	}
	if len(present) == 0 {
		return removed, kept, nil
	}
	pruned, err := s.PruneCertificateEvidence(ctx, released, present)
	return pruned.Removed, pruned.Kept, err
}
