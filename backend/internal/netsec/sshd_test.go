package netsec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSSHDDump(t *testing.T) {
	out := strings.Join([]string{
		"port 22",
		"port 2222",
		"permitrootlogin prohibit-password",
		"passwordauthentication no",
		"# a comment",
		"",
	}, "\n")
	values := parseSSHDDump(out)
	if len(values["port"]) != 2 || values["port"][0] != "22" {
		t.Fatalf("ports = %q", values["port"])
	}
	if values["passwordauthentication"][0] != "no" {
		t.Fatalf("password = %q", values["passwordauthentication"])
	}
}

// sshd takes the *first* value for a keyword, not the last. A parser that
// reads the file like an ini reports the opposite of what the daemon does.
func TestParseSSHDFilesTakesTheFirstValue(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sshd_config"), `
PasswordAuthentication no
PasswordAuthentication yes
`)
	values, _, err := parseSSHDFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if values["passwordauthentication"][0] != "no" {
		t.Fatalf("got %q, want the first value", values["passwordauthentication"])
	}
}

func TestParseSSHDFilesFollowsIncludes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sshd_config.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "sshd_config"), "Include "+dir+"/sshd_config.d/*.conf\nPermitRootLogin yes\n")
	write(t, filepath.Join(dir, "sshd_config.d", "10-local.conf"), "PermitRootLogin no\n")

	values, _, err := parseSSHDFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The include is read where it appears, so its value comes first and
	// therefore wins — which is the whole reason distributions put the
	// Include at the top.
	if values["permitrootlogin"][0] != "no" {
		t.Fatalf("got %q; the drop-in should win", values["permitrootlogin"])
	}
}

// Everything after a Match applies conditionally, and reporting those values
// as the host's settings would be wrong. The fact that they exist is reported
// instead.
func TestParseSSHDFilesStopsAtMatch(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sshd_config"), `
PermitRootLogin no
Match User deploy
    PasswordAuthentication yes
`)
	values, matched, err := parseSSHDFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Error("Match block not reported")
	}
	if len(values["passwordauthentication"]) != 0 {
		t.Errorf("read a conditional value as unconditional: %q", values["passwordauthentication"])
	}
}

func TestIncludesDropIns(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "sshd_config"), "Include /etc/ssh/sshd_config.d/*.conf\nPort 22\n")
	if !includesDropIns(dir) {
		t.Error("an Include at the top should be found")
	}

	// An Include *after* a directive means a drop-in setting that directive
	// would be ignored — a file this dashboard wrote and sshd discarded.
	other := t.TempDir()
	write(t, filepath.Join(other, "sshd_config"), "Port 22\nInclude /etc/ssh/sshd_config.d/*.conf\n")
	if includesDropIns(other) {
		t.Error("an Include below a directive must not count")
	}
}

func TestApplyDirectivesReplacesInPlace(t *testing.T) {
	got := applyDirectives("# header\nPasswordAuthentication yes\nPort 22\n",
		map[string]string{"passwordauthentication": "no"}, false)
	if !strings.Contains(got, "PasswordAuthentication no") {
		t.Fatalf("not replaced:\n%s", got)
	}
	if strings.Contains(got, "PasswordAuthentication yes") {
		t.Fatalf("old value left behind:\n%s", got)
	}
	if !strings.Contains(got, "Port 22") {
		t.Fatalf("unrelated line lost:\n%s", got)
	}
}

// A directive appended after an existing one is read and discarded by sshd,
// which looks exactly like a setting that took effect. Later duplicates are
// commented out rather than deleted so the file still shows what was there.
func TestApplyDirectivesCommentsOutLaterDuplicates(t *testing.T) {
	got := applyDirectives("PermitRootLogin yes\nPort 22\nPermitRootLogin yes\n",
		map[string]string{"permitrootlogin": "no"}, false)
	if !strings.Contains(got, "# PermitRootLogin yes") {
		t.Fatalf("the later duplicate should be commented out, not deleted:\n%s", got)
	}
	// One active line, and it is the new value. Counted over uncommented
	// lines, because the commented one still contains the directive name.
	active := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "PermitRootLogin") {
			active++
			if !strings.HasSuffix(line, " no") {
				t.Fatalf("active line is not the new value: %q", line)
			}
		}
	}
	if active != 1 {
		t.Fatalf("expected exactly one active value, got %d:\n%s", active, got)
	}
}

func TestApplyDirectivesAppendsWhenAbsent(t *testing.T) {
	got := applyDirectives("Port 22\n", map[string]string{"x11forwarding": "no"}, false)
	if !strings.Contains(got, "X11Forwarding no") {
		t.Fatalf("not appended:\n%s", got)
	}
	if !strings.Contains(got, "# Set by Just Dashboard") {
		t.Fatalf("appended lines are unmarked:\n%s", got)
	}
}

func TestApplyDirectivesWritesAHeaderForANewDropIn(t *testing.T) {
	got := applyDirectives("", map[string]string{"passwordauthentication": "no"}, true)
	if !strings.Contains(got, "Written by Just Dashboard") {
		t.Fatalf("a new drop-in should explain itself:\n%s", got)
	}
}

func TestCanonicalDirective(t *testing.T) {
	if got := canonicalDirective("permitrootlogin"); got != "PermitRootLogin" {
		t.Errorf("got %q", got)
	}
	if got := canonicalDirective("somethingelse"); got != "somethingelse" {
		t.Errorf("unknown keys should pass through, got %q", got)
	}
}

func TestValidateSSHValue(t *testing.T) {
	choice, _ := sshDirectiveFor("permitrootlogin")
	if err := validateSSHValue(choice, "no"); err != nil {
		t.Errorf("rejected a listed option: %v", err)
	}
	if err := validateSSHValue(choice, "maybe"); err == nil {
		t.Error("accepted a value not on the list")
	}
	number, _ := sshDirectiveFor("maxauthtries")
	if err := validateSSHValue(number, "3"); err != nil {
		t.Errorf("rejected a number: %v", err)
	}
	if err := validateSSHValue(number, "three"); err == nil {
		t.Error("accepted a non-number")
	}
	if err := validateSSHValue(number, "999999"); err == nil {
		t.Error("accepted an out-of-range number")
	}
}

func TestSSHDirectiveSecure(t *testing.T) {
	root, _ := sshDirectiveFor("permitrootlogin")
	if !root.secure("prohibit-password") || !root.secure("no") {
		t.Error("both acceptable answers should count as secure")
	}
	if root.secure("yes") {
		t.Error("yes is the one that is not")
	}
	tries, _ := sshDirectiveFor("maxauthtries")
	if !tries.secure("3") || tries.secure("10") {
		t.Error("maxauthtries bound wrong")
	}
	idle, _ := sshDirectiveFor("clientaliveinterval")
	if idle.secure("0") {
		t.Error("zero means no idle check at all")
	}
}

// The guard exists for the commonest way people lose a server: turning off
// password authentication on a host where nobody has a key.
func TestGuardSSHLockout(t *testing.T) {
	withKeys := &SSHDConfig{
		KeyedAccounts: []KeyedAccount{{User: "deploy", Keys: 1}},
		Settings: []SSHSetting{
			{Key: "passwordauthentication", Value: "yes"},
			{Key: "pubkeyauthentication", Value: "yes"},
			{Key: "permitrootlogin", Value: "prohibit-password"},
		},
	}
	if err := guardSSHLockout(withKeys, map[string]string{"passwordauthentication": "no"}); err != nil {
		t.Fatalf("refused the correct thing to do: %v", err)
	}

	noKeys := &SSHDConfig{Settings: withKeys.Settings}
	if err := guardSSHLockout(noKeys, map[string]string{"passwordauthentication": "no"}); err == nil {
		t.Fatal("allowed a certain lockout")
	}

	if err := guardSSHLockout(withKeys, map[string]string{
		"passwordauthentication": "no", "pubkeyauthentication": "no",
	}); err == nil {
		t.Fatal("allowed a configuration nothing can authenticate against")
	}

	rootOnly := &SSHDConfig{
		KeyedAccounts: []KeyedAccount{{User: "root", Keys: 2}},
		Settings:      withKeys.Settings,
	}
	if err := guardSSHLockout(rootOnly, map[string]string{
		"permitrootlogin": "no", "passwordauthentication": "no",
	}); err == nil {
		t.Fatal("allowed locking out the only keyed account")
	}
}

func TestApplySSHSettingsRejectsUnknownKeys(t *testing.T) {
	s := New()
	if _, err := s.ApplySSHSettings(t.Context(), map[string]string{"authorizedkeysfile": "/tmp/keys"}); err == nil {
		t.Error("accepted a directive outside the closed set")
	}
	if _, err := s.ApplySSHSettings(t.Context(), map[string]string{}); err == nil {
		t.Error("accepted an empty change set")
	}
	if _, err := s.ApplySSHSettings(t.Context(), map[string]string{
		"permitrootlogin": "no\nPermitEmptyPasswords yes",
	}); err == nil {
		t.Error("accepted a value carrying a second directive")
	}
}

func TestCountAuthorizedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authorized_keys")
	write(t, path, "# a comment\n\nssh-ed25519 AAAA one\nssh-rsa AAAA two\n")
	if got := countAuthorizedKeys(path); got != 2 {
		t.Fatalf("counted %d, want 2", got)
	}
	if got := countAuthorizedKeys(filepath.Join(t.TempDir(), "missing")); got != 0 {
		t.Fatalf("counted %d for a missing file", got)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Moving sshd's port with no firewall rule for the new one is the same class
// of mistake as turning off passwords with no key: it applies cleanly and the
// next connection has nowhere to land.
func TestGuardSSHPort(t *testing.T) {
	denyAll := &FirewallStatus{
		Available: true, Enabled: true,
		Policy: DefaultPolicy{Incoming: "deny"},
		Rules:  []Rule{{Action: "ALLOW", Port: "22", To: "22/tcp"}},
	}
	if err := guardSSHPort("2222", denyAll); err == nil {
		t.Fatal("allowed a move to a port the firewall denies")
	}
	if err := guardSSHPort("22", denyAll); err != nil {
		t.Fatalf("refused a port the firewall already allows: %v", err)
	}

	// A comma-separated rule covers several ports at once.
	multi := &FirewallStatus{
		Available: true, Enabled: true,
		Policy: DefaultPolicy{Incoming: "deny"},
		Rules:  []Rule{{Action: "ALLOW", Port: "22,2222", To: "22,2222/tcp"}},
	}
	if err := guardSSHPort("2222", multi); err != nil {
		t.Errorf("a list rule should cover its ports: %v", err)
	}

	// Nothing to guard against when the firewall is off or lets everything in.
	for _, fw := range []*FirewallStatus{
		nil,
		{Available: false},
		{Available: true, Enabled: false},
		{Available: true, Enabled: true, Policy: DefaultPolicy{Incoming: "allow"}},
	} {
		if err := guardSSHPort("2222", fw); err != nil {
			t.Errorf("refused with no firewall in the way: %v", err)
		}
	}
}

// An allow list that names nobody who holds a key, with passwords already off,
// is a lockout written as a whitelist.
func TestGuardAllowUsers(t *testing.T) {
	current := &SSHDConfig{KeyedAccounts: []KeyedAccount{{User: "deploy", Keys: 1}}}
	if err := guardAllowUsers(current, "someone-else", true); err == nil {
		t.Fatal("allowed a list excluding every keyed account")
	}
	if err := guardAllowUsers(current, "deploy admin", true); err != nil {
		t.Fatalf("refused a list that includes the keyed account: %v", err)
	}
	// With passwords still on, the list is a restriction rather than the only
	// way in, and refusing it would block a legitimate tightening.
	if err := guardAllowUsers(current, "someone-else", false); err != nil {
		t.Fatalf("refused with passwords still available: %v", err)
	}
	if err := guardAllowUsers(current, "", true); err != nil {
		t.Fatalf("an empty list is no restriction at all: %v", err)
	}
}

func TestValidateSSHListValues(t *testing.T) {
	def, _ := sshDirectiveFor("allowusers")
	if err := validateSSHValue(def, "deploy admin ci-runner"); err != nil {
		t.Errorf("rejected ordinary account names: %v", err)
	}
	if err := validateSSHValue(def, ""); err != nil {
		t.Errorf("an empty list means no restriction and must be allowed: %v", err)
	}
	for _, bad := range []string{"deploy; rm -rf /", "deploy\nPermitRootLogin yes", "a$b"} {
		if err := validateSSHValue(def, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A port is not better or worse, but it does have a legal range.
func TestValidateSSHPort(t *testing.T) {
	def, _ := sshDirectiveFor("port")
	if err := validateSSHValue(def, "2222"); err != nil {
		t.Errorf("rejected a valid port: %v", err)
	}
	for _, bad := range []string{"0", "70000", "-1"} {
		if err := validateSSHValue(def, bad); err == nil {
			t.Errorf("accepted port %q", bad)
		}
	}
	if !def.secure("2222") {
		t.Error("a port has no better or worse value and should not be graded")
	}
}

// Writing a keyword with no argument stops sshd from starting, so an emptied
// list has to be commented out instead.
func TestApplyDirectivesRemovesAnEmptiedList(t *testing.T) {
	got := applyDirectives("AllowUsers deploy\nPort 22\n", map[string]string{"allowusers": ""}, false)
	if strings.Contains(got, "\nAllowUsers") {
		t.Fatalf("left an active AllowUsers behind:\n%s", got)
	}
	if !strings.Contains(got, "# AllowUsers deploy") {
		t.Fatalf("should be commented out rather than deleted:\n%s", got)
	}
	if !strings.Contains(got, "Port 22") {
		t.Fatalf("unrelated line lost:\n%s", got)
	}
}

func TestApplyDirectivesDoesNotAppendAnEmptyList(t *testing.T) {
	got := applyDirectives("Port 22\n", map[string]string{"allowusers": ""}, false)
	if strings.Contains(got, "AllowUsers") {
		t.Fatalf("wrote a keyword with no argument:\n%s", got)
	}
}

// The bastion settings, read from `sshd -T`, which prints every directive
// lowercased with its effective value. This excerpt is a stock Ubuntu 25.04
// sshd (OpenSSH 9.9), whose defaults are the ones the recommendations differ
// from.
const sshdBastionDump = `port 22
permitrootlogin without-password
maxsessions 10
gatewayports no
allowtcpforwarding yes
allowagentforwarding yes
permittunnel no
`

func TestBastionDirectivesReadFromSSHDDump(t *testing.T) {
	values := parseSSHDDump(sshdBastionDump)
	type want struct {
		value  string
		secure bool
	}
	for key, w := range map[string]want{
		// Not graded: a jump host needs it, and the recommendation says so.
		"allowtcpforwarding":   {"yes", true},
		"gatewayports":         {"no", true},
		"allowagentforwarding": {"yes", false},
		"permittunnel":         {"no", true},
		"maxsessions":          {"10", true},
	} {
		def, ok := sshDirectiveFor(key)
		if !ok {
			t.Fatalf("%s is not in the closed list", key)
		}
		value := def.canonical(first(values[def.Key], def.Default))
		if value != w.value || def.secure(value) != w.secure {
			t.Errorf("%s = %q (secure %v), want %q (secure %v)", key, value, def.secure(value), w.value, w.secure)
		}
	}
}

// An absent line means sshd's default, which is what the setting shows.
func TestBastionDirectivesFallBackToTheirDefaults(t *testing.T) {
	for key, want := range map[string]string{
		"allowtcpforwarding": "yes", "gatewayports": "no", "allowagentforwarding": "yes",
		"permittunnel": "no", "maxsessions": "10",
	} {
		def, _ := sshDirectiveFor(key)
		if def.Default != want {
			t.Errorf("%s defaults to %q, want %q", key, def.Default, want)
		}
	}
}

func TestBastionDirectiveValues(t *testing.T) {
	tests := []struct {
		key, value string
		ok         bool
	}{
		{"allowtcpforwarding", "no", true},
		{"allowtcpforwarding", "local", true},
		{"allowtcpforwarding", "remote", true},
		{"allowtcpforwarding", "all", true},
		{"allowtcpforwarding", "yes", true},
		{"allowtcpforwarding", "sometimes", false},
		{"gatewayports", "clientspecified", true},
		{"gatewayports", "no", true},
		{"gatewayports", "everywhere", false},
		{"allowagentforwarding", "no", true},
		{"allowagentforwarding", "local", false},
		{"permittunnel", "point-to-point", true},
		{"permittunnel", "ethernet", true},
		{"permittunnel", "tun", false},
		{"maxsessions", "1", true},
		{"maxsessions", "64", true},
		{"maxsessions", "0", false},
		{"maxsessions", "65", false},
		{"maxsessions", "many", false},
	}
	for _, tc := range tests {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			def, ok := sshDirectiveFor(tc.key)
			if !ok {
				t.Fatalf("%s is not in the closed list", tc.key)
			}
			err := validateSSHValue(def, tc.value)
			// Every value that is accepted has to reach the file, and the
			// pattern PlanSSHSettings checks first is what gates that.
			if tc.ok && !sshValueRe.MatchString(tc.value) {
				t.Fatalf("%q would be refused before validation", tc.value)
			}
			if (err == nil) != tc.ok {
				t.Fatalf("validateSSHValue(%s, %q) = %v, want ok=%v", tc.key, tc.value, err, tc.ok)
			}
		})
	}
}

func TestBastionDirectivesAreWrittenInSSHDsSpelling(t *testing.T) {
	changes := map[string]string{
		"allowtcpforwarding": "local", "gatewayports": "no", "allowagentforwarding": "no",
		"permittunnel": "no", "maxsessions": "20",
	}
	// A new drop-in, which is where a host that includes sshd_config.d gets
	// them.
	got := applyDirectives("", changes, true)
	for _, line := range []string{
		"AllowTcpForwarding local", "GatewayPorts no", "AllowAgentForwarding no", "PermitTunnel no", "MaxSessions 20",
	} {
		if !strings.Contains(got, line+"\n") {
			t.Errorf("drop-in lacks %q:\n%s", line, got)
		}
	}
	// In place, in sshd_config itself: the first value wins, so a line that is
	// already there is replaced where it stands, and a commented default is
	// not mistaken for a setting.
	original := "# Defaults\n#AllowTcpForwarding yes\nAllowAgentForwarding yes\nMaxSessions 10\nPort 22\n"
	got = applyDirectives(original, changes, false)
	for _, line := range []string{"AllowAgentForwarding no", "MaxSessions 20", "AllowTcpForwarding local", "PermitTunnel no", "GatewayPorts no"} {
		if !strings.Contains(got, "\n"+line+"\n") {
			t.Errorf("in-place edit lacks %q:\n%s", line, got)
		}
	}
	if strings.Contains(got, "AllowAgentForwarding yes") || strings.Contains(got, "MaxSessions 10") {
		t.Errorf("an old value survived:\n%s", got)
	}
	if !strings.Contains(got, "#AllowTcpForwarding yes") || !strings.Contains(got, "Port 22") {
		t.Errorf("unrelated lines changed:\n%s", got)
	}
}

// Forwarding settings cannot lock anybody out of SSH, and the guard must not
// start reading them as if they could.
func TestGuardSSHLockoutIgnoresBastionSettings(t *testing.T) {
	current := &SSHDConfig{
		KeyedAccounts: []KeyedAccount{{User: "deploy", Keys: 1}},
		Settings: []SSHSetting{
			{Key: "passwordauthentication", Value: "yes"},
			{Key: "pubkeyauthentication", Value: "yes"},
			{Key: "permitrootlogin", Value: "prohibit-password"},
		},
	}
	if err := guardSSHLockout(current, map[string]string{
		"allowtcpforwarding": "no", "gatewayports": "no", "allowagentforwarding": "no", "permittunnel": "no", "maxsessions": "1",
	}); err != nil {
		t.Fatalf("refused a change that cannot cost access: %v", err)
	}
	// With the rest of the configuration unchanged, the same verdicts stand.
	if err := guardSSHLockout(&SSHDConfig{Settings: current.Settings}, map[string]string{
		"passwordauthentication": "no", "allowtcpforwarding": "no",
	}); err == nil {
		t.Fatal("a lockout was let through because it came with a forwarding change")
	}
}

// A pending apply snapshots every file before the first write, so the plan
// has to name the socket drop-in a port move writes as well as the directive
// file, with the bytes each will hold.
func TestSSHPlanNamesEveryFileItWrites(t *testing.T) {
	plan := &SSHApplyPlan{File: "/etc/ssh/sshd_config.d/99-just-dashboard.conf", Content: "Port 2222\n"}
	if files := plan.Files(); len(files) != 1 || files[0].Path != plan.File || plan.SocketUnit() != "" {
		t.Fatalf("directive-only plan=%+v unit=%q", files, plan.SocketUnit())
	}
	plan.Socket = SSHSocket{Unit: "ssh.socket", DropIn: "/etc/systemd/system/ssh.socket.d/10-just-dashboard.conf", Listen: []string{"0.0.0.0:22", "[::]:22"}}
	plan.SocketPort = "2222"
	files := plan.Files()
	if len(files) != 2 || files[1].Path != plan.Socket.DropIn || plan.SocketUnit() != "ssh.socket" {
		t.Fatalf("socket plan=%+v", files)
	}
	if !strings.Contains(files[1].Content, "ListenStream=0.0.0.0:2222") || !strings.Contains(files[1].Content, "ListenStream=[::]:2222") {
		t.Fatalf("socket candidate=%q", files[1].Content)
	}
}

// What an immediate apply reports as a partial success is a failure a
// pending apply must restore.
func TestSSHApplyResultFailureCoversWhatDidNotTakeEffect(t *testing.T) {
	for _, tc := range []struct {
		res  *SSHApplyResult
		fail bool
	}{
		{&SSHApplyResult{Written: true, Valid: true, Reloaded: true}, false},
		{&SSHApplyResult{Written: true, Valid: true, ReloadError: "unit not found"}, true},
		{&SSHApplyResult{Written: true, Valid: true, Reloaded: true, SocketUnit: "ssh.socket", SocketError: "restart failed"}, true},
		{nil, true},
	} {
		if got := tc.res.Failure() != nil; got != tc.fail {
			t.Errorf("Failure(%+v)=%v", tc.res, got)
		}
	}
}
