package netpath

import (
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

func pathFixture(owner, prediction string) *Result {
	return &Result{Scope: Scope{Protocol: "tcp", Port: 5432, Address: "192.0.2.10", SourceAddress: "192.0.2.10"}, Evidence: []Evidence{
		{ID: "rules", Title: "Policy rules", Basis: Observed, State: "observed", OwnerPath: "/network/routing", Facts: []Fact{{"Priority 0", "from all to all · lookup table local · mark "}}},
		{ID: "route", Title: "Kernel route", Basis: Observed, State: "observed", OwnerPath: "/network/routing"},
		{ID: "firewall", Title: "Firewall", Basis: Modeled, State: "modeled", Owner: "ufw", OwnerPath: "/network/firewall", Facts: []Fact{{"Adapter prediction", prediction}}, Limitations: []string{"Foreign chains are not modeled."}},
		{ID: "nat", Title: "NAT", Basis: Observed, State: "observed", OwnerPath: "/network/gateway"},
		{ID: "owner", Title: "Destination owner", Basis: Observed, State: owner, OwnerPath: "/ports", Facts: []Fact{{"Process", "postgres"}}},
	}}
}

func TestJoinProbeKeepsLayerBasesAndOwnerLinks(t *testing.T) {
	res := &netsec.ProbeResult{Tool: "route"}
	JoinProbe(res, pathFixture("observed", "allow"), "rules", "firewall", "nat")
	if len(res.Tables) != 1 || len(res.Tables[0].Rows) != 3 || res.Tables[0].Rows[1][1] != "modeled" || res.Tables[0].RowLinks[1] != "/network/firewall" {
		t.Fatalf("table = %+v", res.Tables)
	}
	labels := []string{}
	for _, f := range res.Facts {
		labels = append(labels, f.Label+"="+f.Value+"/"+f.Basis)
	}
	joined := strings.Join(labels, ";")
	if !strings.Contains(joined, "Firewall prediction (tcp/5432)=allow/inferred") || !strings.Contains(joined, "Policy rule 0=") || strings.Contains(joined, "postgres") {
		t.Fatalf("facts = %s", joined)
	}
	if len(res.Links) != 1 || res.Links[0].Href != "/network/investigate" || !strings.Contains(strings.Join(res.Limitations, " "), "Foreign chains") {
		t.Fatalf("links/limits = %+v %+v", res.Links, res.Limitations)
	}
}

func TestCorrelatePortNamesDisagreementsWithoutChoosingACause(t *testing.T) {
	for _, tc := range []struct {
		ok                bool
		owner, prediction string
		want              string
	}{
		{true, "absent", "allow", "no-local-owner"},
		{false, "observed", "allow", "listener-not-reached"},
		{false, "absent", "allow", "nothing-listening"},
		{true, "observed", "deny", "firewall-disagrees"},
		{false, "observed", "reject", "firewall-blocks"},
	} {
		res := &netsec.ProbeResult{Tool: "port", OK: tc.ok, Verdict: netsec.ProbeOK}
		if !tc.ok {
			res.Verdict = netsec.ProbeFailed
		}
		CorrelatePort(res, pathFixture(tc.owner, tc.prediction))
		found := false
		for _, f := range res.Findings {
			found = found || f.ID == tc.want
		}
		if !found {
			t.Errorf("%+v => %+v", tc, res.Findings)
		}
		if tc.ok && res.Verdict != netsec.ProbeFindings {
			t.Errorf("a disagreement left the verdict %q", res.Verdict)
		}
	}
	remote := pathFixture("unknown", "allow")
	remote.Evidence[4].Basis = Unknown
	res := &netsec.ProbeResult{Tool: "port", OK: true, Verdict: netsec.ProbeOK}
	CorrelatePort(res, remote)
	if len(res.Findings) != 0 || !strings.Contains(res.Facts[0].Value, "not local") || res.Verdict != netsec.ProbeOK {
		t.Fatalf("remote = %+v", res)
	}
}
