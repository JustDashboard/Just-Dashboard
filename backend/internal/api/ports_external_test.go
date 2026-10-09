package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netvantage"
)

func TestExternalPortEvidenceJoinsSourcesAndKeepsUnmeasuredChecksUnknown(t *testing.T) {
	enrolled := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	vantages := []netvantage.Vantage{
		{ID: "a", Name: "Probe A", Location: "Region A", Placement: "external_host", EnrolledAt: &enrolled,
			Scopes: []netvantage.Scope{{ID: "service", Target: "shop.example", Addresses: []string{"203.0.113.9"}, Ports: []int{443, 5432}, Families: []string{"inet"}}}},
		{ID: "b", Name: "Fixture", Placement: "controlled_fixture", EnrolledAt: &enrolled, RevokedAt: &enrolled,
			Scopes: []netvantage.Scope{{ID: "service", Target: "lab", Addresses: []string{"192.0.2.1"}, Ports: []int{22}, Families: []string{"inet"}}}},
	}
	measured := enrolled.Add(time.Hour)
	checks := []netvantage.Check{
		{ID: "c1", VantageID: "a", Status: "completed", CreatedAt: measured, Request: netvantage.Request{VantageID: "a", ScopeID: "service", Family: "inet", Port: 5432},
			Result: &netvantage.Result{Address: "203.0.113.9", EndedAt: measured, Stages: []netvantage.Stage{{Name: "dns", Basis: "observed", State: "not_applicable"}, {Name: "tcp", Basis: "measured", State: "connected", Detail: "connected in 31 ms"}}}},
		{ID: "c2", VantageID: "a", Status: "expired", CreatedAt: measured.Add(time.Minute), Request: netvantage.Request{VantageID: "a", ScopeID: "service", Family: "inet", Port: 443}},
		{ID: "c3", VantageID: "gone", Status: "completed", CreatedAt: enrolled, Request: netvantage.Request{Port: 22},
			Result: &netvantage.Result{Address: "198.51.100.4", EndedAt: enrolled, Stages: []netvantage.Stage{{Name: "tcp", Basis: "measured", State: "failed"}}}},
	}
	local := func(address string) bool { return address == "198.51.100.4" }
	out := externalPortEvidence(vantages, checks, local)
	if len(out.Scopes) != 1 || out.Scopes[0].Source != "Probe A" || len(out.Scopes[0].Ports) != 2 {
		t.Fatalf("only enrolled, unrevoked sources offer scopes: %+v", out.Scopes)
	}
	if len(out.Evidence) != 3 || out.Evidence[0].CheckID != "c2" {
		t.Fatalf("evidence is newest first: %+v", out.Evidence)
	}
	byID := map[string]portExternalEvidence{}
	for _, e := range out.Evidence {
		byID[e.CheckID] = e
	}
	if e := byID["c1"]; e.State != "connected" || e.Basis != "measured" || e.Local || e.Source != "Probe A" || e.Placement != "external_host" {
		t.Fatalf("a measured connection through an address not on this host: %+v", e)
	}
	if e := byID["c2"]; e.State != "unknown" || e.Basis != "unknown" || e.Status != "expired" {
		t.Fatalf("an expired check is unknown, never a failed measurement: %+v", e)
	}
	if e := byID["c3"]; e.Source != "Removed source" || !e.Local || e.State != "failed" {
		t.Fatalf("a check from a removed source keeps its measurement: %+v", e)
	}
}

func TestPortsExternalIsAnAdminsReading(t *testing.T) {
	for _, test := range []struct {
		role auth.Role
		want int
	}{{auth.RoleReadOnly, http.StatusForbidden}, {auth.RoleAdmin, http.StatusOK}} {
		s, router := gatewayRouter(t, test.role, false)
		router.Route("/ports", s.mountPortRoutes)
		if rec := gwDo(router, http.MethodGet, "/ports/external", ""); rec.Code != test.want {
			t.Fatalf("%s: %d %s", test.role, rec.Code, rec.Body.String())
		}
	}
}
