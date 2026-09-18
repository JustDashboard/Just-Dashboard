package hostexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandInDirKeepsLocalWorkingDirectoryAndArgv(t *testing.T) {
	dir := t.TempDir()
	cmd := CommandInDir(context.Background(), dir, "sh", "-c", "printf '%s' \"$1\"", "sh", "a b;$HOME")
	if cmd.Dir != dir {
		t.Fatalf("Dir = %q, want %q", cmd.Dir, dir)
	}
	got, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a b;$HOME" {
		t.Fatalf("output = %q; argv was not preserved", got)
	}
}

func TestHostArgvWithoutDirectoryRunsTheCommandStraightAfterTheTerminator(t *testing.T) {
	got := hostArgv("", "test", []string{"-d", "/srv/a b"})
	want := []string{"--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid", "--",
		"test", "-d", "/srv/a b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hostArgv() = %#v, want %#v", got, want)
	}
}

// The directory must be changed into on the far side of the namespace switch,
// not by nsenter before it crosses — see hostArgv. Pinning the argv is how
// that stays true: a reviewer reaching for the shorter `--wd` spelling breaks
// this test rather than every terminal opened in a directory.
func TestHostArgvChangesDirectoryAfterCrossingAndPassesItAsAnArgument(t *testing.T) {
	got := hostArgv("/srv/a b", "su", []string{"-s", "/bin/zsh", "ubuntu"})
	want := []string{"--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid", "--",
		"sh", "-c", `cd "$1" || exit 1; shift; exec "$@"`, "sh", "/srv/a b",
		"su", "-s", "/bin/zsh", "ubuntu"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hostArgv() = %#v, want %#v", got, want)
	}
	for _, arg := range got {
		if strings.HasPrefix(arg, "--wd") {
			t.Fatal("nsenter --wd resolves the directory in the wrong mount namespace")
		}
	}
}

// The wrapper is a real shell, so the contract that survives it is the one
// worth testing: the command keeps its own argv, and a directory carrying a
// space or a dollar sign is one argument rather than script text.
func TestHostArgvShellWrapperPreservesArgvAndAwkwardDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a b$HOME'q")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	argv := hostArgv(dir, "sh", []string{"-c", `printf '%s|%s' "$(pwd)" "$1"`, "sh", "x y;$PATH"})
	// Everything after nsenter's own options is what would run on the host,
	// and it has to mean the same thing when it is run here.
	terminator := 0
	for i, arg := range argv {
		if arg == "--" {
			terminator = i + 1
			break
		}
	}
	out, err := exec.Command(argv[terminator], argv[terminator+1:]...).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != dir+"|x y;$PATH" {
		t.Fatalf("output = %q, want %q", out, dir+"|x y;$PATH")
	}
}

func TestHostArgvShellWrapperFailsWhenTheDirectoryIsMissing(t *testing.T) {
	argv := hostArgv(filepath.Join(t.TempDir(), "missing"), "echo", []string{"ran"})
	terminator := 0
	for i, arg := range argv {
		if arg == "--" {
			terminator = i + 1
			break
		}
	}
	if err := exec.Command(argv[terminator], argv[terminator+1:]...).Run(); err == nil {
		t.Fatal("a missing directory must fail rather than running the command elsewhere")
	}
}

func TestAsOwnerPreservesExistingProcessGroupPolicy(t *testing.T) {
	if syscall.Geteuid() != 0 {
		t.Skip("owner dropping is meaningful only as root")
	}
	dir := t.TempDir()
	if err := os.Chown(dir, 65534, 65534); err != nil {
		t.Skipf("cannot prepare non-root-owned directory: %v", err)
	}
	cmd := exec.Command("true")
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	AsOwner(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("AsOwner replaced the existing process-group policy")
	}
	if cmd.SysProcAttr.Credential == nil || cmd.SysProcAttr.Credential.Uid != 65534 {
		t.Fatal("AsOwner did not add the directory owner's credential")
	}
}

func TestRunGroupTerminatesDescendantsAndRecordsForcedCleanup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c",
		`trap '' TERM; /bin/sh -c 'trap "" TERM; while :; do sleep 1; done' & echo $! > "$1"; wait`,
		"sh", pidFile)

	type outcome struct {
		result GroupResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := RunGroup(ctx, cmd, 75*time.Millisecond)
		done <- outcome{result: result, err: err}
	}()
	childPID := waitForPIDFile(t, pidFile)
	cancel()

	select {
	case got := <-done:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("RunGroup error = %v, want context cancellation", got.err)
		}
		if !got.result.TERMSent || !got.result.KILLSent {
			t.Fatalf("cleanup evidence = %#v, want TERM then KILL", got.result)
		}
		var groupErr *GroupError
		if !errors.As(got.err, &groupErr) || groupErr.Result != got.result {
			t.Fatalf("group error did not retain cleanup evidence: %v", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunGroup did not complete after cancellation")
	}
	waitForProcessGone(t, childPID)
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("parse child pid: %v", err)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child pid was not written")
	return 0
}

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant process %d survived process-group cancellation", pid)
}
