package httpx

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

const SessionCookie = "vpsd_session"

type Authenticator struct {
	Svc    *auth.Service
	Secure bool
}

// SetSessionCookie issues the session credential. HttpOnly keeps it away from
// page scripts, SameSite=Strict blunts cross-site request forgery, and Secure
// is set whenever the dashboard is not being served over plain loopback HTTP.
func (a *Authenticator) SetSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

func (a *Authenticator) ClearSessionCookie(w http.ResponseWriter) {
	a.SetSessionCookie(w, "", -1)
}

// Authenticate resolves a session cookie or Bearer token into a Principal.
// Routes mounted behind it are unreachable without a valid credential — the
// UI shell is a separate concern and never stands in for this check.
func (a *Authenticator) Authenticate(next http.Handler) http.Handler {
	return a.authenticate(next, false)
}

// AuthenticatePasswordChange still requires every configured factor, but lets
// an account replace its temporary password before normal feature access.
func (a *Authenticator) AuthenticatePasswordChange(next http.Handler) http.Handler {
	return a.authenticate(next, true)
}

func (a *Authenticator) authenticate(next http.Handler, allowPasswordChange bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.resolveWithPasswordChange(r, allowPasswordChange)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// AuthenticatePartial accepts a session whose second factor is still
// outstanding. Authentication status, logout and the 2FA routes use it.
func (a *Authenticator) AuthenticatePartial(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := sessionToken(r)
		if token == "" {
			WriteError(w, r, ErrUnauthorized)
			return
		}
		sess, user, err := a.Svc.ResolveSession(r.Context(), token)
		if err != nil {
			WriteError(w, r, Err(http.StatusUnauthorized, "unauthorized", "session invalid or expired"))
			return
		}
		p := &Principal{
			User: user, Role: user.Role, SessionID: sess.ID, Kind: "session",
			IP: ClientIP(r), UserAgent: r.UserAgent(), Elevated: sess.TwoFAPassed,
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

func (a *Authenticator) resolve(r *http.Request) (*Principal, error) {
	return a.resolveWithPasswordChange(r, false)
}

func (a *Authenticator) resolveWithPasswordChange(r *http.Request, allowPasswordChange bool) (*Principal, error) {
	if bearer := bearerToken(r); bearer != "" {
		tok, user, role, err := a.Svc.ResolveAPIToken(r.Context(), bearer)
		if err != nil {
			return nil, Err(http.StatusUnauthorized, "unauthorized", "api token invalid, revoked or expired")
		}
		if user.MustChangePW {
			return nil, passwordChangeRequired()
		}
		return &Principal{
			User: user, Role: role, TokenID: tok.ID, Kind: "token",
			IP: ClientIP(r), UserAgent: r.UserAgent(), Elevated: true,
		}, nil
	}
	token := sessionToken(r)
	if token == "" {
		return nil, ErrUnauthorized
	}
	sess, user, err := a.Svc.ResolveSession(r.Context(), token)
	if err != nil {
		if errors.Is(err, auth.ErrAccountDisabled) {
			return nil, Err(http.StatusForbidden, "account_disabled", "account disabled")
		}
		return nil, Err(http.StatusUnauthorized, "unauthorized", "session invalid or expired")
	}
	// A password alone is never enough: an un-elevated session is refused
	// everywhere except the second-factor routes.
	if !sess.TwoFAPassed {
		code := "totp_required"
		if !user.TOTPEnabled {
			code = "totp_enrollment_required"
		}
		return nil, Err(http.StatusUnauthorized, code, "two-factor authentication required")
	}
	if user.MustChangePW && !allowPasswordChange {
		return nil, passwordChangeRequired()
	}
	return &Principal{
		User: user, Role: user.Role, SessionID: sess.ID, Kind: "session",
		IP: ClientIP(r), UserAgent: r.UserAgent(), Elevated: true,
	}, nil
}

func passwordChangeRequired() error {
	return Err(http.StatusForbidden, "password_change_required", "change your temporary password before continuing")
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const p = "bearer "
	if len(h) > len(p) && strings.EqualFold(h[:len(p)], p) {
		return strings.TrimSpace(h[len(p):])
	}
	return ""
}

// RequireCapability gates a route on a capability rather than a role name, so
// adding a role later cannot silently widen an existing endpoint.
func RequireCapability(c auth.Capability) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := MustPrincipal(r)
			if !p.Can(c) {
				WriteError(w, r, Err(http.StatusForbidden, "forbidden",
					"your role does not permit this action ("+string(c)+")"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireSession blocks API-token principals from routes that must be driven
// by a human — changing your own password, minting tokens, managing accounts.
func RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if MustPrincipal(r).Kind != "session" {
			WriteError(w, r, Err(http.StatusForbidden, "session_required",
				"this action requires an interactive session, not an API token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
