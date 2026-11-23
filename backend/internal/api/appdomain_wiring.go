package api

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/appdomain"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// initAppDomain makes the just-dashboard.com address's service over the
// account link. It is built whether or not JD_APP_DOMAINS is on, so the
// Settings card can say it is off; off, it starts nothing and sends nothing.
func (s *Server) initAppDomain() {
	if s.modules.accountLink == nil {
		return
	}
	var certs appdomain.Certificates
	if s.modules.proxy != nil {
		certs = s.modules.proxy
	}
	s.modules.appDomain = appdomain.New(appdomain.Options{
		Store: s.Store, Link: s.modules.accountLink, Certs: certs, Jobs: s.modules.jobs,
		Audit: s.Audit, Log: s.Log, Enabled: s.Cfg.AppDomains,
		DataDir: s.Cfg.DataDir, BackendURL: appdomain.BackendURL(s.Cfg.Addr),
	})
	if s.modules.proxy != nil {
		// Neither proxy ever orders a certificate for a name under the
		// zone: the handle's wildcard is the only one there.
		svc := s.modules.appDomain
		s.modules.proxy.SetAppDomainZone(func() string {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return svc.Zone(ctx)
		})
	}
}

// appDomainOnClear is the account link's OnClear hook: the transaction that
// ends a link, or starts a new one, detaches the handle the old link held.
// Once it has committed the app domain asks the site at once, so a link just
// approved claims the address the account chose for it there (an offer)
// without waiting for the hourly check.
func (s *Server) appDomainOnClear(ctx context.Context, tx *sql.Tx) (func(), error) {
	if err := appdomain.DetachInTx(ctx, tx, time.Now().Unix()); err != nil {
		return nil, err
	}
	return func() {
		if svc := s.modules.appDomain; svc != nil {
			svc.LinkChanged()
		}
	}, nil
}

// appDomainNames is the deployment executor's view of the app domain.
type appDomainNames struct{ s *Server }

func (n appDomainNames) DetachedAppDomain(ctx context.Context, hostname string) bool {
	svc := n.s.modules.appDomain
	return svc != nil && svc.Classify(ctx, hostname) == appdomain.Detached
}

// appDomainCertificateIssuer is a deployment's certificate step aware of the
// app domain. Names under the handle this server holds are the wildcard's,
// issued through the site when it is missing or due, and never ordered one
// by one; the rest go to the Certificates feature as before. Activation then
// serves each set with its own certificate (proxysvc/app_domain_routes.go).
type appDomainCertificateIssuer struct {
	s      *Server
	issuer deploy.CertificateIssuer
}

func (a appDomainCertificateIssuer) EnsureDeploymentCertificate(
	ctx context.Context,
	domains []string,
	log func(stream, text string),
) (proxysvc.DeploymentCertificate, error) {
	svc := a.s.modules.appDomain
	if svc == nil {
		return a.issuer.EnsureDeploymentCertificate(ctx, domains, log)
	}
	var held, rest []string
	for _, domain := range domains {
		switch svc.Classify(ctx, domain) {
		case appdomain.Held:
			held = append(held, strings.ToLower(domain))
		case appdomain.Detached:
			// The executor leaves these out of the release; one reaching
			// here is refused rather than ordered.
			return proxysvc.DeploymentCertificate{}, appdomain.ErrNotHeldName
		default:
			rest = append(rest, domain)
		}
	}
	if len(held) == 0 {
		return a.issuer.EnsureDeploymentCertificate(ctx, domains, log)
	}
	wildcard, err := svc.EnsureCertificate(ctx, held, log)
	if err != nil || len(rest) == 0 {
		return wildcard, err
	}
	if log != nil {
		log("status", "Covered "+strings.Join(held, ", ")+" with "+wildcard.Name+"; resolving a certificate for "+strings.Join(rest, ", "))
	}
	return a.issuer.EnsureDeploymentCertificate(ctx, rest, log)
}
