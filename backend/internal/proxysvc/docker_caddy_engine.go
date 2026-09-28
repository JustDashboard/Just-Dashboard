package proxysvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// ErrNoIngress is a request for the Docker Caddy ingress on a host where none
// is running — including one where the first deployment would start it.
var ErrNoIngress = errors.New("no Caddy ingress container is running on this host")

// ingressCaddyfile is the Caddyfile inside the container, where discovery
// requires one to be bind-mounted and where the routes are imported from.
const ingressCaddyfile = "/etc/caddy/Caddyfile"

// ingress is the running Docker Caddy the engine controls act on.
func (s *Service) ingress(ctx context.Context) (*dockerCaddy, error) {
	edge, err := s.dockerCaddy(ctx)
	if err != nil {
		return nil, err
	}
	if edge == nil {
		return nil, ErrNoIngress
	}
	return edge, nil
}

// validate is the container's own `caddy validate` of the Caddyfile it
// serves, with its output kept for the page: the test the deployment routes
// already pass before every reload, which reported nothing but an exit code.
// The positions Caddy names are the container's paths and are left as they
// are — resolving them on this side would name a different file. A test that
// gives no verdict is an *UnfinishedTestError, and so is docker's own failure
// to reach the container, which it reports with the exit status Caddy refuses
// with.
func (c *dockerCaddy) validate(ctx context.Context) (*ValidationResult, error) {
	run, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	args := []string{"caddy", "validate", "--config", ingressCaddyfile, "--adapter", "caddyfile"}
	cmd := hostexec.Command(run, "docker", append([]string{"exec", c.ID}, args...)...)
	cmd.WaitDelay = testWaitDelay
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	res := &ValidationResult{
		Valid:       err == nil,
		Output:      strings.TrimSpace(buf.String()),
		Command:     "docker exec " + c.Name + " " + strings.Join(args, " "),
		Diagnostics: ParseCaddyDiagnostics(buf.String()),
	}
	for _, d := range res.Diagnostics {
		if d.Level == "warn" {
			res.Warnings++
		}
	}
	why, cause := verdictless(ctx, run, err)
	if why == "" && err != nil && dockerFailed(res.Output) {
		why, cause = "could not be run: "+strings.SplitN(res.Output, "\n", 2)[0], err
	}
	if why != "" {
		return res, &UnfinishedTestError{Command: res.Command, Why: why, Output: res.Output, cause: cause}
	}
	return res, nil
}

// dockerFailed is output the docker CLI wrote about itself — no container to
// exec in, no daemon to ask — rather than anything Caddy said.
func dockerFailed(output string) bool {
	return strings.HasPrefix(output, "Error response from daemon:") ||
		strings.HasPrefix(output, "Cannot connect to the Docker daemon")
}

// reloadIngress is Reload for the Docker Caddy: its test, then `caddy reload`
// inside the container, and nothing when the test fails.
func (s *Service) reloadIngress(ctx context.Context) (*ReloadResult, error) {
	edge, err := s.ingress(ctx)
	if err != nil {
		return nil, err
	}
	started := time.Now()
	validation, err := edge.validate(context.WithoutCancel(ctx))
	if err != nil {
		return nil, err
	}
	res := &ReloadResult{Validation: validation}
	s.remember(KindCaddyIngress, started, res.Validation)
	if !res.Validation.Valid {
		return res, ErrInvalidConf
	}
	out, err := hostexec.Command(ctx, "docker", "exec", edge.ID,
		"caddy", "reload", "--config", ingressCaddyfile, "--adapter", "caddyfile").CombinedOutput()
	res.Output = strings.TrimSpace(string(out))
	res.Reloaded = err == nil
	if err != nil {
		return res, fmt.Errorf("reload failed: %s", res.Output)
	}
	return res, nil
}
