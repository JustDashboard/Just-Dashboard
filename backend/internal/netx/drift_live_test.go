package netx

import (
	"context"
	"slices"
	"testing"
)

// Inspection reaches only the disposable namespace. Mutations here simulate
// another writer; the inspector itself may issue only inventory commands.
func TestLiveDriftObservesCLIChangesWithoutRepairingNativeObjects(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	useLive(t, ns)
	ns.must(t, "ip", "link", "add", "owned0", "type", "dummy")
	ns.must(t, "ip", "link", "set", "owned0", "mtu", "1400", "up")
	ns.must(t, "ip", "addr", "add", "192.0.2.7/24", "dev", "owned0")
	ns.must(t, "ip", "route", "add", "198.51.100.0/24", "dev", "owned0", "table", "100", "src", "192.0.2.7")
	ns.must(t, "ip", "rule", "add", "priority", "10001", "table", "100")
	ns.must(t, "ip", "link", "add", "native0", "type", "dummy")
	s := testService(t)
	sp := emptySpec()
	sp.Links = []LinkSpec{{Name: "owned0", Kind: "dummy", MTU: 1400, Up: true, Addresses: []string{"192.0.2.7/24"}}}
	sp.Routes = []RouteSpec{{ID: 1, Family: "inet", Type: "unicast", Destination: "198.51.100.0/24", Device: "owned0", Table: 100, Source: "192.0.2.7"}}
	sp.Rules = []RuleSpec{{ID: 2, Family: "inet", Priority: 10001, Action: "lookup", Table: 100}}
	before := s.driftRuntime(context.Background(), sp)
	for _, o := range before {
		if o.Status != "matching" {
			t.Fatalf("initial kernel state not observed: %+v", o)
		}
	}
	ns.must(t, "ip", "link", "set", "owned0", "mtu", "1500")
	ns.must(t, "ip", "addr", "del", "192.0.2.7/24", "dev", "owned0")
	ns.must(t, "ip", "rule", "del", "priority", "10001", "table", "100")
	ns.must(t, "ip", "rule", "add", "priority", "10001", "table", "200")
	after := s.driftRuntime(context.Background(), sp)
	if o := driftFind(t, after, "link", "owned0"); o.Status != "drift" || !o.Repairable {
		t.Fatalf("CLI MTU drift missed: %+v", o)
	}
	if o := driftFind(t, after, "address", "owned0/192.0.2.7/24"); o.Status != "missing" {
		t.Fatalf("CLI address removal missed: %+v", o)
	}
	if o := driftFind(t, after, "rule", "2"); o.Status != "conflict" || o.Repairable {
		t.Fatalf("foreign priority collision missed: %+v", o)
	}
	ns.must(t, "ip", "link", "del", "owned0")
	lost := s.driftRuntime(context.Background(), sp)
	if o := driftFind(t, lost, "link", "owned0"); o.Status != "missing" {
		t.Fatalf("managed link disappearance missed: %+v", o)
	}
	if o := driftFind(t, lost, "route", "1"); o.Status != "missing" {
		t.Fatalf("dependent route disappearance missed: %+v", o)
	}
	native, err := driftArray[ipLink](context.Background(), "ip", "-j", "-d", "link", "show")
	if err != nil || !slices.ContainsFunc(native, func(l ipLink) bool { return l.IfName == "native0" }) {
		t.Fatal("inspection removed native device")
	}
	if slices.ContainsFunc(native, func(l ipLink) bool { return l.IfName == "owned0" }) {
		t.Fatal("inspection silently recreated lost managed device")
	}
}
