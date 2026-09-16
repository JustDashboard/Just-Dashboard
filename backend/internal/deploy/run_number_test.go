package deploy

import (
	"context"
	"testing"
)

func TestRunNumbersBelongToProjectAcrossEnvironmentsAndWriters(t *testing.T) {
	f := newOrchestrationFixture(t)
	ctx := context.Background()
	legacy := NewStore(f.store, nil, nil)
	production := f.addEnvironment(t, "production", EnvironmentProduction)
	first := f.enqueue(t, production)
	preview := f.addEnvironment(t, "preview", EnvironmentPreview)
	second := f.enqueue(t, preview)
	if first.RunNumber != 1 || second.RunNumber != 2 {
		t.Fatalf("environment runs: first=%+v second=%+v", first, second)
	}

	oldProjectID := f.projectID
	if _, err := legacy.Archive(ctx, oldProjectID); err != nil {
		t.Fatal(err)
	}
	result, err := f.store.DB.Exec(`INSERT INTO deploy_projects(name, repo_path, hook_secret, hook_id, created_at)
		VALUES('orch-test', '/srv/orch-test', 'sealed', 'replacement-hook', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	f.projectID, err = result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	production = f.addEnvironment(t, "production", EnvironmentProduction)
	fresh := f.enqueue(t, production)
	if fresh.RunNumber != 1 || fresh.ID <= second.ID {
		t.Fatalf("recreated project must start at 1 with an independent identity: %+v", fresh)
	}

	// Independent compatibility writers must share the engine's sequence even
	// when requests arrive concurrently within the same second.
	const concurrent = 12
	results := make(chan error, concurrent)
	for i := 0; i < concurrent; i++ {
		go func() {
			_, err := legacy.StartRun(ctx, f.projectID, "manual", "operator", "")
			results <- err
		}()
	}
	for i := 0; i < concurrent; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	last := f.enqueue(t, production)
	if last.RunNumber != concurrent+2 {
		t.Fatalf("engine run after concurrent compatibility inserts: %+v", last)
	}
	all, err := legacy.Runs(ctx, f.projectID, 200)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, run := range all {
		if seen[run.RunNumber] || run.RunNumber < 1 || run.RunNumber > concurrent+2 {
			t.Fatalf("duplicate or invalid run number: %+v", run)
		}
		seen[run.RunNumber] = true
	}
	if len(seen) != concurrent+2 {
		t.Fatalf("got %d run numbers, want %d", len(seen), concurrent+2)
	}
	retained, err := legacy.Run(ctx, first.ID)
	if err != nil || retained.ProjectID != oldProjectID || retained.RunNumber != 1 {
		t.Fatalf("archived history changed: %+v, %v", retained, err)
	}
}
