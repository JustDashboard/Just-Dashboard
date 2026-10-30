package deploy

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A transcript's tail is its last lines in the order they were printed, says
// whether earlier ones exist — still kept, or compacted away — and finds a run
// by the number its page shows.
func TestRunTranscriptTailKeepsTheEndInOrder(t *testing.T) {
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) { req.Steps = []StepKey{StepResolveSource} })
	lease, err := f.runs.ClaimNext(ctx, "worker", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	step, err := f.runs.TransitionStep(ctx, run.ID, StepResolveSource, lease.Token, StepTransition{State: StepRunning})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := f.runs.AppendLog(ctx, run.ID, step.ID, lease.Token, "stdout", fmt.Sprintf("line-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	lines, earlier, err := f.runs.RunTranscriptTail(ctx, run.ID, 3)
	if err != nil || !earlier || len(lines) != 3 || lines[0].Text != "line-2" || lines[2].Text != "line-4" || lines[0].StepID != step.ID {
		t.Fatalf("tail of 3 = %+v earlier=%v %v", lines, earlier, err)
	}
	if lines, earlier, _ := f.runs.RunTranscriptTail(ctx, run.ID, 10); earlier || len(lines) != 5 {
		t.Fatalf("whole transcript = %d lines, earlier=%v", len(lines), earlier)
	}

	if _, err := f.runs.TransitionStep(ctx, run.ID, StepResolveSource, lease.Token, StepTransition{State: StepPassed}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []RunState{RunRunning, RunVerifying, RunActivating, RunSucceeded} {
		if _, err := f.runs.TransitionRun(ctx, run.ID, lease.Token, state, TransitionDetail{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.runs.CompactRunLogs(ctx, run.ID, 2); err != nil {
		t.Fatal(err)
	}
	if lines, earlier, _ := f.runs.RunTranscriptTail(ctx, run.ID, 10); !earlier || len(lines) != 2 || lines[1].Text != "line-4" {
		t.Fatalf("after compaction = %+v earlier=%v", lines, earlier)
	}

	found, err := f.runs.ProjectRunByNumber(ctx, f.projectID, run.RunNumber)
	if err != nil || found.ID != run.ID {
		t.Fatalf("run #%d = %+v %v", run.RunNumber, found, err)
	}
	if _, err := f.runs.ProjectRunByNumber(ctx, f.projectID, run.RunNumber+1); err != ErrRunNotFound {
		t.Fatalf("a run that is not there: %v", err)
	}
}

// A run from before sequenced chunks has only the bounded tail on its row,
// which is read as lines.
func TestRunTranscriptTailReadsAPreEngineRunsLog(t *testing.T) {
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID)
	if _, err := f.store.DB.Exec(`DELETE FROM deploy_log_chunks WHERE run_id = ?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_runs SET log = ? WHERE id = ?`, "git pull\ndocker compose up\ndone\n", run.ID); err != nil {
		t.Fatal(err)
	}
	lines, earlier, err := f.runs.RunTranscriptTail(ctx, run.ID, 2)
	if err != nil || !earlier || len(lines) != 2 || lines[0].Text != "docker compose up" || lines[1].Text != "done" {
		t.Fatalf("legacy tail = %+v earlier=%v %v", lines, earlier, err)
	}
	if _, _, err := f.runs.RunTranscriptTail(ctx, run.ID+99, 2); err != ErrRunNotFound {
		t.Fatalf("a run that is not there: %v", err)
	}
}

// Following a run is reading after the last line read: oldest first, a page at
// a time, saying whether more were left, and nothing but log lines — a step's
// state changes are in the run, not the transcript.
func TestRunTranscriptAfterPagesForward(t *testing.T) {
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	environmentID := f.addEnvironment(t, "production", EnvironmentProduction)
	run := f.enqueue(t, environmentID, func(req *RunRequest) { req.Steps = []StepKey{StepResolveSource} })
	lease, err := f.runs.ClaimNext(ctx, "worker", QueueBudget{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	step, err := f.runs.TransitionStep(ctx, run.ID, StepResolveSource, lease.Token, StepTransition{State: StepRunning})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := f.runs.AppendLog(ctx, run.ID, step.ID, lease.Token, "stdout", fmt.Sprintf("line-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	first, more, err := f.runs.RunTranscriptAfter(ctx, run.ID, 0, 2)
	if err != nil || !more || len(first) != 2 || first[0].Text != "line-0" || first[1].Text != "line-1" || first[0].StepID != step.ID {
		t.Fatalf("first page = %+v more=%v %v", first, more, err)
	}
	rest, more, err := f.runs.RunTranscriptAfter(ctx, run.ID, first[1].Seq, 10)
	if err != nil || more || len(rest) != 3 || rest[0].Text != "line-2" || rest[2].Text != "line-4" {
		t.Fatalf("rest = %+v more=%v %v", rest, more, err)
	}
	if none, more, err := f.runs.RunTranscriptAfter(ctx, run.ID, rest[2].Seq, 10); err != nil || more || len(none) != 0 {
		t.Fatalf("after the last line = %+v more=%v %v", none, more, err)
	}
	if _, _, err := f.runs.RunTranscriptAfter(ctx, run.ID+999, 0, 10); err != ErrRunNotFound {
		t.Fatalf("a run that is not there: %v", err)
	}
}
