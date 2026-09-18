package dockerx

import (
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
)

// The rules in diagnose.go are claims about what is wrong with a container.
// These pin the claims that would be embarrassing to get backwards — telling
// an operator their service is fine when it is looping, or crying wolf over a
// one-shot job that succeeded.

func inspect(mut func(*container.InspectResponse)) container.InspectResponse {
	insp := container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			State:      &container.State{Running: true, StartedAt: time.Now().Add(-time.Hour).Format(time.RFC3339Nano)},
			HostConfig: &container.HostConfig{},
		},
		Config: &container.Config{Image: "example:1.2.3"},
	}
	insp.HostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyUnlessStopped}
	if mut != nil {
		mut(&insp)
	}
	return insp
}

func findingIDs(f []DockerFinding) string {
	ids := make([]string, 0, len(f))
	for _, item := range f {
		ids = append(ids, item.ID)
	}
	return strings.Join(ids, ",")
}

func has(f []DockerFinding, prefix string) *DockerFinding {
	for i := range f {
		if strings.HasPrefix(f[i].ID, prefix) {
			return &f[i]
		}
	}
	return nil
}

// A container that is doing its job raises nothing that reads as a problem.
// It may still collect recommendations — a health check it does not define, a
// memory limit it does not set — and those are deliberately a different
// severity, because a panel that paints "you could add a health check" the
// same colour as "this is looping" is a panel nobody reads twice.
func TestDiagnoseHealthyContainerRaisesNothingAlarming(t *testing.T) {
	ct := Container{ID: "abc", Name: "web", State: "running"}
	got := diagnoseContainer(ct, inspect(nil))
	for _, f := range got {
		if f.Severity == SeverityCritical || f.Severity == SeverityWarning {
			t.Fatalf("a healthy container raised %q at %s", f.ID, f.Severity)
		}
	}
}

// A container with a health check that passes and a memory limit set has
// nothing at all to say about it.
func TestDiagnoseWellConfiguredContainerIsSilent(t *testing.T) {
	ct := Container{ID: "abc", Name: "web", State: "running"}
	got := diagnoseContainer(ct, inspect(func(i *container.InspectResponse) {
		i.State.Health = &container.Health{Status: "healthy"}
		i.HostConfig.Memory = 512 * 1024 * 1024
	}))
	if len(got) != 0 {
		t.Fatalf("a well-configured container should produce no findings, got %s", findingIDs(got))
	}
}

// The split this whole model exists for: security and storage findings are not
// runtime health, so a container that is up and serving does not turn the
// runtime summary red by being badly configured.
func TestAttentionAndRuntimeAreCountedApart(t *testing.T) {
	findings := []DockerFinding{
		normalizeFinding(DockerFinding{ID: "a", Severity: SeverityCritical, Class: ClassSecurity}),
		normalizeFinding(DockerFinding{ID: "b", Severity: SeverityWarning, Class: ClassStorage}),
		normalizeFinding(DockerFinding{ID: "c", Severity: SeverityRecommendation, Class: ClassConfiguration}),
		normalizeFinding(DockerFinding{ID: "d", Severity: SeverityCritical, Class: ClassRuntime}),
	}
	a := summarizeAttention(findings)
	if a.Total != 3 {
		t.Errorf("attention counts everything that is not runtime, got %d", a.Total)
	}
	if a.Issues != 2 || a.Recommendations != 1 {
		t.Errorf("issues and recommendations are separate counts: %+v", a)
	}

	// The runtime summary is built from container states, never from the
	// findings, so it can say how many things are fine.
	rt := summarizeRuntime([]Container{
		{ID: "1", State: "running"},
		{ID: "2", State: "running"},
		{ID: "3", State: "exited"},
	}, map[string]healthFacts{
		"1": {hasCheck: true, status: "healthy"},
		"2": {},
	})
	if rt.Running != 2 || rt.Healthy != 1 || rt.NoHealthchk != 1 || rt.Exited != 1 {
		t.Fatalf("runtime summary miscounted: %+v", rt)
	}
	if rt.Status != "notice" {
		t.Errorf("a stopped container is worth a look, not an alarm: %q", rt.Status)
	}
	if !strings.Contains(rt.Summary, "without a health check") {
		t.Errorf("the summary must not let unchecked containers read as healthy: %q", rt.Summary)
	}
}

