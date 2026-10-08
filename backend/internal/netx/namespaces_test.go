package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNamespacesParse(t *testing.T) {
	list, err := parseNamespaces(fixture(t, "links-netns.json"))
	if err != nil || len(list) != 2 || list[0].Name != "tenant-a" || list[0].ID == nil || *list[0].ID != 0 || list[1].ID != nil {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if got, err := parseNamespaces(""); err != nil || got != nil {
		t.Fatalf("empty output = %v, %v", got, err)
	}
	if _, err := parseNamespaces("not json"); err == nil {
		t.Fatal("unreadable output accepted")
	}
	devices, err := parseNamespaceDevices(fixture(t, "links-ns-addr.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Name != "eth0" || devices[0].State != "up" || devices[0].MTU != 1500 ||
		strings.Join(devices[0].Addresses, ",") != "192.168.50.2/24,fe80::ff:fecc:dd01/64" {
		t.Fatalf("devices = %+v", devices)
	}
}

func TestNamespacesListsNamedOnesAndRunningContainers(t *testing.T) {
	rec := record(t).
		on("ip -j netns list", fixture(t, "links-netns.json")).
		on("ip -n tenant-a -j addr show", fixture(t, "links-ns-addr.json")).
		on("ip -n lab -j addr show", "[]").
		on("nsenter --target 4242 --net -- ip -j addr show", fixture(t, "links-ns-addr.json")).
		fail("nsenter --target 4343 --net -- ip -j addr show", "nsenter: reassociate to namespace 'ns/net' failed: No such process")
	s := testService(t)
	rtSaveSpec(t, s, func() *Spec {
		sp := emptySpec()
		sp.Namespaces = []NamespaceSpec{{Name: "tenant-a"}}
		return sp
	}())
	got, err := s.Namespaces(context.Background(), Inventory{Containers: []ContainerNet{
		{ID: "b", Name: "web", Image: "nginx:1", PID: 4242},
		{ID: "c", Name: "gone", Image: "alpine", PID: 4343},
		{ID: "d", Name: "stopped", PID: 0},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("namespaces = %+v", got)
	}
	want := []struct {
		name, kind string
		managed    bool
		devices    int
	}{{"lab", "named", false, 0}, {"tenant-a", "named", true, 1}, {"gone", "container", false, 0}, {"web", "container", false, 1}}
	for i, w := range want {
		g := got[i]
		if g.Name != w.name || g.Kind != w.kind || g.Managed != w.managed || len(g.Devices) != w.devices {
			t.Errorf("namespace %d = %+v, want %+v", i, g, w)
		}
		if g.Devices == nil {
			t.Errorf("%s: devices serialise as null", g.Name)
		}
	}
	if got[3].Image != "nginx:1" || got[3].PID != 4242 {
		t.Errorf("container = %+v", got[3])
	}
	for _, c := range rec.commands() {
		if strings.Contains(c, " add ") && !strings.Contains(c, "addr show") {
			t.Fatalf("a read changed something: %s", c)
		}
	}
}

func TestNamespacesWithNoneAndNoContainers(t *testing.T) {
	record(t).on("ip -j netns list", "[]")
	s := testService(t)
	got, err := s.Namespaces(context.Background(), Inventory{})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %+v, %v; want an empty list, not null", got, err)
	}
}

func TestNamespacesBoundsHowManyContainersAreEnteredAtOnce(t *testing.T) {
	// A hundred containers must not become a hundred concurrent nsenters.
	record(t).on("ip -j netns list", "[]").on("nsenter", "[]")
	var inv Inventory
	for i := 1; i <= 40; i++ {
		inv.Containers = append(inv.Containers, ContainerNet{Name: "c" + strconv.Itoa(i), PID: 1000 + i})
	}
	prev := run
	var inFlight, peak atomic.Int32
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "nsenter" {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
		return prev(ctx, name, args...)
	}
	t.Cleanup(func() { run = prev })
	s := testService(t)
	got, err := s.Namespaces(context.Background(), inv)
	if err != nil || len(got) != 40 {
		t.Fatalf("got %d namespaces, %v", len(got), err)
	}
	if peak.Load() > containerReads {
		t.Fatalf("%d containers were entered at once; the bound is %d", peak.Load(), containerReads)
	}
}

func TestNamespacesCreatePlainNamespace(t *testing.T) {
	rec := rtHost(t).on("ip -j netns list", "[]").on("ip -batch -", "")
	s := testService(t)
	ns, err := s.CreateNamespace(context.Background(), NamespaceRequest{Name: "lab"}, rtClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if ns.Name != "lab" || ns.CreatedBy != "ion" {
		t.Fatalf("namespace = %+v", ns)
	}
	if got := string(rec.stdin["ip -batch -"]); got != "netns add lab\n" {
		t.Fatalf("batch = %q", got)
	}
	if got := rtLoad(t, s).Namespaces; len(got) != 1 || got[0].Name != "lab" {
		t.Fatalf("spec = %+v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile)); !strings.Contains(string(b), "netns add lab\n") {
		t.Fatalf("the boot file lacks it:\n%s", b)
	}
}

func TestNamespacesCreateWithAVethIntoABridge(t *testing.T) {
	rec := rtHost(t).on("ip -j netns list", "[]").on("ip -batch -", "")
	s := testService(t)
	rtSaveSpec(t, s, func() *Spec {
		sp := emptySpec()
		sp.Links = []LinkSpec{{Name: "jd-lan", Kind: "bridge"}}
		return sp
	}())
	_, err := s.CreateNamespace(context.Background(), NamespaceRequest{Name: "tenant-a", Veth: &VethRequest{
		HostName: "jd-v0", PeerName: "eth0", Bridge: "jd-lan", PeerAddress: "192.168.50.2/24",
	}}, rtClient, "ion")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"netns add tenant-a",
		"link add jd-v0 type veth peer name eth0 netns tenant-a",
		"link set jd-v0 master jd-lan",
		"netns exec tenant-a ip addr add 192.168.50.2/24 dev eth0",
		"link set jd-v0 up",
		"netns exec tenant-a ip link set lo up",
		"netns exec tenant-a ip link set eth0 up",
	}, "\n") + "\n"
	if got := string(rec.stdin["ip -batch -"]); got != want {
		t.Fatalf("batch =\n%s\nwant\n%s", got, want)
	}
	after := rtLoad(t, s)
	if len(after.Namespaces) != 1 || len(after.Links) != 2 || len(after.Addresses) != 1 {
		t.Fatalf("spec = %+v", after)
	}
	veth := after.Links[1]
	if veth.Kind != "veth" || veth.PeerNamespace != "tenant-a" || veth.Master != "jd-lan" || !veth.Up || veth.CreatedBy != "ion" {
		t.Fatalf("veth = %+v", veth)
	}
	if a := after.Addresses[0]; a.Link != "eth0" || a.CIDR != "192.168.50.2/24" || a.ID == 0 {
		t.Fatalf("address = %+v", a)
	}
}

func TestNamespacesCreateRefusals(t *testing.T) {
	cases := []struct {
		name  string
		req   NamespaceRequest
		spec  func(*Spec)
		list  string
		check func(*testing.T, error)
	}{
		{name: "a name with a slash", req: NamespaceRequest{Name: "a/b"}, list: "[]", check: func(t *testing.T, err error) {
			if err == nil || !strings.Contains(err.Error(), "namespace name") {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "a namespace the dashboard made", req: NamespaceRequest{Name: "lab"}, list: "[]",
			spec: func(sp *Spec) { sp.Namespaces = []NamespaceSpec{{Name: "lab"}} },
			check: func(t *testing.T, err error) {
				if !errors.Is(err, ErrExists) {
					t.Fatalf("err = %v", err)
				}
			}},
		{name: "a namespace somebody else made", req: NamespaceRequest{Name: "lab"}, list: `[{"name":"lab"}]`, check: func(t *testing.T, err error) {
			if !errors.Is(err, ErrExists) {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "a host end the host already has", req: NamespaceRequest{Name: "x", Veth: &VethRequest{HostName: "eth0", PeerName: "eth1"}}, list: "[]", check: func(t *testing.T, err error) {
			if !errors.Is(err, ErrExists) {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "a bridge that is not there", req: NamespaceRequest{Name: "x", Veth: &VethRequest{HostName: "jd-v0", PeerName: "eth0", Bridge: "br9"}}, list: "[]", check: func(t *testing.T, err error) {
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v", err)
			}
		}},
		{name: "Docker's bridge", req: NamespaceRequest{Name: "x", Veth: &VethRequest{HostName: "jd-v0", PeerName: "eth0", Bridge: "docker0"}}, list: "[]", check: func(t *testing.T, err error) {
			rtGuarded(t, err)
		}},
		{name: "an address on a bridge port", req: NamespaceRequest{Name: "x", Veth: &VethRequest{HostName: "jd-v0", PeerName: "eth0", Bridge: "jd-lan", HostAddress: "10.1.0.1/24"}}, list: "[]", check: func(t *testing.T, err error) {
			if g := rtGuarded(t, err); !strings.Contains(g.Reason, "bridge port's addresses") {
				t.Fatalf("reason = %q", g.Reason)
			}
		}},
		{name: "a bad peer address", req: NamespaceRequest{Name: "x", Veth: &VethRequest{HostName: "jd-v0", PeerName: "eth0", PeerAddress: "nope"}}, list: "[]", check: func(t *testing.T, err error) {
			if err == nil {
				t.Fatal("accepted")
			}
		}},
		{name: "two ends with one name", req: NamespaceRequest{Name: "x", Veth: &VethRequest{HostName: "v0", PeerName: "v0"}}, list: "[]", check: func(t *testing.T, err error) {
			if err == nil {
				t.Fatal("accepted")
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := rtHost(t).on("ip -j netns list", c.list)
			s := testService(t)
			if c.spec != nil {
				sp := emptySpec()
				c.spec(sp)
				rtSaveSpec(t, s, sp)
			}
			_, err := s.CreateNamespace(context.Background(), c.req, rtClient, "ion")
			c.check(t, err)
			if m := rtMutations(rec); len(m) != 0 {
				t.Fatalf("a refused change ran %v", m)
			}
		})
	}
}

func TestNamespacesCreateIsUndoneWhenTheBatchFailsHalfway(t *testing.T) {
	rec := rtHost(t).on("ip -j netns list", "[]").fail("ip -batch -", "ip: RTNETLINK answers: File exists").on("ip link del", "").on("ip netns del", "")
	s := testService(t)
	_, err := s.CreateNamespace(context.Background(), NamespaceRequest{Name: "lab", Veth: &VethRequest{HostName: "jd-v0", PeerName: "eth0"}}, rtClient, "ion")
	if err == nil {
		t.Fatal("a failed batch was reported as done")
	}
	if got := rtMutations(rec); strings.Join(got, "|") != "ip -batch -|ip link del jd-v0|ip netns del lab" {
		t.Fatalf("mutations = %v", got)
	}
	if rtSpecWritten(s) {
		t.Fatal("a failed change reached the spec")
	}
}

func TestNamespacesDelete(t *testing.T) {
	made := func() *Spec {
		sp := emptySpec()
		sp.Namespaces = []NamespaceSpec{{Name: "tenant-a"}, {Name: "other"}}
		// vx42 stands in for the veth: it is a device the recorded host has.
		sp.Links = []LinkSpec{
			{Name: "vx42", Kind: "veth", Peer: "eth0", PeerNamespace: "tenant-a", Master: "jd-lan"},
			{Name: "keep0", Kind: "dummy"},
		}
		sp.Addresses = []AddressSpec{{ID: 1, Link: "eth0", CIDR: "192.168.50.2/24"}, {ID: 2, Link: "wg0", CIDR: "10.8.0.9/24"}}
		return sp
	}
	t.Run("removes the veths that lead into it, then the namespace", func(t *testing.T) {
		rec := rtHost(t).on("ip link del", "").on("ip netns del", "")
		s := testService(t)
		rtSaveSpec(t, s, made())
		if err := s.DeleteNamespace(context.Background(), "tenant-a", rtClient, "ion"); err != nil {
			t.Fatal(err)
		}
		if got := rtMutations(rec); strings.Join(got, "|") != "ip link del vx42|ip netns del tenant-a" {
			t.Fatalf("mutations = %v", got)
		}
		after := rtLoad(t, s)
		if len(after.Namespaces) != 1 || after.Namespaces[0].Name != "other" ||
			len(after.Links) != 1 || after.Links[0].Name != "keep0" ||
			len(after.Addresses) != 1 || after.Addresses[0].Link != "wg0" {
			t.Fatalf("spec = %+v", after)
		}
		if b, _ := os.ReadFile(filepath.Join(s.paths.Dir, linksFile)); strings.Contains(string(b), "tenant-a") {
			t.Fatalf("the boot file still makes it:\n%s", b)
		}
	})
	t.Run("one the dashboard did not make", func(t *testing.T) {
		rec := rtHost(t).on("ip -j netns list", `[{"name":"lab"}]`)
		s := testService(t)
		if err := s.DeleteNamespace(context.Background(), "lab", rtClient, "ion"); !errors.Is(err, ErrNotManaged) {
			t.Fatalf("err = %v", err)
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatal("a refused delete ran")
		}
	})
	t.Run("one that is not there", func(t *testing.T) {
		rtHost(t).on("ip -j netns list", `[]`)
		s := testService(t)
		if err := s.DeleteNamespace(context.Background(), "lab", rtClient, "ion"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a veth that carries the browser", func(t *testing.T) {
		rec := rtHost(t)
		rec.replies = append([]reply{{prefix: "ip -j route get", out: `[{"dst":"100.110.34.9","dev":"vx42","prefsrc":"192.168.50.1","flags":[]}]`}}, rec.replies...)
		s := testService(t)
		rtSaveSpec(t, s, made())
		err := s.DeleteNamespace(context.Background(), "tenant-a", rtClient, "ion")
		rtGuarded(t, err)
		if len(rtMutations(rec)) != 0 {
			t.Fatal("a refused delete ran")
		}
	})
	t.Run("a veth a route leaves through", func(t *testing.T) {
		rec := rtHost(t)
		s := testService(t)
		sp := made()
		sp.Routes = []RouteSpec{{ID: 9, Family: "inet", Destination: "10.9.0.0/24", Type: "unicast", Device: "vx42", Table: 254}}
		rtSaveSpec(t, s, sp)
		if err := s.DeleteNamespace(context.Background(), "tenant-a", rtClient, "ion"); err == nil || !strings.Contains(err.Error(), "route to 10.9.0.0/24") {
			t.Fatalf("err = %v", err)
		}
		if len(rtMutations(rec)) != 0 {
			t.Fatal("a refused delete ran")
		}
	})
	t.Run("is remade when the client path moves", func(t *testing.T) {
		rec := rtHost(t).on("ip link del", "").on("ip netns del", "").on("ip -force -batch -", "")
		rtAnswerAfter(rec, "ip netns del", "ip -j route get", fixture(t, "routing-route-get-moved.json"))
		s := testService(t)
		rtSaveSpec(t, s, made())
		rtGuarded(t, s.DeleteNamespace(context.Background(), "tenant-a", rtClient, "ion"))
		if !strings.Contains(string(rec.stdin["ip -force -batch -"]), "netns add tenant-a") {
			t.Fatalf("not remade: %v", rtMutations(rec))
		}
		if len(rtLoad(t, s).Namespaces) != 2 {
			t.Fatal("a rolled-back delete dropped the entry")
		}
	})
	t.Run("partial deletion restores the removed pairs even while the namespace remains", func(t *testing.T) {
		rec := rtHost(t).on("ip link del", "").fail("ip netns del", "namespace is busy").on("ip -force -batch -", "")
		s := testService(t)
		rtSaveSpec(t, s, made())
		if err := s.DeleteNamespace(context.Background(), "tenant-a", rtClient, "ion"); err == nil {
			t.Fatal("a partial deletion was reported successful")
		}
		if got := string(rec.stdin["ip -force -batch -"]); !strings.Contains(got, "link add vx42 type veth") || !strings.Contains(got, "netns add tenant-a") {
			t.Fatalf("removed pair was not restored: %q", got)
		}
		if len(rtLoad(t, s).Links) != 2 || len(rtLoad(t, s).Namespaces) != 2 {
			t.Fatal("a failed deletion changed the spec")
		}
	})
}
