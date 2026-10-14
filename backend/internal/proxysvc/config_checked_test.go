package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func checkedConfigHost(t *testing.T, kind Kind) (*Service, *changeLog, string) {
	t.Helper()
	svc, log, root := recordingService(t)
	if err := os.WriteFile(filepath.Join(root, "bin", "caddy"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := caddyScratchRoot
	caddyScratchRoot = filepath.Join(t.TempDir(), "scratch")
	t.Cleanup(func() { caddyScratchRoot = previous })
	file := filepath.Join(root, "nginx.conf")
	if kind == KindCaddy {
		file = filepath.Join(root, "Caddyfile")
	}
	if err := os.WriteFile(file, []byte("original\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return svc, log, file
}

func TestCheckedConfigRefusesStaleAndRemovedFiles(t *testing.T) {
	for _, kind := range []Kind{KindNginx, KindCaddy} {
		t.Run(string(kind), func(t *testing.T) {
			svc, log, file := checkedConfigHost(t, kind)
			base, err := svc.ReadConfigSnapshot(file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.WriteConfigChecked(context.Background(), kind, file, "operator A\n", base.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.WriteConfigChecked(context.Background(), kind, file, "operator B stale\n", base.Revision); !errors.Is(err, ErrConfigChanged) {
				t.Fatalf("stale save = %v", err)
			}
			if got, _ := os.ReadFile(file); string(got) != "operator A\n" {
				t.Fatalf("newer content overwritten: %q", got)
			}
			if len(log.changes) != 1 {
				t.Fatalf("recorded %d successful writes", len(log.changes))
			}
			if st, _ := os.Stat(file); st.Mode().Perm() != 0o640 {
				t.Fatalf("permissions changed: %v", st.Mode())
			}
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.WriteConfigChecked(context.Background(), kind, file, "resurrected", base.Revision); !errors.Is(err, ErrConfigChanged) {
				t.Fatalf("removed save = %v", err)
			}
			if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("deleted file restored: %v", err)
			}
		})
	}
}

func TestCheckedConfigSerializesRevisionComparison(t *testing.T) {
	for _, kind := range []Kind{KindNginx, KindCaddy} {
		t.Run(string(kind), func(t *testing.T) {
			svc, _, file := checkedConfigHost(t, kind)
			base, _ := svc.ReadConfigSnapshot(file)
			var wg sync.WaitGroup
			results := make(chan error, 2)
			for _, content := range []string{"A", "B"} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := svc.WriteConfigChecked(context.Background(), kind, file, content, base.Revision)
					results <- err
				}()
			}
			wg.Wait()
			close(results)
			successes, conflicts := 0, 0
			for err := range results {
				if err == nil {
					successes++
				} else if errors.Is(err, ErrConfigChanged) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
			}
		})
	}
}

func TestCheckedConfigPreservesNativeEditsDuringValidation(t *testing.T) {
	for _, kind := range []Kind{KindNginx, KindCaddy} {
		t.Run(string(kind), func(t *testing.T) {
			svc, log, file := checkedConfigHost(t, kind)
			base, _ := svc.ReadConfigSnapshot(file)
			binary := "nginx"
			if kind == KindCaddy {
				binary = "caddy"
			}
			// The native validator stands in for another native writer while
			// the dashboard is checking a candidate, outside its Go lock.
			script := "#!/bin/sh\nprintf '%s' 'native edit' > '" + file + "'\nexit 0\n"
			if err := os.WriteFile(filepath.Join(filepath.Dir(file), "bin", binary), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.WriteConfigChecked(context.Background(), kind, file, "candidate", base.Revision); !errors.Is(err, ErrConfigChanged) {
				t.Fatalf("native edit = %v", err)
			}
			if got, _ := os.ReadFile(file); string(got) != "native edit" {
				t.Fatalf("native edit overwritten: %q", got)
			}
			if len(log.changes) != 0 {
				t.Fatal("conflicted save recorded as installed")
			}
		})
	}
}

func TestCheckedConfigRestoresOnlyItsRefusedCandidate(t *testing.T) {
	for _, kind := range []Kind{KindNginx, KindCaddy} {
		t.Run(string(kind), func(t *testing.T) {
			svc, _, file := checkedConfigHost(t, kind)
			base, _ := svc.ReadConfigSnapshot(file)
			binary := "nginx"
			if kind == KindCaddy {
				binary = "caddy"
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(file), "bin", binary), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.WriteConfigChecked(context.Background(), kind, file, "refused", base.Revision); !errors.Is(err, ErrInvalidConf) {
				t.Fatalf("refused save = %v", err)
			}
			if got, _ := os.ReadFile(file); string(got) != base.Content {
				t.Fatalf("refused candidate left installed: %q", got)
			}
		})
	}
}

func TestCheckedConfigRestoresAnInterruptedCandidate(t *testing.T) {
	for _, kind := range []Kind{KindNginx, KindCaddy} {
		t.Run(string(kind), func(t *testing.T) {
			svc, _, file := checkedConfigHost(t, kind)
			base, _ := svc.ReadConfigSnapshot(file)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := svc.WriteConfigChecked(ctx, kind, file, "interrupted candidate", base.Revision); !errors.Is(err, ErrTestUnfinished) {
				t.Fatalf("interrupted save = %v", err)
			}
			if got, _ := os.ReadFile(file); string(got) != base.Content {
				t.Fatalf("interrupted candidate left installed: %q", got)
			}
		})
	}
}

func TestConfigSnapshotWaitsForTransientValidation(t *testing.T) {
	svc, _, file := checkedConfigHost(t, KindNginx)
	svc.mu.Lock()
	if err := os.WriteFile(file, []byte("transient validation candidate"), 0o640); err != nil {
		svc.mu.Unlock()
		t.Fatal(err)
	}
	result := make(chan *ConfigSnapshot, 1)
	failure := make(chan error, 1)
	go func() {
		snapshot, err := svc.ReadConfigSnapshot(file)
		if err != nil {
			failure <- err
		} else {
			result <- snapshot
		}
	}()
	select {
	case snapshot := <-result:
		svc.mu.Unlock()
		t.Fatalf("read exposed candidate before writer restored it: %+v", snapshot)
	case err := <-failure:
		svc.mu.Unlock()
		t.Fatal(err)
	case <-time.After(40 * time.Millisecond):
	}
	if err := os.WriteFile(file, []byte("original\n"), 0o640); err != nil {
		svc.mu.Unlock()
		t.Fatal(err)
	}
	svc.mu.Unlock()
	select {
	case snapshot := <-result:
		if snapshot.Content != "original\n" || snapshot.Revision != ContentDigest(snapshot.Content) {
			t.Fatalf("read = %+v", snapshot)
		}
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("read did not resume after writer finished")
	}
}
