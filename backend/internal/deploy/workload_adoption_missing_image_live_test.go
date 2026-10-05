package deploy

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

// This isolates raw export/import behavior; the managed container test separately
// exercises the production cached recovery path through adoption and rollback.
// It deletes only a unique fixture image and exports only its owned container.
func TestLiveDeletedImageExportDoesNotChangeOriginalRuntime(t *testing.T) {
	if os.Getenv("JD_DOCKER_ADOPTION_LIVE") != "1" {
		t.Skip("set JD_DOCKER_ADOPTION_LIVE=1 for the isolated deleted-image experiment")
	}
	ctx := t.Context()
	client := liveC4Docker(t)
	if _, err := client.InspectImage(ctx, "caddy:2-alpine"); err != nil {
		t.Skip("fixture requires locally available caddy:2-alpine")
	}
	root := t.TempDir()
	name := fmt.Sprintf("jd-export-proof-%d", time.Now().UnixNano())
	tag := name + ":original"
	importedTag := name + ":recovered"
	port := liveC5LoopbackPort(t)
	if err := os.Mkdir(filepath.Join(root, "public"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{"public/index.html": "adoption-ready", "Caddyfile": "{\n admin off\n auto_https off\n}\n:8080 {\n root * /srv\n file_server\n}\n", "Dockerfile": "FROM caddy:2-alpine\nLABEL fixture.deleted-image=\"" + name + "\"\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	docker := func(args ...string) []byte {
		t.Helper()
		output, err := liveDockerOutput(ctx, args...)
		if err != nil {
			t.Fatal(err)
		}
		return output
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = liveDockerOutput(cleanupCtx, "rm", "--force", name, name+"-replay")
		for _, v := range []string{name + "-config", name + "-data", name + "-persistent"} {
			_, _ = liveDockerOutput(cleanupCtx, "volume", "rm", v)
		}
		_, _ = liveDockerOutput(cleanupCtx, "image", "rm", "--force", tag, importedTag)
	})
	docker("build", "--pull=false", "--tag", tag, root)
	id := strings.TrimSpace(string(docker("run", "--detach", "--name", name, "--publish", "127.0.0.1:"+strconv.Itoa(port)+":8080", "--volume", filepath.Join(root, "public")+":/srv:ro", "--volume", filepath.Join(root, "Caddyfile")+":/etc/caddy/Caddyfile:ro", "--volume", name+"-config:/config", "--volume", name+"-data:/data", "--volume", name+"-persistent:/persistent", "--env", "API_TOKEN=do-not-bake-captured-environment", tag)))
	adoptionLiveHTTP(t, port)
	docker("exec", id, "/bin/sh", "-c", "printf original-volume-only > /persistent/sentinel")
	original, err := client.CaptureAdoptionContainer(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if safe, _ := recoverableWritableLayer(original); !safe {
		t.Fatal("owned export fixture has meaningful writable-layer changes")
	}
	platform := original.Image.OS + "/" + original.Image.Architecture
	t.Logf("owned image platform=%s inspect manifest platform available=%v", platform, original.Inspection.ImageManifestDescriptor != nil && original.Inspection.ImageManifestDescriptor.Platform != nil)
	// Containerd's image store refuses removal while a container runs. The
	// owned fixture is stopped only during setup to create the missing-image
	// condition, then resumed before the read-only capture proof begins.
	docker("stop", "--time", "2", id)
	docker("image", "rm", "--force", original.Inspection.Image)
	docker("start", id)
	adoptionLiveHTTP(t, port)
	var prepared []container.InspectResponse
	if err := json.Unmarshal(docker("inspect", id), &prepared); err != nil || len(prepared) != 1 {
		t.Fatal("fixture inspection unavailable", err)
	}
	original.Inspection = prepared[0]
	if _, err := client.InspectImage(ctx, original.Inspection.Image); err == nil {
		t.Fatal("owned image still exists; deleted-image experiment is invalid")
	}
	var samples, failures atomic.Int64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		probe := &http.Client{Timeout: time.Second}
		for {
			select {
			case <-stop:
				return
			default:
			}
			response, err := probe.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
			if err != nil {
				failures.Add(1)
			} else {
				body, _ := io.ReadAll(response.Body)
				response.Body.Close()
				if response.StatusCode != 200 || string(body) != "adoption-ready" {
					failures.Add(1)
				}
			}
			samples.Add(1)
			time.Sleep(10 * time.Millisecond)
		}
	}()
	stopped := false
	defer func() {
		if !stopped {
			close(stop)
			<-done
		}
	}()
	archive := filepath.Join(root, "rootfs.tar")
	docker("export", "--output", archive, id)
	close(stop)
	<-done
	stopped = true
	if failures.Load() != 0 || samples.Load() == 0 {
		t.Fatalf("export interrupted traffic: samples=%d failures=%d", samples.Load(), failures.Load())
	}
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(file)
	entries := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries++
		if strings.TrimPrefix(header.Name, "/") == "persistent/sentinel" {
			t.Fatal("Docker export unexpectedly included mounted volume data")
		}
	}
	file.Close()
	if entries == 0 {
		t.Fatal("empty exported archive")
	}
	var current []container.InspectResponse
	if err := json.Unmarshal(docker("inspect", id), &current); err != nil || len(current) != 1 {
		t.Fatal("original inspection unavailable", err)
	}
	if current[0].ID != original.Inspection.ID || current[0].State.Pid != original.Inspection.State.Pid || current[0].State.StartedAt != original.Inspection.State.StartedAt || !current[0].State.Running || current[0].State.Paused || string(mustJSON(current[0].Config)) != string(mustJSON(original.Inspection.Config)) || string(mustJSON(current[0].HostConfig)) != string(mustJSON(original.Inspection.HostConfig)) {
		t.Fatal("export changed original runtime")
	}
	importedID := strings.TrimSpace(string(docker("import", "--platform", platform, archive, importedTag)))
	imported, err := client.InspectImage(ctx, importedID)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Env) != 0 || len(imported.Labels) != 0 || len(imported.Entrypoint) != 0 || len(imported.Command) != 0 {
		t.Fatal("import copied runtime environment/configuration into the image")
	}
	probePort := liveC5LoopbackPort(t)
	docker("run", "--detach", "--name", name+"-replay", "--publish", "127.0.0.1:"+strconv.Itoa(probePort)+":8080", "--volume", filepath.Join(root, "public")+":/srv:ro", "--volume", filepath.Join(root, "Caddyfile")+":/etc/caddy/Caddyfile:ro", "--volume", name+"-config:/config", "--volume", name+"-data:/data", "--volume", name+"-persistent:/persistent", "--env", "API_TOKEN=do-not-bake-captured-environment", "--entrypoint", "/usr/bin/caddy", importedID, "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")
	adoptionLiveHTTP(t, probePort)
	if got := string(docker("exec", name+"-replay", "/bin/cat", "/persistent/sentinel")); got != "original-volume-only" {
		t.Fatal("imported rootfs did not reuse original volume data")
	}
	adoptionLiveHTTP(t, port)
	evidence := map[string]any{"test": t.Name(), "fixtureName": name, "checkedAt": time.Now().UTC(), "deletedFixtureImage": true, "originalIDPIDStartedAtConfigUnchanged": true, "originalRemainedRunningAndUnpaused": true, "HTTPExportSamples": samples.Load(), "HTTPExportFailures": failures.Load(), "mountedVolumeExcludedFromExport": true, "capturedEnvironmentAbsentFromImportedImageConfig": true, "importedFilesystemRunsSameHTTPApplication": true, "persistentDataReused": true, "platform": platform, "rawExportImportCapabilityExperiment": true}
	if location := os.Getenv("JD_ADOPTION_EVIDENCE_DIR"); location != "" {
		if err := os.MkdirAll(location, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(location, name+".json"), append(mustJSON(evidence), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("sanitized deleted-image experiment evidence: %s", mustJSON(evidence))
}
