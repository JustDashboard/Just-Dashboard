package uninstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
)

// Removing the dashboard from the server it manages.
//
// The work is split across two processes, in the order the stack can lose
// them. The backend goes first while it is still serving the page that
// watches: it pauses everything that would put a deployment back — the
// engine, the Git watcher, the schedules, the ingress keeper — and, for a
// wipe, removes the deployments. Then it starts a sibling container from its
// own image, which takes the stack down and removes the rest. That half
// cannot be watched: the page asking about it is served by the stack it takes
// down, which is why its transcript also goes to DurableLog.

// SiblingContainer is the fixed name of the container that finishes the job.
const SiblingContainer = "just-dashboard-uninstall"

const siblingLabel = "com.just-dashboard.role=uninstall"

// staleAfter is when a run whose sibling cannot be found is given up on.
const staleAfter = 30 * time.Minute

// pauseBudget is how long running deployments are given to reach a point the
// engine can stop at. A build in progress is minutes, and interrupting one
// halfway is exactly what pausing is for.
const pauseBudget = 15 * time.Minute

var (
	// ErrInProgress is an uninstall, update or restart already running.
	ErrInProgress = errors.New("the dashboard is already being updated, restarted or uninstalled")
	// ErrNoLocation is an install with no compose project to take down.
	ErrNoLocation = errors.New("this install cannot uninstall itself")
	// ErrMode is a request that is neither keep nor wipe.
	ErrMode = errors.New(`mode must be "keep" or "wipe"`)
)

// Options is what the service needs from the rest of the dashboard.
type Options struct {
	DataDir    string
	BackupDir  string
	NginxDir   string
	DockerHost string
	// MasterKey is the key this process runs with, hex as in .env. A
	// reinstall running with the key a kept install left beside its data
	// has a .env of its own again, and the copy is removed.
	MasterKey string
	Locate    func(ctx context.Context) (*selfupdate.Location, error)
	List      selfupdate.Lister
	Teardown  Teardown
	// Pause stops everything that would recreate what a wipe removes, and
	// waits for running deployments to stop at a safe point.
	Pause func(ctx context.Context, out io.Writer) error
	// Resume puts the dashboard back the way it was after a run that failed
	// with the dashboard still up — paused modules cannot be restarted in
	// place, so this recreates the stack.
	Resume func(ctx context.Context) error
	// DurableLog overrides where the transcript's surviving copy goes, for a
	// test that must not write to the host's /var/log.
	DurableLog string
	Log        *slog.Logger
}

type Service struct {
	o     Options
	store *Store
	// launch starts the sibling; a field so a test can stand in for Docker.
	launch func(ctx context.Context, run *Run) error
}

func New(o Options) *Service {
	if o.DurableLog == "" {
		o.DurableLog = DurableLog
	}
	s := &Service{o: o, store: NewStore(o.DataDir)}
	s.launch = s.startSibling
	return s
}

// Request is what the operator chose.
type Request struct {
	Mode          Mode `json:"mode"`
	DeleteBackups bool `json:"deleteBackups"`
}

// Report is what the configuration page polls while a run is in flight.
type Report struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	Run       *Run   `json:"run,omitempty"`
	Log       string `json:"log,omitempty"`
}

func (s *Service) Report(ctx context.Context) Report {
	rep := Report{}
	if _, err := s.o.Locate(ctx); err != nil {
		rep.Reason = err.Error()
	} else {
		rep.Supported = true
	}
	if run, err := s.store.Load(); err == nil && run != nil {
		rep.Run = run
		rep.Log = s.store.Tail()
	}
	return rep
}

// Usage is how much is in a directory.
type Usage struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
	// Partial is a count that ran out of time before the end of the tree.
	Partial bool `json:"partial,omitempty"`
}

// Plan is everything the uninstall dialog shows before anything happens.
type Plan struct {
	// Phrase is what a wipe asks to be typed: the server's name, so the
	// operator with several says which one they are clearing.
	Phrase  string `json:"phrase"`
	Dir     string `json:"dir"`
	EnvPath string `json:"envPath"`
	Data    Usage  `json:"data"`
	Backups *Usage `json:"backups,omitempty"`
	// KeptCache is the data directory's nginx-cache, when a site still
	// caches into it and a wipe therefore leaves it.
	KeptCache        string `json:"keptCache,omitempty"`
	Deployments      Owned  `json:"deployments"`
	DeploymentsError string `json:"deploymentsError,omitempty"`
	DurableLog       string `json:"durableLog"`
}

