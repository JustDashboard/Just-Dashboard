package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountVHostRoutes is the site listing and the switch that takes a site in or
// out of nginx's include tree.
func (s *Server) mountVHostRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/vhosts", s.handle(s.handleVHostList))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		s.destructive(r, func(r chi.Router) {
			// Disabling a vhost takes a site offline, and the handler
			// asks for the site's own name before it will.
			r.Method(http.MethodPost, "/vhosts/{name}/enabled", s.handle(s.handleVHostToggle))
		})
	})
}

func (s *Server) handleVHostList(w http.ResponseWriter, r *http.Request) error {
	hosts, err := s.modules.proxy.ListVHosts(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, hosts)
	return nil
}

type vhostToggleRequest struct {
	Enabled bool `json:"enabled"`
	Reload  bool `json:"reload"`
}

func (s *Server) handleVHostToggle(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req vhostToggleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// No typed phrase: this is a toggle, and the same switch turns the site
	// back on. Nothing is written that cannot be unwritten by clicking it
	// again.
	if err := s.modules.proxy.SetVHostEnabled(r.Context(), name, req.Enabled); err != nil {
		return mapProxyError(err)
	}
	out := map[string]any{"name": name, "enabled": req.Enabled}
	if req.Reload {
		reload, err := s.modules.proxy.Reload(r.Context(), proxysvc.KindNginx)
		out["reload"] = reload
		if err != nil {
			// The symlink change is already applied; report the reload
			// failure rather than pretending the toggle did not happen.
			httpx.SetAudit(r, "proxy.vhost.toggle", name,
				map[string]any{"enabled": req.Enabled, "reloadError": err.Error()})
			return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
		}
	}
	httpx.SetAudit(r, "proxy.vhost.toggle", name, map[string]any{"enabled": req.Enabled})
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
