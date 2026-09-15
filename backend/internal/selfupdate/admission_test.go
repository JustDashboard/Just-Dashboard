package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLifecycleAdmissionExcludesOtherOperations(t *testing.T) {
	dir := t.TempDir()
	release, err := LockLifecycle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if another, err := LockLifecycle(dir); !errors.Is(err, ErrInProgress) {
		if another != nil {
			another()
		}
		t.Fatalf("concurrent admission = %v", err)
	}
	release()
	another, err := LockLifecycle(dir)
	if err != nil {
		t.Fatalf("admission did not release: %v", err)
	}
	another()
	for _, name := range []string{StateFile, "self-config.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"status":"pending"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := CheckLifecycleIdle(dir); !errors.Is(err, ErrInProgress) {
			t.Fatalf("ignored live %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"status":"success"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := CheckLifecycleIdle(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, StateFile), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckLifecycleIdle(dir); err == nil {
		t.Fatal("corrupt state treated as idle")
	}
}
