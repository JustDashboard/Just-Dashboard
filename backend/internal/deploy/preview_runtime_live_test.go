package deploy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/docker/docker/errdefs"
)

func TestLivePreviewStorageCredentialsNetworkAndCleanup(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to exercise isolated Docker preview resources")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	client := liveC4Docker(t)
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
	productionVolume := fmt.Sprintf("jd-preview-test-production-%d", environmentID)
	if _, err := client.CreateVolume(ctx, dockerx.VolumeSpec{Name: productionVolume}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.RemoveVolume(context.Background(), productionVolume, false) })
	production, err := client.Create(ctx, dockerx.ContainerSpec{
		Name: productionVolume, Image: immutableRuntimeImage(image),
		Command: []string{"sh", "-c", "printf 'production-sentinel' > /data/index.html; exec caddy file-server --listen :8080 --root /data"},
		Env:     []dockerx.EnvVar{{Name: "DATABASE_URL", Value: "postgres://production:secret@production/db"}},
		Mounts:  []dockerx.MountSpec{{Type: "volume", Source: productionVolume, Target: "/data"}},
		Ports:   []dockerx.PortMapping{{HostIP: "127.0.0.1", HostPort: liveC5LoopbackPort(t), ContainerPort: 8080, Protocol: "tcp"}},
		Start:   true,
	}, nil)
	if production != nil {
		t.Cleanup(func() { _ = client.RemoveContainer(context.Background(), production.ID, true, false) })
	}
	if err != nil {
		t.Fatal(err)
	}
	owner := NewDockerRuntimeOwner(client)
	plan := RuntimePlanConfig{PreviewIsolation: true, InternalPort: 8080, BindAddress: "127.0.0.1", Strategy: StrategyStopFirst,
		Command: []string{"caddy", "file-server", "--listen", ":8080", "--root", "/data"},
		Mounts:  []RuntimeMount{{Source: previewVolumeName(environmentID, "/data"), Target: "/data", Ownership: OwnershipManaged}},
	}
	run := EngineRun{ID: environmentID, EnvironmentID: environmentID}
	release := Release{ID: environmentID, EnvironmentID: environmentID, RunID: run.ID, Number: 1}
	preview, err := owner.StartCandidate(ctx, CandidateRuntimeRequest{
		Run: run, Release: release, Snapshot: runtimeReleaseSnapshot{Version: 1, Plan: plan, Image: image},
		Host: "127.0.0.1", Port: liveC5LoopbackPort(t), RuntimeVariables: map[string]string{"PREVIEW": "true"},
	}, nil)
	t.Cleanup(func() { _ = owner.RemovePreviewResources(context.Background(), environmentID) })
	if err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"test", "!", "-f", "/data/index.html"}, {"sh", "-c", "test -z \"$DATABASE_URL\""}} {
		code, _, err := client.ExecCheck(ctx, preview.Input.RuntimeID, argv, 5*time.Second)
		if err != nil || code != 0 {
			t.Fatalf("preview could read a production input: exit=%d, %v", code, err)
		}
	}
	if code, _, err := client.ExecCheck(ctx, preview.Input.RuntimeID, []string{"sh", "-c", "printf preview-only > /data/index.html"}, 5*time.Second); err != nil || code != 0 {
		t.Fatalf("preview storage is not usable: %d, %v", code, err)
	}
	read := func(port int) string {
		t.Helper()
		httpClient := &http.Client{Timeout: 5 * time.Second}
		response, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1024))
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("application response: %d, %v", response.StatusCode, err)
		}
		return string(body)
	}
	if got := read(preview.Input.Port); got != "preview-only" {
		t.Fatalf("preview content = %q", got)
	}
	var productionPort int
	for _, port := range production.Ports {
		if port.ContainerPort == 8080 {
			productionPort = port.HostPort
		}
	}
	if got := read(productionPort); got != "production-sentinel" {
		t.Fatalf("production content changed: %q", got)
	}
	network, err := client.InspectNetwork(ctx, previewNetworkName(environmentID))
	if err != nil || network.Driver != "bridge" || !matchesPreviewLabels(network.Labels, environmentID) {
		t.Fatalf("preview network isolation missing: %v", err)
	}
	// The production bridge is a different network. The preview must not be
	// able to read its service even though both containers share this daemon.
	address, err := liveDockerOutput(ctx, "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", production.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := client.ExecCheck(ctx, preview.Input.RuntimeID, []string{"wget", "-q", "-T", "2", "-O", "/dev/null", "http://" + strings.TrimSpace(string(address)) + ":8080/"}, 4*time.Second)
	if err == nil && code == 0 {
		t.Fatal("preview reached the production network")
	}
	if err := owner.RemovePreviewResources(ctx, environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.InspectVolume(ctx, plan.Mounts[0].Source); !errdefs.IsNotFound(err) {
		t.Fatalf("preview volume survived cleanup: %v", err)
	}
	if _, err := client.InspectVolume(ctx, productionVolume); err != nil {
		t.Fatalf("cleanup removed production volume: %v", err)
	}
	if got := read(productionPort); got != "production-sentinel" {
		t.Fatalf("cleanup changed production: %q", got)
	}
}
