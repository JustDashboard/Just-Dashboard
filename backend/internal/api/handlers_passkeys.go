package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Passkeys (auth/passkeys.go) over HTTP. Signing in with one sits beside the
// password sign-in, behind the same allowlist, CSRF header and audit, and its
// finish spends the password sign-in's per-address budget; beginning one only
// hands out a challenge, so it has a budget of its own that a sign-in page's
// autofill does not exhaust. Registering, renaming and removing one are the
// account holder's, from a complete session: registering also takes the
// password and, where one is enrolled, the authenticator's code again.

func (s *Server) mountPasskeySignIn(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(s.passkeyLim.Middleware)
		r.Use(httpx.AuditMutations(s.Audit))
		r.Use(httpx.RequireCSRF)
		r.Method(http.MethodPost, "/auth/passkey/begin", s.handle(s.handlePasskeyLoginBegin))
	})
}

func (s *Server) mountPasskeySignInFinish(r chi.Router) {
	r.Method(http.MethodPost, "/auth/passkey/finish", s.handle(s.handlePasskeyLoginFinish))
}

func (s *Server) mountAccountPasskeys(r chi.Router) {
	r.Method(http.MethodGet, "/passkeys", s.handle(s.handlePasskeyList))
	r.Method(http.MethodPost, "/passkeys/register/begin", s.handle(s.handlePasskeyRegisterBegin))
	r.Method(http.MethodPost, "/passkeys/register/finish", s.handle(s.handlePasskeyRegisterFinish))
	r.Method(http.MethodPatch, "/passkeys/{id}", s.handle(s.handlePasskeyRename))
	r.Method(http.MethodDelete, "/passkeys/{id}", s.handle(s.handlePasskeyRemove))
}

// passkeyParty is the relying party a request's ceremony is for: the
// dashboard's name as the browser reached it.
func (s *Server) passkeyParty(r *http.Request) (auth.RelyingParty, error) {
	rp, err := auth.PasskeyParty(r.Header.Get("Origin"), r.Host, s.Cfg.AllowedOrigins)
	if err != nil {
		return rp, httpx.Err(http.StatusBadRequest, "passkey_unavailable", err.Error())
	}
	return rp, nil
}

func mapPasskeyError(err error) error {
	switch {
	case errors.Is(err, auth.ErrPasskeyChallenge):
		return httpx.Err(http.StatusBadRequest, "passkey_expired", err.Error())
	case errors.Is(err, auth.ErrPasskeyUnknown):
		return httpx.Err(http.StatusUnauthorized, "passkey_unknown", err.Error())
	case errors.Is(err, auth.ErrPasskeyCloned):
		return httpx.Err(http.StatusUnauthorized, "passkey_cloned", err.Error())
	case errors.Is(err, auth.ErrPasskeyRejected):
		return httpx.Err(http.StatusBadRequest, "passkey_rejected", err.Error())
	case errors.Is(err, auth.ErrAccountDisabled):
		return httpx.Err(http.StatusForbidden, "account_disabled", err.Error())
	case errors.Is(err, auth.ErrNotFound):
		return httpx.ErrNotFound
	default:
		return httpx.Internal(err)
	}
}

type passkeyBegin struct {
	ChallengeID string `json:"challengeId"`
	PublicKey   any    `json:"publicKey"`
}

func (s *Server) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) error {
	rp, err := s.passkeyParty(r)
	if err != nil {
		return err
	}
	assertion, challenge, err := s.Auth.BeginPasskeyLogin(r.Context(), rp)
	if err != nil {
		return mapPasskeyError(err)
	}
	// A challenge is not an attempt; the finish is what is recorded.
	httpx.SkipAudit(r)
	httpx.JSON(w, http.StatusOK, passkeyBegin{ChallengeID: challenge, PublicKey: assertion.Response})
	return nil
}

type passkeyFinishRequest struct {
	ChallengeID string          `json:"challengeId"`
	Credential  json.RawMessage `json:"credential"`
	Name        string          `json:"name,omitempty"`
}

