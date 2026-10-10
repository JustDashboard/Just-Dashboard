package netx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeImmediateEditDoesNotCreateRecoveryState(t *testing.T) {
	for _, owner := range []int64{-1, 0} {
		dir := filepath.Join(t.TempDir(), "network")
		s := New(Options{Paths: Paths{Dir: dir}})
		view, err := s.EditNativeProfile(WithPendingConfirmation(t.Context(), owner), "d0", NativeEditRequest{}, "192.0.2.17")
		var confirmation *ConfirmationError
		if view != nil || !errors.As(err, &confirmation) {
			t.Fatalf("nonpositive pending owner %d = %+v %v", owner, view, err)
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("refused native request created recovery state: %v", err)
		}
	}
}

func TestNativeImmediateEditCannotFinalizePriorConfirmedChange(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	f.j.Phase = "confirmed"
	if err := f.j.save(); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(f.s.paths.Dir, recoveryFile)
	paths := []string{journalPath, nativeHostPath(f.u.Files[0].Before.Path), nativeHostPath(f.u.Files[0].CandidatePath), nativeHostPath(f.u.Files[0].RollbackPath)}
	before := make([][]byte, len(paths))
	for i, path := range paths {
		var err error
		before[i], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	nativeExecute = func(context.Context, []byte, string, ...string) (string, error) {
		t.Fatal("an immediate native edit reached prior terminal cleanup")
		return "", errors.New("unexpected native operation")
	}
	view, err := f.s.EditNativeProfile(t.Context(), "d0", NativeEditRequest{}, "192.0.2.17")
	var confirmation *ConfirmationError
	if view != nil || !errors.As(err, &confirmation) {
		t.Fatalf("immediate edit = %+v %v", view, err)
	}
	for i, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, before[i]) {
			t.Fatalf("refused request changed the retained native transaction at %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.s.paths.Dir, ".change.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("immediate native request acquired recovery state: %v", err)
	}
}
