package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestContextPathsResolveWhatTheFolderHolds(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for _, dir := range []string{"srv/app", "srv/.hidden"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustLink := func(target, link string) {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	mustLink(filepath.Join(root, "srv/app"), filepath.Join(root, "srv/current"))
	mustLink(outside, filepath.Join(root, "srv/away"))
	mustLink(filepath.Join(root, "srv"), filepath.Join(root, "www"))

	s := New([]string{root})
	got, err := s.ContextPaths(filepath.Join(root, "www"), false, []string{filepath.Join(root, "www/app"), "relative"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got.Folder != filepath.Join(root, "srv") {
		t.Fatalf("folder = %q, want the folder the link is", got.Folder)
	}
	real := map[string]string{}
	for _, c := range got.Children {
		real[filepath.Base(c.Path)] = c.Real
	}
	if _, ok := real[".hidden"]; ok {
		t.Fatal("a hidden entry was read with hidden off")
	}
	if real["current"] != filepath.Join(root, "srv/app") {
		t.Fatalf("current resolves to %q", real["current"])
	}
	// Outside the roots: kept as the link it is, matching nothing it points at.
	if real["away"] != filepath.Join(root, "srv/away") {
		t.Fatalf("an outward link resolved to %q", real["away"])
	}
	if got.Known[filepath.Join(root, "www/app")] != filepath.Join(root, "srv/app") {
		t.Fatalf("known path not resolved: %v", got.Known)
	}
	if _, ok := got.Known["relative"]; ok {
		t.Fatal("a relative inventory path was resolved")
	}

	few, err := s.ContextPaths(filepath.Join(root, "srv"), true, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !few.Truncated || len(few.Children) > 2 {
		t.Fatalf("limit not kept: %d entries, truncated %v", len(few.Children), few.Truncated)
	}

	if _, err := s.ContextPaths(outside, false, nil, 10); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside the roots: %v", err)
	}
}
