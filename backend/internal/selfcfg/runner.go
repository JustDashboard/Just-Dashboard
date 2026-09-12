package selfcfg

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
// Started as `just-dashboard -self-restart`, with no configuration of its own:
// everything it needs is in the record on disk, which is also what the
// operator reads afterwards, so the job described is the job that ran.
//
// Its one interesting responsibility is the one the dashboard cannot have:
// deciding that a new configuration did not work, and putting the old one
// back. The process that would otherwise make that call is the process that
// just failed to start.

// How long the stack is given to answer before a configuration is judged bad.
// A restart is seconds; a rebuild compiles a Go binary and a Next app.
const (
	restartWindow = 3 * time.Minute
	rebuildWindow = 15 * time.Minute
	// rollbackWindow is deliberately shorter. By the time it is used the
	// operator has already waited out one of the windows above, and the
	// configuration being restored is one that was working minutes ago.
	rollbackWindow = 3 * time.Minute
)

// RunRestart carries out the restart described by the record in stateDir.
func RunRestart(stateDir string) error {
	store := NewStore(stateDir)
	run, err := store.Load()
	if err != nil {
		return fmt.Errorf("read the restart record: %w", err)
	}
	if run == nil {
		return fmt.Errorf("no restart was requested (%s is not there)", store.Path())
	}
	if !run.Status.Live() {
		return fmt.Errorf("the restart recorded in %s already finished as %s", store.Path(), run.Status)
	}

	logFile, err := store.AppendLog()
	if err != nil {
		return fmt.Errorf("open the transcript: %w", err)
	}
	defer logFile.Close()
	// Both destinations, for the same reason the updater writes twice: during
	// a restart the dashboard that would show this is the thing being
	// restarted, and `docker logs` is what is left.
	out := io.MultiWriter(logFile, os.Stdout)

	_ = store.Update(func(r *Run) {
		r.Status = StatusRunning
		r.Phase = PhaseApplying
	})

	ctx := context.Background()
	applyErr := applyStack(ctx, store, run, out)
	if applyErr == nil {
		_ = store.Finish(nil, false)
		fmt.Fprintf(out, "\nThe dashboard is running the new configuration.\n")
		if run.Endpoint != "" {
			fmt.Fprintf(out, "Open %s\n", run.Endpoint)
		}
		_ = exec.Command("docker", "rm", "-f", RestartContainer).Run()
		return nil
	}

	fmt.Fprintf(out, "\nFAILED: %v\n", applyErr)

	// Nothing was written, so there is nothing to put back: a plain restart or
	// rebuild that fails leaves the configuration exactly as it was, and the
	// operator needs the transcript rather than a recovery.
	if run.Backup == "" || run.EnvPath == "" {
		_ = store.Finish(applyErr, false)
		return applyErr
	}

	fmt.Fprintf(out, "\nPutting the previous configuration back.\n")
	_ = store.Update(func(r *Run) { r.Phase = PhaseRollback })
	if rbErr := rollback(ctx, run, out); rbErr != nil {
		combined := fmt.Errorf("%w\n\nthe previous configuration could not be restored either: %v\n\n"+
			"the file the dashboard was running before this change is at %s — copy it over %s and run "+
			"`docker compose up -d` in %s", applyErr, rbErr, run.Backup, run.EnvPath, run.Dir)
		_ = store.Finish(combined, false)
		return combined
	}
	fmt.Fprintf(out, "\nThe previous configuration is back and the dashboard is answering.\n")
	_ = store.Finish(applyErr, true)
	// The container is kept on a rollback. `docker logs` on it is the last
	// resort for somebody whose dashboard is up but not the way they wanted.
	return applyErr
}

