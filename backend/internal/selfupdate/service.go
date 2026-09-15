package selfupdate

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Service is the module the API holds: the check, the installer, and the one
// question a handler actually asks — "where does this install stand".
type Service struct {
	current   string
	dataDir   string
	updateDir string
	checker   *Checker
	installer *Installer
	store     *Store
	log       *slog.Logger

	// list is how this package sees the host's containers. It is a function
	// rather than a Docker client so that locating an install — the part with
	// all the decisions in it — is testable without a daemon.
	list Lister
	// health is the URL the updater probes once the stack is back up. Derived
	// from the address this server binds, since that is the address the
	// rebuilt one will bind too.
	health string

	mu     sync.Mutex
	loc    *Location
	locErr error
	locAt  time.Time
}

// Options is what the API knows and this package does not.
type Options struct {
	// Current is the version running, i.e. version.Version.
	Current string
	DataDir string
	// UpdateDir overrides the checkout discovery entirely. Empty is the normal
	// case: the dashboard asks Docker where it was deployed from.
	UpdateDir   string
	Repo        string
	Ref         string
	CheckOnline bool
	DockerHost  string
	// Health is the URL that answers once the dashboard is back — built from
	// the bind address, because the updater has no configuration of its own.
	Health string
	List   Lister
	Log    *slog.Logger
	// BaseURL overrides the raw-content host the changelog is read from.
	// Empty everywhere but the tests, and deliberately not reachable from the
	// environment: JD_UPDATE_REPO covers running a fork, while an arbitrary
	// host setting would be a way to make a dashboard install somebody else's
	// code, which is not a feature this product wants.
	BaseURL string
}

func New(o Options) *Service {
	store := NewStore(o.DataDir)
	checker := NewChecker(o.Current, o.Repo, o.Ref, o.CheckOnline, o.Log)
	if o.BaseURL != "" {
		checker.baseURL = strings.TrimSuffix(o.BaseURL, "/")
	}
	return &Service{
		current:   Normalise(o.Current),
		dataDir:   o.DataDir,
		updateDir: o.UpdateDir,
		checker:   checker,
		installer: NewInstaller(store, o.DataDir, o.DockerHost, o.Log),
		store:     store,
		list:      o.List,
		health:    o.Health,
		log:       o.Log,
	}
}

// Start begins the periodic check and settles any upgrade that was in flight
// when this process began — which, after a successful upgrade, is every time.
func (s *Service) Start(ctx context.Context) {
	s.installer.Reconcile(ctx, s.current, s.list)
	s.checker.Start(ctx)
}

func (s *Service) Stop() { s.checker.Stop() }

// CheckMeta is the how and when of the last check, separate from its answer so
// the UI can say "checked eleven minutes ago, and could not reach GitHub"
// without that reading as "there is no update".
type CheckMeta struct {
	Enabled bool `json:"enabled"`
	// A pointer, because `omitempty` does not omit a zero time.Time — it is a
	// struct — and an install that has never checked was therefore sending
	// 0001-01-01 down the wire, which the UI rendered as "checked 739855 days
	// ago". Nil is the honest shape for "never", and the TypeScript side has
	// always declared it optional.
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Error     string     `json:"error,omitempty"`
	Source    string     `json:"source,omitempty"`
	Repo      string     `json:"repo"`
	Ref       string     `json:"ref"`
}

// Install is whether this particular install can upgrade itself, and if not,
// why not. A dashboard running from a binary on a systemd unit is a perfectly
// good install; it simply has no compose stack to rebuild, and it is told that
// rather than shown a button that fails.
type Install struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	Dir       string `json:"dir,omitempty"`
	Compose   string `json:"compose,omitempty"`
	// Dirty lists uncommitted changes in the checkout, in `git status
	// --porcelain` form. A warning, not a bar: the upgrade fast-forwards, so a
	// local edit survives unless it collides.
	Dirty []string `json:"dirty,omitempty"`
}

