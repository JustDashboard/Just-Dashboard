package dockerx

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
)

// PathOwner is what a container path is, read through the Engine's archive
// API rather than a process inside it: whether it exists, whether it is a
// directory, and who owns it.
type PathOwner struct {
	Exists bool
	Dir    bool
	UID    int
	GID    int
}

// PathOwnership reads a path of a created container, started or not. A
// directory's owner comes from the first header of its archive, which is the
// directory itself; the stream is closed after that one header, so asking
// about a volume holding a world does not copy the world.
func (c *Client) PathOwnership(ctx context.Context, id, target string) (PathOwner, error) {
	if err := validContainerPath(target); err != nil {
		return PathOwner{}, err
	}
	cli, err := c.api()
	if err != nil {
		return PathOwner{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stat, err := cli.ContainerStatPath(ctx, id, target)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return PathOwner{}, nil
		}
		return PathOwner{}, err
	}
	owner := PathOwner{Exists: true, Dir: stat.Mode.IsDir()}
	stream, _, err := cli.CopyFromContainer(ctx, id, target)
	if err != nil {
		return PathOwner{}, err
	}
	defer stream.Close()
	header, err := tar.NewReader(stream).Next()
	if err != nil {
		return PathOwner{}, fmt.Errorf("%s could not be read: %w", target, err)
	}
	owner.UID, owner.GID = header.Uid, header.Gid
	return owner, nil
}

// WriteContainerTree writes one regular file under base, an existing
// directory of the container, creating the directories between them. Every
// entry it creates is owned by uid:gid, which the caller reads from base, so
// a file written into a volume belongs to whoever the image's process runs as
// and not to root. The archive holds only base's missing descendants: an
// existing directory is never rewritten, so its mode and owner stay the
// image's.
func (c *Client) WriteContainerTree(ctx context.Context, id, base, relative string, content []byte, mode int64, uid, gid int) error {
	if err := validContainerPath(base); err != nil {
		return err
	}
	relative = strings.TrimPrefix(relative, "/")
	if relative == "" || path.Clean(relative) != relative || strings.HasPrefix(relative, "..") {
		return fmt.Errorf("%q is not a path inside %s", relative, base)
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
	now := time.Now().UTC()
	parts := strings.Split(relative, "/")
	for index := range parts[:len(parts)-1] {
		if err := writer.WriteHeader(&tar.Header{
			Name: strings.Join(parts[:index+1], "/") + "/", Mode: 0o755, Uid: uid, Gid: gid,
			Typeflag: tar.TypeDir, ModTime: now,
		}); err != nil {
			return err
		}
	}
	if err := writer.WriteHeader(&tar.Header{
		Name: relative, Mode: mode, Size: int64(len(content)), Uid: uid, Gid: gid,
		Typeflag: tar.TypeReg, ModTime: now,
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
	return cli.CopyToContainer(ctx, id, base, &archive, container.CopyToContainerOptions{})
}

// ErrContainerFileLimit is a file larger than the caller allowed.
var ErrContainerFileLimit = errors.New("container file exceeds the allowed size")

// DigestContainerFile streams one regular file out of a container and
// returns its sha256 and size, reading at most limit bytes. Anything that is
// not a regular file is refused, as ReadContainerFile refuses it, so a
// symlink cannot stand in for the bytes being verified.
func (c *Client) DigestContainerFile(ctx context.Context, id, target string, limit int64) (string, int64, error) {
	if err := validContainerPath(target); err != nil {
		return "", 0, err
	}
	cli, err := c.api()
	if err != nil {
		return "", 0, err
	}
	stream, _, err := cli.CopyFromContainer(ctx, id, target)
	if err != nil {
		return "", 0, err
	}
	defer stream.Close()
	reader := tar.NewReader(stream)
	header, err := reader.Next()
	if err != nil {
		return "", 0, fmt.Errorf("%s holds no readable file", target)
	}
	if header.Typeflag != tar.TypeReg {
		return "", 0, fmt.Errorf("%s is not a regular file", target)
	}
	if header.Size > limit {
		return "", header.Size, ErrContainerFileLimit
	}
	hash := sha256.New()
	copied, err := io.Copy(hash, io.LimitReader(reader, limit+1))
	if err != nil {
		return "", copied, err
	}
	if copied > limit {
		return "", copied, ErrContainerFileLimit
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), copied, nil
}
