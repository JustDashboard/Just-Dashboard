package gitx

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The relay, against a repository served over HTTPS that answers 401 without
// the token: a credential is given only to git of the dashboard's own, in the
// relay, and the checkout gets what it fetched, and pushes, without one.

const relayToken = "ghp_relay_test_token_0123456789"

// relayGit runs git for a test's own setup, with no account configuration.
func relayGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Seed", "-c", "user.email=seed@example.test", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// relayRemote serves a bare repository holding main (two commits), dev and the
// tag v1, over TLS with the token required, and answers it, its address and a
// seed checkout that pushes to it.
func relayRemote(t *testing.T) (bare, remote, seed string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// The test server's certificate is its own; the account's configuration is
	// a file of the test's.
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "account-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	base := t.TempDir()
	bare = filepath.Join(base, "acme", "shop.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	relayGit(t, bare, "init", "--bare", "-q")
	relayGit(t, bare, "config", "http.receivepack", "true")
	seed = filepath.Join(t.TempDir(), "seed")
	relayGit(t, filepath.Dir(seed), "clone", "-q", bare, "seed")
	write(t, seed, "app.js", "console.log('one')\n")
	relayGit(t, seed, "add", "-A")
	relayGit(t, seed, "commit", "-q", "-m", "Begin")
	relayGit(t, seed, "tag", "-a", "v1", "-m", "v1")
	write(t, seed, "app.js", "console.log('two')\n")
	relayGit(t, seed, "commit", "-qam", "Two")
	relayGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/dev", "refs/tags/v1")
	execPath := relayGit(t, base, "--exec-path")
	backend := &cgi.Handler{
		Path: filepath.Join(execPath, "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + base, "GIT_HTTP_EXPORT_ALL=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "REMOTE_USER=deploy"},
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+relayToken))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return bare, server.URL + "/acme/shop.git", seed
}

// relayCredential is the environment deploy.GitHeaderEnvironment makes.
func relayCredential(remote string) map[string]string {
	return map[string]string{
		"GIT_CONFIG_COUNT":   "2",
		"GIT_CONFIG_KEY_0":   "credential.helper",
		"GIT_CONFIG_VALUE_0": "",
		"GIT_CONFIG_KEY_1":   "http." + remote + ".extraHeader",
		"GIT_CONFIG_VALUE_1": "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+relayToken)),
	}
}

// logEveryGit puts a git (and a git-lfs) first on PATH that writes, for each
// run, whether its environment holds an Authorization, its folder and its
// GIT_DIR, then runs the real one.
func logEveryGit(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "runs.log")
	script := "#!/bin/sh\ncred=0\ncase \"$(env)\" in *Authorization*) cred=1;; esac\n" +
		"printf '%s|%s|%s\\n' \"$cred\" \"$(pwd -P)\" \"$GIT_DIR\" >> " + log + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// And git-lfs, which git runs from PATH as a filter or a subcommand.
	if lfs, err := exec.LookPath("git-lfs"); err == nil {
		wrapped := strings.ReplaceAll(script, "exec "+real, "exec "+lfs)
		if err := os.WriteFile(filepath.Join(dir, "git-lfs"), []byte(wrapped), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(os.PathSeparator))
}

