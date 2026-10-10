package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nsRoutes4 = `[{"dst":"default","gateway":"10.60.0.1","dev":"lab-n","protocol":"static","flags":[]},{"dst":"10.60.0.0/30","dev":"lab-n","protocol":"kernel","scope":"link","prefsrc":"10.60.0.2","flags":[]},{"type":"local","dst":"10.60.0.2","table":"local","dev":"lab-n","protocol":"kernel","scope":"host","prefsrc":"10.60.0.2","flags":[]}]`
const nsRoutes6 = `[{"dst":"fe80::/64","dev":"lab-n","protocol":"kernel","metric":256,"flags":[]},{"dst":"2001:db8:60::/64","dev":"lab-n","protocol":"kernel","metric":256,"flags":[]}]`
const nsListeners = "tcp   LISTEN 0      4096       0.0.0.0:8080      0.0.0.0:*\ntcp   LISTEN 0      4096          [::]:8080         [::]:*\nudp   UNCONN 0      0      127.0.0.11:53335      0.0.0.0:*\nudp   UNCONN 0      0    [fe80::1%lab-n]:546    [::]:*\n"

func TestNamespaceDetailReadsANamedNamespaceAsItsOwnNetwork(t *testing.T) {
	rec := record(t).
		on("ip -j netns list", `[{"name":"lab","id":0}]`).
		on("ip -n lab -j addr show", `[{"ifname":"lo","link_type":"loopback","operstate":"UNKNOWN","addr_info":[]},{"ifname":"lab-n","operstate":"UP","mtu":1500,"address":"02:00:00:00:00:02","addr_info":[{"local":"10.60.0.2","prefixlen":30}]}]`).
		on("ip -n lab -j route show table all", nsRoutes4).
		on("ip -n lab -j -6 route show table all", nsRoutes6).
		on("ip netns exec lab cat /etc/resolv.conf", "# from /etc/netns/lab\nnameserver 10.60.0.1\nsearch lab.internal\noptions ndots:2 edns0\n").
		on("ip netns exec lab ss -H -l -t -u -n", nsListeners)
	s := testService(t)
	sp := emptySpec()
	sp.Namespaces = []NamespaceSpec{{Name: "lab"}}
	if err := s.writeSpecForTest(sp); err != nil {
		t.Fatal(err)
	}
	d, err := s.NamespaceDetail(context.Background(), "named", "lab", Inventory{})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Managed || d.Kind != "named" || d.DevicesRead.State != "ok" || len(d.Devices) != 1 || d.Devices[0].Name != "lab-n" {
		t.Fatalf("devices: %+v", d)
	}
	if d.RoutesRead.State != "ok" || len(d.Routes) != 3 {
		t.Fatalf("both families' routes without local entries or link-local: %+v", d.Routes)
	}
	if d.Routes[0].Destination != "default" || d.Routes[0].Gateway != "10.60.0.1" || d.Routes[2].Family != "inet6" {
		t.Fatalf("routes: %+v", d.Routes)
	}
	if d.DNS == nil || d.DNS.Nameservers[0] != "10.60.0.1" || d.DNS.Search[0] != "lab.internal" || len(d.DNS.Options) != 2 {
		t.Fatalf("dns: %+v", d.DNS)
	}
	if len(d.Listeners) != 4 || d.Listeners[0].Port != 546 || d.Listeners[0].Address != "fe80::1" || d.Listeners[2].Address != "::" || d.Listeners[3].Port != 53335 {
		t.Fatalf("listeners: %+v", d.Listeners)
	}
	if rec.ran("nsenter") {
		t.Fatal("a named namespace is read with ip -n and ip netns exec")
	}
}

