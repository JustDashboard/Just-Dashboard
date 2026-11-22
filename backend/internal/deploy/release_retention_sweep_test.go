package deploy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// guardlessBackend is a build backend that cannot say what holds an image.
type guardlessBackend struct{ BuildBackend }

const releaseTagFixture = "just-dashboard/deployment-4:run-12"

func releaseImageFixture() ReleaseArtifact {
	return ReleaseArtifact{
		Kind: ArtifactImage, Reference: releaseTagFixture, Digest: fakeContentDigest("release"),
		Metadata: mustJSON(map[string]any{"configDigest": fakeContentDigest("config")}),
	}
}

// The remover asks the daemon before it removes anything, and removes no
// image a container uses — running, paused for a warm window, stopped while
// its environment sleeps — nor a name the dashboard did not give it.
func TestReleaseImageRemoverAsksTheDaemonFirst(t *testing.T) {
	t.Parallel()
	artifact := releaseImageFixture()
	current := ResolvedImage{Digest: artifact.Digest, ConfigDigest: fakeContentDigest("config")}
	for _, test := range []struct {
		name     string
		backend  func(*artifactBackendFake) BuildBackend
		artifact ReleaseArtifact
		use      *ImageUse
		useErr   error
		want     error
		removed  []string
	}{
		{name: "nothing uses it", removed: []string{releaseTagFixture}},
		{
			name: "a container uses it, in whatever state", want: ErrArtifactInUse,
			use: &ImageUse{ID: fakeContentDigest("config"), Tags: []string{releaseTagFixture}, Containers: []string{"warm-paused-container", "sleeping-stopped-container"}},
		},
		{
			name: "a pulled Compose service under its registry name", want: ErrArtifactNotOwned,
			artifact: ReleaseArtifact{Kind: ArtifactImage, Reference: "postgres:16", Digest: fakeContentDigest("postgres")},
		},
		{
			name: "the dashboard's own images are not a release's", want: ErrArtifactNotOwned,
			artifact: ReleaseArtifact{Kind: ArtifactImage, Reference: "just-dashboard/backend:latest", Digest: fakeContentDigest("backend")},
		},
		{
			name: "a backend that cannot ask the daemon", want: ErrArtifactUnguarded,
			backend: func(fake *artifactBackendFake) BuildBackend { return guardlessBackend{fake} },
		},
		{name: "already gone", useErr: ErrImageGone},
		{name: "the daemon does not answer", useErr: errors.New("docker: connection refused"), want: errors.New("docker")},
		{name: "runtime records carry no bytes", artifact: ReleaseArtifact{Kind: ArtifactRuntimeConfig, Reference: "runtime"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &artifactBackendFake{inspected: map[string]ResolvedImage{releaseTagFixture: current}, useErr: test.useErr}
			if test.use != nil {
				fake.uses = map[string]ImageUse{releaseTagFixture: *test.use}
			}
			var backend BuildBackend = fake
			if test.backend != nil {
				backend = test.backend(fake)
			}
			candidate := artifact
			if test.artifact.Kind != "" {
				candidate = test.artifact
			}
			remover := NewDockerArtifactRemover(backend)
			checked := remover.CheckArtifact(context.Background(), candidate)
			if len(fake.removed) != 0 {
				t.Fatalf("a check removed %v", fake.removed)
			}
			err := remover.RemoveArtifact(context.Background(), candidate)
			switch {
			case test.want == nil && (err != nil || checked != nil):
				t.Fatalf("remove = %v, check = %v", err, checked)
			case test.want != nil && errors.Is(test.want, ErrArtifactInUse) || errors.Is(test.want, ErrArtifactNotOwned) || errors.Is(test.want, ErrArtifactUnguarded):
				if !errors.Is(err, test.want) || !errors.Is(checked, test.want) {
					t.Fatalf("remove = %v, check = %v, want %v", err, checked, test.want)
				}
			case test.want != nil && (err == nil || !strings.Contains(err.Error(), test.want.Error())):
				t.Fatalf("remove = %v, want %v", err, test.want)
			}
			if strings.Join(fake.removed, ",") != strings.Join(test.removed, ",") {
				t.Fatalf("removed %v, want %v", fake.removed, test.removed)
			}
		})
	}
}

