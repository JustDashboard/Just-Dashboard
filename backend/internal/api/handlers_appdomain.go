package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/appdomain"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The just-dashboard.com address of this server (backend/internal/appdomain,
// docs/internal/backend/app-domains.md).
//
// Under the account link's routes, so administrators only. Claiming and
// releasing publish or withdraw this server's IP address in public DNS under
// the project's domain, so like linking they are a person's own act in a
// browser session: an API key is refused with session_required, and the
// assistant does not make them (agentExcludedRoutes). Releasing is in the
// destructive budget.
func (s *Server) mountAppDomainRoutes(r chi.Router) {
	r.Route("/domain", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleAppDomainStatus))
		r.Method(http.MethodPost, "/certificate", s.handle(s.handleAppDomainCertificate))
		r.Method(http.MethodPut, "/settings", s.handle(s.handleAppDomainSettings))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireSession)
			r.With(httpx.RefuseHubSession).Method(http.MethodPut, "/", s.handle(s.handleAppDomainClaim))
			s.destructive(r, func(r chi.Router) {
				r.With(httpx.RefuseHubSession).Method(http.MethodDelete, "/", s.handle(s.handleAppDomainRelease))
			})
		})
	})
}

func (s *Server) appDomainService() (*appdomain.Service, error) {
	if s.modules.appDomain == nil {
		return nil, httpx.Err(http.StatusServiceUnavailable, "app_domains_unavailable", "just-dashboard.com addresses are not available in this process")
	}
	return s.modules.appDomain, nil
}

// handleAppDomainStatus is the Settings card. Opening it asks the site again,
// at most once a minute, so a suspension or a release done there shows.
func (s *Server) handleAppDomainStatus(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.appDomainService()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	svc.Refresh(ctx)
	view, err := svc.View(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

type appDomainClaimBody struct {
	Handle string `json:"handle"`
}

func (s *Server) handleAppDomainClaim(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.appDomainService()
	if err != nil {
		return err
	}
	var body appDomainClaimBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	handle := strings.ToLower(strings.TrimSpace(body.Handle))
	httpx.SetAudit(r, "app.domain.claim", handle, map[string]any{"address": svc.PublicAddress()})
	view, err := svc.Claim(r.Context(), handle, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapAppDomainError(err)
	}
	httpx.SetAudit(r, "app.domain.claim", view.Domain.Hostname, map[string]any{"address": view.Domain.Address})
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) handleAppDomainRelease(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.appDomainService()
	if err != nil {
		return err
	}
	before, err := svc.View(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	target := ""
	if before.Domain != nil {
		target = before.Domain.Hostname
	}
	httpx.SetAudit(r, "app.domain.release", target, nil)
	if err := svc.Release(r.Context()); err != nil {
		return mapAppDomainError(err)
	}
	httpx.NoContent(w)
	return nil
}

type appDomainCertificateBody struct {
	DryRun bool `json:"dryRun"`
	Force  bool `json:"force"`
}

func (s *Server) handleAppDomainCertificate(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.appDomainService()
	if err != nil {
		return err
	}
	var body appDomainCertificateBody
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(r, &body); err != nil {
			return err
		}
	}
	httpx.SetAudit(r, "app.domain.certificate.start", "", map[string]any{"dryRun": body.DryRun, "force": body.Force})
	view, err := svc.IssueCertificate(r.Context(), appdomain.IssueOptions{DryRun: body.DryRun, Force: body.Force}, httpx.MustPrincipal(r).Username())
	if err != nil {
		return mapAppDomainError(err)
	}
	if view.Domain != nil {
		httpx.SetAudit(r, "app.domain.certificate.start", view.Domain.Hostname, map[string]any{"dryRun": body.DryRun, "force": body.Force})
	}
	httpx.JSON(w, http.StatusAccepted, view)
	return nil
}

type appDomainSettingsBody struct {
	AutoAssign *bool `json:"autoAssign"`
}

func (s *Server) handleAppDomainSettings(w http.ResponseWriter, r *http.Request) error {
	svc, err := s.appDomainService()
	if err != nil {
		return err
	}
	var body appDomainSettingsBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if body.AutoAssign == nil {
		return httpx.BadRequest("autoAssign is required")
	}
	httpx.SetAudit(r, "app.domain.settings", "", map[string]any{"autoAssign": *body.AutoAssign})
	view, err := svc.SetAutoAssign(r.Context(), *body.AutoAssign)
	if err != nil {
		return mapAppDomainError(err)
	}
	httpx.JSON(w, http.StatusOK, view)
	return nil
}

// mapAppDomainError turns the service's and the site's refusals into the
// dashboard's own.
func mapAppDomainError(err error) error {
	var inUse *appdomain.InUseError
	var site *accountlink.DomainError
	switch {
	case errors.As(err, &inUse):
		return httpx.Err(http.StatusConflict, "domain_in_use", err.Error())
	case errors.Is(err, appdomain.ErrDisabled):
		return httpx.Err(http.StatusForbidden, "app_domains_disabled", err.Error())
	case errors.Is(err, accountlink.ErrDisabled):
		return httpx.Err(http.StatusForbidden, "account_link_disabled", err.Error())
	case errors.Is(err, accountlink.ErrNotLinked):
		return httpx.Err(http.StatusConflict, "not_linked", "this server is not linked to a just-dashboard.com account; link it first")
	case errors.Is(err, accountlink.ErrSiteChanged), errors.Is(err, accountlink.ErrIdle):
		return httpx.Err(http.StatusConflict, "link_idle", err.Error())
	case errors.Is(err, appdomain.ErrInvalidHandle):
		return httpx.Err(http.StatusBadRequest, "invalid_handle", err.Error())
	case errors.Is(err, appdomain.ErrNoAddress):
		return httpx.Err(http.StatusConflict, "no_public_address", err.Error())
	case errors.Is(err, appdomain.ErrNoDomain):
		return httpx.Err(http.StatusConflict, "no_domain", err.Error())
	case errors.As(err, &site):
		status := site.Status
		switch {
		case status == http.StatusTooManyRequests:
		case status >= 500 || status < 400:
			status = http.StatusBadGateway
		case status == http.StatusUnauthorized:
			// The site refusing this server's signature is not this
			// request's credential failing.
			status = http.StatusBadGateway
		}
		return httpx.Err(status, site.Code, site.Message)
	}
	var link *accountlink.SiteError
	if errors.As(err, &link) {
		return httpx.Err(http.StatusBadGateway, "site_unreachable", link.Error())
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return httpx.Err(http.StatusBadGateway, "site_unreachable", "the site could not be reached: "+err.Error())
	}
	return httpx.Internal(err)
}

// handleAppDomainNames places names for the deployment pages: the zone, and
// the base every app name ends in while this server holds a handle. Every
// account that reads deployments may ask; it names this server's own handle,
// which its deployments' addresses show anyway.
func (s *Server) handleAppDomainNames(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	if s.modules.appDomain == nil {
		httpx.JSON(w, http.StatusOK, appdomain.Names{})
		return nil
	}
	httpx.JSON(w, http.StatusOK, s.modules.appDomain.Names(r.Context()))
	return nil
}

// The certbot hook's route.
//
// `jd app-domain challenge add|remove` runs as root from certbot, with no
// session and no API key: certbot's renewal timer runs it unattended. It
// shows the token the backend wrote 0600 into its data directory
// (appdomain/hook.go) and reaches this process on its own loopback listener,
// as the tailnet gate's proxy does. A caller that is not on loopback, or that
// came through any proxy, is refused before the token is looked at. The
// token publishes or withdraws a challenge value for this server's own
// handle and does nothing else.
func (s *Server) mountAppDomainHook(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.apiLim.Middleware)
		r.Method(http.MethodPost, "/app-domain/hook/challenge", s.handle(s.handleAppDomainHook))
	})
}

