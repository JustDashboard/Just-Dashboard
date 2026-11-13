package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	m := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(m.Shutdown)
	return m
}

// waitFor polls until the condition holds. Jobs are asynchronous by design, so
// every assertion about a finished one has to wait for it rather than sleep a
// guessed amount and hope.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestStartRunsAndSucceeds(t *testing.T) {
	m := testManager(t)
	started := m.Start(Spec{Kind: "test", Title: "Hello"}, func(ctx context.Context, out Emitter) error {
		out.Status("working")
		out.Line("stdout", "one")
		out.Line("stdout", "two")
		return nil
	})
	if started.Status != StatusRunning {
		t.Fatalf("a job should start running, got %q", started.Status)
	}

	waitFor(t, "the job to finish", func() bool {
		job, _, _ := m.Get(started.ID)
		return job.Status != StatusRunning
	})
	job, lines, ok := m.Get(started.ID)
	if !ok {
		t.Fatal("job vanished")
	}
	if job.Status != StatusSucceeded {
		t.Fatalf("status = %q, error %q", job.Status, job.Error)
	}
	if job.EndedAt == nil {
		t.Error("a finished job should have an end time")
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines: %+v", len(lines), lines)
	}
	if lines[0].Stream != "status" || lines[1].Text != "one" {
		t.Fatalf("lines = %+v", lines)
	}
	// Sequence numbers are what a reconnecting client resumes from.
	for i, l := range lines {
		if l.Seq != i+1 {
			t.Fatalf("line %d has seq %d", i, l.Seq)
		}
	}
}

func TestFailureIsRecordedWithItsReason(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		out.Line("stderr", "something went wrong")
		return errors.New("the thing failed")
	})
	waitFor(t, "failure", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status == StatusFailed
	})
	j, _, _ := m.Get(job.ID)
	// The reason is on the job rather than only in the output, so a console
	// that has scrolled past still shows what went wrong.
	if j.Error != "the thing failed" {
		t.Fatalf("error = %q", j.Error)
	}
}

// A job runs on a goroutine of its own, which no request's recovery covers:
// a runner that panicked used to end the process, and every other job in it.
func TestAJobThatPanicsFailsAndTheOthersCarryOn(t *testing.T) {
	m := testManager(t)
	release := make(chan struct{})
	other := m.Start(Spec{Kind: "other"}, func(ctx context.Context, out Emitter) error {
		<-release
		return nil
	})
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		var parsed []string
		out.Line("stdout", parsed[2]) // the fault: an unusual line
		return nil
	})
	waitFor(t, "the panicking job to fail", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status == StatusFailed
	})
	if j, _, _ := m.Get(job.ID); !strings.Contains(j.Error, "index out of range") {
		t.Fatalf("error = %q, want the fault said on the job", j.Error)
	}
	close(release)
	waitFor(t, "the other job to finish", func() bool {
		j, _, _ := m.Get(other.ID)
		return j.Status == StatusSucceeded
	})
}

// The whole point of the package: a job is not owned by the socket watching
// it, so nothing about attaching or detaching changes whether it runs.
func TestAJobOutlivesItsSubscribers(t *testing.T) {
	m := testManager(t)
	release := make(chan struct{})
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		out.Line("stdout", "before")
		<-release
		out.Line("stdout", "after")
		return nil
	})

	_, _, ch, unsubscribe, ok := m.Subscribe(job.ID, 0)
	if !ok {
		t.Fatal("could not subscribe")
	}
	unsubscribe()
	// Draining what the closed channel already held, so the read below is not
	// racing a send that happened before the unsubscribe.
	for range ch {
	}

	close(release)
	waitFor(t, "the job to finish without a listener", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status == StatusSucceeded
	})
	_, lines, _ := m.Get(job.ID)
	if len(lines) != 2 || lines[1].Text != "after" {
		t.Fatalf("output produced after the listener left was lost: %+v", lines)
	}
}

