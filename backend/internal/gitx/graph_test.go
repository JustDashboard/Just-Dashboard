package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// A branch that comes off an early commit and merges back should get a lane of
// its own for exactly the rows it is alive, and the merge should free it again
// rather than leaving the canvas permanently two lanes wide past the join.
func TestLayoutGraphLanes(t *testing.T) {
	// main:    C1 → C2 → C3 ─┐
	// feature: C1 → F1 → F2 ─┴ C4 (merge)
	commits := []Commit{
		{SHA: "C4", Parents: []string{"C3", "F2"}},
		{SHA: "C3", Parents: []string{"C2"}},
		{SHA: "C2", Parents: []string{"C1"}},
		{SHA: "F2", Parents: []string{"F1"}},
		{SHA: "F1", Parents: []string{"C1"}},
		{SHA: "C1", Parents: nil},
	}
	g := layoutGraph(commits)

	if g.Lanes != 2 {
		t.Fatalf("Lanes = %d, want 2", g.Lanes)
	}
	wantCol := map[string]int{"C4": 0, "C3": 0, "C2": 0, "F2": 1, "F1": 1, "C1": 0}
	for _, c := range g.Commits {
		if want := wantCol[c.SHA]; c.Col != want {
			t.Errorf("%s in lane %d, want %d", c.SHA, c.Col, want)
		}
		// The SVG is sized from Lanes; a dot in a higher lane is silently
		// clipped, so a col past the count is a bug the renderer cannot show.
		if c.Col >= g.Lanes {
			t.Errorf("%s in lane %d but the graph is only %d wide", c.SHA, c.Col, g.Lanes)
		}
	}

	// The merge's second edge runs down the lane the feature branch holds, and
	// the feature branch's last edge stays in that lane until it reaches C1 in
	// lane 0 — bending at F1 instead would draw it through C3 and C2.
	wantVia := map[string][]int{"C4": {0, 1}, "C3": {0}, "C2": {0}, "F2": {1}, "F1": {1}, "C1": nil}
	for _, c := range g.Commits {
		if !slices.Equal(c.ParentLanes, wantVia[c.SHA]) {
			t.Errorf("%s edges run down lanes %v, want %v", c.SHA, c.ParentLanes, wantVia[c.SHA])
		}
	}
}

// A root commit frees its own lane, and the trim that follows must not shrink
// the graph to narrower than the column that root was just drawn in: an orphan
// branch's only commit opens a lane and closes it on the same row.
func TestLayoutGraphCountsARootsLane(t *testing.T) {
	g := layoutGraph([]Commit{
		{SHA: "A", Parents: []string{"B"}},
		{SHA: "R", Parents: nil},
		{SHA: "B", Parents: nil},
	})
	for _, c := range g.Commits {
		if c.Col >= g.Lanes {
			t.Errorf("%s in lane %d but the graph is only %d wide", c.SHA, c.Col, g.Lanes)
		}
	}
}

// A single unbranched line stays in lane 0, and an empty history produces an
// empty graph rather than a panic.
func TestLayoutGraphLinear(t *testing.T) {
	g := layoutGraph([]Commit{
		{SHA: "b", Parents: []string{"a"}},
		{SHA: "a", Parents: nil},
	})
	if g.Lanes != 1 {
		t.Fatalf("Lanes = %d, want 1", g.Lanes)
	}
	for _, c := range g.Commits {
		if c.Col != 0 {
			t.Errorf("%s in lane %d, want 0", c.SHA, c.Col)
		}
	}

	if empty := layoutGraph(nil); empty.Lanes != 0 || len(empty.Commits) != 0 {
		t.Errorf("empty history = %+v, want zero graph", empty)
	}
}

// parseCommitLine reads the unit-separated record both Log and Graph share; a
// line that is not a commit (a --shortstat summary) is rejected.
func TestParseCommitLine(t *testing.T) {
	line := "abc123\x1fabc\x1fFix the thing\x1fAda\x1fada@x\x1f1700000000\x1fHEAD -> main, origin/main\x1fp1 p2"
	c, ok := parseCommitLine(line)
	if !ok {
		t.Fatal("parseCommitLine rejected a valid record")
	}
	if c.SHA != "abc123" || c.Subject != "Fix the thing" || c.Refs != "HEAD -> main, origin/main" {
		t.Fatalf("parsed wrong fields: %+v", c)
	}
	if len(c.Parents) != 2 || !c.IsMerge {
		t.Fatalf("parents = %v isMerge = %v, want two parents and a merge", c.Parents, c.IsMerge)
	}
	if c.At.Unix() != 1700000000 {
		t.Errorf("commit time = %d, want 1700000000", c.At.Unix())
	}

	if _, ok := parseCommitLine(" 3 files changed, 10 insertions(+)"); ok {
		t.Error("parseCommitLine accepted a --shortstat line as a commit")
	}
}

