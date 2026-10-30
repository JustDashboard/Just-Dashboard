package linuxusers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// One test per defect the 2026-10-30 security review found, so none can come back
// without something failing. Each reads as the input that used to go wrong.

func sudoFixture(t *testing.T, sudoers string) []SudoRule {
	t.Helper()
	s, _, _ := fixtureHost(t, map[string]string{"sudoers": sudoers})
	return s.sudoRules()
}

func routesOf(rules []SudoRule, u User) []RootRoute { return rootRoutesFor(u, rules) }

func TestSudoReadingsThatHidRoot(t *testing.T) {
	x := User{Username: "x", UID: 1000, GID: 1000, Password: PasswordSet, Groups: []string{"x"}}
	cases := []struct {
		name, sudoers string
		limited       bool
		noPassword    bool
	}{
		{"a later run-as does not take back an earlier one", "x ALL=(ALL) NOPASSWD: ALL, (nobody) /bin/true\n", false, true},
		{"a comment's trailing backslash does not swallow the rule under it", "# old \\\nx ALL=(ALL) NOPASSWD: ALL\n", false, true},
		{"an alias defined below the rule that uses it", "ADM ALL=(ALL) ALL\nUser_Alias ADM = x\n", false, false},
		{"a quoted name", "\"x\" ALL=(ALL) ALL\n", false, false},
		{"an option before ALL", "x ALL=(ALL) CWD=* ALL\n", false, false},
		{"a second host spec", "x ALL=/bin/ls : ALL=(ALL) ALL\n", false, false},
		{"a negated command is not a limit", "x ALL=(ALL) ALL, !/bin/su\n", false, false},
		{"a tag on a later command is not on the first", "x ALL=(ALL) ALL, NOPASSWD: /bin/ls\n", false, false},
		{"PASSWD takes a NOPASSWD back", "x ALL=(ALL) NOPASSWD: /bin/ls, PASSWD: ALL\n", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routes := routesOf(sudoFixture(t, tc.sudoers), x)
			if len(routes) != 1 {
				t.Fatalf("routes = %+v", routes)
			}
			if routes[0].Limited != tc.limited || routes[0].NoPassword != tc.noPassword {
				t.Fatalf("route = %+v", routes[0])
			}
		})
	}
}

func TestDefaultsNotAuthenticateMakesARuleUsable(t *testing.T) {
	x := User{Username: "x", UID: 1000, GID: 1000, Password: PasswordNone, Groups: []string{"x"}}
	routes := routesOf(sudoFixture(t, "Defaults:x !authenticate\nx ALL=(ALL) ALL\n"), x)
	if len(routes) != 1 || !routes[0].Usable || !routes[0].NoPassword {
		t.Fatalf("routes = %+v", routes)
	}
	other := User{Username: "y", UID: 1001, GID: 1001, Password: PasswordNone, Groups: []string{"y"}}
	routes = routesOf(sudoFixture(t, "Defaults:x !authenticate\ny ALL=(ALL) ALL\n"), other)
	if len(routes) != 1 || routes[0].Usable {
		t.Fatalf("another account's Defaults covered this one: %+v", routes)
	}
}

