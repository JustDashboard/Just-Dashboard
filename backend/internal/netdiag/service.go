package netdiag

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
)

type Runner func(context.Context, netsec.ProbeRequest) (*netsec.ProbeResult, error)

type activeRun struct {
	jobID    string
	done     chan struct{}
	shutdown bool
}

type Service struct {
	mu                sync.Mutex
	store             *Store
	jobs              *jobs.Manager
	runner            Runner
	active            map[string]*activeRun
	pendingWrites     map[string]Run
	started, stopping bool
	now               func() time.Time
	timeout           time.Duration
}

func New(store *Store, manager *jobs.Manager, runner Runner) *Service {
	return &Service{store: store, jobs: manager, runner: runner, active: map[string]*activeRun{}, pendingWrites: map[string]Run{}, now: func() time.Time { return time.Now().UTC() }, timeout: RunTimeout}
}

// Start settles the predecessor's unfinished records. No network work is
// replayed, and an interrupted record never claims that host cleanup was seen.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	if s.store == nil || s.jobs == nil || s.runner == nil {
		return ErrUnavailable
	}
	policy, err := s.store.retention(ctx)
	if err != nil {
		return err
	}
	runs, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if terminal(run.Status) {
			continue
		}
		now := s.now()
		run.Status, run.Outcome, run.OutcomeSource = "interrupted", "interrupted", "recovery"
		run.Error = "The backend restarted before this run finished. It was not rerun; final host-process cleanup was not independently observed."
		run.UpdatedAt, run.EndedAt = now, &now
		for i := range run.Stages {
			if run.Stages[i].Status == "queued" || run.Stages[i].Status == "running" {
				run.Stages[i].Status, run.Stages[i].Outcome, run.Stages[i].EndedAt = "interrupted", "interrupted", &now
			}
		}
		if err := s.store.update(ctx, run); err != nil {
			return err
		}
	}
	if err := s.store.prune(ctx, policy, s.now()); err != nil {
		return err
	}
	s.started = true
	return nil
}

