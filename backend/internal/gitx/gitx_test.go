package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A ref reaches an argument vector, so anything that could be read as an
// option or escape the repository has to be refused before it gets there.
func TestValidateRef(t *testing.T) {
	ok := []string{
		"main", "feature/login", "release-1.2.3", "v2.0", "user@host",
		"a_b.c-d", "origin/main", "HEAD", "abc123def", "HEAD~1", "main^2",
	}
	for _, r := range ok {
		if err := ValidateRef(r); err != nil {
			t.Errorf("ValidateRef(%q) rejected a valid ref: %v", r, err)
		}
	}
	bad := []string{
		"", "--upload-pack=/bin/sh", "--exec=rm -rf /", "-x",
		"../../etc/passwd", "main..dev", "branch.lock",
		"has space", "semi;colon", "pipe|char", "dollar$sub", "back`tick",
		"quote'x", `dquote"x`, "new\nline", strings.Repeat("a", 256),
		"@{-1}", "main^{/fix}", "stash@{0}",
	}
	for _, r := range bad {
		if err := ValidateRef(r); err == nil {
			t.Errorf("ValidateRef(%q) accepted a ref it should refuse", r)
		}
	}
}

// The roots are the boundary an operator configured; a path outside them must
// be refused even when it really is a repository.
func TestResolveRejectsOutsideRoots(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "inside")
	mkRepo(t, repo)

	outside := t.TempDir()
	mkRepo(t, filepath.Join(outside, "other"))

	s := New([]string{dir})

	if _, err := s.Resolve(repo); err != nil {
		t.Errorf("repo inside a root was rejected: %v", err)
	}
	if _, err := s.Resolve(filepath.Join(outside, "other")); err == nil {
		t.Error("repo outside every root was accepted")
	}
	// A prefix match must not be a substring match: /tmp/xyz-evil is not
	// inside /tmp/xyz.
	sibling := dir + "-evil"
	if _, err := s.Resolve(sibling); err == nil {
		t.Error("a sibling directory sharing a name prefix was accepted")
	}
}

func TestResolveRejectsNonRepo(t *testing.T) {
	dir := t.TempDir()
	s := New([]string{dir})
	if _, err := s.Resolve(dir); err == nil {
		t.Error("a plain directory was accepted as a repository")
	}
}

