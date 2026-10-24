package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// Git's refusal is the only place the files in the way are named, so it is
// read line by line, quoted names included.
func TestRefusedPullReadsBothListsFromGitsRefusal(t *testing.T) {
	out := strings.Join([]string{
		"Updating 54d13d70..c12fb880",
		"error: Your local changes to the following files would be overwritten by merge:",
		"\tdocs/internal/boards.md",
		"\t\"caf\\303\\251.txt\"",
		"Please commit your changes or stash them before you merge.",
		"error: The following untracked working tree files would be overwritten by merge:",
		"\tdocs/screenshots/after.png",
		"Please move or remove them before you merge.",
		"Aborting",
	}, "\n")
	got := refusedPull(out)
	if got == nil {
		t.Fatal("refusal not recognised")
	}
	if want := []string{"docs/internal/boards.md", "café.txt"}; !reflect.DeepEqual(got.Changed, want) {
		t.Errorf("changed = %q, want %q", got.Changed, want)
	}
	if want := []string{"docs/screenshots/after.png"}; !reflect.DeepEqual(got.Untracked, want) {
		t.Errorf("untracked = %q, want %q", got.Untracked, want)
	}
	if refusedPull("fatal: Not possible to fast-forward, aborting.") != nil {
		t.Error("a diverged branch is not a blocked pull")
	}
}

// blockedCheckout is a checkout one commit behind its remote, holding an edit
// to the file that commit changes and an untracked file where it adds one.
func blockedCheckout(t *testing.T) (source, checkout string) {
	t.Helper()
	source, checkout = pullFixture(t)
	git := func(args ...string) { pullGit(t, source, args...) }
	write(t, source, "added.txt", "from the remote\n")
	git("add", "added.txt")
	git("commit", "-qm", "add a file")
	write(t, checkout, "a.txt", "edited here\n")
	write(t, checkout, "added.txt", "untracked here\n")
	return source, checkout
}

func TestPullBlockedByLocalWorkNamesItAndChangesNothing(t *testing.T) {
	_, checkout := blockedCheckout(t)
	head := pullGit(t, checkout, "rev-parse", "HEAD")
	_, err := New([]string{checkout}).Pull(context.Background(), checkout)
	var blocked *PullBlocked
	if !errors.As(err, &blocked) {
		t.Fatalf("Pull err = %v, want *PullBlocked", err)
	}
	if !reflect.DeepEqual(blocked.Changed, []string{"a.txt"}) ||
		!reflect.DeepEqual(blocked.Untracked, []string{"added.txt"}) {
		t.Fatalf("blocked = %+v, want a.txt changed and added.txt untracked", blocked)
	}
	if got := pullGit(t, checkout, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, "a.txt")); string(b) != "edited here\n" {
		t.Errorf("the local edit was touched: %q", b)
	}
}

func TestStashAndPullKeepsTheWorkInAStash(t *testing.T) {
	source, checkout := blockedCheckout(t)
	res, err := New([]string{checkout}).StashAndPull(context.Background(), checkout)
	if err != nil || !res.OK {
		t.Fatalf("StashAndPull: %+v %v", res, err)
	}
	if got, want := pullGit(t, checkout, "rev-parse", "HEAD"), pullGit(t, source, "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want %s", got, want)
	}
	if list := pullGit(t, checkout, "stash", "list"); !strings.Contains(list, "Put aside to pull patch/0.7.1") {
		t.Errorf("stash list = %q", list)
	}
	if !strings.Contains(pullGit(t, checkout, "stash", "show", "--include-untracked", "--name-only"), "a.txt") {
		t.Error("the edit is not in the stash")
	}
}

func TestDiscardAndPullDropsOnlyWhatIsInTheWay(t *testing.T) {
	source, checkout := blockedCheckout(t)
	write(t, checkout, "notes.txt", "unrelated, untracked\n")
	res, err := New([]string{checkout}).DiscardAndPull(context.Background(), checkout)
	if err != nil || !res.OK {
		t.Fatalf("DiscardAndPull: %+v %v", res, err)
	}
	if got, want := pullGit(t, checkout, "rev-parse", "HEAD"), pullGit(t, source, "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want %s", got, want)
	}
	if b, _ := os.ReadFile(filepath.Join(checkout, "added.txt")); string(b) != "from the remote\n" {
		t.Errorf("added.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(checkout, "notes.txt")); err != nil {
		t.Errorf("a file that was not in the way went with the rest: %v", err)
	}
}

