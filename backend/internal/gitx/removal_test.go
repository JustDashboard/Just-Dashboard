package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The walk skips hidden directories, which is where worktrees are usually
// put; they are found through the repository that records them, and say
// whose they are.
func TestDiscoverFindsWorktreesInHiddenDirectories(t *testing.T) {
	dir, git := tempRepo(t)
	hidden := filepath.Join(dir, ".worktrees", "task")
	git("worktree", "add", "-q", "-b", "task", hidden)
	gone := filepath.Join(dir, ".worktrees", "gone")
	git("worktree", "add", "-q", "-b", "gone", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	repos, err := New([]string{dir}).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	found := map[string]Repo{}
	for _, r := range repos {
		found[r.Path] = r
	}
	tree, ok := found[hidden]
	if !ok {
		t.Fatalf("worktree under .worktrees not discovered: %+v", repos)
	}
	if !tree.Worktree || tree.Main != dir || tree.Branch != "task" {
		t.Errorf("worktree summary = %+v", tree)
	}
	if main := found[dir]; main.Worktree || main.Main != "" {
		t.Errorf("main checkout flagged as a worktree: %+v", main)
	}
	if _, ok := found[gone]; ok {
		t.Error("a worktree whose checkout is gone was listed")
	}
	if len(repos) != 2 {
		t.Errorf("discovered %d checkouts, want 2: %+v", len(repos), repos)
	}
}

// A submodule's checkout carries a .git file too, but its git directory has
// no commondir: it is a repository of its own, not a worktree.
func TestSubmoduleCheckoutIsNotAWorktree(t *testing.T) {
	dir := t.TempDir()
	modules := filepath.Join(dir, "super", ".git", "modules", "lib")
	if err := os.MkdirAll(modules, 0o755); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(dir, "super", "lib")
	write(t, checkout, ".git", "gitdir: ../.git/modules/lib\n")
	if linked, main, _ := linkedWorktree(checkout); linked || main != "" {
		t.Errorf("submodule read as a worktree: linked=%v main=%q", linked, main)
	}
}

func TestLanguagesShareTrackedCodeAndSkipTheRest(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "main.go", strings.Repeat("g", 700))
	write(t, dir, "web/app.tsx", strings.Repeat("t", 270))
	write(t, dir, "run.sh", strings.Repeat("s", 29))
	write(t, dir, "Dockerfile", strings.Repeat("d", 1))
	write(t, dir, "README.md", strings.Repeat("m", 5000))
	write(t, dir, "data.json", strings.Repeat("j", 5000))
	write(t, dir, "vendor/lib/big.go", strings.Repeat("v", 5000))
	write(t, dir, "web/app.min.js", strings.Repeat("x", 5000))
	write(t, dir, "untracked.py", strings.Repeat("p", 5000))
	git("add", "main.go", "web", "run.sh", "Dockerfile", "README.md", "data.json", "vendor")
	git("commit", "-qm", "code")
	head := strings.TrimSpace(git("rev-parse", "--short", "HEAD"))

	s := New([]string{dir})
	got := s.Languages(context.Background(), dir, head)
	if len(got) != 3 || got[0].Name != "Go" || got[1].Name != "TypeScript" || got[2].Name != "Shell" {
		t.Fatalf("languages = %+v", got)
	}
	if got[0].Share != 0.699 && got[0].Share != 0.7 {
		t.Errorf("Go share = %v, want 0.7", got[0].Share)
	}
	total := 0.0
	for _, lang := range got {
		total += lang.Share
	}
	if total > 1 {
		t.Errorf("shares add up to %v", total)
	}
	// Under one per cent: the Dockerfile's single byte takes no slot.
	for _, lang := range got {
		if lang.Name == "Dockerfile" {
			t.Errorf("a share under 1%% was kept: %+v", got)
		}
	}

	// The same HEAD answers from the cache, even after the files change.
	write(t, dir, "main.go", "")
	if again := s.Languages(context.Background(), dir, head); again[0].Name != "Go" {
		t.Errorf("cached languages = %+v", again)
	}
	if fresh := s.Languages(context.Background(), dir, "other"); fresh[0].Name != "TypeScript" {
		t.Errorf("a new HEAD was answered from the cache: %+v", fresh)
	}
}

