package netx

import (
	"context"
	"strings"
	"testing"
)

// TestLiveBridgeVLANsFloodEndsAndReadinessAgainstARealKernel builds a
// VLAN-filtering bridge, a port's tagged/untagged policy, a unicast VXLAN's
// flood list and two macvlans in a throwaway namespace, then reads them back
// through the same views the pages use, and replays the boot unit's bridge
// lines into a second fresh namespace.
//
//	JD_NETNS_LIVE=1 go test ./internal/netx -run TestLiveBridgeVLANs -v
func TestLiveBridgeVLANsFloodEndsAndReadinessAgainstARealKernel(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	useLive(t, ns)
	ctx := context.Background()
	ns.must(t, "ip", "link", "add", "up0", "type", "dummy")
	ns.must(t, "ip", "addr", "add", "10.99.0.1/24", "dev", "up0")
	ns.must(t, "ip", "link", "set", "up0", "up")
	ns.must(t, "ip", "route", "add", "default", "via", "10.99.0.254", "dev", "up0")
	const client = "10.99.0.50"
	s := testService(t)
	off := false
	for _, req := range []LinkRequest{
		{Name: "jdsw", Kind: "bridge", VLANFiltering: true, MulticastSnooping: &off, Up: true},
		{Name: "jdp0", Kind: "veth", Peer: "jdp1", Master: "jdsw", Up: true},
		{Name: "jdvx", Kind: "vxlan", VNI: 42, Remote: "10.99.0.7", Local: "10.99.0.1", Parent: "up0", Up: true},
		{Name: "jdmvp", Kind: "dummy", Up: true},
		{Name: "jdmv1", Kind: "macvlan", Parent: "jdmvp", Mode: "bridge", Addresses: []string{"10.150.0.1/24"}, Up: true},
		{Name: "jdmv2", Kind: "macvlan", Parent: "jdmvp", Mode: "bridge", Up: true},
	} {
		if _, err := s.CreateLink(ctx, req, client, "live"); err != nil {
			t.Fatalf("create %s: %v", req.Name, err)
		}
	}

	view, err := s.Bridge(ctx, "jdsw")
	if err != nil {
		t.Fatal(err)
	}
	if !view.VLANFiltering || view.MulticastSnooping || !view.Managed || view.SettingsRead.State != "ok" {
		t.Fatalf("bridge settings: %+v", view)
	}

	if _, err := s.SetPortVLANs(ctx, "jdp0", []PortVLAN{{VID: 10, PVID: true, Untagged: true}, {VID: 20}}, client, "live"); err != nil {
		t.Fatal(err)
	}
	vlans := ns.must(t, "bridge", "-j", "vlan", "show", "dev", "jdp0")
	held, err := parseBridgeVLANs(vlans)
	if err != nil {
		t.Fatal(err)
	}
	got := held["jdp0"]
	if len(got) != 2 || got[0] != (PortVLAN{VID: 10, PVID: true, Untagged: true}) || got[1] != (PortVLAN{VID: 20}) {
		t.Fatalf("the kernel holds %+v", got)
	}
	if view, err = s.Bridge(ctx, "jdsw"); err != nil || len(view.Ports) != 2 || len(view.Ports[1].Desired) != 2 {
		t.Fatalf("the view reads the port and its desired list: %+v %v", view, err)
	}

	if _, err := s.SetVXLANRemotes(ctx, "jdvx", []string{"10.99.0.8", "10.99.0.9"}, client, "live"); err != nil {
		t.Fatal(err)
	}
	fdb := ns.must(t, "bridge", "-j", "fdb", "show", "dev", "jdvx")
	liveHas(t, "vxlan fdb", fdb, `"dst":"10.99.0.7"`, `"dst":"10.99.0.8"`, `"dst":"10.99.0.9"`)
	if _, err := s.SetVXLANRemotes(ctx, "jdvx", []string{"10.99.0.9"}, client, "live"); err != nil {
		t.Fatal(err)
	}
	liveLacks(t, "vxlan fdb after removal", ns.must(t, "bridge", "-j", "fdb", "show", "dev", "jdvx"), `"dst":"10.99.0.8"`)

	ready, err := s.Readiness(ctx, "jdvx")
	if err != nil {
		t.Fatal(err)
	}
	checks := checksByID(ready)
	if checks["underlay"].State != "ok" || checks["local"].State != "ok" || checks["route:10.99.0.7"].State != "ok" || checks["remotes"].State != "ok" {
		t.Fatalf("readiness against the real kernel: %+v", ready.Checks)
	}
	if checks["mtu"].State != "ok" {
		t.Fatalf("the kernel sizes a VXLAN to fit its underlay: %+v", checks["mtu"])
	}
	mv, err := s.Readiness(ctx, "jdmv1")
	if err != nil {
		t.Fatal(err)
	}
	if c := checksByID(mv); c["isolation"].State != "info" || !strings.Contains(c["siblings"].Detail, "1 other") {
		t.Fatalf("macvlan readiness: %+v", mv.Checks)
	}

	// The boot unit's bridge lines load against a freshly made copy.
	sp, err := s.loadSpec()
	if err != nil {
		t.Fatal(err)
	}
	fresh := newLiveNS(t)
	fresh.must(t, "ip", "link", "add", "up0", "type", "dummy")
	fresh.must(t, "ip", "addr", "add", "10.99.0.1/24", "dev", "up0")
	fresh.must(t, "ip", "link", "set", "up0", "up")
	if _, err := fresh.run(ctx, []byte(renderLinks(sp)), "ip", "-force", "-batch", "-"); err != nil {
		t.Fatalf("links.batch: %v", err)
	}
	for _, cmd := range bridgeUnitCommands(sp) {
		if _, err := fresh.run(ctx, nil, "bridge", cmd...); err != nil {
			t.Fatalf("boot line bridge %s: %v", strings.Join(cmd, " "), err)
		}
	}
	held, _ = parseBridgeVLANs(fresh.must(t, "bridge", "-j", "vlan", "show", "dev", "jdp0"))
	if len(held["jdp0"]) != 2 || !held["jdp0"][0].PVID {
		t.Fatalf("the boot lines restore the membership: %+v", held["jdp0"])
	}
	liveHas(t, "restored fdb", fresh.must(t, "bridge", "-j", "fdb", "show", "dev", "jdvx"), `"dst":"10.99.0.9"`)
}

