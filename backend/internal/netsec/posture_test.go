package netsec

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gnet "github.com/shirou/gopsutil/v4/net"
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
	{IP: mustIP("2001:41d0:2005:100::13"), Interface: "ens3", Kind: "physical", DefaultRoute: true},
	{IP: mustIP("fe80::f816:3eff:fee3:1a48"), Interface: "ens3", Kind: "physical", DefaultRoute: true},
	{IP: mustIP("10.0.0.1"), Interface: "docker0", Kind: "bridge"},
	{IP: mustIP("fe80::b482:4dff:fe92:4281"), Interface: "docker0", Kind: "bridge"},
	{IP: mustIP("10.0.2.1"), Interface: "br-b05f8e098ad7", Kind: "bridge"},
	{IP: mustIP("fe80::4047:75ff:fe8e:bb04"), Interface: "vethba736b3", Kind: "virtual"},
	{IP: mustIP("100.110.34.31"), Interface: "tailscale0", Kind: "tunnel"},
	{IP: mustIP("fd7a:115c:a1e0::9e37:2220"), Interface: "tailscale0", Kind: "tunnel"},
	{IP: mustIP("fe80::1890:7917:9cdb:1fa5"), Interface: "tailscale0", Kind: "tunnel"},
	{IP: mustIP("192.168.122.1"), Interface: "virbr0", Kind: "bridge"},
	{IP: mustIP("10.8.0.1"), Interface: "wg0", Kind: "tunnel"},
	// A cloud instance's private address on its uplink, and an ISP's CGNAT
	// address, which shares Tailscale's range, on a second NIC.
	{IP: mustIP("172.31.5.9"), Interface: "eth0", Kind: "physical", DefaultRoute: true},
	{IP: mustIP("100.72.0.5"), Interface: "eth1", Kind: "physical"},
	// A bridge that carries the default route is the uplink whatever is
	// enslaved to it.
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

