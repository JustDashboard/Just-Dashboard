package dbx

import (
	"os"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func replicationFrom(t *testing.T, ext string) *MongoReplication {
	t.Helper()
	var raw bson.Raw
	if err := bson.UnmarshalExtJSON([]byte(ext), false, &raw); err != nil {
		t.Fatalf("test reply is not Extended JSON: %v", err)
	}
	return mongoReplicationFrom(raw)
}

// A replSetGetStatus reply as MongoDB 7.0 gave it for a set of one member,
// kept as it arrived so the test does not need a replica set to run.
func TestReplicationStatusOfOneMember(t *testing.T) {
	data, err := os.ReadFile("testdata/mongo_replsetgetstatus.json")
	if err != nil {
		t.Fatal(err)
	}
	status := replicationFrom(t, string(data))
	if !status.ReplicaSet || status.SetName != "jdb5" || status.MyState != "PRIMARY" || len(status.Members) != 1 {
		t.Fatalf("status = %+v", status)
	}
	m := status.Members[0]
	if m.ID != 0 || m.Name != "127.0.0.1:27017" || m.State != "PRIMARY" || !m.Health || !m.Self || m.Uptime != 64 {
		t.Errorf("member = %+v", m)
	}
	if m.OptimeDate != "2026-10-01T10:47:03Z" || m.LagSeconds == nil || *m.LagSeconds != 0 {
		t.Errorf("optime %q, lag %v", m.OptimeDate, m.LagSeconds)
	}
	if m.SyncSource != "" || m.LastHeartbeat != "" || m.Message != "" {
		t.Errorf("a primary with nobody to hear from = %+v", m)
	}
}

// Three members, written by hand in the reply's own shape: lag is measured
// against the primary, a member that is down has none to measure, and an
// entry that is not a member at all does not shift the ones after it.
func TestReplicationStatusMeasuresLag(t *testing.T) {
	status := replicationFrom(t, `{
	  "set": "rs0", "myState": 2,
	  "members": [
	    "not a member",
	    {"_id": 0, "name": "a:27017", "health": 1.0, "state": 1, "stateStr": "PRIMARY", "uptime": 900,
	     "optimeDate": {"$date": "2024-05-01T12:00:30Z"}, "lastHeartbeat": {"$date": "2024-05-01T12:00:31Z"}, "pingMs": {"$numberLong": "2"}},
	    {"_id": 1, "name": "b:27017", "health": 1.0, "state": 2, "stateStr": "SECONDARY", "uptime": 800, "self": true,
	     "optimeDate": {"$date": "2024-05-01T12:00:18Z"}, "syncSourceHost": "a:27017"},
	    {"_id": 2, "name": "c:27017", "health": 0.0, "state": 8, "stateStr": "(not reachable/healthy)", "uptime": 0,
	     "lastHeartbeatMessage": "Error connecting to c:27017 :: caused by :: Connection refused"}
	  ]
	}`)
	if status.MyState != "SECONDARY" || len(status.Members) != 3 {
		t.Fatalf("status = %+v", status)
	}
	primary, secondary, down := status.Members[0], status.Members[1], status.Members[2]
	if primary.LagSeconds == nil || *primary.LagSeconds != 0 || primary.PingMs != 2 || primary.LastHeartbeat != "2024-05-01T12:00:31Z" {
		t.Errorf("primary = %+v", primary)
	}
	if secondary.LagSeconds == nil || *secondary.LagSeconds != 12 || !secondary.Self || secondary.SyncSource != "a:27017" {
		t.Errorf("secondary = %+v", secondary)
	}
	if down.Health || down.LagSeconds != nil || !strings.Contains(down.Message, "Connection refused") {
		t.Errorf("the member that is down = %+v", down)
	}
	// With no primary there is nothing to measure against.
	orphaned := replicationFrom(t, `{"set": "rs0", "members": [
	  {"_id": 1, "name": "b:27017", "health": 1.0, "stateStr": "SECONDARY", "self": true, "optimeDate": {"$date": "2024-05-01T12:00:18Z"}}]}`)
	if len(orphaned.Members) != 1 || orphaned.Members[0].LagSeconds != nil {
		t.Errorf("a set without a primary = %+v", orphaned.Members)
	}
}
