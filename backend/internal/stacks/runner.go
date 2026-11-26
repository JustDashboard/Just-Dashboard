package stacks

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RunnerEnv is what the runner needs beyond Env: somewhere to keep each
// finished step's outputs for the steps after it, and a way to take every
// secret it handed out back out of what is recorded.
type RunnerEnv interface {
	Env
	SetOutputs(step string, outputs map[string]string)
	Redact(text string) string
}

// PassResult is what one pass over the graph came to.
type PassResult struct {
	Status Status
	// Complete: the pass reached the end of the graph. A pass cut short by
	// the dashboard stopping is not, and runs again.
	Complete bool
	Actions  []Action
}

// Runner runs a manifest's steps.
type Runner struct {
	// Parallel bounds the steps run at once.
	Parallel int
	// Backoff is the wait before attempt n+1 (n starting at 1).
	Backoff func(attempt int) time.Duration
	// Now is the clock records are stamped with.
	Now func() time.Time
}

// DefaultRunner waits 5s, 10s, 20s, 40s, then a minute between attempts.
func DefaultRunner() *Runner {
	return &Runner{
		Parallel: 3,
		Backoff: func(attempt int) time.Duration {
			wait := 5 * time.Second << max(0, attempt-1)
			return min(wait, time.Minute)
		},
		Now: time.Now,
	}
}

// skipReason says why a step did not run: absent is a step for a service
// the preset does not run, which other steps treat as satisfied; anything
// else blocks the steps after it.
const absentPrefix = AbsentMessage

// AbsentMessage is the message of a step skipped because its preset does
// not run its service: the page leaves such steps out.
const AbsentMessage = "Not part of this preset"

// Run takes every step of the manifest through one pass and records each
// change through record as it happens.
func (r *Runner) Run(ctx context.Context, manifest *Manifest, env RunnerEnv, record func(StepRecord)) PassResult {
	steps := map[string]Step{}
	status := map[string]StepStatus{}
	absent := map[string]bool{}
	records := map[string]*StepRecord{}
	var mu sync.Mutex
	save := func(id string, change func(*StepRecord)) {
		mu.Lock()
		current := records[id]
		change(current)
		current.Message = env.Redact(current.Message)
		current.Error = env.Redact(current.Error)
		copy := *current
		mu.Unlock()
		record(copy)
	}
	for _, step := range manifest.Steps {
		steps[step.ID] = step
		records[step.ID] = &StepRecord{StepID: step.ID, Status: StepPending}
		present := env.Has(step.Service)
		for _, service := range step.Needs {
			present = present && env.Has(service)
		}
		if !present {
			status[step.ID], absent[step.ID] = StepSkipped, true
			save(step.ID, func(current *StepRecord) {
				current.Status, current.Message = StepSkipped, absentPrefix
			})
			continue
		}
		status[step.ID] = StepPending
		save(step.ID, func(current *StepRecord) { current.Status = StepPending })
	}

	parallel := max(1, r.Parallel)
	type outcome struct {
		id     string
		status StepStatus
	}
	finished := make(chan outcome)
	running := 0
	interrupted := false
	for {
		// Settle what can be settled without running anything: a step
		// waiting on one that failed, needs the person, or was itself held
		// back will not run in this pass.
		for changed := true; changed; {
			changed = false
			for _, step := range manifest.Steps {
				if status[step.ID] != StepPending {
					continue
				}
				for _, after := range step.After {
					if blocking, why := blockedBy(status[after], absent[after]); blocking {
						status[step.ID] = StepSkipped
						reason := fmt.Sprintf("Waits for “%s”, which %s", steps[after].Title, why)
						save(step.ID, func(current *StepRecord) {
							current.Status, current.Message, current.Error = StepSkipped, reason, ""
						})
						changed = true
						break
					}
				}
			}
		}
		if ctx.Err() != nil {
			interrupted = true
		}
		if !interrupted {
			for _, step := range manifest.Steps {
				if running >= parallel {
					break
				}
				if status[step.ID] != StepPending || !ready(step, status, absent) {
					continue
				}
				status[step.ID] = StepRunning
				running++
				go func(step Step) {
					finished <- outcome{step.ID, r.attempt(ctx, step, env, save)}
				}(step)
			}
		}
		if running == 0 {
			break
		}
		done := <-finished
		status[done.id] = done.status
		running--
	}

	result := PassResult{Status: StatusReady, Complete: !interrupted}
	failed, needsPerson := false, false
	for _, step := range manifest.Steps {
		mu.Lock()
		current := *records[step.ID]
		mu.Unlock()
		switch current.Status {
		case StepFailed:
			failed = true
		case StepAction:
			if current.Action != nil {
				result.Actions = append(result.Actions, *current.Action)
				if current.Action.Blocking {
					needsPerson = true
				}
			}
		case StepPending, StepRunning:
			result.Complete = false
		}
	}
	switch {
	case failed || !result.Complete:
		result.Status = StatusFailed
	case needsPerson:
		result.Status = StatusAction
	}
	return result
}