// A remote may carry a token; it is rendered on a list page, so it is scrubbed.
func TestScrubRemote(t *testing.T) {
	cases := map[string]string{
		"https://user:ghp_secrettoken@github.com/a/b.git": "https://***@github.com/a/b.git",
		"https://github.com/a/b.git":                      "https://github.com/a/b.git",
		"git@github.com:a/b.git":                          "git@github.com:a/b.git",
		"":                                                "",
	}
	for in, want := range cases {
		if got := scrubRemote(in); got != want {
			t.Errorf("scrubRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTrackCount(t *testing.T) {
	cases := []struct {
		in, key string
		want    int
	}{
		{"[ahead 2, behind 1]", "ahead ", 2},
		{"[ahead 2, behind 1]", "behind ", 1},
		{"[ahead 12]", "ahead ", 12},
		{"[behind 3]", "ahead ", 0},
		{"", "ahead ", 0},
	}
	for _, c := range cases {
		if got := trackCount(c.in, c.key); got != c.want {
			t.Errorf("trackCount(%q,%q) = %d, want %d", c.in, c.key, got, c.want)
		}
	}
}

// A path list reaches an argument vector after `--`; a leading dash cannot
// become a flag there, but `..` still climbs out of the working tree, so both
// are refused before the command runs.
func TestValidatePaths(t *testing.T) {
	ok := [][]string{
		nil, {},
		{"main.go"}, {"a/b/c.txt", "d.md"}, {"weird name.txt"}, {".env"},
	}
	for _, in := range ok {
		if err := validatePaths(in); err != nil {
			t.Errorf("validatePaths(%q) rejected valid paths: %v", in, err)
		}
	}
	bad := [][]string{
		{""}, {"../escape"}, {"a/../../etc/passwd"}, {"/abs/path"}, {"-x"},
		{"ok.txt", "../bad"},
	}
	for _, in := range bad {
		if err := validatePaths(in); err == nil {
			t.Errorf("validatePaths(%q) accepted paths it should refuse", in)
		}
	}
}

// The stage → commit → unstage round-trip is the whole reason these operations
// exist, and each one's effect is only visible in what `git status` reports
// afterwards — so the flow is exercised end to end against a real repository.
// It skips where git is absent, the same bargain the term package strikes with
// tmux, because a fake git would pass while the product stayed broken.
func TestStageCommitUnstage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "t")
	run("config", "user.email", "t@e")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New([]string{dir})
	ctx := context.Background()

	// An untracked file: staging it makes it show up staged.
	if _, err := s.Stage(ctx, dir, []string{"a.txt"}); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	st, err := s.Status(ctx, dir)
	if err != nil {
		t.Fatalf("Status after stage: %v", err)
	}
	if len(st.Files) != 1 || !st.Files[0].Staged {
		t.Fatalf("after Stage, want one staged file, got %+v", st.Files)
	}

	// An empty message is refused rather than opening an editor the request
	// cannot answer.
	if _, err := s.Commit(ctx, dir, "  ", false); err == nil {
		t.Error("Commit accepted an empty message")
	}

	if _, err := s.Commit(ctx, dir, "add a.txt", false); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if st, err = s.Status(ctx, dir); err != nil || !st.Clean {
		t.Fatalf("after Commit the tree should be clean, got clean=%v err=%v", st.Clean, err)
	}

	// Modify, stage everything, then unstage it: the change survives, only the
	// index pointer moves back.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stage(ctx, dir, nil); err != nil {
		t.Fatalf("Stage all: %v", err)
	}
	if st, err = s.Status(ctx, dir); err != nil || len(st.Files) != 1 || !st.Files[0].Staged {
		t.Fatalf("after Stage all, want one staged file, got %+v (err %v)", st.Files, err)
	}
	if _, err := s.Unstage(ctx, dir, nil); err != nil {
		t.Fatalf("Unstage all: %v", err)
	}
	if st, err = s.Status(ctx, dir); err != nil || len(st.Files) != 1 || st.Files[0].Staged {
		t.Fatalf("after Unstage the file should be modified-not-staged, got %+v (err %v)", st.Files, err)
	}

	// A branch can be created and then deleted; the delete must actually remove
	// it from the branch listing.
	if _, err := s.CreateBranch(ctx, dir, "scratch", ""); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if _, err := s.Checkout(ctx, dir, "main"); err != nil {
		t.Fatalf("Checkout main: %v", err)
	}
	if _, err := s.DeleteBranch(ctx, dir, "scratch", true); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	branches, err := s.Branches(ctx, dir)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	for _, b := range branches {
		if b.Name == "scratch" {
			t.Fatalf("DeleteBranch left 'scratch' in the listing: %+v", branches)
		}
	}
}

func mkRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A local branch may contain a slash, so only the full refname distinguishes
// local from remote.
func TestBranchRemoteDetection(t *testing.T) {
	cases := map[string]bool{
		"refs/heads/main":               false,
		"refs/heads/fix/audit-findings": false,
		"refs/heads/feature/a/b":        false,
		"refs/remotes/origin/main":      true,
		"refs/remotes/upstream/dev":     true,
	}
	for refname, want := range cases {
		if got := strings.HasPrefix(refname, "refs/remotes/"); got != want {
			t.Errorf("%q classified remote=%v, want %v", refname, got, want)
		}
	}
}

// An untracked file has no earlier version, so `git diff` says nothing about
// it — which is exactly the file whose every line is a change. The diff shows
// it as the addition it is, and an untracked directory as each file in it.
func TestDiffShowsUntrackedFilesAsAdditions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "start")
	if err := os.WriteFile(filepath.Join(dir, "fresh.txt"), []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "inner.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New([]string{dir})
	ctx := context.Background()
	diff, err := s.Diff(ctx, dir, "", "fresh.txt", false)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, want := range []string{"diff --git a/fresh.txt b/fresh.txt", "new file mode", "+alpha", "+beta"} {
		if !strings.Contains(diff, want) {
			t.Errorf("untracked diff lacks %q:\n%s", want, diff)
		}
	}
	diff, err = s.Diff(ctx, dir, "", "pkg/", false)
	if err != nil {
		t.Fatalf("Diff of a directory: %v", err)
	}
	if !strings.Contains(diff, "+package pkg") {
		t.Errorf("untracked directory diff lacks its file:\n%s", diff)
	}
	// A tracked, unchanged file still diffs to nothing.
	run("add", "fresh.txt")
	run("commit", "-q", "-m", "add fresh")
	if diff, err = s.Diff(ctx, dir, "", "fresh.txt", false); err != nil || diff != "" {
		t.Errorf("clean tracked file: diff=%q err=%v", diff, err)
	}
}

