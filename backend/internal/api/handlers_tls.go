package api

import (
	"context"
	"net"
	"net/http"
	"strconv"
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
// handlers_domains.go, and the stored reports and the schedule's history in
// handlers_tls_history.go, and the scan of every site in handlers_tls_fleet.go.
func (s *Server) mountTLSRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/watched", s.handle(s.handleWatchedDomains))
	r.Method(http.MethodGet, "/watch-schedule", s.handle(s.handleWatchSettings))
	r.Method(http.MethodGet, "/watched/{id}/history", s.handle(s.handleWatchedHistory))
	r.Method(http.MethodGet, "/reports", s.handle(s.handleTLSScans))
	r.Method(http.MethodGet, "/reports/{id}", s.handle(s.handleTLSStoredScan))
	r.Method(http.MethodGet, "/scans/latest", s.handle(s.handleLatestTLSScans))
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
		r.Method(http.MethodGet, "/scan/deep", s.handle(s.handleTLSDeepScan))
		r.Method(http.MethodPost, "/watched/check", s.handle(s.handleCheckWatchedNow))
		r.Method(http.MethodPut, "/watch-schedule", s.handle(s.handleSetWatchSettings))
		r.Method(http.MethodPost, "/scan-all", s.handle(s.handleTLSScanAll))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/reports", s.handle(s.handleClearTLSScans))
		})
	})
}

// mountProxyToolRoutes is the operator's own tools under /proxy/tools. The
// request tester sends traffic somewhere the caller chooses, which is the
// scanner boundary above, and the directive finder serves the TLS page's
// fixes, which only an administrator can carry out; so the whole subtree is
// admin-only rather than each route remembering to be.
func (s *Server) mountProxyToolRoutes(r chi.Router) {
	r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
	r.Method(http.MethodPost, "/request", s.handle(s.handleRequestTest))
	r.Method(http.MethodGet, "/directive", s.handle(s.handleFindDirective))
}

// handleFindDirective lists every place ?name= is set in the configuration
// nginx loads, so a finding about protocols can open the line behind it.
func (s *Server) handleFindDirective(w http.ResponseWriter, r *http.Request) error {
	name := r.URL.Query().Get("name")
	if !proxysvc.IsDirectiveName(name) {
		return httpx.BadRequest("%q is not an nginx directive name", name)
	}
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	uses, err := s.modules.proxy.FindDirective(ctx, name)
	if err != nil {
		return mapProxyError(err)
	}
	httpx.JSON(w, http.StatusOK, uses)
	return nil
}

// handleRequestTest sends one request to this machine's nginx for one of its
// sites, dialling loopback and naming the site in SNI and Host. It is audited
// because the method is the caller's: a POST reaches the site's application
// like any other.
func (s *Server) handleRequestTest(w http.ResponseWriter, r *http.Request) error {
	var req proxysvc.RequestTest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	vhosts, err := s.modules.proxy.ListVHosts(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	certs, err := s.modules.proxy.ListCertificates(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "proxy.tools.request", strings.ToUpper(req.Method)+" "+req.URL, nil)
	result, err := proxysvc.TestRequest(ctx, req, vhosts, certs)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
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
	opts := proxysvc.ScanOptions{AllAddresses: r.URL.Query().Get("all") == "1"}
	if raw := r.URL.Query().Get("connect"); raw != "" {
		if opts.AllAddresses {
			return httpx.BadRequest("connect to dials one address; scanning every address asks the name's own")
		}
		connect, err := proxysvc.ParseConnectTo(raw, target)
		if err != nil {
			return httpx.BadRequest("%v", err)
		}
		opts.ConnectTo = connect
		// A scan is a read and the audit middleware skips reads, but this one
		// sends the name's handshakes and requests to an address of the
		// caller's choosing, so it leaves a trail.
		httpx.AuditRead(s.Audit, r, "certificates.scan.connect",
			net.JoinHostPort(target.Host, strconv.Itoa(target.Port))+" via "+connect)
	}
	q := r.URL.Query()
	// A service that upgrades with STARTTLS is asked through its own
	// dialogue; Auto (no ?proto=) picks it by port.
	opts.StartTLS, err = proxysvc.ParseStartTLS(q.Get("proto"), target.Port)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	opts.Request, err = proxysvc.ParseRequestShape(q.Get("method"), q.Get("path"), q.Get("host"))
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if opts.Request.Host != "" {
		// Another Host sent to the name's server reaches whichever of its
		// sites answers to it, which may be one the name does not front, so
		// it leaves a trail as connect does.
		httpx.AuditRead(s.Audit, r, "certificates.scan.host",
			net.JoinHostPort(target.Host, strconv.Itoa(target.Port))+" as "+opts.Request.Host)
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	scan := proxysvc.ScanTLSWith(ctx, target.Host, target.Port, opts)
	// A scan cut short by its caller is not what the target serves, so only
	// one that ran to its end is kept for the history — and not one sent to
	// an address of the caller's choosing, which is not what the name's own
	// DNS would reach.
	if r.Context().Err() == nil && opts.ConnectTo == "" {
		if _, err := s.storeScan(context.WithoutCancel(r.Context()), scan); err != nil {
			return httpx.Internal(err)
		}
	}
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