// Report is the whole answer, and the only thing the API serves.
type Report struct {
	Version   string `json:"version"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	// Releases are the versions between the installed one and the newest,
	// newest first. This is what "View changes" shows.
	Releases []Release `json:"releases"`
	// History is what this build knows about its own past. It needs no network
	// and is what the changelog reads on an install that is up to date.
	History  []Release `json:"history"`
	Breaking bool      `json:"breaking"`
	Check    CheckMeta `json:"check"`
	Install  Install   `json:"install"`
	Run      *Run      `json:"run,omitempty"`
	Log      string    `json:"log,omitempty"`
}

// Freshness is how hard the caller wants the online check chased.
type Freshness int

const (
	// Cached is the ordinary poll: read what is known, and start a check only
	// if the answer has gone properly stale.
	Cached Freshness = iota
	// OnLoad is a browser opening the dashboard. It still answers from the
	// cache immediately — nothing about a page load should wait on the
	// network — but it starts a check on a much shorter staleness floor, so
	// the answer an operator gets is at most a few minutes old rather than a
	// few hours. This is the whole of "check for updates on every reload".
	OnLoad
	// Forced is somebody pressing "check now", where waiting is what they
	// asked for.
	Forced
)

// nonZeroTime is nil for a time nobody has recorded.
func nonZeroTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// Report answers where this install stands.
func (s *Service) Report(ctx context.Context, freshness Freshness) Report {
	var check Check
	switch freshness {
	case Forced:
		check, _ = s.checker.Refresh(ctx)
	case OnLoad:
		check = s.checker.Nudge(nudgeInterval)
	default:
		check = s.checker.Nudge(checkInterval)
	}

	local := Local()
	rep := Report{
		Version:   s.current,
		Latest:    check.Latest,
		Available: check.Available,
		Releases:  check.Releases,
		History:   local.Releases,
		Breaking:  HasBreaking(check.Releases),
		Check: CheckMeta{
			Enabled:   check.Enabled,
			CheckedAt: nonZeroTime(check.CheckedAt),
			Error:     check.Error,
			Source:    check.Source,
			Repo:      s.checker.Repo(),
			Ref:       s.checker.Ref(),
		},
	}
	if rep.Releases == nil {
		rep.Releases = []Release{}
	}
	// An install that has never managed a check still knows its own version,
	// so "latest" falls back to that rather than being blank — the sidebar
	// then reads "up to date as far as we can tell" instead of "unknown".
	if rep.Latest == "" {
		rep.Latest = s.current
	}

	run, err := s.store.Load()
	if err == nil && run != nil {
		rep.Run = run
		rep.Log = s.store.Tail()
	}

	loc, locErr := s.Location(ctx)
	switch {
	case locErr != nil:
		rep.Install = Install{Supported: false, Reason: locErr.Error()}
	default:
		rep.Install = Install{Supported: true, Dir: loc.Dir, Compose: loc.Compose}
		// Only worth the subprocess when there is something to install and
		// nothing already running: this report is polled every couple of
		// seconds during an upgrade, and `git status` on every poll would be
		// a subprocess per tick for an answer nobody can act on.
		if rep.Available && (rep.Run == nil || !rep.Run.Status.Live()) {
			rep.Install.Dirty = Dirty(ctx, loc)
		}
	}
	return rep
}

// locationTTL is how long the discovered checkout is trusted. It is a Docker
// round trip plus a few stats, and the answer changes only when somebody moves
// their install.
const locationTTL = 2 * time.Minute

// Location is where this install lives, cached.
func (s *Service) Location(ctx context.Context) (*Location, error) {
	s.mu.Lock()
	if time.Since(s.locAt) < locationTTL && (s.loc != nil || s.locErr != nil) {
		loc, err := s.loc, s.locErr
		s.mu.Unlock()
		return loc, err
	}
	s.mu.Unlock()

	loc, err := Locate(ctx, s.updateDir, s.dataDir, s.list)

	s.mu.Lock()
	s.loc, s.locErr, s.locAt = loc, err, time.Now()
	s.mu.Unlock()
	return loc, err
}

// Install starts an upgrade to target. It returns the recorded run, which the
// caller hands straight back to a browser that is about to lose contact with
// this process.
func (s *Service) Install(ctx context.Context, target, actor string) (*Run, error) {
	loc, err := s.Location(ctx)
	if err != nil {
		return nil, err
	}
	run, err := s.installer.Start(ctx, loc, StartRequest{
		From:   s.current,
		To:     Normalise(target),
		Ref:    s.checker.Ref(),
		Health: s.health,
		Actor:  actor,
	})
	if err != nil {
		return nil, err
	}
	// The location cache is dropped so the next report re-reads the checkout
	// rather than reporting a `git status` taken before the upgrade.
	s.mu.Lock()
	s.locAt = time.Time{}
	s.mu.Unlock()
	return run, nil
}

// Dismiss forgets a finished run, which is what clears the "updated to 0.6"
// notice once it has been read. A run still in flight is left alone.
func (s *Service) Dismiss() error {
	release, err := LockLifecycle(s.installer.dataDir)
	if err != nil {
		return err
	}
	defer release()

	run, err := s.store.Load()
	if err != nil {
		// A corrupt record is exactly the one worth being able to clear.
		return s.store.Clear()
	}
	if run == nil {
		return nil
	}
	if run.Status.Live() {
		return ErrInProgress
	}
	return s.store.Clear()
}

// Current is the version this build is.
func (s *Service) Current() string { return s.current }
