package api

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

func nativeManagerRouter(t *testing.T, role auth.Role, kind string) chi.Router {
	t.Helper()
	s := testServer(t)
	s.modules.network = netx.New(netx.Options{Paths: netx.Paths{Dir: filepath.Join(t.TempDir(), "network")}})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 7, Username: "operator"}, Role: role, Kind: kind, SessionID: "native-session", IP: "192.0.2.17"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountNetworkRoutes(r)
	return r
}

func TestNativeManagerRoutesRequireAdminSession(t *testing.T) {
	for _, principal := range []struct {
		role auth.Role
		kind string
	}{{auth.RoleReadOnly, "session"}, {auth.RoleLimited, "session"}, {auth.RoleAdmin, "token"}} {
		r := nativeManagerRouter(t, principal.role, principal.kind)
		for _, route := range []struct{ method, path string }{
			{http.MethodGet, "/network/native/managers"},
			{http.MethodGet, "/network/native/profiles/eth0"},
			{http.MethodPut, "/network/native/profiles/eth0"},
			{http.MethodPost, "/network/changes/pending-one/cleanup"},
		} {
			if response := gwDo(r, route.method, route.path, `{}`); response.Code != http.StatusForbidden {
				t.Fatalf("%s %s accepted %s/%s: %d %s", route.method, route.path, principal.role, principal.kind, response.Code, response.Body.String())
			}
		}
	}
}

func TestNativeManagerWriteRefusesImmediateApplyAndUnknownProperties(t *testing.T) {
	r := nativeManagerRouter(t, auth.RoleAdmin, "session")
	if response := gwDo(r, http.MethodPut, "/network/native/profiles/eth0", `{"generation":"old","intent":{"ipv4":{"method":"manual","addresses":["192.0.2.1/24"]},"ipv6":{"method":"disabled"}}}`); response.Code != http.StatusConflict {
		t.Fatalf("native profile write skipped pending confirmation: %d %s", response.Code, response.Body.String())
	}
	if response := gwDo(r, http.MethodPut, "/network/native/profiles/eth0", `{"generation":"old","intent":{},"filename":"/etc/foreign.network"}`); response.Code != http.StatusBadRequest {
		t.Fatalf("native owner write accepted a request-defined path: %d %s", response.Code, response.Body.String())
	}
	if !supportsPendingNetworkApply("/network/native/profiles/eth0") {
		t.Fatal("native persistent profile mutation lacks its mandatory pending middleware")
	}
}
