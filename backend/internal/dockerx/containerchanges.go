package dockerx

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
)

// ChangedFiles lists what a container added to or changed in its own writable
// layer, as paths inside the container. It is `docker diff`, which the Engine
// answers for a stopped container as readily as for a running one and
// whatever storage driver holds the layer.
//
// That layer is where an application keeps a database when nobody gave it a
// volume, and it is the one place a walk of the host's filesystem cannot
// follow: the layer's directory is the storage driver's own business.
func (c *Client) ChangedFiles(ctx context.Context, id string) ([]string, error) {
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	changes, err := cli.ContainerDiff(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(changes))
	for _, change := range changes {
		if change.Kind == container.ChangeAdd || change.Kind == container.ChangeModify {
			out = append(out, change.Path)
		}
	}
	return out, nil
}

// ContainerFileHead reads the first n bytes of one regular file in a
// container, with the file's size and modification time.
//
// ReadContainerFile takes the whole file, and refuses one over its edit limit.
// Reading what kind of file something is needs sixteen bytes of it, and the
// file in question may be a database of any size: the archive is abandoned as
// soon as the head has been read.
func (c *Client) ContainerFileHead(ctx context.Context, id, target string, n int) ([]byte, int64, time.Time, error) {
	if err := validContainerPath(target); err != nil {
		return nil, 0, time.Time{}, err
	}
	cli, err := c.api()
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stream, _, err := cli.CopyFromContainer(ctx, id, target)
	if err != nil {
		return nil, 0, time.Time{}, err
	}
	defer stream.Close()
	reader := tar.NewReader(stream)
	header, err := reader.Next()
	if err != nil {
		return nil, 0, time.Time{}, fmt.Errorf("%s holds no readable file", target)
	}
	if header.Typeflag != tar.TypeReg {
		return nil, 0, time.Time{}, fmt.Errorf("%s is not a regular file", target)
	}
	head := make([]byte, n)
	read, err := io.ReadFull(reader, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, 0, time.Time{}, err
	}
	return head[:read], header.Size, header.ModTime.UTC(), nil
}
