package netsec

import (
	"context"
	"strings"
	"testing"
)

// Copied verbatim from `iptables -L -n -v --line-numbers` on a host running
// ufw, Docker and Tailscale — which is to say, a normal one. The column order
// is the whole content of this test: source and destination are the ninth and
// tenth fields, and reading them one to the left reports the *out* interface
// as the rule's source.
const iptablesListing = `Chain INPUT (policy DROP 914K packets, 54M bytes)
num   pkts bytes target     prot opt in     out     source               destination
1     117M  186G ts-input   all  --  *      *       0.0.0.0/0            0.0.0.0/0
2     649M 1272G ufw-before-input  all  --  *      *       10.0.0.0/8           0.0.0.0/0

Chain FORWARD (policy DROP 0 packets, 0 bytes)
num   pkts bytes target     prot opt in     out     source               destination
1     690K 3827M DOCKER-USER  all  --  *      *       0.0.0.0/0            0.0.0.0/0

Chain ufw-before-input (1 references)
num   pkts bytes target     prot opt in     out     source               destination
`

func withIPTablesOutput(t *testing.T, out string) {
	t.Helper()
	previous := run
	run = func(_ context.Context, name string, args ...string) (string, error) {
		return out, nil
	}
	t.Cleanup(func() { run = previous })
}

func TestIPTablesReadsTheColumnsItActuallyPrints(t *testing.T) {
	withIPTablesOutput(t, iptablesListing)
	st, err := (iptablesBackend{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rules) != 3 {
		t.Fatalf("%d rules, want 3: %+v", len(st.Rules), st.Rules)
	}
	second := st.Rules[1]
	if second.From != "10.0.0.0/8" {
		t.Errorf("From = %q, want the source column", second.From)
	}
	if second.To != "0.0.0.0/0" {
		t.Errorf("To = %q, want the destination column", second.To)
	}
	if second.Action != "ufw-before-input" || second.Direction != "INPUT" {
		t.Errorf("target/chain misread: %+v", second)
	}
	if st.Default != "DROP" || st.Policy.Incoming != "deny" {
		t.Errorf("policy = %q/%q, want DROP read as deny", st.Default, st.Policy.Incoming)
	}
}

// The structured policy is compared against "allow" by the posture rules, the
// exposed-port grading and the sshd port guard, so iptables' own word for it
// has to arrive as theirs: an ACCEPT policy read as "accept" was never told
// its inbound default was allow, and had every exposed port graded as though
// a firewall stood in front of it.
func TestIPTablesPolicyArrivesInTheSharedWords(t *testing.T) {
	cases := map[string]string{"ACCEPT": "allow", "DROP": "deny", "REJECT": "reject"}
	for verdict, want := range cases {
		withIPTablesOutput(t, "Chain INPUT (policy "+verdict+" 0 packets, 0 bytes)\n"+
			"num   pkts bytes target     prot opt in     out     source               destination\n")
		st, err := (iptablesBackend{}).Status(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if st.Default != verdict {
			t.Errorf("%s: Default = %q, want the verdict verbatim", verdict, st.Default)
		}
		if st.Policy.Incoming != want {
			t.Errorf("%s: Policy.Incoming = %q, want %q", verdict, st.Policy.Incoming, want)
		}
	}
	if got := iptablesPolicyWord("RETURN"); got != "return" {
		t.Errorf("an unknown verdict is lowered, not invented: %q", got)
	}
}

// A chain line is a tool's prose, and indexing into prose is how a read-only
// status route panics.
func TestIPTablesSurvivesATruncatedChainLine(t *testing.T) {
	withIPTablesOutput(t, "Chain INPUT (policy \n")
	st, err := (iptablesBackend{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.Default != "" {
		t.Errorf("invented a policy from a truncated line: %q", st.Default)
	}
}

// A row with fewer columns than the header promises is skipped rather than
// read at whatever offsets happen to exist.
func TestIPTablesSkipsShortRows(t *testing.T) {
	withIPTablesOutput(t, strings.Join([]string{
		"Chain INPUT (policy ACCEPT 0 packets, 0 bytes)",
		"num   pkts bytes target     prot opt in     out     source               destination",
		"1     0     0 ACCEPT",
		"",
	}, "\n"))
	st, err := (iptablesBackend{}).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rules) != 0 {
		t.Fatalf("read a rule out of a truncated row: %+v", st.Rules)
	}
}
