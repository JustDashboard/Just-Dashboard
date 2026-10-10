package netx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func liveSQMShape(t *testing.T, ctx context.Context, device string) ShapeSpec {
	t.Helper()
	profile, err := normSQMProfile(SQMProfile{Diffserv: "diffserv3", Overhead: 22, MPU: 64})
	if err != nil {
		t.Fatal(err)
	}
	sh := ShapeSpec{Device: device, IngressKbit: 9000, SQM: &SQMSpec{SQMProfile: profile}}
	if err := prepareSQM(ctx, &sh, nil); err != nil {
		t.Fatal(err)
	}
	return sh
}

func TestLiveSQMOwnershipRecoveryAndColdRestore(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "sqm-owner")
	previous, previousStdin := run, runStdin
	run = gwLiveRun(ns)
	runStdin = func(ctx context.Context, input []byte, name string, args ...string) (string, error) {
		if len(input) != 0 {
			t.Fatal("SQM acceptance must use explicit argv")
		}
		return run(ctx, name, args...)
	}
	t.Cleanup(func() { run, runStdin = previous, previousStdin })
	ctx := context.Background()
	device := "sqmsrc"
	gwMustInNS(t, ns, "ip", "link", "add", device, "type", "dummy")
	gwMustInNS(t, ns, "ip", "link", "set", device, "up")
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", device, "root", "handle", "5:", "fq_codel", "limit", "1000", "target", "7ms", "noecn")
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", device, "clsact")
	gwMustInNS(t, ns, "tc", "filter", "add", "dev", device, "egress", "protocol", "all", "pref", "7", "matchall", "action", "pass")
	rootBefore := gwMustInNS(t, ns, "tc", "-j", "qdisc", "show", "dev", device)
	egressBefore := gwMustInNS(t, ns, "tc", "-j", "filter", "show", "dev", device, "egress")
	sh := liveSQMShape(t, ctx, device)
	if sh.SQM.Hook != "clsact" || sh.SQM.CreatedHook {
		t.Fatalf("adopted the foreign hook: %+v", sh.SQM)
	}
	if err := applySQM(ctx, sh, nil); err != nil {
		t.Fatal(err)
	}
	if err := verifyShaping(ctx, sh); err != nil {
		t.Fatal(err)
	}
	wrong := sh
	copy := *sh.SQM
	copy.Overhead++
	wrong.SQM = &copy
	if err := verifySQM(ctx, wrong); !errors.Is(err, errShapingDrift) {
		t.Fatalf("accepted changed native overhead: %v", err)
	}
	if got := gwMustInNS(t, ns, "tc", "-j", "qdisc", "show", "dev", device); got != rootBefore {
		t.Fatalf("source root/clsact replaced: %s -> %s", rootBefore, got)
	}
	if got := gwMustInNS(t, ns, "tc", "-j", "filter", "show", "dev", device, "egress"); got != egressBefore {
		t.Fatal("foreign egress filters replaced")
	}
	old := &Spec{Version: specVersion, Shaping: []ShapeSpec{sh}}
	candidate := sh
	candidate.IngressKbit = 7000
	commands, err := sqmRecoveryPlan(old, &Spec{Shaping: []ShapeSpec{candidate}})
	if err != nil {
		t.Fatal(err)
	}
	// A timer may fire before the apply reaches its first effect, or after a
	// new queue is installed. Both restore the same saved owned profile.
	if err := recoverSQM(ctx, commands[0].Args); err != nil {
		t.Fatalf("prepared recovery: %v", err)
	}
	if err := applySQM(ctx, candidate, &sh); err != nil {
		t.Fatal(err)
	}
	if err := recoverSQM(ctx, commands[0].Args); err != nil {
		t.Fatalf("runtime recovery: %v", err)
	}
	if err := verifySQM(ctx, sh); err != nil {
		t.Fatal(err)
	}
	// Recreate only the owned, reboot-volatile IFB/redirect. The existing
	// native root/clsact owner is already restored and remains intact.
	if err := removeSQM(ctx, sh, false); err != nil {
		t.Fatal(err)
	}
	s := testService(t)
	data, _ := json.Marshal(old)
	if err := writeFileAtomic(s.specPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreSQMBoot(ctx, s.paths.Dir); err != nil {
		t.Fatalf("cold boot restore: %v", err)
	}
	if err := verifySQM(ctx, sh); err != nil {
		t.Fatal(err)
	}
	// An alias pattern alone is insufficient even with the exact saved
	// alias: a replaced native link has a different creation MAC.
	gwMustInNS(t, ns, "ip", "link", "del", "dev", sh.SQM.IFB)
	gwMustInNS(t, ns, "ip", "link", "add", "name", sh.SQM.IFB, "address", "02:00:00:00:00:99", "type", "ifb")
	gwMustInNS(t, ns, "ip", "link", "set", "dev", sh.SQM.IFB, "alias", sqmAliasPrefix+sh.SQM.Token)
	if err := RestoreSQMBoot(ctx, s.paths.Dir); !errors.Is(err, errShapingDrift) {
		t.Fatalf("foreign replacement adopted at boot: %v", err)
	}
	foreignBefore := gwMustInNS(t, ns, "ip", "-j", "-d", "link", "show", "dev", sh.SQM.IFB)
	if err := removeSQM(ctx, sh, false); !errors.Is(err, errShapingDrift) {
		t.Fatalf("foreign replacement removed: %v", err)
	}
	foreignAfter := gwMustInNS(t, ns, "ip", "-j", "-d", "link", "show", "dev", sh.SQM.IFB)
	if foreignBefore != foreignAfter {
		t.Fatal("foreign IFB changed on refusal")
	}
	// Recovery handles death between atomic MAC-bearing creation and alias
	// installation, while keeping the native owner's clsact/egress intact.
	gwMustInNS(t, ns, "ip", "link", "del", "dev", sh.SQM.IFB)
	gwMustInNS(t, ns, append([]string{"tc"}, sqmRedirectArgs(sh, "del")...)...)
	gwMustInNS(t, ns, "ip", "link", "add", "name", sh.SQM.IFB, "address", sqmIFBMAC(sh.SQM.Token), "mtu", "1500", "type", "ifb")
	commands, err = sqmRecoveryPlan(emptySpec(), old)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverSQM(ctx, commands[0].Args); err != nil {
		t.Fatalf("partial creation recovery: %v", err)
	}
	if link, err := readSQMLink(ctx, sh.SQM.IFB); link != nil || err != nil {
		t.Fatalf("partial IFB leaked: %+v %v", link, err)
	}
	if got := gwMustInNS(t, ns, "tc", "-j", "filter", "show", "dev", device, "egress"); got != egressBefore {
		t.Fatal("egress filters changed after all recovery paths")
	}
	if bytes, _ := os.ReadFile(s.specPath()); !strings.Contains(string(bytes), sh.SQM.Token) {
		t.Fatal("boot inspection rewrote saved intent")
	}
}