// Only root can make a folder that belongs to somebody else, so this runs
// under `sudo go test`; the dashboard is root in production.
func TestPullRefusesBeforeWritingIntoAnotherAccountsFolder(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to own a folder for another account")
	}
	source, checkout := pullFixture(t)
	git := func(args ...string) { pullGit(t, source, args...) }
	write(t, source, "drift/repairs.tsx", "one\n")
	write(t, source, "drift/table.tsx", "one\n")
	git("add", "-A")
	git("commit", "-qm", "add drift")
	pullGit(t, checkout, "pull", "-q", "--ff-only", "origin", "patch/0.7.1")
	write(t, source, "drift/repairs.tsx", "two\n")
	write(t, source, "top.txt", "new\n")
	git("commit", "-qam", "change drift")
	git("add", "-A")
	git("commit", "-qm", "add top")

	// The test's own git calls stay root's after the tree changes hands.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "safe.directory")
	t.Setenv("GIT_CONFIG_VALUE_0", "*")
	const owner = 4242
	// Source and checkout share the test's temporary directory, which is
	// private to its creator; the owner has to be able to reach both.
	chownTree(t, filepath.Dir(filepath.Dir(checkout)), owner)
	if err := os.Chown(filepath.Join(checkout, "drift"), 0, 0); err != nil {
		t.Fatal(err)
	}
	s := New([]string{checkout})
	head := pullGit(t, checkout, "rev-parse", "HEAD")
	_, err := s.Pull(context.Background(), checkout)
	var blocked *PullBlocked
	if !errors.As(err, &blocked) || len(blocked.Unwritable) != 1 || blocked.Unwritable[0].Path != "drift" ||
		blocked.Unwritable[0].Owner != "root" {
		t.Fatalf("Pull err = %v (%+v), want drift blocked", err, blocked)
	}
	if pullGit(t, checkout, "rev-parse", "HEAD") != head || pullGit(t, checkout, "status", "--porcelain") != "" {
		t.Fatal("the refused pull wrote something")
	}

	res, given, err := s.OwnAndPull(context.Background(), checkout)
	if err != nil || len(given) != 1 {
		t.Fatalf("OwnAndPull: %+v %v %v", res, given, err)
	}
	if got, want := pullGit(t, checkout, "rev-parse", "HEAD"), pullGit(t, source, "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want %s", got, want)
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(checkout, "drift"), &st); err != nil || st.Uid != owner {
		t.Errorf("drift uid = %d, want %d", st.Uid, owner)
	}
}

// The folder git cannot write into here holds no tracked file yet, only
// copies of what the pull adds, put there by something running as root. Git
// names those copies as in its way, and Discard could not delete them either,
// so the folder has to be the first thing the operator is shown.
func TestPullRefusesAnotherAccountsFolderHoldingOnlyUntrackedFiles(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to own a folder for another account")
	}
	source, checkout := pullFixture(t)
	write(t, source, "logos/mark.svg", "<svg/>\n")
	pullGit(t, source, "add", "-A")
	pullGit(t, source, "commit", "-qm", "add logos")
	write(t, checkout, "logos/mark.svg", "<svg/>\n")

	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "safe.directory")
	t.Setenv("GIT_CONFIG_VALUE_0", "*")
	const owner = 4242
	chownTree(t, filepath.Dir(filepath.Dir(checkout)), owner)
	chownTree(t, filepath.Join(checkout, "logos"), 0)
	s := New([]string{checkout})
	_, err := s.Pull(context.Background(), checkout)
	var blocked *PullBlocked
	if !errors.As(err, &blocked) || len(blocked.Unwritable) != 1 || blocked.Unwritable[0].Path != "logos" {
		t.Fatalf("Pull err = %v (%+v), want logos blocked", err, blocked)
	}

	_, _, err = s.OwnAndPull(context.Background(), checkout)
	if !errors.As(err, &blocked) || !reflect.DeepEqual(blocked.Untracked, []string{"logos/mark.svg"}) {
		t.Fatalf("OwnAndPull err = %v (%+v), want the untracked copy named next", err, blocked)
	}
	res, err := s.DiscardAndPull(context.Background(), checkout)
	if err != nil || !res.OK {
		t.Fatalf("DiscardAndPull: %+v %v", res, err)
	}
	if got, want := pullGit(t, checkout, "rev-parse", "HEAD"), pullGit(t, source, "rev-parse", "HEAD"); got != want {
		t.Errorf("HEAD = %s, want %s", got, want)
	}
}

func chownTree(t *testing.T, root string, uid int) {
	t.Helper()
	err := filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, uid)
	})
	if err != nil {
		t.Fatal(err)
	}
}
