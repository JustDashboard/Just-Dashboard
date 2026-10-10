package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsentSnapshotDirectorySyncFailureKeepsRecoveryNonterminal(t *testing.T) {
	s := testService(t)
	record(t, "systemctl")
	if err := writeFileAtomic(s.paths.Sysctl, []byte("candidate"), 0o644); err != nil {
		t.Fatal(err)
	}
	j := &changeJournal{Paths: s.paths, ChangeStatus: ChangeStatus{ID: "absence", Generation: strings.Repeat("a", 64), Phase: "prepared", Watchdog: "armed", Persistence: "written"}, Files: []recoverySnapshot{{Path: s.paths.Sysctl, Exists: false}}}
	previous := syncNetworkDirectory
	called := ""
	syncNetworkDirectory = func(path string) error { called = path; return errors.New("injected directory sync failure") }
	t.Cleanup(func() { syncNetworkDirectory = previous })
	if err := recoverChange(context.Background(), j); err == nil {
		t.Fatal("an undurable absence was reported recovered")
	}
	if called != filepath.Dir(s.paths.Sysctl) || j.Phase != "degraded" || changeTerminal(j.Phase) || j.Persistence != "unknown" || len(j.RecoveryErrors) == 0 {
		t.Fatalf("failure did not remain visible: directory=%s journal=%+v", called, j)
	}
	if _, err := os.Stat(s.paths.Sysctl); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("test did not reach the successful unlink before failed sync")
	}
	// Recovery retries the same containing directory even though the entry is
	// already absent, then and only then may record its terminal outcome.
	syncNetworkDirectory = previous
	if err := recoverChange(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if j.Phase != "recovered" {
		t.Fatalf("retry did not complete: %+v", j)
	}
}

func TestAbsentSnapshotSyncsExistingParentAndDoesNotCreateMissingParent(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "already-absent")
	previous := syncNetworkDirectory
	calls := []string{}
	syncNetworkDirectory = func(path string) error { calls = append(calls, path); return previous(path) }
	t.Cleanup(func() { syncNetworkDirectory = previous })
	if err := (savedNetworkFile{}).restore(path); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != parent {
		t.Fatalf("already absent entry did not flush exact parent: %v", calls)
	}
	missing := filepath.Join(parent, "missing-parent", "candidate")
	if err := (savedNetworkFile{}).restore(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("absence restoration created a new directory")
	}
}
