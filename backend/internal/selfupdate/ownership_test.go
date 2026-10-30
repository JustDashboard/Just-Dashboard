package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// checkoutIn makes a root-owned install checkout inside a directory owned by
// parentUID and returns the checkout.
func checkoutIn(t *testing.T, parentUID int) string {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "Just-Dashboard")
	if err := os.MkdirAll(filepath.Join(dir, ".git", "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(parent, parentUID, parentUID); err != nil {
		t.Fatal(err)
	}
	return dir
}

func ownerUID(t *testing.T, path string) uint32 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	return st.Uid
}

func TestAdoptCheckoutGivesItToTheOwnerOfItsDirectory(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	dir := checkoutIn(t, 4242)
	s := New(Options{DataDir: t.TempDir(), UpdateDir: dir, List: noContainers})
	s.adoptCheckout(context.Background())
	for _, rel := range []string{".", ".git", ".git/refs", "docker-compose.yml"} {
		if got := ownerUID(t, filepath.Join(dir, rel)); got != 4242 {
			t.Errorf("%s uid = %d, want 4242", rel, got)
		}
	}
}

// /opt/Just-Dashboard is root's because /opt is: nobody else could have
// replaced it, so nobody else is given it.
func TestAdoptCheckoutLeavesACheckoutInRootsDirectory(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	dir := checkoutIn(t, 0)
	s := New(Options{DataDir: t.TempDir(), UpdateDir: dir, List: noContainers})
	s.adoptCheckout(context.Background())
	if got := ownerUID(t, dir); got != 0 {
		t.Errorf("checkout uid = %d, want 0", got)
	}
}

// An upgrade in flight is writing the same tree; the restart after it settles
// the ownership instead.
func TestAdoptCheckoutWaitsOutAnUpgrade(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	dir := checkoutIn(t, 4242)
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, StateFile), []byte(`{"status":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(Options{DataDir: data, UpdateDir: dir, List: noContainers})
	s.adoptCheckout(context.Background())
	if got := ownerUID(t, dir); got != 0 {
		t.Errorf("checkout uid = %d during an upgrade, want 0", got)
	}
}

func noContainers(context.Context) ([]Sibling, error) { return nil, nil }
