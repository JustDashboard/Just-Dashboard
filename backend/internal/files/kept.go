package files

import (
	"io"
	"os"
	"syscall"
)

// maxKeptBytes is the largest content a save hands over to be kept as a
// version: the editor's own limit.
const maxKeptBytes = maxEditBytes

// Replaced is what a save found at its path in the step that published over
// it, read under the same path lock and after the same final revision check,
// so it is exactly the content the save replaced. Content is nil for a file
// larger than the editor's limit (TooLarge), and Existed is false where there
// was no regular file to replace.
type Replaced struct {
	Path     string
	Existed  bool
	Size     int64
	Content  []byte
	TooLarge bool
}

// WriteKept is WriteChecked that also says what the write replaced. A
// failure to read the old content is not a failure of the write: it leaves
// Content nil.
func (s *Service) WriteKept(path, content string, expected *string) (*Replaced, error) {
	kept := &Replaced{}
	err := s.writeChecked(path, content, expected, kept)
	return kept, err
}

// UploadKept is UploadChecked that also says what the upload replaced.
func (s *Service) UploadKept(path string, r io.Reader, overwrite bool, expected *string) (int64, *Replaced, error) {
	kept := &Replaced{}
	n, err := s.uploadChecked(path, r, overwrite, expected, kept)
	return n, kept, err
}

// keep reads what is about to be replaced, through an already opened file:
// nonblocking and checked as a regular file after opening, as the revision
// guard reads it, so a FIFO swapped in never holds the save waiting.
func (kept *Replaced) keep(path string, file *os.File, err error) {
	if kept == nil {
		return
	}
	kept.Path = path
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	kept.Existed, kept.Size = true, info.Size()
	if info.Size() > maxKeptBytes {
		kept.TooLarge = true
		return
	}
	content, err := io.ReadAll(io.LimitReader(file, maxKeptBytes+1))
	if err != nil {
		return
	}
	if int64(len(content)) > maxKeptBytes {
		kept.TooLarge, kept.Size = true, int64(len(content))
		return
	}
	kept.Size, kept.Content = int64(len(content)), content
}

func (kept *Replaced) keepPath(path string) {
	if kept == nil {
		return
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	kept.keep(path, file, err)
}

func (kept *Replaced) keepIn(parent *os.Root, name, path string) {
	if kept == nil {
		return
	}
	file, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	kept.keep(path, file, err)
}