func TestRuntimeStatusIsCriticalOnlyForRuntimeFailures(t *testing.T) {
	unhealthy := summarizeRuntime(
		[]Container{{ID: "1", State: "running"}},
		map[string]healthFacts{"1": {hasCheck: true, status: "unhealthy"}},
	)
	if unhealthy.Status != "critical" {
		t.Errorf("a failing health check is a runtime failure, got %q", unhealthy.Status)
	}
	quiet := summarizeRuntime([]Container{{ID: "1", State: "running"}},
		map[string]healthFacts{"1": {hasCheck: true, status: "healthy"}})
	if quiet.Status != "ok" {
		t.Errorf("everything up and passing is ok, got %q", quiet.Status)
	}
	if empty := summarizeRuntime(nil, nil); empty.Status != "ok" || empty.Total != 0 {
		t.Errorf("no containers is not a problem: %+v", empty)
	}
}

func TestHealthFactsSeparateNoCheckFromNotYetRun(t *testing.T) {
	none := healthFactsOf(inspect(nil))
	if none.hasCheck {
		t.Error("a container with no healthcheck block defines no check")
	}
	declared := healthFactsOf(inspect(func(i *container.InspectResponse) {
		i.Config.Healthcheck = &container.HealthConfig{Test: []string{"CMD", "true"}}
	}))
	if !declared.hasCheck || declared.status != "" {
		t.Errorf("a declared check that has not run yet: %+v", declared)
	}
	disabled := healthFactsOf(inspect(func(i *container.InspectResponse) {
		i.Config.Healthcheck = &container.HealthConfig{Test: []string{"NONE"}}
	}))
	if disabled.hasCheck {
		t.Error("an explicitly disabled check is not a check")
	}
}

func TestDiagnoseOOMKill(t *testing.T) {
	ct := Container{ID: "abc", Name: "db", State: "exited"}
	insp := inspect(func(i *container.InspectResponse) {
		i.State.Running = false
		i.State.OOMKilled = true
		i.State.ExitCode = 137
		i.HostConfig.Memory = 512 * 1024 * 1024
	})
	f := has(diagnoseContainer(ct, insp), "container.oom.")
	if f == nil {
		t.Fatal("an OOM-killed container must be reported")
	}
	if f.Level != "critical" {
		t.Errorf("level = %q, want critical", f.Level)
	}
	// The limit is the fact that makes the finding actionable rather than a
	// restatement of the exit code.
	if !strings.Contains(f.Detail, "512.0 MB") {
		t.Errorf("detail should quote the limit that was hit, got %q", f.Detail)
	}
}

// A clean exit from a one-shot job is success, and reporting it would bury the
// real findings on any host that runs backups or migrations in a container.
func TestDiagnoseCleanExitWithoutRestartPolicyIsSilent(t *testing.T) {
	ct := Container{ID: "abc", Name: "migrate", State: "exited"}
	insp := inspect(func(i *container.InspectResponse) {
		i.State.Running = false
		i.State.ExitCode = 0
		i.HostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	})
	if f := has(diagnoseContainer(ct, insp), "container.exited."); f != nil {
		t.Fatalf("a finished job should not be reported: %q", f.Title)
	}
}

func TestDiagnoseRestartLoop(t *testing.T) {
	ct := Container{ID: "abc", Name: "api", State: "running"}
	insp := inspect(func(i *container.InspectResponse) {
		i.RestartCount = 12
		i.State.StartedAt = time.Now().Add(-30 * time.Second).Format(time.RFC3339Nano)
	})
	f := has(diagnoseContainer(ct, insp), "container.restartloop.")
	if f == nil {
		t.Fatal("a container restarting 12 times in the last minute must be reported")
	}
	if f.Level != "critical" {
		t.Errorf("level = %q, want critical", f.Level)
	}
}

// The same restart count spread over a long-running container is history, not
// a loop: a box up for a year has restarted things for ordinary reasons.
func TestDiagnoseOldRestartsAreNotALoop(t *testing.T) {
	ct := Container{ID: "abc", Name: "api", State: "running"}
	insp := inspect(func(i *container.InspectResponse) {
		i.RestartCount = 12
		i.State.StartedAt = time.Now().Add(-72 * time.Hour).Format(time.RFC3339Nano)
	})
	if f := has(diagnoseContainer(ct, insp), "container.restartloop."); f != nil {
		t.Fatal("restarts from days ago are not a loop")
	}
}

func TestDiagnoseUnhealthyQuotesTheCheckOutput(t *testing.T) {
	ct := Container{ID: "abc", Name: "web", State: "running"}
	insp := inspect(func(i *container.InspectResponse) {
		i.State.Health = &container.Health{
			Status: "unhealthy",
			Log:    []*container.HealthcheckResult{{Output: "curl: (7) Failed to connect to localhost port 8080"}},
		}
	})
	f := has(diagnoseContainer(ct, insp), "container.unhealthy.")
	if f == nil {
		t.Fatal("an unhealthy container must be reported")
	}
	if !strings.Contains(f.Detail, "Failed to connect") {
		t.Errorf("the health check's own output is the useful part, got %q", f.Detail)
	}
}

