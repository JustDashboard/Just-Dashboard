package httpx

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sessioncookie"
)

// SessionCookie is the session cookie of a dashboard served over HTTPS, which
// is every installation but plain loopback HTTP; CookieName is the one this
// dashboard issues.
const SessionCookie = sessioncookie.Name

type Authenticator struct {
	Svc    *auth.Service
	Secure bool
}

// CookieName is the name this dashboard's session cookie has: the __Host-
// one wherever the cookie is Secure, and the plain one on loopback HTTP,
// where a browser would refuse the prefix.
func (a *Authenticator) CookieName() string { return sessioncookie.For(a.Secure) }

// SetSessionCookie issues the session credential. HttpOnly keeps it away from
// page scripts, SameSite=Strict blunts cross-site request forgery, and Secure
// is set whenever the dashboard is not being served over plain loopback HTTP.
// Path=/ and no Domain are what the __Host- prefix requires, and what keeps
// the cookie on this one name.
func (a *Authenticator) SetSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, a.cookie(a.CookieName(), token, maxAge))
}

// ClearSessionCookie signs the browser out under both names: the one this
// dashboard issues and the one releases before it did, which a browser that
// has not been back since the upgrade still holds.
func (a *Authenticator) ClearSessionCookie(w http.ResponseWriter) {
	a.SetSessionCookie(w, "", -1)
	http.SetCookie(w, a.cookie(sessioncookie.LegacyName, "", -1))
}

func (a *Authenticator) cookie(name, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	}
}

// sessionCookie is the session the request carries. The current name wins
// when both are sent; legacy says the token came under the old name, and
// stale that the old name was sent at all.
type sessionCookie struct {
	token  string
	legacy bool
	stale  bool
}

func (a *Authenticator) sessionToken(r *http.Request) sessionCookie {
	var found sessionCookie
	old, err := r.Cookie(sessioncookie.LegacyName)
	found.stale = err == nil
	if c, err := r.Cookie(a.CookieName()); err == nil && c.Value != "" {
		found.token = c.Value
	} else if found.stale && old.Value != "" {
		found.token, found.legacy = old.Value, true
	}
	return found
}

// settle finishes an upgrade for a session that resolved: one that arrived
// under the legacy name is given the current one for what is left of its
// lifetime, and the old cookie is cleared wherever it was sent. The first
// request after an upgrade signs nobody out, and the old name is never sent
// again. Nothing about the session changes, so there is nothing to audit.
func (a *Authenticator) settle(w http.ResponseWriter, found sessionCookie, expires time.Time) {
	if found.legacy {
		if maxAge := int(time.Until(expires).Seconds()); maxAge > 0 {
			a.SetSessionCookie(w, found.token, maxAge)
		}
	}
	if found.stale {
		http.SetCookie(w, a.cookie(sessioncookie.LegacyName, "", -1))
	}
}

// Authenticate resolves a session cookie or Bearer token into a Principal.
// Routes mounted behind it are unreachable without a valid credential — the
// UI shell is a separate concern and never stands in for this check.
func (a *Authenticator) Authenticate(next http.Handler) http.Handler {
	return a.authenticate(next, false, false)
}

// AuthenticateScoped is Authenticate for the few routes a scoped API key may
// reach. Every other group refuses such a key outright, so a key made for one
// job cannot reach a route that was never meant to name it.
func (a *Authenticator) AuthenticateScoped(next http.Handler) http.Handler {
	return a.authenticate(next, false, true)
}

// AuthenticatePasswordChange still requires every configured factor, but lets
// an account replace its temporary password before normal feature access.
func (a *Authenticator) AuthenticatePasswordChange(next http.Handler) http.Handler {
	return a.authenticate(next, true, false)
}

// DeployKeyGate decides whether a deploy key may make this request: nil admits
// it, and an error is the answer it gets instead. Only the feature routes'
// authentication asks one; every other group refuses a deploy key outright.
type DeployKeyGate func(r *http.Request, p *Principal) error

