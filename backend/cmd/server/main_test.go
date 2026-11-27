package main

import (
	"crypto/tls"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Wayy01/Just-Dashboard/backend/internal/agent"
	"github.com/Wayy01/Just-Dashboard/backend/internal/clihost"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// Agent mode serves JD_ADDR over TLS because a hub on another machine must
// reach it (invariant 7). The handshake asks every caller for a certificate
// and requires none, since /agent/info and /agent/enrol answer before a hub
// is pinned; httpx.HubOnly refuses every feature route without the pinned
// one.
func TestAgentModeServesTLSThatAsksEveryCallerForACertificate(t *testing.T) {
	id, err := agent.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := agentTLS(id)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientAuth != tls.RequestClientCert {
		t.Errorf("ClientAuth = %v, want RequestClientCert", cfg.ClientAuth)
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		t.Errorf("MinVersion = %#x, want TLS 1.2 or later", cfg.MinVersion)
	}
	if len(cfg.Certificates) != 1 || agent.Fingerprint(cfg.Certificates[0].Certificate[0]) != id.Fingerprint() {
		t.Errorf("the listener does not serve the agent's own certificate, the one a hub pins")
	}
}

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
