package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

func confirmationRouter(t *testing.T, role auth.Role, kind string, userID int64) (*Server, chi.Router, string) {
	t.Helper()
	s := testServer(t)
	original := s.modules.network
	t.Cleanup(func() { s.modules.network = original })
	dir := t.TempDir()
	paths := netx.Paths{Dir: filepath.Join(dir, "network"), Sysctl: filepath.Join(dir, "sysctl"), Unit: filepath.Join(dir, "unit")}
	s.modules.network = netx.New(netx.Options{Paths: paths})
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	j := map[string]any{"id": "pending-one", "phase": "awaiting_confirmation", "generation": strings.Repeat("a", 64), "updatedAt": time.Now(), "watchdog": "armed", "runtime": "applied", "persistence": "written", "boot": "enabled", "ownerUserId": int64(7), "appliedAt": time.Now().Add(-time.Second), "expiresAt": time.Now().Add(time.Minute), "paths": paths, "files": []any{}, "commands": []any{}}
	b, _ := json.Marshal(j)
	if err := os.WriteFile(filepath.Join(paths.Dir, "change.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: userID, Username: "operator"}, Role: role, Kind: kind, SessionID: "authenticated-session", IP: "192.0.2.17"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Route("/network", func(r chi.Router) { s.mountNetworkChangeRoutes(r) })
	return s, r, paths.Dir
}

func TestNetworkConfirmationRoutesRequireAdminSession(t *testing.T) {
	for _, tc := range []struct {
		role auth.Role
		kind string
	}{{auth.RoleReadOnly, "session"}, {auth.RoleAdmin, "token"}} {
		_, router, _ := confirmationRouter(t, tc.role, tc.kind, 7)
		for _, path := range []string{"/network/changes/current", "/network/changes/pending-one/verify", "/network/changes/pending-one/confirm", "/network/changes/pending-one/recover"} {
			method := http.MethodPost
			if strings.HasSuffix(path, "current") {
				method = http.MethodGet
			}
			if response := gwDo(router, method, path, `{"challenge":"invalid"}`); response.Code != http.StatusForbidden {
				t.Fatalf("%s %s allowed %s/%s: %d %s", method, path, tc.role, tc.kind, response.Code, response.Body.String())
			}
		}
	}
}

func TestNetworkConfirmationRequiresReceivedFreshResponse(t *testing.T) {
	_, router, _ := confirmationRouter(t, auth.RoleAdmin, "session", 7)
	if response := gwDo(router, http.MethodPost, "/network/changes/pending-one/confirm", `{"challenge":"no-response"}`); response.Code != http.StatusConflict {
		t.Fatalf("confirmed without fresh response: %d %s", response.Code, response.Body.String())
	}
	verify := gwDo(router, http.MethodPost, "/network/changes/pending-one/verify", "")
	if verify.Code != http.StatusOK || verify.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("verification=%d %s", verify.Code, verify.Body.String())
	}
	var challenge struct {
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(verify.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(challenge)
	confirm := gwDo(router, http.MethodPost, "/network/changes/pending-one/confirm", string(body))
	if confirm.Code != http.StatusOK || !strings.Contains(confirm.Body.String(), `"phase":"confirmed"`) {
		t.Fatalf("confirmation=%d %s", confirm.Code, confirm.Body.String())
	}
	if response := gwDo(router, http.MethodPost, "/network/changes/pending-one/confirm", string(body)); response.Code != http.StatusConflict {
		t.Fatal("confirmation replay succeeded")
	}
}

func TestOtherAdministratorCannotVerifyPendingChange(t *testing.T) {
	_, router, _ := confirmationRouter(t, auth.RoleAdmin, "session", 8)
	if response := gwDo(router, http.MethodPost, "/network/changes/pending-one/verify", ""); response.Code != http.StatusConflict {
		t.Fatalf("different owner verified: %d %s", response.Code, response.Body.String())
	}
	if response := gwDo(router, http.MethodPost, "/network/changes/old-id/verify", ""); response.Code != http.StatusConflict {
		t.Fatalf("old timer ID verified: %d", response.Code)
	}
}

func TestPendingHeaderDoesNotEnrollUnsupportedNetworkOwners(t *testing.T) {
	s, _, _ := confirmationRouter(t, auth.RoleAdmin, "session", 7)
	for _, path := range []string{"/network/vpn/tailscale", "/network/dns", "/network/firewall/rules", "/network/namespaces", "/network/changes/pending-one/confirm", "/network/gateway/admission/repair"} {
		called := false
		handler := s.pendingNetworkApply(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		request.Header.Set(networkApplyHeader, "pending")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || called {
			t.Fatalf("unsupported owner enrolled: %s %d", path, response.Code)
		}
	}
}
