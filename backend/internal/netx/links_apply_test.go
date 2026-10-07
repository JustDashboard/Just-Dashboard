package netx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The browser in these tests reaches the dashboard over the tailnet: its reply
// leaves through tailscale0 from 100.110.34.31, and eth0 carries the default
// route. Those are the two paths every guard exists to protect.
const rtClient = "100.110.34.9"

// rtHost answers the reads every mutation makes before it acts: the devices,
// the uplink and the path to the client, plus the commands commit runs after
// it. ip-link.json and ip-addr.json are the foundation's fixtures: lo, eth0
// (uplink), a Docker bridge and veth, tailscale0, wg0, jd-lan (a bridge with
// the VLAN eth0.100 as a port) and the VXLAN vx42.
func rtHost(t *testing.T) *recorder {
	t.Helper()
	return record(t).
		on("ip -j -d link show", fixture(t, "ip-link.json")).
		on("ip -j addr show", fixture(t, "ip-addr.json")).
		on("ip -j route show default", `[{"dst":"default","gateway":"203.0.113.1","dev":"eth0","protocol":"dhcp"}]`).
		on("ip -j -6 route show default", `[]`).
		on("ip -j route get", fixture(t, "routing-route-get.json")).
		on("ip -j rule show", "[]").
		on("nft -c -f", "").
		on("systemctl daemon-reload", "").
		on("systemctl is-enabled", "disabled").
		on("systemctl enable", "")
}

// rtMutations is what a test ran that changed something: every command that
// is not a read or a commit's bookkeeping.
func rtMutations(rec *recorder) []string {
	var out []string
	for _, c := range rec.commands() {
		switch {
		case strings.HasPrefix(c, "ip -j"), strings.HasPrefix(c, "nft -c"), strings.HasPrefix(c, "systemctl"):
		default:
			out = append(out, c)
		}
	}
	return out
}

// rtAnswerAfter makes every command starting with prefix answer out once a
// command starting with trigger has run, which is how a test says "the route
// to the client moved after the change was applied".
func rtAnswerAfter(rec *recorder, trigger, prefix, out string) {
	prevRun, prevStdin := run, runStdin
	swap := func(line string, reply func() (string, error)) (string, error) {
		triggered := rec.ran(trigger)
		o, err := reply()
		if triggered && strings.HasPrefix(line, prefix) {
			return out, nil
		}
		return o, err
	}
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		return swap(line, func() (string, error) { return prevRun(ctx, name, args...) })
	}
	runStdin = func(ctx context.Context, stdin []byte, name string, args ...string) (string, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		return swap(line, func() (string, error) { return prevStdin(ctx, stdin, name, args...) })
	}
}

