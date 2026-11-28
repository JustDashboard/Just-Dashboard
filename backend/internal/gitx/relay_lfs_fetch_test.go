package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A branch the relay already moved for another checkout of the same remote
// (another workspace, the GitHub page's clone, a push's own fetch) still brings
// its LFS files into this checkout: what is new is new to the checkout, not to
// the relay.
func TestAFetchGivesTheLFSFilesOfWhatTheCheckoutLacksNotOnlyWhatTheRelayMoved(t *testing.T) {
	_, remote, seed, lfs := lfsRemote(t)
	ctx := context.Background()
	root := realDir(t, t.TempDir())
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, filepath.Join(realDir(t, t.TempDir()), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	extra := relayCredential(remote)
	one, err := s.CloneThrough(ctx, relay, root, "one", RelayCloneOptions{}, extra)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.CloneThrough(ctx, relay, root, "two", RelayCloneOptions{}, extra)
	if err != nil {
		t.Fatal(err)
	}
	second := lfs.put("the second large file\n")
	write(t, seed, "more.bin", lfsPointer("the second large file\n"))
	relayGit(t, seed, "add", "-A")
	relayGit(t, seed, "commit", "-q", "-m", "More")
	relayGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	if _, err := s.FetchThrough(ctx, relay, one, extra); err != nil {
		t.Fatal(err)
	}
	res, err := s.FetchThrough(ctx, relay, two, extra)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(lfsPath(filepath.Join(two, ".git", "lfs", "objects"), second)); err != nil || string(raw) != "the second large file\n" {
		t.Fatalf("the second checkout's LFS object = %q, %v (said %q)", raw, err, res.Output)
	}
	relayGit(t, two, "merge", "-q", "--ff-only", "origin/main")
	if raw, _ := os.ReadFile(filepath.Join(two, "more.bin")); string(raw) != "the second large file\n" {
		t.Fatalf("more.bin after the merge = %q", raw)
	}
}
