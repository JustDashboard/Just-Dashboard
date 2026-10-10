package api

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// mountNetworkRoutingRoutes mounts routing tables, policy rules, forwarding and BGP under /network.
//
// Forwarding on and off are two paths for the reason link up and down are:
// turning it off can take Docker's and Tailscale's traffic with it, turning it
// on cannot, and s.destructive decides by route.
func (s *Server) mountNetworkRoutingRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/routing", s.handle(s.handleNetworkRouting))
	r.Method(http.MethodGet, "/routing/lookup", s.handle(s.handleNetworkRouteLookup))
	r.Method(http.MethodGet, "/routing/history", s.handle(s.handleNetworkRouteHistory))
	r.Method(http.MethodGet, "/bgp", s.handle(s.handleNetworkBGP))
	r.Method(http.MethodGet, "/bgp/routes", s.handle(s.handleNetworkBGPRoutes))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// Previews and plans change nothing; they sit with the forms they
		// review, which only an administrator sees.
		r.Method(http.MethodPost, "/routing/routes/preview", s.handle(s.handleNetworkRoutePreview))
		r.Method(http.MethodPost, "/routing/routes/{id}/plan", s.handle(s.handleNetworkRoutePlan))
		r.Method(http.MethodPost, "/routing/rules/preview", s.handle(s.handleNetworkRulePreview))
		r.Method(http.MethodPost, "/routing/routes", s.handle(s.handleNetworkRouteAdd))
		r.Method(http.MethodPost, "/routing/rules", s.handle(s.handleNetworkRuleAdd))
		r.Method(http.MethodPost, "/forwarding/{family}/on", s.handle(s.handleNetworkForwardingOn))
		s.destructive(r, func(r chi.Router) {
			// An edit replaces a working route, so the path it took is
			// removed as a delete's is.
			r.Method(http.MethodPut, "/routing/routes/{id}", s.handle(s.handleNetworkRouteEdit))
			r.Method(http.MethodDelete, "/routing/routes/{id}", s.handle(s.handleNetworkRouteDelete))
			r.Method(http.MethodDelete, "/routing/rules/{id}", s.handle(s.handleNetworkRuleDelete))
			r.Method(http.MethodPost, "/forwarding/{family}/off", s.handle(s.handleNetworkForwardingOff))
		})
	})
}

