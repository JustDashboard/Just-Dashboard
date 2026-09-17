package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/go-chi/chi/v5"
)

func (s *Server) mountNetSecRoutes(r chi.Router) {
	// How the dashboard itself is reachable. Any signed-in principal may read
	// it: knowing you are exposed is not privileged information, and hiding it
	// from a limited role would only delay somebody noticing.
	r.Method(http.MethodGet, "/exposure", s.handle(s.handleExposure))

	r.Route("/firewall", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleFirewallStatus))
		r.Method(http.MethodGet, "/apps", s.handle(s.handleFirewallApps))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/rules", s.handle(s.handleFirewallAddRule))
			// Editing is a replace, and it is not in the destructive group:
			// the new rule goes in before the old one comes out, so the worst
			// failure leaves the firewall exactly as strict as it was.
			r.Method(http.MethodPut, "/rules/{number}", s.handle(s.handleFirewallReplaceRule))
			// Logging is the one firewall setting that cannot cost anybody
			// their access, so it is the one that stays out of the
			// destructive group.
			r.Method(http.MethodPost, "/logging", s.handle(s.handleFirewallLogging))
			s.destructive(r, func(r chi.Router) {
				// Turning the firewall off, deleting the rule that admits
				// you, flipping the inbound default or wiping every rule is
				// how an operator locks themselves out of the box.
				r.Method(http.MethodPost, "/enabled", s.handle(s.handleFirewallToggle))
				r.Method(http.MethodPost, "/policy", s.handle(s.handleFirewallPolicy))
				r.Method(http.MethodPost, "/reset", s.handle(s.handleFirewallReset))
				r.Method(http.MethodDelete, "/rules/{number}", s.handle(s.handleFirewallDeleteRule))
			})
		})
	})

	r.Route("/fail2ban", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleFail2banStatus))
		r.Method(http.MethodGet, "/history", s.handle(s.handleBanHistory))
		r.Method(http.MethodGet, "/offenders", s.handle(s.handleBanOffenders))
		r.Method(http.MethodGet, "/{jail}/config", s.handle(s.handleJailConfig))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodPost, "/{jail}/unban", s.handle(s.handleFail2banUnban))
			r.Method(http.MethodPost, "/{jail}/ban", s.handle(s.handleFail2banBan))
			r.Method(http.MethodPost, "/{jail}/config", s.handle(s.handleJailParam))
			r.Method(http.MethodPost, "/{jail}/ignore", s.handle(s.handleJailIgnore))
			// Releasing every ban is undone by the jail itself within
			// minutes — whoever earned a ban earns the next one — so it is
			// not in the destructive group despite reading like it should be.
			r.Method(http.MethodPost, "/{jail}/unban-all", s.handle(s.handleJailUnbanAll))
		})
	})

	r.Route("/logins", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleLoginHistory))
		// btmp records whatever was typed at a login prompt, and what people
		// type at a login prompt is sometimes their password in the username
		// field. Successful logins are ordinary operational history; the
		// failed ones are closer to reading somebody's keystrokes, so they
		// need the capability that already implies full access.
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
			r.Method(http.MethodGet, "/failed", s.handle(s.handleFailedLogins))
			r.Method(http.MethodGet, "/attackers", s.handle(s.handleFailedLoginSummary))
		})
	})

	// The posture verdict, SSH hardening, the connection table and the
	// diagnostic tools. Kept in handlers_security.go, mounted from here so
	// the security route map still has one place to be read.
	s.mountSecurityRoutes(r)
}

// handleBanHistory answers what fail2ban has actually been doing.
//
// A jail's status lists only the bans in force at this instant, and bans
// expire — so the burst that was banned and released overnight has already
// vanished from the page by morning. This reads fail2ban's own log, which has
// held the answer all along.
func (s *Server) handleBanHistory(w http.ResponseWriter, r *http.Request) error {
	events, err := s.modules.netsec.BanHistory(r.Context(), loginLimit(r))
	if err != nil {
		return httpx.Internal(err)
	}
	if len(events) == 0 && !s.modules.netsec.Fail2banAvailable() {
		return httpx.Err(http.StatusServiceUnavailable, "fail2ban_unavailable",
			"fail2ban is not installed on this host")
	}
	httpx.JSON(w, http.StatusOK, events)
	return nil
}

