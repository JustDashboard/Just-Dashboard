package term

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// withPasswd points the account lookup at a fixture. resolveAccount reads the
// host's real /etc/passwd, which is exactly the file a test must not depend on.
func withPasswd(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "passwd")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	original := passwdFile
	passwdFile = path
	t.Cleanup(func() { passwdFile = original })
}

const fixture = `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
postgres:x:114:120::/var/lib/postgresql:/bin/bash
ubuntu:x:1000:1000:Ubuntu:/home/ubuntu:/bin/bash
deploy:x:1001:1001::/home/deploy:/bin/zsh
locked:x:1002:1002::/home/locked:/usr/sbin/nologin
nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin
`

// The default has to be the account an operator would have reached by ssh:
// the login the provider created, not root and not a service account.
func TestLowestRegularAccountPicksTheProvisionedLogin(t *testing.T) {
	withPasswd(t, fixture)
	got, ok := lowestRegularAccount()
	if !ok {
		t.Fatal("no regular account found")
	}
	if got.Name != "ubuntu" {
		t.Fatalf("account = %q, want ubuntu", got.Name)
	}
	if got.Home != "/home/ubuntu" || got.Shell != "/bin/bash" {
		t.Fatalf("home/shell = %q/%q, want /home/ubuntu and /bin/bash", got.Home, got.Shell)
	}
	if got.UID != 1000 || got.GID != 1000 {
		t.Fatalf("uid/gid = %d/%d, want 1000/1000", got.UID, got.GID)
	}
}

// A service account with a real shell still must not be chosen: postgres here
// has /bin/bash but a system UID, and landing an operator in it would be both
// surprising and wrong.
func TestLowestRegularAccountSkipsSystemAndNologin(t *testing.T) {
	withPasswd(t, `root:x:0:0:root:/root:/bin/bash
postgres:x:114:120::/var/lib/postgresql:/bin/bash
svc:x:1000:1000::/home/svc:/usr/sbin/nologin
deploy:x:1005:1005::/home/deploy:/bin/sh
`)
	got, ok := lowestRegularAccount()
	if !ok {
		t.Fatal("no regular account found")
	}
	if got.Name != "deploy" {
		t.Fatalf("account = %q, want deploy (svc is nologin, postgres is a system uid)", got.Name)
	}
}

// A box administered only as root has no regular account, and saying so lets
// the caller fall back deliberately rather than inventing one.
func TestLowestRegularAccountReportsWhenThereIsNone(t *testing.T) {
	withPasswd(t, "root:x:0:0:root:/root:/bin/bash\n")
	if _, ok := lowestRegularAccount(); ok {
		t.Fatal("found a regular account in a root-only passwd file")
	}
}

func TestLoginShellOfReadsTheAccountsChoice(t *testing.T) {
	withPasswd(t, fixture)
	if got := loginShellOf("deploy"); got != "/bin/zsh" {
		t.Fatalf("loginShellOf(deploy) = %q, want /bin/zsh", got)
	}
	if got := loginShellOf("absent"); got != "" {
		t.Fatalf("loginShellOf(absent) = %q, want empty", got)
	}
}

// The argument vector is the security-relevant part: options have to reach su
// rather than the shell, and nothing may be assembled into a string for a
// shell to re-parse.
func TestLoginArgv(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("argv depends on being root; su is not usable otherwise")
	}
	a := Account{Name: "ubuntu", UID: 1000, GID: 1000, Home: "/home/ubuntu", Shell: "/bin/bash"}

	got := a.loginArgv("", false)
	want := []string{"su", "-l", "ubuntu"}
	if !equal(got, want) {
		t.Fatalf("loginArgv() = %v, want %v", got, want)
	}

	// su hands anything after the username to the shell, so -s must come
	// before it or the shell receives a stray flag instead.
	got = a.loginArgv("/bin/zsh", false)
	want = []string{"su", "-s", "/bin/zsh", "-l", "ubuntu"}
	if !equal(got, want) {
		t.Fatalf("loginArgv(zsh) = %v, want %v", got, want)
	}

	// Already root: there is nobody to become, so the login shell is exec'd
	// directly and -l is what still makes it read the profile.
	root := Account{Name: "root", UID: 0, GID: 0, Home: "/root", Shell: "/bin/bash"}
	got = root.loginArgv("", false)
	want = []string{"/bin/bash", "-l"}
	if !equal(got, want) {
		t.Fatalf("root loginArgv() = %v, want %v", got, want)
	}
}

