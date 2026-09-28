// Package jobs runs the operations that take longer than a request should.
//
// The dashboard already streams `docker compose up` line by line, and that
// runner is scoped to its socket: closing the tab kills the command, and
// reconnecting re-issues the GET and runs it again. For compose that is a
// defensible trade — the frontend refuses to reconnect for exactly that
// reason — but it is the wrong shape for the operations here.
//
// A certbot issue takes up to a few minutes and a package upgrade can take
// half an hour, and both are things an operator starts and then goes and does
// something else. Neither should die because a VPN blinked, and neither should
// start over because somebody reopened the page. So a job is *detached*: it
// descends from context.Background() like the deploy and backup work does, it
// buffers its own output, and a client attaches to it by id rather than by
// starting it.
//
// That inverts the compose runner's compromise. Reconnecting is safe here
// because the socket does not own the work; the sequence number on each line
// is what lets a returning client pick up where it stopped rather than
// replaying from the beginning.
package jobs

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Status is where a job has got to.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Line is one line of output.
//
// Seq is assigned by the job rather than by the writer, and it is what makes a
// reconnect cheap: a client that saw up to 400 asks for everything after 400
// instead of re-reading a megabyte it already has.
type Line struct {
	Seq int `json:"seq"`
	// Stream is stdout, stderr, or status for the runner's own headings —
	// "Requesting a certificate", "[2/3] Reloading" — which are not the
	// command's output and should not be read as it.
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

// Job is one operation, past or present.
type Job struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// Target names what it acted on, so a list of five renewals is readable.
	Target    string     `json:"target,omitempty"`
	Status    Status     `json:"status"`
	ExitCode  int        `json:"exitCode"`
	Error     string     `json:"error,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	// StartedBy is the principal who asked for it. A job outlives its request,
	// so the audit entry alone would not say who is responsible for something
	// still running twenty minutes later.
	StartedBy string `json:"startedBy,omitempty"`
	// Lines is how many have been produced, including any the ring buffer has
	// already dropped — so a truncated log can say it was truncated.
	Lines int `json:"lines"`
}

// Spec describes a job before it runs.
type Spec struct {
	Kind      string
	Title     string
	Target    string
	StartedBy string
	// Timeout bounds the work. It is the only bound: nothing joins these
	// goroutines and shutdown does not cancel them, for the same reason a
	// deploy halfway through is worse than one that finishes into a dashboard
	// that is no longer running.
	Timeout time.Duration
}

// Emitter is what a Runner writes to.
type Emitter interface {
	// Status writes one of the runner's own headings.
	Status(format string, args ...any)
	// Line writes a line attributed to a stream.
	Line(stream, text string)
	// Run executes a command on the host and forwards its output as it
	// appears, returning the exit code. It is the reason most Runners are
	// three lines long.
	Run(ctx context.Context, name string, args ...string) (int, error)
	// RunEnv is Run with extra environment, for the tools that need
	// DEBIAN_FRONTEND or a plain progress renderer.
	RunEnv(ctx context.Context, env []string, name string, args ...string) (int, error)
	// RunCmd streams a command the caller built, for a tool whose side of the
	// namespace boundary is the caller's decision rather than always the
	// host's: certbot runs wherever the page read it from. The command must
	// carry the job's context, so cancelling the job stops it. shown is the
	// argv the console prints, without the plumbing that reaches the host.
	RunCmd(cmd *exec.Cmd, shown []string) (int, error)
}

// Runner is the work itself. Returning an error fails the job; the error text
// reaches the client as the job's Error rather than as a line, so a console
// that scrolled past still shows what went wrong.
type Runner func(ctx context.Context, out Emitter) error

const (
	// maxLines is the ring buffer's depth. An apt upgrade of a few hundred
	// packages runs to a few thousand lines; a runaway one must not be able to
	// hold the whole dashboard's memory.
	maxLines = 5000
	// maxLineBytes bounds a single line, for a command that emits a megabyte
	// without a newline.
	maxLineBytes = 64 * 1024
	// maxJobs is how many finished jobs are kept. Enough to answer "what
	// happened this afternoon" and not enough to be a log store — the audit
	// table is where a permanent record belongs.
	maxJobs = 50
	// defaultTimeout applies when a Spec does not set one.
	defaultTimeout = 30 * time.Minute
)

type entry struct {
	mu     sync.RWMutex
	job    Job
	lines  []Line
	first  int // Seq of lines[0], so a dropped prefix is still countable
	subs   map[chan Line]struct{}
	cancel context.CancelFunc
	// exclusive is a job started with StartExclusive, and cancelling that
	// it was asked to stop.
	exclusive, cancelling bool
}

// Manager owns every job.
type Manager struct {
	mu      sync.RWMutex
	entries map[string]*entry
	order   []string // newest last, for pruning and listing
	log     *slog.Logger
}

func New(log *slog.Logger) *Manager {
	return &Manager{entries: map[string]*entry{}, order: []string{}, log: log}
}

// Start begins a job and returns immediately.
func (m *Manager) Start(spec Spec, run Runner) Job {
	job, _ := m.start(spec, run, "")
	return job
}

// StartExclusive begins a job unless one whose kind starts with prefix is
// still running, in which case it starts nothing and returns that one.
//
// Some tools cannot run twice at once: a second certbot fails on the lock the
// first holds, and on a page with a console the new job would replace the
// running one on screen. Checking and starting under one lock is what keeps
// two clicks, or two tabs, from both getting through.
func (m *Manager) StartExclusive(prefix string, spec Spec, run Runner) (Job, bool) {
	return m.start(spec, run, prefix)
}

func (m *Manager) start(spec Spec, run Runner, exclusive string) (Job, bool) {
	if spec.Timeout <= 0 {
		spec.Timeout = defaultTimeout
	}
	id := newID()

	m.mu.Lock()
	if exclusive != "" {
		for _, other := range m.order {
			if job := m.entries[other].snapshotJob(); job.Status == StatusRunning && strings.HasPrefix(job.Kind, exclusive) {
				m.mu.Unlock()
				return job, false
			}
		}
	}
	// From Background, not from the request: the whole point is that this
	// outlives the call that asked for it.
	ctx, cancel := context.WithTimeout(context.Background(), spec.Timeout)
	e := &entry{
		job: Job{
			ID: id, Kind: spec.Kind, Title: spec.Title, Target: spec.Target,
			Status: StatusRunning, StartedAt: time.Now().UTC(), StartedBy: spec.StartedBy,
		},
		lines:     make([]Line, 0, 64),
		subs:      map[chan Line]struct{}{},
		cancel:    cancel,
		exclusive: exclusive != "",
	}
	m.entries[id] = e
	m.order = append(m.order, id)
	m.prune()
	m.mu.Unlock()

	go func() {
		defer cancel()
		err := run(ctx, &emitter{entry: e})
		e.finish(err, ctx.Err())
		// Pruned here as well as at Start. A burst of jobs can finish after
		// the last one began, and pruning only on the way in left the cap
		// exceeded until something else was started — which, on a dashboard
		// nobody is currently using, is never.
		m.mu.Lock()
		m.prune()
		m.mu.Unlock()
		if err != nil && m.log != nil {
			m.log.Warn("job failed", "id", id, "kind", spec.Kind, "target", spec.Target, "err", err)
		}
	}()
	return e.snapshotJob(), true
}

// prune drops the oldest finished jobs. Must be called with m.mu held.
//
// A running job is never dropped, however old: removing it would orphan the
// output while the process carried on, and the operator would be left with a
// command they can neither watch nor stop. When everything left is running the
// cap is simply exceeded until something finishes, which is the right way
// round.
func (m *Manager) prune() {
	for len(m.order) > maxJobs {
		removed := false
		for i, id := range m.order {
			e := m.entries[id]
			if e != nil && e.snapshotJob().Status == StatusRunning {
				continue
			}
			delete(m.entries, id)
			m.order = append(m.order[:i], m.order[i+1:]...)
			removed = true
			break
		}
		if !removed {
			return
		}
	}
}

// Get returns a job and the output the buffer still holds.
func (m *Manager) Get(id string) (Job, []Line, bool) {
	m.mu.RLock()
	e := m.entries[id]
	m.mu.RUnlock()
	if e == nil {
		return Job{}, nil, false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.job, append([]Line(nil), e.lines...), true
}

// List returns every job, newest first.
func (m *Manager) List() []Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Job, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		if e := m.entries[m.order[i]]; e != nil {
			out = append(out, e.snapshotJob())
		}
	}
	return out
}

// Subscribe attaches to a job. It returns everything after seq that the buffer
// still holds, plus a channel of what comes next, atomically — a client that
// read the snapshot and then subscribed would miss whatever arrived in between.
func (m *Manager) Subscribe(id string, after int) (Job, []Line, <-chan Line, func(), bool) {
	m.mu.RLock()
	e := m.entries[id]
	m.mu.RUnlock()
	if e == nil {
		return Job{}, nil, nil, nil, false
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	backlog := []Line{}
	for _, l := range e.lines {
		if l.Seq > after {
			backlog = append(backlog, l)
		}
	}
	// Buffered generously: a slow reader must not stall the command, and the
	// send below drops rather than blocks for the same reason.
	ch := make(chan Line, 512)
	e.subs[ch] = struct{}{}
	unsubscribe := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if _, ok := e.subs[ch]; ok {
			delete(e.subs, ch)
			close(ch)
		}
	}
	return e.job, backlog, ch, unsubscribe, true
}

// Cancel stops a running job. The context cancellation kills the process
// group the command is in, which is what makes an interrupted apt upgrade stop
// rather than merely stop being watched.
//
// An exclusive job stays running until its runner returns. Its exclusivity
// exists because the tool cannot run twice, and what the runner is still
// stopping is still running: a systemd unit it started outlives the
// systemctl that waits on it. Asking again while it stops changes nothing.
func (m *Manager) Cancel(id string) bool {
	m.mu.RLock()
	e := m.entries[id]
	m.mu.RUnlock()
	if e == nil {
		return false
	}
	e.mu.Lock()
	running, again := e.job.Status == StatusRunning, e.cancelling
	if running {
		e.cancelling = true
	}
	e.mu.Unlock()
	if !running {
		return false
	}
	if again {
		return true
	}
	e.appendLine("status", "Cancelled from the dashboard.")
	if !e.exclusive {
		e.markCancelled()
	}
	e.cancel()
	return true
}

// Shutdown releases subscribers. The jobs themselves are deliberately left
// running: a certificate issuance interrupted halfway is worse than one that
// completes into a dashboard that has restarted.
func (m *Manager) Shutdown() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.entries {
		e.mu.Lock()
		for ch := range e.subs {
			delete(e.subs, ch)
			close(ch)
		}
		e.mu.Unlock()
	}
}

func (e *entry) snapshotJob() Job {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.job
}

func (e *entry) appendLine(stream, text string) {
	e.mu.Lock()
	e.job.Lines++
	line := Line{Seq: e.job.Lines, Stream: stream, Text: text, At: time.Now().UTC()}
	e.lines = append(e.lines, line)
	if len(e.lines) > maxLines {
		e.lines = e.lines[len(e.lines)-maxLines:]
		e.first = e.lines[0].Seq
	}
	subs := make([]chan Line, 0, len(e.subs))
	for ch := range e.subs {
		subs = append(subs, ch)
	}
	e.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- line:
		default:
			// A subscriber that cannot keep up is skipped rather than allowed
			// to stall the command. It still has the buffer to catch up from
			// when it reconnects.
		}
	}
}

func (e *entry) setExitCode(code int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.job.ExitCode = code
}

func (e *entry) markCancelled() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.job.Status == StatusRunning {
		e.job.Status = StatusCancelled
	}
}

func (e *entry) finish(runErr, ctxErr error) {
	e.mu.Lock()
	now := time.Now().UTC()
	e.job.EndedAt = &now
	switch {
	case e.job.Status == StatusCancelled:
		// Already recorded by Cancel; the runner's error is the cancellation.
	case e.cancelling && runErr != nil:
		// An exclusive job, stopped now.
		e.job.Status = StatusCancelled
	case runErr != nil:
		e.job.Status = StatusFailed
		e.job.Error = runErr.Error()
		if ctxErr == context.DeadlineExceeded {
			e.job.Error = "timed out: " + e.job.Error
		}
	default:
		e.job.Status = StatusSucceeded
	}
	subs := make([]chan Line, 0, len(e.subs))
	for ch := range e.subs {
		subs = append(subs, ch)
		delete(e.subs, ch)
	}
	e.mu.Unlock()

	// Closing every subscriber is how a streaming client learns the job is
	// over without polling for it.
	for _, ch := range subs {
		close(ch)
	}
}

type emitter struct {
	entry *entry
	// lastCode is what the most recent command exited with. A job that runs
	// several — a pull and then an up — is reported by the one that decided
	// the outcome, which is the last one to run.
	lastCode int
}

func (em *emitter) Status(format string, args ...any) {
	em.entry.appendLine("status", fmt.Sprintf(format, args...))
}

func (em *emitter) Line(stream, text string) {
	em.entry.appendLine(stream, text)
}

func (em *emitter) Run(ctx context.Context, name string, args ...string) (int, error) {
	return em.RunEnv(ctx, nil, name, args...)
}

// RunEnv streams a command's output line by line.
//
// The argv is passed through unchanged and never through a shell, as
// everywhere else in this codebase — every caller builds it from validated
// pieces.
func (em *emitter) RunEnv(ctx context.Context, env []string, name string, args ...string) (int, error) {
	cmd := hostexec.CommandOnHost(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	return em.RunCmd(cmd, append([]string{name}, args...))
}

func (em *emitter) RunCmd(cmd *exec.Cmd, shown []string) (int, error) {
	name := shown[0]
	em.Status("$ %s", strings.Join(shown, " "))
	// The code the last command exited with is recorded on the job, because
	// it is a field the API has always carried and nothing ever wrote: every
	// failed job reported "exit 0", which next to "failed" is a contradiction
	// the reader has to resolve by ignoring one of them.
	// -1 up front so a command that never started — a missing binary, a pipe
	// that could not be opened — is not reported with whatever the previous
	// one exited with.
	em.lastCode = -1
	defer func() { em.entry.setExitCode(em.lastCode) }()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}
	if err := cmd.Start(); err != nil {
		// A missing binary fails here rather than at Wait, and "executable
		// file not found in $PATH" is the wrong sentence for an operator who
		// never chose the path.
		return -1, friendlyExecError(name, err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	scan := func(r *bufio.Scanner, stream string) {
		defer wg.Done()
		r.Buffer(make([]byte, 0, 8192), maxLineBytes)
		for r.Scan() {
			em.entry.appendLine(stream, strings.TrimRight(r.Text(), "\r"))
		}
	}
	go scan(bufio.NewScanner(stdout), "stdout")
	go scan(bufio.NewScanner(stderr), "stderr")
	wg.Wait()

	waitErr := cmd.Wait()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	if waitErr != nil && code == 0 {
		code = -1
	}
	em.lastCode = code
	if waitErr != nil {
		if friendly := friendlyExecError(name, waitErr); friendly != waitErr {
			return code, friendly
		}
	}
	return code, nil
}

// friendlyExecError turns Go's "executable file not found in $PATH" into
// something an operator can act on. Every other failure is passed through
// unchanged: the tool's own words are usually the useful ones.
func friendlyExecError(name string, err error) error {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return fmt.Errorf("%s is not installed on this host", name)
	}
	return err
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// A collision here would attach two operators to one console. The
		// timestamp is not unpredictable but it is unique enough to be a
		// safe fallback for something that is an identifier, not a secret.
		return fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
