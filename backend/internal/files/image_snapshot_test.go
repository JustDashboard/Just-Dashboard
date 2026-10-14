package files

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
)

func TestImageSnapshotRevisionNamesExactBytesAndRefusesNonregularFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "image.png")
	s := New([]string{root})
	opened := []byte{'p', 'n', 'g', 0, 255, 128}
	if err := os.WriteFile(path, opened, 0o640); err != nil {
		t.Fatal(err)
	}
	content, revision, err := s.ImageSnapshot(path)
	if err != nil || !bytes.Equal(content, opened) || revision != contentguard.RevisionBytes(content) {
		t.Fatalf("snapshot = %q, %q, %v", content, revision, err)
	}
	if _, _, err := s.ImageSnapshot(filepath.Join(root, "..", "outside.png")); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("root containment = %v", err)
	}
	pipe := filepath.Join(root, "pipe.png")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ImageSnapshot(pipe); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("FIFO snapshot = %v", err)
	}
	large := filepath.Join(root, "large.png")
	f, err := os.Create(large)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxImageEditBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err := s.ImageSnapshot(large); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("snapshot bound = %v", err)
	}
}

type beforeRead struct {
	r      io.Reader
	before func()
}

func (r *beforeRead) Read(p []byte) (int, error) {
	if r.before != nil {
		r.before()
		r.before = nil
	}
	return r.r.Read(p)
}

func TestImageUploadCheckedDetectsNativeReplacementDuringTransfer(t *testing.T) {
	for _, change := range []string{"replace", "remove", "retarget"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "image.png")
			s := New([]string{root})
			if err := os.WriteFile(path, []byte("opened\x00"), 0o640); err != nil {
				t.Fatal(err)
			}
			_, expected, err := s.ImageSnapshot(path)
			if err != nil {
				t.Fatal(err)
			}
			newer := filepath.Join(root, "newer.png")
			reader := &beforeRead{r: strings.NewReader("draft\x00"), before: func() {
				switch change {
				case "replace":
					if err := os.WriteFile(newer, []byte("native\x00"), 0o640); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(newer, path); err != nil {
						t.Fatal(err)
					}
				case "remove":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "retarget":
					if err := os.WriteFile(newer, []byte("opened\x00"), 0o640); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(newer, path); err != nil {
						t.Fatal(err)
					}
				}
			}}
			if _, err := s.UploadChecked(path, reader, true, &expected); !errors.Is(err, contentguard.ErrConflict) {
				t.Fatalf("publication = %v", err)
			}
			if change == "replace" {
				content, _ := os.ReadFile(path)
				if string(content) != "native\x00" {
					t.Fatalf("native content lost: %q", content)
				}
			}
			if change == "remove" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("removed image recreated: %v", err)
				}
			}
			if change == "retarget" {
				content, _ := os.ReadFile(newer)
				if string(content) != "opened\x00" {
					t.Fatalf("symlink target changed: %q", content)
				}
			}
			temps, _ := filepath.Glob(filepath.Join(root, ".jd-upload-*"))
			if len(temps) != 0 {
				t.Fatalf("staging files leaked: %v", temps)
			}
		})
	}
}

func TestImageUploadAndTextWriterAcceptOnlyOneSaveFromARevision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "shared.png")
	if err := os.WriteFile(path, []byte("opened"), 0o640); err != nil {
		t.Fatal(err)
	}
	expected := contentguard.Revision("opened")
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := New([]string{root}).UploadChecked(path, strings.NewReader("image draft"), true, &expected)
		results <- err
	}()
	go func() {
		<-start
		results <- New([]string{root}).WriteChecked(path, "text draft", &expected)
	}()
	close(start)
	ok, conflict := 0, 0
	for range 2 {
		if err := <-results; err == nil {
			ok++
		} else if errors.Is(err, contentguard.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("saves=%d conflicts=%d", ok, conflict)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode changed: %v", info.Mode())
	}
}

func TestImageUploadRevisionAllowsImagesLargerThanTextLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.png")
	content := bytes.Repeat([]byte{0, 255}, (maxEditBytes+2)/2)
	if err := os.WriteFile(path, content, 0o640); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})
	_, expected, err := s.ImageSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadChecked(path, bytes.NewReader([]byte("new picture")), true, &expected); err != nil {
		t.Fatal(err)
	}
}
