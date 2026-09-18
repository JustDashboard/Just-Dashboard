package dockerx

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
)

// maxContainerFileBytes bounds both directions. These reads exist for
// configuration files an operator edits, not for pulling a world out of a
// container; anything larger belongs in a backup.
const maxContainerFileBytes = 4 << 20

var ErrContainerFileTooLarge = errors.New("container file exceeds the 4 MiB edit limit")

func validContainerPath(target string) error {
	if !strings.HasPrefix(target, "/") || strings.Contains(target, "\x00") {
		return fmt.Errorf("container path %q must be absolute", target)
	}
	if path.Clean(target) != target || strings.Contains(target, "..") {
		return fmt.Errorf("container path %q is not a normalized path", target)
	}
	return nil
}

// ReadContainerFile copies one regular file out of a container. It reads the
// first entry of the archive the Engine returns and refuses anything that is
// not a regular file, so a symlink planted inside the container cannot redirect
// the read.
func (c *Client) ReadContainerFile(ctx context.Context, id, target string) ([]byte, error) {
	if err := validContainerPath(target); err != nil {
		return nil, err
	}
	cli, err := c.api()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stream, _, err := cli.CopyFromContainer(ctx, id, target)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	reader := tar.NewReader(io.LimitReader(stream, maxContainerFileBytes+512<<10))
	header, err := reader.Next()
	if err != nil {
		return nil, fmt.Errorf("%s holds no readable file", target)
	}
	if header.Typeflag != tar.TypeReg {
		return nil, fmt.Errorf("%s is not a regular file", target)
	}
	if header.Size > maxContainerFileBytes {
		return nil, ErrContainerFileTooLarge
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxContainerFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxContainerFileBytes {
		return nil, ErrContainerFileTooLarge
	}
	return content, nil
}

// WriteContainerFile replaces one regular file inside a container, keeping its
// mode. The archive contains exactly one entry named after the file, so the
// Engine cannot be asked to unpack anything else.
func (c *Client) WriteContainerFile(ctx context.Context, id, target string, content []byte, mode int64) error {
	if err := validContainerPath(target); err != nil {
		return err
	}
	if int64(len(content)) > maxContainerFileBytes {
		return ErrContainerFileTooLarge
	}
	if mode <= 0 {
		mode = 0o644
	}
	cli, err := c.api()
	if err != nil {
		return err
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err := writer.WriteHeader(&tar.Header{
		Name: path.Base(target), Mode: mode, Size: int64(len(content)),
		Typeflag: tar.TypeReg, ModTime: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if _, err := writer.Write(content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return cli.CopyToContainer(ctx, id, path.Dir(target), &archive, container.CopyToContainerOptions{})
}
