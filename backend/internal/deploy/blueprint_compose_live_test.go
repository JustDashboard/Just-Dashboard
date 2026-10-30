package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/blueprint"
)

// Every multi-service template is brought up the way a deployment brings one
// up: its source saved as this dashboard's render, the document written into
// a private workspace, every service image resolved and pulled through the
// Compose build step, the stack started through the runtime owner's Compose
// path with generated secrets in the frozen variable scope, and then asked
// the readiness questions its own definition ships, against the loopback
// publication of the one public service. Each service must also report its
// own Docker healthcheck healthy, only the public service may publish a port,
// and that port only on loopback. JD_BLUEPRINT_ONLY narrows the sweep to a
// comma-separated list of ids; images the sweep pulled are removed again.
func TestLiveEveryComposeTemplateStartsAndAnswersItsOwnChecks(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to pull and start every multi-service template")
	}
	harness := newLiveComposeHarness(t)
	definitions, err := blueprint.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	only := strings.Split(os.Getenv("JD_BLUEPRINT_ONLY"), ",")
	ran := 0
	for _, definition := range definitions {
		if !definition.IsCompose() || (os.Getenv("JD_BLUEPRINT_ONLY") != "" && !slices.Contains(only, definition.ID)) {
			continue
		}
		ran++
		t.Run(definition.ID, func(t *testing.T) {
			harness.startsAndAnswersItsOwnChecks(t, definition, DraftSourceConfig{
				Kind: SourceCompose, Mode: SourceModeBlueprint, BlueprintID: definition.ID,
				BlueprintVersion: definition.Version, BlueprintInputs: definition.Fixtures[0].Inputs,
			})
		})
	}
	if ran == 0 {
		t.Skip("no multi-service template matched JD_BLUEPRINT_ONLY")
	}
}

// liveComposeHarness is what the sweep shares across stacks: the build step
// that resolves and pulls a stack's images, the runtime owner that starts it,
// and the runner that asks its checks.
type liveComposeHarness struct {
	builder *ArtifactBuilder
	owner   *DockerRuntimeOwner
	checks  *CheckRunner
}

func newLiveComposeHarness(t *testing.T) liveComposeHarness {
	t.Helper()
	client := liveC4Docker(t)
	return liveComposeHarness{
		builder: NewArtifactBuilder(NewDockerArtifactBackend(client)),
		owner:   NewDockerRuntimeOwner(client), checks: NewCheckRunner(client),
	}
}

