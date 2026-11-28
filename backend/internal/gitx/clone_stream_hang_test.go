package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A streamed clone ends when its git does, even when something git started has
// left git's process group with the output still open (a hook's `setsid … &`,
// in the account's own template): the WaitDelay backstop holds for a streamed
// command as for the others, and a clone that git finished is a clone made.
func TestAStreamedCloneEndsWhenAHooksProcessKeepsItsOutputOpen(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is not installed")
	}
	dir, git := tempRepo(t)
	write(t, dir, "README.md", "hello\n")
	git("add", "-A")
	git("commit", "-qm", "first")
	server, _ := httpGitServer(t, filepath.Dir(dir))
	remote := server.URL + "/" + filepath.Base(dir)

	templates := t.TempDir()
	pidFile := filepath.Join(t.TempDir(), "pid")
	write(t, templates, "hooks/post-checkout", "#!/bin/sh\nsetsid sh -c 'echo $$ > "+pidFile+"; exec sleep 25' &\nexit 0\n")
	if err := os.Chmod(filepath.Join(templates, "hooks", "post-checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "gitconfig")
	write(t, filepath.Dir(config), filepath.Base(config), "[init]\n\ttemplateDir = "+templates+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	destination := t.TempDir()
	s := New([]string{destination})
	plan, err := s.PrepareClone(destination, remote, "copy", CloneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- s.CloneStreaming(context.Background(), plan, nil, func(string, string) {}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("clone: %v", err)
		}
		if took := time.Since(start); took > 15*time.Second {
			t.Fatalf("the clone took %s, as long as the hook's process held its output", took)
		}
	case <-time.After(40 * time.Second):
		t.Fatal("the clone was still running 40 seconds after git finished")
	}
	if data, err := os.ReadFile(filepath.Join(plan.Target, "README.md")); err != nil || string(data) != "hello\n" {
		t.Fatalf("cloned file: %q, %v", data, err)
	}
}

// So does every other git: a commit whose post-commit hook leaves a process in
// the background holding its output is a commit made, answered when git ends
// (WaitDelay after it), not a failure, nor a wait for that process.
func TestACommitWhoseHookLeavesAProcessBehindIsACommitMade(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir, git := tempRepo(t)
	write(t, dir, "README.md", "hello\n")
	git("add", "-A")
	git("commit", "-qm", "first")
	pidFile := filepath.Join(t.TempDir(), "pid")
	write(t, dir, ".git/hooks/post-commit", "#!/bin/sh\nsetsid sh -c 'echo $$ > "+pidFile+"; exec sleep 25' &\nexit 0\n")
	if err := os.Chmod(filepath.Join(dir, ".git", "hooks", "post-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	write(t, dir, "README.md", "hello again\n")
	s := New([]string{filepath.Dir(dir)})
	if _, err := s.Stage(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := s.CommitAs(context.Background(), dir, "Second", "t", "t@example.com"); err != nil {
		t.Fatalf("the commit failed: %v", err)
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Fatalf("the commit took %s", took)
	}
	if subject := git("log", "-1", "--format=%s"); strings.TrimSpace(subject) != "Second" {
		t.Fatalf("HEAD is %q", subject)
	}
}
