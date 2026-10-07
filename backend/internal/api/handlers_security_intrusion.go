package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/go-chi/chi/v5"
)

// CrowdSec and Suricata, the intrusion tools beside fail2ban on the Security
// section's Intrusion page. Mounted from mountSecurityRoutes.
//
// An absent tool is information, as fail2ban's is: both readings answer 200
// with installed false, and the page draws the placeholder.
func (s *Server) mountSecurityIntrusionRoutes(r chi.Router) {
	r.Route("/security/crowdsec", func(r chi.Router) {
		// The decisions are the addresses currently banned and why, which
		// fail2ban's status shows to every role that can read it.
		r.Method(http.MethodGet, "/", s.handle(s.handleCrowdSec))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/decisions", s.handle(s.handleCrowdSecAdd))
			// Releasing a ban re-admits whatever earned it. Recoverable —
			// the engine bans it again on its next offence — but it is
			// the one action here that lowers a defence, so it is
			// marked like the other releases that do.
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/decisions/{id}", s.handle(s.handleCrowdSecDelete))
			})
		})
	})
	// An alert names the internal hosts it was raised about and the ports they
	// were reached on, so Suricata is system.admin for the reason the failed
	// logins are.
	r.Route("/security/suricata", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/", s.handle(s.handleSuricata))
	})
}

func (s *Server) handleCrowdSec(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 40*time.Second)
	defer cancel()
	view, err := s.modules.netsec.CrowdSec(ctx)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

type crowdSecDecisionRequest struct {
	Value    string `json:"value"`
	Duration string `json:"duration"`
	Reason   string `json:"reason"`
}

func (s *Server) handleCrowdSecAdd(w http.ResponseWriter, r *http.Request) error {
	var req crowdSecDecisionRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	// The caller's own address goes down with the request for the reason it
	// does on the fail2ban ban: a decision is a drop at the bouncer, and
	// banning the address you are connected from ends this session.
	out, err := s.modules.netsec.AddDecision(ctx, req.Value, req.Duration, req.Reason, httpx.ClientIP(r))
	if err != nil {
		switch {
		case errors.Is(err, netsec.ErrLockout):
			httpx.SetAudit(r, "crowdsec.decision.add", req.Value, map[string]any{"result": "refused_lockout"})
			return httpx.Err(http.StatusConflict, "would_lock_you_out", err.Error())
		case errors.Is(err, netsec.ErrCrowdSecMissing):
			return httpx.Err(http.StatusServiceUnavailable, "crowdsec_unavailable", err.Error())
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "crowdsec.decision.add", req.Value, map[string]any{"duration": req.Duration, "reason": req.Reason})
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

func (s *Server) handleCrowdSecDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		return httpx.BadRequest("invalid decision id")
	}
	ctx, cancel := timeoutCtx(r, 30*time.Second)
	defer cancel()
	out, err := s.modules.netsec.DeleteDecision(ctx, id)
	if err != nil {
		if errors.Is(err, netsec.ErrCrowdSecMissing) {
			return httpx.Err(http.StatusServiceUnavailable, "crowdsec_unavailable", err.Error())
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "crowdsec.decision.delete", strconv.Itoa(id), nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

func (s *Server) handleSuricata(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := timeoutCtx(r, 20*time.Second)
	defer cancel()
	// eve.json is a log, so it is read as every log is: only inside JD_LOG_ROOTS,
	// by the check the log viewer uses. The path handed back is the one the
	// check resolved, so a link swapped in between is not what gets opened.
	allow := func(path string) (string, error) {
		if err := s.modules.logs.Allow(path); err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved, nil
		}
		return path, nil
	}
	view, err := s.modules.netsec.Suricata(ctx, allow)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}