// tempRepo makes a repository with one commit and hands back a git runner
// bound to it, for the tests that exercise real git rather than parsers.
func tempRepo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "t")
	git("config", "user.email", "t@e")
	write(t, dir, "a.txt", "one\n")
	git("add", "-A")
	git("commit", "-qm", "first")
	return dir, git
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The NUL-separated status is the one whose paths are literal. Without -z a
// file named café.txt arrived quoted and octal-escaped, which the page showed
// verbatim and then could not stage.
func TestParseStatusReadsLiteralPathsRenamesAndBothSides(t *testing.T) {
	out := " M café.txt\x00R  new name.txt\x00old name.txt\x00MM both.go\x00?? fresh/\x00AA clash.c\x00A  added.rs\x00"
	files := parseStatus(out)
	if len(files) != 6 {
		t.Fatalf("parsed %d entries, want 6: %+v", len(files), files)
	}
	if files[0].Path != "café.txt" || files[0].Staged || !files[0].Unstaged || files[0].Label != "modified" {
		t.Errorf("unicode path: %+v", files[0])
	}
	if files[1].Path != "new name.txt" || files[1].From != "old name.txt" || !files[1].Staged || files[1].Label != "renamed" {
		t.Errorf("rename: %+v", files[1])
	}
	// Staged and then edited again: both sides are true, so a client lists it
	// twice rather than hiding the second edit behind the first.
	if !files[2].Staged || !files[2].Unstaged || files[2].Index != "M" || files[2].Worktree != "M" {
		t.Errorf("MM file: %+v", files[2])
	}
	if files[3].Label != "untracked" || files[3].Staged || !files[3].Unstaged {
		t.Errorf("untracked dir: %+v", files[3])
	}
	// A both-added conflict carries no U, and used to be filed under "ready
	// to commit" — where the commit it promised could not happen.
	if files[4].Label != "conflicted" || files[4].Staged || !files[4].Unstaged {
		t.Errorf("AA conflict: %+v", files[4])
	}
	if !files[5].Staged || files[5].Unstaged || files[5].Label != "added" {
		t.Errorf("staged addition: %+v", files[5])
	}
}

func TestStatusAgainstARealRepository(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "café.txt", "x\n")
	write(t, dir, "a.txt", "two\n")
	git("add", "a.txt")
	write(t, dir, "a.txt", "three\n")

	s := New([]string{dir})
	ctx := context.Background()
	st, err := s.Status(ctx, dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	byPath := map[string]FileChange{}
	for _, f := range st.Files {
		byPath[f.Path] = f
	}
	if f, ok := byPath["café.txt"]; !ok || f.Label != "untracked" {
		t.Errorf("unicode untracked file: %+v (present %v)", f, ok)
	}
	if f := byPath["a.txt"]; !f.Staged || !f.Unstaged {
		t.Errorf("a file staged and edited again should be on both sides: %+v", f)
	}
	if st.Identity.Name != "t" || st.Identity.Email != "t@e" {
		t.Errorf("identity = %+v", st.Identity)
	}
	if st.Repo.Branch != "main" || st.Repo.Staged != 1 || st.Repo.Untracked != 1 || st.Repo.Changes != 2 {
		t.Errorf("summary = %+v", st.Repo)
	}
	// The unicode file can be staged by the name status reported.
	if _, err := s.Stage(ctx, dir, []string{"café.txt"}); err != nil {
		t.Fatalf("Stage unicode path: %v", err)
	}
}

// A fresh `git init` has a branch and no commits; the summary used to show
// neither, because rev-parse has nothing to abbreviate before the first
// commit.
func TestSummaryOfAnEmptyRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "trunk")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	r, err := New([]string{dir}).Summary(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if r.Branch != "trunk" || !r.Empty || r.Detached {
		t.Errorf("empty repo summary = %+v", r)
	}
}

