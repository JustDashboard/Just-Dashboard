package backups

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Scheduler drives cron-scheduled jobs. The schedule set is rebuilt from the
// database whenever a job changes, so there is exactly one source of truth and
// no drift between what is stored and what will fire.
type Scheduler struct {
	store  *Store
	runner *Runner
	log    *slog.Logger

	mu      sync.Mutex
	cron    *cron.Cron
	entries map[int64]cron.EntryID
}

func NewScheduler(store *Store, runner *Runner, log *slog.Logger) *Scheduler {
	return &Scheduler{
		store:   store,
		runner:  runner,
		log:     log,
		entries: map[int64]cron.EntryID{},
	}
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	// Descriptors ("@daily") and standard five-field expressions are both
	// accepted, which is what operators expect from anything cron-shaped.
	s.cron = cron.New(cron.WithParser(cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)))
	s.cron.Start()
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.Stop()
	}()
	return s.Reload(ctx)
}

func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron != nil {
		s.cron.Stop()
	}
}

// Reload rebuilds every schedule from the stored jobs.
func (s *Scheduler) Reload(ctx context.Context) error {
	jobs, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron == nil {
		return nil
	}
	for id, entryID := range s.entries {
		s.cron.Remove(entryID)
		delete(s.entries, id)
	}
	for _, job := range jobs {
		if !job.Enabled || job.Schedule == "" {
			continue
		}
		id := job.ID
		name := job.Name
		entryID, err := s.cron.AddFunc(job.Schedule, func() {
			// Each firing gets its own bounded context: a hung transfer must
			// not hold the slot forever and block the next run.
			runCtx, cancel := context.WithTimeout(context.Background(), 12*time.Hour)
			defer cancel()
			if _, err := s.runner.Execute(runCtx, id, "schedule"); err != nil {
				s.log.Error("scheduled backup failed", "job", name, "err", err)
			}
		})
		if err != nil {
			s.log.Error("invalid backup schedule", "job", name, "schedule", job.Schedule, "err", err)
			continue
		}
		s.entries[id] = entryID
	}
	return nil
}

// NextRun reports when a job will next fire, for display alongside its history.
func (s *Scheduler) NextRun(jobID int64) *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cron == nil {
		return nil
	}
	entryID, ok := s.entries[jobID]
	if !ok {
		return nil
	}
	entry := s.cron.Entry(entryID)
	if entry.Next.IsZero() {
		return nil
	}
	next := entry.Next.UTC()
	return &next
}

var scheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ValidateSchedule is used by the API so a bad expression is rejected at save
// time rather than silently never firing.
func ValidateSchedule(expr string) error {
	if expr == "" {
		return nil
	}
	_, err := scheduleParser.Parse(expr)
	return err
}

// ScheduleInterval is the gap between two consecutive firings after `from`,
// or zero for a job that has no schedule. It is what "overdue" is measured
// against: a nightly job is late after two nights, an hourly one after two
// hours.
func ScheduleInterval(expr string, from time.Time) time.Duration {
	if expr == "" {
		return 0
	}
	schedule, err := scheduleParser.Parse(expr)
	if err != nil {
		return 0
	}
	first := schedule.Next(from)
	second := schedule.Next(first)
	if first.IsZero() || second.IsZero() {
		return 0
	}
	return second.Sub(first)
}

// IsOverdue reads a job's history against its schedule. A job is overdue
// once two intervals have passed without a successful run — one missed
// firing is a transient, two is a job nobody would notice had stopped.
func (j *Job) IsOverdue(now time.Time) bool {
	if !j.Enabled || j.Schedule == "" {
		return false
	}
	interval := ScheduleInterval(j.Schedule, now)
	if interval <= 0 {
		return false
	}
	since := j.CreatedAt
	if j.LastSuccessAt != nil {
		since = *j.LastSuccessAt
	}
	return now.Sub(since) > 2*interval
}
