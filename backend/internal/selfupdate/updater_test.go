package selfupdate

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The git half of an upgrade, against a real repository.
//
// This is the part with an operator's own edits in the blast radius, and every
// way it can be wrong is a way it destroys work that was never backed up. A
// fake git would answer whatever these tests assumed; a real one is the only
// thing that says what `--ff-only` actually does to a checkout with local
// changes in it. Skips where git is absent, so the suite stays green on a bare
// machine — the same bargain the live database and tmux tests make.

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, buf.String())
	}
	return buf.String()
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// release commits the version file and a changelog whose newest entry is version.
func release(t *testing.T, repo, version string) {
	t.Helper()
	write(t, filepath.Join(repo, "version.txt"), version+"\n")
	entries := []string{}
	for _, known := range []string{"0.5", "0.6", "0.7"} {
		if Compare(known, version) <= 0 {
			entries = append(entries, `{"version":"`+known+`","date":"2026-01-01","title":"t","changes":[{"kind":"fixed","text":"x"}]}`)
		}
	}
	write(t, filepath.Join(repo, ManifestPath), `{"latest":"`+version+`","releases":[`+strings.Join(entries, ",")+`]}`)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", version)
}

// installed builds an "upstream" repository with two commits and a checkout of
// its first, which is the shape of a dashboard one release behind.
func installed(t *testing.T) (upstream, checkout string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed on this machine")
	}
	root := t.TempDir()
	upstream = filepath.Join(root, "upstream")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, upstream, "init", "-q", "-b", "main")
	write(t, filepath.Join(upstream, "docker-compose.yml"), "name: just-dashboard\n")
	release(t, upstream, "0.5")

	checkout = filepath.Join(root, "checkout")
	git(t, root, "clone", "-q", upstream, checkout)

	release(t, upstream, "0.6")
	return upstream, checkout
}

// forward is fastForward as a fork's update runs it: with no tag on origin the
// branch stands in. The project's own repository passes tagOnly instead, and
// the tests for that say so.
func forward(checkout, version string, log io.Writer) (commit, landedOn string, err error) {
	landed, err := fastForward(context.Background(), checkout, request{ref: "main", version: version}, log)
	return landed.commit, landed.version, err
}

func TestFastForwardMovesTheCheckoutOn(t *testing.T) {
	_, checkout := installed(t)
	var log bytes.Buffer

	commit, version, err := forward(checkout, "0.6", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if commit == "" {
		t.Error("the commit it landed on was not recorded")
	}
	if version != "0.6" {
		t.Errorf("reported version %q", version)
	}
	got, err := os.ReadFile(filepath.Join(checkout, "version.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "0.6" {
		t.Fatalf("the checkout is still on %q", strings.TrimSpace(string(got)))
	}
	// The transcript is what an operator reads when this goes wrong, so it has
	// to name the commands that ran.
	for _, want := range []string{"git fetch", "git merge --ff-only origin/main"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("the transcript does not show %q:\n%s", want, log.String())
		}
	}
}

// The reason this is a fast-forward and not a reset. An operator's edited
// compose file is the normal state of a self-hosted install, and an upgrade
// that silently deleted it would be found out weeks later.
func TestFastForwardKeepsALocalEditThatDoesNotCollide(t *testing.T) {
	_, checkout := installed(t)
	local := filepath.Join(checkout, "docker-compose.yml")
	write(t, local, "name: just-dashboard\n# my own port mapping\n")

	var log bytes.Buffer
	if _, _, err := forward(checkout, "0.6", &log); err != nil {
		t.Fatalf("an unrelated local edit blocked the upgrade: %v\n%s", err, log.String())
	}
	got, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "my own port mapping") {
		t.Fatal("the operator's edit was destroyed by the upgrade")
	}
}

