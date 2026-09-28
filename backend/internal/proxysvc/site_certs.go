package proxysvc

import (
	"context"
	"fmt"
	"sort"
)

// SiteCertificate is a certificate the site form can switch a site onto: one
// covering every domain it serves, with the key that goes with it. Paths and
// facts about the certificate only, never key material.
type SiteCertificate struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	KeyPath  string   `json:"keyPath"`
	Domains  []string `json:"domains"`
	Issuer   string   `json:"issuer"`
	DaysLeft int      `json:"daysLeft"`
	Source   string   `json:"source"`
}

// SiteCertificates lists the usable certificates covering every one of
// domains, a wildcard covering one label, the longest-lived first: of two
// that fit, the one that will not need renewing soonest is the better pick.
func (s *Service) SiteCertificates(ctx context.Context, domains []string) ([]SiteCertificate, error) {
	if len(domains) == 0 {
		return nil, fmt.Errorf("at least one domain is required")
	}
	for _, domain := range domains {
		if !certDomainRe.MatchString(domain) {
			return nil, fmt.Errorf("%q is not a valid domain name", domain)
		}
	}
	pairs, err := s.coveringCertificates(ctx, domains)
	if err != nil {
		return nil, err
	}
	out := make([]SiteCertificate, 0, len(pairs))
	for _, pair := range pairs {
		out = append(out, SiteCertificate{
			Name: pair.Name, Path: pair.Path, KeyPath: pair.KeyPath, Domains: pair.Domains,
			Issuer: pair.Issuer, DaysLeft: pair.DaysLeft, Source: pair.Source,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DaysLeft > out[j].DaysLeft })
	return out, nil
}

// SiteACMEWebroot is the folder a site saved with ManagedACME serves its
// HTTP-01 challenge from, and so the webroot the site form issues into.
const SiteACMEWebroot = deploymentACMEWebroot
