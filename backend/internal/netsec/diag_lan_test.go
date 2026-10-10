package netsec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func stubLANDiagnostics(t *testing.T) {
	t.Helper()
	oldRun, oldHas, oldInterface, oldSend := diagnosticRun, diagnosticHas, lanInterface, wakeSend
	t.Cleanup(func() { diagnosticRun, diagnosticHas, lanInterface, wakeSend = oldRun, oldHas, oldInterface, oldSend })
	diagnosticHas = func(string) bool { return true }
	lanInterface = func(name string) (*net.Interface, error) {
		return &net.Interface{Name: name, Index: 12, HardwareAddr: net.HardwareAddr{2, 1, 2, 3, 4, 5}, Flags: net.FlagUp | net.FlagBroadcast}, nil
	}
	diagnosticRun = func(context.Context, time.Duration, string, ...string) (string, string, error) {
		t.Fatal("unexpected diagnostic command")
		return "", "", nil
	}
	wakeSend = func(int, []byte) error { t.Fatal("unexpected wake frame"); return nil }
}

func TestRouteLookupUsesCanonicalAddressAndFamily(t *testing.T) {
	stubLANDiagnostics(t)
	for _, tc := range []struct{ target, family, canonical string }{
		{"192.0.2.1", "-4", "192.0.2.1"},
		{"2001:0db8::1", "-6", "2001:db8::1"},
		{"::ffff:192.0.2.1", "-4", "192.0.2.1"},
	} {
		diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
			if cmd != "ip" || !reflect.DeepEqual(args, []string{tc.family, "route", "get", tc.canonical}) {
				t.Fatalf("unexpected command %s %v", cmd, args)
			}
			return tc.canonical + " dev eth0 src 192.0.2.2", "1ms", nil
		}
		res, err := New().RouteLookup(t.Context(), tc.target)
		if err != nil || !res.OK || res.Target != tc.canonical {
			t.Fatalf("%s: %+v %v", tc.target, res, err)
		}
	}
	for _, bad := range []string{"example.com", "-help", "fe80::1%eth0", "192.0.2.1\nrule flush"} {
		if _, err := New().RouteLookup(t.Context(), bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestPathMTUUsesIPv6AndReportsMissingTool(t *testing.T) {
	stubLANDiagnostics(t)
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		if cmd != "tracepath" || !reflect.DeepEqual(args, []string{"-n", "-m", "20", "-6", "2001:db8::1"}) {
			t.Fatalf("unexpected command %s %v", cmd, args)
		}
		return " 1?: [LOCALHOST]                        0.010ms pmtu 1500\n 1:  2001:db8::1                            0.300ms reached\n     Resume: pmtu 1500 hops 1 back 1", "1ms", nil
	}
	res, err := New().PathMTU(t.Context(), "2001:db8::1")
	if err != nil || !res.OK || res.Verdict != ProbeOK || !strings.Contains(res.Output, "pmtu 1500") {
		t.Fatalf("%+v %v", res, err)
	}
	diagnosticHas = func(string) bool { return false }
	res, err = New().PathMTU(t.Context(), "example.com")
	if err != nil || res.OK || !strings.Contains(res.Error, "install") {
		t.Fatalf("missing tracepath: %+v %v", res, err)
	}
}

func TestPacketSnapshotBoundsCaptureAndClosesFilter(t *testing.T) {
	stubLANDiagnostics(t)
	diagnosticRun = func(ctx context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		want := []string{"-nn", "-p", "-q", "-l", "-s", "96", "-c", "50", "-i", "eno1", "udp"}
		if cmd != "tcpdump" || !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected command %s %v", cmd, args)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			t.Fatal("capture has no 15-second budget")
		}
		return "IP 192.0.2.1.53 > 192.0.2.2.4321: UDP", "1ms", nil
	}
	res, err := New().PacketSnapshot(t.Context(), "eno1", "udp")
	if err != nil || !res.OK || !strings.Contains(res.Output, "50 packets") {
		t.Fatalf("%+v %v", res, err)
	}
	for _, bad := range []string{"tcp or host 1.1.1.1", "-w", "udp\nexec"} {
		if _, err := New().PacketSnapshot(t.Context(), "eno1", bad); err == nil {
			t.Fatalf("accepted filter %q", bad)
		}
	}
	if _, err := New().PacketSnapshot(t.Context(), "eno1;id", "all"); err == nil {
		t.Fatal("accepted bad interface")
	}
}

// stubNeighbourCache answers the cache read Wake-on-LAN makes before sending.
func stubNeighbourCache(t *testing.T, cache string) {
	t.Helper()
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		if cmd != "ip" || !reflect.DeepEqual(args, []string{"neigh", "show"}) {
			t.Fatalf("unexpected command %s %v", cmd, args)
		}
		return cache, "1ms", nil
	}
}