// Reconnecting is the case the compose runner cannot handle. A client that saw
// up to seq N gets everything after N and nothing it already has.
func TestSubscribeResumesFromASequence(t *testing.T) {
	m := testManager(t)
	release := make(chan struct{})
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		out.Line("stdout", "one")
		out.Line("stdout", "two")
		<-release
		out.Line("stdout", "three")
		return nil
	})
	waitFor(t, "the first two lines", func() bool {
		_, lines, _ := m.Get(job.ID)
		return len(lines) >= 2
	})

	_, backlog, ch, unsubscribe, ok := m.Subscribe(job.ID, 1)
	if !ok {
		t.Fatal("could not subscribe")
	}
	defer unsubscribe()
	if len(backlog) != 1 || backlog[0].Text != "two" {
		t.Fatalf("backlog = %+v, want only line two", backlog)
	}

	close(release)
	var live []Line
	for l := range ch {
		live = append(live, l)
	}
	if len(live) != 1 || live[0].Text != "three" {
		t.Fatalf("live = %+v", live)
	}
}

// A finished job closes its subscribers, which is how a streaming client
// learns it is over without polling.
func TestSubscribersAreClosedWhenTheJobEnds(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		out.Line("stdout", "done")
		return nil
	})
	_, _, ch, unsubscribe, _ := m.Subscribe(job.ID, 0)
	defer unsubscribe()
	select {
	case <-time.After(5 * time.Second):
		t.Fatal("the channel was never closed")
	case _, open := <-drain(ch):
		if open {
			t.Fatal("expected the channel to be closed")
		}
	}
}

// drain reads everything and returns a channel that closes when the source does.
func drain(ch <-chan Line) <-chan Line {
	out := make(chan Line)
	go func() {
		defer close(out)
		for range ch {
		}
	}()
	return out
}

func TestCancelStopsARunningJob(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		<-ctx.Done()
		return ctx.Err()
	})
	waitFor(t, "the job to be running", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status == StatusRunning
	})
	if !m.Cancel(job.ID) {
		t.Fatal("cancel returned false for a running job")
	}
	waitFor(t, "cancellation", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status == StatusCancelled
	})
	// A finished job cannot be cancelled again, and saying so beats pretending.
	if m.Cancel(job.ID) {
		t.Error("cancelled an already-finished job")
	}
	if m.Cancel("nope") {
		t.Error("cancelled a job that does not exist")
	}
}

func TestTimeoutFailsTheJobAndSaysSo(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test", Timeout: 50 * time.Millisecond},
		func(ctx context.Context, out Emitter) error {
			<-ctx.Done()
			return ctx.Err()
		})
	waitFor(t, "the timeout", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status == StatusFailed
	})
	j, _, _ := m.Get(job.ID)
	if !strings.Contains(j.Error, "timed out") {
		t.Fatalf("a timeout should say so: %q", j.Error)
	}
}

// A command that prints without stopping must not be able to hold the whole
// dashboard's memory.
func TestOutputIsBounded(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		for i := 0; i < maxLines+250; i++ {
			out.Line("stdout", fmt.Sprintf("line %d", i))
		}
		return nil
	})
	waitFor(t, "the job to finish", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status == StatusSucceeded
	})
	j, lines, _ := m.Get(job.ID)
	if len(lines) != maxLines {
		t.Fatalf("buffer held %d lines, want it capped at %d", len(lines), maxLines)
	}
	// The count is of everything produced, not of what survived, so a
	// truncated log can say it was truncated.
	if j.Lines != maxLines+250 {
		t.Fatalf("Lines = %d, want the full count", j.Lines)
	}
	if lines[0].Seq == 1 {
		t.Error("the oldest lines should have been dropped, not the newest")
	}
}

// A subscriber that stops reading must not stall the command producing the
// output — that would make a hung browser tab into a hung upgrade.
func TestASlowSubscriberDoesNotStallTheJob(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		for i := 0; i < 2000; i++ {
			out.Line("stdout", fmt.Sprintf("line %d", i))
		}
		return nil
	})
	_, _, _, unsubscribe, ok := m.Subscribe(job.ID, 0)
	if !ok {
		t.Fatal("could not subscribe")
	}
	defer unsubscribe()
	// Never reading from the channel.
	waitFor(t, "the job to finish despite a subscriber that never reads", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status == StatusSucceeded
	})
}

func TestListIsNewestFirst(t *testing.T) {
	m := testManager(t)
	for i := 0; i < 3; i++ {
		m.Start(Spec{Kind: "test", Title: fmt.Sprintf("job %d", i)},
			func(ctx context.Context, out Emitter) error { return nil })
	}
	waitFor(t, "all three to finish", func() bool {
		for _, j := range m.List() {
			if j.Status == StatusRunning {
				return false
			}
		}
		return len(m.List()) == 3
	})
	list := m.List()
	if list[0].Title != "job 2" || list[2].Title != "job 0" {
		t.Fatalf("order = %q, %q, %q", list[0].Title, list[1].Title, list[2].Title)
	}
}

