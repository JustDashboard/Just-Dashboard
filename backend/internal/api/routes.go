package api

import (
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Routes builds the whole API surface.
//
// The layering is deliberate and is the security contract of this service:
//
//	network allowlist  →  rate limit  →  authentication  →  capability  →  handler
//
// Nothing below /api is reachable without passing every layer above it. There
// is no "UI-only" protection anywhere: the frontend hides controls a role
// cannot use, but the server re-decides every request on its own.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(httpx.RealIP(s.Cfg.TrustedProxies))
	r.Use(httpx.Recoverer(s.Log))
	r.Use(httpx.RequestLogger(s.Log))
	r.Use(httpx.SecurityHeaders)

	// Liveness is the only unauthenticated route, and it reveals nothing:
	// a fixed body, no version, no hostname.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(httpx.AllowlistCIDRs(s.Cfg.AllowedCIDRs, s.Log))

		// Deploy webhooks authenticate with their own per-project HMAC
		// signature rather than a dashboard session, since they are called
		// by CI. They still sit behind the network allowlist.
		r.Route("/hooks", func(r chi.Router) {
			r.Use(s.apiLim.Middleware)
			// Audited like every other mutating group. Without this the only
			// trail was whatever the handler chose to write, and it wrote
			// nothing for an unknown hook id — so enumerating ids was silent,
			// on the one route reachable without a dashboard session.
			r.Use(httpx.AuditMutations(s.Audit))
			r.Method(http.MethodPost, "/deploy/{hookID}", s.handle(s.handleDeployWebhook))
			r.Method(http.MethodPost, "/providers/{provider}/{hookID}", s.handle(s.handleDeploymentProviderWebhook))
			// The GitHub App's one webhook: signed with the App's own secret
			// and routed by repository to whichever triggers asked for it.
			r.Method(http.MethodPost, "/github-app", s.handle(s.handleGitHubAppWebhook))
			r.Method(http.MethodPost, "/scoped/{hookID}", s.handle(s.handleDeploymentGenericWebhook))
		})
		r.Group(func(r chi.Router) {
			r.Use(s.apiLim.Middleware)
			r.Use(httpx.AuditMutations(s.Audit))
			r.Method(http.MethodPost, "/deploy/{id}/hooks/{triggerID}", s.handle(s.handleDeploymentGenericWebhook))
		})

		if s.Cfg.AgentMode {
			// An agent has no human login surface at all: no password route,
			// no session, no second factor. The only way in is the hub's
			// certificate, and the only way to become that hub is /agent/enrol.
			s.mountAgentRoutes(r)
		} else {
			r.Group(func(r chi.Router) {
				r.Use(s.loginLim.Middleware)
				r.Use(httpx.AuditMutations(s.Audit))
				r.Use(httpx.RequireCSRF)
				r.Method(http.MethodPost, "/auth/login", s.handle(s.handleLogin))
			})

			// Half-authenticated: password proved, second factor outstanding.
			r.Group(func(r chi.Router) {
				r.Use(s.Authn.AuthenticatePartial)
				r.Use(s.apiLim.ByPrincipal)
				r.Use(httpx.AuditMutations(s.Audit))
				r.Use(httpx.RequireCSRF)
				r.Method(http.MethodGet, "/auth/session", s.handle(s.handleSession))
				r.Method(http.MethodPost, "/auth/2fa/setup", s.handle(s.handleTOTPSetup))
				r.Method(http.MethodPost, "/auth/2fa/enable", s.handle(s.handleTOTPEnable))
				r.Method(http.MethodPost, "/auth/2fa/verify", s.handle(s.handleTOTPVerify))
				r.Method(http.MethodPost, "/auth/logout", s.handle(s.handleLogout))
			})
			r.Group(func(r chi.Router) {
				r.Use(s.Authn.AuthenticatePasswordChange)
				r.Use(s.apiLim.ByPrincipal)
				r.Use(httpx.AuditMutations(s.Audit))
				r.Use(httpx.RequireCSRF)
				r.Use(httpx.RequireSession)
				r.Method(http.MethodPost, "/account/password", s.handle(s.handleChangePassword))
			})
		}

		// The feature surface is the same program either way. What differs is
		// who is allowed to reach it: a person with a session or token, or the
		// one hub this agent was enrolled against.
		r.Group(func(r chi.Router) {
			if s.Cfg.AgentMode {
				r.Use(httpx.HubOnly(s.Agent))
			} else {
				r.Use(s.Authn.Authenticate)
			}
			r.Use(s.apiLim.ByPrincipal)
			r.Use(httpx.AuditMutations(s.Audit))
			r.Use(httpx.RequireCSRF)

			s.mountAccountRoutes(r)
			s.mountSystemRoutes(r)
			s.mountDockerRoutes(r)
			s.mountProcessRoutes(r)
			s.mountLogRoutes(r)
			s.mountTerminalRoutes(r)
			s.mountFileRoutes(r)
			s.mountGitRoutes(r)
			s.mountPackageRoutes(r)
			s.mountSelfUpdateRoutes(r)
			s.mountProxyRoutes(r)
			s.mountDatabaseRoutes(r)
			s.mountLinuxUserRoutes(r)
			s.mountNetSecRoutes(r)
			s.mountBackupRoutes(r)
			s.mountDeployRoutes(r)
			s.mountJobRoutes(r)
		})
	})
	return r
}

