package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authStatus struct {
	NeedsPasswordChange bool              `json:"needsPasswordChange"`
	Authenticated       bool              `json:"authenticated"`
	User                *auth.User        `json:"user,omitempty"`
	Capabilities        []auth.Capability `json:"capabilities,omitempty"`
	NeedsTOTP           bool              `json:"needsTotp"`
	NeedsEnroll         bool              `json:"needsEnrollment"`
	Require2FA          bool              `json:"require2fa"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) error {
	var req loginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	res, err := s.Auth.Login(r.Context(), req.Username, req.Password, httpx.ClientIP(r), r.UserAgent())
	if err != nil {
		s.Audit.Record(r.Context(), audit.Entry{
			Username: req.Username, IP: httpx.ClientIP(r), Actor: "anonymous",
			Action: "auth.login", Method: r.Method, Path: r.URL.Path,
			Status: http.StatusUnauthorized, Success: false, Detail: err.Error(),
		})
		httpx.SkipAudit(r)
		switch {
		case errors.Is(err, auth.ErrAccountLocked):
			return httpx.Err(http.StatusTooManyRequests, "account_locked", err.Error())
		case errors.Is(err, auth.ErrAccountDisabled):
			return httpx.Err(http.StatusForbidden, "account_disabled", err.Error())
		case errors.Is(err, auth.ErrInvalidCredentials):
			return httpx.Err(http.StatusUnauthorized, "invalid_credentials", err.Error())
		default:
			return httpx.Internal(err)
		}
	}
	s.Authn.SetSessionCookie(w, res.Token, int(time.Until(res.ExpiresAt).Seconds()))
	httpx.SetAudit(r, "auth.login", res.User.Username, map[string]any{
		"needsTotp": res.NeedsTOTP, "needsEnrollment": res.NeedsEnroll,
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

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	if p.SessionID != "" {
		if err := s.Auth.RevokeSession(r.Context(), p.SessionID); err != nil {
			return httpx.Internal(err)
		}
	}
	s.Authn.ClearSessionCookie(w)
	httpx.SetAudit(r, "auth.logout", p.Username(), nil)
	httpx.NoContent(w)
	return nil
}

// handleSession answers "who am I" for the frontend shell. It reports the
// pending second-factor state rather than 401-ing, so the login screen knows
// which step to render.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) error {
	p, ok := httpx.PrincipalFrom(r.Context())
	if !ok || p.User == nil {
		httpx.JSON(w, http.StatusOK, authStatus{Require2FA: s.Auth.Require2FA()})
		return nil
	}
	sessions, err := s.Auth.ListSessions(r.Context(), p.UserID(), p.SessionID)
	if err != nil {
		return httpx.Internal(err)
	}
	var current *auth.Session
	for _, sess := range sessions {
		if sess.Current {
			current = sess
			break
		}
	}
	st := authStatus{
		User:         p.User,
		Capabilities: p.Role.Capabilities(),
		Require2FA:   s.Auth.Require2FA(),
	}
	switch {
	case current != nil && !current.TwoFAPassed && p.User.TOTPEnabled:
		st.NeedsTOTP = true
	case current != nil && !current.TwoFAPassed && s.Auth.Require2FA():
		st.NeedsEnroll = true
	case p.User.MustChangePW:
		st.NeedsPasswordChange = true
	default:
		st.Authenticated = true
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	if err := s.limitSecondFactor(r); err != nil {
		return err
	}
	if p.User.TOTPEnabled {
		return httpx.Err(http.StatusConflict, "totp_already_enabled",
			"two-factor is already enrolled; an admin must reset it first")
	}
	enroll, err := s.Auth.BeginTOTPEnrollment(r.Context(), p.UserID())
	if err != nil {
		if errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
			return httpx.Err(http.StatusConflict, "totp_already_enabled", err.Error())
		}
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "auth.2fa.setup", p.Username(), nil)
	httpx.JSON(w, http.StatusOK, enroll)
	return nil
}

type codeRequest struct {
	Code string `json:"code"`
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) error {
	var req codeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	if err := s.limitSecondFactor(r); err != nil {
		return err
	}
	codes, err := s.Auth.ConfirmTOTPEnrollmentForSession(r.Context(), p.UserID(), p.SessionID, req.Code)
	if err != nil {
		if errors.Is(err, auth.ErrTOTPAlreadyEnabled) {
			return httpx.Err(http.StatusConflict, "totp_already_enabled", err.Error())
		}
		if errors.Is(err, auth.ErrInvalidTOTP) {
			httpx.SetAudit(r, "auth.2fa.enable", p.Username(), map[string]any{"result": "rejected"})
			return httpx.Err(http.StatusUnauthorized, "invalid_code", err.Error())
		}
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "auth.2fa.enable", p.Username(), nil)
	httpx.JSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
	return nil
}

func (s *Server) handleTOTPVerify(w http.ResponseWriter, r *http.Request) error {
	var req codeRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	if err := s.limitSecondFactor(r); err != nil {
		return err
	}
	if err := s.Auth.VerifySecondFactor(r.Context(), p.SessionID, p.UserID(), req.Code); err != nil {
		httpx.SetAudit(r, "auth.2fa.verify", p.Username(), map[string]any{"result": "rejected"})
		return httpx.Err(http.StatusUnauthorized, "invalid_code", "invalid verification code")
	}
	httpx.SetAudit(r, "auth.2fa.verify", p.Username(), nil)
	httpx.JSON(w, http.StatusOK, authStatus{
		Authenticated:       !p.User.MustChangePW,
		NeedsPasswordChange: p.User.MustChangePW,
		User:                p.User,
		Capabilities:        p.Role.Capabilities(),
		Require2FA:          s.Auth.Require2FA(),
	})
	return nil
}

// Enrollment and verification share an account budget; changing routes or
// source addresses must not multiply the number of guesses.
func (s *Server) limitSecondFactor(r *http.Request) error {
	p := httpx.MustPrincipal(r)
	if p.Elevated && p.User.MustChangePW {
		return httpx.Err(http.StatusForbidden, "password_change_required", "change your temporary password before continuing")
	}
	key := "totp|" + strconv.FormatInt(p.UserID(), 10)
	if !s.loginLim.Allow(key) {
		return httpx.Err(http.StatusTooManyRequests, "rate_limited", "too many verification attempts")
	}
	return nil
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) error {
	var req changePasswordRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	if !s.Auth.VerifyUserPassword(r.Context(), p.UserID(), req.CurrentPassword) {
		httpx.SetAudit(r, "auth.password.change", p.Username(), map[string]any{"result": "rejected"})
		return httpx.Err(http.StatusUnauthorized, "invalid_credentials", "current password is incorrect")
	}
	if req.NewPassword == req.CurrentPassword {
		return httpx.BadRequest("choose a different password")
	}
	if err := s.Auth.SetPassword(r.Context(), p.UserID(), req.NewPassword); err != nil {
		return httpx.BadRequest("%v", err)
	}
	// Every other session for this account is dropped: a password change is
	// the operator's lever for kicking out a suspected intruder.
	if err := s.Auth.RevokeAllSessions(r.Context(), p.UserID()); err != nil {
		return httpx.Internal(err)
	}
	s.Authn.ClearSessionCookie(w)
	httpx.SetAudit(r, "auth.password.change", p.Username(), nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleRecoveryCodesRegen(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	codes, err := s.Auth.RegenerateRecoveryCodes(r.Context(), p.UserID())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "auth.2fa.recovery.regenerate", p.Username(), nil)
	httpx.JSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
	return nil
}

// handleDisableTOTP turns the caller's own authenticator off.
//
// The current password is required, and that is the whole security argument
// for this route: a session left open on an unlocked laptop is exactly the
// threat two-factor exists to answer, so removing it has to cost something
// only the account holder has.
func (s *Server) handleDisableTOTP(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	if !s.Auth.VerifyUserPassword(r.Context(), p.UserID(), req.Password) {
		httpx.SetAudit(r, "auth.2fa.disable", p.Username(), map[string]any{"result": "rejected"})
		return httpx.Err(http.StatusUnauthorized, "invalid_credentials", "that password is incorrect")
	}
	if err := s.Auth.DisableTOTP(r.Context(), p.UserID()); err != nil {
		if errors.Is(err, auth.ErrTOTPRequired) {
			return httpx.Err(http.StatusConflict, "totp_required", err.Error())
		}
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "auth.2fa.disable", p.Username(), nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleListOwnSessions(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	sessions, err := s.Auth.ListSessions(r.Context(), p.UserID(), p.SessionID)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, sessions)
	return nil
}

func (s *Server) handleRevokeOwnSession(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	id := chi.URLParam(r, "id")
	sessions, err := s.Auth.ListSessions(r.Context(), p.UserID(), p.SessionID)
	if err != nil {
		return httpx.Internal(err)
	}
	owned := false
	for _, sess := range sessions {
		if sess.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		return httpx.ErrNotFound
	}
	if err := s.Auth.RevokeSession(r.Context(), id); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "auth.session.revoke", id, nil)
	httpx.NoContent(w)
	return nil
}

// --- dashboard account administration ---

type createUserRequest struct {
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Password    string    `json:"password"`
	Role        auth.Role `json:"role"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := s.Auth.ListUsers(r.Context())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, users)
	return nil
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) error {
	var req createUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	u, err := s.Auth.CreateUser(r.Context(), req.Username, req.Password, req.Role, true)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	if req.DisplayName != "" {
		if err := s.Auth.SetProfile(r.Context(), u.ID, auth.Profile{DisplayName: &req.DisplayName}); err != nil {
			return httpx.BadRequest("%v", err)
		}
		if u, err = s.Auth.UserByID(r.Context(), u.ID); err != nil {
			return httpx.Internal(err)
		}
	}
	httpx.SetAudit(r, "dashboard.user.create", u.Username, map[string]any{"role": u.Role})
	httpx.JSON(w, http.StatusCreated, u)
	return nil
}

