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

func TestReplicasAreOnlyForStatelessReleases(t *testing.T) {
	t.Parallel()
	base := func() PlanConfiguration {
		return PlanConfiguration{
			Build: BuildPlanConfig{Method: BuildImage, Secrets: []BuildSecretConfig{}, ReleaseTasks: []ReleaseTaskConfig{}},
			Runtime: RuntimePlanConfig{
				Image: "nginx:1.27", InternalPort: 80, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen,
				Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
			},
			Variables: []PlannedVariable{}, Dependencies: []PlannedDependency{}, Checks: []PlannedCheck{}, Domains: []PlannedDomain{},
		}
	}
	for _, replicas := range []int{0, 1, 2, MaxRuntimeReplicas} {
		plan := base()
		plan.Runtime.Replicas = replicas
		plan.Runtime.Mounts = []RuntimeMount{{Source: "/srv/static", Target: "/usr/share/nginx/html", ReadOnly: true, Ownership: OwnershipLinked}}
		if err := plan.Validate(); err != nil {
			t.Fatalf("%d replicas with a read-only mount were refused: %v", replicas, err)
		}
	}
	cases := []struct {
		name   string
		change func(*PlanConfiguration)
		reason string
	}{
		{"too many", func(p *PlanConfiguration) { p.Runtime.Replicas = MaxRuntimeReplicas + 1 }, "between 1 and 8"},
		{"negative", func(p *PlanConfiguration) { p.Runtime.Replicas = -1 }, "between 1 and 8"},
		{"writable mount", func(p *PlanConfiguration) {
			p.Runtime.Mounts = []RuntimeMount{{Source: "data", Target: "/var/lib/app", Ownership: OwnershipManaged}}
		}, "/var/lib/app is writable"},
		{"host network", func(p *PlanConfiguration) { p.Runtime.HostNetwork = true }, "host network"},
		{"host port", func(p *PlanConfiguration) { p.Runtime.HostPort = 8080; p.Runtime.Strategy = StrategyStopFirst }, "host port 8080"},
		{"published port", func(p *PlanConfiguration) {
			p.Runtime.Ports = []PublishedPort{{HostPort: 2222, ContainerPort: 22}}
		}, "published port 2222"},
		{"compose", func(p *PlanConfiguration) { p.Build.Method = BuildCompose }, "Compose stack"},
		{"tailnet only", func(p *PlanConfiguration) { p.Runtime.Tailnet = &RouteTailnet{} }, "tailnet-only environment"},
	}
	for _, tc := range cases {
		plan := base()
		plan.Runtime.Replicas = 2
		tc.change(&plan)
		err := plan.Validate()
		var field *ValidationError
		if err == nil || !errors.As(err, &field) || field.Field != "runtime.replicas" || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: %v, want a runtime.replicas refusal naming %q", tc.name, err, tc.reason)
		}
	}
	// One replica is the release as it always was, whatever the plan holds.
	plan := base()
	plan.Runtime.Replicas = 1
	plan.Runtime.HostNetwork = true
	plan.Runtime.Strategy = StrategyStopFirst
	if err := validateReplicas(plan.Build.Method, plan.Runtime); err != nil {
		t.Fatalf("one replica on the host network was refused: %v", err)
	}
}

func TestPreflightSaysWhatReplicasCost(t *testing.T) {
	t.Parallel()
	runtime := RuntimePlanConfig{Replicas: 3, MemoryMB: 256}
	item, ok := replicasFinding(BuildImage, runtime)
	if !ok || item.Code != "replicas" || item.Severity != PreflightPass || item.FieldID != "runtime.replicas" ||
		!strings.Contains(item.Means, "768 MiB for the release") || !strings.Contains(item.Means, "every one of them passes its readiness check") {
		t.Fatalf("finding = %+v", item)
	}
	if _, ok := replicasFinding(BuildImage, RuntimePlanConfig{Replicas: 1}); ok {
		t.Fatal("one replica raised a finding")
	}
	runtime.Mounts = []RuntimeMount{{Source: "data", Target: "/data"}}
	if item, ok := replicasFinding(BuildImage, runtime); !ok || item.Severity != PreflightBlocked || !strings.Contains(item.Means, "/data is writable") {
		t.Fatalf("refusal = %+v", item)
	}
}

