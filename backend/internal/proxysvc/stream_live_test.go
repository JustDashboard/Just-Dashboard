package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// liveStreamImage is nginx.org's build, which has the stream module this
// host's nginx lacks.
const liveStreamImage = "nginx:1.27-alpine"

// liveStreamNginx runs a real nginx with the stream module on the host
// network, as this user, reading <root>/stream.d, and puts a shim first on
// PATH so the Service's `nginx -t` and `nginx -s reload` reach it. Gated like
// the other live tests, and skipped where Docker or the image is missing.
func liveStreamNginx(t *testing.T) (*Service, string) {
	t.Helper()
	return liveStreamNginxWith(t, func(root string) string {
		return fmt.Sprintf("pid %[1]s/nginx.pid;\nerror_log %[1]s/error.log;\nevents {}\nstream {\n    include %[1]s/stream.d/*.conf;\n}\n", root)
	})
}

// liveStreamNginxWith is liveStreamNginx started on the nginx.conf conf writes for the
// directory, which it is handed.
func liveStreamNginxWith(t *testing.T, conf func(root string) string) (*Service, string) {
	t.Helper()
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to exercise a real nginx stream module")
	}
	if err := exec.Command("docker", "image", "inspect", liveStreamImage).Run(); err != nil {
		t.Skipf("%s is not available: %v", liveStreamImage, err)
	}
	root := t.TempDir()
	for _, dir := range []string{"stream.d", "bin", "modules-enabled"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(conf(root)), 0o644); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("jd-stream-live-%d", time.Now().UnixNano())
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "--network", "host",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "-v", root+":"+root,
		"--entrypoint", "nginx", liveStreamImage,
		"-e", root+"/startup.log", "-c", root+"/nginx.conf", "-g", "daemon off;").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := os.ReadFile(filepath.Join(root, "error.log"))
			t.Logf("nginx error.log:\n%s", logs)
		}
		exec.Command("docker", "rm", "-f", name).Run()
	})
	// `docker run -d` returns before nginx has read its configuration. A test
	// that changes nginx.conf or stream.d before then changed what nginx
	// started with, and one it refuses stops the container.
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(root, "nginx.pid")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("nginx did not start: %s", logs)
		}
	}
	shim := fmt.Sprintf("#!/bin/sh\nexec docker exec %s nginx -e %s/startup.log -c %s/nginx.conf \"$@\"\n", name, root, root)
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return New(root, filepath.Join(root, "Caddyfile")), root
}

func freeLoopbackPort(t *testing.T, network string) int {
	t.Helper()
	if network == "udp" {
		c, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.LocalAddr().(*net.UDPAddr).Port
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// freeLoopbackPortPair is a loopback port free for TCP and UDP at once, for a
// stream or a backend of both.
func freeLoopbackPortPair(t *testing.T) int {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		port := freeLoopbackPort(t, "tcp")
		c, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			c.Close()
			return port
		}
	}
	t.Fatal("no port free for TCP and UDP both")
	return 0
}

// Every shape the renderer writes passes a real nginx with the stream module,
// including two streams whose names folded to one upstream before.
func TestLiveStreamRendersPassNginx(t *testing.T) {
	svc, _ := liveStreamNginx(t)
	ctx := context.Background()
	specs := []*StreamSpec{
		{Name: "a-b", Listen: freeLoopbackPort(t, "tcp"), Address: "127.0.0.1", Upstream: "127.0.0.1:9", AllowFrom: []string{"127.0.0.1"}},
		{Name: "a_b", Listen: freeLoopbackPort(t, "tcp"), Address: "127.0.0.1", Upstream: "127.0.0.1:9", ConnectTimeout: 5, Timeout: 3600},
		{Name: "dns", Listen: freeLoopbackPort(t, "udp"), Address: "127.0.0.1", Protocol: "udp", UDPMode: "request", Upstream: "127.0.0.1:9"},
		{Name: "game", Listen: freeLoopbackPort(t, "udp"), Address: "127.0.0.1", Protocol: "udp", ProxyProtocol: true, Upstream: "127.0.0.1:9"},
		{Name: "socket", Listen: freeLoopbackPort(t, "tcp"), Address: "127.0.0.1", Upstream: "unix:/run/nothing.sock", AllowFrom: []string{"all"}},
		{Name: "both", Listen: freeLoopbackPortPair(t), Address: "127.0.0.1", Protocol: "both", UDPMode: "request", Upstream: "127.0.0.1:9",
			ConnectTimeout: 90, Timeout: 5400},
	}
	for _, spec := range specs {
		res, err := svc.ApplyStream(ctx, spec, "", true)
		if err != nil {
			t.Fatalf("%s: %v (%+v)", spec.Name, err, res)
		}
		if !res.Validation.Valid || !res.Reloaded || res.Validation.Note != "" {
			t.Fatalf("%s: %+v %+v", spec.Name, res, res.Validation)
		}
	}
}

