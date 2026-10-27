package clihost

import (
	"os"
	"path/filepath"
	"testing"
)

// The server's jd is replaced only when it is there, is jd, and is not the
// one built beside the dashboard already.
func TestRefreshReplacesOnlyAnOlderJD(t *testing.T) {
	host := t.TempDir()
	if err := os.MkdirAll(filepath.Join(host, filepath.Dir(Path)), 0o755); err != nil {
		t.Fatal(err)
	}
	built := filepath.Join(t.TempDir(), "jd")
	if err := os.WriteFile(built, []byte("new build\n"+Marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(host, Path)

	if replaced, err := Refresh(host, built); err != nil || replaced {
		t.Fatalf("no jd on the server: replaced=%v %v", replaced, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("a jd was installed where there was none: %v", err)
	}

	if err := os.WriteFile(target, []byte("#!/bin/sh\necho someone else's jd\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if replaced, err := Refresh(host, built); err != nil || replaced {
		t.Fatalf("another program called jd: replaced=%v %v", replaced, err)
	}

	if err := os.WriteFile(target, []byte("old build\n"+Marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if replaced, err := Refresh(host, built); err != nil || !replaced {
		t.Fatalf("an older jd: replaced=%v %v", replaced, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new build\n"+Marker+"\n" {
		t.Fatalf("the server's jd is now %q %v", got, err)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", info.Mode())
	}
	if replaced, err := Refresh(host, built); err != nil || replaced {
		t.Fatalf("the current jd: replaced=%v %v", replaced, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".jd-*")); len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}

	if replaced, err := Refresh(host, filepath.Join(t.TempDir(), "missing")); err != nil || replaced {
		t.Fatalf("a dashboard built without jd: replaced=%v %v", replaced, err)
	}
}
