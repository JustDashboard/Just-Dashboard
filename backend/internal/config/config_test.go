package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// The dashboard was called "VPS Dashboard" and read VPSD_* settings. Both
// fallbacks below exist so that pulling the rename does not lock an operator
// out of their own server, which is the one upgrade failure with no recovery
// short of restoring a backup.

func TestEnvFallsBackToLegacyPrefix(t *testing.T) {
	t.Setenv("JD_TERMINAL_SHELL", "")
	t.Setenv("VPSD_TERMINAL_SHELL", "/bin/zsh")
	if got := Env("JD_TERMINAL_SHELL"); got != "/bin/zsh" {
		t.Fatalf("legacy VPSD_ name ignored: got %q", got)
	}

	t.Setenv("JD_TERMINAL_SHELL", "/bin/fish")
	if got := Env("JD_TERMINAL_SHELL"); got != "/bin/fish" {
		t.Fatalf("current JD_ name should win: got %q", got)
	}
}

func TestEnvIgnoresLegacyForNonPrefixedKeys(t *testing.T) {
	t.Setenv("VPSD_PATH", "surprise")
	if got := Env("PATH_THAT_IS_NOT_OURS"); got != "" {
		t.Fatalf("unprefixed key should not consult VPSD_: got %q", got)
	}
}

func TestAdoptLegacyDataPrefersTheDirectoryHoldingTheDatabase(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "just-dashboard")
	legacy := filepath.Join(root, "vps-dashboard")

	// Docker bind-mounts create the configured directory whether or not
	// anything has ever been written to it, so an empty new directory must not
	// count as "already migrated".
	mkdir(t, current)
	mkdir(t, legacy)
	writeDatabase(t, legacy)

	if got := adoptLegacyData(current, legacy); got != legacy {
		t.Fatalf("expected the legacy database to be adopted, got %q", got)
	}

	// Once the new location has its own database, the old one is history.
	writeDatabase(t, current)
	if got := adoptLegacyData(current, legacy); got != current {
		t.Fatalf("expected the configured directory to win, got %q", got)
	}
}

func TestAdoptLegacyDataLeavesFreshInstallsAlone(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "just-dashboard")
	legacy := filepath.Join(root, "vps-dashboard")
	mkdir(t, current)

	if got := adoptLegacyData(current, legacy); got != current {
		t.Fatalf("no legacy database exists, so nothing should be adopted: got %q", got)
	}
}

func TestDeployRootsRejectsExplicitEmptyValue(t *testing.T) {
	t.Setenv("JD_DEPLOY_ROOTS", "  ")
	l := &loader{}
	if roots := deployRoots(l); roots != nil {
		t.Fatalf("deployRoots = %#v, want nil", roots)
	}
	if len(l.errs) != 1 {
		t.Fatalf("errors = %#v, want one configuration error", l.errs)
	}
}

