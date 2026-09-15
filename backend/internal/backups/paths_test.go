package backups

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestBackupPathsAreContainedBeforeSaving(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s := NewStore(nil, nil, files.New([]string{root}))
	for _, paths := range [][2]string{
		{outside, root}, {root, outside},
		{filepath.Join(root, "link"), root},
		{root, filepath.Join(root, "link", "new", "nested")},
		{"", root},
	} {
		job := &Job{Name: "invalid", Sources: []string{paths[0]}, TargetKind: TargetLocal, Target: TargetConfig{Path: paths[1]}}
		// The nil database is intentional: a rejected path must not reach a write.
		if _, err := s.Create(t.Context(), job, nil); err == nil {
			t.Fatalf("saved uncontained paths: %v", paths)
		}
	}
}

func TestExistingBackupRechecksRootsAndCanBeRepaired(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root, outside := t.TempDir(), t.TempDir()
	old := NewStore(db, nil)
	job, err := old.Create(ctx, &Job{Name: "legacy", Sources: []string{outside},
		TargetKind: TargetLocal, Target: TargetConfig{Path: filepath.Join(root, "archives")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	restricted := NewStore(db, nil, files.New([]string{root}))
	if _, err := restricted.Get(ctx, job.ID); err != nil {
		t.Fatalf("invalid metadata must remain readable for repair: %v", err)
	}
	runner := NewRunner(restricted, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	run, err := runner.Execute(ctx, job.ID, "test")
	if err != nil || run.Status != StatusFailed || run.Artifact != "" {
		t.Fatalf("legacy job escaped narrowed roots: %#v, %v", run, err)
	}
	if _, err := os.Stat(job.Target.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blocked run touched its target: %v", err)
	}
	job.Sources = []string{root}
	if _, err := restricted.Update(ctx, job.ID, job, nil); err != nil {
		t.Fatalf("could not repair invalid legacy job: %v", err)
	}
	if err := restricted.Delete(ctx, job.ID); err != nil {
		t.Fatalf("could not remove legacy job: %v", err)
	}
}

func TestBackupArtifactReadsRespectCurrentRoots(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root, outside := t.TempDir(), t.TempDir()
	s := NewStore(db, nil, files.New([]string{root}))
	job, err := s.Create(ctx, &Job{Name: "archive", Sources: []string{root}, TargetKind: TargetLocal, Target: TargetConfig{Path: root}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.StartRun(ctx, job.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, id, StatusSuccess, filepath.Join(outside, "old.tar.gz"), 0, ""); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(s, t.TempDir(), slog.Default())
	if _, err := r.ListArchive(ctx, id, 1); !errors.Is(err, files.ErrOutsideRoot) {
		t.Fatalf("archive listing bypassed roots: %v", err)
	}
	if _, err := r.Restore(ctx, id, filepath.Join(root, "restore")); !errors.Is(err, files.ErrOutsideRoot) {
		t.Fatalf("archive restore bypassed roots: %v", err)
	}
}
