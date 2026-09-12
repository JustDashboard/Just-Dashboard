package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type orderingRuntimeOwner struct {
	mu              sync.Mutex
	running         map[string]bool
	calls           []string
	contextErrors   []error
	allowConcurrent bool
}

func (o *orderingRuntimeOwner) StartCandidate(
	_ context.Context,
	request CandidateRuntimeRequest,
	_ func(BuildLog) error,
) (StartedRuntime, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.allowConcurrent {
		for id, running := range o.running {
			if running {
				return StartedRuntime{}, fmt.Errorf("candidate started concurrently with %s", id)
			}
		}
	}
	id := fmt.Sprintf("candidate-%d", request.Release.ID)
	o.running[id] = true
	o.calls = append(o.calls, "start:"+id)
	return StartedRuntime{
		Input: ReleaseRuntimeInput{
			ReleaseID: request.Release.ID, Kind: "container", RuntimeID: id, Name: id,
			Host: request.Host, Port: request.Port, Metadata: json.RawMessage(`{"version":1}`),
		},
		Target: CheckTarget{ContainerID: id, Host: request.Host, Port: request.Port},
	}, nil
}

type countingActivationProxy struct {
	applyCalls   int
	restoreCalls int
	verifyCalls  int
}

type unavailableCertificateProxy struct {
	countingActivationProxy
}

func (p *unavailableCertificateProxy) ResolveDeploymentCertificate(
	_ context.Context,
	_ []string,
) (string, string, error) {
	return "", "", errors.New("fixture certificate is unavailable")
}

func (p *countingActivationProxy) ApplyDeploymentRoute(
	_ context.Context,
	_ proxysvc.DeploymentRoute,
) (proxysvc.DeploymentRouteResult, error) {
	p.applyCalls++
	return proxysvc.DeploymentRouteResult{}, nil
}

func (p *countingActivationProxy) RestoreDeploymentRoute(
	_ context.Context,
	_ proxysvc.DeploymentRouteSnapshot,
) error {
	p.restoreCalls++
	return nil
}

func (p *countingActivationProxy) VerifyDeploymentRoute(_ context.Context, _ proxysvc.DeploymentRoute) error {
	p.verifyCalls++
	return nil
}