func blockedBy(status StepStatus, absent bool) (bool, string) {
	switch {
	case status == StepFailed:
		return true, "failed"
	case status == StepAction:
		return true, "needs you first"
	case status == StepSkipped && !absent:
		return true, "did not run"
	}
	return false, ""
}

func ready(step Step, status map[string]StepStatus, absent map[string]bool) bool {
	for _, after := range step.After {
		if status[after] == StepDone || (status[after] == StepSkipped && absent[after]) {
			continue
		}
		return false
	}
	return true
}

// attempt runs one step to an outcome: done, failed after its attempts,
// action_required, or pending again when the pass was cancelled under it.
func (r *Runner) attempt(ctx context.Context, step Step, env RunnerEnv, save func(string, func(*StepRecord))) StepStatus {
	started := r.Now().UTC()
	lastError := ""
	for attempt := 1; attempt <= step.Attempts; attempt++ {
		save(step.ID, func(current *StepRecord) {
			current.Status, current.Attempts, current.StartedAt, current.FinishedAt = StepRunning, attempt, started, time.Time{}
			current.Error, current.Action = lastError, nil
		})
		result, err := runOnce(ctx, step, env)
		var action *ActionError
		switch {
		case err == nil:
			env.SetOutputs(step.ID, result.Outputs)
			save(step.ID, func(current *StepRecord) {
				current.Status, current.Message, current.Error = StepDone, result.Message, ""
				current.Outputs, current.FinishedAt = result.Outputs, r.Now().UTC()
			})
			return StepDone
		case errors.As(err, &action):
			save(step.ID, func(current *StepRecord) {
				current.Status, current.Message, current.Error = StepAction, action.Action.Description, ""
				current.Action, current.FinishedAt = &action.Action, r.Now().UTC()
			})
			return StepAction
		case ctx.Err() != nil:
			save(step.ID, func(current *StepRecord) {
				current.Status, current.Error = StepPending, "interrupted before it finished"
			})
			return StepPending
		}
		lastError = err.Error()
		if IsPermanent(err) || attempt == step.Attempts {
			break
		}
		save(step.ID, func(current *StepRecord) { current.Error = lastError })
		timer := time.NewTimer(r.Backoff(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			save(step.ID, func(current *StepRecord) {
				current.Status, current.Error = StepPending, "interrupted before it finished"
			})
			return StepPending
		case <-timer.C:
		}
	}
	save(step.ID, func(current *StepRecord) {
		current.Status, current.Error, current.FinishedAt = StepFailed, lastError, r.Now().UTC()
	})
	return StepFailed
}

// runOnce bounds one attempt and turns a panic in an adapter into a failure
// of that step rather than of the dashboard.
func runOnce(ctx context.Context, step Step, env RunnerEnv) (result Result, err error) {
	attemptCtx, cancel := context.WithTimeout(ctx, step.Timeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = Permanent(fmt.Errorf("the %s step crashed: %v", step.ID, recovered))
		}
	}()
	result, err = step.Run(attemptCtx, env)
	if err != nil && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		err = fmt.Errorf("%w (no answer within %s)", err, step.Timeout)
	}
	return result, err
}
