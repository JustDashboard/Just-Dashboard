package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A workspace switches to a branch whose LFS files it was never given (the
// deployment's branch changed, one the fetch did not move): the checkout is
// made, those files stay pointers, and the ones it holds are real.
func TestAWorkspaceSwitchesToABranchWhoseLFSFilesItWasNeverGiven(t *testing.T) {
	_, remote, seed, lfs := lfsRemote(t)
	lfs.put("a release file\n")
	relayGit(t, seed, "checkout", "-q", "-b", "release")
	write(t, seed, "release.bin", lfsPointer("a release file\n"))
	relayGit(t, seed, "add", "-A")
	relayGit(t, seed, "commit", "-q", "-m", "Release")
	relayGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/release")
	ctx := context.Background()
	root := realDir(t, t.TempDir())
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, filepath.Join(realDir(t, t.TempDir()), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	extra := relayCredential(remote)
	folder, err := s.CloneThrough(ctx, relay, root, "shop", RelayCloneOptions{Branch: "main"}, extra)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ForAccounts().FetchThrough(ctx, relay, folder, extra); err != nil {
		t.Fatal(err)
	}
	if res, err := s.ForAccounts().Checkout(ctx, folder, "release"); err != nil {
		t.Fatalf("checkout release: %v (%v)", err, res)
	}
	if raw, _ := os.ReadFile(filepath.Join(folder, "release.bin")); string(raw) != lfsPointer("a release file\n") {
		t.Fatalf("release.bin = %q", raw)
	}
	if raw, _ := os.ReadFile(filepath.Join(folder, "big.bin")); string(raw) != "the first large file\n" {
		t.Fatalf("big.bin on release = %q", raw)
	}
	if status := relayGit(t, folder, "status", "--porcelain"); status != "" {
		t.Fatalf("the checkout is not clean: %s", status)
	}
}
