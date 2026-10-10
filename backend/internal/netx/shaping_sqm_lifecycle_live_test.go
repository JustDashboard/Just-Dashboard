package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveSQMConfirmsFreshResponseAndRecoversFailedOwnedUpdate(t *testing.T) {
	gwLiveRequired(t)
	ns := gwLiveNS(t, "sqm-life")
	gwMustInNS(t, ns, "ip", "link", "add", "sqmsrc", "type", "dummy")
	gwMustInNS(t, ns, "ip", "link", "set", "sqmsrc", "up")
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", "sqmsrc", "root", "handle", "5:", "fq_codel", "limit", "1000", "target", "7ms", "noecn")
	gwMustInNS(t, ns, "tc", "qdisc", "add", "dev", "sqmsrc", "clsact")
	gwMustInNS(t, ns, "tc", "filter", "add", "dev", "sqmsrc", "egress", "protocol", "all", "pref", "7", "matchall", "action", "pass")
	s := testService(t)
	s.independentRecovery, s.recoveryInstalled = true, true
	previous, previousStdin, previousHas, previousAnchors := run, runStdin, has, anchorPaths
	t.Cleanup(func() { run, runStdin, has, anchorPaths = previous, previousStdin, previousHas, previousAnchors })
	has = func(string) bool { return true }
	anchorPaths = func(context.Context) []anchorPath { return nil }
	native := gwLiveRun(ns)
	brokenBootReadback := false
	reloads := 0
	run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == filepath.Join(s.paths.Dir, recoveryBinary) {
			if len(args) == 1 && args[0] == "--network-sqm-check" {
				return "sqm-v1\n", nil
			}
			return "", nil
		}
		if name == "systemd-run" {
			return "fixture independent timer armed", nil
		}
		if name == "systemctl" {
			if len(args) > 0 && args[0] == "daemon-reload" {
				reloads++
				return "", nil
			}
			if len(args) > 0 && args[0] == "is-enabled" {
				return "enabled\n", nil
			}
			if len(args) > 0 && args[0] == "show" {
				if brokenBootReadback {
					return "FragmentPath=" + s.paths.Unit + "\nDropInPaths=\nExecStart={ path=/foreign; argv[]=/foreign ; ignore_errors=no ; }\n", nil
				}
				return "FragmentPath=" + s.paths.Unit + "\nDropInPaths=\nExecStart={ path=" + filepath.Join(s.paths.Dir, recoveryBinary) + " ; argv[]=" + filepath.Join(s.paths.Dir, recoveryBinary) + " --network-sqm-restore " + s.paths.Dir + " ; ignore_errors=no ; }\n", nil
			}
			return "", nil
		}
		return native(ctx, name, args...)
	}
	runStdin = func(ctx context.Context, input []byte, name string, args ...string) (string, error) {
		if len(input) != 0 {
			t.Fatal("unexpected stdin")
		}
		return run(ctx, name, args...)
	}
	ctx := WithPendingConfirmation(context.Background(), 7)
	request := ShapeRequest{IngressKbit: 9000, SQM: &SQMProfile{}}
	if err := s.SetShaping(ctx, "sqmsrc", request, "127.0.0.1", "fixture"); err != nil {
		t.Fatal(err)
	}
	j, err := readChange(s.paths.Dir)
	if err != nil || j.Phase != "awaiting_confirmation" || j.Watchdog != "armed" || j.Boot != "enabled" {
		t.Fatalf("missing independent pending evidence: %+v %v", j, err)
	}
	if _, err := s.ConfirmChange(context.Background(), j.ID, 7, "fresh-session", "wrong", "https://fixture"); err == nil {
		t.Fatal("SQM was confirmed without a fresh response")
	}
	challenge, err := s.VerifyReconnection(context.Background(), j.ID, 7, "fresh-session", "https://fixture")
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.ConfirmChange(context.Background(), j.ID, 7, "fresh-session", challenge.Challenge, "https://fixture")
	if err != nil || status.Phase != "confirmed" {
		t.Fatalf("positive confirmation failed: %+v %v", status, err)
	}
	sp, err := s.loadSpec()
	if err != nil || len(sp.Shaping) != 1 {
		t.Fatalf("saved SQM intent: %+v %v", sp, err)
	}
	sh := sp.Shaping[0]
	if err := RecoverNetwork(context.Background(), s.paths.Dir, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := verifySQM(context.Background(), sh); err != nil {
		t.Fatalf("old watchdog undid confirmed SQM: %v", err)
	}
	before, _ := os.ReadFile(s.specPath())
	unitBefore, _ := os.ReadFile(s.paths.Unit)
	// The new rate reaches the native IFB, but its loaded boot dependency
	// cannot be verified. Restore both saved intent and the actual prior
	// queue rather than presenting one half of the update as enabled.
	brokenBootReadback = true
	request.IngressKbit = 7000
	beforeReloads := reloads
	if err := s.SetShaping(ctx, "sqmsrc", request, "127.0.0.1", "fixture"); !errors.Is(err, errShapingDrift) {
		t.Fatalf("unverified boot dependency accepted: %v", err)
	}
	after, _ := os.ReadFile(s.specPath())
	unitAfter, _ := os.ReadFile(s.paths.Unit)
	if string(before) != string(after) || string(unitBefore) != string(unitAfter) {
		t.Fatal("failed update changed prior saved intent or boot bytes")
	}
	if err := verifySQM(context.Background(), sh); err != nil {
		t.Fatalf("failed update lost the prior native profile: %v", err)
	}
	j, err = readChange(s.paths.Dir)
	if err != nil || j.Phase != "recovered" || len(j.RecoveryErrors) != 0 {
		t.Fatalf("failed update recovery evidence: %+v %v", j, err)
	}
	// A rate-only update did not change the unit bytes; it still reloads
	// only when a candidate unit was actually written/restored.
	if reloads != beforeReloads {
		t.Fatal("a rate-only rollback reloaded an unrelated unchanged unit")
	}
	brokenBootReadback = false
	if err := s.ClearShaping(ctx, "sqmsrc"); err != nil {
		t.Fatal(err)
	}
	j, err = readChange(s.paths.Dir)
	if err != nil || j.Phase != "awaiting_confirmation" {
		t.Fatalf("clear bypassed reconnect confirmation: %+v %v", j, err)
	}
	if link, err := readSQMLink(context.Background(), sh.SQM.IFB); err != nil || link != nil {
		t.Fatalf("owned IFB not removed: %+v %v", link, err)
	}
	if got := gwMustInNS(t, ns, "tc", "-j", "qdisc", "show", "dev", "sqmsrc"); !strings.Contains(got, `"kind":"clsact"`) || !strings.Contains(got, `"handle":"5:"`) {
		t.Fatalf("clear removed the native baseline: %s", got)
	}
	if got := gwMustInNS(t, ns, "tc", "-j", "filter", "show", "dev", "sqmsrc", "egress"); !strings.Contains(got, `"pref":7`) {
		t.Fatalf("clear removed native egress filters: %s", got)
	}
	challenge, err = s.VerifyReconnection(context.Background(), j.ID, 7, "fresh-session", "https://fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmChange(context.Background(), j.ID, 7, "fresh-session", challenge.Challenge, "https://fixture"); err != nil {
		t.Fatal(err)
	}
	clearSpec, _ := os.ReadFile(s.specPath())
	clearUnit, _ := os.ReadFile(s.paths.Unit)
	beforeReloads = reloads
	brokenBootReadback = true
	request.IngressKbit = 9000
	if err := s.SetShaping(ctx, "sqmsrc", request, "127.0.0.1", "fixture"); !errors.Is(err, errShapingDrift) {
		t.Fatalf("fresh enable kept an unverified boot dependency: %v", err)
	}
	after, _ = os.ReadFile(s.specPath())
	unitAfter, _ = os.ReadFile(s.paths.Unit)
	if string(after) != string(clearSpec) || string(unitAfter) != string(clearUnit) || reloads != beforeReloads+2 {
		t.Fatal("failed fresh enable did not restore and reload the previous boot unit")
	}
	if got := gwMustInNS(t, ns, "ip", "-j", "-d", "link", "show"); strings.Contains(got, `"info_kind":"ifb"`) {
		t.Fatalf("failed fresh enable leaked an IFB: %s", got)
	}
}
