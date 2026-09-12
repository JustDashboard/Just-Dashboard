package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Installing an update, from inside one of the things being replaced.
//
// This is the awkward part, and it is worth being explicit about why the
// obvious implementation cannot work. `docker compose up -d --build` on this
// stack rebuilds three images and then recreates three containers, one of
// which is the process that ran the command. Whatever launched it dies
// somewhere in the middle: as a child of the backend it is killed with the
// backend's cgroup, and the frontend and the proxy are then never recreated —
// leaving a half-upgraded install and a dashboard that cannot report what
// happened to it, which is the worst possible outcome for a feature whose
// whole job is to be trustworthy.
//
// So the backend does not run the upgrade. It starts a **sibling container**
// that does — a separate container created through the Docker socket, not a
// child process — and that container is untouched by its own stack being torn
// down and rebuilt around it. It writes its progress to a file in the data
// directory, which both halves mount, so the new backend can pick the story up
// where the old one left off. This is the same trick Watchtower uses to
// replace the container it is running in, for the same reason.
//
// The image the sibling runs is this dashboard's own backend image. It already
// contains git, the docker CLI and the compose plugin — nothing to pull,
// nothing extra to keep current, and an upgrade that still works on a server
// that cannot reach a registry.

// UpdaterContainer is the fixed name of the sibling. Fixed rather than
// generated so that a backend which has just been restarted into a new version
// can find the container that put it there.
const UpdaterContainer = "just-dashboard-updater"

// updaterLabel marks the container as ours, so it is identifiable in the
// Docker page as something the dashboard started rather than a stray.
const updaterLabel = "com.just-dashboard.role=updater"

// staleAfter is when a run whose updater cannot be found is given up on. It is
// generous because the work is a Go compile, a Next build and three image
// pulls on whatever hardware the operator has.
const staleAfter = 90 * time.Minute

var (
	ErrInProgress  = errors.New("an update is already running")
	ErrNotNewer    = errors.New("that version is not newer than the one installed")
	ErrNoLocation  = errors.New("this install cannot update itself in place")
	errNoContainer = errors.New("no updater container")
)

// Installer starts upgrades and reconciles the ones it finds half-finished.
type Installer struct {
	store   *Store
	dataDir string
	// socket is the host path of the Docker socket to hand the sibling, or ""
	// when Docker is addressed some other way (DOCKER_HOST is passed instead).
	socket     string
	dockerHost string
	log        *slog.Logger
}

func NewInstaller(store *Store, dataDir, dockerHost string, log *slog.Logger) *Installer {
	i := &Installer{store: store, dataDir: dataDir, dockerHost: dockerHost, log: log}
	if path, ok := strings.CutPrefix(dockerHost, "unix://"); ok {
		i.socket = path
	}
	return i
}

func (i *Installer) Store() *Store { return i.store }

// StartRequest is everything an upgrade needs that this package cannot work
// out for itself: which versions are involved, which ref to move to, and who
// asked. Grouped into a struct because four bare string arguments in a row is
// how the from and to versions end up the wrong way round.
type StartRequest struct {
	From string
	To   string
	Ref  string
	// Health is the URL the updater probes once the stack is back up. It
	// belongs in the request rather than being written to the record after the
	// container has been created: the updater may read that record within
	// milliseconds of starting, and a second writer arriving late would either
	// be ignored or would stamp its own copy over progress already written.
	// One writer at a time is the rule this whole file rests on.
	Health string
	Actor  string
}