// Plan works out what an uninstall would remove.
func (s *Service) Plan(ctx context.Context) (*Plan, error) {
	loc, err := s.o.Locate(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoLocation, err)
	}
	plan := &Plan{
		Phrase:     Phrase(),
		Dir:        loc.Dir,
		EnvPath:    filepath.Join(loc.Dir, ".env"),
		Data:       usage(s.o.DataDir, time.Second),
		DurableLog: DurableLog,
	}
	if s.o.BackupDir != "" {
		if _, err := os.Stat(s.o.BackupDir); err == nil {
			backups := usage(s.o.BackupDir, time.Second)
			plan.Backups = &backups
		}
	}
	if s.cacheInUse() {
		plan.KeptCache = filepath.Join(s.o.DataDir, nginxCache)
	}
	teardown := s.o.Teardown
	teardown.OwnProject = loc.Project
	owned, err := teardown.Inventory(ctx)
	if err != nil {
		plan.DeploymentsError = err.Error()
	}
	plan.Deployments = owned
	return plan, nil
}

// Begin admits an uninstall, records it and starts it. It returns once the
// record is written; the work carries on after the response.
func (s *Service) Begin(ctx context.Context, req Request, actor string) (*Run, error) {
	if !req.Mode.Valid() {
		return nil, ErrMode
	}
	release, err := selfupdate.LockLifecycle(s.o.DataDir)
	if err != nil {
		return nil, lifecycleError(err)
	}
	defer release()
	if err := selfupdate.CheckLifecycleIdle(s.o.DataDir); err != nil {
		return nil, lifecycleError(err)
	}
	loc, err := s.o.Locate(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoLocation, err)
	}

	run := &Run{
		ID:            time.Now().UTC().Format("20060102T150405Z"),
		Status:        StatusPending,
		Phase:         PhaseQueued,
		Mode:          req.Mode,
		DeleteBackups: req.Mode == ModeWipe && req.DeleteBackups,
		Dir:           loc.Dir,
		Compose:       loc.Compose,
		Project:       loc.Project,
		Image:         loc.Image,
		EnvPath:       filepath.Join(loc.Dir, ".env"),
		DataDir:       s.o.DataDir,
		BackupDir:     s.o.BackupDir,
		Container:     SiblingContainer,
		Actor:         actor,
		StartedAt:     time.Now().UTC(),
	}
	if run.Mode == ModeWipe && s.cacheInUse() {
		run.Keep = []string{nginxCache}
	}

	header := fmt.Sprintf("Uninstalling Just Dashboard, requested by %s\n%s\nstack: %s (%s)\n\n",
		run.Actor, modeLine(run), run.Dir, run.Compose)
	if f, err := s.store.OpenLog(); err == nil {
		_, _ = f.WriteString(header)
		f.Close()
	}
	if f, err := os.OpenFile(s.o.DurableLog, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
		_, _ = f.WriteString(header)
		f.Close()
	}
	if err := s.store.Save(run); err != nil {
		return nil, fmt.Errorf("record the uninstall: %w", err)
	}
	s.o.Log.Warn("dashboard uninstall started", "mode", run.Mode, "dir", run.Dir, "actor", run.Actor)

	teardown := s.o.Teardown
	teardown.OwnProject = loc.Project
	go s.work(run, teardown)
	return run, nil
}

