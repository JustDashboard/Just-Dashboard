package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The network overview retains each native reading and omits retired advisory payloads.
func TestNetworkOverviewReportsNativeReadingsWithoutAdvisories(t *testing.T) {
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
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"findings", "incidents", "incidentsError"} {
			if _, exists := payload[field]; exists {
				t.Errorf("retired advisory field %s remains", field)
			}
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
	for _, source := range []string{"client", "firewall", "connections", "gateway", "protection", "forwarding", "identity.inet", "identity.inet6"} {
		if !seen[source] {
			t.Fatalf("no outcome for %s: %+v", source, body.Observations)
		}
	}
	if body.Flows.State == "" || body.Flows.Edges == nil || body.ReadAt == "" {
		t.Fatalf("flows: %+v readAt %q", body.Flows, body.ReadAt)
	}

}

// A native read does not require a browser client address.
func TestNetworkOverviewReadWithoutBrowserClient(t *testing.T) {
	s := testServer(t)
	out, err := s.readNetworkOverview(t.Context(), "")
	if err != nil {
		t.Skipf("this host cannot read its devices: %v", err)
	}
	if out.Client.Address != "" || out.ReadAt.IsZero() {
		t.Fatalf("native read: %+v", out.Client)
	}
}
