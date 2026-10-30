package linuxusers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLastlogSeparatesTheTerminalFromTheAddress(t *testing.T) {
	out := parseLastlog(`Username         Port     From                                       Latest
root             pts/1    198.51.100.23                               Fri Oct 23 12:39:03 +0200 2026
daemon                                                              **Never logged in**
ion              pts/0    2001:db8::1                               Sat Oct 24 01:02:03 +0000 2026
local            tty1                                               Sun Oct 25 09:00:00 +0000 2026
bare             203.0.113.9                                        Mon Oct 26 09:00:00 +0000 2026
`)
	if got := out["root"]; got.from != "198.51.100.23" {
		t.Fatalf("root from %q", got.from)
	}
	if got := out["root"].at; !got.Equal(time.Date(2026, 10, 23, 10, 39, 3, 0, time.UTC)) {
		t.Fatalf("root at %v", got)
	}
	if _, ok := out["daemon"]; ok {
		t.Fatal("never logged in is not a sign-in")
	}
	if got := out["ion"]; got.from != "2001:db8::1" {
		t.Fatalf("ion from %q", got.from)
	}
	if got := out["local"]; got.from != "" || got.at.IsZero() {
		t.Fatalf("a console sign-in came from nowhere: %+v", got)
	}
	if got := out["bare"]; got.from != "203.0.113.9" {
		t.Fatalf("bare from %q", got.from)
	}
}

func TestLastlogOrigin(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"pts/1"}, ""},
		{[]string{"tty1"}, ""},
		{[]string{"ssh"}, ""},
		{[]string{"198.51.100.23"}, "198.51.100.23"},
		{[]string{"pts/1", "198.51.100.23"}, "198.51.100.23"},
		{[]string{"pts/1", "host.example"}, "host.example"},
	} {
		if got := lastlogOrigin(c.in); got != c.want {
			t.Errorf("%v = %q, want %q", c.in, got, c.want)
		}
	}
}

func hostAccounts(t *testing.T) (*Service, *fakeHost, string) {
	t.Helper()
	dir := t.TempDir()
	s, fake, etc := fixtureHost(t, map[string]string{
		"passwd": "root:x:0:0:root:" + dir + "/root:/bin/bash\n" +
			"daemon:x:1:1:daemon:" + dir + "/daemon:/usr/sbin/nologin\n" +
			"nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin\n" +
			"ubuntu:x:1000:1000:Ubuntu:" + dir + "/ubuntu:/bin/bash\n" +
			"zoro:x:1001:1001::" + dir + "/zoro:/bin/bash\n",
		"group": "root:x:0:\ndaemon:x:1:\nsudo:x:27:ubuntu,zoro\nlxd:x:101:ubuntu\nubuntu:x:1000:\nzoro:x:1001:\nnogroup:x:65534:\n",
		"shadow": "root:$y$j9T$a$b:20000:0:99999:7:::\ndaemon:*:20000:0:99999:7:::\nnobody:*:20000:0:99999:7:::\n" +
			"ubuntu:!:20000:0:99999:7:::\nzoro:!:20000:0:99999:7::1:\n",
		"shells":                        "# /etc/shells\n/bin/sh\n/bin/bash\n/usr/bin/zsh\n",
		"sudoers":                       "root ALL=(ALL:ALL) ALL\n%sudo ALL=(ALL:ALL) ALL\n@includedir sudoers.d\n",
		"sudoers.d/90-cloud-init-users": "ubuntu ALL=(ALL) NOPASSWD:ALL\n",
	})
	fake.answer = func(_, name string, _ ...string) (string, error) {
		switch {
		case strings.HasSuffix(name, "sshd"):
			return "permitrootlogin yes\npasswordauthentication yes\npubkeyauthentication yes\nusepam yes\n", nil
		case name == "lastlog":
			return "Username Port From Latest\nroot pts/1 198.51.100.23 Fri Oct 23 12:39:03 +0200 2026\n", nil
		}
		return "", nil
	}
	return s, fake, etc
}

