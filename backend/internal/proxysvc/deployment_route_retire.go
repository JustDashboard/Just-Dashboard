package proxysvc

import (
	"context"
	"slices"
	"strings"
)

// RetiredRoute is what retiring a deployment's route took with it.
type RetiredRoute struct {
	Name string `json:"name"`
	// Certificates are the names whose certificate Caddy kept only for this
	// route, removed from its storage with it.
	Certificates []string `json:"certificates"`
	// CertificateError says why those could not be removed. The route is
	// gone either way: a certificate nothing serves is only clutter.
	CertificateError string `json:"certificateError,omitempty"`
}

// RetireDeploymentRoute removes a route the deployment that wrote it no
// longer will: its project deleted or archived. Nothing else ever removed
// one — the proxy pages offered no Delete on a deployment's route, rightly
// while it deploys, and so a route outlived its project, still serving its
// domain and listed with its certificate on every proxy page.
//
// On the Docker Caddy ingress it also drops what Caddy keeps for the names
// only that route served: Caddy renews what it holds whether or not a route
// asks for it, so the certificate stayed listed, and renewed, for a domain
// nothing answers.
func (s *Service) RetireDeploymentRoute(ctx context.Context, name string) (RetiredRoute, error) {
	out := RetiredRoute{Name: name, Certificates: []string{}}
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return out, err
	}
	var served []string
	if edge != nil {
		sites, err := edge.vhosts(ctx)
		if err != nil {
			return out, err
		}
		for _, site := range sites {
			if site.Name == name {
				served = site.ServerNames
			}
		}
	}
	if err := s.RemoveDeploymentRoute(ctx, name); err != nil {
		return out, err
	}
	if edge == nil || len(served) == 0 {
		return out, nil
	}
	left, err := edge.vhosts(ctx)
	if err != nil {
		out.CertificateError = err.Error()
		return out, nil
	}
	still := map[string]bool{}
	for _, site := range left {
		for _, host := range site.ServerNames {
			still[strings.ToLower(host)] = true
		}
	}
	for _, host := range orphanedCaddyNames(served, still) {
		// Caddy keeps a name's certificate at <root>/<issuer>/<name>/, one
		// directory per issuer that signed it. The name is checked as a
		// domain first, so it cannot name anything but such a directory.
		if _, err := edge.command(ctx, "", "sh", "-c", `rm -rf "$1"/*/"$2"`, "sh", caddyCertificatesRoot, host); err != nil {
			out.CertificateError = err.Error()
			continue
		}
		out.Certificates = append(out.Certificates, host)
	}
	return out, nil
}

// orphanedCaddyNames is the plain domains among served that no remaining
// route answers for. A wildcard, or anything that is not a domain, is left
// alone: it is not a directory name Caddy writes from a managed route.
func orphanedCaddyNames(served []string, still map[string]bool) []string {
	out := []string{}
	for _, host := range served {
		host = strings.ToLower(strings.TrimSpace(host))
		if still[host] || strings.HasPrefix(host, "*.") || !certDomainRe.MatchString(host) || slices.Contains(out, host) {
			continue
		}
		out = append(out, host)
	}
	return out
}
