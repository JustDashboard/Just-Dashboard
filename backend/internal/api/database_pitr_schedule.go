package api

import (
	"context"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/jobs"
	"github.com/docker/docker/errdefs"
	"github.com/robfig/cron/v3"
)

// The base backups of every archive a server writes into, on the archive's
// own schedule. A firing is the same job a page starts, so a scheduled base
// backup shows on the page while it runs and the next is refused while one
// is under way.

type pitrSchedule struct {
	mu      sync.Mutex
	cron    *cron.Cron
	entries map[int64]cron.EntryID
}

func (s *Server) startPITRSchedule(ctx context.Context) {
	s.pitr.sched.mu.Lock()
	// The parser the backup jobs use, so a schedule means the same here.
	s.pitr.sched.cron = cron.New(cron.WithParser(cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)))
	s.pitr.sched.entries = map[int64]cron.EntryID{}
	s.pitr.sched.cron.Start()
	s.pitr.sched.mu.Unlock()
	s.reloadPITRSchedule(ctx)
}

func (s *Server) stopPITRSchedule() {
	s.pitr.sched.mu.Lock()
	defer s.pitr.sched.mu.Unlock()
	if s.pitr.sched.cron != nil {
		s.pitr.sched.cron.Stop()
	}
}

// reloadPITRSchedule rebuilds the schedule from the archives, the one source
// of what will fire.
func (s *Server) reloadPITRSchedule(ctx context.Context) {
	archives, err := s.activePITRArchives(ctx)
	if err != nil {
		s.Log.Warn("the base backup schedule could not be read", "err", err)
		return
	}
	s.pitr.sched.mu.Lock()
	defer s.pitr.sched.mu.Unlock()
	if s.pitr.sched.cron == nil {
		return
	}
	for id, entry := range s.pitr.sched.entries {
		s.pitr.sched.cron.Remove(entry)
		delete(s.pitr.sched.entries, id)
	}
	for _, a := range archives {
		if a.Schedule == "" {
			continue
		}
		id := a.ID
		entry, err := s.pitr.sched.cron.AddFunc(a.Schedule, func() { s.scheduledPITRBackup(id) })
		if err != nil {
			s.Log.Error("invalid base backup schedule", "container", a.Container, "schedule", a.Schedule, "err", err)
			continue
		}
		s.pitr.sched.entries[id] = entry
	}
}

// nextPITRBackup is when an archive's next base backup is due.
func (s *Server) nextPITRBackup(archive int64) *time.Time {
	s.pitr.sched.mu.Lock()
	defer s.pitr.sched.mu.Unlock()
	if s.pitr.sched.cron == nil {
		return nil
	}
	entry, ok := s.pitr.sched.entries[archive]
	if !ok {
		return nil
	}
	next := s.pitr.sched.cron.Entry(entry).Next
	if next.IsZero() {
		return nil
	}
	next = next.UTC()
	return &next
}

// scheduledPITRBackup is one firing: a base backup job, unless the server
// has another job running.
func (s *Server) scheduledPITRBackup(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a, err := s.pitrArchiveByID(ctx, id)
	if err != nil || a.EndedAt != nil {
		return
	}
	// The server was removed — its container, and with it the tool and the
	// keys it archived with. Its archive stays, with every moment it holds,
	// and is no longer written to.
	if _, err := s.modules.docker.Inspect(ctx, a.Container); errdefs.IsNotFound(err) {
		if err := s.endPITRArchive(ctx, a.ID); err == nil {
			s.Log.Info("an archive's server is gone; its archive is kept and no longer scheduled", "container", a.Container)
			s.reloadPITRSchedule(ctx)
		}
		return
	}
	base := audit.Entry{Actor: "system", Username: "schedule", Method: "SCHEDULE", Path: "pitr/" + a.Container}
	_, started := s.startPITR(a.Container, jobs.Spec{
		Kind: pitrJobPrefix(a.Container) + "backup", Title: "Base backup of " + a.Container,
		Target: a.Container, Timeout: pitrBackupTimeout, StartedBy: "schedule",
	}, func(ctx context.Context, out jobs.Emitter) error {
		srv := s.pitrServerNamed(ctx, a.Container)
		err := s.runPITRBackup(ctx, srv, a, out)
		s.recordTransfer(base, "database.pitr.backup.finish", a.Container, map[string]any{"archive": a.ID, "scheduled": true}, err)
		return err
	})
	if !started {
		s.Log.Info("a scheduled base backup was skipped: another job is running for the server", "container", a.Container)
	}
}
