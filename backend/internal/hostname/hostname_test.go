package hostname

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// host is a server the tests can rename: its kernel name, the programs it
// has, and an /etc under a temporary root.
type host struct {
	t        *testing.T
	name     string
	programs map[string]bool
	calls    []string
	// fail makes the named program exit with what it would print.
	fail      map[string]string
	tailscale string
}

func newHost(t *testing.T, name string, programs ...string) (*host, *Service) {
	t.Helper()
	h := &host{t: t, name: name, programs: map[string]bool{}, fail: map[string]string{}}
	for _, p := range programs {
		h.programs[p] = true
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := &Service{
		run:     h.run,
		has:     func(name string) bool { return h.programs[name] },
		current: func() (string, error) { return h.name, nil },
		root:    root,
	}
	return h, svc
}

func (h *host) run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	h.calls = append(h.calls, call)
	if said, ok := h.fail[name]; ok && !strings.HasPrefix(call, "tailscale status") {
		return []byte(said), errors.New("exit status 1")
	}
	switch {
	case call == "tailscale status --json":
		return []byte(h.tailscale), nil
	case name == "hostnamectl" && len(args) == 2 && args[0] == "set-hostname":
		h.name = args[1]
	case name == "hostname" && len(args) == 1:
		h.name = args[0]
	}
	return nil, nil
}

