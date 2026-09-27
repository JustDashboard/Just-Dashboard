package netsec

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The verdict is the product making a claim, so these pin the claims. Each
// case is one thing an operator would be told, and getting any of them
// backwards is the kind of mistake that is embarrassing rather than subtle.

// writableFirewall is what ufw and firewalld report. Most cases here are about
// something other than capability, and leaving it zero would add a read-only
// notice to every one of them.
var writableFirewall = FirewallCapabilities{
	Editable: true, Toggle: true, DefaultPolicy: true, Logging: true, Reset: true, Profiles: true,
}

func findingByID(p *Posture, id string) (SecurityFinding, bool) {
	for _, f := range p.Findings {
		if f.ID == id {
			return f, true
		}
	}
	return SecurityFinding{}, false
}

func TestAssessAQuietHostReportsNothing(t *testing.T) {
	p := Assess(AssessInput{
		Exposure: &Exposure{Grade: "tailscale"},
		Firewall: &FirewallStatus{
			Available: true, Enabled: true, Logging: "on (low)",
			Policy:       DefaultPolicy{Incoming: "deny", Outgoing: "allow"},
			Rules:        []Rule{{Action: "ALLOW", To: "22/tcp", Port: "22"}},
			Capabilities: writableFirewall,
		},
		Fail2ban: &Fail2banStatus{Available: true, Running: true, Jails: []Jail{{Name: "sshd"}}},
		SSH: &SSHDConfig{Available: true, Settings: []SSHSetting{
			{Key: "permitrootlogin", Value: "prohibit-password"},
			{Key: "passwordauthentication", Value: "no"},
			{Key: "permitemptypasswords", Value: "no"},
			{Key: "maxauthtries", Value: "3"},
		}},
		PackageManager: "apt", SecurityFiltering: true,
		LoginRecordRead: true,
		Now:             time.Now(),
	})
	if p.Status != "ok" {
		t.Fatalf("status = %q with findings %+v", p.Status, p.Findings)
	}
	if len(p.Skipped) != 0 {
		t.Errorf("nothing should be skipped when everything answered: %q", p.Skipped)
	}
}

// A package manager with no advisory data cannot be quoted a count of zero.
// Left silent, the verdict reads as "checked, nothing outstanding" on every
// Alpine and Arch host — a clean bill of health nothing on those machines is
// in a position to give.
func TestAssessSaysWhenSecurityUpdatesCannotBeCounted(t *testing.T) {
	p := Assess(AssessInput{
		Exposure:       &Exposure{Grade: "tailscale"},
		PackageManager: "apk", SecurityFiltering: false,
		Now: time.Now(),
	})
	f, ok := findingByID(p, "updates.unknown")
	if !ok {
		t.Fatal("a host that cannot count security updates reported as having none")
	}
	if f.Level != "notice" {
		t.Errorf("level = %q, want notice — this is missing information, not a misconfiguration", f.Level)
	}
	if !strings.Contains(f.Detail, "apk") {
		t.Errorf("the finding does not name the manager: %q", f.Detail)
	}
	if !slices.Contains(p.Skipped, "security updates") {
		t.Errorf("skipped = %q, want the unanswerable check listed", p.Skipped)
	}
}

// The counted case is unchanged: a manager that does publish advisories says
// nothing when there is nothing to say.
func TestAssessSaysNothingWhenSecurityUpdatesAreCountedAndZero(t *testing.T) {
	p := Assess(AssessInput{
		Exposure:       &Exposure{Grade: "tailscale"},
		PackageManager: "dnf", SecurityFiltering: true,
		Now: time.Now(),
	})
	if _, ok := findingByID(p, "updates.unknown"); ok {
		t.Error("a manager that can count reported as unable to")
	}
}

func TestAssessGradesExposure(t *testing.T) {
	open := Assess(AssessInput{Exposure: &Exposure{Grade: "open"}})
	if f, ok := findingByID(open, "exposure.open"); !ok || f.Level != "critical" {
		t.Fatalf("open exposure = %+v", f)
	}
	if open.Status != "critical" {
		t.Errorf("status = %q", open.Status)
	}
	public := Assess(AssessInput{Exposure: &Exposure{Grade: "public", Allowlist: []string{"203.0.113.0/24"}}})
	if f, ok := findingByID(public, "exposure.public"); !ok || f.Level != "warning" {
		t.Fatalf("public exposure = %+v", f)
	}
}

