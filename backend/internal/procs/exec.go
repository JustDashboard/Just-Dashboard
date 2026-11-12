// Package procs manages the things running on the host: PM2 applications,
// systemd units, the raw process table and cron.
//
// Every external command in this package is executed with an explicit argument
// vector through exec.Command — never through a shell — and every identifier
// that reaches an argument is validated against a strict pattern first. That
// combination is what keeps a unit name or PM2 process name from turning into
// command injection.
package procs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNotInstalled = errors.New("not installed on this host")
	// ErrNoSystemd is systemctl here and no systemd for it to ask: a host
	// booted with another init, or a container without the host's
	// /run/systemd. systemctl itself says so with exit status 1 and "System
	// has not been booted with systemd as init system", which reached the
	// Services page as 502 command_failed.
	ErrNoSystemd   = errors.New("systemd is not running on this host")
	ErrInvalidName = errors.New("name contains characters that are not allowed")
)

// Unit and application names are restricted to what systemd and PM2 actually
// permit. Anything outside this set is rejected rather than escaped.
//
// The first character may not be a hyphen: nothing here goes through a shell,
// but a name that starts with one is still a name systemctl or pm2 would read
// as an option rather than an argument. No exploit was constructible — "=" is
// excluded and the single argument is consumed as a value — so this is
// hardening a shape, not closing a hole.
var safeName = regexp.MustCompile(`^[A-Za-z0-9._@:][A-Za-z0-9._@:\-]{0,127}$`)

func ValidateName(name string) error {
	if !safeName.MatchString(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	return nil
}

type CommandResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Command  string `json:"command"`
}

func run(ctx context.Context, timeout time.Duration, name string, args ...string) (*CommandResult, error) {
	// Every systemctl call passes here, the ones behind a button included.
	// Without a manager each would run, fail and be answered as a command
	// that failed; none is started instead.
	if name == "systemctl" && binaryExists(name) && !systemdBooted() {
		return &CommandResult{Command: name + " " + strings.Join(args, " ")}, ErrNoSystemd
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	// The dashboard shares the host PID namespace, so PID 1's root is the
	// host's while this process's root is the container image. systemd reads
	// that difference as a chroot and refuses every command with "Running in
	// chroot, ignoring command", printing nothing to stdout — which surfaced
	// as an unexplained JSON parse failure on the systemd page. The host's
	// systemd is genuinely reachable over the mounted D-Bus socket, so the
	// check is telling us about the mount layout, not about reachability.
	cmd.Env = append(os.Environ(), "SYSTEMD_IGNORE_CHROOT=1")
	return runPrepared(ctx, cmd, timeout, name, args...)
}

func binaryExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// SystemdRunDir is the directory systemd creates when it is the init
// system, which is what sd_booted(3) looks for. systemctl runs on this side
// and reaches the host's manager through /run/systemd, so on this side is
// where it is looked for: in the dashboard's container it is the host's,
// mounted, and it is missing there exactly when systemctl would have nothing
// to talk to. A variable so a test with a systemctl of its own can stand a
// manager beside it.
var SystemdRunDir = "/run/systemd/system"

func systemdBooted() bool {
	info, err := os.Stat(SystemdRunDir)
	return err == nil && info.IsDir()
}

func runPrepared(ctx context.Context, cmd *exec.Cmd, timeout time.Duration, name string, args ...string) (*CommandResult, error) {
	var stdout, stderr commandBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	res := &CommandResult{
		Stdout:  stdout.String(),
		Stderr:  stderr.String(),
		Command: name + " " + strings.Join(args, " "),
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return res, fmt.Errorf("%s %w", name, ErrNotInstalled)
	}
	if ctx.Err() == context.DeadlineExceeded {
		return res, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	if stdout.truncated || stderr.truncated {
		return res, fmt.Errorf("%s output exceeded 4 MiB", name)
	}
	if err != nil {
		return res, fmt.Errorf("%s exited %d: %s", name, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res, nil
}

// User-owned PM2 executables must not be able to exhaust dashboard memory by
// writing an unbounded listing. Excess output is drained until the timeout.
type commandBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *commandBuffer) Write(p []byte) (int, error) {
	const limit = 4 << 20
	remaining := limit - b.buffer.Len()
	if len(p) > remaining {
		b.truncated = true
	}
	_, _ = b.buffer.Write(p[:min(len(p), remaining)])
	return len(p), nil
}

func (b *commandBuffer) String() string { return b.buffer.String() }
