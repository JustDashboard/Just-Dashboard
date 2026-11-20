package files

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
)

func TestWriteKeptHandsOverExactlyWhatItReplaced(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "app.conf")
	s := New([]string{root})

	kept, err := s.WriteKept(path, "first\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Existed || kept.Content != nil || kept.Path != path {
		t.Fatalf("a new file replaced something: %+v", kept)
	}
	// A shell edit between the open and the save is what is replaced, and so
	// what is handed over.
	if err := os.WriteFile(path, []byte("edited in a shell\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	expected := contentguard.Revision("edited in a shell\n")
	kept, err = s.WriteKept(path, "second\n", &expected)
	if err != nil {
		t.Fatal(err)
	}
	if !kept.Existed || string(kept.Content) != "edited in a shell\n" || kept.Size != int64(len("edited in a shell\n")) {
		t.Fatalf("kept %+v", kept)
	}
	// A refused save hands nothing over as replaced.
	stale := contentguard.Revision("nothing like it")
	if _, err := s.WriteKept(path, "third\n", &stale); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	if current, _ := os.ReadFile(path); string(current) != "second\n" {
		t.Fatalf("stale save published: %q", current)
	}
}

func TestWriteKeptDoesNotHoldAFileOverTheEditorsLimit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxKeptBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	kept, err := New([]string{root}).WriteKept(path, "small", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !kept.Existed || !kept.TooLarge || kept.Content != nil || kept.Size != maxKeptBytes+1 {
		t.Fatalf("kept %+v", kept)
	}
}

func TestUploadKeptHandsOverTheReplacedPicture(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "logo.png")
	if err := os.WriteFile(path, []byte("\x89PNG old"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})
	expected := contentguard.RevisionBytes([]byte("\x89PNG old"))
	_, kept, err := s.UploadKept(path, strings.NewReader("\x89PNG new"), true, &expected)
	if err != nil {
		t.Fatal(err)
	}
	if !kept.Existed || string(kept.Content) != "\x89PNG old" {
		t.Fatalf("kept %+v", kept)
	}
	_, kept, err = s.UploadKept(filepath.Join(root, "fresh.png"), strings.NewReader("new"), false, nil)
	if err != nil || kept.Existed {
		t.Fatalf("a new upload replaced something: %+v %v", kept, err)
	}
}