func (s *Server) handleNetworkRouteLookup(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if _, err := netx.ParseAddr(q.Get("target")); err != nil {
		return httpx.BadRequest("target must be an IPv4 or IPv6 address")
	}
	if source := q.Get("source"); source != "" {
		if _, err := netx.ParseAddr(source); err != nil {
			return httpx.BadRequest("source must be an IPv4 or IPv6 address")
		}
	}
	ctx, cancel := timeoutCtx(r, 5*time.Second)
	defer cancel()
	view, err := s.modules.network.LookupRoute(ctx, q.Get("target"), q.Get("source"), q.Get("mark"))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// forwardingNeeds is what depends on this host forwarding traffic, besides
// what the network module's own spec holds: Docker's bridge networks, and
// what Tailscale advertises.
func (s *Server) forwardingNeeds(ctx context.Context) netx.ForwardingNeeds {
	needs := dockerForwardingNeeds(s.networkInventory(ctx))
	// What this server offers its tailnet routes other machines' traffic
	// through it, so it needs forwarding exactly as a NAT entry does.
	needs.TailscaleExitNode, needs.TailscaleSubnetRoutes = s.modules.network.TailscaleNeedsForwarding(ctx)
	return needs
}

func dockerForwardingNeeds(inv netx.Inventory) netx.ForwardingNeeds {
	needs := netx.ForwardingNeeds{DockerNetworksUnknown: inv.DockerNetworksUnknown}
	for _, n := range inv.Networks {
		if n.Driver != "bridge" {
			continue
		}
		// The inventory does not expose EnableIPv4, and custom IPAM may omit
		// subnets. Keep the existing conservative IPv4 dependency for bridges.
		needs.DockerNetworks++
		ipv6 := n.IPv6
		for _, subnet := range n.Subnets {
			if prefix, err := netip.ParsePrefix(subnet); err == nil && prefix.Addr().Unmap().Is6() {
				ipv6 = true
			}
		}
		if ipv6 {
			needs.DockerIPv6Networks++
		}
	}
	return needs
}

// routingResponse is the routing view with the forwarding switches beside it,
// which the Routing page shows together.
type routingResponse struct {
	*netx.RoutingView
	Forwarding netx.ForwardingView `json:"forwarding"`
}

func (s *Server) handleNetworkRouting(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	view, err := s.modules.network.Routing(ctx, s.networkClient(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, routingResponse{
		RoutingView: view,
		Forwarding:  s.modules.network.Forwarding(ctx, s.forwardingNeeds(ctx)),
	})
	return nil
}

func (s *Server) handleNetworkBGP(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	view, err := s.modules.network.BGP(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleNetworkRouteAdd(w http.ResponseWriter, r *http.Request) error {
	var req netx.RouteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.route.add", req.Destination, req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	route, err := s.modules.network.AddRoute(ctx, req, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusCreated, route)
	return nil
}

func (s *Server) handleNetworkRouteDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "network.route.delete", strconv.Itoa(id), nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteRoute(ctx, id, s.networkClient(r), actor(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleNetworkRuleAdd(w http.ResponseWriter, r *http.Request) error {
	var req netx.RuleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.rule.add", strconv.Itoa(req.Priority), req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	rule, err := s.modules.network.AddRule(ctx, req, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusCreated, rule)
	return nil
}

func (s *Server) handleNetworkRuleDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "network.rule.delete", strconv.Itoa(id), nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteRule(ctx, id, s.networkClient(r), actor(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleNetworkForwardingOn(w http.ResponseWriter, r *http.Request) error {
	return s.setForwarding(w, r, true)
}

func (s *Server) handleNetworkForwardingOff(w http.ResponseWriter, r *http.Request) error {
	return s.setForwarding(w, r, false)
}

func (s *Server) setForwarding(w http.ResponseWriter, r *http.Request, on bool) error {
	family := chi.URLParam(r, "family")
	action := "network.forwarding.off"
	if on {
		action = "network.forwarding.on"
	}
	httpx.SetAudit(r, action, family, nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	view, err := s.modules.network.SetForwarding(ctx, family, on, s.forwardingNeeds(ctx), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleNetworkRouteHistory(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	query := netx.RouteHistoryQuery{Limit: atoiDefault(q.Get("limit"), 200), Target: q.Get("target")}
	switch family := q.Get("family"); family {
	case "", "inet", "inet6":
		query.Family = family
	default:
		return httpx.BadRequest("family is inet or inet6")
	}
	switch object := q.Get("object"); object {
	case "", "route", "rule":
		query.Object = object
	default:
		return httpx.BadRequest("object is route or rule")
	}
	if query.Target != "" {
		if _, err := netx.ParsePrefix(query.Target); err != nil {
			return httpx.BadRequest("target must be an IPv4 or IPv6 address or network")
		}
	}
	history, err := s.modules.network.RouteHistory(r.Context(), query)
	if errors.Is(err, netx.ErrUnavailable) {
		return httpx.Err(http.StatusServiceUnavailable, "history_unavailable", "route history needs the dashboard's database")
	}
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, history)
	return nil
}

// impactCandidates are the addresses the api layer knows of that netx does
// not read for itself: Docker's networks.
func (s *Server) impactCandidates(ctx context.Context) []netx.ImpactCandidate {
	var out []netx.ImpactCandidate
	for _, n := range s.networkInventory(ctx).Networks {
		for _, subnet := range n.Subnets {
			out = append(out, netx.ImpactCandidate{Kind: "docker", Name: "the Docker network " + n.Name, Address: subnet})
		}
	}
	return out
}

func (s *Server) handleNetworkRoutePreview(w http.ResponseWriter, r *http.Request) error {
	var req netx.RouteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	impact, err := s.modules.network.PreviewRoute(ctx, req, s.networkClient(r), s.impactCandidates(ctx))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, impact)
	return nil
}

func (s *Server) handleNetworkRoutePlan(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req netx.RouteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	plan, err := s.modules.network.PlanRouteEdit(ctx, id, req, s.networkClient(r), s.impactCandidates(ctx))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

func (s *Server) handleNetworkRouteEdit(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req netx.RouteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.route.edit", strconv.Itoa(id), req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	route, err := s.modules.network.EditRoute(ctx, id, req, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, route)
	return nil
}

// rulePreviewRequest is a rule and, optionally, a packet to evaluate it with.
type rulePreviewRequest struct {
	netx.RuleRequest
	Probe *netx.RouteTuple `json:"probe,omitempty"`
}

func (s *Server) handleNetworkRulePreview(w http.ResponseWriter, r *http.Request) error {
	var req rulePreviewRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	preview, err := s.modules.network.PreviewRule(ctx, req.RuleRequest, req.Probe, s.networkClient(r), s.impactCandidates(ctx))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, preview)
	return nil
}

func (s *Server) handleNetworkBGPRoutes(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	view, err := s.modules.network.BGPRoutes(ctx, r.URL.Query().Get("family"), r.URL.Query().Get("prefix"))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}
