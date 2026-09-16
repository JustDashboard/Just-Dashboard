package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func canaryDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

type recoveryFixtureChecker struct {
	directory  string
	calls      int
	cleanupErr error
	check      func(RecoveryCheckRequest) error
}

func (c *recoveryFixtureChecker) CheckRecovery(_ context.Context, request RecoveryCheckRequest) (RecoveryCheckResult, error) {
	c.directory, c.calls = request.Directory, c.calls+1
	if c.check != nil {
		if err := c.check(request); err != nil {
			return RecoveryCheckResult{}, err
		}
	}
	data, err := os.ReadFile(filepath.Join(request.Directory, "source-0001", "canary"))
	return RecoveryCheckResult{ImageDigest: request.Plan.Image, OutputDigest: canaryDigest(string(data))}, err
}

func (c *recoveryFixtureChecker) CleanupRecovery(context.Context, int64, string) error {
	return c.cleanupErr
}

func recoveryFixture(t *testing.T) (*Store, *Runner, *Job, *Run, *recoveryFixtureChecker, string) {
	t.Helper()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "canary"), []byte("known-record"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, runner, job, run := manifestFixture(t, []string{source}, nil)
	job.Recovery = &RecoveryPlan{Image: "sha256:" + strings.Repeat("a", 64), Command: []string{"/app/check"}, SchemaVersion: "schema-v1", ExpectedOutputDigest: canaryDigest("known-record")}
	job, err := s.Update(t.Context(), job.ID, job, nil)
	if err != nil {
		t.Fatal(err)
	}
	checker := &recoveryFixtureChecker{}
	runner.WithRecoveryChecker(checker)
	return s, runner, job, run, checker, source
}

