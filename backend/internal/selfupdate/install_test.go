package selfupdate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate/selfupdatetest"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The argv of the updater container is the whole feature in one line, and it
// is the one part that cannot be exercised on a machine without Docker. Every
// flag here is load-bearing and every one of them has a way of being wrong
// that the operator only discovers when their dashboard does not come back.
func TestUpdaterArgs(t *testing.T) {
	i := NewInstaller(NewStore("/var/lib/jd"), "/var/lib/jd", "unix:///var/run/docker.sock", quiet())
	run := &Run{Dir: "/opt/Just-Dashboard", Compose: "docker-compose.yml"}
	loc := &Location{Image: "just-dashboard-backend:latest"}
	args := i.updaterArgs(run, loc)
	joined := strings.Join(args, " ")

	for _, want := range []string{
		// Detached, or the handler would block for the length of a build on a
		// connection that is about to be severed.
		"run --detach",
		// A fixed name, so a backend restarted into the new version can find
		// the container that put it there.
		"--name " + UpdaterContainer,
		// Never restarted: a machine rebooted mid-upgrade must not replay one.
		"--restart no",
		// The socket is how it replaces its own stack.
		"-v /var/run/docker.sock:/var/run/docker.sock",
		// The checkout and the data directory, each at the path it already
		// has, so one name means the same thing in all three places.
		"-v /opt/Just-Dashboard:/opt/Just-Dashboard",
		"-v /var/lib/jd:/var/lib/jd",
		"-w /opt/Just-Dashboard",
		"--entrypoint /usr/local/bin/just-dashboard",
		"just-dashboard-backend:latest -self-update -state-dir /var/lib/jd",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("updater argv is missing %q\ngot: docker %s", want, joined)
		}
	}

	// Nothing from this process's environment travels to a container that
	// anybody with the Docker socket can inspect.
	for _, forbidden := range []string{"JD_MASTER_KEY", "JD_BOOTSTRAP", "--privileged"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("updater argv carries %q: %s", forbidden, joined)
		}
	}
}

// A DOCKER_HOST that is not a unix socket has nothing to bind-mount, so it is
// passed through as configuration instead.
func TestUpdaterArgsForARemoteDaemon(t *testing.T) {
	i := NewInstaller(NewStore("/data"), "/data", "tcp://10.0.0.2:2376", quiet())
	joined := strings.Join(i.updaterArgs(&Run{Dir: "/opt/jd"}, &Location{Image: "img"}), " ")
	if strings.Contains(joined, "docker.sock") {
		t.Errorf("bind-mounted a socket that does not exist: %s", joined)
	}
	if !strings.Contains(joined, "-e DOCKER_HOST=tcp://10.0.0.2:2376") {
		t.Errorf("the daemon address was not passed on: %s", joined)
	}
}

