package gitx

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// PushWith never forces, whatever the checked-out branch is called: a branch
// named "+main" is a name, not git's force mark on a refspec.
func TestPushWithNeverForcesThroughABranchsName(t *testing.T) {
	dir, git := tempRepo(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	git("remote", "add", "origin", bare)
	write(t, dir, "a.txt", "two\n")
	git("commit", "-qam", "second")
	git("push", "-q", "origin", "main")
	pushed := strings.TrimSpace(git("rev-parse", "main"))
	// main is moved back locally, and a branch named "+main", with no upstream,
	// is the one checked out.
	git("checkout", "-q", "-b", "+main")
	git("branch", "-f", "main", "HEAD~1")
	s := New([]string{filepath.Dir(dir)})
	_, _ = s.PushWith(context.Background(), dir, "", nil)
	out, err := exec.Command("git", "--git-dir", bare, "rev-parse", "main").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != pushed {
		t.Fatalf("the remote's main was forced from %s to %s", pushed, got)
	}
	// A branch of an ordinary name with no upstream is pushed as itself and
	// tracks what it was pushed to.
	git("checkout", "-q", "-b", "feature", "main")
	if _, err := s.PushWith(context.Background(), dir, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git("rev-parse", "--abbrev-ref", "feature@{upstream}")); got != "origin/feature" {
		t.Fatalf("feature tracks %q", got)
	}
}
