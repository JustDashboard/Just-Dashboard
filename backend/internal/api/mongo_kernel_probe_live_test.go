package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// probeMongoKernelVariants is a temporary CI probe: it starts MongoDB images
// with and without GLIBC_TUNABLES on the runner's kernel and logs which of
// them reach "Waiting for connections". It never fails.
func probeMongoKernelVariants(t *testing.T) {
	docker := dockerx.New("unix:///var/run/docker.sock")
	t.Logf("probe: kernel %s", hostKernelRelease())
	type variant struct{ image, tunables string }
	for i, v := range []variant{
		{"mongo:8.0", ""},
		{"mongo:8.0", "glibc.pthread.rseq=0"},
		{"mongo:8.0", "glibc.pthread.rseq=1"},
		{"mongo:7.0", ""},
		{"mongo:6.0", ""},
	} {
		name := fmt.Sprintf("jd-redesign-kprobe-%d-%d", i, time.Now().UnixNano())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		env := []dockerx.EnvVar{
			{Name: "MONGO_INITDB_ROOT_USERNAME", Value: "jd"},
			{Name: "MONGO_INITDB_ROOT_PASSWORD", Value: "probe-password-1"},
			{Name: "MONGO_INITDB_DATABASE", Value: "app"},
		}
		if v.tunables != "" {
			env = append(env, dockerx.EnvVar{Name: "GLIBC_TUNABLES", Value: v.tunables})
		}
		_, err := docker.Create(ctx, dockerx.ContainerSpec{Name: name, Image: v.image, Start: true, Env: env}, nil)
		if err != nil {
			t.Logf("probe %s tunables=%q: create failed: %v", v.image, v.tunables, err)
			cancel()
			continue
		}
		verdict := "timeout"
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(2 * time.Second)
			tail, _ := fixtureLogTail(docker, name, 400)
			joined := strings.Join(tail, "\n")
			detail, _ := docker.Inspect(ctx, name)
			if strings.Contains(joined, "MongoDB cannot start") {
				verdict = "refused"
				break
			}
			// The image's entrypoint runs a temporary server first; the real
			// one is the second "Waiting for connections" after "Shutdown".
			if strings.Count(joined, `"Waiting for connections"`) >= 2 {
				verdict = "started"
				break
			}
			if detail != nil && detail.State != "running" {
				verdict = "exited " + detail.State
				break
			}
		}
		version := ""
		tail, _ := fixtureLogTail(docker, name, 400)
		for _, line := range tail {
			if strings.Contains(line, `"Build Info"`) {
				version = clip(line, 200)
				break
			}
		}
		detail, _ := docker.Inspect(ctx, name)
		state := ""
		if detail != nil {
			state = fmt.Sprintf("state %q exit %d restarts %d", detail.State, detail.ExitCode, detail.RestartNum)
		}
		last := ""
		if len(tail) > 0 {
			last = clip(tail[len(tail)-1], 400)
		}
		t.Logf("probe %s tunables=%q: %s (%s); build %s; last line %s", v.image, v.tunables, verdict, state, version, last)
		for _, line := range tail {
			if strings.Contains(line, `"s":"F"`) || strings.Contains(line, `"s":"E"`) || strings.Contains(line, "rseq") || strings.Contains(line, "tcmalloc") {
				t.Logf("probe %s tunables=%q: %s", v.image, v.tunables, clip(line, 400))
			}
		}
		_ = docker.RemoveContainer(context.Background(), name, true, true)
		cancel()
	}
}
