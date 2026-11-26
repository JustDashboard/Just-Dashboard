package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Wayy01/Just-Dashboard/backend/internal/clihost"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A goroutine that ends while locked takes its thread with it, and the one
// thread that cannot go is parked forever in whatever namespace it was moved
// to. So no such goroutine may ever be given the main thread.
func TestLockedGoroutinesNeverGetTheMainThread(t *testing.T) {
	var wg sync.WaitGroup
	for range 4000 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			if unix.Gettid() == os.Getpid() {
				t.Error("a goroutine was scheduled on the main thread")
			}
		}()
		runtime.Gosched()
	}
	wg.Wait()
}

// A backend running on the server itself has no /host mount. The boot
// refresh still reaches the jd the installer put at /usr/local/bin/jd, and
// writes nothing under a /host that is not there.
func TestBootRefreshUpdatesTheServersJDWithoutAHostMount(t *testing.T) {
	server := t.TempDir()
	onServer := func(path string) string { return filepath.Join(server, path) }
	target := onServer(clihost.Path)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("installed build\n"+clihost.Marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	built := filepath.Join(t.TempDir(), "jd")
	if err := os.WriteFile(built, []byte("this build\n"+clihost.Marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	refreshHostCLI(slog.New(slog.DiscardHandler), built, onServer)

	if got, err := os.ReadFile(target); err != nil || string(got) != "this build\n"+clihost.Marker+"\n" {
		t.Fatalf("the server's jd is %q %v", got, err)
	}
	if _, err := os.Lstat(onServer("/host")); !os.IsNotExist(err) {
		t.Fatalf("the refresh wrote under /host: %v", err)
	}

	// The path the dashboard boots with is the server's own file, as this
	// test process opens it: never one under /host.
	if got := hostexec.HostPath(clihost.Path); got != clihost.Path && got != "/proc/1/root"+clihost.Path {
		t.Fatalf("the boot refresh would write %s", got)
	}
}
