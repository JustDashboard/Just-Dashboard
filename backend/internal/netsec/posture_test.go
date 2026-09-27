package netsec

import (
	"fmt"
	"net"
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

// thisHost is this machine's network as ReadHostNetwork reads it: the public
// uplink ens3 with its link-local address, Docker's bridges, the tailnet, and
// a libvirt bridge and a WireGuard tunnel of the kind other hosts have.
var thisHost = HostNetwork{Addresses: []HostAddress{
	{IP: mustIP("57.131.21.87"), Interface: "ens3", Kind: "physical", DefaultRoute: true},
	{IP: mustIP("fe80::f816:3eff:fee3:1a48"), Interface: "ens3", Kind: "physical", DefaultRoute: true},
	{IP: mustIP("10.0.0.1"), Interface: "docker0", Kind: "bridge"},
	{IP: mustIP("fe80::b482:4dff:fe92:4281"), Interface: "docker0", Kind: "bridge"},
	{IP: mustIP("10.0.2.1"), Interface: "br-b05f8e098ad7", Kind: "bridge"},
	{IP: mustIP("fe80::4047:75ff:fe8e:bb04"), Interface: "vethba736b3", Kind: "virtual"},
	{IP: mustIP("100.110.34.31"), Interface: "tailscale0", Kind: "tunnel"},
	{IP: mustIP("192.168.122.1"), Interface: "virbr0", Kind: "bridge"},
	{IP: mustIP("10.8.0.1"), Interface: "wg0", Kind: "tunnel"},
	// A cloud instance's private address on its uplink, and an ISP's CGNAT
	// address, which shares Tailscale's range, on a second NIC.
	{IP: mustIP("172.31.5.9"), Interface: "eth0", Kind: "physical", DefaultRoute: true},
	{IP: mustIP("100.72.0.5"), Interface: "eth1", Kind: "physical"},
	// A bridge that carries the default route is the uplink, as on a host
	// whose NIC is enslaved to it.
	{IP: mustIP("192.168.1.20"), Interface: "br-lan", Kind: "bridge", DefaultRoute: true},
}}

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panic("bad test address " + s)
	}
	return ip
}

// Who can reach a private address depends on the interface it is on. A
// provider can map a public address onto the uplink, never onto docker0 or a
// link-local address, and the advice used to say it could for all of them —
// while dropping the catalogue's reason the service is dangerous at all.
func TestAssessJudgesAPrivateAddressByItsInterface(t *testing.T) {
	const redisDanger = "Unauthenticated by default: an exposed Redis is a remote shell"
	for _, c := range []struct {
		address, title, who string
		provider            bool
	}{
		{"10.0.0.1", "Redis is listening on a bridge address", "the containers and virtual machines on docker0", false},
		{"10.0.2.1", "Redis is listening on a bridge address", "the containers and virtual machines on br-b05f8e098ad7", false},
		{"fe80::b482:4dff:fe92:4281", "Redis is listening on a link-local address", "the containers and virtual machines on docker0", false},
		{"fe80::4047:75ff:fe8e:bb04", "Redis is listening on a link-local address", "the containers and virtual machines on vethba736b3", false},
		{"fe80::f816:3eff:fee3:1a48", "Redis is listening on a link-local address", "the machines on the same link", false},
		{"100.110.34.31", "Redis is listening on a tailnet address", "every device on the tailnet", false},
		{"10.8.0.1", "Redis is listening on a VPN address", "the peers on wg0", false},
		{"172.31.5.9", "Redis is listening on a private address", "the machines on that network", true},
		{"100.72.0.5", "Redis is listening on a private address", "the machines on that network", true},
		{"192.168.1.20", "Redis is listening on a private address", "the machines on that network", true},
		// An address on no interface the host listed: judged as the uplink's.
		{"10.99.0.1", "Redis is listening on a private address", "the machines on that network", true},
	} {
		p := Assess(AssessInput{Network: thisHost, Listeners: []ExposedPort{
			{Port: 6379, Protocol: "tcp", Address: c.address, Process: "redis-server", Exposed: true},
		}})
		f, ok := findingByID(p, "ports.exposed.tcp.6379")
		if !ok {
			t.Errorf("Redis on %s was not reported", c.address)
			continue
		}
		if f.Level != "warning" || f.Title != c.title {
			t.Errorf("Redis on %s = %q %q, want warning %q", c.address, f.Level, f.Title, c.title)
		}
		want := "Anything that can reach " + c.address + " can connect to it: " + c.who
		if !strings.HasPrefix(f.Advice, redisDanger) || !strings.Contains(f.Advice, want) {
			t.Errorf("Redis on %s: advice %q, want the danger and %q", c.address, f.Advice, want)
		}
		if got := strings.Contains(f.Advice, "provider maps a public address"); got != c.provider {
			t.Errorf("Redis on %s: advice %q names a provider's mapping: %v, want %v", c.address, f.Advice, got, c.provider)
		}
	}
}

