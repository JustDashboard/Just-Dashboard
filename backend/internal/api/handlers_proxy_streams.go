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
	httpx.JSON(w, http.StatusOK, s.modules.proxy.Streams(r.Context()))
	return nil
}

type streamRequest struct {
	Spec      proxysvc.StreamSpec `json:"spec"`
	Reload    bool                `json:"reload"`
	Overwrite bool                `json:"overwrite"`
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
	httpx.JSON(w, http.StatusOK, map[string]any{"content": content})
	return nil
}

func (s *Server) handleStreamApply(w http.ResponseWriter, r *http.Request) error {
	var req streamRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 60*time.Second)
	defer cancel()
	res, err := s.modules.proxy.ApplyStream(ctx, &req.Spec, req.Reload, req.Overwrite)
	if err != nil {
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			httpx.SetAudit(r, "proxy.stream.apply", req.Spec.Name, map[string]any{"result": "rejected"})
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
		}
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "proxy.stream.apply", req.Spec.Name, map[string]any{
		"listen": req.Spec.Listen, "protocol": req.Spec.Protocol, "upstream": req.Spec.Upstream,
	})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleStreamDelete(w http.ResponseWriter, r *http.Request) error {
	name := chi.URLParam(r, "name")
	if err := s.modules.proxy.DeleteStream(r.Context(), name); err != nil {
		return httpx.BadRequest("%v", err)
	}
	reload, reloadErr := s.modules.proxy.Reload(r.Context(), proxysvc.KindNginx)
	httpx.SetAudit(r, "proxy.stream.delete", name, map[string]any{"reloaded": reloadErr == nil})
	out := map[string]any{"name": name, "reload": reload}
	if reloadErr != nil {
		out["reloadError"] = reloadErr.Error()
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}
