package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/go-chi/chi/v5"
)

func investigatorRouter(t *testing.T, role auth.Role) (*Server, chi.Router) {
	t.Helper()
	s := testServer(t)
	origNetwork, origSecurity, origDocker := s.modules.network, s.modules.netsec, s.modules.docker
	t.Cleanup(func() { s.modules.network, s.modules.netsec, s.modules.docker = origNetwork, origSecurity, origDocker })
	s.modules.network, s.modules.netsec, s.modules.docker = nil, nil, nil
	r := chi.NewRouter()
	r.Use(httpx.AuditMutations(s.Audit))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: role, Kind: "session", IP: "127.0.0.1"})))
		})
	})
	r.Route("/network", s.mountNetworkInvestigatorRoutes)
	return s, r
}

func TestInvestigatorRequiresAdminOnEveryRoute(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		_, r := investigatorRouter(t, role)
		for _, route := range []struct{ method, path, body string }{{"GET", "/network/investigate/sources", ""}, {"POST", "/network/investigate", `{"sourceKind":"host","target":"private.corp","family":"inet","protocol":"tcp","port":443,"measure":true}`}} {
			if rec := gwDo(r, route.method, route.path, route.body); rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s role %s: %d %s", route.method, route.path, role, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestInvestigatorRejectsExecutablePIDAndInvalidTuple(t *testing.T) {
	_, r := investigatorRouter(t, auth.RoleAdmin)
	for _, body := range []string{
		`{"sourceKind":"host","target":"private.corp","family":"inet","protocol":"tcp","port":443,"pid":1}`,
		`{"sourceKind":"host","target":"private.corp","family":"inet","protocol":"tcp","port":443,"program":"sh"}`,
		`{"sourceKind":"host","target":"--help","family":"inet","protocol":"tcp","port":443}`,
		`{"sourceKind":"container","containerId":"short","target":"private.corp","family":"inet","protocol":"tcp","port":443}`,
	} {
		if rec := gwDo(r, "POST", "/network/investigate", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("unsafe request accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestInvestigatorUnavailablePrivateScopeDoesNotSubstitute(t *testing.T) {
	s, r := investigatorRouter(t, auth.RoleAdmin)
	rec := gwDo(r, "POST", "/network/investigate", `{"sourceKind":"host","target":"private.corp","family":"inet","protocol":"tcp","port":443,"measure":true}`)
	var result netpath.Result
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &result) != nil {
		t.Fatalf("response %d %s", rec.Code, rec.Body.String())
	}
	if result.Scope.Address != "" || result.Measurement != nil || result.Evidence[0].Basis != netpath.Unknown || !strings.Contains(result.Evidence[0].Summary, "no fallback") {
		t.Fatalf("scope disappeared: %#v", result)
	}
	for _, e := range result.Evidence[1:] {
		if e.State != "skipped" {
			t.Fatalf("unresolved private target progressed: %#v", e)
		}
	}
	var detail string
	if err := s.Store.DB.QueryRow(`SELECT detail FROM audit_log WHERE action='network.path.investigate' AND target='private.corp'`).Scan(&detail); err != nil || !strings.Contains(detail, `"measure":true`) || !strings.Contains(detail, `"sourceKind":"host"`) {
		t.Fatalf("bounded probe request was not audited: %q %v", detail, err)
	}
}

func TestInvestigatorMissingDockerInventoryIsExplicit(t *testing.T) {
	_, r := investigatorRouter(t, auth.RoleAdmin)
	if rec := gwDo(r, "GET", "/network/investigate/sources", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Docker inventory is unavailable") {
		t.Fatalf("missing owner became empty inventory %d %s", rec.Code, rec.Body.String())
	}
	if rec := gwDo(r, "POST", "/network/investigate", `{"sourceKind":"container","containerId":"`+strings.Repeat("a", 64)+`","target":"192.0.2.8","family":"inet","protocol":"tcp","port":443}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "no host-source substitute") {
		t.Fatalf("container silently became host %d %s", rec.Code, rec.Body.String())
	}
}