// Removing by id removes every name an image carries, so a release whose
// tag moved is not removed by id while another name still holds the image.
func TestReleaseImageRemoverKeepsAnImageAnotherNameHolds(t *testing.T) {
	t.Parallel()
	artifact := releaseImageFixture()
	config := fakeContentDigest("config")
	fake := &artifactBackendFake{
		inspected: map[string]ResolvedImage{releaseTagFixture: {Digest: fakeContentDigest("moved"), ConfigDigest: fakeContentDigest("moved-config")}},
		uses:      map[string]ImageUse{config: {ID: config, Tags: []string{"example/app:prod"}}},
	}
	if err := NewDockerArtifactRemover(fake).RemoveArtifact(context.Background(), artifact); !errors.Is(err, ErrArtifactNotOwned) || len(fake.removed) != 0 {
		t.Fatalf("remove = %v, removed %v", err, fake.removed)
	}
	fake.uses[config] = ImageUse{ID: config, Tags: []string{"just-dashboard/deployment-4:run-13"}}
	if err := NewDockerArtifactRemover(fake).RemoveArtifact(context.Background(), artifact); err != nil || len(fake.removed) != 1 || fake.removed[0] != config {
		t.Fatalf("remove = %v, removed %v, want the config id", err, fake.removed)
	}
}

// retentionFixture is an environment with releases 1..n that went live, the
// last of them live now, each with one image.
type retentionFixture struct {
	*releaseStoreFixture
	releases  []int64
	artifacts []int64
}