func write(t *testing.T, svc *Service, path, content string) {
	t.Helper()
	full := filepath.Join(svc.root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, svc *Service, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(svc.root, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"atlas":                 "atlas",
		"  Atlas-01 ":           "atlas-01",
		"db.example.com":        "db.example.com",
		"7":                     "7",
		strings.Repeat("a", 63): strings.Repeat("a", 63),
	} {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "   ", "my server", "under_score", "-lead", "trail-", "a..b", ".a", "a.",
		"localhost", "localhost.localdomain", "naïve", "a/b", "$(reboot)",
		strings.Repeat("a", 64), strings.Repeat("a", 40) + "." + strings.Repeat("b", 40),
	} {
		got, err := Normalize(in)
		var invalid *InvalidError
		if !errors.As(err, &invalid) {
			t.Errorf("Normalize(%q) = %q, %v; want it refused", in, got, err)
		}
	}
}

const providerHosts = `# Your system has configured 'manage_etc_hosts' as True.
#
127.0.1.1 vps-1.provider.example vps-1
127.0.0.1 localhost

# The following lines are desirable for IPv6 capable hosts
::1 localhost ip6-localhost ip6-loopback
`

func TestWithServerNameAddsOneMarkedLineAndLeavesTheRest(t *testing.T) {
	got := string(withServerName([]byte(providerHosts), "atlas"))
	want := strings.Replace(providerHosts, "127.0.0.1 localhost\n",
		"127.0.0.1 localhost\n127.0.1.1 atlas "+hostsMark+"\n", 1)
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	// A second rename replaces the dashboard's line and nothing else.
	again := string(withServerName([]byte(got), "zeus"))
	if strings.Contains(again, "atlas") || strings.Count(again, hostsMark) != 1 ||
		!strings.Contains(again, "127.0.1.1 zeus "+hostsMark) {
		t.Fatalf("second rename left:\n%s", again)
	}

	// Back to a name the provider's own line carries: no line of ours.
	back := string(withServerName([]byte(again), "vps-1"))
	if back != providerHosts {
		t.Fatalf("renaming back did not restore the file:\n%s", back)
	}
}

func TestWithServerNameOnOddFiles(t *testing.T) {
	// No terminator on the last line, and no loopback record to sit after.
	if got := string(withServerName([]byte("10.0.0.5 gateway"), "atlas")); got != "10.0.0.5 gateway\n127.0.1.1 atlas "+hostsMark+"\n" {
		t.Errorf("unterminated file became %q", got)
	}
	if got := string(withServerName(nil, "atlas")); got != "127.0.1.1 atlas "+hostsMark+"\n" {
		t.Errorf("empty file became %q", got)
	}
	// A name in a comment is not a record that resolves.
	if got := string(withServerName([]byte("# atlas lives here\n"), "atlas")); !strings.Contains(got, "127.0.1.1 atlas") {
		t.Errorf("a comment was taken for a record: %q", got)
	}
	// CRLF lines are carried as they are.
	crlf := "127.0.0.1 localhost\r\n::1 localhost\r\n"
	if got := string(withServerName([]byte(crlf), "atlas")); !strings.HasPrefix(got, "127.0.0.1 localhost\r\n127.0.1.1 atlas") || !strings.HasSuffix(got, "::1 localhost\r\n") {
		t.Errorf("CRLF file became %q", got)
	}
}

func TestRenameSetsTheNameAndRecordsIt(t *testing.T) {
	h, svc := newHost(t, "vps-1", "hostnamectl")
	write(t, svc, "/etc/hosts", "127.0.0.1 localhost\n")

	result, err := svc.Rename(context.Background(), " Atlas ")
	if err != nil {
		t.Fatal(err)
	}
	if result.Hostname != "atlas" || result.Previous != "vps-1" || len(result.Warnings) != 0 || result.TailscaleName != "" {
		t.Fatalf("result = %+v", result)
	}
	if got := strings.Join(h.calls, "; "); got != "hostnamectl set-hostname atlas" {
		t.Fatalf("ran %q", got)
	}
	if got := read(t, svc, "/etc/hosts"); got != "127.0.0.1 localhost\n127.0.1.1 atlas "+hostsMark+"\n" {
		t.Fatalf("hosts = %q", got)
	}
}

func TestRenameToTheSameNameDoesNothing(t *testing.T) {
	h, svc := newHost(t, "atlas", "hostnamectl")
	write(t, svc, "/etc/hosts", "127.0.0.1 localhost\n")
	result, err := svc.Rename(context.Background(), "atlas")
	if err != nil || result.Hostname != "atlas" || result.Previous != "atlas" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if len(h.calls) != 0 {
		t.Fatalf("ran %v", h.calls)
	}
	if got := read(t, svc, "/etc/hosts"); got != "127.0.0.1 localhost\n" {
		t.Fatalf("hosts = %q", got)
	}
}

func TestRenameRefusesABadNameBeforeTouchingTheHost(t *testing.T) {
	h, svc := newHost(t, "vps-1", "hostnamectl", "tailscale")
	if _, err := svc.Rename(context.Background(), "my server"); err == nil {
		t.Fatal("accepted a name with a space")
	}
	if len(h.calls) != 0 {
		t.Fatalf("ran %v", h.calls)
	}
}

// Without hostnamectl, or with one whose daemon will not answer, the name is
// written to /etc/hostname and given to the kernel directly.
func TestRenameWithoutSystemd(t *testing.T) {
	for _, broken := range []bool{false, true} {
		programs := []string{"hostname"}
		if broken {
			programs = append(programs, "hostnamectl")
		}
		h, svc := newHost(t, "vps-1", programs...)
		if broken {
			h.fail["hostnamectl"] = "Failed to connect to bus: No such file or directory"
		}
		write(t, svc, "/etc/hostname", "vps-1\n")
		if _, err := svc.Rename(context.Background(), "atlas"); err != nil {
			t.Fatalf("broken=%v: %v", broken, err)
		}
		if got := read(t, svc, "/etc/hostname"); got != "atlas\n" {
			t.Errorf("broken=%v: /etc/hostname = %q", broken, got)
		}
		if h.name != "atlas" {
			t.Errorf("broken=%v: kernel name is %q", broken, h.name)
		}
	}
}

func TestRenameSaysWhyTheHostRefused(t *testing.T) {
	h, svc := newHost(t, "vps-1", "hostnamectl", "hostname")
	h.fail["hostnamectl"] = "Could not set static hostname: Access denied"
	h.fail["hostname"] = "hostname: you must be root to change the host name"
	_, err := svc.Rename(context.Background(), "atlas")
	if err == nil {
		t.Fatal("reported a rename that did not happen")
	}
	for _, want := range []string{"Access denied", "you must be root"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not carry %q", err, want)
		}
	}
	if h.name != "vps-1" {
		t.Errorf("kernel name is %q", h.name)
	}
}

