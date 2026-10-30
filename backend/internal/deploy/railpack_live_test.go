package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestLiveRailpackBuildsAndServesWithoutARecipe builds two repositories the
// way Railway and Coolify do, through the pinned Railpack release: a Node app
// with only a package.json and an index.js, and a Python app with only a
// requirements.txt and an app.py. Each goes through the real planner
// container, the real BuildKit frontend and the runtime owner a recipe's
// image goes through, and must answer over HTTP; a build variable reaches the
// plan's build command and no layer, and a repository with nothing to build
// is refused at the planner in Railpack's words. Images, containers and the
// pinned Railpack image, when this test pulled it, are removed.
func TestLiveRailpackBuildsAndServesWithoutARecipe(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to build with the real Railpack")
	}
	client := liveC4Docker(t)
	_, image, err := railpackRelease("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := liveDockerOutput(context.Background(), "image", "inspect", image); err != nil {
		// The planner's container pulls it; it is this test's to remove.
		t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", image) })
	}
	backend := NewDockerArtifactBackend(client).WithRailpackDir(t.TempDir())
	owner := NewDockerRuntimeOwner(client)
	for _, fixture := range []struct {
		name, provider, pkg, answer string
	}{
		{"railpack-node", "node", "node", "railpack node fixture"},
		{"railpack-python", "python", "python", "railpack python fixture"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
			defer cancel()
			root := t.TempDir()
			copyFrameworkFixture(t, fixture.name, root)
			// Detection builds this with a recipe and names Railpack as the
			// choice beside it.
			detection, err := (Detector{}).DetectPath(ctx, root, SourceIdentity{})
			if err != nil {
				t.Fatal(err)
			}
			if candidate := selectedDetectionCandidate(&detection); candidate == nil || candidate.BuildMethod != BuildRecipe ||
				candidate.RailpackProvider != fixture.provider {
				t.Fatalf("detection = %+v", detection.Candidates)
			}
			stamp := time.Now().UnixNano()
			tag := fmt.Sprintf("jd-railpack-test:%s-%d", fixture.name, stamp)
			t.Cleanup(func() { _, _ = liveDockerOutput(context.Background(), "image", "rm", "--force", tag) })
			secret := fmt.Sprintf("jd-railpack-secret-%d", stamp)
			variables := map[string]string{"PROBE_TOKEN": secret}
			// The plan's build command proves the value reached the commands
			// Railpack runs, as a BuildKit secret.
			config := BuildPlanConfig{Method: BuildRailpack, BuildCommand: `test -n "$PROBE_TOKEN"`}
			logs := []string{}
			result := livePrepareAndBuildWithin(t, NewArtifactBuilder(backend), root, "", tag, config, variables,
				func(line BuildLog) error { logs = append(logs, line.Text); return nil })
			assertLiveArtifactSecretFree(t, tag, secret, result, logs)
			evidence := result.Prepared.Railpack
			if evidence == nil || evidence.Version != RailpackDefaultVersion || !evidence.Success ||
				!slices.Contains(evidence.Providers, fixture.provider) || evidence.StartCommand == "" ||
				!slices.Contains(evidence.Secrets, "PROBE_TOKEN") ||
				!slices.ContainsFunc(evidence.Packages, func(pkg RailpackPackage) bool { return pkg.Name == fixture.pkg && pkg.Resolved != "" }) ||
				!slices.ContainsFunc(evidence.Images, func(name string) bool { return strings.Contains(name, "railpack-builder:mise-") }) {
				t.Fatalf("Railpack evidence = %+v", evidence)
			}
			if !strings.HasPrefix(result.Prepared.Toolchain, fixture.pkg+" ") || !strings.Contains(result.Prepared.Toolchain, "railpack "+RailpackDefaultVersion) {
				t.Fatalf("toolchain = %q", result.Prepared.Toolchain)
			}
			if !slices.ContainsFunc(logs, func(line string) bool { return strings.Contains(line, "railpack-frontend") }) {
				t.Fatal("the build did not go through Railpack's frontend")
			}
			run := EngineRun{ID: stamp, EnvironmentID: stamp}
			release := Release{ID: stamp, EnvironmentID: stamp, RunID: stamp, Number: 1}
			plan := RuntimePlanConfig{InternalPort: railpackDefaultPort, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst, GracePeriodSeconds: 3}
			started, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{Run: run, Release: release,
				Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: result.Image}, Host: "127.0.0.1", Port: liveC5LoopbackPort(t)}, nil)
			t.Cleanup(func() {
				_, _ = liveDockerOutput(context.Background(), "rm", "--force", "--volumes", fmt.Sprintf("jd-e%d-r1", stamp))
			})
			if err != nil {
				t.Fatal(err)
			}
			check := PlannedCheck{Name: "readiness", Kind: "http", Phase: "readiness", Required: true,
				Config: mustJSON(map[string]any{"path": "/", "attempts": 30, "timeoutSeconds": 2, "intervalSeconds": 1})}
			if evidence := NewCheckRunner(client).Run(ctx, check, started.Target); evidence.Outcome != HealthPassed {
				t.Fatalf("readiness: %+v", evidence)
			}
			response, err := (&http.Client{Timeout: 5 * time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/", started.Input.Port))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK || !strings.Contains(string(body), fixture.answer) {
				t.Fatalf("served %d %q", response.StatusCode, body)
			}
		})
	}
	t.Run("nothing to build", func(t *testing.T) {
		root := t.TempDir()
		writeBuildFixture(t, root, "README.md", "# notes\n")
		_, err := NewArtifactBuilder(backend).Prepare(t.Context(), root, BuildPlanConfig{Method: BuildRailpack}, false, "jd-railpack-test:none")
		var failure *RailpackPrepareError
		if !errors.As(err, &failure) {
			t.Fatalf("prepare = %v", err)
		}
		if cause := railpackPrepareCause(failure, causeContext{build: BuildPlanConfig{Method: BuildRailpack}}); cause.Code != "railpack_no_provider" ||
			cause.Fix == nil || cause.Fix.Field != "configuration.build.railpackProvider" || len(failure.Output) == 0 {
			t.Fatalf("cause = %+v, output %q", cause, failure.Output)
		}
	})
}
