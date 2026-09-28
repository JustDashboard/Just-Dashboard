package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountVHostRoutes is the site listing, what on disk the running nginx has
// not loaded, the switch that takes a site in or out of nginx's include tree,
// and the removal of a link in sites-enabled that no site's switch owns.
func (s *Server) mountVHostRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/vhosts", s.handle(s.handleVHostList))
	// Paths and change times only, which the listing and the config
	// editor's read already give every account; the reload it leads to is
	// POST /proxy/reload, gated with the rest of the engine.
	r.Method(http.MethodGet, "/pending", s.handle(s.handleProxyPending))
	// What unknown hosts get: the listen lines of the files the listing and
	// the config editor already show every account, and no probe.
	r.Method(http.MethodGet, "/default-site", s.handle(s.handleDefaultSiteGet))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPut, "/default-site", s.handle(s.handleDefaultSitePut))
		r.Method(http.MethodGet, "/resolve", s.handle(s.handleRouteResolve))
		s.destructive(r, func(r chi.Router) {
			// Removing the catch-all hands unknown hosts back to whichever
			// site nginx reads first.
			r.Method(http.MethodDelete, "/default-site", s.handle(s.handleDefaultSiteDelete))
			// Disabling a vhost takes a site offline, and removing a link
			// takes whatever it pointed at out of nginx's configuration.
			r.Method(http.MethodPost, "/vhosts/{name}/enabled", s.handle(s.handleVHostToggle))
			r.Method(http.MethodDelete, "/vhosts/{name}/link", s.handle(s.handleVHostUnlink))
		})
	})
	r.Route("/access-lists", s.mountAccessListRoutes)
}

func (s *Server) handleVHostList(w http.ResponseWriter, r *http.Request) error {
	hosts, err := s.modules.proxy.ListVHosts(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	if err := s.markDeploymentRoutes(r.Context(), hosts); err != nil {
		return httpx.Internal(err)
	}
	if err := s.keepOpenableLogs(r.Context(), hosts); err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, hosts)
	return nil
}

// keepOpenableLogs clears each site's access and error log unless the Logs
// page lists that file, which is the only way it opens one. A site that logs
// outside JD_LOG_ROOTS, or to a file nginx has not created yet, was offered
// Access log and Error log that landed on "Requested log source
// unavailable".
func (s *Server) keepOpenableLogs(ctx context.Context, hosts []proxysvc.VHost) error {
	if !slices.ContainsFunc(hosts, func(v proxysvc.VHost) bool { return v.AccessLog != "" || v.ErrorLog != "" }) {
		return nil
	}
	sources, err := s.modules.logs.Discover(ctx)
	if err != nil {
		return err
	}
	listed := map[string]bool{}
	for _, source := range sources {
		listed[source.ID] = true
	}
	for i := range hosts {
		if !listed[hosts[i].AccessLog] {
			hosts[i].AccessLog = ""
		}
		if !listed[hosts[i].ErrorLog] {
			hosts[i].ErrorLog = ""
		}
	}
	return nil
}

// deploymentRouteRe is the name deploy.RouteNameFor gives an environment's
// route, on nginx and in the Docker Caddy ingress alike.
var deploymentRouteRe = regexp.MustCompile(`^just-dashboard-env-([1-9][0-9]{0,17})\.conf$`)

// markDeploymentRoutes names the deployment environment that writes each
// route. Such a route looked like any hand-made site, with Edit, Disable and
// Delete on it: an edit was overwritten by the next deploy without a word,
// and a delete took the application offline and failed the deployment's
// next check of its route. A name whose environment is gone — its project
// deleted — is left unowned, since nothing will write it again.
func (s *Server) markDeploymentRoutes(ctx context.Context, hosts []proxysvc.VHost) error {
	ids := map[int64][]int{}
	for i, host := range hosts {
		m := deploymentRouteRe.FindStringSubmatch(host.Name)
		if m == nil {
			continue
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		// The same spelling back, so a name deploy would never write is
		// never taken for one it did.
		if err != nil || deploy.RouteNameFor(id) != host.Name {
			continue
		}
		ids[id] = append(ids[id], i)
	}
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, 0, len(ids))
	for id := range ids {
		args = append(args, id)
	}
	// An archived project's name column holds a tombstone; its name is in
	// archived_name, as the deploy store reads it.
	rows, err := s.Store.DB.QueryContext(ctx, `
		SELECT e.id, e.project_id, e.name,
		       CASE WHEN p.archived_name != '' THEN p.archived_name ELSE p.name END,
		       e.archived_at, p.archived_at
		  FROM deploy_environments e JOIN deploy_projects p ON p.id = e.project_id
		 WHERE e.id IN (?`+strings.Repeat(",?", len(args)-1)+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var owner proxysvc.VHostOwner
		var environmentArchived, projectArchived int64
		if err := rows.Scan(&owner.EnvironmentID, &owner.ProjectID, &owner.Environment, &owner.Project,
			&environmentArchived, &projectArchived); err != nil {
			return err
		}
		owner.Archived = environmentArchived != 0 || projectArchived != 0
		for _, i := range ids[owner.EnvironmentID] {
			own := owner
			hosts[i].Owner = &own
		}
	}
	return rows.Err()
}

