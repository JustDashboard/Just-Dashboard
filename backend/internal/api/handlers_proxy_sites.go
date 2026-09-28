package api

import (
	"context"
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
	// A page is what every visitor to the site may be shown, so reading
	// one needs no more than reading the site.
	r.Method(http.MethodGet, "/{name}/pages/{page}", s.handle(s.handleSitePageGet))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// Preview renders and touches nothing, but it lives inside the
		// admin group because the form that calls it is admin-only and a
		// separate gate would be a claim about a boundary that is not
		// there.
		r.Method(http.MethodPost, "/preview", s.handle(s.handleSitePreview))
		r.Method(http.MethodPost, "/", s.handle(s.handleSiteApply))
		r.Method(http.MethodPut, "/{name}/pages/{page}", s.handle(s.handleSitePagePut))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{name}", s.handle(s.handleSiteDelete))
			// Maintenance takes the site away from its visitors, the
			// same as disabling it, and sits behind the same gate.
			r.Method(http.MethodPost, "/{name}/maintenance", s.handle(s.handleSiteMaintenance))
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
	out := map[string]any{
		"spec": spec, "managed": managed, "content": content,
		"warnings": proxysvc.SpecWarnings(spec),
		// Which version of the file this is: the form keeps it with a draft,
		// keeps the draft over a trip away and back while the file is still
		// this version, and sends it with the save, which is refused if the
		// file changed in between.
		"digest": proxysvc.ContentDigest(content),
	}
	path := name
	// Whether nginx reads the site, so the form offers to keep a disabled
	// one disabled or enable it, instead of a "Save and reload" that did
	// neither — whether it is in conf.d, where enabling it is renaming
	// the file, which a save does not do — and whether nginx serves a file
	// of its own under the site's name instead, which a save does not reach.
	if file, err := s.modules.proxy.SiteFile(spec.Name); err == nil {
		out["enabled"], out["confd"], out["servedCopy"] = file.Enabled, file.Confd, file.ServedCopy
		path = file.Path
	}
	// What a save of the file as the form reads it would drop: lines added
	// by hand that the form has no field for. Left out when the form cannot
	// write the file at all, which its preview says.
	if dropped, err := s.modules.proxy.SiteDrift(spec.Name, path, content, nil); err == nil {
		out["dropped"], out["lossless"] = dropped, len(dropped) == 0
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type siteRequest struct {
	Spec          proxysvc.SiteSpec `json:"spec"`
	Enable        siteEnable        `json:"enable"`
	Reload        bool              `json:"reload"`
	Overwrite     bool              `json:"overwrite"`
	AllowConflict bool              `json:"allowConflict"`
	// BaseDigest is the digest of the file the form read, which the save
	// refuses to write over once the file has changed.
	BaseDigest string `json:"baseDigest"`
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
	s.modules.proxy.SetPagesDir(&req.Spec)
	content, err := proxysvc.RenderNginx(&req.Spec)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	out := map[string]any{"content": content, "warnings": proxysvc.SpecWarnings(&req.Spec)}
	// Where the save would write, and whether a file or a sites-enabled link
	// holds the name already: a new site's name follows its domain, so the
	// form says which file that is, and that it belongs to another site,
	// before the save refuses it.
	if file, err := s.modules.proxy.SiteFile(req.Spec.Name); err == nil {
		out["path"], out["exists"], out["enabled"], out["confd"] = file.Path, file.Exists, file.Enabled, file.Confd
		out["servedCopy"] = file.ServedCopy
		if file.EnabledElsewhere != "" {
			out["enabledElsewhere"] = file.EnabledElsewhere
		}
		// The file there now, for an edit: which version it is, so a form
		// open while it changes finds out before it saves, and what saving
		// this spec over it drops of what the form cannot hold — less what
		// the spec now carries itself, such as lines moved into its extra
		// configuration.
		if file.Exists {
			if content, err := s.modules.proxy.ReadConfig(file.Path); err == nil {
				out["digest"] = proxysvc.ContentDigest(content)
				if dropped, err := s.modules.proxy.SiteDrift(req.Spec.Name, file.Path, content, &req.Spec); err == nil {
					out["dropped"] = dropped
				}
			}
		}
	}
	httpx.JSON(w, http.StatusOK, out)
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
		AllowConflict: req.AllowConflict, BaseDigest: req.BaseDigest,
	})
	if err != nil {
		switch {
		case errors.Is(err, proxysvc.ErrSiteChanged):
			httpx.SetAudit(r, "proxy.site.apply", req.Spec.Name, map[string]any{"result": "changed_on_disk"})
			return httpx.Err(http.StatusConflict, "site_changed", err.Error())
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
	if res.TestedAsEnabled {
		detail["testedAsEnabled"] = true
		detail["valid"] = res.Validation.Valid
	}
	if res.ServedCopy {
		detail["servedCopy"] = true
	}
	if res.Backup != "" {
		detail["backup"] = res.Backup
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

// handleSiteMaintenance turns a site's maintenance page on or off: the site
// saved as the form reads it with the switch changed, tested and reloaded.
func (s *Server) handleSiteMaintenance(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	var req proxysvc.MaintenanceChange
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.proxy.SetMaintenance(ctx, name, req)
	detail := map[string]any{"on": req.On}
	if req.RetryAfter != nil {
		detail["retryAfter"] = *req.RetryAfter
	}
	if req.BypassFrom != nil {
		detail["bypassFrom"] = *req.BypassFrom
	}
	if err != nil {
		switch {
		case errors.Is(err, proxysvc.ErrSiteChanged):
			detail["result"] = "changed_on_disk"
			httpx.SetAudit(r, "proxy.site.maintenance", name, detail)
			return httpx.Err(http.StatusConflict, "site_changed", err.Error())
		case errors.Is(err, proxysvc.ErrNotManaged):
			detail["result"] = "not_managed"
			httpx.SetAudit(r, "proxy.site.maintenance", name, detail)
			return httpx.Err(http.StatusConflict, "not_managed", err.Error())
		case errors.Is(err, proxysvc.ErrInvalidConf):
			detail["result"] = "rejected"
			httpx.SetAudit(r, "proxy.site.maintenance", name, detail)
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
		}
		detail["result"] = "failed"
		httpx.SetAudit(r, "proxy.site.maintenance", name, detail)
		return mapProxyError(err)
	}
	detail["reloaded"] = res.Reloaded
	if res.ReloadError != "" {
		detail["reloadError"] = res.ReloadError
	}
	httpx.SetAudit(r, "proxy.site.maintenance", name, detail)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleSitePageGet(w http.ResponseWriter, r *http.Request) error {
	page, err := s.modules.proxy.ReadSitePage(chi.URLParam(r, "name"), chi.URLParam(r, "page"))
	if err != nil {
		return mapProxyError(err)
	}
	httpx.JSON(w, http.StatusOK, page)
	return nil
}

func (s *Server) handleSitePagePut(w http.ResponseWriter, r *http.Request) error {
	name, pageName := chi.URLParam(r, "name"), chi.URLParam(r, "page")
	var req struct {
		Content string `json:"content"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	page, err := s.modules.proxy.WriteSitePage(name, pageName, req.Content)
	if err != nil {
		httpx.SetAudit(r, "proxy.site.page", name, map[string]any{"page": pageName, "result": "failed"})
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "proxy.site.page", name, map[string]any{"page": pageName, "bytes": len(req.Content)})
	httpx.JSON(w, http.StatusOK, page)
	return nil
}

// certbotEmailKey is the setting holding the contact email last issued with,
// so the site form does not ask for it on every certificate.
const certbotEmailKey = "certbot.email"

// mountSiteCertificateRoutes is the site form's certificate picker. It sits
// under /certificates rather than /proxy/sites, where a static path would take
// the address of a site that happened to share its name.
func (s *Server) mountSiteCertificateRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		// Admin only: the answer carries the remembered contact email, and
		// the form that asks is admin-only.
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/covering", s.handle(s.handleSiteCertificates))
	})
}

// handleSiteCertificates answers GET /certificates/covering?domains=a,b with
// the certificates that cover every one of the names, each with its key, and
// the email the last issuance used.
func (s *Server) handleSiteCertificates(w http.ResponseWriter, r *http.Request) error {
	domains := strings.FieldsFunc(r.URL.Query().Get("domains"), func(c rune) bool {
		return c == ',' || c == ' '
	})
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	certificates, err := s.modules.proxy.SiteCertificates(ctx, domains)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	email, _, err := s.Store.Setting(ctx, certbotEmailKey)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"email": email, "certificates": certificates, "webRoot": proxysvc.SiteACMEWebroot,
	})
	return nil
}

// rememberCertbotEmail keeps the contact email an issuance was started with.
// A failure to keep it costs the operator retyping it next time, which is no
// reason to refuse the issuance.
func (s *Server) rememberCertbotEmail(ctx context.Context, email string) {
	if err := s.Store.SetSetting(ctx, certbotEmailKey, email); err != nil {
		s.Log.Warn("could not remember the certbot email", "err", err)
	}
}