func TestReplicatedRuntimeIsReadAsOneReleaseOfSeveralContainers(t *testing.T) {
	t.Parallel()
	metadata := dockerReleaseRuntimeMetadata{Version: 1, PrimaryContainerID: "c1", ContainerIDs: []string{"c1", "c2"},
		Replicas: []runtimeReplica{
			{Index: 1, ContainerID: "c1", Name: "jd-e7-r3", Host: "127.0.0.1", Port: 32001},
			{Index: 2, ContainerID: "c2", Name: "jd-e7-r3-2", Host: "0.0.0.0", Port: 32002},
		}}
	runtime := ReleaseRuntime{Kind: "container", RuntimeID: "c1", Name: "jd-e7-r3", Host: "127.0.0.1", Port: 32001, Metadata: mustJSON(metadata)}
	if got := runtimeUpstreams(runtime); strings.Join(got, " ") != "http://127.0.0.1:32001 http://127.0.0.1:32002" {
		t.Fatalf("upstreams = %v", got)
	}
	targets := replicaTargets(runtime, runtimeReleaseSnapshot{Plan: RuntimePlanConfig{InternalPort: 3000}})
	if len(targets) != 2 || targets[1].ContainerID != "c2" || targets[1].Port != 32002 || targets[1].Host != "127.0.0.1" ||
		targets[0].OriginalPorts[0] != 3000 {
		t.Fatalf("targets = %+v", targets)
	}
	if ids := runtimeContainerIDs(runtime); strings.Join(ids, " ") != "c1 c2" {
		t.Fatalf("container ids = %v", ids)
	}
	if ids := runtimeMetricIDs(runtime); strings.Join(ids, " ") != "c1 c2" {
		t.Fatalf("metric ids = %v", ids)
	}
	if sources := runtimeMetricSources(runtime); len(sources) != 2 || sources[1].Name != "jd-e7-r3-2" {
		t.Fatalf("metric sources = %+v", sources)
	}
	single := ReleaseRuntime{Kind: "container", RuntimeID: "c9", Host: "127.0.0.1", Port: 32009, Metadata: json.RawMessage(`{"version":1}`)}
	if got := runtimeUpstreams(single); len(got) != 1 || got[0] != "http://127.0.0.1:32009" {
		t.Fatalf("a one-container release's upstreams = %v", got)
	}
	if name := replicaContainerName(7, 3, 1); name != "jd-e7-r3" {
		t.Fatalf("first replica's name = %s", name)
	}
	if name := replicaContainerName(7, 3, 4); name != "jd-e7-r3-4" {
		t.Fatalf("fourth replica's name = %s", name)
	}
	orphan := ReleaseRuntime{Kind: "container", RuntimeID: replicaContainerName(7, 3, 1), Metadata: orphanReplicas(7, 3, 3)}
	if ids := runtimeContainerIDs(orphan); strings.Join(ids, " ") != "jd-e7-r3-2 jd-e7-r3-3 jd-e7-r3" {
		t.Fatalf("orphan names = %v", ids)
	}
}

func TestWarmReplicatedReleaseIsOneState(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		states []string
		want   string
	}{
		{[]string{"paused", "paused"}, "paused"},
		{[]string{"paused", "running"}, "running"},
		{[]string{"running", "running"}, "running"},
		{[]string{"paused", "missing"}, "missing"},
		{[]string{"exited", "running"}, "exited"},
		{[]string{"running", "exited"}, "exited"},
		{[]string{"paused"}, "paused"},
	} {
		if got := warmStateOf(tc.states); got != tc.want {
			t.Errorf("%v = %s, want %s", tc.states, got, tc.want)
		}
	}
}

// replicaRuntimeOwner starts each candidate as the replicas its request
// asks for, on the ports the executor leased.
type replicaRuntimeOwner struct {
	mu       sync.Mutex
	requests []CandidateRuntimeRequest
	stopped  []string
}

