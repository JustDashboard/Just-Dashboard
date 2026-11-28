package gitx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A repository's .lfsconfig never moves where the relay uploads: git-lfs reads
// it from the relay's HEAD (a branch master the remote has, which a push of
// the workspace's can make), and its lfs.pushurl wins over lfs.url for an
// upload.
func TestARepositorysLFSConfigNeverMovesWhereTheRelayUploads(t *testing.T) {
	_, remote, seed, lfs := lfsRemote(t)
	var mu sync.Mutex
	var elsewhere []string
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		elsewhere = append(elsewhere, r.Method+" "+r.URL.Path)
		mu.Unlock()
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer other.Close()
	write(t, seed, ".lfsconfig", "[lfs]\n\tpushurl = "+other.URL+"/elsewhere.git/info/lfs\n")
	relayGit(t, seed, "add", "-A")
	relayGit(t, seed, "commit", "-q", "-m", "Elsewhere")
	relayGit(t, seed, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/master")
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
	write(t, folder, "made.bin", "a large file made here\n")
	relayGit(t, folder, "add", "-A")
	relayGit(t, folder, "commit", "-q", "-m", "Made")
	pushed, err := s.PushThrough(ctx, relay, folder, "", extra)
	mu.Lock()
	asked := strings.Join(elsewhere, "\n")
	mu.Unlock()
	if asked != "" {
		t.Fatalf("the upload went where the repository's .lfsconfig says:\n%s", asked)
	}
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("a large file made here\n"))
	if _, ok := lfs.has(hex.EncodeToString(sum[:])); !ok {
		t.Fatalf("the object did not reach the remote (said %q)", pushed.Output)
	}
}

// A remote spelled without .git, as a deployment's often is, has its LFS files
// where git-lfs itself looks: beside <remote>.git, with the credential.
func TestARemoteWithoutDotGitFindsItsLFSFiles(t *testing.T) {
	_, remote, _, _ := lfsRemote(t)
	remote = strings.TrimSuffix(remote, ".git")
	ctx := context.Background()
	root := realDir(t, t.TempDir())
	s := New([]string{root})
	relay, err := s.OpenRelay(ctx, filepath.Join(realDir(t, t.TempDir()), "workspace-git"), remote)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := s.CloneThrough(ctx, relay, root, "shop", RelayCloneOptions{}, relayCredential(remote))
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(folder, "big.bin")); string(raw) != "the first large file\n" {
		t.Fatalf("big.bin = %q", raw)
	}
}

func TestTheLFSEndpointIsBesideTheRepositorysDotGit(t *testing.T) {
	for remote, want := range map[string]string{
		"https://git.example.com/o/r.git":  "https://git.example.com/o/r.git/info/lfs",
		"https://git.example.com/o/r":      "https://git.example.com/o/r.git/info/lfs",
		"https://git.example.com/o/r/":     "https://git.example.com/o/r.git/info/lfs",
		"https://git.example.com/o/r.git/": "https://git.example.com/o/r.git/info/lfs",
	} {
		if got := lfsEndpoint(remote); got != want {
			t.Errorf("lfsEndpoint(%q) = %q, want %q", remote, got, want)
		}
	}
}
