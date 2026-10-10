package netsec

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// This drives a real ufw inside a throwaway network and mount namespace:
// copies of ufw's shipped configuration are bound over /etc/ufw and
// /etc/default/ufw, and a private tmpfs covers /run for its lock, so the
// host's own firewall is never read or written. Module loading and sysctl
// application are switched off in the copied defaults.
//
//	JD_NETNS_LIVE=1 go test ./internal/netsec/ -run TestLiveUFW -v

type ufwSandbox struct {
	pid int
}

func sandboxSudo(args ...string) *exec.Cmd {
	if os.Geteuid() == 0 {
		return exec.Command(args[0], args[1:]...)
	}
	return exec.Command("sudo", append([]string{"-n"}, args...)...)
}

func newUFWSandbox(t *testing.T) *ufwSandbox {
	t.Helper()
	if os.Getenv("JD_NETNS_LIVE") != "1" {
		t.Skip("set JD_NETNS_LIVE=1 to run against a throwaway namespace")
	}
	if os.Geteuid() != 0 {
		if err := exec.Command("sudo", "-n", "true").Run(); err != nil {
			t.Skip("needs root or passwordless sudo")
		}
	}
	if _, err := os.Stat("/usr/sbin/ufw"); err != nil {
		t.Skip("ufw is not installed")
	}
	root := t.TempDir()
	etc := filepath.Join(root, "etc", "ufw")
	for _, dir := range []string{etc, filepath.Join(etc, "applications.d"), filepath.Join(root, "etc", "default")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"before.rules", "before6.rules", "after.rules", "after6.rules", "user.rules", "ufw.conf", "iptables/user6.rules"} {
		b, err := os.ReadFile(filepath.Join("/usr/share/ufw", name))
		if err != nil {
			t.Skipf("ufw's shipped templates are needed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(etc, filepath.Base(name)), b, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(etc, "applications.d", "openssh-server"), []byte("[OpenSSH]\ntitle=Secure shell server\ndescription=OpenSSH\nports=22/tcp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(etc, "sysctl.conf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	defaults := "IPV6=yes\nDEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=\"ACCEPT\"\nDEFAULT_FORWARD_POLICY=\"DROP\"\nDEFAULT_APPLICATION_POLICY=\"SKIP\"\nMANAGE_BUILTINS=no\nIPT_SYSCTL=/etc/ufw/sysctl.conf\nIPT_MODULES=\"\"\n"
	if err := os.WriteFile(filepath.Join(root, "etc", "default", "ufw"), []byte(defaults), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`echo $$; exec unshare --net --mount --propagation private -- sh -c 'mount -t tmpfs tmpfs /run && mount --bind %s /etc/ufw && mount --bind %s /etc/default/ufw && ip link set lo up && echo ready && exec sleep 600'`,
		etc, filepath.Join(root, "etc", "default", "ufw"))
	cmd := sandboxSudo("sh", "-c", script)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sb := &ufwSandbox{}
	t.Cleanup(func() {
		if sb.pid > 0 {
			_ = sandboxSudo("kill", strconv.Itoa(sb.pid)).Run()
		}
		_ = cmd.Wait()
	})
	r := bufio.NewReader(out)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("holder did not start: %v %s", err, stderr.String())
	}
	if sb.pid, err = strconv.Atoi(strings.TrimSpace(line)); err != nil {
		t.Fatalf("holder pid %q", line)
	}
	if line, err = r.ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("holder said %q %v: %s", line, err, stderr.String())
	}
	prevRun, prevAvailable, prevDefaults := run, availableOnHost, ufwDefaultsFile
	run = sb.run
	availableOnHost = func(name string) bool { return name == "ufw" || name == "iptables" }
	ufwDefaultsFile = filepath.Join(root, "etc", "default", "ufw")
	t.Cleanup(func() { run, availableOnHost, ufwDefaultsFile = prevRun, prevAvailable, prevDefaults })
	return sb
}

func (sb *ufwSandbox) run(ctx context.Context, name string, args ...string) (string, error) {
	full := append([]string{"nsenter", "--target", strconv.Itoa(sb.pid), "--net", "--mount", "--", name}, args...)
	var cmd *exec.Cmd
	if os.Geteuid() == 0 {
		cmd = exec.CommandContext(ctx, full[0], full[1:]...)
	} else {
		cmd = exec.CommandContext(ctx, "sudo", append([]string{"-n"}, full...)...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String() + stderr.String(), fmt.Errorf("%s: %s", name, strings.TrimSpace(stdout.String()+stderr.String()))
	}
	// ufw warns on stderr that the sandbox's copies are not root's; the
	// listing a parser reads is standard output.
	return stdout.String(), nil
}

func TestLiveUFWParsesGuardsAndPlansAgainstTheRealTool(t *testing.T) {
	sb := newUFWSandbox(t)
	ctx := context.Background()
	must := func(args ...string) string {
		t.Helper()
		out, err := sb.run(ctx, "ufw", args...)
		if err != nil {
			t.Fatalf("ufw %v: %v", args, err)
		}
		return out
	}
	must("allow", "in", "on", "fw0", "to", "any", "port", "2222", "proto", "tcp")
	must("allow", "OpenSSH")
	must("allow", "80,443/tcp")
	must("allow", "from", "10.0.0.0/8", "to", "any", "port", "6379", "proto", "tcp")

	s := New()
	access := AccessContext{Client: "198.51.100.9", Interface: "fw0", Dashboard: []int{8443}, SSH: []int{22}, Ingress: []int{80, 443},
		Profiles: map[string][]string{"OpenSSH": {"22/tcp"}}}
	actx := WithAccess(ctx, access)

	// Inactive, ufw lists nothing; its configured rules are read instead,
	// unnumbered, so the preflight sees what enabling would enforce.
	st, err := s.Status(actx)
	if err != nil || st.Backend != BackendUFW || st.Enabled || st.RulesFrom != "configured" || len(st.Rules) != 4 {
		t.Fatalf("status = %+v %v", st, err)
	}
	if r := st.Rules[0]; r.Interface != "fw0" || r.Port != "2222" || r.Protocol != "tcp" || r.Number != 0 || !r.BothFamilies {
		t.Fatalf("the configured interface rule = %+v", r)
	}
	if r := st.Rules[1]; r.To != "OpenSSH" || r.Port != "" {
		t.Fatalf("the configured profile rule = %+v", r)
	}

	// Enabling would refuse the dashboard's port, which no rule admits.
	if _, err := s.SetEnabled(actx, true); err == nil || !strings.Contains(err.Error(), "dashboard") {
		t.Fatalf("enable without the dashboard's port: %v", err)
	}
	if out := must("status"); !strings.Contains(out, "Status: inactive") {
		t.Fatalf("a refused enable changed ufw: %s", out)
	}
	if _, err := s.AddRule(actx, RuleRequest{Action: "allow", Port: "8443", Protocol: "tcp", Comment: "dashboard"}, "198.51.100.9"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnabled(actx, true); err != nil {
		t.Fatalf("enable with the operator admitted: %v", err)
	}
	st, _ = s.Status(actx)
	var iface *Rule
	for i := range st.Rules {
		if st.Rules[i].Interface == "fw0" && st.Rules[i].IPv6 {
			iface = &st.Rules[i]
		}
	}
	if st.RulesFrom != "" || iface == nil || iface.Port != "2222" || iface.Number == 0 {
		t.Fatalf("the enforced listing's IPv6 interface rule = %+v in %+v", iface, st.Rules)
	}
	for _, c := range s.EvaluateAccess(actx, st) {
		if c.Required && c.Verdict != "admitted" {
			t.Fatalf("access after enabling = %+v", c)
		}
	}

	// A plan: narrow Redis to one network, removing the wider rule; the
	// IPv6 twin of each goes with it and identities survive renumbering.
	var redis Rule
	for _, r := range st.Rules {
		if r.Port == "6379" {
			redis = r
		}
	}
	plan := FirewallPlan{Operations: []PlanOperation{
		{Op: "delete", RuleID: redis.ID},
		{Op: "add", Rule: &RuleRequest{Action: "allow", Port: "6379", Protocol: "tcp", From: "10.1.0.0/16"}},
	}}
	review, err := s.ApplyPlan(actx, plan, "198.51.100.9")
	if err != nil {
		t.Fatalf("plan: %v %+v", err, review)
	}
	listing := must("status", "numbered")
	if strings.Contains(listing, "10.0.0.0/8") || !strings.Contains(listing, "10.1.0.0/16") {
		t.Fatalf("after the plan:\n%s", listing)
	}

	// Removing the dashboard's rule would shut the operator out.
	st, _ = s.Status(actx)
	for _, r := range st.Rules {
		if r.Comment == "dashboard" && !r.IPv6 {
			if _, err := s.DeleteRule(actx, r.Number); err == nil || !strings.Contains(err.Error(), "dashboard") {
				t.Fatalf("deleting the dashboard's rule: %v", err)
			}
		}
	}
	// A shadowed rule is found in the real order.
	must("insert", "1", "deny", "from", "203.0.113.0/24")
	must("allow", "from", "203.0.113.7")
	st, _ = s.Status(actx)
	shadowed := false
	for _, f := range st.Findings {
		shadowed = shadowed || (f.Kind == "shadowed" && f.ByNumber == 1)
	}
	if !shadowed {
		t.Fatalf("findings = %+v", st.Findings)
	}
}
