package netsec

import (
	"strings"
	"testing"
	"time"
)

func tailnetHost(t *testing.T) BoundaryInput {
	return BoundaryInput{
		Allowlist: cidrs(t, "127.0.0.1/32", "::1/128", "100.64.0.0/10"),
		Client:    "100.110.34.9", Operator: "100.110.34.9",
		CaddyPort: 8443, Binds: []string{"100.110.34.31", "localhost"},
		ListenersRead: true,
		Listeners: []BoundaryListener{
			{Protocol: "tcp", Address: "100.110.34.31", Port: 8443, Process: "caddy", Caddy: true},
			{Protocol: "tcp", Address: "127.0.0.1", Port: 8443, Process: "caddy", Caddy: true},
			{Protocol: "tcp", Address: "127.0.0.1", Port: 8080, Process: "just-dashboard", Dashboard: true},
			{Protocol: "tcp", Address: "0.0.0.0", Port: 22, Process: "sshd"},
		},
		TailnetIP: "100.110.34.31", TailnetUp: true,
		SSHPorts:     []string{"22"},
		PreviewsRead: true,
		Previews:     []PreviewServe{{Port: 21000, Upstream: 31000}, {Port: 443, Upstream: 0}},
		Now:          time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC),
	}
}

func checkState(b AccessBoundary, id string) BoundaryCheck {
	for _, c := range b.Checks {
		if c.ID == id {
			return c
		}
	}
	return BoundaryCheck{}
}

func TestBoundaryHoldsOnATailnetHost(t *testing.T) {
	b := DescribeBoundary(tailnetHost(t))
	for _, id := range []string{"ingress", "allowlist", "tailnet", "ssh", "previews"} {
		if c := checkState(b, id); c.State != BoundaryHeld {
			t.Fatalf("%s=%+v", id, c)
		}
	}
	if !strings.Contains(checkState(b, "previews").Detail, "1 preview port") {
		t.Fatalf("a served port outside the range was counted: %s", checkState(b, "previews").Detail)
	}
}

func TestBoundaryNamesEachWayItBreaks(t *testing.T) {
	for name, tc := range map[string]struct {
		edit  func(*BoundaryInput)
		id    string
		state string
		says  string
	}{
		"backend on a routable address": {func(in *BoundaryInput) {
			in.Listeners = append(in.Listeners, BoundaryListener{Protocol: "tcp", Address: "0.0.0.0", Port: 3000, Process: "next-server", Dashboard: true})
		}, "ingress", BoundaryBroken, "next-server on 0.0.0.0:3000"},
		"nothing on the dashboard's port": {func(in *BoundaryInput) { in.Listeners = in.Listeners[2:] }, "ingress", BoundaryBroken, "Nothing that is Caddy"},
		"sockets unreadable":              {func(in *BoundaryInput) { in.ListenersRead = false }, "ingress", BoundaryUnknown, "could not be read"},
		"tailscale down":                  {func(in *BoundaryInput) { in.TailnetUp, in.TailnetIP = false, "" }, "tailnet", BoundaryBroken, "no tailscale interface"},
		"sshd moved off its socket":       {func(in *BoundaryInput) { in.SSHPorts = []string{"2222"} }, "ssh", BoundaryBroken, "No socket listens"},
		"preview funnelled":               {func(in *BoundaryInput) { in.Previews = []PreviewServe{{Port: 21001}} }, "previews", BoundaryBroken, "21001 is funnelled"},
		"serve unreadable":                {func(in *BoundaryInput) { in.PreviewsRead = false }, "previews", BoundaryUnknown, ""},
		"tunnel session":                  {func(in *BoundaryInput) { in.Client, in.Operator = "127.0.0.1", "198.51.100.20" }, "allowlist", BoundaryHeld, "SSH tunnel from 198.51.100.20"},
		"rewritten address":               {func(in *BoundaryInput) { in.Client = "203.0.113.5" }, "allowlist", BoundaryUnknown, "rewrote"},
	} {
		t.Run(name, func(t *testing.T) {
			in := tailnetHost(t)
			tc.edit(&in)
			c := checkState(DescribeBoundary(in), tc.id)
			if c.State != tc.state || !strings.Contains(c.Detail, tc.says) {
				t.Fatalf("%s=%+v", tc.id, c)
			}
		})
	}
}

func impactsOf(t *testing.T, in BoundaryInput, p BoundaryProposal) []BoundaryImpact {
	t.Helper()
	return BoundaryImpacts(DescribeBoundary(in), p)
}

func hasImpact(impacts []BoundaryImpact, boundary, level, text string) bool {
	for _, i := range impacts {
		if i.Boundary == boundary && i.Level == level && strings.Contains(i.Text, text) {
			return true
		}
	}
	return false
}

