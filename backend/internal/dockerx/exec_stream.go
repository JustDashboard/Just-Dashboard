package dockerx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// execLineBytes bounds one line of a command's output, for a tool that
// prints without newlines.
const execLineBytes = 64 << 10

// ExecStream runs one command in a running container as user, with an
// argument vector, env added to the container's own and no TTY, and hands
// every line it prints to out as it prints it, standard output and standard
// error apart. It returns the command's exit status; a status that is not
// zero is not an error, because what it means is the command's to say.
//
// It is the long-running sibling of ExecCheck: a base backup prints for as
// long as it takes, and its lines are the progress a page shows.
func (c *Client) ExecStream(ctx context.Context, id string, cmd []string, user string, env []string, out func(LogLine)) (int, error) {
	if len(cmd) == 0 {
		return -1, errors.New("exec command is empty")
	}
	cli, err := c.api()
	if err != nil {
		return -1, err
	}
	created, err := cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		AttachStdout: true, AttachStderr: true, Tty: false, User: user, Env: env, Cmd: append([]string(nil), cmd...),
	})
	if err != nil {
		return -1, err
	}
	attached, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: false})
	if err != nil {
		return -1, err
	}
	defer attached.Close()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			attached.Close()
		case <-done:
		}
	}()
	var mu sync.Mutex
	stdout := &lineWriter{stream: "stdout", mu: &mu, out: out}
	stderr := &lineWriter{stream: "stderr", mu: &mu, out: out}
	_, copyErr := stdcopy.StdCopy(stdout, stderr, attached.Reader)
	close(done)
	stdout.flush()
	stderr.flush()
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		return -1, copyErr
	}
	inspected, err := cli.ContainerExecInspect(context.WithoutCancel(ctx), created.ID)
	if err != nil {
		return -1, err
	}
	if inspected.Running {
		return -1, fmt.Errorf("the command in %s did not exit", id)
	}
	return inspected.ExitCode, nil
}

// lineWriter turns one stream of a command's output into lines. The two
// streams share a lock so that a caller's out is never called from both at
// once.
type lineWriter struct {
	stream  string
	mu      *sync.Mutex
	out     func(LogLine)
	partial bytes.Buffer
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			w.emit()
			continue
		}
		if w.partial.Len() < execLineBytes {
			w.partial.WriteByte(b)
		}
	}
	return len(p), nil
}

func (w *lineWriter) emit() {
	text := bytes.TrimRight(w.partial.Bytes(), "\r")
	if w.out != nil {
		w.out(LogLine{Stream: w.stream, Text: string(text)})
	}
	w.partial.Reset()
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.partial.Len() > 0 {
		w.emit()
	}
}

// CopyArchive unpacks a tar archive into a directory of a container, created
// or running, and into whatever volume is mounted there. The archive's own
// ownership and modes are kept, which is how a file is handed to an account
// inside the container that this process has no name for.
func (c *Client) CopyArchive(ctx context.Context, id, dir string, archive io.Reader) error {
	if err := validContainerPath(dir); err != nil {
		return err
	}
	cli, err := c.api()
	if err != nil {
		return err
	}
	return cli.CopyToContainer(ctx, id, dir, archive, container.CopyToContainerOptions{})
}
