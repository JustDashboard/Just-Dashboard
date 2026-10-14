package dockerx

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/contentguard"
)

func TestWriteComposeFileCheckedPreservesNewContentAndBackupOnConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.yaml")
	if err := os.WriteFile(path, []byte("opened"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".bak", []byte("older backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := contentguard.Revision("opened")
	if err := os.WriteFile(path, []byte("native edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeFileChecked(path, "draft", &expected); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("stale compose save: %v", err)
	}
	for filename, want := range map[string]string{path: "native edit", path + ".bak": "older backup"} {
		current, _ := os.ReadFile(filename)
		if string(current) != want {
			t.Fatalf("%s: %q", filename, current)
		}
	}
	expected = contentguard.Revision("native edit")
	if err := WriteComposeFileChecked(path, "reviewed draft", &expected); err != nil {
		t.Fatal(err)
	}
	backup, _ := os.ReadFile(path + ".bak")
	if string(backup) != "native edit" {
		t.Fatalf("backup: %q", backup)
	}
	if err := WriteComposeFileChecked(path, "other old draft", &expected); !errors.Is(err, contentguard.ErrConflict) {
		t.Fatalf("reused compose revision: %v", err)
	}
}

func TestWriteComposeFileCheckedRefusesAFIFOWithoutWaitingForAWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.yaml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	expected := contentguard.Revision("opened regular file")
	done := make(chan error, 1)
	go func() { done <- WriteComposeFileChecked(path, "draft", &expected) }()
	select {
	case err := <-done:
		if !errors.Is(err, contentguard.ErrConflict) {
			t.Fatalf("special-file save: %v", err)
		}
	case <-time.After(time.Second):
		if writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			writer.Close()
		}
		t.Fatal("save waited for a FIFO writer")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO was replaced: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("refused save created a backup: %v", err)
	}
}