// A linked worktree's checkout carries a `.git` file rather than a directory,
// and the walk used to step over it as an ordinary file.
func TestDiscoverFindsLinkedWorktrees(t *testing.T) {
	dir, git := tempRepo(t)
	root := t.TempDir()
	linked := filepath.Join(root, "linked")
	git("worktree", "add", "-q", "-b", "side", linked)

	repos, err := New([]string{root, dir}).Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	found := map[string]Repo{}
	for _, r := range repos {
		found[r.Path] = r
	}
	if _, ok := found[linked]; !ok {
		t.Errorf("linked worktree not discovered: %v", repos)
	}
	if r, ok := found[dir]; !ok || r.Branch != "main" {
		t.Errorf("main checkout: %+v (found %v)", r, ok)
	}
}

func TestLogSearchSkipAndFile(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "b.txt", "b\n")
	git("add", "-A")
	git("commit", "-qm", "Add the b file")
	write(t, dir, "a.txt", "changed\n")
	git("add", "-A")
	git("commit", "-qm", "Touch a again")

	s := New([]string{dir})
	ctx := context.Background()
	all, err := s.Log(ctx, dir, LogQuery{})
	if err != nil || len(all) != 3 {
		t.Fatalf("Log = %d commits, err %v", len(all), err)
	}
	if all[0].Files != 1 || all[0].Insert != 1 || all[0].Delete != 1 {
		t.Errorf("shortstat of the newest commit = %+v", all[0])
	}
	found, err := s.Log(ctx, dir, LogQuery{Search: "THE B"})
	if err != nil || len(found) != 1 || found[0].Subject != "Add the b file" {
		t.Errorf("Search = %+v, err %v", found, err)
	}
	// A term beginning with a dash is a term, not an option.
	if _, err := s.Log(ctx, dir, LogQuery{Search: "--upload-pack=x"}); err != nil {
		t.Errorf("a dashed search term was refused: %v", err)
	}
	page, err := s.Log(ctx, dir, LogQuery{Limit: 1, Skip: 1})
	if err != nil || len(page) != 1 || page[0].Subject != "Add the b file" {
		t.Errorf("Skip = %+v, err %v", page, err)
	}
	history, err := s.Log(ctx, dir, LogQuery{File: "a.txt"})
	if err != nil || len(history) != 2 {
		t.Errorf("file history = %+v, err %v", history, err)
	}
	if _, err := s.Log(ctx, dir, LogQuery{File: "../escape"}); err == nil {
		t.Error("a traversing file path was accepted")
	}
}

func TestShowListsWhatACommitChanged(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "b.txt", "b\nc\n")
	write(t, dir, "a.txt", "one\ntwo\n")
	git("add", "-A")
	git("commit", "-qm", "Second commit\n\nWith a body that explains it.")
	git("mv", "b.txt", "moved.txt")
	git("commit", "-qm", "Move b")

	s := New([]string{dir})
	ctx := context.Background()
	head, err := s.Show(ctx, dir, "HEAD~1")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if head.Subject != "Second commit" || head.Body != "With a body that explains it." {
		t.Errorf("subject/body = %q / %q", head.Subject, head.Body)
	}
	if len(head.ChangedFile) != 2 || head.Insert != 3 || head.Files != 2 {
		t.Fatalf("changes = %+v (ins %d files %d)", head.ChangedFile, head.Insert, head.Files)
	}
	byPath := map[string]ChangedFile{}
	for _, f := range head.ChangedFile {
		byPath[f.Path] = f
	}
	if byPath["b.txt"].Status != "added" || byPath["b.txt"].Insertions != 2 {
		t.Errorf("b.txt = %+v", byPath["b.txt"])
	}
	if byPath["a.txt"].Status != "modified" || byPath["a.txt"].Insertions != 1 {
		t.Errorf("a.txt = %+v", byPath["a.txt"])
	}
	moved, err := s.Show(ctx, dir, "HEAD")
	if err != nil {
		t.Fatalf("Show rename: %v", err)
	}
	if len(moved.ChangedFile) != 1 || moved.ChangedFile[0].Status != "renamed" ||
		moved.ChangedFile[0].From != "b.txt" || moved.ChangedFile[0].Path != "moved.txt" {
		t.Errorf("rename record = %+v", moved.ChangedFile)
	}
	// The root commit diffs against the empty tree rather than failing.
	root, err := s.Show(ctx, dir, "HEAD~2")
	if err != nil || len(root.ChangedFile) != 1 || root.ChangedFile[0].Status != "added" {
		t.Errorf("root commit = %+v, err %v", root, err)
	}
	// A commit's diff carries no header and a file narrows it.
	diff, err := s.Diff(ctx, dir, "HEAD~1", "a.txt", false)
	if err != nil || strings.Contains(diff, "Author:") || !strings.Contains(diff, "+two") || strings.Contains(diff, "b.txt") {
		t.Errorf("commit diff of one file = %q, err %v", diff, err)
	}
}

