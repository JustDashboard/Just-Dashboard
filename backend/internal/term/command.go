package term

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A command run the way the terminal would run it, to its end, with nobody at
// the keyboard: the dashboard's assistant's run_command. It is the terminal's
// account, its login shell and its environment rather than this process's, on the
// host, in the account's home; nothing is read from standard input, and a
// command that outlives its time is stopped with everything it started.
//
// The line is the shell's to parse, as a line typed into the terminal is. That is
// the point of it and the one place outside a terminal window where it happens;
// security invariant 6 names it beside deploy.Deployer.shell.

// CommandOutputMax is how much of a command's output is kept: its end, which is
// where a command says how it went.
const CommandOutputMax = 64 << 10

// CommandResult is what a command came to.
type CommandResult struct {
	// Output is the end of what it wrote, its output and its errors as they came.
	Output string
	// Cut is how many bytes of the start were left out.
	Cut int
	// ExitCode is -1 when it was stopped.
	ExitCode int
	TimedOut bool
	// User is the account it ran as.
	User     string
	Duration time.Duration
}

// RunCommand runs one command line as the terminal's account and waits for it,
// up to timeout.
func (m *Manager) RunCommand(ctx context.Context, line string, timeout time.Duration) (CommandResult, error) {
	if !m.enabled {
		return CommandResult{}, ErrDisabled
	}
	account := m.account
	if m.accountErr != nil && account.Name == "" {
		return CommandResult{}, m.accountErr
	}
	shell := m.shell
	if shell == "" {
		shell = account.Shell
	}
	if shell == "" {
		shell = "/bin/bash"
	}
	argv := account.commandArgv(shell, line)
	// Its own deadline rather than the caller's alone, so a command that is stopped
	// says it timed out rather than that the press went away.
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := hostexec.CommandOnHostInDir(runCtx, account.Home, argv[0], argv[1:]...)
	cmd.Env = commandEnv(account, shell)
	cmd.Stdin = nil
	out := &tailBuffer{max: CommandOutputMax}
	cmd.Stdout, cmd.Stderr = out, out
	started := time.Now()
	res, err := hostexec.RunGroup(runCtx, cmd, 3*time.Second)
	result := CommandResult{ExitCode: res.ExitCode, User: account.Name, Duration: time.Since(started)}
	result.Output, result.Cut = out.String()
	var group *hostexec.GroupError
	switch {
	case errors.As(err, &group) && errors.Is(group.Cause, context.DeadlineExceeded) && ctx.Err() == nil:
		result.TimedOut, result.ExitCode = true, -1
		return result, nil
	case errors.As(err, &group):
		return result, group.Cause
	case err != nil && result.ExitCode < 0:
		// It never started: no such shell, or nsenter refused.
		return result, err
	}
	return result, nil
}

// commandArgv is the account's login shell running one line, as su would make
// it for another account.
func (a Account) commandArgv(shell, line string) []string {
	if a.IsRoot() || os.Geteuid() != 0 {
		return []string{shell, "-l", "-c", line}
	}
	// Options before the name: su hands anything after it to the shell.
	return []string{"su", "-s", shell, "-l", "-c", line, a.Name}
}

// commandEnv is what the command starts with: built, never inherited, because
// this process's environment carries the dashboard's own secrets. su replaces it
// for another account; what is added for root says there is nobody to ask.
func commandEnv(account Account, shell string) []string {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + account.Home,
		"USER=" + account.Name,
		"LOGNAME=" + account.Name,
		"SHELL=" + shell,
		"TERM=dumb",
		"NO_COLOR=1",
		"DEBIAN_FRONTEND=noninteractive",
		"SYSTEMD_PAGER=",
		"PAGER=cat",
		"GIT_PAGER=cat",
	}
	if lang := os.Getenv("LANG"); lang != "" {
		env = append(env, "LANG="+lang)
	}
	return env
}

// tailBuffer keeps the last max bytes written to it and counts the rest.
type tailBuffer struct {
	mu   sync.Mutex
	max  int
	buf  []byte
	cut  int
	seen int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen += len(p)
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.cut += over
		t.buf = append(t.buf[:0:0], t.buf[over:]...)
	}
	return len(p), nil
}

// String is what was kept and how much was left out before it.
func (t *tailBuffer) String() (string, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf), t.cut
}

// Foreground reports whether a job other than the shell holds a window's
// terminal, and the job's name: the kernel's own answer, read off the PTY, so a
// caller typing into the window knows when what it typed has finished. The
// instant between a job ending and the shell taking the terminal back counts as
// the job's.
func (s *Session) Foreground() (busy bool, process string) {
	fg, busy, process, _, transition := s.inspect()
	if fg <= 0 {
		return false, ""
	}
	return busy || transition, process
}
