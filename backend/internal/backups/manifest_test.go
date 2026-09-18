package backups

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func manifestFixture(t *testing.T, sources []string, excludes []string) (*Store, *Runner, *Job, *Run) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewStore(db, nil)
	r := NewRunner(s, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	job, err := s.Create(t.Context(), &Job{Name: "manifest", Sources: sources, Excludes: excludes,
		TargetKind: TargetLocal, Target: TargetConfig{Path: t.TempDir()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.Execute(t.Context(), job.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	return s, r, job, run
}

func TestBackupManifestSeparatesSameBasenameSourcesAndVerifiesRestoredData(t *testing.T) {
	sources := []string{filepath.Join(t.TempDir(), "_data"), filepath.Join(t.TempDir(), "_data")}
	for i, source := range sources {
		if err := os.Mkdir(source, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "canary"), []byte(strings.Repeat("record", i+1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, runner, _, run := manifestFixture(t, sources, nil)
	if run.Status != StatusSuccess || run.Manifest == nil || len(run.Manifest.Sources) != 2 {
		t.Fatalf("missing successful manifest: %#v", run)
	}
	if err := runner.VerifyCoverage(t.Context(), run.ID, sources); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if _, err := runner.Restore(t.Context(), run.ID, dest); err != nil {
		t.Fatal(err)
	}
	for i, entry := range run.Manifest.Sources {
		got, err := os.ReadFile(filepath.Join(dest, entry.ArchivePath, "canary"))
		if err != nil || string(got) != strings.Repeat("record", i+1) {
			t.Fatalf("source %d lost data during restore: %q, %v", i, got, err)
		}
	}
}

func TestBackupManifestCannotGainCoverageFromLaterJobEditsOrNewPaths(t *testing.T) {
	source, added := t.TempDir(), t.TempDir()
	s, runner, job, run := manifestFixture(t, []string{source}, nil)
	job.Sources = append(job.Sources, added)
	if _, err := s.Update(t.Context(), job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	if err := runner.VerifyCoverage(t.Context(), run.ID, []string{added}); err == nil {
		t.Fatal("old archive inherited coverage from a job edit")
	}
	newPath := filepath.Join(source, "new-data")
	if err := os.Mkdir(newPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runner.VerifyCoverage(t.Context(), run.ID, []string{newPath}); err == nil {
		t.Fatal("old archive claimed a path that did not exist when archived")
	}
}

func TestBackupManifestRejectsMissingFilteredAndCorruptData(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		_, _, _, run := manifestFixture(t, []string{filepath.Join(t.TempDir(), "missing")}, nil)
		if run.Status != StatusFailed || run.Artifact != "" || run.Manifest.Complete {
			t.Fatalf("missing data was recorded as success: %#v", run)
		}
	})
	t.Run("filtered", func(t *testing.T) {
		source := t.TempDir()
		_, runner, _, run := manifestFixture(t, []string{source}, []string{"*.db"})
		if err := runner.VerifyCoverage(t.Context(), run.ID, []string{source}); err == nil {
			t.Fatal("filtered archive claimed complete data coverage")
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		source := t.TempDir()
		_, runner, _, run := manifestFixture(t, []string{source}, nil)
		if err := os.WriteFile(run.Artifact, []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runner.VerifyCoverage(t.Context(), run.ID, []string{source}); err == nil {
			t.Fatal("corrupt artifact passed verification")
		}
		if _, err := runner.Restore(t.Context(), run.ID, t.TempDir()); err == nil {
			t.Fatal("corrupt artifact was restored")
		}
	})
}

func TestBackupRestoreUsesTheArchivedTargetAfterJobDestinationChanges(t *testing.T) {
	s, runner, job, run := manifestFixture(t, []string{t.TempDir()}, nil)
	job.TargetKind, job.Target = TargetS3, TargetConfig{Bucket: "unused-bucket"}
	if _, err := s.Update(t.Context(), job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Restore(t.Context(), run.ID, t.TempDir()); err != nil {
		t.Fatalf("job edit redirected a local archive read: %v", err)
	}
}