// handleProxyPending says which changes on disk the running nginx has not
// loaded. `after` is the generation the caller saw before it asked for a
// reload: the answer then waits a few seconds for nginx to load a newer one,
// because `nginx -s reload` returns as soon as the signal is sent.
func (s *Server) handleProxyPending(w http.ResponseWriter, r *http.Request) error {
	after := r.URL.Query().Get("after")
	if after != "" {
		if _, _, err := proxysvc.ParseGeneration(after); err != nil {
			return httpx.BadRequest("%v", err)
		}
	}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	pending, err := s.modules.proxy.Pending(ctx, after)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, pending)
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
	// Decoded: chi hands back a name as the client escaped it, and
	// "vb%3A8080" is no file in sites-available.
	name := httpx.URLParam(r, "name")
	var req vhostToggleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// No typed phrase: this is a toggle, and the same switch turns the site
	// back on. Nothing is written that cannot be unwritten by clicking it
	// again.
	reload, err := s.modules.proxy.ToggleVHost(r.Context(), name, req.Enabled, req.Reload)
	if err != nil {
		return refusedLinkChange(r, "proxy.vhost.toggle", name, map[string]any{"enabled": req.Enabled}, err)
	}
	out := vhostLinkResult{Name: name, Enabled: req.Enabled}
	out.reloaded(reload)
	httpx.SetAudit(r, "proxy.vhost.toggle", name, out.auditDetail(map[string]any{"enabled": req.Enabled}))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleVHostUnlink(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	reload, err := s.modules.proxy.RemoveVHostLink(r.Context(), name, true)
	if err != nil {
		return refusedLinkChange(r, "proxy.vhost.unlink", name, nil, err)
	}
	out := vhostLinkResult{Name: name}
	out.reloaded(reload)
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
// and line — after what it means for this change when the error is in
// another file — with the whole test output kept as the raw detail for the
// page to show on request.
func refusedLinkChange(r *http.Request, action, name string, detail map[string]any, err error) error {
	var refused *proxysvc.RefusedError
	if !errors.As(err, &refused) {
		return mapProxyError(err)
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["result"] = "refused"
	detail["reason"] = refused.Reason()
	httpx.SetAudit(r, action, name, detail)
	return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", refused.Reason()).
		Because("nginx -t failed with the change in place, so the change was undone", refused.Validation.Output)
}

// reloaded records how the reload after a link change went. A reload that
// fails is reported rather than returned as an error: the link change is
// already on disk and passed the test, and answering "failed" would send the
// operator to undo something that worked.
func (out *vhostLinkResult) reloaded(reload *proxysvc.LinkReload) {
	if reload == nil {
		return
	}
	out.Reload = reload.Result
	switch {
	case reload.Err == nil:
		out.Reloaded = true
	case errors.Is(reload.Err, proxysvc.ErrInvalidConf):
		out.ReloadError = "nginx -t failed: " + proxysvc.FailureHeadline(reload.Result.Validation)
	default:
		out.ReloadError = reload.Err.Error()
	}
}

func (s *Server) handleDefaultSiteGet(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	site, err := s.modules.proxy.DefaultSite(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, site)
	return nil
}

type defaultSiteRequest struct {
	Choice     proxysvc.DefaultChoice `json:"choice"`
	RedirectTo string                 `json:"redirectTo"`
}

// defaultSiteResult is what an Apply or a removal of the catch-all did; the
// reload is reported the way a link change reports it.
type defaultSiteResult struct {
	vhostLinkResult
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

func (s *Server) handleDefaultSitePut(w http.ResponseWriter, r *http.Request) error {
	var req defaultSiteRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	detail := map[string]any{"choice": req.Choice}
	if req.Choice == proxysvc.DefaultRedirect {
		detail["redirectTo"] = req.RedirectTo
	}
	res, err := s.modules.proxy.ApplyDefaultSite(ctx, req.Choice, req.RedirectTo, true)
	if err != nil {
		var conflict *proxysvc.DefaultConflictError
		if errors.As(err, &conflict) {
			detail["result"] = "refused"
			detail["reason"] = conflict.Error()
			httpx.SetAudit(r, "proxy.default_site.apply", "jd-default", detail)
			return httpx.Err(http.StatusConflict, "other_default", conflict.Error())
		}
		return refusedLinkChange(r, "proxy.default_site.apply", "jd-default", detail, err)
	}
	out := defaultSiteResult{vhostLinkResult: vhostLinkResult{Name: "jd-default", Enabled: true},
		Path: res.Path, Content: res.Content}
	out.reloaded(res.Reload)
	httpx.SetAudit(r, "proxy.default_site.apply", res.Path, out.auditDetail(detail))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleDefaultSiteDelete(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.proxy.RemoveDefaultSite(ctx, true)
	if err != nil {
		return mapProxyError(err)
	}
	out := defaultSiteResult{vhostLinkResult: vhostLinkResult{Name: "jd-default"}, Path: res.Path}
	out.reloaded(res.Reload)
	httpx.SetAudit(r, "proxy.default_site.remove", res.Path, out.auditDetail(map[string]any{}))
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
