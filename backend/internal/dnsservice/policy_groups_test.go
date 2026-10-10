package dnsservice

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDNSServiceClientGroupJSONRejectsNullAndKeepsExplicitZeroAndEmpty(t *testing.T) {
	for _, groups := range []string{"[null]", "[0,null]", "[true]", "[\"0\"]", "[0.0]", "null"} {
		var req ChangeRequest
		if json.Unmarshal([]byte(`{"action":"client_groups","client":{"address":"198.51.100.77","groups":`+groups+`}}`), &req) == nil {
			t.Fatalf("malformed request groups %s decoded", groups)
		}
	}
	var unknown ChangeRequest
	if json.Unmarshal([]byte(`{"action":"client_groups","client":{"address":"198.51.100.77","groups":[],"nativePayload":true}}`), &unknown) == nil {
		t.Fatal("custom decoder admitted an unknown nested field")
	}
	for _, groups := range [][]int{{}, {0}} {
		s, connection, f := newPolicyFixture(t, PiHole)
		body, _ := json.Marshal(ChangeRequest{Action: "client_groups", Client: &ClientGroupChange{Address: "198.51.100.77", Groups: groups}})
		var req ChangeRequest
		if err := json.Unmarshal(body, &req); err != nil || req.Client.Groups == nil {
			t.Fatal("explicit valid membership was lost", err)
		}
		plan, err := s.Preview(t.Context(), connection.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		plan, err = s.Apply(t.Context(), plan.ID)
		if err != nil || plan.State != "verified" || !reflect.DeepEqual(plan.After.SelectedClient.Groups, groups) || plan.Before.SelectedClient.CommentFingerprint != plan.After.SelectedClient.CommentFingerprint || f.writes != 1 {
			t.Fatal("valid empty/zero membership or native comment was changed", err, plan.State)
		}
	}
}

func TestDNSServiceClientGroupMalformedNativePolicyRemainsUnknownAndRefused(t *testing.T) {
	for _, malformed := range []string{"client-null-group", "client-bool-group", "client-decimal-group", "client-missing-groups", "group-null-id", "group-missing-id", "group-null-enabled", "group-negative-id", "group-duplicate-id"} {
		t.Run(malformed, func(t *testing.T) {
			s, connection, f := newPolicyFixture(t, PiHole)
			switch malformed {
			case "client-null-group":
				f.clients[0]["groups"] = []any{nil}
			case "client-bool-group":
				f.clients[0]["groups"] = []any{true}
			case "client-decimal-group":
				f.clients[0]["groups"] = []any{0.5}
			case "client-missing-groups":
				delete(f.clients[0], "groups")
			case "group-null-id":
				f.groups[0]["id"] = nil
			case "group-missing-id":
				delete(f.groups[0], "id")
			case "group-null-enabled":
				f.groups[0]["enabled"] = nil
			case "group-negative-id":
				f.groups[0]["id"] = -1
			case "group-duplicate-id":
				f.groups[1]["id"] = 0
			}
			view, err := s.Inspect(t.Context(), connection.ID)
			if err != nil || view.State != "available" || view.Snapshot == nil {
				t.Fatal("unrelated native configuration became unavailable", err)
			}
			if len(malformed) >= 6 && malformed[:6] == "client" {
				if view.Snapshot.ClientEvidence.State != "unknown" || len(view.Snapshot.Clients) != 0 {
					t.Fatal("malformed membership became a configured native client")
				}
			} else if view.Snapshot.AppClientEvidence.State != "unknown" || len(view.Snapshot.FilterGroups) != 0 {
				t.Fatal("malformed group identity became a configured or partial inventory")
			}
			request := ChangeRequest{Action: "client_groups", Client: &ClientGroupChange{Address: "198.51.100.77", Groups: []int{}}}
			if _, err = s.Preview(t.Context(), connection.ID, request); err == nil || f.writes != 0 {
				t.Fatal("malformed native policy established a replacement baseline")
			}
		})
	}
}
