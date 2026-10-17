package netx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A kept port against a real kernel, in throwaway namespaces like every other
// gateway live test (JD_NETNS_LIVE=1). The second half is the claim the
// feature rests on: a port published the way Docker publishes one — a DNAT
// rule in the nat table's PREROUTING and an accept in FORWARD, which no
// input rule ever sees — is still kept to the list, because the drop runs at
// prerouting before the translation.
func TestLiveAKeptPortAdmitsTheListAndRefusesTheRest(t *testing.T) {
	gwLiveRequired(t)
	gw, cl, sv := gwLiveNS(t, "kgw"), gwLiveNS(t, "kclient"), gwLiveNS(t, "kserver")

	// client(10.77.0.2 listed, 10.77.0.3 not) -- gwc(10.77.0.1) [host] gws(10.88.0.1) -- container(10.88.0.5)
	gwMustInNS(t, cl, "ip", "link", "add", "c0", "type", "veth", "peer", "name", "gwc", "netns", gw)
	gwMustInNS(t, sv, "ip", "link", "add", "s0", "type", "veth", "peer", "name", "gws", "netns", gw)
	for _, c := range [][]string{
		{cl, "ip", "addr", "add", "10.77.0.2/24", "dev", "c0"}, {cl, "ip", "addr", "add", "10.77.0.3/24", "dev", "c0"},
		{cl, "ip", "link", "set", "c0", "up"}, {cl, "ip", "link", "set", "lo", "up"},
		{cl, "ip", "route", "add", "default", "via", "10.77.0.1"},
		{sv, "ip", "addr", "add", "10.88.0.5/24", "dev", "s0"}, {sv, "ip", "link", "set", "s0", "up"}, {sv, "ip", "link", "set", "lo", "up"},
		{sv, "ip", "route", "add", "default", "via", "10.88.0.1"},
		{gw, "ip", "addr", "add", "10.77.0.1/24", "dev", "gwc"}, {gw, "ip", "link", "set", "gwc", "up"},
		{gw, "ip", "addr", "add", "10.88.0.1/24", "dev", "gws"}, {gw, "ip", "link", "set", "gws", "up"},
		{gw, "ip", "link", "set", "lo", "up"},
		{gw, "sysctl", "-w", "net.ipv4.ip_forward=1"},
		// What Docker writes for a container's port published on every
		// interface: the translation, and the forward accept for it.
		{gw, "iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", "15432", "-j", "DNAT", "--to-destination", "10.88.0.5:80"},
		{gw, "iptables", "-A", "FORWARD", "-d", "10.88.0.5", "-p", "tcp", "--dport", "80", "-j", "ACCEPT"},
		{gw, "iptables", "-A", "FORWARD", "-s", "10.88.0.5", "-p", "tcp", "--sport", "80", "-j", "ACCEPT"},
		// And a host firewall that would refuse the port were it asked: it
		// is not, which is the whole difficulty.
		{gw, "iptables", "-A", "INPUT", "-p", "tcp", "--dport", "15432", "-j", "DROP"},
	} {
		gwMustInNS(t, c[0], c[1:]...)
	}
	gwLiveServe(t, gw, "0.0.0.0", 5432)
	gwLiveServe(t, sv, "10.88.0.5", 80)

	sys := t.TempDir()
	for _, p := range []string{"net/ipv4/ip_forward", "net/ipv6/conf/all/forwarding"} {
		full := filepath.Join(sys, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("1\n"), 0o644)
	}
	prevSys, prevClass, prevRun, prevHas := gatewaySysRoot, gatewayClassNet, run, has
	gatewaySysRoot, gatewayClassNet, run, has = sys, t.TempDir(), gwLiveRun(gw), func(string) bool { return false }
	t.Cleanup(func() { gatewaySysRoot, gatewayClassNet, run, has = prevSys, prevClass, prevRun, prevHas })
	svc := testService(t)
	ctx := context.Background()

	from := func(source, url string) (string, bool) {
		t.Helper()
		out, err := gwInNS(t, cl, "curl", "-s", "-m", "3", "--interface", source, url)
		return strings.TrimSpace(out), err == nil
	}
	for _, url := range []string{"http://10.77.0.1:5432/", "http://10.77.0.1:15432/"} {
		if _, ok := from("10.77.0.3", url); !ok {
			t.Fatalf("%s was not reachable before any list", url)
		}
	}

	native, err := svc.SetPortAllowlist(ctx, PortAllowlistRequest{Owner: "database:native", Name: "native", Protocol: "tcp", Port: 5432, Sources: []string{"10.77.0.2"}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPortAllowlist(ctx, PortAllowlistRequest{Owner: "database:shop-db", Name: "shop-db", Protocol: "tcp", Port: 15432, Sources: []string{"10.77.0.2"}}, "test"); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"http://10.77.0.1:5432/", "http://10.77.0.1:15432/"} {
		if body, ok := from("10.77.0.2", url); !ok || body != "10.77.0.2" {
			t.Errorf("the listed address at %s: got %q, %v", url, body, ok)
		}
		if _, ok := from("10.77.0.3", url); ok {
			t.Errorf("an address not on the list reached %s", url)
		}
	}
	// This machine's own programs are not judged by the list.
	if out, err := gwInNS(t, gw, "curl", "-s", "-m", "3", "http://127.0.0.1:5432/"); err != nil || strings.TrimSpace(out) != "127.0.0.1" {
		t.Errorf("loopback to a kept port: %q, %v", out, err)
	}

	reading, err := svc.PortAllowlists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reading.Available || !reading.Loaded || len(reading.Entries) != 2 {
		t.Fatalf("reading = %+v", reading)
	}
	for _, e := range reading.Entries {
		if !e.Enforced || e.Refused == 0 {
			t.Errorf("%s: enforced %v, refused %d", e.Owner, e.Enforced, e.Refused)
		}
	}

	// An edited list is judged as the new one, and a released port is open
	// to whatever its binding allows again.
	if _, err := svc.SetPortAllowlist(ctx, PortAllowlistRequest{Owner: "database:native", Name: "native", Protocol: "tcp", Port: 5432, Sources: []string{"10.77.0.3"}}, "test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := from("10.77.0.2", "http://10.77.0.1:5432/"); ok {
		t.Error("the address taken off the list still reached the port")
	}
	if body, ok := from("10.77.0.3", "http://10.77.0.1:5432/"); !ok || body != "10.77.0.3" {
		t.Errorf("the address put on the list: got %q, %v", body, ok)
	}
	if removed, err := svc.RemovePortAllowlist(ctx, "database:shop-db"); err != nil || !removed {
		t.Fatalf("release = %v, %v", removed, err)
	}
	if body, ok := from("10.77.0.3", "http://10.77.0.1:15432/"); !ok || body != "10.77.0.3" {
		t.Errorf("a released port: got %q, %v", body, ok)
	}
	if got := svc.mustSpec(t).PortAllowlists; len(got) != 1 || got[0].ID != native.ID {
		t.Fatalf("kept after release: %+v", got)
	}
}

func (s *Service) mustSpec(t *testing.T) *Spec {
	t.Helper()
	sp, err := s.loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	return sp
}