// work is the backend's half: pause, remove the deployments for a wipe, and
// hand over to the sibling.
func (s *Service) work(run *Run, teardown Teardown) {
	ctx := context.Background()
	out, done := s.transcript()
	defer done()
	_ = s.store.Update(func(r *Run) {
		r.Status = StatusRunning
		r.Phase = PhasePausing
	})

	fmt.Fprintf(out, "Pausing deployments, schedules and backups\n")
	if s.o.Pause != nil {
		pauseCtx, cancel := context.WithTimeout(ctx, pauseBudget)
		err := s.o.Pause(pauseCtx, out)
		cancel()
		if err != nil {
			s.abort(ctx, out, fmt.Errorf("the deployments could not be paused: %w", err))
			return
		}
	}

	if run.Mode == ModeWipe {
		_ = s.store.Update(func(r *Run) { r.Phase = PhaseDeployments })
		fmt.Fprintf(out, "\nRemoving the deployments\n")
		owned, err := teardown.Inventory(ctx)
		if err != nil {
			s.abort(ctx, out, err)
			return
		}
		if owned.Empty() {
			fmt.Fprintln(out, "there are no deployments on this server")
		}
		if warnings := teardown.Remove(ctx, owned, out); len(warnings) > 0 {
			_ = s.store.Update(func(r *Run) { r.Warnings = append(r.Warnings, warnings...) })
		}
	}

	_ = s.store.Update(func(r *Run) { r.Phase = PhaseStack })
	fmt.Fprintf(out, "\nHanding over to %s, which stops the dashboard\n", SiblingContainer)
	launchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := s.launch(launchCtx, run)
	cancel()
	if err != nil {
		s.abort(ctx, out, err)
	}
}

// abort ends a run that failed while the dashboard is still up, and brings
// back what pausing stopped.
func (s *Service) abort(ctx context.Context, out io.Writer, err error) {
	fmt.Fprintf(out, "\nFAILED: %v\n", err)
	_ = s.store.Finish(err)
	s.o.Log.Error("dashboard uninstall failed", "error", err)
	if s.o.Resume == nil {
		return
	}
	fmt.Fprintf(out, "\nRestarting the dashboard to resume what was paused\n")
	if resumeErr := s.o.Resume(ctx); resumeErr != nil {
		fmt.Fprintf(out, "the restart could not be started: %v — restart the dashboard from Configuration\n", resumeErr)
	}
}

// transcript writes to the record's log and to the copy that outlives it.
func (s *Service) transcript() (io.Writer, func()) {
	var files []*os.File
	if f, err := s.store.AppendLog(); err == nil {
		files = append(files, f)
	}
	if f, err := os.OpenFile(s.o.DurableLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		files = append(files, f)
	}
	writers := make([]io.Writer, len(files))
	for i, f := range files {
		writers[i] = f
	}
	return io.MultiWriter(writers...), func() {
		for _, f := range files {
			f.Close()
		}
	}
}

