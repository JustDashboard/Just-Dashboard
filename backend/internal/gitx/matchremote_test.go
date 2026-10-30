package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// matchFixture is a clone whose remote has moved on: main is ahead, a merged
// branch and an unmerged one are deleted there, and one is still published.
func matchFixture(t *testing.T) (source, checkout string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	source, git := tempRepo(t)
	git("branch", "-M", "main")
	git("checkout", "-qb", "merged")
	write(t, source, "merged.txt", "merged\n")
	git("add", ".")
	git("commit", "-qm", "merged work")
	git("checkout", "-q", "main")
	git("checkout", "-qb", "live")
	write(t, source, "live.txt", "live\n")
	git("add", ".")
	git("commit", "-qm", "live work")
	git("checkout", "-q", "main")
	checkout = filepath.Join(t.TempDir(), "checkout")
	git("clone", "-q", "--no-local", "--", source, checkout)
	pullGit(t, checkout, "checkout", "-q", "merged")
	pullGit(t, checkout, "checkout", "-q", "live")
	pullGit(t, checkout, "checkout", "-q", "-b", "unpublished", "main")
	write(t, checkout, "own.txt", "own\n")
	pullGit(t, checkout, "add", ".")
	pullGit(t, checkout, "commit", "-qm", "only here")

	git("merge", "-q", "--no-ff", "--no-edit", "merged")
	git("branch", "-qD", "merged")
	git("checkout", "-q", "live")
	write(t, source, "live.txt", "more\n")
	git("commit", "-qam", "more live work")
	git("checkout", "-q", "main")
	pullGit(t, checkout, "fetch", "-q", "--prune")
	return source, checkout
}

func matchNames(rows []MatchBranch) string {
	names := []string{}
	for _, row := range rows {
		names = append(names, row.Name)
	}
	return strings.Join(names, ",")
}

func TestMatchPlanSortsBranchesByWhatTheyWouldLose(t *testing.T) {
	_, checkout := matchFixture(t)
	plan, err := New([]string{checkout}).MatchPlan(context.Background(), checkout)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Remote != "origin" || plan.Branch != "main" || !plan.Switch {
		t.Errorf("plan = %+v, want origin, ending on main after a switch", plan)
	}
	if got := matchNames(plan.Delete); got != "merged" {
		t.Errorf("delete = %q, want the branch the remote merged and deleted", got)
	}
	if got := matchNames(plan.Update); got != "live,main" {
		t.Errorf("update = %q, want the two the remote moved", got)
	}
	if got := matchNames(plan.Unique); got != "unpublished" || plan.Unique[0].Commits != 1 {
		t.Errorf("unique = %+v, want unpublished with one commit", plan.Unique)
	}
}

