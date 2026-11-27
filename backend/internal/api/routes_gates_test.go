package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// Invariants 3, 4 and 5 are about every mutating route at once: it is
// audited, it asks for a capability a read-only account lacks, and if it is
// irreversible it goes through s.destructive. A route mounted outside the
// group it was meant for breaks all three without a single handler changing,
// so they are read off the router's middleware chains here.
//
// Nothing below calls a route's handler, and no request goes through the
// router. The only code that runs is httpx.RequireCapability's closure and a
// limiter's, each wrapped around a stub that records whether it was reached.

// selfAuditedRoutes are the mutating routes outside every AuditMutations group,
// each with the handler that writes its own entry instead.
var selfAuditedRoutes = map[string]string{
	// certbot's hook has no principal for the middleware to name: every
	// challenge it adds or removes is recorded as certbot, success or not
	// (handlers_appdomain.go).
	"POST /api/v1/app-domain/hook/challenge": "handleAppDomainHook",
}

// ungatedReason is why a mutating route asks for no capability above read.
type ungatedReason string

const (
	reasonSelfService   ungatedReason = "self-service"
	reasonSignIn        ungatedReason = "sign-in"
	reasonOwnSecret     ungatedReason = "own secret"
	reasonPostRead      ungatedReason = "a read sent as POST"
	reasonCheckedByHand ungatedReason = "checked by hand"
)

