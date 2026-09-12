package selfcfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/portalloc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
)

// Starting a restart from inside the thing being restarted.
//
// The same manoeuvre as internal/selfupdate's installer, and the reasoning is
// identical: `docker compose up -d` on this stack recreates the container
// running the command, so a child process would be killed halfway through and
// leave a half-applied configuration nobody can see. A sibling container is
// untouched by its own stack being recreated around it.

// RestartContainer is the fixed name of the sibling. Fixed, so a backend that
// has just been restarted can find the container that restarted it.
const RestartContainer = "just-dashboard-reconfigure"

const restartLabel = "com.just-dashboard.role=reconfigure"

// staleAfter is when a run whose sibling cannot be found is given up on.
const staleAfter = 30 * time.Minute

var (
	// ErrInProgress is a second restart asked for while one is running. It is
	// refused rather than queued: two processes running `docker compose up`
	// against one project is how a stack ends up with containers from two
	// different configurations.
	ErrInProgress = errors.New("a restart is already running")
	// ErrNoLocation is an install this cannot manage — no compose project to
	// recreate, so nothing here can work.
	ErrNoLocation = errors.New("this install cannot restart itself")
)

// Applier starts restarts and settles the ones it finds half-finished.
type Applier struct {
	store      *Store
	dataDir    string
	socket     string
	dockerHost string
	log        *slog.Logger
}

func NewApplier(store *Store, dataDir, dockerHost string, log *slog.Logger) *Applier {
	a := &Applier{store: store, dataDir: dataDir, dockerHost: dockerHost, log: log}
	if path, ok := strings.CutPrefix(dockerHost, "unix://"); ok {
		a.socket = path
	}
	return a
}

// StartRequest is everything a restart needs that this package cannot work out
// for itself.
type StartRequest struct {
	Action  Action
	Changes []Change
	// EnvPath and Backup are empty for a plain restart or rebuild, which write
	// nothing and therefore have nothing to undo.
	EnvPath string
	Backup  string
	// Health is the new configuration's probe; RollbackHealth is the outgoing
	// one's, kept for the recovery path.
	Health         string
	RollbackHealth string
	Endpoint       string
	// Note is a caveat about the configuration being applied, recorded on the
	// run so the page that watches the restart can say it afterwards.
	Note  string
	Actor string
}

// Start launches the sibling and returns as soon as it is running.
//
// It cannot wait: the caller is an HTTP handler whose response has to reach a
// browser that is about to lose contact with this process. The record on disk
// is written first, because a `docker run` that succeeds and is then never
// recorded is a restart nobody can watch.
func (a *Applier) Start(ctx context.Context, loc *selfupdate.Location, req StartRequest) (*Run, error) {
	if loc == nil {
		return nil, ErrNoLocation
	}
	if prev, err := a.store.Load(); err == nil && prev != nil && prev.Status.Live() {
		return nil, ErrInProgress
	}

	run := &Run{
		ID:             time.Now().UTC().Format("20060102T150405Z"),
		Status:         StatusPending,
		Phase:          PhaseQueued,
		Action:         req.Action,
		Changes:        req.Changes,
		Dir:            loc.Dir,
		Compose:        loc.Compose,
		Image:          loc.Image,
		EnvPath:        req.EnvPath,
		Backup:         req.Backup,
		Health:         req.Health,
		RollbackHealth: req.RollbackHealth,
		Endpoint:       req.Endpoint,
		Note:           req.Note,
		Container:      RestartContainer,
		Actor:          req.Actor,
		StartedAt:      time.Now().UTC(),
	}

	if f, err := a.store.OpenLog(); err == nil {
		fmt.Fprintf(f, "%s requested by %s\n", headline(run), run.Actor)
		fmt.Fprintf(f, "stack: %s (%s)\n", run.Dir, run.Compose)
		for _, c := range run.Changes {
			fmt.Fprintf(f, "  %s: %s → %s\n", c.Label, c.From, c.To)
		}
		if run.Note != "" {
			fmt.Fprintf(f, "note: %s\n", run.Note)
		}
		fmt.Fprintln(f)
		f.Close()
	}
	if err := a.store.Save(run); err != nil {
		return nil, fmt.Errorf("record the restart: %w", err)
	}

	_ = exec.CommandContext(ctx, "docker", "rm", "-f", RestartContainer).Run()

	cmd := exec.CommandContext(ctx, "docker", a.siblingArgs(run)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(out.String())
		if detail == "" {
			detail = err.Error()
		}
		// The .env has already been written at this point, so a sibling that
		// never starts would leave the stack running the old configuration and
		// the file describing the new one — a disagreement that surfaces weeks
		// later as "I changed that and it did nothing". Put the file back.
		restored := a.restoreEnv(run)
		_ = a.store.Finish(fmt.Errorf("the restart could not be started: %s%s", detail, restored), false)
		return nil, fmt.Errorf("could not start the restart container: %s", detail)
	}
	a.log.Warn("dashboard restart started",
		"action", run.Action, "dir", run.Dir, "actor", run.Actor, "changes", len(run.Changes))
	return run, nil
}

