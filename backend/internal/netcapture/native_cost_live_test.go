package netcapture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func cpu(r syscall.Rusage) time.Duration {
	return time.Duration(r.Utime.Nano() + r.Stime.Nano())
}

// TestLiveCaptureCostAtFullBounds measures what one capture job costs at its
// largest allowed bounds under a flood, and idle at its time bound, in an
// owned disposable namespace. It records CPU of the native capture process
// tree, the backend's own CPU and heap, and asserts the artifact stays inside
// its byte cap. Nothing outside the namespace is touched.
func TestLiveCaptureCostAtFullBounds(t *testing.T) {
	if os.Getenv("JD_NETCAPTURE_LIVE") != "1" {
		t.Skip("set JD_NETCAPTURE_LIVE=1 for owned disposable namespace capture cost")
	}
	if os.Geteuid() != 0 {
		cmd := exec.Command("sudo", "-n", "env", "JD_NETCAPTURE_LIVE=1", "TMPDIR="+os.TempDir(), os.Args[0], "-test.run=^TestLiveCaptureCostAtFullBounds$", "-test.v")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("owned native fixture needs root or passwordless sudo: %v\n%s", err, output)
		}
		t.Logf("root fixture:\n%s", output)
		return
	}
	for _, tool := range []string{"ip", "tcpdump", "timeout", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	name := fmt.Sprintf("jd-cap-%08x", time.Now().UnixNano()&0xffffffff)
	ip := func(args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("ip %q: %v %s", args, err, out)
		}
	}
	ip("netns", "add", name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "ip", "netns", "delete", name).CombinedOutput(); err != nil {
			t.Errorf("fixture cleanup: %v %s", err, out)
		}
	})
	ip("-n", name, "link", "set", "lo", "up")
	ip("-n", name, "addr", "add", "127.0.0.2/8", "dev", "lo")
	native := &Native{command: func(ctx context.Context, tool string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "ip", append([]string{"netns", "exec", name, tool}, args...)...)
	}}

	for _, tc := range []struct {
		name    string
		flood   bool
		request Request
		stop    string
	}{
		{"flood at full byte and packet bounds", true, Request{Interface: "lo", Family: "inet", Protocol: "udp", Source: "127.0.0.2", Destination: "127.0.0.3", Port: 53117, Packets: 10000, Seconds: 30, MaxBytes: 2 * 1024 * 1024, SnapshotLength: 512}, "byte_limit"},
		{"idle until the time bound", false, Request{Interface: "lo", Family: "inet", Protocol: "udp", Source: "127.0.0.2", Destination: "127.0.0.3", Port: 53117, Packets: 10000, Seconds: 10, MaxBytes: 2 * 1024 * 1024, SnapshotLength: 512}, "time_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var generator *exec.Cmd
			if tc.flood {
				code := `import socket,time; s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.bind(('127.0.0.2',0)); end=time.time()+40; p=b'x'*400
while time.time()<end: s.sendto(p,('127.0.0.3',53117))`
				generator = exec.Command("timeout", "45", "ip", "netns", "exec", name, "python3", "-c", code)
				// Its own group, so stopping it stops the sender and not only
				// the timeout wrapper around it.
				generator.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
				if err := generator.Start(); err != nil {
					t.Fatal(err)
				}
				time.Sleep(300 * time.Millisecond)
			}
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			var peak atomic.Uint64
			stopSampling := make(chan struct{})
			sampled := make(chan struct{})
			go func() {
				defer close(sampled)
				var m runtime.MemStats
				for {
					runtime.ReadMemStats(&m)
					if m.HeapInuse > peak.Load() {
						peak.Store(m.HeapInuse)
					}
					select {
					case <-stopSampling:
						return
					case <-time.After(20 * time.Millisecond):
					}
				}
			}()
			var selfBefore, childBefore, selfAfter, childAfter syscall.Rusage
			_ = syscall.Getrusage(syscall.RUSAGE_SELF, &selfBefore)
			_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &childBefore)
			start := time.Now()
			result, err := native.Capture(t.Context(), tc.request)
			wall := time.Since(start)
			_ = syscall.Getrusage(syscall.RUSAGE_SELF, &selfAfter)
			_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &childAfter)
			close(stopSampling)
			<-sampled
			if generator != nil {
				_ = syscall.Kill(-generator.Process.Pid, syscall.SIGKILL)
				_ = generator.Wait()
			}
			if err != nil || result == nil {
				t.Fatalf("capture: %+v %v", result, err)
			}
			if result.StopReason != tc.stop || len(result.Artifact) > tc.request.MaxBytes || !result.IdentityVerified {
				t.Fatalf("result = stop %s bytes %d identity %v", result.StopReason, len(result.Artifact), result.IdentityVerified)
			}
			heapGrowth := int64(peak.Load()) - int64(before.HeapInuse)
			nativeCPU := cpu(childAfter) - cpu(childBefore)
			backendCPU := cpu(selfAfter) - cpu(selfBefore)
			t.Logf("COST %s: wall=%s packets=%d artifact=%d bytes stop=%s native_tree_cpu=%s native_max_rss=%d KiB backend_cpu=%s backend_peak_heap_growth=%d KiB",
				tc.name, wall.Round(time.Millisecond), result.Packets, len(result.Artifact), result.StopReason, nativeCPU.Round(time.Millisecond), childAfter.Maxrss, backendCPU.Round(time.Millisecond), heapGrowth/1024)
			if heapGrowth > 32*1024*1024 {
				t.Fatalf("backend heap grew %d bytes for a 2 MiB artifact", heapGrowth)
			}
			if childAfter.Maxrss > 128*1024 {
				t.Fatalf("native capture tree peaked at %d KiB", childAfter.Maxrss)
			}
			if !tc.flood && nativeCPU > 2*time.Second {
				t.Fatalf("an idle capture used %s of CPU", nativeCPU)
			}
		})
	}
}
