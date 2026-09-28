package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// handleProxySettings answers nginx's server-wide directives as nginx loads
// them, each with the file and line that sets it or nginx's default. It runs
// `nginx -T`, the config test's trust, so it is mounted with the test.
func (s *Server) handleProxySettings(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), effectiveBudget)
	defer cancel()
	rep, err := s.modules.proxy.Settings(ctx)
	if err != nil {
		return settingsError(w, r, err)
	}
	httpx.JSON(w, http.StatusOK, rep)
	return nil
}

type proxySettingsRequest struct {
	Changes []proxysvc.SettingChange `json:"changes"`
	Reload  bool                     `json:"reload"`
}

// handleProxySettingsPreview answers the lines a change would write, and in
// which file, without writing them.
func (s *Server) handleProxySettingsPreview(w http.ResponseWriter, r *http.Request) error {
	var req proxySettingsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), effectiveBudget)
	defer cancel()
	edits, err := s.modules.proxy.PreviewSettings(ctx, req.Changes)
	if err != nil {
		return settingsError(w, r, err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"edits": edits})
	return nil
}

// handleProxySettingsWrite writes each change into the file that sets it, or
// the dashboard's own conf.d file, through the config editor's tested write,
// and reloads when asked. A write nginx refuses leaves its file as it was.
func (s *Server) handleProxySettingsWrite(w http.ResponseWriter, r *http.Request) error {
	var req proxySettingsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// Two config tests and the wait for the service lock: generous for the
	// tests, short of a certificate order holding the lock for minutes.
	ctx, cancel := context.WithTimeout(r.Context(), effectiveBudget+10*time.Second)
	defer cancel()
	edits, res, err := s.modules.proxy.ApplySettings(ctx, req.Changes)
	files := make([]string, 0, len(edits))
	for _, e := range edits {
		files = append(files, e.File)
	}
	meta := map[string]any{"changes": req.Changes, "written": files}
	var refused *proxysvc.DumpRefusedError
	if err != nil {
		meta["result"] = "failed"
		httpx.SetAudit(r, "proxy.settings.write", "nginx", meta)
		if errors.Is(err, proxysvc.ErrInvalidConf) && !errors.As(err, &refused) {
			return refuseInvalidConfig(w, r, httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Output), res)
		}
		return settingsError(w, r, err)
	}
	out := map[string]any{"edits": edits, "validation": res}
	if req.Reload {
		reload, err := s.modules.proxy.Reload(r.Context(), proxysvc.KindNginx)
		out["reload"] = reload
		if err != nil {
			meta["reloaded"] = false
			httpx.SetAudit(r, "proxy.settings.write", "nginx", meta)
			return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
		}
	}
	meta["reloaded"] = req.Reload
	httpx.SetAudit(r, "proxy.settings.write", "nginx", meta)
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// handleProxyLint answers what the configuration nginx loads does that is
// legal but probably not meant, each at its file and line.
func (s *Server) handleProxyLint(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), effectiveBudget)
	defer cancel()
	files, at, err := s.modules.proxy.EffectiveConfigAt(ctx)
	if err != nil {
		return effectiveError(w, r, err)
	}
	tree, err := proxysvc.NginxTree(files)
	if err != nil {
		return httpx.Wrap(http.StatusBadGateway, "tree_failed", err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"findings": proxysvc.Lint(tree), "checkedAt": at})
	return nil
}

// settingsError answers a settings read or change that could not be made: a
// change refused before any file was touched is the caller's, the rest are
// the dump's or the file's.
func settingsError(w http.ResponseWriter, r *http.Request, err error) error {
	var refused *proxysvc.DumpRefusedError
	switch {
	case errors.Is(err, proxysvc.ErrBadSetting):
		return httpx.BadRequest("%v", err)
	case errors.As(err, &refused), errors.Is(err, context.DeadlineExceeded):
		return effectiveError(w, r, err)
	}
	return mapProxyError(err)
}