func TestTagsRoundTrip(t *testing.T) {
	dir, _ := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	if _, err := s.CreateTag(ctx, dir, "v1.0.0", "", "First release"); err != nil {
		t.Fatalf("CreateTag annotated: %v", err)
	}
	if _, err := s.CreateTag(ctx, dir, "marker", "HEAD", ""); err != nil {
		t.Fatalf("CreateTag lightweight: %v", err)
	}
	if _, err := s.CreateTag(ctx, dir, "--force", "", ""); err == nil {
		t.Error("an option-shaped tag name was accepted")
	}
	tags, err := s.Tags(ctx, dir)
	if err != nil || len(tags) != 2 {
		t.Fatalf("Tags = %+v, err %v", tags, err)
	}
	byName := map[string]Tag{}
	for _, tag := range tags {
		byName[tag.Name] = tag
	}
	if v := byName["v1.0.0"]; !v.Annotated || v.Message != "First release" || v.Commit == "" {
		t.Errorf("annotated tag = %+v", v)
	}
	if m := byName["marker"]; m.Annotated || m.Commit != byName["v1.0.0"].Commit {
		t.Errorf("lightweight tag = %+v", m)
	}
	if _, err := s.DeleteTag(ctx, dir, "marker"); err != nil {
		t.Fatalf("DeleteTag: %v", err)
	}
	if tags, _ = s.Tags(ctx, dir); len(tags) != 1 {
		t.Errorf("after delete, tags = %+v", tags)
	}
}

func TestStashesListApplyAndDrop(t *testing.T) {
	dir, _ := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	write(t, dir, "a.txt", "edited\n")
	if _, err := s.Stash(ctx, dir, "parked work"); err != nil {
		t.Fatalf("Stash: %v", err)
	}
	write(t, dir, "fresh.txt", "new\n")
	if _, err := s.Stash(ctx, dir, ""); err != nil {
		t.Fatalf("Stash unnamed: %v", err)
	}
	stashes, err := s.Stashes(ctx, dir)
	if err != nil || len(stashes) != 2 {
		t.Fatalf("Stashes = %+v, err %v", stashes, err)
	}
	if stashes[1].Index != 1 || stashes[1].Message != "parked work" || stashes[1].Branch != "main" {
		t.Errorf("named stash = %+v", stashes[1])
	}
	if stashes[0].Index != 0 || stashes[0].Branch != "main" {
		t.Errorf("unnamed stash = %+v", stashes[0])
	}
	diff, err := s.StashDiff(ctx, dir, 1)
	if err != nil || !strings.Contains(diff, "+edited") {
		t.Errorf("StashDiff = %q, err %v", diff, err)
	}
	if _, err := s.StashApply(ctx, dir, 1, false); err != nil {
		t.Fatalf("StashApply: %v", err)
	}
	if st, _ := s.Status(ctx, dir); st.Stashes != 2 || len(st.Files) != 1 {
		t.Errorf("after apply: stashes %d files %+v", st.Stashes, st.Files)
	}
	if _, err := s.StashDrop(ctx, dir, 1); err != nil {
		t.Fatalf("StashDrop: %v", err)
	}
	if stashes, _ = s.Stashes(ctx, dir); len(stashes) != 1 {
		t.Errorf("after drop = %+v", stashes)
	}
	if _, err := s.StashDrop(ctx, dir, -1); err == nil {
		t.Error("a negative stash index was accepted")
	}
}

