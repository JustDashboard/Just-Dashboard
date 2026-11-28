package gitx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What the account's side writes for an LFS object it hands over is kept only
// up to the object's size: an account whose git-lfs (or whatever answers for
// it) writes without end never fills the dashboard's disk with it.
func TestAnLFSObjectFromTheCheckoutIsReadNoFurtherThanItsSize(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	ctx := context.Background()
	s := New([]string{t.TempDir()})
	relay, err := s.OpenRelay(ctx, filepath.Join(t.TempDir(), "workspace-git"), "https://git.example.com/acme/shop.git")
	if err != nil {
		t.Fatal(err)
	}
	checkout := t.TempDir()
	content := "ten bytes\n"
	sum := sha256.Sum256([]byte(content))
	oid := hex.EncodeToString(sum[:])
	write(t, filepath.Join(checkout, ".git", "lfs", "objects", oid[0:2], oid[2:4]), oid, content)
	finished := filepath.Join(t.TempDir(), "finished")
	// The account's side: far more than the object, then a mark that it was
	// let write all of it.
	endless := func(ctx context.Context, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "head -c 67108864 /dev/zero && touch "+finished)
	}
	store := filepath.Join(relay.Dir, "lfs", "objects")
	start := time.Now()
	err = relay.lfsFromCheckout(ctx, endless, checkout, store, oid)
	if err == nil || !strings.Contains(err.Error(), "larger than its size") {
		t.Fatalf("lfsFromCheckout = %v", err)
	}
	if _, err := os.Stat(finished); err == nil {
		t.Fatalf("the account's side was read to its end (%s)", time.Since(start))
	}
	if _, err := os.Stat(lfsPath(store, oid)); err == nil {
		t.Fatal("the relay kept an object that was not its content")
	}
}