// startsAndAnswersItsOwnChecks brings one stack up from the source a draft
// saved for it, as a deployment does, and holds it to its definition: every
// readiness check passes, every service reports healthy, and only the public
// service publishes, on loopback. Everything it made is removed afterwards.
func (h liveComposeHarness) startsAndAnswersItsOwnChecks(t *testing.T, definition *blueprint.Blueprint, saved DraftSourceConfig) {
	t.Helper()
	builder, owner, checks := h.builder, h.owner, h.checks
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Minute)
	defer cancel()
	started := time.Now()
	stamp := time.Now().UnixNano()
	source, err := saved.WithTemplateDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if err := source.validateForNewDeployment(); err != nil {
		t.Fatal(err)
	}
	plan, err := RenderBlueprintPlan(source, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := writeComposeDocuments(root, source.ComposeFiles); err != nil {
		t.Fatal(err)
	}
	cached := map[string]bool{}
	for _, service := range definition.Services {
		_, err := liveDockerOutput(ctx, "image", "inspect", service.Image.Reference)
		cached[service.Image.Reference] = err == nil
	}
	present := liveImageDigests(ctx)
	built, err := builder.Build(ctx, root, fmt.Sprintf("jd-live-template-%d:release", stamp),
		plan.Configuration.Build, PreparedBuild{}, nil, nil, "", plan.Detection.Source, plan.Detection.Compose, nil)
	if built.Compose != nil {
		liveComposeTemplateImages(t, built.Compose, cached, present)
	}
	if err != nil {
		t.Fatalf("resolve and pull the stack's images: %v", err)
	}
	variables := map[string]string{}
	for _, variable := range plan.Configuration.Variables {
		switch {
		case variable.Generate > 0:
			value, err := generatedSecretValue(variable.Generate, variable.GenerateFormat)
			if err != nil {
				t.Fatal(err)
			}
			variables[variable.Name] = value
		default:
			variables[variable.Name] = variable.Value
		}
	}
	project := fmt.Sprintf("jd-e%d", stamp)
	runtime := plan.Configuration.Runtime
	var output strings.Builder
	startedRuntime, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{
		Run:     EngineRun{ID: stamp, EnvironmentID: stamp},
		Release: Release{ID: stamp, EnvironmentID: stamp, RunID: stamp, Number: 1},
		Snapshot: runtimeReleaseSnapshot{
			Version: 1, Plan: runtime, Compose: built.Compose,
		},
		SourceRoot: root, RuntimeVariables: variables, Host: "127.0.0.1",
	}, func(line BuildLog) error {
		output.WriteString(line.Text + "\n")
		return nil
	})
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if startedRuntime.Input.RuntimeID != "" {
			_, _ = owner.Stop(cleanup, runtimeFromInput(startedRuntime.Input), runtime, variables, true, nil)
		}
		label := "label=com.docker.compose.project=" + project
		if ids, err := liveDockerOutput(cleanup, "ps", "-aq", "--filter", label); err == nil && len(strings.Fields(string(ids))) > 0 {
			_, _ = liveDockerOutput(cleanup, append([]string{"rm", "--force", "--volumes"}, strings.Fields(string(ids))...)...)
		}
		if names, err := liveDockerOutput(cleanup, "volume", "ls", "-q", "--filter", label); err == nil && len(strings.Fields(string(names))) > 0 {
			_, _ = liveDockerOutput(cleanup, append([]string{"volume", "rm", "--force"}, strings.Fields(string(names))...)...)
		}
		if names, err := liveDockerOutput(cleanup, "network", "ls", "-q", "--filter", label); err == nil && len(strings.Fields(string(names))) > 0 {
			_, _ = liveDockerOutput(cleanup, append([]string{"network", "rm"}, strings.Fields(string(names))...)...)
		}
	})
	if err != nil {
		t.Fatalf("start the stack: %v\n--- compose output ---\n%s%s", err, output.String(), liveComposeTemplateLogs(ctx, project))
	}
	// The checks introduce themselves as the proxy does for the
	// release's domain, which is how an application that builds its
	// redirects from that domain is asked in production.
	target := startedRuntime.Target
	target.OriginalPorts = []int{runtime.InternalPort}
	for _, domain := range plan.Configuration.Domains {
		target.PublicURLs = append(target.PublicURLs, "https://"+domain.Hostname)
	}
	for _, check := range plan.Configuration.Checks {
		if check.Phase != "readiness" {
			continue
		}
		if evidence := checks.Run(ctx, check, target); evidence.Outcome != HealthPassed {
			t.Fatalf("%s: %+v\n%s", check.Name, evidence, liveComposeTemplateLogs(ctx, project))
		}
	}
	published, err := liveComposeTemplateHealth(ctx, project, definition)
	if err != nil {
		t.Fatalf("%v\n%s", err, liveComposeTemplateLogs(ctx, project))
	}
	services := make([]string, 0, len(definition.Services))
	for _, service := range definition.Services {
		services = append(services, service.Name)
	}
	t.Logf("%s → %s → every check passed, every service healthy, only %s published (%s, %d MB of limits)",
		definition.ID, strings.Join(services, ", "), published, time.Since(started).Round(time.Second), plan.Rendered.MemoryMB)
}