// Start launches the upgrade and returns as soon as the sibling is running.
//
// It deliberately does not wait: the caller is an HTTP handler whose response
// has to reach a browser that is about to lose its connection to this process
// entirely. The record on disk is the only durable account of what happens
// next, which is why it is written before the container is created — a
// `docker run` that succeeds and is then never recorded is an upgrade nobody
// can see, and the ordering is what rules that out.
func (i *Installer) Start(ctx context.Context, loc *Location, req StartRequest) (*Run, error) {
	if loc == nil {
		return nil, ErrNoLocation
	}
	if prev, err := i.store.Load(); err == nil && prev != nil && prev.Status.Live() {
		return nil, ErrInProgress
	}
	if !Newer(req.From, req.To) {
		return nil, ErrNotNewer
	}

	run := &Run{
		ID:          time.Now().UTC().Format("20060102T150405Z"),
		Status:      StatusPending,
		Phase:       PhaseQueued,
		FromVersion: Normalise(req.From),
		ToVersion:   Normalise(req.To),
		Ref:         req.Ref,
		Dir:         loc.Dir,
		Compose:     loc.Compose,
		Image:       loc.Image,
		Container:   UpdaterContainer,
		Health:      req.Health,
		FromCommit:  HeadCommit(ctx, loc),
		Actor:       req.Actor,
		StartedAt:   time.Now().UTC(),
	}

	// The transcript is opened first and truncated, so the log an operator
	// reads is always this run's and never the tail of the last one.
	if f, err := i.store.OpenLog(); err == nil {
		fmt.Fprintf(f, "Updating Just Dashboard %s → %s\n", run.FromVersion, run.ToVersion)
		fmt.Fprintf(f, "checkout: %s (%s)\n\n", run.Dir, run.Ref)
		f.Close()
	}
	if err := i.store.Save(run); err != nil {
		return nil, fmt.Errorf("record the update: %w", err)
	}

	// A previous updater, successful or not, is in the way of the fixed name.
	// Removing it here rather than at the end of the last run is what leaves a
	// failed one around long enough for `docker logs` to be useful when things
	// went wrong badly enough that our own transcript never got written.
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", UpdaterContainer).Run()

	cmd := exec.CommandContext(ctx, "docker", i.updaterArgs(run, loc)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(out.String())
		if detail == "" {
			detail = err.Error()
		}
		_ = i.store.Finish(fmt.Errorf("could not start the updater: %s", detail))
		return nil, fmt.Errorf("could not start the updater container: %s", detail)
	}
	i.log.Warn("self-update started",
		"from", run.FromVersion, "to", run.ToVersion, "dir", run.Dir, "actor", run.Actor)
	return run, nil
}

// updaterArgs builds the sibling's argv.
//
// A pure function, and tested as one: everything that can go wrong with this
// feature in a way the operator cannot recover from is a wrong flag here, and
// a test that reads the argv is the only check that does not need a machine
// with Docker on it to run.
func (i *Installer) updaterArgs(run *Run, loc *Location) []string {
	args := []string{
		"run", "--detach",
		"--name", UpdaterContainer,
		"--label", updaterLabel,
		// The stack this is upgrading runs on the host network; so does this,
		// so `git fetch` resolves names exactly as the host's own git would.
		"--network", "host",
		"--pid", "host",
		// Never restarted. A half-run upgrade replayed at boot on a machine
		// that has been rebooted mid-update is not a recovery, it is a second
		// unattended upgrade nobody asked for.
		"--restart", "no",
	}
	if i.socket != "" {
		args = append(args, "-v", i.socket+":/var/run/docker.sock")
	} else if i.dockerHost != "" {
		args = append(args, "-e", "DOCKER_HOST="+i.dockerHost)
	}
	// The checkout and the data directory, each mounted at the path it already
	// has. Same-path mounts are what let the record on disk name one directory
	// that means the same thing to the backend, to the updater and to the
	// operator reading it over ssh.
	args = append(args,
		"-v", run.Dir+":"+run.Dir,
		"-v", i.dataDir+":"+i.dataDir,
		"-w", run.Dir,
		// Explicit, though the image already declares it: a sibling that
		// silently ran the *server* instead of the updater would try to bind
		// the API port and leave the operator with no upgrade and a confusing
		// second dashboard.
		"--entrypoint", "/usr/local/bin/just-dashboard",
	)
	// Nothing from this process's environment is passed. The updater needs no
	// secret — it runs git and compose, and compose reads the stack's own .env
	// from the checkout — and the master key sitting in an environment
	// variable of a container anyone with the socket can inspect is exactly
	// what main.scrubSecretEnv exists to prevent.
	args = append(args, loc.Image, "-self-update", "-state-dir", i.dataDir)
	return args
}

