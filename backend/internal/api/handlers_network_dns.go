package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// mountNetworkDNSRoutes mounts the resolver, its upstreams and the host's records under /network.
//
// Reading the resolver chain and the records is `read`, like every other
// network reading that does not name who connects. Changing the upstreams is
// system.admin and inside s.destructive: a wrong upstream does not break one
// page, it breaks name resolution for every program on the host, the
// dashboard's own updates and certificate renewals among them. The module
// checks that a name still resolves afterwards and puts the old settings back
// if it does not; the confirmation is for the time between.
func (s *Server) mountNetworkDNSRoutes(r chi.Router) {
	r.Route("/dns", func(r chi.Router) {
		s.mountNetworkDNSEvidenceRoutes(r)
		s.mountNetworkDNSServiceRoutes(r)
		r.Method(http.MethodGet, "/", s.handle(s.handleDNS))
		r.Method(http.MethodGet, "/hosts", s.handle(s.handleDNSHosts))
		// A lookup is `read`, though it is a POST and sends packets. It asks
		// the native resolver policy by default; direct comparison requires named
		// configured/preset destinations and disclosure acknowledgement. The module
		// accepts no arbitrary address from the
		// caller, so it cannot be pointed at a machine of the caller's
		// choosing the way the probes under /network/probe can, and a
		// resolver is a service that expects to be asked.
		r.Method(http.MethodPost, "/lookup", s.handle(s.handleDNSLookup))
		// The certificate check opens TLS sessions only to configured or preset
		// DNS-over-TLS servers and asks them for the root's NS records; like a
		// lookup it is `read` and names no address of the caller's.
		r.Method(http.MethodPost, "/tls-check", s.handle(s.handleDNSTLSCheck))
		// Which address each managed host name resolves to is `read`: the names
		// are the hosts file's, which any role reads already.
		r.Method(http.MethodGet, "/hosts/resolution", s.handle(s.handleDNSHostsResolution))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPut, "/hosts", s.handle(s.handleDNSHostsSet))
			// Planning and previewing change nothing; they belong to the
			// administrator's editing flow. The DNSSEC chain reads native
			// per-link policy, which is the policy investigation's capability.
			r.Method(http.MethodPost, "/hosts/preview", s.handle(s.handleDNSHostsPreview))
			r.Method(http.MethodPost, "/verification-plan", s.handle(s.handleDNSPlan))
			r.Method(http.MethodPost, "/dnssec-chain", s.handle(s.handleDNSSECChain))
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodPost, "/", s.handle(s.handleDNSSet))
				r.Method(http.MethodDelete, "/", s.handle(s.handleDNSReset))
			})
		})
	})
}

// dnsContainers is what the module needs to spot an ad-blocking resolver in a
// container: its name, image, state and published ports. A host without
// Docker, or whose Docker does not answer, has none.
func (s *Server) dnsContainers(ctx context.Context) []netx.DNSContainer {
	if s.modules.docker == nil {
		return nil
	}
	containers, err := s.modules.docker.ListContainers(ctx, false)
	if err != nil {
		return nil
	}
	out := make([]netx.DNSContainer, 0, len(containers))
	for _, c := range containers {
		dc := netx.DNSContainer{ID: c.ID, Name: c.Name, Image: c.Image, State: c.State}
		for _, p := range c.Ports {
			dc.Ports = append(dc.Ports, netx.DNSPort{IP: p.IP, Private: p.PrivatePort, Public: p.PublicPort, Type: p.Type})
		}
		out = append(out, dc)
	}
	return out
}

func (s *Server) handleDNS(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	view, err := s.modules.network.DNS(ctx, s.dnsContainers(ctx))
	if err != nil {
		return mapDNSError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// applyContext is the context a change to the resolver runs under. It is cut
// off from the request's cancellation on purpose: the module writes a file,
// restarts the resolver and checks it, and a browser that gives up halfway
// would leave the new file in force with nobody to take it back. The change
// finishes or rolls itself back, and only the time it may take is bounded.
func applyContext(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), d)
}