// liveComposeTemplateHealth waits for every service of the stack to report
// its own Docker healthcheck healthy, and confirms the isolation the template
// promised: the public service publishes one port and only on loopback, and
// no private service publishes anything or shares a network with the edge.
func liveComposeTemplateHealth(ctx context.Context, project string, definition *blueprint.Blueprint) (string, error) {
	public := definition.PublicService()
	deadline := time.Now().Add(10 * time.Minute)
	for {
		raw, err := liveDockerOutput(ctx, "ps", "-aq", "--filter", "label=com.docker.compose.project="+project)
		if err != nil {
			return "", err
		}
		ids := strings.Fields(string(raw))
		if len(ids) != len(definition.Services) {
			return "", fmt.Errorf("the stack runs %d containers, the template declares %d services", len(ids), len(definition.Services))
		}
		inspected, err := liveDockerOutput(ctx, append([]string{"inspect"}, ids...)...)
		if err != nil {
			return "", err
		}
		var containers []struct {
			Config struct {
				Labels map[string]string
			}
			State struct {
				Status string
				Health *struct{ Status string }
			}
			NetworkSettings struct {
				Ports    map[string][]struct{ HostIP, HostPort string }
				Networks map[string]any
			}
		}
		if err := json.Unmarshal(inspected, &containers); err != nil {
			return "", err
		}
		unhealthy, published := []string{}, ""
		for _, container := range containers {
			name := container.Config.Labels["com.docker.compose.service"]
			if container.State.Health == nil || container.State.Health.Status != "healthy" {
				status := container.State.Status
				if container.State.Health != nil {
					status += "/" + container.State.Health.Status
				}
				unhealthy = append(unhealthy, name+" "+status)
			}
			for port, bindings := range container.NetworkSettings.Ports {
				for _, binding := range bindings {
					if name != public.Name {
						return "", fmt.Errorf("private service %s publishes %s on %s:%s", name, port, binding.HostIP, binding.HostPort)
					}
					if binding.HostIP != "127.0.0.1" {
						return "", fmt.Errorf("public service %s publishes %s on %q, not loopback", name, port, binding.HostIP)
					}
					if published != "" {
						return "", fmt.Errorf("public service %s publishes more than one port", name)
					}
					published = fmt.Sprintf("%s:%s→%s", binding.HostIP, binding.HostPort, port)
				}
			}
			networks := make([]string, 0, len(container.NetworkSettings.Networks))
			for network := range container.NetworkSettings.Networks {
				networks = append(networks, network)
			}
			sort.Strings(networks)
			if name == public.Name {
				// The ingress joins the public container on the first of its
				// networks by name (proxysvc.publishedTarget): that has to be
				// the edge, so the proxy shares no network with the database.
				if len(networks) == 0 || networks[0] != project+"_default" {
					return "", fmt.Errorf("public service %s would be reached over %v, not the edge network", name, networks)
				}
			} else if slices.Contains(networks, project+"_default") {
				return "", fmt.Errorf("private service %s shares the edge network the ingress joins", name)
			}
		}
		if len(unhealthy) == 0 {
			if published == "" {
				return "", fmt.Errorf("public service %s publishes nothing", public.Name)
			}
			return published, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("services never became healthy: %s", strings.Join(unhealthy, ", "))
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// liveComposeTemplateImages removes, after the test, what the sweep pulled
// for a stack — each image by the digest it was pulled at — and nothing that
// was on the host before it. An image whose tag was not cached goes whole; a
// cached tag keeps its image and loses only the digest reference the pull
// added beside it, which the daemon lists as an untagged image of its own. A
// full sweep pulls gigabytes, and an image somebody else had cached is not
// this test's to delete.
func liveComposeTemplateImages(t *testing.T, resolved *ResolvedComposeSnapshot, cached, present map[string]bool) {
	t.Helper()
	for _, service := range resolved.Services {
		reference := service.Plan.Image
		if service.Source != "pull" || !contentDigestRE.MatchString(service.Digest) || present[service.Digest] {
			continue
		}
		remove := []string{"image", "rm", service.Reference + "@" + service.Digest}
		if !cached[reference] {
			remove = []string{"image", "rm", "--force", service.Reference + "@" + service.Digest}
		}
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_, _ = liveDockerOutput(cleanup, remove...)
		})
	}
}

// liveImageDigests is every digest the daemon lists an image under, tagged or
// not: what was on the host before a stack's images were resolved.
func liveImageDigests(ctx context.Context) map[string]bool {
	present := map[string]bool{}
	raw, err := liveDockerOutput(ctx, "image", "ls", "--all", "--digests", "--no-trunc", "--format", "{{.Digest}}")
	if err != nil {
		return present
	}
	for _, digest := range strings.Fields(string(raw)) {
		present[digest] = true
	}
	return present
}

func liveComposeTemplateLogs(ctx context.Context, project string) string {
	raw, err := liveDockerOutput(ctx, "ps", "-aq", "--filter", "label=com.docker.compose.project="+project)
	if err != nil {
		return ""
	}
	var logs strings.Builder
	for _, id := range strings.Fields(string(raw)) {
		name, _ := liveDockerOutput(ctx, "inspect", "--format", "{{index .Config.Labels \"com.docker.compose.service\"}} {{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}", id)
		tail, _ := liveDockerOutput(ctx, "logs", "--tail", "40", id)
		logs.WriteString("--- " + strings.TrimSpace(string(name)) + " ---\n" + string(tail))
	}
	return logs.String()
}
