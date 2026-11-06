package api

import (
	"context"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/version"
	"github.com/go-chi/chi/v5"
)

// Connecting this dashboard to a Just Hub on its tailnet. The contract is
// docs/internal/backend/hub-link-protocol.md (section 8); the implementation is
// described in docs/internal/backend/hub-link.md.

// hubLinkSession is the hub field of GET /auth/session.
type hubLinkSession = hublink.SessionHub

// initHubLink makes the connector to a Just Hub. It is built whether or not
// the switch is on, because the Settings page still has to say that it is off;
// with JD_HUB_LINK=false it opens no connection and starts nothing. An agent
// answers only to its mTLS hub, which is a different thing, so none of this
// exists there.
func (s *Server) initHubLink() {
	if s.Cfg.AgentMode {
		return
	}
	if s.Cfg.HubLinkTestOrigin != "" {
		s.Log.Warn("JD_HUB_LINK_TEST_ORIGIN is set; this is for tests and must never be used in production",
			"origin", s.Cfg.HubLinkTestOrigin)
	}
	s.modules.hubLink = hublink.New(hublink.Options{
		Store: s.Store, Sealer: s.Sealer, Audit: s.Audit, Log: s.Log,
		Enabled: s.Cfg.HubLink, TestOrigin: s.Cfg.HubLinkTestOrigin,
		// Its own Host: the snapshot's rates are differences between two
		// reads, and each link reads on its own schedule.
		Host: newAccountHost(s), Version: version.Version,
		Endpoint: s.hubLinkEndpoint,
		SignIn:   s.Cfg.HubSignIn, Auth: s.Auth,
		// The sockets of the hub sessions a revocation ended close at once.
		SessionsEnded: s.hubSockets.kick,
	})
	// A change to an account that ends its hub sign-in grant is made inside
	// auth's own transaction; the audit entry is written here once it has
	// committed, naming whoever made the change.
	s.Auth.SetHubGrantEnded(func(ctx context.Context, end auth.HubGrantEnd) {
		var by audit.Entry
		if p, ok := httpx.PrincipalFrom(ctx); ok && p.User != nil {
			by = httpx.AuditWho(p)
		}
		s.modules.hubLink.AuditAccountEnd(ctx, end, by)
	})
}

// hubLinkEndpoint is the address a browser opens this dashboard at, exactly as
// access.endpoint in every heartbeat (accountHost.access) reads it, so the
// hub's approval screen and its Open button agree.
func (s *Server) hubLinkEndpoint(ctx context.Context) string {
	if s.modules.selfConfig == nil {
		return ""
	}
	if rep := s.modules.selfConfig.Report(ctx); rep.Supported {
		return rep.Endpoint
	}
	return ""
}

// mountHubHello is the hub's discovery probe: no sign-in, but inside /api/v1,
// so the network allowlist runs first, and under the API rate limit per
// address. It is not mounted with the hub link switched off, so that answers
// 404 as an older dashboard does.
func (s *Server) mountHubHello(r chi.Router) {
	if s.modules.hubLink == nil || !s.modules.hubLink.Enabled() {
		return
	}
	r.Group(func(r chi.Router) {
		r.Use(s.apiLim.Middleware)
		r.Method(http.MethodGet, "/hub/hello", s.handle(s.handleHubHello))
	})
}

// handleHubHello says whether this server can be linked. Never the version,
// the host name, the hub's address or anything else.
func (s *Server) handleHubHello(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(hublink.HelloBody(s.modules.hubLink.Hello(r.Context())))
	return nil
}

// mountHubLinkRoutes is the system.admin part of /dashboard/hub. Connecting,
// cancelling, re-scoping and disconnecting are session-only: an API key must
// not be able to hand this server to a hub or take it back. The status read
// sits with the every-role reads of /dashboard (mountSelfUpdateRoutes).
func (s *Server) mountHubLinkRoutes(r chi.Router) {
	// A heartbeat now changes nothing on this machine and nothing about who
	// the link belongs to, so a key may ask for one.
	r.Method(http.MethodPost, "/hub/sync", s.handle(s.handleHubLinkSync))
	r.Group(func(r chi.Router) {
		r.Use(httpx.RequireSession)
		// A session a hub sign-in made may not hand this server to a hub or
		// widen what one may do (hub-link-protocol.md section 9.9).
		r.Group(func(r chi.Router) {
			r.Use(httpx.RefuseHubSession)
			r.Method(http.MethodPost, "/hub", s.handle(s.handleHubLinkConnect))
			r.Method(http.MethodDelete, "/hub/request", s.handle(s.handleHubLinkCancel))
			r.Method(http.MethodPut, "/hub/scope", s.handle(s.handleHubLinkScope))
		})
		// Removing an account's sign-in grant only takes power away from the
		// hub, so a hub session may do it, as it may disconnect.
		r.Method(http.MethodDelete, "/hub/signin/{userId}", s.handle(s.handleHubSignInRemove))
		// Disconnecting stops everything the hub could do: it is in the
		// destructive budget, as unlinking the account is.
		s.destructive(r, func(r chi.Router) {
			r.Method(http.MethodDelete, "/hub", s.handle(s.handleHubLinkDisconnect))
		})
	})
}

func (s *Server) handleHubLinkStatus(w http.ResponseWriter, r *http.Request) error {
	return s.writeHubLinkStatus(w, r)
}