func (o *replicaRuntimeOwner) StartCandidate(_ context.Context, request CandidateRuntimeRequest, _ func(BuildLog) error) (StartedRuntime, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, request)
	count := request.Snapshot.Plan.EffectiveReplicas()
	metadata := dockerReleaseRuntimeMetadata{Version: 1}
	ports := append([]int{request.Port}, request.ReplicaPorts...)
	for index := 1; index <= count; index++ {
		id := fmt.Sprintf("candidate-%d-%d", request.Release.ID, index)
		metadata.Replicas = append(metadata.Replicas, runtimeReplica{Index: index, ContainerID: id, Name: id, Host: request.Host, Port: ports[index-1]})
		metadata.ContainerIDs = append(metadata.ContainerIDs, id)
	}
	primary := metadata.Replicas[0]
	metadata.PrimaryContainerID = primary.ContainerID
	return StartedRuntime{
		Input: ReleaseRuntimeInput{ReleaseID: request.Release.ID, Kind: "container", RuntimeID: primary.ContainerID,
			Name: primary.Name, Host: primary.Host, Port: primary.Port, Metadata: mustJSON(metadata)},
		Target: CheckTarget{ContainerID: primary.ContainerID, Host: primary.Host, Port: primary.Port},
	}, nil
}

func (o *replicaRuntimeOwner) StartExisting(context.Context, ReleaseRuntime, map[string]string, func(BuildLog) error) error {
	return nil
}

func (o *replicaRuntimeOwner) Stop(_ context.Context, runtime ReleaseRuntime, _ RuntimePlanConfig, _ map[string]string, remove bool, _ func(BuildLog) error) (RuntimeStopEvidence, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopped = append(o.stopped, runtimeContainerIDs(runtime)...)
	return RuntimeStopEvidence{RuntimeID: runtime.RuntimeID, Removed: remove}, nil
}

// replicaCheckBackend answers a command check per container, failing the
// replicas whose id ends with failing.
type replicaCheckBackend struct {
	mu      sync.Mutex
	asked   []string
	failing string
}

func (b *replicaCheckBackend) ContainerHealth(context.Context, string) (string, error) {
	return "healthy", nil
}

func (b *replicaCheckBackend) ExecCheck(_ context.Context, id string, _ []string, _ time.Duration) (int, []byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asked = append(b.asked, id)
	if b.failing != "" && strings.HasSuffix(id, b.failing) {
		return 1, []byte("not ready"), nil
	}
	return 0, nil, nil
}

// replicaRouteProxy keeps the routes it was asked to apply.
type replicaRouteProxy struct {
	mu     sync.Mutex
	routes []proxysvc.DeploymentRoute
}

func (p *replicaRouteProxy) ApplyDeploymentRoute(_ context.Context, route proxysvc.DeploymentRoute) (proxysvc.DeploymentRouteResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes = append(p.routes, route)
	return proxysvc.DeploymentRouteResult{Applied: true, Verified: true}, nil
}

func (p *replicaRouteProxy) RestoreDeploymentRoute(context.Context, proxysvc.DeploymentRouteSnapshot) error {
	return nil
}

func (p *replicaRouteProxy) VerifyDeploymentRoute(context.Context, proxysvc.DeploymentRoute) error {
	return nil
}

