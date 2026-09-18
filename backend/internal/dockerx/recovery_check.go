package dockerx

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// CompletedCommandOutput waits for a disposable non-TTY checker. Only a
// bounded stdout is returned; stderr and Docker logs never become recovery
// evidence, where application output could disclose restored data.
func (c *Client) CompletedCommandOutput(ctx context.Context, id string) ([]byte, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	result, failed := cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-failed:
		if err == nil {
			err = errors.New("recovery command wait ended without an exit status")
		}
		return nil, err
	case ended := <-result:
		if ended.Error != nil || ended.StatusCode != 0 {
			return nil, fmt.Errorf("recovery command exited with status %d", ended.StatusCode)
		}
	}
	stream, err := cli.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true})
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	output := &boundedCheckOutput{limit: (64 << 10) + 1}
	// Multiplexed Docker framing is bounded too. A huge or endlessly growing
	// result cannot make a check pass by matching a truncated prefix.
	limited := &io.LimitedReader{R: stream, N: (128 << 10) + 1}
	if _, err := stdcopy.StdCopy(output, io.Discard, limited); err != nil {
		return nil, errors.New("recovery command output exceeded its bound or was incomplete")
	}
	if output.Len() >= 64<<10 || limited.N == 0 {
		return nil, errors.New("recovery command output exceeded its bound")
	}
	return output.Bytes(), nil
}
