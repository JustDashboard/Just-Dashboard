package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/backups"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

func TestRecoveredBackupPolicyRequiresActualCompleteArtifactAndLeavesJobsUntouched(t *testing.T) {
	s := testServer(t)
	root := s.Cfg.FileRoots[0]
	source, other, destination := filepath.Join(root, "data"), filepath.Join(root, "other"), filepath.Join(root, "archives")
	for _, directory := range []string{source, other, destination} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "state.db"), []byte("owned fixture state"), 0600); err != nil {
		t.Fatal(err)
	}
	job, err := s.modules.backupStore.Create(t.Context(), &backups.Job{
		Name: "existing policy", Sources: []string{source}, TargetKind: backups.TargetLocal,
		Target: backups.TargetConfig{Path: destination}, Retention: 2, Enabled: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	newRecovery := func(paths ...string) *deploy.RecoveredWorkload {
		recovered := &deploy.RecoveredWorkload{Adoption: &deploy.WorkloadAdoption{}}
		for _, path := range paths {
			recovered.Configuration.Runtime.Mounts = append(recovered.Configuration.Runtime.Mounts, deploy.RuntimeMount{Source: path, Target: "/data"})
		}
		return recovered
	}
	withoutArchive := newRecovery(source)
	s.recoverExistingBackupPolicy(t.Context(), withoutArchive)
	if len(withoutArchive.Configuration.Dependencies) != 0 {
		t.Fatal("a job with no archive was presented as verified coverage")
	}
	run, err := s.modules.backupRunner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != backups.StatusSuccess {
		t.Fatalf("owned backup fixture: %+v %v", run, err)
	}
	recovered := newRecovery(source, source)
	s.recoverExistingBackupPolicy(t.Context(), recovered)
	if len(recovered.Configuration.Dependencies) != 1 || recovered.Configuration.Dependencies[0].Ownership != deploy.OwnershipLinked {
		t.Fatalf("verified complete existing policy was not linked: %+v", recovered.Configuration.Dependencies)
	}
	var policy map[string]any
	if err := json.Unmarshal(recovered.Configuration.Dependencies[0].Config, &policy); err != nil || policy["maxAgeSeconds"] != float64(recoveredBackupMaxAgeSeconds) || policy["requiredBeforeDeploy"] == true {
		t.Fatal("import prepared an unexpected job execution policy", policy, err)
	}
	last, err := s.modules.backupStore.LastRun(t.Context(), job.ID)
	if err != nil || last.ID != run.ID {
		t.Fatal("import executed or changed the backup job")
	}
	uncovered := newRecovery(source, other)
	s.recoverExistingBackupPolicy(t.Context(), uncovered)
	if len(uncovered.Configuration.Dependencies) != 0 {
		t.Fatal("partial data coverage was accepted")
	}
	if err := os.WriteFile(run.Artifact, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	tampered := newRecovery(source)
	s.recoverExistingBackupPolicy(t.Context(), tampered)
	if len(tampered.Configuration.Dependencies) != 0 {
		t.Fatal("a corrupted backup artifact was accepted")
	}
}
