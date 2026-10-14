package files

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
)

func TestWriteCheckedPreservesAChangedOrRemovedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config")
	s := New([]string{root})
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	opened, err := s.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteChecked(path, "draft\n", &opened.Revision); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	current, _ := os.ReadFile(path)
	if string(current) != "new\n" {
		t.Fatalf("native edit lost: %q", current)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteChecked(path, "draft\n", &opened.Revision); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("deleted target: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("deleted file recreated: %v", err)
	}
}

func TestWriteCheckedAllowsOnlyOneSaveFromTheSameRevision(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config")
	if err := os.WriteFile(path, []byte("opened"), 0o640); err != nil {
		t.Fatal(err)
	}
	expected := contentguard.Revision("opened")
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, draft := range []string{"first", "second"} {
		go func() {
			ready.Done()
			<-start
			// Separate Services still share the lock; Files and Compose do too.
			results <- New([]string{root}).WriteChecked(path, draft, &expected)
		}()
	}
	ready.Wait()
	close(start)
	ok, conflict := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
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

func TestWriteCheckedSaveAsRequiresAnUnusedName(t *testing.T) {
	root := t.TempDir()
	s := New([]string{root})
	path := filepath.Join(root, "copy")
	absent := ""
	if err := s.WriteChecked(path, "draft", &absent); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteChecked(path, "replacement", &absent); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("occupied Save As: %v", err)
	}
	if err := s.WriteChecked(filepath.Join(root, "..", "outside"), "draft", &absent); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("root guard: %v", err)
	}
}

func TestWriteCheckedRefusesAFIFOWithoutWaitingForAWriter(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	expected := contentguard.Revision("opened regular file")
	done := make(chan error, 1)
	go func() { done <- New([]string{root}).WriteChecked(path, "draft", &expected) }()
	select {
	case err := <-done:
		if !errors.Is(err, contentguard.ErrConflict) {
			t.Fatalf("special-file save: %v", err)
		}
	case <-time.After(time.Second):
		// Unblock the old blocking-open implementation before failing the test.
		if writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			writer.Close()
		}
		t.Fatal("save waited for a FIFO writer")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO was replaced: info=%v err=%v", info, err)
	}
}
