package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// mountNetworkEgressRoutes mounts monitored egress groups under /network.
//
// Reads are `read`, like routing. Creating, editing and simulating are
// system.admin. Everything that can withdraw a path some traffic is using —
// enabling a group (its selected traffic leaves the main table), disabling
// or removing one (it goes back), a manual switch, and turning automation
// on (the monitor may then switch on its own) — is inside s.destructive.
// Turning automation off withdraws nothing.
func (s *Server) mountNetworkEgressRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/egress", s.handle(s.handleNetworkEgress))
	r.Method(http.MethodGet, "/egress/simulations/{sim}", s.handle(s.handleNetworkEgressSimulation))
	r.Method(http.MethodGet, "/egress/{id}/events", s.handle(s.handleNetworkEgressEvents))
	r.Method(http.MethodGet, "/egress/{id}/simulations", s.handle(s.handleNetworkEgressSimulations))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/egress", s.handle(s.handleNetworkEgressCreate))
		r.Method(http.MethodPut, "/egress/{id}", s.handle(s.handleNetworkEgressUpdate))
		r.Method(http.MethodPost, "/egress/{id}/simulate", s.handle(s.handleNetworkEgressSimulate))
		r.Method(http.MethodPost, "/egress/{id}/automation/off", s.handle(s.handleNetworkEgressAutomationOff))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/egress/{id}", s.handle(s.handleNetworkEgressDelete))
			r.Method(http.MethodPost, "/egress/{id}/enable", s.handle(s.handleNetworkEgressEnable))
			r.Method(http.MethodPost, "/egress/{id}/disable", s.handle(s.handleNetworkEgressDisable))
			r.Method(http.MethodPost, "/egress/{id}/switch", s.handle(s.handleNetworkEgressSwitch))
			r.Method(http.MethodPost, "/egress/{id}/automation/on", s.handle(s.handleNetworkEgressAutomationOn))
		})
	})
}

func (s *Server) handleNetworkEgress(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	view, err := s.modules.network.Egress(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleNetworkEgressEvents(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			return httpx.BadRequest("limit is between 1 and 500")
		}
		limit = n
	}
	events, err := s.modules.network.EgressEvents(r.Context(), id, limit)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, events)
	return nil
}

func (s *Server) handleNetworkEgressSimulations(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, s.modules.network.EgressSimulations(r.Context(), id))
	return nil
}

func (s *Server) handleNetworkEgressSimulation(w http.ResponseWriter, r *http.Request) error {
	sim, err := s.modules.network.EgressSimulationByID(r.Context(), chi.URLParam(r, "sim"))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, sim)
	return nil
}

func (s *Server) handleNetworkEgressCreate(w http.ResponseWriter, r *http.Request) error {
	var req netx.EgressGroupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.egress.create", req.Name, req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	g, err := s.modules.network.CreateEgressGroup(ctx, req, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusCreated, g)
	return nil
}

func (s *Server) handleNetworkEgressUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req netx.EgressGroupRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.egress.update", strconv.Itoa(id), req)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	g, err := s.modules.network.UpdateEgressGroup(ctx, id, req, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, g)
	return nil
}

func (s *Server) handleNetworkEgressDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "network.egress.delete", strconv.Itoa(id), nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.DeleteEgressGroup(ctx, id, s.networkClient(r), actor(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleNetworkEgressEnable(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "network.egress.enable", strconv.Itoa(id), nil)
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	g, err := s.modules.network.EnableEgressGroup(ctx, id, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, g)
	return nil
}

func (s *Server) handleNetworkEgressDisable(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "network.egress.disable", strconv.Itoa(id), nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	g, err := s.modules.network.DisableEgressGroup(ctx, id, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, g)
	return nil
}

func (s *Server) handleNetworkEgressSwitch(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req netx.EgressSwitchRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.egress.switch", strconv.Itoa(id), req)
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.network.SwitchEgress(ctx, id, req, s.networkClient(r), actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleNetworkEgressSimulate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "network.egress.simulate", strconv.Itoa(id), nil)
	sim, err := s.modules.network.SimulateEgress(r.Context(), id, actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusAccepted, sim)
	return nil
}

func (s *Server) handleNetworkEgressAutomationOn(w http.ResponseWriter, r *http.Request) error {
	return s.setEgressAutomation(w, r, true)
}

func (s *Server) handleNetworkEgressAutomationOff(w http.ResponseWriter, r *http.Request) error {
	return s.setEgressAutomation(w, r, false)
}

func (s *Server) setEgressAutomation(w http.ResponseWriter, r *http.Request, on bool) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	action := "network.egress.automation.off"
	if on {
		action = "network.egress.automation.on"
	}
	httpx.SetAudit(r, action, strconv.Itoa(id), nil)
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	g, err := s.modules.network.SetEgressAutomation(ctx, id, on, actor(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, g)
	return nil
}