// TestLiveMacvlanBridgeModeConnectivity is the connectivity the macvlan
// readiness explains: bridge-mode siblings on one parent reach each other,
// and the parent's own stack does not reach them.
func TestLiveMacvlanBridgeModeConnectivity(t *testing.T) {
	liveRequired(t)
	ns := newLiveNS(t)
	ns.must(t, "ip", "link", "add", "mvp", "type", "dummy")
	ns.must(t, "ip", "addr", "add", "10.151.0.254/24", "dev", "mvp")
	ns.must(t, "ip", "link", "set", "mvp", "up")
	for i, name := range []string{"left", "right"} {
		dev := "mv" + name
		ns.must(t, "ip", "netns", "add", name)
		ns.must(t, "ip", "link", "add", dev, "link", "mvp", "type", "macvlan", "mode", "bridge")
		ns.must(t, "ip", "link", "set", dev, "netns", name)
		ns.must(t, "ip", "-n", name, "addr", "add", []string{"10.151.0.1/24", "10.151.0.2/24"}[i], "dev", dev)
		ns.must(t, "ip", "-n", name, "link", "set", dev, "up")
		ns.must(t, "ip", "-n", name, "link", "set", "lo", "up")
	}
	if _, err := ns.run(context.Background(), nil, "ip", "netns", "exec", "left", "ping", "-c", "1", "-W", "2", "10.151.0.2"); err != nil {
		t.Fatalf("bridge-mode siblings must reach each other: %v", err)
	}
	if _, err := ns.run(context.Background(), nil, "ping", "-c", "1", "-W", "1", "10.151.0.1"); err == nil {
		t.Fatal("the parent's own stack must not reach a macvlan on it")
	}
}
