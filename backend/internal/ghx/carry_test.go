package ghx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const signedIn = `github.com
  ✓ Logged in to github.com account Wayy01 (keyring)
  - Active account: true
  - Git operations protocol: https
`

// fakeAccounts answers gh as root (dir "") and as the owner of every other
// directory, with root signed in and the owner signed in only once a token
// has been handed over.
type fakeAccounts struct {
	ownerIn bool
	handed  string
	calls   []string
}

func (f *fakeAccounts) run(_ context.Context, dir, stdin string, args ...string) (string, error) {
	cmd := strings.Join(args, " ")
	f.calls = append(f.calls, dir+": "+cmd)
	switch {
	case cmd == "auth status":
		if dir == "" || f.ownerIn {
			return signedIn, nil
		}
		return "You are not logged into any GitHub hosts.\n", errors.New("exit status 1")
	case cmd == "auth token --hostname github.com" && dir == "":
		return "gho_roots\n", nil
	case strings.HasPrefix(cmd, "auth login ") && dir != "":
		f.handed, f.ownerIn = strings.TrimSpace(stdin), true
		return "", nil
	case strings.HasPrefix(cmd, "auth setup-git "):
		return "", nil
	}
	return "", errors.New("unexpected gh " + cmd)
}

// ownedCheckout is a directory belonging to an account other than root, with
// git's global config somewhere that account can write.
func ownedCheckout(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root to own a directory as somebody else")
	}
	dir := t.TempDir()
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, config} {
		if err := os.Chown(p, 4242, 4242); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	return dir
}

func TestCarryLoginSignsTheNewOwnerInWithRootsToken(t *testing.T) {
	dir := ownedCheckout(t)
	f := &fakeAccounts{}
	s := NewWithRunner(f.run)
	carried, err := s.CarryLogin(context.Background(), dir)
	if err != nil || !carried {
		t.Fatalf("CarryLogin = %v, %v; want true, nil", carried, err)
	}
	if f.handed != "gho_roots" {
		t.Errorf("handed %q to the owner, want root's token", f.handed)
	}
}

// A login of the owner's own is theirs: it is neither read nor replaced.
func TestCarryLoginKeepsTheOwnersOwnLogin(t *testing.T) {
	dir := ownedCheckout(t)
	f := &fakeAccounts{ownerIn: true}
	s := NewWithRunner(f.run)
	if carried, err := s.CarryLogin(context.Background(), dir); err != nil || carried {
		t.Fatalf("CarryLogin = %v, %v; want false, nil", carried, err)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "auth token") || strings.Contains(call, "auth login") {
			t.Errorf("touched a signed-in owner's login: %s", call)
		}
	}
}

// A checkout that is still root's has nobody to carry the login to.
func TestCarryLoginLeavesARootCheckout(t *testing.T) {
	f := &fakeAccounts{}
	s := NewWithRunner(f.run)
	if carried, err := s.CarryLogin(context.Background(), t.TempDir()); err != nil || carried {
		t.Fatalf("CarryLogin = %v, %v; want false, nil", carried, err)
	}
	if len(f.calls) != 0 {
		t.Errorf("ran gh for a root checkout: %v", f.calls)
	}
}