func (s *Server) handleDNSSet(w http.ResponseWriter, r *http.Request) error {
	var req netx.DNSSettings
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := applyContext(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.network.SetDNS(ctx, req, actor(r))
	if err != nil {
		var up *netx.UpstreamError
		if errors.As(err, &up) {
			httpx.SetAudit(r, "network.dns.set", "", map[string]any{"result": "refused_verification", "request": req})
			// The refusal carries every check beside the error, so the page can
			// say which one failed and what the others found.
			refusal := httpx.Err(http.StatusConflict, "dns_upstream_unreachable", up.Reason)
			httpx.JSON(w, refusal.Status, map[string]any{"error": refusal, "verification": up.Verification})
			return nil
		}
		return mapDNSError(err)
	}
	httpx.SetAudit(r, "network.dns.set", "", req)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleDNSPlan(w http.ResponseWriter, r *http.Request) error {
	var req netx.DNSSettings
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 15*time.Second)
	defer cancel()
	plan, err := s.modules.network.PlanDNS(ctx, req)
	if err != nil {
		return mapDNSError(err)
	}
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

type dnsTLSCheckRequest struct {
	Servers []string `json:"servers"`
}

func (s *Server) handleDNSTLSCheck(w http.ResponseWriter, r *http.Request) error {
	var req dnsTLSCheckRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	report, err := s.modules.network.CheckDNSTLS(ctx, req.Servers)
	if err != nil {
		return mapDNSError(err)
	}
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, report)
	return nil
}

type dnssecChainRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleDNSSECChain(w http.ResponseWriter, r *http.Request) error {
	var req dnssecChainRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 25*time.Second)
	defer cancel()
	chain, err := s.modules.network.InvestigateDNSSEC(ctx, req.Name, nil)
	if err != nil {
		return mapDNSError(err)
	}
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, chain)
	return nil
}

func (s *Server) handleDNSReset(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := applyContext(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.network.ResetDNS(ctx)
	if err != nil {
		return mapDNSError(err)
	}
	httpx.SetAudit(r, "network.dns.reset", "", nil)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

type dnsLookupRequest struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	IncludePublic bool   `json:"includePublic"`
	netx.LookupOptions
}

func (s *Server) handleDNSLookup(w http.ResponseWriter, r *http.Request) error {
	var req dnsLookupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	if req.IncludePublic {
		return httpx.Err(http.StatusBadRequest, "dns_comparison_disclosure", "Select named comparison destinations and acknowledge private-name disclosure.")
	}
	res, err := s.modules.network.LookupWithOptions(ctx, req.Name, req.Type, req.LookupOptions)
	if err != nil {
		return mapDNSError(err)
	}
	// A question asked of resolvers changes nothing on the host, so it has no
	// place in the trail of changes.
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleDNSHostsPreview(w http.ResponseWriter, r *http.Request) error {
	var req dnsHostsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	preview, err := s.modules.network.PreviewHostRecords(r.Context(), req.Records)
	if err != nil {
		return mapDNSError(err)
	}
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, preview)
	return nil
}

func (s *Server) handleDNSHostsResolution(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 25*time.Second)
	defer cancel()
	evidence, err := s.modules.network.HostResolution(ctx)
	if err != nil {
		return mapDNSError(err)
	}
	httpx.JSON(w, http.StatusOK, evidence)
	return nil
}

func (s *Server) handleDNSHosts(w http.ResponseWriter, r *http.Request) error {
	recs, err := s.modules.network.HostRecords(r.Context())
	if err != nil {
		return mapDNSError(err)
	}
	httpx.JSON(w, http.StatusOK, recs)
	return nil
}

type dnsHostsRequest struct {
	Records []netx.HostRecord `json:"records"`
}

func (s *Server) handleDNSHostsSet(w http.ResponseWriter, r *http.Request) error {
	var req dnsHostsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := applyContext(r, 15*time.Second)
	defer cancel()
	recs, err := s.modules.network.SetHostRecords(ctx, req.Records)
	if err != nil {
		return mapDNSError(err)
	}
	httpx.SetAudit(r, "network.dns.hosts.set", recs.Path, map[string]any{"records": len(req.Records)})
	httpx.JSON(w, http.StatusOK, recs)
	return nil
}

// mapDNSError adds the one refusal that is the resolver's own to the network
// module's mapping: upstreams that did not answer, which is a conflict with
// the state of the host rather than a mistake in the request.
func mapDNSError(err error) error {
	var up *netx.UpstreamError
	if errors.As(err, &up) {
		return httpx.Err(http.StatusConflict, "dns_upstream_unreachable", up.Reason)
	}
	var refusal *netx.DNSPolicyRefusal
	if errors.As(err, &refusal) {
		return httpx.Err(http.StatusConflict, refusal.Code, refusal.Reason)
	}
	return mapNetworkError(err)
}
