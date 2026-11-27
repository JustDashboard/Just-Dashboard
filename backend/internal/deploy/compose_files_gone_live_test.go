package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The images are ones a Docker host running this suite usually has, so the
// stack starts without a pull. Two services, since one would deploy as a
// single image rather than through Compose.
const liveFilesGoneCompose = `services:
  web:
    image: nginx:1.28-alpine
    ports: ["8089:80"]
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://127.0.0.1/"]
      interval: 2s
      timeout: 2s
      retries: 30
  cache:
    image: redis:7-alpine
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 2s
      timeout: 2s
      retries: 30
`

// A Compose release whose files went with its run's workspace (a dashboard
// of 22 to 27 Nov 2026 swept them) is still stopped, started again and
// removed: by the containers its runtime record names, with a status line
// saying so, where Compose itself would fail for want of the files.
func TestLiveAComposeReleaseWithoutItsFilesIsStoppedAndStartedByItsContainers(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to start a Compose release and take its files away")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	fixture := newTemplateFixture(t)
	created, err := fixture.planning.CreateComposeTemplate(ctx, ComposeTemplateRequest{
		Name: "Live files gone", Compose: liveFilesGoneCompose, HealthPath: "/",
	}, "live-test")
	if err != nil {
		t.Fatal(err)
	}
	source, err := fixture.planning.WithUserTemplate(ctx, DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeBlueprint, BlueprintID: created.ID,
		BlueprintVersion: created.Version, BlueprintInputs: created.Definition.Fixtures[0].Inputs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if source, err = source.WithTemplateDocuments(); err != nil {
		t.Fatal(err)
	}
	plan, err := RenderBlueprintPlan(source, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "run-1", "source")
	if err := writeComposeDocuments(root, source.ComposeFiles); err != nil {
		t.Fatal(err)
	}
	harness := newLiveComposeHarness(t)
	stamp := time.Now().UnixNano()
	built, err := harness.builder.Build(ctx, root, fmt.Sprintf("jd-live-files-gone-%d:release", stamp),
		plan.Configuration.Build, PreparedBuild{}, nil, nil, "", plan.Detection.Source, plan.Detection.Compose, nil)
	if err != nil {
		t.Fatalf("resolve the stack's images: %v", err)
	}
	project := fmt.Sprintf("jd-e%d", stamp)
	runtimePlan := plan.Configuration.Runtime
	started, err := harness.owner.StartCandidate(ctx, CandidateRuntimeRequest{
		Run:        EngineRun{ID: stamp, EnvironmentID: stamp},
		Release:    Release{ID: stamp, EnvironmentID: stamp, RunID: stamp, Number: 1},
		Snapshot:   runtimeReleaseSnapshot{Version: 1, Plan: runtimePlan, Compose: built.Compose},
		SourceRoot: root, RuntimeVariables: map[string]string{}, Host: "127.0.0.1",
	}, nil)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		label := "label=com.docker.compose.project=" + project
		if ids, err := liveDockerOutput(cleanup, "ps", "-aq", "--filter", label); err == nil && len(strings.Fields(string(ids))) > 0 {
			_, _ = liveDockerOutput(cleanup, append([]string{"rm", "--force", "--volumes"}, strings.Fields(string(ids))...)...)
		}
		if names, err := liveDockerOutput(cleanup, "network", "ls", "-q", "--filter", label); err == nil && len(strings.Fields(string(names))) > 0 {
			_, _ = liveDockerOutput(cleanup, append([]string{"network", "rm"}, strings.Fields(string(names))...)...)
		}
	})
	if err != nil {
		t.Fatalf("start the stack: %v", err)
	}
	runtime := runtimeFromInput(started.Input)
	metadata, err := decodeDockerRuntimeMetadata(runtime.Metadata)
	if err != nil || len(metadata.ContainerIDs) == 0 {
		t.Fatalf("runtime metadata = %+v, %v", metadata, err)
	}
	running := func() []string {
		t.Helper()
		states := []string{}
		for _, id := range metadata.ContainerIDs {
			out, err := liveDockerOutput(ctx, "inspect", "--format", "{{.State.Running}}", id)
			if err != nil {
				states = append(states, "gone")
				continue
			}
			states = append(states, strings.TrimSpace(string(out)))
		}
		return states
	}

	if err := os.RemoveAll(filepath.Dir(root)); err != nil {
		t.Fatal(err)
	}
	var lines []string
	emit := func(line BuildLog) error {
		lines = append(lines, line.Text)
		return nil
	}
	if _, err := harness.owner.Stop(ctx, runtime, runtimePlan, nil, false, emit); err != nil {
		t.Fatalf("stop without the files: %v", err)
	}
	if got := strings.Join(running(), ","); strings.Contains(got, "true") || strings.Contains(got, "gone") {
		t.Fatalf("after the stop the containers are %s, want all stopped and still there", got)
	}
	if err := harness.owner.StartExisting(ctx, runtime, nil, emit); err != nil {
		t.Fatalf("start again without the files: %v", err)
	}
	if got := strings.Join(running(), ","); strings.Contains(got, "false") || strings.Contains(got, "gone") {
		t.Fatalf("after the start the containers are %s, want all running", got)
	}
	evidence, err := harness.owner.Stop(ctx, runtime, runtimePlan, nil, true, emit)
	if err != nil || !evidence.Removed {
		t.Fatalf("remove without the files: %+v, %v", evidence, err)
	}
	for _, state := range running() {
		if state != "gone" {
			t.Fatalf("after the removal a container is still there (%s)", state)
		}
	}
	said := strings.Join(lines, "\n")
	if !strings.Contains(said, "stopped one by one") || !strings.Contains(said, "started one by one") {
		t.Fatalf("the status lines did not say how the release was handled:\n%s", said)
	}
}
