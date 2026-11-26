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

	if replaced, err := Refresh(target, built); err != nil || replaced {
		t.Fatalf("no jd on the server: replaced=%v %v", replaced, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("a jd was installed where there was none: %v", err)
	}

	if err := os.WriteFile(target, []byte("#!/bin/sh\necho someone else's jd\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if replaced, err := Refresh(target, built); err != nil || replaced {
		t.Fatalf("another program called jd: replaced=%v %v", replaced, err)
	}

	if err := os.WriteFile(target, []byte("old build\n"+Marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if replaced, err := Refresh(target, built); err != nil || !replaced {
		t.Fatalf("an older jd: replaced=%v %v", replaced, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new build\n"+Marker+"\n" {
		t.Fatalf("the server's jd is now %q %v", got, err)
	}
	if info, _ := os.Stat(target); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v", info.Mode())
	}
	if replaced, err := Refresh(target, built); err != nil || replaced {
		t.Fatalf("the current jd: replaced=%v %v", replaced, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".jd-*")); len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}

	if replaced, err := Refresh(target, filepath.Join(t.TempDir(), "missing")); err != nil || replaced {
		t.Fatalf("a dashboard built without jd: replaced=%v %v", replaced, err)
	}
}

// Install puts jd on the server only where nothing is: a jd that is there
// already, or another program called jd, is never touched.
func TestInstallPutsJDOnlyWhereNothingIs(t *testing.T) {
	host := t.TempDir()
	built := filepath.Join(t.TempDir(), "jd")
	if err := os.WriteFile(built, []byte("this build\n"+Marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(host, Path)

	// /usr/local/bin may not exist yet on a minimal host.
	if installed, err := Install(target, built); err != nil || !installed {
		t.Fatalf("nothing there: installed=%v %v", installed, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("installed jd = %v, %v", info, err)
	}
	if raw, _ := os.ReadFile(target); string(raw) != "this build\n"+Marker+"\n" {
		t.Fatalf("installed content = %q", raw)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".jd-*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}

	// Already there: left as it is, whatever it is.
	for _, content := range []string{"older build\n" + Marker + "\n", "#!/bin/sh\necho someone else's jd\n"} {
		if err := os.WriteFile(target, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		if installed, err := Install(target, built); err != nil || installed {
			t.Fatalf("%q: installed=%v %v", content, installed, err)
		}
		if raw, _ := os.ReadFile(target); string(raw) != content {
			t.Fatalf("an existing file was replaced: %q", raw)
		}
	}

	// A build that is not jd is never installed.
	other := filepath.Join(t.TempDir(), "jd")
	if err := os.WriteFile(other, []byte("not it"), 0o755); err != nil {
		t.Fatal(err)
	}
	if installed, err := Install(filepath.Join(t.TempDir(), Path), other); err == nil || installed {
		t.Fatalf("not jd: installed=%v %v", installed, err)
	}
}
