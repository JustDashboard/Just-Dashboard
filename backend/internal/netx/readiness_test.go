package netx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const readinessLinks = `[
{"ifindex":2,"ifname":"eth0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","address":"52:54:00:00:00:01","stats64":{"rx":{"packets":10},"tx":{"packets":10}}},
{"ifindex":10,"ifname":"vx42","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UNKNOWN","link":"eth0","linkinfo":{"info_kind":"vxlan","info_data":{"id":42,"remote":"198.51.100.7","local":"203.0.113.20","port":4789}},"stats64":{"rx":{"packets":0},"tx":{"packets":3}}},
{"ifindex":11,"ifname":"gre1","flags":["POINTOPOINT","NOARP","UP","LOWER_UP"],"mtu":1476,"operstate":"UNKNOWN","linkinfo":{"info_kind":"gre","info_data":{"remote":"198.51.100.9","local":"192.0.2.99","ikey":"0.0.0.7","okey":"0.0.0.7"}},"stats64":{"rx":{"packets":12},"tx":{"packets":12}}},
{"ifindex":12,"ifname":"mv0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","link":"eth0","address":"02:aa:bb:cc:dd:ee","linkinfo":{"info_kind":"macvlan","info_data":{"mode":"bridge"}},"stats64":{"rx":{"packets":0},"tx":{"packets":0}}},
{"ifindex":13,"ifname":"mv1","flags":["UP","LOWER_UP"],"mtu":1500,"operstate":"UP","link":"eth0","linkinfo":{"info_kind":"macvlan","info_data":{"mode":"bridge"}}},
{"ifindex":14,"ifname":"svc0","flags":["BROADCAST","NOARP","UP","LOWER_UP"],"mtu":1500,"operstate":"UNKNOWN","linkinfo":{"info_kind":"dummy"}}
]`

const readinessAddrs = `[
{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.20","prefixlen":24,"scope":"global"}]},
{"ifname":"svc0","addr_info":[{"family":"inet","local":"10.255.0.1","prefixlen":32,"scope":"global"}]}
]`

func readinessHost(t *testing.T) *recorder {
	t.Helper()
	return record(t).
		on("ip -j -d -s link show", readinessLinks).
		on("ip -j addr show", readinessAddrs)
}

func checksByID(r *LinkReadiness) map[string]ReadinessCheck {
	out := map[string]ReadinessCheck{}
	for _, c := range r.Checks {
		out[c.ID] = c
	}
	return out
}

func TestReadinessOfAVXLANReadsUnderlayEndsMTUAndFloodList(t *testing.T) {
	readinessHost(t).
		on("ip -j route get 198.51.100.7", `[{"dst":"198.51.100.7","gateway":"203.0.113.1","dev":"eth0","prefsrc":"203.0.113.20"}]`).
		on("ip -j route get 198.51.100.8", `[{"dst":"198.51.100.8","dev":"vx42","prefsrc":"10.42.0.1"}]`).
		on("bridge -j fdb show dev vx42", `[{"mac":"00:00:00:00:00:00","ifname":"vx42","dst":"198.51.100.7","flags":["self"],"state":"permanent"}]`)
	s := testService(t)
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "vx42", Kind: "vxlan", VNI: 42, Remote: "198.51.100.7", Remotes: []string{"198.51.100.8"}, Parent: "eth0"}}
	rtSaveSpec(t, s, sp)
	r, err := s.Readiness(context.Background(), "vx42")
	if err != nil {
		t.Fatal(err)
	}
	c := checksByID(r)
	if c["underlay"].State != "ok" || c["local"].State != "ok" {
		t.Fatalf("underlay and local end: %+v", c)
	}
	if c["route:198.51.100.7"].State != "ok" || !strings.Contains(c["route:198.51.100.7"].Detail, "not a probe") {
		t.Fatalf("a route is the kernel's answer, not a probe: %+v", c["route:198.51.100.7"])
	}
	if c["route:198.51.100.8"].State != "failed" || !strings.Contains(c["route:198.51.100.8"].Detail, "itself") {
		t.Fatalf("a remote routed through the tunnel itself fails: %+v", c["route:198.51.100.8"])
	}
	if c["mtu"].State != "warning" || !strings.Contains(c["mtu"].Detail, "1450") {
		t.Fatalf("1500 inside a 1500 underlay does not fit 50 bytes of VXLAN: %+v", c["mtu"])
	}
	if c["remotes"].State != "warning" || !strings.Contains(c["remotes"].Detail, "198.51.100.8") {
		t.Fatalf("a flood end missing from the kernel is named: %+v", c["remotes"])
	}
	if c["liveness"].State != "warning" {
		t.Fatalf("nothing received is a warning: %+v", c["liveness"])
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "neither encrypted nor authenticated") {
		t.Fatalf("limits: %v", r.Limits)
	}
}

