package hostexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
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
	want := []string{"--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid", "--no-fork", "--",
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
	want := []string{"--target", "1", "--mount", "--uts", "--ipc", "--net", "--pid", "--no-fork", "--",
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

func TestCommandsCarryAWaitDelayBackstop(t *testing.T) {
	ctx := context.Background()
	for name, cmd := range map[string]*exec.Cmd{
		"Command":            Command(ctx, "true"),
		"CommandInDir":       CommandInDir(ctx, t.TempDir(), "true"),
		"CommandOnHost":      CommandOnHost(ctx, "true"),
		"CommandOnHostInDir": CommandOnHostInDir(ctx, "/", "true"),
		"missing binary":     Command(ctx, "jd-no-such-binary"),
	} {
		if cmd.WaitDelay != commandWaitDelay {
			t.Errorf("%s: WaitDelay = %v, want %v", name, cmd.WaitDelay, commandWaitDelay)
		}
	}
}

// RunGroup owns termination: its own loop is what waits for the group, and a
// delay left on the command would give up on output the group is still writing.
func TestRunGroupKeepsNoWaitDelayAndCollectsEveryWrite(t *testing.T) {
	cmd := Command(context.Background(), "sh", "-c", `(sleep 0.2; echo late) & echo early`)
	cmd.WaitDelay = time.Millisecond
	var out strings.Builder
	cmd.Stdout = &out
	if _, err := RunGroup(context.Background(), cmd, time.Second); err != nil {
		t.Fatal(err)
	}
	if cmd.WaitDelay != 0 {
		t.Fatalf("WaitDelay = %v, want 0", cmd.WaitDelay)
	}
	if out.String() != "early\nlate\n" {
		t.Fatalf("output = %q", out.String())
	}
}

// A shell's pipeline survives the shell being killed and keeps its output pipe
// open, which is what the backstop is for.
func TestCancelledCommandReturnsWhileAPipelineHoldsItsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := Command(ctx, "sh", "-c", `sleep 8 | cat; echo unreachable`).Output()
	if err == nil {
		t.Fatal("a cancelled command reported success")
	}
	if took := time.Since(began); took > commandWaitDelay+2*time.Second {
		t.Fatalf("Wait took %v with a %v backstop", took, commandWaitDelay)
	}
}

// liveHostNamespaces skips unless this is where the backend runs: root, a PID
// namespace whose init is in another mount namespace, and nsenter. Run the test
// binary in a throwaway `docker run --privileged --pid=...` container.
func liveHostNamespaces(t *testing.T) {
	t.Helper()
	if fd, err := hostMountNamespace(); err != nil || fd < 0 || !hostReachable() {
		t.Skip("no host namespace to cross into")
	}
}

// The command Go started is the host command itself, so a deadline reaches it.
func TestLiveDeadlineStopsTheHostCommand(t *testing.T) {
	liveHostNamespaces(t)
	marker := "9" + strconv.Itoa(os.Getpid()%1000) + ".25"
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	began := time.Now()
	cmd := CommandOnHost(ctx, "sleep", marker)
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		t.Fatal("a killed command reported success")
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("returned after %v, want the 500ms deadline", took)
	}
	if survivor := processWithArg(t, marker); survivor != 0 {
		t.Fatalf("host command survived as pid %d", survivor)
	}
}

func TestLiveHostCommandIsTheStartedProcessAndSeesHostPIDs(t *testing.T) {
	liveHostNamespaces(t)
	cmd := CommandOnHost(context.Background(), "sh", "-c",
		`echo "$$"; readlink /proc/self/ns/pid; readlink /proc/1/ns/pid; readlink /proc/self/ns/mnt; readlink /proc/1/ns/mnt`)
	var out strings.Builder
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(out.String())
	if len(lines) != 5 {
		t.Fatalf("output = %q", out.String())
	}
	if lines[0] != strconv.Itoa(cmd.Process.Pid) {
		t.Fatalf("command is pid %s, Go started %d", lines[0], cmd.Process.Pid)
	}
	if lines[1] != lines[2] {
		t.Fatalf("PID namespace %s, init's %s", lines[1], lines[2])
	}
	if lines[3] != lines[4] {
		t.Fatalf("mount namespace %s, init's %s", lines[3], lines[4])
	}
}

// What the command's children get is what the host's tools report on.
func TestLiveChildrenOfAHostCommandAreInTheHostPIDNamespace(t *testing.T) {
	liveHostNamespaces(t)
	out, err := CommandOnHost(context.Background(), "sh", "-c",
		`sleep 0.1 & child=$!; readlink /proc/$child/ns/pid; readlink /proc/1/ns/pid; test -d /proc/$child && echo seen; wait`).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != 3 || lines[0] != lines[1] || lines[2] != "seen" {
		t.Fatalf("output = %q", out)
	}
}

