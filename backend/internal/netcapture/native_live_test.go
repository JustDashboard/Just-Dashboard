package netcapture

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLiveBoundedNativePCAPBothFamiliesAndCleanup(t *testing.T) {
	if os.Getenv("JD_NETCAPTURE_LIVE") != "1" {
		t.Skip("set JD_NETCAPTURE_LIVE=1 for owned disposable namespace capture")
	}
	if namespace := os.Getenv("JD_NETCAPTURE_DEATH_NAMESPACE"); namespace != "" {
		if os.Geteuid() != 0 || !regexp.MustCompile(`^jd-cap-[0-9a-f]{8}$`).MatchString(namespace) {
			t.Fatal("invalid owned backend-death fixture namespace")
		}
		native := &Native{command: func(ctx context.Context, tool string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "ip", append([]string{"netns", "exec", namespace, tool}, args...)...)
		}}
		r := request()
		r.Seconds = 3
		if _, err := native.Capture(t.Context(), r); err != nil {
			t.Fatal(err)
		}
		return
	}
	if os.Geteuid() != 0 {
		cmd := exec.Command("sudo", "-n", "env", "JD_NETCAPTURE_LIVE=1", "TMPDIR="+os.TempDir(), os.Args[0], "-test.run=^TestLiveBoundedNativePCAPBothFamiliesAndCleanup$", "-test.v")
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
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("ip %q: %v %s", args, err, out)
		}
		return string(out)
	}
	run("netns", "add", name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ip", "netns", "delete", name).CombinedOutput()
		if err != nil {
			t.Errorf("fixture cleanup: %v %s", err, out)
		}
	})
	run("-n", name, "link", "set", "lo", "up")
	run("-n", name, "addr", "add", "127.0.0.2/8", "dev", "lo")
	run("-n", name, "-6", "addr", "add", "fd42:5317::2/128", "dev", "lo")
	run("-n", name, "-6", "addr", "add", "fd42:5317::3/128", "dev", "lo")
	native := &Native{command: func(ctx context.Context, tool string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "ip", append([]string{"netns", "exec", name, tool}, args...)...)
	}}
	for _, family := range []string{"inet", "inet6"} {
		t.Run(family, func(t *testing.T) {
			r := request()
			r.Family = family
			r.Packets = 2
			r.Seconds = 4
			if family == "inet6" {
				r.Source, r.Destination = "fd42:5317::2", "fd42:5317::3"
			}
			type outcome struct {
				result *Result
				err    error
			}
			done := make(chan outcome, 1)
			go func() { result, err := native.Capture(t.Context(), r); done <- outcome{result, err} }()
			time.Sleep(300 * time.Millisecond)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			code := `import socket,sys,time; s=socket.socket(socket.AF_INET6 if sys.argv[1]=='inet6' else socket.AF_INET,socket.SOCK_DGRAM); s.bind((sys.argv[2],0)); [(s.sendto(b'JD_OWNED_CAPTURE_PROOF',(sys.argv[3],53117)),time.sleep(.04)) for _ in range(25)]`
			cmd := exec.CommandContext(ctx, "ip", "netns", "exec", name, "python3", "-c", code, family, r.Source, r.Destination)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("fixture traffic: %v %s", err, out)
			}
			got := <-done
			if got.err != nil || got.result == nil || got.result.Packets != 2 || got.result.StopReason != "packet_limit" || !got.result.IdentityVerified || !bytes.Contains(got.result.Artifact, []byte("JD_OWNED_CAPTURE_PROOF")) {
				t.Fatalf("native=%+v %v", got.result, got.err)
			}
			if len(got.result.Artifact) > r.MaxBytes {
				t.Fatal("native byte cap exceeded")
			}
			drops := "unknown"
			if got.result.KernelDropped != nil {
				drops = fmt.Sprint(*got.result.KernelDropped)
			}
			t.Logf("%s native tcpdump packets=%d bytes=%d stop=%s drops=%s SHA256=%s", family, got.result.Packets, got.result.Bytes, got.result.StopReason, drops, got.result.SHA256)
		})
	}
	quiet := request()
	quiet.Seconds = 1
	start := time.Now()
	result, err := native.Capture(t.Context(), quiet)
	if err != nil || result.StopReason != "time_limit" || result.Packets != 0 || time.Since(start) > 4*time.Second {
		t.Fatalf("quiet independent timeout=%+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	quiet.Seconds = 120
	go func() { _, err := native.Capture(ctx, quiet); done <- err }()
	time.Sleep(300 * time.Millisecond)
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("cancel=%v", err)
	}
	// Kill the applying process only after the real quiet capture is running.
	// Its host timeout must outlive that process and finish without a backend.
	helper := exec.Command(os.Args[0], "-test.run=^TestLiveBoundedNativePCAPBothFamiliesAndCleanup$", "-test.v")
	helper.Env = append(os.Environ(), "JD_NETCAPTURE_DEATH_NAMESPACE="+name)
	var helperOutput bytes.Buffer
	helper.Stdout, helper.Stderr = &helperOutput, &helperOutput
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = helper.Process.Kill()
			_ = helper.Wait()
		}
	})
	hasCapture := func() bool {
		for _, pid := range strings.Fields(run("netns", "pids", name)) {
			comm, _ := os.ReadFile("/proc/" + pid + "/comm")
			if strings.TrimSpace(string(comm)) == "tcpdump" {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(3 * time.Second)
	for !hasCapture() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !hasCapture() {
		t.Fatal("owned backend-death capture did not start")
	}
	killedAt := time.Now()
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()
	waited = true
	if !hasCapture() {
		t.Fatal("fixture did not establish a capture surviving backend death")
	}
	deadline = time.Now().Add(6 * time.Second)
	for strings.TrimSpace(run("netns", "pids", name)) != "" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("owned native capture survived applying-process SIGKILL and independent timeout removed all namespace PIDs after %s", time.Since(killedAt))
	// This namespace belongs only to the fixture; any remaining PID is a leak.
	if pids := strings.TrimSpace(run("netns", "pids", name)); pids != "" {
		t.Fatalf("owned capture process survived cleanup: %s", pids)
	}
}