func TestReadingTheHostGivesTheTruthAboutEachAccount(t *testing.T) {
	s, _, _ := hostAccounts(t)
	users, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]User{}
	for _, u := range users {
		byName[u.Username] = u
	}
	if users[0].Username != "ubuntu" || users[1].Username != "zoro" {
		t.Fatalf("people come first: %v, %v", users[0].Username, users[1].Username)
	}
	root := byName["root"]
	if !root.System || !root.Protected || root.Password != PasswordSet || !root.SignIn.Password.Allowed ||
		root.LastFrom != "198.51.100.23" || len(root.RootRoutes) != 1 || root.RootRoutes[0].Kind != "uid0" {
		t.Fatalf("root = %+v", root)
	}
	ubuntu := byName["ubuntu"]
	if ubuntu.System || ubuntu.Password != PasswordNone || ubuntu.SignIn.Any() {
		t.Fatalf("ubuntu = %+v", ubuntu)
	}
	if ubuntu.PrimaryGroup != "ubuntu" || !contains(ubuntu.Groups, "lxd") || !contains(ubuntu.Groups, "ubuntu") {
		t.Fatalf("ubuntu's groups = %+v", ubuntu)
	}
	hasCloud, hasLXD := false, false
	for _, route := range ubuntu.RootRoutes {
		hasCloud = hasCloud || (route.NoPassword && route.Usable)
		hasLXD = hasLXD || route.Via == "lxd"
	}
	if !hasCloud || !hasLXD {
		t.Fatalf("ubuntu's routes = %+v", ubuntu.RootRoutes)
	}
	zoro := byName["zoro"]
	if !zoro.Disabled || zoro.ExpiresOn != "1970-01-02" || zoro.SignIn.Password.Why != WhyDisabled {
		t.Fatalf("zoro = %+v", zoro)
	}
	if nobody := byName["nobody"]; !nobody.System || !nobody.Protected || nobody.CanLogin {
		t.Fatalf("nobody = %+v", nobody)
	}
}

func TestAnUnreadableShadowFileIsUnknownAndNotAnAnswer(t *testing.T) {
	s, _, etc := hostAccounts(t)
	if err := os.Remove(filepath.Join(etc, "shadow")); err != nil {
		t.Fatal(err)
	}
	users, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.Password != PasswordUnknown {
			t.Fatalf("%s = %q", u.Username, u.Password)
		}
	}
}

