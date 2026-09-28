package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountTLSRoutes is what a domain actually serves, as opposed to what is on
// disk: the live certificate check, the TLS report, DNS, and the watched
// domains checked on a schedule. The watch list itself is in
// handlers_domains.go.
func (s *Server) mountTLSRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/watched", s.handle(s.handleWatchedDomains))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// These checks emit traffic to caller-chosen destinations. Keeping
		// them with the host-administration routes prevents a read-only
		// account from turning the server into an internal network scanner.
		r.Method(http.MethodGet, "/check", s.handle(s.handleCertCheck))
		r.Method(http.MethodGet, "/scan", s.handle(s.handleTLSScan))
		r.Method(http.MethodGet, "/dns", s.handle(s.handleDomainDNS))
		r.Method(http.MethodPut, "/dns/resolvers", s.handle(s.handleSetDNSResolvers))
		r.Method(http.MethodPost, "/watched", s.handle(s.handleWatchDomain))
		r.Method(http.MethodDelete, "/watched/{id}", s.handle(s.handleUnwatchDomain))
	})
}

// mountProxyToolRoutes is the operator's own probes under /proxy/tools. Every
// one of them sends traffic somewhere the caller chooses, which is the scanner
// boundary above, so the whole subtree is admin-only rather than each route
// remembering to be.
func (s *Server) mountProxyToolRoutes(r chi.Router) {
	r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
}

// scanTarget reads ?domain= and ?port= the way the page does, so a pasted
// URL or host:port reaches the host it names, and anything that is not an
// address is refused with the reason before a packet leaves.
func scanTarget(r *http.Request) (proxysvc.ScanTarget, error) {
	target, err := proxysvc.ParseScanQuery(r.URL.Query().Get("domain"), r.URL.Query().Get("port"))
	if err != nil {
		return proxysvc.ScanTarget{}, httpx.BadRequest("%v", err)
	}
	return target, nil
}

func (s *Server) handleCertCheck(w http.ResponseWriter, r *http.Request) error {
	target, err := scanTarget(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	cert, err := proxysvc.CheckDomain(ctx, target.Host, target.Port)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, cert)
	return nil
}

// handleTLSScan is the live report: protocol versions, the chain as presented,
// and the headers the site actually sends. Everything else on this page reads
// files, and a file is not what a visitor gets.
func (s *Server) handleTLSScan(w http.ResponseWriter, r *http.Request) error {
	target, err := scanTarget(r)
	if err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	scan := proxysvc.ScanTLS(ctx, target.Host, target.Port)
	if scan.Certificate != nil {
		// The trace is a reading beside the scan: a host whose sites cannot
		// be listed still gets its report, only without the site behind it.
		vhosts, err := s.modules.proxy.ListVHosts(ctx)
		if err == nil {
			certs, err := s.modules.proxy.ListCertificates(ctx)
			if err == nil {
				scan.Origin = proxysvc.TraceOrigin(vhosts, certs, scan.Domain, scan.Port, scan.Certificate)
			}
		}
	}
	httpx.JSON(w, http.StatusOK, scan)
	return nil
}

func (s *Server) handleDomainDNS(w http.ResponseWriter, r *http.Request) error {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		return httpx.BadRequest("domain query parameter is required")
	}
	if r.URL.Query().Get("deep") == "1" {
		return s.handleDeepDNS(w, r, domain)
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, proxysvc.CheckDomainDNS(ctx, domain))
	return nil
}

// dnsResolversSetting holds the resolvers the DNS panel compares the system
// resolver against, comma-separated. Absent means the defaults.
const dnsResolversSetting = "proxy.dns.resolvers"

func (s *Server) dnsResolvers(r *http.Request) ([]string, error) {
	raw, ok, err := s.Store.Setting(r.Context(), dnsResolversSetting)
	if err != nil || !ok {
		return proxysvc.DefaultDNSResolvers, err
	}
	list, err := proxysvc.ParseDNSResolvers(strings.Split(raw, ","))
	if err != nil || len(list) == 0 {
		return proxysvc.DefaultDNSResolvers, nil
	}
	return list, nil
}

// handleDeepDNS is the TLS page's DNS panel: records with their TTLs, the
// CAA walk and the propagation table, asked on the wire rather than through
// the system resolver, which hides all three.
func (s *Server) handleDeepDNS(w http.ResponseWriter, r *http.Request, domain string) error {
	name, err := proxysvc.NormalizeDNSName(domain)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	resolvers, err := s.dnsResolvers(r)
	if err != nil {
		return httpx.Internal(err)
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, proxysvc.InspectDNS(ctx, name, resolvers))
	return nil
}

type dnsResolversRequest struct {
	Resolvers []string `json:"resolvers"`
}

// handleSetDNSResolvers saves the comparison list. An empty list restores the
// defaults rather than leaving the table with only the system resolver, which
// would have nothing to compare.
func (s *Server) handleSetDNSResolvers(w http.ResponseWriter, r *http.Request) error {
	var req dnsResolversRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	list, err := proxysvc.ParseDNSResolvers(req.Resolvers)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if len(list) == 0 {
		list = proxysvc.DefaultDNSResolvers
	}
	if err := s.Store.SetSetting(r.Context(), dnsResolversSetting, strings.Join(list, ",")); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "certificates.dns.resolvers", strings.Join(list, ","), nil)
	httpx.JSON(w, http.StatusOK, dnsResolversRequest{Resolvers: list})
	return nil
}
