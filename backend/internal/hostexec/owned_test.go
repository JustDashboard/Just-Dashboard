package hostexec

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

func foreignPaths(fs []Foreign) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.Path)
	}
	return out
}

// Every directory on the way down counts, each is named once, and the walk
// ends at what git would create or would not follow.
func TestUnwritableWalksEachPathsDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"a/b", "real"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	// Nothing here belongs to this owner, so every directory reached is one.
	stranger := Owner{UID: uint32(os.Getuid()) + 1}
	got := foreignPaths(Unwritable(root, stranger, []string{
		"a/b/file.txt", "a/other.txt", "a/b/new/deeper/file.txt", "link/inside.txt", "top.txt",
	}))
	if want := []string{"a", "a/b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Unwritable = %q, want %q", got, want)
	}
	if got := Unwritable(root, Owner{UID: uint32(os.Getuid())}, []string{"a/b/file.txt"}); len(got) != 0 {
		t.Errorf("own directories reported: %+v", got)
	}
}

func TestGiveBackRefusesARootThatIsNotTheOwners(t *testing.T) {
	root := t.TempDir()
	if err := GiveBack(root, Owner{UID: uint32(os.Getuid()) + 1}, []string{"x"}); err == nil {
		t.Fatal("gave back inside a tree the owner does not own")
	}
}

// The chown runs as root inside a tree the owner can rewrite, so a link the
// owner planted must never be followed out of it.
func TestGiveBackDoesNotFollowALink(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	const owner = 4242
	root := t.TempDir()
	outside := t.TempDir()
	for _, p := range []string{filepath.Join(root, "dir"), filepath.Join(outside, "victim")} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(root, owner, owner); err != nil {
		t.Fatal(err)
	}
	o := Owner{UID: owner, GID: owner}
	if err := GiveBack(root, o, []string{"dir"}); err != nil {
		t.Fatalf("GiveBack dir: %v", err)
	}
	if err := GiveBack(root, o, []string{"escape/victim"}); err == nil {
		t.Error("followed a link out of the tree")
	}
	var st syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(root, "dir"), &st); err != nil || st.Uid != owner {
		t.Errorf("dir uid = %d, want %d", st.Uid, owner)
	}
	if err := syscall.Lstat(filepath.Join(outside, "victim"), &st); err != nil || st.Uid == owner {
		t.Errorf("victim outside the tree was chowned")
	}
}