// proxy_responses 1 ended a UDP session at the first reply, so each datagram
// reached the backend from a new nginx source port: a game server saw a new
// player per packet. A session stream keeps one; a request stream does not.
func TestLiveUDPSessionModes(t *testing.T) {
	svc, _ := liveStreamNginx(t)
	backend, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	sources := make(chan int, 64)
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := backend.ReadFrom(buf)
			if err != nil {
				return
			}
			sources <- from.(*net.UDPAddr).Port
			backend.WriteTo(buf[:n], from)
		}
	}()
	upstream := backend.LocalAddr().String()

	for _, tc := range []struct {
		mode      string
		samePorts bool
	}{{"session", true}, {"request", false}} {
		t.Run(tc.mode, func(t *testing.T) {
			spec := &StreamSpec{Name: "udp-" + tc.mode, Listen: freeLoopbackPort(t, "udp"), Address: "127.0.0.1",
				Protocol: "udp", UDPMode: tc.mode, Upstream: upstream}
			if _, err := svc.ApplyStream(context.Background(), spec, "", true); err != nil {
				t.Fatal(err)
			}
			client, err := net.Dial("udp4", fmt.Sprintf("127.0.0.1:%d", spec.Listen))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			seen := map[int]bool{}
			for i := 0; i < 3; i++ {
				var port int
				// The reload is asynchronous in the master; the first datagram
				// may arrive before the new listener does.
				for attempt := 0; ; attempt++ {
					client.Write([]byte(fmt.Sprintf("hello%d", i)))
					client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
					buf := make([]byte, 64)
					if _, err := client.Read(buf); err == nil {
						port = <-sources
						break
					}
					if attempt == 10 {
						t.Fatalf("no reply through %s", spec.Name)
					}
				}
				seen[port] = true
			}
			if (len(seen) == 1) != tc.samePorts {
				t.Fatalf("%s mode: the backend saw source ports %v", tc.mode, seen)
			}
		})
	}
}

// One stream of both protocols carries TCP and UDP on one port to the same
// upstream port, as DNS runs.
func TestLiveBothProtocolsForward(t *testing.T) {
	svc, _ := liveStreamNginx(t)
	port := freeLoopbackPortPair(t)
	tcp, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp, err := net.ListenPacket("udp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("tcp answer\n"))
			conn.Close()
		}
	}()
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			udp.WriteTo(append([]byte("udp answer to "), buf[:n]...), from)
		}
	}()

	spec := &StreamSpec{Name: "dns", Listen: freeLoopbackPortPair(t), Address: "127.0.0.1", Protocol: "both", UDPMode: "request",
		Upstream: fmt.Sprintf("127.0.0.1:%d", port), ConnectTimeout: 5, Timeout: 600}
	res, err := svc.ApplyStream(context.Background(), spec, "", true)
	if err != nil || !res.Reloaded {
		t.Fatalf("%v %+v", err, res)
	}
	for _, want := range []string{"proxy_connect_timeout 5s;", "proxy_timeout 10m;"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("missing %q:\n%s", want, res.Content)
		}
	}
	addr := fmt.Sprintf("127.0.0.1:%d", spec.Listen)
	deadline := time.Now().Add(5 * time.Second)
	for got := ""; !strings.HasPrefix(got, "tcp answer"); {
		if time.Now().After(deadline) {
			t.Fatalf("no TCP answer through the stream: %q", got)
		}
		if conn, err := net.DialTimeout("tcp4", addr, time.Second); err == nil {
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 64)
			n, _ := conn.Read(buf)
			conn.Close()
			got = string(buf[:n])
		}
		time.Sleep(50 * time.Millisecond)
	}
	client, err := net.Dial("udp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for attempt := 0; ; attempt++ {
		client.Write([]byte("query"))
		client.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		buf := make([]byte, 64)
		if n, err := client.Read(buf); err == nil {
			if got := string(buf[:n]); got != "udp answer to query" {
				t.Fatalf("UDP answered %q", got)
			}
			return
		}
		if attempt == 10 {
			t.Fatal("no UDP answer through the stream")
		}
	}
}

