package procs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// systemctlShim puts a systemctl first on PATH that records each run and
// prints an empty unit list, and answers with the file it records to.
func systemctlShim(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "systemctl.log")
	script := "#!/bin/sh\necho \"$*\" >> \"" + log + "\"\necho '[]'\n"
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

// standSystemd says whether a manager is running, for the length of a test.
func standSystemd(t *testing.T, running bool) {
	t.Helper()
	before := SystemdRunDir
	t.Cleanup(func() { SystemdRunDir = before })
	SystemdRunDir = t.TempDir()
	if !running {
		SystemdRunDir = filepath.Join(SystemdRunDir, "absent")
	}
}

// Available was "is systemctl on PATH", and the image always ships it. On a
// host booted with another init every read ran systemctl and answered its
// refusal as a command that failed. Nothing is run now, the reads say the
// module is not available, and a control says why.
func TestSystemdIsNotAvailableWithoutAManager(t *testing.T) {
	log := systemctlShim(t)
	standSystemd(t, false)
	s := NewSystemd()

	if s.Available() {
		t.Fatal("systemctl without a manager was reported as available")
	}
	if _, err := s.List(context.Background()); !errors.Is(err, ErrNoSystemd) {
		t.Fatalf("List: got %v, want ErrNoSystemd", err)
	}
	if _, err := s.Timers(context.Background()); !errors.Is(err, ErrNoSystemd) {
		t.Fatalf("Timers: got %v, want ErrNoSystemd", err)
	}
	// A control has no Available check of its own; the runner refuses it.
	res, err := s.Control(context.Background(), "nginx.service", UnitRestart)
	if !errors.Is(err, ErrNoSystemd) || res == nil || res.Command != "systemctl restart nginx.service" {
		t.Fatalf("Control: got %+v, %v", res, err)
	}
	if errors.Is(err, ErrNotInstalled) {
		t.Fatal("a missing manager was reported as a missing program")
	}
	if ran, err := os.ReadFile(log); err == nil {
		t.Fatalf("systemctl was run with nothing to ask: %q", ran)
	}
}

func TestSystemdIsAvailableBesideItsManager(t *testing.T) {
	log := systemctlShim(t)
	standSystemd(t, true)
	s := NewSystemd()

	if !s.Available() {
		t.Fatal("systemctl beside a running manager was reported as unavailable")
	}
	if _, err := s.Control(context.Background(), "nginx.service", UnitRestart); err != nil {
		t.Fatal(err)
	}
	if ran, _ := os.ReadFile(log); string(ran) != "restart nginx.service\n" {
		t.Fatalf("systemctl ran %q", ran)
	}
}

// The program missing is still said as that.
func TestSystemdWithoutSystemctlIsNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	standSystemd(t, true)
	s := NewSystemd()
	if s.Available() {
		t.Fatal("available without systemctl")
	}
	if _, err := s.List(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("got %v, want ErrNotInstalled", err)
	}
}