// Graph reads every ref, not just HEAD's history: a branch that was never
// merged still has to appear, or the view cannot show where it left off. Run
// against a real repository, skipping where git is absent — a fake git would
// pass while the traversal flags were wrong.
func TestGraphSpansEveryBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a")
	git("add", "-A")
	git("commit", "-qm", "first")
	git("checkout", "-qb", "side")
	write("b")
	git("add", "-A")
	git("commit", "-qm", "side work")
	git("checkout", "-q", "main")
	write("c")
	git("add", "-A")
	git("commit", "-qm", "main moves on")

	g, err := New([]string{dir}).Graph(context.Background(), dir, 50)
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	subjects := map[string]bool{}
	for _, c := range g.Commits {
		subjects[c.Subject] = true
	}
	if !subjects["side work"] {
		t.Errorf("Graph left out an unmerged branch's commit: %+v", g.Commits)
	}
	if len(g.Commits) != 3 || g.Lanes < 1 {
		t.Errorf("Graph = %d commits, %d lanes; want 3 commits and at least one lane", len(g.Commits), g.Lanes)
	}
}

// The graph is read a page at a time as the reader scrolls, and the pages are
// drawn as one canvas: a commit has to be in the same lane whether it arrived
// on the first page or the fourth, or every boundary is a visible break.
func TestGraphPagesKeepTheirLanes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-qm", "root", "--allow-empty")
	for _, branch := range []string{"one", "two"} {
		git("checkout", "-qb", branch, "main")
		for i := range 3 {
			git("commit", "-qm", branch+" work "+strconv.Itoa(i), "--allow-empty")
		}
	}
	git("checkout", "-q", "main")
	for i := range 3 {
		git("commit", "-qm", "main work "+strconv.Itoa(i), "--allow-empty")
	}
	git("merge", "-q", "--no-ff", "-m", "merge one", "one")

	svc := New([]string{dir})
	whole, err := svc.GraphPage(context.Background(), dir, GraphQuery{Limit: 50})
	if err != nil {
		t.Fatalf("GraphPage: %v", err)
	}
	if whole.Total != len(whole.Commits) || whole.HasMore {
		t.Fatalf("whole graph: total %d, %d commits, hasMore %v", whole.Total, len(whole.Commits), whole.HasMore)
	}
	want := map[string]int{}
	for _, c := range whole.Commits {
		want[c.SHA] = c.Col
	}

	seen := 0
	for skip := 0; ; skip += 3 {
		page, err := svc.GraphPage(context.Background(), dir, GraphQuery{Limit: 3, Skip: skip})
		if err != nil {
			t.Fatalf("GraphPage skip %d: %v", skip, err)
		}
		for _, c := range page.Commits {
			if c.Col != want[c.SHA] {
				t.Errorf("%q is in lane %d on the page at %d, lane %d in the whole graph", c.Subject, c.Col, skip, want[c.SHA])
			}
		}
		seen += len(page.Commits)
		if skip > 0 && page.Total != 0 {
			t.Errorf("page at %d counted the history again", skip)
		}
		if !page.HasMore {
			break
		}
	}
	if seen != len(whole.Commits) {
		t.Errorf("pages held %d commits, the whole graph %d", seen, len(whole.Commits))
	}

	// A search result is a list of matches, not a topology: one lane however
	// many unmatched commits sit between them.
	found, err := svc.GraphPage(context.Background(), dir, GraphQuery{Limit: 50, Search: "WORK 1"})
	if err != nil {
		t.Fatalf("GraphPage search: %v", err)
	}
	if len(found.Commits) != 3 || found.Total != 3 || found.Lanes != 1 {
		t.Fatalf("search: %d commits, total %d, %d lanes; want 3, 3, 1", len(found.Commits), found.Total, found.Lanes)
	}
	for _, c := range found.Commits {
		if c.Col != 0 || c.ParentLanes != nil {
			t.Errorf("search result %q laid out in lane %d with edges %v", c.Subject, c.Col, c.ParentLanes)
		}
	}
}

// refs/remotes/<remote>/HEAD is a symbolic ref, not a branch; it must not show
// up in the branch list as a phantom entry named after the remote.
func TestBranchesSkipsRemoteHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-qm", "init", "--allow-empty")
	// Fake a fetched remote: a tracking ref plus the origin/HEAD symref git
	// writes on clone.
	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	branches, err := New([]string{dir}).Branches(context.Background(), dir)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	for _, b := range branches {
		if b.Name == "origin" || b.Name == "origin/HEAD" {
			t.Fatalf("Branches listed the remote HEAD symref: %+v", branches)
		}
	}
}

// A branch checked out in another worktree cannot be deleted or switched to,
// even with -D. Branches reports the holding worktree so the UI can say so
// instead of offering a button that errors.
func TestBranchesReportsForeignWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-qm", "init", "--allow-empty")
	linked := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", "-b", "side", linked)

	branches, err := New([]string{dir}).Branches(context.Background(), dir)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	var side *Branch
	for i := range branches {
		if branches[i].Name == "side" {
			side = &branches[i]
		}
		if branches[i].Name == "main" && branches[i].Worktree != "" {
			t.Errorf("the current branch reported a worktree constraint: %q", branches[i].Worktree)
		}
	}
	if side == nil {
		t.Fatal("Branches dropped the branch held by the linked worktree")
	}
	if side.Worktree == "" {
		t.Errorf("Branches did not report 'side' as held by another worktree: %+v", side)
	}
}