// ungatedMutations are the mutating routes that ask for no capability a
// read-only account lacks, by the reason each was reviewed for. A route goes
// here only when every role may make the request: otherwise it belongs behind
// httpx.RequireCapability or s.destructive.
var ungatedMutations = map[ungatedReason][]string{
	// The account's own things, which every role has: its password, second
	// factor, passkeys, sessions, keys, profile, avatar, workspace, map layout,
	// assistant chats and GitHub connections. Each handler reaches only the
	// caller's own rows.
	reasonSelfService: {
		"POST /api/v1/account/password",
		"POST /api/v1/account/2fa/disable",
		"POST /api/v1/account/recovery-codes",
		"POST /api/v1/account/avatar",
		"DELETE /api/v1/account/avatar",
		"PUT /api/v1/account/hub-signin",
		"DELETE /api/v1/account/hub-signin",
		"POST /api/v1/account/passkeys/register/begin",
		"POST /api/v1/account/passkeys/register/finish",
		"PATCH /api/v1/account/passkeys/{id}",
		"DELETE /api/v1/account/passkeys/{id}",
		"PATCH /api/v1/account/profile",
		"POST /api/v1/account/sessions/revoke-others",
		"DELETE /api/v1/account/sessions/{id}",
		"PUT /api/v1/account/workspace",
		"POST /api/v1/tokens/",
		"POST /api/v1/tokens/mcp",
		"PUT /api/v1/tokens/{id}/connections",
		"PUT /api/v1/map/layout",
		"DELETE /api/v1/map/layout",
		"PATCH /api/v1/assistant/chats/{chat}",
		"DELETE /api/v1/assistant/chats/{chat}",
		"POST /api/v1/github/connections/{connection}/refresh",
	},
	// Proving who the caller is, or ending that: there is no principal yet
	// to hold a capability. An agent's enrolment is its one-time token.
	reasonSignIn: {
		"POST /api/v1/auth/login",
		"POST /api/v1/auth/logout",
		"POST /api/v1/auth/2fa/setup",
		"POST /api/v1/auth/2fa/enable",
		"POST /api/v1/auth/2fa/verify",
		"POST /api/v1/auth/passkey/begin",
		"POST /api/v1/auth/passkey/finish",
		"POST /api/v1/auth/hub/enter",
		"POST /api/v1/agent/enrol",
	},
	// Called by a machine with a secret of its own, which the handler checks
	// before it does anything: a webhook's signature, certbot's hook token, a
	// network vantage's enrolment and key.
	reasonOwnSecret: {
		"POST /api/v1/hooks/deploy/{hookID}",
		"POST /api/v1/hooks/providers/{provider}/{hookID}",
		"POST /api/v1/hooks/github-app",
		"POST /api/v1/hooks/scoped/{hookID}",
		"POST /api/v1/deploy/{id}/hooks/{triggerID}",
		"POST /api/v1/app-domain/hook/challenge",
		"POST /api/v1/probe-agent/enroll",
		"POST /api/v1/probe-agent/poll",
		"POST /api/v1/probe-agent/result",
	},
	// The question is a body (a statement to classify or plan, a filter, a
	// certificate, a name to look up), so it is a POST, but nothing is
	// changed. DELETE /mcp answers 405 to everyone.
	reasonPostRead: {
		"POST /api/v1/certificates/decode",
		"POST /api/v1/databases/{id}/classify",
		"POST /api/v1/databases/{id}/explain/index-candidates",
		"POST /api/v1/databases/{id}/explain/what-if",
		"POST /api/v1/databases/{id}/mongo/aggregate/preview",
		"POST /api/v1/databases/{id}/mongo/command/classify",
		"POST /api/v1/databases/{id}/mongo/count",
		"POST /api/v1/databases/{id}/mongo/document",
		"POST /api/v1/databases/{id}/mongo/explain",
		"POST /api/v1/databases/{id}/mongo/find",
		"POST /api/v1/databases/{id}/mongo/schema",
		"POST /api/v1/databases/{id}/mongo/validation/check",
		"POST /api/v1/databases/{id}/orm",
		"POST /api/v1/databases/{id}/redis/classify",
		"POST /api/v1/databases/{id}/rows/sql",
		"POST /api/v1/databases/{id}/seed/plan",
		"POST /api/v1/git/patch/check",
		"POST /api/v1/network/dns/lookup",
		"POST /api/v1/network/dns/tls-check",
		"DELETE /api/v1/mcp",
	},
	// What the request needs depends on what it carries or whose it is, so
	// the handler asks Principal.Can itself and fails closed: the statement's
	// risk for an analysed plan (authoriseSQL), the tool for an MCP call, the
	// owner for a draft, a key or a GitHub sign-in.
	reasonCheckedByHand: {
		"POST /api/v1/databases/{id}/explain",
		"PUT /api/v1/deploy/drafts/{draft}",
		"DELETE /api/v1/deploy/drafts/{draft}",
		"POST /api/v1/deploy/drafts/{draft}/detect",
		"POST /api/v1/deploy/drafts/{draft}/preflight",
		"POST /api/v1/github/callback",
		"POST /api/v1/mcp",
		"DELETE /api/v1/tokens/{id}",
	},
}

// gatedRoute is a mutating route as chi.Walk reads it.
type gatedRoute struct {
	method, pattern string
	// audited: httpx.AuditMutations is in the chain.
	audited bool
	// caps: what each httpx.RequireCapability in the chain asks for.
	caps []auth.Capability
	// destructive: s.destructive's mark is in the chain.
	destructive bool
}

func (rt gatedRoute) key() string { return rt.method + " " + rt.pattern }

// gated reports whether the chain asks for something a read-only account
// does not hold.
func (rt gatedRoute) gated() bool {
	return slices.ContainsFunc(rt.caps, func(c auth.Capability) bool { return !auth.RoleReadOnly.Can(c) })
}

// mutatingMethod is what AuditMutations records: everything but a read.
func mutatingMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// middlewareName is the function a middleware was made by. The compiler
// inlines httpx.AuditMutations and httpx.RequireCapability into the groups
// that call them, and the closure is then named after the group with the
// constructor's name inside it (api.(*Server).mountX.func1.RequireCapability.2),
// so the name, not the function's identity, is what tells them apart.
func middlewareName(mw func(http.Handler) http.Handler) string {
	fn := runtime.FuncForPC(reflect.ValueOf(mw).Pointer())
	if fn == nil {
		return ""
	}
	return fn.Name()
}

