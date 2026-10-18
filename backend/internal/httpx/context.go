package httpx

import (
	"context"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

type ctxKey int

const principalKey ctxKey = iota

// Principal is the authenticated identity behind a request, whether it arrived
// with a session cookie or an API token.
type Principal struct {
	User      *auth.User
	Role      auth.Role
	SessionID string
	TokenID   int64
	Kind      string // "session" | "token"
	// Scope is the one capability a scoped API key holds; empty for a session
	// and for an ordinary key.
	Scope     string
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
