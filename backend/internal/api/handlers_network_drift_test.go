package api

import (
	"net/http"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
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
