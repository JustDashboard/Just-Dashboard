package dockerx

import (
	"context"
	"time"
)

// ContainerExit is what a stopped container's last run left behind, which a
// listing cannot say: when it started and stopped, how it ended, and whether
// the kernel killed it for memory. The host alerts read it to tell a
// container that crashed from one somebody stopped, and only for a container
// that is not running, so it costs one inspect of a container that stopped
// rather than one of every container on every pass.
type ContainerExit struct {
	ExitCode   int
	OOMKilled  bool
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// LastExit inspects one container for its last run's end.
func (c *Client) LastExit(ctx context.Context, id string) (ContainerExit, error) {
	insp, err := c.inspectContainer(ctx, id)
	if err != nil {
		return ContainerExit{}, err
	}
	out := ContainerExit{}
	if insp.State == nil {
		return out, nil
	}
	out.ExitCode, out.OOMKilled, out.Error = insp.State.ExitCode, insp.State.OOMKilled, insp.State.Error
	// Docker reports a time it has not reached as the zero date in RFC 3339,
	// which parses; IsZero is how a reader tells "never" from a moment.
	if at, err := time.Parse(time.RFC3339Nano, insp.State.StartedAt); err == nil && at.Year() > 1 {
		out.StartedAt = at.UTC()
	}
	if at, err := time.Parse(time.RFC3339Nano, insp.State.FinishedAt); err == nil && at.Year() > 1 {
		out.FinishedAt = at.UTC()
	}
	return out, nil
}