func TestLiveExitStatusAndOutputCrossUnchanged(t *testing.T) {
	liveHostNamespaces(t)
	cmd := CommandOnHostInDir(context.Background(), "/", "sh", "-c", `pwd; exit 7`)
	out, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("err = %v, want exit 7", err)
	}
	if string(out) != "/\n" {
		t.Fatalf("output = %q", out)
	}
}

// A command that leaves a daemon behind on its output is told apart from a
// failure of its own, and no longer holds the caller for the daemon's lifetime.
func TestLiveDaemonHoldingOutputEndsTheWaitWithErrWaitDelay(t *testing.T) {
	liveHostNamespaces(t)
	began := time.Now()
	out, err := CommandOnHost(context.Background(), "sh", "-c", `sleep 8 & echo started`).Output()
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("err = %v, want exec.ErrWaitDelay", err)
	}
	if string(out) != "started\n" {
		t.Fatalf("output = %q", out)
	}
	if took := time.Since(began); took > commandWaitDelay+2*time.Second {
		t.Fatalf("Wait took %v", took)
	}
}

func TestLiveDaemonThatClosesItsOutputDoesNotDelayTheCommand(t *testing.T) {
	liveHostNamespaces(t)
	began := time.Now()
	if err := CommandOnHost(context.Background(), "sh", "-c", `sleep 3 >/dev/null 2>&1 </dev/null & echo started`).Run(); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took > time.Second {
		t.Fatalf("Wait took %v", took)
	}
}

func TestLiveRunGroupStopsAHostPipeline(t *testing.T) {
	liveHostNamespaces(t)
	marker := "8" + strconv.Itoa(os.Getpid()%1000) + ".75"
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cmd := CommandOnHost(ctx, "sh", "-c", `sleep "$1" | cat`, "sh", marker)
	var out strings.Builder
	cmd.Stdout = &out
	if _, err := RunGroup(ctx, cmd, time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if survivor := processWithArg(t, marker); survivor != 0 {
		t.Fatalf("pipeline survived as pid %d", survivor)
	}
}

// A terminal's shell is now the session's leader rather than nsenter's child.
func TestLiveTerminalShellLeadsItsSessionWithJobControl(t *testing.T) {
	liveHostNamespaces(t)
	if !AvailableOnHost("bash") {
		t.Skip("no bash on this host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := CommandOnHost(ctx, "bash", "--noprofile", "--norc", "-i")
	cmd.Env = append(os.Environ(), "TERM=dumb", "PS1=")
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("sleep 0.1 & fg >/dev/null; echo \"status=$? sid=$(cut -d' ' -f6 /proc/$$/stat) pid=$$ end\"; exit\n"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_, _ = io.Copy(&output, f) // A closed terminal reads as EIO.
	_ = cmd.Wait()
	want := "status=0 sid=" + strconv.Itoa(cmd.Process.Pid) + " pid=" + strconv.Itoa(cmd.Process.Pid) + " end"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

// Host tools that read /proc, and the service manager, answer as they would at
// a host shell.
func TestLiveHostToolsAnswerFromTheHost(t *testing.T) {
	liveHostNamespaces(t)
	if AvailableOnHost("ps") {
		out, err := CommandOnHost(context.Background(), "sh", "-c", `ps -o pid= -p 1; ps -o pid= -p "$$"; echo "$$"`).Output()
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(out))
		if len(fields) != 3 || fields[0] != "1" || fields[1] != fields[2] {
			t.Fatalf("ps output = %q", out)
		}
	}
	if !AvailableOnHost("systemctl") {
		return
	}
	out, err := CommandOnHost(context.Background(), "systemctl", "show", "--property=Version", "--value").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Fatalf("systemctl show: %q, %v", out, err)
	}
	// The transient unit is what a detached job is, and it belongs to the
	// host's systemd rather than to this command.
	unit := "jd-hostexec-test-" + strconv.Itoa(os.Getpid())
	out, err = CommandOnHost(context.Background(), "systemd-run", "--quiet", "--collect", "--unit="+unit, "sleep", "1").CombinedOutput()
	if err != nil {
		t.Fatalf("systemd-run: %q, %v", out, err)
	}
	state, err := CommandOnHost(context.Background(), "systemctl", "is-active", unit).Output()
	if err != nil || strings.TrimSpace(string(state)) != "active" {
		t.Fatalf("transient unit state = %q, %v", state, err)
	}
}

func processWithArg(t *testing.T, arg string) int {
	t.Helper()
	// Give the kernel a moment to reap what was just killed.
	for attempt := 0; ; attempt++ {
		found := 0
		entries, err := os.ReadDir("/proc")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			pid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
			if err == nil && strings.Contains(string(raw), "\x00"+arg+"\x00") {
				found = pid
			}
		}
		if found == 0 || attempt == 20 {
			return found
		}
		time.Sleep(50 * time.Millisecond)
	}
}
