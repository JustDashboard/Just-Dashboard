package term

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestMain gives this package's tests a tmux server of their own.
//
// Two reasons, and both are the difference between a test suite that passes
// and one that means anything. The obvious one is the developer's machine: the
// terminal's whole promise is that a session outlives the process that made
// it, so a test that opened one against the real tmux server would leave it
// there, and a test that listed sessions would find the operator's. The other
// is that `go test ./...` runs packages concurrently — the sessions this
// package creates and the ones another package creates would land on the same
// server and appear in each other's listings, which is exactly the sort of
// cross-talk the feature must never have and a test must never invent.
//
// tmux takes its socket directory from TMUX_TMPDIR, and every `tmux` this
// package runs is a child of this process, so setting it here is enough.
func TestMain(m *testing.M) {
	// A parent tmux session overrides TMUX_TMPDIR unless cleared first.
	if err := os.Unsetenv("TMUX"); err != nil {
		fmt.Fprintln(os.Stderr, "clear inherited tmux server:", err)
		os.Exit(1)
	}
	dir, err := os.MkdirTemp("", "jdtmux")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create private tmux directory:", err)
		os.Exit(1)
	}
	// Short, because a unix socket path has about a hundred characters to
	// play with and a nested temp directory can spend them all. Fail closed:
	// tests and cleanup must never fall back to the operator's default server.
	if err := os.Setenv("TMUX_TMPDIR", dir); err != nil {
		os.RemoveAll(dir)
		fmt.Fprintln(os.Stderr, "select private tmux directory:", err)
		os.Exit(1)
	}
	if _, err := exec.LookPath("tmux"); err == nil {
		// Keep the private server alive between tests. Killing the last test
		// session otherwise races the next tmux client's connection against
		// automatic server exit, producing "server exited unexpectedly".
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, err := exec.CommandContext(ctx, "tmux", "-f", "/dev/null", "start-server", ";", "set-option", "-s", "exit-empty", "off").CombinedOutput()
		cancel()
		if err != nil {
			exec.Command("tmux", "kill-server").Run()
			os.RemoveAll(dir)
			fmt.Fprintf(os.Stderr, "start private tmux server: %v: %s\n", err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	// The server outlives the tests otherwise: that is the property under
	// test, and a stray tmux server per run is not a legacy worth keeping.
	exec.Command("tmux", "kill-server").Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// A list endpoint that answers JSON null instead of [] crashes any client that
// iterates the result, so the contract is worth pinning down.
func TestTmuxSessionsMarshalsAsArray(t *testing.T) {
	m := NewManager(true, "/bin/sh", "")
	got := m.TmuxSessions(context.Background())
	if got == nil {
		t.Fatal("TmuxSessions returned a nil slice")
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "null" {
		t.Fatalf("TmuxSessions marshalled to null, want an array")
	}
}

func TestRingBufferKeepsMostRecentBytes(t *testing.T) {
	r := newRingBuffer(8)
	r.Write([]byte("abcdef"))
	r.Write([]byte("ghij"))
	if got := string(r.Bytes()); got != "cdefghij" {
		t.Fatalf("Bytes() = %q, want %q", got, "cdefghij")
	}

	// A single write larger than the buffer keeps only its tail.
	r.Write([]byte("0123456789"))
	if got := string(r.Bytes()); got != "23456789" {
		t.Fatalf("Bytes() = %q, want %q", got, "23456789")
	}
}

func TestResizeUpdatesKernelAndReportedSize(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	defer tty.Close()

	sess := &Session{Rows: 24, Cols: 80, pty: ptmx}
	changed, err := sess.Resize(43, 156)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("Resize reported no change")
	}
	rows, cols := sess.Size()
	if rows != 43 || cols != 156 {
		t.Fatalf("session size = %dx%d, want 43x156", rows, cols)
	}
	winsize, err := pty.GetsizeFull(ptmx)
	if err != nil {
		t.Fatal(err)
	}
	if winsize.Rows != 43 || winsize.Cols != 156 {
		t.Fatalf("kernel PTY size = %dx%d, want 43x156", winsize.Rows, winsize.Cols)
	}
	if err := sess.SynchronizeSize(51, 173); err != nil {
		t.Fatal(err)
	}
	winsize, err = pty.GetsizeFull(ptmx)
	if err != nil {
		t.Fatal(err)
	}
	if winsize.Rows != 51 || winsize.Cols != 173 {
		t.Fatalf("synchronized kernel PTY size = %dx%d, want 51x173", winsize.Rows, winsize.Cols)
	}

	changed, err = sess.Resize(51, 173)
	if err != nil || changed {
		t.Fatalf("duplicate resize = changed %v, err %v; want no-op", changed, err)
	}
}

func TestTerminalEnvReplacesInheritedCapabilities(t *testing.T) {
	got := terminalEnv([]string{
		"PATH=/usr/bin", "TERM=dumb", "COLORTERM=", "JD_SESSION=old", "LANG=C.UTF-8",
	}, "new")
	want := []string{
		"PATH=/usr/bin", "LANG=C.UTF-8", "TERM=xterm-256color", "COLORTERM=truecolor", "JD_SESSION=new",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("terminalEnv() = %#v, want %#v", got, want)
	}
}

func TestTmuxNewSessionSetsPaneTruecolorBeforeLoginStarts(t *testing.T) {
	// tmux does not copy COLORTERM from a client into an existing server by
	// default. Keep this assertion beside terminalEnv so neither half of the
	// PTY -> tmux -> pane capability chain can regress independently.
	got := tmuxNewSessionArgv("vpsd-test", "/srv/app", []string{"su", "-l", "ubuntu"})
	want := []string{
		"tmux", "new-session", "-A", "-e", "COLORTERM=truecolor", "-s", "vpsd-test",
		"-c", "/srv/app", "su", "-l", "ubuntu",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tmuxNewSessionArgv() = %#v, want %#v", got, want)
	}
}