func newRetentionFixture(t *testing.T, count int) *retentionFixture {
	t.Helper()
	fixture := &retentionFixture{releaseStoreFixture: newReleaseStoreFixture(t)}
	for number := 1; number <= count; number++ {
		created := fixture.now.Add(time.Duration(number-count) * 24 * time.Hour).Unix()
		result, err := fixture.base.DB.Exec(
			"INSERT INTO deploy_releases(project_id, environment_id, release_number, state, plan_revision, source_revision, source_identity_json, image_digest, config_digest, variables_digest, strategy, provenance_json, created_at) VALUES(?, ?, ?, 'retained', 1, ?, '{}', ?, ?, ?, 'blue_green', '{}', ?)",
			fixture.projectID, fixture.envID, number, strings.Repeat(fmt.Sprint(number%10), 40),
			fakeContentDigest(fmt.Sprintf("image-%d", number)), fakeContentDigest("config"), fakeContentDigest("variables"), created)
		if err != nil {
			t.Fatal(err)
		}
		releaseID, _ := result.LastInsertId()
		fixture.releases = append(fixture.releases, releaseID)
		result, err = fixture.base.DB.Exec(
			"INSERT INTO deploy_release_artifacts(release_id, kind, reference, digest, metadata_json, size_bytes, state, created_at) VALUES(?, 'image', ?, ?, '{}', 1000, 'available', ?)",
			releaseID, fmt.Sprintf("just-dashboard/deployment-%d:run-%d", fixture.envID, number),
			fakeContentDigest(fmt.Sprintf("image-%d", number)), created)
		if err != nil {
			t.Fatal(err)
		}
		artifactID, _ := result.LastInsertId()
		fixture.artifacts = append(fixture.artifacts, artifactID)
	}
	live := fixture.releases[count-1]
	if _, err := fixture.base.DB.Exec("UPDATE deploy_releases SET state = 'live' WHERE id = ?", live); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.base.DB.Exec("UPDATE deploy_environments SET live_release_id = ? WHERE id = ?", live, fixture.envID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *retentionFixture) decisions(t *testing.T) map[int64]ArtifactRetentionDecision {
	t.Helper()
	decisions, err := f.runs.ArtifactRetentionPlan(context.Background(), f.now)
	if err != nil {
		t.Fatal(err)
	}
	byRelease := map[int64]ArtifactRetentionDecision{}
	for _, decision := range decisions {
		byRelease[decision.Artifact.ReleaseID] = decision
	}
	return byRelease
}

func (f *retentionFixture) artifactState(t *testing.T, index int) (string, int64) {
	t.Helper()
	var state string
	var prunedAt int64
	if err := f.base.DB.QueryRow("SELECT state, pruned_at FROM deploy_release_artifacts WHERE id = ?", f.artifacts[index]).
		Scan(&state, &prunedAt); err != nil {
		t.Fatal(err)
	}
	return state, prunedAt
}

// Beyond the six newest that went live, a release keeps its image while a
// container of it may still be on the server — warm, stopped, draining, a
// quarantined preview — or while an unfinished rollback names it.
func TestRetentionKeepsWhatAContainerOrAQueuedRollbackStillNeeds(t *testing.T) {
	t.Parallel()
	fixture := newRetentionFixture(t, 10)
	for index, state := range map[int]string{0: "warm", 1: "stopped", 2: "retired", 3: "removed"} {
		if _, err := fixture.base.DB.Exec(
			"INSERT INTO deploy_release_runtimes(release_id, environment_id, kind, runtime_id, state, created_at, updated_at) VALUES(?, ?, 'container', ?, ?, 1, 1)",
			fixture.releases[index], fixture.envID, fmt.Sprintf("container-%d", index), state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fixture.base.DB.Exec(
		`INSERT INTO deploy_runs(project_id, environment_id, started_at, status, state, operation, metadata_json) VALUES(?, ?, 1, 'queued', 'queued', 'rollback', ?)`,
		fixture.projectID, fixture.envID, fmt.Sprintf(`{"targetReleaseId":%d}`, fixture.releases[2])); err != nil {
		t.Fatal(err)
	}
	decisions := fixture.decisions(t)
	for index, want := range map[int]string{0: "container", 1: "container", 2: "unfinished rollback", 9: "live"} {
		if decision := decisions[fixture.releases[index]]; !decision.Retain || !strings.Contains(decision.Reason, want) {
			t.Fatalf("release %d = %+v, want retained for %q", index+1, decision, want)
		}
	}
	if decision := decisions[fixture.releases[3]]; decision.Retain {
		t.Fatalf("a release whose container was removed, outside retention, was kept: %+v", decision)
	}
	for index := 4; index <= 8; index++ {
		if decision := decisions[fixture.releases[index]]; !decision.Retain || !strings.Contains(decision.Reason, "five prior") {
			t.Fatalf("release %d = %+v, want the rollback set", index+1, decision)
		}
	}
}

// countingRemover records what it removed and how many removals overlapped.
type countingRemover struct {
	mu       sync.Mutex
	removed  []int64
	refuse   map[int64]error
	inFlight atomic.Int32
	overlap  atomic.Bool
	hold     chan struct{}
}

func (r *countingRemover) RemoveArtifact(_ context.Context, artifact ReleaseArtifact) error {
	if r.inFlight.Add(1) > 1 {
		r.overlap.Store(true)
	}
	defer r.inFlight.Add(-1)
	if r.hold != nil {
		<-r.hold
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.refuse[artifact.ID]; err != nil {
		return err
	}
	r.removed = append(r.removed, artifact.ID)
	return nil
}

func (r *countingRemover) CheckArtifact(_ context.Context, artifact ReleaseArtifact) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refuse[artifact.ID]
}

func (r *countingRemover) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.removed)
}

// A pass removes what retention no longer keeps, at most its limit, audits
// each removal, refuses an image the daemon says is in use without marking
// it failed, and the release's readout says when its image went.
func TestRetentionSweepRemovesAuditsAndReports(t *testing.T) {
	t.Parallel()
	fixture := newRetentionFixture(t, 10)
	remover := &countingRemover{refuse: map[int64]error{
		fixture.artifacts[1]: fmt.Errorf("%w: 1 container(s), warm first", ErrArtifactInUse),
		fixture.artifacts[2]: errors.New("docker: image is being used by a running build"),
	}}
	var mu sync.Mutex
	events := []ArtifactPruneEvent{}
	sweeper := NewReleaseImageSweeper(fixture.runs, remover, func(_ context.Context, event ArtifactPruneEvent) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}, nil)
	sweeper.limit = 2

	// A dry run removes and writes nothing.
	dry, err := sweeper.DryRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wouldRemove := 0
	for _, decision := range dry.Decisions {
		if !decision.Retain {
			wouldRemove++
		}
	}
	if !dry.DryRun || remover.count() != 0 || wouldRemove != 3 || len(events) != 0 {
		t.Fatalf("dry run: would remove %d, removed %d, events %d", wouldRemove, remover.count(), len(events))
	}
	if state, _ := fixture.artifactState(t, 0); state != "available" {
		t.Fatalf("a dry run changed state to %s", state)
	}

	report, err := sweeper.Sweep(context.Background(), "daily")
	if err != nil {
		t.Fatal(err)
	}
	// Releases 1..4 are outside retention: 1 removed, 2 in use, 3 failed,
	// and 4 left for the next pass by the limit of two tries.
	if report.Pruned != 1 || report.Failed != 1 || report.Deferred != 1 || report.Reclaimed != 1000 {
		t.Fatalf("report = %+v", report)
	}
	if state, prunedAt := fixture.artifactState(t, 0); state != "pruned" || prunedAt != fixture.now.Unix() {
		t.Fatalf("removed artifact = %s at %d", state, prunedAt)
	}
	if state, _ := fixture.artifactState(t, 1); state != "available" {
		t.Fatalf("an image in use was left %s, want available", state)
	}
	if state, _ := fixture.artifactState(t, 2); state != "prune_failed" {
		t.Fatalf("a failed removal was left %s", state)
	}
	if len(events) != 2 || !events[0].Decision.Removed || events[0].Trigger != "daily" ||
		events[1].Decision.Removed || !strings.Contains(events[1].Decision.Error, "running build") {
		t.Fatalf("audited %+v", events)
	}
	statuses, err := fixture.runs.ReleaseArtifactStatus(context.Background(), fixture.releases[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Retained || statuses[0].PrunedAt == nil ||
		!strings.Contains(statuses[0].Reason, "removed this from the server") {
		t.Fatalf("readout = %+v", statuses)
	}
	for _, decision := range report.Decisions {
		if decision.Artifact.ID == fixture.artifacts[1] && (!decision.Retain || !strings.Contains(decision.Reason, "container")) {
			t.Fatalf("an image in use reads %+v", decision)
		}
	}
}

// No removal happens while any deployment holds a lease.
func TestRetentionSweepWaitsForEveryLease(t *testing.T) {
	t.Parallel()
	fixture := newRetentionFixture(t, 8)
	result, err := fixture.base.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, status, state) VALUES(?, ?, 1, 'running', 'running')`,
		fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := result.LastInsertId()
	if _, err := fixture.base.DB.Exec(
		`INSERT INTO deploy_queue_leases(run_id, environment_id, claim_token, slot_class, claimed_by, expires_at, heartbeat_at, created_at) VALUES(?, ?, 'token', 'heavy', 'worker', ?, ?, ?)`,
		runID, fixture.envID, fixture.now.Add(time.Minute).Unix(), fixture.now.Unix(), fixture.now.Unix()); err != nil {
		t.Fatal(err)
	}
	remover := &countingRemover{}
	report, err := NewReleaseImageSweeper(fixture.runs, remover, nil, nil).Sweep(context.Background(), "daily")
	if err != nil {
		t.Fatal(err)
	}
	if report.Pruned != 0 || remover.count() != 0 {
		t.Fatalf("removed %d under a lease", remover.count())
	}
}

// Passes run one at a time, the first after start-up, and a finished run
// asks for one.
func TestRetentionSweepRunsSeriallyAndAfterAFinishedRun(t *testing.T) {
	t.Parallel()
	fixture := newRetentionFixture(t, 9)
	remover := &countingRemover{hold: make(chan struct{})}
	sweeper := NewReleaseImageSweeper(fixture.runs, remover, nil, nil)
	sweeper.first, sweeper.settle = time.Hour, 0
	done := make(chan struct{}, 2)
	for range 2 {
		go func() {
			_, _ = sweeper.Sweep(context.Background(), "daily")
			done <- struct{}{}
		}()
	}
	for range 3 {
		remover.hold <- struct{}{}
	}
	<-done
	<-done
	if remover.overlap.Load() || remover.count() != 3 {
		t.Fatalf("overlapping passes = %v, removed %d", remover.overlap.Load(), remover.count())
	}

	fresh := newRetentionFixture(t, 8)
	quick := &countingRemover{}
	runner := NewReleaseImageSweeper(fresh.runs, quick, nil, nil)
	runner.first, runner.settle = time.Hour, 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner.Start(ctx)
	runner.RunFinished(context.Background(), EngineRun{ID: 1, State: RunQueued})
	time.Sleep(50 * time.Millisecond)
	if quick.count() != 0 {
		t.Fatal("a run that has not finished asked for a pass")
	}
	runner.RunFinished(context.Background(), EngineRun{ID: 1, State: RunSucceeded})
	deadline := time.Now().Add(5 * time.Second)
	for quick.count() != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("a finished run's pass removed %d, want 2", quick.count())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
