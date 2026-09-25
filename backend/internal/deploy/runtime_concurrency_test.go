package deploy

import "testing"

func TestWebConcurrencyIsSizedToTheContainer(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		plan RuntimePlanConfig
		cpus int
		want int
	}{
		{RuntimePlanConfig{}, 8, 2},
		{RuntimePlanConfig{MemoryMB: 4096}, 8, 16},
		{RuntimePlanConfig{MemoryMB: 4096, CPUs: 1}, 8, 3},
		{RuntimePlanConfig{MemoryMB: 512}, 8, 2},
		{RuntimePlanConfig{MemoryMB: 256}, 8, 1},
		{RuntimePlanConfig{MemoryMB: 128}, 2, 1},
	} {
		if got := webConcurrency(fixture.plan, fixture.cpus); got != fixture.want {
			t.Fatalf("%+v on %d CPUs: %d, want %d", fixture.plan, fixture.cpus, got, fixture.want)
		}
	}
	snapshot := runtimeReleaseSnapshot{WebConcurrency: true, Plan: RuntimePlanConfig{MemoryMB: 1024, CPUs: 1}}
	if env := webConcurrencyEnvironment(snapshot, map[string]string{}); len(env) != 1 || env[0].Name != "WEB_CONCURRENCY" || env[0].Value != "3" {
		t.Fatalf("env = %+v", env)
	}
	if env := webConcurrencyEnvironment(snapshot, map[string]string{"WEB_CONCURRENCY": "8"}); len(env) != 0 {
		t.Fatalf("an explicit variable wins: %+v", env)
	}
	if env := webConcurrencyEnvironment(runtimeReleaseSnapshot{Plan: snapshot.Plan}, nil); len(env) != 0 {
		t.Fatalf("an image the recipe did not mark: %+v", env)
	}
}
