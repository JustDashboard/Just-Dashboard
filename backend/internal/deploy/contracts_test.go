package deploy

import (
	"errors"
	"testing"
)

func TestRunTransitionTableExhaustivelyAllowsOnlyFrozenEdges(t *testing.T) {
	want := map[[2]RunState]bool{}
	allow := func(from RunState, to ...RunState) {
		for _, next := range to {
			want[[2]RunState{from, next}] = true
		}
	}
	allow(RunRequested, RunValidating, RunCancelling, RunFailed)
	allow(RunValidating, RunQueued, RunCancelling, RunFailed)
	allow(RunQueued, RunPreparing, RunCancelling, RunSuperseded, RunFailed)
	allow(RunPreparing, RunRunning, RunCancelling, RunFailed)
	allow(RunRunning, RunVerifying, RunCancelling, RunFailed)
	allow(RunVerifying, RunActivating, RunCancelling, RunFailed)
	allow(RunActivating, RunSucceeded, RunFailedActivation)
	allow(RunFailedActivation, RunRestoringPrevious, RunFailed)
	allow(RunRestoringPrevious, RunRolledBack, RunFailed)
	allow(RunCancelling, RunCancelled, RunFailed)

	for _, from := range allRunStates {
		for _, to := range allRunStates {
			got := CanTransitionRun(from, to)
			if got != want[[2]RunState{from, to}] {
				t.Errorf("CanTransitionRun(%q, %q) = %v, want %v", from, to, got, !got)
			}
			err := ValidateRunTransition(from, to)
			if got && err != nil {
				t.Errorf("valid transition %s -> %s returned %v", from, to, err)
			}
			if !got && !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("invalid transition %s -> %s returned %v", from, to, err)
			}
		}
	}
}

func TestTerminalRunStatesHaveNoOutgoingEdge(t *testing.T) {
	for _, state := range allRunStates {
		wantTerminal := state == RunSucceeded || state == RunFailed || state == RunCancelled ||
			state == RunRolledBack || state == RunSuperseded
		if state.Terminal() != wantTerminal {
			t.Errorf("%s Terminal() = %v, want %v", state, state.Terminal(), wantTerminal)
		}
		if !wantTerminal {
			continue
		}
		for _, next := range allRunStates {
			if CanTransitionRun(state, next) {
				t.Errorf("terminal state %s can transition to %s", state, next)
			}
		}
	}
}

func TestStepTransitionTableExhaustivelyAllowsOnlyFrozenEdges(t *testing.T) {
	want := map[[2]StepState]bool{}
	allow := func(from StepState, to ...StepState) {
		for _, next := range to {
			want[[2]StepState{from, next}] = true
		}
	}
	allow(StepPending, StepBlocked, StepRunning, StepSkipped, StepCancelled, StepUnavailable)
	allow(StepBlocked, StepPending, StepRunning, StepFailed, StepCancelled, StepUnavailable)
	allow(StepRunning, StepPassed, StepWarning, StepFailed, StepSkipped, StepCancelled, StepUnavailable)
	allow(StepFailed, StepPending)

	for _, from := range allStepStates {
		for _, to := range allStepStates {
			got := CanTransitionStep(from, to)
			if got != want[[2]StepState{from, to}] {
				t.Errorf("CanTransitionStep(%q, %q) = %v, want %v", from, to, got, !got)
			}
		}
	}
}

func TestDefaultReleasePathHasEveryFrozenStepOnce(t *testing.T) {
	if len(DefaultStepKeys) != 16 {
		t.Fatalf("default release path has %d steps, want 16", len(DefaultStepKeys))
	}
	seen := map[StepKey]bool{}
	for _, step := range DefaultStepKeys {
		if seen[step] {
			t.Errorf("default release path repeats %q", step)
		}
		seen[step] = true
	}
}
