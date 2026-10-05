package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A broken reader must not leave the test process blocked in open(2). The
// child owns only the parent's temporary source path and has a short deadline.
func TestLocalSourceSnapshotRejectsReplacedFIFO(t *testing.T) {
	if os.Getenv("JD_LOCAL_SNAPSHOT_FIFO_HELPER") == "1" {
		source := os.Getenv("JD_LOCAL_SNAPSHOT_FIFO_SOURCE")
		expected, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(source, 0600); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("JD_LOCAL_SNAPSHOT_FIFO_READER") == "digest" {
			input, err := openRegularSnapshotFile(source, expected)
			if input != nil {
				input.Close()
			}
			if err == nil {
				t.Fatal("a FIFO replaced the captured regular source file")
			}
		} else {
			target := source + ".snapshot"
			if err := copyRegularSnapshotFile(source, target, expected.Mode().Perm(), expected.Size()); err == nil {
				t.Fatal("a FIFO was copied into the source snapshot")
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatal("rejected FIFO created a snapshot file")
			}
		}
		return
	}
	for _, reader := range []string{"digest", "copy"} {
		t.Run(reader, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "server.js")
			if err := os.WriteFile(source, []byte("module.exports = 42\n"), 0644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLocalSourceSnapshotRejectsReplacedFIFO$")
			command.Env = append(os.Environ(), "JD_LOCAL_SNAPSHOT_FIFO_HELPER=1", "JD_LOCAL_SNAPSHOT_FIFO_SOURCE="+source, "JD_LOCAL_SNAPSHOT_FIFO_READER="+reader)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("source reader did not safely reject its replaced FIFO: %v (deadline=%v), %s", err, ctx.Err(), output)
			}
		})
	}
}

func TestLocalSourceSnapshotRejectsChangedRegularIdentity(t *testing.T) {
	source := filepath.Join(t.TempDir(), "server.js")
	if err := os.WriteFile(source, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	original, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	expected, err := original.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replaced"), 0644); err != nil {
		t.Fatal(err)
	}
	input, err := openRegularSnapshotFile(source, expected)
	if input != nil {
		input.Close()
	}
	if err == nil {
		t.Fatal("a same-sized replacement substituted the captured source identity")
	}
}
