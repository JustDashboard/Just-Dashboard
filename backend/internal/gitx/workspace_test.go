package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryKeepsTheCurrentBranchAndFiles(t *testing.T) {
	dir, git := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	write(t, dir, "a.txt", "second\n")
	git("commit", "-qam", "second")
	lost := strings.TrimSpace(git("rev-parse", "HEAD"))
	git("reset", "--hard", "HEAD~1")
	write(t, dir, "a.txt", "unfinished\n")
	entries, err := s.Reflog(ctx, dir, 0)
	if err != nil || len(entries) < 3 || entries[1].SHA != lost || entries[0].At.IsZero() {
		t.Fatalf("reflog: %+v, %v", entries, err)
	}
	if _, err := s.Recover(ctx, dir, "rescued", entries[1].SHA); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git("rev-parse", "rescued")); got != lost {
		t.Fatalf("recovered %s, want %s", got, lost)
	}
	if got := strings.TrimSpace(git("branch", "--show-current")); got != "main" {
		t.Fatal(got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "unfinished\n" {
		t.Fatalf("overwrote work: %s", got)
	}
}

func TestBlamePagingAndUnsignedCommit(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "café file.txt", strings.Repeat("line\n", 205))
	git("add", "-A")
	git("commit", "-qm", "unicode path")
	s, ctx := New([]string{dir}), context.Background()
	blame, err := s.Blame(ctx, dir, "HEAD", "café file.txt", 1)
	if err != nil || len(blame.Lines) != 200 || !blame.HasMore || blame.Lines[0].Author != "t" || blame.Lines[0].Subject != "unicode path" {
		t.Fatalf("blame %+v, %v", blame, err)
	}
	page, err := s.Blame(ctx, dir, blame.Ref, blame.File, 201)
	if err != nil || len(page.Lines) != 5 || page.HasMore || page.Lines[0].Line != 201 {
		t.Fatalf("page %+v, %v", page, err)
	}
	if _, err := s.Blame(ctx, dir, "HEAD", "../secret", 1); err == nil {
		t.Fatal("accepted traversal")
	}
	sig, err := s.Signature(ctx, dir, "HEAD")
	if err != nil || sig.Status != "N" {
		t.Fatalf("signature %+v, %v", sig, err)
	}
}

func TestComparisonFreezesRefsAndListsRenames(t *testing.T) {
	dir, git := tempRepo(t)
	git("checkout", "-qb", "feature")
	git("mv", "a.txt", "renamed file.txt")
	write(t, dir, "extra.txt", "new\n")
	git("add", "-A")
	git("commit", "-qm", "rename and add")
	s, ctx := New([]string{dir}), context.Background()
	cmp, err := s.Compare(ctx, dir, "main", "feature")
	if err != nil || len(cmp.Changes) != 2 || cmp.BaseSHA == "" || cmp.HeadSHA == "" {
		t.Fatalf("compare %+v, %v", cmp, err)
	}
	var renamed bool
	for _, f := range cmp.Changes {
		if f.From == "a.txt" && f.Path == "renamed file.txt" {
			renamed = true
		}
	}
	if !renamed {
		t.Fatalf("lost rename: %+v", cmp.Changes)
	}
	write(t, dir, "extra.txt", "later\n")
	git("commit", "-qam", "later")
	diff, err := s.CompareDiff(ctx, dir, cmp.BaseSHA, cmp.HeadSHA, "extra.txt")
	if err != nil || !strings.Contains(diff, "+new") || strings.Contains(diff, "later") {
		t.Fatalf("unstable comparison: %s, %v", diff, err)
	}
}

func TestGraphSearchRefAndOlderPage(t *testing.T) {
	dir, git := tempRepo(t)
	git("commit", "--allow-empty", "-qm", "second")
	git("commit", "--allow-empty", "-qm", "third")
	s, ctx := New([]string{dir}), context.Background()
	page, err := s.GraphPage(ctx, dir, GraphQuery{Limit: 1})
	if err != nil || !page.HasMore || len(page.Commits) != 1 || page.Commits[0].Subject != "third" {
		t.Fatalf("page %+v, %v", page, err)
	}
	page, err = s.GraphPage(ctx, dir, GraphQuery{Limit: 1, Skip: 1})
	if err != nil || page.Commits[0].Subject != "second" {
		t.Fatalf("older %+v, %v", page, err)
	}
	page, err = s.GraphPage(ctx, dir, GraphQuery{Search: "FIRST", Ref: "main"})
	if err != nil || len(page.Commits) != 1 || page.Commits[0].Subject != "first" {
		t.Fatalf("search %+v, %v", page, err)
	}
	if _, err := s.GraphPage(ctx, dir, GraphQuery{Ref: "--all"}); err == nil {
		t.Fatal("accepted an option as a ref")
	}
}

func TestWorktreeLifecycleRefusesDirtyAndOutsideRoots(t *testing.T) {
	dir, _ := tempRepo(t)
	parent := t.TempDir()
	s, ctx := New([]string{dir, parent}), context.Background()
	if _, err := s.AddWorktree(ctx, dir, parent, "hotfix", "hotfix", "main", true); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "hotfix")
	list, err := s.Worktrees(ctx, dir)
	if err != nil || len(list) != 2 || list[1].Branch != "hotfix" || !list[1].Accessible {
		t.Fatalf("worktrees %+v, %v", list, err)
	}
	if _, err := s.RemoveWorktree(ctx, dir, dir); err == nil {
		t.Fatal("removed main worktree")
	}
	if _, err := New([]string{dir}).RemoveWorktree(ctx, dir, target); err == nil {
		t.Fatal("removed outside-root tree")
	}
	write(t, target, "unfinished.txt", "keep me")
	if _, err := s.RemoveWorktree(ctx, dir, target); err == nil {
		t.Fatal("removed dirty tree")
	}
	if _, err := os.Stat(filepath.Join(target, "unfinished.txt")); err != nil {
		t.Fatal("lost untracked file")
	}
	if err := os.Remove(filepath.Join(target, "unfinished.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RemoveWorktree(ctx, dir, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("worktree still present")
	}
	if _, err := s.AddWorktree(ctx, dir, parent, "../escape", "side", "main", true); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestRemoteUpdateAndUpstream(t *testing.T) {
	dir, git := tempRepo(t)
	s, ctx := New([]string{dir}), context.Background()
	git("remote", "add", "origin", "https://example.invalid/old.git")
	if _, err := s.SetRemoteURL(ctx, dir, "origin", "https://example.invalid/new.git"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git("remote", "get-url", "origin")); !strings.HasSuffix(got, "/new.git") {
		t.Fatal(got)
	}
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	if _, err := s.SetUpstream(ctx, dir, "main", "origin/main"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git("rev-parse", "--abbrev-ref", "@{upstream}")); got != "origin/main" {
		t.Fatal(got)
	}
	if _, err := s.SetUpstream(ctx, dir, "main", ""); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"/tmp/repo", "--upload-pack=bad", "https://***@example.invalid/repo"} {
		if _, err := s.SetRemoteURL(ctx, dir, "origin", url); err == nil {
			t.Fatalf("accepted %q", url)
		}
	}
}
