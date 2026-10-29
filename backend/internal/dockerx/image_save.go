package dockerx

import (
	"context"
	"io"
)

// SaveImage streams one image as the archive `docker save` writes: its
// manifest, config and layers. The caller closes the reader. It is how an
// image reaches a container that has no socket to ask the daemon with.
func (c *Client) SaveImage(ctx context.Context, ref string) (io.ReadCloser, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	return cli.ImageSave(ctx, []string{ref})
}
