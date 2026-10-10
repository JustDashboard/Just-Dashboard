package netsec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures named fail2ban-get-* are Fail2Ban v1.1.0's own answers,
// recorded from a server run in a throwaway container with an sshd jail on
// iptables-multiport, an nginx jail on nftables-allports and a recidive jail
// on nftables[type=allports].

// f2bHost answers fail2ban-client from fixtures by argument line.
func f2bHost(t *testing.T, answers map[string]string, failing ...string) *[]string {
	t.Helper()
	var calls []string
	prev := run
	run = func(_ context.Context, name string, args ...string) (string, error) {
		line := strings.Join(args, " ")
		calls = append(calls, name+" "+line)
		for _, f := range failing {
			if line == f {
				return testdata(t, "fail2ban-get-action-type-missing.txt"), errors.New("exit status 255")
			}
		}
		if out, ok := answers[line]; ok {
			return out, nil
		}
		t.Errorf("unexpected command: %s %s", name, line)
		return "", errors.New("unexpected")
	}
	t.Cleanup(func() { run = prev })
	return &calls
}

func TestParseActionListReadsEveryAction(t *testing.T) {
	if got := parseActionList(testdata(t, "fail2ban-get-actions.txt")); strings.Join(got, ",") != "iptables-multiport" {
		t.Fatalf("one action=%v", got)
	}
	// The address-list parser read this line as prose and dropped both.
	if got := parseActionList(testdata(t, "fail2ban-get-actions-two.txt")); strings.Join(got, ",") != "iptables-multiport,sendmail-whois" {
		t.Fatalf("two actions=%v", got)
	}
}

