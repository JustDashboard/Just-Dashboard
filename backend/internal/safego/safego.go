// Package safego starts the dashboard's background goroutines so that a panic
// in one of them is that task's problem and not the process's.
//
// A panic on a goroutine nothing recovers ends the program. Most of the
// dashboard's background loops parse what the host prints (nginx, ss, systemd,
// Docker's JSON), so a parser fault on one unusual line used to take the whole
// dashboard down for everybody; and since the line was still there after the
// restart, it came up, read the line and went down again, which left the
// operator without the tool they would have looked for the cause with.
//
// A task started here is recovered, its panic logged once with its stack
// under the task's name, and started again after a pause that doubles up to a
// minute. Every task is remembered by name (Tasks) so a page can say which
// one is in trouble.
//
// httpx.Recoverer does the same for request goroutines. What neither covers
// is a goroutine started with a bare `go`. The background work the server
// begins at boot is started from functions called Start, and
// TestStartFunctionsStartSupervisedGoroutines fails on one that starts a
// goroutine any other way; a goroutine a request handler starts for itself is
// still its author's to protect, with Recover where it matters. Jobs are the
// exception already made: every job runs under Try (jobs.Manager.run).
package safego

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

const (
	// firstPause is the wait before a task that panicked is started again,
	// doubling with each panic in a row up to maxPause.
	firstPause = time.Second
	maxPause   = time.Minute
	// A run that lasted this long was working: the next panic starts the
	// pauses over rather than carrying on from where an old one left off.
	steadyAfter = 5 * time.Minute
)

// Task is what is known of one supervised goroutine.
type Task struct {
	Name string `json:"name"`
	// Running is false once the task has returned for good, and while it
	// waits to be started again after a panic.
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"startedAt,omitzero"`
	// Panics counts every panic since the process started.
	Panics      int        `json:"panics"`
	LastPanic   string     `json:"lastPanic,omitempty"`
	LastPanicAt *time.Time `json:"lastPanicAt,omitempty"`
	// RestartAt is when a task that panicked will be started again.
	RestartAt *time.Time `json:"restartAt,omitempty"`
}

var registry = struct {
	sync.Mutex
	tasks map[string]*Task
}{tasks: map[string]*Task{}}

// Tasks is every task started since the process began, by name.
func Tasks() []Task {
	registry.Lock()
	defer registry.Unlock()
	out := make([]Task, 0, len(registry.tasks))
	for _, t := range registry.tasks {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Faulted is the tasks that have panicked since the process began.
func Faulted() []Task {
	var out []Task
	for _, t := range Tasks() {
		if t.Panics > 0 {
			out = append(out, t)
		}
	}
	return out
}

func update(name string, change func(*Task)) {
	registry.Lock()
	defer registry.Unlock()
	t, ok := registry.tasks[name]
	if !ok {
		t = &Task{Name: name}
		registry.tasks[name] = t
	}
	change(t)
}

// Go starts fn on a goroutine of its own and keeps it running: see Run.
//
// after is what the loop used to do on its way out and can only do once,
// closing the channel its Stop waits on above all: each is called, in order,
// when the task has returned for good. The channel Go returns closes after
// them, for a caller with no channel of its own to close.
//
// name is a fixed phrase, never built from an id: tasks are remembered by
// name for the life of the process.
func Go(ctx context.Context, log *slog.Logger, name string, fn func(context.Context), after ...func()) <-chan struct{} {
	return launch(ctx, log, name, fn, firstPause, maxPause, after)
}

func launch(ctx context.Context, log *slog.Logger, name string, fn func(context.Context), first, longest time.Duration, after []func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			for _, then := range after {
				then()
			}
		}()
		run(ctx, log, name, fn, first, longest)
	}()
	return done
}

// Run calls fn and returns when fn returns. If fn panics, the panic is logged
// with its stack and fn is called again after a pause, unless ctx has ended by
// then: ctx is what tells a task to stop, so a task whose owner cancelled it
// is not started again, and the pause itself ends with ctx.
//
// fn is called more than once, so it must not do on its way out what can only
// be done once (closing a channel, WaitGroup.Done): that belongs after Run, or
// to the channel Go returns.
func Run(ctx context.Context, log *slog.Logger, name string, fn func(context.Context)) {
	run(ctx, log, name, fn, firstPause, maxPause)
}

func run(ctx context.Context, log *slog.Logger, name string, fn func(context.Context), first, longest time.Duration) {
	pause := first
	for {
		began := time.Now()
		update(name, func(t *Task) { t.Running, t.StartedAt, t.RestartAt = true, began, nil })
		panicked := call(log, name, func() { fn(ctx) })
		if !panicked || ctx.Err() != nil {
			update(name, func(t *Task) { t.Running, t.RestartAt = false, nil })
			return
		}
		if time.Since(began) >= steadyAfter {
			pause = first
		}
		again := time.Now().Add(pause)
		update(name, func(t *Task) { t.Running, t.RestartAt = false, &again })
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			update(name, func(t *Task) { t.RestartAt = nil })
			return
		case <-timer.C:
		}
		if pause *= 2; pause > longest {
			pause = longest
		}
	}
}

// Once starts fn on a goroutine of its own, once. A panic in it is logged and
// remembered like any task's, and it is not started again: for work that is
// done when it returns, such as a reconcile at boot. after is as for Go.
func Once(log *slog.Logger, name string, fn func(), after ...func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			for _, then := range after {
				then()
			}
		}()
		began := time.Now()
		update(name, func(t *Task) { t.Running, t.StartedAt = true, began })
		call(log, name, fn)
		update(name, func(t *Task) { t.Running = false })
	}()
	return done
}

// Recover is for a goroutine that is started some other way and must still not
// take the process with it: `defer safego.Recover(log, "name")` as its first
// statement. The goroutine ends; nothing starts it again.
func Recover(log *slog.Logger, name string) {
	if r := recover(); r != nil {
		record(log, name, r, debug.Stack())
	}
}

// Try calls fn on the caller's goroutine and returns its error. A panic in fn
// is logged and remembered like any task's and comes back as an error: for a
// goroutine whose work has somebody to tell, such as a job, which then fails
// with a reason instead of ending the process and every other job with it.
func Try(log *slog.Logger, name string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			record(log, name, r, debug.Stack())
			err = fmt.Errorf("stopped by a fault in the dashboard itself (%v); the backend's log has the stack trace", r)
		}
	}()
	return fn()
}

// call runs fn and reports whether it panicked.
func call(log *slog.Logger, name string, fn func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			record(log, name, r, debug.Stack())
		}
	}()
	fn()
	return false
}

func record(log *slog.Logger, name string, value any, stack []byte) {
	now := time.Now()
	message := fmt.Sprint(value)
	update(name, func(t *Task) {
		t.Panics++
		t.LastPanic, t.LastPanicAt = message, &now
	})
	if log == nil {
		log = slog.Default()
	}
	log.Error("background task panicked", "task", name, "panic", message, "stack", string(stack))
}

// Close returns a function that closes done, for the `after` of Go or Once.
//
// It is here for when the channel is read: now, as the task is launched. A
// loop that closed its own channel with a deferred close read the channel as
// it began, and a Stop that clears the field before waiting must still see
// that same channel closed, not have the task close whatever the field holds
// by the time it ends.
func Close(done chan struct{}) func() { return func() { close(done) } }
