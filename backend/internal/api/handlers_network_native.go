package api

import (
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountNativeManagerRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireSession)
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/native/managers", s.handle(s.handleNativeManagers))
		r.Method(http.MethodGet, "/native/profiles/{device}", s.handle(s.handleNativeProfile))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPut, "/native/profiles/{device}", s.handle(s.handleNativeProfileEdit))
		})
	})
}

func (s *Server) handleNativeManagers(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	view, err := s.modules.network.NativeManagers(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleNativeProfile(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	view, err := s.modules.network.NativeProfile(ctx, chi.URLParam(r, "device"))
	if err != nil {
		return mapNetworkError(err)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleNativeProfileEdit(w http.ResponseWriter, r *http.Request) error {
	var req netx.NativeEditRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	device := chi.URLParam(r, "device")
	httpx.SetAudit(r, "network.native.edit", device, nil)
	ctx, cancel := applyContext(r, 60*time.Second)
	defer cancel()
	view, err := s.modules.network.EditNativeProfile(ctx, device, req, s.networkClient(r))
	if err != nil {
		return mapNetworkConfirmationError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}
