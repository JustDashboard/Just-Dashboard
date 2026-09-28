package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountSiteOpsRoutes holds what is done to a site as a whole, as opposed to
// what the site form writes into it. It shares /proxy/sites with the builder
// but not its file, so the two grow independently.
func (s *Server) mountSiteOpsRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// Destructive whatever the action: a bulk disable takes sites
		// offline and a bulk delete removes their files, and an enable is
		// the same switch a single toggle gates the same way.
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/bulk", s.handle(s.handleSitesBulk))
			// Destructive as a delete is: the old name stops existing, and
			// anything that reached the site by it — a bookmark, a script,
			// a log path — no longer does.
			r.Method(http.MethodPost, "/{name}/rename", s.handle(s.handleSiteRename))
		})
	})
}

type sitesBulkRequest struct {
	Action proxysvc.BulkAction `json:"action"`
	Names  []string            `json:"names"`
}

type sitesBulkResult struct {
	Action      proxysvc.BulkAction    `json:"action"`
	Changed     []string               `json:"changed"`
	Unchanged   []string               `json:"unchanged"`
	Reloaded    bool                   `json:"reloaded"`
	ReloadError string                 `json:"reloadError,omitempty"`
	Reload      *proxysvc.ReloadResult `json:"reload,omitempty"`
}

// handleSitesBulk enables, disables or deletes several sites as one change:
// one nginx -t over all of them, every site put back on a refusal, then one
// reload.
func (s *Server) handleSitesBulk(w http.ResponseWriter, r *http.Request) error {
	var req sitesBulkRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	const action = "proxy.sites.bulk"
	detail := map[string]any{"action": req.Action, "names": req.Names}
	res, reload, err := s.modules.proxy.BulkSites(r.Context(), req.Action, req.Names, true)
	if err != nil {
		var refused *proxysvc.RefusedError
		if errors.As(err, &refused) {
			return refusedLinkChange(r, action, "", detail, err)
		}
		// Recorded too: a refusal names which site stopped the whole
		// change, and that nothing was changed.
		detail["result"] = "refused"
		detail["reason"] = err.Error()
		httpx.SetAudit(r, action, "", detail)
		return mapProxyError(err)
	}
	link := vhostLinkResult{}
	link.reloaded(reload)
	out := sitesBulkResult{
		Action: req.Action, Changed: res.Changed, Unchanged: res.Unchanged,
		Reloaded: link.Reloaded, ReloadError: link.ReloadError, Reload: link.Reload,
	}
	detail["changed"] = res.Changed
	httpx.SetAudit(r, action, "", link.auditDetail(detail))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type siteRenameRequest struct {
	To     string `json:"to"`
	Reload bool   `json:"reload"`
}

type siteRenameResult struct {
	vhostLinkResult
	From       string   `json:"from"`
	Path       string   `json:"path"`
	Rerendered bool     `json:"rerendered"`
	Warnings   []string `json:"warnings"`
}

// handleSiteRename moves a site to a new name behind one nginx test, and
// reloads when asked.
func (s *Server) handleSiteRename(w http.ResponseWriter, r *http.Request) error {
	from := httpx.URLParam(r, "name")
	var req siteRenameRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	const action = "proxy.site.rename"
	detail := map[string]any{"to": req.To}
	res, reload, err := s.modules.proxy.RenameSite(r.Context(), from, req.To, req.Reload)
	if err != nil {
		var refused *proxysvc.RefusedError
		if errors.As(err, &refused) {
			return refusedLinkChange(r, action, from, detail, err)
		}
		detail["result"] = "refused"
		detail["reason"] = err.Error()
		httpx.SetAudit(r, action, from, detail)
		return mapProxyError(err)
	}
	out := siteRenameResult{
		vhostLinkResult: vhostLinkResult{Name: res.Name, Enabled: res.Enabled},
		From:            from, Path: res.Path, Rerendered: res.Rerendered, Warnings: res.Warnings,
	}
	out.reloaded(reload)
	detail["to"], detail["path"], detail["rerendered"] = res.Name, res.Path, res.Rerendered
	httpx.SetAudit(r, action, from, out.auditDetail(detail))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
