package dockerx

import (
	"archive/tar"
	"context"
	"encoding/binary"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/container"
)

var pythonCacheName = regexp.MustCompile(`^(.+)\.cpython-[0-9]+(?:\.opt-[012])?\.pyc$`)

// A cache is expendable only when unchanged source exists and its timestamp
// header matches that source. Sourceless and unverifiable bytecode stays data.
func (c *Client) captureRegenerablePythonCaches(ctx context.Context, capture *AdoptionContainer) {
	cli, err := c.api()
	if err != nil {
		return
	}
	for _, change := range capture.Changes {
		match := pythonCacheName.FindStringSubmatch(path.Base(change.Path))
		if change.Kind != container.ChangeAdd || len(match) != 2 || path.Base(path.Dir(change.Path)) != "__pycache__" || !capture.ChangeModes[change.Path].IsRegular() {
			continue
		}
		source := path.Join(path.Dir(path.Dir(change.Path)), match[1]+".py")
		changed := false
		for _, candidate := range capture.Changes {
			if candidate.Path == source || (candidate.Kind == container.ChangeDelete && strings.HasPrefix(source, candidate.Path+"/")) {
				changed = true
				break
			}
		}
		if changed {
			continue
		}
		stat, err := cli.ContainerStatPath(ctx, capture.Inspection.ID, source)
		if err != nil || !stat.Mode.IsRegular() || stat.Size < 0 || stat.Size > 8<<20 {
			continue
		}
		stream, _, err := cli.CopyFromContainer(ctx, capture.Inspection.ID, change.Path)
		if err != nil {
			continue
		}
		reader := tar.NewReader(io.LimitReader(stream, 64<<10))
		header, err := reader.Next()
		var raw [16]byte
		if err == nil && header.Typeflag == tar.TypeReg {
			_, err = io.ReadFull(reader, raw[:])
		} else {
			stream.Close()
			continue
		}
		stream.Close()
		if err == nil && pythonCacheMatchesSource(raw[:], uint32(stat.Mtime.Unix()), uint32(stat.Size)) {
			capture.VerifiedPythonCaches = append(capture.VerifiedPythonCaches, change.Path)
		}
	}
}

func pythonCacheMatchesSource(header []byte, modified, size uint32) bool {
	return len(header) == 16 && header[2] == '\r' && header[3] == '\n' && binary.LittleEndian.Uint32(header[4:8]) == 0 && binary.LittleEndian.Uint32(header[8:12]) == modified && binary.LittleEndian.Uint32(header[12:16]) == size
}
