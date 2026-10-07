package dockerx

import "context"

// ContainerPID is a running container's init process as the host numbers it,
// which is how its network namespace is reached from outside. Zero for a
// container that is not running.
func (c *Client) ContainerPID(ctx context.Context, id string) (int, error) {
	insp, err := c.inspectContainer(ctx, id)
	if err != nil {
		return 0, err
	}
	if insp.State == nil || !insp.State.Running {
		return 0, nil
	}
	return insp.State.Pid, nil
}
