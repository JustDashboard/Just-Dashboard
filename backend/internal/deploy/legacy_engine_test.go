package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	basestore "github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestLegacyCompatibilityPipelineRunsThroughPersistentEngine(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for the compatibility integration fixture")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	source := filepath.Join(root, "source")
	checkout := filepath.Join(root, "checkout")
	runGitFixture(t, root, "init", "--bare", remote)
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, source, "init", "-b", "main")
	runGitFixture(t, source, "config", "user.name", "Deploy Fixture")
	runGitFixture(t, source, "config", "user.email", "deploy-fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, source, "add", "README.md")
	runGitFixture(t, source, "commit", "-m", "fixture")
	runGitFixture(t, source, "remote", "add", "origin", remote)
	runGitFixture(t, source, "push", "-u", "origin", "main")
	runGitFixture(t, root, "clone", "--branch", "main", remote, checkout)

	st, err := basestore.Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	legacyStore := NewStore(st, sealer, []string{root})
	project, _, err := legacyStore.Create(t.Context(), &Project{
		Name: "persistent-legacy", RepoPath: checkout, Branch: "main",
		ComposeFile: "compose.yml", PreCommand: `printf hook-ran > hook.txt; printf '%s\n' "$FIXTURE_SECRET"`, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacyStore.SetEnv(t.Context(), project.ID, "FIXTURE_SECRET", "must-not-appear-in-log"); err != nil {
		t.Fatal(err)
	}
	runs := NewOrchestrationStore(st)
	environmentID, revision, err := runs.ProductionEnvironment(t.Context(), project.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, created, err := runs.Enqueue(t.Context(), RunRequest{
		ProjectID: project.ID, EnvironmentID: environmentID,
		Operation: OperationDeploy, Trigger: TriggerManual, Actor: "integration-test",
		RequestDigest: "legacy-engine-integration", PlanRevision: revision,
		SlotClass: SlotHeavy, Metadata: mustJSON(map[string]any{"compatibility": true}),
		Steps: []StepKey{StepLegacyPipeline},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("integration run unexpectedly joined an existing request")
	}
	engine := NewEngine(runs, NewLegacyStepExecutor(NewDeployer(legacyStore, nil)), nil, EngineConfig{
		WorkerID: "legacy-integration", PollEvery: 5 * time.Millisecond, LeaseTTL: time.Minute,
	}, nil)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForRunState(t, runs, run.ID, RunSucceeded)
	shutdownEngine(t, engine)

	compatibility, err := legacyStore.Run(t.Context(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if compatibility.Status != StatusSuccess || compatibility.EndedAt == nil {
		t.Fatalf("legacy run projection = %#v", compatibility)
	}
	if compatibility.FromCommit == "" || compatibility.ToCommit != compatibility.FromCommit {
		t.Fatalf("persistent commit evidence = from %q to %q", compatibility.FromCommit, compatibility.ToCommit)
	}
	for _, marker := range []string{"git fetch --prune origin", "FIXTURE_SECRET", "no compose file"} {
		if !strings.Contains(compatibility.Log, marker) {
			t.Errorf("compatibility log is missing %q: %s", marker, compatibility.Log)
		}
	}
	if strings.Contains(compatibility.Log, "must-not-appear-in-log") {
		t.Fatal("sealed environment value leaked into the compatibility log")
	}
	if data, err := os.ReadFile(filepath.Join(checkout, "hook.txt")); err != nil || string(data) != "hook-ran" {
		t.Fatalf("stored release task result = %q, %v", data, err)
	}
	envInfo, err := os.Stat(filepath.Join(checkout, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if envInfo.Mode().Perm() != 0o600 {
		t.Fatalf("generated .env mode = %o, want 600", envInfo.Mode().Perm())
	}
}

func runGitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