func TestAssessFirewall(t *testing.T) {
	absent := Assess(AssessInput{Firewall: &FirewallStatus{Available: false}})
	if _, ok := findingByID(absent, "firewall.absent"); !ok {
		t.Error("no firewall should be reported")
	}
	if len(absent.Skipped) == 0 {
		t.Error("a check that could not run must say so rather than passing quietly")
	}

	off := Assess(AssessInput{Firewall: &FirewallStatus{
		Available: true, Enabled: false, Rules: []Rule{{}},
		Capabilities: FirewallCapabilities{Editable: true, Toggle: true},
	}})
	f, ok := findingByID(off, "firewall.disabled")
	if !ok || f.Level != "critical" {
		t.Fatalf("disabled firewall = %+v", f)
	}
	if f.Fix == "" {
		t.Error("the dashboard can turn it on, so the finding should offer to")
	}

	permissive := Assess(AssessInput{Firewall: &FirewallStatus{
		Available: true, Enabled: true, Policy: DefaultPolicy{Incoming: "allow"},
		Capabilities: writableFirewall,
	}})
	if _, ok := findingByID(permissive, "firewall.default-allow"); !ok {
		t.Error("a default-allow inbound policy should be reported")
	}

	quiet := Assess(AssessInput{Firewall: &FirewallStatus{
		Available: true, Enabled: true, Logging: "off",
		Policy:       DefaultPolicy{Incoming: "deny"},
		Capabilities: writableFirewall,
	}})
	if _, ok := findingByID(quiet, "firewall.logging-off"); !ok {
		t.Error("logging off should be reported")
	}
}

func TestAssessDangerousFirewallRule(t *testing.T) {
	p := Assess(AssessInput{Firewall: &FirewallStatus{
		Available: true, Enabled: true, Policy: DefaultPolicy{Incoming: "deny"},
		Capabilities: writableFirewall,
		Rules: []Rule{{
			Number: 3, Action: "ALLOW", To: "6379/tcp", Port: "6379",
			Service: "Redis", Danger: "Never open this to the world.", Raw: "6379/tcp ALLOW IN Anywhere",
		}},
	}})
	f, ok := findingByID(p, "firewall.dangerous-rule.3")
	if !ok || f.Level != "critical" {
		t.Fatalf("got %+v", f)
	}
}

// ufw prints every rule twice on a dual-stack host. Reporting both would make
// one mistake look like two.
func TestAssessDoesNotDoubleCountTheV6Rule(t *testing.T) {
	p := Assess(AssessInput{Firewall: &FirewallStatus{
		Available: true, Enabled: true, Policy: DefaultPolicy{Incoming: "deny"},
		Capabilities: writableFirewall,
		Rules: []Rule{
			{Number: 3, Action: "ALLOW", Port: "6379", Danger: "no"},
			{Number: 4, Action: "ALLOW", Port: "6379", Danger: "no", IPv6: true},
		},
	}})
	count := 0
	for _, f := range p.Findings {
		if f.Area == "firewall" && f.Level == "critical" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("reported %d findings for one rule", count)
	}
}

func TestAssessSSH(t *testing.T) {
	p := Assess(AssessInput{SSH: &SSHDConfig{Available: true, Settings: []SSHSetting{
		{Key: "permitrootlogin", Value: "yes"},
		{Key: "permitemptypasswords", Value: "yes"},
		{Key: "passwordauthentication", Value: "yes"},
		{Key: "maxauthtries", Value: "10"},
	}}})
	for _, id := range []string{"ssh.root-login", "ssh.empty-passwords", "ssh.password-auth", "ssh.max-auth-tries"} {
		if _, ok := findingByID(p, id); !ok {
			t.Errorf("%s not reported", id)
		}
	}
	if p.Status != "critical" {
		t.Errorf("status = %q", p.Status)
	}
}

