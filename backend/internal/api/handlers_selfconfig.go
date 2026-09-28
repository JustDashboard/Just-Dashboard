package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfcfg"
)

// The dashboard's own settings: where it listens, who may reach it, and the
// buttons that restart it into a change.
//
// It sits beside /dashboard/update rather than under /system for the same
// reason that one does: /system is the operator's server, this is the tool
// they are looking at it through, and the two being confusable is exactly why
// they are named apart.

// handleSelfConfigStatus answers everything the settings page needs in one
// request: the configuration on disk, the endpoint it implies, anything the
// running process disagrees with, and the restart in flight if there is one.
func (s *Server) handleSelfConfigStatus(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, s.modules.selfConfig.Report(r.Context()))
	return nil
}

// handleSelfConfigApply writes new settings and restarts into them.
//
// It answers 202 before the work finishes, because the work replaces the
// process serving the request. The browser then follows the run record, which
// is on disk and survives everything that is about to happen — including,
// after a port change, the fact that the address it was reading it from is no
// longer the address the dashboard answers on.
func (s *Server) handleSelfConfigApply(w http.ResponseWriter, r *http.Request) error {
	var next selfcfg.Settings
	if err := httpx.DecodeJSON(r, &next); err != nil {
		return err
	}

	rep := s.modules.selfConfig.Report(r.Context())
	if !rep.Supported {
		return httpx.Err(http.StatusServiceUnavailable, "config_unsupported", rep.Reason)
	}

	// No typed phrase, deliberately. A change that moves the address is worth
	// reading before it is made — but the reading happens in the dialog, which
	// lists every before-and-after and states the URL the dashboard will answer
	// on next. Making somebody transcribe a forty-character MagicDNS name on
	// top of that tested their typing, not their attention, and the change is
	// undone on its own anyway when the new configuration does not come back.
	actor := httpx.MustPrincipal(r).Username()
	run, err := s.modules.selfConfig.Apply(r.Context(), next, actor, httpx.ClientIP(r))
	if err != nil {
		return mapSelfConfigError(err)
	}
	httpx.SetAudit(r, "dashboard.config.apply", run.Endpoint, map[string]any{
		"changes": run.Changes, "run": run.ID,
	})
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

// handleSelfConfigRestart recreates the stack on the configuration already on
// disk, rebuilding the images first when asked.
func (s *Server) handleSelfConfigRestart(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Rebuild bool `json:"rebuild"`
	}
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &body); err != nil {
			return err
		}
	}
	actor := httpx.MustPrincipal(r).Username()
	run, err := s.modules.selfConfig.Restart(r.Context(), body.Rebuild, actor)
	if err != nil {
		return mapSelfConfigError(err)
	}
	httpx.SetAudit(r, "dashboard.restart", string(run.Action), map[string]any{"run": run.ID})
	httpx.JSON(w, http.StatusAccepted, run)
	return nil
}

// handleSelfConfigCertificate issues the Tailscale certificate now.
//
// The keeper would get there on its own, but "on its own" is up to ten minutes
// after an operator flipped the one switch this was waiting for — and they are
// looking at the browser warning while it counts down. This is that wait, made
// pressable: it issues the certificate and restarts the proxy that serves it,
// so the padlock arrives while the page they asked from is still open.
func (s *Server) handleSelfConfigCertificate(w http.ResponseWriter, r *http.Request) error {
	// Ensure is a no-op in any other mode, and a no-op reported as success is
	// a button that lies about what it did.
	if !strings.EqualFold(strings.TrimSpace(s.Cfg.TLSMode), selfcfg.TLSTailscale) {
		return httpx.BadRequest("this dashboard is not set to use a Tailscale certificate, so there is none to issue")
	}
	if err := s.modules.certKeeper.Ensure(r.Context()); err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "dashboard.config.certificate", s.Cfg.Site, nil)
	httpx.JSON(w, http.StatusOK, selfcfg.Certificate(s.Cfg.DataDir, s.Cfg.Site))
	return nil
}

// handleSelfConfigLog answers the whole transcript of the last restart as
// plain text — a rebuild's every BuildKit step, where the report has room for
// only the last 64 KB of them.
func (s *Server) handleSelfConfigLog(w http.ResponseWriter, r *http.Request) error {
	writeTranscript(w, s.modules.selfConfig.Transcript())
	return nil
}

// handleSelfConfigDismiss forgets a finished run, which is what clears the
// card once it has been read.
func (s *Server) handleSelfConfigDismiss(w http.ResponseWriter, r *http.Request) error {
	if err := s.modules.selfConfig.Dismiss(); err != nil {
		return mapSelfConfigError(err)
	}
	httpx.NoContent(w)
	return nil
}

func mapSelfConfigError(err error) error {
	switch {
	case errors.Is(err, selfcfg.ErrInProgress):
		return httpx.Err(http.StatusConflict, "restart_in_progress", err.Error())
	case errors.Is(err, selfcfg.ErrNoChange):
		return httpx.BadRequest("%v", err)
	case errors.Is(err, selfcfg.ErrNoLocation):
		return httpx.Err(http.StatusServiceUnavailable, "config_unsupported", err.Error())
	default:
		// Everything else reaching here is a validation failure with a
		// sentence written for the person reading it, so it is passed through
		// rather than flattened into "internal error".
		return httpx.BadRequest("%v", err)
	}
}
