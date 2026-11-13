package dockerx

import (
	"bytes"
	"context"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
)

// InspectRunning is every running container as the daemon describes it, in
// one JSON array: what `docker inspect $(docker ps -q)` prints, without the
// two processes.
//
// The proxy looks its Docker ingress up from that output, every thirty
// seconds and on its page reads, and used to start the docker CLI twice for
// it each time: several thousand processes a day on a host where nobody was
// looking. The answer is the same bytes, so the code that reads it did not
// change.
//
// A container that went away between the listing and its inspect is left
// out. The CLI failed the whole command for it, and the lookup with it.
func (c *Client) InspectRunning(ctx context.Context) ([]byte, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	items, err := cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteByte('[')
	for _, item := range items {
		_, raw, err := cli.ContainerInspectWithRaw(ctx, item.ID, false)
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if out.Len() > 1 {
			out.WriteByte(',')
		}
		out.Write(raw)
	}
	out.WriteByte(']')
	return out.Bytes(), nil
}

// InspectOne is `docker inspect --type container name`: an array of the one
// container of that name or id, running or not, and an error when there is
// none.
func (c *Client) InspectOne(ctx context.Context, name string) ([]byte, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	_, raw, err := cli.ContainerInspectWithRaw(ctx, name, false)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(raw)+2)
	out = append(out, '[')
	out = append(out, raw...)
	return append(out, ']'), nil
}
