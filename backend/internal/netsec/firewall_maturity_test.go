package netsec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// fakeHost is a host with a chosen set of firewall tools: ufw with a listing
// and a switch, firewalld's state, iptables' rules. Every command is
// recorded; mutations change the listing the way the tools would.
type fakeHost struct {
	t         *testing.T
	installed map[string]bool
	ufwActive bool
	listing   string
	defaults  string
	firewalld string
	iptables  string
	calls     []string
	fail      map[string]error
}

func newFakeHost(t *testing.T, tools ...string) *fakeHost {
	t.Helper()
	h := &fakeHost{t: t, installed: map[string]bool{}, fail: map[string]error{},
		defaults: "deny (incoming), allow (outgoing), disabled (routed)", firewalld: "not running"}
	for _, tool := range tools {
		h.installed[tool] = true
	}
	prevRun, prevAvailable, prevDefaults := run, availableOnHost, ufwDefaultsFile
	run = h.run
	availableOnHost = func(name string) bool { return h.installed[name] }
	ufwDefaultsFile = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { run, availableOnHost, ufwDefaultsFile = prevRun, prevAvailable, prevDefaults })
	return h
}

func (h *fakeHost) mutations() []string {
	var out []string
	for _, c := range h.calls {
		if strings.HasPrefix(c, "ufw status") || c == "firewall-cmd --state" || strings.HasPrefix(c, "iptables -S") || strings.HasPrefix(c, "ufw app") {
			continue
		}
		out = append(out, c)
	}
	return out
}

func (h *fakeHost) run(_ context.Context, name string, args ...string) (string, error) {
	call := strings.TrimSpace(name + " " + strings.Join(args, " "))
	h.calls = append(h.calls, call)
	for prefix, err := range h.fail {
		if strings.HasPrefix(call, prefix) {
			return "", err
		}
	}
	state := "inactive"
	if h.ufwActive {
		state = "active"
	}
	switch {
	case call == "ufw status":
		return "Status: " + state + "\n", nil
	case call == "ufw status numbered":
		return "Status: " + state + "\n\n     To                         Action      From\n     --                         ------      ----\n" + h.listing, nil
	case call == "ufw status verbose":
		return "Status: " + state + "\nLogging: on (low)\nDefault: " + h.defaults + "\n", nil
	case call == "ufw --force enable":
		h.ufwActive = true
		return "Firewall is active and enabled on system startup", nil
	case call == "ufw --force disable":
		h.ufwActive = false
		return "Firewall stopped and disabled on system startup", nil
	case strings.HasPrefix(call, "ufw default "):
		verdict, direction := args[1], args[2]
		parts := strings.Split(h.defaults, ", ")
		for i, p := range parts {
			if strings.HasSuffix(p, "("+direction+")") {
				parts[i] = verdict + " (" + direction + ")"
			}
		}
		h.defaults = strings.Join(parts, ", ")
		return "Default " + direction + " policy changed to '" + verdict + "'", nil
	case strings.HasPrefix(call, "ufw --force delete "):
		var n int
		fmt.Sscanf(args[len(args)-1], "%d", &n)
		h.listing = deleteNumberedLine(h.listing, n)
		return "Rule deleted", nil
	case strings.HasPrefix(call, "ufw app list"):
		return "Available applications:\n  OpenSSH\n", nil
	case strings.HasPrefix(call, "ufw app info OpenSSH"):
		return "Profile: OpenSSH\nTitle: Secure shell server\nDescription: OpenSSH\n\nPort:\n  22/tcp\n", nil
	case strings.HasPrefix(call, "ufw "):
		return "Rule added\nRule added (v6)", nil
	case call == "firewall-cmd --state":
		if h.firewalld == "running" {
			return "running", nil
		}
		return "not running", fmt.Errorf("firewall-cmd: not running")
	case call == "iptables -S":
		return h.iptables, nil
	}
	h.t.Logf("fake host has no answer for %q", call)
	return "", fmt.Errorf("%s: not modelled", call)
}

const ufwAdmitsSSHAndWeb = `[ 1] 22/tcp                     ALLOW IN    Anywhere
[ 2] 80,443/tcp                 ALLOW IN    Anywhere
[ 3] 22/tcp (v6)                ALLOW IN    Anywhere (v6)
[ 4] 80,443/tcp (v6)            ALLOW IN    Anywhere (v6)
`