func TestFailedReadinessLeavesLiveRuntimeAndRouteUntouched(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	firstRun, firstLease := fixture.claimedRun(t, 1)
	first := createRuntimeCandidate(t, fixture, *firstRun, firstLease, plan, "preactivation-v1")
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *firstRun, firstLease.Token,
		ReleaseRuntimeInput{
			ReleaseID: first.Release.ID, Kind: "container", RuntimeID: "preactivation-old",
			Host: "127.0.0.1", Port: 32001, Metadata: json.RawMessage(`{"version":1}`),
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runs.ActivateCandidate(context.Background(), firstRun.ID, firstLease.Token, first.Release.ID); err != nil {
		t.Fatal(err)
	}
	fixture.finishActivatedRun(t, firstRun.ID, first.Release.ID, firstLease.Token)

	fixture.now = fixture.now.Add(time.Minute)
	fixture.addPlanWithRuntime(t, 2, strings.Repeat("b", 40), plan)
	secondRun, secondLease := fixture.claimedRun(t, 2)
	check := PlannedCheck{
		Name: "required", Kind: string(CheckCommand), Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"command":["app","check"],"attempts":1,"timeoutSeconds":1}`),
	}
	second := createRuntimeCandidateWithDomains(t, fixture, *secondRun, secondLease, plan, "preactivation-v2",
		[]PlannedCheck{check}, []PlannedDomain{{Hostname: "app.example.test"}})
	owner := &orderingRuntimeOwner{running: map[string]bool{"preactivation-old": true}, allowConcurrent: true}
	proxy := &countingActivationProxy{}
	executor := &NormalizedStepExecutor{
		store: fixture.runs, variables: fixture.variables, runtime: owner,
		checks: NewCheckRunner(&checkBackendFake{exitCode: 1, err: errors.New("fixture failure")}), proxy: proxy,
	}
	if result := executor.startCandidate(context.Background(), StepExecution{
		Run: *secondRun, ClaimToken: secondLease.Token, Output: discardStepOutput{},
	}, mustExecutionPlan(t, fixture, *secondRun)); result.State != StepPassed {
		t.Fatalf("start candidate = %#v", result)
	}
	result := executor.verifyChecks(context.Background(), StepExecution{
		Run: *secondRun, ClaimToken: secondLease.Token, Output: discardStepOutput{},
	}, mustExecutionPlan(t, fixture, *secondRun), "readiness")
	if result.State != StepFailed || result.ErrorCode != "health_gate_failed" {
		t.Fatalf("readiness result = %#v", result)
	}
	live, err := fixture.runs.LiveRelease(context.Background(), fixture.envID)
	if err != nil || live.Release.ID != first.Release.ID {
		t.Fatalf("live release = %#v, error=%v", live, err)
	}
	owner.mu.Lock()
	oldRunning := owner.running["preactivation-old"]
	candidateRunning := owner.running[fmt.Sprintf("candidate-%d", second.Release.ID)]
	owner.mu.Unlock()
	if !oldRunning || candidateRunning || proxy.applyCalls != 0 || proxy.restoreCalls != 0 || proxy.verifyCalls != 0 {
		t.Fatalf("preactivation changed state: old=%t candidate=%t proxy=%#v", oldRunning, candidateRunning, proxy)
	}
}

func (o *orderingRuntimeOwner) StartExisting(
	_ context.Context,
	runtime ReleaseRuntime,
	_ map[string]string,
	_ func(BuildLog) error,
) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.running[runtime.RuntimeID] = true
	o.calls = append(o.calls, "restore:"+runtime.RuntimeID)
	return nil
}

func (o *orderingRuntimeOwner) Stop(
	ctx context.Context,
	runtime ReleaseRuntime,
	_ RuntimePlanConfig,
	_ map[string]string,
	remove bool,
	_ func(BuildLog) error,
) (RuntimeStopEvidence, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.contextErrors = append(o.contextErrors, ctx.Err())
	if !o.running[runtime.RuntimeID] {
		return RuntimeStopEvidence{}, fmt.Errorf("runtime %s was not running", runtime.RuntimeID)
	}
	o.running[runtime.RuntimeID] = false
	o.calls = append(o.calls, "stop:"+runtime.RuntimeID)
	return RuntimeStopEvidence{
		RuntimeID: runtime.RuntimeID, Signal: "SIGTERM", GraceSeconds: 10,
		StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(), Removed: remove,
	}, nil
}

func TestCancellationAfterCandidateStartRestoresStopFirstRuntime(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, HostPort: 31993, BindAddress: "127.0.0.1",
		Strategy: StrategyStopFirst, StopSignal: "SIGTERM", GracePeriodSeconds: 2,
		Command: []string{}, Capabilities: []string{}, Devices: []string{},
		Mounts: []RuntimeMount{{Source: "exclusive-state", Target: "/data", Ownership: OwnershipManaged}},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	firstRun, firstLease := fixture.claimedRun(t, 1)
	first := createRuntimeCandidate(t, fixture, *firstRun, firstLease, plan, "cancel-v1")
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *firstRun, firstLease.Token,
		ReleaseRuntimeInput{
			ReleaseID: first.Release.ID, Kind: "container", RuntimeID: "cancel-old",
			Name: "cancel-old", Host: "127.0.0.1", Port: plan.HostPort, Metadata: json.RawMessage(`{"version":1}`),
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runs.ActivateCandidate(context.Background(), firstRun.ID, firstLease.Token, first.Release.ID); err != nil {
		t.Fatal(err)
	}
	fixture.finishActivatedRun(t, firstRun.ID, first.Release.ID, firstLease.Token)

	fixture.now = fixture.now.Add(time.Minute)
	fixture.addPlanWithRuntime(t, 2, strings.Repeat("b", 40), plan)
	secondRun, secondLease := fixture.claimedRun(t, 2)
	second := createRuntimeCandidate(t, fixture, *secondRun, secondLease, plan, "cancel-v2")
	owner := &orderingRuntimeOwner{running: map[string]bool{"cancel-old": true}}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner}
	if result := executor.startCandidate(context.Background(), StepExecution{
		Run: *secondRun, ClaimToken: secondLease.Token, Output: discardStepOutput{},
	}, mustExecutionPlan(t, fixture, *secondRun)); result.State != StepPassed {
		t.Fatalf("start candidate = %#v", result)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	evidence, err := executor.CleanupCancelledRun(cancelled, *secondRun, secondLease.Token)
	if err != nil || !strings.Contains(string(evidence), `"completed":true`) {
		t.Fatalf("cancellation cleanup = %s, error=%v", evidence, err)
	}
	owner.mu.Lock()
	oldRunning := owner.running["cancel-old"]
	candidateRunning := owner.running[fmt.Sprintf("candidate-%d", second.Release.ID)]
	contextErrors := append([]error(nil), owner.contextErrors...)
	owner.mu.Unlock()
	if !oldRunning || candidateRunning {
		t.Fatalf("cleanup old running=%t candidate running=%t", oldRunning, candidateRunning)
	}
	for _, contextErr := range contextErrors {
		if contextErr != nil {
			t.Fatalf("safety cleanup inherited cancellation: %v", contextErr)
		}
	}
}

func TestWildcardRuntimeBindingsUseLoopbackForChecksAndProxy(t *testing.T) {
	t.Parallel()
	if got := runtimeCheckHost("0.0.0.0"); got != "127.0.0.1" {
		t.Fatalf("IPv4 check host = %q", got)
	}
	if got := runtimeCheckHost("::"); got != "::1" {
		t.Fatalf("IPv6 check host = %q", got)
	}
	route := deploymentRoute(7, []PlannedDomain{{Hostname: "app.example.test"}}, "0.0.0.0", 32123)
	if route.Upstream != "http://127.0.0.1:32123" {
		t.Fatalf("wildcard proxy upstream = %q", route.Upstream)
	}
	target := targetForRuntime(ReleaseRuntime{Kind: "container", RuntimeID: "runtime", Host: "::", Port: 32123}, runtimeReleaseSnapshot{})
	if target.Host != "::1" {
		t.Fatalf("wildcard check target = %#v", target)
	}
	public := publicRouteActivationEvidence(proxysvc.DeploymentRouteResult{
		Snapshot: proxysvc.DeploymentRouteSnapshot{
			Name: "route.conf", Existed: true, Content: "credential-like prior bytes",
			ContentDigest: fakeContentDigest("prior route"),
		},
		Applied: true, Verified: true,
	})
	raw, err := json.Marshal(public)
	if err != nil || strings.Contains(string(raw), "credential-like") || !strings.Contains(string(raw), fakeContentDigest("prior route")) {
		t.Fatalf("public route evidence = %s, error=%v", raw, err)
	}
}

func TestHTTPSActivationWithoutExistingCertificateStopsCandidateBeforeCutover(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	run, lease := fixture.claimedRun(t, 1)
	release := createRuntimeCandidateWithDomains(t, fixture, *run, lease, plan, "tls-candidate", nil,
		[]PlannedDomain{{Hostname: "app.example.test", HTTPS: true, Ownership: OwnershipManaged}})
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *run, lease.Token, ReleaseRuntimeInput{
		ReleaseID: release.Release.ID, Kind: "container", RuntimeID: "tls-candidate",
		Host: "127.0.0.1", Port: 32123, Metadata: json.RawMessage(`{"version":1}`),
	}); err != nil {
		t.Fatal(err)
	}
	owner := &orderingRuntimeOwner{running: map[string]bool{"tls-candidate": true}, allowConcurrent: true}
	proxy := &unavailableCertificateProxy{}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner, proxy: proxy}
	result := executor.activate(context.Background(), StepExecution{
		Run: *run, ClaimToken: lease.Token, Output: discardStepOutput{},
	}, mustExecutionPlan(t, fixture, *run))
	if result.State != StepUnavailable || result.ErrorCode != "certificate_unavailable" || proxy.applyCalls != 0 {
		t.Fatalf("TLS activation = %#v, proxy=%#v", result, proxy)
	}
	owner.mu.Lock()
	running := owner.running["tls-candidate"]
	owner.mu.Unlock()
	var liveReleaseID int64
	if err := fixture.base.DB.QueryRow(`SELECT live_release_id FROM deploy_environments WHERE id = ?`, fixture.envID).Scan(&liveReleaseID); err != nil {
		t.Fatal(err)
	}
	if running || liveReleaseID != 0 {
		t.Fatalf("unavailable certificate changed live state: running=%t live=%d", running, liveReleaseID)
	}
}

type discardStepOutput struct{}

func (discardStepOutput) Log(string, string) error { return nil }
func (discardStepOutput) Event(EventInput) error   { return nil }

func TestStopFirstExecutorNeverStartsStatefulCandidateConcurrently(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, HostPort: 31991, BindAddress: "127.0.0.1",
		Strategy: StrategyStopFirst, StopSignal: "SIGTERM", GracePeriodSeconds: 5,
		Command: []string{}, Capabilities: []string{}, Devices: []string{},
		Mounts: []RuntimeMount{{Source: "stateful-data", Target: "/data", Ownership: OwnershipManaged}},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	firstRun, firstLease := fixture.claimedRun(t, 1)
	first := createRuntimeCandidate(t, fixture, *firstRun, firstLease, plan, "stateful-v1")
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *firstRun, firstLease.Token,
		ReleaseRuntimeInput{
			ReleaseID: first.Release.ID, Kind: "container", RuntimeID: "old-stateful",
			Name: "old-stateful", Host: "127.0.0.1", Port: plan.HostPort, Metadata: json.RawMessage(`{"version":1}`),
		}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runs.SetRuntimeState(context.Background(), firstRun.ID, firstLease.Token, first.Release.ID, "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runs.ActivateCandidate(context.Background(), firstRun.ID, firstLease.Token, first.Release.ID); err != nil {
		t.Fatal(err)
	}
	fixture.finishActivatedRun(t, firstRun.ID, first.Release.ID, firstLease.Token)

	fixture.now = fixture.now.Add(time.Minute)
	fixture.addPlanWithRuntime(t, 2, strings.Repeat("b", 40), plan)
	secondRun, secondLease := fixture.claimedRun(t, 2)
	second := createRuntimeCandidate(t, fixture, *secondRun, secondLease, plan, "stateful-v2")
	owner := &orderingRuntimeOwner{running: map[string]bool{"old-stateful": true}}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner}
	result := executor.startCandidate(context.Background(), StepExecution{
		Run: *secondRun, ClaimToken: secondLease.Token, Output: discardStepOutput{},
	}, mustExecutionPlan(t, fixture, *secondRun))
	if result.State != StepPassed {
		t.Fatalf("start candidate = %#v", result)
	}
	owner.mu.Lock()
	calls := append([]string(nil), owner.calls...)
	oldRunning := owner.running["old-stateful"]
	newRunning := owner.running[fmt.Sprintf("candidate-%d", second.Release.ID)]
	owner.mu.Unlock()
	want := []string{"stop:old-stateful", fmt.Sprintf("start:candidate-%d", second.Release.ID)}
	if fmt.Sprint(calls) != fmt.Sprint(want) || oldRunning || !newRunning {
		t.Fatalf("runtime ordering calls=%v old=%t new=%t, want %v", calls, oldRunning, newRunning, want)
	}
}

func TestFleetKeepsDisabledUnavailableWarningAndPassedHealthDistinct(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		observed *CheckEvidence
		want     HealthOutcome
	}{
		{name: "disabled", observed: &CheckEvidence{Name: "ready", Kind: "http", Phase: "readiness", Required: true, Outcome: HealthDisabled, Attempts: []CheckAttemptEvidence{}}, want: HealthDisabled},
		{name: "unavailable", observed: nil, want: HealthUnavailable},
		{name: "warning", observed: &CheckEvidence{Name: "ready", Kind: "http", Phase: "readiness", Required: false, Outcome: HealthWarning, Attempts: []CheckAttemptEvidence{}}, want: HealthWarning},
		{name: "passed", observed: &CheckEvidence{Name: "ready", Kind: "http", Phase: "readiness", Required: true, Outcome: HealthPassed, Attempts: []CheckAttemptEvidence{}}, want: HealthPassed},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			fixture := newReleaseStoreFixture(t)
			plan := RuntimePlanConfig{
				InternalPort: 3000, Strategy: StrategyBlueGreen, BindAddress: "127.0.0.1",
				Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
			}
			fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
			run, lease := fixture.claimedRun(t, 1)
			check := PlannedCheck{Name: "ready", Kind: "http", Phase: "readiness", Required: test.name != "warning"}
			release := createRuntimeCandidate(t, fixture, *run, lease, plan, "health-"+test.name, check)
			if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *run, lease.Token,
				ReleaseRuntimeInput{
					ReleaseID: release.Release.ID, Kind: "container", RuntimeID: "health-" + test.name,
					Host: "127.0.0.1", Port: 32111, Metadata: json.RawMessage(`{"version":1}`),
				}); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.runs.ActivateCandidate(context.Background(), run.ID, lease.Token, release.Release.ID); err != nil {
				t.Fatal(err)
			}
			if test.observed != nil {
				evidence := checkStepEvidence{Phase: "readiness", Outcome: test.observed.Outcome, Checks: []CheckEvidence{*test.observed}}
				if _, err := fixture.base.DB.Exec(`
					UPDATE deploy_steps SET status = 'passed', evidence_json = ?
					 WHERE run_id = ? AND step_key = 'verify_readiness'`, string(mustJSON(evidence)), run.ID); err != nil {
					t.Fatal(err)
				}
			}
			fixture.finishActivatedRun(t, run.ID, release.Release.ID, lease.Token)
			fleet, err := fixture.runs.Fleet(context.Background(), QueueBudget{})
			if err != nil || len(fleet.Deployments) != 1 || fleet.Deployments[0].Health != string(test.want) {
				t.Fatalf("fleet health = %#v, error=%v, want %s", fleet, err, test.want)
			}
			if fleet.Deployments[0].HostPort != 32111 {
				t.Fatalf("fleet showed the planned port instead of actual binding: %+v", fleet.Deployments[0])
			}
		})
	}
}

func TestRestartReusesLiveRuntimeWithoutCreatingARelease(t *testing.T) {
	t.Parallel()
	fixture := newReleaseStoreFixture(t)
	plan := RuntimePlanConfig{
		InternalPort: 3000, HostPort: 31992, BindAddress: "127.0.0.1",
		Strategy: StrategyStopFirst, Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
	deployRun, deployLease := fixture.claimedRun(t, 1)
	live := createRuntimeCandidate(t, fixture, *deployRun, deployLease, plan, "restart-live")
	if _, err := fixture.runs.RecordCandidateRuntime(context.Background(), *deployRun, deployLease.Token,
		ReleaseRuntimeInput{
			ReleaseID: live.Release.ID, Kind: "container", RuntimeID: "restart-runtime",
			Host: "127.0.0.1", Port: plan.HostPort, Metadata: json.RawMessage(`{"version":1}`),
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runs.ActivateCandidate(context.Background(), deployRun.ID, deployLease.Token, live.Release.ID); err != nil {
		t.Fatal(err)
	}
	fixture.finishActivatedRun(t, deployRun.ID, live.Release.ID, deployLease.Token)

	metadata := mustJSON(map[string]any{"targetReleaseId": live.Release.ID})
	restartRun, _, err := fixture.runs.Enqueue(context.Background(), RunRequest{
		ProjectID: fixture.projectID, EnvironmentID: fixture.envID,
		Operation: OperationRestart, Trigger: TriggerManual, Actor: "admin",
		RequestDigest: "restart-live-runtime", PlanRevision: 1,
		VariableSnapshotRunID: deployRun.ID, SlotClass: SlotLight, Metadata: metadata,
		Steps: []StepKey{StepStartCandidate, StepVerifyReadiness, StepVerifySmoke, StepRecordRelease},
	})
	if err != nil {
		t.Fatal(err)
	}
	restartLease, err := fixture.runs.ClaimNext(context.Background(), "restart-worker", QueueBudget{}, time.Minute)
	if err != nil || restartLease == nil {
		t.Fatalf("restart lease = %#v, error=%v", restartLease, err)
	}
	claimed, _ := fixture.runs.Run(context.Background(), restartRun.ID)
	owner := &orderingRuntimeOwner{running: map[string]bool{"restart-runtime": true}}
	executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: owner}
	result := executor.restartLiveRuntime(context.Background(), StepExecution{
		Run: *claimed, ClaimToken: restartLease.Token, Output: discardStepOutput{},
	})
	if result.State != StepPassed {
		t.Fatalf("restart result = %#v", result)
	}
	owner.mu.Lock()
	calls := append([]string(nil), owner.calls...)
	running := owner.running["restart-runtime"]
	owner.mu.Unlock()
	if fmt.Sprint(calls) != "[stop:restart-runtime restore:restart-runtime]" || !running {
		t.Fatalf("restart calls=%v running=%t", calls, running)
	}
	releases, err := fixture.runs.EnvironmentReleases(context.Background(), fixture.projectID, fixture.envID, 10)
	if err != nil || len(releases) != 1 || releases[0].ID != live.Release.ID {
		t.Fatalf("restart created a release: %#v, error=%v", releases, err)
	}
}

func createRuntimeCandidate(
	t *testing.T,
	fixture *releaseStoreFixture,
	run EngineRun,
	lease QueueLease,
	plan RuntimePlanConfig,
	identity string,
	checks ...PlannedCheck,
) *ReleaseWithArtifacts {
	return createRuntimeCandidateWithDomains(t, fixture, run, lease, plan, identity, checks, nil)
}

func createRuntimeCandidateWithDomains(
	t *testing.T,
	fixture *releaseStoreFixture,
	run EngineRun,
	lease QueueLease,
	plan RuntimePlanConfig,
	identity string,
	checks []PlannedCheck,
	domains []PlannedDomain,
) *ReleaseWithArtifacts {
	t.Helper()
	imageDigest := fakeContentDigest(identity)
	snapshot := runtimeReleaseSnapshot{
		Version: 1, Plan: plan,
		Image: ResolvedImage{
			Reference: "example.test/stateful:current", Digest: imageDigest,
			ConfigDigest: fakeContentDigest(identity + "-config"), Platforms: []string{"linux/amd64"},
		},
		Variables: []ReleaseVariableSnapshot{}, Dependencies: []PlannedDependency{},
		Checks: append([]PlannedCheck(nil), checks...), Domains: append([]PlannedDomain(nil), domains...),
		SourceIdentity: SourceIdentity{Kind: SourceLocal, LocalPath: "/srv/release-fixture", Revision: strings.Repeat("a", 40)},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	release, err := fixture.runs.CreateCandidateRelease(context.Background(), run, lease.Token, CandidateReleaseInput{
		Artifacts: []ReleaseArtifactInput{{
			Kind: ArtifactImage, Reference: "example.test/stateful:current", Digest: imageDigest,
			Metadata: json.RawMessage(`{"os":"linux","architecture":"amd64"}`), SizeBytes: 1024,
		}},
		Prepared: PreparedBuild{
			Method: BuildDockerfile, Dockerfile: "Dockerfile", DockerfileDigest: fakeContentDigest("Dockerfile"),
			BuildArgv: []string{"docker", "buildx", "build", "."}, BaseImages: []ResolvedImage{},
			CachePolicy: "reuse", SecretIDs: []string{},
		},
		RuntimeSnapshot: raw, RuntimeDigest: digestBytes(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func mustExecutionPlan(t *testing.T, fixture *releaseStoreFixture, run EngineRun) *StoredExecutionPlan {
	t.Helper()
	plan, err := fixture.runs.ExecutionPlan(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