func realDir(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestARelayClonesFetchesAndPushesWithTheCredentialOnlyInItsOwnGit(t *testing.T) {
	bare, remote, seed := relayRemote(t)
	log := logEveryGit(t)
	ctx := context.Background()
	root := realDir(t, t.TempDir())
	base := filepath.Join(realDir(t, t.TempDir()), "workspace-git")
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, base, remote)
	if err != nil {
		t.Fatal(err)
	}
	extra := relayCredential(remote)

	folder, err := s.CloneThrough(ctx, relay, root, "shop", RelayCloneOptions{}, extra)
	if err != nil {
		t.Fatal(err)
	}
	inFolder := func(args ...string) string { t.Helper(); return relayGit(t, folder, args...) }
	if got := inFolder("rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("the clone is on %q, not the remote's default", got)
	}
	if got := inFolder("rev-parse", "--abbrev-ref", "main@{upstream}"); got != "origin/main" {
		t.Fatalf("main tracks %q", got)
	}
	if got := inFolder("remote", "get-url", "origin"); got != remote {
		t.Fatalf("origin is %q, not the remote", got)
	}
	if got := inFolder("cat-file", "-t", "v1"); got != "tag" {
		t.Fatalf("the tag v1 came as %q", got)
	}
	if raw, _ := os.ReadFile(filepath.Join(folder, "app.js")); string(raw) != "console.log('two')\n" {
		t.Fatalf("app.js = %q", raw)
	}
	inFolder("rev-parse", "--verify", "refs/remotes/origin/dev")

	// The remote moves on and drops dev: a fetch follows it, pruned.
	write(t, seed, "app.js", "console.log('three')\n")
	relayGit(t, seed, "commit", "-qam", "Three")
	relayGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main", ":refs/heads/dev")
	if _, err := s.FetchThrough(ctx, relay, folder, extra); err != nil {
		t.Fatal(err)
	}
	if got, want := inFolder("rev-parse", "origin/main"), relayGit(t, seed, "rev-parse", "HEAD"); got != want {
		t.Fatalf("origin/main = %s after the fetch, the remote's is %s", got, want)
	}
	if out := inFolder("for-each-ref", "refs/remotes/origin/dev"); out != "" {
		t.Fatalf("origin/dev was kept: %s", out)
	}

	// A new branch is pushed as itself, and tracks what it was pushed to.
	inFolder("checkout", "-q", "--no-track", "-b", "feature", "origin/main")
	write(t, folder, "feature.txt", "new\n")
	inFolder("add", "-A")
	inFolder("commit", "-q", "-m", "Feature")
	head := inFolder("rev-parse", "HEAD")
	if _, err := s.PushThrough(ctx, relay, folder, "", extra); err != nil {
		t.Fatal(err)
	}
	if got := relayGit(t, bare, "rev-parse", "refs/heads/feature"); got != head {
		t.Fatalf("the remote's feature = %s, want %s", got, head)
	}
	if got := inFolder("rev-parse", "--abbrev-ref", "feature@{upstream}"); got != "origin/feature" {
		t.Fatalf("feature tracks %q", got)
	}
	if got := inFolder("rev-parse", "origin/feature"); got != head {
		t.Fatalf("origin/feature = %s after the push, want %s", got, head)
	}
	// Nothing of the credential is kept in the checkout.
	if config, _ := os.ReadFile(filepath.Join(folder, ".git", "config")); strings.Contains(string(config), "Authorization") {
		t.Fatalf("the checkout kept the credential: %s", config)
	}

	// Every git run with the credential ran in the relay; none in the checkout.
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	credentialed := 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 || parts[0] != "1" {
			continue
		}
		credentialed++
		if !within(parts[1], base) || !within(parts[2], base) || within(parts[1], root) {
			t.Fatalf("a git with the credential ran in %s (GIT_DIR %q), outside the relay %s", parts[1], parts[2], base)
		}
	}
	if credentialed == 0 {
		t.Fatalf("no git ran with the credential:\n%s", raw)
	}
}

// The relay pushes a commit to a whole branch name, never forced: a branch
// called "+main" is a name, and a push the remote refuses because it moved is
// refused with git's words.
func TestARelayPushNeverForces(t *testing.T) {
	bare, remote, _ := relayRemote(t)
	ctx := context.Background()
	root := t.TempDir()
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, filepath.Join(t.TempDir(), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	extra := relayCredential(remote)
	folder, err := s.CloneThrough(ctx, relay, root, "shop", RelayCloneOptions{Branch: "main"}, extra)
	if err != nil {
		t.Fatal(err)
	}
	inFolder := func(args ...string) string { t.Helper(); return relayGit(t, folder, args...) }
	pushed := relayGit(t, bare, "rev-parse", "main")
	inFolder("checkout", "-q", "-b", "+main")
	inFolder("branch", "-f", "main", "HEAD~1")
	_, _ = s.PushThrough(ctx, relay, folder, "", extra)
	if got := relayGit(t, bare, "rev-parse", "main"); got != pushed {
		t.Fatalf("the remote's main was forced from %s to %s", pushed, got)
	}
	// main, moved back and given a commit of its own, is refused, not forced.
	inFolder("checkout", "-q", "main")
	write(t, folder, "other.txt", "other\n")
	inFolder("add", "-A")
	inFolder("commit", "-q", "-m", "Other")
	res, err := s.PushThrough(ctx, relay, folder, "", extra)
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("a push behind the remote = %v", err)
	}
	if res == nil || !strings.Contains(res.Output, "rejected") {
		t.Fatalf("the refusal said %+v", res)
	}
	if got := relayGit(t, bare, "rev-parse", "main"); got != pushed {
		t.Fatalf("the remote's main moved from %s to %s", pushed, got)
	}
}

