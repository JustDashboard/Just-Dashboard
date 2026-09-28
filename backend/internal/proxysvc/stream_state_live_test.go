package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The states are read from the running nginx itself: the master found by the
// configuration it was started on, the sockets it holds, and the bind it
// logged as failed.
func TestLiveStreamStatesAreReadFromNginx(t *testing.T) {
	svc, root := liveStreamNginx(t)
	withListeners(t, ListListeners)
	previous := readNginx
	readNginx = func(ctx context.Context, s *Service, files []ConfigFile, master int32) *socketView {
		return s.readNginxSockets(ctx, files, master)
	}
	t.Cleanup(func() { readNginx = previous })
	ctx := context.Background()

	port := freeLoopbackPort(t, "tcp")
	res, err := svc.ApplyStream(ctx, &StreamSpec{Name: "live", Listen: port, Address: "127.0.0.1", Upstream: "127.0.0.1:9",
		AllowFrom: []string{"127.0.0.1"}}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Listening == nil || !*res.Listening {
		t.Fatalf("a stream nginx took up: %+v", res)
	}

	// This process holds a port, a hand-written stream asks for it, and the
	// reload nginx is sent fails on it — while the command exits 0.
	blocker, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	held := blocker.Addr().(*net.TCPAddr).Port
	writeStream(t, svc, "held", fmt.Sprintf("server { listen 127.0.0.1:%d; proxy_pass 127.0.0.1:9; }\n", held))
	// Two streams on one address: nginx warns and the first takes it.
	writeStream(t, svc, "zz-copy", fmt.Sprintf("server { listen 127.0.0.1:%d; proxy_pass 127.0.0.1:10; }\n", port))
	if reloaded, _, failure := reloadNginxChecked(ctx); !reloaded {
		t.Fatalf("the reload command failed: %s", failure)
	}
	logged := fmt.Sprintf("bind() to 127.0.0.1:%d failed", held)
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(mustRead(t, filepath.Join(root, "error.log")), logged); {
		if time.Now().After(deadline) {
			t.Fatalf("nginx never logged %q", logged)
		}
		time.Sleep(50 * time.Millisecond)
	}

	status, err := svc.Streams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]StreamEntry{}
	for _, entry := range status.Streams {
		got[entry.Name] = entry
	}
	if entry := got["live"]; entry.State != StreamLive {
		t.Errorf("live: %s %q", entry.State, entry.StateReason)
	}
	if entry := got["zz-copy"]; entry.State != StreamShadowed || entry.Blocker == nil || entry.Blocker.Name != "live" {
		t.Errorf("zz-copy: %s %q %+v", entry.State, entry.StateReason, entry.Blocker)
	}
	entry := got["held"]
	if entry.State != StreamNotListening || entry.Blocker == nil || entry.Blocker.PID != int32(os.Getpid()) ||
		!strings.Contains(entry.BindError, logged) || !strings.Contains(entry.StateReason, "every reload fails") {
		t.Errorf("held: %s %q %+v %q", entry.State, entry.StateReason, entry.Blocker, entry.BindError)
	}
}

// A port taken between the check and the reload — the one moment the check
// cannot see — makes nginx refuse the reload. The save puts the stream back
// and says so, and the next reload is not left to fail on it.
func TestLiveASaveNginxCannotBindIsPutBack(t *testing.T) {
	svc, root := liveStreamNginx(t)
	withListeners(t, ListListeners)
	previous := readNginx
	readNginx = func(ctx context.Context, s *Service, files []ConfigFile, master int32) *socketView {
		return s.readNginxSockets(ctx, files, master)
	}
	t.Cleanup(func() { readNginx = previous })

	// The shim waits, when it is sent to reload, for this test to take the
	// port first.
	shimPath := filepath.Join(root, "bin", "nginx")
	shim := mustRead(t, shimPath)
	wait := fmt.Sprintf(`case "$1" in
-s) touch '%[1]s/reloading'; i=0
    while [ ! -e '%[1]s/held' ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done ;;
esac
`, root)
	if err := os.WriteFile(shimPath, []byte(strings.Replace(shim, "\n", "\n"+wait, 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	port := freeLoopbackPort(t, "tcp")
	taken := make(chan net.Listener, 1)
	go func() {
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(root, "reloading")); err != nil {
				continue
			}
			l, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
			taken <- l
			if err == nil {
				os.WriteFile(filepath.Join(root, "held"), nil, 0o644)
			}
			return
		}
		taken <- nil
	}()

	_, err := svc.ApplyStream(context.Background(), &StreamSpec{Name: "late", Listen: port, Address: "127.0.0.1",
		Upstream: "127.0.0.1:9", AllowFrom: []string{"127.0.0.1"}}, "", true)
	if l := <-taken; l != nil {
		defer l.Close()
	} else {
		t.Fatal("the port was not taken during the reload")
	}
	var inUse *PortInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("got %v, want the save refused on nginx's bind", err)
	}
	if !strings.Contains(inUse.BindError, fmt.Sprintf("bind() to 127.0.0.1:%d failed", port)) || inUse.PID != int32(os.Getpid()) {
		t.Fatalf("refusal = %+v", inUse)
	}
	if _, err := os.Stat(filepath.Join(root, "stream.d", "late.conf")); !os.IsNotExist(err) {
		t.Fatal("the stream nginx could not bind was left for every later reload to fail on")
	}
	os.Remove(filepath.Join(root, "reloading"))
	if res := runValidator(context.Background(), "nginx", "-t"); !res.Valid {
		t.Fatalf("nginx fails its test after the rollback: %s", res.Output)
	}
}