// And when it *would* collide, the upgrade stops rather than choosing a
// winner. The message has to say what to do about it, because the operator is
// reading it in a transcript with no dashboard in front of them.
func TestFastForwardRefusesToOverwriteACollidingEdit(t *testing.T) {
	_, checkout := installed(t)
	write(t, filepath.Join(checkout, "version.txt"), "mine\n")

	var log bytes.Buffer
	_, _, err := forward(checkout, "0.6", &log)
	if err == nil {
		t.Fatal("an upgrade overwrote a local edit to a file the release also changed")
	}
	if !strings.Contains(err.Error(), "commit, stash or revert") {
		t.Errorf("the refusal does not say what to do about it: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(checkout, "version.txt"))
	if strings.TrimSpace(string(got)) != "mine" {
		t.Fatalf("the local edit was destroyed anyway: %q", strings.TrimSpace(string(got)))
	}
}

// A checkout that has diverged — someone committed a change of their own on
// top — cannot be fast-forwarded, and silently rebasing or resetting it would
// be rewriting a stranger's history on their own server.
func TestFastForwardRefusesADivergedCheckout(t *testing.T) {
	_, checkout := installed(t)
	write(t, filepath.Join(checkout, "local-only.txt"), "mine\n")
	git(t, checkout, "add", ".")
	git(t, checkout, "commit", "-q", "-m", "a change of my own")

	var log bytes.Buffer
	if _, _, err := forward(checkout, "0.6", &log); err == nil {
		t.Fatal("a diverged checkout was fast-forwarded, which is not possible without losing a commit")
	}
	if _, err := os.Stat(filepath.Join(checkout, "local-only.txt")); err != nil {
		t.Fatal("the local commit was discarded")
	}
}

// Nothing new upstream is not a failure: the operator may simply have pulled
// by hand and never rebuilt, and rebuilding is exactly the fix.
func TestFastForwardOnACurrentCheckoutIsFine(t *testing.T) {
	_, checkout := installed(t)
	var log bytes.Buffer
	first, _, err := forward(checkout, "0.6", &log)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := forward(checkout, "0.6", &log)
	if err != nil {
		t.Fatalf("re-running an upgrade on an already-current checkout failed: %v", err)
	}
	if first != second {
		t.Fatalf("the commit moved from %s to %s with nothing new upstream", first, second)
	}
}

// git refuses to touch a repository owned by another account —
// "detected dubious ownership" — which is the normal case for a dashboard
// installed with sudo and inspected from a container. -c safe.directory is
// what makes every command in this package work at all, and it is scoped to
// the one directory rather than set globally.
func TestGitIsScopedToTheDirectoryItIsGiven(t *testing.T) {
	_, checkout := installed(t)
	// Read back only what the command set: a machine's own global or system
	// safe.directory (CI images set "*") would otherwise answer for it.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	out, err := gitOutput(context.Background(), checkout, "config", "--get-all", "safe.directory")
	if err != nil {
		t.Fatalf("git could not read back its own configuration: %v", err)
	}
	if strings.TrimSpace(out) != filepath.Clean(checkout) {
		t.Fatalf("safe.directory is %q, want just %q", strings.TrimSpace(out), checkout)
	}
}

func TestUpdaterRefusesAJobThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	if err := RunUpdater(dir); err == nil {
		t.Fatal("the updater ran with no job recorded")
	}
	store := NewStore(dir)
	if err := store.Save(&Run{ID: "x", Status: StatusSuccess}); err != nil {
		t.Fatal(err)
	}
	// Re-running a finished job would be a second unattended upgrade nobody
	// asked for — the case a rebooted machine would otherwise produce.
	if err := RunUpdater(dir); err == nil {
		t.Fatal("the updater replayed a job that had already finished")
	}
}

// The updater is root and the checkout is somebody's. What the update writes
// has to end up theirs, or their own next `git pull` stops halfway in the
// first folder this update created.
func TestFastForwardLeavesNothingRootOwnedBehind(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root, which is what the updater runs as")
	}
	upstream, checkout := installed(t)
	if err := os.MkdirAll(filepath.Join(upstream, "network", "drift"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(upstream, "network", "drift", "repairs.tsx"), "new\n")
	git(t, upstream, "add", ".")
	git(t, upstream, "commit", "-q", "-m", "0.7")
	const owner = 4242
	root := filepath.Dir(checkout)
	err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil || strings.HasPrefix(p, upstream) {
			return err
		}
		return os.Lchown(p, owner, owner)
	})
	if err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	if _, _, err := forward(checkout, "0.6", &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	var left []string
	_ = filepath.Walk(checkout, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Sys().(*syscall.Stat_t).Uid != owner {
			left = append(left, p)
		}
		return nil
	})
	if len(left) > 0 {
		t.Fatalf("not the owner's after the update: %v\n%s", left, log.String())
	}
}