// rtSaveSpec writes a spec where the service reads it.
func rtSaveSpec(t *testing.T, s *Service, sp *Spec) {
	t.Helper()
	b, err := json.Marshal(sp)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(s.specPath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func rtLoad(t *testing.T, s *Service) *Spec {
	t.Helper()
	sp, err := s.loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

// rtSpecWritten reports whether a change reached the spec file.
func rtSpecWritten(s *Service) bool {
	_, err := os.Stat(s.specPath())
	return err == nil
}

func rtGuarded(t *testing.T, err error) *GuardError {
	t.Helper()
	var g *GuardError
	if !errors.As(err, &g) {
		t.Fatalf("want a guard refusal, got %v", err)
	}
	return g
}

// rtGoldenSpec is every kind of device and entry the batch file can hold.
func rtGoldenSpec() *Spec {
	sp := emptySpec()
	sp.Namespaces = []NamespaceSpec{{Name: "tenant-a"}}
	sp.Links = []LinkSpec{
		{Name: "jd-lan", Kind: "bridge", STP: true, MTU: 1450, Addresses: []string{"192.168.50.1/24", "2001:db8:50::1/64"}, Up: true},
		{Name: "jd-d0", Kind: "dummy", Up: true},
		{Name: "jd-v100", Kind: "vlan", Parent: "jd-d0", VLANID: 100, Master: "jd-lan", Up: true},
		{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7", Local: "203.0.113.20", Parent: "eth0", Port: 4789, Up: true},
		{Name: "vxg", Kind: "vxlan", VNI: 43, Group: "239.1.1.1", Parent: "jd-d0", Port: 4789},
		{Name: "gre1", Kind: "gre", Local: "203.0.113.20", Remote: "198.51.100.8", TTL: 64, Key: 5, Addresses: []string{"10.200.0.1/30"}, Up: true},
		{Name: "gt1", Kind: "gretap", Remote: "198.51.100.9"},
		{Name: "g61", Kind: "ip6gre", Local: "2001:db8::20", Remote: "2001:db8::2", TTL: 64},
		{Name: "g6t", Kind: "ip6gretap", Remote: "2001:db8::3"},
		{Name: "mv0", Kind: "macvlan", Parent: "eth0", Mode: "bridge", Up: true},
		{Name: "jd-v0", Kind: "veth", Peer: "jd-v1", PeerNamespace: "tenant-a", Master: "jd-lan", MTU: 1400, Up: true},
		{Name: "pair0", Kind: "veth", Peer: "pair1", Addresses: []string{"10.201.0.1/30"}, Up: true},
	}
	sp.Addresses = []AddressSpec{
		{ID: 1, Link: "jd-v1", CIDR: "192.168.50.2/24"},
		{ID: 2, Link: "eth0", CIDR: "203.0.113.50/24"},
	}
	sp.Routes = []RouteSpec{
		{ID: 3, Family: "inet", Destination: "172.16.9.0/24", Type: "unicast", Gateway: "192.168.50.2", Device: "jd-lan", Table: 254, Metric: 50},
		{ID: 4, Family: "inet", Destination: "default", Type: "unicast", Gateway: "198.51.100.1", Device: "eth0", Table: 200, Source: "203.0.113.50"},
		{ID: 5, Family: "inet6", Destination: "2001:db8:88::/64", Type: "unicast", Gateway: "2001:db8:50::2", Table: 100},
		{ID: 6, Family: "inet6", Destination: "default", Type: "unicast", Device: "g61", Table: 200},
		{ID: 7, Family: "inet", Destination: "198.51.100.0/24", Type: "blackhole", Table: 254},
		{ID: 8, Family: "inet6", Destination: "2001:db8:98::/64", Type: "unreachable", Table: 254, Metric: 10},
	}
	sp.Rules = []RuleSpec{
		{ID: 9, Family: "inet", Priority: 10000, From: "192.168.50.0/24", Action: "lookup", Table: 200},
		{ID: 10, Family: "inet", Priority: 10001, To: "198.51.100.0/24", IIF: "jd-lan", Action: "blackhole"},
		{ID: 11, Family: "inet", Priority: 10002, FWMark: "0x10/0xff", OIF: "eth0", Action: "lookup", Table: 100},
		{ID: 12, Family: "inet", Priority: 10003, From: "10.9.0.0/16", Action: "prohibit"},
		{ID: 13, Family: "inet6", Priority: 10004, From: "2001:db8:88::/64", Action: "lookup", Table: 100},
	}
	return sp
}

func TestLinksRenderGolden(t *testing.T) {
	got := renderLinks(rtGoldenSpec())
	path := filepath.Join("testdata", "links-batch.golden")
	if os.Getenv("JD_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := fixture(t, "links-batch.golden"); got != want {
		t.Fatalf("rendered links.batch differs from %s (JD_UPDATE_GOLDEN=1 rewrites it):\n%s", path, got)
	}
}

func TestLinksRenderIsEmptyForAnEmptySpec(t *testing.T) {
	if got := renderLinks(emptySpec()); got != generatedHeader {
		t.Fatalf("an empty spec rendered %q", got)
	}
}

func TestLinksRenderNeverWritesWhatDoesNotValidate(t *testing.T) {
	sp := emptySpec()
	sp.Links = []LinkSpec{
		{Name: "ok0", Kind: "dummy", Up: true},
		{Name: "bad\nlink add evil type dummy", Kind: "dummy"},
		{Name: "vl0", Kind: "vlan", Parent: "ok0 up", VLANID: 5},
		{Name: "ok1", Kind: "dummy", Addresses: []string{"10.0.0.1/24 dev lo", "10.1.0.1/24"}},
	}
	sp.Routes = []RouteSpec{{ID: 1, Family: "inet", Destination: "10.9.0.0/24\nroute add", Type: "unicast", Device: "ok0", Table: 254}}
	sp.Namespaces = []NamespaceSpec{{Name: "a b"}}
	got := renderLinks(sp)
	for _, bad := range []string{"evil", "bad", "vl0", "route add 10.9", "a b", "dev lo"} {
		if strings.Contains(got, bad) {
			t.Errorf("rendered %q from an invalid entry:\n%s", bad, got)
		}
	}
	for _, want := range []string{"link add ok0 type dummy", "link set ok0 up", "addr add 10.1.0.1/24 dev ok1"} {
		if !strings.Contains(got, want) {
			t.Errorf("a valid entry went missing (%q):\n%s", want, got)
		}
	}
}

func TestLinksRenderPutsDependenciesBeforeWhatNeedsThem(t *testing.T) {
	lines := batchLines(rtGoldenSpec())
	index := func(prefix string) int {
		for i, l := range lines {
			if strings.HasPrefix(l, prefix) {
				return i
			}
		}
		t.Fatalf("no line starts with %q", prefix)
		return -1
	}
	order := []string{
		"netns add tenant-a",
		"link add jd-lan type bridge",
		"link add jd-v100 link jd-d0 type vlan",
		"link set jd-v0 master jd-lan",
		"link set jd-lan mtu 1450",
		"addr add 192.168.50.1/24 dev jd-lan",
		"link set jd-lan up",
		"netns exec tenant-a ip link set lo up",
		"route add 172.16.9.0/24",
		"rule add priority 10000",
	}
	for i := 1; i < len(order); i++ {
		if index(order[i-1]) >= index(order[i]) {
			t.Errorf("%q must come before %q", order[i-1], order[i])
		}
	}
}

func TestLinksRequestValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  LinkRequest
		want string
	}{
		{"unknown kind", LinkRequest{Name: "x0", Kind: "wireguard"}, "device kind"},
		{"empty name", LinkRequest{Kind: "dummy"}, "interface name"},
		{"long name", LinkRequest{Name: "abcdefghijklmnop", Kind: "dummy"}, "interface name"},
		{"name with a space", LinkRequest{Name: "a b", Kind: "dummy"}, "interface name"},
		{"vlan id zero", LinkRequest{Name: "v0", Kind: "vlan", Parent: "eth0"}, "VLAN id"},
		{"vlan id too high", LinkRequest{Name: "v0", Kind: "vlan", Parent: "eth0", VLANID: 4095}, "VLAN id"},
		{"vlan without parent", LinkRequest{Name: "v0", Kind: "vlan", VLANID: 5}, ""},
		{"vni too high", LinkRequest{Name: "x0", Kind: "vxlan", VNI: 16777216, Remote: "198.51.100.7"}, "VNI"},
		{"vxlan without an end", LinkRequest{Name: "x0", Kind: "vxlan", VNI: 5}, "remote address or a multicast group"},
		{"vxlan with both", LinkRequest{Name: "x0", Kind: "vxlan", VNI: 5, Remote: "198.51.100.7", Group: "239.1.1.1"}, "either"},
		{"vxlan group not multicast", LinkRequest{Name: "x0", Kind: "vxlan", VNI: 5, Group: "198.51.100.7", Parent: "eth0"}, "multicast"},
		{"vxlan group without device", LinkRequest{Name: "x0", Kind: "vxlan", VNI: 5, Group: "239.1.1.1"}, "device it sends from"},
		{"vxlan mixed families", LinkRequest{Name: "x0", Kind: "vxlan", VNI: 5, Remote: "198.51.100.7", Local: "2001:db8::1"}, "all IPv4 or all IPv6"},
		{"vxlan port too high", LinkRequest{Name: "x0", Kind: "vxlan", VNI: 5, Remote: "198.51.100.7", Port: 70000}, "port"},
		{"gre without remote", LinkRequest{Name: "g0", Kind: "gre"}, "other end"},
		{"gre with v6 ends", LinkRequest{Name: "g0", Kind: "gre", Remote: "2001:db8::2"}, "IPv4"},
		{"ip6gre with v4 ends", LinkRequest{Name: "g0", Kind: "ip6gre", Remote: "198.51.100.8"}, "IPv6"},
		{"gre ttl too high", LinkRequest{Name: "g0", Kind: "gre", Remote: "198.51.100.8", TTL: 300}, "TTL"},
		{"macvlan bad mode", LinkRequest{Name: "m0", Kind: "macvlan", Parent: "eth0", Mode: "source"}, "macvlan mode"},
		{"veth same names", LinkRequest{Name: "a0", Kind: "veth", Peer: "a0"}, "different names"},
		{"veth without peer", LinkRequest{Name: "a0", Kind: "veth"}, "other end"},
		{"mtu too small", LinkRequest{Name: "d0", Kind: "dummy", MTU: 67}, "MTU"},
		{"mtu too large", LinkRequest{Name: "d0", Kind: "dummy", MTU: 65536}, "MTU"},
		{"bad address", LinkRequest{Name: "d0", Kind: "dummy", Addresses: []string{"nope"}}, "not an IP address"},
		{"multicast address", LinkRequest{Name: "d0", Kind: "dummy", Addresses: []string{"224.0.0.1/24"}}, "cannot be the address"},
		{"own parent", LinkRequest{Name: "v0", Kind: "vlan", Parent: "v0", VLANID: 5}, "own parent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.req.spec()
			if c.want == "" {
				// A VLAN with no parent is caught where the batch is built.
				if err != nil {
					return
				}
				if _, err := linkAddArgs(LinkSpec{Name: c.req.Name, Kind: c.req.Kind, VLANID: c.req.VLANID}); err == nil {
					t.Fatal("a VLAN with no parent was accepted")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestLinksRequestDefaultsAndNormalises(t *testing.T) {
	l, err := LinkRequest{Name: " x0 ", Kind: "VXLAN", VNI: 9, Remote: "198.51.100.7", Addresses: []string{"10.5.0.1/24", "10.5.0.1/24", "10.6.0.1"}}.spec()
	if err != nil {
		t.Fatal(err)
	}
	if l.Name != "x0" || l.Kind != "vxlan" || l.Port != 4789 {
		t.Fatalf("spec = %+v", l)
	}
	if got := strings.Join(l.Addresses, ","); got != "10.5.0.1/24,10.6.0.1/32" {
		t.Fatalf("addresses = %s", got)
	}
	m, err := LinkRequest{Name: "m0", Kind: "macvlan", Parent: "eth0"}.spec()
	if err != nil || m.Mode != "bridge" {
		t.Fatalf("macvlan = %+v, %v", m, err)
	}
}

func TestLinksCreateRunsTheSameLinesTheBootFileHasAndRecordsThem(t *testing.T) {
	rec := rtHost(t).on("ip -batch -", "")
	s := testService(t)
	got, err := s.CreateLink(context.Background(), LinkRequest{
		Name: "jd-d0", Kind: "dummy", MTU: 1400, Addresses: []string{"10.77.0.1/24"}, Up: true,
	}, rtClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedBy != "ion" || got.CreatedAt.IsZero() {
		t.Fatalf("not stamped: %+v", got.Made)
	}
	lines := "link add jd-d0 type dummy\nlink set jd-d0 mtu 1400\naddr add 10.77.0.1/24 dev jd-d0\nlink set jd-d0 up\n"
	if want := []string{"ip -batch -"}; strings.Join(rtMutations(rec), "|") != strings.Join(want, "|") {
		t.Fatalf("mutations = %v", rtMutations(rec))
	}
	if in := string(rec.stdin["ip -batch -"]); in != lines {
		t.Fatalf("batch on stdin =\n%s\nwant\n%s", in, lines)
	}
	sp := rtLoad(t, s)
	if len(sp.Links) != 1 || sp.Links[0].Name != "jd-d0" || !sp.Links[0].Up {
		t.Fatalf("spec = %+v", sp.Links)
	}
	boot, err := os.ReadFile(filepath.Join(s.paths.Dir, linksFile))
	if err != nil || !strings.HasSuffix(string(boot), lines) {
		t.Fatalf("links.batch = %q, %v", boot, err)
	}
	if !rec.ran("systemctl enable " + UnitName) {
		t.Fatal("the boot unit was not enabled")
	}
}

func TestLinksCreateBridgePortBeforeAddressesAndUp(t *testing.T) {
	rec := rtHost(t).on("ip -batch -", "")
	s := testService(t)
	rtSaveSpec(t, s, func() *Spec {
		sp := emptySpec()
		sp.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge"}}
		return sp
	}())
	if _, err := s.CreateLink(context.Background(), LinkRequest{
		Name: "jd-v0", Kind: "veth", Peer: "jd-v1", Master: "jd-lan", Up: true,
	}, rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	want := "link add jd-v0 type veth peer name jd-v1\nlink set jd-v0 master jd-lan\nlink set jd-v0 up\nlink set jd-v1 up\n"
	if got := string(rec.stdin["ip -batch -"]); got != want {
		t.Fatalf("batch =\n%s\nwant\n%s", got, want)
	}
}

func TestLinksCreateIsRolledBackWhenItShadowsTheClientPath(t *testing.T) {
	rec := rtHost(t).on("ip -batch -", "").on("ip link del", "")
	rtAnswerAfter(rec, "ip -batch -", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
	s := testService(t)
	_, err := s.CreateLink(context.Background(), LinkRequest{
		Name: "jd-d0", Kind: "dummy", Addresses: []string{"100.110.0.0/16"}, Up: true,
	}, rtClient, "ion")
	g := rtGuarded(t, err)
	if !strings.Contains(g.Reason, "your connection") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip -batch -|ip link del jd-d0" {
		t.Fatalf("mutations = %v", got)
	}
	if rtSpecWritten(s) {
		t.Fatal("a rolled-back change reached the spec")
	}
}

func TestLinksCreateUndoesAHalfAppliedBatch(t *testing.T) {
	rec := rtHost(t).fail("ip -batch -", "ip: RTNETLINK answers: File exists").on("ip link del", "")
	s := testService(t)
	_, err := s.CreateLink(context.Background(), LinkRequest{Name: "jd-d0", Kind: "dummy", Up: true}, rtClient, "ion")
	if err == nil {
		t.Fatal("a failed batch was reported as done")
	}
	if !rec.ran("ip link del jd-d0") {
		t.Fatalf("what the batch did before it failed was left behind: %v", rtMutations(rec))
	}
	if rtSpecWritten(s) {
		t.Fatal("a failed change reached the spec")
	}
}

func TestLinksCreateRefusals(t *testing.T) {
	cases := []struct {
		name  string
		spec  func(*Spec)
		req   LinkRequest
		check func(*testing.T, error)
	}{
		{
			name: "a name the host already has",
			req:  LinkRequest{Name: "eth0", Kind: "dummy"},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrExists) {
					t.Fatalf("err = %v", err)
				}
			},
		},
		{
			name: "a veth end the host already has",
			req:  LinkRequest{Name: "pair0", Kind: "veth", Peer: "wg0"},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrExists) {
					t.Fatalf("err = %v", err)
				}
			},
		},
		{
			name: "a parent that is not there",
			req:  LinkRequest{Name: "v0", Kind: "vlan", Parent: "eth9", VLANID: 5},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v", err)
				}
			},
		},
		{
			name: "a bridge that is not there",
			req:  LinkRequest{Name: "d0", Kind: "dummy", Master: "br9"},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v", err)
				}
			},
		},
		{
			name: "a master that is not a bridge",
			req:  LinkRequest{Name: "d0", Kind: "dummy", Master: "eth0"},
			check: func(t *testing.T, err error) {
				if err == nil || !strings.Contains(err.Error(), "not a bridge") {
					t.Fatalf("err = %v", err)
				}
			},
		},
		{
			name: "Docker's bridge",
			req:  LinkRequest{Name: "d0", Kind: "dummy", Master: "docker0"},
			check: func(t *testing.T, err error) {
				if g := rtGuarded(t, err); !strings.Contains(g.Reason, "docker") {
					t.Fatalf("reason = %q", g.Reason)
				}
			},
		},
		{
			name: "an address on a new bridge port",
			spec: func(sp *Spec) { sp.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge"}} },
			req:  LinkRequest{Name: "d0", Kind: "dummy", Master: "jd-lan", Addresses: []string{"10.1.0.1/24"}},
			check: func(t *testing.T, err error) {
				if g := rtGuarded(t, err); !strings.Contains(g.Reason, "bridge port's addresses") {
					t.Fatalf("reason = %q", g.Reason)
				}
			},
		},
		{
			name: "a namespace the dashboard did not make",
			req:  LinkRequest{Name: "v0", Kind: "veth", Peer: "v1", PeerNamespace: "other"},
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v", err)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := rtHost(t)
			s := testService(t)
			if c.spec != nil {
				sp := emptySpec()
				c.spec(sp)
				rtSaveSpec(t, s, sp)
			}
			_, err := s.CreateLink(context.Background(), c.req, rtClient, "ion")
			c.check(t, err)
			if m := rtMutations(rec); len(m) != 0 {
				t.Fatalf("a refused change ran %v", m)
			}
		})
	}
}

func TestLinksCreateVethIntoAManagedNamespaceChecksTheNameInside(t *testing.T) {
	rec := rtHost(t).on("ip -n tenant-a -j link show", fixture(t, "links-ns-link.json")).on("ip -batch -", "")
	s := testService(t)
	sp := emptySpec()
	sp.Namespaces = []NamespaceSpec{{Name: "tenant-a"}}
	rtSaveSpec(t, s, sp)
	_, err := s.CreateLink(context.Background(), LinkRequest{Name: "v0", Kind: "veth", Peer: "eth0", PeerNamespace: "tenant-a"}, rtClient, "ion")
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.CreateLink(context.Background(), LinkRequest{Name: "v0", Kind: "veth", Peer: "eth1", PeerNamespace: "tenant-a", Up: true}, rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	want := "link add v0 type veth peer name eth1 netns tenant-a\nlink set v0 up\nnetns exec tenant-a ip link set lo up\nnetns exec tenant-a ip link set eth1 up\n"
	if got := string(rec.stdin["ip -batch -"]); got != want {
		t.Fatalf("batch =\n%s\nwant\n%s", got, want)
	}
}

func TestLinksDelete(t *testing.T) {
	managed := func(links ...LinkSpec) func(*Spec) {
		return func(sp *Spec) { sp.Links = links }
	}
	cases := []struct {
		name  string
		spec  func(*Spec)
		link  string
		check func(*testing.T, error)
	}{
		{"a device the dashboard did not make", nil, "wg0", func(t *testing.T, err error) {
			if !errors.Is(err, ErrNotManaged) {
				t.Fatalf("err = %v", err)
			}
		}},
		{"a device that is not there", nil, "nothing0", func(t *testing.T, err error) {
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v", err)
			}
		}},
		{"the uplink, even recorded as made here", managed(LinkSpec{Name: "eth0", Kind: "dummy"}), "eth0", func(t *testing.T, err error) {
			if g := rtGuarded(t, err); !strings.Contains(g.Reason, "default route") {
				t.Fatalf("reason = %q", g.Reason)
			}
		}},
		{"the device the browser is reached through", managed(LinkSpec{Name: "tailscale0", Kind: "dummy"}), "tailscale0", func(t *testing.T, err error) {
			if g := rtGuarded(t, err); !strings.Contains(g.Reason, "Your browser is reached through tailscale0") {
				t.Fatalf("reason = %q", g.Reason)
			}
		}},
		{"loopback", managed(LinkSpec{Name: "lo", Kind: "dummy"}), "lo", func(t *testing.T, err error) {
			rtGuarded(t, err)
		}},
		{"a parent of another managed device", managed(
			LinkSpec{Name: "eth0.100", Kind: "dummy"},
			LinkSpec{Name: "v200", Kind: "vlan", Parent: "eth0.100", VLANID: 200},
		), "eth0.100", func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "delete v200 first") {
				t.Fatalf("err = %v", err)
			}
		}},
		{"a device a route leaves through", func(sp *Spec) {
			sp.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge"}}
			sp.Routes = []RouteSpec{{ID: 1, Family: "inet", Destination: "10.9.0.0/24", Type: "unicast", Device: "jd-lan", Table: 254}}
		}, "jd-lan", func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "route to 10.9.0.0/24") {
				t.Fatalf("err = %v", err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := rtHost(t)
			s := testService(t)
			sp := emptySpec()
			if c.spec != nil {
				c.spec(sp)
			}
			rtSaveSpec(t, s, sp)
			c.check(t, s.DeleteLink(context.Background(), c.link, rtClient, "ion"))
			if m := rtMutations(rec); len(m) != 0 {
				t.Fatalf("a refused delete ran %v", m)
			}
		})
	}
}

