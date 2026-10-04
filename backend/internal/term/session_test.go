package term

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

func TestCWDAndNewWindowIgnoreBackgroundJobDirectory(t *testing.T) {
	m, sess, out := cwdSession(t)
	project, background := t.TempDir(), t.TempDir()
	command := "cd -- " + cwdShellQuote(project) + "\n(cd -- " + cwdShellQuote(background) + "; printf 'background-child-ready:%s\\n' \"$PWD\"; sleep 30) & JD_BG_PID=$!\nprintf 'background-ready:%s\\n' \"$PWD\"\n"
	if _, err := sess.Write([]byte(command)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Write([]byte("kill \"$JD_BG_PID\" 2>/dev/null\n")) })
	seen := await(t, out, "", "background-ready:"+project)
	await(t, out, seen, "background-child-ready:"+background)
	foreground := sess.foregroundGroup()
	if foreground == 0 {
		t.Fatal("PTY has no foreground group")
	}
	backgroundSeen := false
	for _, child := range descendants(foreground, 16) {
		if cwd, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(child), "cwd")); err == nil && cwd == background {
			backgroundSeen = true
		}
	}
	if !backgroundSeen {
		t.Fatal("the background job was not running in its separate directory")
	}
	if cwd := sess.CWD(); cwd != project {
		t.Fatalf("foreground prompt cwd = %q, want %q", cwd, project)
	}
	opened, err := m.NewDirectWindowWithOptions(context.Background(), sess.WorkspaceID, DirectWindowOptions{
		SourceWindowID: sess.ID, Rows: 24, Cols: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened.CWDHint != project {
		t.Fatalf("new terminal inherited %q, want foreground directory %q", opened.CWDHint, project)
	}
}

func TestCWDReportsBusyForegroundProgramDirectory(t *testing.T) {
	_, sess, out := cwdSession(t)
	jobDirectory := t.TempDir()
	command := "(cd -- " + cwdShellQuote(jobDirectory) + "; printf 'foreground-ready:%s\\n' \"$PWD\"; exec sleep 30)\n"
	if _, err := sess.Write([]byte(command)); err != nil {
		t.Fatal(err)
	}
	await(t, out, "", "foreground-ready:"+jobDirectory)
	if cwd := sess.CWD(); cwd != jobDirectory {
		t.Fatalf("busy foreground cwd = %q, want %q", cwd, jobDirectory)
	}
}

func TestCWDReportsForegroundDirectoryWithEmptyArgv(t *testing.T) {
	_, sess, out := cwdSessionWithShell(t, "/bin/bash")
	jobDirectory := t.TempDir()
	command := "(cd -- " + cwdShellQuote(jobDirectory) + "; printf 'empty-argv-ready:%s\\n' \"$PWD\"; exec -a '' /bin/sleep 30)\n"
	if _, err := sess.Write([]byte(command)); err != nil {
		t.Fatal(err)
	}
	await(t, out, "", "empty-argv-ready:"+jobDirectory)
	// Synchronize to the explicit empty-argv fixture, rather than to its
	// output immediately before exec. The directory assertion stays strict.
	deadline := time.Now().Add(5 * time.Second)
	for {
		foreground := sess.foregroundGroup()
		cwd, err := processCWD(foreground)
		if err == nil && cwd == jobDirectory && cmdline(foreground) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("foreground process did not reach its empty argv fixture")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cwd := sess.CWD(); cwd != jobDirectory {
		t.Fatalf("empty-argv foreground cwd = %q, want %q", cwd, jobDirectory)
	}
}

func TestCWDReportsEmptyArgvPipelineAfterLeaderExit(t *testing.T) {
	_, sess, out := cwdSessionWithShell(t, "/bin/bash")
	jobDirectory := t.TempDir()
	command := "printf seed | /bin/bash -c 'cd -- \"$1\"; printf \"pipeline-ready:%s\\n\" \"$PWD\"; exec -a \"\" /bin/sleep 30' bash " + cwdShellQuote(jobDirectory) + "\n"
	if _, err := sess.Write([]byte(command)); err != nil {
		t.Fatal(err)
	}
	await(t, out, "", "pipeline-ready:"+jobDirectory)
	deadline := time.Now().Add(5 * time.Second)
	for {
		foreground := sess.foregroundGroup()
		_, leaderErr := processCWD(foreground)
		member := groupMemberPID(foreground, sess.PID)
		cwd, memberErr := processCWD(member)
		if leaderErr != nil && memberErr == nil && cwd == jobDirectory && cmdline(member) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pipeline did not reach an exited leader and live empty-argv member")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cwd := sess.CWD(); cwd != jobDirectory {
		t.Fatalf("leaderless pipeline cwd = %q, want %q", cwd, jobDirectory)
	}
}

func cwdSession(t *testing.T) (*Manager, *Session, <-chan []byte) {
	return cwdSessionWithShell(t, "")
}

func cwdSessionWithShell(t *testing.T, shell string) (*Manager, *Session, <-chan []byte) {
	t.Helper()
	m := heldManager(t, t.TempDir())
	type holderProcess struct {
		cmd  *exec.Cmd
		done <-chan error
	}
	var holders []holderProcess
	m.SetHolderLauncherForTest(m.holders, func(_ context.Context, _, socket string) error {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "JD_TEST_HOLDER="+socket)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		holders = append(holders, holderProcess{cmd, done})
		return nil
	})
	if shell != "" {
		m.shell = shell
	}
	// Directory tests need a predictable login and a readiness signal before
	// typing. The account's own rc can negotiate with a real emulator or read
	// input, neither of which is part of this headless process fixture.
	m.account.Home = t.TempDir()
	for _, profile := range []string{".profile", ".bash_profile"} {
		if err := os.WriteFile(filepath.Join(m.account.Home, profile), []byte("printf 'cwd-fixture:%s\\n' ready\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, sess := range m.List() {
			m.Kill(context.Background(), sess.ID)
		}
		m.Shutdown()
		// This fixture owns detached holders; transport close alone is not
		// a shutdown acknowledgement. Stop them directly and wait for shell
		// reaping, including history writes, before TempDir removes HOME.
		for _, holder := range holders {
			holder.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case err := <-holder.done:
				if err != nil {
					t.Errorf("CWD fixture holder exited: %v", err)
				}
			case <-time.After(10 * time.Second):
				holder.cmd.Process.Kill()
				<-holder.done
				t.Error("CWD fixture holder did not exit after hangup")
			}
		}
	})
	sess, err := m.Create(context.Background(), CreateOptions{Rows: 24, Cols: 120})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, id, out, err := sess.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sess.Unsubscribe(id) })
	await(t, out, string(snapshot), "cwd-fixture:ready")
	return m, sess, out
}

func cwdShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
