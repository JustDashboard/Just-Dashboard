package gitx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// relayTestAccount is the uid a root test hands the account's folders to.
const relayTestAccount = 4242

// accountTrap is what an account can make of a folder of root's that anyone
// may write in (/tmp): a bare repository of its own whose hook writes the uid it
// ran as to the answered file.
func accountTrap(t *testing.T) (string, string) {
	t.Helper()
	trap := t.TempDir()
	if err := os.Chmod(trap, 0o1777); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(trap, "ran-as")
	write(t, trap, "HEAD", "ref: refs/heads/main\n")
	write(t, trap, "config", "[core]\n\tbare = true\n\thooksPath = "+filepath.Join(trap, "hooks")+"\n")
	write(t, trap, "hooks/reference-transaction", "#!/bin/sh\nid -u >> "+ran+"\n")
	for _, dir := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(trap, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(trap, "hooks", "reference-transaction"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"HEAD", "config", "hooks", "hooks/reference-transaction", "objects", "refs"} {
		if err := os.Lchown(filepath.Join(trap, name), relayTestAccount, relayTestAccount); err != nil {
			t.Fatal(err)
		}
	}
	return trap, ran
}

// accountHome is a folder of the account's, as its home is.
func accountHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	// The test's own folders above it, which the account passes through.
	for _, dir := range []string{filepath.Dir(home), home} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(home, relayTestAccount, relayTestAccount); err != nil {
		t.Fatal(err)
	}
	return home
}

// A root dashboard never runs git as root in a checkout the account has made a
// link of (swapped in after it was resolved, while the relay fetched): the
// link's folder is root's, but what is in it is the account's, and git as root
// runs the hook its configuration names.
func TestAFetchThroughALinkNeverRunsTheAccountsHookAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	_, remote, _ := relayRemote(t)
	ctx := context.Background()
	home := accountHome(t)
	trap, ran := accountTrap(t)
	link := filepath.Join(home, "shop")
	if err := os.Symlink(trap, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Lchown(link, relayTestAccount, relayTestAccount); err != nil {
		t.Fatal(err)
	}
	s := New([]string{home})
	relay, err := s.OpenRelay(ctx, filepath.Join(t.TempDir(), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	_, fetchErr := s.FetchThrough(ctx, relay, link, relayCredential(remote))
	if raw, err := os.ReadFile(ran); err == nil && strings.Contains(string(raw), "0\n") {
		t.Fatalf("the account's hook ran as root (fetch: %v)", fetchErr)
	}
	if fetchErr == nil {
		t.Fatal("a fetch into a folder of root's the account links to succeeded")
	}
}

// Nor does the account's own fetch (a workspace with no credential of the
// dashboard's): the account's repository there sends origin over SSH with a
// command of its own.
func TestAnAccountsFetchThroughALinkNeverRunsAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	home := accountHome(t)
	trap, ran := accountTrap(t)
	ssh := filepath.Join(trap, "hooks", "ssh")
	write(t, trap, "hooks/ssh", "#!/bin/sh\nid -u >> "+ran+"\nexit 1\n")
	if err := os.Chmod(ssh, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, trap, "config", "[core]\n\tbare = true\n\tsshCommand = "+ssh+"\n"+
		"[remote \"origin\"]\n\turl = https://example.com/acme/shop.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"+
		"[url \"ssh://git@example.com/\"]\n\tinsteadOf = https://example.com/\n")
	link := filepath.Join(home, "shop")
	if err := os.Symlink(trap, link); err != nil {
		t.Fatal(err)
	}
	s := New([]string{home})
	_, _ = s.FetchOrigin(context.Background(), link)
	if raw, err := os.ReadFile(ran); err == nil && strings.Contains(string(raw), "0\n") {
		t.Fatal("the account's SSH command ran as root")
	}
}

// A clone that fails never removes a folder it did not make: with the
// workspaces folder (the account's own, in its home) swapped for a link while
// the account's git cloned, the failed clone's place is a folder of root's of
// the same name, elsewhere.
func TestAFailedCloneNeverRemovesWhatALinkPointsAt(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	home := accountHome(t)
	parent := filepath.Join(home, "jd-workspaces")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(parent, relayTestAccount, relayTestAccount); err != nil {
		t.Fatal(err)
	}
	victims := t.TempDir()
	write(t, victims, "www/index.html", "root's\n")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		http.Error(w, "late", http.StatusServiceUnavailable)
	}))
	defer slow.Close()
	s := New([]string{home})
	plan, err := s.PrepareClone(parent, slow.URL+"/acme/www.git", "www", CloneOptions{})
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	go func() {
		// The account, while its git waits on the remote.
		time.Sleep(500 * time.Millisecond)
		_ = os.Rename(parent, parent+".was")
		_ = os.Symlink(victims, parent)
	}()
	said := []string{}
	if err := s.CloneStreaming(short, plan, nil, func(_, text string) { said = append(said, text) }); err == nil {
		t.Fatal("the clone outlived its deadline")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the clone failed before its deadline: %v %q", err, said)
	}
	if _, err := os.Lstat(filepath.Join(victims, "www", "index.html")); err != nil {
		t.Fatal("a failed clone removed root's folder a link pointed at")
	}
}