// A clone of a tag is on no branch, and pushes its HEAD to the branch named;
// without one it says it needs one.
func TestARelayPushFromNoBranchGoesToTheBranchNamed(t *testing.T) {
	bare, remote, _ := relayRemote(t)
	ctx := context.Background()
	root := t.TempDir()
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, filepath.Join(t.TempDir(), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	extra := relayCredential(remote)
	folder, err := s.CloneThrough(ctx, relay, root, "shop", RelayCloneOptions{Branch: "v1"}, extra)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CurrentBranch(ctx, folder); err == nil {
		t.Fatal("a clone of a tag is on a branch")
	}
	if _, err := s.PushThrough(ctx, relay, folder, "", extra); err == nil || !strings.Contains(err.Error(), "not on a branch") {
		t.Fatalf("a push from no branch, naming none = %v", err)
	}
	if _, err := s.PushThrough(ctx, relay, folder, "fix-it", extra); err != nil {
		t.Fatal(err)
	}
	if got, want := relayGit(t, bare, "rev-parse", "refs/heads/fix-it"), relayGit(t, folder, "rev-parse", "HEAD"); got != want {
		t.Fatalf("the remote's fix-it = %s, want %s", got, want)
	}
}

// A relay is only ever for an HTTPS address, and only in a folder of the
// dashboard's own.
func TestARelayIsOnlyForHTTPS(t *testing.T) {
	s := New([]string{t.TempDir()})
	for _, remote := range []string{"git@github.com:acme/shop.git", "http://example.com/acme/shop.git", "https://user:pw@example.com/acme/shop.git"} {
		if _, err := s.OpenRelay(context.Background(), filepath.Join(t.TempDir(), "workspace-git"), remote); err == nil {
			t.Fatalf("a relay was opened for %s", remote)
		}
	}
	base := filepath.Join(t.TempDir(), "workspace-git")
	if err := os.Symlink(t.TempDir(), base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenRelay(context.Background(), base, "https://example.com/acme/shop.git"); err == nil {
		t.Fatal("a relay was opened through a link")
	}
}

// A clone that fails part of the way, through the relay or by the account's own
// git (killed at its deadline), leaves nothing at its place for the next one to
// call "already there".
func TestAWorkspaceCloneThatFailsLeavesNothing(t *testing.T) {
	_, remote, _ := relayRemote(t)
	ctx := context.Background()
	root := t.TempDir()
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, filepath.Join(t.TempDir(), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	// The account's own configuration gives every new checkout a hook that
	// fails its checkout.
	templates := filepath.Join(t.TempDir(), "templates")
	write(t, templates, "hooks/post-checkout", "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(filepath.Join(templates, "hooks", "post-checkout"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("GIT_CONFIG_GLOBAL"), []byte("[init]\n\ttemplateDir = "+templates+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloneThrough(ctx, relay, root, "shop", RelayCloneOptions{Branch: "main"}, relayCredential(remote)); err == nil {
		t.Fatal("the clone whose checkout failed succeeded")
	}
	if _, err := os.Lstat(filepath.Join(root, "shop")); err == nil {
		t.Fatal("the failed clone through the relay left its folder")
	}

	// A remote that answers nothing for longer than the clone may take.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		http.Error(w, "late", http.StatusServiceUnavailable)
	}))
	defer slow.Close()
	plan, err := s.PrepareClone(root, slow.URL+"/acme/shop.git", "slow", CloneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.CloneStreaming(short, plan, nil, func(string, string) {}); err == nil {
		t.Fatal("the clone outlived its deadline")
	}
	if _, err := os.Lstat(plan.Target); err == nil {
		t.Fatal("the clone killed at its deadline left its folder")
	}
}

// A relay nothing opened for 30 days is removed at the next open of another,
// never while one is in use; a shallow clone's relay left behind goes after a
// day.
func TestARelayUnusedForThirtyDaysIsSwept(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	ctx := context.Background()
	start := time.Unix(1_800_000_000, 0)
	now := start
	s := New([]string{t.TempDir()})
	s.now = func() time.Time { return now }
	base := filepath.Join(t.TempDir(), "workspace-git")
	open := func(remote string) *Relay {
		t.Helper()
		r, err := s.OpenRelay(ctx, base, remote)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	kept := open("https://example.com/acme/kept.git")
	old := open("https://example.com/acme/old.git")
	left := filepath.Join(base, "once", "clone-left.git")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(left, start, start); err != nil {
		t.Fatal(err)
	}
	gone := func(path string) bool { _, err := os.Lstat(path); return err != nil }

	now = start.Add(29 * 24 * time.Hour)
	open(kept.Remote)
	if gone(old.Dir) {
		t.Fatal("a relay unused for 29 days was removed")
	}
	if !gone(left) {
		t.Fatal("a shallow clone's relay left for 29 days was kept")
	}
	// Thirty-one days on, but in use: kept while its lock is held.
	now = start.Add(31 * 24 * time.Hour)
	unlock := s.Lock(old.Dir)
	open(kept.Remote)
	if gone(old.Dir) {
		t.Fatal("a relay in use was removed")
	}
	unlock()
	open(kept.Remote)
	if !gone(old.Dir) {
		t.Fatal("a relay unused for 31 days was kept")
	}
	if gone(kept.Dir) {
		t.Fatal("the relay opened was removed")
	}
	// It is made again when it is needed again.
	if again := open(old.Remote); gone(again.Dir) {
		t.Fatal("the swept relay was not made again")
	}
}