// applyStack recreates the containers and waits for the result to answer.
func applyStack(ctx context.Context, store *Store, run *Run, out io.Writer) error {
	if _, err := os.Stat(run.Dir); err != nil {
		return fmt.Errorf("%s is not there — the checkout this dashboard was installed from has moved or was not mounted", run.Dir)
	}
	compose := run.Compose
	if compose == "" {
		compose = "docker-compose.yml"
	}
	if run.Action == ActionRebuild {
		if err := stream(ctx, run.Dir, out, "docker", "compose", "-f", compose, "build"); err != nil {
			return err
		}
	}
	err := stackports.Start(ctx, run.Dir, run.Compose, out, func() error {
		return composeUp(ctx, run, false, run.Action != ActionApply, out)
	}, func(selected stackports.Selection) error {
		if selected.Health() == "" {
			return nil
		}
		if selected.Changed && run.Backup == "" {
			run.Backup, run.EnvPath, run.RollbackHealth = selected.Backup, selected.EnvPath, run.Health
		}
		run.Health, run.Endpoint = selected.Health(), selected.Endpoint
		return store.Update(func(r *Run) {
			r.Health, r.Endpoint = run.Health, run.Endpoint
			r.Backup, r.EnvPath, r.RollbackHealth = run.Backup, run.EnvPath, run.RollbackHealth
		})
	})
	if err != nil {
		return err
	}
	_ = store.Update(func(r *Run) { r.Phase = PhaseWaiting })
	window := restartWindow
	if run.Action == ActionRebuild {
		window = rebuildWindow
	}
	if run.Health == "" {
		return nil
	}
	fmt.Fprintf(out, "\nwaiting for the dashboard to answer at %s\n", run.Health)
	return waitHealthy(ctx, run.Health, window, out)
}

// rollback restores the previous .env and brings the stack back up on it.
func rollback(ctx context.Context, run *Run, out io.Writer) error {
	saved, err := os.ReadFile(run.Backup)
	if err != nil {
		return fmt.Errorf("read %s: %w", run.Backup, err)
	}
	if err := os.WriteFile(run.EnvPath, saved, 0o600); err != nil {
		return fmt.Errorf("restore %s: %w", run.EnvPath, err)
	}
	fmt.Fprintf(out, "restored %s from %s\n", run.EnvPath, run.Backup)
	// Never a rebuild: the images are whatever they were, and the failure
	// being recovered from is a configuration one. Spending fifteen minutes
	// compiling before the dashboard comes back would be its own outage.
	if err := composeUp(ctx, run, false, false, out); err != nil {
		return err
	}
	if run.RollbackHealth == "" {
		return nil
	}
	fmt.Fprintf(out, "\nwaiting for the dashboard to answer at %s\n", run.RollbackHealth)
	return waitHealthy(ctx, run.RollbackHealth, rollbackWindow, out)
}

// composeUp recreates the stack.
//
// force is what makes the Restart button mean something. `compose up -d`
// compares each service against its configuration and does nothing at all when
// they match, so a plain restart — the one action whose entire purpose is to
// start the processes again — would have exited zero having changed nothing.
// An apply deliberately does not force: its own change is what recreates the
// services the change touches, and recreating the other two as well would take
// the dashboard away for longer than the setting warranted.
func composeUp(ctx context.Context, run *Run, build, force bool, out io.Writer) error {
	compose := run.Compose
	if compose == "" {
		compose = "docker-compose.yml"
	}
	args := []string{"compose", "-f", compose, "up", "-d", "--remove-orphans", "--wait", "--wait-timeout", "90"}
	if build {
		args = append(args, "--build")
	}
	if force {
		args = append(args, "--force-recreate")
	}
	fmt.Fprintf(out, "\n$ docker %s\n", strings.Join(args, " "))
	return stream(ctx, run.Dir, out, "docker", args...)
}

// waitHealthy is what makes "the restart finished" mean the dashboard came
// back rather than that a command exited zero.
func waitHealthy(ctx context.Context, url string, window time.Duration, out io.Writer) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(window)
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
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("the stack was recreated but the dashboard did not answer within %s (last attempt: %s)", window, last)
}

func stream(ctx context.Context, dir string, out io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		// Plain progress: the default renderer redraws with cursor movement,
		// which in a transcript read back later is a screenful of half-written
		// words.
		"COMPOSE_PROGRESS=plain",
		"BUILDKIT_PROGRESS=plain",
		"DOCKER_CLI_HINTS=false",
		"DOCKER_BUILDKIT=1",
		"COMPOSE_DOCKER_CLI_BUILD=1",
	)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fmt.Fprintln(out, sc.Text())
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args[:min(len(args), 3)], " "), err)
	}
	return nil
}
