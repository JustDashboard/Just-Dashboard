package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/updates"
	"github.com/go-chi/chi/v5"
)

// mountStreamRoutes is stream forwarding. It is a sibling of the site builder
// rather than part of it: nginx's stream block is a top-level context, not
// something a server file can reach, and pretending otherwise in the API would
// invite a stream to be written where nginx never reads it.
//
// Connecting the directory — the include that has nginx read it — lives under
// /include, and its removal is a POST to /include/remove rather than a DELETE:
// DELETE /{name} deletes a stream, and a stream may be called "include".
func (s *Server) mountStreamRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handleStreamList))
	// Traffic and sessions name client addresses, as the nginx access logs
	// the log viewer already shows every account do; reading is no change.
	r.Method(http.MethodGet, "/traffic", s.handle(s.handleStreamTrafficSummary))
	r.Method(http.MethodGet, "/{name}/traffic", s.handle(s.handleStreamTraffic))
	r.Method(http.MethodGet, "/{name}/sessions", s.handle(s.handleStreamSessions))
	r.Method(http.MethodGet, "/{name}/path", s.handle(s.handleStreamPath))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/preview", s.handle(s.handleStreamPreview))
		// A test sends traffic to an address the caller chose, which is the
		// scanner boundary: admin only, like the network tools.
		r.Method(http.MethodPost, "/test", s.handle(s.handleStreamTest))
		r.Method(http.MethodPost, "/", s.handle(s.handleStreamApply))
		r.Method(http.MethodGet, "/include/plan", s.handle(s.handleStreamIncludePlan))
		r.Method(http.MethodPost, "/include", s.handle(s.handleStreamIncludeApply))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{name}", s.handle(s.handleStreamDelete))
			// Pausing stops a forward; resume is the same switch, so it
			// shares the route and the capability.
			r.Method(http.MethodPost, "/{name}/enabled", s.handle(s.handleStreamToggle))
			r.Method(http.MethodPost, "/include/remove", s.handle(s.handleStreamIncludeRemove))
		})
	})
}

// mountModuleRoutes is what this nginx was built with and loads, which the
// engine line reads before offering what needs a module. Readable by every
// account, like the status it sits beside: it names modules, not secrets.
func (s *Server) mountModuleRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handleNginxModules))
}

// modulePackages remembers which module packages the host's package manager
// has. Asking costs a repository lookup, and the answer changes only with the
// repositories, so it is kept for ten minutes.
type modulePackages struct {
	// catalogue is the host's package manager; a test puts one in its place.
	catalogue packageCatalogue
	mu        sync.Mutex
	known     map[string]packageAnswer
}

// packageCatalogue is what of the package manager says whether it has a
// package: its name, which decides the package's, and a lookup that answers
// updates.ErrUnknownPackage for one it does not have.
type packageCatalogue interface {
	Manager() string
	Describe(ctx context.Context, name string) (*updates.PackageDetail, error)
}

type packageAnswer struct {
	has bool
	at  time.Time
}

const modulePackageTTL = 10 * time.Minute

// modulePackage names the package that installs a dynamic module on this host,
// or nothing: only a package the package manager says it has is named, so the
// page never offers an install that cannot find its package. A lookup that
// fails for another reason names it anyway, and the install says the rest.
func (s *Server) modulePackage(ctx context.Context, module string) string {
	cache := &s.modules.proxyExtras.modulePackages
	pkg := proxysvc.ModulePackage(cache.catalogue.Manager(), module)
	if pkg == "" {
		return ""
	}
	cache.mu.Lock()
	answer, ok := cache.known[pkg]
	cache.mu.Unlock()
	if !ok || time.Since(answer.at) > modulePackageTTL {
		_, err := cache.catalogue.Describe(ctx, pkg)
		answer = packageAnswer{has: !errors.Is(err, updates.ErrUnknownPackage), at: time.Now()}
		cache.mu.Lock()
		if cache.known == nil {
			cache.known = map[string]packageAnswer{}
		}
		cache.known[pkg] = answer
		cache.mu.Unlock()
	}
	if !answer.has {
		return ""
	}
	return pkg
}

