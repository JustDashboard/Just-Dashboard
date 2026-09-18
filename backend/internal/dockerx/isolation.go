package dockerx

import (
	"context"
	"errors"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
)

// StopOwnedForIsolation preserves the container and its data, but prevents a
// daemon restart from reviving it. Identity is checked again at the mutation.
func (c *Client) StopOwnedForIsolation(ctx context.Context, id string, labels map[string]string) error {
	if id == "" || len(labels) == 0 {
		return errors.New("container isolation requires an exact owner")
	}
	cli, err := c.api()
	if err != nil {
		return err
	}
	detail, err := cli.ContainerInspect(ctx, id)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if detail.Config == nil || detail.ID != id {
		return errors.New("container isolation identity changed")
	}
	for key, value := range labels {
		if detail.Config.Labels[key] != value {
			return errors.New("container isolation ownership changed")
		}
	}
	_, updateErr := cli.ContainerUpdate(ctx, id, container.UpdateConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
	})
	if detail.State != nil && detail.State.Paused {
		if err := cli.ContainerUnpause(ctx, id); err != nil && !errdefs.IsNotModified(err) {
			return errors.Join(updateErr, err)
		}
	}
	grace := 5
	stopErr := cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &grace})
	if errdefs.IsNotFound(stopErr) {
		return nil
	}
	if errdefs.IsNotModified(stopErr) {
		stopErr = nil
	}
	if err := errors.Join(updateErr, stopErr); err != nil {
		return err
	}
	detail, err = cli.ContainerInspect(ctx, id)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if detail.State == nil || detail.State.Running || detail.State.Restarting || detail.HostConfig == nil ||
		detail.HostConfig.RestartPolicy.Name != container.RestartPolicyDisabled {
		return errors.New("container isolation could not be verified")
	}
	return nil
}
