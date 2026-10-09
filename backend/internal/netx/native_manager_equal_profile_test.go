package netx

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func nativeEqualProfileFixture(t *testing.T) *nativeRecoveryFixture {
	t.Helper()
	f := newNativeRecoveryFixture(t, "networkd")
	intent, err := normalizeNativeIntent(f.u.BeforeIntent)
	if err != nil {
		t.Fatal(err)
	}
	f.u.CandidateIntent = intent
	f.u.Files[0].Candidate.Data = bytes.Clone(f.u.Files[0].Before.Data)
	if err := os.WriteFile(nativeHostPath(f.u.Files[0].Before.Path), f.u.Files[0].Candidate.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveNativeUndo(f.j, f.u); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestNativeRecoveryEqualBytesRestoreTheCapturedPriorInode(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	for _, test := range []struct {
		name  string
		boot  bool
		prior string
	}{
		{"same_boot_candidate", false, ""},
		{"changed_boot_candidate", true, ""},
		{"changed_boot_authored_prior", true, "authored"},
		{"changed_boot_rollback_copy", true, "rollback"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := nativeEqualProfileFixture(t)
			file, wantedID := f.u.Files[0], f.u.Files[0].Before.Identity
			if test.prior != "" {
				restore := file.Before
				restore.Path = file.CandidatePath
				if test.prior == "rollback" {
					restore.Path, restore.Identity, wantedID = file.RollbackPath, file.RollbackID, file.RollbackID
				}
				if err := nativeReplaceProfile(restore, file.Candidate); err != nil {
					t.Fatal(err)
				}
			}
			if test.boot {
				if err := os.WriteFile(nativeHostPath("/proc/sys/kernel/random/boot_id"), []byte("83847f2b-c249-4bc4-9ae9-57438d273438"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ownership, err := nativeRecoveryOwnershipPreflight(f.u)
			if err != nil || ownership.AllPrior != (test.prior != "") || ownership.UnprovenPrior {
				t.Fatal("exact equal-byte candidate lost its distinct recorded inode", ownership, err)
			}
			if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err != nil {
				t.Fatal(err)
			}
			current, err := nativeReadProfile(f.u.Files[0].Before.Path)
			if err != nil || current.Identity != wantedID || !bytes.Equal(current.Data, f.u.Files[0].Before.Data) {
				t.Fatal("equal-byte rollback did not return its retained authored inode", current, err)
			}
			finished, err := readChange(f.s.paths.Dir)
			if err != nil || finished.Phase != "recovered" || finished.Cleanup != "complete" {
				t.Fatal("equal-byte recovery did not reach a durable cleaned prior decision", finished, err)
			}
		})
	}
}

func TestNativeRecoveryEqualBytesPreserveForeignInodesBeforeEffects(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	for _, location := range []string{"selected", "selected_after_boot", "displaced", "rollback"} {
		t.Run(location, func(t *testing.T) {
			f := nativeEqualProfileFixture(t)
			file := f.u.Files[0]
			path := map[string]string{"selected": file.Before.Path, "selected_after_boot": file.Before.Path, "displaced": file.CandidatePath, "rollback": file.RollbackPath}[location]
			nativeRecoveryTestReplace(t, path, file.Before.Data)
			if location == "selected_after_boot" {
				if err := os.WriteFile(nativeHostPath("/proc/sys/kernel/random/boot_id"), []byte("83847f2b-c249-4bc4-9ae9-57438d273438"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			foreign, err := nativeReadProfile(path)
			if err != nil {
				t.Fatal(err)
			}
			transcript := nativeExecute
			nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
				joined := strings.Join(args, " ")
				if strings.HasSuffix(joined, " Reload") || strings.Contains(joined, "ReconfigureLink") || strings.Contains(joined, "ActivateConnection") || strings.Contains(joined, "CheckpointRollback") {
					t.Fatal("foreign equal-byte inode reached an owner effect", joined)
				}
				return transcript(ctx, input, tool, args...)
			}
			if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err == nil {
				t.Fatal("foreign equal-byte inode was accepted", location)
			}
			if err := nativeCurrentFile(*foreign, foreign.Identity); err != nil {
				t.Fatal("foreign equal-byte file was overwritten", location, err)
			}
			finished, err := readChange(f.s.paths.Dir)
			if err != nil || finished.Phase != "degraded" || finished.Cleanup == "complete" {
				t.Fatal("foreign equal-byte refusal lost its recovery evidence", finished, err)
			}
			for _, stage := range []string{file.CandidatePath, file.RollbackPath} {
				if _, err := nativeReadProfile(stage); err != nil {
					t.Fatal("foreign equal-byte refusal destroyed an owned restore stage", stage, err)
				}
			}
		})
	}
}
