package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// workspaceFixture is a store with runs in the given states and a workspace
// folder with a run-N checkout for each.
func workspaceFixture(t *testing.T, states ...RunState) (*NormalizedStepExecutor, []int64, string) {
	t.Helper()
	fixture := newReleaseStoreFixture(t)
	root := filepath.Join(t.TempDir(), "deployment-workspaces")
	ids := []int64{}
	for _, state := range states {
		result, err := fixture.base.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, status, state) VALUES(?, ?, 1, ?, ?)`,
			fixture.projectID, fixture.envID, string(state), string(state))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		ids = append(ids, id)
		writeBuildFixture(t, filepath.Join(root, "run-"+strconv.FormatInt(id, 10)), "source/package.json", "{}")
		writeBuildFixture(t, filepath.Join(root, "run-"+strconv.FormatInt(id, 10)), "source.json", "{}")
	}
	return &NormalizedStepExecutor{store: fixture.runs, workspaceRoot: root}, ids, root
}

func workspaceExists(t *testing.T, root string, runID int64) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(root, "run-"+strconv.FormatInt(runID, 10)))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// A finished run's workspace goes when it finishes; one that is under way
// keeps its own.
func TestAFinishedRunsWorkspaceIsRemovedWhenItFinishes(t *testing.T) {
	t.Parallel()
	executor, ids, root := workspaceFixture(t, RunFailed, RunRunning)
	observer := executor.HousekeepingObserver()
	observer.RunFinished(context.Background(), EngineRun{ID: ids[0], State: RunFailed})
	if workspaceExists(t, root, ids[0]) {
		t.Fatal("a failed run's workspace was left behind")
	}
	observer.RunFinished(context.Background(), EngineRun{ID: ids[1], State: RunRunning})
	if !workspaceExists(t, root, ids[1]) {
		t.Fatal("a running run's workspace was removed")
	}
	// A run without a workspace, and a repeat, are nothing to do.
	observer.RunFinished(context.Background(), EngineRun{ID: ids[0], State: RunFailed})
}

// At start-up, every workspace whose run is terminal or no longer recorded
// goes; every run short of terminal keeps its, and nothing that is not a
// run-N directory is touched.
func TestStartUpSweepRemovesOnlyFinishedRunsWorkspaces(t *testing.T) {
	t.Parallel()
	executor, ids, root := workspaceFixture(t,
		RunSucceeded, RunFailed, RunCancelled, RunRolledBack, RunSuperseded,
		RunQueued, RunRunning, RunVerifying, RunActivating, RunFailedActivation, RunRestoringPrevious, RunCancelling)
	writeBuildFixture(t, filepath.Join(root, "run-999999"), "source.json", "{}")
	writeBuildFixture(t, root, "notes/keep.txt", "the operator's")
	writeBuildFixture(t, root, "run-abc/keep.txt", "not a run")
	if err := os.WriteFile(filepath.Join(root, "run-424242"), []byte("a file, not a workspace"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	writeBuildFixture(t, outside, "keep.txt", "elsewhere")
	if err := os.Symlink(outside, filepath.Join(root, "run-777777")); err != nil {
		t.Fatal(err)
	}

	removed, err := executor.SweepWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 6 {
		t.Fatalf("removed %d workspaces, want the five finished runs' and the unrecorded one", removed)
	}
	for index, id := range ids {
		if want := index >= 5; workspaceExists(t, root, id) != want {
			t.Fatalf("run %d (%d): workspace present = %v, want %v", id, index, !want, want)
		}
	}
	if workspaceExists(t, root, 999999) {
		t.Fatal("the workspace of a run no longer recorded was kept")
	}
	for _, keep := range []string{"notes/keep.txt", "run-abc/keep.txt", "run-424242", "run-777777"} {
		if _, err := os.Lstat(filepath.Join(root, keep)); err != nil {
			t.Fatalf("%s was touched: %v", keep, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("a link's target was removed: %v", err)
	}
	// A folder that does not exist yet is nothing to sweep.
	empty := &NormalizedStepExecutor{store: executor.store, workspaceRoot: filepath.Join(t.TempDir(), "none")}
	if removed, err := empty.SweepWorkspaces(context.Background()); removed != 0 || err != nil {
		t.Fatalf("missing root = %d, %v", removed, err)
	}
}