// A kernel that kept its old name after every command succeeded is a failed
// rename, not a finished one.
func TestRenameChecksTheNameItSet(t *testing.T) {
	_, svc := newHost(t, "vps-1", "hostnamectl")
	svc.current = func() (string, error) { return "vps-1", nil }
	if _, err := svc.Rename(context.Background(), "atlas"); err == nil || !strings.Contains(err.Error(), `"vps-1"`) {
		t.Fatalf("err = %v", err)
	}
}

const tailnetStatus = `{"BackendState":"Running","Self":{"HostName":"vps-1","DNSName":"vps-1-2.tail1234.ts.net."}}`

// Tailscale follows the host's name unless told what to ask for, and the
// dashboard's address is spelt with the tailnet's. The name is held first.
func TestRenameHoldsTheTailnetNameBeforeTheHostNameMoves(t *testing.T) {
	h, svc := newHost(t, "vps-1", "hostnamectl", "tailscale")
	h.tailscale = tailnetStatus
	result, err := svc.Rename(context.Background(), "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if result.TailscaleName != "vps-1-2" {
		t.Fatalf("tailnet name = %q", result.TailscaleName)
	}
	want := "tailscale status --json; tailscale set --hostname=vps-1-2; hostnamectl set-hostname atlas"
	if got := strings.Join(h.calls, "; "); got != want {
		t.Fatalf("ran %q\nwant %q", got, want)
	}
}

func TestRenameStopsWhenARunningTailnetNameCannotBeHeld(t *testing.T) {
	h, svc := newHost(t, "vps-1", "hostnamectl", "tailscale")
	h.tailscale = tailnetStatus
	h.fail["tailscale"] = "Access denied: prefs write access denied"
	if _, err := svc.Rename(context.Background(), "atlas"); err == nil || !strings.Contains(err.Error(), "prefs write access denied") {
		t.Fatalf("err = %v", err)
	}
	if h.name != "vps-1" {
		t.Fatalf("the server was renamed to %q anyway", h.name)
	}
}

func TestRenameGoesOnWhenAStoppedTailscaleCannotBeTold(t *testing.T) {
	h, svc := newHost(t, "vps-1", "hostnamectl", "tailscale")
	h.tailscale = strings.Replace(tailnetStatus, "Running", "Stopped", 1)
	h.fail["tailscale"] = "failed to connect to local tailscaled"
	result, err := svc.Rename(context.Background(), "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if h.name != "atlas" || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "Tailscale") {
		t.Fatalf("name %q, result %+v", h.name, result)
	}
}

func TestCurrentReadsTheTailnetName(t *testing.T) {
	h, svc := newHost(t, "vps-1", "tailscale")
	h.tailscale = "Warning: client version differs\n" + tailnetStatus
	state, err := svc.Current(context.Background())
	if err != nil || state.Hostname != "vps-1" || state.TailscaleName != "vps-1-2" {
		t.Fatalf("state = %+v, %v", state, err)
	}
	// Signed out: no node, so no name to hold.
	h.tailscale = `{"BackendState":"NeedsLogin","Self":null}`
	if state, _ := svc.Current(context.Background()); state.TailscaleName != "" {
		t.Fatalf("signed-out tailnet name = %q", state.TailscaleName)
	}
}

// cloud-init regenerates /etc/hosts from its template at every start where
// the file's own header says so; the line has to be in the template to last.
func TestRenameReachesCloudInitsHostsTemplate(t *testing.T) {
	_, svc := newHost(t, "vps-1", "hostnamectl")
	write(t, svc, "/etc/hosts", providerHosts)
	template := "## template:jinja\n{#\nheader\n#}\n127.0.1.1 {{fqdn}} {{hostname}}\n127.0.0.1 localhost\n\n::1 localhost ip6-localhost\n"
	write(t, svc, "/etc/cloud/templates/hosts.debian.tmpl", template)
	write(t, svc, "/etc/cloud/templates/chrony.conf.debian.tmpl", "pool x\n")

	result, err := svc.Rename(context.Background(), "atlas")
	if err != nil || len(result.Warnings) != 0 {
		t.Fatalf("result = %+v, %v", result, err)
	}
	want := strings.Replace(template, "127.0.0.1 localhost\n", "127.0.0.1 localhost\n127.0.1.1 atlas "+hostsMark+"\n", 1)
	if got := read(t, svc, "/etc/cloud/templates/hosts.debian.tmpl"); got != want {
		t.Fatalf("template:\n%s\nwant:\n%s", got, want)
	}
	if got := read(t, svc, "/etc/cloud/templates/chrony.conf.debian.tmpl"); got != "pool x\n" {
		t.Fatalf("another template was touched: %q", got)
	}
}