func TestJailPolicyReadsTheRunningServer(t *testing.T) {
	calls := f2bHost(t, map[string]string{
		"get sshd bantime":                        "7200",
		"get sshd findtime":                       "600",
		"get sshd maxretry":                       "5",
		"get sshd ignoreip":                       testdata(t, "fail2ban-get-ignoreip.txt"),
		"get sshd ignoreself":                     testdata(t, "fail2ban-get-ignoreself.txt"),
		"get sshd logpath":                        testdata(t, "fail2ban-get-logpath-none.txt"),
		"get sshd journalmatch":                   testdata(t, "fail2ban-get-journalmatch.txt"),
		"get sshd actions":                        testdata(t, "fail2ban-get-actions-two.txt"),
		"get sshd action iptables-multiport port": testdata(t, "fail2ban-get-action-port.txt"),
		"get sshd action sendmail-whois port":     "",
		"get sshd action sendmail-whois type":     "",
	}, "get sshd action iptables-multiport type")
	p, err := New().JailPolicy(t.Context(), "sshd", []string{"2222"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Rule != "5 failures within 10 minutes earn a 2-hour ban" {
		t.Fatalf("rule=%q", p.Rule)
	}
	if p.Watches.Kind != "journal" || p.Watches.Match != "_SYSTEMD_UNIT=ssh.service + _COMM=sshd" {
		t.Fatalf("watches=%+v", p.Watches)
	}
	if !p.IgnoreSelf || strings.Join(p.IgnoreIP, ",") != "127.0.0.0/8,::1,100.110.34.9" {
		t.Fatalf("ignore=%v %v", p.IgnoreSelf, p.IgnoreIP)
	}
	if len(p.Actions) != 2 || p.Actions[0].Kind != "firewall" || p.Actions[0].AllPorts || strings.Join(p.Actions[0].Ports, ",") != "ssh" ||
		p.Actions[1].Kind != "report" || p.Actions[1].Enforces {
		t.Fatalf("actions=%+v", p.Actions)
	}
	// sshd moved to 2222 and the ban still names ssh: the finding the jail's
	// own numbers can never show.
	if p.Coverage == nil || strings.Join(p.Coverage.Uncovered, ",") != "2222" || !hasPolicyFinding(*p, "critical", "sshd listens on 2222") {
		t.Fatalf("coverage=%+v findings=%+v", p.Coverage, p.Findings)
	}
	for _, c := range *calls {
		if !strings.HasPrefix(c, "fail2ban-client get ") {
			t.Fatalf("policy ran a command that is not a read: %s", c)
		}
	}
}

func TestJailPolicyExplainsActionsCoverageAndDrift(t *testing.T) {
	base := policyInput{Name: "sshd", BanTime: "3600", FindTime: "600", MaxRetry: "3", JournalMatch: "_COMM=sshd",
		Actions: []string{"iptables-multiport"}, Props: map[string]map[string]string{"iptables-multiport": {"port": "ssh"}},
		SSHDPorts: []string{"22"}}

	p := ExplainJailPolicy(base)
	if p.Rule != "3 failures within 10 minutes earn a 1-hour ban" || p.Coverage == nil || len(p.Coverage.Uncovered) != 0 || len(p.Findings) != 0 {
		t.Fatalf("clean policy=%+v", p)
	}

	allports := base
	allports.SSHDPorts = []string{"2222"}
	allports.Actions = []string{"nftables"}
	allports.Props = map[string]map[string]string{"nftables": {"port": "0:65535", "type": "allports"}}
	if p := ExplainJailPolicy(allports); !p.Actions[0].AllPorts || len(p.Coverage.Uncovered) != 0 {
		t.Fatalf("allports=%+v", p)
	}

	ranged := base
	ranged.SSHDPorts = []string{"2222", "22"}
	ranged.Props = map[string]map[string]string{"iptables-multiport": {"port": "ssh,2000:2300"}}
	if p := ExplainJailPolicy(ranged); len(p.Coverage.Uncovered) != 0 || len(p.Coverage.Covered) != 2 {
		t.Fatalf("ranged=%+v", p.Coverage)
	}

	reportOnly := base
	reportOnly.Actions = []string{"sendmail-whois", "abuseipdb"}
	if p := ExplainJailPolicy(reportOnly); !hasPolicyFinding(p, "critical", "No action blocks traffic") || p.Coverage != nil {
		t.Fatalf("report only=%+v", p)
	}

	drifted := base
	drifted.DropIn = map[string]string{"bantime": "2h", "maxretry": "3"}
	p = ExplainJailPolicy(drifted)
	if !p.Values[0].Drift || p.Values[0].DropIn != "2h" || p.Values[2].Drift || !hasPolicyFinding(p, "warning", "a restart changes it") {
		t.Fatalf("drift=%+v %+v", p.Values, p.Findings)
	}

	permanent := base
	permanent.BanTime = "-1"
	permanent.JournalMatch = ""
	if p := ExplainJailPolicy(permanent); p.Rule != "3 failures within 10 minutes earn a ban that never expires" || !hasPolicyFinding(p, "warning", "watches no log file") {
		t.Fatalf("permanent=%+v", p)
	}

	for name, kind := range map[string]string{"route": "route", "cloudflare": "edge", "hostsdeny": "report", "ufw": "firewall", "firewallcmd-ipset": "firewall", "custom-thing": "unknown"} {
		if got := classifyAction(name, map[string]string{"port": "ssh"}); got.Kind != kind {
			t.Errorf("%s=%s want %s", name, got.Kind, kind)
		}
	}
}

func TestJailDropInIsReadForOneSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "99-just-dashboard.local")
	body := "# Written by Just Dashboard.\n[nginx-http-auth]\nbantime = 600\n[sshd]\nbantime = 7200\n; a comment\nmaxretry = 5\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := readJailDropIn(path, "sshd")
	if got["bantime"] != "7200" || got["maxretry"] != "5" || len(got) != 2 {
		t.Fatalf("drop-in=%v", got)
	}
	if got := readJailDropIn(filepath.Join(t.TempDir(), "missing"), "sshd"); len(got) != 0 {
		t.Fatalf("missing drop-in=%v", got)
	}
}

func TestJailPolicyRefusesABadJailName(t *testing.T) {
	if _, err := New().JailPolicy(t.Context(), "sshd;reboot", nil); err == nil {
		t.Fatal("accepted an injected jail name")
	}
}

func hasPolicyFinding(p JailPolicy, level, text string) bool {
	for _, f := range p.Findings {
		if f.Level == level && strings.Contains(f.Text, text) {
			return true
		}
	}
	return false
}
