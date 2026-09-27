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
	spec, managed, content, err := s.modules.proxy.ReadSiteSpec(name)
	if err != nil {
		return httpx.ErrNotFound
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"spec": spec, "managed": managed, "content": content,
		"warnings": proxysvc.SpecWarnings(spec),
	})
	return nil
}

type siteRequest struct {
	Spec          proxysvc.SiteSpec `json:"spec"`
	Enable        siteEnable        `json:"enable"`
	Reload        bool              `json:"reload"`
	Overwrite     bool              `json:"overwrite"`
	AllowConflict bool              `json:"allowConflict"`
}

// siteEnable is what a save does to the site's sites-enabled link: "enable"
// links it, "keep" leaves it as it was. The booleans are the older spelling
// of the same two, and false always meant keep — it declined to make a link,
// it never removed one.
type siteEnable bool

func (e *siteEnable) UnmarshalJSON(raw []byte) error {
	switch string(raw) {
	case "true", `"enable"`:
		*e = true
	case "false", `"keep"`:
		*e = false
	default:
		return errors.New(`enable must be "enable" or "keep"`)
	}
	return nil
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
	res, err := s.modules.proxy.SaveSite(ctx, &req.Spec, proxysvc.SiteSave{
		Enable: bool(req.Enable), Reload: req.Reload, Overwrite: req.Overwrite,
		AllowConflict: req.AllowConflict,
	})
	if err != nil {
		switch {
		case errors.Is(err, proxysvc.ErrInvalidConf):
			httpx.SetAudit(r, "proxy.site.apply", req.Spec.Name, map[string]any{"result": "rejected"})
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
		case errors.Is(err, proxysvc.ErrServerNameConflict):
			httpx.SetAudit(r, "proxy.site.apply", req.Spec.Name, map[string]any{
				"result": "name_conflict", "conflicts": res.Conflicts,
			})
			return httpx.Err(http.StatusConflict, "name_conflict", proxysvc.ConflictSummary(res.Conflicts))
		}
		return mapProxyError(err)
	}
	detail := map[string]any{
		"domains": req.Spec.Domains, "kind": req.Spec.Kind,
		"tls": req.Spec.TLS, "enabled": res.Enabled, "reloaded": res.Reloaded,
	}
	if res.ReloadError != "" {
		detail["reloadError"] = res.ReloadError
	}
	if len(res.Conflicts) > 0 {
		detail["conflicts"] = res.Conflicts
	}
	httpx.SetAudit(r, "proxy.site.apply", req.Spec.Name, detail)
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
