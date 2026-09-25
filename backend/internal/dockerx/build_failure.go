package dockerx

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// ErrBuildTimeout is a build that ran out of its own time limit while the
// deployment asking for it was still alive. It deliberately does not wrap
// context.DeadlineExceeded: callers treat a context error as the operator
// cancelling, and nobody cancelled this build.
var ErrBuildTimeout = errors.New("the build exceeded its 30-minute limit")

// BuildTimeoutError is ErrBuildTimeout with the process-group cleanup that
// stopped the build, which the caller keeps as cleanup evidence.
type BuildTimeoutError struct {
	Result hostexec.GroupResult
}

func (e *BuildTimeoutError) Error() string        { return ErrBuildTimeout.Error() }
func (e *BuildTimeoutError) Is(target error) bool { return target == ErrBuildTimeout }

// BuildError is a buildx run that exited non-zero, read back into the
// instruction BuildKit says failed. Without it the caller only ever sees
// exec's "exit status 1", which names neither the step nor its exit code.
//
// Step is BuildKit's vertex number, so the caller can find that instruction's
// own lines in the stream. Command is set when a RUN's process failed, and
// ExitCode is then that process's code; any other failure (a COPY source
// that does not exist, a Dockerfile that does not parse) leaves Command empty
// and ExitCode -1, and names itself in Reason.
type BuildError struct {
	Step        int
	Instruction string
	Command     string
	ExitCode    int
	Reason      string
	BuildxExit  int
}

func (e *BuildError) Error() string {
	switch {
	case e.Command != "":
		return fmt.Sprintf("the build step `%s` exited with code %d", e.Command, e.ExitCode)
	case e.Reason != "":
		return "the build failed: " + e.Reason
	default:
		return fmt.Sprintf("docker buildx exited with code %d", e.BuildxExit)
	}
}

// These bound everything the reader keeps from the stream: the
// failure it reports is one instruction, however much the build printed.
const (
	buildFailureTextLimit = 4096
	buildVertexNameLimit  = 256
	buildVertexNameCount  = 512
)

var (
	// "#10 [4/5] RUN npm ci" — the vertex header naming an instruction.
	buildVertexHeaderRE = regexp.MustCompile(`^#(\d+) \[[^\]]*\] (.+)$`)
	// "#10 ERROR: process "/bin/sh -c npm ci" did not complete successfully: exit code: 1".
	// The command is Go-quoted, so it is unquoted rather than matched lazily.
	buildProcessFailureRE = regexp.MustCompile(`^(?:#(\d+) ERROR|ERROR: failed to (?:build|solve)(?:: failed to solve)?): process ("(?:[^"\\]|\\.)*") did not complete successfully: exit code: (\d+)$`)
	// "#7 ERROR: failed to calculate checksum of ref …: "/app/dist": not found".
	buildVertexErrorRE = regexp.MustCompile(`^#(\d+) ERROR: (.+)$`)
	// "ERROR: failed to build: failed to solve: dockerfile parse error on line 1: …".
	buildSolveErrorRE = regexp.MustCompile(`^ERROR: (?:failed to build: )?failed to solve: (.+)$`)
)

// buildFailureReader follows a plain-progress stream and keeps only what a
// BuildError needs. It is written from the stdout and stderr scanners at once.
type buildFailureReader struct {
	mu          sync.Mutex
	names       map[int]string
	step        int
	command     string
	exitCode    int
	vertexError string
	solveError  string
}

func newBuildFailureReader() *buildFailureReader {
	return &buildFailureReader{names: map[int]string{}, exitCode: -1}
}

func (r *buildFailureReader) observe(line string) {
	if !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "ERROR: ") {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if match := buildProcessFailureRE.FindStringSubmatch(line); match != nil {
		command, err := strconv.Unquote(match[2])
		if err != nil {
			command = strings.Trim(match[2], `"`)
		}
		code, _ := strconv.Atoi(match[3])
		// The final summary repeats the vertex's own line without its number;
		// it must not erase the step the vertex line named.
		if match[1] != "" {
			r.step, _ = strconv.Atoi(match[1])
		}
		r.command = boundedText(strings.TrimPrefix(command, "/bin/sh -c "), buildFailureTextLimit)
		r.exitCode = code
		return
	}
	if match := buildVertexHeaderRE.FindStringSubmatch(line); match != nil {
		vertex, _ := strconv.Atoi(match[1])
		if _, known := r.names[vertex]; known || len(r.names) < buildVertexNameCount {
			r.names[vertex] = boundedText(match[2], buildVertexNameLimit)
		}
		return
	}
	if match := buildVertexErrorRE.FindStringSubmatch(line); match != nil {
		r.step, _ = strconv.Atoi(match[1])
		r.vertexError = boundedText(match[2], buildFailureTextLimit)
		return
	}
	if match := buildSolveErrorRE.FindStringSubmatch(line); match != nil {
		r.solveError = boundedText(match[1], buildFailureTextLimit)
	}
}

func (r *buildFailureReader) failure(buildxExit int) *BuildError {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := &BuildError{
		Step: r.step, Instruction: r.names[r.step], Command: r.command,
		ExitCode: r.exitCode, BuildxExit: buildxExit,
	}
	if result.Command == "" {
		result.ExitCode = -1
		result.Reason = r.solveError
		if result.Reason == "" {
			result.Reason = r.vertexError
		}
	}
	return result
}

// buildRunError tells a build that ran out of its own time from one whose
// deployment was cancelled; every other error is returned unchanged.
func buildRunError(ctx, buildCtx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() == nil && errors.Is(buildCtx.Err(), context.DeadlineExceeded) {
		timeout := &BuildTimeoutError{Result: hostexec.GroupResult{ExitCode: -1}}
		var group *hostexec.GroupError
		if errors.As(err, &group) {
			timeout.Result = group.Result
		}
		return timeout
	}
	return err
}

func boundedText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
