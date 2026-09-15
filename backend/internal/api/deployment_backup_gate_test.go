package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestDeploymentBackupGateUsesBackupsOwnerAndReturnsOnlySafeEvidence(t *testing.T) {
	server := testServer(t)
	source := filepath.Join(server.Cfg.FileRoots[0], "backup-source")
	destination := filepath.Join(server.Cfg.FileRoots[0], "backup-output")
	for _, path := range []string{source, destination} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "state.db"), []byte("fixture state"), 0o600); err != nil {
		t.Fatal(err)
	}
	job, err := server.modules.backupStore.Create(context.Background(), &backups.Job{
		Name: "deployment-gate", Sources: []string{source}, Excludes: []string{},
		TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: destination},
		Retention: 2, Enabled: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gate := newDeploymentBackupGate(server.modules.backupStore, server.modules.backupRunner)
	evidence, err := gate.Evaluate(context.Background(), deploy.BackupGateRequest{
		JobID: job.ID, RequiredBeforeDeploy: true, MaxAgeSeconds: 3600,
		PersistentSources: []string{filepath.Join(source, "state.db")},
	})
	if err != nil || evidence.Status != string(backups.StatusSuccess) || evidence.RunID == 0 ||
		!evidence.Fresh || evidence.RestoreTested || evidence.EndedAt == nil {
		t.Fatalf("backup gate evidence = %#v, error=%v", evidence, err)
	}
	run, err := server.modules.backupStore.Run(context.Background(), evidence.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(run.Artifact); err != nil {
		t.Fatalf("Backups owner did not retain the gate artifact: %v", err)
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), source) || strings.Contains(string(raw), destination) || strings.Contains(string(raw), "fixture state") {
		t.Fatalf("backup evidence crossed the safe boundary: %s", raw)
	}

	gate.now = func() time.Time { return evidence.EndedAt.Add(2 * time.Hour) }
	stale, err := gate.Evaluate(context.Background(), deploy.BackupGateRequest{
		JobID: job.ID, MaxAgeSeconds: 60, PersistentSources: []string{source},
	})
	if err != nil || stale.Fresh || stale.Detail != "latest successful backup is older than the policy maximum" {
		t.Fatalf("stale backup evidence = %#v, error=%v", stale, err)
	}
	if _, err := gate.Evaluate(context.Background(), deploy.BackupGateRequest{
		JobID: job.ID, RequiredBeforeDeploy: true, PersistentSources: []string{t.TempDir()},
	}); err == nil {
		t.Fatal("backup gate accepted a job that does not cover the persistent source")
	}

	database, err := server.Store.DB.Exec(`
		INSERT INTO db_connections(name, driver, dsn_enc, created_at)
		VALUES('deployment-db', 'postgres', 'sealed-fixture', ?)`, time.Now().UTC().Unix())
	if err != nil {
		t.Fatal(err)
	}
	databaseID, _ := database.LastInsertId()
	observer := newDeploymentDependencyObserver(server.Store, server.modules.backupStore, nil)
	observed, err := observer.ObserveDependencies(context.Background(), []deploy.PlannedDependency{
		{Kind: "backup", Ownership: deploy.OwnershipLinked, ResourceKind: "backup_job", ResourceID: fmt.Sprint(job.ID), Config: json.RawMessage(`{"maxAgeSeconds":3600}`)},
		{Kind: "database", Ownership: deploy.OwnershipLinked, ResourceKind: "database_connection", ResourceID: fmt.Sprint(databaseID)},
		{Kind: "storage", Ownership: deploy.OwnershipLinked, ResourceKind: "bind_path", ResourceID: source},
		{Kind: "storage", Ownership: deploy.OwnershipObserved, ResourceKind: "docker_volume", ResourceID: "missing-volume"},
	})
	if err != nil || len(observed) != 4 {
		t.Fatalf("dependency observations = %#v, error=%v", observed, err)
	}
	if !observed[0].Available || !observed[0].Fresh || observed[0].Status != string(backups.StatusSuccess) ||
		!observed[1].Available || observed[1].Status != "deployment-db" || !observed[2].Available ||
		observed[3].Available || observed[3].Detail != "Docker volume inventory is unavailable" {
		t.Fatalf("owner dependency observations = %#v", observed)
	}
}
