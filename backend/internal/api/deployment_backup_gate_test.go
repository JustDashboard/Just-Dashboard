package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestDeploymentBackupGateIncludesLinkedDatabaseData(t *testing.T) {
	s := testServer(t)
	source := filepath.Join(s.Cfg.FileRoots[0], "application-files")
	database := filepath.Join(s.Cfg.FileRoots[0], "database-files")
	for _, path := range []string{source, database} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	job, err := s.modules.backupStore.Create(t.Context(), &backups.Job{Name: "linked-database-coverage", Sources: []string{source}, TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: filepath.Join(s.Cfg.FileRoots[0], "output")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gate := newDeploymentBackupGate(s.modules.backupStore, s.modules.backupRunner, nil)
	request := deploy.BackupGateRequest{JobID: job.ID, RequiredBeforeDeploy: true, DatabaseConnections: []int64{17}}
	if _, err := gate.Evaluate(t.Context(), request); err == nil {
		t.Fatal("missing database backup adapter accepted")
	}
	gate.WithDatabaseSources(func(context.Context, int64) ([]string, error) { return []string{database}, nil })
	if _, err := gate.Evaluate(t.Context(), request); err == nil {
		t.Fatal("unrelated file backup covered a linked database")
	}
	job.Sources = append(job.Sources, database)
	if _, err := s.modules.backupStore.Update(t.Context(), job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	if evidence, err := gate.Evaluate(t.Context(), request); err != nil || evidence.Status != "success" {
		t.Fatalf("complete linked data coverage: %+v %v", evidence, err)
	}
	gate.WithDatabaseSources(func(context.Context, int64) ([]string, error) {
		return nil, errors.New("native remote database capture unavailable")
	})
	if _, err := gate.Evaluate(t.Context(), request); err == nil {
		t.Fatal("unresolved database was silently omitted")
	}
}

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
	gate := newDeploymentBackupGate(server.modules.backupStore, server.modules.backupRunner, nil)
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

func TestDeploymentBackupGateRejectsUncoveredNamedVolumesAndHistoricalCoverageChanges(t *testing.T) {
	server := testServer(t)
	root := server.Cfg.FileRoots[0]
	covered := filepath.Join(root, "covered")
	uncovered := filepath.Join(root, "production-data")
	for _, path := range []string{covered, uncovered} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	job, err := server.modules.backupStore.Create(t.Context(), &backups.Job{
		Name: "volume-coverage", Sources: []string{covered}, TargetKind: backups.TargetLocal,
		Target: backups.TargetConfig{Path: filepath.Join(root, "archives")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gate := newDeploymentBackupGate(server.modules.backupStore, server.modules.backupRunner, nil)
	gate.volumePath = func(_ context.Context, name string) (string, error) {
		switch name {
		case "production-data":
			return uncovered, nil
		case "backed-up-data":
			return covered, nil
		}
		return "", fmt.Errorf("volume not found")
	}
	for _, source := range []string{"production-data", "unknown-volume", uncovered, ""} {
		_, err := gate.Evaluate(t.Context(), deploy.BackupGateRequest{
			JobID: job.ID, RequiredBeforeDeploy: true, PersistentSources: []string{source},
		})
		if err == nil {
			t.Fatalf("uncovered persistent source was accepted: %q", source)
		}
	}
	evidence, err := gate.Evaluate(t.Context(), deploy.BackupGateRequest{
		JobID: job.ID, RequiredBeforeDeploy: true, PersistentSources: []string{"backed-up-data"},
	})
	if err != nil || evidence.ManifestDigest == "" {
		t.Fatalf("covered named volume failed: %#v, %v", evidence, err)
	}
	job.Sources = append(job.Sources, uncovered)
	if _, err := server.modules.backupStore.Update(t.Context(), job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Evaluate(t.Context(), deploy.BackupGateRequest{
		JobID: job.ID, MaxAgeSeconds: 3600, PersistentSources: []string{"production-data"},
	}); err == nil {
		t.Fatal("editing a job retroactively added named-volume coverage to an existing artifact")
	}
}

// A job that dumps the linked database natively covers it without the gate
// having to map the engine's files; the archive must really hold the dump.
func TestDeploymentBackupGateAcceptsNativeDatabaseDumps(t *testing.T) {
	s := testServer(t)
	source := filepath.Join(s.Cfg.FileRoots[0], "application-files")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	dumper := &gateFakeDumper{content: "PGDMP canary"}
	s.modules.backupStore.WithDatabaseValidator(dumper.DescribeDatabase)
	s.modules.backupRunner.WithDatabaseDumper(dumper)
	job, err := s.modules.backupStore.Create(t.Context(), &backups.Job{Name: "native-dump-coverage", Sources: []string{source}, DatabaseDumps: []int64{17},
		TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: filepath.Join(s.Cfg.FileRoots[0], "output")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gate := newDeploymentBackupGate(s.modules.backupStore, s.modules.backupRunner, nil)
	request := deploy.BackupGateRequest{JobID: job.ID, RequiredBeforeDeploy: true, DatabaseConnections: []int64{17}}
	evidence, err := gate.Evaluate(t.Context(), request)
	if err != nil || evidence.Status != "success" || len(evidence.DatabaseDumps) != 1 || evidence.DatabaseDumps[0] != 17 {
		t.Fatalf("native dump coverage: %+v %v", evidence, err)
	}
	if dumper.dumps != 1 {
		t.Fatalf("dumps taken = %d", dumper.dumps)
	}
	// A second linked database the job does not dump still needs file coverage.
	request.DatabaseConnections = []int64{17, 18}
	if _, err := gate.Evaluate(t.Context(), request); err == nil {
		t.Fatal("undumped database passed without file coverage")
	}
	gate.WithDatabaseSources(func(context.Context, int64) ([]string, error) { return nil, errors.New("external") })
	if _, err := gate.Evaluate(t.Context(), request); err == nil || !strings.Contains(err.Error(), "database dumps") {
		t.Fatalf("undumped external database passed or did not point at dumps: %v", err)
	}
	// An older run taken before the dump was configured cannot satisfy it.
	older := &backups.Job{Name: "older", Sources: []string{source}, TargetKind: backups.TargetLocal, Target: backups.TargetConfig{Path: filepath.Join(s.Cfg.FileRoots[0], "output-older")}}
	older, err = s.modules.backupStore.Create(t.Context(), older, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.modules.backupRunner.Execute(t.Context(), older.ID, "test"); err != nil {
		t.Fatal(err)
	}
	older.DatabaseDumps = []int64{17}
	if _, err := s.modules.backupStore.Update(t.Context(), older.ID, older, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Evaluate(t.Context(), deploy.BackupGateRequest{JobID: older.ID, MaxAgeSeconds: 86400, DatabaseConnections: []int64{17}}); err == nil {
		t.Fatal("an archive without the dump satisfied the gate")
	}
}

type gateFakeDumper struct {
	content string
	dumps   int
}

func (d *gateFakeDumper) DescribeDatabase(_ context.Context, id int64) (backups.DatabaseDescription, error) {
	return backups.DatabaseDescription{ID: id, Name: fmt.Sprintf("db-%d", id), Driver: "postgres", Database: "app"}, nil
}

func (d *gateFakeDumper) DumpDatabase(_ context.Context, id int64, directory string) (backups.DatabaseDumpResult, error) {
	d.dumps++
	path := filepath.Join(directory, "app.dump")
	if err := os.WriteFile(path, []byte(d.content), 0o600); err != nil {
		return backups.DatabaseDumpResult{}, err
	}
	return backups.DatabaseDumpResult{Description: backups.DatabaseDescription{ID: id, Name: fmt.Sprintf("db-%d", id), Driver: "postgres", Database: "app"}, Path: path, Method: "pg_dump"}, nil
}

func (d *gateFakeDumper) RestoreDatabase(context.Context, int64, string, string) (string, error) {
	return "", errors.New("not used")
}
