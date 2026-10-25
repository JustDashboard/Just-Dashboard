package api

import (
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Linking this dashboard to a just-dashboard.com account.
//
// Mounted inside the admin-only /dashboard group. Beginning, cancelling,
// re-scoping and ending a link are also session-only: an API key must not be
// able to hand this server to a site account, or take it back. The details of
// the protocol, the trust model and what each scope shares are in
// docs/internal/backend/account-link.md.
func (s *Server) mountAccountLinkRoutes(r chi.Router) {
	r.Route("/link", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleAccountLinkStatus))
		// A heartbeat now changes nothing on this machine and nothing about who
		// the link belongs to, so a key may ask for one.
		r.Method(http.MethodPost, "/sync", s.handle(s.handleAccountLinkSync))

		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireSession)
			r.Method(http.MethodPost, "/", s.handle(s.handleAccountLinkBegin))
			r.Method(http.MethodDelete, "/request", s.handle(s.handleAccountLinkCancel))
			r.Method(http.MethodPut, "/scope", s.handle(s.handleAccountLinkScope))
			// Ending a link stops everything the site could do, which is a
			// service interruption of a kind: it is in the destructive budget.
			s.destructive(r, func(r chi.Router) {
				r.Method(http.MethodDelete, "/", s.handle(s.handleAccountLinkUnlink))
			})
		})
	})
}

func (s *Server) handleAccountLinkStatus(w http.ResponseWriter, r *http.Request) error {
	st, err := s.modules.accountLink.Status(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}

type accountLinkScopeBody struct {
	Scope string `json:"scope"`
}

func readAccountLinkScope(r *http.Request) (accountlink.Scope, error) {
	var body accountLinkScopeBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return "", err
	}
	scope, ok := accountlink.ParseScope(body.Scope)
	if !ok {
		return "", httpx.BadRequest("scope must be observe, operate or maintain")
	}
	return scope, nil
}

// mapAccountLinkError turns the connector's errors into the dashboard's own.
func mapAccountLinkError(err error) error {
	var site *accountlink.SiteError
	switch {
	case errors.Is(err, accountlink.ErrDisabled):
		return httpx.Err(http.StatusForbidden, "account_link_disabled", "linking to a just-dashboard.com account is switched off on this server (JD_ACCOUNT_LINK=false)")
	case errors.Is(err, accountlink.ErrAlreadyLinked):
		return httpx.Err(http.StatusConflict, "already_linked", "this server is already linked to a just-dashboard.com account; unlink it first")
	case errors.Is(err, accountlink.ErrLinkPending):
		return httpx.Err(http.StatusConflict, "link_pending", "a link request is already waiting for approval; cancel it or approve it")
	case errors.Is(err, accountlink.ErrNotLinked):
		return httpx.Err(http.StatusConflict, "not_linked", "this server is not linked to a just-dashboard.com account")
	case errors.Is(err, accountlink.ErrSiteChanged):
		return httpx.Err(http.StatusConflict, "site_changed", err.Error())
	case errors.Is(err, accountlink.ErrBadScope):
		return httpx.BadRequest("scope must be observe, operate or maintain")
	case errors.As(err, &site):
		return httpx.Err(http.StatusBadGateway, "site_unreachable", site.Error())
	}
	return httpx.Internal(err)
}

func (s *Server) handleAccountLinkBegin(w http.ResponseWriter, r *http.Request) error {
	scope, err := readAccountLinkScope(r)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "account.link.begin", string(scope), map[string]any{"site": s.modules.accountLink.SiteURL()})
	if _, err := s.modules.accountLink.BeginLink(r.Context(), scope, httpx.MustPrincipal(r).Username()); err != nil {
		return mapAccountLinkError(err)
	}
	return s.writeAccountLinkStatus(w, r)
}

func (s *Server) handleAccountLinkCancel(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "account.link.cancel", "", nil)
	if err := s.modules.accountLink.CancelLink(r.Context()); err != nil {
		return httpx.Internal(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleAccountLinkScope(w http.ResponseWriter, r *http.Request) error {
	scope, err := readAccountLinkScope(r)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "account.link.scope", string(scope), nil)
	if err := s.modules.accountLink.SetScope(r.Context(), scope); err != nil {
		return mapAccountLinkError(err)
	}
	return s.writeAccountLinkStatus(w, r)
}

func (s *Server) handleAccountLinkUnlink(w http.ResponseWriter, r *http.Request) error {
	before, err := s.modules.accountLink.Status(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	owner := ""
	if before.Link != nil {
		owner = before.Link.Owner
	}
	httpx.SetAudit(r, "account.unlink", owner, nil)
	notified, err := s.modules.accountLink.Unlink(r.Context())
	if err != nil {
		return mapAccountLinkError(err)
	}
	httpx.SetAudit(r, "account.unlink", owner, map[string]any{"siteNotified": notified})
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleAccountLinkSync(w http.ResponseWriter, r *http.Request) error {
	if err := s.modules.accountLink.SyncNow(r.Context()); err != nil {
		return mapAccountLinkError(err)
	}
	return s.writeAccountLinkStatus(w, r)
}

func (s *Server) writeAccountLinkStatus(w http.ResponseWriter, r *http.Request) error {
	st, err := s.modules.accountLink.Status(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}