func TestIncludesWithATabAreRead(t *testing.T) {
	s, _, dir := fixtureHost(t, map[string]string{
		"sudoers":      "",
		"sudoers.d/ci": "x ALL=(ALL) ALL\n",
	})
	if err := os.WriteFile(filepath.Join(dir, "sudoers"), []byte("#includedir\t"+filepath.Join(dir, "sudoers.d")+"\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	if rules := s.sudoRules(); len(rules) != 1 {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestEndSessionsRefusesASecondRootAndASharedNumber(t *testing.T) {
	s, fake, _ := fixtureHost(t, map[string]string{
		"passwd": "root:x:0:0::/root:/bin/bash\ntoor:x:0:0::/root:/bin/bash\nann:x:1000:1000::/home/ann:/bin/bash\nbob:x:1000:1000::/home/bob:/bin/bash\n",
	})
	for _, name := range []string{"toor", "ann"} {
		if err := s.EndSessions(context.Background(), name); !errors.Is(err, ErrSharedNumber) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if fake.ran("pkill") || fake.ran("loginctl") {
		t.Fatalf("something was killed: %+v", fake.calls)
	}
}

func TestDeletingASharedNumberRemovesOnlyTheName(t *testing.T) {
	s, fake, _ := fixtureHost(t, map[string]string{
		"passwd": "root:x:0:0::/root:/bin/bash\ntoor:x:0:0::/root:/bin/bash\nann:x:1000:1000::/home/ann:/bin/bash\n",
		"shadow": "root:$y$x:20000:0:99999:7:::\ntoor:!:20000:0:99999:7:::\nann:$y$x:20000:0:99999:7:::\n",
	})
	ctx := context.Background()
	if _, err := s.Delete(ctx, "toor", RemovalOptions{EndSessions: true}); !errors.Is(err, ErrSharedNumber) {
		t.Fatalf("EndSessions: err = %v", err)
	}
	if _, err := s.Delete(ctx, "toor", RemovalOptions{TransferTo: "ann"}); !errors.Is(err, ErrSharedNumber) {
		t.Fatalf("TransferTo: err = %v", err)
	}
	if fake.ran("find") || fake.ran("chown") || fake.ran("pkill") {
		t.Fatalf("something ran against uid 0: %+v", fake.calls)
	}
	if _, err := s.Delete(ctx, "toor", RemovalOptions{Home: HomeKeep}); err != nil {
		t.Fatal(err)
	}
	call := fake.lastCall("userdel")
	if call == nil || !contains(call.args, "--force") || contains(call.args, "--remove") {
		t.Fatalf("userdel = %+v", call)
	}
}

// keepNoProcesses answers ps as an account with nothing running, and lets every
// other tool succeed.
func keepNoProcesses(stdin, name string, args ...string) (string, error) {
	if name == "ps" {
		return "", fmt.Errorf("ps: nothing")
	}
	return "", nil
}

func TestHandoverCannotBeSteeredByTheAccount(t *testing.T) {
	s, fake, _ := fixtureHost(t, map[string]string{
		"passwd": "root:x:0:0::/root:/bin/bash\nann:x:1000:1000::/home/ann:/bin/bash\nbob:x:1001:1001::/home/bob:/bin/bash\n",
		"shadow": "root:$y$x:20000:0:99999:7:::\nann:$y$x:20000:0:99999:7:::\nbob:$y$x:20000:0:99999:7:::\n",
	})
	fake.answer = keepNoProcesses
	if _, err := s.Delete(context.Background(), "ann", RemovalOptions{TransferTo: "bob", EndSessions: true}); err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for _, call := range fake.calls {
		order = append(order, call.name)
	}
	at := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		t.Fatalf("%s never ran: %v", name, order)
		return -1
	}
	if !(at("chage") < at("crontab") && at("crontab") < at("pkill") && at("pkill") < at("find") && at("find") < at("userdel")) {
		t.Fatalf("the account was not closed first: %v", order)
	}
	find := fake.lastCall("find")
	if !contains(find.args, "-execdir") || contains(find.args, "-exec") || !contains(find.args, "--from=1000") {
		t.Fatalf("find = %v", find.args)
	}
	if chown := fake.lastCall("chown"); chown != nil && !contains(chown.args, "--from=1000") {
		t.Fatalf("chown = %v", chown.args)
	}
}

func TestTarIsToldTheNameIsNotAFlag(t *testing.T) {
	s, fake, _ := fixtureHost(t, nil)
	fake.answer = func(stdin, name string, args ...string) (string, error) {
		if name == "stat" {
			return "100", nil
		}
		return "", nil
	}
	if _, err := s.archiveHome(context.Background(), User{Username: "a", Home: t.TempDir() + "/-Tlist"}); err != nil {
		// The home does not exist, which is not archived at all.
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "-Tlist")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.archiveHome(context.Background(), User{Username: "a", Home: home}); err != nil {
		t.Fatal(err)
	}
	tar := fake.lastCall("tar")
	if tar == nil {
		t.Fatal("tar never ran")
	}
	last := len(tar.args) - 1
	if tar.args[last] != "-Tlist" || tar.args[last-1] != "--" {
		t.Fatalf("tar = %v", tar.args)
	}
}

func TestAHomeWithAnotherHomeInsideItOrALinkIsNotRemoved(t *testing.T) {
	users := []User{{Username: "alice", Home: "/srv/app"}, {Username: "bob", Home: "/srv/app/bob"}}
	if !homeIsUnsafe("/srv/app", "alice", users) {
		t.Fatal("a home holding another account's home read as safe")
	}
	if homeIsUnsafe("/srv/app/bob", "bob", users) {
		t.Fatal("the inner home is bob's alone")
	}
	if homeIsUnsafe("/srv/application", "carol", users) {
		t.Fatal("a shared prefix is not nesting")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "home")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if !homeIsUnsafe(link, "alice", nil) {
		t.Fatal("a symlinked home read as safe")
	}
}

func TestRulesByGroupOrNumberGoWithTheAccount(t *testing.T) {
	s, _, _ := fixtureHost(t, map[string]string{
		"passwd":  "root:x:0:0::/root:/bin/bash\nalice:x:1001:1001::/home/alice:/bin/bash\n",
		"group":   "root:x:0:\nalice:x:1001:\n",
		"sudoers": "%alice ALL=(ALL) ALL\n#1001 ALL=(ALL) ALL\n",
	})
	alice, err := s.lookup("alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.removeRulesNaming(context.Background(), *alice); err != nil {
		t.Fatal(err)
	}
	if rules := s.sudoRules(); len(rules) != 0 {
		t.Fatalf("still granted to whoever is next: %+v", rules)
	}
}

func TestAGroupOthersShareIsNotTheAccountsToClear(t *testing.T) {
	s, _, _ := fixtureHost(t, map[string]string{
		"passwd":  "alice:x:1001:1001::/home/alice:/bin/bash\nbob:x:1002:1002::/home/bob:/bin/bash\n",
		"group":   "alice:x:1001:bob\nbob:x:1002:\n",
		"sudoers": "%alice ALL=(ALL) ALL\n",
	})
	alice, _ := s.lookup("alice")
	if err := s.removeRulesNaming(context.Background(), *alice); err != nil {
		t.Fatal(err)
	}
	if rules := s.sudoRules(); len(rules) != 1 {
		t.Fatalf("bob lost a rule he holds through the group: %+v", rules)
	}
}

func TestANormallySpacedRuleIsRewrittenExactly(t *testing.T) {
	got, ok := rewriteRuleWho("alice, bob ALL=(ALL) ALL", []string{"bob"})
	if !ok || got != "bob ALL=(ALL) ALL" {
		t.Fatalf("got %q", got)
	}
}

func TestAnEditedFileIsNotRewrittenByPosition(t *testing.T) {
	s, _, dir := fixtureHost(t, map[string]string{"sudoers": "x ALL=(ALL) ALL\n"})
	rules := s.sudoRules()
	if err := os.WriteFile(filepath.Join(dir, "sudoers"), []byte("somebody ALL=(ALL) ALL\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	if err := s.dropRuleFor(context.Background(), rules[0], "x", []string{"x"}); err == nil {
		t.Fatal("the wrong line was removed")
	}
}

func TestIdenticalLinesAreAllRemoved(t *testing.T) {
	s, _, _ := fixtureHost(t, map[string]string{
		"passwd":  "x:x:1000:1000::/home/x:/bin/bash\n",
		"sudoers": "x ALL=(ALL) ALL\nx ALL=(ALL) ALL\n",
	})
	rules := s.sudoRules()
	if err := s.RemoveSudoRule(context.Background(), "x", rules[0].ID); err != nil {
		t.Fatal(err)
	}
	if rules := s.sudoRules(); len(rules) != 0 {
		t.Fatalf("one stayed and success was reported: %+v", rules)
	}
}

func TestShellsThatInjectAreRefused(t *testing.T) {
	s, _, _ := fixtureHost(t, map[string]string{"shells": "/bin/bash\n"})
	for _, shell := range []string{"/bin/sh\nSKEL=/home/m/skel\nx/nologin", "/bin/sh:x/nologin"} {
		if err := s.validateShell(shell); err == nil {
			t.Fatalf("%q was accepted", shell)
		}
	}
	if isLoginShell("/opt/notfalse") != true {
		t.Fatal("a suffix decided this was no login")
	}
	if err := s.validateShell("/opt/notfalse"); err == nil {
		t.Fatal("a shell not in /etc/shells skipped the check by its ending")
	}
}

func TestForgedLogLinesAreNotHistory(t *testing.T) {
	now := fixedNow
	forged := "2026-10-30T10:59:48+01:00 host sshd[1]: Invalid user sudo: alice : PWD=/ ; USER=root ; COMMAND=/bin/rm from 1.2.3.4"
	if events := parseSudoHistory([]string{forged}, "alice", 10, now); len(events) != 0 {
		t.Fatalf("a stranger's username wrote a sudo event: %+v", events)
	}
	forgedKey := "2026-10-30T10:59:48+01:00 host sshd[1]: Invalid user Accepted publickey for root from 1 port 1 ssh2: E SHA256:abc from 2.3.4.5"
	if uses := keyUses([]string{forgedKey}, "root", now); len(uses) != 0 {
		t.Fatalf("a stranger's username wrote a key's last use: %+v", uses)
	}
	real := "2026-10-30T10:59:48+01:00 host sshd-session[9]: Accepted publickey for root from 1.2.3.4 port 22 ssh2: ED25519 SHA256:abc"
	if uses := keyUses([]string{real}, "root", now); len(uses) != 1 {
		t.Fatal("a real line stopped being read")
	}
	realSudo := "Oct 30 10:59:48 host sudo:   alice : TTY=pts/0 ; PWD=/ ; USER=root ; COMMAND=/bin/ls"
	if events := parseSudoHistory([]string{realSudo}, "alice", 10, now); len(events) != 1 {
		t.Fatal("a real sudo line stopped being read")
	}
}

func TestKeysInOtherFilesCountAndUnreadableIsNotZero(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	u := User{Username: "x", UID: os.Getuid(), Home: home, Shell: "/bin/bash"}
	policy := SSHPolicy{Installed: true, Known: true, KeyAuth: true,
		AuthorizedKeysFiles: []string{".ssh/authorized_keys", ".ssh/authorized_keys2"}}
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ9XhKjzLq2c8t3mN5pQrS7vW1xY4bZ6dE8fG0hI2jK4 other"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys2"), []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, unreadable := otherKeys(policy, u)
	if len(keys) != 1 || unreadable || keys[0].File == "" {
		t.Fatalf("keys = %+v unreadable = %v", keys, unreadable)
	}
	if err := os.Remove(filepath.Join(home, ".ssh", "authorized_keys2")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(home, ".ssh", "authorized_keys2")); err != nil {
		t.Fatal(err)
	}
	if _, unreadable = otherKeys(policy, u); !unreadable {
		t.Fatal("a link read as no keys, which would let the guard close the last way in")
	}
	route := keyRoute(User{KeysUnknown: true}, policy)
	if !route.Allowed || route.Why != WhyKeysUnknown {
		t.Fatalf("route = %+v", route)
	}
}

func TestAuthorizedKeysFileExpansion(t *testing.T) {
	u := User{Username: "ann", UID: 1000, Home: "/home/ann"}
	policy := SSHPolicy{Known: true, AuthorizedKeysFiles: []string{"%h/.ssh/authorized_keys", "/etc/ssh/keys/%u", "none"}}
	got := otherKeyFiles(policy, u)
	if len(got) != 1 || got[0] != "/etc/ssh/keys/ann" {
		t.Fatalf("got %v", got)
	}
}

// adminHost is one administrator whose only way in is the key it has.
func adminHost(t *testing.T) (*Service, *fakeHost) {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	s, fake, _ := fixtureHost(t, map[string]string{
		"passwd":  "root:x:0:0::/root:/usr/sbin/nologin\nadmin:x:1000:1000::" + home + ":/bin/bash\n",
		"group":   "admin:x:1000:\nops:x:1001:\n",
		"shadow":  "root:!:20000:0:99999:7:::\nadmin:!:20000:0:99999:7:::\n",
		"sudoers": "admin ALL=(ALL) NOPASSWD: ALL\n",
	})
	return s, fake
}

func stateWithKeys(t *testing.T, s *Service) *accountState {
	t.Helper()
	st, err := s.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range st.users {
		if st.users[i].Username == "admin" {
			st.users[i].SSHKeys = 2
			st.users[i].SignIn = evaluateSignIn(st.users[i], st.policy)
		}
	}
	return st
}

func TestGuardCoversWhatItUsedToMiss(t *testing.T) {
	s, _ := adminHost(t)
	st := stateWithKeys(t, s)
	if len(wayIn(st.users)) != 1 {
		t.Fatalf("fixture has no way in: %+v", st.users)
	}
	asksPassword, ok := parseSudoRule("admin ALL=(ALL:ALL) ALL", sudoAliases{})
	if !ok {
		t.Fatal("rule did not parse")
	}
	limited, _ := parseSudoRule("admin ALL=(root) /bin/ls", sudoAliases{})
	current := st.rules[0].ID
	newName := "renamed"
	primary := "ops"
	for name, change := range map[string]Change{
		"a replacement rule asking for a password it lacks": {Username: "admin", DropRule: current, AddRule: &asksPassword},
		"a replacement rule narrowed to one command":        {Username: "admin", DropRule: current, AddRule: &limited},
		"a new home without its files":                      {Username: "admin", KeysLost: true},
		"both copies of a key on two lines":                 {Username: "admin", KeysRemoved: 2},
	} {
		if err := judge(st, change); !errors.Is(err, ErrLastWayIn) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	st.policy.AllowUsers = []string{"admin"}
	st.users = refreshSignIn(st)
	if err := judge(st, Change{Username: "admin", NewName: &newName}); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("a rename outside AllowUsers: err = %v", err)
	}
	st.policy.AllowUsers = nil
	st.rules, _ = groupRuleOnly(t)
	st.users = refreshSignIn(st)
	if err := judge(st, Change{Username: "admin", PrimaryGroup: &primary}); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("a main group that carried the rule: err = %v", err)
	}
}

func groupRuleOnly(t *testing.T) ([]SudoRule, error) {
	t.Helper()
	return sudoFixture(t, "%admin ALL=(ALL) NOPASSWD: ALL\n"), nil
}

func refreshSignIn(st *accountState) []User {
	out := make([]User, len(st.users))
	for i, u := range st.users {
		u.RootRoutes = rootRoutesFor(u, st.rules)
		u.SignIn = evaluateSignIn(u, st.policy)
		out[i] = u
	}
	return out
}

func TestAgingThatExpiresTheLastWayInIsRefused(t *testing.T) {
	s, _ := adminHost(t)
	dir := s.etc
	if err := os.WriteFile(filepath.Join(dir, "shadow"), []byte("root:!:20000:0:99999:7:::\nadmin:$y$x:"+strconv.Itoa(s.today()-40)+":0:99999:7:::\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	zero := 0
	err := s.SetAging(context.Background(), "admin", AgingChange{MaxDays: &zero, InactiveDays: &zero})
	if !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("err = %v", err)
	}
	dead := shadowEntry{LastChange: s.today() - 40, Max: 30, Inactive: 5}
	if !dead.passwordDead(s.today()) {
		t.Fatal("past maximum and grace is dead")
	}
	alive := shadowEntry{LastChange: s.today() - 40, Max: 30, Inactive: -1}
	if alive.passwordDead(s.today()) {
		t.Fatal("no grace rule means it never closes")
	}
	if route := evaluateSignIn(User{PasswordDead: true, Shell: "/bin/bash", Password: PasswordSet, SSHKeys: 1},
		SSHPolicy{Installed: true, Known: true, PasswordAuth: true, KeyAuth: true, UsePAM: true}); route.Any() {
		t.Fatalf("a dead account could sign in: %+v", route)
	}
}

func TestPasswordsAreCapped(t *testing.T) {
	if err := validatePassword(strings.Repeat("a", maxPasswordLength+1)); err == nil {
		t.Fatal("a password past chpasswd's line was accepted")
	}
	if err := validatePassword(strings.Repeat("a", maxPasswordLength)); err != nil {
		t.Fatal(err)
	}
}

func TestAHardLinkedKeyFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	ssh := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(secret, filepath.Join(ssh, "authorized_keys")); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Open(ssh)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if _, err := readKeysAt(handle); err == nil {
		t.Fatal("a second name did not stop the read")
	}
}

func TestGitHubRedirectsStayOnGitHub(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ssh-ed25519 AAAAevil attacker")
	}))
	defer elsewhere.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/x.keys", http.StatusFound)
	}))
	defer source.Close()
	previous := githubKeysURL
	githubKeysURL = source.URL + "/%s.keys"
	defer func() { githubKeysURL = previous }()
	if keys, err := FetchGitHubKeys(context.Background(), "octocat"); err == nil {
		t.Fatalf("keys were taken from where it pointed: %v", keys)
	}
}

func TestAlertTextIsClipped(t *testing.T) {
	if got := clipForAlert(strings.Repeat("x", 5000)); len(got) > alertTextCap+4 {
		t.Fatalf("len = %d", len(got))
	}
}
