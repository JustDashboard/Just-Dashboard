package linuxusers

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
)

func keyAccount(t *testing.T) *user.User {
	return &user.User{Uid: strconv.Itoa(os.Getuid()), Gid: strconv.Itoa(os.Getgid()), HomeDir: t.TempDir()}
}
func TestSSHDirectoryAndKeySymlinksCannotRedirectPrivilegedAccess(t *testing.T) {
	account := keyAccount(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(account.HomeDir, ".ssh")); err != nil {
		t.Fatal(err)
	}
	if dir, _, _, err := openSSHDir(account, true); err == nil {
		dir.Close()
		t.Fatal("followed .ssh symlink")
	}
	if err := os.Remove(filepath.Join(account.HomeDir, ".ssh")); err != nil {
		t.Fatal(err)
	}
	dir, _, _, err := openSSHDir(account, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	target := filepath.Join(outside, "privileged")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(account.HomeDir, ".ssh", "authorized_keys")); err != nil {
		t.Fatal(err)
	}
	if _, err := readKeysAt(dir); err == nil {
		t.Fatal("followed key symlink")
	}
	if contents, _ := os.ReadFile(target); string(contents) != "unchanged" {
		t.Fatal("changed symlink target")
	}
}
func TestSSHAtomicReplacementDoesNotModifyHardlinkTargetAndAnchorsDirectory(t *testing.T) {
	account := keyAccount(t)
	dir, uid, gid, err := openSSHDir(account, true)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	target := filepath.Join(t.TempDir(), "privileged")
	if err := os.WriteFile(target, []byte("unchanged"), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(account.HomeDir, ".ssh", "authorized_keys")
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(account.HomeDir, "original")
	if err := os.Rename(filepath.Join(account.HomeDir, ".ssh"), moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(account.HomeDir, ".ssh")); err != nil {
		t.Fatal(err)
	}
	if err := writeKeysAt(dir, []byte("new key\n"), uid, gid); err != nil {
		t.Fatal(err)
	}
	if contents, _ := os.ReadFile(target); string(contents) != "unchanged" {
		t.Fatal("modified hardlink target")
	}
	info, err := os.Stat(filepath.Join(moved, "authorized_keys"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("replacement: %v, %v", info, err)
	}
}
