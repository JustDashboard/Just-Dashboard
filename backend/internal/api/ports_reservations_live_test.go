package api

import (
	"os"
	"testing"
)

// TestLiveFreePortPolicyOnThisHost reads every claim the free-port search
// consults on the actual host — leases, the preview and ephemeral ranges,
// the firewall's inbound rules and the gateway's forwards — for a wildcard
// TCP search, and lists which ports each would pass over. It binds nothing.
// Run the compiled binary as root so the firewall can be read.
func TestLiveFreePortPolicyOnThisHost(t *testing.T) {
	if os.Getenv("JD_PORT_POLICY_LIVE") != "1" {
		t.Skip("set JD_PORT_POLICY_LIVE=1 to read this host's free-port policy")
	}
	s := testServer(t)
	policy := s.freePortPolicy(t.Context(), "tcp", "0.0.0.0")
	for _, source := range policy.sources {
		t.Logf("source %-12s %-13s %s", source.Key, source.State, source.Detail)
	}
	bySource := map[string][]portReservation{}
	for port := 1; port <= 65535; port++ {
		if r, ok := policy.reserved(port); ok {
			bySource[r.Source] = append(bySource[r.Source], r)
		}
	}
	for source, reservations := range bySource {
		t.Logf("%s passes over %d ports; first: %d %s", source, len(reservations), reservations[0].Port, reservations[0].Detail)
		if source == "firewall" || source == "gateway" {
			for _, r := range reservations[:min(8, len(reservations))] {
				t.Logf("    %d %s", r.Port, r.Detail)
			}
		}
	}
	for _, source := range policy.sources {
		if source.Key == "provider" && source.State != portSourceNotSupplied {
			t.Fatalf("provider reservations have no adapter: %+v", source)
		}
	}
}
