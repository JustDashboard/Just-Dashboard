package stacks_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/stacks"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

type serviceFixture struct {
	store    *stacks.Store
	service  *stacks.Service
	mu       sync.Mutex
	live     stacks.Live
	liveErr  error
	passes   int
	project  int64
	manifest *stacks.Manifest
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
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
	fixture := &serviceFixture{store: stacks.NewStore(st.DB, sealer), project: project, liveErr: stacks.ErrNoLiveRelease}
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
					if _, err := env.Credential(ctx, "a-admin"); err != nil {
						return stacks.Result{}, err
					}
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
		Runner: runner,
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