func (s *Server) mountAccountRoutes(r chi.Router) {
	r.Route("/account", func(r chi.Router) {
		r.Use(httpx.RequireSession)
		r.Method(http.MethodPost, "/recovery-codes", s.handle(s.handleRecoveryCodesRegen))
		// Enrolling is on the half-authenticated group above, because that is
		// where an install requiring 2FA sends you. Turning it off is only
		// ever done by somebody already signed in, so it lives here.
		r.Method(http.MethodPost, "/2fa/disable", s.handle(s.handleDisableTOTP))
		r.Method(http.MethodGet, "/sessions", s.handle(s.handleListOwnSessions))
		r.Method(http.MethodDelete, "/sessions/{id}", s.handle(s.handleRevokeOwnSession))
		r.Method(http.MethodPost, "/sessions/revoke-others", s.handle(s.handleRevokeOtherSessions))
		r.Method(http.MethodPatch, "/profile", s.handle(s.handleUpdateProfile))
		r.Method(http.MethodGet, "/avatar", s.handle(s.handleOwnAvatar))
		r.Method(http.MethodPost, "/avatar", s.handle(s.handleUploadOwnAvatar))
		r.Method(http.MethodDelete, "/avatar", s.handle(s.handleDeleteOwnAvatar))
	})

	r.Route("/tokens", func(r chi.Router) {
		r.Method(http.MethodGet, "/", s.handle(s.handleListTokens))
		r.Group(func(r chi.Router) {
			r.Use(httpx.RequireSession)
			r.Method(http.MethodPost, "/", s.handle(s.handleCreateToken))
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleRevokeToken))
		})
	})

	r.Route("/dashboard-users", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Use(httpx.RequireSession)
		r.Method(http.MethodGet, "/", s.handle(s.handleListUsers))
		r.Method(http.MethodPost, "/", s.handle(s.handleCreateUser))
		r.Method(http.MethodPatch, "/{id}", s.handle(s.handleUpdateUser))
		r.Method(http.MethodGet, "/{id}/avatar", s.handle(s.handleUserAvatar))
		r.Method(http.MethodPost, "/{id}/reset-totp", s.handle(s.handleResetUserTOTP))
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/{id}", s.handle(s.handleDeleteUser))
		})
	})

	r.Route("/audit", func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapSystemAdmin))
		r.Method(http.MethodGet, "/", s.handle(s.handleAuditList))
	})
}

// destructive marks routes whose effects cannot be undone.
//
// It is the one marker for that, and it is applied to every such route even
// where the surrounding group already demands a stricter capability — nesting
// it inside a system.admin group costs nothing (admin holds every capability)
// and buys the tighter rate budget plus one honest answer to "which routes are
// irreversible?". The typed-confirmation phrase is still enforced inside each
// handler, where the expected phrase is known; adding a destructive route
// means doing both.
//
// The one route that cannot use it is POST /databases/{id}/query, where
// whether the request is destructive depends on the SQL rather than on the
// route. That handler applies the same capability check and the same budget by
// hand, after classifying the statement.
func (s *Server) destructive(r chi.Router, fn func(chi.Router)) {
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireCapability(auth.CapDestructive))
		r.Use(s.destrLim.ByPrincipal)
		fn(r)
	})
}
