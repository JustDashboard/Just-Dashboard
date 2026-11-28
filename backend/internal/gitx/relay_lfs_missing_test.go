package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A branch whose LFS object the server no longer has (one never uploaded, an
// old branch's) does not fail the fetch, nor keep the LFS files of the branches
// that are whole from the checkout: the fetch is done, and says what is not.
func TestAFetchWithAnLFSObjectTheServerLacksStillFetches(t *testing.T) {
	_, remote, seed, lfs := lfsRemote(t)
	ctx := context.Background()
	root := realDir(t, t.TempDir())
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, filepath.Join(realDir(t, t.TempDir()), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	extra := relayCredential(remote)
	folder, err := s.CloneThrough(ctx, relay, root, "shop", RelayCloneOptions{}, extra)
	if err != nil {
		t.Fatal(err)
	}
	write(t, seed, "lost.bin", lfsPointer("an object nobody uploaded\n"))
	relayGit(t, seed, "add", "-A")
	relayGit(t, seed, "commit", "-q", "-m", "Lost")
	relayGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/old")
	relayGit(t, seed, "reset", "-q", "--hard", "HEAD~1")
	second := lfs.put("the second large file\n")
	write(t, seed, "more.bin", lfsPointer("the second large file\n"))
	relayGit(t, seed, "add", "-A")
	relayGit(t, seed, "commit", "-q", "-m", "More")
	relayGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main")
	res, err := s.FetchThrough(ctx, relay, folder, extra)
	if err != nil {
		t.Fatalf("the fetch failed: %v", err)
	}
	if relayGit(t, folder, "rev-parse", "refs/remotes/origin/old") == "" {
		t.Fatal("the fetch did not bring old")
	}
	if raw, err := os.ReadFile(lfsPath(filepath.Join(folder, ".git", "lfs", "objects"), second)); err != nil || string(raw) != "the second large file\n" {
		t.Fatalf("main's LFS object = %q, %v (said %q)", raw, err, res.Output)
	}
	if !strings.Contains(res.Output, "1 Git LFS file was fetched") || !strings.Contains(res.Output, "could not be fetched") {
		t.Fatalf("the fetch said %q", res.Output)
	}
}
