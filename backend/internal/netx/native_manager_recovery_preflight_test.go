package netx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeRecoveryTestReplace(t *testing.T, path string, data []byte) {
	t.Helper()
	temporary := nativeHostPath(path + ".fixture-replacement")
	if err := os.WriteFile(temporary, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, nativeHostPath(path)); err != nil {
		t.Fatal(err)
	}
}

func nativeRecoveryTestWriter(t *testing.T, f *nativeRecoveryFixture) func(context.Context, []byte, string, ...string) (string, error) {
	t.Helper()
	image, err := os.ReadFile("/usr/bin/dbus-daemon")
	if err != nil || len(image) > 2<<20 || !bytes.Contains(image, []byte("libsystemd.so.0")) {
		t.Fatal("bounded non-migrating ELF fixture unavailable", err)
	}
	path := nativeHostPath("/proc/17/exe")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
	base := f.ownerTranscript(t)
	return func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		if tool == "busctl" && strings.HasSuffix(strings.Join(args, " "), "GetConnectionUnixProcessID s "+f.u.OwnerBus) {
			return `{"type":"u","data":[17]}`, nil
		}
		return base(ctx, input, tool, args...)
	}
}

func nativeRecoveryTestCheckpoint(t *testing.T, f *nativeRecoveryFixture, next func(context.Context, []byte, string, ...string) (string, error)) (func(context.Context, []byte, string, ...string) (string, error), *int) {
	t.Helper()
	present, releases := true, new(int)
	return func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		var value any
		switch {
		case strings.HasSuffix(joined, " Checkpoints"):
			value = []string{}
			if present {
				value = []string{f.u.Checkpoint}
			}
		case strings.HasSuffix(joined, " Devices"):
			value = []string{f.u.DeviceObject}
		case strings.Contains(joined, "CheckpointDestroy o "):
			j, err := readChange(f.s.paths.Dir)
			if err != nil {
				t.Fatal(err)
			}
			u, err := nativeJournalUndo(j)
			if err != nil || u.CheckpointState != "releasing" || u.Checkpoint != f.u.Checkpoint || !strings.Contains(joined, f.u.OwnerBus) || !strings.HasSuffix(joined, " "+f.u.Checkpoint) {
				t.Fatal("deadline containment lost its durable exact checkpoint ownership", err, joined)
			}
			present = false
			*releases++
			return "", nil
		default:
			return next(ctx, input, tool, args...)
		}
		encoded, err := json.Marshal(map[string]any{"type": "ao", "data": value})
		return string(encoded), err
	}, releases
}