func TestWakeOnLANFramesAndLocalInterface(t *testing.T) {
	stubLANDiagnostics(t)
	stubNeighbourCache(t, "")
	calls := 0
	wakeSend = func(index int, packet []byte) error {
		calls++
		if index != 12 || len(packet) != 102 || !bytes.Equal(packet[:6], bytes.Repeat([]byte{0xff}, 6)) {
			t.Fatalf("wrong interface or header: %d %x", index, packet)
		}
		for i := range 16 {
			if !bytes.Equal(packet[6+i*6:12+i*6], []byte{2, 0x11, 0x22, 0x33, 0x44, 0x55}) {
				t.Fatalf("wrong target in repetition %d", i)
			}
		}
		return nil
	}
	res, err := New().WakeOnLAN(t.Context(), "02:11:22:33:44:55", "eno1", "", 0)
	if err != nil || !res.OK || calls != 1 || !strings.Contains(res.Output, "does not confirm") {
		t.Fatalf("%+v %v calls=%d", res, err, calls)
	}
	for _, bad := range []string{"00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff", "01:11:22:33:44:55", "192.0.2.1", "02:11:22:33:44:55:66:77"} {
		if _, err := New().WakeOnLAN(t.Context(), bad, "eno1", "", 0); err == nil {
			t.Fatalf("accepted MAC %q", bad)
		}
	}
	lanInterface = func(name string) (*net.Interface, error) {
		return &net.Interface{Name: name, Index: 12, Flags: net.FlagUp | net.FlagPointToPoint}, nil
	}
	if _, err := New().WakeOnLAN(t.Context(), "02:11:22:33:44:55", "wg0", "", 0); err == nil {
		t.Fatal("accepted a tunnel as the LAN segment")
	}
	if calls != 1 {
		t.Fatalf("invalid request sent a frame, calls=%d", calls)
	}
}

func TestWakeOnLANReportsRawSocketFailure(t *testing.T) {
	stubLANDiagnostics(t)
	stubNeighbourCache(t, "")
	wakeSend = func(int, []byte) error { return errors.New("operation not permitted") }
	res, err := New().WakeOnLAN(t.Context(), "02:11:22:33:44:55", "eno1", "", 0)
	if err != nil || res.OK || !strings.Contains(res.Error, "CAP_NET_RAW") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestEgressSupportsIPv6OnlyHosts(t *testing.T) {
	stubLANDiagnostics(t)
	diagnosticRun = func(_ context.Context, _ time.Duration, _ string, args ...string) (string, string, error) {
		if args[0] == "-4" {
			return "", "1ms", errors.New("network unreachable")
		}
		if args[2] == "show" {
			return "default via fe80::1 dev eth0 proto ra metric 1024 pref medium", "1ms", nil
		}
		return "2606:4700:4700::1111 from :: via fe80::1 dev eth0 proto ra src 2001:db8::2 metric 1024 pref medium", "1ms", nil
	}
	res, err := New().Egress(t.Context())
	if err != nil || !res.OK || !reflect.DeepEqual(res.Records, []string{"2001:db8::2", "ipv6 dev eth0 via fe80::1"}) || res.Summary != "IPv6 only: the other family has no route from this host." || !strings.Contains(res.Output, "IPv4:") || !strings.Contains(res.Output, "IPv6:") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestListenersIncludeUDPAndTCP(t *testing.T) {
	stubLANDiagnostics(t)
	diagnosticRun = func(_ context.Context, _ time.Duration, cmd string, args ...string) (string, string, error) {
		if cmd != "ss" || !reflect.DeepEqual(args, []string{"-tulnp"}) {
			t.Fatalf("listeners exclude a transport: %s %v", cmd, args)
		}
		return "udp UNCONN 127.0.0.1:53\ntcp LISTEN 127.0.0.1:443", "1ms", nil
	}
	res, err := New().Listeners(t.Context())
	if err != nil || !res.OK || !strings.Contains(res.Output, "udp") || !strings.Contains(res.Output, "tcp") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestProbeOutputBoundsMemoryWhileDraining(t *testing.T) {
	var b probeOutput
	payload := bytes.Repeat([]byte("x"), maxProbeOutput+4096)
	if n, err := b.Write(payload); n != len(payload) || err != nil {
		t.Fatalf("write = %d %v", n, err)
	}
	if n, err := b.Write(payload); n != len(payload) || err != nil {
		t.Fatalf("second write = %d %v", n, err)
	}
	if b.Len() != maxProbeOutput || !strings.HasSuffix(b.String(), "… (truncated)") {
		t.Fatal("unbounded or unmarked output")
	}
	// os/exec drains pipe output with io.Copy. An embedded bytes.Buffer
	// promotes ReadFrom and silently bypasses Write's cap on this path.
	var piped probeOutput
	n, err := io.Copy(&piped, struct{ io.Reader }{bytes.NewReader(payload)})
	if err != nil || n != int64(len(payload)) || piped.Len() != maxProbeOutput || !piped.truncated {
		t.Fatalf("pipe output was not capped/drained: n=%d len=%d truncated=%t err=%v", n, piped.Len(), piped.truncated, err)
	}
}

func TestRunProbeStopsWrappedDescendantsOnTimeout(t *testing.T) {
	started := time.Now()
	out, _, err := runProbe(t.Context(), 100*time.Millisecond, "sh", "-c", "sleep 30 & child=$!; echo $child; wait")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("wrapped probe did not stop on its budget: %q, %v", out, err)
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(out))
	if parseErr != nil || pid <= 1 {
		t.Fatalf("missing child PID: %q, %v", out, parseErr)
	}
	if killErr := syscall.Kill(pid, 0); errors.Is(killErr, syscall.ESRCH) {
		return
	}
	// A container's PID 1 may reap later; a zombie has stopped executing.
	stat, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(readErr, os.ErrNotExist) {
		return
	}
	_, state, ok := strings.Cut(string(stat), ") ")
	if readErr != nil || !ok || !strings.HasPrefix(state, "Z ") {
		t.Fatalf("probe left its child executing: %s, %v", stat, readErr)
	}
}
