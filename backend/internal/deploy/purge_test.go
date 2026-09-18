package deploy

import (
	"errors"
	"testing"
)

func TestPurgeArchivedChecksEveryRunState(t *testing.T) {
	for _, state := range append(append([]RunState{}, allRunStates...), RunState("unknown-future-state")) {
		t.Run(string(state), func(t *testing.T) {
			f := newPlanningStoreFixture(t)
			projectID, environmentID := insertConfigurationFixture(t, f)
			projects := NewStore(f.store, f.sealer, []string{t.TempDir()})
			if _, err := projects.Archive(t.Context(), projectID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.DB.Exec(`INSERT INTO deploy_runs(project_id, environment_id, started_at, status, state) VALUES(?, ?, 1, 'running', ?)`, projectID, environmentID, state); err != nil {
				t.Fatal(err)
			}
			_, err := projects.PurgeArchived(t.Context(), projectID)
			if state.Terminal() && err != nil {
				t.Fatalf("terminal: %v", err)
			}
			if !state.Terminal() && !errors.Is(err, ErrAlreadyDeploying) {
				t.Fatalf("unfinished: %v", err)
			}
		})
	}
}

func TestPurgeArchivedPreservesLegacyRunUntilItFinishes(t *testing.T) {
	f := newPlanningStoreFixture(t)
	projectID, _ := insertConfigurationFixture(t, f)
	projects := NewStore(f.store, f.sealer, []string{t.TempDir()})
	if _, err := projects.Archive(t.Context(), projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB.Exec(`INSERT INTO deploy_runs(project_id, started_at, status) VALUES(?, 1, 'running')`, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.PurgeArchived(t.Context(), projectID); !errors.Is(err, ErrAlreadyDeploying) {
		t.Fatalf("legacy active run: %v", err)
	}
	if _, err := f.store.DB.Exec(`UPDATE deploy_runs SET status = 'success' WHERE project_id = ?`, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := projects.PurgeArchived(t.Context(), projectID); err != nil {
		t.Fatal(err)
	}
}
