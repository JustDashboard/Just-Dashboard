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
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/errdefs"
)

// livePreviewComposeIsolation previews a two-service Compose release whose
// file asks for production's own name, port, volume, a host directory it
// writes, a host file, a secret and production's network, beside a
// production container holding all of it. Every service runs on the
// preview's bridge alone with storage of its own; only the public service
// answers, on the loopback port the preview's route reaches; and cleanup
// leaves exactly production.
func livePreviewComposeIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	client := liveC4Docker(t)
	if !client.ComposeAvailable(ctx) {
		t.Skip("docker compose is unavailable")
	}
	backend := NewDockerArtifactBackend(client)
	resolved, err := backend.ResolveImage(ctx, "caddy:2-alpine", "")
	if err != nil {
		t.Fatal(err)
	}
	image, err := backend.PullImage(ctx, resolved.Reference+"@"+resolved.Digest, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	environmentID := time.Now().UnixNano()
	stamp := strconv.FormatInt(environmentID, 10)

	// Production: a volume, a host directory and a network of its own, and a
	// container on them under the name the Compose file asks for.
	productionVolume := "jd-preview-test-compose-production-" + stamp
	if _, err := client.CreateVolume(ctx, dockerx.VolumeSpec{Name: productionVolume}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.RemoveVolume(context.Background(), productionVolume, false) })
	productionNetwork := "jd-preview-test-compose-production-" + stamp
	if _, err := client.CreateNetwork(ctx, dockerx.NetworkSpec{Name: productionNetwork}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if network, err := client.InspectNetwork(context.Background(), productionNetwork); err == nil {
			_ = client.RemoveNetwork(context.Background(), network.ID)
		}
	})
	hostDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(hostDirectory, "production.txt"), []byte("production-cache"), 0o644); err != nil {
		t.Fatal(err)
	}
	productionPort := liveC5LoopbackPort(t)
	productionName := "jd-preview-test-compose-web-" + stamp
	production, err := client.Create(ctx, dockerx.ContainerSpec{
		Name: productionName, Image: immutableRuntimeImage(image),
		Command:  []string{"sh", "-c", "printf 'production-sentinel' > /data/index.html; exec caddy file-server --listen :8080 --root /data"},
		Env:      []dockerx.EnvVar{{Name: "DATABASE_URL", Value: "postgres://production:secret@production/db"}},
		Mounts:   []dockerx.MountSpec{{Type: "volume", Source: productionVolume, Target: "/data"}},
		Ports:    []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: productionPort, ContainerPort: 8080, Protocol: "tcp"}},
		Networks: []string{productionNetwork},
		Start:    true,
	}, nil)
	if production != nil {
		t.Cleanup(func() { _ = client.RemoveContainer(context.Background(), production.ID, true, false) })
	}
	if err != nil {
		t.Fatal(err)
	}

	// The pull request's stack. Every line naming production is something
	// the preview has to be kept from.
	source := t.TempDir()
	apiPort := liveC5LoopbackPort(t)
	files := map[string]string{
		"site.txt":  "from-the-commit",
		"token.txt": "production-token",
		"compose.yml": fmt.Sprintf(`services:
  web:
    image: caddy:2-alpine
    container_name: %[1]s
    command: ["caddy", "file-server", "--listen", ":8080", "--root", "/data"]
    environment:
      DATABASE_URL: ${DATABASE_URL:-}
    ports: ["127.0.0.1:%[2]d:8080"]
    networks: [front, production]
    secrets: [token]
    volumes:
      - data:/data
      - ./site.txt:/srv/site.txt:ro
      - /etc/hostname:/host/hostname:ro
  api:
    image: caddy:2-alpine
    command: ["caddy", "respond", "--listen", ":9090", "api-answer"]
    ports: ["127.0.0.1:%[3]d:9090"]
    networks:
      front:
        aliases: [api.internal]
    volumes:
      - %[4]s:/cache
volumes:
  data:
    external: true
    name: %[5]s
networks:
  front: {}
  production:
    external: true
    name: %[6]s
secrets:
  token:
    file: ./token.txt
`, productionName, productionPort, apiPort, hostDirectory, productionVolume, productionNetwork),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	owner := NewDockerRuntimeOwner(client)
	plan := RuntimePlanConfig{PreviewIsolation: true, InternalPort: 8080, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst}
	services := []ResolvedComposeService{}
	for _, name := range []string{"web", "api"} {
		services = append(services, ResolvedComposeService{
			Plan: ComposeServicePlan{Name: name}, Reference: image.Reference, Digest: image.Digest, ConfigDigest: image.ConfigDigest,
		})
	}
	run := EngineRun{ID: environmentID, EnvironmentID: environmentID}
	release := Release{ID: environmentID, EnvironmentID: environmentID, RunID: run.ID, Number: 1}
	port := liveC5LoopbackPort(t)
	var transcript []string
	preview, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{
		Run: run, Release: release, SourceRoot: source,
		Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Compose: &ResolvedComposeSnapshot{
			Files: []string{"compose.yml"}, Services: services, PrimaryService: "web",
		}},
		Host: "127.0.0.1", Port: port, RuntimeVariables: map[string]string{"PREVIEW": "true"},
	}, func(line BuildLog) error {
		transcript = append(transcript, line.Text)
		return nil
	})
	t.Cleanup(func() { _ = owner.RemovePreviewResources(context.Background(), environmentID) })
	if err != nil {
		t.Fatal(err)
	}
	if preview.Input.Kind != "compose" || preview.Input.Host != "127.0.0.1" || preview.Input.Port != port {
		t.Fatalf("preview runtime = %+v, want the public service on 127.0.0.1:%d", preview.Input, port)
	}
	if !strings.Contains(strings.Join(transcript, "\n"), "Left out /host/hostname of service web") {
		t.Fatalf("the transcript does not say what was left out: %q", transcript)
	}

	containers, err := client.ListContainersWithLabels(ctx, previewLabels(environmentID))
	if err != nil {
		t.Fatal(err)
	}
	byService := map[string]dockerx.Container{}
	for _, container := range containers {
		byService[container.ComposeSvc] = container
	}
	if len(containers) != 2 || byService["web"].ID == "" || byService["api"].ID == "" {
		t.Fatalf("preview services = %+v", containers)
	}
	for name, container := range byService {
		if container.Name == productionName || strings.TrimPrefix(container.Name, "/") == productionName {
			t.Fatalf("service %s took production's container name", name)
		}
		raw, err := liveDockerOutput(ctx, "inspect", "--format", "{{json .NetworkSettings.Networks}}|{{json .HostConfig.PortBindings}}", container.ID)
		if err != nil {
			t.Fatal(err)
		}
		networkJSON, bindingJSON, _ := strings.Cut(strings.TrimSpace(string(raw)), "|")
		var networks map[string]json.RawMessage
		if err := json.Unmarshal([]byte(networkJSON), &networks); err != nil {
			t.Fatal(err)
		}
		attached := make([]string, 0, len(networks))
		for network := range networks {
			attached = append(attached, network)
		}
		sort.Strings(attached)
		if len(attached) != 1 || attached[0] != previewNetworkName(environmentID) {
			t.Fatalf("service %s is attached to %v, want the preview's bridge alone", name, attached)
		}
		var bindings map[string][]struct{ HostIp, HostPort string }
		_ = json.Unmarshal([]byte(bindingJSON), &bindings)
		switch name {
		case "web":
			if got := bindings["8080/tcp"]; len(bindings) != 1 || len(got) != 1 || got[0].HostIp != "127.0.0.1" || got[0].HostPort != strconv.Itoa(port) {
				t.Fatalf("public service publishes %s, want 127.0.0.1:%d alone", bindingJSON, port)
			}
		default:
			if len(bindings) != 0 {
				t.Fatalf("private service %s publishes %s", name, bindingJSON)
			}
		}
	}

	web, api := byService["web"].ID, byService["api"].ID
	check := func(id string, argv ...string) int {
		t.Helper()
		// A command that ran and exited non-zero is an answer; one that
		// could not run is a broken fixture.
		code, _, err := client.ExecCheck(ctx, id, argv, 6*time.Second)
		if err != nil && code <= 0 {
			t.Fatalf("%v in %s: %v", argv, id, err)
		}
		return code
	}
	for _, argv := range [][]string{
		{"test", "!", "-e", "/data/index.html"},
		{"sh", "-c", `test -z "$DATABASE_URL"`},
		{"test", "!", "-e", "/run/secrets/token"},
		{"test", "!", "-e", "/host/hostname"},
		{"sh", "-c", "test \"$(cat /srv/site.txt)\" = from-the-commit"},
	} {
		if code := check(web, argv...); code != 0 {
			t.Fatalf("web: %v exited %d: the preview reached a production input", argv, code)
		}
	}
	if code := check(web, "sh", "-c", "printf x > /srv/site.txt"); code == 0 {
		t.Fatal("the commit's file was writable from the preview")
	}
	if code := check(api, "test", "!", "-e", "/cache/production.txt"); code != 0 {
		t.Fatal("the private service could read production's host directory")
	}
	if code := check(api, "sh", "-c", "printf preview > /cache/preview.txt"); code != 0 {
		t.Fatal("the private service's own storage is not writable")
	}
	if code := check(web, "sh", "-c", "printf preview-only > /data/index.html"); code != 0 {
		t.Fatal("the public service's own storage is not writable")
	}
	// The services still find each other, by name and by the alias the file
	// gave, on the preview's bridge.
	for _, address := range []string{"http://api:9090/", "http://api.internal:9090/"} {
		if code := check(web, "wget", "-q", "-T", "3", "-O", "/dev/null", address); code != 0 {
			t.Fatalf("web could not reach %s on the preview's bridge", address)
		}
	}

	read := func(port int) (string, error) {
		httpClient := &http.Client{Timeout: 3 * time.Second}
		response, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
		return string(body), err
	}
	if got, err := read(port); err != nil || got != "preview-only" {
		t.Fatalf("the preview's route reads %q, %v", got, err)
	}
	if got, err := read(apiPort); err == nil {
		t.Fatalf("the private service answered on the port its file publishes: %q", got)
	}
	if got, err := read(productionPort); err != nil || got != "production-sentinel" {
		t.Fatalf("production changed: %q, %v", got, err)
	}
	address, err := liveDockerOutput(ctx, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", production.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range strings.Fields(string(address)) {
		for _, id := range []string{web, api} {
			if code := check(id, "wget", "-q", "-T", "2", "-O", "/dev/null", "http://"+ip+":8080/"); code == 0 {
				t.Fatalf("a preview service reached production at %s", ip)
			}
		}
	}

	if err := owner.RemovePreviewResources(ctx, environmentID); err != nil {
		t.Fatal(err)
	}
	left, err := client.ListContainersWithLabels(ctx, map[string]string{"io.just-dashboard.environment-id": stamp})
	if err != nil || len(left) != 0 {
		t.Fatalf("cleanup left containers %+v, %v", left, err)
	}
	volumes, err := client.ListVolumes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, volume := range volumes {
		if strings.HasPrefix(volume.Name, fmt.Sprintf("jd-preview-e%d-", environmentID)) || volume.Labels["com.docker.compose.project"] == fmt.Sprintf("jd-e%d", environmentID) {
			t.Fatalf("cleanup left volume %s", volume.Name)
		}
	}
	if _, err := client.InspectNetwork(ctx, previewNetworkName(environmentID)); !errdefs.IsNotFound(err) {
		t.Fatalf("the preview network survived cleanup: %v", err)
	}
	if out, err := liveDockerOutput(ctx, "network", "ls", "--quiet", "--filter", "label=com.docker.compose.project="+fmt.Sprintf("jd-e%d", environmentID)); err != nil || strings.TrimSpace(string(out)) != "" {
		t.Fatalf("Compose left a network of the preview's project: %q, %v", out, err)
	}
	if _, err := client.InspectVolume(ctx, productionVolume); err != nil {
		t.Fatalf("cleanup removed production's volume: %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(hostDirectory, "production.txt")); err != nil || string(content) != "production-cache" {
		t.Fatalf("production's host directory changed: %q, %v", content, err)
	}
	if entries, _ := os.ReadDir(hostDirectory); len(entries) != 1 {
		t.Fatalf("the preview wrote into production's host directory: %v", entries)
	}
	if got, err := read(productionPort); err != nil || got != "production-sentinel" {
		t.Fatalf("cleanup changed production: %q, %v", got, err)
	}
}