func TestStartRefusesWhatItCannotDo(t *testing.T) {
	dir := t.TempDir()
	i := NewInstaller(NewStore(dir), dir, "unix:///var/run/docker.sock", quiet())
	loc := &Location{Dir: "/opt/jd", Image: "img", Compose: "docker-compose.yml"}

	if _, err := i.Start(context.Background(), nil, StartRequest{From: "0.5", To: "0.6"}); err != ErrNoLocation {
		t.Errorf("started an upgrade with nowhere to run it: %v", err)
	}
	// A downgrade is not an upgrade, whatever a client asks for. The server
	// re-decides this because the version is chosen in the browser.
	if _, err := i.Start(context.Background(), loc, StartRequest{From: "0.6", To: "0.5"}); err != ErrNotNewer {
		t.Errorf("accepted a downgrade: %v", err)
	}
	if _, err := i.Start(context.Background(), loc, StartRequest{From: "0.6", To: "0.6"}); err != ErrNotNewer {
		t.Errorf("accepted an upgrade to the version already installed: %v", err)
	}

	// Two upgrades at once would have two compose runs fighting over the same
	// containers, which is how an install ends up half on each version.
	store := NewStore(dir)
	if err := store.Save(&Run{ID: "x", Status: StatusRunning, ToVersion: "0.7", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Start(context.Background(), loc, StartRequest{From: "0.5", To: "0.6"}); err != ErrInProgress {
		t.Errorf("started a second concurrent upgrade: %v", err)
	}
}

// The updater is started, replaced and tidied away on the daemon JD_DOCKER_HOST
// names. The CLI used to inherit the process environment and reach
// /var/run/docker.sock whatever the setting said, where the fixed container
// name may be another dashboard's.
func TestStartAndReconcileUseTheConfiguredDaemon(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	dir := t.TempDir()
	const host = "unix:///srv/jd/docker.sock"
	i := NewInstaller(NewStore(dir), dir, host, quiet())
	loc := &Location{Dir: "/opt/jd", Image: "img", Compose: "docker-compose.yml"}

	if _, err := i.Start(context.Background(), loc, StartRequest{From: "0.5", To: "0.6"}); err != nil {
		t.Fatal(err)
	}
	i.Reconcile(context.Background(), "0.6", lister())

	if diff := selfupdatetest.AimedAt(calls(), host,
		"version ", "rm -f "+UpdaterContainer, "run --detach", "rm -f "+UpdaterContainer); diff != "" {
		t.Fatal(diff)
	}
}

// dashboard.update.install against a daemon that does not answer is refused
// with that reason, before anything is written: the previous update's record
// and transcript are what the page keeps showing, and no `docker rm` or
// `docker run` is tried on any daemon.
func TestStartAgainstADaemonThatDoesNotAnswerWritesNothing(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	dir := t.TempDir()
	store := NewStore(dir)
	done := time.Now().UTC()
	if err := store.Save(&Run{ID: "previous", Status: StatusSuccess, Phase: PhaseFinished, ToVersion: "0.5", FinishedAt: &done}); err != nil {
		t.Fatal(err)
	}
	if f, err := store.OpenLog(); err != nil {
		t.Fatal(err)
	} else {
		f.WriteString("the 0.5 update\n")
		f.Close()
	}

	missing := selfupdatetest.MissingHost(t)
	i := NewInstaller(store, dir, missing, quiet())
	_, err := i.Start(context.Background(), &Location{Dir: "/opt/jd", Image: "img"}, StartRequest{From: "0.5", To: "0.6"})
	if !errors.Is(err, ErrDockerUnreachable) {
		t.Fatalf("err = %v, want ErrDockerUnreachable", err)
	}
	if !strings.Contains(err.Error(), "the update was not started") || !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not say the update never began, or which daemon did not answer", err)
	}
	if diff := selfupdatetest.AimedAt(calls(), missing, "version "); diff != "" {
		t.Fatal(diff)
	}
	if run, _ := store.Load(); run == nil || run.ID != "previous" || run.Status != StatusSuccess {
		t.Fatalf("record = %+v, want the previous update's", run)
	}
	if log := store.Transcript(); log != "the 0.5 update\n" {
		t.Fatalf("transcript = %q, want the previous update's", log)
	}
	if err := CheckLifecycleIdle(dir); err != nil {
		t.Fatalf("a refused update holds the lifecycle: %v", err)
	}
}

// Reconcile is the half of the design that runs *after* the restart, and its
// job is to turn "the record still says running" into a verdict.
func TestReconcile(t *testing.T) {
	// A settled success removes the updater; the fake stands in for the daemon
	// so the test never asks the one on this machine to remove anything.
	selfupdatetest.Docker(t)
	newRun := func(t *testing.T) (*Store, *Installer) {
		t.Helper()
		dir := t.TempDir()
		store := NewStore(dir)
		if err := store.Save(&Run{
			ID: "x", Status: StatusRunning, Phase: PhaseBuilding,
			FromVersion: "0.5", ToVersion: "0.6", StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		return store, NewInstaller(store, dir, "unix:///var/run/docker.sock", quiet())
	}

	t.Run("the version moved, so it worked", func(t *testing.T) {
		store, inst := newRun(t)
		inst.Reconcile(context.Background(), "0.6", lister())
		run, _ := store.Load()
		if run.Status != StatusSuccess {
			t.Fatalf("status %s — a backend running the target version is the proof the upgrade landed", run.Status)
		}
		if run.FinishedAt == nil {
			t.Error("a finished run with no finish time")
		}
	})

	t.Run("the updater is gone and the version did not move", func(t *testing.T) {
		store, inst := newRun(t)
		inst.Reconcile(context.Background(), "0.5", lister())
		run, _ := store.Load()
		if run.Status != StatusFailed {
			t.Fatalf("status %s, want failed", run.Status)
		}
		if !strings.Contains(run.Error, "0.6") || !strings.Contains(run.Error, "0.5") {
			t.Errorf("error %q does not say which version was wanted and which is running", run.Error)
		}
	})

	t.Run("the updater is still working", func(t *testing.T) {
		store, inst := newRun(t)
		inst.Reconcile(context.Background(), "0.6", lister(Sibling{Name: UpdaterContainer, State: "running"}))
		run, _ := store.Load()
		if run.Status != StatusRunning {
			t.Fatalf("status %s — an upgrade still recreating the frontend was closed out early", run.Status)
		}
		if run.Phase != PhaseRestarting {
			t.Errorf("phase %s, want %s once the backend is back", run.Phase, PhaseRestarting)
		}
	})

	t.Run("docker cannot be asked", func(t *testing.T) {
		store, inst := newRun(t)
		// Saying nothing is right: marking it failed would be a guess
		// presented to the operator as a fact.
		inst.Reconcile(context.Background(), "0.5", nil)
		run, _ := store.Load()
		if run.Status != StatusRunning {
			t.Fatalf("status %s — an unanswerable question was answered anyway", run.Status)
		}
	})

	t.Run("a run nothing has touched for hours", func(t *testing.T) {
		dir := t.TempDir()
		store := NewStore(dir)
		if err := store.Save(&Run{
			ID: "x", Status: StatusRunning, FromVersion: "0.5", ToVersion: "0.6",
			StartedAt: time.Now().Add(-3 * time.Hour).UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		NewInstaller(store, dir, "", quiet()).Reconcile(context.Background(), "0.5", nil)
		run, _ := store.Load()
		if run.Status != StatusFailed {
			t.Fatalf("status %s — a run abandoned three hours ago is still described as in progress", run.Status)
		}
	})

	t.Run("a finished run is left alone", func(t *testing.T) {
		dir := t.TempDir()
		store := NewStore(dir)
		done := time.Now().UTC()
		if err := store.Save(&Run{ID: "x", Status: StatusFailed, Error: "the original reason", FinishedAt: &done}); err != nil {
			t.Fatal(err)
		}
		NewInstaller(store, dir, "", quiet()).Reconcile(context.Background(), "0.6", lister())
		run, _ := store.Load()
		if run.Error != "the original reason" {
			t.Fatalf("a settled run was rewritten: %q", run.Error)
		}
	})
}
