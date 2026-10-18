package remotedesk

import (
	"context"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// TestLiveSessionsDrawADesktop installs the real engine and opens a session
// to a throwaway Linux desktop over each protocol, through guacd, and waits
// for the first frame of the screen. It needs Docker and the two desktop
// images, so it only runs when asked:
//
//	JD_REMOTE_DESKTOP_LIVE=1 go test ./internal/remotedesk -run Live -v
//
// JD_REMOTE_DESKTOP_KEEP=1 leaves the engine installed afterwards.
func TestLiveSessionsDrawADesktop(t *testing.T) {
	if os.Getenv("JD_REMOTE_DESKTOP_LIVE") != "1" {
		t.Skip("set JD_REMOTE_DESKTOP_LIVE=1 to run against Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	host := os.Getenv("JD_DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	engine := NewDockerEngine(host)
	defer engine.Close()
	if err := engine.Install(ctx, func(line string) { t.Log(line) }); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("JD_REMOTE_DESKTOP_KEEP") != "1" {
		defer func() {
			if err := engine.Remove(context.Background()); err != nil {
				t.Errorf("remove the engine: %v", err)
			}
		}()
	}
	if st := engine.Status(ctx); st.State != EngineRunning || !strings.HasPrefix(st.Address, "127.0.0.1:") {
		t.Fatalf("engine after install: %#v", st)
	}

	cli, err := client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	desktops := []struct {
		name, image, protocol, username, password string
		port                                      int
	}{
		{"jd-remote-desktop-live-vnc", "consol/debian-xfce-vnc:latest", ProtocolVNC, "", "vncpassword", 5901},
		{"jd-remote-desktop-live-rdp", "scottyhardy/docker-remote-desktop:latest", ProtocolRDP, "ubuntu", "ubuntu", 3389},
	}
	svc := newTestService(t, engine)
	for _, d := range desktops {
		t.Run(d.protocol, func(t *testing.T) {
			cli.ContainerRemove(ctx, d.name, container.RemoveOptions{Force: true})
			created, err := cli.ContainerCreate(ctx, &container.Config{Image: d.image, Hostname: d.name},
				&container.HostConfig{NetworkMode: container.NetworkMode(engineName), ShmSize: 256 << 20},
				&network.NetworkingConfig{}, nil, d.name)
			if err != nil {
				t.Fatalf("create %s: %v", d.image, err)
			}
			defer cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
			if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
				t.Fatal(err)
			}
			inspect, err := cli.ContainerInspect(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			address := inspect.NetworkSettings.Networks[engineName].IPAddress
			pc, err := svc.Add(ctx, ComputerInput{Name: d.name, OS: OSLinux, Host: address, Protocol: d.protocol,
				Port: d.port, Username: d.username, Password: ptr(d.password)})
			if err != nil {
				t.Fatal(err)
			}
			// The desktops take a few seconds to start their servers; the
			// backend probes, as the add flow does, until the port answers.
			deadline := time.Now().Add(90 * time.Second)
			for svc.Probe(ctx, address, d.port).State != ReachOpen {
				if time.Now().After(deadline) {
					t.Fatalf("%s never opened port %d", d.image, d.port)
				}
				time.Sleep(time.Second)
			}
			var sess *Session
			for attempt := 0; ; attempt++ {
				sess, err = svc.Connect(ctx, pc.ID, Display{Width: 1280, Height: 800, DPI: 96, Timezone: "UTC"}, "live-test")
				if err == nil || attempt == 10 {
					break
				}
				time.Sleep(3 * time.Second)
			}
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer svc.Finish(context.Background(), sess, nil)
			sess.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			seen := map[string]int{}
			for seen["img"] == 0 || seen["sync"] == 0 {
				_, in, err := sess.rd.Read()
				if err != nil {
					t.Fatalf("after %v: %v", seen, err)
				}
				if in.Opcode == "error" {
					t.Fatalf("guacd: %v", errorFrom(in))
				}
				seen[in.Opcode]++
			}
			t.Logf("%s drew its first frame: %v", d.protocol, seen)
		})
	}
}

// TestLiveBridgeServesTheStreamPage installs the real stream bridge with its
// seeded configuration, makes it again once it has an address to publish
// WebRTC on, and reads its stream page and account the way the proxy does,
// under the dashboard's prefix and account header.
//
//	JD_REMOTE_DESKTOP_LIVE=1 go test ./internal/remotedesk -run LiveBridge -v
func TestLiveBridgeServesTheStreamPage(t *testing.T) {
	if os.Getenv("JD_REMOTE_DESKTOP_LIVE") != "1" {
		t.Skip("set JD_REMOTE_DESKTOP_LIVE=1 to run against Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	host := os.Getenv("JD_DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	media := ""
	// Its own name, volume and port, so a server with Stream mode installed
	// keeps its bridge and pairings while this runs.
	spec := bridgeSpec
	spec.name, spec.volume, spec.prefer = "jd-live-test-stream", "jd-live-test-stream-data", 4890
	spec.media = func(context.Context) string { return media }
	bridge := newDockerEngine(host, spec)
	defer bridge.Close()
	if err := bridge.Install(ctx, func(line string) { t.Log(line) }); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("JD_REMOTE_DESKTOP_KEEP") != "1" {
		defer func() {
			if err := bridge.Remove(context.Background()); err != nil {
				t.Errorf("remove the bridge: %v", err)
			}
		}()
	}
	st := bridge.Status(ctx)
	if st.State != EngineRunning || !strings.HasPrefix(st.Address, "127.0.0.1:") || st.Media != "" {
		t.Fatalf("bridge after install: %#v", st)
	}
	// Loopback stands in for the tailnet address: the bridge is made again
	// with its UDP block published there.
	media = "127.0.0.1"
	if err := bridge.Install(ctx, func(line string) { t.Log(line) }); err != nil {
		t.Fatal(err)
	}
	st = bridge.Status(ctx)
	if st.State != EngineRunning || !strings.HasPrefix(st.Media, "127.0.0.1:") {
		t.Fatalf("bridge after media: %#v", st)
	}
	// The image's own range outranks its config, so the block is in its env.
	inspected, err := bridge.(*dockerEngine).cli.ContainerInspect(ctx, spec.name)
	if err != nil {
		t.Fatal(err)
	}
	first, last, _ := strings.Cut(strings.TrimPrefix(st.Media, "127.0.0.1:"), "-")
	if !slices.Contains(inspected.Config.Env, "WEBRTC_PORT_RANGE="+first+":"+last) {
		t.Fatalf("env %v, media %s", inspected.Config.Env, st.Media)
	}
	get := func(path string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+st.Address+BridgePrefix+path, nil)
		req.Header.Set(BridgeUserHeader, BridgeAccount)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}
	if code, body := get("/stream.html"); code != http.StatusOK || !strings.Contains(body, "stream.js") {
		t.Fatalf("stream page: %d %.200s", code, body)
	}
	if code, body := get("/api/user"); code != http.StatusOK || !strings.Contains(body, `"name":"`+BridgeAccount+`"`) {
		t.Fatalf("account: %d %s", code, body)
	}
}