// A replicated candidate is started on a port of its own per replica, is
// ready only when every replica passes the readiness check, and goes live as
// one route sharing its requests between all of them.
func TestReplicatedCandidateIsReadyOnlyWhenEveryReplicaIs(t *testing.T) {
	t.Parallel()
	plan := RuntimePlanConfig{
		InternalPort: 3000, BindAddress: "127.0.0.1", Strategy: StrategyBlueGreen, Replicas: 3,
		Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
	}
	check := PlannedCheck{
		Name: "ready", Kind: string(CheckCommand), Phase: "readiness", Required: true,
		Config: json.RawMessage(`{"command":["app","ready"],"attempts":1,"timeoutSeconds":1}`),
	}
	type outcome struct {
		readiness StepResult
		owner     *replicaRuntimeOwner
		backend   *replicaCheckBackend
		proxy     *replicaRouteProxy
		releaseID int64
	}
	run := func(t *testing.T, failing string) outcome {
		fixture := newReleaseStoreFixture(t)
		fixture.addPlanWithRuntime(t, 1, strings.Repeat("a", 40), plan)
		claimed, lease := fixture.claimedRun(t, 1)
		release := createRuntimeCandidateWithDomains(t, fixture, *claimed, lease, plan, "replicated-v1",
			[]PlannedCheck{check}, []PlannedDomain{{Hostname: "app.example.test"}})
		got := outcome{owner: &replicaRuntimeOwner{}, backend: &replicaCheckBackend{failing: failing},
			proxy: &replicaRouteProxy{}, releaseID: release.Release.ID}
		executor := &NormalizedStepExecutor{store: fixture.runs, variables: fixture.variables, runtime: got.owner,
			checks: NewCheckRunner(got.backend), proxy: got.proxy}
		execution := StepExecution{Run: *claimed, ClaimToken: lease.Token, Output: discardStepOutput{}}
		if result := executor.startCandidate(context.Background(), execution, mustExecutionPlan(t, fixture, *claimed)); result.State != StepPassed {
			t.Fatalf("start candidate = %#v", result)
		}
		request := got.owner.requests[0]
		if len(request.ReplicaPorts) != 2 || request.ReplicaPorts[0] == request.Port || request.ReplicaPorts[1] == request.Port ||
			request.ReplicaPorts[0] == request.ReplicaPorts[1] {
			t.Fatalf("replica ports = %d + %v", request.Port, request.ReplicaPorts)
		}
		got.readiness = executor.verifyChecks(context.Background(), execution, mustExecutionPlan(t, fixture, *claimed), "readiness")
		if got.readiness.State == StepPassed {
			if activated := executor.activate(context.Background(), execution, nil); activated.State != StepPassed {
				t.Fatalf("activate = %#v", activated)
			}
		}
		return got
	}

	t.Run("one replica that does not answer fails the release", func(t *testing.T) {
		t.Parallel()
		got := run(t, "-2")
		if got.readiness.State != StepFailed {
			t.Fatalf("readiness = %#v", got.readiness)
		}
		// The third replica is not asked once the second has failed, and
		// the whole candidate — every replica — is removed.
		if strings.Join(got.backend.asked, " ") != fmt.Sprintf("candidate-%d-1 candidate-%d-2", got.releaseID, got.releaseID) {
			t.Fatalf("asked = %v", got.backend.asked)
		}
		if len(got.owner.stopped) != 3 || len(got.proxy.routes) != 0 {
			t.Fatalf("stopped %v, routes %v", got.owner.stopped, got.proxy.routes)
		}
		var evidence struct {
			Health checkStepEvidence `json:"health"`
		}
		if err := json.Unmarshal(got.readiness.Evidence, &evidence); err != nil || len(evidence.Health.Checks) != 2 ||
			evidence.Health.Checks[0].Replica != 1 || evidence.Health.Checks[1].Replica != 2 {
			t.Fatalf("evidence = %s, %v", got.readiness.Evidence, err)
		}
	})

	t.Run("every replica ready goes live as one route over all of them", func(t *testing.T) {
		t.Parallel()
		got := run(t, "")
		if got.readiness.State != StepPassed || len(got.backend.asked) != 3 {
			t.Fatalf("readiness = %#v, asked %v", got.readiness, got.backend.asked)
		}
		if len(got.proxy.routes) != 1 {
			t.Fatalf("routes = %+v", got.proxy.routes)
		}
		request := got.owner.requests[0]
		want := []string{fmt.Sprintf("http://127.0.0.1:%d", request.Port)}
		for _, port := range request.ReplicaPorts {
			want = append(want, fmt.Sprintf("http://127.0.0.1:%d", port))
		}
		if servers := got.proxy.routes[0].Servers(); strings.Join(servers, " ") != strings.Join(want, " ") {
			t.Fatalf("route servers = %v, want %v", servers, want)
		}
	})
}
