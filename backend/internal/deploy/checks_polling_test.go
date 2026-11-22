package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// pollingClock is a fake clock the runner reads and sleeps on: a sleep moves
// it on at once, and an attempt moves it on by however long the fake
// application takes to answer.
type pollingClock struct {
	mu    sync.Mutex
	now   time.Time
	start time.Time
	waits []time.Duration
}

func newPollingClock() *pollingClock {
	start := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	return &pollingClock{now: start, start: start}
}

func (c *pollingClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *pollingClock) advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(by)
}

func (c *pollingClock) elapsed() time.Duration { return c.read().Sub(c.start) }

func (c *pollingClock) sleep(ctx context.Context, wait time.Duration) {
	if ctx.Err() != nil {
		return
	}
	c.mu.Lock()
	c.waits = append(c.waits, wait)
	c.mu.Unlock()
	c.advance(wait)
}

// pollingRunner asks a fake TCP application that starts listening `upAt`
// into the check and takes `answer` to answer each attempt.
func pollingRunner(clock *pollingClock, backend ContainerCheckBackend, upAt, answer time.Duration) *CheckRunner {
	runner := NewCheckRunner(backend)
	runner.now, runner.sleep = clock.read, clock.sleep
	runner.dial = func(context.Context, string, string) (net.Conn, error) {
		clock.advance(answer)
		if upAt >= 0 && clock.elapsed() >= upAt {
			server, client := net.Pipe()
			server.Close()
			return client, nil
		}
		return nil, errors.New("dial tcp: connect: connection refused")
	}
	return runner
}

func pollingCheck(config string) PlannedCheck {
	return PlannedCheck{Name: "TCP readiness", Kind: "tcp", Phase: "readiness", Required: true, Config: json.RawMessage(config)}
}

var pollingTarget = CheckTarget{Host: "127.0.0.1", Port: 3000}

// Run 18's application came up somewhere between 3 s and 6 s and was found
// on the 6 s tick. Asked every quarter second at first, it is found within
// a quarter second of answering.
func TestReadinessFindsAnApplicationSoonAfterItAnswers(t *testing.T) {
	t.Parallel()
	clock := newPollingClock()
	runner := pollingRunner(clock, nil, 3200*time.Millisecond, 0)
	evidence := runner.Run(context.Background(), pollingCheck(`{"attempts":20,"intervalSeconds":3,"timeoutSeconds":3}`), pollingTarget)
	if evidence.Outcome != HealthPassed {
		t.Fatalf("outcome = %s, want passed: %+v", evidence.Outcome, evidence.Attempts)
	}
	if found := evidence.CompletedAt.Sub(evidence.StartedAt); found < 3200*time.Millisecond || found > 3450*time.Millisecond {
		t.Fatalf("found the application %s into the check, want within 250 ms of 3.2 s", found)
	}
	// Thirteen refusals and the answer, kept as the first refusal with its
	// twelve repeats and the answer.
	if len(evidence.Attempts) != 2 || evidence.AttemptCount() != 14 {
		t.Fatalf("attempts kept = %d, asked = %d: %+v", len(evidence.Attempts), evidence.AttemptCount(), evidence.Attempts)
	}
	first, last := evidence.Attempts[0], evidence.Attempts[1]
	if first.Code != "connection_failed" || first.Number != 1 || first.Repeats != 12 || first.LastNumber != 13 ||
		first.SpanMillis != 3000 || last.Number != 14 || last.Outcome != HealthPassed || last.Repeats != 0 {
		t.Fatalf("collapsed evidence = %+v", evidence.Attempts)
	}
}

// A check that never passes waits out the same budget as before — nineteen
// 3 s waits — and no longer, however many more times it asked.
func TestReadinessKeepsItsBudgetWhenItAsksQuickly(t *testing.T) {
	t.Parallel()
	clock := newPollingClock()
	runner := pollingRunner(clock, nil, -1, 0)
	evidence := runner.Run(context.Background(), pollingCheck(`{"attempts":20,"intervalSeconds":3,"timeoutSeconds":3}`), pollingTarget)
	if evidence.Outcome != HealthFailed {
		t.Fatalf("outcome = %s", evidence.Outcome)
	}
	if took := evidence.CompletedAt.Sub(evidence.StartedAt); took != 57*time.Second {
		t.Fatalf("a failing check took %s, want the 57 s it always had", took)
	}
	for index, wait := range clock.waits {
		at := time.Duration(0)
		for _, before := range clock.waits[:index] {
			at += before
		}
		if at < earlyPollingWindow && wait != earlyPollingInterval {
			t.Fatalf("wait %d at %s = %s, want %s inside the early window", index, at, wait, earlyPollingInterval)
		}
		if at >= earlyPollingWindow && wait > 3*time.Second {
			t.Fatalf("wait %d at %s = %s, longer than the interval", index, at, wait)
		}
	}
	if len(evidence.Attempts) != 2 || evidence.Attempts[0].Repeats != evidence.AttemptCount()-2 {
		t.Fatalf("identical refusals were not folded: %+v", evidence.Attempts)
	}
	message := checkFailureMessage("readiness", evidence.Outcome, evidence)
	if !strings.Contains(message, "could not connect") || !strings.Contains(message, "attempt(s)") ||
		strings.Contains(message, "after 2 attempt(s)") {
		t.Fatalf("message = %q", message)
	}
}

