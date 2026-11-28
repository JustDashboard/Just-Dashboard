package stacks_test

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

type serviceFixture struct {
	db      *sql.DB
	store   *stacks.Store
	service *stacks.Service
	mu      sync.Mutex
	live    stacks.Live
	liveErr error
	// released is what ReleaseServices answers: nil, every service;
	// releasedErr is a read of it that failed.
	released    map[string]bool
	releasedErr error
	passes      int
	project     int64
	manifest    *stacks.Manifest
	// warned counts the service's warnings that a pass could not be prepared.
	warned int
}

// Write counts the service's log lines that say a pass could not be prepared.
func (f *serviceFixture) Write(line []byte) (int, error) {
	if strings.Contains(string(line), "could not be prepared") {
		f.mu.Lock()
		f.warned++
		f.mu.Unlock()
	}
	return len(line), nil
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	result, err := st.DB.Exec(`INSERT INTO deploy_projects (name, repo_path, hook_secret, hook_id, created_at) VALUES ('media', '', 'secret', 'hook-media', ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	project, _ := result.LastInsertId()
	fixture := &serviceFixture{db: st.DB, store: stacks.NewStore(st.DB, sealer), project: project, liveErr: stacks.ErrNoLiveRelease}
	fixture.manifest = &stacks.Manifest{ID: "test", Version: "1", Title: "Test",
		Presets: []stacks.Preset{
			{ID: "small", Template: "test-small", Services: []string{"a"}},
			{ID: "large", Template: "test-large", Services: []string{"a", "b"}},
		},
		Services:    []stacks.ServiceInfo{{Name: "a"}, {Name: "b"}},
		Credentials: []stacks.CredentialSpec{{Name: "a-admin", Service: "a", Label: "A", Length: 12}},
		Steps: []stacks.Step{
			{ID: "a.setup", Service: "a", Title: "Set up A", Stage: stacks.StageInitialize, Timeout: time.Second, Attempts: 1,
				Run: func(ctx context.Context, env stacks.Env) (stacks.Result, error) {
					fixture.mu.Lock()
					fixture.passes++
					fixture.mu.Unlock()
					password, err := env.Credential(ctx, "a-admin")
					if err != nil {
						return stacks.Result{}, err
					}
					env.Logf("A signs in with %s", password)
					return stacks.Result{Message: "A is set up", Outputs: map[string]string{"quality": env.Option("quality")}}, nil
				}},
			{ID: "b.link", Service: "b", Title: "Link B", Stage: stacks.StageLink, After: []string{"a.setup"}, Timeout: time.Second, Attempts: 1,
				Run: func(context.Context, stacks.Env) (stacks.Result, error) { return stacks.Result{Message: "linked"}, nil }},
		},
		Options: []stacks.Option{{Name: "quality", Variable: "MEDIA_QUALITY", Default: "1080p"}},
	}
	runner := stacks.DefaultRunner()
	runner.Backoff = func(int) time.Duration { return time.Millisecond }
	fixture.service = stacks.NewService(stacks.ServiceDeps{
		Store:    fixture.store,
		Manifest: func(id string) (*stacks.Manifest, bool) { return fixture.manifest, id == "test" },
		LiveRelease: func(context.Context, int64) (stacks.Live, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			return fixture.live, fixture.liveErr
		},
		Variables: func(context.Context, int64, int64) (map[string]string, error) {
			return map[string]string{"MEDIA_QUALITY": "2160p"}, nil
		},
		ReleaseServices: func(context.Context, stacks.Preset, stacks.Live) (map[string]bool, error) {
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			return fixture.released, fixture.releasedErr
		},
		Runner: runner,
		Log:    slog.New(slog.NewTextHandler(fixture, nil)),
	})
	if err := fixture.store.Register(context.Background(), project, 7, "test", "small", "tester"); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f *serviceFixture) goLive(release int64, template string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live = stacks.Live{ReleaseID: release, Kind: "compose", RuntimeID: "jd-test", TemplateID: template}
	f.liveErr = nil
}

// settle ticks until no pass is running.
func (f *serviceFixture) settle(t *testing.T) *stacks.Record {
	t.Helper()
	ctx := context.Background()
	f.service.Tick(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for f.service.Running(f.project) {
		if time.Now().After(deadline) {
			t.Fatal("the pass did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	record, err := f.store.Get(ctx, f.project)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func (f *serviceFixture) passCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.passes
}

func TestServiceConfiguresEachLiveReleaseOnceAndAgainOnRequest(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	if record := f.settle(t); record.Status != stacks.StatusWaiting || f.passCount() != 0 {
		t.Fatalf("before a release: %+v, %d passes", record, f.passCount())
	}
	if err := f.service.Request(ctx, f.project); !errors.Is(err, stacks.ErrWaiting) {
		t.Fatalf("Retry before a release = %v", err)
	}

	f.goLive(11, "test-small")
	record := f.settle(t)
	if record.Status != stacks.StatusReady || record.ConfiguredReleaseID != 11 || f.passCount() != 1 {
		t.Fatalf("after the first release: %+v, %d passes", record, f.passCount())
	}
	steps, _ := f.store.Steps(ctx, f.project)
	if steps["a.setup"].Status != stacks.StepDone || steps["a.setup"].Outputs["quality"] != "2160p" ||
		steps["b.link"].Status != stacks.StepSkipped || steps["b.link"].Message != stacks.AbsentMessage {
		t.Fatalf("steps = %+v", steps)
	}
	if logged := steps["a.setup"].Log; len(logged) != 1 || logged[0] != "A signs in with ••••••" {
		t.Fatalf("the step's log, read back = %q", logged)
	}
	stored, _ := f.store.StoredCredentials(ctx, f.project)
	if !stored["a-admin"] {
		t.Fatal("the generated account was not kept")
	}
	first, _, _ := f.store.Credentials(f.project).Get(ctx, "a-admin")

	// Nothing new: no pass.
	f.settle(t)
	if f.passCount() != 1 {
		t.Fatalf("%d passes without a new release", f.passCount())
	}
	// Retry runs one, and the account is the same one.
	if err := f.service.Request(ctx, f.project); err != nil {
		t.Fatal(err)
	}
	f.settle(t)
	again, _, _ := f.store.Credentials(f.project).Get(ctx, "a-admin")
	if f.passCount() != 2 || again != first {
		t.Fatalf("after Retry: %d passes, account changed %v", f.passCount(), again != first)
	}

	// The source moved to the larger preset: the stack follows, and the
	// step it skipped runs.
	f.goLive(12, "test-large")
	record = f.settle(t)
	steps, _ = f.store.Steps(ctx, f.project)
	if record.Preset != "large" || record.ConfiguredReleaseID != 12 || steps["b.link"].Status != stacks.StepDone {
		t.Fatalf("after the preset change: %+v %+v", record, steps["b.link"])
	}

	// A release of something that is not this stack is left alone.
	f.goLive(13, "whoami")
	f.settle(t)
	if f.passCount() != 3 {
		t.Fatalf("%d passes after the source left the stack", f.passCount())
	}
}

func TestServiceRunsAPassARestartInterruptedAgain(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	f.goLive(21, "test-small")
	if err := f.store.BeginPass(ctx, f.project, 21); err != nil {
		t.Fatal(err)
	}
	if err := f.store.ResetInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	record := f.settle(t)
	if record.Status != stacks.StatusReady || record.ConfiguredReleaseID != 21 || f.passCount() != 1 {
		t.Fatalf("after the restart: %+v, %d passes", record, f.passCount())
	}
}

// A deployment removed while its stack is being configured takes the pass
// with it: the next look stops it rather than leaving it to set up
// containers that are going away, holding one of the two places a pass
// runs in.
func TestServiceStopsThePassOfADeploymentThatWasRemoved(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	started := make(chan struct{})
	f.manifest.Steps[0].Timeout = time.Minute
	f.manifest.Steps[0].Run = func(ctx context.Context, _ stacks.Env) (stacks.Result, error) {
		close(started)
		<-ctx.Done()
		return stacks.Result{}, ctx.Err()
	}
	f.goLive(31, "test-small")
	f.service.Tick(ctx)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the pass did not start")
	}
	if _, err := f.db.Exec(`DELETE FROM deploy_projects WHERE id = ?`, f.project); err != nil {
		t.Fatal(err)
	}
	f.service.Tick(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for f.service.Running(f.project) {
		if time.Now().After(deadline) {
			t.Fatal("the pass of a removed deployment is still running")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Retry on a deployment whose source left the stack is refused in words:
// Tick never starts a pass for a release that is not a preset, so a request
// would be answered and never run.
func TestServiceRefusesAPassForAReleaseThatIsNotAPreset(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	f.goLive(41, "whoami")
	if err := f.service.Request(ctx, f.project); !errors.Is(err, stacks.ErrNotAPreset) {
		t.Fatalf("Retry on a release that is not a preset = %v", err)
	}
	record, err := f.store.Get(ctx, f.project)
	if err != nil {
		t.Fatal(err)
	}
	if !record.RequestedAt.IsZero() {
		t.Fatalf("a pass that will never run was recorded as asked for: %+v", record)
	}
	f.goLive(42, "test-small")
	if err := f.service.Request(ctx, f.project); err != nil {
		t.Fatalf("Retry on a preset's release = %v", err)
	}
}

// A release that does not run every service of the preset (one of an older
// version of the template, from before a service was added to it) leaves
// that service's steps out of the pass; the release that runs it sets it up.
func TestServiceLeavesOutAServiceTheReleaseDoesNotRun(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	f.mu.Lock()
	f.released = map[string]bool{"a": true}
	f.mu.Unlock()
	f.goLive(21, "test-large")
	record := f.settle(t)
	steps, _ := f.store.Steps(ctx, f.project)
	if record.Status != stacks.StatusReady || steps["a.setup"].Status != stacks.StepDone ||
		steps["b.link"].Status != stacks.StepSkipped || steps["b.link"].Message != stacks.AbsentMessage {
		t.Fatalf("a release without b: %+v %+v", record, steps)
	}

	f.mu.Lock()
	f.released = nil
	f.mu.Unlock()
	f.goLive(22, "test-large")
	f.settle(t)
	steps, _ = f.store.Steps(ctx, f.project)
	if steps["b.link"].Status != stacks.StepDone {
		t.Fatalf("the release that runs b: %+v", steps["b.link"])
	}
}

// A pass that cannot read what the release runs runs nothing: it is not
// recorded as configured and is asked for again, so it runs once the read
// works, even when what made it due was a Retry that no longer stands.
func TestServiceRunsNothingWhenWhatTheReleaseRunsCannotBeRead(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	f.goLive(51, "test-large")
	f.settle(t)
	if f.passCount() != 1 {
		t.Fatalf("%d passes over the first release", f.passCount())
	}
	f.mu.Lock()
	f.releasedErr = errors.New("database is locked")
	f.mu.Unlock()
	if err := f.service.Request(ctx, f.project); err != nil {
		t.Fatal(err)
	}
	record := f.settle(t)
	if f.passCount() != 1 || record.Status != stacks.StatusFailed || record.RequestedAt.IsZero() {
		t.Fatalf("a pass whose release could not be read: %d passes, %+v", f.passCount(), record)
	}

	// It is asked for again at every look while the read fails, and says so once.
	for range 3 {
		f.settle(t)
	}
	f.mu.Lock()
	warned := f.warned
	f.releasedErr = nil
	f.mu.Unlock()
	if warned != 1 {
		t.Fatalf("a reason that stands was logged %d times over four looks", warned)
	}
	record = f.settle(t)
	if f.passCount() != 2 || record.Status != stacks.StatusReady || !record.RequestedAt.IsZero() {
		t.Fatalf("once it reads: %d passes, %+v", f.passCount(), record)
	}
}

// A rollback to a release that does not run a service the last one did is a
// new live release, so a pass runs over it, and the step of that service
// takes back what it made in the apps that stay.
func TestServiceWithdrawsAServiceARollbackLeftOut(t *testing.T) {
	f := newServiceFixture(t)
	ctx := context.Background()
	withdrawn := 0
	f.manifest.Steps[1].Withdraw = func(context.Context, stacks.Env) (stacks.Result, error) {
		f.mu.Lock()
		withdrawn++
		f.mu.Unlock()
		return stacks.Result{Message: "taken back"}, nil
	}
	f.goLive(61, "test-large")
	f.settle(t)
	steps, _ := f.store.Steps(ctx, f.project)
	if steps["b.link"].Status != stacks.StepDone || withdrawn != 0 {
		t.Fatalf("the release that runs b: %+v, %d withdrawn", steps["b.link"], withdrawn)
	}

	f.mu.Lock()
	f.released = map[string]bool{"a": true}
	f.mu.Unlock()
	f.goLive(60, "test-large")
	record := f.settle(t)
	steps, _ = f.store.Steps(ctx, f.project)
	f.mu.Lock()
	defer f.mu.Unlock()
	if record.ConfiguredReleaseID != 60 || record.Status != stacks.StatusReady || withdrawn != 1 ||
		steps["b.link"].Status != stacks.StepSkipped || steps["b.link"].Message != stacks.AbsentMessage {
		t.Fatalf("after the rollback: %+v %+v, %d withdrawn", record, steps["b.link"], withdrawn)
	}
}