// In the shipped stack the server's /etc/hosts and /etc/hostname are busy
// under /host as well, because Docker mounts its own over the container's
// copies; each write is handed over by the server's own path so it can be
// made where they are not.
func TestRenameWritesTheServersFilesByTheirOwnPaths(t *testing.T) {
	_, svc := newHost(t, "vps-1", "hostname")
	write(t, svc, "/etc/hostname", "vps-1\n")
	write(t, svc, "/etc/hosts", providerHosts)
	write(t, svc, "/etc/cloud/templates/hosts.debian.tmpl", "127.0.0.1 localhost\n")
	var handed []string
	svc.onServer = func(real, mirrored string, write func(string) error) error {
		if mirrored != filepath.Join(svc.root, real) {
			t.Errorf("%s was handed over as %s", real, mirrored)
		}
		handed = append(handed, real)
		return write(mirrored)
	}

	result, err := svc.Rename(context.Background(), "atlas")
	if err != nil || len(result.Warnings) != 0 {
		t.Fatalf("result = %+v, %v", result, err)
	}
	want := []string{"/etc/hostname", "/etc/hosts", "/etc/cloud/templates/hosts.debian.tmpl"}
	if strings.Join(handed, " ") != strings.Join(want, " ") {
		t.Fatalf("handed over %v, want %v", handed, want)
	}
	if got := read(t, svc, "/etc/hostname"); got != "atlas\n" {
		t.Fatalf("/etc/hostname = %q", got)
	}
}

// A write that fails where it was made is the rename's warning, as before.
func TestRenameWarnsWhenTheServersHostsFileCannotBeWritten(t *testing.T) {
	_, svc := newHost(t, "vps-1", "hostnamectl")
	write(t, svc, "/etc/hosts", "127.0.0.1 localhost\n")
	svc.onServer = func(real, _ string, _ func(string) error) error {
		return errors.New("rename " + real + ": device or resource busy")
	}
	result, err := svc.Rename(context.Background(), "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "/etc/hosts could not be updated") {
		t.Fatalf("warnings = %q", result.Warnings)
	}
}

func TestRenameLeavesTheTemplateAloneWhereCloudInitDoesNotOwnHosts(t *testing.T) {
	_, svc := newHost(t, "vps-1", "hostnamectl")
	write(t, svc, "/etc/hosts", "127.0.0.1 localhost\n")
	write(t, svc, "/etc/cloud/templates/hosts.debian.tmpl", "127.0.0.1 localhost\n")
	if _, err := svc.Rename(context.Background(), "atlas"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, svc, "/etc/cloud/templates/hosts.debian.tmpl"); got != "127.0.0.1 localhost\n" {
		t.Fatalf("template = %q", got)
	}
}

// A hosts file that is a link is somebody's arrangement. The server is
// renamed and told what was left undone, and the link still points where it did.
func TestRenameWarnsAboutAHostsFileItWillNotReplace(t *testing.T) {
	_, svc := newHost(t, "vps-1", "hostnamectl")
	write(t, svc, "/etc/static-hosts", "127.0.0.1 localhost\n")
	if err := os.Symlink("static-hosts", filepath.Join(svc.root, "etc/hosts")); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Rename(context.Background(), "atlas")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "/etc/hosts") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	if st, err := os.Lstat(filepath.Join(svc.root, "etc/hosts")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	if got := read(t, svc, "/etc/static-hosts"); got != "127.0.0.1 localhost\n" {
		t.Fatalf("the link's target was written: %q", got)
	}
}

func TestWriteKeepingKeepsTheMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeKeeping(path, []byte("b\n")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", st.Mode().Perm(), err)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".hosts.*"))
	if len(left) != 0 {
		t.Fatalf("temporary files left behind: %v", left)
	}
}