// Telling somebody to turn off password authentication when they have no key
// is telling them to lock themselves out. The advice has to change, and the
// one-click fix has to disappear.
func TestAssessPasswordAdviceDependsOnWhetherAKeyExists(t *testing.T) {
	noKeys := Assess(AssessInput{SSH: &SSHDConfig{Available: true, Settings: []SSHSetting{
		{Key: "passwordauthentication", Value: "yes"},
	}}})
	f, _ := findingByID(noKeys, "ssh.password-auth")
	if f.Fix != "" {
		t.Error("offered a one-click fix that would lock the operator out")
	}
	if f.Level != "notice" {
		t.Errorf("level = %q; without a key this is not yet actionable", f.Level)
	}

	withKeys := Assess(AssessInput{SSH: &SSHDConfig{
		Available:     true,
		KeyedAccounts: []KeyedAccount{{User: "deploy", Keys: 1}},
		Settings:      []SSHSetting{{Key: "passwordauthentication", Value: "yes"}},
	}})
	f, _ = findingByID(withKeys, "ssh.password-auth")
	if f.Fix == "" {
		t.Error("with a key present the fix is safe and should be offered")
	}
	if f.Level != "warning" {
		t.Errorf("level = %q", f.Level)
	}
}

func TestAssessExposedPorts(t *testing.T) {
	p := Assess(AssessInput{
		Listeners: []ExposedPort{
			{Port: 6379, Protocol: "tcp", Address: "0.0.0.0", Process: "redis-server", Exposed: true},
			{Port: 5432, Protocol: "tcp", Address: "127.0.0.1", Process: "postgres", Exposed: false},
			{Port: 443, Protocol: "tcp", Address: "0.0.0.0", Process: "nginx", Exposed: true},
		},
	})
	if _, ok := findingByID(p, "ports.exposed.tcp.6379"); !ok {
		t.Error("an exposed Redis should be reported")
	}
	if _, ok := findingByID(p, "ports.exposed.tcp.5432"); ok {
		t.Error("a loopback-bound database is the correct arrangement and must stay silent")
	}
	if _, ok := findingByID(p, "ports.exposed.tcp.443"); ok {
		t.Error("an exposed web server is the point of the machine")
	}
}

// A firewall in front of the port changes what the finding means, and saying
// so is the difference between a finding and a false alarm.
func TestAssessSoftensAnExposedPortBehindADenyPolicy(t *testing.T) {
	p := Assess(AssessInput{
		Firewall: &FirewallStatus{
			Available: true, Enabled: true, Policy: DefaultPolicy{Incoming: "deny"},
			Capabilities: writableFirewall,
		},
		Listeners: []ExposedPort{{Port: 6379, Protocol: "tcp", Address: "0.0.0.0", Exposed: true}},
	})
	f, ok := findingByID(p, "ports.exposed.tcp.6379")
	if !ok {
		t.Fatal("still worth reporting")
	}
	if f.Level != "warning" {
		t.Errorf("level = %q, want warning behind a deny policy", f.Level)
	}
}

// Redis on the host's public address is as reachable as Redis on 0.0.0.0.
// It used to go unreported, because only a wildcard bind counted as exposed;
// the same database on a tailnet or a Docker bridge is reachable only from
// that network, which is worth a warning rather than an alarm.
func TestAssessJudgesAPortByTheAddressItIsBoundTo(t *testing.T) {
	for _, c := range []struct {
		address, level, title, advice string
	}{
		{"0.0.0.0", "critical", "Redis is listening on every interface", "rather than 6379:."},
		{"203.0.113.5", "critical", "Redis is listening on a public address", "rather than 203.0.113.5:6379:."},
		{"2001:db8:1::5", "critical", "Redis is listening on a public address", "rather than [2001:db8:1::5]:6379:."},
		{"100.64.1.2", "warning", "Redis is listening on a tailnet address", "every device on the tailnet"},
		{"fd7a:115c:a1e0::9e37:2220", "warning", "Redis is listening on a tailnet address", "every device on the tailnet"},
		{"10.0.0.1", "warning", "Redis is listening on a private address", "if the provider maps a public address onto it"},
	} {
		p := Assess(AssessInput{Listeners: []ExposedPort{
			{Port: 6379, Protocol: "tcp", Address: c.address, Process: "redis-server", Exposed: true},
		}})
		f, ok := findingByID(p, "ports.exposed.tcp.6379")
		if !ok {
			t.Errorf("Redis on %s was not reported", c.address)
			continue
		}
		if f.Level != c.level || f.Title != c.title || !strings.Contains(f.Advice, c.advice) {
			t.Errorf("Redis on %s = %q %q, advice %q; want %q %q with %q", c.address, f.Level, f.Title, f.Advice, c.level, c.title, c.advice)
		}
		if !strings.Contains(f.Detail, c.address) && c.address != "0.0.0.0" {
			t.Errorf("Redis on %s: detail %q does not name the address", c.address, f.Detail)
		}
	}
}

