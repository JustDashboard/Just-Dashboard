package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type checkBackendFake struct {
	mu       sync.Mutex
	health   []string
	exitCode int
	output   []byte
	err      error
	commands [][]string
}

func TestHTTPReadinessFollowsOnlyCandidateRedirects(t *testing.T) {
	var externalRequests int
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalRequests++
		w.WriteHeader(http.StatusOK)
	}))
	defer external.Close()
	candidate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			http.Redirect(w, r, "/page", http.StatusTemporaryRedirect)
		case "/page":
			w.WriteHeader(http.StatusInternalServerError)
		case "/healthy":
			http.Redirect(w, r, "/ready", http.StatusTemporaryRedirect)
		case "/ready":
			w.WriteHeader(http.StatusOK)
		case "/external":
			http.Redirect(w, r, external.URL, http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer candidate.Close()
	for _, test := range []struct {
		path     string
		expected []int
		outcome  HealthOutcome
		status   int
	}{
		{"/", nil, HealthFailed, 500}, {"/healthy", nil, HealthPassed, 200},
		{"/external", nil, HealthFailed, 302}, {"/loop", nil, HealthFailed, 302},
		{"/", []int{307}, HealthPassed, 307},
	} {
		check := PlannedCheck{Name: "ready", Kind: "http", Phase: "readiness", Required: true,
			Config: mustJSON(CheckConfiguration{URL: candidate.URL + test.path, Attempts: 1, TimeoutSeconds: 1, ExpectedStatus: test.expected})}
		evidence := NewCheckRunner(nil).Run(context.Background(), check, CheckTarget{})
		if evidence.Outcome != test.outcome || evidence.Attempts[0].StatusCode != test.status {
			t.Fatalf("%s: %+v", test.path, evidence)
		}
	}
	if externalRequests != 0 {
		t.Fatal("candidate readiness visited an external server")
	}
}

func (f *checkBackendFake) ContainerHealth(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.health) == 0 {
		return "", f.err
	}
	status := f.health[0]
	f.health = f.health[1:]
	return status, f.err
}

func (f *checkBackendFake) ExecCheck(_ context.Context, _ string, command []string, _ time.Duration) (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, append([]string(nil), command...))
	return f.exitCode, append([]byte(nil), f.output...), f.err
}