func (s *Server) writeHubLinkStatus(w http.ResponseWriter, r *http.Request) error {
	p := httpx.MustPrincipal(r)
	st, err := s.modules.hubLink.StatusFor(r.Context(), p.Can(auth.CapSystemAdmin), p.UserID())
	if err != nil {
		return httpx.Internal(err)
	}
	httpx.JSON(w, http.StatusOK, st)
	return nil
}

// fitMessage keeps an error's message inside the 300 characters the error
// shape allows.
func fitMessage(msg string) string {
	if len(msg) <= 300 {
		return msg
	}
	msg = msg[:300]
	for !utf8.ValidString(msg) {
		msg = msg[:len(msg)-1]
	}
	return msg
}

// mapHubLinkError turns the connector's errors into the dashboard's own.
func mapHubLinkError(err error) error {
	var rule *hublink.URLError
	var unreachable *hublink.UnreachableError
	switch {
	case errors.Is(err, hublink.ErrDisabled):
		return httpx.Err(http.StatusForbidden, "hub_link_disabled", "connecting to Just Hub is switched off on this server (JD_HUB_LINK=false)")
	case errors.Is(err, hublink.ErrAlreadyLinked):
		return httpx.Err(http.StatusConflict, "already_linked", "this server is already connected to a hub; disconnect it first")
	case errors.Is(err, hublink.ErrLinkPending):
		return httpx.Err(http.StatusConflict, "link_pending", "a request to connect to a hub is already waiting for approval; cancel it or approve it in the hub")
	case errors.Is(err, hublink.ErrNotLinked):
		return httpx.Err(http.StatusConflict, "not_linked", "this server is not connected to a hub")
	case errors.Is(err, hublink.ErrURLRefused):
		return httpx.Err(http.StatusConflict, "hub_url_refused", fitMessage(err.Error()))
	case errors.Is(err, hublink.ErrBadScope):
		return httpx.BadRequest("scope must be observe, operate or maintain")
	case errors.As(err, &rule):
		return httpx.Err(http.StatusBadRequest, rule.Code, fitMessage(rule.Message))
	case errors.As(err, &unreachable):
		return httpx.Err(http.StatusBadGateway, "hub_unreachable", fitMessage(unreachable.Error()))
	}
	return httpx.Internal(err)
}

type hubLinkConnectBody struct {
	URL   string `json:"url"`
	Scope string `json:"scope"`
}

func (s *Server) handleHubLinkConnect(w http.ResponseWriter, r *http.Request) error {
	var body hubLinkConnectBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}
	scope, ok := accountlink.ParseScope(body.Scope)
	if !ok {
		return httpx.BadRequest("scope must be observe, operate or maintain")
	}
	httpx.SetAudit(r, "hub.link.begin", string(scope), map[string]any{"hub": fitMessage(body.URL)})
	req, err := s.modules.hubLink.Connect(r.Context(), hublink.ConnectRequest{
		URL: body.URL, Scope: scope, By: httpx.MustPrincipal(r).Username(),
	})
	if err != nil {
		return mapHubLinkError(err)
	}
	httpx.SetAudit(r, "hub.link.begin", string(scope), map[string]any{"hub": req.URL, "hubNode": s.modules.hubLink.NodeNote(req)})
	return s.writeHubLinkStatus(w, r)
}

// handleHubLinkCheck applies the URL rule, steps 1 to 8, to ?url= and says
// what Connect would store and whose machine answers there (protocol 8.2). It
// starts nothing and sends nothing to the hub, so it is not audited.
func (s *Server) handleHubLinkCheck(w http.ResponseWriter, r *http.Request) error {
	res, err := s.modules.hubLink.Check(r.Context(), r.URL.Query().Get("url"))
	if err != nil {
		return mapHubLinkError(err)
	}
	httpx.JSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) handleHubLinkCancel(w http.ResponseWriter, r *http.Request) error {
	httpx.SetAudit(r, "hub.link.cancel", "", nil)
	if err := s.modules.hubLink.Cancel(r.Context()); err != nil {
		return httpx.Internal(err)
	}
	httpx.NoContent(w)
	return nil
}

func (s *Server) handleHubLinkScope(w http.ResponseWriter, r *http.Request) error {
	scope, err := readAccountLinkScope(r)
	if err != nil {
		return err
	}
	httpx.SetAudit(r, "hub.link.scope", string(scope), nil)
	if err := s.modules.hubLink.SetScope(r.Context(), scope); err != nil {
		return mapHubLinkError(err)
	}
	return s.writeHubLinkStatus(w, r)
}

func (s *Server) handleHubLinkDisconnect(w http.ResponseWriter, r *http.Request) error {
	name := ""
	if st, linked, err := s.modules.hubLink.Linked(r.Context()); err != nil {
		return httpx.Internal(err)
	} else if linked {
		name = st.Owner
	}
	httpx.SetAudit(r, "hub.unlink", name, nil)
	notified, err := s.modules.hubLink.Disconnect(r.Context())
	if err != nil {
		return mapHubLinkError(err)
	}
	httpx.SetAudit(r, "hub.unlink", name, map[string]any{"hubNotified": notified})
	// The link is gone either way; the page says whether the hub heard, since
	// a hub that did not still lists this server until it is removed there.
	httpx.JSON(w, http.StatusOK, map[string]bool{"hubNotified": notified})
	return nil
}

func (s *Server) handleHubLinkSync(w http.ResponseWriter, r *http.Request) error {
	if err := s.modules.hubLink.Sync(r.Context()); err != nil {
		return mapHubLinkError(err)
	}
	return s.writeHubLinkStatus(w, r)
}