// An application that takes two seconds to refuse is never asked again
// before its last answer is in, and never waited on longer than the
// configured interval.
func TestReadinessDoesNotHurryAnApplicationThatAnswersSlowly(t *testing.T) {
	t.Parallel()
	clock := newPollingClock()
	runner := pollingRunner(clock, nil, -1, 2*time.Second)
	evidence := runner.Run(context.Background(), pollingCheck(`{"attempts":5,"intervalSeconds":3,"timeoutSeconds":3}`), pollingTarget)
	if evidence.Outcome != HealthFailed {
		t.Fatalf("outcome = %s", evidence.Outcome)
	}
	for index, wait := range clock.waits {
		if wait < 2*time.Second || wait > 3*time.Second {
			t.Fatalf("wait %d = %s, want between the attempt's 2 s and the 3 s interval", index, wait)
		}
	}
	var waited time.Duration
	for _, wait := range clock.waits {
		waited += wait
	}
	if waited != 12*time.Second {
		t.Fatalf("waited %s in all, want the 12 s budget of four 3 s waits", waited)
	}
	// Settings says a check gives up after attempts × (timeout + interval).
	if took := evidence.CompletedAt.Sub(evidence.StartedAt); took > 5*(3+3)*time.Second {
		t.Fatalf("a slow check took %s, past the 30 s Settings shows", took)
	}
}

// Without an interval the attempts run back to back, as many as configured.
func TestReadinessWithoutAnIntervalRunsItsAttempts(t *testing.T) {
	t.Parallel()
	clock := newPollingClock()
	runner := pollingRunner(clock, nil, -1, 0)
	evidence := runner.Run(context.Background(), pollingCheck(`{"attempts":7,"intervalSeconds":0,"timeoutSeconds":1}`), pollingTarget)
	if evidence.AttemptCount() != 7 || len(clock.waits) != 0 {
		t.Fatalf("asked %d times with %d waits, want 7 and none", evidence.AttemptCount(), len(clock.waits))
	}
}

// Cancelling the run stops the check at the wait it is in.
func TestReadinessStopsWhenTheRunIsCancelled(t *testing.T) {
	t.Parallel()
	clock := newPollingClock()
	ctx, cancel := context.WithCancel(context.Background())
	runner := pollingRunner(clock, nil, -1, 0)
	runner.sleep = func(ctx context.Context, wait time.Duration) {
		clock.sleep(ctx, wait)
		if clock.elapsed() >= 2*time.Second {
			cancel()
		}
	}
	evidence := runner.Run(ctx, pollingCheck(`{"attempts":20,"intervalSeconds":3,"timeoutSeconds":3}`), pollingTarget)
	if evidence.Outcome != HealthFailed || clock.elapsed() > 2*time.Second || evidence.AttemptCount() != 8 {
		t.Fatalf("cancelled check ran on: %s elapsed, %d attempts", clock.elapsed(), evidence.AttemptCount())
	}
}

// Asking quickly does not shorten the crash-loop grace: a candidate that has
// restarted six times is still given its first thirty seconds.
func TestReadinessKeepsTheCrashLoopGraceWhenItAsksQuickly(t *testing.T) {
	t.Parallel()
	clock := newPollingClock()
	backend := &livenessBackendFake{state: dockerx.ContainerLiveness{Status: "restarting", Restarting: true, ExitCode: 1, RestartCount: 6}}
	runner := pollingRunner(clock, backend, -1, 0)
	target := pollingTarget
	target.ContainerID = "candidate"
	evidence := runner.Run(context.Background(), pollingCheck(`{"attempts":20,"intervalSeconds":3,"timeoutSeconds":3}`), target)
	last := evidence.Attempts[len(evidence.Attempts)-1]
	took := evidence.CompletedAt.Sub(evidence.StartedAt)
	if last.Code != "application_exited" || took < crashLoopGrace || took >= 57*time.Second {
		t.Fatalf("gave up after %s with %+v, want the loop read after the 30 s grace", took, last)
	}
}

// Every distinct answer is kept in order, with the first and the last, and
// each run of repeats says how many it stands for and over how long.
func TestCollapseAttemptsKeepsEveryDistinctAnswer(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	answers := []CheckAttemptEvidence{
		{Outcome: HealthFailed, Code: "connection_failed"},
		{Outcome: HealthFailed, Code: "connection_failed"},
		{Outcome: HealthFailed, Code: "connection_failed"},
		{Outcome: HealthFailed, Code: "unexpected_status", StatusCode: 503},
		{Outcome: HealthFailed, Code: "unexpected_status", StatusCode: 502},
		{Outcome: HealthFailed, Code: "unexpected_status", StatusCode: 502},
		{Outcome: HealthFailed, Code: "unexpected_status", StatusCode: 502},
	}
	timed := make([]timedAttempt, len(answers))
	for index, answer := range answers {
		answer.Number = index + 1
		began := start.Add(time.Duration(index) * 500 * time.Millisecond)
		timed[index] = timedAttempt{answer, began, began.Add(100 * time.Millisecond)}
	}
	kept := collapseAttempts(timed)
	want := []struct {
		number, repeats, lastNumber int
		span                        int64
		status                      int
	}{
		{1, 2, 3, 1100, 0},
		{4, 0, 0, 0, 503},
		{5, 1, 6, 600, 502},
		{7, 0, 0, 0, 502},
	}
	if len(kept) != len(want) {
		t.Fatalf("kept %d entries: %+v", len(kept), kept)
	}
	for index, entry := range kept {
		w := want[index]
		if entry.Number != w.number || entry.Repeats != w.repeats || entry.LastNumber != w.lastNumber ||
			entry.SpanMillis != w.span || entry.StatusCode != w.status {
			t.Fatalf("entry %d = %+v, want %+v", index, entry, w)
		}
	}
	if count := (CheckEvidence{Attempts: kept}).AttemptCount(); count != len(answers) {
		t.Fatalf("AttemptCount = %d, want %d", count, len(answers))
	}
	if one := collapseAttempts(timed[:1]); len(one) != 1 || one[0].Repeats != 0 {
		t.Fatalf("a single attempt = %+v", one)
	}
}
