package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func pullGit(t *testing.T, dir string, args ...string) string {
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
	return strings.TrimSpace(string(out))
}

func pullFixture(t *testing.T) (source, checkout string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	source, git := tempRepo(t)
	git("branch", "patch/0.7.1")
	checkout = filepath.Join(t.TempDir(), "checkout")
	git("clone", "--no-local", "--", source, checkout)
	pullGit(t, checkout, "checkout", "--no-track", "-b", "patch/0.7.1", "HEAD")
	git("checkout", "patch/0.7.1")
	write(t, source, "a.txt", "remote patch\n")
	git("commit", "-qam", "advance remote patch")
	return source, checkout
}

func TestPullEstablishesMissingUpstream(t *testing.T) {
	source, checkout := pullFixture(t)
	// The remote branch need not already be in this checkout's fetched refs.
	pullGit(t, checkout, "update-ref", "-d", "refs/remotes/origin/patch/0.7.1")
	s := New([]string{checkout})
	result, err := s.Pull(context.Background(), checkout)
	if err != nil || result == nil || !result.OK {
		t.Fatalf("Pull: result=%+v err=%v", result, err)
	}
	if got, want := pullGit(t, checkout, "rev-parse", "HEAD"), pullGit(t, source, "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want remote patch %s", got, want)
	}
	if got := pullGit(t, checkout, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/patch/0.7.1" {
		t.Errorf("upstream = %q", got)
	}
	if _, err := s.Pull(context.Background(), checkout); err != nil {
		t.Fatalf("second Pull: %v", err)
	}
}

func TestPullMissingUpstreamSelectsRemote(t *testing.T) {
	emptyRemote := func(t *testing.T, checkout string) string {
		t.Helper()
		dir := t.TempDir()
		pullGit(t, checkout, "init", "--bare", dir)
		return dir
	}
	addMirror := func(t *testing.T, source, checkout string) {
		t.Helper()
		pullGit(t, checkout, "remote", "add", "mirror", source)
	}
	cases := []struct {
		name    string
		setup   func(t *testing.T, source, checkout string)
		remote  string
		failure string
	}{
		{
			name: "sole non-origin remote",
			setup: func(t *testing.T, _, checkout string) {
				pullGit(t, checkout, "remote", "rename", "origin", "mirror")
			},
			remote: "mirror",
		},
		{
			name: "remote fetch refspec does not include this branch",
			setup: func(t *testing.T, _, checkout string) {
				pullGit(t, checkout, "config", "remote.origin.fetch", "+refs/heads/main:refs/remotes/origin/main")
				pullGit(t, checkout, "update-ref", "-d", "refs/remotes/origin/patch/0.7.1")
			},
			remote: "origin",
		},
		{
			name: "branch remote selects between matches",
			setup: func(t *testing.T, source, checkout string) {
				addMirror(t, source, checkout)
				pullGit(t, checkout, "config", "branch.patch/0.7.1.remote", "mirror")
			},
			remote: "mirror",
		},
		{
			name: "checkout default selects between matches",
			setup: func(t *testing.T, source, checkout string) {
				addMirror(t, source, checkout)
				pullGit(t, checkout, "config", "checkout.defaultRemote", "mirror")
			},
			remote: "mirror",
		},
		{
			name: "branch remote takes precedence over checkout default",
			setup: func(t *testing.T, source, checkout string) {
				addMirror(t, source, checkout)
				pullGit(t, checkout, "config", "branch.patch/0.7.1.remote", "mirror")
				pullGit(t, checkout, "config", "checkout.defaultRemote", "origin")
			},
			remote: "mirror",
		},
		{
			name: "unique match ignores stale fetched branch",
			setup: func(t *testing.T, source, checkout string) {
				addMirror(t, source, checkout)
				pullGit(t, checkout, "remote", "set-url", "origin", emptyRemote(t, checkout))
			},
			remote: "mirror",
		},
		{
			name:    "ambiguous matches",
			setup:   addMirror,
			failure: "multiple remotes (mirror, origin)",
		},
		{
			name: "missing branch",
			setup: func(t *testing.T, _, checkout string) {
				pullGit(t, checkout, "remote", "set-url", "origin", emptyRemote(t, checkout))
			},
			failure: "no matching branch",
		},
		{
			name: "no remotes",
			setup: func(t *testing.T, _, checkout string) {
				pullGit(t, checkout, "remote", "remove", "origin")
			},
			failure: "no matching branch",
		},
		{
			name: "remote lookup fails",
			setup: func(t *testing.T, _, checkout string) {
				pullGit(t, checkout, "remote", "set-url", "origin", t.TempDir())
			},
			failure: "git ls-remote",
		},
		{
			name: "configured remote is unavailable",
			setup: func(t *testing.T, _, checkout string) {
				pullGit(t, checkout, "config", "branch.patch/0.7.1.remote", "gone")
			},
			failure: "Remote \"gone\" is not available",
		},
		{
			name: "configured remote has no match",
			setup: func(t *testing.T, source, checkout string) {
				addMirror(t, source, checkout)
				pullGit(t, checkout, "remote", "set-url", "origin", emptyRemote(t, checkout))
				pullGit(t, checkout, "config", "branch.patch/0.7.1.remote", "origin")
			},
			failure: "no matching branch",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, checkout := pullFixture(t)
			tc.setup(t, source, checkout)
			before := pullGit(t, checkout, "rev-parse", "HEAD")
			configPath := filepath.Join(checkout, ".git", "config")
			config, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			result, err := New([]string{checkout}).Pull(context.Background(), checkout)
			if tc.failure != "" {
				if err == nil || result == nil || result.OK || !strings.Contains(result.Output, tc.failure) {
					t.Fatalf("Pull: result=%+v err=%v, want %q", result, err, tc.failure)
				}
				if got := pullGit(t, checkout, "rev-parse", "HEAD"); got != before {
					t.Errorf("failed remote selection moved HEAD to %s", got)
				}
				if after, err := os.ReadFile(configPath); err != nil || string(after) != string(config) {
					t.Errorf("failed remote selection changed config: err=%v", err)
				}
				return
			}
			if err != nil || result == nil || !result.OK {
				t.Fatalf("Pull: result=%+v err=%v", result, err)
			}
			if got := pullGit(t, checkout, "rev-parse", "HEAD"); got != pullGit(t, source, "rev-parse", "HEAD") {
				t.Errorf("Pull did not reach the remote commit: %s", got)
			}
			if got := pullGit(t, checkout, "config", "branch.patch/0.7.1.remote"); got != tc.remote {
				t.Errorf("upstream remote = %q, want %q", got, tc.remote)
			}
			if got := pullGit(t, checkout, "config", "branch.patch/0.7.1.merge"); got != "refs/heads/patch/0.7.1" {
				t.Errorf("upstream merge ref = %q", got)
			}
		})
	}
}

