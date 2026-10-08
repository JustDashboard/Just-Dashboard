package netvantage

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// This opt-in fixture creates only its named loopback namespace. It has no
// interface to the production host, no listener on host ingress and no scan.
func TestControlledVantageSeparateNamespace(t *testing.T) {
	if os.Getenv("JD_NETVANTAGE_CHILD") == "1" {
		inode, e := os.Readlink("/proc/self/ns/net")
		if e != nil {
			t.Fatal(e)
		}
		if inode == os.Getenv("JD_NETVANTAGE_PARENT_NS") {
			t.Fatal("fixture never crossed network namespace")
		}
		if os.Geteuid() == 0 {
			t.Fatal("probe fixture must be rootless")
		}
		t.Run("native_protocols", TestNativeDNSPinnedTCPAndVerifiedTLSBothFamilies)
		t.Run("dns_scope_refusal", TestDNSRebindingOutsideEnrolledAddressesStopsBeforeTCP)
		t.Run("untrusted_tls", TestTLSVerificationFailureRemainsMeasuredFailure)
		t.Run("trust_replay", TestServerSigningIdentityRejectsCrossInstallationReplay)
		t.Run("signed_native_protocol", TestSignedAgentNativeEvidenceBothFamilies)
		return
	}
	if os.Getenv("JD_NETVANTAGE_LIVE") != "1" {
		t.Skip("set JD_NETVANTAGE_LIVE=1 for an isolated controlled namespace fixture")
	}
	if os.Geteuid() == 0 {
		t.Skip("run the opt-in fixture as a non-root contributor with sudo for namespace setup")
	}
	ns := "jd-vantage-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	run := func(args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
		defer cancel()
		cmd := exec.Command("sudo", append([]string{"-n", "ip"}, args...)...)
		out, e := hostexec.RunGroup(ctx, cmd, time.Second)
		if e != nil {
			t.Fatalf("isolated namespace command: %v %#v", e, out)
		}
	}
	run("netns", "add", ns)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.Command("sudo", "-n", "ip", "netns", "delete", ns)
		_, e := hostexec.RunGroup(ctx, cmd, time.Second)
		if e != nil {
			t.Error("delete owned namespace", e)
		}
	})
	run("-n", ns, "link", "set", "lo", "up")
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	user := strconv.Itoa(os.Getuid())
	parent, e := os.Readlink("/proc/self/ns/net")
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("sudo", "-n", "ip", "netns", "exec", ns, "setpriv", "--reuid="+user, "--regid="+strconv.Itoa(os.Getgid()), "--clear-groups", "env", "JD_NETVANTAGE_CHILD=1", "JD_NETVANTAGE_PARENT_NS="+parent, exe, "-test.run=^TestControlledVantageSeparateNamespace$", "-test.v")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	out, e := hostexec.RunGroup(ctx, cmd, time.Second)
	if e != nil {
		t.Fatalf("rootless controlled namespace: %v %#v", e, out)
	}
	if !strings.Contains(output.String(), "native_protocols/inet6") || !strings.Contains(output.String(), "trust_replay") || !strings.Contains(output.String(), "signed_native_protocol/inet6") {
		t.Fatalf("child acceptance cases did not execute: %s", output.String())
	}
	t.Log(output.String())
}
