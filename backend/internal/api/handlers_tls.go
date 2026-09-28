package api

import (
	"net/http"
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
	httpx.JSON(w, http.StatusOK, scan)
	return nil
}

func (s *Server) handleDomainDNS(w http.ResponseWriter, r *http.Request) error {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		return httpx.BadRequest("domain query parameter is required")
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, proxysvc.CheckDomainDNS(ctx, domain))
	return nil
}