func TestDeployRootsRetainsShippedDefaultWhenUnset(t *testing.T) {
	for _, key := range []string{"JD_DEPLOY_ROOTS", "VPSD_DEPLOY_ROOTS"} {
		value, set := os.LookupEnv(key)
		t.Cleanup(func() {
			if set {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	l := &loader{}
	want := []string{"/opt", "/srv", "/home", "/root"}
	got := deployRoots(l)
	if len(got) != len(want) {
		t.Fatalf("deployRoots = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("deployRoots = %#v, want %#v", got, want)
		}
	}
}

func TestDeploymentWorkerSettingsUseBoundedDefaults(t *testing.T) {
	l := &loader{}
	if got := l.integer("JD_DEPLOY_HEAVY_SLOTS", 1, 1, 8); got != 1 {
		t.Fatalf("heavy slots = %d, want 1", got)
	}
	if got := l.integer("JD_DEPLOY_LIGHT_SLOTS", 2, 1, 8); got != 2 {
		t.Fatalf("light slots = %d, want 2", got)
	}
	if got := l.boundedDuration("JD_DEPLOY_LEASE_TTL", 30*time.Second, 5*time.Second, 5*time.Minute); got != 30*time.Second {
		t.Fatalf("lease TTL = %s, want 30s", got)
	}
	if err := l.err(); err != nil {
		t.Fatal(err)
	}
}

func TestDeploymentWorkerSettingsRejectMalformedAndOutOfRangeValues(t *testing.T) {
	t.Setenv("JD_DEPLOY_HEAVY_SLOTS", "many")
	t.Setenv("JD_DEPLOY_LIGHT_SLOTS", "0")
	t.Setenv("JD_DEPLOY_LEASE_TTL", "4s")
	l := &loader{}
	_ = l.integer("JD_DEPLOY_HEAVY_SLOTS", 1, 1, 8)
	_ = l.integer("JD_DEPLOY_LIGHT_SLOTS", 2, 1, 8)
	_ = l.boundedDuration("JD_DEPLOY_LEASE_TTL", 30*time.Second, 5*time.Second, 5*time.Minute)
	err := l.err()
	if err == nil {
		t.Fatal("invalid deployment worker settings were accepted")
	}
	for _, key := range []string{"JD_DEPLOY_HEAVY_SLOTS", "JD_DEPLOY_LIGHT_SLOTS", "JD_DEPLOY_LEASE_TTL"} {
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("error %q does not identify %s", err, key)
		}
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeDatabase(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, store.DatabaseFile), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteDesktopBridgeMustBeHostAndPort(t *testing.T) {
	t.Setenv("JD_MASTER_KEY", strings.Repeat("ab", 32))
	t.Setenv("JD_ADDR", "127.0.0.1:0")
	t.Setenv("JD_REMOTE_DESKTOP_BRIDGE", "100.64.0.5")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JD_REMOTE_DESKTOP_BRIDGE") {
		t.Fatalf("a bridge without a port must refuse to start, got %v", err)
	}
	t.Setenv("JD_REMOTE_DESKTOP_BRIDGE", " 100.64.0.5:8080 ")
	c, err := Load()
	if err != nil || c.RemoteDesktopBridge != "100.64.0.5:8080" {
		t.Fatalf("bridge %q, %v", c.RemoteDesktopBridge, err)
	}
	t.Setenv("JD_REMOTE_DESKTOP_BRIDGE", "")
	if c, err := Load(); err != nil || c.RemoteDesktopBridge != "" {
		t.Fatalf("no bridge: %q, %v", c.RemoteDesktopBridge, err)
	}
}

func TestAccountLinkDefaultsToOnAtTheProjectsSiteAndRefusesAnythingButAnOrigin(t *testing.T) {
	t.Setenv("JD_MASTER_KEY", strings.Repeat("ab", 32))
	t.Setenv("JD_ADDR", "127.0.0.1:0")
	c, err := Load()
	if err != nil || !c.AccountLink || c.AccountURL != "https://just-dashboard.com" {
		t.Fatalf("defaults: link %v, url %q, %v", c.AccountLink, c.AccountURL, err)
	}

	// One spelling of the origin, so the site's own address compares equal to it.
	t.Setenv("JD_ACCOUNT_URL", "https://Just-Dashboard.COM:443/")
	if c, err := Load(); err != nil || c.AccountURL != "https://just-dashboard.com" {
		t.Fatalf("a default port and capitals in the host: %q, %v", c.AccountURL, err)
	}

	t.Setenv("JD_ACCOUNT_LINK", "false")
	t.Setenv("JD_ACCOUNT_URL", "http://localhost:3000/")
	c, err = Load()
	if err != nil || c.AccountLink || c.AccountURL != "http://localhost:3000" {
		t.Fatalf("switched off, loopback site: link %v, url %q, %v", c.AccountLink, c.AccountURL, err)
	}

	for _, bad := range []string{
		"http://just-dashboard.com", "https://u:p@just-dashboard.com", "https://just-dashboard.com/link",
		"https://just-dashboard.com?x=1", "just-dashboard.com", "ftp://just-dashboard.com",
	} {
		t.Setenv("JD_ACCOUNT_URL", bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JD_ACCOUNT_URL") {
			t.Fatalf("%q must refuse to start, got %v", bad, err)
		}
	}
	t.Setenv("JD_ACCOUNT_URL", "")
	t.Setenv("JD_ACCOUNT_LINK", "maybe")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JD_ACCOUNT_LINK") {
		t.Fatalf("a malformed switch must refuse to start, got %v", err)
	}
}