// handleLoginHistory answers "who got in while I was not watching".
//
// The SSH sessions table only ever showed the logins in progress at the
// instant it was loaded, which is the one moment nobody is worried about. This
// reads the host's own wtmp — the record was always there, the dashboard just
// never showed it.
func (s *Server) handleLoginHistory(w http.ResponseWriter, r *http.Request) error {
	records, err := s.modules.netsec.LoginHistory(r.Context(), loginLimit(r))
	if err != nil {
		return httpx.Err(http.StatusServiceUnavailable, "login_history_unavailable",
			"the host's login record could not be read on this system")
	}
	httpx.JSON(w, http.StatusOK, records)
	return nil
}

func (s *Server) handleFailedLogins(w http.ResponseWriter, r *http.Request) error {
	records, err := s.modules.netsec.FailedLogins(r.Context(), loginLimit(r))
	if err != nil {
		return httpx.Err(http.StatusServiceUnavailable, "login_history_unavailable",
			"the host keeps no failed-login record, or it could not be read")
	}
	httpx.JSON(w, http.StatusOK, records)
	return nil
}

// handleFailedLoginSummary answers "who is trying", which the failed-login
// listing cannot: five hundred lines of the same address are one attacker,
// and the address that appears on every page of the listing is the one worth
// a firewall rule. Behind system.admin for the reason the listing is — it is
// folded from the same record.
func (s *Server) handleFailedLoginSummary(w http.ResponseWriter, r *http.Request) error {
	window := failedLoginWindow
	if hours := atoiDefault(r.URL.Query().Get("hours"), 0); hours > 0 && hours <= 24*90 {
		window = time.Duration(hours) * time.Hour
	}
	top := atoiDefault(r.URL.Query().Get("top"), 25)
	if top < 1 || top > 100 {
		top = 25
	}
	summary, err := s.modules.netsec.FailedLoginSummary(r.Context(), window, top)
	if err != nil {
		return httpx.Err(http.StatusServiceUnavailable, "login_history_unavailable",
			"the host keeps no failed-login record, or it could not be read")
	}
	httpx.JSON(w, http.StatusOK, summary)
	return nil
}

// loginLimit caps the read. btmp on an internet-facing host runs to hundreds
// of thousands of lines, and a table nobody can scroll is not more honest than
// a capped one.
func loginLimit(r *http.Request) int {
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	return limit
}