func TestGetAsksSSHDAboutThatAccountByName(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	if _, err := s.Get(context.Background(), "ubuntu"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, call := range fake.calls {
		if strings.HasSuffix(call.name, "sshd") && strings.Contains(strings.Join(call.args, " "), "-C user=ubuntu,host=localhost,addr=127.0.0.1") {
			found = true
		}
	}
	if !found {
		t.Fatal("no -C user=ubuntu")
	}
	if _, err := s.Get(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestAMatchBlockThatDeniesPasswordsIsInTheOneAccountsAnswer(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	inner := fake.answer
	fake.answer = func(stdin, name string, args ...string) (string, error) {
		if strings.HasSuffix(name, "sshd") && len(args) > 1 {
			return "permitrootlogin yes\npasswordauthentication no\npubkeyauthentication yes\nusepam yes\n", nil
		}
		return inner(stdin, name, args...)
	}
	u, err := s.Get(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	if u.SignIn.Password.Why != WhyPasswordAuthOff {
		t.Fatalf("root = %+v", u.SignIn)
	}
}

func TestSSHDMissingAndSSHDRefusingAreDifferentAnswers(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	fake.answer = func(_, name string, _ ...string) (string, error) {
		return "", errorsJoin(ErrToolMissing, name)
	}
	if got := s.sshPolicy(context.Background(), ""); got.Installed {
		t.Fatalf("got %+v", got)
	}
	fake.answer = func(_, name string, _ ...string) (string, error) {
		return "", errors.New("sshd: Bad configuration options")
	}
	if got := s.sshPolicy(context.Background(), ""); !got.Installed || got.Known {
		t.Fatalf("got %+v", got)
	}
}

func TestAnSSHDThatHasNotStartedYetIsAskedAgain(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	tries := 0
	fake.answer = func(_, name string, args ...string) (string, error) {
		if strings.HasSuffix(name, "sshd") {
			tries++
			if tries == 1 {
				return "", errors.New("sshd: Missing privilege separation directory: /run/sshd")
			}
			return "passwordauthentication no\n", nil
		}
		return "", nil
	}
	got := s.sshPolicy(context.Background(), "")
	if !got.Known || got.PasswordAuth {
		t.Fatalf("got %+v", got)
	}
	if call := fake.lastCall("mkdir"); call == nil || call.args[len(call.args)-1] != "/run/sshd" {
		t.Fatalf("mkdir = %+v", call)
	}
}

func errorsJoin(err error, name string) error {
	return errors.Join(err, errors.New(name))
}

func TestAShellHasToBeOneTheHostKnows(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	ctx := context.Background()
	if err := s.SetShell(ctx, "ubuntu", "/bin/bahs"); err == nil {
		t.Fatal("a typo was accepted")
	}
	if err := s.SetShell(ctx, "ubuntu", "bash"); err == nil {
		t.Fatal("a relative shell was accepted")
	}
	if fake.ran("usermod") {
		t.Fatal("usermod was asked")
	}
	if err := s.SetShell(ctx, "ubuntu", "/usr/bin/zsh"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetShell(ctx, "ubuntu", "/usr/sbin/nologin"); err != nil {
		t.Fatal(err)
	}
	// Root keeps a shell it can log in with, and the host's last way in is root.
	if err := s.SetShell(ctx, "root", "/usr/sbin/nologin"); !errors.Is(err, ErrProtected) {
		t.Fatalf("root: %v", err)
	}
	// Only the shell's own word for it counts, so a list that is missing altogether
	// has nothing to judge against.
	s, _, etc := hostAccounts(t)
	if err := os.Remove(filepath.Join(etc, "shells")); err != nil {
		t.Fatal(err)
	}
	if err := s.validateShell("/opt/custom/sh"); err != nil {
		t.Fatal(err)
	}
}

func TestAnAccountsMainGroupIsNotListedAsAMembership(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	if err := s.SetGroups(context.Background(), "ubuntu", []string{"ubuntu", "lxd", "sudo"}); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("usermod"); strings.Join(call.args, " ") != "--groups lxd,sudo ubuntu" {
		t.Fatalf("usermod = %+v", call)
	}
}

func TestShellsReadsTheHostsListWithoutComments(t *testing.T) {
	s, _, _ := hostAccounts(t)
	if got := strings.Join(s.Shells(), " "); got != "/bin/sh /bin/bash /usr/bin/zsh" {
		t.Fatalf("got %q", got)
	}
}

func TestCreateBuildsItsArgumentsAndLocksTheResult(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	ctx := context.Background()
	err := s.Create(ctx, CreateOptions{Username: "mira", Comment: "Mira K", Shell: "/bin/bash", Groups: []string{"sudo", "lxd"}})
	if err != nil {
		t.Fatal(err)
	}
	add := fake.lastCall("useradd")
	if add == nil || strings.Join(add.args, " ") != "--create-home --shell /bin/bash --comment Mira K --groups sudo,lxd mira" {
		t.Fatalf("useradd = %+v", add)
	}
	if lock := fake.lastCall("usermod"); lock == nil || strings.Join(lock.args, " ") != "--lock mira" {
		t.Fatalf("usermod = %+v", lock)
	}
	if err := s.Create(ctx, CreateOptions{Username: "svc", System: true, NoHome: true, Shell: "/usr/sbin/nologin"}); err != nil {
		t.Fatal(err)
	}
	if add := fake.lastCall("useradd"); strings.Join(add.args, " ") != "--no-create-home --system --shell /usr/sbin/nologin svc" {
		t.Fatalf("useradd = %+v", add)
	}
	for _, bad := range []CreateOptions{
		{Username: "Bad Name"},
		{Username: "-rf"},
		{Username: "ok", Comment: "has:colon"},
		{Username: "ok", Comment: "line\nbreak"},
		{Username: "ok", Shell: "bash"},
		{Username: "ok", Groups: []string{"not a group"}},
	} {
		if err := s.Create(ctx, bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}

func TestAKeyListSurvivesAnAccountWithNoHome(t *testing.T) {
	s, _, _ := hostAccounts(t)
	keys, path, err := s.ListKeys("zoro")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 || !strings.HasSuffix(path, "/zoro/.ssh/authorized_keys") {
		t.Fatalf("keys = %v, path = %q", keys, path)
	}
	if _, _, err := s.ListKeys("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestLookupNeedsOnlyThePasswdFile(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	u, err := s.Lookup("daemon")
	if err != nil {
		t.Fatal(err)
	}
	if !u.System || u.UID != 1 {
		t.Fatalf("u = %+v", u)
	}
	if u, _ := s.Lookup("ubuntu"); u.System {
		t.Fatal("ubuntu is a person")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("ran %+v", fake.calls)
	}
	if _, err := s.Lookup("../etc"); !errors.Is(err, ErrInvalidUser) {
		t.Fatalf("err = %v", err)
	}
}

func TestProtectedAccountsKeepTheirNameNumberAndHome(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	ctx := context.Background()
	for name, change := range map[string]ProfileChange{
		"rename": {NewName: stringPtr("toor")},
		"uid":    {UID: intPtr(1500)},
		"home":   {Home: stringPtr("/home/root")},
		"group":  {PrimaryGroup: stringPtr("sudo")},
	} {
		if _, err := s.UpdateProfile(ctx, "root", change); !errors.Is(err, ErrProtected) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if fake.ran("usermod") {
		t.Fatal("usermod was asked")
	}
	if _, err := s.UpdateProfile(ctx, "root", ProfileChange{Comment: stringPtr("Superuser")}); err != nil {
		t.Fatal(err)
	}
}

func TestAProfileChangeRefusesASecondRoot(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	ctx := context.Background()
	if _, err := s.UpdateProfile(ctx, "ubuntu", ProfileChange{UID: intPtr(0)}); err == nil {
		t.Fatal("uid 0 was accepted")
	}
	if _, err := s.UpdateProfile(ctx, "ubuntu", ProfileChange{Home: stringPtr("/")}); err == nil {
		t.Fatal("a home of / was accepted")
	}
	if _, err := s.UpdateProfile(ctx, "ubuntu", ProfileChange{Home: stringPtr("relative")}); err == nil {
		t.Fatal("a relative home was accepted")
	}
	if _, err := s.UpdateProfile(ctx, "ubuntu", ProfileChange{Comment: stringPtr("a:b")}); err == nil {
		t.Fatal("a colon was accepted")
	}
	if fake.ran("usermod") {
		t.Fatal("usermod was asked")
	}
	if _, err := s.UpdateProfile(ctx, "ubuntu", ProfileChange{
		Comment: stringPtr("U"), Home: stringPtr("/data/ubuntu/"), MoveHome: true, UID: intPtr(2000), PrimaryGroup: stringPtr("lxd"),
	}); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("usermod"); strings.Join(call.args, " ") != "--comment U --gid lxd --home /data/ubuntu --move-home --uid 2000 ubuntu" {
		t.Fatalf("usermod = %+v", call)
	}
}

func TestRenamingMovesTheCrontabAndTheManagedRuleAndTheGroup(t *testing.T) {
	s, fake, etc := hostAccounts(t)
	ctx := context.Background()
	managed := filepath.Join(etc, "sudoers.d", "jd-zoro")
	if err := os.WriteFile(managed, []byte("zoro ALL=(ALL:ALL) ALL\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	inner := fake.answer
	fake.answer = func(stdin, name string, args ...string) (string, error) {
		switch name {
		case "crontab":
			if args[0] == "-l" {
				return "0 1 * * * /bin/true\n", nil
			}
		case "usermod":
			b, _ := os.ReadFile(filepath.Join(etc, "passwd"))
			return "", os.WriteFile(filepath.Join(etc, "passwd"), []byte(strings.ReplaceAll(string(b), "zoro:x:", "mira:x:")), 0o644)
		}
		return inner(stdin, name, args...)
	}
	final, err := s.UpdateProfile(ctx, "zoro", ProfileChange{NewName: stringPtr("mira")})
	if err != nil {
		t.Fatal(err)
	}
	if final != "mira" {
		t.Fatalf("final = %q", final)
	}
	if call := fake.lastCall("groupmod"); call == nil || strings.Join(call.args, " ") != "--new-name mira zoro" {
		t.Fatalf("groupmod = %+v", call)
	}
	if call := fake.lastCall("crontab"); call == nil || call.stdin != "0 1 * * * /bin/true\n" || strings.Join(call.args, " ") != "-u mira -" {
		t.Fatalf("crontab = %+v", call)
	}
	if _, err := os.Stat(managed); err == nil {
		t.Fatal("the old rule is still live")
	}
	b, err := os.ReadFile(filepath.Join(etc, "sudoers.d", "jd-mira"))
	if err != nil || !strings.Contains(string(b), "mira ALL=(ALL:ALL) ALL") {
		t.Fatalf("new rule = %q, %v", b, err)
	}
}

func TestRenamingIsRefusedWhileSomebodyElsesFileNamesTheAccount(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	// Left behind, cloud-init's rule would hand root to the next account called
	// ubuntu.
	if _, err := s.UpdateProfile(context.Background(), "ubuntu", ProfileChange{NewName: stringPtr("mira")}); err == nil ||
		!strings.Contains(err.Error(), "90-cloud-init-users") {
		t.Fatalf("err = %v", err)
	}
	if fake.ran("usermod") {
		t.Fatal("usermod was asked")
	}
	if _, err := s.UpdateProfile(context.Background(), "zoro", ProfileChange{NewName: stringPtr("ubuntu")}); err == nil {
		t.Fatal("renamed onto an existing account")
	}
}
