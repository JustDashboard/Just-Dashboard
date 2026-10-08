package api

import (
	"net/http"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

func TestNetworkDriftIsReadableAndOffersNoMutationEndpoint(t *testing.T) {
	_, viewer, _ := networkClients(t)
	w := viewer.do(http.MethodGet, "/api/v1/network/drift", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("inspection = %d %s", w.Code, w.Body.String())
	}
	var report netx.DriftReport
	decodeNetworkBody(t, w.Body.Bytes(), &report)
	if report.CheckedAt.IsZero() || report.RepairPlan.Executable || report.RepairPlan.Items == nil || report.Files == nil || report.Runtime == nil {
		t.Fatalf("inspection schema = %+v", report)
	}
	for _, path := range []string{"/api/v1/network/drift", "/api/v1/network/drift/repair"} {
		w = viewer.do(http.MethodPost, path, `{}`, nil)
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("inspection exposed mutation at %s: %d", path, w.Code)
		}
	}
}

func TestNetworkDriftRepairRequiresAdministratorAndDestructiveCapability(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited, auth.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			s, router := gatewayRouter(t, role, false)
			router.Route("/drift-owner", func(r chi.Router) { s.mountNetworkDriftRoutes(r) })
			response := gwDo(router, http.MethodPost, "/drift-owner/drift/repairs", `{}`)
			want := http.StatusForbidden
			if role == auth.RoleAdmin {
				want = http.StatusBadRequest
			}
			if response.Code != want {
				t.Fatalf("repair authorization %s: %d %s", role, response.Code, response.Body.String())
			}
		})
	}
}

func TestNetworkDriftPendingEnrollmentHasAnExactRouteScope(t *testing.T) {
	for _, path := range []string{"/network/drift/repairs", "/network/drift/repairs/"} {
		if !supportsPendingNetworkApply(path) {
			t.Fatalf("selected repair missing recovery enrollment: %s", path)
		}
	}
	for _, path := range []string{"/network/drift", "/network/drift/repairs/foreign", "/network/drift/repair", "/network/gateway/admission/repair", "/network/dns", "/network/vpn/tailscale"} {
		if supportsPendingNetworkApply(path) {
			t.Fatalf("unserialized owner enrolled: %s", path)
		}
	}
}
