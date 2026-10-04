package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

func TestLiveManagedStandaloneContainerAdoptionAndRollback(t *testing.T) {
	liveManagedStandaloneAdoption(t, false)
}

func TestLiveManagedDeletedImageContainerAdoptionAndRollback(t *testing.T) {
	liveManagedStandaloneAdoption(t, true)
}

func liveManagedStandaloneAdoption(t *testing.T, missingImage bool) {
	if os.Getenv("JD_DOCKER_ADOPTION_LIVE") != "1" {
		t.Skip("set JD_DOCKER_ADOPTION_LIVE=1 for the isolated standalone-container lifecycle")
	}
	ctx := t.Context()
	client := liveC4Docker(t)
	if _, err := client.InspectImage(ctx, "caddy:2-alpine"); err != nil {
		t.Skip("fixture requires locally available caddy:2-alpine")
	}
	root := t.TempDir()
	name := fmt.Sprintf("jd-container-adoption-proof-%d", time.Now().UnixNano())
	network, volume := name+"-network", name+"-data"
	port := liveC5LoopbackPort(t)
	if err := os.Mkdir(filepath.Join(root, "public"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "public", "index.html"), []byte("adoption-ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Caddyfile"), []byte("{\n admin off\n auto_https off\n}\n:8080 {\n root * /srv\n file_server\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	docker := func(args ...string) []byte {
		t.Helper()
		output, err := liveDockerOutput(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	sourceTag := "caddy:2-alpine"
	recoveredImageID := ""
	if missingImage {
		sourceTag = name + ":original"
		if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM caddy:2-alpine\nLABEL fixture.deleted-image=\""+name+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		docker("build", "--pull=false", "--tag", sourceTag, root)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		containers, _ := client.ListContainersWithLabels(cleanupCtx, map[string]string{"fixture.just-dashboard.adoption": name})
		for _, item := range containers {
			if item.Labels["fixture.just-dashboard.adoption"] == name {
				_, _ = liveDockerOutput(cleanupCtx, "rm", "--force", item.ID)
			}
		}
		_, _ = liveDockerOutput(cleanupCtx, "rm", "--force", name)
		for _, existing := range []string{volume, volume + "-config", volume + "-caddy"} {
			_, _ = liveDockerOutput(cleanupCtx, "volume", "rm", existing)
		}
		_, _ = liveDockerOutput(cleanupCtx, "network", "rm", network)
		if missingImage {
			_, _ = liveDockerOutput(cleanupCtx, "image", "rm", "--force", sourceTag)
			if recoveredImageID != "" {
				_, _ = liveDockerOutput(cleanupCtx, "image", "rm", "--force", recoveredImageID)
			}
		}
	})
	docker("network", "create", network)
	for _, existing := range []string{volume, volume + "-config", volume + "-caddy"} {
		docker("volume", "create", existing)
	}
	id := strings.TrimSpace(string(docker("run", "--detach", "--name", name, "--network", network, "--network-alias", name+"-alias", "--publish", "127.0.0.1:"+strconv.Itoa(port)+":8080", "--volume", filepath.Join(root, "public")+":/srv:ro", "--volume", filepath.Join(root, "Caddyfile")+":/etc/caddy/Caddyfile:ro", "--volume", volume+":/persistent", "--volume", volume+"-config:/config", "--volume", volume+"-caddy:/data", "--memory", "100663296", "--cpus", "0.5", "--stop-timeout", "2", "--label", "fixture.just-dashboard.adoption="+name, "--env", "API_TOKEN=standalone-private-value", "--env", "EMPTY=", sourceTag)))
	adoptionLiveHTTP(t, port)
	if missingImage {
		image, err := client.InspectImage(ctx, sourceTag)
		if err != nil {
			t.Fatal(err)
		}
		docker("stop", "--time", "2", id)
		docker("image", "rm", "--force", image.ID)
		docker("start", id)
		adoptionLiveHTTP(t, port)
	}
	docker("exec", id, "/bin/sh", "-c", "printf persistent-proof > /persistent/sentinel")
	original, err := client.CaptureAdoptionContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	candidate := WorkloadCandidate{Key: "container:" + id, Kind: "container", Name: name, ResourceID: id, Running: 1, Total: 1, Services: []WorkloadService{{Name: name, ResourceID: id, State: "running", Image: "caddy:2-alpine", Ports: []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: port, ContainerPort: 8080, Protocol: "tcp"}}}}}
	candidate.Digest = WorkloadDigest(candidate)
	recovered, err := RecoverDockerWorkload(ctx, candidate, client, files.New([]string{root}), filepath.Join(root, "baseline-cache"))
	if err != nil {
		t.Fatalf("recovery blocked: %v %+v", err, recovered.Adoption.Issues)
	}
	var baselineSnapshot runtimeReleaseSnapshot
	if err := json.Unmarshal(recovered.Adoption.Snapshot, &baselineSnapshot); err != nil {
		t.Fatal(err)
	}
	expectedManagedImage := baselineSnapshot.Compose.Services[0].ConfigDigest
	if missingImage {
		recoveredImageID = expectedManagedImage
		fresh, err := RecoverDockerWorkload(ctx, candidate, client, files.New([]string{root}), filepath.Join(root, "baseline-cache"))
		if err != nil || fresh.Adoption.BaselineDigest != recovered.Adoption.BaselineDigest || string(fresh.Adoption.Runtime.Metadata) != string(recovered.Adoption.Runtime.Metadata) {
			t.Fatal("missing-image recovery is not stable across fresh review/adopt capture", err)
		}
		image, err := client.InspectImage(ctx, recoveredImageID)
		if err != nil || len(image.Env) != 0 || len(image.Entrypoint) != 0 || len(image.Command) != 0 {
			t.Fatal("snapshot image contains captured runtime configuration", err)
		}
	}
	fixture := newPlanningStoreFixture(t)
	if _, err := fixture.store.DB.Exec(`INSERT INTO sqlite_sequence(name,seq) VALUES('deploy_environments',?)`, time.Now().UnixMicro()); err != nil {
		t.Fatal(err)
	}
	draft, err := fixture.plans.CreateRecoveredDraft(ctx, 41, "operator", DraftIntentConfig{Name: name, Profile: ProfileCompose}, recovered)
	if err != nil {
		t.Fatal(err)
	}
	configuration := *draft.Data.Configuration
	configuration.Checks = []PlannedCheck{{Name: "Intentional failure", Kind: "command", Phase: "readiness", Required: true, Config: json.RawMessage(`{"command":["false"],"attempts":1,"timeoutSeconds":1}`)}}
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
	after, err := client.CaptureAdoptionContainer(ctx, id)
	if err != nil || after.Inspection.ID != original.Inspection.ID || after.Inspection.State.Pid != original.Inspection.State.Pid || after.Inspection.State.StartedAt != original.Inspection.State.StartedAt || string(mustJSON(after.Inspection.Config)) != string(mustJSON(original.Inspection.Config)) || string(mustJSON(after.Inspection.HostConfig)) != string(mustJSON(original.Inspection.HostConfig)) {
		t.Fatal("adoption changed the standalone runtime")
	}
	adoptionLiveHTTP(t, port)
	runs := NewOrchestrationStore(fixture.store)
	baseline, err := runs.LiveRelease(ctx, result.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	owner := NewDockerRuntimeOwner(client)
	sources := NewHostSourceAnalyzer([]string{root}, []string{root}, filepath.Join(root, "source-cache"), client, fixture.plans)
	executor := NewNormalizedStepExecutor(runs, fixture.plans, sources, NewArtifactBuilder(NewDockerArtifactBackend(client)), owner, NewCheckRunner(client), nil, filepath.Join(root, "runtime-cache")).WithPreflightObserver(&preflightObserverFake{observation: HostObservation{Facilities: map[string]FacilityObservation{"docker": {Available: true}, "compose": {Available: true}}}})
	engine := NewEngine(runs, executor, nil, EngineConfig{WorkerID: "standalone-adoption-live", PollEvery: 50 * time.Millisecond, LeaseTTL: 2 * time.Minute}, nil)
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
		request := RunRequest{ProjectID: result.ProjectID, EnvironmentID: result.EnvironmentID, Operation: operation, Trigger: TriggerManual, Actor: "operator", PlanRevision: revision, SlotClass: SlotHeavy, RequestDigest: fmt.Sprintf("standalone-%s-%d", operation, time.Now().UnixNano())}
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
		t.Fatalf("intentional standalone Deploy failure did not fail: %+v", failed)
	}
	restored, err := client.Inspect(ctx, id)
	if err != nil || restored.ID != id || restored.State != "running" {
		t.Fatalf("first failed Deploy did not restore original standalone ID: %v", err)
	}
	adoptionLiveHTTP(t, port)
	updated, err := fixture.plans.SaveEnvironmentConfiguration(ctx, result.ProjectID, result.EnvironmentID, ConfigurationWriteRequest{Revision: 2, Build: configuration.Build, Runtime: configuration.Runtime, Checks: []PlannedCheck{{Name: "HTTP readiness", Kind: "http", Phase: "readiness", Required: true, Config: json.RawMessage(`{"path":"/","attempts":10,"timeoutSeconds":2,"intervalSeconds":1}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	successful := enqueue(OperationDeploy, updated.Revision, nil)
	if successful.State != RunSucceeded {
		t.Fatalf("managed standalone Deploy failed: %+v", successful)
	}
	assertCurrent := func() {
		t.Helper()
		live, err := runs.LiveRelease(ctx, result.EnvironmentID)
		if err != nil {
			t.Fatal(err)
		}
		runtime, err := runs.RuntimeForRelease(ctx, live.Release.ID)
		if err != nil {
			t.Fatal(err)
		}
		ids := runtimeContainerIDs(*runtime)
		if len(ids) == 0 {
			t.Fatal("managed runtime has no container")
		}
		currentID := ids[0]
		capture, err := client.CaptureAdoptionContainer(ctx, currentID)
		if err != nil {
			t.Fatal(err)
		}
		if capture.Inspection.Image != expectedManagedImage || capture.Inspection.HostConfig.Memory != original.Inspection.HostConfig.Memory || capture.Inspection.HostConfig.NanoCPUs != original.Inspection.HostConfig.NanoCPUs {
			t.Fatal("managed standalone lost image or resource settings")
		}
		adoptionLiveHTTP(t, port)
		if got := string(docker("exec", currentID, "/bin/cat", "/persistent/sentinel")); got != "persistent-proof" {
			t.Fatal("standalone persistent data was lost")
		}
		for _, alias := range []string{name, name + "-alias"} {
			if got := strings.TrimSpace(string(docker("run", "--rm", "--network", network, "--entrypoint", "/bin/busybox", "caddy:2-alpine", "wget", "-q", "-O", "-", "http://"+alias+":8080/"))); got != "adoption-ready" {
				t.Fatal("standalone network alias was lost")
			}
		}
	}
	assertCurrent()
	rolled := enqueue(OperationRollback, 1, baseline)
	if rolled.State != RunSucceeded {
		t.Fatalf("standalone baseline Rollback failed: %+v", rolled)
	}
	assertCurrent()
	evidence := map[string]any{"test": t.Name(), "fixtureName": name, "checkedAt": time.Now().UTC(), "adoptionPreservedIDsPIDsStartedAtAndSettings": true, "failedDeployRestoredOriginalContainerID": true, "failedDeployRun": failed.ID, "managedDeployRun": successful.ID, "managedDeployState": successful.State, "baselineRollbackRun": rolled.ID, "baselineRollbackState": rolled.State, "persistentDataPreserved": true, "originalNameAndCustomNetworkAliasPreserved": true, "fixtureRemovedAtCleanup": true, "originalImageMissing": missingImage, "cachedImageStableAcrossRecoveryAndAdopt": missingImage}
	if location := os.Getenv("JD_ADOPTION_EVIDENCE_DIR"); location != "" {
		if err := os.MkdirAll(location, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(location, name+".json"), append(mustJSON(evidence), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("sanitized standalone lifecycle evidence: %s", mustJSON(evidence))
}
