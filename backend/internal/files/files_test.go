package files

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Resolve is the single choke point every client-supplied path passes through,
// and it had no test at all. These are the three shapes that matter: traversal
// out of the roots, a symlink inside the roots pointing out of them, and a
// path that does not exist yet.
func TestResolveContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})

	if _, err := s.Resolve(filepath.Join(root, "..", filepath.Base(outside))); err == nil {
		t.Fatal("a ../ traversal out of the roots must be refused")
	}
	if _, err := s.Resolve(filepath.Join(root, "escape", "secret")); err == nil {
		t.Fatal("a symlink inside the roots pointing out of them must be refused")
	}
	got, err := s.Resolve(filepath.Join(root, "sub", "new.txt"))
	if err != nil {
		t.Fatalf("a file that does not exist yet must resolve on its parent: %v", err)
	}
	if want := filepath.Join(root, "sub", "new.txt"); got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

// Missing descendants used to make Resolve stop before it reached an outward
// symlink. MkdirAll and archive/copy writers then followed that link as root.
func TestMutationsRejectOutwardSymlinkBeforeMissingDescendants(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})

	t.Run("mkdir", func(t *testing.T) {
		path := filepath.Join(root, "escape", "missing", "directory")
		if err := s.Mkdir(path, 0o755); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("Mkdir error = %v, want ErrOutsideRoot", err)
		}
		if _, err := os.Stat(filepath.Join(outside, "missing")); !os.IsNotExist(err) {
			t.Fatalf("mkdir escaped the root: %v", err)
		}
	})

	t.Run("copy", func(t *testing.T) {
		src := filepath.Join(root, "source")
		if err := os.Mkdir(src, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "payload"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, "escape", "copy-parent", "copy")
		if err := s.Copy(src, dst, false); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("Copy error = %v, want ErrOutsideRoot", err)
		}
		if _, err := os.Stat(filepath.Join(outside, "copy-parent")); !os.IsNotExist(err) {
			t.Fatalf("copy escaped the root: %v", err)
		}
	})

	t.Run("extract", func(t *testing.T) {
		archive := filepath.Join(root, "payload.zip")
		f, err := os.Create(archive)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		entry, err := zw.Create("payload")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		dst := filepath.Join(root, "escape", "extract-parent", "extract")
		if _, err := s.Extract(context.Background(), archive, dst); !errors.Is(err, ErrOutsideRoot) {
			t.Fatalf("Extract error = %v, want ErrOutsideRoot", err)
		}
		if _, err := os.Stat(filepath.Join(outside, "extract-parent")); !os.IsNotExist(err) {
			t.Fatalf("extract escaped the root: %v", err)
		}
	})
}

// ResolveEntry exists because Resolve dereferences, which is wrong for every
// operation acting on the entry itself: deleting "current -> releases/v1"
// used to remove the release directory and leave the link behind.
func TestResolveEntryDoesNotDereference(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "releases")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "current")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})

	if got, err := s.Resolve(link); err != nil || got != target {
		t.Fatalf("Resolve should dereference: got %q, err %v", got, err)
	}
	if got, err := s.ResolveEntry(link); err != nil || got != link {
		t.Fatalf("ResolveEntry should not dereference: got %q, err %v", got, err)
	}
	if err := s.Delete(link, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("deleting the symlink took its target with it: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("the symlink itself should be gone")
	}
}

func TestStatReportsTheSymlinkItself(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "file"), link); err != nil {
		t.Fatal(err)
	}
	e, err := New([]string{root}).Stat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !e.IsSymlink {
		t.Fatal("Stat on a symlink reported the target, not the link")
	}
}

// Chmod dereferences on Linux and there is no lchmod, so a recursive walk that
// chmod-ed the symlinks it visited could change the mode of anything they
// pointed at — including, as root, /etc.
func TestChmodDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})
	if err := s.Chmod(root, "777", true); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(victim)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("a file outside the roots was chmod-ed through a symlink: %v", st.Mode().Perm())
	}
}

// A symlink is only as contained as what it points at; the target used to go
// unchecked entirely.
func TestSymlinkChecksItsTarget(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	s := New([]string{root})
	if err := s.Symlink(outside, filepath.Join(root, "escape")); err == nil {
		t.Fatal("a symlink pointing outside the roots must be refused")
	}
	if err := s.Symlink("./inside", filepath.Join(root, "ok")); err != nil {
		t.Fatalf("a symlink inside the roots must be allowed: %v", err)
	}
}

// The NSS caches are package-level maps written from the request goroutine.
// A concurrent map write is a runtime throw that httpx.Recoverer cannot catch,
// so two people browsing files at once took the whole process down. Run with
// -race to see it.
func TestLookupUserIsConcurrencySafe(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n uint32) {
			defer wg.Done()
			lookupUser(n)
			lookupGroup(n)
		}(uint32(i % 4))
	}
	wg.Wait()
}
