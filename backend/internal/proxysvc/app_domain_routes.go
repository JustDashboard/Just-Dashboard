package proxysvc

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

// A route that serves an app domain beside its other names.
//
// A deployment moved onto its just-dashboard.com address keeps the names it
// had: shop.example.com stays, and shop.acme.just-dashboard.com is added. No
// single certificate covers both, and none may be ordered that would: the
// app name is covered by the handle's wildcard and nothing else under the
// zone is ever ordered. So the route serves the app names with the wildcard's
// lineage and the rest with the certificate it resolved for them: a server of
// their own on nginx, a site block of their own on the Docker Caddy ingress.
// The split is only ever made for an app domain lineage, which is the one
// certificate a route is not allowed to order a replacement for; every other
// route still resolves one certificate for all of its names, as before.

// RouteCertificate is a set of a route's names served with a certificate
// other than the route's own.
type RouteCertificate struct {
	Domains  []string `json:"domains"`
	CertPath string   `json:"certPath"`
	KeyPath  string   `json:"keyPath"`
}

// appDomainPairs are the usable app domain lineages on this host.
func (s *Service) appDomainPairs(ctx context.Context) []certificatePair {
	certificates, err := s.ListCertificates(ctx)
	if err != nil {
		return nil
	}
	var pairs []certificatePair
	for _, certificate := range certificates {
		name := filepath.Base(filepath.Dir(certificate.Path))
		if !isAppDomainLineage(name) || certificate.Error != "" || certificate.Expired || (certificate.Staging && !acmeDirectory().staging()) {
			continue
		}
		pairs = append(pairs, certificatePair{
			Certificate: certificate, KeyPath: filepath.Join(filepath.Dir(certificate.Path), "privkey.pem"),
		})
	}
	return pairs
}

// splitAppDomainNames sets aside the names an app domain lineage covers. It
// answers the rest, the pair covering the first name set aside, and whether
// any was.
func (s *Service) splitAppDomainNames(ctx context.Context, domains []string) ([]string, []RouteCertificate) {
	pairs := s.appDomainPairs(ctx)
	if len(pairs) == 0 {
		return domains, nil
	}
	rest := []string{}
	var groups []RouteCertificate
	for _, domain := range domains {
		placed := false
		for _, pair := range pairs {
			if !certificateCoversAll(pair.Domains, []string{domain}) {
				continue
			}
			index := slices.IndexFunc(groups, func(g RouteCertificate) bool { return g.CertPath == pair.Path })
			if index < 0 {
				groups = append(groups, RouteCertificate{CertPath: pair.Path, KeyPath: pair.KeyPath})
				index = len(groups) - 1
			}
			groups[index].Domains = append(groups[index].Domains, domain)
			placed = true
			break
		}
		if !placed {
			rest = append(rest, domain)
		}
	}
	return rest, groups
}

// resolveAroundAppDomains is ResolveDeploymentCertificate for names no single
// certificate covers: the app names are set aside for their wildcard, and the
// rest resolved alone. With nothing but app names left, the wildcard is the
// route's own certificate.
func (s *Service) resolveAroundAppDomains(ctx context.Context, domains []string) (string, string, bool, error) {
	rest, groups := s.splitAppDomainNames(ctx, domains)
	if len(groups) == 0 {
		return "", "", false, nil
	}
	if len(rest) == 0 {
		if len(groups) > 1 {
			return "", "", true, errors.New("the route's names fall under two app domain certificates")
		}
		return groups[0].CertPath, groups[0].KeyPath, true, nil
	}
	pairs, err := s.coveringCertificates(ctx, rest)
	if err != nil {
		return "", "", true, err
	}
	if len(pairs) > 0 {
		return pairs[0].Path, pairs[0].KeyPath, true, nil
	}
	if pair, ok := s.caddyEvidenceForEach(ctx, rest); ok {
		return pair.Path, pair.KeyPath, true, nil
	}
	return "", "", true, errors.New("no available certificate covers every deployment domain")
}

// withAppDomainCertificates gives a TLS route's app names their wildcard,
// when the route's own certificate does not cover them.
func (s *Service) withAppDomainCertificates(ctx context.Context, route DeploymentRoute) DeploymentRoute {
	if !route.TLS || len(route.Certificates) > 0 || route.CertPath == "" {
		return route
	}
	if isAppDomainLineage(filepath.Base(filepath.Dir(route.CertPath))) {
		return route
	}
	var own []string
	if !isCaddyEvidence(filepath.Base(filepath.Dir(route.CertPath))) {
		if certificates, err := s.ListCertificates(ctx); err == nil {
			for _, certificate := range certificates {
				if certificate.Path == route.CertPath {
					own = certificate.Domains
				}
			}
		}
	}
	tlsServed, _ := route.servedByScheme()
	var candidates []string
	for _, name := range tlsServed {
		if len(own) == 0 || !certificateCoversAll(own, []string{name}) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return route
	}
	_, groups := s.splitAppDomainNames(ctx, candidates)
	route.Certificates = groups
	return route
}

// certificateGroupNames are the names a route serves with another
// certificate.
func (route DeploymentRoute) certificateGroupNames() map[string]bool {
	names := map[string]bool{}
	for _, group := range route.Certificates {
		for _, name := range group.Domains {
			names[strings.ToLower(name)] = true
		}
	}
	return names
}

// copySource is the one certificate a Docker Caddy route serves a copy of:
// its own when that is explicit, else its app names' wildcard. A route
// serves at most one copy, which is what the refresh of copies after a
// renewal keeps current (docker_caddy_copies.go).
func (route DeploymentRoute) copySource() (string, string, bool) {
	if servesExplicitCertificate(route) {
		return route.CertPath, route.KeyPath, true
	}
	if route.TLS && len(route.Certificates) == 1 {
		return route.Certificates[0].CertPath, route.Certificates[0].KeyPath, true
	}
	return "", "", false
}

// validateRouteCertificates holds a route's extra certificates to what the
// renderers can write.
func validateRouteCertificates(route DeploymentRoute, caddy bool) error {
	if len(route.Certificates) == 0 {
		return nil
	}
	if !route.TLS {
		return errors.New("a route served over plain HTTP has no certificates to split")
	}
	served := map[string]bool{}
	for _, name := range route.Domains {
		served[strings.ToLower(name)] = true
	}
	for _, group := range route.Certificates {
		if group.CertPath == "" || group.KeyPath == "" || len(group.Domains) == 0 {
			return errors.New("a route's extra certificate needs its names, its certificate and its key")
		}
		for _, name := range group.Domains {
			if !served[strings.ToLower(name)] || route.plain(name) {
				return errors.New(name + " is given a certificate of its own but is not one of the route's HTTPS names")
			}
		}
	}
	if caddy && (servesExplicitCertificate(route) || len(route.Certificates) > 1) {
		return errors.New("on the Docker Caddy ingress a route serves at most one certificate copy, and this one would serve two; serve its other names with Caddy's own certificates, or move them to a route of their own")
	}
	return nil
}
