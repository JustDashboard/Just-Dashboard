package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// A request the MCP endpoint dispatches for a generated tool carries the MCP
// key's principal in its context: the feature group asks the deploy key gate
// about it as about a deploy key, and holds it to every deploy key rule from
// there; a group with no gate refuses it.
func TestAnMCPDelegateIsAdmittedOnlyThroughTheDeployKeyGate(t *testing.T) {
	key := &Principal{Kind: "token", Role: auth.RoleAdmin, TokenID: 7, Token: &auth.APIToken{ID: 7, Scope: auth.ScopeMCP, Name: "claude"}}
	a := &Authenticator{}
	var seen *Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = MustPrincipal(r) })
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/deploy/1/environments", nil)
		return r.WithContext(WithMCPDelegate(r.Context(), key))
	}

	asked := 0
	admit := func(r *http.Request, p *Principal) error { asked++; return nil }
	w := httptest.NewRecorder()
	a.AuthenticateFeatures(admit, nil)(next).ServeHTTP(w, request())
	if w.Code != http.StatusOK || asked != 1 || seen == nil || !seen.ViaMCP || !seen.DeployRoute || !seen.DeployKey() || seen.TokenID != 7 {
		t.Fatalf("admitted = %d, asked %d, principal %+v", w.Code, asked, seen)
	}
	if key.ViaMCP || key.DeployRoute {
		t.Error("admission changed the MCP request's own principal")
	}

	seen = nil
	refuse := func(r *http.Request, p *Principal) error {
		return Err(http.StatusForbidden, "key_scope", "this deploy key was not given this deployment")
	}
	w = httptest.NewRecorder()
	a.AuthenticateFeatures(refuse, nil)(next).ServeHTTP(w, request())
	if w.Code != http.StatusForbidden || seen != nil || !strings.Contains(w.Body.String(), "not given this deployment") {
		t.Fatalf("refused = %d %s", w.Code, w.Body.String())
	}

	for name, group := range map[string]func(http.Handler) http.Handler{
		"Authenticate":       a.Authenticate,
		"AuthenticateScoped": a.AuthenticateScoped,
		"no gate":            a.AuthenticateFeatures(nil, nil),
	} {
		w = httptest.NewRecorder()
		group(next).ServeHTTP(w, request())
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "key_scope") || seen != nil {
			t.Errorf("%s admitted an MCP key's tool: %d %s", name, w.Code, w.Body.String())
		}
	}

	// An MCP key is a deploy key for a handler only when it came this way.
	if (&Principal{Token: key.Token}).DeployKey() {
		t.Error("an MCP key at its own endpoint reads as a deploy key")
	}
}
