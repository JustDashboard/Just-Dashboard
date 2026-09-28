package proxysvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// ErrTestUnfinished is a config test that ended without the engine's verdict:
// it ran out of time, was stopped, or could not be run at all. It says nothing
// about the configuration, so it is never kept as the last test and never
// answered as a refusal — a test cut off by a closed tab read as "nginx's
// configuration fails its test" until the next clean one. Its errors are
// *UnfinishedTestError.
var ErrTestUnfinished = errors.New("the configuration test did not finish")

// testTimeout bounds one config test. A variable so tests can use their own.
var testTimeout = 30 * time.Second

// testWaitDelay bounds the wait, once a test is stopped, for whatever still
// holds its output open: killing nsenter or a shell leaves the nginx it
// started writing, and the test would run on past its timeout.
const testWaitDelay = 2 * time.Second

// UnfinishedTestError is ErrTestUnfinished for one run: the command, why it
// gave no verdict, and what it printed before it stopped.
type UnfinishedTestError struct {
	Command string
	// Why completes a sentence whose subject is the command.
	Why    string
	Output string
	cause  error
}

func (e *UnfinishedTestError) Error() string { return e.Command + " " + e.Why }

// Unwrap is ErrTestUnfinished and what stopped the test: the context's error
// for one that ran out of time or was stopped, exec's for one that never ran.
func (e *UnfinishedTestError) Unwrap() []error { return []error{ErrTestUnfinished, e.cause} }

// runTest runs an engine's config test to its verdict, within testTimeout. A
// nil error means res is the engine's own answer, passed or refused; a test
// that gave none comes back as an *UnfinishedTestError beside what it
// printed.
func runTest(ctx context.Context, name string, args ...string) (*ValidationResult, error) {
	run, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	cmd := hostexec.Command(run, name, args...)
	cmd.WaitDelay = testWaitDelay
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	res := &ValidationResult{
		Valid:   err == nil,
		Output:  strings.TrimSpace(buf.String()),
		Command: name + " " + strings.Join(args, " "),
	}
	res.diagnose(name)
	if why, cause := verdictless(ctx, run, err); why != "" {
		return res, &UnfinishedTestError{Command: res.Command, Why: why, Output: res.Output, cause: cause}
	}
	return res, nil
}

// verdictless says why a test that ended with err, run under run within the
// caller's ctx, gave no verdict, or "" when err is the engine's own refusal:
// an exit status it chose. Both engines refuse with 1.
func verdictless(ctx, run context.Context, err error) (why string, cause error) {
	switch {
	case err == nil:
		return "", nil
	case ctx.Err() != nil:
		return fmt.Sprintf("was stopped before it finished (%v)", ctx.Err()), ctx.Err()
	case run.Err() != nil:
		return fmt.Sprintf("took longer than %s and was stopped", testTimeout), run.Err()
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return "could not be run: " + err.Error(), err
	}
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return fmt.Sprintf("was ended by a signal (%s)", status.Signal()), err
	}
	// "Cannot execute" and "not found", from whatever stands between the
	// dashboard and the engine: nsenter reaching the host's binary, docker
	// exec the container's.
	if code := exit.ExitCode(); code == 126 || code == 127 {
		return fmt.Sprintf("could not be run (%s)", exit), err
	}
	return "", nil
}