func TestNativeRecoveryForeignOwnershipRefusesBeforeCheckpointAndActivation(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *nativeRecoveryFixture) string
	}{
		{"candidate_bytes", func(t *testing.T, f *nativeRecoveryFixture) string {
			path := f.u.Files[0].Before.Path
			if err := os.WriteFile(nativeHostPath(path), []byte("foreign candidate bytes\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"candidate_inode", func(t *testing.T, f *nativeRecoveryFixture) string {
			path := f.u.Files[0].Before.Path
			nativeRecoveryTestReplace(t, path, f.u.Files[0].Candidate.Data)
			return path
		}},
		{"rollback_bytes", func(t *testing.T, f *nativeRecoveryFixture) string {
			path := f.u.Files[0].RollbackPath
			if err := os.WriteFile(nativeHostPath(path), []byte("foreign rollback bytes\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"rollback_inode", func(t *testing.T, f *nativeRecoveryFixture) string {
			path := f.u.Files[0].RollbackPath
			nativeRecoveryTestReplace(t, path, f.u.Files[0].Before.Data)
			return path
		}},
		{"displaced_prior_bytes", func(t *testing.T, f *nativeRecoveryFixture) string {
			path := f.u.Files[0].CandidatePath
			if err := os.WriteFile(nativeHostPath(path), []byte("foreign displaced bytes\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"displaced_prior_inode", func(t *testing.T, f *nativeRecoveryFixture) string {
			path := f.u.Files[0].CandidatePath
			nativeRecoveryTestReplace(t, path, f.u.Files[0].Before.Data)
			return path
		}},
		{"missing_prior_stages", func(t *testing.T, f *nativeRecoveryFixture) string {
			for _, path := range []string{f.u.Files[0].CandidatePath, f.u.Files[0].RollbackPath} {
				if err := os.Remove(nativeHostPath(path)); err != nil {
					t.Fatal(err)
				}
			}
			return f.u.Files[0].Before.Path
		}},
		{"unexpected_cleanup_claim", func(t *testing.T, f *nativeRecoveryFixture) string {
			path := f.u.Files[0].RollbackPath + "-cleanup"
			if err := os.WriteFile(nativeHostPath(path), []byte("foreign cleanup claim\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, "NetworkManager")
			path := test.mutate(t, f)
			before, err := nativeReadProfile(path)
			if err != nil {
				t.Fatal(err)
			}
			writer, releases := nativeRecoveryTestCheckpoint(t, f, nativeRecoveryTestWriter(t, f))
			writerChecked := false
			nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "CheckpointRollback") || strings.Contains(joined, "LoadConnections") || strings.Contains(joined, "ActivateConnection") {
					t.Fatal("foreign recovery ownership reached a native effect", tool, joined)
				}
				if strings.Contains(joined, "GetConnectionUnixProcessID") {
					writerChecked = true
				}
				return writer(ctx, input, tool, args...)
			}
			if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err == nil {
				t.Fatal("foreign ownership was accepted")
			}
			if !writerChecked || *releases != 1 {
				t.Fatal("fixture failed before the native writer admission boundary")
			}
			after, err := nativeReadProfile(path)
			if err != nil || after.Identity != before.Identity || !bytes.Equal(after.Data, before.Data) {
				t.Fatal("foreign recovery evidence was overwritten", err)
			}
			journal, err := readChange(f.s.paths.Dir)
			if err != nil || journal.ID != f.j.ID || journal.Phase != "degraded" {
				t.Fatal("refusal discarded the pending exact ownership evidence", err)
			}
			undo, err := nativeJournalUndo(journal)
			if err != nil || undo.CheckpointState != "released" || undo.Files[0].Before.Identity != f.u.Files[0].Before.Identity || undo.Files[0].Candidate.Identity != f.u.Files[0].Candidate.Identity || undo.Files[0].RollbackID != f.u.Files[0].RollbackID {
				t.Fatal("deadline containment discarded exact rollback evidence", err)
			}
			if _, err := f.s.prepareChange(context.Background(), emptySpec(), nil, nil, []byte("candidate"), nil, nil); err == nil {
				t.Fatal("ordinary change replaced failed native ownership evidence")
			}
			if err := finishPriorChange(context.Background(), f.s.paths.Dir); err == nil {
				t.Fatal("native admission discarded its unresolved prior recovery")
			}
		})
	}
}

func TestNativeRecoveryRechecksOwnershipAtCheckpointEffectBoundary(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "NetworkManager")
	writer, releases := nativeRecoveryTestCheckpoint(t, f, nativeRecoveryTestWriter(t, f))
	inventoryRead := false
	foreign := []byte("native owner edited candidate while checkpoint inventory was read\n")
	nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.HasSuffix(joined, " Checkpoints") && !inventoryRead {
			inventoryRead = true
			if err := os.WriteFile(nativeHostPath(f.u.Files[0].Before.Path), foreign, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if strings.Contains(joined, "CheckpointRollback") || strings.Contains(joined, "LoadConnections") || strings.Contains(joined, "ActivateConnection") {
			t.Fatal("inventory-time foreign write reached a native effect", joined)
		}
		return writer(ctx, input, tool, args...)
	}
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err == nil || !inventoryRead || *releases != 1 {
		t.Fatal("checkpoint effect-boundary ownership check was not exercised", err)
	}
	actual, err := nativeReadProfile(f.u.Files[0].Before.Path)
	if err != nil || !bytes.Equal(actual.Data, foreign) {
		t.Fatal("native checkpoint effect overwrote a concurrent foreign edit", err)
	}
}

func TestNativeRecoveryPriorIdentityCanOnlyCompleteReadOnly(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	for _, owner := range []string{"networkd", "NetworkManager"} {
		t.Run(owner, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, owner)
			nativeRecoveryTestReplace(t, f.u.Files[0].Before.Path, f.u.Files[0].Before.Data)
			state, err := nativeRecoveryOwnershipPreflight(f.u)
			if owner == "networkd" {
				if err == nil {
					t.Fatal("independent recovery adopted a foreign prior inode")
				}
				return
			}
			if err != nil || !state.AllPrior || !state.UnprovenPrior {
				t.Fatal("native restored prior inode lost its read-only witness", state, err)
			}
			writer, _ := nativeRecoveryTestCheckpoint(t, f, nativeRecoveryTestWriter(t, f))
			readAttempted := false
			nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "CheckpointRollback") || strings.Contains(joined, "LoadConnections") || strings.Contains(joined, "ActivateConnection") {
					t.Fatal("unrecorded prior inode authorized a native effect", joined)
				}
				if strings.Contains(joined, "NameHasOwner") {
					readAttempted = true
					return "", errors.New("fixture refuses complete active ownership proof")
				}
				return writer(ctx, input, tool, args...)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := RecoverNetwork(ctx, f.s.paths.Dir, f.j.ID); err == nil || !readAttempted {
				t.Fatal("unknown prior inode was accepted without complete read-only active proof", err)
			}
		})
	}
}

func TestNativeRecoveryOwnedStagesRemainIdempotent(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	f := newNativeRecoveryFixture(t, "networkd")
	if state, err := nativeRecoveryOwnershipPreflight(f.u); err != nil || state.AllPrior || state.UnprovenPrior {
		t.Fatal("exact candidate and retained prior stages refused", state, err)
	}
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err != nil {
		t.Fatal(err)
	}
	if state, err := nativeRecoveryOwnershipPreflight(f.u); err != nil || !state.AllPrior || state.UnprovenPrior {
		t.Fatal("completed exact-inode restore cannot be inspected idempotently", state, err)
	}
	if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err != nil {
		t.Fatal("terminal recovery retry failed", err)
	}
}

func TestNativeRecoveryRefusalRetainsUnknownCheckpointReleaseForRetry(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	for _, failure := range []string{"lost_reply", "unsaved_release", "foreign_scope", "unsaved_admission"} {
		t.Run(failure, func(t *testing.T) {
			f := newNativeRecoveryFixture(t, "NetworkManager")
			path := f.u.Files[0].Before.Path
			foreign := []byte("foreign selected profile retained for native owner review\n")
			if err := os.WriteFile(nativeHostPath(path), foreign, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := nativeReadProfile(path)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint, releases := nativeRecoveryTestCheckpoint(t, f, nativeRecoveryTestWriter(t, f))
			lost := false
			nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, "CheckpointRollback") || strings.Contains(joined, "LoadConnections") || strings.Contains(joined, "ActivateConnection") {
					t.Fatal("refused foreign profile reached rollback or activation", joined)
				}
				if failure == "foreign_scope" && strings.HasSuffix(joined, " Devices") {
					return `{"type":"ao","data":["/org/freedesktop/NetworkManager/Devices/9"]}`, nil
				}
				out, err := checkpoint(ctx, input, tool, args...)
				if failure == "lost_reply" && strings.Contains(joined, "CheckpointDestroy o ") && !lost {
					lost = true
					return "", errors.New("fixture lost successful checkpoint release reply")
				}
				return out, err
			}
			writer := writeChangeJournal
			writeChangeJournal = func(path string, data []byte, mode os.FileMode) error {
				var journal changeJournal
				if err := json.Unmarshal(data, &journal); err != nil {
					t.Fatal(err)
				}
				undo, err := nativeJournalUndo(&journal)
				if err != nil {
					t.Fatal(err)
				}
				if failure == "unsaved_release" && undo.CheckpointState == "released" || failure == "unsaved_admission" && undo.CheckpointState == "releasing" && journal.Phase == "recovering" {
					return errors.New("fixture failed durable checkpoint progress")
				}
				return writer(path, data, mode)
			}
			firstErr := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID)
			writeChangeJournal = writer
			if firstErr == nil {
				t.Fatal("foreign recovery was accepted")
			}
			journal, err := readChange(f.s.paths.Dir)
			if err != nil {
				t.Fatal(err)
			}
			undo, err := nativeJournalUndo(journal)
			if err != nil || journal.Phase != "degraded" && failure != "unsaved_release" {
				t.Fatal("refusal lost its recovery journal", err)
			}
			if failure == "foreign_scope" || failure == "unsaved_admission" {
				if *releases != 0 || failure == "foreign_scope" && undo.CheckpointState != "armed" || failure == "unsaved_admission" && undo.CheckpointState != "releasing" || !strings.Contains(firstErr.Error(), "deadline") {
					t.Fatal("unproven checkpoint containment was claimed", *releases, undo.CheckpointState, firstErr)
				}
			} else {
				if *releases != 1 || undo.CheckpointState != "releasing" {
					t.Fatal("uncertain release lost its durable retry state", *releases, undo.CheckpointState)
				}
				if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err == nil {
					t.Fatal("release retry adopted the foreign profile")
				}
				journal, err = readChange(f.s.paths.Dir)
				if err != nil {
					t.Fatal(err)
				}
				undo, err = nativeJournalUndo(journal)
				if err != nil || undo.CheckpointState != "released" || *releases != 1 {
					t.Fatal("exact absent-checkpoint retry was not idempotent", err)
				}
			}
			after, err := nativeReadProfile(path)
			if err != nil || after.Identity != before.Identity || !bytes.Equal(after.Data, foreign) {
				t.Fatal("containment retry overwrote foreign profile", err)
			}
			for _, stage := range []string{f.u.Files[0].CandidatePath, f.u.Files[0].RollbackPath} {
				if _, err := nativeReadProfile(stage); err != nil {
					t.Fatal("failed recovery deleted restore evidence", err)
				}
			}
		})
	}
}