func TestCheckRunnerCoversHTTPRetriesTCPDockerCommandAndClosedOutcomes(t *testing.T) {
	t.Parallel()
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	runner := NewCheckRunner(nil)
	httpEvidence := runner.Run(context.Background(), PlannedCheck{
		Name: "ready", Kind: "http", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"url":"` + server.URL + `","attempts":2,"timeoutSeconds":1}`),
	}, CheckTarget{})
	if httpEvidence.Outcome != HealthPassed || len(httpEvidence.Attempts) != 2 ||
		httpEvidence.Attempts[0].Code != "unexpected_status" || httpEvidence.Attempts[1].StatusCode != http.StatusNoContent {
		t.Fatalf("HTTP evidence = %#v", httpEvidence)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	tcpEvidence := runner.Run(context.Background(), PlannedCheck{
		Name: "socket", Kind: "tcp", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"attempts":1,"timeoutSeconds":1}`),
	}, CheckTarget{Host: "127.0.0.1", Port: port})
	if tcpEvidence.Outcome != HealthPassed || tcpEvidence.Attempts[0].Address != listener.Addr().String() {
		t.Fatalf("TCP evidence = %#v", tcpEvidence)
	}

	backend := &checkBackendFake{health: []string{"starting", "healthy"}}
	runner = NewCheckRunner(backend)
	dockerEvidence := runner.Run(context.Background(), PlannedCheck{
		Name: "container", Kind: "docker_health", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"attempts":2,"timeoutSeconds":1}`),
	}, CheckTarget{ContainerID: "candidate"})
	if dockerEvidence.Outcome != HealthPassed || len(dockerEvidence.Attempts) != 2 ||
		dockerEvidence.Attempts[0].ContainerState != "starting" {
		t.Fatalf("Docker health evidence = %#v", dockerEvidence)
	}

	secretOutput := []byte("runtime secret must never be persisted")
	backend = &checkBackendFake{exitCode: 9, output: secretOutput, err: errors.New("exit 9")}
	runner = NewCheckRunner(backend)
	commandEvidence := runner.Run(context.Background(), PlannedCheck{
		Name: "smoke", Kind: "command", Phase: "smoke", Required: false,
		Config: json.RawMessage(`{"command":["app","check"],"attempts":1,"timeoutSeconds":1}`),
	}, CheckTarget{ContainerID: "candidate"})
	encoded, _ := json.Marshal(commandEvidence)
	if commandEvidence.Outcome != HealthWarning || commandEvidence.Attempts[0].ExitCode != 9 ||
		commandEvidence.Attempts[0].OutputDigest == "" || strings.Contains(string(encoded), string(secretOutput)) {
		t.Fatalf("command evidence = %s", encoded)
	}

	disabled := runner.Run(context.Background(), PlannedCheck{
		Name: "off", Kind: "http", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"disabled":true}`),
	}, CheckTarget{})
	unavailable := NewCheckRunner(nil).Run(context.Background(), PlannedCheck{
		Name: "missing", Kind: "docker_health", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"attempts":1}`),
	}, CheckTarget{})
	if disabled.Outcome != HealthDisabled || len(disabled.Attempts) != 0 ||
		unavailable.Outcome != HealthUnavailable || summarizeChecks([]CheckEvidence{disabled}) != HealthDisabled ||
		summarizeChecks([]CheckEvidence{commandEvidence}) != HealthWarning {
		t.Fatalf("closed outcomes: disabled=%#v unavailable=%#v", disabled, unavailable)
	}
}

func TestCheckConfigurationRejectsUnknownFieldsCredentialURLsAndSecretArgv(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		kind string
		raw  string
	}{
		{"http", `{"headers":{"X-Test":"value"}}`},
		{"http", `{"url":"https://user:pass@example.test/health"}`},
		{"http", `{"path":"relative"}`},
		{"command", `{"command":["check","--api-token","plain"]}`},
		{"command", `{"command":[]}`},
		{"tcp", `{"url":"https://example.test"}`},
	} {
		if err := validateCheckConfiguration(fixture.kind, json.RawMessage(fixture.raw)); err == nil {
			t.Fatalf("%s accepted invalid configuration %s", fixture.kind, fixture.raw)
		}
	}
}

func TestCheckRunnerTimeoutIsBoundedAndEvidenceDoesNotExposeTransportError(t *testing.T) {
	t.Parallel()
	runner := NewCheckRunner(nil)
	runner.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, errors.New("credential-like upstream error token=do-not-store")
	}
	evidence := runner.Run(context.Background(), PlannedCheck{
		Name: "bounded", Kind: "tcp", Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"host":"127.0.0.1","port":9,"attempts":1,"timeoutSeconds":1}`),
	}, CheckTarget{})
	raw, _ := json.Marshal(evidence)
	if evidence.Outcome != HealthFailed || evidence.Attempts[0].Code != "timeout" ||
		strings.Contains(string(raw), "do-not-store") {
		t.Fatalf("timeout evidence = %s", raw)
	}
}

func TestReadinessFollowsAllocatedRuntimePortAndPreservesExplicitTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(parsed.Port())
	target := targetForRuntime(ReleaseRuntime{Kind: "container", RuntimeID: "candidate", Host: "127.0.0.1", Port: port}, runtimeReleaseSnapshot{Plan: RuntimePlanConfig{InternalPort: 3123, HostPort: 3124}})
	evidence := NewCheckRunner(nil).Run(context.Background(), PlannedCheck{Name: "HTTP readiness", Kind: "http", Phase: "readiness", Required: true, Config: json.RawMessage(`{"port":3123,"attempts":1}`)}, target)
	if evidence.Outcome != HealthPassed || evidence.Attempts[0].Address != server.URL+"/" {
		t.Fatalf("readiness = %+v", evidence)
	}
	host, explicit := targetAddress(CheckConfiguration{Host: "other.example.test", Port: 3123}, target)
	if host != "other.example.test" || explicit != 3123 {
		t.Fatal("rewrote explicitly separate host")
	}
	_, explicit = targetAddress(CheckConfiguration{Port: 9000}, target)
	if explicit != 9000 {
		t.Fatal("rewrote unrelated health port")
	}
}

func TestReadinessFailureReportsSafeCause(t *testing.T) {
	message := checkFailureMessage("readiness", HealthFailed, CheckEvidence{Name: "HTTP readiness", Required: true, Outcome: HealthFailed, Attempts: []CheckAttemptEvidence{{Code: "unexpected_status", StatusCode: 503, Address: "http://user:secret@127.0.0.1:40905/private?token=secret"}}})
	if !strings.Contains(message, "HTTP 503") || !strings.Contains(message, "127.0.0.1:40905") || strings.Contains(message, "secret") || strings.Contains(message, "/private") {
		t.Fatalf("unsafe or incomplete message: %s", message)
	}
}
