package selfcfg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
)

func TestConfigurationRestoredWhenRestartCannotBeRecorded(t *testing.T) {
	for _, existed := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%v", existed), func(t *testing.T) {
			dir, checkout := t.TempDir(), t.TempDir()
			path := filepath.Join(checkout, ".env")
			const original = "# preserve these exact bytes\nJD_SITE=localhost\n"
			if existed {
				if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			s := New(Options{DataDir: dir, Locate: func(context.Context) (*selfupdate.Location, error) {
				return &selfupdate.Location{Dir: "/host-only/checkout", Visible: checkout}, nil
			}})
			// A directory at the temp-record path fails Save before Docker is run.
			if err := os.Mkdir(s.store.Path()+".tmp", 0o700); err != nil {
				t.Fatal(err)
			}
			next := defaults()
			next.TerminalEnabled = !next.TerminalEnabled
			if _, err := s.Apply(t.Context(), next, "test", "127.0.0.1"); err == nil {
				t.Fatal("expected recording failure")
			}
			got, err := os.ReadFile(path)
			if !existed {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("new configuration survived failed start: %q, %v", got, err)
				}
			} else if err != nil || string(got) != original {
				t.Fatalf("previous configuration not restored: %q, %v", got, err)
			}
		})
	}
}

func TestConfigurationDoesNotWriteDuringAnUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	const original = "JD_SITE=localhost\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := selfupdate.NewStore(dir).Save(&selfupdate.Run{Status: selfupdate.StatusRunning}); err != nil {
		t.Fatal(err)
	}
	s := New(Options{DataDir: dir})
	if _, err := s.Apply(t.Context(), Settings{}, "test", "127.0.0.1"); !errors.Is(err, ErrInProgress) {
		t.Fatalf("apply raced an update: %v", err)
	}
	if _, err := s.Restart(t.Context(), false, "test"); !errors.Is(err, ErrInProgress) {
		t.Fatalf("restart raced an update: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != original {
		t.Fatalf("configuration changed: %q, %v", got, err)
	}
	if _, err := os.Stat(path + ".jd-previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("busy operation overwrote the rollback backup")
	}
}