// A root dashboard clones, fetches and pushes through the relay into a folder
// of an account's, the checkout made and owned by that account.
func TestARootRelayMakesTheAccountsCheckout(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	bare, remote, _ := relayRemote(t)
	// The account's git reads this, so it is the account's to read.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	ctx := context.Background()
	home := accountHome(t)
	s := New([]string{home})
	relay, err := s.OpenRelay(ctx, filepath.Join(t.TempDir(), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	extra := relayCredential(remote)
	for _, options := range []RelayCloneOptions{{}, {Depth: 1, Branch: "main"}} {
		name := "shop-" + strings.Repeat("s", options.Depth)
		folder, err := s.CloneThrough(ctx, relay, home, name, options, extra)
		if err != nil {
			t.Fatalf("clone %+v: %v", options, err)
		}
		for _, rel := range []string{".", ".git", ".git/config", ".git/HEAD", "app.js"} {
			info, err := os.Lstat(filepath.Join(folder, rel))
			if err != nil || !ownedBy(info, relayTestAccount) {
				t.Fatalf("%s of the clone %+v is not the account's: %v", rel, options, err)
			}
		}
		if options.Depth > 0 {
			info, err := os.Lstat(filepath.Join(folder, ".git", "shallow"))
			if err != nil || !ownedBy(info, relayTestAccount) {
				t.Fatalf("the shallow clone's .git/shallow is not the account's: %v", err)
			}
		}
	}
	folder := filepath.Join(home, "shop-")
	if _, err := s.FetchThrough(ctx, relay, folder, extra); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PushThrough(ctx, relay, folder, "copy", extra); err != nil {
		t.Fatal(err)
	}
	if got := relayGit(t, bare, "rev-parse", "refs/heads/copy"); got != relayGit(t, bare, "rev-parse", "main") {
		t.Fatalf("the remote's copy = %s", got)
	}
}

// Every git the workspaces run (gitx.ForAccounts: status, stage, commit, the
// branch read) is refused as root through such a link, not only the relay's;
// Local Git's own view keeps its root fallback, as before.
func TestTheWorkspacesGitNeverRunsAsRootThroughALink(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	home := accountHome(t)
	trap, ran := accountTrap(t)
	link := filepath.Join(home, "shop")
	if err := os.Symlink(trap, link); err != nil {
		t.Fatal(err)
	}
	s := New([]string{home})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"status": func() error { _, err := s.ForAccounts().Status(ctx, link); return err },
		"stage":  func() error { _, err := s.ForAccounts().Stage(ctx, link, nil); return err },
		"branch": func() error { _, err := s.ForAccounts().CurrentBranch(ctx, link); return err },
		"head":   func() error { _, err := s.ForAccounts().HeadCommit(ctx, link); return err },
		"diffstat": func() error {
			if s.ForAccounts().DiffStat(ctx, link) != "" {
				return errors.New("a diff was read")
			}
			return errors.New("refused")
		},
	} {
		if err := call(); err == nil {
			t.Fatalf("%s ran in a folder of root's the account links to", name)
		}
	}
	if _, err := s.ForAccounts().HeadCommit(ctx, link); err == nil || !strings.Contains(err.Error(), "not a folder of one account's") {
		t.Fatalf("the refusal said %v", err)
	}
	if raw, err := os.ReadFile(ran); err == nil && strings.Contains(string(raw), "0\n") {
		t.Fatal("the account's program ran as root")
	}
	// Local Git's own view is unchanged (its root fallback is the page's).
	if _, err := s.HeadCommit(ctx, link); err != nil && strings.Contains(err.Error(), "not a folder of one account's") {
		t.Fatal("Local Git's own view refused")
	}
}

// A root dashboard's relay hands a repository's Git LFS files to the account
// and takes a new one back from it: what lands in the checkout is the
// account's, and the upload reaches the server.
func TestARootRelayCarriesTheAccountsGitLFSFiles(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	_, remote, _, lfs := lfsRemote(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	ctx := context.Background()
	home := accountHome(t)
	s := New([]string{home})
	relay, err := s.OpenRelay(ctx, filepath.Join(t.TempDir(), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	extra := relayCredential(remote)
	folder, err := s.CloneThrough(ctx, relay, home, "shop", RelayCloneOptions{}, extra)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(folder, "big.bin"))
	if raw, _ := os.ReadFile(filepath.Join(folder, "big.bin")); err != nil || string(raw) != "the first large file\n" || !ownedBy(info, relayTestAccount) {
		t.Fatalf("big.bin = %q, %v", raw, err)
	}
	if info, err := os.Lstat(filepath.Join(folder, ".git", "lfs", "objects")); err != nil || !ownedBy(info, relayTestAccount) {
		t.Fatalf("the checkout's LFS store is not the account's: %v", err)
	}
	// The account's own commit of a new LFS file (its git, as itself).
	write(t, folder, "made.bin", "made by the account\n")
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=a", "-c", "user.email=a@example.test", "commit", "-q", "-m", "Made"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = folder
		cmd.Env = append(os.Environ(), "HOME="+home)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: relayTestAccount, Gid: relayTestAccount}}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v as the account: %v\n%s", args, err, out)
		}
	}
	if _, err := s.PushThrough(ctx, relay, folder, "", extra); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("made by the account\n"))
	if content, ok := lfs.has(hex.EncodeToString(sum[:])); !ok || string(content) != "made by the account\n" {
		t.Fatalf("the account's LFS object did not reach the server: %q %v", content, ok)
	}
}