func TestRestoreVerificationBindsArchivedDatasetImageSchemaAndCanary(t *testing.T) {
	s, runner, job, run, checker, source := recoveryFixture(t)
	if err := os.WriteFile(filepath.Join(source, "canary"), []byte("new-live-record"), 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := runner.VerifyRestore(t.Context(), run.ID)
	if err != nil || !record.Matches(run, job.Recovery) {
		t.Fatalf("verification=%+v error=%v", record, err)
	}
	if checker.directory == source || record.OutputDigest != canaryDigest("known-record") {
		t.Fatal("verification read live data rather than the archive")
	}
	if _, err := os.Stat(checker.directory); !os.IsNotExist(err) {
		t.Fatalf("temporary restoration was retained: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(source, "canary")); err != nil || string(data) != "new-live-record" {
		t.Fatal("verification changed live data")
	}
	loaded, err := s.Run(t.Context(), run.ID)
	if err != nil || loaded.RestoreVerification == nil || !loaded.RestoreVerification.Matches(loaded, job.Recovery) {
		t.Fatalf("evidence not persisted: %+v %v", loaded, err)
	}
	changedPlan := *job.Recovery
	changedPlan.SchemaVersion = "schema-v2"
	if record.Matches(run, &changedPlan) {
		t.Fatal("old verification accepted for a different schema")
	}
	changedPlan = *job.Recovery
	changedPlan.Image = "sha256:" + strings.Repeat("b", 64)
	if record.Matches(run, &changedPlan) {
		t.Fatal("old verification accepted for another application image")
	}
	otherRun := *run
	otherRun.ID++
	if record.Matches(&otherRun, job.Recovery) {
		t.Fatal("another artifact run borrowed restore evidence")
	}
	manifest := *run.Manifest
	manifest.Sources = append([]ManifestSource{}, run.Manifest.Sources...)
	manifest.Sources[0].Path = t.TempDir()
	otherRun = *run
	otherRun.Manifest = &manifest
	if record.Matches(&otherRun, job.Recovery) {
		t.Fatal("another dataset borrowed restore evidence")
	}
}

func TestRestoreVerificationRefusesCorruptionLimitsAndWrongCanary(t *testing.T) {
	for _, failure := range []string{"corrupt", "limit", "canary", "check", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			s, runner, job, run, checker, _ := recoveryFixture(t)
			ctx := t.Context()
			switch failure {
			case "corrupt":
				if err := os.WriteFile(run.Artifact, []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "limit":
				job.Recovery.MaxBytes = 1
			case "canary":
				job.Recovery.ExpectedOutputDigest = canaryDigest("another-record")
			case "check":
				checker.check = func(RecoveryCheckRequest) error { return errors.New("failed to open restored database") }
			case "cancel":
				cancelled, cancel := context.WithCancel(ctx)
				ctx = cancelled
				checker.check = func(RecoveryCheckRequest) error { cancel(); return cancelled.Err() }
			}
			if _, err := s.Update(t.Context(), job.ID, job, nil); err != nil {
				t.Fatal(err)
			}
			record, err := runner.VerifyRestore(ctx, run.ID)
			if err == nil || record.Matches(run, job.Recovery) {
				t.Fatalf("accepted %s: %+v %v", failure, record, err)
			}
			if failure == "corrupt" {
				if checker.calls != 0 {
					t.Fatal("corrupted archive reached the application")
				}
				return
			}
			stored, err := s.RestoreVerification(t.Context(), run.ID)
			if err != nil || stored == nil || stored.State != "failed" || !stored.CleanupComplete {
				t.Fatalf("failed check not recorded/cleaned: %+v %v", stored, err)
			}
		})
	}
}

func TestRestoreVerificationRetainsCleanupOwnershipAndRecoversInterruption(t *testing.T) {
	s, runner, job, run, checker, _ := recoveryFixture(t)
	checker.cleanupErr = errors.New("Docker is unavailable")
	record, err := runner.VerifyRestore(t.Context(), run.ID)
	if err == nil || record.State != "cleanup_failed" || record.Matches(run, job.Recovery) {
		t.Fatalf("cleanup failure was accepted: %+v %v", record, err)
	}
	if _, err := os.Stat(checker.directory); err != nil {
		t.Fatal("workspace deleted while a container could still be using it")
	}
	if err := s.Delete(t.Context(), job.ID); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("deleted cleanup ownership: %v", err)
	}
	job.Retention = 1
	if _, err := s.Update(t.Context(), job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Execute(t.Context(), job.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(run.Artifact); err != nil {
		t.Fatal("retention removed an artifact with unfinished recovery cleanup")
	}
	if _, err := s.st.DB.Exec(`UPDATE backup_restore_tests SET state='running' WHERE id=?`, record.ID); err != nil {
		t.Fatal(err)
	}
	checker.cleanupErr = nil
	if err := runner.RecoverRestoreChecks(t.Context()); err != nil {
		t.Fatal(err)
	}
	stored, err := s.RestoreVerification(t.Context(), run.ID)
	if err != nil || stored.State != "failed" || !stored.CleanupComplete || stored.Matches(run, job.Recovery) {
		t.Fatalf("interrupted verification promoted to success: %+v %v", stored, err)
	}
	if _, err := os.Stat(checker.directory); !os.IsNotExist(err) {
		t.Fatalf("recovered workspace retained: %v", err)
	}
}

func TestRestoreVerificationPinsArtifactAndRefusesConcurrentChecks(t *testing.T) {
	s, runner, job, run, checker, _ := recoveryFixture(t)
	job.Retention = 1
	if _, err := s.Update(t.Context(), job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	checker.check = func(RecoveryCheckRequest) error {
		if _, err := runner.VerifyRestore(t.Context(), run.ID); !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("concurrent verification accepted: %v", err)
		}
		if err := s.Delete(t.Context(), job.ID); !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("active verification lost its ownership record: %v", err)
		}
		if _, err := runner.Execute(t.Context(), job.ID, "test"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(run.Artifact); err != nil {
			t.Fatal("retention deleted the artifact while verification was reading it")
		}
		return nil
	}
	if _, err := runner.VerifyRestore(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Execute(t.Context(), job.ID, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(run.Artifact); !os.IsNotExist(err) {
		t.Fatalf("unpinned archive was not pruned: %v", err)
	}
}

func TestAutomaticRestoreChecksKeepArchiveAndVerificationOutcomesSeparate(t *testing.T) {
	s, runner, job, _, checker, _ := recoveryFixture(t)
	job.Recovery.Automatic = true
	if _, err := s.Update(t.Context(), job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	run, err := runner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != StatusSuccess || !run.RestoreVerification.Matches(run, job.Recovery) {
		t.Fatalf("automatic restore check: %+v %v", run, err)
	}
	checker.check = func(RecoveryCheckRequest) error { return errors.New("application check failed") }
	run, err = runner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != StatusSuccess || run.RestoreVerification == nil || run.RestoreVerification.State != "failed" {
		t.Fatalf("archive and verification outcomes were conflated: %+v %v", run, err)
	}
	if _, err := os.Stat(run.Artifact); err != nil {
		t.Fatal("a failed application check discarded the backup")
	}
}

func TestInterruptedBackupCannotRemainSuccessfulOrBlockDeletionForever(t *testing.T) {
	s, runner, job, _, _, _ := recoveryFixture(t)
	id, err := s.StartRun(t.Context(), job.ID, "test-interrupted")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(t.Context(), job.ID); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("active backup was forgotten: %v", err)
	}
	if err := runner.RecoverInterruptedRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, err := s.Run(t.Context(), id)
	if err != nil || run.Status != StatusFailed || run.EndedAt == nil || !strings.Contains(run.Log, "interrupted") {
		t.Fatalf("interrupted archive has no failure record: %+v %v", run, err)
	}
	if err := s.Delete(t.Context(), job.ID); err != nil {
		t.Fatalf("interrupted backup permanently blocked job deletion: %v", err)
	}
}
