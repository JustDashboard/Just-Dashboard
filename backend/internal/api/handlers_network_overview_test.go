package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestNetworkOverviewReportsEachReadingItsIdentityFlowsAndHistory reads the
// Overview of whatever host the test runs on. Its contract is the shape:
// both families' identities, an outcome for every reading the attention list
// is judged from, a flow reading with a state, and a history that a second
// read keeps.
func TestNetworkOverviewReportsEachReadingItsIdentityFlowsAndHistory(t *testing.T) {
	c, _ := newClient(t)
	var body struct {
		Identity []struct {
			Family string `json:"family"`
			Public string `json:"public"`
		} `json:"identity"`
		Observations []struct {
			Source string `json:"source"`
			State  string `json:"state"`
			Href   string `json:"href"`
		} `json:"observations"`
		Flows struct {
			State string            `json:"state"`
			Edges []json.RawMessage `json:"edges"`
		} `json:"flows"`
		Findings []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
		} `json:"findings"`
		Incidents []struct {
			FindingID string `json:"findingId"`
		} `json:"incidents"`
		ReadAt string `json:"readAt"`
	}
	for read := 0; read < 2; read++ {
		w := c.do(http.MethodGet, "/api/v1/network/overview", "", nil)
		if w.Code == http.StatusServiceUnavailable {
			t.Skip("this host has no iproute2")
		}
		if w.Code != http.StatusOK {
			t.Fatalf("got %d: %s", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
	if len(body.Identity) != 2 || body.Identity[0].Family != "inet" || body.Identity[1].Family != "inet6" || body.Identity[0].Public == "" {
		t.Fatalf("identity: %+v", body.Identity)
	}
	seen := map[string]bool{}
	for i, o := range body.Observations {
		seen[o.Source] = true
		if o.State != "ok" && o.State != "failed" && o.State != "unavailable" || o.Href == "" {
			t.Fatalf("observation: %+v", o)
		}
		if i > 0 && body.Observations[i-1].Source > o.Source {
			t.Fatal("observations are sorted")
		}
	}
	for _, source := range []string{"client", "history", "firewall", "connections", "gateway", "protection", "forwarding", "identity.inet", "identity.inet6"} {
		if !seen[source] {
			t.Fatalf("no outcome for %s: %+v", source, body.Observations)
		}
	}
	if body.Flows.State == "" || body.Flows.Edges == nil || body.ReadAt == "" {
		t.Fatalf("flows: %+v readAt %q", body.Flows, body.ReadAt)
	}
	if body.Incidents == nil {
		t.Fatal("incidents serialise as an array")
	}
	open := map[string]bool{}
	for _, inc := range body.Incidents {
		open[inc.FindingID] = true
	}
	for _, f := range body.Findings {
		if f.Source == "" {
			t.Fatalf("finding without a source: %+v", f)
		}
		if !open[f.ID] {
			t.Fatalf("finding %s is not in the recorded history %+v", f.ID, body.Incidents)
		}
	}
}
