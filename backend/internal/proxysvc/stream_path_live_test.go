package proxysvc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// streamModule is where Debian and Ubuntu ship nginx's dynamic stream module.
const streamModule = "/usr/lib/nginx/modules/ngx_stream_module.so"

// The host's nginx binary on a private prefix, loading the host's stream
// module, forwarding a loopback port to a backend this test holds: while a
// client's connection is open the stream's path counts it and nginx's own
// connection to the backend, and once it closes the session nginx logged is
// found by its client, naming the backend it reached.
func TestLiveStreamPathReadsNginxsBackendLeg(t *testing.T) {
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx is not installed")
	}
	if _, err := os.Stat(streamModule); err != nil {
		t.Skip("the stream module is not installed")
	}
	root := t.TempDir()
	for _, dir := range []string{"stream.d", "logs", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	previous := streamLogDir
	streamLogDir = filepath.Join(root, "logs")
	t.Cleanup(func() { streamLogDir = previous })
	conf := fmt.Sprintf("load_module %[2]s;\npid %[1]s/nginx.pid;\nerror_log %[1]s/logs/error.log;\nevents {}\nstream {\n    include %[1]s/stream.d/*.conf;\n}\n", root, streamModule)
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf("#!/bin/sh\nexec '%s' -e '%s/logs/startup.log' -p '%s' -c '%s/nginx.conf' \"$@\"\n", binary, root, root, root)
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))

	backend, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	accepted := make(chan struct{}, 4)
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			go func() { _, _ = io.Copy(io.Discard, conn); conn.Close() }()
		}
	}()

	port := freePort(t)
	svc := New(root, filepath.Join(root, "Caddyfile"))
	content, err := RenderStream(&StreamSpec{Name: "pg", Listen: port, Address: "127.0.0.1", Protocol: "tcp",
		Upstream: backend.Addr().String(), LogConnections: true})
	if err != nil {
		t.Fatal(err)
	}
	writeStream(t, svc, "pg", content)
	var output bytes.Buffer
	nginx := exec.Command(binary, "-e", root+"/logs/startup.log", "-p", root, "-c", root+"/nginx.conf", "-g", "daemon off;")
	nginx.Stdout, nginx.Stderr = &output, &output
	if err := nginx.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = nginx.Process.Signal(syscall.SIGQUIT)
		_ = nginx.Wait()
		if t.Failed() {
			logs, _ := os.ReadFile(filepath.Join(root, "logs", "error.log"))
			t.Logf("nginx: %s\nerror.log:\n%s", output.String(), logs)
		}
	})

	var client net.Conn
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if client, err = net.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stream never listened: %v", err)
		}
	}
	sent := time.Now()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("nginx never connected to the backend")
	}
	ctx := context.Background()
	path, err := svc.StreamPath(ctx, "pg", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !path.SocketsRead || path.Clients < 1 || len(path.Servers) != 1 || path.Servers[0].Connections < 1 {
		t.Fatalf("with a session open: %+v", path)
	}
	client.Close()

	session, err := svc.AwaitStreamSession(ctx, "pg", "127.0.0.1", sent.Add(-time.Second), 3*time.Second)
	if err != nil || session == nil {
		t.Fatalf("no logged session: %+v, %v", session, err)
	}
	if session.Status != 200 || session.Upstream != backend.Addr().String() {
		t.Fatalf("session = %+v, want 200 to %s", session, backend.Addr())
	}
	path, err = svc.StreamPath(ctx, "pg", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if path.LogSessions < 1 || path.Servers[0].Sessions < 1 {
		t.Fatalf("after the session: %+v", path)
	}
}
