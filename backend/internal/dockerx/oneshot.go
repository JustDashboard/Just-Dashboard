package dockerx

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
)

// RunToCompletion runs one container from an already present image until its
// process exits, streams everything it prints, and removes it however the
// run ends. It is how a deployment's release task runs inside the release's
// own image — with the application's toolchain — instead of on the host:
// `docker run --rm`, with the container's lifetime owned here so a
// cancelled or timed-out task leaves nothing behind. removed reports whether
// that held: false only when a created container could not be removed.
func (c *Client) RunToCompletion(ctx context.Context, spec ContainerSpec, out chan<- LogLine) (code int, removed bool, err error) {
	if c == nil {
		return -1, true, errors.New("docker client is unavailable")
	}
	spec.Start, spec.AutoRemove, spec.RestartPolicy, spec.Pull = false, false, "no", "missing"
	created, err := c.Create(ctx, spec, nil)
	if err != nil {
		return -1, true, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		removeErr := c.RemoveContainer(cleanup, created.ID, true, true)
		removed = removeErr == nil || errdefs.IsNotFound(removeErr)
	}()
	cli, err := c.api()
	if err != nil {
		return -1, false, err
	}
	// Wait is registered before the start so an instant exit is not missed.
	waitResult, waitErr := cli.ContainerWait(ctx, created.ID, container.WaitConditionNextExit)
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return -1, false, err
	}
	lines, closer, err := c.Logs(ctx, created.ID, LogOptions{Follow: true, Tail: "all"})
	if err != nil {
		return -1, false, err
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
			return -1, false, fmt.Errorf("wait for container: %s", result.Error.Message)
		}
		return int(result.StatusCode), false, nil
	case err := <-waitErr:
		if ctx.Err() != nil {
			return -1, false, ctx.Err()
		}
		return -1, false, err
	case <-ctx.Done():
		return -1, false, ctx.Err()
	}
}
