package selfupdate

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
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
	write(t, filepath.Join(upstream, "version.txt"), "0.5\n")
	git(t, upstream, "add", ".")
	git(t, upstream, "commit", "-q", "-m", "0.5")

	checkout = filepath.Join(root, "checkout")
	git(t, root, "clone", "-q", upstream, checkout)

	write(t, filepath.Join(upstream, "version.txt"), "0.6\n")
	git(t, upstream, "add", ".")
	git(t, upstream, "commit", "-q", "-m", "0.6")
	return upstream, checkout
}

func TestFastForwardMovesTheCheckoutOn(t *testing.T) {
	_, checkout := installed(t)
	var log bytes.Buffer

	commit, err := fastForward(context.Background(), checkout, "main", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if commit == "" {
		t.Error("the commit it landed on was not recorded")
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
	if _, err := fastForward(context.Background(), checkout, "main", &log); err != nil {
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
	_, err := fastForward(context.Background(), checkout, "main", &log)
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
	if _, err := fastForward(context.Background(), checkout, "main", &log); err == nil {
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
	first, err := fastForward(context.Background(), checkout, "main", &log)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fastForward(context.Background(), checkout, "main", &log)
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