func TestRemovalCountsWorkThatExistsOnlyHere(t *testing.T) {
	dir, git := tempRepo(t)
	git("checkout", "-q", "-b", "side")
	write(t, dir, "b.txt", "b\n")
	git("add", "-A")
	git("commit", "-qm", "side work")
	git("checkout", "-q", "main")
	write(t, dir, "a.txt", "stashed\n")
	git("stash", "push", "-q")
	write(t, dir, "a.txt", "edited\n")
	write(t, dir, "new.txt", "new\n")

	rm, err := New([]string{filepath.Dir(dir)}).Removal(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("Removal: %v", err)
	}
	if rm.Changes != 2 || rm.Untracked != 1 || rm.Stashes != 1 || rm.Remotes != 0 {
		t.Errorf("counts = %+v", rm)
	}
	// No remote: both commits on main and the one on side exist only here.
	if rm.Unpushed != 2 || len(rm.LocalBranches) != 2 {
		t.Errorf("unpushed = %d, branches = %+v", rm.Unpushed, rm.LocalBranches)
	}
	if rm.Protected != "" || rm.Worktree || len(rm.Worktrees) != 0 {
		t.Errorf("an ordinary checkout = %+v", rm)
	}
}

func TestDeleteRepositoryRefusesWhatItMustNotRemove(t *testing.T) {
	dir, git := tempRepo(t)
	ctx := context.Background()

	// A root itself.
	if _, err := New([]string{dir}).DeleteRepository(ctx, dir, ""); !errors.Is(err, ErrProtected) {
		t.Errorf("deleting a root = %v", err)
	}
	s := New([]string{filepath.Dir(dir)})
	// The dashboard's own install, or anything holding it.
	if _, err := s.DeleteRepository(ctx, dir, filepath.Join(dir, "sub")); !errors.Is(err, ErrProtected) {
		t.Errorf("deleting the install checkout = %v", err)
	}
	// A repository whose worktrees would be orphaned.
	tree := filepath.Join(dir, ".worktrees", "task")
	git("worktree", "add", "-q", "-b", "task", tree)
	rm, err := s.Removal(ctx, dir, "")
	if err != nil || len(rm.Worktrees) != 1 || rm.Worktrees[0].Branch != "task" ||
		!strings.Contains(rm.Protected, tree) {
		t.Fatalf("removal with a worktree = %+v, %v", rm, err)
	}
	if _, err := s.DeleteRepository(ctx, dir, ""); !errors.Is(err, ErrProtected) {
		t.Errorf("deleting a repository with worktrees = %v", err)
	}
	// A locked worktree.
	git("worktree", "lock", tree)
	if _, err := s.DeleteRepository(ctx, tree, ""); !errors.Is(err, ErrProtected) {
		t.Errorf("deleting a locked worktree = %v", err)
	}
	git("worktree", "unlock", tree)
	// Another repository inside it.
	nested := filepath.Join(dir, "inner")
	write(t, nested, "x.txt", "x\n")
	if err := os.Mkdir(filepath.Join(nested, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	git("worktree", "remove", tree)
	rm, _ = s.Removal(ctx, dir, "")
	if len(rm.Nested) != 1 || rm.Nested[0] != nested || rm.Protected == "" {
		t.Errorf("nested checkout = %+v", rm)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("a refused delete removed the checkout: %v", err)
	}
}

func TestDeleteRepositoryRemovesAWorktreeThroughItsRepository(t *testing.T) {
	dir, git := tempRepo(t)
	tree := filepath.Join(dir, ".worktrees", "task")
	git("worktree", "add", "-q", "-b", "task", tree)
	write(t, tree, "dirty.txt", "uncommitted\n")
	s := New([]string{filepath.Dir(dir)})
	ctx := context.Background()

	rm, err := s.Removal(ctx, tree, "")
	if err != nil || !rm.Worktree || rm.Main != dir || rm.Untracked != 1 || rm.Unpushed != 0 {
		t.Fatalf("worktree removal preview = %+v, %v", rm, err)
	}
	if _, err := s.DeleteRepository(ctx, tree, ""); err != nil {
		t.Fatalf("delete worktree: %v", err)
	}
	if _, err := os.Stat(tree); !os.IsNotExist(err) {
		t.Errorf("worktree directory still there: %v", err)
	}
	if list := attachedWorktrees(dir); len(list) != 0 {
		t.Errorf("the repository still records the worktree: %v", list)
	}
	// Its branch belongs to the repository and survives.
	if out := git("branch", "--list", "task"); !strings.Contains(out, "task") {
		t.Errorf("the worktree's branch went with it: %q", out)
	}

	// With the worktree gone the repository itself can go.
	if _, err := s.DeleteRepository(ctx, dir, ""); err != nil {
		t.Fatalf("delete repository: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("repository directory still there: %v", err)
	}
}
