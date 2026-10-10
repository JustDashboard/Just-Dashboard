package netx

import (
	"context"
	"strings"
	"testing"
)

// Against a real kernel in an owned namespace: the verdict the page shows
// before a change for a kernel default, a captured fq_codel and a foreign
// hierarchy; the parameters read back after an apply; and a queue changed in
// place with `tc qdisc change`, which a kind-only check passes and the
// comparison with what was applied names.
func TestLiveQueueOwnershipAndAppliedParameters(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "qown")
	for _, dev := range []string{"d0", "d1", "d2"} {
		gwMustInNS(t, ns, "ip", "link", "add", dev, "type", "dummy")
		gwMustInNS(t, ns, "ip", "link", "set", dev, "up")
	}
	// d1: a foreign hierarchy somebody built.
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", "d1", "root", "handle", "1:", "htb", "default", "10")
	gwMustInNS(t, ns, "tc", "class", "add", "dev", "d1", "parent", "1:", "classid", "1:10", "htb", "rate", "5000kbit")
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", "d1", "parent", "1:10", "handle", "10:", "sfq")
	// d2: a classless fq_codel somebody tuned.
	gwMustInNS(t, ns, "tc", "qdisc", "replace", "dev", "d2", "root", "fq_codel", "target", "7ms", "limit", "2048")

	previous := run
	run = gwLiveRun(ns)
	t.Cleanup(func() { run = previous })
	ctx := context.Background()
	verdict := func(dev string) ShapeOwnership {
		qs, err := deviceQdiscs(ctx, dev)
		if err != nil {
			t.Fatal(err)
		}
		return ownershipOf(ctx, dev, qs)
	}
	if v := verdict("d0"); v.Verdict != "kernel" {
		t.Fatalf("a dummy's own queue = %+v", v)
	}
	if v := verdict("d1"); v.Verdict != "refused" || !strings.Contains(v.Reason, "unmanaged queue hierarchy") {
		t.Fatalf("a foreign hierarchy = %+v", v)
	}
	if v := verdict("d2"); v.Verdict != "preserved" || !strings.Contains(v.Reason, "target 7000us") {
		t.Fatalf("a tuned fq_codel = %+v", v)
	}
	// The baseline captured for d2 restores its tuning exactly.
	qs, _ := deviceQdiscs(ctx, "d2")
	baseline, _, err := unmanagedRoot("d2", qs, rootFiltersOf(ctx, "d2"), currentDefaultQdisc())
	if err != nil {
		t.Fatal(err)
	}
	before := effectiveOptions(qs[0])
	removeRoot(ctx, "d2")
	if err := runShapeLines(ctx, baseline); err != nil {
		t.Fatal(err)
	}
	qs, _ = deviceQdiscs(ctx, "d2")
	if after := effectiveOptions(qs[0]); after["target"] != before["target"] || after["limit"] != before["limit"] || qs[0].Kind != "fq_codel" {
		t.Fatalf("restored %v, was %v", after, before)
	}

	s := New(Options{DB: trafficStore(t)})
	sh := ShapeSpec{Device: "d0", Qdisc: "cake"}
	if err := runShapeLines(ctx, shapeLines(sh)); err != nil {
		t.Fatal(err)
	}
	s.recordApplied(ctx, sh)
	qs, _ = deviceQdiscs(ctx, "d0")
	v, applied := s.verifyShapeEntry(ctx, sh, qs)
	if v.Status != "verified" || applied == nil || applied.Kind != "cake" || applied.Options["rtt"] == "" {
		t.Fatalf("after apply: %+v %+v", v, applied)
	}
	// Changed in place: same kind, same handle, the kind-only check passes.
	gwMustInNS(t, ns, "tc", "qdisc", "change", "dev", "d0", "root", "cake", "rtt", "20ms")
	qs, _ = deviceQdiscs(ctx, "d0")
	if plain := readShapeVerification(ctx, sh); plain.Status != "verified" {
		t.Fatalf("the saved-entry check = %+v", plain)
	}
	v, _ = s.verifyShapeEntry(ctx, sh, qs)
	if v.Status != "drift" || !strings.Contains(v.Reason, "rtt 100000 → 20000") {
		t.Fatalf("changed in place = %+v", v)
	}
	// Replaced by another owner: a new handle.
	gwMustInNS(t, ns, "tc", "qdisc", "replace", "dev", "d0", "root", "handle", "77:", "cake")
	qs, _ = deviceQdiscs(ctx, "d0")
	v, _ = s.verifyShapeEntry(ctx, sh, qs)
	if v.Status != "drift" || !strings.Contains(v.Reason, "replaced by cake 77:") {
		t.Fatalf("replaced = %+v", v)
	}
	// Clearing gives the device back to the kernel.
	removeRoot(ctx, "d0")
	s.forgetApplied(ctx, "d0")
	if v := verdict("d0"); v.Verdict != "kernel" {
		t.Fatalf("after clearing = %+v", v)
	}
}
