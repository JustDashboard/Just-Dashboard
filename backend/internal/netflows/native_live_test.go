package netflows

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type fixtureMessage struct {
	PID, ClientPort, ServerPort, UDPPort, Payload int
	ShortPorts                                    []int
	Error                                         string
}

// This helper runs only in a disposable namespace or exact owned test
// container. Its stdin control channel never adds network traffic to samples.
func TestFlowFixtureProcess(t *testing.T) {
	if os.Getenv("JD_NETFLOWS_HELPER") != "1" {
		t.Skip("fixture subprocess")
	}
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	peer, err := net.DialUDP("udp4", nil, udp.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.Write([]byte("fixture"))
	msg := fixtureMessage{PID: os.Getpid(), UDPPort: peer.LocalAddr().(*net.UDPAddr).Port}
	var listener net.Listener
	var client, server net.Conn
	if os.Getenv("JD_NETFLOWS_UDP_ONLY") != "1" {
		listener, err = net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		client, err = net.Dial("tcp4", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		server, err = listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer server.Close()
		msg.ClientPort = client.LocalAddr().(*net.TCPAddr).Port
		msg.ServerPort = server.LocalAddr().(*net.TCPAddr).Port
		send := func(from, to net.Conn, n int) error {
			done := make(chan error, 1)
			go func() { _, err := io.CopyN(io.Discard, to, int64(n)); done <- err }()
			_, err := from.Write(make([]byte, n))
			if err != nil {
				return err
			}
			return <-done
		}
		if err = send(client, server, 4096); err != nil {
			t.Fatal(err)
		}
		if err = send(server, client, 2048); err != nil {
			t.Fatal(err)
		}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.Encode(msg)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch scanner.Text() {
		case "payload":
			const size = 131072
			done := make(chan error, 1)
			go func() { _, err := io.CopyN(io.Discard, server, size); done <- err }()
			_, err := client.Write(make([]byte, size))
			if err == nil {
				err = <-done
			}
			response := fixtureMessage{Payload: size}
			if err != nil {
				response.Error = err.Error()
			}
			encoder.Encode(response)
		case "short":
			ports := []int{}
			for i := 0; i < 32; i++ {
				c, err := net.Dial("tcp4", listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				s, err := listener.Accept()
				if err != nil {
					t.Fatal(err)
				}
				ports = append(ports, c.LocalAddr().(*net.TCPAddr).Port)
				c.Close()
				s.Close()
			}
			encoder.Encode(fixtureMessage{ShortPorts: ports})
		case "quit":
			return
		default:
			t.Fatal("unknown fixture command")
		}
	}
}

type fixtureProcess struct {
	cmd   *exec.Cmd
	in    io.WriteCloser
	lines chan []byte
	ready fixtureMessage
}

func startFixture(t *testing.T, cmd *exec.Cmd) *fixtureProcess {
	t.Helper()
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &fixtureProcess{cmd: cmd, in: in, lines: make(chan []byte, 8)}
	go func() {
		defer close(p.lines)
		scanner := bufio.NewScanner(out)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			p.lines <- line
		}
	}()
	t.Cleanup(func() {
		p.in.Write([]byte("quit\n"))
		p.in.Close()
		done := make(chan error, 1)
		go func() { done <- p.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	})
	p.ready = p.read(t)
	return p
}
func (p *fixtureProcess) read(t *testing.T) fixtureMessage {
	t.Helper()
	select {
	case line, ok := <-p.lines:
		var msg fixtureMessage
		if !ok || json.Unmarshal(line, &msg) != nil || msg.Error != "" {
			t.Fatalf("fixture response=%s", line)
		}
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("fixture response timed out")
	}
	return fixtureMessage{}
}
func (p *fixtureProcess) command(t *testing.T, command string) fixtureMessage {
	t.Helper()
	if _, err := p.in.Write([]byte(command + "\n")); err != nil {
		t.Fatal(err)
	}
	return p.read(t)
}
func nativeExec(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %s: %v", name, args, out, err)
	}
	return strings.TrimSpace(string(out))
}
func privateNamespace(t *testing.T) string {
	t.Helper()
	if os.Getenv("JD_NETFLOWS_LIVE") != "1" || os.Geteuid() != 0 {
		t.Skip("JD_NETFLOWS_LIVE=1 and root are required for disposable native fixtures")
	}
	name := fmt.Sprintf("jdf-%x", time.Now().UnixNano())
	nativeExec(t, "ip", "netns", "add", name)
	t.Cleanup(func() { nativeExec(t, "ip", "netns", "del", name) })
	nativeExec(t, "ip", "-n", name, "link", "set", "lo", "up")
	return name
}
func observedCycle(t *testing.T, n *Native) Cycle {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	c := n.Collect(ctx)
	for _, src := range c.Sources {
		if src.Status == "unavailable" {
			t.Fatalf("native source unavailable: %+v", src)
		}
	}
	return c
}
func socketAt(c Cycle, port int) *Socket {
	for _, src := range c.Sources {
		for _, sk := range src.Values {
			if sk.Protocol == "tcp" && sk.LocalPort == port {
				copy := sk
				return &copy
			}
		}
	}
	return nil
}
func TestLiveNativeTCPUDPShortFlowCoverageAndMeasuredOverhead(t *testing.T) {
	ns := privateNamespace(t)
	cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", ns, os.Args[0], "-test.run=^TestFlowFixtureProcess$")
	cmd.Env = append(os.Environ(), "JD_NETFLOWS_HELPER=1")
	p := startFixture(t, cmd)
	n := &Native{hostNamespacePath: filepath.Join("/var/run/netns", ns)}
	first := observedCycle(t, n)
	sk := socketAt(first, p.ready.ClientPort)
	if sk == nil || sk.Tx == nil || sk.Rx == nil || sk.Owner.PID != p.ready.PID || sk.Owner.Status != "verified_process" {
		t.Fatalf("native TCP=%+v cycle=%+v", sk, first)
	}
	udp := false
	for _, src := range first.Sources {
		for _, sk := range src.Values {
			if sk.Protocol == "udp" && sk.LocalPort == p.ready.UDPPort {
				udp = true
				if sk.Tx != nil || sk.Rx != nil || sk.RemoteAddress != "127.0.0.1" {
					t.Fatalf("UDP fabricated bytes/peer: %+v", sk)
				}
			}
		}
	}
	if !udp {
		t.Fatal("held UDP peer not observed")
	}
	d := differencer{}
	d.observe(&first, 30*time.Second)
	payload := p.command(t, "payload")
	second := observedCycle(t, n)
	if !first.At.Truncate(time.Hour).Equal(second.At.Truncate(time.Hour)) {
		t.Skip("fixture crossed UTC-hour boundary")
	}
	rows := d.observe(&second, 30*time.Second)
	found := false
	for _, row := range rows {
		if row.Socket.LocalPort == p.ready.ClientPort && row.Socket.Protocol == "tcp" {
			found = true
			if row.TxBytes == nil || *row.TxBytes != uint64(payload.Payload) {
				t.Fatalf("native measured TCP delta=%+v payload=%d", row, payload.Payload)
			}
		}
	}
	if !found {
		t.Fatal("held TCP delta missing")
	}
	short := p.command(t, "short")
	third := observedCycle(t, n)
	observed := 0
	for _, src := range third.Sources {
		for _, sk := range src.Values {
			for _, port := range short.ShortPorts {
				if sk.LocalPort == port && sk.Cookie != "" && sk.Inode != "" {
					observed++
				}
			}
		}
	}
	if observed != 0 || third.DroppedEvents != nil {
		t.Fatalf("short flows usable=%d dropped=%v", observed, third.DroppedEvents)
	}
	var selfBefore, childBefore, selfAfter, childAfter syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &selfBefore)
	syscall.Getrusage(syscall.RUSAGE_CHILDREN, &childBefore)
	at := time.Now()
	samples := 8
	maxMillis := int64(0)
	for i := 0; i < samples; i++ {
		c := observedCycle(t, n)
		maxMillis = max(maxMillis, c.ElapsedMillis)
	}
	wall := time.Since(at)
	syscall.Getrusage(syscall.RUSAGE_SELF, &selfAfter)
	syscall.Getrusage(syscall.RUSAGE_CHILDREN, &childAfter)
	cpu := func(r syscall.Rusage) int64 {
		return r.Utime.Sec*1000000 + r.Utime.Usec + r.Stime.Sec*1000000 + r.Stime.Usec
	}
	cpuMicros := cpu(selfAfter) - cpu(selfBefore) + cpu(childAfter) - cpu(childBefore)
	t.Logf("MEASURED source=%q kernel=%q held_tcp_delta=%d UDP_bytes=unknown short_flows_created=%d usable_short_flows=%d dropped_events=unknown sample_fixture=8_immediate_snapshots configured_interval=30s wall=%s per_sample=%s max_capture_ms=%d CPU_total_us=%d amortized_single_namespace_CPU_percent_at_30s=%.4f MaxRSS_process_KiB=%d", first.ToolVersion, first.KernelRelease, payload.Payload, len(short.ShortPorts), observed, wall, wall/time.Duration(samples), maxMillis, cpuMicros, float64(cpuMicros)/float64(samples*30*1000000)*100, selfAfter.Maxrss)
}

type selectedDocker struct {
	*dockerx.Client
	ids map[string]bool
}

func (d selectedDocker) ListRunning(ctx context.Context) ([]dockerx.Container, error) {
	items, err := d.Client.ListRunning(ctx)
	if err != nil {
		return nil, err
	}
	chosen := []dockerx.Container{}
	for _, item := range items {
		if d.ids[item.ID] {
			chosen = append(chosen, item)
		}
	}
	return chosen, nil
}
func TestLiveDockerAttributionAndSharedNamespaceNoDoubleCounting(t *testing.T) {
	ns := privateNamespace(t)
	image := "python:3.11-slim"
	nativeExec(t, "docker", "image", "inspect", image)
	name := fmt.Sprintf("jd-flow-%x", time.Now().UnixNano())
	secondName := name + "-shared"
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", secondName, name).Run() })
	mount := "type=bind,src=" + os.Args[0] + ",dst=/jd-fixture,readonly"
	cmd := exec.CommandContext(t.Context(), "docker", "run", "--rm", "-i", "--name", name, "--network", "none", "--mount", mount, "-e", "JD_NETFLOWS_HELPER=1", "--entrypoint", "/jd-fixture", image, "-test.run=^TestFlowFixtureProcess$")
	first := startFixture(t, cmd)
	id := nativeExec(t, "docker", "inspect", "--format", "{{.Id}}", name)
	secondCmd := exec.CommandContext(t.Context(), "docker", "run", "--rm", "-i", "--name", secondName, "--network", "container:"+id, "--mount", mount, "-e", "JD_NETFLOWS_HELPER=1", "-e", "JD_NETFLOWS_UDP_ONLY=1", "--entrypoint", "/jd-fixture", image, "-test.run=^TestFlowFixtureProcess$")
	second := startFixture(t, secondCmd)
	otherID := nativeExec(t, "docker", "inspect", "--format", "{{.Id}}", secondName)
	client := dockerx.New("unix:///var/run/docker.sock")
	defer client.Close()
	n := &Native{Docker: selectedDocker{client, map[string]bool{id: true, otherID: true}}, hostNamespacePath: filepath.Join("/var/run/netns", ns)}
	at := time.Now()
	c := observedCycle(t, n)
	elapsed := time.Since(at)
	shared := 0
	tcpOwners, udpOther := 0, 0
	for _, src := range c.Sources {
		if src.Status == "shared_namespace" {
			shared++
		}
		for _, sk := range src.Values {
			if sk.Protocol == "tcp" && (sk.LocalPort == first.ready.ClientPort || sk.LocalPort == first.ready.ServerPort) {
				if sk.Owner.ContainerID != id || sk.Owner.Status != "verified_container" {
					t.Fatalf("wrong TCP container attribution=%+v", sk.Owner)
				}
				tcpOwners++
			}
			if sk.Protocol == "udp" && sk.LocalPort == second.ready.UDPPort {
				if sk.Owner.ContainerID != otherID || sk.Owner.Status != "verified_container" {
					t.Fatalf("wrong shared namespace descriptor owner=%+v", sk.Owner)
				}
				udpOther++
			}
		}
	}
	if shared != 1 || tcpOwners != 2 || udpOther != 1 {
		t.Fatalf("shared=%d TCP owners=%d UDP other=%d cycle=%+v", shared, tcpOwners, udpOther, c)
	}
	t.Logf("MEASURED selected_Docker_instances=2 unique_Docker_namespaces=1 shared_namespace_duplicate_samples=0 TCP_descriptor_attribution=2/2 connected_UDP_descriptor_attribution=1/1 capture=%s omitted_sources=%d dropped_events=unknown", elapsed, c.OmittedSources)
}