func TestABanIsJudgedAgainstTheSessionTheAllowlistAndPreviews(t *testing.T) {
	in := tailnetHost(t)
	if got := impactsOf(t, in, BoundaryProposal{Kind: "ban", Target: "100.110.34.0/24"}); !hasImpact(got, "allowlist", ImpactCuts, "100.110.34.9") {
		t.Fatalf("own session=%+v", got)
	}
	got := impactsOf(t, in, BoundaryProposal{Kind: "ban", Target: "100.64.0.12"})
	if !hasImpact(got, "allowlist", ImpactAffects, "100.64.0.0/10") || !hasImpact(got, "previews", ImpactAffects, "21000") {
		t.Fatalf("tailnet peer=%+v", got)
	}
	if got := impactsOf(t, in, BoundaryProposal{Kind: "ban", Target: "203.0.113.9"}); len(got) != 0 {
		t.Fatalf("a stranger's ban touched the boundary: %+v", got)
	}
	// Behind a tunnel the operator's address is the SSH peer, not loopback.
	tunnel := tailnetHost(t)
	tunnel.Client, tunnel.Operator = "127.0.0.1", "198.51.100.20"
	if got := impactsOf(t, tunnel, BoundaryProposal{Kind: "ban", Target: "198.51.100.0/24"}); !hasImpact(got, "allowlist", ImpactCuts, "198.51.100.20") {
		t.Fatalf("tunnel peer=%+v", got)
	}
	open := tailnetHost(t)
	open.Allowlist = cidrs(t, "0.0.0.0/0")
	if got := impactsOf(t, open, BoundaryProposal{Kind: "ban", Target: "203.0.113.9"}); len(got) != 0 {
		t.Fatalf("an open allowlist made every ban a boundary change: %+v", got)
	}
}

func TestAFirewallRuleIsJudgedByThePortsItReaches(t *testing.T) {
	in := tailnetHost(t)
	if got := impactsOf(t, in, BoundaryProposal{Kind: "firewall.rule", Action: "deny", Target: "100.64.0.0/10", Port: "5432/tcp"}); len(got) != 0 {
		t.Fatalf("a database port touched the boundary: %+v", got)
	}
	if got := impactsOf(t, in, BoundaryProposal{Kind: "firewall.rule", Action: "deny", Target: "100.64.0.0/10", Port: "8443"}); !hasImpact(got, "allowlist", ImpactCuts, "100.110.34.9") {
		t.Fatalf("dashboard port=%+v", got)
	}
	if got := impactsOf(t, in, BoundaryProposal{Kind: "firewall.rule", Action: "allow", Target: "100.64.0.0/10"}); len(got) != 0 {
		t.Fatalf("an allow touched the boundary: %+v", got)
	}
	if got := impactsOf(t, in, BoundaryProposal{Kind: "firewall.rule", Action: "deny", Target: "any", Port: "41641", Protocol: "udp"}); !hasImpact(got, "tailnet", ImpactAffects, "DERP") {
		t.Fatalf("tailscaled port=%+v", got)
	}
	if got := impactsOf(t, in, BoundaryProposal{Kind: "firewall.rule", Action: "deny", Target: "100.64.0.0/10", Port: "21000:21010"}); !hasImpact(got, "previews", ImpactAffects, "previews") {
		t.Fatalf("preview range=%+v", got)
	}
	if got := impactsOf(t, in, BoundaryProposal{Kind: "firewall.policy", Policy: "deny"}); !hasImpact(got, "ingress", ImpactAffects, "port 8443") {
		t.Fatalf("policy=%+v", got)
	}
}

func TestAnSSHChangeIsJudgedForTheTunnel(t *testing.T) {
	tunnel := tailnetHost(t)
	tunnel.Client, tunnel.Operator = "127.0.0.1", "198.51.100.20"
	got := impactsOf(t, tunnel, BoundaryProposal{Kind: "ssh", Settings: map[string]string{"port": "2222", "allowtcpforwarding": "no", "allowusers": "deploy"}})
	if !hasImpact(got, "ssh", ImpactAffects, "from 22 to 2222") || !hasImpact(got, "ssh", ImpactCuts, "ssh -L") || !hasImpact(got, "ssh", ImpactAffects, "Only deploy") {
		t.Fatalf("tunnel=%+v", got)
	}
	remote := tailnetHost(t)
	if got := impactsOf(t, remote, BoundaryProposal{Kind: "ssh", Settings: map[string]string{"allowtcpforwarding": "no"}}); !hasImpact(got, "ssh", ImpactAffects, "ssh -L") {
		t.Fatalf("remote=%+v", got)
	}
	if got := impactsOf(t, remote, BoundaryProposal{Kind: "ssh", Settings: map[string]string{"passwordauthentication": "no", "port": "22"}}); len(got) != 0 {
		t.Fatalf("an unrelated directive touched the boundary: %+v", got)
	}
}

func TestBoundaryComparisonNamesWhatWasLost(t *testing.T) {
	before := DescribeBoundary(tailnetHost(t))
	in := tailnetHost(t)
	in.TailnetUp, in.TailnetIP = false, ""
	in.PreviewsRead = false
	changes := CompareBoundaries(before, DescribeBoundary(in))
	lost := map[string]bool{}
	for _, c := range changes {
		lost[c.ID] = c.Lost
	}
	if !lost["tailnet"] || !lost["previews"] || lost["ingress"] || lost["ssh"] || len(changes) != 5 {
		t.Fatalf("changes=%+v", changes)
	}
}