func (s *Service) ready() error {
	if !s.started || s.stopping {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) prune(ctx context.Context) error {
	if err := s.flush(ctx); err != nil {
		return err
	}
	policy, err := s.store.retention(ctx)
	if err != nil {
		return err
	}
	return s.store.prune(ctx, policy, s.now())
}

// A failed final write must not turn an old running row into a successful read.
// Retry only recording on later access, never the probe. Admission stops while
// these writes fail, bounding this volatile queue to the four admitted runs.
func (s *Service) flush(ctx context.Context) error {
	for id, run := range s.pendingWrites {
		if err := s.store.update(ctx, run); err != nil {
			return fmt.Errorf("%w: a finished diagnostic could not be recorded: %v", ErrUnavailable, err)
		}
		delete(s.pendingWrites, id)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, name string, req netsec.ProbeRequest, actor string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.create(ctx, name, req, actor, "")
}

func (s *Service) create(ctx context.Context, name string, req netsec.ProbeRequest, actor, rerunOf string) (Run, error) {
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	name, err := validName(name)
	if err != nil {
		return Run{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	req, err = netsec.ValidateProbeRequest(req)
	if err != nil {
		return Run{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(s.active) >= MaxRunning {
		return Run{}, ErrBusy
	}
	if err := s.prune(ctx); err != nil {
		return Run{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Run{}, err
	}
	now := s.now()
	run := Run{ID: hex.EncodeToString(random[:]), Name: name, Request: req, Scope: scopeFor(req), Status: "queued", CreatedAt: now, UpdatedAt: now, CreatedBy: actor, RerunOf: rerunOf,
		Stages: []Stage{{ID: "validation", Status: "completed", StartedAt: &now, EndedAt: &now, Outcome: "validated"}, {ID: "probe", Status: "queued"}, {ID: "recording", Status: "queued"}}}
	if err := s.store.insert(ctx, run); err != nil {
		return Run{}, err
	}
	entry := &activeRun{done: make(chan struct{})}
	s.active[run.ID] = entry
	gate := make(chan struct{})
	// An exclusive job remains running while cancellation drains its process
	// group. The prefix is unique to this record; four different runs may work.
	job, _ := s.jobs.StartExclusive(JobPrefix+run.ID+".", jobs.Spec{Kind: JobPrefix + run.ID + "." + req.Tool, Title: name, Target: req.Target, StartedBy: actor, Timeout: s.timeout},
		func(jobCtx context.Context, out jobs.Emitter) error { <-gate; return s.execute(jobCtx, run, out) })
	entry.jobID, run.JobID = job.ID, job.ID
	err = s.store.bindJob(ctx, run.ID, job.ID)
	if err != nil {
		s.jobs.Cancel(job.ID)
	}
	close(gate)
	if err != nil {
		return Run{}, err
	}
	return run, nil
}

func (s *Service) execute(ctx context.Context, run Run, out jobs.Emitter) error {
	// Create returns its queued snapshot while this worker advances. Give the
	// worker its own stage slice so serializing that response cannot race.
	run.Stages = append([]Stage(nil), run.Stages...)
	s.mu.Lock()
	entry := s.active[run.ID]
	now := s.now()
	run.StartedAt, run.UpdatedAt = &now, now
	run.Status = "running"
	if ctx.Err() != nil {
		run.Status = "cancelling"
	}
	run.Stages[1].Status, run.Stages[1].StartedAt = "running", &now
	startErr := s.store.update(context.Background(), run)
	s.mu.Unlock()
	var result *netsec.ProbeResult
	err := startErr
	if err == nil && ctx.Err() == nil {
		out.Status("Running %s from this host", run.Request.Tool)
		result, err = s.runner(ctx, run.Request)
	}
	if err == nil && result == nil && ctx.Err() == nil {
		err = errors.New("the probe returned no result")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { delete(s.active, run.ID); close(entry.done) }()
	now = s.now()
	run.UpdatedAt, run.EndedAt = now, &now
	run.Result, run.ResultTruncated = boundedResult(result)
	run.HasResult = run.Result != nil
	run.Outcome, run.OutcomeSource = outcome(ctx.Err(), result, err)
	switch {
	case entry.shutdown:
		run.Status, run.Outcome, run.OutcomeSource = "interrupted", "interrupted", "recovery"
	case ctx.Err() == context.Canceled:
		run.Status = "cancelled"
	case ctx.Err() != nil || err != nil || result == nil || !result.OK:
		run.Status = "failed"
	default:
		run.Status = "completed"
	}
	if err != nil {
		run.Error, _ = clip(err.Error(), 2048)
	} else if run.Result != nil {
		run.Error = run.Result.Error
	}
	if ctx.Err() != nil && run.Error == "" {
		run.Error = ctx.Err().Error()
	}
	run.Stages[1].Status, run.Stages[1].Outcome, run.Stages[1].EndedAt = run.Status, run.Outcome, &now
	run.Stages[2] = Stage{ID: "recording", Status: "completed", StartedAt: &now, EndedAt: &now, Outcome: "saved"}
	if saveErr := s.store.update(context.Background(), run); saveErr != nil {
		s.pendingWrites[run.ID] = run
		return fmt.Errorf("diagnostic outcome could not be saved: %w", saveErr)
	}
	if err := s.prune(context.Background()); err != nil {
		return fmt.Errorf("diagnostic retention failed: %w", err)
	}
	out.Status("%s; result saved", run.Outcome)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if run.Status == "failed" {
		return fmt.Errorf("%s: %s", run.Outcome, run.Error)
	}
	return nil
}

func (s *Service) List(ctx context.Context) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := s.prune(ctx); err != nil {
		return nil, err
	}
	return s.store.List(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	if err := s.prune(ctx); err != nil {
		return Run{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) Save(ctx context.Context, id, name string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	if err := s.prune(ctx); err != nil {
		return Run{}, err
	}
	name, err := validName(name)
	if err != nil {
		return Run{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := s.store.rename(ctx, id, name, s.now()); err != nil {
		return Run{}, err
	}
	return s.store.Get(ctx, id)
}

func (s *Service) Rerun(ctx context.Context, id, actor string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	if err := s.prune(ctx); err != nil {
		return Run{}, err
	}
	previous, err := s.store.Get(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if !terminal(previous.Status) {
		return Run{}, ErrRunning
	}
	return s.create(ctx, previous.Name, previous.Request, actor, id)
}

func (s *Service) Cancel(ctx context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Run{}, err
	}
	run, err := s.store.Get(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if terminal(run.Status) {
		return Run{}, ErrNotRunning
	}
	entry := s.active[id]
	if entry == nil {
		return Run{}, ErrUnavailable
	}
	run.Status, run.UpdatedAt = "cancelling", s.now()
	err = s.store.update(ctx, run)
	s.jobs.Cancel(entry.jobID)
	if err != nil {
		return Run{}, fmt.Errorf("%w: cancellation was signalled but could not be recorded: %v", ErrUnavailable, err)
	}
	return run, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	if err := s.prune(ctx); err != nil {
		return err
	}
	run, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !terminal(run.Status) {
		return ErrRunning
	}
	return s.store.remove(ctx, id)
}

func (s *Service) Retention(ctx context.Context) (Retention, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return Retention{}, err
	}
	return s.store.retention(ctx)
}

func (s *Service) SetRetention(ctx context.Context, policy Retention) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ready(); err != nil {
		return err
	}
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := s.flush(ctx); err != nil {
		return err
	}
	if err := s.store.saveRetention(ctx, policy); err != nil {
		return err
	}
	return s.store.prune(ctx, policy, s.now())
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.stopping = true
	var pending []chan struct{}
	for _, entry := range s.active {
		entry.shutdown = true
		s.jobs.Cancel(entry.jobID)
		pending = append(pending, entry.done)
	}
	s.mu.Unlock()
	for _, done := range pending {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Detailed protocol stages are not available from every legacy tool. Keep the
// classification provenance rather than treating an error-text match as proof.
func outcome(ctxErr error, result *netsec.ProbeResult, runErr error) (string, string) {
	if errors.Is(ctxErr, context.Canceled) {
		return "cancelled", "context"
	}
	if errors.Is(ctxErr, context.DeadlineExceeded) {
		return "timed_out", "context"
	}
	message := ""
	if result != nil {
		message = result.Error
	}
	if runErr != nil {
		message = runErr.Error()
	}
	if message != "" {
		text := strings.ToLower(message)
		switch {
		case strings.Contains(text, "not installed"), strings.Contains(text, "not supported"), strings.Contains(text, "executable file not found"):
			return "unsupported", "error_text"
		case strings.Contains(text, "permission denied"), strings.Contains(text, "operation not permitted"), strings.Contains(text, "cap_net_raw"):
			return "permission_denied", "error_text"
		case strings.Contains(text, "no such host"), strings.Contains(text, "dns"), strings.Contains(text, "no mx records"):
			return "dns_failure", "error_text"
		case strings.Contains(text, "certificate"), strings.Contains(text, "x509"):
			return "invalid_certificate", "error_text"
		case strings.Contains(text, "refused"):
			return "refused", "error_text"
		case strings.Contains(text, "timed out"), strings.Contains(text, "timeout"), strings.Contains(text, "deadline"):
			return "timed_out", "error_text"
		default:
			return "failed", "tool_result"
		}
	}
	if result == nil || !result.OK {
		return "failed", "tool_result"
	}
	if (result.Tool == "dnsbl" || result.Tool == "httpsec") && len(result.Records) > 0 {
		return "completed_with_findings", "tool_result"
	}
	return "completed", "tool_result"
}
