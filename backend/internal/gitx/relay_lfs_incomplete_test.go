package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A push whose commit carries an LFS pointer that neither the checkout nor the
// relay holds, and the remote lacks, is refused, as git-lfs's own hook refuses
// it: the remote never holds a pointer it has no object for.
func TestAPushOfAnLFSPointerNobodyHoldsIsRefused(t *testing.T) {
	bare, remote, _, _ := lfsRemote(t)
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
	before := relayGit(t, bare, "rev-parse", "main")
	write(t, folder, "ghost.bin", lfsPointer("content nobody has\n"))
	relayGit(t, folder, "add", "-A")
	relayGit(t, folder, "commit", "-q", "-m", "Ghost")
	if res, err := s.PushThrough(ctx, relay, folder, "", extra); err == nil {
		t.Fatalf("the push went: %q", res.Output)
	}
	if after := relayGit(t, bare, "rev-parse", "main"); after != before {
		t.Fatal("the remote holds a pointer it has no object for")
	}
}

// One whose objects the relay never held but the remote has (a branch made
// from a tag, whose LFS files no fetch gave) goes, and says no upload.
func TestAPushOfObjectsOnlyTheRemoteHoldsGoes(t *testing.T) {
	_, remote, seed, lfs := lfsRemote(t)
	lfs.put("tagged only\n")
	write(t, seed, "tagged.bin", lfsPointer("tagged only\n"))
	relayGit(t, seed, "add", "-A")
	relayGit(t, seed, "commit", "-q", "-m", "Tagged")
	relayGit(t, seed, "tag", "v1")
	relayGit(t, seed, "push", "-q", "origin", "refs/tags/v1")
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
	cmd := exec.Command("git", "checkout", "-q", "-b", "feature", "v1")
	cmd.Dir = folder
	cmd.Env = append(os.Environ(), "GIT_LFS_SKIP_SMUDGE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}
	write(t, folder, "app.js", "changed\n")
	relayGit(t, folder, "add", "-A")
	relayGit(t, folder, "commit", "-q", "-m", "Change")
	res, err := s.PushThrough(ctx, relay, folder, "", extra)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "uploaded") {
		t.Fatalf("the push says it uploaded what the remote had: %q", res.Output)
	}
}