func (s *Server) handleNginxModules(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	report, err := s.modules.proxy.NginxModules(ctx, r.URL.Query().Get("fresh") != "")
	if errors.Is(err, proxysvc.ErrNoNginx) {
		return httpx.Err(http.StatusServiceUnavailable, "not_installed", err.Error())
	}
	if err != nil {
		return httpx.Err(http.StatusBadGateway, "nginx_unreadable", err.Error()).Retry()
	}
	// The report is shared by every caller for a minute; the packages go on
	// a copy.
	out := *report
	out.Modules = append([]proxysvc.NginxModule{}, report.Modules...)
	for i, module := range out.Modules {
		if module.State == proxysvc.ModuleNotInstalled {
			out.Modules[i].Package = s.modulePackage(ctx, module.Name)
		}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (s *Server) handleStreamList(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	status, err := s.modules.proxy.Streams(ctx)
	if err != nil {
		// Retryable: permission denied is fixed on the host, and the page
		// should not have to be reloaded to see that it was.
		return httpx.Err(http.StatusInternalServerError, "stream_dir_unreadable", err.Error()).
			Describe("read", "the stream directory").Retry()
	}
	// Naming the package is worth a package-manager probe only when the
	// module is not installed, the one time the page offers to install it.
	if module := &status.Module; module.State == proxysvc.ModuleNotInstalled {
		module.Package = s.modulePackage(ctx, "stream")
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

// handleStreamTrafficSummary is the last hour of every stream that logs, for
// the cards.
func (s *Server) handleStreamTrafficSummary(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	sums, err := s.modules.proxy.StreamTrafficSummaries(ctx, time.Now())
	if err != nil {
		return httpx.Err(http.StatusInternalServerError, "stream_dir_unreadable", err.Error()).Retry()
	}
	httpx.JSON(w, http.StatusOK, sums)
	return nil
}

// handleStreamTraffic is one stream's sessions over ?window= (1h, 24h, 7d).
func (s *Server) handleStreamTraffic(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	traffic, err := s.modules.proxy.StreamTraffic(ctx, httpx.URLParam(r, "name"), r.URL.Query().Get("window"), time.Now())
	if errors.Is(err, proxysvc.ErrStreamNotFound) {
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, traffic)
	return nil
}

// handleStreamSessions is who is connected to a stream now.
func (s *Server) handleStreamSessions(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	sessions, err := s.modules.proxy.StreamSessions(ctx, httpx.URLParam(r, "name"))
	if errors.Is(err, proxysvc.ErrStreamNotFound) {
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	}
	if err != nil {
		return httpx.Err(http.StatusInternalServerError, "sessions_unreadable", err.Error()).Retry()
	}
	httpx.JSON(w, http.StatusOK, sessions)
	return nil
}

// handleStreamPath is one stream as the network page joins it: its forward,
// state, the client sessions and backend connections nginx holds now and its
// last hour by backend. It names addresses as the sessions do.
func (s *Server) handleStreamPath(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	path, err := s.modules.proxy.StreamPath(ctx, httpx.URLParam(r, "name"), time.Now())
	if errors.Is(err, proxysvc.ErrStreamNotFound) {
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	}
	if err != nil {
		return httpx.Err(http.StatusInternalServerError, "stream_unreadable", err.Error()).Retry()
	}
	httpx.JSON(w, http.StatusOK, path)
	return nil
}

type streamRequest struct {
	Spec proxysvc.StreamSpec `json:"spec"`
	// Previous is the name the form opened on: empty for a new stream, and
	// different from Spec.Name for a rename.
	Previous string `json:"previous"`
	Reload   bool   `json:"reload"`
}

// portConflict is a port refusal as the form reads it: who holds the port,
// the next one free, and the sentence the save would refuse with.
type portConflict struct {
	proxysvc.PortOwner
	Suggest int    `json:"suggest,omitempty"`
	Message string `json:"message"`
}

// handleStreamPreview renders the stream and says, before the save, whether
// its port is taken — the refusal the save would meet, while the port is
// still being typed.
func (s *Server) handleStreamPreview(w http.ResponseWriter, r *http.Request) error {
	var req streamRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	content, err := proxysvc.RenderStream(&req.Spec)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	warnings := proxysvc.StreamWarnings(&req.Spec)
	// Said while the form is filled in, as the save would refuse it.
	if err := proxysvc.StreamAddressError(&req.Spec); err != nil {
		warnings = append(warnings, "The save will be refused: "+err.Error()+".")
	}
	out := map[string]any{"content": content, "warnings": warnings}
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	if refused := s.modules.proxy.StreamConflict(ctx, &req.Spec, req.Previous); refused != nil {
		out["conflict"] = portConflict{PortOwner: refused.PortOwner, Suggest: refused.Suggest, Message: refused.Error()}
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleStreamTest dials a stream's upstream, or its own port through nginx,
// from this host. It changes nothing, but it is audited because it makes the
// server connect where the caller says.
func (s *Server) handleStreamTest(w http.ResponseWriter, r *http.Request) error {
	var req proxysvc.StreamDialRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, err := proxysvc.DialStream(r.Context(), req)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "proxy.stream.test", net.JoinHostPort(req.Target, strconv.Itoa(req.Port)),
		map[string]any{"protocol": req.Protocol, "mode": req.Mode, "outcome": res.Outcome})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleStreamApply(w http.ResponseWriter, r *http.Request) error {
	var req streamRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.proxy.ApplyStream(ctx, &req.Spec, req.Previous, req.Reload)
	var inUse *proxysvc.PortInUseError
	var handwritten *proxysvc.HandwrittenStreamError
	switch {
	case errors.Is(err, proxysvc.ErrInvalidConf):
		httpx.SetAudit(r, "proxy.stream.apply", req.Spec.Name, map[string]any{"result": "rejected"})
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
	case errors.As(err, &inUse):
		if inUse.BindError != "" {
			// Written, tested and reloaded before nginx refused the port, then
			// put back: an attempt the audit should show.
			httpx.SetAudit(r, "proxy.stream.apply", req.Spec.Name, map[string]any{"result": "rolled-back", "bindError": inUse.BindError})
		}
		out := httpx.Err(http.StatusConflict, "port_in_use", err.Error())
		out.Field = "spec.listen"
		return out
	case errors.As(err, &handwritten):
		return httpx.Err(http.StatusConflict, "stream_handwritten", err.Error())
	case errors.Is(err, proxysvc.ErrStreamExists):
		out := httpx.Err(http.StatusConflict, "stream_exists", err.Error())
		out.Field = "spec.name"
		return out
	case errors.Is(err, proxysvc.ErrStreamNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, proxysvc.ErrNoStreamSSL):
		return httpx.Err(http.StatusConflict, "stream_ssl_missing", err.Error())
	case errors.Is(err, proxysvc.ErrNoStreamPreread):
		return httpx.Err(http.StatusConflict, "stream_preread_missing", err.Error())
	case errors.Is(err, proxysvc.ErrNoStreamRealIP):
		return httpx.Err(http.StatusConflict, "stream_realip_missing", err.Error())
	case errors.Is(err, proxysvc.ErrStreamAddressNotLocal):
		out := httpx.Err(http.StatusUnprocessableEntity, "address_not_local", err.Error())
		out.Field = "spec.address"
		return out
	case err != nil:
		return mapProxyError(err)
	}
	detail := map[string]any{
		"listen": req.Spec.Listen, "protocol": req.Spec.Protocol, "upstream": req.Spec.Upstream,
		"reloaded": res.Reloaded,
	}
	if len(req.Spec.Servers) > 0 {
		detail["servers"] = len(req.Spec.Servers)
		detail["balance"] = req.Spec.Balance
	}
	if req.Spec.ListenEnd > 0 {
		detail["listenEnd"] = req.Spec.ListenEnd
		detail["samePort"] = req.Spec.SamePort
	}
	if req.Spec.Address != "" {
		detail["address"] = req.Spec.Address
	}
	if req.Spec.AcceptProxy {
		detail["trustedProxies"] = req.Spec.TrustedProxies
	}
	if req.Spec.TLS {
		detail["tls"] = req.Spec.CertPath
	}
	if req.Spec.UpstreamTLS {
		detail["upstreamTls"] = map[string]any{"name": req.Spec.UpstreamName, "verify": req.Spec.UpstreamVerify}
	}
	if len(req.Spec.Routes) > 0 {
		routes := make([]string, 0, len(req.Spec.Routes))
		for _, route := range req.Spec.Routes {
			routes = append(routes, route.Name+" -> "+route.Upstream)
		}
		detail["routes"] = routes
	}
	if res.Renamed != "" {
		detail["renamedFrom"] = res.Renamed
	}
	if res.ReloadError != "" {
		detail["reloadError"] = res.ReloadError
	}
	if res.Listening != nil {
		detail["listening"] = *res.Listening
	}
	httpx.SetAudit(r, "proxy.stream.apply", req.Spec.Name, detail)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// handleStreamDelete removes the file and reloads when nginx was reading it.
// A stream nginx never read stops forwarding nothing, and reloading for it
// would only apply every other pending hand edit on the host.
//
// The name is unescaped: the listing shows any *.conf, and a+b or a@b arrive
// percent-encoded, which chi hands over as sent.
func (s *Server) handleStreamDelete(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	deleted, err := s.modules.proxy.DeleteStream(ctx, name)
	if errors.Is(err, proxysvc.ErrStreamNotFound) {
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	out := map[string]any{"name": name, "reloaded": false}
	detail := map[string]any{"reloaded": false}
	for key, value := range map[string]string{"backup": deleted.Backup, "link": deleted.Link, "unread": deleted.Unread} {
		if value != "" {
			out[key], detail[key] = value, value
		}
	}
	if deleted.Read {
		reload, reloadErr := s.modules.proxy.Reload(ctx, proxysvc.KindNginx)
		out["reload"], out["reloaded"], detail["reloaded"] = reload, reloadErr == nil, reloadErr == nil
		if reloadErr != nil {
			message := reloadErr.Error()
			if errors.Is(reloadErr, proxysvc.ErrInvalidConf) {
				message = "nginx refused to reload because its configuration test fails: " + reload.Validation.Output
			}
			out["reloadError"], detail["reloadError"] = message, message
		}
	}
	httpx.SetAudit(r, "proxy.stream.delete", name, detail)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type streamToggleRequest struct {
	Enabled bool `json:"enabled"`
}

// handleStreamToggle pauses a stream or resumes a paused one
// (proxysvc.SetStreamEnabled). The name is unescaped, as for a delete.
func (s *Server) handleStreamToggle(w http.ResponseWriter, r *http.Request) error {
	name := httpx.URLParam(r, "name")
	var req streamToggleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.proxy.SetStreamEnabled(ctx, name, req.Enabled)
	var inUse *proxysvc.PortInUseError
	switch {
	case errors.Is(err, proxysvc.ErrInvalidConf):
		httpx.SetAudit(r, "proxy.stream.toggle", name, map[string]any{"enabled": req.Enabled, "result": "rejected"})
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
	case errors.As(err, &inUse):
		if inUse.BindError != "" {
			httpx.SetAudit(r, "proxy.stream.toggle", name,
				map[string]any{"enabled": true, "result": "rolled-back", "bindError": inUse.BindError})
		}
		return httpx.Err(http.StatusConflict, "port_in_use", err.Error())
	case errors.Is(err, proxysvc.ErrStreamLinkPause):
		return httpx.Err(http.StatusConflict, "stream_linked", err.Error())
	case errors.Is(err, proxysvc.ErrStreamExists):
		return httpx.Err(http.StatusConflict, "stream_exists", err.Error())
	case errors.Is(err, proxysvc.ErrStreamNotFound):
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	case err != nil:
		return httpx.BadRequest("%v", err)
	}
	detail := map[string]any{"enabled": req.Enabled, "reloaded": res.Reloaded}
	if res.ReloadError != "" {
		detail["reloadError"] = res.ReloadError
	}
	if res.Listening != nil {
		detail["listening"] = *res.Listening
	}
	httpx.SetAudit(r, "proxy.stream.toggle", name, detail)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// streamIncludeError answers a refused connect or disconnect with its own
// code: the page tells "install the module first" from "someone changed
// nginx.conf since you looked" by it.
func streamIncludeError(err error) error {
	var refused *proxysvc.StreamIncludeError
	if errors.As(err, &refused) {
		return httpx.Err(http.StatusConflict, refused.Code, refused.Reason)
	}
	return mapProxyError(err)
}

func (s *Server) handleStreamIncludePlan(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 45*time.Second)
	defer cancel()
	plan, err := s.modules.proxy.PlanStreamInclude(ctx)
	var refused *proxysvc.StreamIncludeError
	if errors.As(err, &refused) {
		return httpx.Err(http.StatusConflict, refused.Code, refused.Reason).Describe("work out", "the change")
	}
	if err != nil {
		return mapProxyError(err)
	}
	httpx.JSON(w, http.StatusOK, plan)
	return nil
}

type streamIncludeRequest struct {
	// Mode and Path are the plan the operator was shown. A plan that has
	// changed since is refused rather than made in its place.
	Mode   string `json:"mode"`
	Path   string `json:"path"`
	Reload bool   `json:"reload"`
}

// handleStreamIncludeApply connects the stream directory. The audit names the
// file changed and where its copy was kept, because nginx.conf may be it.
func (s *Server) handleStreamIncludeApply(w http.ResponseWriter, r *http.Request) error {
	var req streamIncludeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// Labelled before it runs, so a refusal is recorded as the connect it was.
	httpx.SetAudit(r, "proxy.stream.include.add", req.Path, map[string]any{"mode": req.Mode})
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	res, err := s.modules.proxy.ApplyStreamInclude(ctx, req.Mode, req.Path, req.Reload)
	if errors.Is(err, proxysvc.ErrInvalidConf) {
		httpx.SetAudit(r, "proxy.stream.include.add", req.Path, map[string]any{"mode": req.Mode, "result": "rejected"})
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
	}
	if err != nil {
		return streamIncludeError(err)
	}
	httpx.SetAudit(r, "proxy.stream.include.add", res.Path, includeAuditDetail(res))
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// handleStreamIncludeRemove takes out the include the dashboard added. Every
// stream stops forwarding at the reload, so it sits with the destructive
// routes; it asks no typed phrase, since connecting again puts it back.
func (s *Server) handleStreamIncludeRemove(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Reload bool `json:"reload"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	httpx.SetAudit(r, "proxy.stream.include.remove", "", nil)
	ctx, cancel := timeoutCtx(r, 90*time.Second)
	defer cancel()
	res, err := s.modules.proxy.RemoveStreamInclude(ctx, req.Reload)
	if errors.Is(err, proxysvc.ErrInvalidConf) {
		httpx.SetAudit(r, "proxy.stream.include.remove", res.Path, map[string]any{"mode": res.Mode, "result": "rejected"})
		return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
	}
	if err != nil {
		return streamIncludeError(err)
	}
	httpx.SetAudit(r, "proxy.stream.include.remove", res.Path, includeAuditDetail(res))
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func includeAuditDetail(res *proxysvc.StreamIncludeResult) map[string]any {
	detail := map[string]any{"mode": res.Mode, "streams": res.Streams, "reloaded": res.Reloaded}
	if res.Backup != "" {
		detail["backup"] = res.Backup
	}
	if res.ReloadError != "" {
		detail["reloadError"] = res.ReloadError
	}
	return detail
}