// The Docker API on a bridge is still root on the host for every container
// on it, and the advice says so.
func TestAssessKeepsTheDangerForAPortShortOfTheInternet(t *testing.T) {
	p := Assess(AssessInput{Network: thisHost, Listeners: []ExposedPort{
		{Port: 2375, Protocol: "tcp", Address: "10.0.0.1", Process: "dockerd", Exposed: true},
	}})
	f, ok := findingByID(p, "ports.exposed.tcp.2375")
	if !ok || !strings.Contains(f.Advice, "Reaching the Docker API is equivalent to being root on this host.") {
		t.Errorf("finding = %+v, want the Docker API's danger in the advice", f)
	}
}

// An open resolver or a remote desktop is a danger from the internet. On a
// bridge or a VPN it is doing its job — libvirt's dnsmasq answering its VMs —
// and was a permanent warning that repeated the provider-mapping clause.
func TestAssessPassesOverAnInternetOnlyDangerTheInternetCannotReach(t *testing.T) {
	for _, c := range []struct {
		port     uint32
		protocol string
		address  string
		want     string
	}{
		{53, "udp", "192.168.122.1", ""},
		{53, "udp", "fe80::b482:4dff:fe92:4281", ""},
		{53, "udp", "100.110.34.31", ""},
		{3389, "tcp", "100.110.34.31", ""},
		{5900, "tcp", "10.8.0.1", ""},
		{5900, "tcp", "10.0.0.1", ""},
		// On the uplink a provider may forward it, and in public it is open.
		{53, "udp", "172.31.5.9", "warning"},
		{53, "udp", "57.131.21.87", "critical"},
		{3389, "tcp", "0.0.0.0", "critical"},
	} {
		p := Assess(AssessInput{Network: thisHost, Listeners: []ExposedPort{
			{Port: c.port, Protocol: c.protocol, Address: c.address, Exposed: true},
		}})
		id := fmt.Sprintf("ports.exposed.%s.%d", c.protocol, c.port)
		f, ok := findingByID(p, id)
		switch {
		case c.want == "" && ok:
			t.Errorf("%s on %s was reported: %+v", id, c.address, f)
		case c.want != "" && (!ok || f.Level != c.want):
			t.Errorf("%s on %s = %+v (found %v), want %s", id, c.address, f, ok, c.want)
		}
	}
}

// A database on docker0 and on the uplink's private address is one finding,
// at the uplink's reach, which a provider may forward.
func TestAssessPrefersTheUplinkOverABridge(t *testing.T) {
	p := Assess(AssessInput{Network: thisHost, Listeners: []ExposedPort{
		{Port: 5432, Protocol: "tcp", Address: "10.0.0.1", Process: "postgres", Exposed: true},
		{Port: 5432, Protocol: "tcp", Address: "172.31.5.9", Process: "postgres", Exposed: true},
		{Port: 5432, Protocol: "tcp", Address: "fe80::b482:4dff:fe92:4281", Process: "postgres", Exposed: true},
	}})
	f, ok := findingByID(p, "ports.exposed.tcp.5432")
	if !ok || f.Title != "PostgreSQL is listening on a private address" || !strings.Contains(f.Detail, "172.31.5.9") {
		t.Errorf("finding = %+v, want the uplink's bind", f)
	}
}

func TestReachGradesABindForThePortsPage(t *testing.T) {
	for address, want := range map[string]Reach{
		"0.0.0.0":                   ReachAll,
		"::":                        ReachAll,
		"57.131.21.87":              ReachPublic,
		"2001:41d0:2005:100::13":    ReachPublic,
		"127.0.0.53":                ReachLoopback,
		"::1":                       ReachLoopback,
		"10.0.0.1":                  ReachHost,
		"fe80::b482:4dff:fe92:4281": ReachHost,
		"192.168.122.1":             ReachHost,
		"100.110.34.31":             ReachNetwork,
		"10.8.0.1":                  ReachNetwork,
		"172.31.5.9":                ReachNetwork,
		"fe80::f816:3eff:fee3:1a48": ReachNetwork,
		"192.168.1.20":              ReachNetwork,
	} {
		if got := thisHost.Reach(address); got != want {
			t.Errorf("Reach(%s) = %q, want %q", address, got, want)
		}
	}
	// Knowing no interfaces, a private address is judged as the uplink's.
	if got := (HostNetwork{}).Reach("10.0.0.1"); got != ReachNetwork {
		t.Errorf("Reach(10.0.0.1) on an unknown network = %q, want network", got)
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
