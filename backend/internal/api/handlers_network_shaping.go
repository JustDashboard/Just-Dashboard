package api

import (
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// mountNetworkShapingRoutes mounts queue disciplines and speed limits under
// /network.
//
// Setting a limit is system.admin; clearing one is destructive, because what
// the limit was holding back is released at once. BBR is a switch on the
// whole host and is not: turning it either way is a congestion-control
// change nobody's connection is lost to. "bbr" is a route of its own beside
// {device}, and chi matches the static segment first, so a device named bbr
// cannot be shaped by name here — a name this host's devices do not have.
func (s *Server) mountNetworkShapingRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/shaping", s.handle(s.handleNetworkShaping))
	// Per-algorithm figures folded from every non-loopback socket: they name
	// no peer and no program, so they are a reading like the queue counters.
	r.Method(http.MethodGet, "/shaping/congestion", s.handle(s.handleShapingCongestion))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/shaping/bbr", s.handle(s.handleShapingBBR))
		r.Method(http.MethodPost, "/shaping/{device}", s.handle(s.handleShapingSet))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/shaping/{device}", s.handle(s.handleShapingClear))
		})
	})
}

func (s *Server) handleNetworkShaping(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	v, err := s.modules.network.Shaping(ctx, s.networkClient(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) handleShapingSet(w http.ResponseWriter, r *http.Request) error {
	device := chi.URLParam(r, "device")
	var req netx.ShapeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	client := s.networkClient(r)
	if err := s.modules.network.SetShaping(ctx, device, req, client, actor(r)); err != nil {
		auditChange(r, "network.shaping.set", device, req, err)
		return mapNetworkError(err)
	}
	httpx.SetAudit(r, "network.shaping.set", device, req)
	view, err := s.modules.network.Shaping(ctx, client)
	if err != nil {
		return mapNetworkError(err)
	}
	for _, d := range view.Devices {
		if d.Name == device {
			httpx.JSON(w, http.StatusOK, d)
			return nil
		}
	}
	return httpx.Err(http.StatusNotFound, "not_found", "the device is gone")
}

func (s *Server) handleShapingClear(w http.ResponseWriter, r *http.Request) error {
	device := chi.URLParam(r, "device")
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.ClearShaping(ctx, device); err != nil {
		auditChange(r, "network.shaping.clear", device, nil, err)
		return mapNetworkError(err)
	}
	httpx.SetAudit(r, "network.shaping.clear", device, nil)
	httpx.NoContent(w)
	return nil
}

type shapingBBRRequest struct {
	On bool `json:"on"`
}

func (s *Server) handleShapingBBR(w http.ResponseWriter, r *http.Request) error {
	var req shapingBBRRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	if err := s.modules.network.SetBBR(ctx, req.On, actor(r)); err != nil {
		return mapNetworkError(err)
	}
	httpx.SetAudit(r, "network.shaping.bbr", "bbr", req)
	view, err := s.modules.network.Shaping(ctx, s.networkClient(r))
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, view.BBR)
	return nil
}

func (s *Server) handleShapingCongestion(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	v, err := s.modules.network.Congestion(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, v)
	return nil
}
