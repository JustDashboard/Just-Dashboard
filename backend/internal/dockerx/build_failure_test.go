package dockerx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/docker/docker/client"
)

// The streams below are BuildKit's own plain progress, captured from
// buildx v0.37 against fixtures: an npm lockfile out of sync, a shell
// command that does not exist, a Go-quoted command, a COPY of a directory
// the build never wrote and a Dockerfile that does not parse.
func TestBuildFailureReaderNamesTheFailedInstruction(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		lines []string
		want  BuildError
		text  string
	}{
		"failed RUN with its replay and summary": {
			lines: []string{
				"#0 building with \"default\" instance using docker driver",
				"#10 [4/5] RUN npm ci",
				"#10 1.303 npm error code EUSAGE",
				"#10 ERROR: process \"/bin/sh -c npm ci\" did not complete successfully: exit code: 1",
				"------",
				" > [4/5] RUN npm ci:",
				"1.303 npm error code EUSAGE",
				"------",
				"ERROR: failed to build: failed to solve: process \"/bin/sh -c npm ci\" did not complete successfully: exit code: 1",
			},
			want: BuildError{Step: 10, Instruction: "RUN npm ci", Command: "npm ci", ExitCode: 1, BuildxExit: 1},
			text: "the build step `npm ci` exited with code 1",
		},
		"command not found": {
			lines: []string{
				"#6 [3/3] RUN npm run build",
				"#6 0.181 /bin/sh: npm: not found",
				"#6 ERROR: process \"/bin/sh -c npm run build\" did not complete successfully: exit code: 127",
			},
			want: BuildError{Step: 6, Instruction: "RUN npm run build", Command: "npm run build", ExitCode: 127, BuildxExit: 1},
		},
		"quoted command": {
			lines: []string{
				`#6 ERROR: process "/bin/sh -c echo \"a \\\"quoted\\\" arg\" && false" did not complete successfully: exit code: 1`,
			},
			want: BuildError{Step: 6, Command: `echo "a \"quoted\" arg" && false`, ExitCode: 1, BuildxExit: 1},
		},
		"summary alone keeps the command": {
			lines: []string{
				"ERROR: failed to solve: process \"/bin/sh -c cargo build --release\" did not complete successfully: exit code: 101",
			},
			want: BuildError{Command: "cargo build --release", ExitCode: 101, BuildxExit: 1},
		},
		"missing COPY source": {
			lines: []string{
				"#7 [stage-1 2/2] COPY --from=build /app/dist/ /usr/share/nginx/html/",
				`#7 ERROR: failed to calculate checksum of ref 5f5pg8::s5bkep: "/app/dist": not found`,
				`ERROR: failed to build: failed to solve: failed to compute cache key: failed to calculate checksum of ref 5f5pg8::s5bkep: "/app/dist": not found`,
			},
			want: BuildError{
				Step: 7, Instruction: "COPY --from=build /app/dist/ /usr/share/nginx/html/", ExitCode: -1, BuildxExit: 1,
				Reason: `failed to compute cache key: failed to calculate checksum of ref 5f5pg8::s5bkep: "/app/dist": not found`,
			},
			text: `the build failed: failed to compute cache key`,
		},
		"Dockerfile that does not parse": {
			lines: []string{
				"ERROR: failed to build: failed to solve: dockerfile parse error on line 1: unknown instruction: FROMM (did you mean FROM?)",
			},
			want: BuildError{
				ExitCode: -1, BuildxExit: 1,
				Reason: "dockerfile parse error on line 1: unknown instruction: FROMM (did you mean FROM?)",
			},
		},
		"nothing BuildKit named": {
			lines: []string{"some unrelated output"},
			want:  BuildError{ExitCode: -1, BuildxExit: 1},
			text:  "docker buildx exited with code 1",
		},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			reader := newBuildFailureReader()
			for _, line := range test.lines {
				reader.observe(line)
			}
			got := reader.failure(1)
			if *got != test.want {
				t.Fatalf("failure = %+v\nwant      %+v", *got, test.want)
			}
			if test.text != "" && !strings.HasPrefix(got.Error(), test.text) {
				t.Fatalf("Error() = %q, want prefix %q", got.Error(), test.text)
			}
		})
	}
}

func TestBuildFailureReaderStaysBounded(t *testing.T) {
	t.Parallel()
	reader := newBuildFailureReader()
	for vertex := 0; vertex < buildVertexNameCount*2; vertex++ {
		reader.observe("#" + strconv.Itoa(vertex) + " [1/1] RUN " + strings.Repeat("x", 1000))
	}
	if len(reader.names) != buildVertexNameCount {
		t.Fatalf("kept %d vertex names", len(reader.names))
	}
	for _, name := range reader.names {
		if len(name) > buildVertexNameLimit {
			t.Fatalf("vertex name kept %d bytes", len(name))
		}
	}
	reader.observe(`#3 ERROR: process "/bin/sh -c ` + strings.Repeat("é", buildFailureTextLimit) + `" did not complete successfully: exit code: 2`)
	failure := reader.failure(1)
	if len(failure.Command) > buildFailureTextLimit || !strings.HasPrefix(failure.Command, "é") ||
		strings.ContainsRune(failure.Command, '�') {
		t.Fatalf("command kept %d bytes or split a rune", len(failure.Command))
	}
}