// AuthenticateFeatures is Authenticate for the group that holds the feature
// routes, where a deploy key may also be admitted: gate is asked about every
// request that carries one, and a key it admits is held to its role from
// there on, with the route recorded as admitted so RequireSession lets it by.
// A nil gate admits no deploy key at all.
//
// A refusal is written to log: it is answered before the group's own audit
// middleware runs, and a valid key tried where it does not reach — a reveal,
// a credential — is the one refusal an operator most wants to find.
func (a *Authenticator) AuthenticateFeatures(gate DeployKeyGate, log *audit.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return a.authenticateWith(next, false, false, gate, log)
	}
}

func (a *Authenticator) authenticate(next http.Handler, allowPasswordChange, allowScoped bool) http.Handler {
	return a.authenticateWith(next, allowPasswordChange, allowScoped, nil, nil)
}

func (a *Authenticator) authenticateWith(next http.Handler, allowPasswordChange, allowScoped bool, gate DeployKeyGate, log *audit.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.resolveWithPasswordChange(w, r, allowPasswordChange)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		switch {
		// An MCP key opens its own endpoint, which AuthenticateMCP guards,
		// and no route behind this one, scoped or not.
		case p.Scope == auth.ScopeMCP:
			WriteError(w, r, Err(http.StatusForbidden, "key_scope",
				"this is an MCP key: it opens the MCP endpoint, /api/v1/mcp, and nothing else"))
			return
		case p.Scope == auth.ScopeDeploy:
			if gate == nil {
				WriteError(w, r, Err(http.StatusForbidden, "key_scope",
					"this is a deploy key: it reaches the deployment routes under /api/v1/deploy and nothing else"))
				return
			}
			if err := gate(r, p); err != nil {
				WriteError(w, r, err)
				if log != nil {
					recordKeyRefusal(log, r, p, err)
				}
				return
			}
			// The scope has done its work by choosing the route. From here
			// the key is held to its role, as an MCP key is inside its
			// endpoint, so it carries no capability scope that would make
			// Principal.Can refuse everything.
			p.Scope, p.DeployRoute = "", true
		case p.Scope != "" && !allowScoped:
			WriteError(w, r, Err(http.StatusForbidden, "key_scope",
				"this API key is limited to "+p.Scope+" and cannot reach this route"))
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// AuthenticatePartial accepts a session whose second factor is still
// outstanding. Authentication status, logout and the 2FA routes use it.
func (a *Authenticator) AuthenticatePartial(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		found := a.sessionToken(r)
		if found.token == "" {
			WriteError(w, r, ErrUnauthorized)
			return
		}
		sess, user, err := a.Svc.ResolveSession(r.Context(), found.token)
		if err != nil {
			WriteError(w, r, Err(http.StatusUnauthorized, "unauthorized", "session invalid or expired"))
			return
		}
		a.settle(w, found, sess.ExpiresAt)
		p := &Principal{
			User: user, Role: user.Role, SessionID: sess.ID, Kind: "session",
			IP: ClientIP(r), UserAgent: r.UserAgent(), Elevated: sess.TwoFAPassed,
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

// resolveWithPasswordChange is the request's principal. A session found under
// the legacy name is renamed on w as soon as it resolves, whether or not the
// route then admits it: a session owing a second factor is still one to keep.
// A bearer token is read first, so a script's request never has its cookies
// rewritten.
func (a *Authenticator) resolveWithPasswordChange(w http.ResponseWriter, r *http.Request, allowPasswordChange bool) (*Principal, error) {
	if bearer := bearerToken(r); bearer != "" {
		tok, user, role, err := a.Svc.ResolveAPIToken(r.Context(), bearer)
		if err != nil {
			return nil, Err(http.StatusUnauthorized, "unauthorized", "api token invalid, revoked or expired")
		}
		if user.MustChangePW {
			return nil, passwordChangeRequired()
		}
		return tokenPrincipal(r, tok, user, role), nil
	}
	found := a.sessionToken(r)
	if found.token == "" {
		return nil, ErrUnauthorized
	}
	sess, user, err := a.Svc.ResolveSession(r.Context(), found.token)
	if err != nil {
		if errors.Is(err, auth.ErrAccountDisabled) {
			return nil, Err(http.StatusForbidden, "account_disabled", "account disabled")
		}
		return nil, Err(http.StatusUnauthorized, "unauthorized", "session invalid or expired")
	}
	a.settle(w, found, sess.ExpiresAt)
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

// AuthenticateMCP admits an MCP key and nothing else. A session cookie is
// not read, so no page in a browser can reach the endpoint and it needs no
// CSRF header, and a key for the rest of the API is refused: an AI client
// reaches the databases its own key was granted and no others.
func (a *Authenticator) AuthenticateMCP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The challenge names no authorization server, so a client finds
		// nothing to sign in through and says the key it was given failed.
		bearer := bearerToken(r)
		if bearer == "" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Just Dashboard MCP"`)
			WriteError(w, r, Err(http.StatusUnauthorized, "unauthorized", "send an MCP key as a bearer token"))
			return
		}
		tok, user, role, err := a.Svc.ResolveAPIToken(r.Context(), bearer)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Just Dashboard MCP", error="invalid_token"`)
			WriteError(w, r, Err(http.StatusUnauthorized, "unauthorized", "api token invalid, revoked or expired"))
			return
		}
		if user.MustChangePW {
			WriteError(w, r, passwordChangeRequired())
			return
		}
		if tok.Scope != auth.ScopeMCP {
			WriteError(w, r, Err(http.StatusForbidden, "key_scope",
				"this endpoint takes an MCP key, made under Databases → MCP server; this key is for the rest of the API"))
			return
		}
		// The scope has done its work by bringing the key here. Inside the
		// endpoint the key is held to its owner's role and to its grants,
		// which the handler reads, so it carries no capability scope that
		// would make Principal.Can refuse everything.
		p := tokenPrincipal(r, tok, user, role)
		p.Scope = ""
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

func tokenPrincipal(r *http.Request, tok *auth.APIToken, user *auth.User, role auth.Role) *Principal {
	return &Principal{
		User: user, Role: role, TokenID: tok.ID, Token: tok, Kind: "token", Scope: tok.Scope,
		IP: ClientIP(r), UserAgent: r.UserAgent(), Elevated: true,
	}
}

func passwordChangeRequired() error {
	return Err(http.StatusForbidden, "password_change_required", "change your temporary password before continuing")
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

// RequireToken is the mirror of RequireSession, for a route that exists to be
// called by a script: a signed-in browser has no business there, and a cookie
// is never the credential for it.
func RequireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if MustPrincipal(r).Kind != "token" {
			WriteError(w, r, Err(http.StatusForbidden, "token_required",
				"this route is for API keys, not an interactive session"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSession blocks API-token principals from routes that must be driven
// by a human — changing your own password, minting tokens, managing accounts.
// The one key it lets by is a deploy key the deploy key table admitted to this
// very route (AuthenticateFeatures): that table, not this middleware, is
// where a deployment write is opened to a key, one line per route.
func RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := MustPrincipal(r)
		if p.Kind != "session" && !p.DeployRoute {
			WriteError(w, r, Err(http.StatusForbidden, "session_required",
				"this action requires an interactive session, not an API token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// recordKeyRefusal writes a deploy key's refused request to the audit trail
// the way AuditMutations writes a refused write, reads included: what was
// asked for is the target, and the refusal is the detail.
func recordKeyRefusal(log *audit.Logger, r *http.Request, p *Principal, err error) {
	status, detail := http.StatusForbidden, err.Error()
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		status, detail = apiErr.Status, apiErr.Code+": "+apiErr.Message
	}
	log.Record(r.Context(), audit.Entry{
		UserID: p.UserID(), Username: p.Username(), Role: string(p.Role), IP: ClientIP(r),
		Actor: principalKind(p), Action: "auth.deploy_key.refused", Target: r.Method + " " + r.URL.Path,
		Method: r.Method, Path: r.URL.Path, Status: status, Success: false, Detail: detail,
		KeyID: p.KeyID(), KeyName: p.KeyName(),
	})
}
