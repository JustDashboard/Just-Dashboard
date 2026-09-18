package netsec

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Raw iptables is the fallback, and it is deliberately read-only.
//
// Not because writing rules is hard — it is the easiest of the three — but
// because iptables has no persistence of its own. A rule added here would work
// perfectly until the machine rebooted and then silently vanish, which for a
// firewall is the worst possible behaviour: the page would show a host that is
// protected and the host would not be. Debian has iptables-persistent and RHEL
// has firewalld precisely because nobody wants to solve that per-tool, so the
// honest answer is to show what is there and say why the buttons are absent.
type iptablesBackend struct{}

func (iptablesBackend) Kind() Backend { return BackendIPTables }
func (iptablesBackend) Detect() bool  { return hostexec.AvailableOnHost("iptables") }

func (iptablesBackend) Capabilities() FirewallCapabilities {
	return FirewallCapabilities{
		ReadOnlyReason: "This host has raw iptables with no ufw or firewalld in front of it. " +
			"iptables keeps no rules across a reboot on its own, so a rule added from here would " +
			"quietly disappear at the next restart. Install ufw or firewalld to manage rules from the dashboard.",
	}
}

func (iptablesBackend) Status(ctx context.Context) (*FirewallStatus, error) {
	out, err := run(ctx, "iptables", "-L", "-n", "-v", "--line-numbers")
	st := &FirewallStatus{Backend: BackendIPTables, Available: true, Rules: []Rule{}, Raw: out}
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	chain := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "Chain ") {
			fields := strings.Fields(trimmed)
			if len(fields) > 1 {
				chain = fields[1]
			}
			if chain == "INPUT" {
				if idx := strings.Index(trimmed, "policy "); idx >= 0 {
					// A chain line always names a verdict after "policy", but
					// this is parsing a tool's prose and an index into it is
					// the kind of thing that panics a handler.
					if fields := strings.Fields(trimmed[idx+len("policy "):]); len(fields) > 0 {
						st.Default = strings.Trim(fields[0], "()")
						st.Policy = DefaultPolicy{Incoming: iptablesPolicyWord(st.Default)}
					}
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, "num ") || strings.HasPrefix(trimmed, "pkts ") {
			continue
		}
		// The columns of `iptables -L -n -v --line-numbers` are, in order:
		//
		//	num pkts bytes target prot opt in out source destination
		//
		// so source and destination are the ninth and tenth, not the eighth
		// and ninth. Read one place to the left they report the *out*
		// interface as the rule's source and the source as its destination,
		// which on a host with any rules at all makes every row say "* → the
		// address this rule is actually about".
		fields := strings.Fields(trimmed)
		if len(fields) < 10 {
			continue
		}
		num, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		st.Rules = append(st.Rules, Rule{
			Number: num, Action: fields[3], Protocol: fields[4],
			From: fields[8], To: fields[9], Direction: chain,
			Raw: trimmed,
		})
	}
	// A default DROP with rules present is the practical definition of
	// "enabled" for raw iptables, which has no on/off switch of its own.
	st.Enabled = st.Default == "DROP" || len(st.Rules) > 0
	return st, nil
}

// iptablesPolicyWord folds a chain's verdict onto the three words every
// reader of DefaultPolicy speaks. The verdict itself stays verbatim in Default.
//
// The structured field is compared against "allow" by the posture rules, by
// the grading of an exposed port and by the sshd port guard, and "accept"
// matched none of them: an iptables host with an ACCEPT policy was never told
// its inbound default was allow, had its exposed databases graded as though a
// firewall were refusing them, and was refused an SSH port move as a lockout
// that policy could never have caused.
func iptablesPolicyWord(verdict string) string {
	switch strings.ToUpper(verdict) {
	case "ACCEPT":
		return "allow"
	case "DROP":
		return "deny"
	case "REJECT":
		return "reject"
	}
	return strings.ToLower(verdict)
}

// Everything below refuses, for the reason on the type.
func (b iptablesBackend) refuse() error {
	return fmt.Errorf("%w: %s", ErrReadOnly, b.Capabilities().ReadOnlyReason)
}

func (b iptablesBackend) AddRule(context.Context, RuleRequest) (string, error) {
	return "", b.refuse()
}
func (b iptablesBackend) DeleteRule(context.Context, int) (string, error) { return "", b.refuse() }
func (b iptablesBackend) SetEnabled(context.Context, bool) (string, error) {
	return "", b.refuse()
}
func (b iptablesBackend) SetDefaultPolicy(context.Context, string, string) (string, error) {
	return "", b.refuse()
}
func (b iptablesBackend) SetLogging(context.Context, string) (string, error) {
	return "", b.refuse()
}
func (b iptablesBackend) Reset(context.Context) (string, error) { return "", b.refuse() }
func (b iptablesBackend) Profiles(context.Context) ([]AppProfile, error) {
	return []AppProfile{}, nil
}
