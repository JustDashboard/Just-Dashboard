package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

func TestNetworkCreationGatesHostDriversOptionsAndReservedOwnerLabels(t *testing.T) {
	for _, test := range []struct {
		name string
		role auth.Role
		body string
		want int
	}{
		{"reader", auth.RoleReadOnly, `{"name":"test"}`, http.StatusForbidden},
		{"limited bridge", auth.RoleLimited, `{"name":"test"}`, http.StatusCreated},
		{"limited driver", auth.RoleLimited, `{"name":"test","driver":"macvlan"}`, http.StatusForbidden},
		{"limited options", auth.RoleLimited, `{"name":"test","options":{"parent":"eth0"}}`, http.StatusForbidden},
		{"admin driver", auth.RoleAdmin, `{"name":"test","driver":"macvlan","options":{"parent":"eth0"}}`, http.StatusCreated},
		{"reserved deployment", auth.RoleAdmin, `{"name":"test","labels":{"io.just-dashboard.managed":"true"}}`, http.StatusBadRequest},
		{"reserved compose", auth.RoleAdmin, `{"name":"test","labels":{"com.docker.compose.project":"foreign"}}`, http.StatusBadRequest},
		{"invalid allocation", auth.RoleAdmin, `{"name":"test","subnet":"192.0.2.0/24","gateway":"198.51.100.1"}`, http.StatusBadRequest},
		{"observed client range", auth.RoleAdmin, `{"name":"test","subnet":"192.0.2.0/24"}`, http.StatusConflict},
		{"mapped observed client range", auth.RoleLimited, `{"name":"test","subnet":"192.0.2.0/24"}`, http.StatusConflict},
		{"observed IPv6 client range", auth.RoleAdmin, `{"name":"test","ipv6":true,"ipam":[{"subnet":"2001:db8::/64"}]}`, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, router := gatewayRouter(t, test.role, false)
			calls := 0
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/_ping" {
					w.Header().Set("API-Version", "1.47")
					return
				}
				calls++
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/networks/create") {
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"Id":"fixture"}`))
					return
				}
				if strings.HasSuffix(r.URL.Path, "/networks/fixture") {
					_, _ = w.Write([]byte(`{"Id":"fixture","Name":"test","Driver":"bridge"}`))
					return
				}
				t.Errorf("unexpected Engine request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}))
			defer engine.Close()
			original := s.modules.docker
			s.modules.docker = dockerx.New(engine.URL)
			t.Cleanup(func() { _ = s.modules.docker.Close(); s.modules.docker = original })
			s.mountDockerRoutes(router)
			request := httptest.NewRequest(http.MethodPost, "/docker/networks/", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.RemoteAddr = "198.51.100.9:43117"
			if test.name == "observed client range" {
				request.RemoteAddr = "192.0.2.17:43117"
			}
			if test.name == "mapped observed client range" {
				request.RemoteAddr = "[::ffff:192.0.2.17]:43117"
			}
			if test.name == "observed IPv6 client range" {
				request.RemoteAddr = "[2001:db8::17]:43117"
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("create = %d %s, want %d", response.Code, response.Body.String(), test.want)
			}
			if test.want != http.StatusCreated && calls != 0 {
				t.Fatalf("refused request reached the Engine %d times", calls)
			}
		})
	}
}
