package deploy

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// historyRun is a run that printed lines and ended, as the history policy
// finds it: a step with its evidence, the variables and the plan it was
// bound to, its state events and a tail on its row.
func (f *orchestrationFixture) historyRun(t *testing.T, environmentID int64, lines int, end RunState) *EngineRun {
	t.Helper()
	ctx := context.Background()
	run := f.enqueue(t, environmentID, func(req *RunRequest) { req.Steps = []StepKey{StepResolveSource} })
	lease, err := f.runs.ClaimNext(ctx, "worker", QueueBudget{}, time.Minute)
	if err != nil || lease == nil || lease.RunID != run.ID {
		t.Fatalf("claim: %+v %v", lease, err)
	}
	step, err := f.runs.TransitionStep(ctx, run.ID, StepResolveSource, lease.Token, StepTransition{State: StepRunning})
	if err != nil {
		t.Fatal(err)
	}
	batch := make([]LogLine, 0, lines)
	for i := 1; i <= lines; i++ {
		batch = append(batch, LogLine{Stream: "stdout", Text: fmt.Sprintf("run %d line %d %s\n", run.ID, i, strings.Repeat("x", 60))})
	}
	if _, err := f.runs.AppendLogs(ctx, run.ID, step.ID, lease.Token, batch); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runs.TransitionStep(ctx, run.ID, StepResolveSource, lease.Token, StepTransition{State: StepPassed}); err != nil {
		t.Fatal(err)
	}
	path := []RunState{RunRunning, RunVerifying, RunActivating, RunSucceeded}
	if end == RunFailed {
		path = []RunState{RunRunning, RunFailed}
	}
	for _, state := range path {
		if _, err := f.runs.TransitionRun(ctx, run.ID, lease.Token, state, TransitionDetail{}); err != nil {
			t.Fatal(err)
		}
	}
	return run
}