func TestMatchRemoteKeepsWhatOnlyExistsHereUnlessNamed(t *testing.T) {
	source, checkout := matchFixture(t)
	pullGit(t, checkout, "update-ref", "refs/remotes/pr/7", "HEAD")
	s := New([]string{checkout})
	res, err := s.MatchRemote(context.Background(), checkout, nil)
	if err != nil {
		t.Fatalf("MatchRemote: %+v %v", res, err)
	}
	if got := pullGit(t, checkout, "branch", "--show-current"); got != "main" {
		t.Errorf("on %q, want main", got)
	}
	for _, branch := range []string{"main", "live"} {
		if got, want := pullGit(t, checkout, "rev-parse", branch), pullGit(t, source, "rev-parse", branch); got != want {
			t.Errorf("%s = %s, want the remote's %s", branch, got, want)
		}
	}
	branches := pullGit(t, checkout, "branch", "--format=%(refname:short)")
	if strings.Contains(branches, "merged") {
		t.Errorf("merged survived: %q", branches)
	}
	if !strings.Contains(branches, "unpublished") {
		t.Errorf("unpublished was deleted without being named: %q", branches)
	}
	if got := pullGit(t, checkout, "for-each-ref", "refs/remotes/pr"); got != "" {
		t.Errorf("stale ref survived: %q", got)
	}
	if !strings.Contains(res.Output, "Kept 1 branch") {
		t.Errorf("output = %q, want it to say what was kept", res.Output)
	}

	plan, err := s.MatchPlan(context.Background(), checkout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MatchRemote(context.Background(), checkout, []MatchChoice{{Name: "unpublished", Head: plan.Unique[0].Head}}); err != nil {
		t.Fatal(err)
	}
	if branches := pullGit(t, checkout, "branch", "--format=%(refname:short)"); strings.Contains(branches, "unpublished") {
		t.Errorf("unpublished survived being named: %q", branches)
	}
}

// A commit made after the dialog opened is not what the operator agreed to lose.
func TestMatchRemoteRefusesAChoiceMadeAtAnOlderCommit(t *testing.T) {
	_, checkout := matchFixture(t)
	s := New([]string{checkout})
	old := pullGit(t, checkout, "rev-parse", "unpublished")
	write(t, checkout, "newer.txt", "newer\n")
	pullGit(t, checkout, "add", ".")
	pullGit(t, checkout, "commit", "-qm", "made after the dialog opened")
	if _, err := s.MatchRemote(context.Background(), checkout, []MatchChoice{{Name: "unpublished", Head: old}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pullGit(t, checkout, "branch", "--format=%(refname:short)"), "unpublished") {
		t.Error("a branch that moved was discarded on the strength of its old commit")
	}
}

func TestMatchRemotePutsUncommittedWorkInAStash(t *testing.T) {
	_, checkout := matchFixture(t)
	write(t, checkout, "own.txt", "edited\n")
	write(t, checkout, "untracked.txt", "new\n")
	res, err := New([]string{checkout}).MatchRemote(context.Background(), checkout, nil)
	if err != nil {
		t.Fatalf("MatchRemote: %+v %v", res, err)
	}
	if got := pullGit(t, checkout, "stash", "list"); !strings.Contains(got, "Put aside to match origin") {
		t.Errorf("stash list = %q", got)
	}
	if !strings.Contains(res.Output, "2 uncommitted files") {
		t.Errorf("output = %q", res.Output)
	}
	if got := pullGit(t, checkout, "status", "--porcelain"); got != "" {
		t.Errorf("tree not clean: %q", got)
	}
}

func TestMatchRemoteFastForwardsTheBranchItIsAlreadyOn(t *testing.T) {
	source, checkout := matchFixture(t)
	pullGit(t, checkout, "checkout", "-q", "main")
	write(t, checkout, "ignored-by-nothing.txt", "keeps\n")
	if _, err := New([]string{checkout}).MatchRemote(context.Background(), checkout, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := pullGit(t, checkout, "rev-parse", "HEAD"), pullGit(t, source, "rev-parse", "main"); got != want {
		t.Errorf("HEAD = %s, want %s", got, want)
	}
}

// A linked worktree shares the refs but cannot stop being on its own branch,
// and neither checkout may pull a branch out from under the other.
func TestMatchRemoteLeavesBranchesOtherCheckoutsHold(t *testing.T) {
	_, checkout := matchFixture(t)
	tree := filepath.Join(t.TempDir(), "tree")
	pullGit(t, checkout, "worktree", "add", "-q", tree, "merged")
	s := New([]string{checkout, tree})
	plan, err := s.MatchPlan(context.Background(), checkout)
	if err != nil {
		t.Fatal(err)
	}
	if got := matchNames(plan.InUse); got != "merged" {
		t.Errorf("in use = %q", got)
	}
	if _, err := s.MatchRemote(context.Background(), checkout, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pullGit(t, checkout, "branch", "--format=%(refname:short)"), "merged") {
		t.Error("a branch held by a worktree was deleted")
	}

	plan, err = s.MatchPlan(context.Background(), tree)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Switch || plan.Branch != "" {
		t.Errorf("a linked worktree was told to switch: %+v", plan)
	}
}

func TestMatchPlanBlocksOnAnOperationInProgress(t *testing.T) {
	_, checkout := matchFixture(t)
	gitDir := strings.TrimSpace(pullGit(t, checkout, "rev-parse", "--absolute-git-dir"))
	if err := os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New([]string{checkout})
	plan, err := s.MatchPlan(context.Background(), checkout)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Blocked, "merge") {
		t.Errorf("blocked = %q", plan.Blocked)
	}
	if _, err := s.MatchRemote(context.Background(), checkout, nil); err == nil {
		t.Error("MatchRemote ran through an operation in progress")
	}
}

func TestCheckoutUpdatedFastForwardsAndSurvivesWhenItCannot(t *testing.T) {
	source, checkout := matchFixture(t)
	s := New([]string{checkout})
	if _, err := s.CheckoutUpdated(context.Background(), checkout, "main", ""); err != nil {
		t.Fatal(err)
	}
	if got, want := pullGit(t, checkout, "rev-parse", "HEAD"), pullGit(t, source, "rev-parse", "main"); got != want {
		t.Errorf("HEAD = %s, want %s", got, want)
	}
	// unpublished has no upstream at all.
	res, err := s.CheckoutUpdated(context.Background(), checkout, "unpublished", "")
	if err != nil || !strings.Contains(res.Output, "could not bring it up to date") {
		t.Errorf("res = %+v err = %v", res, err)
	}
	if got := pullGit(t, checkout, "branch", "--show-current"); got != "unpublished" {
		t.Errorf("on %q", got)
	}
}

// A checkout left on a gone upstream still ends on the remote's default branch
// rather than trying to delete the branch it is standing on.
func TestMatchRemoteKeepsTheDefaultBranchWhoseUpstreamWentAway(t *testing.T) {
	source, checkout := matchFixture(t)
	pullGit(t, checkout, "checkout", "-q", "main")
	pullGit(t, checkout, "config", "branch.main.merge", "refs/heads/retired")
	if _, err := New([]string{checkout}).MatchRemote(context.Background(), checkout, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := pullGit(t, checkout, "rev-parse", "main"), pullGit(t, source, "rev-parse", "main"); got != want {
		t.Errorf("main = %s, want %s", got, want)
	}
}
