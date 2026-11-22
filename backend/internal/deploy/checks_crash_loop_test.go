package deploy

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// livenessBackendFake answers readiness's question about the candidate's
// container with the same state every time.
type livenessBackendFake struct {
	mu    sync.Mutex
	state dockerx.ContainerLiveness
	reads int
}

func (f *livenessBackendFake) ContainerHealth(context.Context, string) (string, error) {
	return "", nil
}

func (f *livenessBackendFake) ExecCheck(context.Context, string, []string, time.Duration) (int, []byte, error) {
	return 0, nil, nil
}

func (f *livenessBackendFake) ContainerLiveness(context.Context, string) (dockerx.ContainerLiveness, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.state, nil
}

// closedPort is a loopback port nothing listens on, so every HTTP attempt is
// refused at once, as a candidate's is while its start command has not
// reached the server.
func closedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

func TestReadinessStopsWaitingForACandidateThatKeepsDying(t *testing.T) {
	t.Parallel()
	port := closedPort(t)
	readiness := PlannedCheck{Name: "HTTP readiness", Kind: "http", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"path":"/","attempts":40,"intervalSeconds":0,"timeoutSeconds":1}`)}
	target := CheckTarget{ContainerID: "candidate", Host: "127.0.0.1", Port: port}
	for _, test := range []struct {
		name     string
		state    dockerx.ContainerLiveness
		frozen   bool
		stopped  bool
		attempts int
		reason   string
	}{
		{
			// The failure this was found with: drizzle-kit exited 1 every two seconds
			// and the gate read "could not connect" two minutes later.
			name:    "a start command failing before the server listens",
			state:   dockerx.ContainerLiveness{Status: "restarting", Restarting: true, ExitCode: 1, RestartCount: 6},
			stopped: true, reason: "the application kept exiting (code 1, restarted 6 time(s)) instead of answering at 127.0.0.1:",
		},
		{
			name:    "a container nothing will start again",
			state:   dockerx.ContainerLiveness{Status: "exited", ExitCode: 2},
			stopped: true, attempts: 1, reason: "the application exited with code 2 and nothing starts it again",
		},
		{
			name:  "a server still starting",
			state: dockerx.ContainerLiveness{Status: "running", Running: true, RestartCount: 9},
		},
		{
			// An application that exits while its database starts is given
			// its restarts, and its first thirty seconds, before it reads as
			// a loop.
			name:  "a few restarts while a database comes up",
			state: dockerx.ContainerLiveness{Status: "restarting", Restarting: true, ExitCode: 1, RestartCount: 2},
		},
		{
			name:   "many quick restarts inside the first thirty seconds",
			state:  dockerx.ContainerLiveness{Status: "restarting", Restarting: true, ExitCode: 1, RestartCount: 6},
			frozen: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &livenessBackendFake{state: test.state}
			runner := NewCheckRunner(backend)
			// Each reading of the clock is five seconds later, so the grace
			// is over after a handful of attempts rather than in real time.
			clock := time.Date(2026, 11, 15, 15, 46, 26, 0, time.UTC)
			runner.now = func() time.Time {
				if !test.frozen {
					clock = clock.Add(5 * time.Second)
				}
				return clock
			}
			evidence := runner.Run(context.Background(), readiness, target)
			last := evidence.Attempts[len(evidence.Attempts)-1]
			if !test.stopped {
				if evidence.AttemptCount() != 40 || last.Code != "connection_failed" {
					t.Fatalf("a candidate that may still answer was given up on: %d attempts, last %+v", evidence.AttemptCount(), last)
				}
				return
			}
			if evidence.Outcome != HealthFailed || last.Code != "application_exited" || evidence.AttemptCount() >= 40 ||
				last.ExitCode != test.state.ExitCode || last.Restarts != test.state.RestartCount || last.ContainerState != test.state.Status {
				t.Fatalf("readiness did not stop for a candidate that is gone: %d attempts, last %+v", evidence.AttemptCount(), last)
			}
			if test.attempts > 0 && evidence.AttemptCount() != test.attempts {
				t.Fatalf("attempts = %d, want %d", evidence.AttemptCount(), test.attempts)
			}
			message := checkFailureMessage("readiness", evidence.Outcome, evidence)
			if !strings.Contains(message, test.reason) || strings.Contains(message, "could not connect") {
				t.Fatalf("message = %q, want it to say %q", message, test.reason)
			}
		})
	}
}

func TestReadinessWithoutAContainerOrALivenessReaderKeepsAsking(t *testing.T) {
	t.Parallel()
	port := closedPort(t)
	readiness := PlannedCheck{Name: "HTTP readiness", Kind: "http", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"path":"/","attempts":3,"intervalSeconds":0,"timeoutSeconds":1}`)}
	gone := dockerx.ContainerLiveness{Status: "exited", ExitCode: 1}
	for name, test := range map[string]struct {
		backend ContainerCheckBackend
		target  CheckTarget
	}{
		"no container id":            {&livenessBackendFake{state: gone}, CheckTarget{Host: "127.0.0.1", Port: port}},
		"a backend that cannot tell": {&checkBackendFake{}, CheckTarget{ContainerID: "candidate", Host: "127.0.0.1", Port: port}},
		"no backend at all":          {nil, CheckTarget{ContainerID: "candidate", Host: "127.0.0.1", Port: port}},
	} {
		evidence := NewCheckRunner(test.backend).Run(context.Background(), readiness, test.target)
		if evidence.AttemptCount() != 3 || evidence.Attempts[len(evidence.Attempts)-1].Code != "connection_failed" {
			t.Fatalf("%s: %+v", name, evidence.Attempts)
		}
	}
}
