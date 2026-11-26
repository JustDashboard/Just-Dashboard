package stacks_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks/stackstest"
)

func quickRunner() *stacks.Runner {
	runner := stacks.DefaultRunner()
	runner.Backoff = func(int) time.Duration { return time.Millisecond }
	return runner
}

type journal struct {
	mu      sync.Mutex
	last    map[string]stacks.StepRecord
	started []string
}

func (j *journal) record(record stacks.StepRecord) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.last == nil {
		j.last = map[string]stacks.StepRecord{}
	}
	if record.Status == stacks.StepRunning && record.Attempts == 1 {
		j.started = append(j.started, record.StepID)
	}
	j.last[record.StepID] = record
}

func step(id, service string, after []string, run func(context.Context, stacks.Env) (stacks.Result, error)) stacks.Step {
	return stacks.Step{ID: id, Service: service, Title: "Step " + id, Stage: stacks.StageInitialize,
		After: after, Timeout: time.Second, Attempts: 3, Run: run}
}

func ok(message string) func(context.Context, stacks.Env) (stacks.Result, error) {
	return func(context.Context, stacks.Env) (stacks.Result, error) {
		return stacks.Result{Message: message, Outputs: map[string]string{"said": message}}, nil
	}
}

func manifest(steps ...stacks.Step) *stacks.Manifest {
	return &stacks.Manifest{ID: "test", Version: "1", Title: "Test",
		Presets:  []stacks.Preset{{ID: "p", Template: "t", Services: []string{"a", "b"}}},
		Services: []stacks.ServiceInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}},
		Steps:    steps}
}

func fake(services ...string) *stackstest.Fake {
	env := &stackstest.Fake{Services: map[string]string{}}
	for _, service := range services {
		env.Services[service] = "http://" + service
	}
	return env
}

func TestRunnerOrdersStepsAndPassesOutputs(t *testing.T) {
	var seen string
	m := manifest(
		step("a.one", "a", nil, ok("first")),
		step("b.two", "b", []string{"a.one"}, func(_ context.Context, env stacks.Env) (stacks.Result, error) {
			seen = env.Output("a.one", "said")
			return stacks.Result{Message: "second"}, nil
		}),
	)
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	var j journal
	result := quickRunner().Run(context.Background(), m, fake("a", "b"), j.record)
	if result.Status != stacks.StatusReady || !result.Complete {
		t.Fatalf("result %+v", result)
	}
	if seen != "first" {
		t.Fatalf("b.two read %q from a.one", seen)
	}
	if strings.Join(j.started, ",") != "a.one,b.two" {
		t.Fatalf("started %v", j.started)
	}
}

func TestRunnerSkipsAbsentServicesWithoutBlockingAndBlocksAfterFailure(t *testing.T) {
	m := manifest(
		step("c.absent", "c", nil, ok("never")),
		step("a.after-absent", "a", []string{"c.absent"}, ok("runs")),
		step("a.fails", "a", nil, func(context.Context, stacks.Env) (stacks.Result, error) {
			return stacks.Result{}, errors.New("upstream said no")
		}),
		step("b.blocked", "b", []string{"a.fails"}, ok("never")),
	)
	var j journal
	result := quickRunner().Run(context.Background(), m, fake("a", "b"), j.record)
	if result.Status != stacks.StatusFailed || !result.Complete {
		t.Fatalf("result %+v", result)
	}
	want := map[string]stacks.StepStatus{"c.absent": stacks.StepSkipped, "a.after-absent": stacks.StepDone,
		"a.fails": stacks.StepFailed, "b.blocked": stacks.StepSkipped}
	for id, status := range want {
		if j.last[id].Status != status {
			t.Fatalf("%s is %s, want %s (%+v)", id, j.last[id].Status, status, j.last[id])
		}
	}
	if j.last["a.fails"].Attempts != 3 || j.last["a.fails"].Error != "upstream said no" {
		t.Fatalf("a.fails recorded %+v", j.last["a.fails"])
	}
	if !strings.Contains(j.last["b.blocked"].Message, "failed") {
		t.Fatalf("blocked step says %q", j.last["b.blocked"].Message)
	}
}

