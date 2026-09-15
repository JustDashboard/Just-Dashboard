package files

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCopyAndMoveRejectOutwardDestination(t *testing.T) {
	for _, operation := range []string{"copy", "move"} {
		for _, kind := range []string{"file", "directory"} {
			if operation == "move" && kind == "file" {
				continue // Moving replaces the entry; the target is never written.
			}
			t.Run(operation+"/"+kind, func(t *testing.T) {
				root, outside := t.TempDir(), t.TempDir()
				src, dst := filepath.Join(root, "source"), filepath.Join(root, "link")
				target := filepath.Join(outside, "target")
				if err := os.WriteFile(src, []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := os.Mkdir(target, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, dst); err != nil {
					t.Fatal(err)
				}
				s := New([]string{root})
				op := s.Copy
				if operation == "move" {
					op = s.Move
				}
				if err := op(src, dst); !errors.Is(err, ErrOutsideRoot) {
					t.Fatalf("want containment refusal, got %v", err)
				}
				if data, err := os.ReadFile(src); err != nil || string(data) != "new" {
					t.Fatalf("source changed: %q %v", data, err)
				}
				if kind == "file" {
					if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
						t.Fatalf("outside data changed: %q %v", data, err)
					}
				} else if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
					t.Fatalf("outside directory changed: %v %v", entries, err)
				}
			})
		}
	}
}

func TestMoveReplacesTheSymlinkEntryAndCopyPreservesFileMode(t *testing.T) {
	root, outside := t.TempDir(), filepath.Join(t.TempDir(), "data")
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
	if err := os.WriteFile(source, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, destination); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})
	if err := s.Move(source, destination); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "keep" {
		t.Fatalf("symlink target changed: %q %v", b, err)
	}
	if err := os.WriteFile(source, []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Copy(source, destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("destination mode was not retained: %v %v", info, err)
	}
}

func TestCopyRejectsSameFileAndDescendant(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	if err := os.WriteFile(src, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "hardlink")
	if err := os.Link(src, alias); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})
	for _, dst := range []string{src, root, alias} {
		if err := s.Copy(src, dst); err == nil {
			t.Fatalf("copy onto same inode accepted: %s", dst)
		}
		if b, err := os.ReadFile(src); err != nil || string(b) != "keep" {
			t.Fatalf("source lost: %q %v", b, err)
		}
	}
	dir := filepath.Join(root, "directory")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Copy(dir, filepath.Join(dir, "child")); err == nil {
		t.Fatal("copy into descendant accepted")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("descendant created: %v %v", entries, err)
	}
}

func TestRecursiveCopyRejectsExistingChildSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	src, dest := filepath.Join(root, "src"), filepath.Join(root, "dest")
	for _, dir := range []string{src, filepath.Join(dest, "src")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "data"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "data")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dest, "src", "data")); err != nil {
		t.Fatal(err)
	}
	if err := New([]string{root}).Copy(src, dest); err == nil {
		t.Fatal("recursive symlink destination accepted")
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
		t.Fatalf("outside data lost: %q %v", b, err)
	}
}

func TestCopyPreservesNormalReplacementAndSourceLinks(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	if err := os.Mkdir(src, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "data"), []byte("value"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("data", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	s := New([]string{root})
	if err := s.Copy(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "data")); err != nil || string(b) != "value" {
		t.Fatalf("copy: %q %v", b, err)
	}
	if link, err := os.Readlink(filepath.Join(dst, "link")); err != nil || link != "data" {
		t.Fatalf("link: %q %v", link, err)
	}
	if err := s.Copy(filepath.Join(src, "data"), filepath.Join(dst, "data")); err != nil {
		t.Fatal(err)
	}
}

// Linux's POSIX ACL xattr format is a little-endian version followed by
// tag/permissions/id entries. The owning group has no access despite a read
// mask in the mode bits; the named user can read through that mask.
func restrictedCopyACL() []byte {
	const undefined = ^uint32(0)
	value := binary.LittleEndian.AppendUint32(nil, 2)
	for _, entry := range []struct {
		tag, permissions uint16
		id               uint32
	}{
		{1, 6, undefined},
		{2, 4, 65534},
		{4, 0, undefined},
		{16, 4, undefined},
		{32, 0, undefined},
	} {
		value = binary.LittleEndian.AppendUint16(value, entry.tag)
		value = binary.LittleEndian.AppendUint16(value, entry.permissions)
		value = binary.LittleEndian.AppendUint32(value, entry.id)
	}
	return value
}

func setCopyACLFixture(t *testing.T, path, name string, acl []byte) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := unix.Fsetxattr(int(file.Fd()), name, acl, 0); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) {
			t.Skip("test filesystem does not support POSIX ACLs")
		}
		t.Fatal(err)
	}
}

func TestCopyPreservesExistingAccessACL(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
	for path, contents := range map[string]string{source: "replacement", destination: "original"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	acl := restrictedCopyACL()
	setCopyACLFixture(t, destination, copyAccessACL, acl)
	before, err := os.Stat(destination)
	if err != nil || before.Mode().Perm() != 0o640 {
		t.Fatalf("ACL fixture must expose mask as group mode: %v %v", before, err)
	}
	if err := New([]string{root}).Copy(source, destination); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := readCopyAccessACL(file)
	if err != nil || !bytes.Equal(got, acl) {
		t.Fatalf("replacement widened owning-group access: ACL %x, error %v", got, err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "replacement" {
		t.Fatalf("copy lost contents: %q %v", data, err)
	}
}

func TestCopyRemovesInheritedACLWhenDestinationHadNone(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
	for _, path := range []string{source, destination} {
		if err := os.WriteFile(path, []byte("fixture"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	setCopyACLFixture(t, root, "system.posix_acl_default", restrictedCopyACL())
	if err := New([]string{root}).Copy(source, destination); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if got, err := readCopyAccessACL(file); err != nil || got != nil {
		t.Fatalf("replacement inherited an access ACL absent from original: %x %v", got, err)
	}
	if info, err := file.Stat(); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("replacement lost original mode: %v %v", info, err)
	}
}

func TestCopyAccessACLHelpersFailClosed(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if _, err := readCopyAccessACL(file); err == nil {
		t.Fatal("ACL read error treated as absent ACL")
	}
	for _, acl := range [][]byte{nil, restrictedCopyACL()} {
		if err := writeCopyAccessACL(file, acl); err == nil {
			t.Fatal("ACL preservation error ignored")
		}
	}
}
