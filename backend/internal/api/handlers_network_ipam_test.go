package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netipam"
	"github.com/go-chi/chi/v5"
)

func ipamRouter(t *testing.T, role auth.Role) (*Server, chi.Router) {
	s := testServer(t)
	s.modules.ipam = netipam.New(s.Store, func(context.Context) (netipam.Snapshot, error) {
		return netipam.Snapshot{CheckedAt: time.Now().UTC(), Observations: []netipam.Observation{}, Coverage: []netipam.Coverage{{Source: "provider", State: "unknown", Detail: "Not authoritative"}}}, nil
	})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), &httpx.Principal{User: &auth.User{ID: 1, Username: "operator"}, Role: role, Kind: "session"})))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	r.Route("/network", s.mountNetworkIPAMRoutes)
	return s, r
}
func ipamDo(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
func TestIPAMAllRoutesRequireAdmin(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		_, r := ipamRouter(t, role)
		for _, item := range []struct{ method, path string }{{"GET", "/network/ipam/"}, {"POST", "/network/ipam/preview"}, {"POST", "/network/ipam/pools"}, {"POST", "/network/ipam/reservations"}, {"DELETE", "/network/ipam/pools/id"}, {"DELETE", "/network/ipam/reservations/id"}} {
			if response := ipamDo(r, item.method, item.path, `{}`); response.Code != 403 {
				t.Fatal(role, item, response.Code, response.Body.String())
			}
		}
	}
}
func TestIPAMPreviewReserveReleaseAreAuditedAndDoNotApplyNativeChanges(t *testing.T) {
	s, r := ipamRouter(t, auth.RoleAdmin)
	response := ipamDo(r, "POST", "/network/ipam/pools", `{"name":"shared","prefix":"10.244.0.0/16","allocationBits":24}`)
	var pool netipam.Pool
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &pool) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	body := string(netipamTestJSON(netipam.ReserveRequest{PoolID: pool.ID, Owner: "docker_network", Resource: "example"}))
	if response = ipamDo(r, "POST", "/network/ipam/reservations", body); response.Code != 409 {
		t.Fatal("unknown coverage implied free space", response.Code, response.Body.String())
	}
	body = string(netipamTestJSON(netipam.ReserveRequest{PoolID: pool.ID, Owner: "docker_network", Resource: "example", AcknowledgeUnknown: true}))
	response = ipamDo(r, "POST", "/network/ipam/reservations", body)
	var reservation netipam.Reservation
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &reservation) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	response = ipamDo(r, "POST", "/network/ipam/preview", `{"prefix":"10.244.0.0/24"}`)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "known_overlap") || !strings.Contains(response.Body.String(), "reserved_plan") {
		t.Fatal(response.Code, response.Body.String())
	}
	response = ipamDo(r, "DELETE", "/network/ipam/reservations/"+reservation.ID, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"nativeChanged":false`) {
		t.Fatal(response.Code, response.Body.String())
	}
	var actions string
	if e := s.Store.DB.QueryRow(`SELECT group_concat(action) FROM audit_log`).Scan(&actions); e != nil || !strings.Contains(actions, "network.ipam.reserve") || !strings.Contains(actions, "network.ipam.release") || !strings.Contains(actions, "network.ipam.preview") {
		t.Fatal("IPAM mutations were unaudited", actions, e)
	}
}
func netipamTestJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }
