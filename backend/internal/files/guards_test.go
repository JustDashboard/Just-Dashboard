package files

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The create verbs are creates. Touch bumped the timestamp of an existing
// file and Mkdir accepted an existing directory, so "New file" and "New
// folder" both reported success for a name that was already taken — and the
// page then opened somebody else's file under the name just typed as new.
func TestCreateVerbsRefuseAnExistingPath(t *testing.T) {
	root := t.TempDir()
	s := New([]string{root})
	existing := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Touch(existing); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Touch on an existing file = %v, want ErrExist", err)
	}
	if b, _ := os.ReadFile(existing); string(b) != "keep" {
		t.Fatalf("Touch changed the file: %q", b)
	}
	if err := s.Touch(filepath.Join(root, "new.txt")); err != nil {
		t.Fatalf("Touch on a new name: %v", err)
	}

	if err := s.Mkdir(root, 0); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Mkdir on an existing directory = %v, want ErrExist", err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := s.Mkdir(nested, 0); err != nil {
		t.Fatalf("Mkdir with missing parents: %v", err)
	}
	if st, err := os.Stat(nested); err != nil || !st.IsDir() {
		t.Fatalf("nested directory missing: %v", err)
	}
}

// A move onto an occupied name is a 409 unless the caller asked to replace it.
// rename(2) replaces silently, which was how "move a.txt here" ate the a.txt
// that was already there.
func TestMoveRefusesAnOccupiedDestinationUnlessOverwriting(t *testing.T) {
	root := t.TempDir()
	s := New([]string{root})
	src := filepath.Join(root, "a.txt")
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	taken := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(taken, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.Move(src, dir, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Move into a directory holding the name = %v, want ErrExist", err)
	}
	if b, _ := os.ReadFile(taken); string(b) != "old" {
		t.Fatalf("the occupant was replaced without overwrite: %q", b)
	}
	if err := s.Move(src, dir, true); err != nil {
		t.Fatalf("Move with overwrite: %v", err)
	}
	if b, _ := os.ReadFile(taken); string(b) != "new" {
		t.Fatalf("overwrite did not replace: %q", b)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("source still present after move: %v", err)
	}

	// Moving a folder into its own subtree is refused with a sentence rather
	// than rename(2)'s EINVAL.
	if err := s.Move(dir, filepath.Join(dir, "inner"), false); err == nil ||
		!strings.Contains(err.Error(), "into itself") {
		t.Fatalf("Move into itself = %v", err)
	}
	// Moving something onto itself is a no-op rather than an error.
	if err := s.Move(dir, dir, false); err != nil {
		t.Fatalf("Move onto itself = %v", err)
	}
}

func TestCopyRefusesAnOccupiedDestinationUnlessOverwriting(t *testing.T) {
	root := t.TempDir()
	s := New([]string{root})
	src, dst := filepath.Join(root, "src.txt"), filepath.Join(root, "dst.txt")
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Copy(src, dst, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Copy onto an occupied name = %v, want ErrExist", err)
	}
	if err := s.Copy(src, dst, true); err != nil {
		t.Fatalf("Copy with overwrite: %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("overwrite did not replace: %q", b)
	}
}

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("connection reset")
	}
	f.n--
	return copy(p, []byte("partial data\n")), nil
}

// An upload lands whole or not at all, and a replaced file keeps its identity.
func TestUploadIsAtomicAndKeepsTheOriginalMode(t *testing.T) {
	root := t.TempDir()
	s := New([]string{root})
	path := filepath.Join(root, "logo.png")

	n, err := s.Upload(path, strings.NewReader("first"), false)
	if err != nil || n != 5 {
		t.Fatalf("Upload new file: n=%d err=%v", n, err)
	}
	if _, err := s.Upload(path, strings.NewReader("second"), false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Upload over an existing file without overwrite = %v, want ErrExist", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "first" {
		t.Fatalf("refused upload still changed the file: %q", b)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(path, strings.NewReader("second"), true); err != nil {
		t.Fatalf("Upload with overwrite: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("replacement lost the mode: %v %v", st, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "second" {
		t.Fatalf("overwrite did not replace: %q", b)
	}

	// A transfer that dies halfway leaves the previous contents in place and
	// no temporary file behind.
	if _, err := s.Upload(path, &failingReader{n: 3}, true); err == nil {
		t.Fatal("a failed transfer reported success")
	}
	if b, _ := os.ReadFile(path); string(b) != "second" {
		t.Fatalf("a failed transfer truncated the file: %q", b)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".jd-upload-") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
	if _, err := s.Upload(root, strings.NewReader("x"), true); !errors.Is(err, ErrIsDir) {
		t.Fatalf("Upload onto a directory = %v, want ErrIsDir", err)
	}
}

// An archive this server writes must never carry a "../" entry, and a
// selected symlink is archived as the link rather than as its target.
func TestArchiveMembersStayUnderTheBaseAndKeepLinks(t *testing.T) {
	root := t.TempDir()
	s := New([]string{root})
	base := filepath.Join(root, "site")
	elsewhere := filepath.Join(root, "shared")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "secret.txt"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "index.html"), []byte("<h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "secret.txt"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.ResolveArchive(base, []string{filepath.Join(elsewhere, "secret.txt")}); err == nil {
		t.Fatal("a member outside the base was accepted")
	}
	baseDir, members, err := s.ResolveArchive(base, []string{
		filepath.Join(base, "index.html"), filepath.Join(base, "link"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := s.Compress(&buf, baseDir, members, FormatTarGz); err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	names := map[string]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(hdr.Name, "..") {
			t.Fatalf("archive carries a traversal name: %s", hdr.Name)
		}
		names[hdr.Name] = hdr.Typeflag
	}
	if names["index.html"] != tar.TypeReg {
		t.Fatalf("index.html missing or wrong type: %v", names)
	}
	if names["link"] != tar.TypeSymlink {
		t.Fatalf("the symlink was dereferenced: %v", names)
	}
}