// release records that a run built a release, in the state given.
func (f *orchestrationFixture) release(t *testing.T, environmentID, runID int64, number int, state string, pinned bool) int64 {
	t.Helper()
	result, err := f.store.DB.Exec(`
		INSERT INTO deploy_releases(project_id, environment_id, release_number, run_id, state, pinned, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, f.projectID, environmentID, number, runID, state, pinned, f.now.Unix())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	return id
}

func (f *orchestrationFixture) outputLines(t *testing.T, runID int64) int {
	t.Helper()
	var count int
	if err := f.store.DB.QueryRow(`
		SELECT COUNT(*) FROM deploy_log_chunks WHERE run_id = ? AND event_type = 'step.log' AND compacted = 0`,
		runID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// everythingButOutput counts what a rollback, a retry and the history page
// read of every run: none of it may change when output is cut.
func (f *orchestrationFixture) everythingButOutput(t *testing.T) string {
	t.Helper()
	counts := []int{}
	for _, query := range []string{
		`SELECT COUNT(*) FROM deploy_runs`,
		`SELECT COUNT(*) FROM deploy_steps`,
		`SELECT COALESCE(SUM(length(evidence_json)), 0) FROM deploy_steps`,
		`SELECT COUNT(*) FROM deploy_run_variable_snapshots`,
		`SELECT COUNT(*) FROM deploy_run_plan_snapshots`,
		`SELECT COUNT(*) FROM deploy_log_chunks WHERE event_type <> 'step.log'`,
		`SELECT COUNT(*) FROM deploy_releases`,
	} {
		var count int
		if err := f.store.DB.QueryRow(query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts = append(counts, count)
	}
	return fmt.Sprintf("runs, steps, evidence, variables, plans, events, releases = %v", counts)
}

// An environment's newest runs keep everything they printed; an older one
// keeps the end of it, in its transcript and on its row, and all the rest of
// its record. A second pass has nothing to do.
func TestHistoryKeepsTheNewestRunsWholeAndTheEndOfOlderOnes(t *testing.T) {
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	const printed = historyTailLines + 150
	runs := []*EngineRun{}
	for i := 0; i < 5; i++ {
		end := RunSucceeded
		if i%2 == 1 {
			end = RunFailed
		}
		runs = append(runs, f.historyRun(t, environmentID, printed, end))
	}
	before := f.everythingButOutput(t)

	pruned, next, err := f.runs.PruneRunHistory(ctx, HistoryPolicy{KeepRuns: 2}, 0, 0)
	if err != nil || pruned != 3 || next != 0 {
		t.Fatalf("pass = %d cut, goes on from %d, %v; want the three oldest of five and nothing left", pruned, next, err)
	}
	for index, run := range runs {
		want := printed
		if index < 3 {
			want = historyTailLines
		}
		if got := f.outputLines(t, run.ID); got != want {
			t.Errorf("run %d of 5 keeps %d lines of %d, want %d", index+1, got, printed, want)
		}
		var tail string
		if err := f.store.DB.QueryRow(`SELECT log FROM deploy_runs WHERE id = ?`, run.ID).Scan(&tail); err != nil {
			t.Fatal(err)
		}
		last := fmt.Sprintf("run %d line %d ", run.ID, printed)
		if !strings.Contains(tail, last) {
			t.Errorf("run %d of 5: the tail on its row lost its last line", index+1)
		}
		if length := utf8.RuneCountInString(tail); index < 3 && (length > historyTailChars+1 || !strings.HasPrefix(tail, "…")) {
			t.Errorf("run %d of 5: the tail on its row is %d characters, want at most %d behind a mark that it was cut", index+1, length, historyTailChars)
		} else if index >= 3 && length < historyTailChars*2 {
			t.Errorf("run %d of 5 is one the policy keeps whole, and its tail was cut to %d characters", index+1, length)
		}
	}
	// What an old run's page reads: the end of the output, and that there
	// was more before it.
	lines, earlier, err := f.runs.RunTranscriptTail(ctx, runs[0].ID, 500)
	if err != nil || !earlier || len(lines) != historyTailLines ||
		!strings.HasPrefix(lines[len(lines)-1].Text, fmt.Sprintf("run %d line %d ", runs[0].ID, printed)) {
		t.Fatalf("an old run's transcript = %d lines, earlier=%v, %v; want its last %d and a mark that more came before",
			len(lines), earlier, err, historyTailLines)
	}
	if after := f.everythingButOutput(t); after != before {
		t.Fatalf("cutting output changed the rest of the record:\nbefore %s\nafter  %s", before, after)
	}
	for _, run := range runs {
		steps, err := f.runs.Steps(ctx, run.ID)
		if err != nil || len(steps) != 1 || steps[0].State != StepPassed {
			t.Fatalf("run %d: steps = %+v %v", run.ID, steps, err)
		}
		if _, err := f.runs.Snapshot(ctx, run.ID); err != nil {
			t.Fatalf("run %d: snapshot: %v", run.ID, err)
		}
	}

	if pruned, next, err := f.runs.PruneRunHistory(ctx, HistoryPolicy{KeepRuns: 2}, 0, 0); err != nil || pruned != 0 || next != 0 {
		t.Fatalf("a second pass = %d cut, goes on from %d, %v; want nothing left to do", pruned, next, err)
	}
}

// The limit is per environment: a quiet environment's few runs are not
// counted against a busy one's many.
func TestHistoryCountsEachEnvironmentsRunsApart(t *testing.T) {
	f := newOrchestrationFixture(t)
	busy := f.addEnvironment(t, "production", EnvironmentProduction)
	quiet := f.addEnvironment(t, "staging", EnvironmentStaging)
	old := f.historyRun(t, quiet, historyTailLines+20, RunSucceeded)
	for i := 0; i < 4; i++ {
		f.historyRun(t, busy, historyTailLines+20, RunSucceeded)
	}
	pruned, _, err := f.runs.PruneRunHistory(context.Background(), HistoryPolicy{KeepRuns: 2}, 0, 0)
	if err != nil || pruned != 2 {
		t.Fatalf("pass = %d cut, %v; want the busy environment's two oldest", pruned, err)
	}
	if got := f.outputLines(t, old.ID); got != historyTailLines+20 {
		t.Fatalf("the quiet environment's only run keeps %d lines: it was counted with another environment's runs", got)
	}
}

// What a rollback reaches keeps its whole output whatever its age: the run
// behind the live release, behind a pinned one and behind the six newest
// releases that went live. A run that has not ended is not touched either.
func TestHistoryLeavesWhatARollbackReachesAndWhatHasNotEnded(t *testing.T) {
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	const printed = historyTailLines + 20
	// Oldest first: a pinned release, a release long retired, eight that went
	// live (the newest of them live now), then failures on top of them.
	pinned := f.historyRun(t, environmentID, printed, RunSucceeded)
	f.release(t, environmentID, pinned.ID, 1, "retired", true)
	retired := f.historyRun(t, environmentID, printed, RunSucceeded)
	f.release(t, environmentID, retired.ID, 2, "retired", false)
	wentLive := []*EngineRun{}
	var liveRelease int64
	for number := 3; number <= 10; number++ {
		run := f.historyRun(t, environmentID, printed, RunSucceeded)
		state := "retained"
		if number == 10 {
			state = "live"
		}
		liveRelease = f.release(t, environmentID, run.ID, number, state, false)
		wentLive = append(wentLive, run)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_environments SET live_release_id = ? WHERE id = ?`, liveRelease, environmentID); err != nil {
		t.Fatal(err)
	}
	failures := []*EngineRun{}
	for i := 0; i < 3; i++ {
		failures = append(failures, f.historyRun(t, environmentID, printed, RunFailed))
	}
	open := f.enqueue(t, environmentID)

	// Keep one run whole: everything but the queued run is past the limit.
	pruned, next, err := f.runs.PruneRunHistory(ctx, HistoryPolicy{KeepRuns: 1}, 0, 0)
	if err != nil || next != 0 {
		t.Fatalf("pass: goes on from %d, %v", next, err)
	}
	whole := map[int64]string{pinned.ID: "the run behind a pinned release", open.ID: "a run that has not ended"}
	for _, run := range wentLive[2:] {
		whole[run.ID] = "a run behind one of the six newest releases that went live"
	}
	cut := map[int64]string{retired.ID: "the run behind a retired release"}
	for _, run := range wentLive[:2] {
		cut[run.ID] = "a run behind a release older than the six a rollback reaches"
	}
	for _, run := range failures {
		cut[run.ID] = "a failed run past the limit"
	}
	for id, what := range whole {
		if id == open.ID {
			continue
		}
		if got := f.outputLines(t, id); got != printed {
			t.Errorf("%s keeps %d lines of %d: its output was cut", what, got, printed)
		}
	}
	for id, what := range cut {
		if got := f.outputLines(t, id); got != historyTailLines {
			t.Errorf("%s keeps %d lines, want %d", what, got, historyTailLines)
		}
	}
	if pruned != len(cut) {
		t.Errorf("pass cut %d runs, want %d", pruned, len(cut))
	}
	if run, err := f.runs.Run(ctx, open.ID); err != nil || run.State.Terminal() {
		t.Fatalf("the queued run = %+v %v", run, err)
	}
	var marked int
	if err := f.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_run_output_pruned WHERE run_id = ?`, open.ID).Scan(&marked); err != nil || marked != 0 {
		t.Fatalf("a run that has not ended was recorded as cut (%d %v): it would keep everything it goes on to print for ever", marked, err)
	}
}

// 0 is the operator saying keep everything, and a pass takes a batch at a
// time and says when runs are still waiting.
func TestHistoryIsOffAtZeroAndWorksThroughABacklogInBatches(t *testing.T) {
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	runs := []*EngineRun{}
	for i := 0; i < 4; i++ {
		runs = append(runs, f.historyRun(t, environmentID, historyTailLines+5, RunSucceeded))
	}
	if pruned, next, err := f.runs.PruneRunHistory(ctx, HistoryPolicy{}, 0, 0); err != nil || pruned != 0 || next != 0 {
		t.Fatalf("no limit = %d cut, goes on from %d, %v", pruned, next, err)
	}
	for _, run := range runs {
		if got := f.outputLines(t, run.ID); got != historyTailLines+5 {
			t.Fatalf("with no limit a run keeps %d lines of %d", got, historyTailLines+5)
		}
	}
	pruned, next, err := f.runs.PruneRunHistory(ctx, HistoryPolicy{KeepRuns: 1}, 0, 2)
	if err != nil || pruned != 2 || next != runs[1].ID {
		t.Fatalf("first batch = %d cut, goes on from %d, %v; want 2 and to go on from the second run, %d", pruned, next, err, runs[1].ID)
	}
	if got := f.outputLines(t, runs[0].ID); got != historyTailLines {
		t.Fatalf("the oldest run is cut first; it keeps %d lines", got)
	}
	if pruned, next, err := f.runs.PruneRunHistory(ctx, HistoryPolicy{KeepRuns: 1}, next, 2); err != nil || pruned != 1 || next != 0 {
		t.Fatalf("second batch = %d cut, goes on from %d, %v; want the last one and the end", pruned, next, err)
	}
}

// Runs a pass cannot cut (they have not ended) do not hold it back however
// many of them are oldest: each call goes on after the last run it looked at.
func TestHistoryPassesGoOnPastRunsTheyCannotCut(t *testing.T) {
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	stuck := f.addEnvironment(t, "staging", EnvironmentStaging)
	for i := 0; i < 5; i++ {
		f.enqueue(t, stuck)
	}
	// Left running by nothing this test starts, so they never end.
	if _, err := f.store.DB.Exec(`UPDATE deploy_runs SET state = ? WHERE environment_id = ?`, RunRunning, stuck); err != nil {
		t.Fatal(err)
	}
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	old := f.historyRun(t, environmentID, historyTailLines+5, RunSucceeded)
	f.historyRun(t, environmentID, historyTailLines+5, RunSucceeded)
	// The running runs are older than the one to cut, and a batch of one
	// lists four runs: all of them still running.
	total, after := 0, int64(0)
	for pass := 0; pass < 10; pass++ {
		pruned, next, err := f.runs.PruneRunHistory(ctx, HistoryPolicy{KeepRuns: 1}, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		total += pruned
		if after = next; after == 0 {
			break
		}
	}
	if total != 1 || f.outputLines(t, old.ID) != historyTailLines {
		t.Fatalf("%d runs cut and the old run keeps %d lines: the runs ahead of it that have not ended held every pass back", total, f.outputLines(t, old.ID))
	}
}

// A pass runs every hour over every run there has ever been. It finds its
// runs from the index on (environment_id, id) and the table of cut runs; a
// plan that reads run rows reads every log tail in the store on the way.
func TestTheHistoryPassFindsItsRunsWithoutReadingRunRows(t *testing.T) {
	f := newOrchestrationFixture(t)
	rows, err := f.store.DB.Query(`EXPLAIN QUERY PLAN `+historyCandidatesQuery, 200, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	whole := strings.Join(plan, "\n")
	if !strings.Contains(whole, "SCAN r USING COVERING INDEX idx_deploy_runs_environment") {
		t.Fatalf("the pass does not read its runs from the index alone:\n%s", whole)
	}
	for _, step := range plan {
		if (strings.HasPrefix(step+" ", "SCAN r ") || strings.HasPrefix(step+" ", "SEARCH r ")) && !strings.Contains(step, "COVERING INDEX") {
			t.Fatalf("the pass reads run rows (%q):\n%s", step, whole)
		}
	}
}

type printingExecutor struct {
	lines int
	end   StepState
}

func (e printingExecutor) Execute(_ context.Context, execution StepExecution) StepResult {
	for i := 1; i <= e.lines; i++ {
		if err := execution.Output.Log("stdout", fmt.Sprintf("line %d\n", i)); err != nil {
			return StepResult{State: StepFailed, ErrorCode: "output", ErrorMessage: err.Error()}
		}
	}
	return StepResult{State: e.end, ErrorCode: "build_failed", ErrorMessage: "the build failed"}
}

// A run that fails is held to the hot limit as one that succeeds is. Only
// success used to be, and a build that fails in a loop prints the most.
func TestAFailedRunIsHeldToTheHotLimit(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) { req.Steps = []StepKey{StepResolveSource} })
	engine := NewEngine(f.runs, printingExecutor{lines: 40, end: StepFailed}, fixedReconciler{}, EngineConfig{
		WorkerID: "engine-hot-limit", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute, HotLogLimit: 10,
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForRunState(t, f.runs, run.ID, RunFailed)
	shutdownEngine(t, engine)
	if got := f.outputLines(t, run.ID); got != 10 {
		t.Fatalf("a failed run that printed 40 lines keeps %d, want the hot limit of 10", got)
	}
	lines, earlier, err := f.runs.RunTranscriptTail(context.Background(), run.ID, 100)
	if err != nil || !earlier || len(lines) != 10 || lines[9].Text != "line 40\n" {
		t.Fatalf("its transcript = %d lines, earlier=%v, %v; want the last ten", len(lines), earlier, err)
	}
}

// The engine applies the policy by itself while it runs, and an engine
// given no limit never starts.
func TestTheEngineAppliesTheHistoryPolicyWhileItRuns(t *testing.T) {
	f := newOrchestrationFixture(t)
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	old := f.historyRun(t, environmentID, historyTailLines+30, RunSucceeded)
	newest := f.historyRun(t, environmentID, historyTailLines+30, RunSucceeded)
	start := func(policy HistoryPolicy) *Engine {
		engine := NewEngine(f.runs, &recordingExecutor{}, fixedReconciler{}, EngineConfig{
			WorkerID: "engine-history", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute,
			History: policy, historyFirst: 5 * time.Millisecond, historyEvery: 5 * time.Millisecond,
		}, nil)
		if err := engine.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		return engine
	}
	engine := start(HistoryPolicy{})
	time.Sleep(100 * time.Millisecond)
	shutdownEngine(t, engine)
	if got := f.outputLines(t, old.ID); got != historyTailLines+30 {
		t.Fatalf("an engine with no limit cut a run to %d lines", got)
	}

	engine = start(HistoryPolicy{KeepRuns: 1})
	deadline := time.Now().Add(5 * time.Second)
	for f.outputLines(t, old.ID) != historyTailLines && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	shutdownEngine(t, engine)
	if got := f.outputLines(t, old.ID); got != historyTailLines {
		t.Fatalf("the engine left a run past the limit with %d lines, want %d", got, historyTailLines)
	}
	if got := f.outputLines(t, newest.ID); got != historyTailLines+30 {
		t.Fatalf("the engine cut the newest run to %d lines", got)
	}
}
