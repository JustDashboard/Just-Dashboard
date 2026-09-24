package dockerx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
)

// RunToCompletion runs one container from an already present image until its
// process exits, streams everything it prints, and removes it however the
// run ends. It is how a deployment's release task runs inside the release's
// own image — with the application's toolchain — instead of on the host:
// `docker run --rm`, with the container's lifetime owned here so a
// cancelled or timed-out task leaves nothing behind.
func (c *Client) RunToCompletion(ctx context.Context, spec ContainerSpec, out chan<- LogLine) (int, error) {
	if c == nil {
		return -1, errors.New("docker client is unavailable")
	}
	spec.Start, spec.AutoRemove, spec.RestartPolicy, spec.Pull = false, false, "no", "missing"
	created, err := c.Create(ctx, spec, nil)
	if err != nil {
		return -1, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = c.RemoveContainer(cleanup, created.ID, true, true)
	}()
	cli, err := c.api()
	if err != nil {
		return -1, err
	}
	// Wait is registered before the start so an instant exit is not missed.
	waitResult, waitErr := cli.ContainerWait(ctx, created.ID, container.WaitConditionNextExit)
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return -1, err
	}
	lines, closer, err := c.Logs(ctx, created.ID, LogOptions{Follow: true, Tail: "all"})
	if err != nil {
		return -1, err
	}
	defer closer.Close()
	for line := range lines {
		if out == nil {
			continue
		}
		select {
		case out <- line:
		case <-ctx.Done():
		}
	}
	select {
	case result := <-waitResult:
		if result.Error != nil {
			return -1, fmt.Errorf("wait for container: %s", result.Error.Message)
		}
		return int(result.StatusCode), nil
	case err := <-waitErr:
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		return -1, err
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}
