package netx

import (
	"context"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func incidentService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := testService(t)
	s.db = st.DB
	return s
}

func TestIncidentsResolveOnlyWhenTheirReadingSucceeded(t *testing.T) {
	s := incidentService(t)
	ctx := context.Background()
	start := time.Unix(1_800_000_000, 0)
	firewall := Finding{ID: "firewall.off", Level: "warning", Title: "off", Href: "/network/firewall", Source: "firewall"}
	carrier := Finding{ID: "link.carrier.ens3", Level: "critical", Title: "no carrier", Source: "links"}
	ok := []Observation{{Source: "firewall", State: "ok"}, {Source: "links", State: "ok"}}
	if err := s.RecordFindings(ctx, start, []Finding{firewall}, ok); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFindings(ctx, start.Add(time.Minute), []Finding{firewall, carrier}, ok); err != nil {
		t.Fatal(err)
	}
	failedFirewall := []Observation{{Source: "firewall", State: "failed", Error: "ufw: timeout"}, {Source: "links", State: "ok"}}
	if err := s.RecordFindings(ctx, start.Add(2*time.Minute), []Finding{carrier}, failedFirewall); err != nil {
		t.Fatal(err)
	}
	got, err := s.Incidents(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("two incidents, newest first: %+v", got)
	}
	fw := got[1]
	if fw.FindingID != "firewall.off" || fw.ResolvedAt != nil || fw.UnobservedSince == nil || !fw.UnobservedSince.Equal(start.Add(2*time.Minute)) {
		t.Fatalf("a failed reading must leave its incident open and unobserved, not resolved: %+v", fw)
	}
	if got[0].FindingID != "link.carrier.ens3" || len(got[0].Related) != 1 || got[0].Related[0] != fw.ID {
		t.Fatalf("incidents opening within the window are correlated: %+v", got[0])
	}
	if err := s.RecordFindings(ctx, start.Add(10*time.Minute), nil, ok); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Incidents(ctx, 50)
	for _, inc := range got {
		if inc.ResolvedAt == nil || !inc.ResolvedAt.Equal(start.Add(10*time.Minute)) {
			t.Fatalf("a successful reading without the finding resolves it: %+v", inc)
		}
	}
	if err := s.RecordFindings(ctx, start.Add(20*time.Minute), []Finding{firewall}, ok); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Incidents(ctx, 50)
	if len(got) != 3 || got[0].ResolvedAt != nil || len(got[0].Related) != 0 {
		t.Fatalf("a recurrence is a new, uncorrelated incident: %+v", got)
	}
}

func TestIncidentsPruneOldResolvedHistory(t *testing.T) {
	s := incidentService(t)
	ctx := context.Background()
	start := time.Unix(1_800_000_000, 0)
	f := Finding{ID: "dns.plain", Level: "notice", Source: "dns"}
	ok := []Observation{{Source: "dns", State: "ok"}}
	if err := s.RecordFindings(ctx, start, []Finding{f}, ok); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFindings(ctx, start.Add(time.Hour), nil, ok); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFindings(ctx, start.Add(incidentRetention+2*time.Hour), nil, ok); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Incidents(ctx, 50)
	if len(got) != 0 {
		t.Fatalf("resolved incidents past retention are removed: %+v", got)
	}
}

func TestIncidentsWithoutADatabaseAreEmpty(t *testing.T) {
	s := testService(t)
	if err := s.RecordFindings(context.Background(), time.Now(), []Finding{{ID: "x"}}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Incidents(context.Background(), 10)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}
