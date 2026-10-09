package api

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// TestLivePostureUnknownsOnThisHost grades the actual host through the real
// handler and lists the layers its checks could not see. It reads only. Run
// the compiled binary as root so the firewall and the nftables ruleset can be
// read, as the dashboard's backend does.
func TestLivePostureUnknownsOnThisHost(t *testing.T) {
	if os.Getenv("JD_POSTURE_LIVE") != "1" {
		t.Skip("set JD_POSTURE_LIVE=1 to grade this host's posture")
	}
	c, _ := newClient(t)
	w := c.do(http.MethodGet, "/api/v1/security/posture", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("posture: %d %s", w.Code, w.Body.String())
	}
	var posture netsec.Posture
	if err := json.Unmarshal(w.Body.Bytes(), &posture); err != nil {
		t.Fatal(err)
	}
	t.Logf("status %s, %d findings, skipped %v (euid %d)", posture.Status, len(posture.Findings), posture.Skipped, os.Geteuid())
	for _, u := range posture.Unknowns {
		t.Logf("unknown %-9s %s: %s", u.Layer, u.Title, u.Detail)
	}
	if len(posture.Unknowns) == 0 || posture.Unknowns[0].Layer != "provider" {
		t.Fatalf("provider policy must be named unknown: %+v", posture.Unknowns)
	}
}
