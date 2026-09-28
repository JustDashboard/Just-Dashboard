package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func conflictedRepo(t *testing.T) (string, *Service, func(...string) string) {
	t.Helper()
	dir, git := tempRepo(t)
	git("checkout", "-qb", "feature")
	write(t, dir, "a.txt", "incoming\n")
	git("commit", "-qam", "incoming")
	git("checkout", "-q", "main")
	write(t, dir, "a.txt", "current\n")
	git("commit", "-qam", "current")
	s := New([]string{dir})
	if _, err := s.StartOperation(context.Background(), dir, "merge", "feature"); err == nil {
		t.Fatal("expected a conflict")
	}
	return dir, s, git
}

func TestConflictCanBeEditedStagedAndContinued(t *testing.T) {
	dir, s, git := conflictedRepo(t)
	ctx := context.Background()
	conflict, err := s.Conflict(ctx, dir, "a.txt")
	if err != nil || conflict.Operation != "merge" || conflict.Base.Content != "one\n" || conflict.Ours.Content != "current\n" || conflict.Theirs.Content != "incoming\n" || !conflict.Editable {
		t.Fatalf("conflict %+v, %v", conflict, err)
	}
	if _, err := s.ContinueOperation(ctx, dir); err == nil {
		t.Fatal("continued unresolved merge")
	}
	if _, err := s.ResolveConflict(ctx, dir, ResolveConflictRequest{File: "a.txt", Version: conflict.Version, Choice: "result", Content: "current and incoming\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ContinueOperation(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if got := git("show", "HEAD:a.txt"); got != "current and incoming\n" {
		t.Fatal(got)
	}
	if op := s.operationInProgress(ctx, dir); op != "" {
		t.Fatal(op)
	}
	if got := strings.Fields(git("show", "--no-patch", "--format=%P", "HEAD")); len(got) != 2 {
		t.Fatalf("not a merge: %v", got)
	}
}

func TestConflictRejectsStaleEditsAndAbortRestoresTheBranch(t *testing.T) {
	dir, s, git := conflictedRepo(t)
	ctx := context.Background()
	conflict, err := s.Conflict(ctx, dir, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a.txt", "another editor's work\n")
	if _, err := s.ResolveConflict(ctx, dir, ResolveConflictRequest{File: "a.txt", Version: conflict.Version, Choice: "result", Content: "overwrite"}); err == nil {
		t.Fatal("accepted stale resolution")
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(body) != "another editor's work\n" {
		t.Fatal("lost a concurrent edit")
	}
	if _, err := s.AbortOperation(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if got := git("show", "HEAD:a.txt"); got != "current\n" {
		t.Fatal(got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "current\n" {
		t.Fatal("abort did not restore file")
	}
}

func TestConflictCanChooseSideOrDeletion(t *testing.T) {
	for _, choice := range []string{"ours", "theirs", "delete"} {
		t.Run(choice, func(t *testing.T) {
			dir, s, git := conflictedRepo(t)
			ctx := context.Background()
			conflict, err := s.Conflict(ctx, dir, "a.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ResolveConflict(ctx, dir, ResolveConflictRequest{File: "a.txt", Version: conflict.Version, Choice: choice}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ContinueOperation(ctx, dir); err != nil {
				t.Fatal(err)
			}
			if choice == "delete" {
				if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
					t.Fatal("file was not deleted")
				}
			} else {
				want := "current\n"
				if choice == "theirs" {
					want = "incoming\n"
				}
				if got := git("show", "HEAD:a.txt"); got != want {
					t.Fatalf("got %s, want %s", got, want)
				}
			}
		})
	}
}

func TestInteractiveOperationRefusesDirtyWorkAndInvalidPaths(t *testing.T) {
	dir, git := tempRepo(t)
	git("branch", "feature")
	s, ctx := New([]string{dir}), context.Background()
	write(t, dir, "untracked", "work")
	if _, err := s.StartOperation(ctx, dir, "merge", "feature"); err == nil {
		t.Fatal("started over uncommitted work")
	}
	for _, file := range []string{"../outside", ".git/config", "nested/.git/config", "/etc/passwd", "--help"} {
		if _, err := s.Conflict(ctx, dir, file); err == nil {
			t.Fatalf("accepted %s", file)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := repositoryEntry(dir, "escape/secret"); err == nil {
		t.Fatal("followed outside symlink")
	}
}
