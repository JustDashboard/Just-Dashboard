package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// diagnosingRuntimeOwner is the ordering owner plus a diagnosis that returns a
// fixed transcript containing a runtime secret, so the test can prove the
// secret never reaches the persisted log.
type diagnosingRuntimeOwner struct {
	*orderingRuntimeOwner
	diagnostics RuntimeDiagnostics
	err         error
	diagnosed   []string
}

func (o *diagnosingRuntimeOwner) DiagnoseRuntime(_ context.Context, runtime ReleaseRuntime) (RuntimeDiagnostics, error) {
	o.diagnosed = append(o.diagnosed, runtime.RuntimeID)
	return o.diagnostics, o.err
}

type recordingStepOutput struct {
	mu    sync.Mutex
	lines []string
}

func (o *recordingStepOutput) Log(stream, text string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, stream+": "+text)
	return nil
}

func (o *recordingStepOutput) Event(EventInput) error { return nil }

func (o *recordingStepOutput) joined() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.lines, "\n")
}

func TestFailedReadinessCapturesRedactedCandidateOutputBeforeCompensation(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("c", 40), plan)
	run, lease := fixture.claimedRun(t, 1)
	check := PlannedCheck{
		Name: "HTTP readiness", Kind: string(CheckCommand), Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"command":["app","check"],"attempts":1,"timeoutSeconds":1}`),
	}
	release := createRuntimeCandidateWithDomains(t, fixture, *run, lease, plan, "diagnose-v1", []PlannedCheck{check}, nil)
	owner := &diagnosingRuntimeOwner{
		orderingRuntimeOwner: &orderingRuntimeOwner{running: map[string]bool{}, allowConcurrent: true},
		diagnostics: RuntimeDiagnostics{Containers: []ContainerDiagnostics{{
			ID: "abc", Name: fmt.Sprintf("candidate-%d", release.Release.ID), State: "exited", ExitCode: 1, OOMKilled: true,
			Lines: []RuntimeLogLine{
				{Stream: "stdout", Text: "> next start"},
				{Stream: "stderr", Text: "Error: DATABASE_URL is not set (tried postgres://app:s3cr3t-pass@db/app)"},
			},
			Truncated: true,
		}}},
	}
	output := &recordingStepOutput{}
	executor := &NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner,
		checks: NewCheckRunner(&checkBackendFake{exitCode: 1, err: errors.New("fixture failure")}),
		proxy:  &countingActivationProxy{},
	}
	execution := StepExecution{Run: *run, ClaimToken: lease.Token, Output: output}
	if result := executor.startCandidate(context.Background(), execution, mustExecutionPlan(t, fixture, *run)); result.State != StepPassed {
		t.Fatalf("start candidate = %#v", result)
	}
	result := executor.verifyChecks(context.Background(), execution, mustExecutionPlan(t, fixture, *run), "readiness")
	// The kernel's OOM kill is the cause the container's own state proves,
	// ahead of anything the output says.
	if result.State != StepFailed || result.ErrorCode != "runtime_oom" {
		t.Fatalf("readiness result = %#v", result)
	}
	if !strings.Contains(result.ErrorMessage, "last output is in the build log") {
		t.Fatalf("error message does not point at the transcript: %q", result.ErrorMessage)
	}
	if len(owner.diagnosed) != 1 {
		t.Fatalf("diagnosed runtimes = %v", owner.diagnosed)
	}
	transcript := output.joined()
	for _, want := range []string{
		"exited, exit code 1, killed by the kernel for exceeding its memory limit), last 2 line(s):",
		"… earlier output omitted",
		"stdout: > next start",
		"stderr: Error: DATABASE_URL is not set",
	} {
		if !strings.Contains(transcript, want) {
			t.Fatalf("transcript lacks %q:\n%s", want, transcript)
		}
	}
	var evidence struct {
		Diagnostics *runtimeDiagnosticsEvidence `json:"diagnostics"`
		Recovery    json.RawMessage             `json:"recovery"`
	}
	if err := json.Unmarshal(result.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Diagnostics == nil || !evidence.Diagnostics.Available || evidence.Diagnostics.Lines != 2 || len(evidence.Diagnostics.Containers) != 1 || !evidence.Diagnostics.Containers[0].OOMKilled {
		t.Fatalf("diagnostics evidence = %+v", evidence.Diagnostics)
	}
	if strings.Contains(string(result.Evidence), "DATABASE_URL") || strings.Contains(string(result.Evidence), "next start") {
		t.Fatalf("evidence must carry counts, not output: %s", result.Evidence)
	}
	if len(evidence.Recovery) == 0 {
		t.Fatal("compensation evidence missing")
	}
	owner.mu.Lock()
	candidateRunning := owner.running[fmt.Sprintf("candidate-%d", release.Release.ID)]
	owner.mu.Unlock()
	if candidateRunning {
		t.Fatal("candidate was left running after diagnosis")
	}
}

func TestFailedReadinessNamesMissingSchemaFromCandidateOutput(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("d", 40), plan)
	run, lease := fixture.claimedRun(t, 1)
	check := PlannedCheck{
		Name: "HTTP readiness", Kind: string(CheckCommand), Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"command":["app","check"],"attempts":1,"timeoutSeconds":1}`),
	}
	release := createRuntimeCandidateWithDomains(t, fixture, *run, lease, plan, "schema-v1", []PlannedCheck{check}, nil)
	owner := &diagnosingRuntimeOwner{
		orderingRuntimeOwner: &orderingRuntimeOwner{running: map[string]bool{}, allowConcurrent: true},
		diagnostics: RuntimeDiagnostics{Containers: []ContainerDiagnostics{{
			ID: "abc", Name: fmt.Sprintf("candidate-%d", release.Release.ID), State: "running",
			Lines: []RuntimeLogLine{
				{Stream: "stdout", Text: "prisma:error Invalid `prisma.product.findMany()` invocation:"},
				{Stream: "stdout", Text: "The table `public.products` does not exist in the current database."},
			},
		}}},
	}
	output := &recordingStepOutput{}
	executor := &NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner,
		checks: NewCheckRunner(&checkBackendFake{exitCode: 1, err: errors.New("fixture failure")}),
		proxy:  &countingActivationProxy{},
	}
	execution := StepExecution{Run: *run, ClaimToken: lease.Token, Output: output}
	if result := executor.startCandidate(context.Background(), execution, mustExecutionPlan(t, fixture, *run)); result.State != StepPassed {
		t.Fatalf("start candidate = %#v", result)
	}
	result := executor.verifyChecks(context.Background(), execution, mustExecutionPlan(t, fixture, *run), "readiness")
	// The cause the output proves is the step's code, so the run's terminal
	// code names it rather than the gate that noticed.
	if result.State != StepFailed || result.ErrorCode != "schema_missing" {
		t.Fatalf("readiness result = %#v", result)
	}
	for _, want := range []string{
		"table public.products does not exist in its database",
		"prisma migrate deploy",
		"last output is in the build log",
	} {
		if !strings.Contains(result.ErrorMessage, want) {
			t.Fatalf("error message lacks %q: %q", want, result.ErrorMessage)
		}
	}
	if !strings.Contains(output.joined(), "Diagnosis: the application reports that table public.products does not exist") {
		t.Fatalf("transcript lacks the diagnosis:\n%s", output.joined())
	}
	var evidence struct {
		Diagnostics *runtimeDiagnosticsEvidence `json:"diagnostics"`
	}
	if err := json.Unmarshal(result.Evidence, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Diagnostics == nil || evidence.Diagnostics.Cause == nil || evidence.Diagnostics.Cause.Code != "schema_missing" || evidence.Diagnostics.Cause.Table != "public.products" {
		t.Fatalf("diagnostics evidence = %+v", evidence.Diagnostics)
	}
	if strings.Contains(string(result.Evidence), "findMany") {
		t.Fatalf("evidence must carry the cause, not output: %s", result.Evidence)
	}
}

