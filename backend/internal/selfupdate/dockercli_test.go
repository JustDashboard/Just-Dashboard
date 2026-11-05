package selfupdate

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate/selfupdatetest"
)

// The process environment names /var/run/docker.sock, which on the machine in
// the bug report was another dashboard's daemon; the CLI is aimed at
// JD_DOCKER_HOST instead.
func TestDockerCommandNamesTheConfiguredDaemon(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	const host = "unix:///srv/jd/docker.sock"

	if err := DockerCommand(context.Background(), host, "rm", "-f", UpdaterContainer).Run(); err != nil {
		t.Fatal(err)
	}
	if diff := selfupdatetest.AimedAt(calls(), host, "rm -f "+UpdaterContainer); diff != "" {
		t.Fatal(diff)
	}

	// No host configured leaves the CLI to its own defaults, as compose's
	// calls do.
	if env := DockerCommand(context.Background(), "", "ps").Env; env != nil {
		t.Fatalf("an empty host still set an environment: %q", env)
	}
}

func TestDockerReachable(t *testing.T) {
	calls := selfupdatetest.Docker(t)
	const host = "unix:///srv/jd/docker.sock"
	if err := DockerReachable(context.Background(), host); err != nil {
		t.Fatalf("a daemon that answers: %v", err)
	}
	if diff := selfupdatetest.AimedAt(calls(), host, "version "); diff != "" {
		t.Fatal(diff)
	}

	missing := selfupdatetest.MissingHost(t)
	err := DockerReachable(context.Background(), missing)
	if !errors.Is(err, ErrDockerUnreachable) {
		t.Fatalf("a daemon that does not answer = %v, want ErrDockerUnreachable", err)
	}
	// The operator is told which setting names the daemon, and the CLI's own
	// account of why it could not connect.
	for _, want := range []string{missing + " (JD_DOCKER_HOST)", "failed to connect to the docker API at " + missing} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}

	t.Setenv("PATH", t.TempDir())
	if err := DockerReachable(context.Background(), missing); !errors.Is(err, ErrDockerUnreachable) ||
		!strings.Contains(err.Error(), "docker command is not installed") {
		t.Fatalf("no docker CLI = %v", err)
	}
}
