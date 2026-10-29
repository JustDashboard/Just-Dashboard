package httpx

import (
	"context"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

type ctxKey int

const (
	principalKey ctxKey = iota
	// delegateKey is the MCP key's principal a dispatched tool call carries.
	delegateKey ctxKey = 300
)

// Principal is the authenticated identity behind a request, whether it arrived
// with a session cookie or an API token.
type Principal struct {
	User      *auth.User
	Role      auth.Role
	SessionID string
	TokenID   int64
	// Token is the key behind a token principal, scope and all.
	Token *auth.APIToken
	Kind  string // "session" | "token"
	// Scope is the one capability a scoped API key holds; empty for a session
	// and for an ordinary key.
	Scope string
	// DeployRoute is a deploy key the deploy key table admitted to the route
	// this request matched. It is set by AuthenticateFeatures alone, and it is
	// what lets such a key past RequireSession on that route and no other.
	DeployRoute bool
	// ViaMCP is an MCP key's call of a tool generated from the deployment
	// API, which the MCP endpoint dispatched through the router
	// (WithMCPDelegate). AuthenticateFeatures sets it alone, after the deploy
	// key table admitted the route, and from there the key is held to every
	// rule a deploy key is (DeployKey).
	ViaMCP    bool
	IP        string
	UserAgent string
	// Elevated is whether this credential has passed its second factor. It is
	// always true behind Authenticate, which refuses anything else; only the
	// partial routes see it false, and only one of them cares — enrolling an
	// authenticator means something different when the session that asked is
	// already signed in.
	Elevated bool

	// FailureReason is filled in by the error writer so the audit middleware
	// can record why a request was rejected.
	FailureReason string
}

// Can fails closed for a scoped key: whatever its role would grant, it holds
// only the capability it was narrowed to.
func (p *Principal) Can(c auth.Capability) bool {
	if p.Scope != "" && string(c) != p.Scope {
		return false
	}
	return p.Role.Can(c)
}

// DeployKey reports whether the request came with a deploy key, or is an MCP
// key's generated tool, which is held to the same rules. A handler asks before
// it answers with something a deploy key is never given, such as a value it
// could otherwise read back, or before a write a deploy key may not make
// whatever the route.
func (p *Principal) DeployKey() bool {
	return p != nil && p.Token != nil && (p.Token.Scope == auth.ScopeDeploy || p.ViaMCP)
}

// KeyID and KeyName are the API key behind a token principal, for the audit
// trail; zero and empty for a session.
func (p *Principal) KeyID() int64 {
	if p == nil || p.Token == nil {
		return 0
	}
	return p.Token.ID
}

func (p *Principal) KeyName() string {
	if p == nil || p.Token == nil {
		return ""
	}
	return p.Token.Name
}

func (p *Principal) Username() string {
	if p == nil || p.User == nil {
		return ""
	}
	return p.User.Username
}

func (p *Principal) UserID() int64 {
	if p == nil || p.User == nil {
		return 0
	}
	return p.User.ID
}

// WithMCPDelegate is the context of a request the MCP endpoint dispatches
// through the router for one of its generated tools, as the MCP key whose
// principal p is. AuthenticateFeatures takes the key from it in place of a
// credential and asks the deploy key gate about it, as about a deploy key:
// the decision table's line for the route, the key's chosen deployments, and
// from there every handler's deploy key rule. A context value is set only by
// code in this process, so no request from the network carries one.
//
// The dispatched request is audited by its own route, so the MCP call's audit
// entry is not reachable from it.
func WithMCPDelegate(ctx context.Context, p *Principal) context.Context {
	ctx = context.WithValue(ctx, delegateKey, p)
	return context.WithValue(ctx, auditCtxKey, &auditInfo{})
}

func mcpDelegate(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(delegateKey).(*Principal)
	return p, ok && p != nil && p.Token != nil && p.Token.Scope == auth.ScopeMCP
}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

func PrincipalFrom(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey).(*Principal)
	return p, ok
}

// MustPrincipal is safe on any route mounted behind Authenticate.
func MustPrincipal(r *http.Request) *Principal {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		return &Principal{Role: auth.RoleReadOnly}
	}
	return p
}
