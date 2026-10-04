package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

// This opt-in fixture owns every resource it creates and never adopts the
// operator's projects. It proves the real engine's compensation, not a fake.
func TestLiveManagedComposeAdoptionAndRollback(t *testing.T) {
	if os.Getenv("JD_DOCKER_ADOPTION_LIVE") != "1" {
		t.Skip("set JD_DOCKER_ADOPTION_LIVE=1 to run the isolated Docker adoption lifecycle")
	}
	ctx := t.Context()
	client := liveC4Docker(t)
	if _, err := client.InspectImage(ctx, "caddy:2-alpine"); err != nil {
		t.Skip("the isolated fixture requires the already available caddy:2-alpine image")
	}
	root := t.TempDir()
	project := fmt.Sprintf("jd-adoption-proof-%d", time.Now().UnixNano())
	port := liveC5LoopbackPort(t)
	composeFile := filepath.Join(root, "compose.yml")
	if err := os.Mkdir(filepath.Join(root, "public"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "public", "index.html"), []byte("adoption-ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Caddyfile"), []byte("{\n admin off\n auto_https off\n}\n:8080 {\n root * /srv\n file_server\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	compose := fmt.Sprintf(`services:
  web:
    image: caddy:2-alpine
    ports: ["127.0.0.1:%d:8080"]
    volumes:
      - "%s:/srv:ro"
      - "%s:/etc/caddy/Caddyfile:ro"
      - "data:/persistent"
      - "caddy_config:/config"
      - "caddy_data:/data"
    environment: {API_TOKEN: "private-proof-value", EMPTY: ""}
    mem_limit: 100663296
    cpus: 0.5
    init: false
    stop_grace_period: 2s
    logging: {driver: local, options: {max-size: 5m}}
  worker:
    image: caddy:2-alpine
    entrypoint: ["/bin/sleep"]
    command: ["infinity"]
    init: false
    stop_grace_period: 1s
  inactive_a:
    image: caddy:2-alpine
    entrypoint: ["/bin/sleep"]
    command: ["infinity"]
    init: false
    stop_grace_period: 1s
  inactive_b:
    image: caddy:2-alpine
    entrypoint: ["/bin/sleep"]
    command: ["infinity"]
    init: false
    stop_grace_period: 1s
  never_created:
    image: caddy:2-alpine
    entrypoint: ["/bin/sleep"]
    command: ["infinity"]
    init: false
    stop_grace_period: 1s
volumes:
  data: {}
  caddy_config: {}
  caddy_data: {}
`, port, filepath.Join(root, "public"), filepath.Join(root, "Caddyfile"))
	if err := os.WriteFile(composeFile, []byte(compose), 0o600); err != nil {
		t.Fatal(err)
	}
	docker := func(args ...string) []byte {
		t.Helper()
		out, err := liveDockerOutput(ctx, args...)
		if err != nil {
			t.Fatalf("fixture Docker operation failed: %v", err)
		}
		return out
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = liveDockerOutput(cleanupCtx, "compose", "--project-name", project, "-f", composeFile, "down", "--volumes", "--remove-orphans", "--timeout", "2")
	})
	docker("compose", "--project-name", project, "-f", composeFile, "up", "-d", "--no-build", "web", "worker", "inactive_a", "inactive_b")
	docker("compose", "--project-name", project, "-f", composeFile, "stop", "--timeout", "2", "inactive_a", "inactive_b")
	before, candidate := adoptionLiveInventory(t, client, project)
	if len(before) != 4 || candidate.Running != 2 {
		t.Fatalf("invalid original fixture: %d containers %d running", len(before), candidate.Running)
	}
	docker("exec", before["web"].ID, "/bin/sh", "-c", "printf persistent-proof > /persistent/sentinel")
	adoptionLiveHTTP(t, port)
	var httpSamples, httpFailures atomic.Int64
	probeDone := make(chan struct{})
	probeStop := make(chan struct{})
	go func() {
		defer close(probeDone)
		probe := &http.Client{Timeout: time.Second}
		for {
			select {
			case <-probeStop:
				return
			default:
			}
			response, err := probe.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
			if err != nil {
				httpFailures.Add(1)
			} else {
				content, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if response.StatusCode != 200 || strings.TrimSpace(string(content)) != "adoption-ready" {
					httpFailures.Add(1)
				}
			}
			httpSamples.Add(1)
			time.Sleep(20 * time.Millisecond)
		}
	}()
	probeStopped := false
	defer func() {
		if !probeStopped {
			close(probeStop)
			<-probeDone
		}
	}()
	recovered, err := RecoverDockerWorkload(ctx, candidate, client, files.New([]string{root}), filepath.Join(root, "baseline-cache"))
	if err != nil {
		for _, service := range candidate.Services {
			if service.ResourceID != "" {
				capture, _ := client.CaptureAdoptionContainer(ctx, service.ResourceID)
				t.Logf("fixture writable-layer %s: %+v", service.Name, capture.Changes)
			}
		}
		t.Fatalf("recovery: %v; issues=%+v", err, recovered.Adoption.Issues)
	}
	fixture := newPlanningStoreFixture(t)
	draft, err := fixture.plans.CreateRecoveredDraft(ctx, 41, "operator", DraftIntentConfig{Name: project, Profile: ProfileCompose}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	configuration := *draft.Data.Configuration
	configuration.Checks = []PlannedCheck{{Name: "Intentional failed replacement", Kind: "command", Phase: "readiness", Required: true, Config: json.RawMessage(`{"command":["false"],"attempts":1,"timeoutSeconds":2}`)}}
	draft, err = fixture.plans.Save(ctx, draft.ID, 41, false, DraftSaveRequest{Revision: draft.Revision, Step: DraftConfiguration, Configuration: &configuration})
	if err != nil {
		t.Fatal(err)
	}
	draft = checkRecoveredDraft(t, fixture, draft)
	ack := []string{}
	for _, finding := range draft.Findings {
		if finding.Severity == PreflightWarning {
			ack = append(ack, finding.Code)
		}
	}
	result, err := fixture.plans.Commit(ctx, draft.ID, 41, false, DraftCommitRequest{Revision: draft.Revision, AcknowledgedWarnings: ack})
	if err != nil {
		t.Fatal(err)
	}
	afterAdoption, _ := adoptionLiveInventory(t, client, project)
	for service, original := range before {
		actual := afterAdoption[service]
		if original != actual {
			t.Fatalf("adoption mutated %s: before=%+v after=%+v", service, original, actual)
		}
	}
	adoptionLiveHTTP(t, port)
	close(probeStop)
	<-probeDone
	probeStopped = true
	if httpFailures.Load() != 0 || httpSamples.Load() == 0 {
		t.Fatalf("adoption HTTP continuity: samples=%d failures=%d", httpSamples.Load(), httpFailures.Load())
	}
	runs := NewOrchestrationStore(fixture.store)
	baseline, err := runs.LiveRelease(ctx, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	orphanName := project + "-external-oneoff"
	orphanID := strings.TrimSpace(string(docker("run", "--detach", "--name", orphanName, "--label", "com.docker.compose.project="+project, "--label", "com.docker.compose.service=external_oneoff", "--label", "com.docker.compose.oneoff=True", "--entrypoint", "/bin/sleep", "caddy:2-alpine", "infinity")))
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = liveDockerOutput(cleanupCtx, "rm", "--force", orphanName)
	})
	orphanBefore, err := client.Inspect(ctx, orphanID)
	if err != nil {
		t.Fatal(err)
	}
	owner := NewDockerRuntimeOwner(client)
	sources := NewHostSourceAnalyzer([]string{root}, []string{root}, filepath.Join(root, "source-cache"), client, fixture.plans)
	executor := NewNormalizedStepExecutor(runs, fixture.plans, sources, NewArtifactBuilder(NewDockerArtifactBackend(client)), owner, NewCheckRunner(client), nil, filepath.Join(root, "runtime-cache"))
	executor.WithPreflightObserver(&preflightObserverFake{observation: HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}, "compose": {Available: true}}}})
	engine := NewEngine(runs, executor, nil, EngineConfig{WorkerID: "adoption-live", PollEvery: 50 * time.Millisecond, LeaseTTL: 2 * time.Minute}, nil)
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = engine.Shutdown(shutdownCtx)
	})
	enqueue := func(operation Operation, revision int, target *ReleaseWithArtifacts) *EngineRun {
		t.Helper()
		request := RunRequest{ProjectID: result.ProjectID, EnvironmentID: result.EnvironmentID, Operation: operation, Trigger: TriggerManual, Actor: "operator", RequestDigest: fmt.Sprintf("%s-%d", operation, time.Now().UnixNano()), PlanRevision: revision, SlotClass: SlotHeavy}
		if target != nil {
			request.Metadata = mustJSON(map[string]any{"targetReleaseId": target.Release.ID})
			request.VariableSnapshotRunID = target.Release.RunID
		}
		run, _, err := runs.Enqueue(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		engine.Notify()
		return adoptionLiveWaitRun(t, runs, run.ID)
	}
	failed := enqueue(OperationDeploy, 2, nil)
	if failed.State != RunFailed {
		t.Fatalf("intentional readiness failure did not fail Deploy: %+v", failed)
	}
	failedSnapshot, _ := runs.Snapshot(ctx, failed.ID)
	compensated := false
	for _, step := range failedSnapshot.Steps {
		if step.Key != StepVerifyReadiness || step.State != StepFailed {
			continue
		}
		var captured struct {
			Recovery recoveryEvidence `json:"recovery"`
		}
		if err := json.Unmarshal(step.Evidence, &captured); err != nil {
			t.Fatal(err)
		}
		compensated = captured.Recovery.CandidateStopped && captured.Recovery.RuntimeRestored && captured.Recovery.RouteRestored && captured.Recovery.TailnetRestored
	}
	if !compensated {
		t.Fatal("failed first Deploy did not verify complete compensation")
	}
	adoptionLiveAssertBaseline(t, client, project, port)
	orphanAfter, err := client.Inspect(ctx, orphanID)
	if err != nil || orphanAfter.State != "running" || orphanAfter.ID != orphanBefore.ID || !orphanAfter.StartedAt.Equal(*orphanBefore.StartedAt) {
		t.Fatalf("shared-project lifecycle mutated external oneoff: %v", err)
	}
	live, err := runs.LiveRelease(ctx, result.EnvironmentID)
	if err != nil || live.Release.ID != baseline.Release.ID {
		t.Fatalf("failed deploy changed live pointer: %+v %v", live, err)
	}
	updated, err := fixture.plans.SaveEnvironmentConfiguration(ctx, result.ProjectID, result.EnvironmentID, ConfigurationWriteRequest{Revision: 2, Build: configuration.Build, Runtime: configuration.Runtime, Dependencies: configuration.Dependencies, Domains: configuration.Domains, Checks: []PlannedCheck{{Name: "Ready HTTP", Kind: "http", Phase: "readiness", Required: true, Config: json.RawMessage(`{"path":"/","attempts":10,"timeoutSeconds":2,"intervalSeconds":1}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	successful := enqueue(OperationDeploy, updated.Revision, nil)
	if successful.State != RunSucceeded {
		t.Fatalf("managed Deploy failed: %+v", successful)
	}
	deployed, _ := adoptionLiveInventory(t, client, project)
	if len(deployed) != 5 {
		t.Fatalf("explicit deploy did not create all five reviewed services: %+v", deployed)
	}
	for _, container := range deployed {
		if !container.Running {
			t.Fatal("explicit Deploy left a reviewed service inactive")
		}
	}
	adoptionLiveHTTP(t, port)
	rolled := enqueue(OperationRollback, 1, baseline)
	if rolled.State != RunSucceeded {
		t.Fatalf("baseline rollback failed: %+v", rolled)
	}
	adoptionLiveAssertBaseline(t, client, project, port)
	orphanAfter, err = client.Inspect(ctx, orphanID)
	if err != nil || orphanAfter.State != "running" || orphanAfter.ID != orphanBefore.ID || !orphanAfter.StartedAt.Equal(*orphanBefore.StartedAt) {
		t.Fatalf("baseline rollback mutated external oneoff: %v", err)
	}
	evidence := map[string]any{"test": t.Name(), "fixtureProject": project, "checkedAt": time.Now().UTC(), "baselineReleaseId": baseline.Release.ID, "adoptionPreservedIDsPIDsStartedAtAndSettings": true, "originalContainers": 4, "originalRunning": 2, "declaredServices": 5, "failedDeployRun": failed.ID, "failedDeployState": failed.State, "managedDeployRun": successful.ID, "managedDeployState": successful.State, "baselineRollbackRun": rolled.ID, "baselineRollbackState": rolled.State, "persistentDataPreserved": true, "externalOneoffPreserved": true, "HTTPContinuityAtAdoption": true, "HTTPAdoptionSamples": httpSamples.Load(), "HTTPAdoptionFailures": httpFailures.Load(), "fixtureRemovedAtCleanup": true}
	if location := os.Getenv("JD_ADOPTION_EVIDENCE_DIR"); location != "" {
		if err := os.MkdirAll(location, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(location, project+".json"), append(mustJSON(evidence), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("sanitized lifecycle evidence: %s", mustJSON(evidence))
}

type adoptionLiveContainer struct {
	ID, Image, SettingsDigest, StartedAt string
	PID                                  int
	Running                              bool
}

func adoptionLiveInventory(t *testing.T, client *dockerx.Client, project string) (map[string]adoptionLiveContainer, WorkloadCandidate) {
	t.Helper()
	containers, err := client.ListContainersWithLabels(t.Context(), map[string]string{"com.docker.compose.project": project})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })
	result := map[string]adoptionLiveContainer{}
	candidate := WorkloadCandidate{Key: "stack:" + project, Kind: "stack", Name: project, ResourceID: project, Total: 5, Services: []WorkloadService{}}
	for _, item := range containers {
		if strings.EqualFold(item.Labels["com.docker.compose.oneoff"], "true") {
			continue
		}
		capture, err := client.CaptureAdoptionContainer(t.Context(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		inspect := capture.Inspection
		name := inspect.Config.Labels["com.docker.compose.service"]
		result[name] = adoptionLiveContainer{ID: inspect.ID, Image: inspect.Image, PID: inspect.State.Pid, Running: inspect.State.Running, StartedAt: inspect.State.StartedAt, SettingsDigest: digestBytes(mustJSON(inspect.Config), mustJSON(inspect.HostConfig), mustJSON(inspect.Mounts), mustJSON(inspect.NetworkSettings.Networks))}
		ports := []dockerx.PortMapping{}
		for _, port := range item.Ports {
			ports = append(ports, dockerx.PortMapping{HostIP: port.IP, HostPort: int(port.PublicPort), ContainerPort: int(port.PrivatePort), Protocol: port.Type})
		}
		candidate.Services = append(candidate.Services, WorkloadService{Name: name, ResourceID: item.ID, State: item.State, Image: item.Image, Ports: ports})
		if inspect.State.Running {
			candidate.Running++
		}
	}
	if _, exists := result["never_created"]; !exists {
		candidate.Services = append(candidate.Services, WorkloadService{Name: "never_created", State: "not created"})
	}
	candidate.Digest = WorkloadDigest(candidate)
	return result, candidate
}

func adoptionLiveHTTP(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		client := &http.Client{Timeout: time.Second}
		response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
		if err == nil {
			content, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode == 200 && strings.TrimSpace(string(content)) == "adoption-ready" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("original HTTP application is unavailable")
}

func adoptionLiveAssertBaseline(t *testing.T, client *dockerx.Client, project string, port int) {
	t.Helper()
	containers, _ := adoptionLiveInventory(t, client, project)
	if len(containers) != 4 {
		t.Fatalf("restoration changed originally created services: %+v", containers)
	}
	for service, item := range containers {
		if item.Running != (service == "web" || service == "worker") {
			t.Fatalf("restoration changed %s activation state: %+v", service, item)
		}
	}
	adoptionLiveHTTP(t, port)
	output, err := liveDockerOutput(t.Context(), "exec", containers["web"].ID, "/bin/cat", "/persistent/sentinel")
	if err != nil || string(output) != "persistent-proof" {
		t.Fatalf("persistent volume lost: %s %v", output, err)
	}
}

func adoptionLiveWaitRun(t *testing.T, runs *OrchestrationStore, id int64) *EngineRun {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		run, err := runs.Run(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.State.Terminal() {
			return run
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("managed adoption run %d did not finish", id)
	return nil
}
