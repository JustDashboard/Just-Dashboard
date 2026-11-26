package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// The order invariant 2 writes: the factors first, then the password change.
// Only a session past its factors and still owing the change is refused.
func TestRefuseOwedPasswordChangeRefusesOnlyACompleteSessionOwingTheChange(t *testing.T) {
	for _, tc := range []struct {
		name    string
		p       *Principal
		refused bool
	}{
		{"owes its factor and the change", &Principal{Kind: "session", User: &auth.User{MustChangePW: true}}, false},
		{"gave its factors, owes the change", &Principal{Kind: "session", Elevated: true, User: &auth.User{MustChangePW: true}}, true},
		{"gave its factors, owes nothing", &Principal{Kind: "session", Elevated: true, User: &auth.User{}}, false},
		{"owes its factor only", &Principal{Kind: "session", User: &auth.User{}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true })
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
			w := httptest.NewRecorder()
			RefuseOwedPasswordChange(next).ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), tc.p)))
			if tc.refused && (reached || w.Code != http.StatusForbidden) {
				t.Fatalf("reached = %v, status %d %s; want 403 password_change_required", reached, w.Code, w.Body.String())
			}
			if !tc.refused && !reached {
				t.Fatalf("refused with %d %s", w.Code, w.Body.String())
			}
		})
	}
}
