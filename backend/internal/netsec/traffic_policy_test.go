package netsec

import "testing"

func TestTrafficPolicyUsesDirectionFamilyAndSource(t *testing.T) {
	status := &FirewallStatus{Available: true, Enabled: true, Backend: BackendUFW, Policy: DefaultPolicy{Outgoing: "allow"}, Rules: []Rule{
		{Number: 1, Direction: "IN", Action: "DENY", To: "443/tcp", Port: "443", Protocol: "tcp", From: "Anywhere"},
		{Number: 2, Direction: "OUT", Action: "DENY", To: "443/tcp", Port: "443", Protocol: "tcp", From: "192.0.2.0/24"},
	}}
	if p := ModelTrafficPolicy(status, "192.0.2.2", "198.51.100.1", "tcp", 443, "out"); p.Verdict != "deny" || p.Rule != 2 || len(p.Limitations) == 0 {
		t.Fatalf("wrong scoped prediction %#v", p)
	}
	if p := ModelTrafficPolicy(status, "203.0.113.1", "198.51.100.1", "tcp", 443, "out"); p.Verdict != "allow" {
		t.Fatalf("source rule overapplied %#v", p)
	}
}

func TestTrafficPolicyOpaqueSelectorsStayUnknown(t *testing.T) {
	for _, rule := range []Rule{{Direction: "OUT", Action: "ALLOW", To: "App Profile", From: "Anywhere"}, {Direction: "OUT", Action: "ALLOW", To: "Anywhere", From: "Anywhere", Protocol: "opaque"}, {Direction: "OUT", Action: "LIMIT", To: "Anywhere", From: "Anywhere"}, {Direction: "OUT", Action: "ALLOW", To: "Anywhere", From: "Anywhere", Raw: "ALLOW OUT on eth0"}} {
		status := &FirewallStatus{Available: true, Enabled: true, Backend: BackendUFW, Policy: DefaultPolicy{Outgoing: "allow"}, Rules: []Rule{rule}}
		if p := ModelTrafficPolicy(status, "192.0.2.2", "198.51.100.1", "tcp", 443, "out"); p.Verdict != "unknown" {
			t.Fatalf("opaque rule became prediction: %#v %#v", rule, p)
		}
	}
}
