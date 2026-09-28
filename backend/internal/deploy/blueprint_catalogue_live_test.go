package deploy

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

// Every deployable blueprint is started the way a deployment starts it — its
// pinned image pulled, its rendered plan's mounts, limits, variables and
// generated secrets applied through the same runtime owner — and then asked
// the readiness questions its own definition ships. A wrong health path, a
// setting the image refuses to start without, a port that is not the one the
// application listens on: each fails here rather than on an operator's first
// deploy. JD_BLUEPRINT_ONLY narrows the sweep to a comma-separated list of ids.
func TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to pull and start every reviewed blueprint")
	}
	client := liveC4Docker(t)
	backend := NewDockerArtifactBackend(client)
	owner := NewDockerRuntimeOwner(client)
	checks := NewCheckRunner(client)
	definitions, err := blueprint.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	only := strings.Split(os.Getenv("JD_BLUEPRINT_ONLY"), ",")
	for _, definition := range definitions {
		if os.Getenv("JD_BLUEPRINT_ONLY") != "" && !slices.Contains(only, definition.ID) {
			continue
		}
		if supported, reason := blueprint.DeploymentSupport(definition); !supported {
			t.Logf("%s: not deployable in this release: %s", definition.ID, reason)
			continue
		}
		t.Run(definition.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
			defer cancel()
			stamp := time.Now().UnixNano()
			fixture := definition.Fixtures[0]
			plan, err := RenderBlueprintPlan(DraftSourceConfig{
				Kind: SourceBlueprint, Mode: SourceModeBlueprint, BlueprintID: definition.ID,
				BlueprintVersion: definition.Version, BlueprintInputs: fixture.Inputs,
			}, fmt.Sprintf("catalogue %d %s", stamp, definition.ID))
			if err != nil {
				t.Fatal(err)
			}
			image := liveCatalogueImage(t, ctx, backend, plan.Rendered.Image)
			variables := map[string]string{}
			for _, variable := range plan.Configuration.Variables {
				switch {
				case variable.Generate > 0:
					value, err := generatedSecret(variable.Generate)
					if err != nil {
						t.Fatal(err)
					}
					variables[variable.Name] = value
				case variable.Value != "":
					variables[variable.Name] = variable.Value
				}
			}
			for name, value := range liveCatalogueDependencies(t, ctx, backend, definition.ID) {
				variables[name] = value
			}
			// Loopback for everything this sweep publishes: a pinned port such
			// as Gitea's 2222 must not open on the host running the suite.
			runtime := plan.Configuration.Runtime
			runtime.Strategy, runtime.BindAddress = StrategyStopFirst, "127.0.0.1"
			for index := range runtime.Ports {
				runtime.Ports[index].BindAddress, runtime.Ports[index].HostPort = "127.0.0.1", liveC5LoopbackPort(t)
			}
			container := fmt.Sprintf("jd-e%d-r1", stamp)
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				_, _ = liveDockerOutput(cleanup, "rm", "--force", "--volumes", container)
				for _, mount := range runtime.Mounts {
					if mount.Ownership == OwnershipManaged {
						_, _ = liveDockerOutput(cleanup, "volume", "rm", "--force", mount.Source)
					}
				}
			})
			started, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{
				Run: EngineRun{ID: stamp, EnvironmentID: stamp}, Release: Release{ID: stamp, EnvironmentID: stamp, RunID: stamp, Number: 1},
				Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: runtime, Image: image}, RuntimeVariables: variables,
				Host: "127.0.0.1", Port: liveC5LoopbackPort(t),
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			target := started.Target
			target.OriginalPorts = []int{runtime.InternalPort, runtime.HostPort}
			ran := 0
			for _, check := range plan.Configuration.Checks {
				if check.Phase != "readiness" {
					continue
				}
				ran++
				if evidence := checks.Run(ctx, check, target); evidence.Outcome != HealthPassed {
					logs, _ := liveDockerOutput(ctx, "logs", "--tail", "60", container)
					t.Fatalf("%s: %+v\n--- container output ---\n%s", check.Name, evidence, logs)
				}
			}
			if ran == 0 {
				t.Fatal("the definition ships no readiness check")
			}
			// A published secondary port is bound where the plan said, not on
			// the routed port's lease.
			for _, port := range runtime.Ports {
				address := fmt.Sprintf("127.0.0.1:%d", port.HostPort)
				if evidence := checks.Run(ctx, PlannedCheck{Name: "published", Kind: "tcp", Phase: "readiness", Required: true,
					Config: mustJSON(map[string]any{"host": "127.0.0.1", "port": port.HostPort, "attempts": 20, "timeoutSeconds": 2, "intervalSeconds": 1})}, target); evidence.Outcome != HealthPassed {
					t.Fatalf("published port %s: %+v", address, evidence)
				}
			}
		})
	}
}

// liveCatalogueImage pulls a blueprint's pinned image and removes it again
// afterwards unless it was already on the host; a full sweep pulls tens of
// gigabytes and must not leave them behind.
func liveCatalogueImage(t *testing.T, ctx context.Context, backend *DockerArtifactBackend, reference string) ResolvedImage {
	t.Helper()
	_, present := liveDockerOutput(ctx, "image", "inspect", reference)
	image, err := backend.PullImage(ctx, reference, "", func(BuildLog) error { return nil })
	if err != nil {
		t.Fatalf("pull %s: %v", reference, err)
	}
	if present != nil {
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_, _ = liveDockerOutput(cleanup, "image", "rm", "--force", reference)
		})
	}
	return image
}

// liveCatalogueDependencies starts what a definition cannot answer without.
// Mongo Express is an administration page for a database somebody else runs;
// the sweep runs that database from the catalogue's own MongoDB blueprint.
func liveCatalogueDependencies(t *testing.T, ctx context.Context, backend *DockerArtifactBackend, id string) map[string]string {
	t.Helper()
	if id != "mongo-express" {
		return nil
	}
	mongo, err := blueprint.Get("mongodb")
	if err != nil {
		t.Fatal(err)
	}
	liveCatalogueImage(t, ctx, backend, mongo.Image.Reference)
	password, err := generatedSecret(32)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("jd-catalogue-mongo-%d", time.Now().UnixNano())
	if _, err := liveDockerOutput(ctx, "run", "-d", "--name", name, "-e", "MONGO_INITDB_ROOT_USERNAME=root", "-e", "MONGO_INITDB_ROOT_PASSWORD="+password, mongo.Image.Reference); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, _ = liveDockerOutput(cleanup, "rm", "--force", "--volumes", name)
	})
	raw, err := liveDockerOutput(ctx, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", name)
	if err != nil {
		t.Fatal(err)
	}
	address := strings.TrimSpace(string(raw))
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if _, err := liveDockerOutput(ctx, "exec", name, "mongosh", "--quiet", "--eval", "db.adminCommand('ping')"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("MongoDB did not answer a ping in time")
		}
		time.Sleep(2 * time.Second)
	}
	return map[string]string{"ME_CONFIG_MONGODB_URL": fmt.Sprintf("mongodb://root:%s@%s:27017/", password, address)}
}