// A merge that would conflict is abandoned, not left half-finished: the tree
// is back where it was and git's own words say what clashed.
func TestMergeAbortsOnConflict(t *testing.T) {
	dir, git := tempRepo(t)
	git("checkout", "-qb", "side")
	write(t, dir, "a.txt", "side\n")
	git("commit", "-qam", "side change")
	git("checkout", "-q", "main")
	write(t, dir, "a.txt", "main\n")
	git("commit", "-qam", "main change")

	s := New([]string{dir})
	ctx := context.Background()
	res, err := s.Merge(ctx, dir, "side")
	if err == nil {
		t.Fatal("a conflicting merge succeeded")
	}
	if res == nil || !strings.Contains(strings.ToLower(res.Output), "conflict") {
		t.Errorf("merge output did not name the conflict: %+v", res)
	}
	st, err := s.Status(ctx, dir)
	if err != nil || !st.Clean || st.Operation != "" {
		t.Errorf("after an aborted merge the tree should be clean with no operation: clean=%v op=%q err=%v",
			st.Clean, st.Operation, err)
	}

	// A clean merge lands.
	git("checkout", "-qb", "docs")
	write(t, dir, "README", "hi\n")
	git("add", "-A")
	git("commit", "-qm", "docs")
	git("checkout", "-q", "main")
	if _, err := s.Merge(ctx, dir, "docs"); err != nil {
		t.Fatalf("clean merge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "README")); err != nil {
		t.Error("merge did not bring the file in")
	}
}

func TestRevertAndCherryPick(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "b.txt", "b\n")
	git("add", "-A")
	git("commit", "-qm", "add b")
	s := New([]string{dir})
	ctx := context.Background()
	if _, err := s.Revert(ctx, dir, "HEAD"); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); !os.IsNotExist(err) {
		t.Error("revert left the file the commit added")
	}
	log, _ := s.Log(ctx, dir, LogQuery{})
	if len(log) != 3 || !strings.HasPrefix(log[0].Subject, "Revert") {
		t.Errorf("history after revert = %+v", log)
	}
	// Bring the reverted commit back by picking it.
	if _, err := s.CherryPick(ctx, dir, log[1].SHA); err != nil {
		t.Fatalf("CherryPick: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err != nil {
		t.Error("cherry-pick did not restore the file")
	}
}

func TestCompareCountsWhatABranchWouldBring(t *testing.T) {
	dir, git := tempRepo(t)
	git("checkout", "-qb", "feature")
	write(t, dir, "f.txt", "1\n2\n")
	git("add", "-A")
	git("commit", "-qm", "feature work")
	git("checkout", "-q", "main")
	write(t, dir, "m.txt", "m\n")
	git("add", "-A")
	git("commit", "-qm", "main moved")

	cmp, err := New([]string{dir}).Compare(context.Background(), dir, "main", "feature")
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if cmp.Ahead != 1 || cmp.Behind != 1 || len(cmp.Commits) != 1 || cmp.Commits[0].Subject != "feature work" {
		t.Errorf("Compare = %+v", cmp)
	}
	if cmp.Files != 1 || cmp.Insertions != 2 {
		t.Errorf("diffstat = files %d ins %d", cmp.Files, cmp.Insertions)
	}
	if _, err := New([]string{dir}).Compare(context.Background(), dir, "main..x", "feature"); err == nil {
		t.Error("a ref containing .. was accepted")
	}
}

func TestBranchesReportMergedGoneAndRemoteHalves(t *testing.T) {
	dir, git := tempRepo(t)
	git("checkout", "-qb", "merged-already")
	git("checkout", "-q", "main")
	git("checkout", "-qb", "ahead")
	write(t, dir, "x", "x\n")
	git("add", "-A")
	git("commit", "-qm", "ahead work")
	git("checkout", "-q", "main")
	git("remote", "add", "origin", "https://example.invalid/r.git")
	git("update-ref", "refs/remotes/origin/feature/thing", "HEAD")
	git("branch", "--set-upstream-to=origin/feature/thing", "ahead")
	git("update-ref", "-d", "refs/remotes/origin/feature/thing")
	git("update-ref", "refs/remotes/origin/main", "HEAD")

	branches, err := New([]string{dir}).Branches(context.Background(), dir)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	byName := map[string]Branch{}
	for _, b := range branches {
		byName[b.Name] = b
	}
	if !byName["merged-already"].Merged {
		t.Errorf("a branch at HEAD should read as merged: %+v", byName["merged-already"])
	}
	if byName["ahead"].Merged {
		t.Errorf("a branch with its own commit should not read as merged: %+v", byName["ahead"])
	}
	if !byName["ahead"].Gone {
		t.Errorf("a branch whose upstream was deleted should read as gone: %+v", byName["ahead"])
	}
	if r := byName["origin/main"]; !r.Remote || r.RemoteName != "origin" || r.Local != "main" {
		t.Errorf("remote branch halves = %+v", r)
	}
}

