package deploy

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
)

// The release retention sweep.
//
// ArtifactRetentionPlan has always said which release images the policy
// keeps — the live release, the five before it that went live, pins,
// candidates, a queued promotion's source, a failed release's first week —
// and PruneArtifacts could remove the rest, but nothing called it, so every
// deploy added its image for good (about 1.6 GB for a Next.js release). The
// sweep calls it a minute after start-up, once a day, and after every run
// that finishes, one pass at a time and at most retentionSweepLimit
// removals a pass, each audited.
//
// It is a destructive automatic action, so what it may remove is narrow
// twice over: the plan keeps every release a container may still be on the
// server for (heldRuntimeStates) and the target of an unfinished rollback,
// and before each removal the remover asks the daemon, refusing an image
// any container uses in any state and a name the dashboard did not give
// (DockerArtifactRemover). Nothing is removed while any deployment holds a
// lease (reserveArtifactPrune).

const (
	// retentionSweepLimit bounds the removals of one pass; the next pass
	// carries on.
	retentionSweepLimit = 20
	// retentionSweepBound is the longest one pass runs.
	retentionSweepBound = 10 * time.Minute
	// retentionSweepInterval is the daily pass.
	retentionSweepInterval = 24 * time.Hour
	// retentionSweepFirst is how long after start-up the first pass waits:
	// a start-up is busy enough without it.
	retentionSweepFirst = time.Minute
	// retentionSweepSettle is how long a pass asked for by a finished run
	// waits, so the run has let go of its lease, which holds every image
	// back, and the runs it finished beside have too.
	retentionSweepSettle = 30 * time.Second
)

// ArtifactPruneEvent is one removal the sweep tried, for the audit log.
type ArtifactPruneEvent struct {
	Decision ArtifactRetentionDecision
	// Trigger is what started the pass: start-up, daily or run-finished.
	Trigger string
}

// ReleaseImageSweeper runs release retention in the background.
type ReleaseImageSweeper struct {
	store   *OrchestrationStore
	remover ArtifactRemover
	record  func(context.Context, ArtifactPruneEvent)
	log     *slog.Logger

	mu   sync.Mutex
	wake chan struct{}

	limit    int
	interval time.Duration
	first    time.Duration
	settle   time.Duration
}

func NewReleaseImageSweeper(
	store *OrchestrationStore,
	remover ArtifactRemover,
	record func(context.Context, ArtifactPruneEvent),
	log *slog.Logger,
) *ReleaseImageSweeper {
	return &ReleaseImageSweeper{
		store: store, remover: remover, record: record, log: log,
		wake: make(chan struct{}, 1), limit: retentionSweepLimit,
		interval: retentionSweepInterval, first: retentionSweepFirst, settle: retentionSweepSettle,
	}
}

// Sweep runs one pass, waiting for one already under way to finish first.
func (s *ReleaseImageSweeper) Sweep(ctx context.Context, trigger string) (*ArtifactRetentionReport, error) {
	if s == nil || s.store == nil || s.remover == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, retentionSweepBound)
	defer cancel()
	report, err := s.store.PruneArtifactsWith(ctx, s.remover, PruneOptions{
		Limit: s.limit,
		OnDecision: func(ctx context.Context, decision ArtifactRetentionDecision) {
			if s.record != nil {
				s.record(ctx, ArtifactPruneEvent{Decision: decision, Trigger: trigger})
			}
		},
	})
	if report != nil && (report.Pruned > 0 || report.Failed > 0) && s.log != nil {
		s.log.Info("release image retention", "trigger", trigger, "removed", report.Pruned,
			"reclaimedBytes", report.Reclaimed, "failed", report.Failed, "deferred", report.Deferred)
	}
	return report, err
}

// DryRun says what a pass would remove now, removing nothing and writing
// nothing: the plan, with the remover's refusals read from the daemon.
func (s *ReleaseImageSweeper) DryRun(ctx context.Context) (*ArtifactRetentionReport, error) {
	if s == nil || s.store == nil || s.remover == nil {
		return nil, nil
	}
	return s.store.PruneArtifactsWith(ctx, s.remover, PruneOptions{DryRun: true})
}

// Trigger asks for a pass soon; requests while one waits are one request.
func (s *ReleaseImageSweeper) Trigger() {
	if s == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Start runs the passes until ctx ends: the first a minute after start-up,
// then daily, and one after each Trigger.
func (s *ReleaseImageSweeper) Start(ctx context.Context) {
	if s == nil || s.store == nil || s.remover == nil {
		return
	}
	safego.Go(ctx, s.log, "release image retention", func(ctx context.Context) {
		timer := time.NewTimer(s.first)
		defer timer.Stop()
		trigger := "start-up"
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			case <-s.wake:
				trigger = "run-finished"
				select {
				case <-ctx.Done():
					return
				case <-time.After(s.settle):
				}
			}
			if _, err := s.Sweep(ctx, trigger); err != nil && ctx.Err() == nil && s.log != nil {
				s.log.Warn("release image retention pass did not finish", "error", err)
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(s.interval)
			trigger = "daily"
		}
	})
}

// RunStarted and RunFinished make the sweeper a run observer: a run that
// finishes may have retired a release, or given up the lease that held
// every image back.
func (s *ReleaseImageSweeper) RunStarted(context.Context, EngineRun) {}

func (s *ReleaseImageSweeper) RunFinished(_ context.Context, run EngineRun) {
	if run.State.Terminal() {
		s.Trigger()
	}
}
