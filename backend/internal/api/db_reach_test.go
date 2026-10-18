package api

import (
	"net"
	"reflect"
	"strconv"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

// A list naming IPv6 needs the port on both families, which Docker spells as
// no address; every other reach has one address of its own.
func TestReachBindingIsTheAddressEachAnswerPublishesOn(t *testing.T) {
	for _, c := range []struct {
		exposure dbExposure
		address  string
		allow    []string
		want     string
	}{
		{exposureLocal, "", nil, "127.0.0.1"},
		{exposurePrivate, "100.101.1.2", nil, "100.101.1.2"},
		{exposurePublic, "", nil, "0.0.0.0"},
		{exposureAllowlist, "", []string{"203.0.113.7"}, "0.0.0.0"},
		{exposureAllowlist, "", []string{"203.0.113.7", "2001:db8::/32"}, ""},
	} {
		if got := reachBinding(c.exposure, c.address, c.allow); got != c.want {
			t.Errorf("%s %v binds %q, want %q", c.exposure, c.allow, got, c.want)
		}
	}
}

// A recreate drops every session, so it happens only where the binding does
// not already give the reach.
func TestBindingServesOnlyWhatItReaches(t *testing.T) {
	v4 := dbReach{Exposure: exposureAllowlist, Allow: []string{"203.0.113.7"}}
	v6 := dbReach{Exposure: exposureAllowlist, Allow: []string{"2001:db8::/32"}}
	for _, c := range []struct {
		current string
		to      dbReach
		want    bool
	}{
		{"127.0.0.1", dbReach{Exposure: exposureLocal}, true},
		{"0.0.0.0", dbReach{Exposure: exposureLocal}, false},
		{"0.0.0.0", dbReach{Exposure: exposurePublic}, true},
		{"", dbReach{Exposure: exposurePublic}, true},
		{"127.0.0.1", dbReach{Exposure: exposurePublic}, false},
		{"0.0.0.0", v4, true},
		{"", v4, true},
		{"10.0.0.5", v4, false},
		{"0.0.0.0", v6, false},
		{"", v6, true},
		{"100.101.1.2", dbReach{Exposure: exposurePrivate, HostIP: "100.101.1.2"}, true},
		{"100.101.1.2", dbReach{Exposure: exposurePrivate, HostIP: "10.0.0.5"}, false},
	} {
		if got := bindingServes(c.current, c.to); got != c.want {
			t.Errorf("a binding on %q serving %+v = %v, want %v", c.current, c.to, got, c.want)
		}
	}
}

// The order is the safety property: the list is kept before the binding can
// widen and released only once nothing needs it.
func TestAccessStepsKeepBeforeTheyWidenAndReleaseLast(t *testing.T) {
	list := []string{"203.0.113.7"}
	local := dbReach{Exposure: exposureLocal, HostIP: "127.0.0.1"}
	public := dbReach{Exposure: exposurePublic, HostIP: "0.0.0.0"}
	allowed := dbReach{Exposure: exposureAllowlist, HostIP: "0.0.0.0", Allow: list}
	for _, c := range []struct {
		name     string
		from, to dbReach
		kept     bool
		want     dbAccessSteps
	}{
		{"anywhere to a list is no recreate", public, allowed, false, dbAccessSteps{Keep: true}},
		{"this server to a list keeps it first", local, allowed, false, dbAccessSteps{Keep: true, Rebind: true}},
		{"a list to anywhere only releases it", dbReach{Exposure: exposureAllowlist, HostIP: "0.0.0.0", Allow: list}, public, true, dbAccessSteps{Release: true}},
		{"a list to this server rebinds then releases", dbReach{Exposure: exposureAllowlist, HostIP: "0.0.0.0", Allow: list}, local, true, dbAccessSteps{Rebind: true, Release: true}},
		{"the same list again changes nothing", dbReach{Exposure: exposureAllowlist, HostIP: "0.0.0.0", Allow: list}, allowed, true, dbAccessSteps{}},
		{"an edited list is kept again", dbReach{Exposure: exposureAllowlist, HostIP: "0.0.0.0", Allow: []string{"192.0.2.1"}}, allowed, true, dbAccessSteps{Keep: true}},
		{"a list a change left behind is taken away", dbReach{Exposure: exposureLocal, HostIP: "127.0.0.1", Allow: list}, local, true, dbAccessSteps{Release: true}},
		{"an IPv6 network needs both families", allowed, dbReach{Exposure: exposureAllowlist, HostIP: "", Allow: []string{"2001:db8::/32"}}, true, dbAccessSteps{Keep: true, Rebind: true}},
		{"one address to another", dbReach{Exposure: exposurePrivate, HostIP: "100.101.1.2"}, dbReach{Exposure: exposurePrivate, HostIP: "10.0.0.5"}, false, dbAccessSteps{Rebind: true}},
	} {
		if got := accessSteps(c.from, c.to, c.kept); got != c.want {
			t.Errorf("%s: steps = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// ufw as it lists a port opened to anywhere by this route — the IPv4 rule and
// its IPv6 copy — beside an operator's own rule for an office and one for
// SSH.
func reachFirewall(extra ...netsec.Rule) *netsec.FirewallStatus {
	comment := dbFirewallComment(5432)
	rules := []netsec.Rule{
		{Number: 1, ID: "fw-ssh", Action: "ALLOW", Direction: "IN", To: "22/tcp", From: "Anywhere", Port: "22", Protocol: "tcp"},
		{Number: 2, ID: "fw-any4", Action: "ALLOW", Direction: "IN", To: "5432/tcp", From: "Anywhere", Port: "5432", Protocol: "tcp", Comment: comment},
		{Number: 3, ID: "fw-office", Action: "ALLOW", Direction: "IN", To: "5432/tcp", From: "198.51.100.0/24", Port: "5432", Protocol: "tcp"},
		{Number: 4, ID: "fw-any6", Action: "ALLOW", Direction: "IN", To: "5432/tcp", From: "Anywhere", Port: "5432", Protocol: "tcp", Comment: comment, IPv6: true},
	}
	return &netsec.FirewallStatus{Backend: netsec.BackendUFW, Available: true, Enabled: true, Rules: append(rules, extra...)}
}

func opsOf(ops []netsec.PlanOperation) []string {
	out := []string{}
	for _, op := range ops {
		if op.Op == "add" {
			out = append(out, "add "+op.Rule.From)
			continue
		}
		out = append(out, op.Op+" "+op.RuleID)
	}
	return out
}

func TestTheFirewallIsGivenTheRulesTheReachWantsAndOnlyItsOwnAreRemoved(t *testing.T) {
	list := dbReach{Exposure: exposureAllowlist, Allow: []string{"198.51.100.0/24", "203.0.113.7", "2001:db8::/32"}}
	got := opsOf(dbFirewallPlan(reachFirewall(), 5432, dbFirewallWant(list, 5432), list.Allow))
	// The office already has a rule of the operator's, which does the job and
	// stays theirs; the rule from anywhere goes once, its IPv6 copy with it.
	want := []string{"add 203.0.113.7", "add 2001:db8::/32", "delete fw-any4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("anywhere to a list = %v, want %v", got, want)
	}

	comment := dbFirewallComment(5432)
	listed := reachFirewall(
		netsec.Rule{Number: 5, ID: "fw-a", Action: "ALLOW", Direction: "IN", To: "5432/tcp", From: "203.0.113.7", Port: "5432", Protocol: "tcp", Comment: comment},
		netsec.Rule{Number: 6, ID: "fw-b", Action: "ALLOW", Direction: "IN", To: "5432/tcp", From: "2001:db8::/32", Port: "5432", Protocol: "tcp", Comment: comment, IPv6: true},
	)
	edited := dbReach{Exposure: exposureAllowlist, Allow: []string{"192.0.2.10", "2001:db8::/32"}}
	got = opsOf(dbFirewallPlan(listed, 5432, dbFirewallWant(edited, 5432), append(list.Allow, edited.Allow...)))
	want = []string{"add 192.0.2.10", "delete fw-any4", "delete fw-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("an edited list = %v, want %v", got, want)
	}

	// Back to this server: every rule of this route's goes, the operator's
	// own for the office and for SSH do not.
	got = opsOf(dbFirewallPlan(listed, 5432, dbFirewallWant(dbReach{Exposure: exposureLocal}, 5432), list.Allow))
	want = []string{"delete fw-any4", "delete fw-a", "delete fw-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("to this server = %v, want %v", got, want)
	}

	// Anywhere where a rule from anywhere stands is nothing to do.
	if got := dbFirewallPlan(reachFirewall(), 5432, dbFirewallWant(dbReach{Exposure: exposurePublic}, 5432), nil); len(got) != 0 {
		t.Fatalf("anywhere again = %v", opsOf(got))
	}
	if got := dbFirewallWord(nil); got != "already" {
		t.Errorf("no operations read as %q", got)
	}
}

// firewalld keeps no comments, so a rule this route wrote for a listed network
// is known by its shape; a rule from anywhere could be anybody's and is left.
func TestOnFirewalldThisRoutesRulesAreKnownByShape(t *testing.T) {
	st := &netsec.FirewallStatus{Backend: netsec.BackendFirewalld, Available: true, Enabled: true, Rules: []netsec.Rule{
		{Number: 1, ID: "fw-any", Action: "ALLOW", To: "5432/tcp", From: "Anywhere", Port: "5432", Protocol: "tcp"},
		{Number: 2, ID: "fw-a", Action: "ALLOW", To: "5432/tcp", From: "203.0.113.7/32", Port: "5432", Protocol: "tcp"},
		{Number: 3, ID: "fw-other", Action: "ALLOW", To: "5432/tcp", From: "192.0.2.0/24", Port: "5432", Protocol: "tcp"},
	}}
	got := opsOf(dbFirewallPlan(st, 5432, nil, []string{"203.0.113.7"}))
	if want := []string{"delete fw-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("firewalld = %v, want %v", got, want)
	}
}

// A rule this route did not write the shape of is never its own: another
// port, another protocol, a destination address, one interface.
func TestOnlyThePortsOwnShapeIsThisRoutes(t *testing.T) {
	comment := dbFirewallComment(5432)
	st := &netsec.FirewallStatus{Backend: netsec.BackendUFW, Available: true, Enabled: true, Rules: []netsec.Rule{
		{ID: "fw-udp", Action: "ALLOW", Direction: "IN", To: "5432/udp", From: "Anywhere", Port: "5432", Protocol: "udp", Comment: comment},
		{ID: "fw-dest", Action: "ALLOW", Direction: "IN", To: "10.0.0.5 5432/tcp", From: "Anywhere", Port: "5432", Protocol: "tcp", Comment: comment},
		{ID: "fw-on", Action: "ALLOW", Direction: "IN", To: "5432/tcp", From: "Anywhere", Port: "5432", Protocol: "tcp", Comment: comment, Interface: "eth0"},
		{ID: "fw-out", Action: "ALLOW", Direction: "OUT", To: "5432/tcp", From: "Anywhere", Port: "5432", Protocol: "tcp", Comment: comment},
		{ID: "fw-deny", Action: "DENY", Direction: "IN", To: "5432/tcp", From: "Anywhere", Port: "5432", Protocol: "tcp", Comment: comment},
	}}
	if got := dbFirewallOwned(st, 5432, nil); len(got) != 0 {
		t.Fatalf("owned = %+v", got)
	}
}

func TestPlansAreChunkedInOrder(t *testing.T) {
	var ops []netsec.PlanOperation
	for i := range 33 {
		op := netsec.PlanOperation{Op: "delete", RuleID: "fw-" + strconv.Itoa(i)}
		if i < 16 {
			op = netsec.PlanOperation{Op: "add", Rule: &netsec.RuleRequest{From: "192.0.2." + strconv.Itoa(i)}}
		}
		ops = append(ops, op)
	}
	chunks := planChunks(ops)
	if len(chunks) != 2 || len(chunks[0]) != netsec.MaxPlanOperations || len(chunks[1]) != 13 {
		t.Fatalf("chunks of %d", len(chunks))
	}
	if chunks[1][0].RuleID != "fw-20" || chunks[0][15].Op != "add" || chunks[0][16].Op != "delete" {
		t.Fatal("the order did not survive the split")
	}
	if got := dbFirewallWord(ops); got != "updated" {
		t.Errorf("adds and removals read as %q", got)
	}
}

// The addresses offered for one network are the tailnet's, a VPN's and a
// LAN's, tailnet first, in the words of who they face.
func TestBindableAddressesAreTheNetworksThePortCanBeKeptTo(t *testing.T) {
	n := netsec.HostNetwork{Addresses: []netsec.HostAddress{
		{IP: net.ParseIP("127.0.0.1"), Interface: "lo", Kind: "loopback"},
		{IP: net.ParseIP("192.168.1.10"), Interface: "eth0", Kind: "physical", DefaultRoute: true},
		{IP: net.ParseIP("203.0.113.9"), Interface: "eth0", Kind: "physical", DefaultRoute: true},
		{IP: net.ParseIP("fe80::1"), Interface: "eth0", Kind: "physical", DefaultRoute: true},
		{IP: net.ParseIP("172.17.0.1"), Interface: "docker0", Kind: "bridge"},
		{IP: net.ParseIP("10.8.0.1"), Interface: "wg0", Kind: "tunnel"},
		{IP: net.ParseIP("fd7a:115c:a1e0::5"), Interface: "tailscale0", Kind: "tunnel"},
		{IP: net.ParseIP("100.101.1.2"), Interface: "tailscale0", Kind: "tunnel"},
		{IP: net.ParseIP("10.0.0.5"), Interface: "eth1", Kind: "physical"},
	}}
	got := bindableAddresses(n)
	want := []dbHostAddress{
		{Address: "100.101.1.2", Interface: "tailscale0", Network: "tailnet"},
		{Address: "fd7a:115c:a1e0::5", Interface: "tailscale0", Network: "tailnet"},
		{Address: "10.8.0.1", Interface: "wg0", Network: "vpn"},
		{Address: "10.0.0.5", Interface: "eth1", Network: "private"},
		{Address: "192.168.1.10", Interface: "eth0", Network: "uplink"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bindable =\n%+v\nwant\n%+v", got, want)
	}
	if a, ok := bindableAddress(got, "100.101.1.2"); !ok || a.Interface != "tailscale0" {
		t.Errorf("found %+v, %v", a, ok)
	}
	for _, refused := range []string{"203.0.113.9", "172.17.0.1", "127.0.0.1", "nonsense", ""} {
		if _, ok := bindableAddress(got, refused); ok {
			t.Errorf("%q was offered", refused)
		}
	}
}

// A port Docker was handed once per family on every interface is every
// interface of both, and a rebind folds the two into the one binding.
func TestTheConfiguredBindingIsReadAndRebindingFoldsBothFamilies(t *testing.T) {
	server := &dbServer{port: 5432, binding: &dockerx.Port{IP: "0.0.0.0", PrivatePort: 5432, PublicPort: 5433}}
	ports := []dockerx.PortMapping{
		{HostIP: "0.0.0.0", HostPort: 5433, ContainerPort: 5432, Protocol: "tcp"},
		{HostIP: "::", HostPort: 5433, ContainerPort: 5432, Protocol: "tcp"},
		{HostIP: "127.0.0.1", HostPort: 9187, ContainerPort: 9187, Protocol: "tcp"},
	}
	if got, ok := configuredHostIP(ports, server); !ok || got != "" {
		t.Fatalf("both families read as %q, %v", got, ok)
	}
	if got, ok := configuredHostIP(ports[:1], server); !ok || got != "0.0.0.0" {
		t.Fatalf("IPv4 alone read as %q, %v", got, ok)
	}
	if _, ok := configuredHostIP(ports[2:], server); ok {
		t.Fatal("a configuration without the engine's port was read")
	}
	got := rebindPorts(ports, server, "127.0.0.1")
	want := []dockerx.PortMapping{
		{HostIP: "127.0.0.1", HostPort: 5433, ContainerPort: 5432, Protocol: "tcp"},
		{HostIP: "127.0.0.1", HostPort: 9187, ContainerPort: 9187, Protocol: "tcp"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rebound = %+v", got)
	}
	if ports[1].HostIP != "::" {
		t.Fatal("rebinding changed the configuration it was handed")
	}
}

// Who the firewall lets in for the port, read from its rules.
func TestFirewallAdmitsReadsAnywhereAndTheSourcesOnce(t *testing.T) {
	open, sources := firewallAdmits(reachFirewall(
		netsec.Rule{Action: "ALLOW", Direction: "IN", To: "5432/tcp", From: "198.51.100.0/24", Port: "5432", Protocol: "tcp"},
		netsec.Rule{Action: "ALLOW", Direction: "OUT", To: "5432/tcp", From: "192.0.2.1", Port: "5432", Protocol: "tcp"},
	), 5432)
	if !open || !reflect.DeepEqual(sources, []string{"198.51.100.0/24"}) {
		t.Fatalf("admits = %v %v", open, sources)
	}
	if open, sources := firewallAdmits(&netsec.FirewallStatus{}, 5432); open || sources == nil {
		t.Fatalf("nothing admits = %v %v", open, sources)
	}
}