func operatorAccess() AccessContext {
	return AccessContext{Client: "198.51.100.9", Interface: "eth0", Dashboard: []int{8443}, SSH: []int{22}, Ingress: []int{80, 443}}
}

func TestBackendIsChosenByActivityNotOnlyPresence(t *testing.T) {
	h := newFakeHost(t, "ufw", "firewall-cmd", "iptables")
	h.firewalld = "running"
	s := New()
	b, detection := s.selectBackend(context.Background())
	if b == nil || b.Kind() != BackendFirewalld {
		t.Fatalf("a running firewalld beats an installed, inactive ufw: %v %+v", b, detection)
	}
	if detection[0].Backend != BackendUFW || detection[0].Active != "inactive" || detection[1].Active != "active" || !detection[1].Selected {
		t.Fatalf("detection = %+v", detection)
	}
	if detection[2].Active != "not_checked" {
		t.Fatalf("iptables is not probed once a front end is in charge: %+v", detection[2])
	}

	h.ufwActive = true
	st, _ := s.Status(context.Background())
	if st.Capabilities.Editable || !strings.Contains(st.Capabilities.ReadOnlyReason, "Both ufw and firewalld") {
		t.Fatalf("two front ends are a conflict: %+v", st.Capabilities)
	}
	if _, err := s.AddRule(context.Background(), RuleRequest{Action: "allow", Port: "8080", Protocol: "tcp"}, "198.51.100.9"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("a write during a conflict: %v", err)
	}

	h = newFakeHost(t, "ufw", "iptables")
	if b, _ := New().selectBackend(context.Background()); b == nil || b.Kind() != BackendUFW {
		t.Fatalf("an installed ufw that can be switched on comes before iptables: %v", b)
	}
	h = newFakeHost(t, "iptables")
	h.iptables = "-P INPUT DROP\n-P FORWARD ACCEPT\n"
	_, detection = New().selectBackend(context.Background())
	if detection[2].Active != "active" || !strings.Contains(detection[2].Reason, "DROP policy on INPUT") {
		t.Fatalf("iptables activity = %+v", detection[2])
	}
}

func TestRulesGetStableIdentitiesAndFindings(t *testing.T) {
	h := newFakeHost(t, "ufw")
	h.ufwActive = true
	h.listing = `[ 1] 22/tcp                     ALLOW IN    Anywhere
[ 2] 22/tcp                     DENY IN     203.0.113.9
[ 3] 8000:8010/tcp              ALLOW IN    Anywhere
[ 4] 8005/tcp                   ALLOW IN    10.0.0.0/8
[ 5] 22/tcp (v6)                ALLOW IN    Anywhere (v6)
`
	s := New()
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, r := range st.Rules {
		if !strings.HasPrefix(r.ID, "fw-") || ids[r.ID] {
			t.Fatalf("identity = %q", r.ID)
		}
		ids[r.ID] = true
	}
	if st.Rules[0].ID == st.Rules[4].ID {
		t.Fatal("a rule and its IPv6 twin are different rules")
	}
	if len(st.Findings) != 2 {
		t.Fatalf("findings = %+v", st.Findings)
	}
	if f := st.Findings[0]; f.Number != 2 || f.Kind != "shadowed" || f.ByNumber != 1 {
		t.Fatalf("a deny after an allow for everyone never applies: %+v", f)
	}
	if f := st.Findings[1]; f.Number != 4 || f.Kind != "redundant" || f.ByNumber != 3 {
		t.Fatalf("an allow inside an earlier allow is redundant: %+v", f)
	}
	// Deleting rule 1 renumbers the rest; their identities do not move.
	ssh := st.Rules[2].ID
	h.listing = deleteNumberedLine(h.listing, 1)
	st, _ = s.Status(context.Background())
	if st.Rules[1].ID != ssh {
		t.Fatalf("identity moved with the number: %q != %q", st.Rules[1].ID, ssh)
	}
}

func TestStaleIdentitiesAreRefused(t *testing.T) {
	h := newFakeHost(t, "ufw")
	h.ufwActive = true
	h.listing = ufwAdmitsSSHAndWeb
	s := New()
	if _, err := s.RuleByID(context.Background(), "fw-000000000000"); !errors.Is(err, ErrRuleChanged) {
		t.Fatalf("a stale identity: %v", err)
	}
}