func TestReadinessOfGREChecksItsLocalEndAndKeyedOverhead(t *testing.T) {
	readinessHost(t).
		on("ip -j route get 198.51.100.9", `[{"dst":"198.51.100.9","gateway":"203.0.113.1","dev":"eth0","prefsrc":"203.0.113.20"}]`)
	r, err := testService(t).Readiness(context.Background(), "gre1")
	if err != nil {
		t.Fatal(err)
	}
	c := checksByID(r)
	if c["local"].State != "failed" || !strings.Contains(c["local"].Detail, "192.0.2.99") {
		t.Fatalf("a local end this host does not hold fails: %+v", c["local"])
	}
	if c["mtu"].State != "warning" || !strings.Contains(c["mtu"].Detail, "28 bytes") {
		t.Fatalf("a keyed GRE carries 28 bytes over IPv4: %+v", c["mtu"])
	}
	if c["liveness"].State != "warning" || !strings.Contains(c["liveness"].Detail, "none within") {
		t.Fatalf("old traffic without recent traffic: %+v", c["liveness"])
	}
	if !strings.Contains(strings.Join(r.Limits, " "), "not encrypted") {
		t.Fatalf("limits: %v", r.Limits)
	}
}

func TestReadinessOfAMacvlanExplainsIsolationAndProviderMACs(t *testing.T) {
	sys := t.TempDir()
	prev := sysClassNet
	sysClassNet = sys
	t.Cleanup(func() { sysClassNet = prev })
	if err := os.MkdirAll(filepath.Join(sys, "eth0", "device"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../bus/virtio/drivers/virtio_net", filepath.Join(sys, "eth0", "device", "driver")); err != nil {
		t.Fatal(err)
	}
	readinessHost(t)
	r, err := testService(t).Readiness(context.Background(), "mv0")
	if err != nil {
		t.Fatal(err)
	}
	c := checksByID(r)
	if c["parent"].State != "ok" || c["isolation"].State != "info" || !strings.Contains(c["isolation"].Detail, "cannot reach") {
		t.Fatalf("bridge mode isolates the host: %+v", c)
	}
	if !strings.Contains(c["siblings"].Detail, "1 other bridge-mode macvlan on eth0") {
		t.Fatalf("siblings: %+v", c["siblings"])
	}
	if c["mac"].State != "warning" || !strings.Contains(c["mac"].Detail, "virtio_net") || !strings.Contains(c["mac"].Detail, "02:aa:bb:cc:dd:ee") {
		t.Fatalf("a virtual machine's NIC warns about provider MAC filtering: %+v", c["mac"])
	}
	if c["address"].State != "warning" {
		t.Fatalf("no address: %+v", c["address"])
	}
}

func TestReadinessOfADummyShowsItsRoutesAndServices(t *testing.T) {
	readinessHost(t).
		on("ip -j route show dev svc0", `[{"dst":"10.200.0.0/16","dev":"svc0","protocol":"static"}]`).
		on("ip -j -6 route show dev svc0", `[]`).
		on("ss -H -l -t -u -n", "tcp LISTEN 0 4096 10.255.0.1:443 0.0.0.0:*\ntcp LISTEN 0 4096 0.0.0.0:22 0.0.0.0:*\n")
	r, err := testService(t).Readiness(context.Background(), "svc0")
	if err != nil {
		t.Fatal(err)
	}
	c := checksByID(r)
	if c["address"].State != "ok" || c["routes"].State != "ok" || !strings.Contains(c["routes"].Detail, "10.200.0.0/16") {
		t.Fatalf("%+v", c)
	}
	if c["services"].State != "ok" || !strings.Contains(c["services"].Detail, "tcp 10.255.0.1:443") || strings.Contains(c["services"].Detail, ":22") {
		t.Fatalf("only services bound to its own addresses: %+v", c["services"])
	}
}

func TestReadinessOfOtherKindsSaysThereAreNoChecks(t *testing.T) {
	readinessHost(t)
	r, err := testService(t).Readiness(context.Background(), "eth0")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Checks) != 0 || len(r.Limits) != 1 {
		t.Fatalf("%+v", r)
	}
	if _, err := testService(t).Readiness(context.Background(), "nope0"); err == nil {
		t.Fatal("an absent device is not found")
	}
}