func TestNamespaceDetailKeepsEachFailedPartVisible(t *testing.T) {
	root := t.TempDir()
	prev := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = prev })
	if err := os.MkdirAll(filepath.Join(root, "4412", "root", "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "4412", "root", "etc", "resolv.conf"), []byte("nameserver 127.0.0.11\noptions ndots:0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record(t).
		on("nsenter --target 4412 --net -- ip -j addr show", `[{"ifname":"eth0","operstate":"UP","mtu":1500,"addr_info":[{"local":"10.0.0.3","prefixlen":24}]}]`).
		on("nsenter --target 4412 --net -- ip -j route show table all", `[{"dst":"default","gateway":"10.0.0.1","dev":"eth0"}]`).
		fail("nsenter --target 4412 --net -- ip -j -6 route show table all", "nsenter: failed to execute ip: Permission denied").
		fail("nsenter --target 4412 --net -- ss -H", "nsenter: reassociate to namespace 'ns/net' failed: No such process")
	inv := Inventory{Containers: []ContainerNet{{Name: "postgres", Image: "postgres:17", PID: 4412}}}
	d, err := testService(t).NamespaceDetail(context.Background(), "container", "postgres", inv)
	if err != nil {
		t.Fatal(err)
	}
	if d.PID != 4412 || d.Image != "postgres:17" || d.DevicesRead.State != "ok" {
		t.Fatalf("%+v", d)
	}
	if d.RoutesRead.State != "failed" || !strings.HasPrefix(d.RoutesRead.Reason, "IPv6:") || len(d.Routes) != 1 {
		t.Fatalf("the IPv4 routes stay and the IPv6 failure is named: %+v %+v", d.RoutesRead, d.Routes)
	}
	if d.DNSRead.State != "ok" || d.DNS.Nameservers[0] != "127.0.0.11" {
		t.Fatalf("the container's resolver is read from its root: %+v", d.DNS)
	}
	if d.ListenersRead.State != "failed" || len(d.Listeners) != 0 {
		t.Fatalf("a failed listener read is failed, not empty: %+v", d.ListenersRead)
	}
}

func TestNamespaceTargetsComeFromTheHostNotTheRequest(t *testing.T) {
	record(t).on("ip -j netns list", `[{"name":"lab"}]`)
	s := testService(t)
	ctx := context.Background()
	if _, err := s.NamespaceDetail(ctx, "named", "lab;reboot", Inventory{}); err == nil {
		t.Fatal("an invalid namespace name must be refused")
	}
	if _, err := s.NamespaceDetail(ctx, "named", "other", Inventory{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an absent namespace is not found: %v", err)
	}
	if _, err := s.NamespaceDetail(ctx, "container", "postgres", Inventory{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a container that is not running is not found: %v", err)
	}
	if _, err := s.NamespaceDetail(ctx, "container", "postgres", Inventory{ContainersError: "daemon down"}); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Fatalf("an unreadable inventory is said, not reported as absence: %v", err)
	}
}

func TestNamespaceLookupAsksTheNamespaceKernel(t *testing.T) {
	rec := record(t).
		on("ip -j netns list", `[{"name":"lab"}]`).
		on("ip -n lab -j route get 1.1.1.1 from 10.60.0.2", `[{"dst":"1.1.1.1","gateway":"10.60.0.1","dev":"lab-n","prefsrc":"10.60.0.2"}]`)
	p, err := testService(t).NamespaceLookup(context.Background(), "named", "lab", "1.1.1.1", "10.60.0.2", Inventory{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Device != "lab-n" || p.Gateway != "10.60.0.1" || p.Source != "10.60.0.2" {
		t.Fatalf("%+v", p)
	}
	if _, err := testService(t).NamespaceLookup(context.Background(), "named", "lab", "1.1.1.1", "2001:db8::1", Inventory{}); err == nil {
		t.Fatal("mixed families are refused before asking")
	}
	if _, err := testService(t).NamespaceLookup(context.Background(), "named", "lab", "example.com", "", Inventory{}); err == nil {
		t.Fatal("a name is not a literal address")
	}
	if len(rec.commands()) != 2 {
		t.Fatalf("refusals ask nothing: %v", rec.commands())
	}
}

func TestNamespacesKeepPerItemReadFailures(t *testing.T) {
	record(t).
		on("ip -j netns list", `[{"name":"lab"}]`).
		on("ip -n lab -j addr show", `[{"ifname":"lab-n","operstate":"UP","mtu":1500,"addr_info":[]}]`).
		fail("nsenter --target 99", "nsenter: cannot open /proc/99/ns/net: No such file or directory")
	all, err := testService(t).Namespaces(context.Background(), Inventory{Containers: []ContainerNet{{Name: "gone", PID: 99}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ReadError != "" || len(all[0].Devices) != 1 {
		t.Fatalf("the readable namespace keeps its devices: %+v", all)
	}
	if all[1].ReadError == "" || len(all[1].Devices) != 0 {
		t.Fatalf("the failed one says so instead of looking empty: %+v", all[1])
	}
}