func TestRunnerRetriesThenSucceedsAndStopsOnPermanent(t *testing.T) {
	var flaky, permanent atomic.Int32
	m := manifest(
		step("a.flaky", "a", nil, func(context.Context, stacks.Env) (stacks.Result, error) {
			if flaky.Add(1) < 3 {
				return stacks.Result{}, errors.New("not yet")
			}
			return stacks.Result{Message: "up"}, nil
		}),
		step("b.permanent", "b", nil, func(context.Context, stacks.Env) (stacks.Result, error) {
			permanent.Add(1)
			return stacks.Result{}, stacks.Permanent(errors.New("version 2 is not supported"))
		}),
	)
	var j journal
	quickRunner().Run(context.Background(), m, fake("a", "b"), j.record)
	if j.last["a.flaky"].Status != stacks.StepDone || j.last["a.flaky"].Attempts != 3 {
		t.Fatalf("flaky %+v", j.last["a.flaky"])
	}
	if permanent.Load() != 1 || j.last["b.permanent"].Status != stacks.StepFailed {
		t.Fatalf("permanent ran %d times: %+v", permanent.Load(), j.last["b.permanent"])
	}
}

func TestRunnerRecordsActionsAndBlockingStatus(t *testing.T) {
	m := manifest(
		step("a.source", "a", nil, func(context.Context, stacks.Env) (stacks.Result, error) {
			return stacks.Result{}, stacks.NeedsAction(stacks.Action{ID: "media-source", Service: "a",
				Title: "Add a media source", Description: "Add one in Prowlarr.", Blocking: true})
		}),
		step("b.after", "b", []string{"a.source"}, ok("never")),
		step("b.independent", "b", nil, ok("runs")),
	)
	var j journal
	result := quickRunner().Run(context.Background(), m, fake("a", "b"), j.record)
	if result.Status != stacks.StatusAction || len(result.Actions) != 1 || result.Actions[0].ID != "media-source" {
		t.Fatalf("result %+v", result)
	}
	if j.last["b.after"].Status != stacks.StepSkipped || j.last["b.independent"].Status != stacks.StepDone {
		t.Fatalf("after %+v independent %+v", j.last["b.after"], j.last["b.independent"])
	}
}

func TestRunnerTurnsAPanicIntoAFailureAndRedactsSecrets(t *testing.T) {
	env := fake("a", "b")
	env.Vars = map[string]string{"RADARR_API_KEY": "0123456789abcdef"}
	m := manifest(
		step("a.panics", "a", nil, func(context.Context, stacks.Env) (stacks.Result, error) { panic("boom") }),
		step("b.echoes", "b", nil, func(ctx context.Context, env stacks.Env) (stacks.Result, error) {
			key, _ := env.Variable(ctx, "RADARR_API_KEY")
			return stacks.Result{}, stacks.Permanent(errors.New("401: key " + key + " refused"))
		}),
	)
	var j journal
	quickRunner().Run(context.Background(), m, env, j.record)
	if j.last["a.panics"].Status != stacks.StepFailed || !strings.Contains(j.last["a.panics"].Error, "crashed") {
		t.Fatalf("panic recorded %+v", j.last["a.panics"])
	}
	if strings.Contains(j.last["b.echoes"].Error, "0123456789abcdef") {
		t.Fatalf("secret recorded: %q", j.last["b.echoes"].Error)
	}
}

func TestRunnerCancelledPassIsIncompleteAndLeavesStepsPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := manifest(
		step("a.slow", "a", nil, func(ctx context.Context, _ stacks.Env) (stacks.Result, error) {
			cancel()
			<-ctx.Done()
			return stacks.Result{}, ctx.Err()
		}),
		step("b.after", "b", []string{"a.slow"}, ok("never")),
	)
	var j journal
	result := quickRunner().Run(ctx, m, fake("a", "b"), j.record)
	if result.Complete || result.Status != stacks.StatusFailed {
		t.Fatalf("result %+v", result)
	}
	if j.last["a.slow"].Status != stacks.StepPending || j.last["b.after"].Status != stacks.StepPending {
		t.Fatalf("slow %+v after %+v", j.last["a.slow"], j.last["b.after"])
	}
}