// startSibling launches the container that finishes the job.
func (s *Service) startSibling(ctx context.Context, run *Run) error {
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", SiblingContainer).Run()
	cmd := exec.CommandContext(ctx, "docker", s.siblingArgs(run)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(out.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("could not start the uninstall container: %s", detail)
	}
	return nil
}

// siblingArgs builds the sibling's argv.
//
// It differs from the restart's sibling in two flags, both for reaching the
// host: --privileged with the host's PID namespace lets it stop the held
// terminal units and delete host paths through PID 1's root, and --rm is
// because nothing would be left afterwards to remove it.
func (s *Service) siblingArgs(run *Run) []string {
	args := []string{
		"run", "--detach", "--rm",
		"--name", SiblingContainer,
		"--label", siblingLabel,
		"--network", "host",
		"--pid", "host",
		"--privileged",
		"--restart", "no",
	}
	if socket, ok := strings.CutPrefix(s.o.DockerHost, "unix://"); ok {
		args = append(args, "-v", socket+":/var/run/docker.sock")
	} else if s.o.DockerHost != "" {
		args = append(args, "-e", "DOCKER_HOST="+s.o.DockerHost)
	}
	args = append(args,
		"-v", run.Dir+":"+run.Dir,
		"-v", s.o.DataDir+":"+s.o.DataDir,
		"-w", run.Dir,
		"--entrypoint", "/usr/local/bin/just-dashboard",
	)
	// No environment is passed: the sibling needs no secret.
	return append(args, run.Image, "-self-uninstall", "-state-dir", s.o.DataDir)
}

// Start settles a run that was in flight when this process began.
//
// A successful uninstall that left its record behind was a keep, and this
// process running is a reinstall: the record describes a dashboard that is no
// longer gone, so it is cleared. A live run whose sibling is not running
// stopped without saying how, and is closed as failed.
func (s *Service) Start(ctx context.Context) {
	run, err := s.store.Load()
	if err != nil || run == nil {
		return
	}
	if run.Status == StatusSuccess {
		_ = s.store.Clear()
		s.dropKeptEnv()
		return
	}
	if !run.Status.Live() {
		return
	}
	alive, err := s.siblingAlive(ctx)
	switch {
	case err == nil && alive:
		return
	case err != nil && time.Since(run.StartedAt) < staleAfter:
		return
	}
	_ = s.store.Finish(fmt.Errorf("the uninstall stopped during %q without recording an outcome — the dashboard "+
		"is running again, so check the transcript for how far it got", run.Phase))
	s.o.Log.Warn("dashboard uninstall did not record an outcome", "run", run.ID, "phase", run.Phase)
}

// dropKeptEnv removes the copy of .env a kept install left, once the key in
// it is the key this process is running with. Until then it may be the only
// copy of the key the data is sealed with, and it stays.
func (s *Service) dropKeptEnv() {
	path := filepath.Join(s.o.DataDir, KeptEnv)
	b, err := os.ReadFile(path)
	if err != nil || s.o.MasterKey == "" {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "JD_MASTER_KEY="+s.o.MasterKey {
			_ = os.Remove(path)
			return
		}
	}
}

func (s *Service) siblingAlive(ctx context.Context) (bool, error) {
	if s.o.List == nil {
		return false, errors.New("no container listing")
	}
	all, err := s.o.List(ctx)
	if err != nil {
		return false, err
	}
	for _, c := range all {
		if c.Name == SiblingContainer {
			return c.State == "running" || c.State == "created" || c.State == "restarting", nil
		}
	}
	return false, nil
}

// Transcript is the whole of the last run's output.
func (s *Service) Transcript() string { return s.store.Transcript() }

// Dismiss forgets a finished run.
func (s *Service) Dismiss() error {
	release, err := selfupdate.LockLifecycle(s.o.DataDir)
	if err != nil {
		return lifecycleError(err)
	}
	defer release()
	run, err := s.store.Load()
	if err != nil {
		return s.store.Clear()
	}
	if run != nil && run.Status.Live() {
		return ErrInProgress
	}
	return s.store.Clear()
}

// Phrase is the server's name, which is what typing it confirms.
func Phrase() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "just-dashboard"
	}
	return name
}

func lifecycleError(err error) error {
	if errors.Is(err, selfupdate.ErrInProgress) {
		return ErrInProgress
	}
	return err
}

func modeLine(run *Run) string {
	if run.Mode == ModeKeep {
		return "keeping the deployments, the data and the backups for a reinstall"
	}
	if run.DeleteBackups {
		return "wiping the deployments, the data and the backups"
	}
	return "wiping the deployments and the data; keeping the backups"
}

const nginxCache = "nginx-cache"

// cacheInUse reports whether a file under the nginx directory names the data
// directory's cache. Bounded, since it reads configuration on every plan.
func (s *Service) cacheInUse() bool {
	if s.o.NginxDir == "" {
		return false
	}
	cache := filepath.Join(s.o.DataDir, nginxCache)
	if _, err := os.Stat(cache); err != nil {
		return false
	}
	needle := []byte(cache)
	found := false
	seen := 0
	_ = filepath.WalkDir(s.o.NginxDir, func(path string, d fs.DirEntry, err error) error {
		if found || seen > 2000 {
			return filepath.SkipAll
		}
		if err != nil || d.IsDir() {
			return nil
		}
		seen++
		if info, err := os.Stat(path); err != nil || info.IsDir() || info.Size() > 1<<20 {
			return nil
		}
		if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, needle) {
			found = true
		}
		return nil
	})
	return found
}

// usage counts a directory, giving up at the deadline rather than holding a
// dialog open on a slow disk.
func usage(dir string, budget time.Duration) Usage {
	u := Usage{Path: dir}
	deadline := time.Now().Add(budget)
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			u.Partial = true
			return filepath.SkipAll
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				u.Files++
				u.Bytes += info.Size()
			}
		}
		return nil
	})
	return u
}
