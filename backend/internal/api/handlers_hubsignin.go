package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink"
	"github.com/go-chi/chi/v5"
)

// Hub sign-in: a person opens this server from the Just Hub it is linked to
// and lands on it signed in. The contract is hub-link-protocol.md section 9;
// it is the documented exception to invariant 2 (docs/internal/security/
// invariants.md), off unless the operator sets JD_HUB_SIGNIN=true and each
// account opts in for itself.

// maxHubTicketBody bounds POST /auth/hub/enter: a ticket is at most 2048
// characters, and nothing else is sent.
const maxHubTicketBody = 4 << 10

// hubEnterAnswer is POST /auth/login's answer for a complete session, plus
// where to go.
type hubEnterAnswer struct {
	authStatus
	Next string `json:"next"`
}

// mountHubSignIn is the redeem route. It sits with POST /auth/login: the same
// budget per address, CSRF header, and its own audit entry. It needs no
// session: the ticket is the credential.
func (s *Server) mountHubSignIn(r chi.Router) {
	if s.modules.hubLink == nil {
		return
	}
	r.Method(http.MethodPost, "/auth/hub/enter", s.handle(s.handleHubEnter))
}

func (s *Server) handleHubEnter(w http.ResponseWriter, r *http.Request) error {
	// hub.signin is written by the redeem itself, with what each step found.
	httpx.SkipAudit(r)
	var body struct {
		Ticket string `json:"ticket"`
		// Replace is sent by /hub/enter only after the person chose to end
		// another account's session in this browser (section 9.5 step 2).
		Replace bool `json:"replace"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxHubTicketBody)
	if err := httpx.DecodeJSON(r, &body); err != nil {
		// A body that does not decode reaches step 3 with no ticket, after the
		// switches and the link have been asked, as section 9.7 orders it.
		body.Ticket = ""
	}
	res, refused := s.modules.hubLink.Redeem(r.Context(), hublink.RedeemRequest{
		Ticket: body.Ticket, Client: httpx.ClientIP(r), UserAgent: r.UserAgent(),
		Cookie: s.Authn.SessionToken(r), Replace: body.Replace, Path: r.URL.Path,
	})
	if refused != nil {
		if refused.Status >= http.StatusInternalServerError {
			return httpx.Internal(errors.New(refused.Message))
		}
		return httpx.Err(refused.Status, refused.Code, refused.Message)
	}
	s.Authn.SetSessionCookie(w, res.Token, int(time.Until(res.ExpiresAt).Seconds()))
	httpx.JSON(w, http.StatusOK, hubEnterAnswer{
		authStatus: authStatus{
			Authenticated: true,
			User:          res.User,
			Capabilities:  res.User.Role.Capabilities(),
			Require2FA:    s.Auth.Require2FA(),
			Hub:           s.modules.hubLink.Session(r.Context()),
		},
		Next: res.Next,
	})
	return nil
}

// mountHubSignInGrant is the caller's own grant, under /account (session
// only). Making one leaves something behind that outlives the session, so a
// hub session may not; removing one only takes power away, so it may.
func (s *Server) mountHubSignInGrant(r chi.Router) {
	if s.modules.hubLink == nil {
		return
	}
	r.With(httpx.RefuseHubSession).Method(http.MethodPut, "/hub-signin", s.handle(s.handleHubSignInAllow))
	r.Method(http.MethodDelete, "/hub-signin", s.handle(s.handleHubSignInRevoke))
}

func hubSignInError(err error) error {
	switch {
	case errors.Is(err, hublink.ErrSignInDisabled), errors.Is(err, hublink.ErrDisabled):
		return httpx.Err(http.StatusForbidden, "hub_signin_disabled",
			"signing in through Just Hub is switched off on this server (JD_HUB_SIGNIN)")
	case errors.Is(err, hublink.ErrNotLinked):
		return httpx.Err(http.StatusConflict, "not_linked", "this server is not connected to a hub it can use")
	case errors.Is(err, hublink.ErrSignInNotOffered):
		return httpx.Err(http.StatusConflict, "hub_signin_not_offered", err.Error())
	case errors.Is(err, hublink.ErrHubUserNotOffered):
		return httpx.BadRequest("the hub does not offer that hub user")
	case errors.Is(err, hublink.ErrHubUserTaken):
		return httpx.Err(http.StatusConflict, "hub_user_taken", err.Error())
	}
	return httpx.Internal(err)
}

func (s *Server) handleHubSignInAllow(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		HubUser  string `json:"hubUser"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	hub := s.modules.hubLink
	body.HubUser = strings.TrimSpace(body.HubUser)
	rejected := func(err error) error {
		httpx.SetAudit(r, "hub.signin.allow", body.HubUser, map[string]any{"result": "rejected"})
		httpx.AuditFailed(r)
		return err
	}
	// What can be refused without a guess is refused first.
	if err := hub.CanAllow(r.Context(), body.HubUser); err != nil {
		return rejected(hubSignInError(err))
	}
	if p.User.TOTPEnabled && strings.TrimSpace(body.Code) == "" {
		return rejected(httpx.BadRequest("enter the code from your authenticator app"))
	}
	// The password and code are guessed against the account's own budget, as
	// registering a passkey's are, and a right code is used up.
	if err := s.spendGuess(r); err != nil {
		return rejected(err)
	}
	if !s.Auth.VerifyUserPassword(r.Context(), p.UserID(), body.Password) {
		return rejected(httpx.Err(http.StatusUnauthorized, "invalid_credentials", "that password is incorrect"))
	}
	if err := s.Auth.VerifyReauthentication(r.Context(), p.UserID(), body.Password, body.Code); err != nil {
		if errors.Is(err, auth.ErrSecondFactorLocked) {
			return rejected(httpx.Err(http.StatusTooManyRequests, "second_factor_locked", err.Error()))
		}
		if errors.Is(err, auth.ErrReauthentication) {
			return rejected(httpx.Err(http.StatusUnauthorized, "invalid_code", "that code is incorrect"))
		}
		return httpx.Internal(err)
	}
	if err := hub.Allow(r.Context(), hublink.AllowRequest{UserID: p.UserID(), HubUser: body.HubUser}); err != nil {
		return rejected(hubSignInError(err))
	}
	// hub.signin.allow is written by Allow.
	httpx.SkipAudit(r)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleHubSignInRevoke(w http.ResponseWriter, r *http.Request) error {
	err := s.modules.hubLink.Revoke(r.Context(), httpx.MustPrincipal(r).UserID(), "")
	if err != nil && !errors.Is(err, hublink.ErrNoGrant) {
		return httpx.Internal(err)
	}
	// hub.signin.revoke is written by Revoke, when there was a grant.
	httpx.SkipAudit(r)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleHubSignInRemove(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "userId"), 10, 64)
	if err != nil || id < 1 {
		return httpx.BadRequest("userId must be an account id")
	}
	err = s.modules.hubLink.Revoke(r.Context(), id, httpx.MustPrincipal(r).Username())
	if errors.Is(err, hublink.ErrNoGrant) {
		return httpx.Err(http.StatusNotFound, "not_found", "that account has not let Just Hub sign it in")
	}
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.SkipAudit(r)
	httpx.NoContent(w)
	return nil
}
