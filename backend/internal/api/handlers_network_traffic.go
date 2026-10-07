package api

import (
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
	"github.com/go-chi/chi/v5"
)

// mountNetworkTrafficRoutes mounts per-process and per-container traffic, and eBPF under /network.
//
// Registered as Methods on the /network router and not as a /traffic Route:
// mountNetworkRoutes already owns /traffic/live and /traffic/history that way,
// and a Route on the same prefix would replace them (handlers_security.go
// records the time that happened to /ssh-sessions).
func (s *Server) mountNetworkTrafficRoutes(r chi.Router) {
	// Which program talks to which remote address is a map of what this
	// machine does and who it does it with, so it is system.admin for the
	// reason the connection table's owners and the SSH configuration are.
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/traffic/processes", s.handle(s.handleNetworkProcesses))
	})
	// Per container is the recorder's byte counters, which the Docker pages
	// already show to every role that can read them.
	r.Method(http.MethodGet, "/traffic/containers", s.handle(s.handleNetworkContainers))
	// Loaded eBPF programs and where they are attached: the same standing as
	// the interface list that already names an XDP program on a device.
	r.Method(http.MethodGet, "/ebpf", s.handle(s.handleNetworkEBPF))
}

func (s *Server) handleNetworkProcesses(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	res, err := s.modules.network.Processes(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleNetworkContainers(w http.ResponseWriter, r *http.Request) error {
	window := time.Hour
	if raw := r.URL.Query().Get("window"); raw != "" {
		d, err := metrics.ParseWindow(raw)
		if err != nil || d <= 0 || d > 31*24*time.Hour {
			return httpx.BadRequest("window is a duration up to 31d, such as 1h, 6h, 24h or 7d")
		}
		window = d
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	res, err := s.modules.network.Containers(ctx, window)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleNetworkEBPF(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	res, err := s.modules.network.EBPF(ctx)
	if err != nil {
		return mapNetworkError(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
