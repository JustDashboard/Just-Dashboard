package dockerx

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Opt-in acceptance owns and removes only its temporary fixture. It never
// changes a container selected from the operator's existing workload.
func TestLiveAdvisorRestartPolicyPreservesRunningContainer(t *testing.T) {
	if os.Getenv("JD_ADVISOR_DOCKER_LIVE") != "1" {
		t.Skip("set JD_ADVISOR_DOCKER_LIVE=1; requires locally available alpine:3.20")
	}
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	c := New(host)
	cli, err := c.api()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	created, err := cli.ContainerCreate(ctx, &container.Config{Image: "alpine:3.20", Cmd: []string{"sleep", "120"}, NetworkDisabled: true, Labels: map[string]string{"jd.test": "server-advisor"}}, &container.HostConfig{}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := cli.ContainerRemove(cleanup, created.ID, container.RemoveOptions{Force: true}); err != nil {
			t.Error(err)
		}
	})
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateRestartPolicy(ctx, created.ID, "unless-stopped", 0); err != nil {
		t.Fatal(err)
	}
	after, err := cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.State.Running || before.State.Pid != after.State.Pid || before.State.StartedAt != after.State.StartedAt || string(after.HostConfig.RestartPolicy.Name) != "unless-stopped" {
		t.Fatalf("policy replaced or interrupted the process: before=%+v after=%+v", before.State, after.State)
	}
	if _, err := c.UpdateResources(ctx, created.ID, ResourceLimits{MemoryMB: 64, CPUs: 0.5}); err != nil {
		t.Fatal(err)
	}
	limited, err := cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if limited.HostConfig.Memory != 64<<20 || limited.HostConfig.NanoCPUs != 500000000 || limited.State.Pid != before.State.Pid {
		t.Fatal("resource update did not preserve the process and selected limits")
	}
	t.Logf("fixture %s retained PID %d and start time while restart policy, memory and CPU limits changed", created.ID[:12], after.State.Pid)
}

func TestLiveAdvisorReviewedHealthAndLogRemedyRecreatesOnlyOwnedFixture(t *testing.T) {
	if os.Getenv("JD_ADVISOR_DOCKER_LIVE") != "1" {
		t.Skip("set JD_ADVISOR_DOCKER_LIVE=1")
	}
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	c := New(host)
	cli, err := c.api()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Second)
	defer cancel()
	owned := []string{}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		for _, id := range owned {
			_, err := cli.ContainerInspect(cleanup, id)
			if err == nil {
				if err := cli.ContainerRemove(cleanup, id, container.RemoveOptions{Force: true}); err != nil {
					t.Error(err)
				}
			}
		}
	})
	original, err := cli.ContainerCreate(ctx, &container.Config{Image: "alpine:3.20", Cmd: []string{"sleep", "120"}, Env: []string{"ADVISOR_FIXTURE=preserved"}}, &container.HostConfig{NetworkMode: "none"}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	owned = append(owned, original.ID)
	if err := cli.ContainerStart(ctx, original.ID, container.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	spec, err := c.SpecOf(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := cli.ContainerInspect(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	spec.Image = before.Image
	spec.Health = &HealthSpec{Test: []string{"CMD", "true"}, IntervalSec: 1, TimeoutSec: 1, Retries: 2}
	spec.Logging = CappedLogging()
	result, err := c.Recreate(ctx, original.ID, RecreateOptions{Spec: spec}, nil)
	if result != nil {
		owned = append(owned, result.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
	after, err := cli.ContainerInspect(ctx, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID == original.ID || !after.State.Running || after.Image != before.Image || len(after.Config.Healthcheck.Test) != 2 || after.Config.Healthcheck.Test[1] != "true" || after.HostConfig.LogConfig.Config["max-size"] != "10m" || after.HostConfig.NetworkMode != "none" {
		t.Fatalf("reviewed settings not applied: %+v", after)
	}
	preserved := false
	for _, env := range after.Config.Env {
		if env == "ADVISOR_FIXTURE=preserved" {
			preserved = true
		}
	}
	if !preserved {
		t.Fatal("replacement lost fixture environment")
	}
	if _, err := cli.ContainerInspect(ctx, original.ID); err == nil {
		t.Fatal("original remained unexpectedly")
	}
	t.Logf("reviewed fixture replaced %s with %s; image identity, environment and network owner preserved", original.ID[:12], result.ID[:12])
}
