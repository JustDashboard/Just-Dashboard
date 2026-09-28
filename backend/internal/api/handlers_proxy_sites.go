package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountSiteBuilderRoutes is the site form: read a site back into it, preview
// what it would write, and apply or delete the result.
func (s *Server) mountSiteBuilderRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/{name}", s.handle(s.handleSiteSpec))
	// A site's requests, read as a deployment's are: the window, the live
	// tail and the export over the file its access_log names
	// (handlers_proxy_site_requests.go). Reads, like the site itself.
	r.Method(http.MethodGet, "/{name}/requests", s.handle(s.handleSiteRequests))
	r.Method(http.MethodGet, "/{name}/requests/stream", s.handle(s.handleSiteRequestStream))
	r.Method(http.MethodGet, "/{name}/requests/export", s.handle(s.handleSiteRequestExport))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// Preview renders and touches nothing, but it lives inside the
		// admin group because the form that calls it is admin-only and a
		// separate gate would be a claim about a boundary that is not
		// there.
		r.Method(http.MethodPost, "/preview", s.handle(s.handleSitePreview))
		r.Method(http.MethodPost, "/", s.handle(s.handleSiteApply))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{name}", s.handle(s.handleSiteDelete))
		})
	})
}

func (s *Server) handleSiteSpec(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if name == "" || strings.ContainsAny(name, "/\\") {
		return httpx.BadRequest("invalid site name")
	}
	content, err := s.modules.proxy.SiteConfig(name)
	if err != nil {
		return httpx.ErrNotFound
	}
	// The spec carries where the site logs, which is what its page reads its
	// requests and errors from.
	spec, managed := proxysvc.ParseSiteSpec(name, content)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"spec": spec, "managed": managed, "content": content,
		"warnings": proxysvc.SpecWarnings(spec),
	})
	return nil
}

type siteRequest struct {
	Spec      proxysvc.SiteSpec `json:"spec"`
	Enable    bool              `json:"enable"`
	Reload    bool              `json:"reload"`
	Overwrite bool              `json:"overwrite"`
}

// handleSitePreview shows the config a spec would produce, live, as the form
// is filled in. Rendering on the server is what keeps one implementation of
// "what does this spec mean"; a second one in the browser would drift, and the
// one that mattered would be the one nobody was reading.
func (s *Server) handleSitePreview(w http.ResponseWriter, r *http.Request) error {
	var req siteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	content, err := proxysvc.RenderNginx(&req.Spec)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"content": content, "warnings": proxysvc.SpecWarnings(&req.Spec),
	})
	return nil
}

func (s *Server) handleSiteApply(w http.ResponseWriter, r *http.Request) error {
	var req siteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.proxy.ApplySite(ctx, &req.Spec, req.Enable, req.Reload, req.Overwrite)
	if err != nil {
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			httpx.SetAudit(r, "proxy.site.apply", req.Spec.Name, map[string]any{"result": "rejected"})
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
		}
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "proxy.site.apply", req.Spec.Name, map[string]any{
		"domains": req.Spec.Domains, "kind": req.Spec.Kind,
		"tls": req.Spec.TLS, "reloaded": res.Reloaded,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleSiteDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if err := s.modules.proxy.DeleteSite(r.Context(), name); err != nil {
		return httpx.BadRequest("%v", err)
	}
	// The site is gone from disk; nginx keeps serving it until it reloads,
	// which is reported rather than hidden.
	reload, reloadErr := s.modules.proxy.Reload(r.Context(), proxysvc.KindNginx)
	httpx.SetAudit(r, "proxy.site.delete", name, map[string]any{"reloaded": reloadErr == nil})
	out := map[string]any{"name": name, "reload": reload}
	if reloadErr != nil {
		out["reloadError"] = reloadErr.Error()
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
