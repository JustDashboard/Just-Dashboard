package selfupdate

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/stackports"
)

// The program the sibling container runs.
//
// This is the same binary as the dashboard, started with -self-update, and it
// runs with no configuration at all: no master key, no database, no
// environment inherited from the process that launched it. Everything it needs
// is in the record the backend wrote before creating it — which is the point,
// because that record is also what the operator reads afterwards, so the job
// that was described is exactly the job that ran.
//
// Nothing here talks to a browser. The transcript on disk is the whole
// interface, because the process that would have served it is one of the ones
// being restarted.

// probeWindow is how long the dashboard is given to answer after its
// containers have been recreated. A cold start on a small VPS is seconds; five
// minutes covers a machine that is also busy building.
const probeWindow = 5 * time.Minute

// RunUpdater carries out the upgrade described by the record in stateDir.
//
// The error it returns is for the container's exit status. The record on disk
// is the copy that matters, and it is written before returning either way.
func RunUpdater(stateDir string) error {
	store := NewStore(stateDir)
	run, err := store.Load()
	if err != nil {
		return fmt.Errorf("read the update record: %w", err)
	}
	if run == nil {
		return fmt.Errorf("no update was requested (%s is not there)", store.Path())
	}
	if !run.Status.Live() {
		return fmt.Errorf("the update recorded in %s already finished as %s", store.Path(), run.Status)
	}

	logFile, err := store.AppendLog()
	if err != nil {
		return fmt.Errorf("open the transcript: %w", err)
	}
	defer logFile.Close()
	// Everything is written twice: to the transcript the dashboard reads back,
	// and to stdout, so `docker logs just-dashboard-updater` shows the same
	// thing to somebody who has no dashboard to read it in — which, during an
	// upgrade, is the normal case.
	out := io.MultiWriter(logFile, os.Stdout)

	_ = store.Update(func(r *Run) {
		r.Status = StatusRunning
		r.Phase = PhaseFetching
	})

	ctx := context.Background()
	err = upgrade(ctx, store, run, out)
	if ferr := store.Finish(err); ferr != nil {
		fmt.Fprintf(out, "\n! could not record the outcome: %v\n", ferr)
	}
	if err != nil {
		fmt.Fprintf(out, "\nFAILED: %v\n", err)
		return err
	}
	fmt.Fprintf(out, "\nJust Dashboard is now on %s.\n", run.ToVersion)

	// Tidying up after itself, which it can only do by asking Docker to remove
	// the container it is running in. The final state is already on disk, so
	// being killed part-way through this call costs nothing — and the daemon
	// carries out the removal whether or not this process survives to see it.
	// Only on success: a failed run's container is the last place to look when
	// the transcript itself is empty.
	_ = exec.Command("docker", "rm", "-f", UpdaterContainer).Run()
	return nil
}

func upgrade(ctx context.Context, store *Store, run *Run, out io.Writer) error {
	if _, err := os.Stat(run.Dir); err != nil {
		return fmt.Errorf("%s is not there — the checkout this dashboard was installed from has moved or was not mounted", run.Dir)
	}

	commit, err := fastForward(ctx, run.Dir, run.Ref, out)
	if err != nil {
		return err
	}
	if commit != "" {
		_ = store.Update(func(r *Run) { r.ToCommit = commit })
	}

	_ = store.Update(func(r *Run) { r.Phase = PhaseBuilding })
	compose := run.Compose
	if compose == "" {
		compose = "docker-compose.yml"
	}
	if err := stream(ctx, run.Dir, out, "docker", "compose", "-f", compose, "build"); err != nil {
		return err
	}
	err = stackports.Start(ctx, run.Dir, compose, out, func() error {
		args := []string{"compose", "-f", compose, "up", "-d", "--remove-orphans", "--wait", "--wait-timeout", "90"}
		fmt.Fprintf(out, "\n$ docker %s\n", strings.Join(args, " "))
		return stream(ctx, run.Dir, out, "docker", args...)
	}, func(selected stackports.Selection) error {
		if selected.Health() == "" {
			return nil
		}
		run.Health = selected.Health()
		return store.Update(func(r *Run) { r.Health = run.Health })
	})
	if err != nil {
		return err
	}

	_ = store.Update(func(r *Run) { r.Phase = PhaseRestarting })
	if run.Health == "" {
		return nil
	}
	fmt.Fprintf(out, "\nwaiting for the dashboard to answer at %s\n", run.Health)
	if err := waitHealthy(ctx, run.Health, out); err != nil {
		return err
	}
	fmt.Fprintf(out, "the dashboard is answering again\n")
	return nil
}

