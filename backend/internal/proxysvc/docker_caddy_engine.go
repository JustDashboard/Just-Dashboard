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
// are — resolving them on this side would name a different file.
func (c *dockerCaddy) validate(ctx context.Context) *ValidationResult {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"caddy", "validate", "--config", ingressCaddyfile, "--adapter", "caddyfile"}
	cmd := hostexec.Command(ctx, "docker", append([]string{"exec", c.ID}, args...)...)
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
	return res
}

// reloadIngress is Reload for the Docker Caddy: its test, then `caddy reload`
// inside the container, and nothing when the test fails.
func (s *Service) reloadIngress(ctx context.Context) (*ReloadResult, error) {
	edge, err := s.ingress(ctx)
	if err != nil {
		return nil, err
	}
	res := &ReloadResult{Validation: edge.validate(ctx)}
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
