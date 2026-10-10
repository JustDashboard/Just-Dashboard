package dnsservice

import (
	"fmt"
	"testing"
)

func TestDNSServiceRetainedHistoryIsBoundedPrivateMetadataWithoutNativeAccess(t *testing.T) {
	s, _, _, view := newServiceFixture(t, true)
	enabled := false
	plan, err := s.Preview(t.Context(), view.Connection.ID, ChangeRequest{Action: "protection", Protection: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 65 {
		if _, err = s.db.Exec(`INSERT INTO network_dns_service_changes(id,connection_id,generation,request_json,before_json,state,created_at,expires_at) SELECT ?,connection_id,generation,request_json,before_json,'verified',?,expires_at FROM network_dns_service_changes WHERE id=?`, fmt.Sprintf("%032x", i+1), i+1, plan.ID); err != nil {
			t.Fatal(err)
		}
	}
	s.open = func(string) (string, error) {
		t.Fatal("history opened native credentials instead of reading retained private metadata")
		return "", nil
	}
	rows, err := s.Changes(t.Context(), view.Connection.ID)
	if err != nil || len(rows) != 64 || rows[0].ID != plan.ID {
		t.Fatal("retained review ordering/bound changed", len(rows), err)
	}
	for _, row := range rows {
		if row.Before != nil || row.After != nil || row.ConnectionID != view.Connection.ID || row.Request.Action != "protection" {
			t.Fatal("history expanded native snapshots or mixed connection ownership")
		}
	}
	detail, err := s.Change(t.Context(), plan.ID)
	if err != nil || detail.Before == nil || len(detail.Before.Queries) != 0 || detail.State != "planned" {
		t.Fatal("explicit retained detail lost its reviewed baseline or exposed query history", err)
	}
}