func TestFailedReadinessReportsUnreadableDiagnostics(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("d", 40), plan)
	run, lease := fixture.claimedRun(t, 1)
	check := PlannedCheck{
		Name: "required", Kind: string(CheckCommand), Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"command":["app","check"],"attempts":1,"timeoutSeconds":1}`),
	}
	createRuntimeCandidateWithDomains(t, fixture, *run, lease, plan, "diagnose-v2", []PlannedCheck{check}, nil)
	owner := &diagnosingRuntimeOwner{
		orderingRuntimeOwner: &orderingRuntimeOwner{running: map[string]bool{}, allowConcurrent: true},
		err:                  errors.New("docker daemon unavailable"),
	}
	output := &recordingStepOutput{}
	executor := &NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner,
		checks: NewCheckRunner(&checkBackendFake{exitCode: 1, err: errors.New("fixture failure")}),
		proxy:  &countingActivationProxy{},
	}
	execution := StepExecution{Run: *run, ClaimToken: lease.Token, Output: output}
	if result := executor.startCandidate(context.Background(), execution, mustExecutionPlan(t, fixture, *run)); result.State != StepPassed {
		t.Fatalf("start candidate = %#v", result)
	}
	result := executor.verifyChecks(context.Background(), execution, mustExecutionPlan(t, fixture, *run), "readiness")
	if result.State != StepFailed || strings.Contains(result.ErrorMessage, "build log") {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(output.joined(), "Application output could not be read: docker daemon unavailable") {
		t.Fatalf("transcript = %s", output.joined())
	}
}

func TestRuntimeContainerIDsPreferPrimaryThenStack(t *testing.T) {
	metadata := mustJSON(dockerReleaseRuntimeMetadata{PrimaryContainerID: "primary", ContainerIDs: []string{"worker", "primary", ""}})
	ids := runtimeContainerIDs(ReleaseRuntime{Kind: "compose", RuntimeID: "project", Metadata: metadata})
	if strings.Join(ids, ",") != "primary,worker" {
		t.Fatalf("ids = %v", ids)
	}
	if ids := runtimeContainerIDs(ReleaseRuntime{Kind: "container", RuntimeID: "solo", Metadata: json.RawMessage(`{}`)}); strings.Join(ids, ",") != "solo" {
		t.Fatalf("container ids = %v", ids)
	}
}