// probeCapability answers which capability a RequireCapability closure asks
// for. Each probe is an administrator scoped to exactly one capability
// (Principal.Can), so the closure lets through only the one it asks for; the
// stub behind it is all it can reach.
func probeCapability(t *testing.T, key string, mw func(http.Handler) http.Handler) auth.Capability {
	t.Helper()
	var asked []auth.Capability
	for _, c := range auth.RoleAdmin.Capabilities() {
		reached := false
		h := mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		p := &httpx.Principal{Role: auth.RoleAdmin, Scope: string(c), Kind: "token"}
		h.ServeHTTP(httptest.NewRecorder(), req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		if reached {
			asked = append(asked, c)
		}
	}
	if len(asked) != 1 {
		t.Fatalf("%s: a RequireCapability in its chain let through %v; the probe expects exactly one capability", key, asked)
	}
	return asked[0]
}

// mutatingRoutes walks h for every mutating route under /api/v1.
func mutatingRoutes(t *testing.T, h http.Handler) []gatedRoute {
	t.Helper()
	r, ok := h.(chi.Routes)
	if !ok {
		t.Fatal("Routes() no longer returns something chi can walk")
	}
	var out []gatedRoute
	err := chi.Walk(r, func(method, pattern string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(pattern, "/api/v1/") || !mutatingMethod(method) {
			return nil
		}
		rt := gatedRoute{method: method, pattern: pattern, destructive: isDestructive(mws)}
		for _, mw := range mws {
			switch name := middlewareName(mw); {
			case strings.Contains(name, ".AuditMutations."):
				rt.audited = true
			case strings.Contains(name, ".RequireCapability."):
				rt.caps = append(rt.caps, probeCapability(t, rt.key(), mw))
			}
		}
		out = append(out, rt)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 100 {
		t.Fatalf("only %d mutating routes walked; the walk is not seeing the real router", len(out))
	}
	return out
}

func TestEveryMutatingRouteIsAuditedAndGated(t *testing.T) {
	ungated := map[string]ungatedReason{}
	for reason, keys := range ungatedMutations {
		for _, key := range keys {
			if was, ok := ungated[key]; ok {
				t.Errorf("%s is listed under both %q and %q in ungatedMutations; keep the one reason that holds", key, was, reason)
			}
			ungated[key] = reason
		}
	}
	mounted := map[string]bool{}
	for _, mode := range routeModes {
		t.Run(mode.name, func(t *testing.T) {
			var audited, gated, destructive int
			routes := mutatingRoutes(t, mode.server(t).Routes())
			for _, rt := range routes {
				key := rt.key()
				mounted[key] = true

				// Invariant 5: every state-changing request lands in the audit log.
				_, self := selfAuditedRoutes[key]
				switch {
				case rt.audited && self:
					t.Errorf("%s: is in an AuditMutations group now; take it off selfAuditedRoutes, or it is recorded twice", key)
				case !rt.audited && !self:
					t.Errorf("%s: is not audited. Mount it in a group that uses httpx.AuditMutations (every group in routes.go does). "+
						"Only a route that has no principal for the middleware to name, and whose handler writes its own entry "+
						"for every change it makes, goes on selfAuditedRoutes instead, with that handler's name", key)
				case rt.audited:
					audited++
				}

				// Invariant 4: the route asks for its capability.
				reason, listed := ungated[key]
				switch {
				case rt.gated() && listed:
					t.Errorf("%s: asks for %v now; take it off ungatedMutations[%q]", key, rt.caps, reason)
				case !rt.gated() && !listed:
					t.Errorf("%s: asks for no capability a read-only account lacks (it asks for %v). "+
						"Mount it behind httpx.RequireCapability, or s.destructive if it cannot be undone. "+
						"If every role may really make this request, add it to ungatedMutations under the reason that holds: "+
						"%q (the account's own things), %q (proving who the caller is), %q (a webhook or machine with a secret the handler checks), "+
						"%q (changes nothing) or %q (the handler asks Principal.Can itself because the answer depends on the request)",
						key, rt.caps, reasonSelfService, reasonSignIn, reasonOwnSecret, reasonPostRead, reasonCheckedByHand)
				case rt.gated():
					gated++
				}

				// Invariant 3: an irreversible route is behind s.destructive,
				// which is the capability, the destructive budget and the mark
				// together; one without the other two is a route mounted by hand.
				asksDestructive := slices.Contains(rt.caps, auth.CapDestructive)
				switch {
				case rt.destructive && !asksDestructive:
					t.Errorf("%s: carries s.destructive's mark without the destructive capability; mount it with s.destructive(r, …) rather than markDestructive alone", key)
				case asksDestructive && !rt.destructive:
					t.Errorf("%s: asks for the destructive capability outside s.destructive, so it misses the destructive budget "+
						"and is not described as destructive; mount it with s.destructive(r, …)", key)
				case rt.destructive:
					destructive++
				}
			}
			t.Logf("%d mutating routes: %d audited by AuditMutations, %d asking for a capability above read, %d behind s.destructive",
				len(routes), audited, gated, destructive)
		})
	}
	for key := range selfAuditedRoutes {
		if !mounted[key] {
			t.Errorf("selfAuditedRoutes lists %s, which neither router mounts as a mutating route; remove it", key)
		}
	}
	for key, reason := range ungated {
		if !mounted[key] {
			t.Errorf("ungatedMutations[%q] lists %s, which neither router mounts as a mutating route; remove it", reason, key)
		}
	}
}

// Before an agent has a hub, its two routes answer anybody on the allowlisted
// network, so both spend the per-address login budget, the one that makes a
// guessed enrolment token slow. Limiters are method values and cannot be told
// apart by name, so the probe spends the route's own per-address limiters from
// one address until one refuses, then asks s.loginLim about that address: only
// the same bucket can already be empty.
func TestTheAgentsPreEnrolmentRoutesTakeTheLoginBudget(t *testing.T) {
	s := testAgentServer(t)
	r, ok := s.Routes().(chi.Routes)
	if !ok {
		t.Fatal("Routes() no longer returns something chi can walk")
	}
	stub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	ask := func(mw func(http.Handler) http.Handler, addr string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = addr + ":9999"
		w := httptest.NewRecorder()
		mw(stub).ServeHTTP(w, req)
		return w.Code
	}
	seen := 0
	err := chi.Walk(r, func(method, pattern string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		key := method + " " + pattern
		if !agentPreEnrolmentRoutes[key] {
			return nil
		}
		seen++
		// An address of its own for each route, so one route's spending is
		// not read as the other's.
		addr := fmt.Sprintf("192.0.2.%d", 10+seen)
		var limiters []func(http.Handler) http.Handler
		for _, mw := range mws {
			if strings.HasSuffix(middlewareName(mw), ".(*Limiter).Middleware-fm") {
				limiters = append(limiters, mw)
			}
		}
		if len(limiters) == 0 {
			t.Errorf("%s: answers before enrolment with no per-address limiter in front of it; put it behind s.loginLim.Middleware", key)
			return nil
		}
		refused := false
		for i := 0; i < 1000 && !refused; i++ {
			for _, mw := range limiters {
				if ask(mw, addr) == http.StatusTooManyRequests {
					refused = true
				}
			}
		}
		if !refused {
			t.Errorf("%s: a thousand requests from one address were all let through", key)
		} else if ask(s.loginLim.Middleware, addr) != http.StatusTooManyRequests {
			t.Errorf("%s: limited per address, but not by the login budget; put it behind s.loginLim.Middleware", key)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != len(agentPreEnrolmentRoutes) {
		t.Errorf("found %d of the %d pre-enrolment routes in the agent's router", seen, len(agentPreEnrolmentRoutes))
	}
}