// fastForward moves the checkout on to the tracked ref and reports the commit
// it lands on.
//
// --ff-only, never `reset --hard`.
//
// The deploy pipeline in internal/deploy resets, and is right to: a deploy
// target's working tree is disposable. This one is not. It is the operator's
// own checkout, on their own server, and it is entirely normal for it to carry
// an edited docker-compose.yml or a tweaked Caddyfile. A reset would delete
// that silently as a side effect of pressing "update", which is the kind of
// thing an operator finds out about weeks later. A fast-forward keeps every
// local change that does not collide, and when one does collide git refuses
// and says exactly what is in the way — which is a far better outcome than a
// successful upgrade that ate somebody's configuration.
func fastForward(ctx context.Context, dir, ref string, out io.Writer) (string, error) {
	fmt.Fprintf(out, "$ git fetch --prune origin\n")
	if err := gitStream(ctx, dir, out, "fetch", "--prune", "origin"); err != nil {
		return "", err
	}

	target := "origin/" + ref
	fmt.Fprintf(out, "\n$ git merge --ff-only %s\n", target)
	if err := gitStream(ctx, dir, out, "merge", "--ff-only", target); err != nil {
		return "", fmt.Errorf("%w\n\nThe checkout at %s could not be fast-forwarded to %s. "+
			"That normally means it carries local commits, or edits to files this release also "+
			"changes; commit, stash or revert them and try again", err, dir, target)
	}
	head, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		// The merge succeeded, so the upgrade should proceed; only the record
		// of which commit it landed on is missing.
		return "", nil
	}
	commit := strings.TrimSpace(head)
	fmt.Fprintf(out, "now at %s\n", commit)
	return commit, nil
}

// waitHealthy is what makes "the update finished" mean the dashboard came
// back, rather than that a command exited zero. `docker compose up -d` returns
// as soon as the containers are started, and a backend that starts and then
// dies on a configuration error looks identical to a healthy one from there.
func waitHealthy(ctx context.Context, url string, out io.Writer) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(probeWindow)
	var last string
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = resp.Status
		} else {
			last = err.Error()
		}
		time.Sleep(3 * time.Second)
	}
	fmt.Fprintf(out, "still not answering after %s\n", probeWindow)
	return fmt.Errorf("the stack was rebuilt but the dashboard did not answer within %s (last attempt: %s) — "+
		"run `docker compose ps` and `docker compose logs backend` in %s to see why", probeWindow, last, url)
}

// gitStream is gitOutput's streaming twin: same safe.directory handling, but
// the output goes to the transcript as it appears rather than being collected.
func gitStream(ctx context.Context, dir string, out io.Writer, args ...string) error {
	full := append([]string{"-c", "safe.directory=" + dir, "-C", dir}, args...)
	// A credential prompt would hang the upgrade forever with no way to answer
	// it — there is no terminal attached to this container — so git is told to
	// fail instead. A private fork needs a checkout whose remote authenticates
	// without asking, which is what a deploy key is.
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/true",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new",
	)
	return streamEnv(ctx, dir, env, out, "git", full...)
}

func stream(ctx context.Context, dir string, out io.Writer, name string, args ...string) error {
	env := append(os.Environ(),
		// Plain progress for the same reason the compose runner asks for it:
		// the default renderer redraws with cursor movement, which in a file
		// read back later is a screenful of half-written words.
		"COMPOSE_PROGRESS=plain",
		"BUILDKIT_PROGRESS=plain",
		"DOCKER_CLI_HINTS=false",
		// Asked for explicitly rather than relied upon. Compose picks BuildKit
		// on its own when buildx is installed, and silently builds with the
		// classic builder when it is not — which is exactly how this feature
		// shipped an updater that could not build the project it updates. The
		// image now carries buildx; saying so here means the day somebody sets
		// DOCKER_BUILDKIT=0 in the environment, the failure is theirs and
		// visible rather than ours and silent.
		"DOCKER_BUILDKIT=1",
		"COMPOSE_DOCKER_CLI_BUILD=1",
	)
	return streamEnv(ctx, dir, env, out, name, args...)
}

func streamEnv(ctx context.Context, dir string, env []string, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	sc := bufio.NewScanner(pipe)
	// A build log line can be very long — a bundler printing one path per
	// dependency on one line — and the default 64 KB would end the scan with
	// "token too long" halfway through an otherwise successful upgrade.
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fmt.Fprintln(out, sc.Text())
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args[:min(len(args), 3)], " "), err)
	}
	return nil
}
