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

// A Compose release runs from its run's workspace: stopping it for the next
// release, starting it again and rolling back to it run Compose there. The
// workspace is kept while a runtime record names it, retired or not, and
// goes once that record is removed.
func TestAComposeReleasesWorkspaceIsKeptWhileItsRuntimeIsRecorded(t *testing.T) {
	t.Parallel()
	executor, ids, root := workspaceFixture(t, RunSucceeded, RunSucceeded, RunSucceeded)
	db := executor.store.db
	var projectID, environmentID int64
	if err := db.QueryRow(`SELECT project_id, environment_id FROM deploy_runs WHERE id = ?`, ids[0]).Scan(&projectID, &environmentID); err != nil {
		t.Fatal(err)
	}
	runtime := func(runID int64, number int, kind, state, directory string) {
		t.Helper()
		result, err := db.Exec(`INSERT INTO deploy_releases(project_id, environment_id, release_number, run_id, state, created_at) VALUES(?, ?, ?, ?, 'live', 1)`,
			projectID, environmentID, number, runID)
		if err != nil {
			t.Fatal(err)
		}
		releaseID, _ := result.LastInsertId()
		if _, err := db.Exec(`INSERT INTO deploy_release_runtimes(release_id, environment_id, kind, runtime_id, working_directory, state, created_at, updated_at) VALUES(?, ?, ?, 'jd-e1', ?, ?, 1, 1)`,
			releaseID, environmentID, kind, directory, state); err != nil {
			t.Fatal(err)
		}
	}
	workspace := func(id int64) string { return filepath.Join(root, "run-"+strconv.FormatInt(id, 10)) }
	runtime(ids[0], 1, "compose", "retired", filepath.Join(workspace(ids[0]), "source"))
	runtime(ids[1], 2, "compose", "removed", filepath.Join(workspace(ids[1]), "source"))
	// A container release's directory is nothing Compose reads again, and a
	// sibling whose name only starts with the workspace's is another run's.
	runtime(ids[2], 3, "container", "live", filepath.Join(workspace(ids[2]), "source"))
	writeBuildFixture(t, root, "run-"+strconv.FormatInt(ids[0], 10)+"0/source.json", "{}")

	observer := executor.HousekeepingObserver()
	observer.RunFinished(context.Background(), EngineRun{ID: ids[0], State: RunSucceeded})
	if !workspaceExists(t, root, ids[0]) {
		t.Fatal("the workspace a retired Compose release runs from was removed when its run finished")
	}
	removed, err := executor.SweepWorkspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !workspaceExists(t, root, ids[0]) || workspaceExists(t, root, ids[1]) || workspaceExists(t, root, ids[2]) {
		t.Fatalf("after the sweep: kept %v, %v, %v; want only the recorded Compose release's",
			workspaceExists(t, root, ids[0]), workspaceExists(t, root, ids[1]), workspaceExists(t, root, ids[2]))
	}
	if removed != 3 {
		t.Fatalf("removed %d workspaces, want the removed release's, the container release's and the unrecorded run's", removed)
	}
	if _, err := db.Exec(`UPDATE deploy_release_runtimes SET state = 'removed' WHERE working_directory = ?`, filepath.Join(workspace(ids[0]), "source")); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.SweepWorkspaces(context.Background()); err != nil || workspaceExists(t, root, ids[0]) {
		t.Fatalf("a workspace whose runtime was removed was kept: %v", err)
	}
}

// A Compose release whose project directory, a Compose file or its
// override is no longer there is stopped and started by its containers; one
// whose files are all there is Compose's.
func TestComposeFilesGoneNamesAReleaseWithoutItsFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	writeBuildFixture(t, directory, "compose.yaml", "services: {}")
	writeBuildFixture(t, directory, ".just-dashboard/release.yml", "services: {}")
	whole := dockerReleaseRuntimeMetadata{ProjectDirectory: directory, ComposeFiles: []string{"compose.yaml"},
		OverrideFile: filepath.Join(directory, ".just-dashboard/release.yml")}
	if composeFilesGone(whole) {
		t.Fatal("a release with every file was taken for one without")
	}
	for name, metadata := range map[string]dockerReleaseRuntimeMetadata{
		"directory": {ProjectDirectory: filepath.Join(directory, "gone"), ComposeFiles: []string{"compose.yaml"}},
		"file":      {ProjectDirectory: directory, ComposeFiles: []string{"compose.yaml", "other.yaml"}},
		"override":  {ProjectDirectory: directory, ComposeFiles: []string{"compose.yaml"}, OverrideFile: filepath.Join(directory, "missing.yml")},
	} {
		if !composeFilesGone(metadata) {
			t.Fatalf("a release without its %s was taken for a whole one", name)
		}
	}
	if composeFilesGone(dockerReleaseRuntimeMetadata{}) {
		t.Fatal("a record without a project directory is not one whose files went")
	}
}
