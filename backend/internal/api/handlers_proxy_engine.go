package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// mountEngineRoutes is the engine itself: what it is, its config test and
// reload, its service, and the raw configuration editor.
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
		// The engine's last test, whoever ran it and whichever command it
		// ran before. Its output names files and quotes them, which is the
		// same trust as running it.
		r.Method(http.MethodGet, "/test/last", s.handle(s.handleProxyTestLast))
		r.Method(http.MethodPost, "/reload", s.handle(s.handleProxyReload))
		// The engine's own service, resolved here rather than named by the
		// caller. Start and restart run the config test first; stop and
		// restart take every site offline, so they sit behind destructive.
		// Enabling at boot and clearing a failed state start nothing.
		r.Method(http.MethodPost, "/engine/start", s.handle(s.handleProxyEngine(procs.UnitStart)))
		r.Method(http.MethodPost, "/engine/enable", s.handle(s.handleProxyEngine(procs.UnitEnable)))
		r.Method(http.MethodPost, "/engine/reset-failed", s.handle(s.handleProxyEngine(procs.UnitResetFailed)))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodPost, "/engine/restart", s.handle(s.handleProxyEngine(procs.UnitRestart)))
			r.Method(http.MethodPost, "/engine/stop", s.handle(s.handleProxyEngine(procs.UnitStop)))
		})
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
	// The path comes from a list read earlier, and a site can be deleted or
	// renamed in between. That is a state of the host, not a bad request,
	// and reading again once the file is back is the remedy.
	if errors.Is(err, fs.ErrNotExist) {
		return httpx.Err(http.StatusNotFound, "not_found", "That file is not on disk.").
			Because("It may have been removed or renamed since this page last loaded — by a site change, a deploy, or somebody in a shell.", err.Error()).
			Retry()
	}
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

// decode reads a file the config editor validates or saves. The service
// treats every kind but caddy as nginx, so an unknown engine validated as
// nginx, and "caddy-ingress" wrote an nginx file and then reloaded the Docker
// Caddy. The ingress's Caddyfile lives in its container and is written by
// deployments, so these routes refuse it rather than edit a host file for it.
func (req *proxyConfigRequest) decode(r *http.Request) error {
	if err := httpx.DecodeJSON(r, req); err != nil {
		return err
	}
	if err := knownEngine(&req.Kind); err != nil {
		return err
	}
	if req.Kind == proxysvc.KindCaddyIngress {
		return httpx.BadRequest("the Caddy ingress is configured by deployments, not by the config editor")
	}
	return nil
}