// "Open a shell here" is the one request `su -l` cannot serve: login means
// chdir-to-home, so it walks away from whatever directory it was started in.
// Setting tmux's -c was not enough for exactly this reason — tmux put the pane
// in the right place and su moved it straight back.
func TestLoginArgvKeepingTheWorkingDirectory(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("argv depends on being root; su is not usable otherwise")
	}
	a := Account{Name: "ubuntu", UID: 1000, GID: 1000, Home: "/home/ubuntu", Shell: "/bin/bash"}

	// No -l on su, so it does not chdir; -l after -- reaches the shell, which
	// is what still reads the profile. Options stay before the username.
	got := a.loginArgv("", true)
	want := []string{"su", "-s", "/bin/bash", "ubuntu", "--", "-l"}
	if !equal(got, want) {
		t.Fatalf("loginArgv(keepCWD) = %v, want %v", got, want)
	}
	for i, arg := range got {
		if arg == "-l" && i < len(got)-1 {
			t.Fatalf("-l reached su rather than the shell, which would chdir to home: %v", got)
		}
	}

	// A configured shell is honoured the same way.
	got = a.loginArgv("/bin/zsh", true)
	want = []string{"su", "-s", "/bin/zsh", "ubuntu", "--", "-l"}
	if !equal(got, want) {
		t.Fatalf("loginArgv(zsh, keepCWD) = %v, want %v", got, want)
	}

	// Root has no su in the way, so a login shell already stays where it was
	// started and the argv is unchanged.
	root := Account{Name: "root", UID: 0, GID: 0, Home: "/root", Shell: "/bin/bash"}
	if got, want := root.loginArgv("", true), []string{"/bin/bash", "-l"}; !equal(got, want) {
		t.Fatalf("root loginArgv(keepCWD) = %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The listing format is parsed rather than merely read: tmux escapes
// non-printable bytes in format output, so an "obvious" 0x1f unit separator
// comes back as the four literal characters \037 and every line collapses into
// a single field — which is how the persisted-session list silently became
// empty while tmux itself had two sessions running.
//
// Fields, in order: name, title, folder, favourite, colour, created,
// attached, windows, path. The path is last so it may contain the separator.
func TestParseTmuxLine(t *testing.T) {
	got, ok := parseTmuxLine("vpsd-abc123|deploy|Shop|1|violet|1787385285|0|2|/srv/app")
	if !ok {
		t.Fatal("a well-formed line must parse")
	}
	if got.Name != "vpsd-abc123" || got.Title != "deploy" || got.Windows != 2 {
		t.Errorf("parsed = %+v", got)
	}
	if got.Folder != "Shop" || !got.Favourite || got.Colour != "violet" {
		t.Errorf("the organising fields decide where this appears: %+v", got)
	}
	if got.CWD != "/srv/app" {
		t.Errorf("cwd = %q", got.CWD)
	}
	if got.Attached {
		t.Error("session_attached of 0 means nothing is attached")
	}
	if got.CreatedAt.IsZero() {
		t.Error("the creation time is what orders the list")
	}

	// Unset options come back empty, which is the ordinary case for a session
	// nobody has organised yet.
	plain, ok := parseTmuxLine("vpsd-abc123|||||1787385285|1|1|/home/ubuntu")
	if !ok || plain.Title != "" || plain.Folder != "" || plain.Favourite || plain.Colour != "" {
		t.Errorf("an unorganised session must parse as one: %+v", plain)
	}
	if !plain.Attached {
		t.Error("a non-zero session_attached means somebody is attached")
	}

	// The path is last precisely so it can contain the separator. A directory
	// with a pipe in its name is unusual and entirely legal.
	odd, ok := parseTmuxLine("vpsd-abc123|||||1787385285|1|1|/srv/we|rd/path")
	if !ok || odd.CWD != "/srv/we|rd/path" {
		t.Errorf("a path containing the separator must survive intact, got %q", odd.CWD)
	}

	// Somebody else's tmux session on the same host is not ours to list, let
	// alone to offer an attach button for.
	if _, ok := parseTmuxLine("my-own-work|x||||1787385285|0|1|/home/me"); ok {
		t.Error("only sessions this dashboard created may be listed")
	}
	if _, ok := parseTmuxLine("vpsd-abc123|truncated"); ok {
		t.Error("a short line must be rejected rather than half-read")
	}
}

// A title is written into a format tmux reads back through a separator, so
// what goes in has to survive the round trip.
func TestSanitiseField(t *testing.T) {
	if got := sanitiseField("build | deploy"); strings.Contains(got, fieldSep) {
		t.Errorf("the separator must not survive into a field: %q", got)
	}
	if got := sanitiseField("two\nlines\tand\x07a bell"); strings.ContainsAny(got, "\n\t\x07") {
		t.Errorf("control characters come back escaped and must be stripped: %q", got)
	}
	if got := sanitiseField("  padded  "); got != "padded" {
		t.Errorf("got %q", got)
	}
	if got := sanitiseField(strings.Repeat("x", 200)); len(got) > 64 {
		t.Errorf("a title has to fit a tab, got %d chars", len(got))
	}
}

// tmux runs a window with no command of its own through `default-command`, and
// falls back to the shell of whoever started the tmux *server* — which is this
// dashboard, as root. So the first window of a session was the operator's
// account, because it was handed the login explicitly, and every window after
// it was a root shell nobody asked for.
func TestDefaultCommandIsTheLogin(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("argv depends on being root; su is not usable otherwise")
	}
	m := &Manager{account: Account{Name: "ubuntu", UID: 1000, GID: 1000, Home: "/home/ubuntu", Shell: "/bin/bash"}}

	got := m.defaultCommand()
	if got != "su -s /bin/bash ubuntu -- -l" {
		t.Fatalf("defaultCommand() = %q", got)
	}
	// It must not chdir to home: a window inherits the directory tmux started
	// it in, and `su -l` would walk straight back out of it.
	if strings.Contains(got, "-l ubuntu") {
		t.Error("-l reached su rather than the shell, which would chdir to home")
	}
}

// tmux hands default-command to `sh -c`, so anything with a space in it has to
// survive as one word. The values are operator configuration rather than
// per-request input, but a shell path or account name containing a space is
// entirely legal and would otherwise be read as two arguments.
func TestShellWordQuoting(t *testing.T) {
	if got := shellWord("/bin/bash"); got != "/bin/bash" {
		t.Errorf("an ordinary path needs no quoting, got %q", got)
	}
	if got := shellWord("/opt/my shell/bash"); got != "'/opt/my shell/bash'" {
		t.Errorf("a path with a space must become one word, got %q", got)
	}
	if got := shellWord(""); got != "''" {
		t.Errorf("an empty argument must stay an argument, got %q", got)
	}
	if got := shellWord("it's"); !strings.HasPrefix(got, "'") || strings.Count(got, "'") < 4 {
		t.Errorf("a quote must be escaped rather than ending the word, got %q", got)
	}
}

// A session opened as a chosen account goes through its own login: su for anyone
// else, its own shell for root, and never the default account's.
func TestAChosenAccountsLoginIsThatAccounts(t *testing.T) {
	m := &Manager{shell: "", shellDir: "", account: Account{Name: "ubuntu", UID: 1000, Shell: "/bin/bash"}}
	target := Account{Name: "zoro", UID: 1001, Shell: "/bin/zsh", Home: "/home/zoro"}
	args := m.startupArgvFor(target, false, "", "")
	if strings.Contains(strings.Join(args, " "), "ubuntu") {
		t.Fatalf("the default account is in %q", args)
	}
	if os.Geteuid() == 0 {
		if !equal(args, []string{"su", "-l", "zoro"}) {
			t.Fatalf("args = %q", args)
		}
	} else if !equal(args, []string{"/bin/zsh", "-l"}) {
		t.Fatalf("args = %q", args)
	}
	root := m.startupArgvFor(Account{Name: "root", UID: 0, Shell: "/bin/bash"}, false, "", "")
	if !equal(root, []string{"/bin/bash", "-l"}) {
		t.Fatalf("root = %q", root)
	}
	env := strings.Join(heldEnv(target, "abc", "/bin/zsh"), "\n")
	for _, want := range []string{"HOME=/home/zoro", "USER=zoro", "LOGNAME=zoro", "SHELL=/bin/zsh"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env = %s", env)
		}
	}
}

func TestAChosenAccountMustExistAndAllowALogin(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	withPasswd(t, me.Username+":x:"+me.Uid+":"+me.Gid+"::"+me.HomeDir+":/usr/sbin/nologin\n")
	if _, err := resolveLoginAccount(me.Username); !errors.Is(err, ErrAccountCannotLogin) {
		t.Fatalf("nologin: %v", err)
	}
	if _, err := resolveLoginAccount("no-such-account-here"); !errors.Is(err, ErrNoSuchAccount) {
		t.Fatalf("unknown: %v", err)
	}
	withPasswd(t, me.Username+":x:"+me.Uid+":"+me.Gid+"::"+me.HomeDir+":/bin/sh\n")
	got, err := resolveLoginAccount(me.Username)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != me.Username || got.Shell != "/bin/sh" {
		t.Fatalf("got %+v", got)
	}
}
