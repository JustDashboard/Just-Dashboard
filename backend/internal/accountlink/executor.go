package accountlink

import (
	"context"
	"database/sql"
	"errors"
)

// ErrVersionMoved is a dashboard.update.install for a version that is no longer
// the newest. The site is told, and decides what to ask next.
var ErrVersionMoved = errors.New("version_moved")

// Host is everything this package needs from the rest of the dashboard. The api
// package implements it over the services the dashboard's own pages use, so a
// command from the site and a click in the browser take the same code path;
// nothing here reaches Docker, the backups or the updater by itself.
//
// The actor each action receives names who asked, for the records those
// services keep ("account:ion@example.com"); the audit entry for the command
// itself is written here.
type Host interface {
	// Collect reads what a heartbeat reports, as completely as it can. A part
	// it cannot read is left out rather than failing the whole.
	Collect(ctx context.Context) (Snapshot, error)
	// UpdateStatus is where the update checker and installer stand.
	UpdateStatus(ctx context.Context) (UpdateStatus, error)

	Container(ctx context.Context, actor, name, action string) (string, error)
	RestartStack(ctx context.Context, actor, name string) (string, error)
	RunBackup(ctx context.Context, actor string, jobID int64) (string, error)
	// InstallUpdate must return an error wrapping ErrVersionMoved when version
	// is not the newest.
	InstallUpdate(ctx context.Context, actor, version string) (string, error)
	RestartDashboard(ctx context.Context, actor string, rebuild bool) (string, error)
}

// Refuse is how a Host declines a command it must not run: the result is
// reported to the site as refused, with this reason, and not as a failure.
func Refuse(format string, a ...any) error { return refuse(format, a...) }

// The container actions Host.Container takes.
const (
	ContainerStart   = "start"
	ContainerStop    = "stop"
	ContainerRestart = "restart"
)

// execute is the closed switch over command kinds. It is only reached with
// arguments that verify and parseArgs have accepted, and every case calls one
// service that already exists; there is no kind that runs a string.
func (s *Service) execute(ctx context.Context, cmd Command, a Args, actor string) (string, error) {
	switch cmd.Kind {
	case KindSnapshotNow:
		s.kickHeartbeat()
		return "heartbeat queued", nil
	case KindContainerStart:
		return s.host.Container(ctx, actor, a.Container, ContainerStart)
	case KindContainerStop:
		return s.host.Container(ctx, actor, a.Container, ContainerStop)
	case KindContainerReboot:
		return s.host.Container(ctx, actor, a.Container, ContainerRestart)
	case KindStackRestart:
		return s.host.RestartStack(ctx, actor, a.Name)
	case KindBackupRun:
		return s.host.RunBackup(ctx, actor, a.JobID)
	case KindUpdateInstall:
		return s.host.InstallUpdate(ctx, actor, a.Version)
	case KindUpdatePolicy:
		return s.setPolicy(ctx, a.Policy)
	case KindRestart:
		return s.host.RestartDashboard(ctx, actor, a.Rebuild)
	}
	return "", refuse("the kind %q is not one this dashboard runs", clip(cmd.Kind, 64))
}

// setPolicy stores the update policy this dashboard will evaluate itself. A
// new policy gets a fresh chance at the version it names: whatever an earlier
// one tried and failed is forgotten.
func (s *Service) setPolicy(ctx context.Context, p UpdatePolicy) (string, error) {
	if err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		if err := putTx(ctx, tx, keyPolicy, p); err != nil {
			return err
		}
		return delTx(ctx, tx, keyAttempt)
	}); err != nil {
		return "", err
	}
	if p.Auto == AutoOff {
		return "automatic updates are off", nil
	}
	return "automatic updates: " + p.Auto + " between " + p.Window.Start + " and " + p.Window.End + " server time", nil
}

func (s *Service) policy(ctx context.Context) (UpdatePolicy, bool) {
	var p UpdatePolicy
	ok, err := s.kv.get(ctx, keyPolicy, &p)
	if err != nil || !ok || p.validate() != nil {
		return UpdatePolicy{}, false
	}
	return p, true
}