func (s *Server) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) error {
	var req passkeyFinishRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	rp, err := s.passkeyParty(r)
	if err != nil {
		return err
	}
	res, key, err := s.Auth.FinishPasskeyLogin(r.Context(), req.ChallengeID, rp, req.Credential, httpx.ClientIP(r), r.UserAgent())
	if err != nil {
		// Recorded as the password sign-in records a refusal: anonymous,
		// with the reason, since nobody is signed in yet.
		s.Audit.Record(r.Context(), audit.Entry{
			IP: httpx.ClientIP(r), Actor: "anonymous", Action: "auth.passkey.login",
			Method: r.Method, Path: r.URL.Path, Status: http.StatusUnauthorized, Success: false, Detail: err.Error(),
		})
		httpx.SkipAudit(r)
		return mapPasskeyError(err)
	}
	s.Authn.SetSessionCookie(w, res.Token, int(time.Until(res.ExpiresAt).Seconds()))
	httpx.SetAudit(r, "auth.passkey.login", res.User.Username, map[string]any{
		"passkey": key.Name, "rpId": rp.ID, "needsTotp": res.NeedsTOTP, "needsEnrollment": res.NeedsEnroll,
	})
	httpx.JSON(w, http.StatusOK, authStatus{
		Authenticated:       !res.NeedsTOTP && !res.NeedsEnroll && !res.User.MustChangePW,
		NeedsPasswordChange: !res.NeedsTOTP && !res.NeedsEnroll && res.User.MustChangePW,
		User:                res.User,
		Capabilities:        res.User.Role.Capabilities(),
		NeedsTOTP:           res.NeedsTOTP,
		NeedsEnroll:         res.NeedsEnroll,
		Require2FA:          s.Auth.Require2FA(),
	})
	return nil
}

func (s *Server) handlePasskeyList(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	keys, err := s.Auth.ListPasskeys(r.Context(), p.UserID())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, keys)
	return nil
}

type passkeyReauthRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (s *Server) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) error {
	var req passkeyReauthRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	rp, err := s.passkeyParty(r)
	if err != nil {
		return err
	}
	// The password and code are guessed against the second factor's own
	// per-account budget, so this form is no way around it.
	if err := s.spendGuess(r); err != nil {
		return err
	}
	if err := s.Auth.VerifyReauthentication(r.Context(), p.UserID(), req.Password, req.Code); err != nil {
		httpx.SetAudit(r, "auth.passkey.reauth", p.Username(), map[string]any{"result": "rejected"})
		httpx.AuditFailed(r)
		if errors.Is(err, auth.ErrSecondFactorLocked) {
			return httpx.Err(http.StatusTooManyRequests, "second_factor_locked", err.Error())
		}
		if errors.Is(err, auth.ErrReauthentication) {
			return httpx.Err(http.StatusUnauthorized, "invalid_credentials", err.Error())
		}
		return httpx.Internal(err)
	}
	creation, challenge, err := s.Auth.BeginPasskeyRegistration(r.Context(), p.UserID(), p.SessionID, rp)
	if err != nil {
		return mapPasskeyError(err)
	}
	httpx.SetAudit(r, "auth.passkey.reauth", p.Username(), map[string]any{"rpId": rp.ID})
	httpx.JSON(w, http.StatusOK, passkeyBegin{ChallengeID: challenge, PublicKey: creation.Response})
	return nil
}

func (s *Server) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) error {
	var req passkeyFinishRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	rp, err := s.passkeyParty(r)
	if err != nil {
		return err
	}
	key, err := s.Auth.FinishPasskeyRegistration(r.Context(), p.UserID(), p.SessionID, req.ChallengeID, req.Name, rp, req.Credential)
	if err != nil {
		httpx.SetAudit(r, "auth.passkey.register", p.Username(), map[string]any{"rpId": rp.ID, "reason": err.Error()})
		return mapPasskeyError(err)
	}
	httpx.SetAudit(r, "auth.passkey.register", p.Username(), map[string]any{"passkey": key.Name, "rpId": key.RPID, "synced": key.Synced})
	httpx.JSON(w, http.StatusCreated, key)
	return nil
}

func passkeyParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.BadRequest("invalid passkey id")
	}
	return id, nil
}

func (s *Server) handlePasskeyRename(w http.ResponseWriter, r *http.Request) error {
	id, err := passkeyParam(r)
	if err != nil {
		return err
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	key, err := s.Auth.RenamePasskey(r.Context(), p.UserID(), id, req.Name)
	if err != nil {
		return mapPasskeyError(err)
	}
	httpx.SetAudit(r, "auth.passkey.rename", p.Username(), map[string]any{"passkey": key.Name, "id": key.ID})
	httpx.JSON(w, http.StatusOK, key)
	return nil
}

func (s *Server) handlePasskeyRemove(w http.ResponseWriter, r *http.Request) error {
	id, err := passkeyParam(r)
	if err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	key, err := s.Auth.RemovePasskey(r.Context(), p.UserID(), id)
	if err != nil {
		return mapPasskeyError(err)
	}
	httpx.SetAudit(r, "auth.passkey.remove", p.Username(), map[string]any{"passkey": key.Name, "rpId": key.RPID})
	httpx.NoContent(w)
	return nil
}