// restoreEnv puts the previous file back when the sibling never ran. It
// returns a sentence for the error the operator will read, because "it did not
// start" and "it did not start, and your settings were kept" are different
// facts and only one of them is true here.
func (a *Applier) restoreEnv(run *Run) string {
	if run.Backup == "" || run.EnvPath == "" {
		return ""
	}
	saved, err := os.ReadFile(run.Backup)
	if err != nil {
		return fmt.Sprintf(" — the new settings are written to %s but nothing has restarted into them", run.EnvPath)
	}
	if err := os.WriteFile(run.EnvPath, saved, 0o600); err != nil {
		return fmt.Sprintf(" — the new settings are written to %s but nothing has restarted into them", run.EnvPath)
	}
	return " — the previous settings were put back, and nothing about the running dashboard changed"
}

// siblingArgs builds the container's argv.
//
// A pure function, and tested as one: every way this feature can fail in a way
// the operator cannot recover from is a wrong flag here.
func (a *Applier) siblingArgs(run *Run) []string {
	args := []string{
		"run", "--detach",
		"--name", RestartContainer,
		"--label", restartLabel,
		// Host network, so the health probe reaches a loopback port on the
		// host rather than one inside a container namespace.
		"--network", "host",
		"--pid", "host",
		// Never restarted: a half-applied configuration replayed at boot is a
		// second unattended restart nobody asked for.
		"--restart", "no",
	}
	if a.socket != "" {
		args = append(args, "-v", a.socket+":/var/run/docker.sock")
	} else if a.dockerHost != "" {
		args = append(args, "-e", "DOCKER_HOST="+a.dockerHost)
	}
	args = append(args,
		"-v", run.Dir+":"+run.Dir,
		"-v", a.dataDir+":"+a.dataDir,
		"-w", run.Dir,
		"--entrypoint", "/usr/local/bin/just-dashboard",
	)
	// Nothing from this process's environment is passed: the sibling needs no
	// secret, and the master key sitting in the environment of a container
	// anyone with the socket can inspect is what main.scrubSecretEnv exists to
	// prevent.
	args = append(args, run.Image, "-self-restart", "-state-dir", a.dataDir)
	return args
}

// Reconcile settles a run that was in flight when this process started — which,
// after a successful restart, is every time.
//
// The logic mirrors the updater's, with one addition: a restart has no version
// to compare against, so "did it work" is answered by asking whether this
// process is running the configuration the run was aiming at. It is, if it is
// running at all and its own bind address matches — this code is executing
// inside the container the restart recreated.
func (a *Applier) Reconcile(ctx context.Context, list selfupdate.Lister) {
	run, err := a.store.Load()
	if err != nil || run == nil || !run.Status.Live() {
		return
	}
	alive, err := a.siblingAlive(ctx, list)
	switch {
	case err == nil && alive:
		// Still working. Nothing to settle: the sibling owns the record until
		// it finishes, and this process reading it is not a reason to touch it.
		return
	case err != nil && time.Since(run.StartedAt) < staleAfter:
		// Docker could not be asked. Saying nothing is right; marking the run
		// failed would be a guess presented as a fact.
		return
	}
	_ = a.store.Finish(errors.New(
		"the restart container stopped without recording an outcome — this dashboard is running, "+
			"so check its settings below against what you asked for"), false)
	a.log.Warn("dashboard restart did not record an outcome", "run", run.ID)
}

func (a *Applier) siblingAlive(ctx context.Context, list selfupdate.Lister) (bool, error) {
	if list == nil {
		return false, errors.New("no container listing")
	}
	all, err := list(ctx)
	if err != nil {
		return false, err
	}
	for _, c := range all {
		if c.Name != RestartContainer {
			continue
		}
		return c.State == "running" || c.State == "created" || c.State == "restarting", nil
	}
	return false, nil
}

func headline(run *Run) string {
	switch run.Action {
	case ActionRebuild:
		return "Rebuilding Just Dashboard"
	case ActionApply:
		return "Applying new dashboard settings"
	default:
		return "Restarting Just Dashboard"
	}
}

// PortFree reports whether a TCP port can be taken on this host.
//
// Check the host namespace conservatively across IPv4 interfaces. The lifecycle
// runner then verifies ownership and the exact bind addresses before startup.
func PortFree(port int) error {
	return portalloc.Available("0.0.0.0", "tcp", port)
}
