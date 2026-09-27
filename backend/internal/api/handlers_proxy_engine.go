package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountEngineRoutes is the engine itself: what it is, its config test and
// reload, and the raw configuration editor.
func (s *Server) mountEngineRoutes(r chi.Router) {
	r.Method(http.MethodGet, "/status", s.handle(s.handleProxyStatus))
	r.Method(http.MethodGet, "/config", s.handle(s.handleProxyConfigRead))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		// Validation is not a read: nginx cannot test a config it cannot
		// see at its own path, so validating puts the candidate on disk
		// for the length of one `nginx -t`. That is the same trust as
		// writing it, so it is gated the same way.
		r.Method(http.MethodPost, "/validate", s.handle(s.handleProxyValidate))
		r.Method(http.MethodPut, "/config", s.handle(s.handleProxyConfigWrite))
		// A test of what is on disk touches nothing, but it runs the
		// host's own binary and is the same sentence of trust as a
		// reload, so it is gated with it.
		r.Method(http.MethodPost, "/test", s.handle(s.handleProxyTest))
		r.Method(http.MethodPost, "/reload", s.handle(s.handleProxyReload))
	})
}

func (s *Server) handleProxyStatus(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.proxy.Availability(r.Context()))
	return nil
}

func (s *Server) handleProxyConfigRead(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Query().Get("path")
	if path == "" {
		return httpx.BadRequest("path query parameter is required")
	}
	content, err := s.modules.proxy.ReadConfig(path)
	if err != nil {
		return mapProxyError(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"path": path, "content": content})
	return nil
}

type proxyConfigRequest struct {
	Kind    proxysvc.Kind `json:"kind"`
	Path    string        `json:"path"`
	Content string        `json:"content"`
	Reload  bool          `json:"reload"`
}

// handleProxyValidate tells the operator whether a config would be accepted,
// and leaves what is currently serving traffic as it was. It is audited rather
// than skipped because the nginx path touches the real file to do it.
func (s *Server) handleProxyValidate(w http.ResponseWriter, r *http.Request) error {
	var req proxyConfigRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, err := s.modules.proxy.Validate(r.Context(), req.Kind, req.Path, req.Content)
	if err != nil {
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "proxy.config.validate", req.Path,
		map[string]any{"kind": req.Kind, "valid": res.Valid, "bytes": len(req.Content)})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleProxyConfigWrite(w http.ResponseWriter, r *http.Request) error {
	var req proxyConfigRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, err := s.modules.proxy.WriteConfig(r.Context(), req.Kind, req.Path, req.Content)
	if err != nil {
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			httpx.SetAudit(r, "proxy.config.write", req.Path, map[string]any{"result": "rejected"})
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Output)
		}
		return mapProxyError(err)
	}
	out := map[string]any{"validation": res}
	if req.Reload {
		reload, err := s.modules.proxy.Reload(r.Context(), req.Kind)
		out["reload"] = reload
		if err != nil {
			httpx.SetAudit(r, "proxy.config.write", req.Path, map[string]any{"reloaded": false})
			return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
		}
	}
	httpx.SetAudit(r, "proxy.config.write", req.Path,
		map[string]any{"kind": req.Kind, "reloaded": req.Reload, "bytes": len(req.Content)})
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

type reloadRequest struct {
	Kind proxysvc.Kind `json:"kind"`
}

// handleProxyTest answers "would a reload succeed right now" without
// reloading: the server's own config test against the files on disk.
func (s *Server) handleProxyTest(w http.ResponseWriter, r *http.Request) error {
	var req reloadRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Kind == "" {
		req.Kind = proxysvc.KindNginx
	}
	res := s.modules.proxy.Test(r.Context(), req.Kind)
	httpx.SetAudit(r, "proxy.config.test", string(req.Kind), map[string]any{"valid": res.Valid})
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleProxyReload(w http.ResponseWriter, r *http.Request) error {
	var req reloadRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	if req.Kind == "" {
		req.Kind = proxysvc.KindNginx
	}
	res, err := s.modules.proxy.Reload(r.Context(), req.Kind)
	if err != nil {
		httpx.SetAudit(r, "proxy.reload", string(req.Kind), map[string]any{"result": "failed"})
		if errors.Is(err, proxysvc.ErrInvalidConf) {
			return httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output)
		}
		return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
	}
	httpx.SetAudit(r, "proxy.reload", string(req.Kind), nil)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}
