package netx

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedFirewallRendersProtectionsBeforeRulesAndOnlyItsTable(t *testing.T) {
	sp := emptySpec()
	off, err := renderFirewall(sp, nil)
	if err != nil || strings.Contains(off, "chain") || !strings.Contains(off, "delete table inet jd_firewall") {
		t.Fatalf("absent table renders a removal only: %q %v", off, err)
	}
	sp.Firewall = &FirewallSpec{Enabled: true, Incoming: "drop", Rules: []FirewallRuleSpec{
		{ID: 4, Action: "accept", Protocol: "tcp", Ports: "22,8443"},
		{ID: 5, Action: "drop", Source: "203.0.113.0/24"},
		{ID: 6, Action: "accept", Ports: "51820", Interface: "eth0"},
		{ID: 7, Action: "reject", Source: "2001:db8::/32", Protocol: "udp"},
	}}
	got, err := renderFirewall(sp, []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{
		"type filter hook input priority filter + 10; policy drop;",
		"ct state established,related accept",
		`iifname "lo" accept`,
		"meta l4proto { icmp, ipv6-icmp } accept",
		"udp sport 67 udp dport 68 accept",
		"ct mark and 0xff000000 == 0x4a000000 accept",
		"ip saddr @operator4 accept",
		"ip6 saddr @operator6 accept",
		`tcp dport { 22, 8443 } counter accept comment "jd-fw-4"`,
		`ip saddr 203.0.113.0/24 counter drop comment "jd-fw-5"`,
		`iifname "eth0" meta l4proto { tcp, udp } th dport { 51820 } counter accept comment "jd-fw-6"`,
		`ip6 saddr 2001:db8::/32 meta l4proto udp counter reject comment "jd-fw-7"`,
	}
	at := 0
	for _, want := range order {
		i := strings.Index(got[at:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", want, got)
		}
		at += i + len(want)
	}
	if strings.Contains(got, "jd_gateway") || strings.Count(got, "table inet ") != 3 {
		t.Fatalf("the render names only its own table:\n%s", got)
	}
	for _, bad := range []FirewallRuleSpec{
		{ID: 1, Action: "accept", Interface: "eth0; flush ruleset"},
		{ID: 1, Action: "accept", Source: "not-an-address"},
		{ID: 1, Action: "accept", Ports: "22; reboot"},
		{ID: 1, Action: "jump", Ports: "22"},
	} {
		if _, err := firewallRuleLine(bad); err == nil {
			t.Errorf("%+v rendered", bad)
		}
	}
}

func TestOwnedFirewallRequestsAreValidated(t *testing.T) {
	r, err := FirewallRuleRequest{Action: "allow", Protocol: "TCP", Ports: "8000:8010,22", Source: "10.0.0.7", Comment: "office"}.spec()
	if err != nil || r.Action != "accept" || r.Protocol != "tcp" || r.Ports != "8000-8010,22" || r.Source != "10.0.0.7/32" {
		t.Fatalf("rule = %+v %v", r, err)
	}
	for _, req := range []FirewallRuleRequest{
		{Action: "limit", Ports: "22"},
		{Action: "allow"},
		{Action: "allow", Ports: "70000"},
		{Action: "allow", Ports: "22", Protocol: "sctp"},
		{Action: "allow", Ports: "22", Interface: "this-is-far-too-long0"},
	} {
		if _, err := req.spec(); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
}

const ownedListing = `{"nftables":[{"metainfo":{}},{"table":{"family":"inet","name":"jd_firewall"}},{"chain":{"family":"inet","table":"jd_firewall","name":"input","type":"filter","hook":"input","prio":10,"policy":"%s"}},{"rule":{"family":"inet","table":"jd_firewall","chain":"input","comment":"jd-fw-established"}}%s]}`

func ownedRule(id string) string {
	return `,{"rule":{"family":"inet","table":"jd_firewall","chain":"input","comment":"jd-fw-` + id + `"}}`
}

func TestChangeOwnedFirewallLoadsVerifiesAndPersists(t *testing.T) {
	rec := rtHost(t).
		on("nft -f", "").
		on("nft delete table inet jd_firewall", "").
		fail("nft -j list table inet jd_firewall", "Error: No such file or directory")
	s := testService(t)
	err := s.ChangeOwnedFirewall(context.Background(), OwnedFirewallChange{Op: "add", Rule: &FirewallRuleRequest{Action: "allow", Protocol: "tcp", Ports: "22"}}, "ion")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ran("nft -c -f") {
		t.Fatalf("a switched-off table's removal file needs no check: %v", rec.commands())
	}
	sp := rtLoad(t, s)
	if sp.Firewall == nil || len(sp.Firewall.Rules) != 1 || sp.Firewall.Rules[0].ID != 1 || sp.Firewall.Rules[0].CreatedBy != "ion" || sp.Firewall.Enabled {
		t.Fatalf("spec = %+v", sp.Firewall)
	}
	unit, _ := os.ReadFile(s.paths.Unit)
	if !strings.Contains(string(unit), "ExecStart=-nft -f "+filepath.Join(s.paths.Dir, firewallFile)) || strings.Contains(string(unit), "ExecStop=-nft delete table inet jd_firewall") {
		t.Fatalf("the boot unit restores and never removes the owned table:\n%s", unit)
	}
	file, _ := os.ReadFile(filepath.Join(s.paths.Dir, firewallFile))
	if strings.Contains(string(file), "chain input") {
		t.Fatalf("a switched-off table renders a removal only:\n%s", file)
	}

	// Switching it on: the verification requires the saved policy and rules.
	rec.replies = append([]reply{{prefix: "nft -j list table inet jd_firewall", out: strings.Replace(strings.Replace(ownedListing, "%s", "accept", 1), "%s", "", 1)}}, rec.replies...)
	err = s.ChangeOwnedFirewall(context.Background(), OwnedFirewallChange{Op: "enable"}, "ion")
	if err == nil || !strings.Contains(err.Error(), "missing 1 of the owned firewall's rules") {
		t.Fatalf("an incomplete kernel table: %v", err)
	}
	if !rec.ran("nft -c -f") {
		t.Fatalf("the enabled candidate is checked before it loads: %v", rec.commands())
	}
	if rtLoad(t, s).Firewall.Enabled {
		t.Fatal("a failed verification left the table saved as on")
	}
	journal, err := readChange(s.paths.Dir)
	if err != nil || journal.Phase != "recovered" {
		t.Fatalf("journal = %+v %v", journal, err)
	}
	if !rec.ran("nft delete table inet jd_firewall") {
		t.Fatalf("the undo removes the table the old spec did not switch on: %v", rec.commands())
	}
}

func TestOwnedFirewallChangesAreJournaledWithTheirUndo(t *testing.T) {
	rtHost(t).on("nft -f", "")
	s := testService(t)
	old := emptySpec()
	old.Firewall = &FirewallSpec{Enabled: true, Incoming: "accept", Rules: []FirewallRuleSpec{{ID: 1, Action: "accept", Ports: "22", Protocol: "tcp"}}}
	next := old.clone()
	next.Firewall.Incoming = "drop"
	commands, err := s.recoveryPlan(context.Background(), old, next)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range commands {
		if c.Tool == "nft" && strings.Join(c.Args, " ") == "-f "+filepath.Join(s.paths.Dir, firewallFile) {
			found = true
		}
	}
	if !found {
		t.Fatalf("recovery reloads the saved owned table: %+v", commands)
	}
	off := emptySpec()
	commands, _ = s.recoveryPlan(context.Background(), off, next)
	if len(commands) == 0 || commands[0].Tool != "nft" || strings.Join(commands[0].Args, " ") != "delete table inet jd_firewall" || !commands[0].AllowGone {
		t.Fatalf("recovery from no table removes it: %+v", commands)
	}
}

func TestOwnedFirewallReadsTheKernelAndForeignTables(t *testing.T) {
	record(t).
		on("nft -j list tables", `{"nftables":[{"metainfo":{}},{"table":{"family":"ip","name":"filter"}},{"table":{"family":"inet","name":"jd_gateway"}},{"table":{"family":"inet","name":"jd_firewall"}}]}`).
		on("nft list table inet jd_firewall", "table inet jd_firewall {\n}\n")
	s := testService(t)
	sp := emptySpec()
	sp.Firewall = &FirewallSpec{Enabled: true, Incoming: "drop"}
	rtSaveSpec(t, s, sp)
	v, err := s.OwnedFirewall(context.Background())
	if err != nil || v.Runtime != "present" || !v.Enabled || v.Incoming != "drop" || len(v.Foreign) != 2 || v.Foreign[0] != "ip filter" {
		t.Fatalf("view = %+v %v", v, err)
	}
}

func TestGatewayCapabilityCountsTheOwnedFirewallAsOwned(t *testing.T) {
	record(t).
		on("nft -t -j list ruleset", `{"nftables":[{"chain":{"family":"inet","table":"jd_firewall","name":"input","type":"filter","hook":"input","prio":10,"policy":"drop"}},{"rule":{"family":"inet","table":"jd_firewall","chain":"input","expr":[{"match":{"op":"in","left":{"ct":{"key":"state"}},"right":["established","related"]}},{"accept":null}]}}]}`).
		fail("firewall-cmd --state", "not running").
		on("ufw status", "Status: inactive").
		fail("iptables -S DOCKER-USER", "No chain/target/match by that name")
	s := testService(t)
	c := s.GatewayCapability(context.Background())
	if c.Blocker != nil || len(c.Layers) != 1 || c.Layers[0].Status != "owned" {
		t.Fatalf("capability = %+v", c)
	}
}

func ufwFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range ufwRecoveryFiles {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("original "+filepath.Base(path)+"\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	prev := firewallRecoveryRoot
	firewallRecoveryRoot = root
	t.Cleanup(func() { firewallRecoveryRoot = prev })
	return root
}

func TestProtectFirewallChangeRestoresTheToolsFilesWhenVerificationFails(t *testing.T) {
	root := ufwFixture(t)
	rec := record(t).on("ufw --force disable", "Firewall stopped").on("systemctl daemon-reload", "")
	s := testService(t)
	userRules := filepath.Join(root, "/etc/ufw/user.rules")
	err := s.ProtectFirewallChange(context.Background(), FirewallState{Backend: "ufw", Enabled: false}, noCheck,
		func(context.Context) error { return os.WriteFile(userRules, []byte("candidate\n"), 0o640) },
		func(context.Context) error { return errors.New("SSH from your address would be refused") })
	if err == nil || !strings.Contains(err.Error(), "SSH from your address") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(userRules); string(b) != "original user.rules\n" {
		t.Fatalf("user.rules = %q", b)
	}
	if !rec.ran("ufw --force disable") {
		t.Fatalf("the prior switch is put back: %v", rec.commands())
	}
	j, err := readChange(s.paths.Dir)
	if err != nil || j.Firewall != "ufw" || j.Phase != "recovered" || len(j.Files) != len(ufwRecoveryFiles) {
		t.Fatalf("journal = %+v %v", j, err)
	}
}

func TestProtectFirewallChangeSavesAndLeavesTheFilesItChanged(t *testing.T) {
	root := ufwFixture(t)
	record(t)
	s := testService(t)
	userRules := filepath.Join(root, "/etc/ufw/user.rules")
	if err := s.ProtectFirewallChange(context.Background(), FirewallState{Backend: "ufw", Enabled: true}, noCheck,
		func(context.Context) error { return os.WriteFile(userRules, []byte("candidate\n"), 0o640) }, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(userRules); string(b) != "candidate\n" {
		t.Fatalf("user.rules = %q", b)
	}
	j, _ := readChange(s.paths.Dir)
	if j.Phase != "saved" || j.Persistence != "not_applicable" {
		t.Fatalf("journal = %+v", j.ChangeStatus)
	}
	// A later independent recovery of a saved journal changes nothing.
	if err := RecoverNetwork(context.Background(), s.paths.Dir, j.ID); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(userRules); string(b) != "candidate\n" {
		t.Fatal("a saved change was undone")
	}
}

func noCheck(context.Context) error { return nil }

func TestARefusedFirewallChangeOpensNoJournalAndRunsNoRecovery(t *testing.T) {
	root := ufwFixture(t)
	rec := record(t)
	s := testService(t)
	applied := false
	err := s.ProtectFirewallChange(context.Background(), FirewallState{Backend: "ufw", Enabled: true},
		func(context.Context) error { return errors.New("port must be a number") },
		func(context.Context) error { applied = true; return nil }, nil)
	if err == nil || !strings.Contains(err.Error(), "port must be a number") || applied {
		t.Fatalf("err = %v, applied = %v", err, applied)
	}
	if len(rec.commands()) != 0 {
		t.Fatalf("a refused change ran %v", rec.commands())
	}
	if _, err := readChange(s.paths.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a journal was opened for a refused change: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "/etc/ufw/user.rules")); string(b) != "original user.rules\n" {
		t.Fatalf("user.rules = %q", b)
	}
}

func TestIndependentRecoveryRestoresAPendingFirewallChange(t *testing.T) {
	root := ufwFixture(t)
	record(t)
	s := testService(t)
	userRules := filepath.Join(root, "/etc/ufw/user.rules")
	files := []recoverySnapshot{}
	for _, p := range ufwRecoveryFilePaths() {
		saved, _ := saveNetworkFile(p)
		files = append(files, recoverySnapshot{Path: p, Data: saved.data, Mode: saved.perm, Exists: saved.exists})
	}
	j := &changeJournal{Paths: s.paths, Firewall: "ufw", Files: files,
		Commands:     firewallRecoveryCommands(FirewallState{Backend: "ufw", Enabled: true}),
		ChangeStatus: ChangeStatus{ID: "fwpending", Phase: "awaiting_confirmation", Generation: strings.Repeat("a", 64), Watchdog: "armed", Runtime: "applied", Persistence: "not_applicable", Boot: "not_applicable", OwnerUserID: 1}}
	if err := os.MkdirAll(s.paths.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userRules, []byte("unconfirmed\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	var ran []string
	ctx := context.WithValue(context.Background(), recoveryExecutorKey{}, recoveryExecutor(func(_ context.Context, _ []byte, name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return "", nil
	}))
	if err := recoverNetwork(ctx, s.paths.Dir, "fwpending", false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(userRules); string(b) != "original user.rules\n" {
		t.Fatalf("user.rules = %q", b)
	}
	if len(ran) < 2 || ran[0] != "ufw --force enable" || ran[1] != "ufw reload" {
		t.Fatalf("ran = %v", ran)
	}

	// The vocabulary is closed: a firewall journal naming another tool or a
	// file outside the tool's own is refused before anything runs.
	j.Phase = "awaiting_confirmation"
	j.Commands = []recoveryCommand{{Tool: "ufw", Args: []string{"--force", "reset"}}}
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if err := recoverNetwork(ctx, s.paths.Dir, "fwpending", false); err == nil || !strings.Contains(err.Error(), "unexpected recovery tool") {
		t.Fatalf("a foreign command: %v", err)
	}
	j.Commands = firewallRecoveryCommands(FirewallState{Backend: "ufw"})
	j.Files = append(j.Files, recoverySnapshot{Path: "/etc/shadow", Data: []byte("x"), Mode: 0o600, Exists: true})
	if err := j.save(); err != nil {
		t.Fatal(err)
	}
	if err := recoverNetwork(ctx, s.paths.Dir, "fwpending", false); err == nil || !strings.Contains(err.Error(), "unexpected file") {
		t.Fatalf("a foreign file: %v", err)
	}
}

func TestFirewalldRecoveryRestoresUnitServiceAndZone(t *testing.T) {
	lines := func(state FirewallState) string {
		var out []string
		for _, c := range firewallRecoveryCommands(state) {
			out = append(out, c.Tool+" "+strings.Join(c.Args, " "))
		}
		return strings.Join(out, "|")
	}
	if got := lines(FirewallState{Backend: "firewalld", Enabled: true, Unit: "enabled", Zone: "public"}); got != "systemctl enable firewalld|systemctl start firewalld|firewall-cmd --reload" {
		t.Fatalf("commands = %v", got)
	}
	if got := lines(FirewallState{Backend: "firewalld", Enabled: false, Unit: "disabled", Zone: "public"}); got != "systemctl disable firewalld|systemctl stop firewalld" {
		t.Fatalf("commands = %v", got)
	}
	// A unit state systemd did not answer plainly is left as it is: an
	// unreadable answer must not become a firewall that no longer boots.
	if got := lines(FirewallState{Backend: "firewalld", Enabled: true, Zone: "public"}); got != "systemctl start firewalld|firewall-cmd --reload" {
		t.Fatalf("commands = %v", got)
	}
	paths, err := firewallFiles(FirewallState{Backend: "firewalld", Zone: "public"})
	if err != nil || len(paths) != 2 || !strings.HasSuffix(paths[1], "/etc/firewalld/zones/public.xml") {
		t.Fatalf("paths = %v %v", paths, err)
	}
	if _, err := firewallFiles(FirewallState{Backend: "firewalld", Zone: "../../etc"}); err == nil {
		t.Fatal("a zone name escaping the directory")
	}
	if _, err := firewallFiles(FirewallState{Backend: "iptables"}); err == nil {
		t.Fatal("a tool without recovery")
	}
}

func TestRevokingATrustedAddressReloadsTheOwnedTable(t *testing.T) {
	enabled := strings.Replace(strings.Replace(ownedListing, "%s", "drop", 1), "%s", "", 1)
	rec := rtHost(t).on("nft -j list table inet jd_firewall", enabled).on("nft", "")
	s := testService(t)
	sp := emptySpec()
	sp.Firewall = &FirewallSpec{Enabled: true, Incoming: "drop"}
	sp.Trusted = []string{"203.0.113.0/24"}
	rtSaveSpec(t, s, sp)
	before, err := renderFirewall(sp, s.trustedFor(sp))
	if err != nil {
		t.Fatal(err)
	}
	firewallPath := filepath.Join(s.paths.Dir, firewallFile)
	if err := os.WriteFile(firewallPath, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTrusted(context.Background(), "203.0.113.0/24", "198.51.100.9"); err != nil {
		t.Fatal(err)
	}
	if !rec.ran("nft -f " + filepath.Join(s.paths.Dir, firewallApplyFile)) {
		t.Fatalf("the revoked address stays admitted until the table is loaded: %v", rec.commands())
	}
	if b, _ := os.ReadFile(firewallPath); strings.Contains(string(b), "203.0.113.0/24") || !strings.Contains(before, "203.0.113.0/24") {
		t.Fatalf("firewall.nft still admits the revoked range:\n%s", b)
	}
}
