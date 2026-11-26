package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityFollowsTheEntryNotItsName(t *testing.T) {
	root := t.TempDir()
	s := New([]string{root})
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := os.WriteFile(a, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := s.Identity(a)
	if err != nil || first == "" {
		t.Fatalf("identity: %q %v", first, err)
	}
	// A rename keeps it.
	if err := os.Rename(a, b); err != nil {
		t.Fatal(err)
	}
	if moved, _ := s.Identity(b); moved != first {
		t.Fatalf("renamed: %q, was %q", moved, first)
	}
	// Another file renamed over the name has its own, as a copy or a save does.
	other := filepath.Join(root, "other")
	if err := os.WriteFile(other, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, b); err != nil {
		t.Fatal(err)
	}
	if replaced, _ := s.Identity(b); replaced == first {
		t.Fatal("the file that took the name has the identity of the one it replaced")
	}
	// A link is named as the link, not what it points at.
	link := filepath.Join(root, "link")
	if err := os.Symlink(b, link); err != nil {
		t.Fatal(err)
	}
	target, _ := s.Identity(b)
	if linked, _ := s.Identity(link); linked == target {
		t.Fatal("a link took its target's identity")
	}
	if _, err := s.Identity(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("a missing entry: %v", err)
	}
	if _, err := s.Identity("/etc/passwd"); err == nil {
		t.Fatal("an entry outside the roots was named")
	}
}