type appDomainHookBody struct {
	Action string `json:"action"`
	Domain string `json:"domain"`
	Value  string `json:"value"`
}

func (s *Server) handleAppDomainHook(w http.ResponseWriter, r *http.Request) error {
	httpx.SkipAudit(r)
	if !directLoopback(r) {
		return httpx.Err(http.StatusForbidden, "loopback_only", "the certificate hook answers only on this server's loopback")
	}
	svc, err := s.appDomainService()
	if err != nil {
		return err
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !svc.CheckHookToken(r.Context(), token) {
		return httpx.Err(http.StatusUnauthorized, "unauthorized", "the hook's credential is not this server's; issue the certificate again from Settings → Connect")
	}
	var body appDomainHookBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	if !accountlink.ValidChallenge(body.Value) {
		return httpx.Err(http.StatusBadRequest, "invalid_challenge", "CERTBOT_VALIDATION is not a DNS challenge value")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	switch body.Action {
	case "add":
		err = svc.AddChallenge(ctx, body.Domain, body.Value)
	case "remove":
		err = svc.RemoveChallenge(ctx, body.Domain, body.Value)
	default:
		return httpx.BadRequest("action must be add or remove")
	}
	s.Audit.Record(context.WithoutCancel(r.Context()), audit.Entry{
		Username: "certbot", Actor: "certbot", Action: "app.domain.challenge." + body.Action, Target: body.Domain,
		Method: r.Method, Path: r.URL.Path, Status: statusFor(err), Success: err == nil,
		Detail: audit.Detail(errorDetailMap(err)),
	})
	if err != nil {
		if errors.Is(err, appdomain.ErrNotThisDomain) {
			return httpx.Err(http.StatusForbidden, "not_this_domain", err.Error())
		}
		var site *accountlink.DomainError
		if errors.As(err, &site) || errors.Is(err, appdomain.ErrNoDomain) || errors.Is(err, appdomain.ErrDisabled) || errors.Is(err, accountlink.ErrNotLinked) {
			return mapAppDomainError(err)
		}
		return httpx.Err(http.StatusBadGateway, "challenge_failed", err.Error())
	}
	httpx.NoContent(w)
	return nil
}

func statusFor(err error) int {
	if err != nil {
		return http.StatusBadGateway
	}
	return http.StatusNoContent
}

func errorDetailMap(err error) map[string]any {
	if err == nil {
		return nil
	}
	return map[string]any{"error": err.Error()}
}

// directLoopback is a request whose connection comes from this host's
// loopback and that no proxy forwarded.
func directLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		if r.Header.Get(header) != "" {
			return false
		}
	}
	return true
}
