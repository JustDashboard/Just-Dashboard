package deploy

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveSharedVariableReachesTheContainer starts a real container through
// the runtime owner activation calls, with the runtime values a run opens at
// the variable boundary activation uses, and reads them back from inside the
// container: a server variable, a secret one and a project one, each through
// the environment's own variable. A second run after the server value
// changes starts with the new value, because a release keeps the reference
// and a run opens the value. Opt-in: it builds an image and starts containers.
func TestLiveSharedVariableReachesTheContainer(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker/Buildx host")
	}
	ctx := context.Background()
	client := liveC4Docker(t)
	builder := NewArtifactBuilder(NewDockerArtifactBackend(client))
	owner := NewDockerRuntimeOwner(client)
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	tag := "jdtest-sv-shared:" + stamp
	t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
	root := t.TempDir()
	writeBuildFixture(t, root, "Dockerfile", "FROM alpine:3.22\nCMD [\"sleep\", \"600\"]\n")
	image := livePrepareAndBuild(t, builder, root, tag, BuildPlanConfig{Method: BuildDockerfile}, nil, nil, nil).Image

	fixture := newPlanningStoreFixture(t)
	plans := fixture.plans
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	share := func(project int64, name, value, sensitivity string) {
		t.Helper()
		if _, _, err := plans.PutSharedVariable(ctx, project, name, "operator", SharedVariableWrite{Value: &value, Sensitivity: sensitivity}); err != nil {
			t.Fatal(err)
		}
	}
	share(0, "API_URL", "https://api.example.test", "plain")
	share(0, "SENTRY_DSN", "https://public@sentry.example.test/7", "secret")
	share(projectID, "REGION", "eu-west-1", "plain")
	revision := 1
	for _, variable := range []struct{ name, reference, sensitivity string }{
		{"API_URL", "${{server.API_URL}}", "plain"},
		{"SENTRY_DSN", "${{server.SENTRY_DSN}}", "secret"},
		{"AWS_REGION", "${{project.REGION}}", "plain"},
	} {
		result, err := plans.PutVariable(ctx, projectID, environmentID, variable.name, "operator", VariableWriteRequest{
			Revision: revision, Reference: variable.reference, Sensitivity: variable.sensitivity, Scopes: []string{"runtime"},
		})
		if err != nil {
			t.Fatal(err)
		}
		revision = result.DesiredRevision
	}

	runs := NewOrchestrationStore(fixture.store)
	// The container's environment is numbered far from any real one, so its
	// name can never be a running deployment's on a shared daemon.
	containerEnvironment := time.Now().UnixNano()%1_000_000_000 + 700_000_000
	startAndRead := func(label string, number int) string {
		t.Helper()
		run, _, err := runs.Enqueue(ctx, RunRequest{
			ProjectID: projectID, EnvironmentID: environmentID, Operation: OperationDeploy,
			Trigger: TriggerManual, Actor: "operator", RequestDigest: "shared-live-" + label, PlanRevision: revision,
		})
		if err != nil {
			t.Fatal(err)
		}
		values, err := plans.OpenRunScopedVariables(ctx, run.ID, environmentID, "runtime")
		if err != nil {
			t.Fatal(err)
		}
		variables := map[string]string{}
		for _, value := range values {
			variables[value.Name] = value.Value
		}
		plan := RuntimePlanConfig{
			Strategy: StrategyStopFirst, StopSignal: "SIGTERM", GracePeriodSeconds: 1,
			Command: []string{}, Capabilities: []string{}, Devices: []string{}, Mounts: []RuntimeMount{},
		}
		engineRun := EngineRun{ID: containerEnvironment + int64(number), EnvironmentID: containerEnvironment}
		release := Release{ID: containerEnvironment + 100 + int64(number), EnvironmentID: containerEnvironment, RunID: engineRun.ID, Number: int64(number)}
		started, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{
			Run: engineRun, Release: release, Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: image},
			RuntimeVariables: variables, Host: "127.0.0.1",
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		runtime := runtimeFromInput(started.Input)
		defer func() { _, _ = owner.Stop(context.Background(), runtime, plan, nil, true, nil) }()
		output, err := liveDockerOutput(ctx, "exec", runtime.RuntimeID, "sh", "-c",
			`printf 'API_URL=%s\nSENTRY_DSN=%s\nAWS_REGION=%s\n' "$API_URL" "$SENTRY_DSN" "$AWS_REGION"`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.store.DB.Exec(`UPDATE deploy_runs SET status = 'success', state = 'succeeded', ended_at = 1 WHERE id = ?`, run.ID); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s run %d, container %s:\n%s", label, run.ID, runtime.RuntimeID[:12], output)
		return string(output)
	}

	first := startAndRead("first", 1)
	for _, want := range []string{"API_URL=https://api.example.test\n", "SENTRY_DSN=https://public@sentry.example.test/7\n", "AWS_REGION=eu-west-1\n"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the container read\n%s\nwithout %q", first, want)
		}
	}
	share(0, "API_URL", "https://api-v2.example.test", "plain")
	if second := startAndRead("second", 2); !strings.Contains(second, "API_URL=https://api-v2.example.test\n") {
		t.Fatalf("the next run's container read\n%s", second)
	}
}
