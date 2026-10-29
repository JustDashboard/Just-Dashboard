package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/metrics"
)

// The volume-size history against a real daemon: a managed volume with 8 MiB
// written into it is measured by Docker's disk walk, recorded by the running
// recorder, and read back by the capacity route under its deployment. What
// the unit tests cannot hold is that the walk sizes a named volume at all,
// and that the sampler finds a plan's volume by the name Docker gives it.
//
// It creates one volume and one throwaway container of the cached
// alpine:3.20 (never pulled), and removes both.
func TestLiveDeploymentVolumeSizesAreSampled(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	b := sizingBench(t)
	b.s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	volume := fmt.Sprintf("jd-sizing-live-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", volume).Run() })
	run(t, "docker", "volume", "create", "--label", "io.just-dashboard.test=sizing", volume)
	run(t, "docker", "run", "--rm", "--pull=never", "-v", volume+":/v", "alpine:3.20",
		"dd", "if=/dev/zero", "of=/v/blob", "bs=1M", "count=8")
	image := deploy.DraftSourceConfig{Kind: deploy.SourceImage, Mode: deploy.SourceModeImageReference, Image: "nginx:1.27-alpine"}
	seedKeyDeployment(t, b.s, "live-sizing", image,
		`{"strategy":"stop_first","mounts":[{"source":"`+volume+`","target":"/data","ownership":"managed"}]}`, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	sizes, err := b.s.sampleDeploymentVolumes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sampled *metrics.VolumeSize
	for i := range sizes {
		if sizes[i].Name == volume {
			sampled = &sizes[i]
		}
	}
	if sampled == nil || sampled.Size < 8<<20 {
		t.Fatalf("the sampler measured %+v of %d volumes", sampled, len(sizes))
	}
	// Only the deployment's volume: the host's others are not sampled.
	if len(sizes) != 1 {
		t.Errorf("sampled %d volumes, want only the deployment's", len(sizes))
	}

	// The running recorder samples the volumes as it starts.
	recorder := metrics.New(b.s.Store, b.s.Log, time.Minute, 7*24*time.Hour).
		WithVolumes(metrics.VolumeSamplerFunc(b.s.sampleDeploymentVolumes))
	b.s.modules.metrics = recorder
	if err := recorder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	var recorded int64
	for time.Now().Before(deadline) {
		_ = b.s.Store.DB.QueryRow(`SELECT COALESCE(MAX(size_bytes), 0) FROM metric_volume_samples WHERE name = ?`, volume).Scan(&recorded)
		if recorded > 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	recorder.Stop()
	if recorded < 8<<20 {
		t.Fatalf("recorded %d bytes for %s", recorded, volume)
	}

	w := b.inSession(http.MethodGet, "/api/v1/system/metrics/capacity", nil)
	var forecast capacityForecast
	if err := json.Unmarshal(w.Body.Bytes(), &forecast); err != nil || w.Code != http.StatusOK {
		t.Fatalf("capacity = %d %s", w.Code, w.Body.String())
	}
	if len(forecast.Apps) != 1 || forecast.Apps[0].ProjectName != "live-sizing" || len(forecast.Apps[0].Volumes) != 1 {
		t.Fatalf("apps = %+v", forecast.Apps)
	}
	read := forecast.Apps[0].Volumes[0]
	if read.Name != volume || !read.Measured || read.SizeBytes < 8<<20 || read.Status != metrics.ForecastInsufficient {
		t.Errorf("volume = %+v", read)
	}
	if forecast.VolumeIntervalSeconds != int(metrics.VolumeInterval/time.Second) {
		t.Errorf("volume interval = %d", forecast.VolumeIntervalSeconds)
	}
}

// Throttling against a real daemon: a busy loop held to a tenth of a core is
// throttled in most of its scheduler periods, and the recorder keeps the
// counters that say so and right-sizing reads the share back. What the unit
// tests cannot hold is that Docker reports the CFS counters at all on this
// host's cgroup version, and under the container id the release records.
//
// It runs one container of the cached alpine:3.20 (never pulled) and removes
// it.
func TestLiveThrottlingIsRecordedForACPULimitedContainer(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host")
	}
	b := sizingBench(t)
	docker := dockerx.New("unix:///var/run/docker.sock")
	name := fmt.Sprintf("jd-sizing-throttle-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	out, err := exec.Command("docker", "run", "-d", "--pull=never", "--name", name, "--cpus", "0.1",
		"--label", "io.just-dashboard.test=sizing", "alpine:3.20", "sh", "-c", "while :; do :; done").Output()
	if err != nil {
		t.Fatalf("docker run: %v", err)
	}
	id := string(out[:len(out)-1])

	recorder := metrics.New(b.s.Store, b.s.Log, metrics.MinInterval, 7*24*time.Hour).WithContainers(docker.NewStatsSampler())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := recorder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	var share *float64
	for time.Now().Before(deadline) && share == nil {
		time.Sleep(time.Second)
		usage, err := recorder.SizingUsage(ctx, []string{id}, time.Now().Add(-time.Hour), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		share = usage.ThrottledShare([]string{id})
	}
	recorder.Stop()
	if share == nil || *share < 0.5 {
		t.Fatalf("throttled share of a busy loop held to a tenth of a core = %v", share)
	}
	usage, err := recorder.SizingUsage(context.Background(), []string{id}, time.Now().Add(-time.Hour), time.Now())
	if err != nil || usage.CPUPeak <= 0 || usage.CPUPeak > 0.2 {
		t.Errorf("CPU peak of a container held to 0.1 cores = %+v %v", usage, err)
	}
}
