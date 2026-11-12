package deploy

import (
	"context"
	"errors"
	"time"
)

// HistoryPolicy is how much of what finished runs printed is kept. A run's
// output is what a deployment's history weighs: up to ten thousand lines a
// run and a tail on its row, where everything else a run leaves is a few
// short rows. Git watching and preview environments start runs with nobody
// pressing anything, so without a limit the store grows for as long as the
// server lives.
//
// The policy cuts output and nothing else. A run's row, its steps and their
// evidence, its diagnosis, the variables and the plan it was bound to, its
// release, the release's artifacts and every state it passed through stay,
// whatever its age: a rollback, a retry, a promotion and the release
// comparison read those, and none of them reads a line of output.
type HistoryPolicy struct {
	// KeepRuns is how many of an environment's newest runs keep everything
	// they printed. An older one keeps the end of it. 0 keeps every run
	// whole. The server sets it from JD_DEPLOY_HISTORY_RUNS, 200 by default.
	KeepRuns int
}

const (
	// historyTailLines is what a run past the policy keeps of its output:
	// the end, which is where a failure says what it was, and as much of it
	// as a diagnosis asked for later would be built from.
	historyTailLines = DiagnosisTranscriptLines
	// historyTailChars is the same for the bounded tail on the run's row,
	// which the 0.6.6 run shape answers with.
	historyTailChars = 4000
	// historyBatchRuns is how many runs one pass cuts before it lets the
	// store's other writers in; a backlog takes several passes.
	historyBatchRuns = 25
)

// historyCandidatesQuery lists the runs past an environment's newest KeepRuns
// that no pass has cut yet, oldest first, from the run a pass got to and no
// more than asked for. It reads the index on (environment_id, id) and the
// table of cut runs, never a run's row: a row drags its log tail behind it,
// and a pass over every run would read them all.
// TestTheHistoryPassFindsItsRunsWithoutReadingRunRows holds the plan.
const historyCandidatesQuery = `
	SELECT ranked.id FROM (
	  SELECT r.id, ROW_NUMBER() OVER (PARTITION BY r.environment_id ORDER BY r.id DESC) AS newest
	    FROM deploy_runs r
	) ranked
	 WHERE ranked.newest > ? AND ranked.id > ?
	   AND NOT EXISTS (SELECT 1 FROM deploy_run_output_pruned p WHERE p.run_id = ranked.id)
	 ORDER BY ranked.id LIMIT ?`

// historyProtectedQuery lists the runs whose output stays whole whatever
// their age: the one that built what an environment serves now, the ones
// behind a pinned release, and the ones behind the releases artifact
// retention keeps for a rollback (the six newest that went live, the rule
// ArtifactRetentionPlan applies). Whoever rolls back to a release can still
// read how it was built.
const historyProtectedQuery = `
	SELECT run_id FROM (
	  SELECT rel.id, rel.run_id, rel.pinned, e.live_release_id,
	         rel.state IN ('live', 'retained') AS went_live,
	         ROW_NUMBER() OVER (
	           PARTITION BY rel.environment_id, rel.state IN ('live', 'retained')
	           ORDER BY rel.release_number DESC) AS newest
	    FROM deploy_releases rel
	    JOIN deploy_environments e ON e.id = rel.environment_id
	   WHERE rel.run_id <> 0
	)
	 WHERE pinned = 1 OR id = live_release_id OR (went_live AND newest <= 6)`

// PruneRunHistory cuts the output of at most limit runs that the policy no
// longer keeps whole, oldest first among those after the run given, and
// reports how many it cut and the run it got to, for the next call to go on
// from: 0 when it reached the end. A run that has not ended is passed over,
// as is one the policy protects; neither counts, and neither can hold a pass
// back, since the next one starts after them.
func (s *OrchestrationStore) PruneRunHistory(ctx context.Context, policy HistoryPolicy, after int64, limit int) (int, int64, error) {
	if policy.KeepRuns <= 0 {
		return 0, 0, nil
	}
	if limit <= 0 {
		limit = historyBatchRuns
	}
	protected, err := s.historyProtectedRuns(ctx)
	if err != nil {
		return 0, 0, err
	}
	listed := 4 * limit
	rows, err := s.reader().QueryContext(ctx, historyCandidatesQuery, policy.KeepRuns, after, listed)
	if err != nil {
		return 0, 0, err
	}
	candidates := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		candidates = append(candidates, id)
	}
	// Closed before the first write: the cut below is a transaction of its
	// own and must not wait on a cursor this pass still holds.
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, 0, err
	}
	pruned := 0
	for index, id := range candidates {
		if protected[id] {
			continue
		}
		cut, err := s.pruneRunOutput(ctx, id)
		if err != nil {
			return pruned, 0, err
		}
		if cut {
			pruned++
		}
		if pruned == limit && index < len(candidates)-1 {
			return pruned, id, nil
		}
	}
	if len(candidates) == listed {
		return pruned, candidates[len(candidates)-1], nil
	}
	return pruned, 0, nil
}

func (s *OrchestrationStore) historyProtectedRuns(ctx context.Context) (map[int64]bool, error) {
	rows, err := s.reader().QueryContext(ctx, historyProtectedQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	protected := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		protected[id] = true
	}
	return protected, rows.Err()
}

// pruneRunOutput cuts one ended run's output to its end and records that it
// did, in one transaction. It goes through the store's bulk writer: a power
// cut that takes the cut takes its record with it, and the next pass cuts
// the run again.
func (s *OrchestrationStore) pruneRunOutput(ctx context.Context, runID int64) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.st.Bulk().BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := compactRunLogsTx(ctx, tx, runID, historyTailLines); err != nil {
		// Still running, or deleted with its project since the pass listed it.
		if errors.Is(err, ErrRunStillOpen) || errors.Is(err, ErrRunNotFound) {
			return false, nil
		}
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE deploy_runs SET log = '…' || substr(log, ?)
		 WHERE id = ? AND length(log) > ?`, -historyTailChars, runID, historyTailChars+1); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO deploy_run_output_pruned(run_id, pruned_at) VALUES(?, ?)
		ON CONFLICT(run_id) DO NOTHING`, runID, s.now().UTC().Unix()); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// keepHistory applies the policy for as long as the engine runs: a first
// pass a minute after it starts, so a restart is not spent on it, then one
// an hour. A pass cuts a batch and the next goes on from where it stopped a
// moment later, which is how an install that has never had a limit catches
// up without holding the store's other writers back.
func (e *Engine) keepHistory(ctx context.Context) {
	timer := time.NewTimer(e.config.historyFirst)
	defer timer.Stop()
	var after int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		pruned, next, err := e.store.PruneRunHistory(ctx, e.config.History, after, historyBatchRuns)
		if err != nil && ctx.Err() == nil && e.log != nil {
			e.log.Error("deployment history could not be cut", "err", err)
		}
		if pruned > 0 && e.log != nil {
			e.log.Info("deployment history cut to the end of each run's output",
				"runs", pruned, "kept whole per environment", e.config.History.KeepRuns)
		}
		after = next
		if next != 0 {
			timer.Reset(e.config.historyBacklogPause)
		} else {
			timer.Reset(e.config.historyEvery)
		}
	}
}
