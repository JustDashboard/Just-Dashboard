package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountStreamRoutes is stream forwarding. It is a sibling of the site builder
// rather than part of it: nginx's stream block is a top-level context, not
// something a server file can reach, and pretending otherwise in the API would
// invite a stream to be written where nginx never reads it.
func (s *Server) mountStreamRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/", s.handle(s.handleStreamList))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodPost, "/preview", s.handle(s.handleStreamPreview))
		r.Method(http.MethodPost, "/", s.handle(s.handleStreamApply))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{name}", s.handle(s.handleStreamDelete))
		})
	})
}

func (s *Server) handleStreamList(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	status, err := s.modules.proxy.Streams(ctx)
	if err != nil {
		return httpx.Err(http.StatusInternalServerError, "stream_dir_unreadable", err.Error())
	}
	// Naming the package is worth a package-manager probe only when the
	// module is missing, which is the one time the page says what to install.
	if module := &status.Module; !module.Usable && module.State != proxysvc.ModuleUnknown && module.State != proxysvc.ModuleAbsent {
		module.Package = proxysvc.StreamModulePackage(s.modules.updates.Manager())
	}
	httpx.JSON(w, http.StatusOK, status)
	return nil
}

type streamRequest struct {
	Spec proxysvc.StreamSpec `json:"spec"`
	// Previous is the name the form opened on: empty for a new stream, and
	// different from Spec.Name for a rename.
	Previous string `json:"previous"`
	Reload   bool   `json:"reload"`
}

func (s *Server) handleStreamPreview(w http.ResponseWriter, r *http.Request) error {
	var req streamRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	content, err := proxysvc.RenderStream(&req.Spec)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"content": content, "warnings": proxysvc.StreamWarnings(&req.Spec)})
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
	case err != nil:
		return mapProxyError(err)
	}
	detail := map[string]any{
		"listen": req.Spec.Listen, "protocol": req.Spec.Protocol, "upstream": req.Spec.Upstream,
		"reloaded": res.Reloaded,
	}
	if res.Renamed != "" {
		detail["renamedFrom"] = res.Renamed
	}
	if res.ReloadError != "" {
		detail["reloadError"] = res.ReloadError
	}
	httpx.SetAudit(r, "proxy.stream.apply", req.Spec.Name, detail)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// handleStreamDelete removes the file and reloads when nginx was reading it.
// A stream nginx never read stops forwarding nothing, and reloading for it
// would only apply every other pending hand edit on the host.
func (s *Server) handleStreamDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	read, err := s.modules.proxy.DeleteStream(ctx, name)
	if errors.Is(err, proxysvc.ErrStreamNotFound) {
		return httpx.Err(http.StatusNotFound, "not_found", err.Error())
	}
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	out := map[string]any{"name": name, "reloaded": false}
	detail := map[string]any{"reloaded": false}
	if read {
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
