package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountVHostRoutes is the site listing, the switch that takes a site in or
// out of nginx's include tree, and the removal of a link in sites-enabled
// that no site's switch owns.
func (s *Server) mountVHostRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/vhosts", s.handle(s.handleVHostList))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		s.destructive(r, func(r chi.Router) {
			// Disabling a vhost takes a site offline, and removing a link
			// takes whatever it pointed at out of nginx's configuration.
			r.Method(http.MethodPost, "/vhosts/{name}/enabled", s.handle(s.handleVHostToggle))
			r.Method(http.MethodDelete, "/vhosts/{name}/link", s.handle(s.handleVHostUnlink))
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

// vhostLinkResult is what a change to a link in sites-enabled did. The link
// change itself passed `nginx -t` or it would have been undone and refused;
// Reloaded says whether nginx is now running it, and ReloadError why not.
type vhostLinkResult struct {
	Name        string                 `json:"name"`
	Enabled     bool                   `json:"enabled"`
	Reloaded    bool                   `json:"reloaded"`
	ReloadError string                 `json:"reloadError,omitempty"`
	Reload      *proxysvc.ReloadResult `json:"reload,omitempty"`
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
		return refusedLinkChange(r, "proxy.vhost.toggle", name, map[string]any{"enabled": req.Enabled}, err)
	}
	out := vhostLinkResult{Name: name, Enabled: req.Enabled}
	if req.Reload {
		s.reloadAfterLinkChange(r, &out)
	}
	httpx.SetAudit(r, "proxy.vhost.toggle", name, out.auditDetail(map[string]any{"enabled": req.Enabled}))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleVHostUnlink(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if err := s.modules.proxy.RemoveVHostLink(r.Context(), name); err != nil {
		return refusedLinkChange(r, "proxy.vhost.unlink", name, nil, err)
	}
	out := vhostLinkResult{Name: name}
	s.reloadAfterLinkChange(r, &out)
	httpx.SetAudit(r, "proxy.vhost.unlink", name, out.auditDetail(map[string]any{}))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// auditDetail adds how the reload went to detail.
func (out *vhostLinkResult) auditDetail(detail map[string]any) map[string]any {
	detail["reloaded"] = out.Reloaded
	if out.ReloadError != "" {
		detail["reloadError"] = out.ReloadError
	}
	return detail
}

// refusedLinkChange answers a link change that did not happen. One nginx's
// test turned away is a 422 whose message is nginx's own first error, file
// and line, with the whole test output kept as the raw detail for the page to
// show on request.
func refusedLinkChange(r *http.Request, action, name string, detail map[string]any, err error) error {
	var refused *proxysvc.RefusedError
	if !errors.As(err, &refused) {
		return mapProxyError(err)
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["result"] = "refused"
	httpx.SetAudit(r, action, name, detail)
	return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", proxysvc.FailureHeadline(refused.Validation)).
		Because("nginx -t failed with the change in place, so the change was undone", refused.Validation.Output)
}

// reloadAfterLinkChange reloads nginx and records how that went. A reload
// that fails is reported rather than returned as an error: the link change
// is already on disk and passed the test, and answering "failed" would send
// the operator to undo something that worked.
func (s *Server) reloadAfterLinkChange(r *http.Request, out *vhostLinkResult) {
	reload, err := s.modules.proxy.Reload(r.Context(), proxysvc.KindNginx)
	out.Reload = reload
	switch {
	case err == nil:
		out.Reloaded = true
	case errors.Is(err, proxysvc.ErrInvalidConf):
		out.ReloadError = "nginx -t failed: " + proxysvc.FailureHeadline(reload.Validation)
	default:
		out.ReloadError = err.Error()
	}
}
