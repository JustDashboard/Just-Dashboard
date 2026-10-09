package netsec

import (
	"net/netip"
	"testing"
)

// One address is judged by the first rule whose source holds it: a rule for
// an office range admits that range and the default refuses the rest, a deny
// for one address refuses it ahead of a later allow. A rule ufw limits to an
// interface — what `ufw allow in on tailscale0` prints — cannot be judged for
// an address, so it is passed over and returned rather than dropped: the
// default's refusal behind it is not the answer for a tailnet address.
func TestJudgeFirewallFromOneSource(t *testing.T) {
	firewall := &FirewallStatus{Available: true, Enabled: true, Backend: BackendUFW,
		Policy: DefaultPolicy{Incoming: "deny"},
		Rules: ufwRules(
			"[ 1] 443/tcp                    DENY IN     203.0.113.9",
			"[ 2] 443/tcp                    ALLOW IN    203.0.113.0/24",
			"[ 3] 22/tcp on tailscale0       ALLOW IN    Anywhere",
			"[ 4] 443/tcp on tailscale0      ALLOW IN    Anywhere",
			"[ 5] 443/tcp (v6)               ALLOW IN    2001:db8::/32 (v6)",
		)}
	socket := ExposedPort{Port: 443, Protocol: "tcp", Address: "0.0.0.0"}
	for _, tc := range []struct {
		source    string
		verdict   Verdict
		rule      int
		passed    []int
		undecided []int
	}{
		{"203.0.113.9", VerdictBlocked, 1, nil, nil},
		{"203.0.113.10", VerdictAllowed, 2, nil, nil},
		// Rule 3 is for another port and says nothing; rule 4 may decide.
		{"100.101.1.2", VerdictBlocked, 0, []int{4}, []int{4}},
		{"2001:db8::7", VerdictAllowed, 5, nil, nil},
	} {
		v, passed := JudgeFirewallFrom(socket, HostNetwork{}, firewall, netip.MustParseAddr(tc.source), nil)
		undecided := FirewallUndecided(v, passed)
		if v.Verdict != tc.verdict || v.Rule != tc.rule || !sameNumbers(passed, tc.passed) || !sameNumbers(undecided, tc.undecided) {
			t.Errorf("%s: verdict %s rule %d passed %v undecided %v, want %s %d %v %v",
				tc.source, v.Verdict, v.Rule, passed, undecided, tc.verdict, tc.rule, tc.passed, tc.undecided)
		}
	}

	off := *firewall
	off.Enabled = false
	if v, _ := JudgeFirewallFrom(socket, HostNetwork{}, &off, netip.MustParseAddr("198.51.100.4"), nil); v.Verdict != VerdictOff {
		t.Fatalf("an inactive firewall = %s", v.Verdict)
	}
	if v, _ := JudgeFirewallFrom(socket, HostNetwork{}, nil, netip.MustParseAddr("198.51.100.4"), nil); v.Verdict != VerdictUnknown {
		t.Fatalf("an unread firewall = %s", v.Verdict)
	}
}

// A ufw application profile is judged by the profile's own ports where the
// host defines it, and passed over where it does not.
func TestJudgeFirewallFromReadsUFWProfiles(t *testing.T) {
	firewall := &FirewallStatus{Available: true, Enabled: true, Backend: BackendUFW,
		Policy: DefaultPolicy{Incoming: "deny"},
		Rules: ufwRules(
			"[ 1] OpenSSH                    ALLOW IN    Anywhere",
			"[ 2] Nginx Full                 ALLOW IN    Anywhere",
		)}
	socket := ExposedPort{Port: 443, Protocol: "tcp", Address: "0.0.0.0"}
	source := netip.MustParseAddr("198.51.100.4")
	defined := func(name string) ([]string, bool) {
		ports, ok := map[string][]string{"OpenSSH": {"22/tcp"}, "Nginx Full": {"80,443/tcp"}}[name]
		return ports, ok
	}
	if v, passed := JudgeFirewallFrom(socket, HostNetwork{}, firewall, source, defined); v.Verdict != VerdictAllowed || v.Rule != 2 || len(passed) != 0 {
		t.Fatalf("with the profiles: %s rule %d passed %v", v.Verdict, v.Rule, passed)
	}
	none := func(string) ([]string, bool) { return nil, false }
	v, passed := JudgeFirewallFrom(socket, HostNetwork{}, firewall, source, none)
	if v.Verdict != VerdictBlocked || !sameNumbers(FirewallUndecided(v, passed), []int{1, 2}) {
		t.Fatalf("without them: %s passed %v", v.Verdict, passed)
	}
}

// iptables prints the input interface and the match text of each rule. A
// portless rule from a range refuses that range on every port; a rule for
// established connections never meets a new one; a rule on loopback is only
// for a loopback source; a rule on another interface, or a jump to a chain
// that is not read, may decide and is passed over.
func TestJudgeFirewallFromReadsIPTablesListing(t *testing.T) {
	withIPTablesOutput(t, `Chain INPUT (policy ACCEPT 0 packets, 0 bytes)
num   pkts bytes target     prot opt in     out     source               destination
1        0     0 ACCEPT     all  --  *      *       0.0.0.0/0            0.0.0.0/0            ctstate RELATED,ESTABLISHED
2        0     0 ACCEPT     all  --  lo     *       0.0.0.0/0            0.0.0.0/0
3        0     0 DROP       all  --  *      *       198.51.100.0/24      0.0.0.0/0
4        0     0 DROP       tcp  --  eth1   *       0.0.0.0/0            0.0.0.0/0            tcp dpt:443
5        0     0 f2b-nginx  tcp  --  *      *       0.0.0.0/0            0.0.0.0/0            multiport dports 80,443
6        0     0 REJECT     tcp  --  *      *       203.0.113.9          0.0.0.0/0            tcp dpt:443 reject-with icmp-port-unreachable
7        0     0 DROP       tcp  --  *      *       192.0.2.0/24         0.0.0.0/0            tcp dpt:443 /* office */
`)
	firewall, err := (iptablesBackend{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	socket := ExposedPort{Port: 443, Protocol: "tcp", Address: "0.0.0.0"}
	for _, tc := range []struct {
		source    string
		verdict   Verdict
		rule      int
		undecided []int
	}{
		{"198.51.100.4", VerdictBlocked, 3, nil},
		{"127.0.0.1", VerdictAllowed, 2, nil},
		// The interface-limited drop and the fail2ban chain come first.
		{"192.0.2.7", VerdictBlocked, 7, []int{5}},
		{"203.0.113.9", VerdictBlocked, 6, []int{5}},
		{"203.0.113.10", VerdictAllowed, 0, []int{4, 5}},
	} {
		v, passed := JudgeFirewallFrom(socket, HostNetwork{}, firewall, netip.MustParseAddr(tc.source), nil)
		undecided := FirewallUndecided(v, passed)
		if v.Verdict != tc.verdict || v.Rule != tc.rule || !sameNumbers(undecided, tc.undecided) {
			t.Errorf("%s: verdict %s rule %d undecided %v (passed %v), want %s %d %v",
				tc.source, v.Verdict, v.Rule, undecided, passed, tc.verdict, tc.rule, tc.undecided)
		}
	}
}

func sameNumbers(rules []Rule, numbers []int) bool {
	if len(rules) != len(numbers) {
		return false
	}
	for i, r := range rules {
		if r.Number != numbers[i] {
			return false
		}
	}
	return true
}