// Publishing on every interface is the finding that stops this dashboard from
// being the thing that put a database on the internet.
func TestDiagnoseWildcardPublishIsReported(t *testing.T) {
	ct := Container{ID: "abc", Name: "postgres", State: "running"}
	insp := inspect(func(i *container.InspectResponse) {
		i.HostConfig.PortBindings = nat.PortMap{
			"5432/tcp": []nat.PortBinding{{HostIP: "", HostPort: "5432"}},
		}
	})
	if has(diagnoseContainer(ct, insp), "container.exposed.") == nil {
		t.Fatal("a port bound to every interface must be reported")
	}
}

func TestDiagnoseLoopbackPublishIsNotReported(t *testing.T) {
	ct := Container{ID: "abc", Name: "postgres", State: "running"}
	insp := inspect(func(i *container.InspectResponse) {
		i.HostConfig.PortBindings = nat.PortMap{
			"5432/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "5432"}},
		}
	})
	if f := has(diagnoseContainer(ct, insp), "container.exposed."); f != nil {
		t.Fatalf("a loopback binding is the safe choice and must not be flagged: %q", f.Title)
	}
}

func TestDiagnoseNoRestartPolicyOnRunningService(t *testing.T) {
	ct := Container{ID: "abc", Name: "web", State: "running"}
	insp := inspect(func(i *container.InspectResponse) {
		i.HostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	})
	f := has(diagnoseContainer(ct, insp), "container.norestart.")
	if f == nil {
		t.Fatal("a running container that will not survive a reboot must be reported")
	}
	if f.Action != "set-restart" {
		t.Errorf("this finding is fixable in place, so it should carry an action; got %q", f.Action)
	}
}

// Compose owns its containers' restart policy, and telling an operator to set
// one by hand on a stack member is advice that would be undone on the next
// deploy.
func TestDiagnoseComposeMemberSkipsRestartAdvice(t *testing.T) {
	ct := Container{ID: "abc", Name: "web", State: "running", ComposeStack: "shop"}
	insp := inspect(func(i *container.InspectResponse) {
		i.HostConfig.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyDisabled}
	})
	if has(diagnoseContainer(ct, insp), "container.norestart.") != nil {
		t.Fatal("a compose-managed container's restart policy belongs to its compose file")
	}
}

func TestDiagnoseMovingTag(t *testing.T) {
	for _, image := range []string{"nginx", "nginx:latest", "ghcr.io/org/app:latest"} {
		ct := Container{ID: "abc", Name: "web", State: "running"}
		insp := inspect(func(i *container.InspectResponse) { i.Config.Image = image })
		if has(diagnoseContainer(ct, insp), "container.latest.") == nil {
			t.Errorf("%q is a moving tag and should be reported", image)
		}
	}
	for _, image := range []string{"nginx:1.27", "ghcr.io/org/app@sha256:abc"} {
		ct := Container{ID: "abc", Name: "web", State: "running"}
		insp := inspect(func(i *container.InspectResponse) { i.Config.Image = image })
		if f := has(diagnoseContainer(ct, insp), "container.latest."); f != nil {
			t.Errorf("%q is pinned and should not be reported", image)
		}
	}
}

func TestDiagnoseDockerSocketMount(t *testing.T) {
	ct := Container{ID: "abc", Name: "watchtower", State: "running"}
	insp := inspect(func(i *container.InspectResponse) {
		i.Mounts = []container.MountPoint{{Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock"}}
	})
	if has(diagnoseContainer(ct, insp), "container.dockersock.") == nil {
		t.Fatal("mounting the Docker socket is root on the host and must be reported")
	}
}

func TestWorstLevelRanksBySeverity(t *testing.T) {
	cases := []struct {
		findings []DockerFinding
		want     string
	}{
		{nil, "ok"},
		{[]DockerFinding{{Level: "notice"}}, "notice"},
		{[]DockerFinding{{Level: "notice"}, {Level: "warning"}}, "warning"},
		{[]DockerFinding{{Level: "notice"}, {Level: "critical"}, {Level: "warning"}}, "critical"},
	}
	for _, tc := range cases {
		if got := worstLevel(tc.findings); got != tc.want {
			t.Errorf("worstLevel(%v) = %q, want %q", tc.findings, got, tc.want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	// The expectations here are the frontend's `bytes()` output, which is the
	// whole point of the function: a finding quoting a size must spell it the
	// way the table beside it does.
	cases := map[int64]string{
		512:                    "512 B",
		1024:                   "1.0 KB",
		512 * 1024 * 1024:      "512.0 MB",
		3 * 1024 * 1024 * 1024: "3.0 GB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
