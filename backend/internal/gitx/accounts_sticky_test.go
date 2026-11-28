package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A folder of root's that everyone may write in (/tmp, sticky) is not one the
// workspaces' git runs in as root: another account can put a bare repository
// of its own in it, beside an empty .git that makes Local Git take the folder
// for a checkout, and git as root then runs what that repository's
// configuration names.
func TestTheWorkspacesGitNeverRunsAsRootInAFolderAnyoneMayWriteIn(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	trap, ran := accountTrap(t)
	ssh := filepath.Join(trap, "hooks", "ssh")
	write(t, trap, "hooks/ssh", "#!/bin/sh\nid -u >> "+ran+"\nexit 1\n")
	if err := os.Chmod(ssh, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, trap, "config", "[core]\n\tbare = true\n\tsshCommand = "+ssh+"\n"+
		"[remote \"origin\"]\n\turl = https://example.com/acme/shop.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n"+
		"[url \"ssh://git@example.com/\"]\n\tinsteadOf = https://example.com/\n")
	if err := os.Mkdir(filepath.Join(trap, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".git", "config", "hooks/ssh"} {
		if err := os.Lchown(filepath.Join(trap, name), relayTestAccount, relayTestAccount); err != nil {
			t.Fatal(err)
		}
	}
	// As /tmp is: sticky (os.ModeSticky; a bare 0o1777 is not).
	if err := os.Chmod(trap, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	s := New([]string{filepath.Dir(trap)})
	folder, err := s.ForAccounts().Resolve(trap)
	if err != nil {
		t.Skipf("Local Git does not take it for a checkout: %v", err)
	}
	_, fetchErr := s.ForAccounts().FetchOrigin(context.Background(), folder)
	if raw, err := os.ReadFile(ran); err == nil && strings.Contains(string(raw), "0\n") {
		t.Fatalf("the account's SSH command ran as root (fetch: %v)", fetchErr)
	}
	if _, err := s.ForAccounts().HeadCommit(context.Background(), folder); err == nil {
		t.Fatal("git ran in it as root")
	}
}

// A sticky folder on the way is still one no other account can point
// elsewhere; the folder git runs in itself must be root's to write in alone.
func TestRootsAllTheWayTakesAStickyFolderOnlyOnTheWay(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	sticky := t.TempDir()
	if err := os.Chmod(sticky, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(sticky, "checkout")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if !rootsAllTheWay(inside) {
		t.Fatalf("%s, root's in a sticky folder, does not hold", inside)
	}
	if rootsAllTheWay(sticky) {
		t.Fatalf("%s, which anyone may write in, holds", sticky)
	}
}
