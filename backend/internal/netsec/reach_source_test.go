package netsec

import (
	"net/netip"
	"testing"
)

// One address is judged by the first rule whose source holds it: a rule for
// an office range admits that range and the default refuses the rest, a deny
// for one address refuses it ahead of a later allow, and a rule on an
// interface cannot be judged for an address and is counted as passed over.
func TestJudgeFirewallFromOneSource(t *testing.T) {
	firewall := &FirewallStatus{Available: true, Enabled: true, Backend: BackendUFW,
		Policy: DefaultPolicy{Incoming: "deny"},
		Rules: []Rule{
			{Number: 1, Action: "DENY", Port: "443", Protocol: "tcp", From: "203.0.113.9", Direction: "IN"},
			{Number: 2, Action: "ALLOW", Port: "443", Protocol: "tcp", From: "203.0.113.0/24", Direction: "IN"},
			{Number: 3, Action: "ALLOW", Port: "443", Protocol: "tcp", From: "Anywhere on tailscale0", Direction: "IN"},
			{Number: 4, Action: "ALLOW", Port: "443", Protocol: "tcp", From: "2001:db8::/32", Direction: "IN", IPv6: true},
		}}
	socket := ExposedPort{Port: 443, Protocol: "tcp", Address: "0.0.0.0"}
	for _, tc := range []struct {
		source  string
		verdict Verdict
		rule    int
		skipped int
	}{
		{"203.0.113.9", VerdictBlocked, 1, 0},
		{"203.0.113.10", VerdictAllowed, 2, 0},
		{"198.51.100.4", VerdictBlocked, 0, 1},
		{"2001:db8::7", VerdictAllowed, 4, 0},
	} {
		v, skipped := JudgeFirewallFrom(socket, HostNetwork{}, firewall, netip.MustParseAddr(tc.source))
		if v.Verdict != tc.verdict || v.Rule != tc.rule || skipped != tc.skipped {
			t.Errorf("%s: verdict %s rule %d skipped %d, want %s %d %d", tc.source, v.Verdict, v.Rule, skipped, tc.verdict, tc.rule, tc.skipped)
		}
	}
	off := *firewall
	off.Enabled = false
	if v, _ := JudgeFirewallFrom(socket, HostNetwork{}, &off, netip.MustParseAddr("198.51.100.4")); v.Verdict != VerdictOff {
		t.Fatalf("an inactive firewall = %s", v.Verdict)
	}
	if v, _ := JudgeFirewallFrom(socket, HostNetwork{}, nil, netip.MustParseAddr("198.51.100.4")); v.Verdict != VerdictUnknown {
		t.Fatalf("an unread firewall = %s", v.Verdict)
	}
}
