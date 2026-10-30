package hostexec

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func uidOf(t *testing.T, path string) uint32 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	return st.Uid
}

func TestAdoptRefusesATreeOfAThirdAccount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	root := t.TempDir()
	if err := os.Chown(root, 4343, 4343); err != nil {
		t.Fatal(err)
	}
	if _, err := Adopt(root, Owner{UID: 4242, GID: 4242}); err == nil {
		t.Fatal("adopted a tree that belongs to somebody else")
	}
}

// Root's entries move; secrets, other accounts' files, extra hard links and
// whatever a link points at do not.
func TestAdoptHandsOverRootsEntriesOnly(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	const owner, other = 4242, 4343
	root := t.TempDir()
	outside := t.TempDir()
	write := func(path string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, ".git", "objects", "ab", "cd"), 0o444)
	write(filepath.Join(root, "src", "main.go"), 0o644)
	write(filepath.Join(root, ".env"), 0o600)
	write(filepath.Join(root, "theirs.txt"), 0o644)
	write(filepath.Join(root, "private", "key"), 0o644)
	write(filepath.Join(outside, "victim"), 0o644)
	if err := os.Chmod(filepath.Join(root, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(filepath.Join(root, "theirs.txt"), other, other); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(outside, "victim"), filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	n, err := Adopt(root, Owner{UID: owner, GID: owner})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	for _, rel := range []string{".", ".git", ".git/objects", ".git/objects/ab", ".git/objects/ab/cd", "src", "src/main.go", "escape"} {
		if got := uidOf(t, filepath.Join(root, rel)); got != owner {
			t.Errorf("%s uid = %d, want %d", rel, got, owner)
		}
	}
	for rel, want := range map[string]uint32{
		".env": 0, "private": 0, "private/key": 0, "hardlink": 0, "theirs.txt": other,
	} {
		if got := uidOf(t, filepath.Join(root, rel)); got != want {
			t.Errorf("%s uid = %d, want %d", rel, got, want)
		}
	}
	for _, path := range []string{outside, filepath.Join(outside, "victim")} {
		if got := uidOf(t, path); got != 0 {
			t.Errorf("followed a link out of the tree: %s uid = %d", path, got)
		}
	}
	if n != 8 {
		t.Errorf("changed = %d, want 8", n)
	}
	// Settled is settled: a second walk has nothing to do.
	if n, err := Adopt(root, Owner{UID: owner, GID: owner}); err != nil || n != 0 {
		t.Errorf("second Adopt = %d, %v; want 0, nil", n, err)
	}
}