func TestPullPreservesConfiguredUpstream(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(map[bool]string{false: "different branch", true: "gone branch"}[gone], func(t *testing.T) {
			_, checkout := pullFixture(t)
			ref := "refs/heads/main"
			if gone {
				ref = "refs/heads/deleted"
			}
			pullGit(t, checkout, "config", "branch.patch/0.7.1.remote", "origin")
			pullGit(t, checkout, "config", "branch.patch/0.7.1.merge", ref)
			before := pullGit(t, checkout, "rev-parse", "HEAD")
			result, err := New([]string{checkout}).Pull(context.Background(), checkout)
			if (err != nil) != gone {
				t.Fatalf("Pull: result=%+v err=%v, gone=%v", result, err, gone)
			}
			if got := pullGit(t, checkout, "rev-parse", "HEAD"); got != before {
				t.Errorf("Pull switched to the same-named remote branch: %s", got)
			}
			if got := pullGit(t, checkout, "config", "branch.patch/0.7.1.merge"); got != ref {
				t.Errorf("configured upstream changed from %q to %q", ref, got)
			}
		})
	}
}

func TestPullMissingUpstreamPreservesLocalWork(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "uncommitted work", true: "divergent commits"}[committed], func(t *testing.T) {
			_, checkout := pullFixture(t)
			write(t, checkout, "a.txt", "local work\n")
			if committed {
				pullGit(t, checkout, "commit", "-qam", "local change")
				pullGit(t, checkout, "config", "pull.rebase", "true")
			}
			before := pullGit(t, checkout, "rev-parse", "HEAD")
			result, err := New([]string{checkout}).Pull(context.Background(), checkout)
			if err == nil || result == nil || result.OK {
				t.Fatalf("Pull overwrote local work: result=%+v err=%v", result, err)
			}
			if got := pullGit(t, checkout, "rev-parse", "HEAD"); got != before {
				t.Errorf("failed Pull moved HEAD to %s", got)
			}
			if content, err := os.ReadFile(filepath.Join(checkout, "a.txt")); err != nil || string(content) != "local work\n" {
				t.Errorf("local work changed: content=%q err=%v", content, err)
			}
			if _, err := os.Stat(filepath.Join(checkout, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
				t.Errorf("failed Pull left a merge in progress: %v", err)
			}
		})
	}
}

func TestPullUnbornBranchEstablishesTracking(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	source, _ := tempRepo(t)
	checkout := t.TempDir()
	pullGit(t, checkout, "init", "-q", "-b", "main")
	pullGit(t, checkout, "remote", "add", "origin", source)
	result, err := New([]string{checkout}).Pull(context.Background(), checkout)
	if err != nil || result == nil || !result.OK {
		t.Fatalf("Pull: result=%+v err=%v", result, err)
	}
	if got, want := pullGit(t, checkout, "rev-parse", "HEAD"), pullGit(t, source, "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want %s", got, want)
	}
	if got := pullGit(t, checkout, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/main" {
		t.Errorf("upstream = %q", got)
	}
}

func TestPullDetachedHeadDoesNotEstablishTracking(t *testing.T) {
	_, checkout := pullFixture(t)
	pullGit(t, checkout, "checkout", "--detach", "HEAD")
	before := pullGit(t, checkout, "rev-parse", "HEAD")
	result, err := New([]string{checkout}).Pull(context.Background(), checkout)
	if err == nil || result == nil || result.OK || !strings.Contains(result.Output, "not currently on a branch") {
		t.Fatalf("Pull: result=%+v err=%v", result, err)
	}
	if got := pullGit(t, checkout, "rev-parse", "HEAD"); got != before {
		t.Errorf("detached HEAD moved to %s", got)
	}
}