func TestCheckoutRemoteMakesATrackingBranch(t *testing.T) {
	dir, git := tempRepo(t)
	git("update-ref", "refs/remotes/origin/feature/x", "HEAD")
	git("remote", "add", "origin", "https://example.invalid/repo.git")
	s := New([]string{dir})
	ctx := context.Background()
	if _, err := s.CheckoutRemote(ctx, dir, "origin/feature/x", "feature/x"); err != nil {
		t.Fatalf("CheckoutRemote: %v", err)
	}
	if branch, _ := s.CurrentBranch(ctx, dir); branch != "feature/x" {
		t.Errorf("current branch = %q", branch)
	}
	if out := git("config", "--get", "branch.feature/x.merge"); strings.TrimSpace(out) != "refs/heads/feature/x" {
		t.Errorf("upstream not set: %q", out)
	}
	if _, err := s.RenameBranch(ctx, dir, "feature/x", "feature/y"); err != nil {
		t.Fatalf("RenameBranch: %v", err)
	}
	if branch, _ := s.CurrentBranch(ctx, dir); branch != "feature/y" {
		t.Errorf("after rename, current branch = %q", branch)
	}
}

func TestDiscardRemovesAnUntrackedFileAndRestoresATrackedOne(t *testing.T) {
	dir, _ := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	write(t, dir, "a.txt", "edited\n")
	write(t, dir, "junk/new.txt", "x\n")
	if _, err := s.Discard(ctx, dir, "a.txt"); err != nil {
		t.Fatalf("Discard tracked: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\n" {
		t.Errorf("tracked file not restored: %q", b)
	}
	if _, err := s.Discard(ctx, dir, "junk/"); err != nil {
		t.Fatalf("Discard untracked dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk")); !os.IsNotExist(err) {
		t.Error("untracked directory survived a discard")
	}
	if _, err := s.Discard(ctx, dir, "../outside"); err == nil {
		t.Error("a traversing path was accepted")
	}
	// A file whose name contains two dots is an ordinary file.
	write(t, dir, "v1..v2.diff", "d\n")
	if _, err := s.Stage(ctx, dir, []string{"v1..v2.diff"}); err != nil {
		t.Errorf("a file named with .. inside it was refused: %v", err)
	}
}

func TestResetHardWithCleanRemovesUntrackedFiles(t *testing.T) {
	dir, _ := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	write(t, dir, "a.txt", "edited\n")
	write(t, dir, "untracked.txt", "u\n")
	if _, err := s.Reset(ctx, dir, "HEAD", true, false); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "untracked.txt")); err != nil {
		t.Error("a hard reset without clean removed an untracked file")
	}
	if _, err := s.Reset(ctx, dir, "HEAD", true, true); err != nil {
		t.Fatalf("Reset with clean: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "untracked.txt")); !os.IsNotExist(err) {
		t.Error("reset with clean left the untracked file")
	}
}

func TestSetIdentityWritesRepositoryConfig(t *testing.T) {
	dir, git := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	if _, err := s.SetIdentity(ctx, dir, "Ada Lovelace", "ada@example.com"); err != nil {
		t.Fatalf("SetIdentity: %v", err)
	}
	if out := git("config", "--get", "user.name"); strings.TrimSpace(out) != "Ada Lovelace" {
		t.Errorf("user.name = %q", out)
	}
	for _, bad := range [][2]string{{"", "a@b"}, {"x", "nope"}, {"-x", "a@b"}, {"x", "a b@c"}} {
		if _, err := s.SetIdentity(ctx, dir, bad[0], bad[1]); err == nil {
			t.Errorf("SetIdentity(%q, %q) was accepted", bad[0], bad[1])
		}
	}
}

func TestRemotesRoundTrip(t *testing.T) {
	dir, _ := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	if _, err := s.AddRemote(ctx, dir, "origin", "https://user:token@example.com/o/r.git"); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	remotes, err := s.Remotes(ctx, dir)
	if err != nil || len(remotes) != 1 {
		t.Fatalf("Remotes = %+v, err %v", remotes, err)
	}
	if remotes[0].Name != "origin" || remotes[0].FetchURL != "https://***@example.com/o/r.git" || remotes[0].PushURL != "" {
		t.Errorf("remote = %+v", remotes[0])
	}
	if _, err := s.RemoveRemote(ctx, dir, "origin"); err != nil {
		t.Fatalf("RemoveRemote: %v", err)
	}
	if remotes, _ = s.Remotes(ctx, dir); len(remotes) != 0 {
		t.Errorf("after remove = %+v", remotes)
	}
	for _, bad := range []string{"", "-x", "a b", "a/b"} {
		if _, err := s.AddRemote(ctx, dir, bad, "https://example.com/x"); err == nil {
			t.Errorf("remote name %q was accepted", bad)
		}
	}
}

func TestValidateRemoteURL(t *testing.T) {
	for _, ok := range []string{
		"https://github.com/o/r.git", "http://git.local/r", "ssh://git@host:2222/r.git",
		"git@github.com:o/r.git", "git://host/r",
	} {
		if err := ValidateRemoteURL(ok); err != nil {
			t.Errorf("refused %q: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "-x", "--upload-pack=x", "/srv/other", "file:///srv/other", "ext::sh -c x",
		"https://h/r with space", "github.com/o/r", "host:path", "https://",
	} {
		if err := ValidateRemoteURL(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestNameFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/Wayy01/Just-Dashboard.git": "Just-Dashboard",
		"git@github.com:o/repo":                        "repo",
		"https://host/x/y/":                            "y",
	} {
		if got := nameFromURL(in); got != want {
			t.Errorf("nameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCloneRefusesWhatItMust(t *testing.T) {
	root := t.TempDir()
	s := New([]string{root})
	ctx := context.Background()
	if _, _, err := s.Clone(ctx, t.TempDir(), "https://example.invalid/r.git", "r"); err == nil {
		t.Error("a parent outside the roots was accepted")
	}
	if _, _, err := s.Clone(ctx, root, "/srv/other", "r"); err == nil {
		t.Error("a local path was accepted as a clone source")
	}
	if err := os.Mkdir(filepath.Join(root, "taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Clone(ctx, root, "https://example.invalid/r.git", "taken"); err == nil {
		t.Error("an existing target directory was accepted")
	}
	for _, bad := range []string{"..", ".hidden", "-x", "a/b", "a b"} {
		if _, _, err := s.Clone(ctx, root, "https://example.invalid/r.git", bad); err == nil {
			t.Errorf("directory name %q was accepted", bad)
		}
	}
}

func TestInitMakesARepositoryOnMain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})
	ctx := context.Background()
	if _, err := s.Init(ctx, dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	r, err := s.Summary(ctx, dir)
	if err != nil || r.Branch != "main" || !r.Empty {
		t.Errorf("after Init: %+v, err %v", r, err)
	}
	if _, err := s.Init(ctx, dir); err == nil {
		t.Error("initialising an existing repository was accepted")
	}
	if _, err := s.Init(ctx, t.TempDir()); err == nil {
		t.Error("a directory outside the roots was accepted")
	}
}

func TestCapDiffCutsAtALineBoundary(t *testing.T) {
	line := strings.Repeat("x", 99) + "\n"
	body := strings.Repeat(line, maxDiff/100+50)
	out := capDiff(body)
	if !strings.HasSuffix(out, "… diff truncated at 400 KB …") {
		t.Fatal("no truncation notice")
	}
	kept := strings.TrimSuffix(out, "\n\n… diff truncated at 400 KB …")
	if strings.HasSuffix(kept, "\n") || len(kept)%100 != 99 {
		t.Errorf("did not cut at a line boundary: tail %q", kept[len(kept)-20:])
	}
}
