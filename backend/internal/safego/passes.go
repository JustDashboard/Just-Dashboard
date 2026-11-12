package safego

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	// A loop that keeps failing says so again this often, so the reason is
	// still in a log that has rotated since the first line.
	failingReminder = time.Hour
	// A failure whose text differs from the last one logged is a new thing to
	// say, but no more often than this: an error that carries an id or a
	// time is different on every pass.
	changedFailureEvery = time.Minute
)

// Passes says in the log how a loop's passes are going, by change of state:
// the first failure, a failure that has changed, a reminder each hour while it
// lasts, and the recovery with how long it took. A loop that runs every five
// seconds can then report every pass without writing a line every five
// seconds, which is why such loops used to throw the error away instead.
//
// One belongs to one loop and is made where the loop begins:
//
//	passes := safego.NewPasses(log, "git branch watch")
//	for { passes.Report(ctx, w.poll(ctx)); ... }
type Passes struct {
	log  *slog.Logger
	name string
	now  func() time.Time

	mu       sync.Mutex
	failing  bool
	since    time.Time
	failed   int
	logged   string
	loggedAt time.Time
}

// NewPasses is the reporter for the loop called name, the phrase its task is
// started under. A nil log is the process's default.
func NewPasses(log *slog.Logger, name string) *Passes {
	return &Passes{log: log, name: name, now: time.Now}
}

// Report takes what one pass returned: nil for a pass that worked. A pass cut
// short because ctx ended (shutdown, Stop) is neither.
func (p *Passes) Report(ctx context.Context, err error) {
	if p == nil || ctx.Err() != nil {
		return
	}
	log := p.log
	if log == nil {
		log = slog.Default()
	}
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		if p.failing {
			log.Info("background task recovered", "task", p.name,
				"after", now.Sub(p.since).Round(time.Second).String(), "failed_passes", p.failed)
		}
		p.failing, p.failed, p.logged = false, 0, ""
		return
	}
	message := err.Error()
	p.failed++
	switch {
	case !p.failing:
		p.failing, p.since = true, now
		log.Warn("background task failed; it keeps trying and says so again when this changes",
			"task", p.name, "error", message)
	case message != p.logged && now.Sub(p.loggedAt) >= changedFailureEvery,
		now.Sub(p.loggedAt) >= failingReminder:
		log.Warn("background task is still failing", "task", p.name, "error", message,
			"since", p.since.UTC().Format(time.RFC3339), "failed_passes", p.failed)
	default:
		return
	}
	p.logged, p.loggedAt = message, now
}
