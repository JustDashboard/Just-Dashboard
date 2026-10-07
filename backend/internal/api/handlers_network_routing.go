package api

import (
	"context"
	"net/http"
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
	r.Method(http.MethodGet, "/bgp", s.handle(s.handleNetworkBGP))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/routing/routes", s.handle(s.handleNetworkRouteAdd))
		r.Method(http.MethodPost, "/routing/rules", s.handle(s.handleNetworkRuleAdd))
		r.Method(http.MethodPost, "/forwarding/{family}/on", s.handle(s.handleNetworkForwardingOn))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/routing/routes/{id}", s.handle(s.handleNetworkRouteDelete))
			r.Method(http.MethodDelete, "/routing/rules/{id}", s.handle(s.handleNetworkRuleDelete))
			r.Method(http.MethodPost, "/forwarding/{family}/off", s.handle(s.handleNetworkForwardingOff))
		})
	})
}

// forwardingNeeds is what depends on this host forwarding traffic, besides
// what the network module's own spec holds: Docker's bridge networks, and
// what Tailscale advertises.
func (s *Server) forwardingNeeds(ctx context.Context) netx.ForwardingNeeds {
	var needs netx.ForwardingNeeds
	for _, n := range s.networkInventory(ctx).Networks {
		if n.Driver == "bridge" {
			needs.DockerNetworks++
		}
	}
	// What this server offers its tailnet routes other machines' traffic
	// through it, so it needs forwarding exactly as a NAT entry does.
	needs.TailscaleExitNode, needs.TailscaleSubnetRoutes = s.modules.network.TailscaleNeedsForwarding(ctx)
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
	view, err := s.modules.network.Routing(ctx, httpx.ClientIP(r))
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
	route, err := s.modules.network.AddRoute(ctx, req, httpx.ClientIP(r), actor(r))
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
	if err := s.modules.network.DeleteRoute(ctx, id, httpx.ClientIP(r), actor(r)); err != nil {
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
	rule, err := s.modules.network.AddRule(ctx, req, httpx.ClientIP(r), actor(r))
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
	if err := s.modules.network.DeleteRule(ctx, id, httpx.ClientIP(r), actor(r)); err != nil {
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
