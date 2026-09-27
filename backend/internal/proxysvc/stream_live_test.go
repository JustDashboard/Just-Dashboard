package proxysvc

import (
	"context"
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
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to exercise a real nginx stream module")
	}
	if err := exec.Command("docker", "image", "inspect", liveStreamImage).Run(); err != nil {
		t.Skipf("%s is not available: %v", liveStreamImage, err)
	}
	root := t.TempDir()
	for _, dir := range []string{"stream.d", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conf := fmt.Sprintf("pid %[1]s/nginx.pid;\nerror_log %[1]s/error.log;\nevents {}\nstream {\n    include %[1]s/stream.d/*.conf;\n}\n", root)
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(conf), 0o644); err != nil {
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