func TestRunGroupStreamReturnsTheFailedInstruction(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("/bin/sh", "-c", `
echo '#4 [2/2] RUN bun install --frozen-lockfile' >&2
echo '#4 0.485 error: lockfile had changes, but lockfile is frozen' >&2
echo '#4 ERROR: process "/bin/sh -c bun install --frozen-lockfile" did not complete successfully: exit code: 1' >&2
exit 1`)
	out := make(chan LogLine, 16)
	err := runGroupStream(context.Background(), cmd, out, newBuildFailureReader())
	close(out)
	var failure *BuildError
	if !errors.As(err, &failure) || failure.Command != "bun install --frozen-lockfile" ||
		failure.Step != 4 || failure.ExitCode != 1 {
		t.Fatalf("error = %#v", err)
	}
	if err.Error() == "exit status 1" {
		t.Fatal("the failure still reads as exec's exit status")
	}
	lines := 0
	for range out {
		lines++
	}
	if lines != 3 {
		t.Fatalf("streamed %d lines, want 3", lines)
	}
}

func TestBuildRunErrorSeparatesTimeoutFromCancellation(t *testing.T) {
	t.Parallel()
	expired, cancelExpired := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelExpired()
	<-expired.Done()
	group := &hostexec.GroupError{Cause: context.DeadlineExceeded, Result: hostexec.GroupResult{TERMSent: true, ExitCode: -1}}

	err := buildRunError(context.Background(), expired, group)
	var timeout *BuildTimeoutError
	if !errors.Is(err, ErrBuildTimeout) || !errors.As(err, &timeout) || !timeout.Result.TERMSent {
		t.Fatalf("timed-out build = %#v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatal("a build timeout must not read as a cancelled context")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := buildRunError(cancelled, expired, group); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a cancelled deployment's build = %v, want the context error unchanged", err)
	}
	other := errors.New("docker: not found")
	if err := buildRunError(context.Background(), context.Background(), other); err != other {
		t.Fatalf("unrelated error = %v", err)
	}
}

// A process buildx leaves behind can hold the output pipes open after buildx
// exits. Reading stops once nothing more arrives, and nothing already
// written is lost. Not parallel: it shortens the package's grace.
func TestRunGroupStreamDoesNotWaitOnAProcessHoldingItsOutput(t *testing.T) {
	grace := pipeDrainGrace
	pipeDrainGrace = 200 * time.Millisecond
	defer func() { pipeDrainGrace = grace }()
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & echo '#1 DONE 0.1s'; exit 0")
	out := make(chan LogLine, 16)
	started := time.Now()
	err := runGroupStream(context.Background(), cmd, out, newBuildFailureReader())
	elapsed := time.Since(started)
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	close(out)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("waited %s on a process that outlived the build", elapsed)
	}
	lines := []string{}
	for line := range out {
		lines = append(lines, line.Text)
	}
	if len(lines) != 1 || lines[0] != "#1 DONE 0.1s" {
		t.Fatalf("streamed %q", lines)
	}
}

// A registry that refuses a pull before the stream starts answers the pull
// with an error, which must still read as a pull failure; a daemon nobody
// can reach is the engine's own fault and keeps its error.
func TestPullImmutableNamesAnUpFrontRefusalAsAPullFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.47/images/create" {
			t.Errorf("unexpected Docker request: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"Head \"https://registry.example.test/v2/team/app/manifests/sha256:1\": unauthorized: authentication required"}`))
	}))
	defer server.Close()
	reference := "registry.example.test/team/app@sha256:" + strings.Repeat("a", 64)
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	_, err = (&Client{cli: cli}).PullImmutable(t.Context(), reference, "", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "pull image: ") || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("up-front refusal = %v", err)
	}

	// A socket nothing listens on: a closed test server's port could be
	// handed to a parallel test's server meanwhile.
	socket := "unix://" + filepath.Join(t.TempDir(), "docker.sock")
	unreachable, err := client.NewClientWithOpts(client.WithHost(socket), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer unreachable.Close()
	_, err = (&Client{cli: unreachable}).PullImmutable(t.Context(), reference, "", nil)
	if err == nil || strings.Contains(err.Error(), "pull image:") {
		t.Fatalf("unreachable daemon = %v", err)
	}
}