// handleProxyValidate tells the operator whether a config would be accepted,
// and leaves what is currently serving traffic as it was. It is audited rather
// than skipped because the nginx path touches the real file to do it.
func (s *Server) handleProxyValidate(w http.ResponseWriter, r *http.Request) error {
	var req proxyConfigRequest
	if err := req.decode(r); err != nil {
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
	if err := req.decode(r); err != nil {
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

// decode reads which engine a test or reload is for.
func (req *reloadRequest) decode(r *http.Request) error {
	if err := httpx.DecodeJSON(r, req); err != nil {
		return err
	}
	return knownEngine(&req.Kind)
}

// knownEngine makes an unnamed engine nginx and refuses one the service does
// not know, which used to fall through to nginx and act on the wrong server.
func knownEngine(kind *proxysvc.Kind) error {
	if *kind == "" {
		*kind = proxysvc.KindNginx
	}
	if !kind.Known() {
		return httpx.BadRequest("unknown engine %q", *kind)
	}
	return nil
}

// handleProxyTest answers "would a reload succeed right now" without
// reloading: the server's own config test against the files on disk.
func (s *Server) handleProxyTest(w http.ResponseWriter, r *http.Request) error {
	var req reloadRequest
	if err := req.decode(r); err != nil {
		return err
	}
	res, err := s.modules.proxy.Test(r.Context(), req.Kind)
	if err != nil {
		httpx.SetAudit(r, "proxy.config.test", string(req.Kind), map[string]any{"result": "failed"})
		return mapProxyError(err)
	}
	httpx.SetAudit(r, "proxy.config.test", string(req.Kind), map[string]any{"valid": res.Valid})
	httpx.JSON(w, http.StatusOK, s.placeNameConflicts(r, req.Kind, res))
	return nil
}

// handleProxyTestLast answers with the engine's most recent config test, or
// 204 when none has run since the dashboard started.
func (s *Server) handleProxyTestLast(w http.ResponseWriter, r *http.Request) error {
	kind := proxysvc.Kind(r.URL.Query().Get("kind"))
	if err := knownEngine(&kind); err != nil {
		return err
	}
	rec, ok := s.modules.proxy.LastTest(kind)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	rec.Validation = s.placeNameConflicts(r, kind, rec.Validation)
	httpx.JSON(w, http.StatusOK, rec)
	return nil
}

// nameConflictBudget bounds the `nginx -T` a conflicting server name is placed
// with. The dump waits for the service lock, which a certificate order can
// hold for minutes, and the test's answer is worth more now than placed later.
const nameConflictBudget = 3 * time.Second

// placeNameConflicts places nginx's conflicting-server-name warnings at the
// blocks that claim the name, against the configuration as it is now.
func (s *Server) placeNameConflicts(r *http.Request, kind proxysvc.Kind, res *proxysvc.ValidationResult) *proxysvc.ValidationResult {
	if kind != proxysvc.KindNginx {
		return res
	}
	ctx, cancel := context.WithTimeout(r.Context(), nameConflictBudget)
	defer cancel()
	return s.modules.proxy.PlaceNameConflicts(ctx, res)
}

func (s *Server) handleProxyReload(w http.ResponseWriter, r *http.Request) error {
	var req reloadRequest
	if err := req.decode(r); err != nil {
		return err
	}
	res, err := s.modules.proxy.Reload(r.Context(), req.Kind)
	if err != nil {
		httpx.SetAudit(r, "proxy.reload", string(req.Kind), map[string]any{"result": "failed"})
		switch {
		case errors.Is(err, proxysvc.ErrInvalidConf):
			// The test comes back beside the error, as a refused start's
			// does, so the page can place each line at its file.
			return refuseInvalidConfig(w, r, httpx.Err(http.StatusUnprocessableEntity, "invalid_config", res.Validation.Output),
				s.placeNameConflicts(r, req.Kind, res.Validation))
		case errors.Is(err, proxysvc.ErrTestUnfinished):
			return mapProxyError(fmt.Errorf("%w, so nothing was reloaded", err))
		case errors.Is(err, proxysvc.ErrNoIngress):
			return mapProxyError(err)
		}
		return httpx.Err(http.StatusBadGateway, "reload_failed", err.Error())
	}
	httpx.SetAudit(r, "proxy.reload", string(req.Kind), nil)
	// The reload's toast opens its test, which places a conflicting name as
	// Test config's does.
	res.Validation = s.placeNameConflicts(r, req.Kind, res.Validation)
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

// engineDone is what a refused action did not do, for its sentence.
var engineDone = map[procs.UnitAction]string{
	procs.UnitStart: "started", procs.UnitRestart: "restarted",
}

// handleProxyEngine starts, restarts, stops, enables at boot or clears the
// failed state of the engine's systemd unit.
//
// The overview used to post to /systemd/{unit}/restart, which ran systemctl
// straight away. This host's nginx.service tests its configuration before it
// starts, so a restart over a broken file stopped nginx and could not bring
// it back, where Reload had always refused. Start and restart are refused
// with nginx's own words when the test fails, and systemctl never runs; the
// verbs that start nothing run no test.
func (s *Server) handleProxyEngine(action procs.UnitAction) httpx.Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		engine, err := s.modules.proxy.Engine()
		if err != nil {
			return httpx.Err(http.StatusConflict, "no_engine_unit", err.Error())
		}
		event := "proxy.engine." + string(action)
		var out *procs.CommandResult
		control := func() error {
			var err error
			out, err = s.modules.systemd.Control(r.Context(), engine.Unit, action)
			return err
		}
		if _, tested := engineDone[action]; !tested {
			err = control()
		} else {
			var res *proxysvc.ValidationResult
			res, err = s.modules.proxy.WithTestedConfig(r.Context(), engine.Kind, control)
			switch {
			case errors.Is(err, proxysvc.ErrInvalidConf):
				httpx.SetAudit(r, event, engine.Unit, map[string]any{"result": "refused", "valid": false})
				return refuseInvalidConfig(w, r, httpx.Err(http.StatusUnprocessableEntity, "invalid_config",
					fmt.Sprintf("%s was not %s: its configuration test failed.\n%s", engine.Name, engineDone[action], res.Output)),
					s.placeNameConflicts(r, engine.Kind, res))
			case errors.Is(err, proxysvc.ErrTestUnfinished):
				httpx.SetAudit(r, event, engine.Unit, map[string]any{"result": "failed"})
				return mapProxyError(fmt.Errorf("%w, so %s was not %s", err, engine.Name, engineDone[action]))
			}
		}
		if err != nil {
			httpx.SetAudit(r, event, engine.Unit, map[string]any{"result": "failed"})
			return mapProcsError(err)
		}
		httpx.SetAudit(r, event, engine.Unit, map[string]any{"exitCode": out.ExitCode})
		httpx.JSON(w, http.StatusOK, map[string]any{
			"action": action, "unit": engine.Unit,
			"output": strings.TrimSpace(out.Stdout + out.Stderr),
		})
		return nil
	}
}

// refuseInvalidConfig answers a start, restart or reload the config test
// turned down: the error every client reads, and beside it the test itself, so
// the page can put each of nginx's lines at the file and line it names rather
// than print the output as one block. The error's message is kept for the
// audit trail the way the central error writer keeps it.
func refuseInvalidConfig(w http.ResponseWriter, r *http.Request, refusal *httpx.APIError, res *proxysvc.ValidationResult) error {
	if p, ok := httpx.PrincipalFrom(r.Context()); ok {
		p.FailureReason = refusal.Message
	}
	httpx.JSON(w, refusal.Status, map[string]any{"error": refusal, "validation": res})
	return nil
}
