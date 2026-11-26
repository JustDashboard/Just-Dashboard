package dockerx

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// ExecOutput is a finished command's streams and exit status.
type ExecOutput struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// stackExecLimit bounds what one configuration command may print: enough for
// a configuration file or a tool's report, not a log.
const stackExecLimit = 1 << 20

// ExecCapture runs a closed argv in a container, as user when one is named,
// with no TTY and no shell, and returns its two streams separately. It is the
// smart stacks' way to make a folder as the account a service runs as, or to
// run an application's own command-line tool; argv is built by reviewed code,
// never from a request.
func (c *Client) ExecCapture(ctx context.Context, id, user string, command []string, timeout time.Duration) (ExecOutput, error) {
	if len(command) == 0 {
		return ExecOutput{ExitCode: -1}, errors.New("command is empty")
	}
	cli, err := c.api()
	if err != nil {
		return ExecOutput{ExitCode: -1}, err
	}
	if timeout <= 0 {
		timeout = time.Minute
	}
	child, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	created, err := cli.ContainerExecCreate(child, id, container.ExecOptions{
		User: user, AttachStdout: true, AttachStderr: true, Tty: false, Cmd: append([]string(nil), command...),
	})
	if err != nil {
		return ExecOutput{ExitCode: -1}, err
	}
	attached, err := cli.ContainerExecAttach(child, created.ID, container.ExecAttachOptions{Tty: false})
	if err != nil {
		return ExecOutput{ExitCode: -1}, err
	}
	defer attached.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-child.Done():
			attached.Close()
		case <-done:
		}
	}()
	stdout := &boundedBuffer{limit: stackExecLimit}
	stderr := &boundedBuffer{limit: stackExecLimit}
	if _, err := stdcopy.StdCopy(stdout, stderr, attached.Reader); err != nil && child.Err() == nil {
		return ExecOutput{ExitCode: -1}, err
	}
	if child.Err() != nil {
		return ExecOutput{ExitCode: -1}, child.Err()
	}
	inspected, err := cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return ExecOutput{ExitCode: -1}, err
	}
	return ExecOutput{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: inspected.ExitCode}, nil
}

// boundedBuffer keeps the first limit bytes and drops the rest, so a command
// that prints without end cannot grow the dashboard's memory.
type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}
