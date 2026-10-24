package files

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Only root can make an entry that belongs to somebody else, so this runs
// under `sudo go test`; the dashboard is root in production.
func TestNewEntriesBelongToTheFolderTheyAreMadeIn(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to make entries for another account")
	}
	const owner = 4242
	root := t.TempDir()
	home := filepath.Join(root, "home")
	system := filepath.Join(root, "system")
	for _, dir := range []string{home, system} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(home, owner, owner); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, uid int) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, uid, uid); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(system, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(source, "nested", "file.txt"), "copied\n", 0)
	if err := os.Symlink("nested/file.txt", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(home, "theirs.txt"), "before\n", 0)
	archive := filepath.Join(system, "logos.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("logos/mark.svg")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("<svg/>\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	s := New([]string{root})
	steps := []struct {
		name string
		do   func() error
	}{
		{"mkdir", func() error { return s.Mkdir(filepath.Join(home, "a", "b"), 0) }},
		{"touch", func() error { return s.Touch(filepath.Join(home, "new.txt")) }},
		{"write", func() error { return s.Write(filepath.Join(home, "saved.txt"), "x\n") }},
		{"replace", func() error { return s.Write(filepath.Join(home, "theirs.txt"), "after\n") }},
		{"upload", func() error {
			_, err := s.Upload(filepath.Join(home, "up.txt"), strings.NewReader("up\n"), false)
			return err
		}},
		{"copy", func() error { return s.Copy(source, filepath.Join(home, "copies", "source"), false) }},
		{"extract", func() error {
			_, err := s.Extract(context.Background(), archive, filepath.Join(home, "unpacked"))
			return err
		}},
		{"mkdir as root", func() error { return s.Mkdir(filepath.Join(system, "kept"), 0) }},
	}
	for _, step := range steps {
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
	}

	for path, want := range map[string]uint32{
		"home/a":                             owner,
		"home/a/b":                           owner,
		"home/new.txt":                       owner,
		"home/saved.txt":                     owner,
		"home/theirs.txt":                    0,
		"home/up.txt":                        owner,
		"home/copies":                        owner,
		"home/copies/source":                 owner,
		"home/copies/source/nested":          owner,
		"home/copies/source/nested/file.txt": owner,
		"home/copies/source/link":            0,
		"home/unpacked":                      owner,
		"home/unpacked/logos":                owner,
		"home/unpacked/logos/mark.svg":       owner,
		"system/kept":                        0,
	} {
		var st syscall.Stat_t
		if err := syscall.Lstat(filepath.Join(root, path), &st); err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if st.Uid != want || st.Gid != want {
			t.Errorf("%s is %d:%d, want %d:%d", path, st.Uid, st.Gid, want, want)
		}
	}
}