// The updater is the old image, reading the changelog of the release it is
// about to install. A field that release added is not a reason to refuse it.
func TestAReleaseWithAChangelogThisVersionOnlyPartlyUnderstandsIsInstalled(t *testing.T) {
	upstream, checkout := installed(t)
	write(t, filepath.Join(upstream, ManifestPath), `{"latest":"0.7","schema":2,"releases":[
		{"version":"0.7","date":"2026-01-01","title":"t","signedBy":"k","changes":[{"kind":"performance","text":"x"}]},
		{"version":"0.5","date":"2026-01-01","title":"t","changes":[{"kind":"fixed","text":"x"}]}]}`)
	git(t, upstream, "add", ".")
	git(t, upstream, "commit", "-q", "-m", "0.7")

	var log bytes.Buffer
	_, version, err := forward(checkout, "0.7", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if version != "0.7" {
		t.Fatalf("landed on %q", version)
	}
}

// The project's repository keeps the screenshots of its pull requests on
// branches of their own, and an update that fetched every branch downloaded
// all of them. None of it is the release.
func TestAnUpdateFetchesTheBranchItFollowsAndNoOther(t *testing.T) {
	upstream, checkout := installed(t)
	git(t, upstream, "checkout", "-q", "-b", "pr-assets/shots")
	write(t, filepath.Join(upstream, "shot.png"), "a megabyte of pixels\n")
	git(t, upstream, "add", ".")
	git(t, upstream, "commit", "-q", "-m", "evidence")
	evidence := head(t, upstream, "HEAD")
	git(t, upstream, "checkout", "-q", "main")

	var log bytes.Buffer
	if _, version, err := forward(checkout, "0.6", &log); err != nil || version != "0.6" {
		t.Fatalf("version %q, err %v\n%s", version, err, log.String())
	}
	if refs := git(t, checkout, "for-each-ref", "refs/remotes/origin/pr-assets"); refs != "" {
		t.Errorf("the update fetched a branch it does not follow:\n%s", refs)
	}
	probe := exec.Command("git", "cat-file", "-e", evidence)
	probe.Dir = checkout
	if probe.Run() == nil {
		t.Errorf("the update downloaded %s, which only the other branch reaches", evidence)
	}
	if head(t, checkout, "refs/remotes/origin/main") != head(t, upstream, "main") {
		t.Error("origin/main was not brought up to date")
	}
}

// shallow is `installed` as the install guide now clones: the one branch, at
// depth one, so nothing below the commit it was cloned at is there.
func shallow(t *testing.T) (upstream, checkout string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed on this machine")
	}
	root := t.TempDir()
	upstream = filepath.Join(root, "upstream")
	if err := os.MkdirAll(upstream, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, upstream, "init", "-q", "-b", "main")
	write(t, filepath.Join(upstream, "docker-compose.yml"), "name: just-dashboard\n")
	release(t, upstream, "0.4")
	release(t, upstream, "0.5")

	checkout = filepath.Join(root, "checkout")
	// By address: git ignores --depth for a path and copies everything.
	git(t, root, "clone", "-q", "--depth", "1", "--single-branch", "--branch", "main", "file://"+upstream, checkout)
	if git(t, checkout, "rev-parse", "--is-shallow-repository") != "true\n" || git(t, checkout, "rev-list", "--count", "HEAD") != "1\n" {
		t.Fatal("the fixture is not a clone of depth one")
	}

	release(t, upstream, "0.6")
	return upstream, checkout
}

// What the install guide's clone has to survive: a release installed from its
// tag, with the branch already past it, and the way back when it fails. The
// fast-forward and the ancestor test in restoreCheckout both read history, and
// a shallow checkout has only what it was cloned at and what it fetched since.
func TestAShallowSingleBranchCheckoutUpdatesToATagAndGoesBack(t *testing.T) {
	upstream, checkout := shallow(t)
	from := head(t, checkout, "HEAD")
	tagged := head(t, upstream, "HEAD")
	git(t, upstream, "tag", "v0.6")
	release(t, upstream, "0.7")

	var log bytes.Buffer
	landed, err := fastForward(context.Background(), checkout, request{ref: "main", version: "0.6", tagOnly: true}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if landed.commit != tagged || landed.version != "0.6" || !landed.tagged {
		t.Fatalf("landed on %s as %s (tagged %v), want the tag %s as 0.6", landed.commit, landed.version, landed.tagged, tagged)
	}
	if err := restoreCheckout(context.Background(), checkout, from, landed.commit, &log); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if head(t, checkout, "HEAD") != from {
		t.Fatal("the checkout did not go back")
	}
}

func TestAShallowSingleBranchCheckoutFollowsAForksBranch(t *testing.T) {
	upstream, checkout := shallow(t)
	var log bytes.Buffer
	commit, version, err := forward(checkout, "0.6", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if commit != head(t, upstream, "main") || version != "0.6" {
		t.Fatalf("landed on %s as %s", commit, version)
	}
}

// The branch is not what the project's repository installs, so one that has
// gone (a patch branch deleted once it was merged) is no reason to refuse the
// tag. For a fork the branch is the release, and its absence is the answer.
func TestABranchThatIsGoneStopsAForkAndNotATaggedRelease(t *testing.T) {
	upstream, checkout := installed(t)
	tagged := head(t, upstream, "HEAD")
	git(t, upstream, "tag", "v0.6")

	var log bytes.Buffer
	landed, err := fastForward(context.Background(), checkout, request{ref: "patch/gone", version: "0.6", tagOnly: true}, &log)
	if err != nil || landed.commit != tagged {
		t.Fatalf("landed on %s, err %v\n%s", landed.commit, err, log.String())
	}
	if !strings.Contains(log.String(), "could not fetch the branch patch/gone") {
		t.Errorf("the transcript does not say the branch was passed over:\n%s", log.String())
	}

	_, other := installed(t)
	before := head(t, other, "HEAD")
	if _, err := fastForward(context.Background(), other, request{ref: "patch/gone", version: "0.6"}, &log); err == nil {
		t.Fatal("a fork was updated from a branch origin does not have")
	}
	if head(t, other, "HEAD") != before {
		t.Fatal("the checkout moved")
	}
}