// A database bound twice is one exposure, reported at its widest: the
// finding's ID is per port, and two findings with one ID is one too many.
func TestAssessReportsADatabaseBoundTwiceOnceAtItsWidest(t *testing.T) {
	p := Assess(AssessInput{Listeners: []ExposedPort{
		{Port: 5432, Protocol: "tcp", Address: "100.64.1.2", Process: "postgres", Exposed: true},
		{Port: 5432, Protocol: "tcp", Address: "203.0.113.5", Process: "postgres", Exposed: true},
		{Port: 5432, Protocol: "tcp", Address: "10.0.0.1", Process: "postgres", Exposed: true},
	}})
	count := 0
	for _, f := range p.Findings {
		if f.ID == "ports.exposed.tcp.5432" {
			count++
			if f.Level != "critical" || !strings.Contains(f.Detail, "203.0.113.5") {
				t.Errorf("finding = %+v, want the public bind", f)
			}
		}
	}
	if count != 1 {
		t.Errorf("PostgreSQL reported %d times, want once", count)
	}
}

func TestAssessIntrusion(t *testing.T) {
	stopped := Assess(AssessInput{Fail2ban: &Fail2banStatus{Available: true, Running: false}})
	if f, ok := findingByID(stopped, "intrusion.stopped"); !ok || f.Level != "warning" {
		t.Errorf("installed-and-stopped = %+v", f)
	}
	empty := Assess(AssessInput{Fail2ban: &Fail2banStatus{Available: true, Running: true}})
	if _, ok := findingByID(empty, "intrusion.no-jails"); !ok {
		t.Error("running with no jails bans nobody and should say so")
	}
	missing := Assess(AssessInput{SSH: &SSHDConfig{Available: true}})
	if _, ok := findingByID(missing, "intrusion.absent"); !ok {
		t.Error("no fail2ban on a host running sshd should be a notice")
	}
}

func TestAssessFailedLoginVolume(t *testing.T) {
	quiet := Assess(AssessInput{FailedLogins: 5, LoginRecordRead: true})
	if _, ok := findingByID(quiet, "intrusion.failed-logins"); ok {
		t.Error("a handful of failures is background noise")
	}
	busy := Assess(AssessInput{FailedLogins: failedLoginNoticeCount, LoginRecordRead: true})
	if f, _ := findingByID(busy, "intrusion.failed-logins"); f.Level != "notice" {
		t.Errorf("level = %q", f.Level)
	}
	loud := Assess(AssessInput{FailedLogins: failedLoginWarningCount, LoginRecordRead: true})
	if f, _ := findingByID(loud, "intrusion.failed-logins"); f.Level != "warning" {
		t.Errorf("level = %q", f.Level)
	}
}

func TestAssessCertificates(t *testing.T) {
	p := Assess(AssessInput{Certificates: []CertSummary{
		{Name: "expired.example", DaysLeft: -2, Expired: true},
		{Name: "soon.example", DaysLeft: 2},
		{Name: "later.example", DaysLeft: 10},
		{Name: "fine.example", DaysLeft: 60},
	}})
	if f, ok := findingByID(p, "tls.expired.expired.example"); !ok || f.Level != "critical" {
		t.Errorf("expired = %+v", f)
	}
	if f, ok := findingByID(p, "tls.expiring.soon.example"); !ok || f.Level != "critical" {
		t.Errorf("two days left = %+v", f)
	}
	if f, ok := findingByID(p, "tls.expiring.later.example"); !ok || f.Level != "warning" {
		t.Errorf("ten days left = %+v", f)
	}
	if _, ok := findingByID(p, "tls.expiring.fine.example"); ok {
		t.Error("sixty days left is not a finding")
	}
}

