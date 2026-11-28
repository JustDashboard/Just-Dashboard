package gitx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// rootsAllTheWay holds only where no other account can point the path
// anywhere else: every folder root's, none a link, none another account may
// rename entries in (writable by group or others without the sticky bit).
func TestRootsAllTheWayHoldsOnlyWhereNoOtherAccountCanPointThePath(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	base := t.TempDir()
	if !rootsAllTheWay(base) {
		t.Skipf("%s is not root's all the way here", base)
	}
	folder := func(name string, mode os.FileMode, uid int) string {
		t.Helper()
		path := filepath.Join(base, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, uid, uid); err != nil {
			t.Fatal(err)
		}
		inside := filepath.Join(path, "checkout")
		if err := os.Mkdir(inside, 0o755); err != nil {
			t.Fatal(err)
		}
		return inside
	}
	for name, c := range map[string]struct {
		path string
		want bool
	}{
		"root's":                     {folder("plain", 0o755, 0), true},
		"sticky and world-writable":  {folder("sticky", 0o777|os.ModeSticky, 0), true},
		"world-writable, not sticky": {folder("open", 0o777, 0), false},
		"group-writable, not sticky": {folder("group", 0o775, 0), false},
		"an account's":               {folder("account", 0o755, relayTestAccount), false},
		"relative":                   {"checkout", false},
		"missing":                    {filepath.Join(base, "missing"), false},
	} {
		if got := rootsAllTheWay(c.path); got != c.want {
			t.Errorf("%s: rootsAllTheWay(%s) = %v, want %v", name, c.path, got, c.want)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "plain"), link); err != nil {
		t.Fatal(err)
	}
	if rootsAllTheWay(filepath.Join(link, "checkout")) {
		t.Error("a path through a link holds")
	}
}

// The accounts' view shares the service's locks: the assistant's push of a
// checkout and the Local Git page's commit of it never run at once.
func TestTheAccountsViewSharesTheServicesLocks(t *testing.T) {
	s := New([]string{t.TempDir()})
	path := filepath.Join(t.TempDir(), "checkout")
	unlock := s.ForAccounts().Lock(path)
	if _, free := s.tryLock(path); free {
		t.Fatal("the service took a lock its accounts' view holds")
	}
	unlock()
	release, free := s.tryLock(path)
	if !free {
		t.Fatal("the lock was not released")
	}
	release()
	if !s.ForAccounts().accountsOnly || s.accountsOnly {
		t.Fatal("the view and the service are not what they say")
	}
}

// Local Git's own fetch (executeEnv) ends at its deadline as the assistant's
// does: its helper, which holds git's output, is stopped with it.
func TestLocalGitsFetchEndsAtItsDeadline(t *testing.T) {
	t.Setenv("GIT_SSL_NO_VERIFY", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	release := make(chan struct{})
	stalled := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-time.After(time.Minute):
		}
	}))
	defer stalled.Close()
	defer close(release)
	dir, git := tempRepo(t)
	git("remote", "add", "origin", stalled.URL+"/acme/shop.git")
	s := New([]string{filepath.Dir(dir)})
	short, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	done := make(chan error, 1)
	go func() { _, err := s.Fetch(short, dir, false); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a fetch from a remote that never answers succeeded")
		}
		if took := time.Since(start); took > 15*time.Second {
			t.Fatalf("the fetch took %s past a one-second deadline", took)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the fetch was still running 30 seconds after its one-second deadline")
	}
}
