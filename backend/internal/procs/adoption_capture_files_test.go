package procs

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCaptureFileReadersRejectFIFOWithoutWaitingForWriter(t *testing.T) {
	for _, reader := range []struct {
		name string
		read func(string) error
	}{
		{"configuration", func(path string) error { _, err := readCaptureFile(path); return err }},
		{"source", func(path string) error { _, err := CaptureHostSourceFiles([]string{path}); return err }},
	} {
		for _, symlink := range []bool{false, true} {
			name := reader.name
			if symlink {
				name += "_symlink"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				fifo := filepath.Join(root, "capture.pipe")
				if err := syscall.Mkfifo(fifo, 0600); err != nil {
					t.Fatal(err)
				}
				path := fifo
				if symlink {
					path = filepath.Join(root, "authority-link")
					if err := os.Symlink(fifo, path); err != nil {
						t.Fatal(err)
					}
				}
				result := make(chan error, 1)
				go func() { result <- reader.read(path) }()
				select {
				case err := <-result:
					if err == nil {
						t.Fatal("FIFO accepted as authoritative input")
					}
				case <-time.After(time.Second):
					// Release a regressed blocking open so the test never leaks a
					// reader goroutine or needs an external writer to terminate.
					writer, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0)
					if err == nil {
						writer.Close()
					}
					select {
					case <-result:
					case <-time.After(time.Second):
					}
					t.Fatal("capture blocked opening FIFO without a writer")
				}
			})
		}
	}
}

func TestCaptureFileReadersPreserveRegularAndSymlinkInputs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "entrypoint.js")
	content := []byte("process.stdout.write('capture fixture')\n")
	if err := os.WriteFile(path, content, 0640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "entrypoint-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{path, link} {
		captured, err := readCaptureFile(input)
		if err != nil || !bytes.Equal(captured, content) {
			t.Fatalf("regular authoritative input changed: %v", err)
		}
	}
	digests, err := CaptureHostSourceFiles([]string{path, link})
	if err != nil || digests[path] == "" || digests[path] != digests[link] {
		t.Fatalf("symlink entrypoint lost verified file identity: %v", err)
	}
	if _, err := readCaptureFile(root); err == nil {
		t.Fatal("directory accepted as a capture file")
	}
	if _, err := readCaptureFile("/dev/null"); err == nil {
		t.Fatal("device accepted as a capture file")
	}
}

func TestCaptureFileReaderPreservesZeroSizedProcfsAndByteLimit(t *testing.T) {
	info, err := os.Stat("/proc/self/cmdline")
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
		t.Fatalf("expected a zero-sized procfs regular file: %v", err)
	}
	expected, err := os.ReadFile("/proc/self/cmdline")
	if err != nil || len(expected) == 0 {
		t.Fatalf("test process command is unavailable: %v", err)
	}
	actual, err := readCaptureFile("/proc/self/cmdline")
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatalf("zero-sized procfs command lost: %v", err)
	}
	path := filepath.Join(t.TempDir(), "large-config")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(4<<20 + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	file.Close()
	if _, err := readCaptureFile(path); err == nil {
		t.Fatal("oversized configuration accepted")
	}
}
