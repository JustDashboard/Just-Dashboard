package dockerx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// ContainerLiveness is what a readiness gate reads of its candidate between
// attempts: whether it is up, and how often it has died.
type ContainerLiveness struct {
	Status       string
	Running      bool
	Restarting   bool
	ExitCode     int
	RestartCount int
}

// ContainerLiveness reads the container's state from one inspect and nothing
// else: no log, no health output.
func (c *Client) ContainerLiveness(ctx context.Context, id string) (ContainerLiveness, error) {
	cli, err := c.api()
	if err != nil {
		return ContainerLiveness{}, err
	}
	inspected, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return ContainerLiveness{}, err
	}
	if inspected.ContainerJSONBase == nil || inspected.State == nil {
		return ContainerLiveness{}, nil
	}
	return ContainerLiveness{
		Status: inspected.State.Status, Running: inspected.State.Running, Restarting: inspected.State.Restarting,
		ExitCode: inspected.State.ExitCode, RestartCount: inspected.RestartCount,
	}, nil
}

// ContainerHealth returns only the Engine's closed health state. Health-log
// output is deliberately excluded because application health commands often
// echo connection strings or tokens and deployment evidence never needs it.
func (c *Client) ContainerHealth(ctx context.Context, id string) (string, error) {
	cli, err := c.api()
	if err != nil {
		return "", err
	}
	inspected, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", err
	}
	if inspected.State == nil {
		return "", nil
	}
	if inspected.State.Health != nil {
		return inspected.State.Health.Status, nil
	}
	if !inspected.State.Running && inspected.State.Status != "" {
		return inspected.State.Status, nil
	}
	return "", nil
}

// ExecCheck runs a closed argv in a container without a TTY or shell. Output
// is bounded for hashing by the deployment check runner; it is never copied
// verbatim into release evidence.
func (c *Client) ExecCheck(
	ctx context.Context,
	id string,
	command []string,
	timeout time.Duration,
) (int, []byte, error) {
	if len(command) == 0 {
		return -1, nil, errors.New("check command is empty")
	}
	cli, err := c.api()
	if err != nil {
		return -1, nil, err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	created, err := cli.ContainerExecCreate(child, id, container.ExecOptions{
		AttachStdout: true, AttachStderr: true, Tty: false, Cmd: append([]string(nil), command...),
	})
	if err != nil {
		return -1, nil, err
	}
	attached, err := cli.ContainerExecAttach(child, created.ID, container.ExecAttachOptions{Tty: false})
	if err != nil {
		return -1, nil, err
	}
	defer attached.Close()
	done := make(chan struct{})
	go func() {
		select {
		case <-child.Done():
			attached.Close()
		case <-done:
		}
	}()
	output := &boundedCheckOutput{limit: 64 << 10}
	_, copyErr := stdcopy.StdCopy(output, output, attached.Reader)
	close(done)
	if child.Err() != nil {
		return -1, output.Bytes(), child.Err()
	}
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return -1, output.Bytes(), copyErr
	}
	inspected, err := cli.ContainerExecInspect(child, created.ID)
	if err != nil {
		return -1, output.Bytes(), err
	}
	if inspected.Running {
		return -1, output.Bytes(), fmt.Errorf("container check command did not exit")
	}
	if inspected.ExitCode != 0 {
		return inspected.ExitCode, output.Bytes(), fmt.Errorf("container check exited with code %d", inspected.ExitCode)
	}
	return 0, output.Bytes(), nil
}

type boundedCheckOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedCheckOutput) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.Buffer.Write(value)
	}
	return original, nil
}
