package files

import (
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
)

// Image canvases tolerate larger encoded inputs than Monaco, but a snapshot
// must still be bounded before allocating the image and its decoded canvases.
const maxImageEditBytes = 64 << 20

// ImageSnapshot returns one immutable reading: the revision always names the
// bytes the browser decodes, even when a native writer changes the open file.
func (s *Service) ImageSnapshot(path string) ([]byte, string, error) {
	full, err := s.Resolve(path)
	if err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(full, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if info.IsDir() {
		return nil, "", ErrIsDir
	}
	if !info.Mode().IsRegular() {
		return nil, "", contentguard.ErrConflict
	}
	if info.Size() > maxImageEditBytes {
		return nil, "", fmt.Errorf("%w: image editing supports files up to 64 MiB", ErrTooLarge)
	}
	content, err := io.ReadAll(io.LimitReader(f, maxImageEditBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(content) > maxImageEditBytes {
		return nil, "", fmt.Errorf("%w: image editing supports files up to 64 MiB", ErrTooLarge)
	}
	return content, contentguard.RevisionBytes(content), nil
}
