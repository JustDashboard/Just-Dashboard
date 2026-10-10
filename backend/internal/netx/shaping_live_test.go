package netx

import (
	"context"
	"strings"
	"testing"
)

func TestLiveShapingVerifiesEveryRequestedParameter(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "shape-verify")
	previous := run
	run = gwLiveRun(ns)
	t.Cleanup(func() { run = previous })
	ctx := context.Background()
	for _, sh := range []ShapeSpec{
		{Device: "ds0", EgressKbit: 50000, IngressKbit: 100000},
		{Device: "ds1", Qdisc: "cake", EgressKbit: 30000, IngressKbit: 30000},
		{Device: "ds2", Qdisc: "fq", EgressKbit: 9999},
		{Device: "ds3", IngressKbit: 9877},
	} {
		gwMustInNS(t, ns, "ip", "link", "add", sh.Device, "type", "dummy")
		gwMustInNS(t, ns, "ip", "link", "set", sh.Device, "up")
		if err := runShapeLines(ctx, shapeLines(sh)); err != nil {
			t.Fatal(err)
		}
		if err := verifyShaping(ctx, sh); err != nil {
			t.Fatalf("real tc parameters rejected for %+v: %v", sh, err)
		}
		wrong := sh
		if wrong.EgressKbit > 0 {
			wrong.EgressKbit++
		} else {
			wrong.IngressKbit++
		}
		if err := verifyShaping(ctx, wrong); err == nil {
			t.Fatalf("wrong real tc rate accepted for %+v", wrong)
		}
	}
}

func TestLiveShapingRestoresSupportedBaselineAndRefusesForeignTree(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "shape-owner")
	previous := run
	run = gwLiveRun(ns)
	t.Cleanup(func() { run = previous })
	ctx := context.Background()
	device := "ds4"
	gwMustInNS(t, ns, "ip", "link", "add", device, "type", "dummy")
	gwMustInNS(t, ns, "ip", "link", "set", device, "up")
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", device, "root", "handle", "5:", "fq_codel", "limit", "1000", "target", "7ms", "noecn")
	sh := ShapeSpec{Device: device, EgressKbit: 9000}
	restore, err := shapingBaseline(ctx, sh, nil)
	if err != nil || len(restore) != 1 || !strings.Contains(restore[0], "limit 1000") || !strings.Contains(restore[0], "noecn") {
		t.Fatalf("baseline=%v,%v", restore, err)
	}
	if err := runShapeLines(ctx, shapeLines(sh)); err != nil {
		t.Fatal(err)
	}
	if err := undoShaping(ctx, sh, nil); err != nil {
		t.Fatal(err)
	}
	if err := runShapeLines(ctx, restore); err != nil {
		t.Fatal(err)
	}
	qs, err := deviceQdiscs(ctx, device)
	if err != nil || len(qs) != 1 || qs[0].Kind != "fq_codel" || qs[0].Handle != "5:" || string(qs[0].Options["limit"]) != "1000" || string(qs[0].Options["ecn"]) == "true" {
		t.Fatalf("restored queue=%+v,%v", qs, err)
	}
	gwMustInNS(t, ns, "tc", "qdisc", "del", "dev", device, "root")
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", device, "root", "handle", "9:", "htb")
	if _, err := shapingBaseline(ctx, sh, nil); err == nil {
		t.Fatal("unmanaged HTB allowed")
	}
	qs, err = deviceQdiscs(ctx, device)
	if err != nil || len(qs) != 1 || qs[0].Kind != "htb" || qs[0].Handle != "9:" {
		t.Fatalf("foreign queue changed=%+v,%v", qs, err)
	}
}