// Reconcile settles a run that was in flight when this process started.
//
// It is the other half of the sibling-container design. A backend that has
// just been restarted by an upgrade has no memory of it, and the record on
// disk still says "running" because the updater is, at that moment, still
// finishing the frontend and the proxy. So: if the updater is still there,
// leave it alone. If it is gone and this process is the version the run was
// aiming at, the upgrade worked — the only way this code is running at all is
// that the build succeeded and compose got as far as recreating the backend.
// If it is gone and the version did not move, something stopped it.
func (i *Installer) Reconcile(ctx context.Context, current string, list Lister) {
	run, err := i.store.Load()
	if err != nil || run == nil || !run.Status.Live() {
		return
	}
	alive, err := i.updaterAlive(ctx, list)
	switch {
	case err == nil && alive:
		// Still working. The phase is nudged so the UI stops saying
		// "building" while the containers are visibly coming back.
		_ = i.store.Update(func(r *Run) {
			if Compare(current, r.ToVersion) >= 0 && r.Phase == PhaseBuilding {
				r.Phase = PhaseRestarting
			}
		})
		return
	case err != nil && time.Since(run.StartedAt) < staleAfter:
		// Docker could not be asked. Saying nothing is right: the run may well
		// be fine, and marking it failed would be a guess presented as a fact.
		return
	}

	if Compare(current, run.ToVersion) >= 0 {
		_ = i.store.Update(func(r *Run) {
			now := time.Now().UTC()
			r.Status, r.Phase, r.FinishedAt, r.Error = StatusSuccess, PhaseFinished, &now, ""
		})
		i.log.Info("self-update completed", "version", current, "from", run.FromVersion)
		// Only now is the sibling removed: it has done its job, and leaving an
		// exited container in the operator's Docker list after every upgrade
		// is litter. A failed one is kept, because `docker logs` on it is the
		// last resort when the transcript is empty.
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", UpdaterContainer).Run()
		return
	}
	_ = i.store.Finish(fmt.Errorf(
		"the updater stopped before %s was running — this install is still on %s; the transcript below is what it managed",
		run.ToVersion, current))
	i.log.Warn("self-update did not complete", "expected", run.ToVersion, "running", current)
}

// updaterAlive reports whether the sibling container still exists and is
// running. The listing is used rather than `docker inspect` so the one Docker
// dependency this package has stays the one interface Locate already needs.
func (i *Installer) updaterAlive(ctx context.Context, list Lister) (bool, error) {
	if list == nil {
		return false, errNoContainer
	}
	all, err := list(ctx)
	if err != nil {
		return false, err
	}
	for _, c := range all {
		if c.Name != UpdaterContainer {
			continue
		}
		return c.State == "running" || c.State == "created" || c.State == "restarting", nil
	}
	return false, nil
}

// Dirty reports uncommitted changes in the checkout.
//
// Shown before an upgrade rather than discovered during one. The upgrade
// itself uses `git merge --ff-only`, which keeps a local edit that does not
// collide and refuses outright when it would — so a dirty tree is a warning
// about what might stop the upgrade, not a prohibition, and it is worded that
// way.
func Dirty(ctx context.Context, loc *Location) []string {
	if loc == nil || loc.Visible == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := gitOutput(ctx, loc.Visible, "status", "--porcelain")
	if err != nil {
		return nil
	}
	files := []string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
		if len(files) >= 20 {
			break
		}
	}
	return files
}

// gitOutput runs git against a checkout that this process very likely does not
// own.
//
// -c safe.directory is not optional here and its absence is a confusing
// failure rather than a loud one: git refuses to operate on a repository owned
// by another account with "detected dubious ownership", which is exactly the
// case for a dashboard installed with sudo and inspected by a container. The
// setting is scoped to the one directory rather than set globally, so nothing
// this process does later inherits it.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-c", "safe.directory=" + filepath.Clean(dir), "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return buf.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(buf.String()))
	}
	return buf.String(), nil
}

// HeadCommit is the commit the checkout is on, for the record of what an
// upgrade moved away from.
func HeadCommit(ctx context.Context, loc *Location) string {
	if loc == nil || loc.Visible == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := gitOutput(ctx, loc.Visible, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}