// LXD's and Incus's dnsmasq, Podman's aardvark-dns and a Proxmox internal
// network's resolver answer their guests on bridges no name rule knew, and
// were a permanent open-resolver warning with the provider-mapping clause.
// The kernel's link table says they are bridges with only guests behind them;
// and a bridge holding a second NIC, whatever it is called, is that LAN, so a
// Redis on it is not said to be the containers' alone.
func TestAssessGradesABridgeByWhatTheKernelSaysIsOnIt(t *testing.T) {
	iface := func(name string, addrs ...string) gnet.InterfaceStat {
		ifc := gnet.InterfaceStat{Name: name, Flags: []string{"up"}}
		for _, a := range addrs {
			ifc.Addrs = append(ifc.Addrs, gnet.InterfaceAddr{Addr: a})
		}
		return ifc
	}
	network := hostNetworkFrom(gnet.InterfaceStatList{
		iface("ens3", "57.131.21.87/32"),
		iface("lxdbr0", "10.20.30.1/24", "fd42:5c1e:9a2b:1::1/64"),
		iface("incusbr0", "10.40.0.1/24"),
		iface("podman1", "10.89.0.1/24"),
		iface("vmbr1", "10.10.10.1/24"),
		iface("br-lan", "192.168.50.2/24"),
		iface("eth1"),
	}, map[string]bool{"ens3": true}, classifyLinks([]link{
		{name: "ens3", index: 2},
		{name: "lxdbr0", index: 3, kind: "bridge"},
		{name: "veth1a2b3c4d", index: 4, master: 3, kind: "veth"},
		{name: "incusbr0", index: 5, kind: "bridge"},
		{name: "tapa1b2c3d4", index: 6, master: 5, kind: "tun"},
		{name: "podman1", index: 7, kind: "bridge"},
		{name: "veth0", index: 8, master: 7, kind: "veth"},
		{name: "br-lan", index: 9, kind: "bridge"},
		{name: "eth1", index: 10, master: 9},
		{name: "vmbr1", index: 11, kind: "bridge"},
		{name: "tap100i1", index: 12, master: 11, kind: "tun"},
	}))
	for _, address := range []string{"10.20.30.1", "fd42:5c1e:9a2b:1::1", "10.40.0.1", "10.89.0.1", "10.10.10.1"} {
		if got := network.Reach(address); got != ReachHost {
			t.Errorf("Reach(%s) = %q, want host", address, got)
		}
		p := Assess(AssessInput{Network: network, Listeners: []ExposedPort{
			{Port: 53, Protocol: "udp", Address: address, Process: "dnsmasq", Exposed: true},
		}})
		if f, ok := findingByID(p, "ports.exposed.udp.53"); ok {
			t.Errorf("DNS on %s was reported: %+v", address, f)
		}
	}

	if got := network.Reach("192.168.50.2"); got != ReachNetwork {
		t.Errorf("Reach(192.168.50.2) on a bridge holding eth1 = %q, want network", got)
	}
	p := Assess(AssessInput{Network: network, Listeners: []ExposedPort{
		{Port: 6379, Protocol: "tcp", Address: "192.168.50.2", Process: "redis-server", Exposed: true},
	}})
	f, ok := findingByID(p, "ports.exposed.tcp.6379")
	if !ok || f.Level != "warning" || f.Title != "Redis is listening on a private address" ||
		!strings.Contains(f.Advice, "can connect to it: the machines on that network") {
		t.Errorf("Redis on br-lan = %+v (found %v), want a warning that the LAN can reach it", f, ok)
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

// Each bind names the network it is on and the interface holding it, so the
// ports page can say "Tailnet only · tailscale0" where the grade alone says
// "network". The addresses are this host's: its public uplink ens3 in both
// families, the tailnet in both, docker0 and a compose network's bridge.
func TestPlaceNamesTheNetworkAndItsInterface(t *testing.T) {
	for _, c := range []struct {
		address string
		want    Placement
	}{
		{"0.0.0.0", Placement{ReachAll, NetworkAll, ""}},
		{"::", Placement{ReachAll, NetworkAll, ""}},
		{"127.0.0.53", Placement{ReachLoopback, NetworkLoopback, ""}},
		{"::1", Placement{ReachLoopback, NetworkLoopback, ""}},
		{"57.131.21.87", Placement{ReachPublic, NetworkPublic, "ens3"}},
		{"2001:41d0:2005:100::13", Placement{ReachPublic, NetworkPublic, "ens3"}},
		{"100.110.34.31", Placement{ReachNetwork, NetworkTailnet, "tailscale0"}},
		{"fd7a:115c:a1e0::9e37:2220", Placement{ReachNetwork, NetworkTailnet, "tailscale0"}},
		{"10.8.0.1", Placement{ReachNetwork, NetworkVPN, "wg0"}},
		{"10.0.0.1", Placement{ReachHost, NetworkDocker, "docker0"}},
		{"fe80::b482:4dff:fe92:4281", Placement{ReachHost, NetworkDocker, "docker0"}},
		{"10.0.2.1", Placement{ReachHost, NetworkDocker, "br-b05f8e098ad7"}},
		{"192.168.122.1", Placement{ReachHost, NetworkBridge, "virbr0"}},
		{"fe80::4047:75ff:fe8e:bb04", Placement{ReachHost, NetworkBridge, "vethba736b3"}},
		{"fe80::f816:3eff:fee3:1a48", Placement{ReachNetwork, NetworkLinkLocal, "ens3"}},
		{"fe80::1890:7917:9cdb:1fa5", Placement{ReachNetwork, NetworkLinkLocal, "tailscale0"}},
		// A cloud instance's private address on the interface carrying the
		// default route: the uplink, which its provider may map the public
		// address onto.
		{"172.31.5.9", Placement{ReachNetwork, NetworkUplink, "eth0"}},
		// A second NIC on an ISP's CGNAT, which shares Tailscale's range, is
		// that ISP's network and not the tailnet.
		{"100.72.0.5", Placement{ReachNetwork, NetworkPrivate, "eth1"}},
		// A bridge that carries the default route is the uplink.
		{"192.168.1.20", Placement{ReachNetwork, NetworkUplink, "br-lan"}},
		// On no interface the host listed: graded by the address alone.
		{"10.99.0.1", Placement{ReachNetwork, NetworkPrivate, ""}},
		{"203.0.113.5", Placement{ReachPublic, NetworkPublic, ""}},
		{"100.64.1.2", Placement{ReachNetwork, NetworkTailnet, ""}},
	} {
		if got := thisHost.Place(c.address); got != c.want {
			t.Errorf("Place(%s) = %+v, want %+v", c.address, got, c.want)
		}
		if got := thisHost.Reach(c.address); got != c.want.Reach {
			t.Errorf("Reach(%s) = %q, Place says %q", c.address, got, c.want.Reach)
		}
	}
	// Knowing no interfaces — the list could not be read — the address
	// alone still tells the tailnet from a private network, and names none.
	for address, want := range map[string]Placement{
		"100.110.34.31": {ReachNetwork, NetworkTailnet, ""},
		"10.0.0.1":      {ReachNetwork, NetworkPrivate, ""},
		"57.131.21.87":  {ReachPublic, NetworkPublic, ""},
	} {
		if got := (HostNetwork{}).Place(address); got != want {
			t.Errorf("Place(%s) on an unknown network = %+v, want %+v", address, got, want)
		}
	}
}

// The posture's finding names the interface a database is bound on, as the
// ports page does, and levels it by the same placement: a tailnet address is
// a warning, a public one critical.
func TestAssessNamesTheInterfaceADatabaseIsBoundOn(t *testing.T) {
	for _, c := range []struct {
		address, level, detail string
	}{
		{"100.110.34.31", "warning", "TCP/6379 is bound to 100.110.34.31 on tailscale0 by redis-server"},
		{"57.131.21.87", "critical", "TCP/6379 is bound to 57.131.21.87 on ens3 by redis-server"},
		{"10.0.2.1", "warning", "TCP/6379 is bound to 10.0.2.1 on br-b05f8e098ad7 by redis-server"},
		{"0.0.0.0", "critical", "TCP/6379 is bound to every interface by redis-server"},
		{"10.99.0.1", "warning", "TCP/6379 is bound to 10.99.0.1 by redis-server"},
	} {
		p := Assess(AssessInput{Network: thisHost, Listeners: []ExposedPort{
			{Port: 6379, Protocol: "tcp", Address: c.address, Process: "redis-server", Exposed: true},
		}})
		f, ok := findingByID(p, "ports.exposed.tcp.6379")
		if !ok || f.Level != c.level || f.Detail != c.detail {
			t.Errorf("Redis on %s = %+v (found %v), want %s %q", c.address, f, ok, c.level, c.detail)
		}
	}
}

// A socket's grade is the level of the posture's finding for it, from the
// same rules: the ports page and the proxy overview colour a database by it,
// and on a host whose firewall denies inbound by default they used to call
// critical what the posture called a warning.
func TestGradePortIsThePosturesLevel(t *testing.T) {
	firewalls := map[string]*FirewallStatus{
		"none":         nil,
		"deny":         {Available: true, Enabled: true, Policy: DefaultPolicy{Incoming: "deny"}},
		"reject":       {Available: true, Enabled: true, Policy: DefaultPolicy{Incoming: "reject"}},
		"allow":        {Available: true, Enabled: true, Policy: DefaultPolicy{Incoming: "allow"}},
		"inactive":     {Available: true, Enabled: false, Policy: DefaultPolicy{Incoming: "deny"}},
		"policy unset": {Available: true, Enabled: true},
		"deny, rule 10 allows 6379": {Backend: BackendUFW, Available: true, Enabled: true,
			Policy: DefaultPolicy{Incoming: "deny"}, Rules: ufwRules(
				"[ 2] OpenSSH                    ALLOW IN    Anywhere",
				"[10] 6379/tcp                   ALLOW IN    Anywhere",
			)},
	}
	sockets := []ExposedPort{
		{Port: 6379, Protocol: "tcp", Address: "0.0.0.0", Process: "redis-server", Exposed: true},
		// Docker's Postgres, published as -p 5432:5432.
		{Port: 5432, Protocol: "tcp", Address: "0.0.0.0", Process: "docker-proxy", Exposed: true},
		{Port: 5432, Protocol: "tcp", Address: "::", Process: "docker-proxy", Exposed: true},
		{Port: 5432, Protocol: "tcp", Address: "10.0.0.1", Process: "docker-proxy", Exposed: true},
		{Port: 6379, Protocol: "tcp", Address: "::", Process: "redis-server", Exposed: true},
		{Port: 5432, Protocol: "tcp", Address: "57.131.21.87", Process: "postgres", Exposed: true},
		{Port: 5432, Protocol: "tcp", Address: "100.110.34.31", Process: "postgres", Exposed: true},
		{Port: 27017, Protocol: "tcp", Address: "10.0.0.1", Process: "mongod", Exposed: true},
		{Port: 2375, Protocol: "tcp", Address: "172.31.5.9", Process: "dockerd", Exposed: true},
		{Port: 53, Protocol: "udp", Address: "0.0.0.0", Process: "dnsmasq", Exposed: true},
		{Port: 3389, Protocol: "tcp", Address: "172.31.5.9", Process: "xrdp", Exposed: true},
		// What the posture raises nothing for grades as nothing.
		{Port: 53, Protocol: "udp", Address: "192.168.122.1", Process: "dnsmasq", Exposed: true},
		{Port: 5432, Protocol: "tcp", Address: "127.0.0.1", Process: "postgres", Exposed: false},
		{Port: 443, Protocol: "tcp", Address: "0.0.0.0", Process: "nginx", Exposed: true},
		{Port: 6379, Protocol: "udp", Address: "0.0.0.0", Process: "redis-server", Exposed: true},
	}
	for name, firewall := range firewalls {
		for _, l := range sockets {
			grade := GradePort(l, thisHost, firewall)
			p := Assess(AssessInput{Network: thisHost, Firewall: firewall, Listeners: []ExposedPort{l}})
			f, found := findingByID(p, fmt.Sprintf("ports.exposed.%s.%d", l.Protocol, l.Port))
			where := fmt.Sprintf("%s/%d on %s behind %s", l.Protocol, l.Port, l.Address, name)
			if !found {
				if grade != (PortGrade{}) {
					t.Errorf("%s: graded %+v, the posture raises nothing", where, grade)
				}
				continue
			}
			if grade.Level != f.Level {
				t.Errorf("%s: graded %q, the posture says %q", where, grade.Level, f.Level)
			}
			clause := ", though the firewall's inbound default is " + grade.InboundDefault
			if (grade.InboundDefault != "") != strings.HasSuffix(f.Detail, clause) {
				t.Errorf("%s: inbound default %q, the posture's detail %q", where, grade.InboundDefault, f.Detail)
			}
			past := map[PastFirewall]string{
				PastFirewallDocker: ", published by Docker past the firewall's inbound default",
				PastFirewallRule:   fmt.Sprintf(", and firewall rule %d admits it from anywhere", grade.FirewallRule),
			}[grade.PastFirewall]
			if (grade.PastFirewall != "") != (past != "" && strings.HasSuffix(f.Detail, past)) {
				t.Errorf("%s: past the firewall %+v, the posture's detail %q", where, grade, f.Detail)
			}
		}
	}

	// The reviewer's case: Redis on every interface behind ufw's deny.
	redis := GradePort(sockets[0], thisHost, firewalls["deny"])
	if redis != (PortGrade{Level: "warning", InboundDefault: "deny"}) {
		t.Errorf("Redis on 0.0.0.0 behind deny = %+v, want a warning naming deny", redis)
	}
	if got := GradePort(sockets[0], thisHost, nil); got != (PortGrade{Level: "critical"}) {
		t.Errorf("Redis on 0.0.0.0 with no firewall = %+v, want critical", got)
	}
	if got := GradePort(sockets[6], thisHost, nil); got != (PortGrade{Level: "warning"}) {
		t.Errorf("Postgres on the tailnet = %+v, want a warning", got)
	}

	// Docker forwards a port it publishes before ufw's input chain, where
	// the inbound default is, so the default holds nothing back: Docker's
	// Postgres on every interface is the internet's whatever ufw says.
	for _, name := range []string{"deny", "reject", "deny, rule 10 allows 6379"} {
		for _, socket := range sockets[1:3] {
			if got := GradePort(socket, thisHost, firewalls[name]); got != (PortGrade{Level: "critical", PastFirewall: PastFirewallDocker}) {
				t.Errorf("docker-proxy on %s:5432 behind %s = %+v, want critical, published past the firewall", socket.Address, name, got)
			}
		}
	}
	// Published on the bridge's address, it is still only the containers'.
	if got := GradePort(sockets[3], thisHost, firewalls["deny"]); got != (PortGrade{Level: "warning", PastFirewall: PastFirewallDocker}) {
		t.Errorf("docker-proxy on the bridge behind deny = %+v, want a warning published past the firewall", got)
	}
	// A rule admitting the port from anywhere is met before the default.
	if got := GradePort(sockets[0], thisHost, firewalls["deny, rule 10 allows 6379"]); got != (PortGrade{Level: "critical", PastFirewall: PastFirewallRule, FirewallRule: 10}) {
		t.Errorf("Redis on 0.0.0.0 behind deny with 6379 allowed = %+v, want critical, admitted by rule 10", got)
	}

	p := Assess(AssessInput{Network: thisHost, Firewall: firewalls["deny"], Listeners: sockets[1:3]})
	if f, _ := findingByID(p, "ports.exposed.tcp.5432"); f.Level != "critical" ||
		f.Detail != "TCP/5432 is bound to every interface by docker-proxy, published by Docker past the firewall's inbound default" {
		t.Errorf("Docker's Postgres behind deny = %+v, want critical and why the firewall does not hold it", f)
	}
	p = Assess(AssessInput{Network: thisHost, Firewall: firewalls["deny, rule 10 allows 6379"], Listeners: sockets[:1]})
	if f, _ := findingByID(p, "ports.exposed.tcp.6379"); f.Level != "critical" ||
		f.Detail != "TCP/6379 is bound to every interface by redis-server, and firewall rule 10 admits it from anywhere" {
		t.Errorf("Redis behind deny with 6379 allowed = %+v, want critical and the rule named", f)
	}
}

// ufwRules parses lines of `ufw status numbered` as the backend does.
func ufwRules(lines ...string) []Rule {
	rules := []Rule{}
	for _, line := range lines {
		m := ufwNumberedRe.FindStringSubmatch(line)
		num, _ := strconv.Atoi(m[1])
		r := parseUFWRule(num, m[2])
		annotateRule(&r)
		rules = append(rules, r)
	}
	return rules
}

// Which rule, if any, lets a connection from anywhere reach a database
// before the inbound default refuses it, read from each backend's listing as
// the backend reads it. Every case is Redis or VNC, dangerous on every
// interface, behind a default that refuses inbound.
func TestGradePortReadsWhichRuleAdmitsAPort(t *testing.T) {
	redis := ExposedPort{Port: 6379, Protocol: "tcp", Address: "0.0.0.0", Process: "redis-server", Exposed: true}
	onTailnet := ExposedPort{Port: 6379, Protocol: "tcp", Address: "100.110.34.31", Process: "redis-server", Exposed: true}
	vnc := ExposedPort{Port: 5900, Protocol: "tcp", Address: "0.0.0.0", Process: "Xvnc", Exposed: true}
	held := PortGrade{Level: "warning", InboundDefault: "deny"}
	// firewalld's "default" target rejects.
	rejected := PortGrade{Level: "warning", InboundDefault: "reject"}
	admitted := func(rule int) PortGrade {
		return PortGrade{Level: "critical", PastFirewall: PastFirewallRule, FirewallRule: rule}
	}
	ufw := func(lines ...string) *FirewallStatus {
		return &FirewallStatus{Backend: BackendUFW, Available: true, Enabled: true,
			Policy: DefaultPolicy{Incoming: "deny"}, Rules: ufwRules(lines...)}
	}
	firewalld := func(zone string) *FirewallStatus {
		_, rules := parseFirewalldZone(zone)
		for i := range rules {
			rules[i].Number = i + 1
			annotateRule(&rules[i])
		}
		return &FirewallStatus{Backend: BackendFirewalld, Available: true, Enabled: true,
			Policy: firewalldPolicy("default"), Rules: rules}
	}
	iptables := func(listing string) *FirewallStatus {
		withIPTablesOutput(t, listing)
		st, err := (iptablesBackend{}).Status(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	const chain = "Chain INPUT (policy DROP 0 packets, 0 bytes)\n" +
		"num   pkts bytes target     prot opt in     out     source               destination\n"

	for _, c := range []struct {
		name     string
		socket   ExposedPort
		firewall *FirewallStatus
		want     PortGrade
	}{
		{"ufw: the port alone, for both protocols", redis, ufw("[ 3] 6379                       ALLOW IN    Anywhere"), admitted(3)},
		{"ufw: the port in a list", redis, ufw("[ 4] 80,443,6379/tcp            ALLOW IN    Anywhere"), admitted(4)},
		{"ufw: the port in a range", redis, ufw("[ 5] 6000:7000/tcp              ALLOW IN    Anywhere"), admitted(5)},
		{"ufw: rate-limited is still let in", redis, ufw("[ 1] 6379/tcp                   LIMIT IN    Anywhere"), admitted(1)},
		{"ufw: everything from anywhere", redis, ufw("[ 1] Anywhere                   ALLOW IN    Anywhere"), admitted(1)},
		{"ufw: the IPv6 twin alone", redis, ufw("[ 7] 6379/tcp (v6)              ALLOW IN    Anywhere (v6)"), admitted(7)},
		{"ufw: to the public address", redis, ufw("[ 2] 57.131.21.87 6379/tcp      ALLOW IN    Anywhere"), admitted(2)},
		{"ufw: to the private uplink a provider maps", redis, ufw("[ 2] 172.31.5.9 6379/tcp        ALLOW IN    Anywhere"), admitted(2)},
		{"ufw: to the tailnet's address, on the tailnet socket", onTailnet,
			ufw("[ 2] 100.110.34.31 6379/tcp     ALLOW IN    Anywhere"),
			PortGrade{Level: "warning", PastFirewall: PastFirewallRule, FirewallRule: 2}},
		{"ufw: a refusal met first ends it", redis, ufw(
			"[ 1] 6379/tcp                   DENY IN     Anywhere",
			"[ 2] 6379/tcp                   ALLOW IN    Anywhere",
		), held},
		{"ufw: a refusal met second does not", redis, ufw(
			"[ 1] 6379/tcp                   ALLOW IN    Anywhere",
			"[ 2] 6379/tcp                   DENY IN     Anywhere",
		), admitted(1)},
		{"ufw: a refusal on one address leaves the rest", redis, ufw(
			"[ 1] 10.0.0.5 6379/tcp          DENY IN     Anywhere",
			"[ 2] 6379/tcp                   ALLOW IN    Anywhere",
		), admitted(2)},
		{"ufw: another port", redis, ufw("[ 1] 5432/tcp                   ALLOW IN    Anywhere"), held},
		{"ufw: the other protocol", redis, ufw("[ 1] 6379/udp                   ALLOW IN    Anywhere"), held},
		{"ufw: from a private source", redis, ufw("[ 1] 6379/tcp                   ALLOW IN    10.0.0.0/8"), held},
		{"ufw: outbound", redis, ufw("[ 1] 6379/tcp                   ALLOW OUT   Anywhere"), held},
		{"ufw: a route rule", redis, ufw("[ 1] 6379/tcp                   ALLOW FWD   Anywhere"), held},
		{"ufw: an application profile", redis, ufw("[ 1] OpenSSH                    ALLOW IN    Anywhere"), held},
		{"ufw: on the tailnet's interface", redis, ufw("[ 1] 6379/tcp on tailscale0     ALLOW IN    Anywhere"), held},
		{"ufw: to the tailnet's address", redis, ufw("[ 2] 100.110.34.31 6379/tcp     ALLOW IN    Anywhere"), held},
		{"ufw: to a bridge's address", redis, ufw("[ 2] 10.0.0.1 6379/tcp          ALLOW IN    Anywhere"), held},

		{"firewalld: a zone port", redis, firewalld("public (active)\n  target: default\n  services: ssh\n  ports: 6379/tcp\n"), admitted(2)},
		{"firewalld: a port range", vnc, firewalld("public (active)\n  target: default\n  ports: 5900-5903/tcp\n"), admitted(1)},
		{"firewalld: a predefined service", redis, firewalld("public (active)\n  target: default\n  services: ssh redis\n"), admitted(2)},
		{"firewalld: a service over a range", vnc, firewalld("public (active)\n  target: default\n  services: vnc-server\n"), admitted(1)},
		{"firewalld: a rich rule from anywhere", redis, firewalld("public (active)\n  target: default\n  rich rules:\n\trule family=\"ipv4\" port port=\"6379\" protocol=\"tcp\" accept\n"), admitted(1)},
		{"firewalld: other services", redis, firewalld("public (active)\n  target: default\n  services: ssh dhcpv6-client cockpit\n"), rejected},
		{"firewalld: a rich rule from a private source", redis, firewalld("public (active)\n  target: default\n  rich rules:\n\trule family=\"ipv4\" source address=\"10.0.0.0/8\" port port=\"6379\" protocol=\"tcp\" accept\n"), rejected},

		{"iptables: INPUT accepts the port", redis, iptables(chain +
			"1        0     0 ACCEPT     tcp  --  *      *       0.0.0.0/0            0.0.0.0/0            tcp dpt:6379\n"), admitted(1)},
		{"iptables: in a multiport list", redis, iptables(chain +
			"1        0     0 ACCEPT     tcp  --  *      *       0.0.0.0/0            0.0.0.0/0            multiport dports 80,443,6379\n"), admitted(1)},
		{"iptables: in a range", redis, iptables(chain +
			"1        0     0 ACCEPT     tcp  --  *      *       0.0.0.0/0            0.0.0.0/0            tcp dpts:6000:7000\n"), admitted(1)},
		{"iptables: a drop does not end it", redis, iptables(chain +
			"1        0     0 DROP       tcp  --  eth1   *       0.0.0.0/0            0.0.0.0/0            tcp dpt:6379\n" +
			"2        0     0 ACCEPT     tcp  --  *      *       0.0.0.0/0            0.0.0.0/0            tcp dpt:6379\n"), admitted(2)},
		{"iptables: loopback, established and other ports", redis, iptables(chain +
			"1        0     0 ACCEPT     all  --  lo     *       0.0.0.0/0            0.0.0.0/0\n" +
			"2        0     0 ACCEPT     all  --  *      *       0.0.0.0/0            0.0.0.0/0            ctstate RELATED,ESTABLISHED\n" +
			"3        0     0 ACCEPT     tcp  --  *      *       0.0.0.0/0            0.0.0.0/0            tcp dpt:22\n" +
			"4        0     0 ACCEPT     tcp  --  *      *       10.0.0.0/8           0.0.0.0/0            tcp dpt:6379\n"), held},
		{"iptables: another chain", redis, iptables(chain +
			"\nChain ufw-user-input (1 references)\n" +
			"num   pkts bytes target     prot opt in     out     source               destination\n" +
			"1        0     0 ACCEPT     tcp  --  *      *       0.0.0.0/0            0.0.0.0/0            tcp dpt:6379\n"), held},
	} {
		if got := GradePort(c.socket, thisHost, c.firewall); got != c.want {
			t.Errorf("%s: %+v, want %+v (rules %+v)", c.name, got, c.want, c.firewall.Rules)
		}
	}
}

// A firewall whose inbound default could not be read is not counted on: the
// verbose status is a second call that can fail on its own, and the detail
// then promised a policy it did not name.
func TestAssessDoesNotLeanOnAnUnreadInboundDefault(t *testing.T) {
	p := Assess(AssessInput{
		Firewall:  &FirewallStatus{Available: true, Enabled: true, Capabilities: writableFirewall},
		Listeners: []ExposedPort{{Port: 6379, Protocol: "tcp", Address: "0.0.0.0", Exposed: true}},
	})
	f, ok := findingByID(p, "ports.exposed.tcp.6379")
	if !ok || f.Level != "critical" || strings.Contains(f.Detail, "firewall") {
		t.Errorf("finding = %+v (found %v), want critical with no word about the firewall", f, ok)
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

// A port Docker publishes is past the inbound default whatever holds it:
// docker-proxy where the dashboard cannot see the holder, or nothing at all
// where the userland proxy is off and NAT rules alone forward it.
func TestAPortDockerPublishesIsPastTheDefaultWhateverHoldsIt(t *testing.T) {
	deny := &FirewallStatus{Available: true, Enabled: true, Policy: DefaultPolicy{Incoming: "deny"}}
	for _, l := range []ExposedPort{
		{Port: 5432, Protocol: "tcp", Address: "0.0.0.0", Exposed: true, Published: true},
		{Port: 5432, Protocol: "tcp", Address: "::", Process: "dockerd", Exposed: true, Published: true},
	} {
		if got := GradePort(l, thisHost, deny); got != (PortGrade{Level: "critical", PastFirewall: PastFirewallDocker}) {
			t.Errorf("published %s:5432 held by %q behind deny = %+v, want critical past the firewall", l.Address, l.Process, got)
		}
	}
	nat := ExposedPort{Port: 5432, Protocol: "tcp", Address: "0.0.0.0", Exposed: true, Published: true}
	p := Assess(AssessInput{Network: thisHost, Firewall: deny, Listeners: []ExposedPort{nat}})
	if f, _ := findingByID(p, "ports.exposed.tcp.5432"); f.Level != "critical" ||
		f.Detail != "TCP/5432 is bound to every interface, published by Docker past the firewall's inbound default" {
		t.Errorf("a port Docker's NAT alone publishes = %+v", f)
	}
	// Unpublished, the same socket is held by the default.
	nat.Published = false
	if got := GradePort(nat, thisHost, deny); got != (PortGrade{Level: "warning", InboundDefault: "deny"}) {
		t.Errorf("an unpublished socket behind deny = %+v, want the default's warning", got)
	}
}
