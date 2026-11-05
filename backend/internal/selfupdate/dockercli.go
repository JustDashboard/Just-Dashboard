package selfupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The docker CLI, aimed at the daemon this dashboard manages.
//
// The lifecycle siblings (the updater, the restart and the uninstall) are
// started, replaced and removed through the docker CLI rather than the API
// client, and a CLI started with this process's environment talks to
// /var/run/docker.sock whatever JD_DOCKER_HOST says. On a host where the two
// differ that is another daemon, possibly one serving a different dashboard
// under the same fixed container names, where `docker rm -f
// just-dashboard-reconfigure` removes that dashboard's restart halfway
// through. So every call names the daemon, as dockerx's compose calls do.

// ErrDockerUnreachable is a lifecycle operation refused before it started,
// because the daemon that would run its sibling container did not answer.
var ErrDockerUnreachable = errors.New("the Docker daemon cannot be reached")

// dockerProbeTimeout bounds the reachability check. A TCP host that drops
// packets would otherwise hold the request until the CLI gives up on its own.
const dockerProbeTimeout = 15 * time.Second

// dockerEnv is the environment for a docker CLI call aimed at host. An empty
// host leaves the CLI to its own defaults; DOCKER_HOST is appended last, so it
// wins over one this process inherited.
func dockerEnv(host string) []string {
	if host == "" {
		return nil
	}
	return append(os.Environ(), "DOCKER_HOST="+host)
}

// DockerCommand is `docker args...` against the daemon at host.
func DockerCommand(ctx context.Context, host string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = dockerEnv(host)
	return cmd
}

// DockerReachable asks the daemon at host for its API version, the cheapest
// question that needs an answer from it.
//
// It is asked before a lifecycle operation writes anything. Without it a
// daemon that is down is discovered by `docker run`, after the run record and
// its transcript have replaced the previous run's, and the operator is shown a
// failed restart for something that never began.
func DockerReachable(ctx context.Context, host string) error {
	ctx, cancel := context.WithTimeout(ctx, dockerProbeTimeout)
	defer cancel()
	cmd := DockerCommand(ctx, host, "version", "--format", "{{.Server.APIVersion}}")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(out.String())
	switch {
	case errors.Is(err, exec.ErrNotFound):
		detail = "the docker command is not installed here"
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		detail = fmt.Sprintf("it did not answer within %s", dockerProbeTimeout)
	case detail == "":
		detail = err.Error()
	}
	if host == "" {
		return fmt.Errorf("%w: %s", ErrDockerUnreachable, detail)
	}
	return fmt.Errorf("%w at %s (JD_DOCKER_HOST): %s", ErrDockerUnreachable, host, detail)
}
