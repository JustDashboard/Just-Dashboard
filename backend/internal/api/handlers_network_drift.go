package api

import (
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountNetworkDriftRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/drift", s.handle(s.handleNetworkDrift))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/drift/repairs", s.handle(s.handleNetworkDriftRepair))
		})
	})
}

func (s *Server) handleNetworkDriftRepair(w http.ResponseWriter, r *http.Request) error {
	var req netx.DriftRepairRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "network.drift.repair", req.Generation, map[string]any{"selections": req.Selections})
	if len(req.Generation) != 64 || len(req.Selections) == 0 || len(req.Selections) > 12 {
		return httpx.BadRequest("Provide a reviewed generation and between one and twelve selected owned repairs.")
	}
	ctx, cancel := applyContext(r, 60*time.Second)
	defer cancel()
	result, err := s.modules.network.RepairDrift(ctx, req, s.networkClient(r))
	auditChange(r, "network.drift.repair", req.Generation, map[string]any{"selections": req.Selections}, err)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

func (s *Server) handleNetworkDrift(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	httpx.JSON(w, http.StatusOK, s.modules.network.Drift(ctx))
	return nil
}