// Finished jobs are pruned; a running one never is, because dropping it would
// orphan the output while the process carried on.
func TestPruningKeepsRunningJobs(t *testing.T) {
	m := testManager(t)
	release := make(chan struct{})
	defer close(release)
	long := m.Start(Spec{Kind: "test", Title: "long"}, func(ctx context.Context, out Emitter) error {
		<-release
		return nil
	})
	for i := 0; i < maxJobs+5; i++ {
		m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error { return nil })
	}
	waitFor(t, "the list to be pruned", func() bool { return len(m.List()) <= maxJobs })
	if _, _, ok := m.Get(long.ID); !ok {
		t.Fatal("a running job was pruned")
	}
}

func TestGetAndSubscribeRejectUnknownIDs(t *testing.T) {
	m := testManager(t)
	if _, _, ok := m.Get("nope"); ok {
		t.Error("Get accepted an unknown id")
	}
	if _, _, _, _, ok := m.Subscribe("nope", 0); ok {
		t.Error("Subscribe accepted an unknown id")
	}
}

// Two clients watching the same job both see everything.
func TestMultipleSubscribers(t *testing.T) {
	m := testManager(t)
	release := make(chan struct{})
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		<-release
		out.Line("stdout", "shared")
		return nil
	})
	var wg sync.WaitGroup
	seen := make([]int, 2)
	for i := range seen {
		_, _, ch, unsubscribe, ok := m.Subscribe(job.ID, 0)
		if !ok {
			t.Fatal("could not subscribe")
		}
		wg.Add(1)
		go func(i int, ch <-chan Line, unsubscribe func()) {
			defer wg.Done()
			defer unsubscribe()
			for range ch {
				seen[i]++
			}
		}(i, ch, unsubscribe)
	}
	close(release)
	wg.Wait()
	for i, n := range seen {
		if n != 1 {
			t.Errorf("subscriber %d saw %d lines, want 1", i, n)
		}
	}
}

// The Emitter's Run helper is how nearly every real job is written, so its
// exit code and its output both have to be right.
func TestEmitterRunCapturesOutputAndExitCode(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		code, err := out.Run(ctx, "echo", "hello from the job")
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("echo exited %d", code)
		}
		return nil
	})
	waitFor(t, "echo to finish", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status != StatusRunning
	})
	j, lines, _ := m.Get(job.ID)
	if j.Status != StatusSucceeded {
		t.Fatalf("status = %q, error %q", j.Status, j.Error)
	}
	var found bool
	for _, l := range lines {
		if l.Stream == "stdout" && l.Text == "hello from the job" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the command's output was not captured: %+v", lines)
	}
	// The command line itself is echoed as a status line, so the console shows
	// what was actually run rather than only its output.
	if lines[0].Stream != "status" || !strings.Contains(lines[0].Text, "echo") {
		t.Fatalf("first line = %+v", lines[0])
	}
}

func TestEmitterRunReportsANonZeroExit(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		code, err := out.Run(ctx, "false")
		if err != nil {
			return err
		}
		if code == 0 {
			return errors.New("false exited zero")
		}
		return nil
	})
	waitFor(t, "false to finish", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status != StatusRunning
	})
	if j, _, _ := m.Get(job.ID); j.Status != StatusSucceeded {
		t.Fatalf("status = %q, error %q", j.Status, j.Error)
	}
}

func TestEmitterRunSaysWhenABinaryIsMissing(t *testing.T) {
	m := testManager(t)
	job := m.Start(Spec{Kind: "test"}, func(ctx context.Context, out Emitter) error {
		_, err := out.Run(ctx, "definitely-not-a-real-binary-xyz")
		return err
	})
	waitFor(t, "the missing binary", func() bool {
		j, _, _ := m.Get(job.ID)
		return j.Status != StatusRunning
	})
	j, _, _ := m.Get(job.ID)
	if j.Status != StatusFailed || !strings.Contains(j.Error, "not installed") {
		t.Fatalf("status %q error %q", j.Status, j.Error)
	}
}
