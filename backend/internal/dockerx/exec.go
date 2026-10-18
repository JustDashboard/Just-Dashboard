package dockerx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// ExecSession is an interactive shell inside a container. It is functionally
// equivalent to handing the caller a root shell on the host — a container can
// be started privileged, and the socket that created this session can create
// another one with the host filesystem mounted. It is gated on the terminal
// capability and recorded in the audit trail at open time.
type ExecSession struct {
	ID     string
	Conn   io.ReadWriteCloser
	client *Client
}

var ErrNoShell = errors.New("no usable shell found in container (tried bash, sh)")

// Exec starts a shell. When no command is given it probes for bash and falls
// back to sh, which is the behaviour operators expect from a "connect" button.
func (c *Client) Exec(ctx context.Context, id string, cmd []string, user string, rows, cols uint) (*ExecSession, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	if len(cmd) == 0 {
		shell, err := c.detectShell(ctx, id)
		if err != nil {
			return nil, err
		}
		cmd = []string{shell}
	}
	created, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		User:         user,
		Cmd:          cmd,
		Env:          []string{"TERM=xterm-256color"},
	})
	if err != nil {
		return nil, err
	}
	att, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return nil, err
	}
	sess := &ExecSession{ID: created.ID, Conn: att.Conn, client: c}
	if rows > 0 && cols > 0 {
		sess.Resize(ctx, rows, cols)
	}
	return sess, nil
}

func (c *Client) detectShell(ctx context.Context, id string) (string, error) {
	cli, err := c.api()
	if err != nil {
		return "", err
	}
	for _, sh := range []string{"/bin/bash", "/bin/sh"} {
		created, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{
			Cmd: []string{sh, "-c", "exit 0"},
		})
		if err != nil {
			continue
		}
		att, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
		if err != nil {
			continue
		}
		io.Copy(io.Discard, att.Reader)
		att.Close()
		insp, err := cli.ContainerExecInspect(ctx, created.ID)
		if err == nil && insp.ExitCode == 0 {
			return sh, nil
		}
	}
	return "", ErrNoShell
}

// ExecStreams are the standard streams of a command run without a terminal.
// A nil Stdin runs it with none, and a nil writer discards what it prints.
type ExecStreams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExecPipe runs a closed argv in a container with its standard streams joined
// to the caller's, and returns its exit code once it has exited.
//
// It is how a server's own tools are run beside its own data — an archive
// written by one container's pg_dump and read by another's pg_restore — with
// the bytes passing through this process rather than through a shell, a file
// or a network path between the two containers. Env carries what a tool reads
// from its environment, a password among it, so that nothing secret is an
// argument. Ending ctx closes the streams, which a tool writing to or reading
// from them stops on: Docker has no way to signal an exec'd process.
func (c *Client) ExecPipe(ctx context.Context, id, user string, cmd, env []string, streams ExecStreams) (int, error) {
	if len(cmd) == 0 {
		return -1, errors.New("exec command is empty")
	}
	cli, err := c.api()
	if err != nil {
		return -1, err
	}
	created, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		User: user, Cmd: append([]string(nil), cmd...), Env: env,
		AttachStdin: streams.Stdin != nil, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return -1, err
	}
	attached, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return -1, err
	}
	defer attached.Close()
	stop := context.AfterFunc(ctx, attached.Close)
	defer stop()
	if streams.Stdin != nil {
		// Not waited for: a command that exits before reading all of its
		// input leaves this blocked on a reader the caller closes, and its
		// exit code is the answer whatever the copy did.
		go func() {
			_, _ = io.Copy(attached.Conn, streams.Stdin)
			_ = attached.CloseWrite()
		}()
	}
	stdout, stderr := streams.Stdout, streams.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	_, copyErr := stdcopy.StdCopy(stdout, stderr, attached.Reader)
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return -1, copyErr
	}
	// The output ends a moment before the Engine records the exit.
	for wait := 0; ; wait++ {
		inspected, err := cli.ContainerExecInspect(ctx, created.ID)
		if err != nil {
			return -1, err
		}
		if !inspected.Running {
			return inspected.ExitCode, nil
		}
		if wait == 100 {
			return -1, fmt.Errorf("%s closed its output and did not exit", cmd[0])
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (s *ExecSession) Resize(ctx context.Context, rows, cols uint) error {
	cli, err := s.client.api()
	if err != nil {
		return err
	}
	return cli.ContainerExecResize(ctx, s.ID, container.ResizeOptions{Height: rows, Width: cols})
}

func (s *ExecSession) Close() error { return s.Conn.Close() }
