package deploy

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

// Sweeping the workspaces of finished runs.
//
// A run's source is materialized into deployment-workspaces/run-N and
// removed by release_task, so a run that ends before it — a failed build, a
// refused preflight, a cancel — left its checkout behind for good. Nothing
// reads a finished run's workspace again: the materializer reuses one only
// for the same run (its marker), a retry is a new run with its own, and a
// terminal run never runs another step. So a run's workspace is removed
// when the run finishes (the run-finished HousekeepingObserver), and at
// start-up every workspace whose run is terminal, or no longer recorded at
// all, is removed too. A workspace of a run that is queued, running or
// anywhere short of terminal is never touched, nor anything in the folder
// that is not a run-N directory.
//
// One finished run's workspace is read again: a Compose release runs from
// it. Its runtime record names the workspace as the project's directory,
// with the Compose files and the release's override in it
// (runtime_owner.go), and stopping that release for the next one, starting
// it again and rolling back to it all run Compose there. Such a workspace
// is kept for as long as a runtime record that is not removed names it,
// and goes at the first start-up after that.

var runWorkspaceRE = regexp.MustCompile(`^run-([1-9][0-9]{0,18})$`)

// runWorkspace is the workspace of a run, from the trusted root and the
// numeric id alone, as Materialize derives it.
func (e *NormalizedStepExecutor) runWorkspace(runID int64) string {
	if e == nil || e.workspaceRoot == "" || runID <= 0 {
		return ""
	}
	return filepath.Join(e.workspaceRoot, "run-"+strconv.FormatInt(runID, 10))
}

// removeRunWorkspace removes a finished run's workspace, reporting whether
// there was one.
func (e *NormalizedStepExecutor) removeRunWorkspace(ctx context.Context, runID int64) (bool, error) {
	workspace := e.runWorkspace(runID)
	if workspace == "" {
		return false, nil
	}
	if held, err := e.workspaceHoldsARelease(ctx, workspace); err != nil || held {
		return false, err
	}
	info, err := os.Lstat(workspace)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		// Not what Materialize makes; left for a person to look at.
		return false, nil
	}
	if _, err := (MaterializedSource{Workspace: workspace}).Cleanup(); err != nil {
		return false, err
	}
	return true, nil
}

// SweepWorkspaces removes every workspace whose run has finished or is no
// longer recorded, and reports how many it removed.
func (e *NormalizedStepExecutor) SweepWorkspaces(ctx context.Context) (int, error) {
	if e == nil || e.store == nil || e.workspaceRoot == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(e.workspaceRoot)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	var failures []error
	for _, entry := range entries {
		if ctx.Err() != nil {
			return removed, ctx.Err()
		}
		match := runWorkspaceRE.FindStringSubmatch(entry.Name())
		if match == nil || !entry.IsDir() {
			continue
		}
		runID, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			continue
		}
		var state RunState
		err = e.store.db.QueryRowContext(ctx, `SELECT state FROM deploy_runs WHERE id = ?`, runID).Scan(&state)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// The run's record is gone with its project; nothing can run it.
		case err != nil:
			failures = append(failures, err)
			continue
		case !state.Terminal():
			continue
		}
		if gone, err := e.removeRunWorkspace(ctx, runID); err != nil {
			failures = append(failures, err)
		} else if gone {
			removed++
		}
	}
	return removed, errors.Join(failures...)
}

// workspaceHoldsARelease says whether a Compose release still runs from
// the workspace: a runtime record that is not removed names it, or a folder
// inside it, as its project's directory. The comparison is of the paths
// as Materialize and the runtime owner both derive them from the trusted
// root, never a pattern.
func (e *NormalizedStepExecutor) workspaceHoldsARelease(ctx context.Context, workspace string) (bool, error) {
	if e == nil || e.store == nil {
		return false, nil
	}
	var held int
	err := e.store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM deploy_release_runtimes
		 WHERE kind = 'compose' AND state <> 'removed'
		   AND (working_directory = ? OR substr(working_directory, 1, length(?) + 1) = ? || '/')`,
		workspace, workspace, workspace).Scan(&held)
	return held > 0, err
}