func TestAccessChecksFollowFirstMatchDefaultsAndInterfaces(t *testing.T) {
	h := newFakeHost(t, "ufw")
	h.ufwActive = true
	h.listing = `[ 1] 8443/tcp on tailscale0      ALLOW IN    Anywhere
[ 2] OpenSSH                    ALLOW IN    Anywhere
[ 3] 443/tcp                    DENY IN     Anywhere
[ 4] OpenSSH (v6)               ALLOW IN    Anywhere (v6)
`
	s := New()
	a := operatorAccess()
	a.Interface, a.Profiles = "tailscale0", map[string][]string{"OpenSSH": {"22/tcp"}}
	ctx := WithAccess(context.Background(), a)
	st, _ := s.Status(ctx)
	checks := map[string]AccessCheck{}
	for _, c := range s.EvaluateAccess(ctx, st) {
		checks[fmt.Sprintf("%d %s %s", c.Port, c.Family, c.Source)] = c
	}
	if c := checks["8443 ipv4 198.51.100.9"]; c.Verdict != "admitted" || c.Rule != 1 {
		t.Fatalf("the dashboard on the tailnet device = %+v", c)
	}
	if c := checks["22 ipv4 198.51.100.9"]; c.Verdict != "admitted" || c.Rule != 2 {
		t.Fatalf("SSH through the OpenSSH profile = %+v", c)
	}
	if c := checks["443 ipv4 Anywhere"]; c.Verdict != "refused" || c.Rule != 3 {
		t.Fatalf("public HTTPS = %+v", c)
	}
	if c := checks["80 ipv6 Anywhere"]; c.Verdict != "refused" || !strings.Contains(c.Reason, "inbound default is deny") {
		t.Fatalf("public HTTP over IPv6 falls to the default = %+v", c)
	}
	// Without the device, the interface-scoped rule cannot be decided.
	a.Interface = ""
	ctx = WithAccess(context.Background(), a)
	for _, c := range s.EvaluateAccess(ctx, st) {
		if c.Port == 8443 && c.Verdict != "unknown" {
			t.Fatalf("an interface rule with an unknown arrival device = %+v", c)
		}
	}
	// ufw configured not to filter IPv6 leaves it unfiltered.
	defaults := filepath.Join(t.TempDir(), "ufw")
	if err := os.WriteFile(defaults, []byte("IPV6=no\nDEFAULT_INPUT_POLICY=\"DROP\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ufwDefaultsFile = defaults
	st, _ = s.Status(ctx)
	if st.Effective.Families[1].Filtered || !strings.Contains(st.Effective.Families[1].Reason, "IPV6=no") {
		t.Fatalf("effective = %+v", st.Effective)
	}
	for _, c := range s.EvaluateAccess(ctx, st) {
		if c.Family == "ipv6" && c.Verdict != "unfiltered" {
			t.Fatalf("an IPv6 check under IPV6=no = %+v", c)
		}
	}
	if len(st.Effective.Interfaces) != 1 || st.Effective.Interfaces[0].Interface != "tailscale0" {
		t.Fatalf("interfaces = %+v", st.Effective.Interfaces)
	}
}

func TestEnablingIsRefusedWhenItWouldShutTheOperatorOut(t *testing.T) {
	h := newFakeHost(t, "ufw")
	h.listing = "[ 1] 80,443/tcp                 ALLOW IN    Anywhere\n[ 2] 80,443/tcp (v6)            ALLOW IN    Anywhere (v6)\n"
	s := New()
	ctx := WithAccess(context.Background(), operatorAccess())
	_, err := s.SetEnabled(ctx, true)
	var refusal *AccessRefusal
	if !errors.As(err, &refusal) || refusal.Check.Port != 8443 || refusal.Before.Verdict != "unfiltered" {
		t.Fatalf("enabling without the dashboard's port: %v", err)
	}
	if len(h.mutations()) != 0 {
		t.Fatalf("a refused enable ran %v", h.mutations())
	}
	h.listing = "[ 1] 22,8443/tcp               ALLOW IN    Anywhere\n" + h.listing
	if _, err := s.SetEnabled(ctx, true); err != nil {
		t.Fatalf("with the operator's ports admitted: %v", err)
	}
	if strings.Join(h.mutations(), "|") != "ufw --force enable" {
		t.Fatalf("mutations = %v", h.mutations())
	}
	// Without an access context only the source guard applies, as before.
	h = newFakeHost(t, "ufw")
	if _, err := New().SetEnabled(context.Background(), true); err != nil {
		t.Fatalf("a caller without access context: %v", err)
	}
}

func TestPolicyRuleAndDeleteChangesKeepPublicIngressThatWorksNow(t *testing.T) {
	h := newFakeHost(t, "ufw")
	h.ufwActive = true
	h.defaults = "allow (incoming), allow (outgoing), disabled (routed)"
	h.listing = "[ 1] 22,8443/tcp               ALLOW IN    Anywhere\n[ 2] 22,8443/tcp (v6)          ALLOW IN    Anywhere (v6)\n"
	s := New()
	ctx := WithAccess(context.Background(), operatorAccess())
	_, err := s.SetDefaultPolicy(ctx, "incoming", "deny")
	var refusal *AccessRefusal
	if !errors.As(err, &refusal) || refusal.Check.Port != 80 || refusal.Check.Source != "Anywhere" {
		t.Fatalf("a deny default would close Caddy's public ingress: %v", err)
	}
	h.listing = "[ 1] 22,8443/tcp               ALLOW IN    Anywhere\n[ 2] 80,443/tcp                 ALLOW IN    Anywhere\n[ 3] 22,8443/tcp (v6)          ALLOW IN    Anywhere (v6)\n[ 4] 80,443/tcp (v6)            ALLOW IN    Anywhere (v6)\n"
	if _, err := s.SetDefaultPolicy(ctx, "incoming", "deny"); err != nil {
		t.Fatalf("with ingress admitted by rules: %v", err)
	}
	if _, err := s.DeleteRule(ctx, 2); !errors.As(err, &refusal) || refusal.Check.Port != 80 {
		t.Fatalf("removing the rule that admits ingress: %v", err)
	}
	// The source guard refuses a blanket deny from a known caller first;
	// without a caller address the access guard still holds.
	if _, err := s.AddRule(ctx, RuleRequest{Action: "deny", Port: "443", Protocol: "tcp", Position: 1}, "198.51.100.9"); !errors.Is(err, ErrLockout) {
		t.Fatalf("a blanket deny from a known caller: %v", err)
	}
	if _, err := s.AddRule(ctx, RuleRequest{Action: "deny", Port: "443", Protocol: "tcp", Position: 1}, ""); !errors.As(err, &refusal) || refusal.Check.Port != 443 {
		t.Fatalf("a deny in front of ingress: %v", err)
	}
	if _, err := s.AddRule(ctx, RuleRequest{Action: "deny", From: "203.0.113.0/24"}, "198.51.100.9"); err != nil {
		t.Fatalf("blocking someone else: %v", err)
	}
}

func TestPreflightShowsEachCheckBeforeAndAfter(t *testing.T) {
	h := newFakeHost(t, "ufw")
	h.listing = ufwAdmitsSSHAndWeb
	s := New()
	p, err := s.Preflight(WithAccess(context.Background(), operatorAccess()), ProposedChange{Op: "enable"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Refusal == "" || !strings.Contains(p.Refusal, "dashboard") {
		t.Fatalf("refusal = %q", p.Refusal)
	}
	for _, c := range p.Checks {
		if c.Before != "unfiltered" {
			t.Fatalf("before enabling nothing is filtered: %+v", c)
		}
		if c.Port == 8443 && c.Verdict != "refused" {
			t.Fatalf("after: %+v", c)
		}
		if c.Port == 22 && c.Verdict != "admitted" {
			t.Fatalf("after: %+v", c)
		}
	}
	if len(h.mutations()) != 0 {
		t.Fatalf("a preflight changed something: %v", h.mutations())
	}
}

func TestPlansRunAddsBeforeRemovalsAndTakeBackAFailure(t *testing.T) {
	h := newFakeHost(t, "ufw")
	h.ufwActive = true
	h.listing = "[ 1] 22,8443/tcp               ALLOW IN    Anywhere\n[ 2] 80,443/tcp                 ALLOW IN    Anywhere\n[ 3] 6379/tcp                   ALLOW IN    Anywhere\n[ 4] 22,8443/tcp (v6)          ALLOW IN    Anywhere (v6)\n[ 5] 80,443/tcp (v6)            ALLOW IN    Anywhere (v6)\n[ 6] 6379/tcp (v6)              ALLOW IN    Anywhere (v6)\n"
	s := New()
	ctx := WithAccess(context.Background(), operatorAccess())
	st, _ := s.Status(ctx)
	redis := st.Rules[2].ID
	plan := FirewallPlan{Operations: []PlanOperation{
		{Op: "delete", RuleID: redis},
		{Op: "add", Rule: &RuleRequest{Action: "allow", Port: "6379", Protocol: "tcp", From: "10.0.0.0/8"}},
	}}
	review, err := s.PreviewPlan(ctx, plan, "198.51.100.9")
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Steps) != 2 || review.Steps[0].Op != "add" || review.Steps[1].Op != "delete" || review.Refusal != "" {
		t.Fatalf("review = %+v", review)
	}
	if len(h.mutations()) != 0 {
		t.Fatalf("a preview changed something: %v", h.mutations())
	}
	if _, err := s.PreviewPlan(ctx, FirewallPlan{Operations: []PlanOperation{{Op: "delete", RuleID: "fw-000000000000"}}}, "198.51.100.9"); !errors.Is(err, ErrRuleChanged) {
		t.Fatalf("a plan naming a stale rule: %v", err)
	}

	review, err = s.ApplyPlan(ctx, plan, "198.51.100.9")
	if err != nil {
		t.Fatal(err)
	}
	// The removal takes the rule's IPv6 twin with it, found again after
	// the renumbering.
	if got := strings.Join(h.mutations(), "|"); got != "ufw allow in from 10.0.0.0/8 to any port 6379 proto tcp|ufw --force delete 3|ufw --force delete 5" {
		t.Fatalf("mutations = %s", got)
	}
	for _, step := range review.Steps {
		if step.Outcome != "applied" {
			t.Fatalf("steps = %+v", review.Steps)
		}
	}

	// A failing removal takes the add back.
	h = newFakeHost(t, "ufw")
	h.ufwActive = true
	h.listing = "[ 1] 22,8443/tcp               ALLOW IN    Anywhere\n[ 2] 80,443/tcp                 ALLOW IN    Anywhere\n[ 3] 6379/tcp                   ALLOW IN    Anywhere\n[ 4] 22,8443/tcp (v6)          ALLOW IN    Anywhere (v6)\n[ 5] 80,443/tcp (v6)            ALLOW IN    Anywhere (v6)\n[ 6] 6379/tcp (v6)              ALLOW IN    Anywhere (v6)\n"
	h.fail["ufw --force delete 3"] = fmt.Errorf("ufw: ERROR: Could not delete")
	added := false
	prev := run
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		out, err := prev(ctx, name, args...)
		call := name + " " + strings.Join(args, " ")
		if strings.HasPrefix(call, "ufw allow in from 10.0.0.0/8") && !added {
			added = true
			h.listing = "[ 1] 22,8443/tcp               ALLOW IN    Anywhere\n[ 2] 80,443/tcp                 ALLOW IN    Anywhere\n[ 3] 6379/tcp                   ALLOW IN    Anywhere\n[ 4] 6379/tcp                   ALLOW IN    10.0.0.0/8\n[ 5] 22,8443/tcp (v6)          ALLOW IN    Anywhere (v6)\n[ 6] 80,443/tcp (v6)            ALLOW IN    Anywhere (v6)\n[ 7] 6379/tcp (v6)              ALLOW IN    Anywhere (v6)\n"
		}
		return out, err
	}
	review, err = s.ApplyPlan(ctx, plan, "198.51.100.9")
	if err == nil || !strings.Contains(err.Error(), "taken back") {
		t.Fatalf("a failed step: %v", err)
	}
	if review.Steps[0].Outcome != "compensated" || review.Steps[1].Outcome != "failed" {
		t.Fatalf("steps = %+v", review.Steps)
	}
	if !strings.Contains(strings.Join(h.mutations(), "|"), "ufw --force delete 4") {
		t.Fatalf("the added rule was not removed: %v", h.mutations())
	}
}

func TestReplaceRefusesToWidenAnInterfaceRule(t *testing.T) {
	b := editable(BackendUFW)
	b.rules = []Rule{{Number: 1, Action: "ALLOW", Direction: "IN", From: "Anywhere", To: "22/tcp", Port: "22", Protocol: "tcp", Interface: "tailscale0"}}
	if _, err := replaceRule(context.Background(), b, 1, RuleRequest{Action: "allow", Port: "22", Protocol: "tcp"}, ""); err == nil || !strings.Contains(err.Error(), "only on tailscale0") {
		t.Fatalf("err = %v", err)
	}
	if len(b.calls) > 1 {
		t.Fatalf("calls = %v", b.calls)
	}
}

func TestUFWReadsInterfaceScopedRulesInEitherColumn(t *testing.T) {
	in := parseUFWRule(1, "22/tcp on eth0              ALLOW IN    Anywhere")
	if in.Interface != "eth0" || in.To != "22/tcp" || in.Port != "22" {
		t.Fatalf("inbound = %+v", in)
	}
	v6 := parseUFWRule(2, "22/tcp (v6) on eth0         ALLOW IN    Anywhere (v6)")
	if v6.Interface != "eth0" || !v6.IPv6 || v6.Port != "22" {
		t.Fatalf("v6 = %+v", v6)
	}
	out := parseUFWRule(3, "53                         ALLOW OUT   Anywhere on wg0")
	if out.Interface != "wg0" || out.From != "Anywhere" || out.Direction != "OUT" {
		t.Fatalf("outbound = %+v", out)
	}
}

func TestFirewalldZonesAndLandingZoneSemantics(t *testing.T) {
	zones := parseActiveZones("docker\n  interfaces: docker0 br-1\npublic\n  interfaces: eth0\ntrusted\n  sources: 10.0.0.0/8\n")
	if len(zones) != 3 || zones[0].Name != "docker" || len(zones[0].Interfaces) != 2 || zones[2].Sources[0] != "10.0.0.0/8" {
		t.Fatalf("zones = %+v", zones)
	}
	st := &FirewallStatus{Backend: BackendFirewalld, Available: true, Enabled: true, Zone: "public", Default: "default (zone public)",
		Rules: []Rule{
			{Number: 1, Action: "ALLOW", Direction: "IN", From: "Anywhere", To: "ssh", Service: "ssh", BothFamilies: true, Zone: "public"},
			{Number: 2, Action: "DENY", Direction: "IN", From: "198.51.100.9", To: "ssh", Service: "ssh", Zone: "public"},
		},
		Zones: []FirewallZone{
			{Name: "public", Default: true, Target: "default", Interfaces: []string{"eth0"}},
			{Name: "trusted", Target: "ACCEPT", Sources: []string{"10.0.0.0/8"}},
		},
	}
	st.Zones[0].Rules = st.Rules
	checks := evaluateAccess(st, []AccessCheck{
		{Port: 22, Protocol: "tcp", Family: "ipv4", Source: "198.51.100.9"},
		{Port: 22, Protocol: "tcp", Family: "ipv6", Source: "2001:db8::9"},
		{Port: 8443, Protocol: "tcp", Family: "ipv4", Source: "10.1.2.3"},
		{Port: 8443, Protocol: "tcp", Family: "ipv4", Source: "198.51.100.10"},
	}, nil)
	if checks[0].Verdict != "refused" || checks[0].Rule != 2 {
		t.Fatalf("a rich denial beats the service allowance in the same zone: %+v", checks[0])
	}
	if checks[1].Verdict != "admitted" {
		t.Fatalf("the zone service applies to IPv6 too: %+v", checks[1])
	}
	if checks[2].Verdict != "admitted" || !strings.Contains(checks[2].Reason, "trusted") {
		t.Fatalf("a source-bound zone decides first: %+v", checks[2])
	}
	if checks[3].Verdict != "refused" || !strings.Contains(checks[3].Reason, "zone public") {
		t.Fatalf("the default zone's target: %+v", checks[3])
	}
	if findings, note := analyzeRules(BackendFirewalld, st.Rules); len(findings) != 0 || note == "" {
		t.Fatalf("firewalld ordering is not claimed: %+v %q", findings, note)
	}
}

func TestFirewalldResetReloadsAShippedZoneAndIsGuarded(t *testing.T) {
	root := t.TempDir()
	prev := hostRoot
	hostRoot = root
	t.Cleanup(func() { hostRoot = prev })
	zones := filepath.Join(root, "usr", "lib", "firewalld", "zones")
	if err := os.MkdirAll(zones, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(zones, "public.xml"), []byte(`<?xml version="1.0" encoding="utf-8"?>
<zone><short>Public</short><service name="ssh"/><service name="dhcpv6-client"/><port protocol="tcp" port="9090"/></zone>`), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := firewalldZoneDefaults("public")
	if err != nil || d.target != "default" || len(d.rules) != 3 || d.rules[2].Port != "9090" {
		t.Fatalf("defaults = %+v %v", d, err)
	}
	if _, err := firewalldZoneDefaults("custom"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("a zone with no shipped defaults: %v", err)
	}
	st := &FirewallStatus{Backend: BackendFirewalld, Available: true, Enabled: true, Zone: "public", Policy: DefaultPolicy{Incoming: "reject"},
		Rules: []Rule{{Number: 1, Action: "ALLOW", Direction: "IN", From: "Anywhere", To: "8443/tcp", Port: "8443", Protocol: "tcp", BothFamilies: true, Zone: "public"}},
		Zones: []FirewallZone{{Name: "public", Default: true, Target: "default"}}}
	st.Zones[0].Rules = st.Rules
	after := cloneStatus(st)
	if err := simulateReset(context.Background(), after); err != nil {
		t.Fatal(err)
	}
	a := operatorAccess()
	a.Ingress = nil
	err = compareAccess(st, after, a)
	var refusal *AccessRefusal
	if !errors.As(err, &refusal) || refusal.Check.Port != 8443 {
		t.Fatalf("a reset dropping the dashboard's port: %v", err)
	}
}

func TestOwnedTableIsABackendForHostsWithoutAFrontEnd(t *testing.T) {
	newFakeHost(t, "iptables")
	owned := &fakeOwned{table: OwnedTable{Enabled: true, Incoming: "drop", Runtime: "present", Foreign: []string{"ip filter"},
		Rules: []OwnedRule{{ID: 7, Action: "accept", Protocol: "tcp", Ports: "22,8443"}, {ID: 9, Action: "drop", Source: "203.0.113.0/24"}}}}
	s := New()
	s.UseOwnedFirewall(owned)
	st, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Backend != BackendNFTOwned || !st.Enabled || st.Policy.Incoming != "deny" || len(st.Rules) != 2 || st.Rules[0].Port != "22,8443" ||
		!st.Rules[0].BothFamilies || st.Rules[1].From != "203.0.113.0/24" || len(st.Foreign) != 1 || !st.Capabilities.Editable || st.Capabilities.Logging {
		t.Fatalf("status = %+v", st)
	}
	ctx := WithAccess(context.Background(), operatorAccess())
	for _, c := range s.EvaluateAccess(ctx, st) {
		if (c.Port == 22 || c.Port == 8443) && c.Verdict != "admitted" {
			t.Fatalf("access = %+v", c)
		}
	}
	if _, err := s.AddRule(context.Background(), RuleRequest{Action: "allow", Port: "80,443", Protocol: "tcp", Position: 1}, ""); err != nil {
		t.Fatal(err)
	}
	if c := owned.changes[0]; c.Op != "add" || c.Position != 1 || c.Rule.Ports != "80,443" {
		t.Fatalf("change = %+v", c)
	}
	if _, err := s.DeleteRule(context.Background(), 2); err != nil || owned.changes[1].Op != "delete" || owned.changes[1].ID != 9 {
		t.Fatalf("delete by id = %+v %v", owned.changes, err)
	}
	for _, req := range []RuleRequest{
		{Action: "allow", Direction: "out", Port: "53"},
		{Action: "allow", App: "OpenSSH"},
		{Action: "limit", Port: "22"},
		{Action: "allow", Port: "22", To: "10.0.0.1"},
	} {
		if _, err := s.AddRule(context.Background(), req, ""); err == nil {
			t.Errorf("%+v was accepted", req)
		}
	}
	if _, err := s.SetLogging(context.Background(), "low"); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("logging: %v", err)
	}
	if _, err := s.ReplaceRule(context.Background(), 1, RuleRequest{Action: "allow", Port: "2222", Protocol: "tcp"}, ""); err != nil {
		t.Fatal(err)
	}
	last := owned.changes[len(owned.changes)-2:]
	if last[0].Op != "add" || last[0].Position != 1 || last[1].Op != "delete" || last[1].ID != 7 {
		t.Fatalf("a replacement adds in place, then removes by id: %+v", last)
	}
}

type fakeOwned struct {
	table   OwnedTable
	changes []OwnedChange
}

func (f *fakeOwned) Available() bool { return true }
func (f *fakeOwned) Read(context.Context) (OwnedTable, error) {
	return f.table, nil
}
func (f *fakeOwned) Change(_ context.Context, c OwnedChange) error {
	f.changes = append(f.changes, c)
	return nil
}

func TestRuleHistoryFollowsARuleThroughItsReplacements(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New()
	s.UseHistory(st.DB)
	ctx := context.Background()
	for _, e := range []RuleEvent{
		{Actor: "ion", Backend: BackendUFW, Operation: "add", RuleID: "fw-aaaaaaaaaaaa", Outcome: "applied"},
		{Actor: "ion", Backend: BackendUFW, Operation: "add", RuleID: "fw-cccccccccccc", Outcome: "applied"},
		{Actor: "ana", Backend: BackendUFW, Operation: "replace", RuleID: "fw-bbbbbbbbbbbb", PreviousRuleID: "fw-aaaaaaaaaaaa", Outcome: "applied"},
		{Actor: "ana", Backend: BackendUFW, Operation: "delete", RuleID: "fw-bbbbbbbbbbbb", Outcome: "refused", Detail: "would lock you out"},
	} {
		if err := s.RecordRuleEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	h, err := s.History(ctx, "fw-bbbbbbbbbbbb", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, e := range h.Events {
		got = append(got, e.Operation+":"+e.Outcome)
	}
	if strings.Join(got, ",") != "delete:refused,replace:applied,add:applied" || len(h.Limits) == 0 {
		t.Fatalf("lineage = %v", got)
	}
	all, err := s.History(ctx, "", 10)
	if err != nil || len(all.Events) != 4 {
		t.Fatalf("all = %+v %v", all, err)
	}
	if _, err := New().History(ctx, "", 10); err == nil {
		t.Fatal("history without a database")
	}
}

func TestUFWShowAddedIsReadAsTheConfiguredRules(t *testing.T) {
	rules := parseUFWAdded(`Added user rules (see 'ufw status' for running firewall):
ufw allow in on fw0 to any port 2222 proto tcp
ufw allow OpenSSH
ufw allow 80,443/tcp
ufw allow from 10.0.0.0/8 to any port 6379 proto tcp
ufw allow 8443/tcp comment 'dashboard'
ufw deny out on wg0 to 10.9.0.0/16
ufw allow from 10.0.0.0/8 to 10.0.0.5 port 5432 proto tcp
ufw route allow in on fw0 out on eth0
ufw limit 22/tcp
ufw allow from 10.2.0.0/16 to any app OpenSSH
ufw allow from 2001:db8::/32 to any port 53
`)
	if len(rules) != 11 {
		t.Fatalf("rules = %+v", rules)
	}
	want := []struct{ action, direction, to, from, port, proto, iface, comment string }{
		{"ALLOW", "IN", "2222/tcp", "Anywhere", "2222", "tcp", "fw0", ""},
		{"ALLOW", "IN", "OpenSSH", "Anywhere", "", "", "", ""},
		{"ALLOW", "IN", "80,443/tcp", "Anywhere", "80,443", "tcp", "", ""},
		{"ALLOW", "IN", "6379/tcp", "10.0.0.0/8", "6379", "tcp", "", ""},
		{"ALLOW", "IN", "8443/tcp", "Anywhere", "8443", "tcp", "", "dashboard"},
		{"DENY", "OUT", "10.9.0.0/16", "Anywhere", "", "", "wg0", ""},
		{"ALLOW", "IN", "10.0.0.5 5432/tcp", "10.0.0.0/8", "5432", "tcp", "", ""},
		{"ALLOW", "FWD", "Anywhere", "Anywhere", "", "", "fw0", ""},
		{"LIMIT", "IN", "22/tcp", "Anywhere", "22", "tcp", "", ""},
		{"ALLOW", "IN", "OpenSSH", "10.2.0.0/16", "", "", "", ""},
		{"ALLOW", "IN", "53", "2001:db8::/32", "53", "", "", ""},
	}
	for i, w := range want {
		r := rules[i]
		if r.Action != w.action || r.Direction != w.direction || r.To != w.to || r.From != w.from || r.Port != w.port || r.Protocol != w.proto || r.Interface != w.iface || r.Comment != w.comment {
			t.Errorf("rule %d = %+v, want %+v", i+1, r, w)
		}
	}
	if !rules[0].BothFamilies || rules[3].BothFamilies || rules[3].IPv6 || !rules[10].IPv6 {
		t.Fatalf("families = %+v / %+v / %+v", rules[0], rules[3], rules[10])
	}
	// The enforced listing's quirks: "(out)" closes an outbound source, and
	// an IPv6 source carries no "(v6)" marker.
	out := parseUFWRule(5, "10.9.0.0/16                DENY OUT    Anywhere on wg0            (out)")
	if out.From != "Anywhere" || out.Interface != "wg0" || out.Direction != "OUT" {
		t.Fatalf("outbound = %+v", out)
	}
	v6 := parseUFWRule(14, "53                         ALLOW IN    2001:db8::/32")
	if !v6.IPv6 {
		t.Fatalf("an IPv6 source rule = %+v", v6)
	}
}