// A TCP stream forwards, and a stream bound to one address is reachable there.
func TestLiveTCPStreamForwards(t *testing.T) {
	svc, _ := liveStreamNginx(t)
	backend, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("SSH-2.0-OpenSSH_9.6\r\n"))
			conn.Close()
		}
	}()
	spec := &StreamSpec{Name: "bastion", Listen: freeLoopbackPort(t, "tcp"), Address: "127.0.0.1", Upstream: backend.Addr().String(),
		AllowFrom: []string{"127.0.0.1"}}
	if _, err := svc.ApplyStream(context.Background(), spec, "", true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", spec.Listen), time.Second)
		if err == nil {
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 64)
			n, _ := conn.Read(buf)
			conn.Close()
			if strings.HasPrefix(string(buf[:n]), "SSH-2.0") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing forwarded: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A link in stream.d to a file that is gone fails `nginx -t` for the whole
// host, and the page could not delete it. Deleting it from here now leaves a
// configuration nginx passes again.
func TestLiveADanglingStreamLinkIsDeletedAndNginxPassesAgain(t *testing.T) {
	svc, root := liveStreamNginx(t)
	ctx := context.Background()
	if err := os.Symlink(filepath.Join(root, "streams-available", "gone.conf"), filepath.Join(root, "stream.d", "dangling.conf")); err != nil {
		t.Fatal(err)
	}
	if res := runValidator(ctx, "nginx", "-t"); res.Valid || !strings.Contains(res.Output, "dangling.conf") {
		t.Fatalf("a dangling link should fail the test: %+v", res)
	}
	deleted, err := svc.DeleteStream(ctx, "dangling")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Read || deleted.Backup != "" {
		t.Fatalf("deleted = %+v", deleted)
	}
	if res := runValidator(ctx, "nginx", "-t"); !res.Valid {
		t.Fatalf("nginx still fails: %s", res.Output)
	}
}

// Included inside http, the stream directory is read, and nginx refuses a
// stream there: the save fails its test and does not claim the test missed it.
func TestLiveAStreamIncludedInsideHTTPIsRefusedByItsTest(t *testing.T) {
	svc, root := liveStreamNginx(t)
	conf := fmt.Sprintf("pid %[1]s/nginx.pid;\nerror_log %[1]s/error.log;\nevents {}\nhttp {\n    include %[1]s/stream.d/*.conf;\n}\n", root)
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := &StreamSpec{Name: "misplaced", Listen: freeLoopbackPort(t, "tcp"), Address: "127.0.0.1", Upstream: "127.0.0.1:9", AllowFrom: []string{"127.0.0.1"}}
	res, err := svc.ApplyStream(context.Background(), spec, "", false)
	if !errors.Is(err, ErrInvalidConf) {
		t.Fatalf("got %v, want the test to refuse it", err)
	}
	if !strings.Contains(res.Validation.Output, "not allowed here") || res.Validation.Note != "" {
		t.Fatalf("validation = %+v", res.Validation)
	}
	if _, err := os.Stat(filepath.Join(root, "stream.d", "misplaced.conf")); !os.IsNotExist(err) {
		t.Fatal("the refused file was left behind")
	}
}