func TestLinksDeleteRemovesTheDeviceItsMembersMasterAndItsEntry(t *testing.T) {
	rec := rtHost(t).on("ip link del", "")
	s := testService(t)
	sp := emptySpec()
	sp.Links = []LinkSpec{
		{Name: "jd-lan", Kind: "bridge"},
		{Name: "jd-v0", Kind: "veth", Peer: "jd-v1", Master: "jd-lan"},
	}
	rtSaveSpec(t, s, sp)
	if err := s.DeleteLink(context.Background(), "jd-lan", rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip link del jd-lan" {
		t.Fatalf("mutations = %v", got)
	}
	left := rtLoad(t, s).Links
	if len(left) != 1 || left[0].Name != "jd-v0" || left[0].Master != "" {
		t.Fatalf("spec after = %+v", left)
	}
}

func TestLinksDeleteVethForgetsTheFarEndsAddress(t *testing.T) {
	rec := rtHost(t).on("ip link del", "")
	s := testService(t)
	sp := emptySpec()
	sp.Namespaces = []NamespaceSpec{{Name: "ns1"}}
	// vx42 stands in for a veth: it is a device the recorded host has.
	sp.Links = []LinkSpec{{Name: "vx42", Kind: "veth", Peer: "jd-v1", PeerNamespace: "ns1"}}
	sp.Addresses = []AddressSpec{{ID: 1, Link: "jd-v1", CIDR: "10.1.0.2/24"}, {ID: 2, Link: "eth0", CIDR: "203.0.113.50/24"}}
	rtSaveSpec(t, s, sp)
	if err := s.DeleteLink(context.Background(), "vx42", rtClient, "ion"); err != nil {
		t.Fatal(err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip link del vx42" {
		t.Fatalf("mutations = %v", got)
	}
	after := rtLoad(t, s)
	if len(after.Links) != 0 || len(after.Addresses) != 1 || after.Addresses[0].Link != "eth0" {
		t.Fatalf("spec after = %+v", after)
	}
}

func TestLinksDeleteIsPutBackWhenTheClientPathMoves(t *testing.T) {
	rec := rtHost(t).on("ip link del", "").on("ip -batch -", "")
	rtAnswerAfter(rec, "ip link del", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
	s := testService(t)
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge", Addresses: []string{"192.168.50.1/24"}, Up: true}}
	rtSaveSpec(t, s, sp)
	rtGuarded(t, s.DeleteLink(context.Background(), "jd-lan", rtClient, "ion"))
	got := rtMutations(rec)
	if len(got) != 2 || got[0] != "ip link del jd-lan" || got[1] != "ip -batch -" {
		t.Fatalf("mutations = %v", got)
	}
	if !strings.Contains(string(rec.stdin["ip -batch -"]), "link add jd-lan type bridge") {
		t.Fatalf("the device was not remade:\n%s", rec.stdin["ip -batch -"])
	}
	if len(rtLoad(t, s).Links) != 1 {
		t.Fatal("a rolled-back delete removed the entry")
	}
}

func TestLinksSetState(t *testing.T) {
	cases := []struct {
		name    string
		link    string
		up      bool
		refused string
	}{
		{name: "the uplink down", link: "eth0", refused: "default route"},
		{name: "the client path down", link: "tailscale0", refused: "Your browser is reached through tailscale0"},
		{name: "loopback down", link: "lo", refused: "Loopback"},
		{name: "Docker's bridge down", link: "docker0", refused: "Docker owns"},
		{name: "Docker's veth down", link: "veth6e4f828", refused: "Docker owns"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := rtHost(t)
			s := testService(t)
			_, err := s.SetLinkState(context.Background(), c.link, c.up, rtClient, "ion")
			g := rtGuarded(t, err)
			if !strings.Contains(g.Reason, c.refused) {
				t.Fatalf("reason = %q, want %q", g.Reason, c.refused)
			}
			if m := rtMutations(rec); len(m) != 0 {
				t.Fatalf("a refused change ran %v", m)
			}
		})
	}
}

func TestLinksSetStateUpIsNeverRefused(t *testing.T) {
	rec := rtHost(t).on("ip link set", "")
	s := testService(t)
	change, err := s.SetLinkState(context.Background(), "eth0", true, rtClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if change.Persisted || !strings.Contains(change.Note, "not restored at boot") {
		t.Fatalf("change = %+v", change)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip link set eth0 up" {
		t.Fatalf("mutations = %v", got)
	}
}

func TestLinksSetStateOfAnUnmanagedDeviceIsRuntimeOnly(t *testing.T) {
	rec := rtHost(t).on("ip link set", "")
	s := testService(t)
	change, err := s.SetLinkState(context.Background(), "vx42", true, rtClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if change.Persisted || !strings.Contains(change.Note, "not restored at boot") {
		t.Fatalf("change = %+v", change)
	}
	if rtSpecWritten(s) || rec.ran("nft") || rec.ran("systemctl") {
		t.Fatalf("a runtime-only change wrote the boot files: %v", rec.commands())
	}
}

func TestLinksSetStateOfAManagedDevicePersists(t *testing.T) {
	rec := rtHost(t).on("ip link set", "")
	s := testService(t)
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7", Up: true}}
	rtSaveSpec(t, s, sp)
	change, err := s.SetLinkState(context.Background(), "vx42", false, rtClient, "ion")
	if err != nil || !change.Persisted {
		t.Fatalf("change = %+v, %v", change, err)
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip link set vx42 down" {
		t.Fatalf("mutations = %v", got)
	}
	if rtLoad(t, s).Links[0].Up {
		t.Fatal("the spec still says up")
	}
	if b, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile)); strings.Contains(string(b), "link set vx42 up") {
		t.Fatalf("the boot file still brings it up:\n%s", b)
	}
}

func TestLinksSetStateIsPutBackWhenThePathMoves(t *testing.T) {
	rec := rtHost(t).on("ip link set", "")
	rtAnswerAfter(rec, "ip link set vx42 up", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
	s := testService(t)
	_, err := s.SetLinkState(context.Background(), "vx42", true, rtClient, "ion")
	rtGuarded(t, err)
	if got := rtMutations(rec); strings.Join(got, "|") != "ip link set vx42 up|ip link set vx42 down" {
		t.Fatalf("mutations = %v", got)
	}
}

func TestLinksSetStateRefusesTakingAPortOutOfThePathBridge(t *testing.T) {
	// The browser arrives through jd-lan, whose port eth0.100 carries it.
	rec := rtHost(t)
	rec.replies = append([]reply{{prefix: "ip -j route get", out: `[{"dst":"100.110.34.9","dev":"jd-lan","prefsrc":"192.168.50.1","flags":[]}]`}}, rec.replies...)
	s := testService(t)
	_, err := s.SetLinkState(context.Background(), "eth0.100", false, rtClient, "ion")
	g := rtGuarded(t, err)
	if !strings.Contains(g.Reason, "port of jd-lan") {
		t.Fatalf("reason = %q", g.Reason)
	}
}

func TestLinksSetMTU(t *testing.T) {
	t.Run("the uplink below the IPv6 minimum", func(t *testing.T) {
		rec := rtHost(t)
		s := testService(t)
		_, err := s.SetLinkMTU(context.Background(), "eth0", 1200, rtClient, "ion")
		g := rtGuarded(t, err)
		if !strings.Contains(g.Reason, "1280") {
			t.Fatalf("reason = %q", g.Reason)
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatal("a refused change ran")
		}
	})
	t.Run("the client path below the IPv6 minimum", func(t *testing.T) {
		rtHost(t)
		s := testService(t)
		_, err := s.SetLinkMTU(context.Background(), "tailscale0", 1000, rtClient, "ion")
		rtGuarded(t, err)
	})
	t.Run("out of range", func(t *testing.T) {
		rtHost(t)
		s := testService(t)
		if _, err := s.SetLinkMTU(context.Background(), "eth0", 20, rtClient, "ion"); err == nil {
			t.Fatal("an MTU of 20 was accepted")
		}
	})
	t.Run("Docker's device", func(t *testing.T) {
		rtHost(t)
		s := testService(t)
		_, err := s.SetLinkMTU(context.Background(), "docker0", 1400, rtClient, "ion")
		rtGuarded(t, err)
	})
	t.Run("an unmanaged device is runtime only", func(t *testing.T) {
		rec := rtHost(t).on("ip link set", "")
		s := testService(t)
		change, err := s.SetLinkMTU(context.Background(), "eth0", 1400, rtClient, "ion")
		if err != nil || change.Persisted {
			t.Fatalf("change = %+v, %v", change, err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip link set eth0 mtu 1400" {
			t.Fatalf("mutations = %v", got)
		}
		if rtSpecWritten(s) {
			t.Fatal("a runtime-only change wrote the spec")
		}
	})
	t.Run("a managed device persists and is restored to what it was on failure", func(t *testing.T) {
		rec := rtHost(t).on("ip link set", "")
		rtAnswerAfter(rec, "ip link set vx42 mtu 1400", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
		s := testService(t)
		sp := emptySpec()
		sp.Links = []LinkSpec{{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7"}}
		rtSaveSpec(t, s, sp)
		_, err := s.SetLinkMTU(context.Background(), "vx42", 1400, rtClient, "ion")
		rtGuarded(t, err)
		if got := rtMutations(rec); strings.Join(got, "|") != "ip link set vx42 mtu 1400|ip link set vx42 mtu 1450" {
			t.Fatalf("mutations = %v", got)
		}
	})
}

func TestLinksSetMTUIPv6(t *testing.T) {
	s := testService(t)
	// The kernel drops a device's IPv6 addresses when its MTU goes under 1280.
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7"}}
	rtSaveSpec(t, s, sp)
	rec := record(t).
		on("ip -j -d link show", fixture(t, "ip-link.json")).
		on("ip -j addr show", `[{"ifindex":10,"ifname":"vx42","addr_info":[{"family":"inet6","local":"2001:db8:10::1","prefixlen":64,"scope":"global"}]}]`).
		on("ip -j route show default", `[]`).
		on("ip -j -6 route show default", `[]`).
		on("ip -j route get", fixture(t, "routing-route-get.json"))
	_, err := s.SetLinkMTU(context.Background(), "vx42", 1200, rtClient, "ion")
	g := rtGuarded(t, err)
	if !strings.Contains(g.Reason, "IPv6 address") {
		t.Fatalf("reason = %q", g.Reason)
	}
	if len(rtMutations(rec)) != 0 {
		t.Fatal("a refused change ran")
	}
}

func TestLinksSetMaster(t *testing.T) {
	t.Run("an addressed device", func(t *testing.T) {
		rec := rtHost(t)
		s := testService(t)
		sp := emptySpec()
		sp.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge"}}
		rtSaveSpec(t, s, sp)
		// wg0 holds 10.8.0.1/24.
		_, err := s.SetLinkMaster(context.Background(), "wg0", "jd-lan", rtClient, "ion")
		g := rtGuarded(t, err)
		if !strings.Contains(g.Reason, "holds an address") {
			t.Fatalf("reason = %q", g.Reason)
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatal("a refused change ran")
		}
	})
	for _, c := range []struct{ name, link, want string }{
		{"the uplink", "eth0", "default route"},
		{"the client path", "tailscale0", "Your browser"},
		{"loopback", "lo", "Loopback"},
		{"Docker's veth", "veth6e4f828", "Docker owns"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rtHost(t)
			s := testService(t)
			sp := emptySpec()
			sp.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge"}}
			rtSaveSpec(t, s, sp)
			_, err := s.SetLinkMaster(context.Background(), c.link, "jd-lan", rtClient, "ion")
			if g := rtGuarded(t, err); !strings.Contains(g.Reason, c.want) {
				t.Fatalf("reason = %q", g.Reason)
			}
		})
	}
	t.Run("Docker's bridge as the target", func(t *testing.T) {
		rtHost(t)
		s := testService(t)
		_, err := s.SetLinkMaster(context.Background(), "vx42", "docker0", rtClient, "ion")
		rtGuarded(t, err)
	})
	t.Run("an unmanaged device joins a bridge for now", func(t *testing.T) {
		rec := rtHost(t).on("ip link set", "")
		s := testService(t)
		change, err := s.SetLinkMaster(context.Background(), "vx42", "jd-lan", rtClient, "ion")
		if err != nil || change.Persisted {
			t.Fatalf("change = %+v, %v", change, err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip link set vx42 master jd-lan" {
			t.Fatalf("mutations = %v", got)
		}
	})
	t.Run("a managed device persists its master", func(t *testing.T) {
		rec := rtHost(t).on("ip link set", "")
		s := testService(t)
		sp := emptySpec()
		sp.Links = []LinkSpec{{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7"}}
		rtSaveSpec(t, s, sp)
		change, err := s.SetLinkMaster(context.Background(), "vx42", "jd-lan", rtClient, "ion")
		if err != nil || !change.Persisted {
			t.Fatalf("change = %+v, %v", change, err)
		}
		_ = rec
		if got := rtLoad(t, s).Links[0].Master; got != "jd-lan" {
			t.Fatalf("master = %q", got)
		}
	})
	t.Run("detaching puts the old master back on failure", func(t *testing.T) {
		rec := rtHost(t).on("ip link set", "")
		rtAnswerAfter(rec, "ip link set eth0.100 nomaster", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
		s := testService(t)
		_, err := s.SetLinkMaster(context.Background(), "eth0.100", "", rtClient, "ion")
		rtGuarded(t, err)
		if got := rtMutations(rec); strings.Join(got, "|") != "ip link set eth0.100 nomaster|ip link set eth0.100 master jd-lan" {
			t.Fatalf("mutations = %v", got)
		}
	})
	t.Run("detaching a port of the path's bridge is refused", func(t *testing.T) {
		rec := rtHost(t)
		rec.replies = append([]reply{{prefix: "ip -j route get", out: `[{"dst":"100.110.34.9","dev":"jd-lan","prefsrc":"192.168.50.1","flags":[]}]`}}, rec.replies...)
		s := testService(t)
		_, err := s.SetLinkMaster(context.Background(), "eth0.100", "", rtClient, "ion")
		rtGuarded(t, err)
	})
	t.Run("detaching a device that is nobody's port", func(t *testing.T) {
		rtHost(t)
		s := testService(t)
		if _, err := s.SetLinkMaster(context.Background(), "vx42", "", rtClient, "ion"); err == nil {
			t.Fatal("detaching a free device succeeded")
		}
	})
}

func TestLinksAddAddress(t *testing.T) {
	t.Run("to a managed device it joins the device's entry", func(t *testing.T) {
		rec := rtHost(t).on("ip addr add", "")
		s := testService(t)
		sp := emptySpec()
		sp.Links = []LinkSpec{{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7"}}
		rtSaveSpec(t, s, sp)
		change, err := s.AddAddress(context.Background(), "vx42", "10.42.0.1/24", rtClient, "ion")
		if err != nil || !change.Persisted {
			t.Fatalf("change = %+v, %v", change, err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip addr add 10.42.0.1/24 dev vx42" {
			t.Fatalf("mutations = %v", got)
		}
		after := rtLoad(t, s)
		if len(after.Addresses) != 0 || strings.Join(after.Links[0].Addresses, ",") != "10.42.0.1/24" {
			t.Fatalf("spec = %+v", after)
		}
	})
	t.Run("to another device it is recorded on its own and restored at boot", func(t *testing.T) {
		rtHost(t).on("ip addr add", "")
		s := testService(t)
		if _, err := s.AddAddress(context.Background(), "eth0", "203.0.113.50/24", rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		after := rtLoad(t, s)
		if len(after.Addresses) != 1 || after.Addresses[0].Link != "eth0" || after.Addresses[0].ID == 0 || after.Addresses[0].CreatedBy != "ion" {
			t.Fatalf("spec = %+v", after.Addresses)
		}
		if b, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile)); !strings.Contains(string(b), "addr add 203.0.113.50/24 dev eth0") {
			t.Fatalf("the boot file lacks it:\n%s", b)
		}
	})
	t.Run("an IPv6 address", func(t *testing.T) {
		rec := rtHost(t).on("ip addr add", "")
		s := testService(t)
		if _, err := s.AddAddress(context.Background(), "eth0", "2001:db8::50/64", rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip addr add 2001:db8::50/64 dev eth0" {
			t.Fatalf("mutations = %v", got)
		}
	})
	refused := []struct{ name, link, cidr string }{
		{"loopback", "lo", "10.9.0.1/24"},
		{"Docker's bridge", "docker0", "10.9.0.1/24"},
		{"Tailscale's device", "tailscale0", "10.9.0.1/24"},
	}
	for _, c := range refused {
		t.Run("refuses "+c.name, func(t *testing.T) {
			rec := rtHost(t)
			s := testService(t)
			_, err := s.AddAddress(context.Background(), c.link, c.cidr, rtClient, "ion")
			rtGuarded(t, err)
			if len(rtMutations(rec)) != 0 {
				t.Fatal("a refused change ran")
			}
		})
	}
	t.Run("an address the device holds", func(t *testing.T) {
		rtHost(t)
		s := testService(t)
		_, err := s.AddAddress(context.Background(), "eth0", "203.0.113.20/24", rtClient, "ion")
		if !errors.Is(err, ErrExists) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a device that is not there", func(t *testing.T) {
		rtHost(t)
		s := testService(t)
		_, err := s.AddAddress(context.Background(), "nothing0", "10.9.0.1/24", rtClient, "ion")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("an address that is not one", func(t *testing.T) {
		rtHost(t)
		s := testService(t)
		if _, err := s.AddAddress(context.Background(), "eth0", "10.9.0.1/33", rtClient, "ion"); err == nil {
			t.Fatal("accepted a /33")
		}
	})
	t.Run("is taken back when it shadows the client path", func(t *testing.T) {
		rec := rtHost(t).on("ip addr add", "").on("ip addr del", "")
		rtAnswerAfter(rec, "ip addr add", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
		s := testService(t)
		_, err := s.AddAddress(context.Background(), "vx42", "100.110.0.1/16", rtClient, "ion")
		rtGuarded(t, err)
		if got := rtMutations(rec); strings.Join(got, "|") != "ip addr add 100.110.0.1/16 dev vx42|ip addr del 100.110.0.1/16 dev vx42" {
			t.Fatalf("mutations = %v", got)
		}
		if rtSpecWritten(s) {
			t.Fatal("a rolled-back change reached the spec")
		}
	})
}

func TestLinksRemoveAddress(t *testing.T) {
	t.Run("one the dashboard added", func(t *testing.T) {
		rec := rtHost(t).on("ip addr del", "")
		s := testService(t)
		sp := emptySpec()
		sp.Addresses = []AddressSpec{{ID: 1, Link: "eth0", CIDR: "203.0.113.50/24"}, {ID: 2, Link: "eth0", CIDR: "203.0.113.51/24"}}
		rtSaveSpec(t, s, sp)
		if _, err := s.RemoveAddress(context.Background(), "eth0", "203.0.113.50/24", rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip addr del 203.0.113.50/24 dev eth0" {
			t.Fatalf("mutations = %v", got)
		}
		if after := rtLoad(t, s).Addresses; len(after) != 1 || after[0].ID != 2 {
			t.Fatalf("spec = %+v", after)
		}
	})
	t.Run("one on a managed device", func(t *testing.T) {
		rtHost(t).on("ip addr del", "")
		s := testService(t)
		sp := emptySpec()
		sp.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge", Addresses: []string{"192.168.50.1/24", "192.168.51.1/24"}}}
		rtSaveSpec(t, s, sp)
		if _, err := s.RemoveAddress(context.Background(), "jd-lan", "192.168.51.1/24", rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if got := rtLoad(t, s).Links[0].Addresses; strings.Join(got, ",") != "192.168.50.1/24" {
			t.Fatalf("addresses = %v", got)
		}
	})
	t.Run("one the dashboard did not add", func(t *testing.T) {
		rec := rtHost(t)
		s := testService(t)
		_, err := s.RemoveAddress(context.Background(), "eth0", "203.0.113.20/24", rtClient, "ion")
		if !errors.Is(err, ErrNotManaged) {
			t.Fatalf("err = %v", err)
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatal("a refused change ran")
		}
	})
	t.Run("the one the browser is answered from", func(t *testing.T) {
		rec := rtHost(t)
		s := testService(t)
		sp := emptySpec()
		sp.Addresses = []AddressSpec{{ID: 1, Link: "tailscale0", CIDR: "100.110.34.31/32"}}
		rtSaveSpec(t, s, sp)
		_, err := s.RemoveAddress(context.Background(), "tailscale0", "100.110.34.31/32", rtClient, "ion")
		g := rtGuarded(t, err)
		if !strings.Contains(g.Reason, "answered from") {
			t.Fatalf("reason = %q", g.Reason)
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatal("a refused change ran")
		}
	})
	t.Run("one that is already gone", func(t *testing.T) {
		rtHost(t).fail("ip addr del", "ip: RTNETLINK answers: Cannot assign requested address")
		s := testService(t)
		sp := emptySpec()
		sp.Addresses = []AddressSpec{{ID: 1, Link: "eth0", CIDR: "203.0.113.50/24"}}
		rtSaveSpec(t, s, sp)
		if _, err := s.RemoveAddress(context.Background(), "eth0", "203.0.113.50/24", rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if len(rtLoad(t, s).Addresses) != 0 {
			t.Fatal("the entry outlived the address")
		}
	})
	t.Run("is put back when the path moves", func(t *testing.T) {
		rec := rtHost(t).on("ip addr del", "").on("ip addr add", "")
		rtAnswerAfter(rec, "ip addr del", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
		s := testService(t)
		sp := emptySpec()
		sp.Addresses = []AddressSpec{{ID: 1, Link: "eth0", CIDR: "203.0.113.50/24"}}
		rtSaveSpec(t, s, sp)
		_, err := s.RemoveAddress(context.Background(), "eth0", "203.0.113.50/24", rtClient, "ion")
		rtGuarded(t, err)
		if got := rtMutations(rec); strings.Join(got, "|") != "ip addr del 203.0.113.50/24 dev eth0|ip addr add 203.0.113.50/24 dev eth0" {
			t.Fatalf("mutations = %v", got)
		}
		if len(rtLoad(t, s).Addresses) != 1 {
			t.Fatal("a rolled-back removal dropped the entry")
		}
	})
}

// rtUnderlay is a host whose way out runs on devices that are not themselves
// the uplink: the IPv4 default route leaves through the VLAN eth1.50 (parent
// eth1), and the IPv6 one through eth3, a port of the bridge br8. dummy9 and
// br9 carry nothing.
func rtUnderlay(t *testing.T) *recorder {
	t.Helper()
	const up = `"flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","link_type":"ether"`
	links := `[{"ifindex":1,"ifname":"lo","flags":["LOOPBACK","UP","LOWER_UP"],"mtu":65536,"operstate":"UNKNOWN","link_type":"loopback"},
{"ifindex":2,"ifname":"eth1",` + up + `},
{"ifindex":3,"ifname":"eth1.50","link":"eth1",` + up + `,"linkinfo":{"info_kind":"vlan","info_data":{"id":50}}},
{"ifindex":4,"ifname":"br8",` + up + `,"linkinfo":{"info_kind":"bridge","info_data":{}}},
{"ifindex":5,"ifname":"eth3","master":"br8",` + up + `},
{"ifindex":6,"ifname":"dummy9",` + up + `,"linkinfo":{"info_kind":"dummy"}},
{"ifindex":7,"ifname":"br9",` + up + `,"linkinfo":{"info_kind":"bridge","info_data":{}}}]`
	addrs := `[{"ifindex":3,"ifname":"eth1.50","addr_info":[{"family":"inet","local":"203.0.113.9","prefixlen":24,"scope":"global"}]},
{"ifindex":4,"ifname":"br8","addr_info":[{"family":"inet6","local":"2001:db8::9","prefixlen":64,"scope":"global"}]}]`
	return record(t).
		on("ip -j -d link show", links).
		on("ip -j addr show", addrs).
		on("ip -j route show default", `[{"dst":"default","gateway":"203.0.113.1","dev":"eth1.50","protocol":"static"}]`).
		on("ip -j -6 route show default", `[{"dst":"default","gateway":"2001:db8::1","dev":"eth3","protocol":"static"}]`).
		on("ip -j route get", fixture(t, "routing-route-get.json")).
		on("ip -j rule show", "[]").
		on("nft -c -f", "").on("systemctl daemon-reload", "").on("systemctl is-enabled", "disabled").on("systemctl enable", "").
		on("ip link set", "").on("ip link del", "").on("ip -batch -", "")
}

func TestLinksGuardWhatTheUplinkRunsOn(t *testing.T) {
	ctx := context.Background()
	t.Run("the parent of the uplink cannot be set down", func(t *testing.T) {
		rec := rtUnderlay(t)
		s := testService(t)
		for _, name := range []string{"eth1", "br8"} {
			_, err := s.SetLinkState(ctx, name, false, rtClient, "ion")
			if g := rtGuarded(t, err); !strings.Contains(g.Reason, "carries") {
				t.Fatalf("%s: reason = %q", name, g.Reason)
			}
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatalf("a refused change ran %v", rtMutations(rec))
		}
	})
	t.Run("a device that carries nothing can still be set down", func(t *testing.T) {
		rec := rtUnderlay(t)
		s := testService(t)
		if _, err := s.SetLinkState(ctx, "dummy9", false, rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip link set dummy9 down" {
			t.Fatalf("mutations = %v", got)
		}
	})
	t.Run("the parent of the uplink cannot be deleted", func(t *testing.T) {
		rec := rtUnderlay(t)
		s := testService(t)
		sp := emptySpec()
		sp.Links = []LinkSpec{{Name: "eth1", Kind: "dummy"}, {Name: "br8", Kind: "bridge"}}
		rtSaveSpec(t, s, sp)
		for _, name := range []string{"eth1", "br8"} {
			rtGuarded(t, s.DeleteLink(ctx, name, rtClient, "ion"))
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatalf("a refused change ran %v", rtMutations(rec))
		}
	})
	t.Run("the parent of the uplink keeps an MTU of 1280", func(t *testing.T) {
		rec := rtUnderlay(t)
		s := testService(t)
		for _, name := range []string{"eth1", "br8", "eth1.50"} {
			_, err := s.SetLinkMTU(ctx, name, 1200, rtClient, "ion")
			if g := rtGuarded(t, err); !strings.Contains(g.Reason, "1280") {
				t.Fatalf("%s: reason = %q", name, g.Reason)
			}
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatalf("a refused change ran %v", rtMutations(rec))
		}
		if _, err := s.SetLinkMTU(ctx, "eth1", 1400, rtClient, "ion"); err != nil {
			t.Fatalf("a sane MTU on the parent: %v", err)
		}
		if _, err := s.SetLinkMTU(ctx, "dummy9", 1200, rtClient, "ion"); err != nil {
			t.Fatalf("a small MTU on a device that carries nothing: %v", err)
		}
	})
	t.Run("the parent of the uplink cannot be made a bridge port", func(t *testing.T) {
		rec := rtUnderlay(t)
		s := testService(t)
		_, err := s.SetLinkMaster(ctx, "eth1", "br9", rtClient, "ion")
		if g := rtGuarded(t, err); !strings.Contains(g.Reason, "eth1 carries eth1.50") {
			t.Fatalf("reason = %q", g.Reason)
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatalf("a refused change ran %v", rtMutations(rec))
		}
	})
}

func TestLinksRefusePassthruMacvlanOnWhatTheUplinkRunsOn(t *testing.T) {
	ctx := context.Background()
	for _, parent := range []string{"eth1.50", "eth1", "br8", "eth3"} {
		t.Run(parent, func(t *testing.T) {
			rec := rtUnderlay(t)
			s := testService(t)
			_, err := s.CreateLink(ctx, LinkRequest{Name: "mv0", Kind: "macvlan", Parent: parent, Mode: "passthru", Up: true}, rtClient, "ion")
			if g := rtGuarded(t, err); !strings.Contains(g.Reason, "passthru") {
				t.Fatalf("reason = %q", g.Reason)
			}
			if len(rtMutations(rec)) != 0 {
				t.Fatalf("a refused change ran %v", rtMutations(rec))
			}
		})
	}
	t.Run("other modes on the same parent, and passthru elsewhere, are allowed", func(t *testing.T) {
		rec := rtUnderlay(t)
		s := testService(t)
		if _, err := s.CreateLink(ctx, LinkRequest{Name: "mv0", Kind: "macvlan", Parent: "eth1", Mode: "bridge"}, rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateLink(ctx, LinkRequest{Name: "mv1", Kind: "macvlan", Parent: "dummy9", Mode: "passthru"}, rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if got := rtMutations(rec); len(got) != 2 {
			t.Fatalf("mutations = %v", got)
		}
	})
}