type updateUserRequest struct {
	Role        *auth.Role `json:"role,omitempty"`
	Disabled    *bool      `json:"disabled,omitempty"`
	Password    *string    `json:"password,omitempty"`
	Username    *string    `json:"username,omitempty"`
	DisplayName *string    `json:"displayName,omitempty"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid user id")
	}
	var req updateUserRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	target, err := s.Auth.UserByID(r.Context(), id)
	if err != nil {
		return httpx.ErrNotFound
	}
	if req.Username != nil || req.DisplayName != nil {
		if err := s.Auth.SetProfile(r.Context(), id, auth.Profile{
			Username: req.Username, DisplayName: req.DisplayName,
		}); err != nil {
			return httpx.BadRequest("%v", err)
		}
	}
	if req.Role != nil {
		if err := s.Auth.SetRole(r.Context(), id, *req.Role); err != nil {
			return mapAuthError(err)
		}
	}
	if req.Disabled != nil {
		if err := s.Auth.SetDisabled(r.Context(), id, *req.Disabled); err != nil {
			return mapAuthError(err)
		}
	}
	if req.Password != nil {
		if err := s.Auth.SetPassword(r.Context(), id, *req.Password); err != nil {
			return httpx.BadRequest("%v", err)
		}
		if err := s.Auth.RevokeAllSessions(r.Context(), id); err != nil {
			return httpx.Internal(err)
		}
	}
	updated, err := s.Auth.UserByID(r.Context(), id)
	if err != nil {
		return httpx.Internal(err)
	}
	// Deliberately not the request struct: it carries the new password, and
	// audit_log is the one table in JD_DATA_DIR that auth.Sealer does not
	// encrypt. Every system.admin can read it back through GET /audit.
	detail := map[string]any{"passwordChanged": req.Password != nil}
	if req.Role != nil {
		detail["role"] = *req.Role
	}
	if req.Disabled != nil {
		detail["disabled"] = *req.Disabled
	}
	if req.Username != nil && updated.Username != target.Username {
		detail["renamedTo"] = updated.Username
	}
	if req.DisplayName != nil {
		detail["displayName"] = updated.DisplayName
	}
	httpx.SetAudit(r, "dashboard.user.update", target.Username, detail)
	httpx.JSON(w, http.StatusOK, updated)
	return nil
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid user id")
	}
	target, err := s.Auth.UserByID(r.Context(), id)
	if err != nil {
		return httpx.ErrNotFound
	}
	if err := httpx.RequireTypedConfirmation(w, r, target.Username); err != nil {
		return err
	}
	if err := s.Auth.DeleteUser(r.Context(), id); err != nil {
		return mapAuthError(err)
	}
	httpx.SetAudit(r, "dashboard.user.delete", target.Username, nil)
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleResetUserTOTP(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid user id")
	}
	target, err := s.Auth.UserByID(r.Context(), id)
	if err != nil {
		return httpx.ErrNotFound
	}
	if err := s.Auth.ResetTOTP(r.Context(), id); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "dashboard.user.totp.reset", target.Username, nil)
	httpx.NoContent(w)
	return nil
}

func mapAuthError(err error) error {
	switch {
	case errors.Is(err, auth.ErrLastAdmin):
		return httpx.Err(http.StatusConflict, "last_admin", err.Error())
	case errors.Is(err, auth.ErrNotFound):
		return httpx.ErrNotFound
	default:
		return httpx.BadRequest("%v", err)
	}
}

// --- API tokens ---

type createTokenRequest struct {
	Name    string    `json:"name"`
	Role    auth.Role `json:"role"`
	TTLDays int       `json:"ttlDays"`
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	tokens, err := s.Auth.ListAPITokens(r.Context(), p.UserID(), p.Can(auth.CapSystemAdmin))
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, tokens)
	return nil
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) error {
	var req createTokenRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}
	p := httpx.MustPrincipal(r)
	var ttl time.Duration
	if req.TTLDays > 0 {
		ttl = time.Duration(req.TTLDays) * 24 * time.Hour
	}
	tok, secret, err := s.Auth.CreateAPIToken(r.Context(), p.User, req.Name, req.Role, ttl)
	if err != nil {
		return httpx.BadRequest("%v", err)
	}
	httpx.SetAudit(r, "dashboard.token.create", tok.Name, map[string]any{"role": tok.Role, "ttlDays": req.TTLDays})
	// The secret is returned exactly once; only its hash is retained.
	httpx.JSON(w, http.StatusCreated, map[string]any{"token": tok, "secret": secret})
	return nil
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return httpx.BadRequest("invalid token id")
	}
	p := httpx.MustPrincipal(r)
	tokens, err := s.Auth.ListAPITokens(r.Context(), p.UserID(), p.Can(auth.CapSystemAdmin))
	if err != nil {
		return httpx.Internal(err)
	}
	var found *auth.APIToken
	for _, t := range tokens {
		if t.ID == id {
			found = t
			break
		}
	}
	if found == nil {
		return httpx.ErrNotFound
	}
	if err := s.Auth.RevokeAPIToken(r.Context(), id); err != nil {
		return httpx.Internal(err)
	}
	httpx.SetAudit(r, "dashboard.token.revoke", found.Name, nil)
	httpx.NoContent(w)
	return nil
}

// --- audit trail ---

func (s *Server) handleAuditList(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	f := audit.Filter{
		Username: q.Get("username"),
		Action:   q.Get("action"),
		OnlyFail: q.Get("failed") == "true",
		Limit:    atoiDefault(q.Get("limit"), 100),
		Offset:   atoiDefault(q.Get("offset"), 0),
	}
	if v := q.Get("since"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.Since = t
		}
	}
	if v := q.Get("until"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			f.Until = t
		}
	}
	entries, total, err := s.Audit.List(r.Context(), f)
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries, "total": total})
	return nil
}
