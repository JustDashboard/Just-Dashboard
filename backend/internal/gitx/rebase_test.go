package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--git-editor" {
		if err := RunEditor(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRebaseRewordSquashDropAndRecovery(t *testing.T) {
	dir, git := tempRepo(t)
	ctx := context.Background()
	s := New([]string{dir})
	base := strings.TrimSpace(git("rev-parse", "HEAD"))
	for _, name := range []string{"one", "two", "three"} {
		write(t, dir, name, name+"\n")
		git("add", name)
		git("commit", "-m", name)
	}
	plan, err := s.RebasePlan(ctx, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	old := plan.Head
	plan.Items[0].Action = "reword"
	plan.Items[0].Message = "new subject\n\nliteral $(true)"
	plan.Items[1].Action = "squash"
	plan.Items[2].Action = "drop"
	res, err := s.StartRebase(ctx, dir, *plan)
	if err != nil {
		t.Fatal(res, err)
	}
	if got := strings.TrimSpace(git("rev-list", "--count", base+"..HEAD")); got != "1" {
		t.Fatal(got)
	}
	message := git("show", "--format=%B", "--no-patch", "HEAD")
	if !strings.Contains(message, "new subject") || !strings.Contains(message, "two") {
		t.Fatal(message)
	}
	if _, err := os.Stat(dir + "/three"); !os.IsNotExist(err) {
		t.Fatal("dropped commit retained")
	}
	if !strings.Contains(git("branch", "--contains", old), "jd-before-rebase-") {
		t.Fatal("recovery missing")
	}
}

func TestRebaseReorderAndRejectStaleOrPublished(t *testing.T) {
	dir, git := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	base := strings.TrimSpace(git("rev-parse", "HEAD"))
	for _, name := range []string{"one", "two"} {
		write(t, dir, name, name)
		git("add", name)
		git("commit", "-m", name)
	}
	plan, err := s.RebasePlan(ctx, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	plan.Items[0], plan.Items[1] = plan.Items[1], plan.Items[0]
	if _, err := s.StartRebase(ctx, dir, *plan); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git("log", "--format=%s", base+"..HEAD")); got != "one\ntwo" {
		t.Fatal(got)
	}
	if _, err := s.StartRebase(ctx, dir, *plan); err == nil {
		t.Fatal("accepted stale head")
	}
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	if _, err := s.RebasePlan(ctx, dir, base); err == nil {
		t.Fatal("allowed rewriting published commits")
	}
}

func TestRebaseInLinkedWorktree(t *testing.T) {
	dir, git := tempRepo(t)
	parent := t.TempDir()
	linked := filepath.Join(parent, "linked")
	ctx := context.Background()
	base := strings.TrimSpace(git("rev-parse", "HEAD"))
	write(t, dir, "local.txt", "local\n")
	git("add", "local.txt")
	git("commit", "-m", "Local commit")
	git("worktree", "add", "-b", "linked", linked, "HEAD")
	s := New([]string{dir, parent})
	plan, err := s.RebasePlan(ctx, linked, base)
	if err != nil {
		t.Fatal(err)
	}
	plan.Items[0].Action = "reword"
	plan.Items[0].Message = "Edited in linked checkout"
	if _, err := s.StartRebase(ctx, linked, *plan); err != nil {
		t.Fatal(err)
	}
	message, err := s.run(ctx, linked, "show", "--format=%s", "--no-patch", "HEAD")
	if err != nil || strings.TrimSpace(message) != "Edited in linked checkout" {
		t.Fatal(message, err)
	}
	if got := strings.TrimSpace(git("show", "--format=%s", "--no-patch", "HEAD")); got != "Local commit" {
		t.Fatal("main checkout changed", got)
	}
}

func TestRebaseConflictContinueAndAbort(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(fmt.Sprint(abort), func(t *testing.T) {
			dir, git := tempRepo(t)
			s := New([]string{dir})
			ctx := context.Background()
			base := strings.TrimSpace(git("rev-parse", "HEAD"))
			for _, text := range []string{"two\n", "three\n"} {
				write(t, dir, "a.txt", text)
				git("add", "a.txt")
				git("commit", "-m", text)
			}
			plan, err := s.RebasePlan(ctx, dir, base)
			if err != nil {
				t.Fatal(err)
			}
			old := plan.Head
			plan.Items[0], plan.Items[1] = plan.Items[1], plan.Items[0]
			if _, err := s.StartRebase(ctx, dir, *plan); err == nil {
				t.Fatal("expected conflict")
			}
			if got := s.operationInProgress(ctx, dir); got != "rebase" {
				t.Fatal(got)
			}
			if abort {
				if _, err := s.AbortOperation(ctx, dir); err != nil {
					t.Fatal(err)
				}
				if strings.TrimSpace(git("rev-parse", "HEAD")) != old {
					t.Fatal("abort did not restore head")
				}
				return
			}
			for attempts := 0; attempts < 3 && s.operationInProgress(ctx, dir) != ""; attempts++ {
				c, err := s.Conflict(ctx, dir, "a.txt")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.ResolveConflict(ctx, dir, ResolveConflictRequest{File: "a.txt", Version: c.Version, Choice: "result", Content: fmt.Sprintf("resolved %d\n", attempts)}); err != nil {
					t.Fatal(err)
				}
				_, _ = s.ContinueOperation(ctx, dir)
			}
			if s.operationInProgress(ctx, dir) != "" {
				t.Fatal("rebase did not finish")
			}
		})
	}
}
