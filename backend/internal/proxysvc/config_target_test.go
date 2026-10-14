package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestConfigDryRunPreservesNativeReplacement(t *testing.T) {
	svc, _, file := checkedConfigHost(t, KindNginx)
	script := "#!/bin/sh\nprintf '%s' 'native edit during test' > '" + file + "'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(file), "bin", "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Validate(context.Background(), KindNginx, file, "dry-run candidate"); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("dry run = %v", err)
	}
	if got, _ := os.ReadFile(file); string(got) != "native edit during test" {
		t.Fatalf("native edit replaced by rollback: %q", got)
	}
}

func TestConfigDryRunRestoresPriorAbsence(t *testing.T) {
	svc, _, file := checkedConfigHost(t, KindNginx)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Validate(context.Background(), KindNginx, file, "dry-run candidate"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run file remained: %v", err)
	}
}

func TestRawConfigKeepsWritesAndRollbackOnPinnedParent(t *testing.T) {
	for _, operation := range []string{"nginx save", "caddy save", "nginx dry run", "nginx absent dry run"} {
		for _, replacement := range []string{"symlink", "directory"} {
			t.Run(operation+"/"+replacement, func(t *testing.T) {
				kind := KindNginx
				if operation == "caddy save" {
					kind = KindCaddy
				}
				svc, _, file := checkedConfigHost(t, kind)
				parent := filepath.Dir(file)
				moved := parent + "-original"
				outside := t.TempDir()
				original := "original\n"
				candidate := "staged candidate"
				shadow := filepath.Join(outside, filepath.Base(file))
				shadowContent := candidate
				if kind == KindCaddy {
					shadowContent = original
				}
				if err := os.WriteFile(shadow, []byte(shadowContent), 0o600); err != nil {
					t.Fatal(err)
				}
				absent := operation == "nginx absent dry run"
				if absent {
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
				}
				binary := "nginx"
				if kind == KindCaddy {
					binary = "caddy"
				}
				script := "#!/bin/sh\nmv -- '" + parent + "' '" + moved + "'\n"
				if replacement == "symlink" {
					script += "ln -s -- '" + outside + "' '" + parent + "'\n"
				} else {
					script += "mkdir -- '" + parent + "'\nprintf '%s' 'replacement directory content' > '" + file + "'\n"
				}
				script += "exit 0\n"
				if err := os.WriteFile(filepath.Join(parent, "bin", binary), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				var err error
				if operation == "nginx dry run" || absent {
					_, err = svc.Validate(context.Background(), kind, file, candidate)
				} else {
					_, err = svc.WriteConfigChecked(context.Background(), kind, file, candidate, ContentDigest(original))
				}
				if !errors.Is(err, ErrConfigChanged) {
					t.Fatalf("retargeted operation = %v", err)
				}
				if got, _ := os.ReadFile(shadow); string(got) != shadowContent {
					t.Fatalf("outside contents changed: %q", got)
				}
				if info, err := os.Stat(shadow); err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("outside metadata changed: %v %v", info, err)
				}
				restored := filepath.Join(moved, filepath.Base(file))
				if absent {
					if _, err := os.Stat(restored); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("absence not restored: %v", err)
					}
				} else if got, _ := os.ReadFile(restored); string(got) != original {
					t.Fatalf("original parent left with candidate: %q", got)
				}
				for _, dir := range []string{outside, moved} {
					matches, err := filepath.Glob(filepath.Join(dir, ".vpsd-*"))
					if err != nil || len(matches) > 0 {
						t.Fatalf("staging artifacts in %s: %v %v", dir, matches, err)
					}
				}
				if replacement == "directory" {
					if got, _ := os.ReadFile(file); string(got) != "replacement directory content" {
						t.Fatalf("new directory changed: %q", got)
					}
				}
			})
		}
	}
}

func TestProxyPathRefusesMissingSuffixUnderOutwardLink(t *testing.T) {
	svc, _, file := checkedConfigHost(t, KindNginx)
	link := filepath.Join(filepath.Dir(file), "outward")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.allowedPath(filepath.Join(link, "new", "nginx.conf")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("missing outward suffix allowed: %v", err)
	}
}

func TestConfigSnapshotRefusesNativeFIFOWithoutHoldingWriterLock(t *testing.T) {
	svc, _, file := checkedConfigHost(t, KindNginx)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(file, 0o600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := svc.ReadConfigSnapshot(file); result <- err }()
	select {
	case err := <-result:
		if !errors.Is(err, ErrConfigChanged) {
			t.Fatalf("FIFO read = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO read blocked while holding the writer lock")
	}
}

func TestAtomicConfigWritePreservesExistingParentSymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(link, "config"), "existing parent alias"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(real, "config")); string(got) != "existing parent alias" {
		t.Fatalf("contents=%q", got)
	}
}
