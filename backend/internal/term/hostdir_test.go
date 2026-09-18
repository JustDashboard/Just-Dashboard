package term

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// hostDir is the only thing standing between a requested directory and the
// shell that will start in it, so its contract is pinned here: a real
// directory passes, and anything else — missing, a file, empty — falls back
// to home rather than killing the new PTY on arrival.
func TestHostDirAcceptsRealDirectory(t *testing.T) {
	dir := t.TempDir()
	if got := hostDir(context.Background(), dir); got != dir {
		t.Fatalf("hostDir(%q) = %q, want the directory itself", dir, got)
	}
}

func TestHostDirRejectsMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if got := hostDir(context.Background(), missing); got != "" {
		t.Fatalf("hostDir(%q) = %q, want empty", missing, got)
	}
}

func TestHostDirRejectsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := hostDir(context.Background(), file); got != "" {
		t.Fatalf("hostDir(%q) = %q, want empty", file, got)
	}
}

func TestHostDirRejectsEmpty(t *testing.T) {
	if got := hostDir(context.Background(), ""); got != "" {
		t.Fatalf("hostDir(\"\") = %q, want empty", got)
	}
}
