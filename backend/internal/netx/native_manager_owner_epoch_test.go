package netx

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestNativeRecoveryRefusesPriorUniqueOwnerAfterServiceHandoff(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	delegate := nativeExecute
	changed := true
	mutations := 0
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if tool == "busctl" && strings.HasSuffix(joined, "GetNameOwner s "+networkdService) && changed {
			return `{"type":"s","data":[":1.999"]}`, nil
		}
		if tool == "busctl" && (strings.HasSuffix(joined, " Reload") || strings.Contains(joined, " ReconfigureLink")) {
			mutations++
		}
		return delegate(ctx, input, tool, args...)
	}
	for attempt := range 2 {
		if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err == nil {
			t.Fatal("a prior unique owner was allowed to restore after service handoff")
		}
		current, err := nativeReadProfile(f.u.Files[0].Before.Path)
		if err != nil || !bytes.Equal(current.Data, f.u.Files[0].Candidate.Data) || mutations != 0 {
			t.Fatalf("refused handoff performed an effect on attempt %d: %+v %v %d", attempt, current, err, mutations)
		}
		journal, err := readChange(f.s.paths.Dir)
		if err != nil || journal.ID != f.j.ID || journal.Phase != "degraded" || journal.Cleanup == "complete" {
			t.Fatalf("refused owner handoff discarded its journal: %+v %v", journal, err)
		}
		if err := finishPriorChange(context.Background(), f.s.paths.Dir); err == nil {
			t.Fatal("a next change replaced unresolved owner-handoff evidence")
		}
	}
	changed = false
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err != nil {
		t.Fatal("the same original service owner could not retry exact recovery:", err)
	}
}

func TestNativeCheckpointAdmissionRefusesChangedWellKnownOwner(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "NetworkManager")
	delegate := nativeExecute
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if tool == "busctl" && strings.HasSuffix(joined, "GetNameOwner s "+nmService) {
			return `{"type":"s","data":[":1.999"]}`, nil
		}
		if tool == "busctl" && (strings.Contains(joined, " CheckpointCreate ") || strings.Contains(joined, " CheckpointDestroy ") || strings.Contains(joined, " CheckpointRollback ")) {
			t.Fatal("service ownership handoff reached a checkpoint effect on the prior unique owner")
		}
		return delegate(ctx, input, tool, args...)
	}
	f.u.Checkpoint, f.u.CheckpointState = "", "none"
	if err := saveNativeUndo(f.j, f.u); err != nil {
		t.Fatal(err)
	}
	if err := nativeCheckpointCreate(context.Background(), f.j, f.u); err == nil || f.u.CheckpointState != "none" {
		t.Fatal("a changed well-known owner reached native checkpoint admission:", err)
	}
	journal, err := readChange(f.s.paths.Dir)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := nativeJournalUndo(journal)
	if err != nil || retained.OwnerBus != f.u.OwnerBus || retained.CheckpointState != "none" {
		t.Fatal("the saved prior unique owner or admission scope was replaced:", err)
	}
}
