package procs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCapturedInterpreterRejectsApplicationWrapperWithoutExecutingIt(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "executed")
	executable := filepath.Join(root, "node")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\ntouch "+sentinel+"\necho v24.0.0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	files, err := CaptureHostSourceFiles([]string{executable})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeCapturedInterpreter(context.Background(), &HostWorkloadCapture{InterpreterPath: executable, SourceFiles: files, UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}); err == nil {
		t.Fatal("application wrapper accepted as interpreter")
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("wrapper was executed")
	}
}

func TestCapturedInterpreterUsesSanitizedEnvironmentAndExactIdentity(t *testing.T) {
	if _, err := exec.LookPath("setpriv"); err != nil {
		t.Skip("setpriv unavailable")
	}
	for _, name := range []string{"node", "python3"} {
		t.Run(name, func(t *testing.T) {
			executable, err := exec.LookPath(name)
			if err != nil {
				t.Skip("interpreter unavailable")
			}
			executable, err = filepath.EvalSymlinks(executable)
			if err != nil {
				t.Fatal(err)
			}
			files, err := CaptureHostSourceFiles([]string{executable})
			if err != nil {
				t.Fatal(err)
			}
			capture := &HostWorkloadCapture{InterpreterPath: executable, SourceFiles: files, UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}
			t.Setenv("NODE_OPTIONS", "--require /nonexistent/private-loader.js")
			t.Setenv("PYTHONPATH", "/nonexistent/private-site")
			version, err := ProbeCapturedInterpreter(context.Background(), capture)
			if err != nil || version == "" {
				t.Fatalf("fixed version probe failed: %q %v", version, err)
			}
			capture.SourceFiles[executable] = "foreign-hash"
			if _, err := ProbeCapturedInterpreter(context.Background(), capture); err != ErrHostWorkloadChanged {
				t.Fatalf("identity drift=%v", err)
			}
		})
	}
}