func TestNativeRecoveryBootAndLegacyPriorOwnershipRemainClosed(t *testing.T) {
	if !nativeRootTest(t) {
		return
	}
	for _, version := range []int{1, 2, 3} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			f := newNativeRecoveryFixture(t, "networkd")
			f.u.Version = version
			if version >= 2 {
				f.u.RecoveryStrategy = nativeExactOriginStrategy
			}
			if err := saveNativeUndo(f.j, f.u); err != nil {
				t.Fatal("legacy exact journal vocabulary was lost", err)
			}
			nativeRecoveryTestReplace(t, f.u.Files[0].Before.Path, f.u.Files[0].Before.Data)
			before, err := nativeReadProfile(f.u.Files[0].Before.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(nativeHostPath("/proc/sys/kernel/random/boot_id"), []byte("83847f2b-c249-4bc4-9ae9-57438d273438"), 0o600); err != nil {
				t.Fatal(err)
			}
			transcript := nativeExecute
			nativeExecute = func(ctx context.Context, input []byte, tool string, args ...string) (string, error) {
				joined := strings.Join(args, " ")
				if strings.HasSuffix(joined, " Reload") || strings.Contains(joined, "ReconfigureLink") {
					t.Fatal("boot recovery adopted a replaced persistent prior inode", joined)
				}
				return transcript(ctx, input, tool, args...)
			}
			if err := RecoverNetwork(context.Background(), f.s.paths.Dir, f.j.ID); err == nil {
				t.Fatal("legacy boot recovery adopted a foreign prior inode")
			}
			after, err := nativeReadProfile(f.u.Files[0].Before.Path)
			if err != nil || after.Identity != before.Identity || !bytes.Equal(after.Data, before.Data) {
				t.Fatal("boot ownership refusal modified the replaced prior profile", err)
			}
		})
	}
}