func TestAssessUpdates(t *testing.T) {
	few := Assess(AssessInput{SecurityUpdates: 2})
	if f, _ := findingByID(few, "updates.security"); f.Level != "notice" {
		t.Errorf("level = %q", f.Level)
	}
	many := Assess(AssessInput{SecurityUpdates: securityUpdateWarningCount})
	if f, _ := findingByID(many, "updates.security"); f.Level != "warning" {
		t.Errorf("level = %q", f.Level)
	}
	reboot := Assess(AssessInput{RebootRequired: true})
	if _, ok := findingByID(reboot, "updates.reboot"); !ok {
		t.Error("a pending reboot should be reported")
	}
}

// Worst first, and stable between polls: a list that reshuffles itself is one
// nobody can read while it refreshes.
func TestAssessOrdersFindingsWorstFirst(t *testing.T) {
	p := Assess(AssessInput{
		Exposure:        &Exposure{Grade: "public"},
		SecurityUpdates: 1,
		SSH:             &SSHDConfig{Available: true, Settings: []SSHSetting{{Key: "permitrootlogin", Value: "yes"}}},
	})
	if len(p.Findings) < 3 {
		t.Fatalf("expected several findings, got %d", len(p.Findings))
	}
	last := 4
	for _, f := range p.Findings {
		rank := levelRank(f.Level)
		if rank > last {
			t.Fatalf("finding %s (%s) came after a milder one", f.ID, f.Level)
		}
		last = rank
	}
}

// A firewall the dashboard can read but not write should say so once, and must
// not offer a fix it cannot carry out — a button that always answers "not
// supported here" is worse than no button.
func TestAssessReportsAReadOnlyFirewall(t *testing.T) {
	p := Assess(AssessInput{Firewall: &FirewallStatus{
		Backend: BackendIPTables, Available: true, Enabled: false,
		Rules:        []Rule{{Action: "ALLOW", Port: "22"}},
		Capabilities: FirewallCapabilities{ReadOnlyReason: "no persistence across a reboot"},
	}})
	notice, ok := findingByID(p, "firewall.read-only")
	if !ok {
		t.Fatal("a read-only firewall was not reported")
	}
	if notice.Advice == "" {
		t.Error("the reason should reach the operator")
	}
	disabled, _ := findingByID(p, "firewall.disabled")
	if disabled.Fix != "" {
		t.Errorf("offered a fix this backend cannot perform: %q", disabled.Fix)
	}
}

// Zero failed attempts and "the tool that counts them is not installed" are the
// same number and opposite facts. `last` lives in util-linux-extra, which a
// minimal cloud image leaves out, so a verdict reading the two the same way
// reports a quiet server on a host nothing has looked at.
func TestAssessSaysWhenFailedLoginsCannotBeCounted(t *testing.T) {
	p := Assess(AssessInput{
		Exposure:        &Exposure{Grade: "tailscale"},
		SSH:             &SSHDConfig{Available: true},
		Fail2ban:        &Fail2banStatus{Available: true, Running: true, Jails: []Jail{{Name: "sshd"}}},
		LoginRecordRead: false,
		Now:             time.Now(),
	})
	if _, ok := findingByID(p, "intrusion.no-record"); !ok {
		t.Fatal("an unreadable login record reported as no failed logins")
	}
	if !slices.Contains(p.Skipped, "failed logins") {
		t.Errorf("skipped = %q, want the unanswerable check listed", p.Skipped)
	}
}

// With the record readable and quiet, nothing is said — the check passed.
func TestAssessSaysNothingWhenTheRecordIsReadableAndQuiet(t *testing.T) {
	p := Assess(AssessInput{
		Exposure:        &Exposure{Grade: "tailscale"},
		SSH:             &SSHDConfig{Available: true},
		Fail2ban:        &Fail2banStatus{Available: true, Running: true, Jails: []Jail{{Name: "sshd"}}},
		LoginRecordRead: true, FailedLogins: 3,
		Now: time.Now(),
	})
	if _, ok := findingByID(p, "intrusion.no-record"); ok {
		t.Error("a readable record reported as unreadable")
	}
	if _, ok := findingByID(p, "intrusion.failed-logins"); ok {
		t.Error("three attempts is background noise, not a finding")
	}
}