func (s *Server) handleFirewallStatus(w http.ResponseWriter, r *http.Request) error {
	st, err := s.modules.netsec.Status(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}

func (s *Server) handleFirewallAddRule(w http.ResponseWriter, r *http.Request) error {
	var req netsec.RuleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// The caller's own address is handed to the firewall layer so it can
	// refuse a rule that would sever this very connection.
	out, err := s.modules.netsec.AddRule(r.Context(), req, httpx.ClientIP(r))
	if err != nil {
		if errors.Is(err, netsec.ErrLockout) {
			httpx.SetAudit(r, "firewall.rule.add", req.Port, map[string]any{"result": "refused_lockout"})
			return httpx.Err(http.StatusConflict, "would_lock_you_out", err.Error())
		}
		return mapFirewallError(err)
	}
	httpx.SetAudit(r, "firewall.rule.add", req.Port, req)
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

// mapFirewallError distinguishes "this host's firewall cannot do that" from
// "you asked for something invalid". They deserve different words, and only
// the first is worth a code the UI keys off.
func mapFirewallError(err error) error {
	if errors.Is(err, netsec.ErrReadOnly) {
		return httpx.Err(http.StatusNotImplemented, "firewall_read_only", err.Error())
	}
	if errors.Is(err, netsec.ErrNoFirewall) {
		return httpx.Err(http.StatusServiceUnavailable, "no_firewall", err.Error())
	}
	return httpx.BadRequest("%v", err)
}

func (s *Server) handleFirewallReplaceRule(w http.ResponseWriter, r *http.Request) error {
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		return httpx.BadRequest("invalid rule number")
	}
	var req netsec.RuleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	out, err := s.modules.netsec.ReplaceRule(r.Context(), number, req, httpx.ClientIP(r))
	if err != nil {
		if errors.Is(err, netsec.ErrLockout) {
			httpx.SetAudit(r, "firewall.rule.replace", strconv.Itoa(number),
				map[string]any{"result": "refused_lockout"})
			return httpx.Err(http.StatusConflict, "would_lock_you_out", err.Error())
		}
		return mapFirewallError(err)
	}
	httpx.SetAudit(r, "firewall.rule.replace", strconv.Itoa(number), req)
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

func (s *Server) handleFirewallDeleteRule(w http.ResponseWriter, r *http.Request) error {
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil {
		return httpx.BadRequest("invalid rule number")
	}
	// No typed phrase: a rule is one line of configuration, visible on the row
	// being deleted and re-addable from the form beside it. Turning the
	// firewall off entirely is the route below, and that still asks.
	out, err := s.modules.netsec.DeleteRule(r.Context(), number)
	if err != nil {
		return mapFirewallError(err)
	}
	httpx.SetAudit(r, "firewall.rule.delete", strconv.Itoa(number), nil)
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

type firewallToggleRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) handleFirewallToggle(w http.ResponseWriter, r *http.Request) error {
	var req firewallToggleRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	// Enabling ufw applies its default-deny policy immediately; if the
	// dashboard's own port is not already allowed, that is a lockout.
	phrase := "disable firewall"
	if req.Enabled {
		phrase = "enable firewall"
	}
	if err := httpx.RequireTypedConfirmation(w, r, phrase); err != nil {
		return err
	}
	out, err := s.modules.netsec.SetEnabled(r.Context(), req.Enabled)
	if err != nil {
		return mapFirewallError(err)
	}
	httpx.SetAudit(r, "firewall.toggle", "", map[string]any{"enabled": req.Enabled})
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

func (s *Server) handleFail2banStatus(w http.ResponseWriter, r *http.Request) error {
	st, err := s.modules.netsec.Fail2banStatus(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}

type banRequest struct {
	IP string `json:"ip"`
}

func (s *Server) handleFail2banUnban(w http.ResponseWriter, r *http.Request) error {
	var req banRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	jail := chi.URLParam(r, "jail")
	out, err := s.modules.netsec.Unban(r.Context(), jail, req.IP)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "fail2ban.unban", jail, map[string]any{"ip": req.IP})
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

func (s *Server) handleFail2banBan(w http.ResponseWriter, r *http.Request) error {
	var req banRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	jail := chi.URLParam(r, "jail")
	// The caller's own address goes down with the request for the reason it
	// does on the firewall route: a ban is a drop rule, and banning yourself
	// ends this session and every future one from here.
	out, err := s.modules.netsec.Ban(r.Context(), jail, req.IP, httpx.ClientIP(r))
	if err != nil {
		if errors.Is(err, netsec.ErrLockout) {
			httpx.SetAudit(r, "fail2ban.ban", jail, map[string]any{"result": "refused_lockout"})
			return httpx.Err(http.StatusConflict, "would_lock_you_out", err.Error())
		}
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "fail2ban.ban", jail, map[string]any{"ip": req.IP})
	httpx.JSON(w, http.StatusOK, map[string]string{"output": out})
	return nil
}

func (s *Server) handleSSHSessions(w http.ResponseWriter, r *http.Request) error {
	sessions, err := s.modules.netsec.Sessions(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, sessions)
	return nil
}

// handleExposure reports who can reach this dashboard, graded. It reads the
// allowlist the process actually booted with rather than re-reading a file, so
// it describes the running configuration and not an edited one that has yet to
// be applied.
func (s *Server) handleExposure(w http.ResponseWriter, r *http.Request) error {
	exposure := netsec.DescribeExposure(s.Cfg.AllowedCIDRs)
	// The address this request came from, so the pages can name the one
	// address every lockout guard is protecting — and offer it to fail2ban's
	// allowlist before somebody bans themselves.
	exposure.Client = httpx.ClientIP(r)
	httpx.JSON(w, http.StatusOK, exposure)
	return nil
}
