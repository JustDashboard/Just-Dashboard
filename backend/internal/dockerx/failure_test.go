package dockerx

import (
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func diagnosis(insp container.InspectResponse) *FailureDiagnosis {
	d := &FailureDiagnosis{
		Name: "app", Confidence: "inferred",
		Evidence: []FailureEvidence{}, Suggestions: []string{},
	}
	d.Restarts = restartPattern(insp, nil, "app")
	collectFailureEvidence(d, insp)
	concludeFailure(d, insp)
	return d
}

func TestOOMKillIsObservedNotInferred(t *testing.T) {
	d := diagnosis(inspect(func(i *container.InspectResponse) {
		i.State.Running = false
		i.State.OOMKilled = true
		i.State.ExitCode = 137
		i.HostConfig.Memory = 512 * 1024 * 1024
	}))
	if d.Confidence != "observed" {
		t.Errorf("the kernel wrote this one down, so it is observed: %q", d.Confidence)
	}
	if !strings.Contains(d.Likely, "512.0 MB") {
		t.Errorf("the limit belongs in the conclusion: %q", d.Likely)
	}
	if len(d.Suggestions) == 0 {
		t.Error("a diagnosis with no next step is a fact, not a diagnosis")
	}
}

func TestRestartLoopIsNamedAndItsCauseIsLabelledLikely(t *testing.T) {
	d := diagnosis(inspect(func(i *container.InspectResponse) {
		i.RestartCount = 17
		i.State.ExitCode = 1
		i.State.StartedAt = time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	}))
	if d.State != "looping" {
		t.Fatalf("state = %q, want looping", d.State)
	}
	if !strings.Contains(d.Headline, "Restart loop detected") {
		t.Errorf("headline = %q", d.Headline)
	}
	if !strings.HasPrefix(d.Likely, "Likely cause") {
		t.Errorf("an inference must say it is one: %q", d.Likely)
	}
	if d.Confidence == "observed" {
		t.Error("nothing observed the cause of this loop")
	}
	if d.LogWindow == nil {
		t.Error("a loop needs a log window around the failure, not the tail")
	}
}

// The counter alone cannot tell a loop from an old scar. A container that has
// restarted many times but has been up for days is not looping.
func TestALongUptimeWithRestartsIsNotALoop(t *testing.T) {
	d := diagnosis(inspect(func(i *container.InspectResponse) {
		i.RestartCount = 40
		i.State.StartedAt = time.Now().Add(-72 * time.Hour).Format(time.RFC3339Nano)
	}))
	if d.State != "running" {
		t.Fatalf("state = %q, want running", d.State)
	}
	if d.Restarts.Looping {
		t.Error("40 restarts across three days is not a loop")
	}
	if !strings.Contains(d.Headline, "nothing to report") {
		t.Errorf("a healthy container deserves to be told it is healthy: %q", d.Headline)
	}
}

func TestCleanExitIsNotReportedAsAFailure(t *testing.T) {
	d := diagnosis(inspect(func(i *container.InspectResponse) {
		i.State.Running = false
		i.State.ExitCode = 0
	}))
	if !strings.Contains(d.Headline, "exited cleanly") {
		t.Errorf("headline = %q", d.Headline)
	}
	if strings.Contains(strings.ToLower(d.Headline), "fail") {
		t.Errorf("status 0 is not a failure: %q", d.Headline)
	}
}

func TestUnhealthyIsObservedAndCarriesTheCheckOutput(t *testing.T) {
	d := diagnosis(inspect(func(i *container.InspectResponse) {
		i.State.Health = &container.Health{
			Status: "unhealthy", FailingStreak: 4,
			Log: []*container.HealthcheckResult{{Output: "connection refused"}},
		}
	}))
	if d.State != "unhealthy" || d.Confidence != "observed" {
		t.Fatalf("state = %q confidence = %q", d.State, d.Confidence)
	}
	found := false
	for _, e := range d.Evidence {
		if strings.Contains(e.Value, "connection refused") {
			found = true
		}
	}
	if !found {
		t.Errorf("the check's own output is the evidence: %+v", d.Evidence)
	}
}

func TestEvidenceLeadsWithWhatSettlesIt(t *testing.T) {
	d := diagnosis(inspect(func(i *container.InspectResponse) {
		i.State.Running = false
		i.State.ExitCode = 137
		i.HostConfig.Memory = 256 * 1024 * 1024
	}))
	if len(d.Evidence) == 0 {
		t.Fatal("no evidence collected")
	}
	if d.Evidence[0].Weight != "decisive" {
		t.Errorf("the first row should be the one that settles it: %+v", d.Evidence[0])
	}
	for _, e := range d.Evidence {
		if e.Source == "" {
			t.Errorf("every fact names where it came from: %+v", e)
		}
	}
}

func TestExitCodeMeanings(t *testing.T) {
	cases := map[int]string{
		0:   "clean exit",
		125: "Docker itself",
		126: "could not be executed",
		127: "was not found",
		137: "SIGKILL",
		139: "segmentation fault",
		143: "SIGTERM",
		130: "signal 2",
	}
	for code, want := range cases {
		if got := exitCodeMeaning(code); !strings.Contains(got, want) {
			t.Errorf("exitCodeMeaning(%d) = %q, want it to mention %q", code, got, want)
		}
	}
}
