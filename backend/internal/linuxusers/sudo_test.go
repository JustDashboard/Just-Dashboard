package linuxusers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sudoersTree is a host's sudo configuration with one of each thing the reader
// has to get right.
func sudoersTree() map[string]string {
	return map[string]string{
		"sudoers": `# The stock file.
Defaults	env_reset
Defaults:ion	!lecture
root	ALL=(ALL:ALL) ALL
%admin ALL=(ALL) ALL
%sudo	ALL=(ALL:ALL) ALL
User_Alias OPS = mira, %contractors : DEPLOY = deploy
Cmnd_Alias SERVICES = /usr/bin/systemctl restart nginx, \
                      /usr/bin/systemctl reload nginx
Runas_Alias DB = postgres, mysql
OPS ALL=(root) SERVICES
DEPLOY ALL=(ALL) NOPASSWD: /usr/local/bin/release
webadmin ALL=(www-data) ALL
dbadmin ALL=(DB) ALL
nearly ALL=(ALL) ALL, !/bin/su
#1000 ALL=(ALL) NOPASSWD: ALL
multi, other ALL=(root, www-data) /bin/ls, /bin/cat
@includedir sudoers.d
#includedir /etc/sudoers.alt
`,
		"sudoers.d/90-cloud-init-users": "# Created by cloud-init\nubuntu ALL=(ALL) NOPASSWD:ALL\n",
		"sudoers.d/jd-ion":              "# Written by Just Dashboard\nion ALL=(ALL:ALL) ALL\n",
		"sudoers.d/ignored.bak":         "ignored ALL=(ALL) ALL\n",
		"sudoers.d/ignored~":            "tilde ALL=(ALL) ALL\n",
		"sudoers.alt/legacy":            "legacy ALL=(ALL) ALL\n",
	}
}

func ruleNaming(t *testing.T, rules []SudoRule, who string) SudoRule {
	t.Helper()
	for _, rule := range rules {
		if contains(rule.Who, who) {
			return rule
		}
	}
	t.Fatalf("no rule names %s in %+v", who, rules)
	return SudoRule{}
}

func TestSudoersTreeIsReadTheWaySudoReadsIt(t *testing.T) {
	s, _, _ := fixtureHost(t, sudoersTree())
	rules := s.sudoRules()
	if rules == nil {
		t.Fatal("the tree is there")
	}

	group := ruleNaming(t, rules, "%sudo")
	if !group.Full || group.NoPassword || group.RunAs != "ALL:ALL" {
		t.Fatalf("%%sudo = %+v", group)
	}
	cloud := ruleNaming(t, rules, "ubuntu")
	if !cloud.Full || !cloud.NoPassword || !strings.HasSuffix(cloud.File, "90-cloud-init-users") {
		t.Fatalf("ubuntu = %+v", cloud)
	}
	if cloud.Managed {
		t.Fatal("cloud-init's file is not the dashboard's")
	}
	if managed := ruleNaming(t, rules, "ion"); !managed.Managed || managed.Line != 2 {
		t.Fatalf("ion = %+v", managed)
	}

	// A file with a dot in it, or ending in a tilde, is one sudo does not read.
	for _, rule := range rules {
		if contains(rule.Who, "ignored") || contains(rule.Who, "tilde") {
			t.Fatalf("read a file sudo skips: %+v", rule)
		}
	}
	// The older "#includedir" spelling is a directive and not a comment.
	if legacy := ruleNaming(t, rules, "legacy"); !strings.Contains(legacy.File, "sudoers.alt") {
		t.Fatalf("legacy = %+v", legacy)
	}
}

func TestAliasesAndContinuationsExpand(t *testing.T) {
	s, _, _ := fixtureHost(t, sudoersTree())
	rules := s.sudoRules()
	ops := ruleNaming(t, rules, "mira")
	if !contains(ops.Who, "%contractors") {
		t.Fatalf("who = %v", ops.Who)
	}
	if len(ops.Commands) != 2 || ops.Commands[1] != "/usr/bin/systemctl reload nginx" {
		t.Fatalf("a continued alias lost its second command: %v", ops.Commands)
	}
	if ops.Full || !ops.AsRoot {
		t.Fatalf("ops = %+v", ops)
	}
	deploy := ruleNaming(t, rules, "deploy")
	if !deploy.NoPassword || deploy.Full || !deploy.AsRoot {
		t.Fatalf("deploy = %+v", deploy)
	}
}

func TestARuleThatDoesNotReachRootIsNotARouteToIt(t *testing.T) {
	s, _, _ := fixtureHost(t, sudoersTree())
	rules := s.sudoRules()
	if rule := ruleNaming(t, rules, "webadmin"); rule.AsRoot || rule.Full {
		t.Fatalf("webadmin = %+v", rule)
	}
	if rule := ruleNaming(t, rules, "dbadmin"); rule.AsRoot {
		t.Fatalf("a runas alias was not expanded: %+v", rule)
	}
	web := User{Username: "webadmin", UID: 1005, Password: PasswordSet}
	if routes := rootRoutesFor(web, rules); len(routes) != 0 {
		t.Fatalf("routes = %+v", routes)
	}
}

// sudo's own manual says a negated command is no protection: whoever may run ALL can
// copy the binary and run the copy. Reading such a rule as limited would understate
// root, which is the one mistake this reading must not make.
func TestANegatedCommandStillCountsAsEverything(t *testing.T) {
	s, _, _ := fixtureHost(t, sudoersTree())
	rule := ruleNaming(t, s.sudoRules(), "nearly")
	if !rule.Full || !rule.AsRoot {
		t.Fatalf("nearly = %+v", rule)
	}
	routes := rootRoutesFor(User{Username: "nearly", UID: 1006, Password: PasswordSet}, []SudoRule{rule})
	if len(routes) != 1 || routes[0].Limited {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestAUidAtTheStartOfALineIsARuleAndNotAComment(t *testing.T) {
	s, _, _ := fixtureHost(t, sudoersTree())
	rules := s.sudoRules()
	rule := ruleNaming(t, rules, "#1000")
	if !rule.Full || !rule.NoPassword {
		t.Fatalf("rule = %+v", rule)
	}
	ion := User{Username: "ion", UID: 1000, Password: PasswordNone}
	found := false
	for _, route := range rootRoutesFor(ion, []SudoRule{rule}) {
		found = found || (route.NoPassword && route.Usable)
	}
	if !found {
		t.Fatal("#1000 should apply to the account numbered 1000")
	}
}

func TestACommaInsideARunasListDoesNotSplitTheCommands(t *testing.T) {
	s, _, _ := fixtureHost(t, sudoersTree())
	rule := ruleNaming(t, s.sudoRules(), "multi")
	if !contains(rule.Who, "other") {
		t.Fatalf("who = %v", rule.Who)
	}
	if rule.RunAs != "root, www-data" || !rule.AsRoot {
		t.Fatalf("runas = %q", rule.RunAs)
	}
	if strings.Join(rule.Commands, "|") != "/bin/ls|/bin/cat" {
		t.Fatalf("commands = %v", rule.Commands)
	}
}

func TestNoSudoersFileIsAHostWithoutSudoAndAnUnreadableOneIsBlind(t *testing.T) {
	s, _, dir := fixtureHost(t, map[string]string{})
	rules := s.sudoRules()
	if rules == nil || len(rules) != 0 {
		t.Fatalf("rules = %+v", rules)
	}
	// Reading through a directory fails the way an unreadable file does, and works
	// for root too, which the mode bits would not.
	if err := os.Mkdir(filepath.Join(dir, "sudoers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if rules := s.sudoRules(); rules != nil {
		t.Fatalf("rules = %+v", rules)
	}
	routes := rootRoutesFor(User{Username: "ion", UID: 1000, Groups: []string{"sudo"}, Password: PasswordSet}, nil)
	if len(routes) != 1 || routes[0].Kind != "sudo" || routes[0].Via != "sudo" {
		t.Fatalf("a blind reading should fall back to the conventional names: %+v", routes)
	}
}

func TestAnIncludeLoopEnds(t *testing.T) {
	s, _, _ := fixtureHost(t, map[string]string{
		"sudoers": "@include other\nion ALL=(ALL) ALL\n",
		"other":   "@include sudoers\nmira ALL=(ALL) ALL\n",
	})
	rules := s.sudoRules()
	if len(rules) != 2 {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestRootRoutes(t *testing.T) {
	s, _, _ := fixtureHost(t, sudoersTree())
	rules := s.sudoRules()

	root := rootRoutesFor(User{Username: "root", UID: 0, Password: PasswordSet}, rules)
	if len(root) != 1 || root[0].Kind != "uid0" || !root[0].Usable {
		t.Fatalf("root = %+v", root)
	}

	// ubuntu in the stock configuration: no password, cloud-init's rule, lxd.
	ubuntu := rootRoutesFor(User{
		Username: "ubuntu", UID: 1001, Password: PasswordNone,
		Groups: []string{"ubuntu", "sudo", "lxd"},
	}, rules)
	byKind := map[string][]RootRoute{}
	for _, route := range ubuntu {
		byKind[route.Kind] = append(byKind[route.Kind], route)
	}
	if len(byKind["group"]) != 1 || byKind["group"][0].Via != "lxd" || !byKind["group"][0].Usable {
		t.Fatalf("lxd = %+v", byKind["group"])
	}
	usable := map[bool]int{}
	for _, route := range byKind["sudo"] {
		usable[route.Usable]++
		if route.Via == "sudo" && route.Usable {
			t.Fatal("the group's rule asks for a password ubuntu does not have")
		}
		if route.Via == "" && (!route.NoPassword || !route.Usable) {
			t.Fatalf("cloud-init's rule = %+v", route)
		}
	}
	if usable[true] != 1 || usable[false] != 1 {
		t.Fatalf("sudo routes = %+v", byKind["sudo"])
	}

	// zoro: in sudo, with no password. It reads like root and cannot be used.
	zoro := rootRoutesFor(User{Username: "zoro", UID: 1002, Password: PasswordNone, Groups: []string{"sudo"}}, rules)
	if len(zoro) != 1 || zoro[0].Usable {
		t.Fatalf("zoro = %+v", zoro)
	}

	// A locked password is no more use to sudo than none at all.
	locked := rootRoutesFor(User{Username: "lk", UID: 1003, Password: PasswordLocked, Groups: []string{"sudo"}}, rules)
	if len(locked) != 1 || locked[0].Usable {
		t.Fatalf("locked = %+v", locked)
	}
	for _, group := range []string{"docker", "lxd", "lxc", "libvirt", "disk"} {
		routes := rootRoutesFor(User{Username: "x", UID: 1004, Password: PasswordNone, Groups: []string{group}}, nil)
		if len(routes) != 1 || routes[0].Kind != "group" || routes[0].Via != group || !routes[0].Usable {
			t.Fatalf("%s = %+v", group, routes)
		}
	}
}

func TestANegatedUserIsTakenBackOut(t *testing.T) {
	rule, ok := parseSudoRule("ALL, !ion ALL=(ALL) ALL", sudoAliases{users: map[string][]string{}, commands: map[string][]string{}, runas: map[string][]string{}})
	if !ok {
		t.Fatal("did not parse")
	}
	if applies, _ := ruleCarrier(rule, User{Username: "ion", UID: 1000}); applies {
		t.Fatal("!ion should not apply to ion")
	}
	if applies, _ := ruleCarrier(rule, User{Username: "mira", UID: 1001}); !applies {
		t.Fatal("ALL should apply to mira")
	}
}

func TestSudoGrantLines(t *testing.T) {
	cases := []struct {
		grant SudoGrant
		want  string
	}{
		{SudoGrant{Level: SudoPassword}, "ion ALL=(ALL:ALL) ALL"},
		{SudoGrant{Level: SudoNoPassword}, "ion ALL=(ALL:ALL) NOPASSWD: ALL"},
		{SudoGrant{Level: SudoPassword, Commands: []string{"/usr/bin/systemctl restart nginx"}}, "ion ALL=(root) /usr/bin/systemctl restart nginx"},
		{SudoGrant{Level: SudoNoPassword, Commands: []string{"/bin/ls", "/bin/cat -n"}}, "ion ALL=(root) NOPASSWD: /bin/ls, /bin/cat -n"},
		{SudoGrant{Level: SudoPassword, Commands: []string{"/usr/bin/env A=B"}}, `ion ALL=(root) /usr/bin/env A\=B`},
	}
	for _, c := range cases {
		got, err := c.grant.line("ion")
		if err != nil || got != c.want {
			t.Errorf("%+v: %q, %v", c.grant, got, err)
		}
	}
	for _, bad := range []string{"ls", "/usr/bin/foo@bar", "/usr/bin/echo a@b", "/bin/ls, ALL", "/bin/ls\nroot ALL=(ALL) ALL", "/bin/ls : ALL", "/bin/ls;id", "/bin/`id`"} {
		if _, err := (SudoGrant{Level: SudoPassword, Commands: []string{bad}}).line("ion"); err == nil {
			t.Errorf("%q was accepted as a command", bad)
		}
	}
	if _, err := (SudoGrant{Level: "root"}).line("ion"); err == nil {
		t.Error("an unknown level was accepted")
	}
}

// visudoPasses answers like a visudo that accepts everything it is given.
func visudoRecorder(fake *fakeHost, refuse func(args []string) error) {
	fake.answer = func(_, name string, args ...string) (string, error) {
		if name == "visudo" && refuse != nil {
			return "", refuse(args)
		}
		return "", nil
	}
}

func sudoHost(t *testing.T) (*Service, *fakeHost, string) {
	t.Helper()
	dir := t.TempDir()
	files := sudoersTree()
	files["passwd"] = "root:x:0:0:root:" + dir + "/root:/bin/bash\n" +
		"ion:x:1000:1000::" + dir + "/ion:/bin/bash\n" +
		"ubuntu:x:1001:1001::" + dir + "/ubuntu:/bin/bash\n"
	files["group"] = "root:x:0:\nion:x:1000:\nubuntu:x:1001:\nsudo:x:27:\n"
	files["shadow"] = "root:$y$x:20000:0:99999:7:::\nion:!:20000:0:99999:7:::\nubuntu:!:20000:0:99999:7:::\n"
	return fixtureHost(t, files)
}

func TestAGrantIsCheckedBeforeItCanTakeEffect(t *testing.T) {
	s, fake, dir := sudoHost(t)
	path := filepath.Join(dir, "sudoers.d", "jd-new")
	var checked []string
	visudoRecorder(fake, func(args []string) error {
		checked = append(checked, strings.Join(args, " "))
		if len(args) == 3 {
			// The file visudo is asked about must be one sudo itself would skip.
			if base := filepath.Base(args[2]); !strings.Contains(base, ".") {
				t.Fatalf("checked %q, which sudo would read", base)
			}
			if _, err := os.Stat(path); err == nil {
				t.Fatal("the rule was live before it was checked")
			}
		}
		return nil
	})
	if err := s.SetSudoGrant(context.Background(), "new", SudoGrant{Level: SudoNoPassword}); err != nil {
		t.Fatal(err)
	}
	if len(checked) != 2 || checked[1] != "--check" {
		t.Fatalf("checked = %v", checked)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "new ALL=(ALL:ALL) NOPASSWD: ALL\n") {
		t.Fatalf("file = %q", b)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o440 {
		t.Fatalf("mode = %v", info.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".jd-") {
			t.Fatalf("left %s behind", entry.Name())
		}
	}
	if got := s.ManagedGrant("new"); got.Level != SudoNoPassword || len(got.Commands) != 0 {
		t.Fatalf("read back = %+v", got)
	}
}

func TestARefusedGrantLeavesNothingBehind(t *testing.T) {
	s, fake, dir := sudoHost(t)
	visudoRecorder(fake, func(args []string) error { return fmt.Errorf("visudo: parse error") })
	err := s.SetSudoGrant(context.Background(), "new", SudoGrant{Level: SudoPassword})
	if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sudoers.d", "jd-new")); err == nil {
		t.Fatal("the refused rule is live")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "sudoers.d"))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".jd-") {
			t.Fatalf("left %s behind", entry.Name())
		}
	}
}

func TestAGrantThatBreaksTheWholeConfigurationIsTakenBack(t *testing.T) {
	s, fake, dir := sudoHost(t)
	path := filepath.Join(dir, "sudoers.d", "jd-ion")
	before, _ := os.ReadFile(path)
	visudoRecorder(fake, func(args []string) error {
		if len(args) == 1 {
			return fmt.Errorf("visudo: duplicate alias")
		}
		return nil
	})
	if err := s.SetSudoGrant(context.Background(), "ion", SudoGrant{Level: SudoNoPassword}); err == nil {
		t.Fatal("accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatalf("the old rule was not restored: %q", after)
	}
	// And a new file goes away altogether.
	if err := s.SetSudoGrant(context.Background(), "fresh", SudoGrant{Level: SudoNoPassword}); err == nil {
		t.Fatal("accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "sudoers.d", "jd-fresh")); err == nil {
		t.Fatal("the new rule stayed")
	}
}

func TestAHostWithoutVisudoSaysSudoIsNotInstalled(t *testing.T) {
	s, fake, _ := sudoHost(t)
	visudoRecorder(fake, func(args []string) error { return fmt.Errorf("%w: visudo", ErrToolMissing) })
	err := s.SetSudoGrant(context.Background(), "new", SudoGrant{Level: SudoPassword})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestGrantingNoneRemovesTheManagedFile(t *testing.T) {
	s, _, dir := sudoHost(t)
	path := filepath.Join(dir, "sudoers.d", "jd-ion")
	if err := s.SetSudoGrant(context.Background(), "ion", SudoGrant{Level: SudoNone}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("still there")
	}
	// And granting none where there was nothing is no error.
	if err := s.SetSudoGrant(context.Background(), "mira", SudoGrant{Level: SudoNone}); err != nil {
		t.Fatal(err)
	}
}

func TestRemovingARuleRewritesTheFileWithoutIt(t *testing.T) {
	s, _, dir := sudoHost(t)
	rule := ruleNaming(t, s.sudoRules(), "ubuntu")
	if err := s.RemoveSudoRule(context.Background(), "ubuntu", rule.ID); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "sudoers.d", "90-cloud-init-users"))
	if strings.Contains(string(b), "NOPASSWD") {
		t.Fatalf("file = %q", b)
	}
	if !strings.Contains(string(b), "# Created by cloud-init") {
		t.Fatal("the rest of the file was lost")
	}
}

func TestRemovingOnePersonFromASharedRuleKeepsTheOthers(t *testing.T) {
	s, _, dir := sudoHost(t)
	rule := ruleNaming(t, s.sudoRules(), "multi")
	if err := s.RemoveSudoRule(context.Background(), "multi", rule.ID); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "sudoers"))
	if !containsLine(string(b), "other ALL=(root, www-data) /bin/ls, /bin/cat") {
		t.Fatalf("file = %q", b)
	}
	if !strings.Contains(string(b), "%sudo") {
		t.Fatal("the rest of the file was lost")
	}
	if s.sudoRules() == nil {
		t.Fatal("the rewritten file no longer reads")
	}
}

func TestARuleSpanningContinuationLinesGoesWhole(t *testing.T) {
	s, _, dir := fixtureHost(t, map[string]string{
		"sudoers": "%sudo ALL=(ALL) ALL\nion ALL=(ALL) \\\n    ALL\nafter ALL=(ALL) ALL\n",
	})
	rule := ruleNaming(t, s.sudoRules(), "ion")
	if rule.Line != 2 || rule.endLine != 3 {
		t.Fatalf("rule = %+v", rule)
	}
	if err := s.dropRuleFor(context.Background(), rule, "ion", []string{"ion"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "sudoers"))
	if string(b) != "%sudo ALL=(ALL) ALL\nafter ALL=(ALL) ALL\n" {
		t.Fatalf("file = %q", b)
	}
}

func TestAGroupsRuleCannotBeTakenFromOneMember(t *testing.T) {
	s, fake, _ := sudoHost(t)
	rule := ruleNaming(t, s.sudoRules(), "%sudo")
	err := s.RemoveSudoRule(context.Background(), "ion", rule.ID)
	if err == nil || !strings.Contains(err.Error(), "group sudo") {
		t.Fatalf("err = %v", err)
	}
	if fake.ran("visudo") {
		t.Fatal("wrote something")
	}
	if err := s.RemoveSudoRule(context.Background(), "ubuntu", "no-such-id"); err == nil {
		t.Fatal("a rule that is gone was removed")
	}
	if err := s.RemoveSudoRule(context.Background(), "root", rule.ID); !errors.Is(err, ErrProtected) {
		t.Fatalf("root: %v", err)
	}
}

func TestDeletingAnAccountsRulesLeavesOthersAlone(t *testing.T) {
	s, _, dir := sudoHost(t)
	if err := s.removeRulesNaming(context.Background(), User{Username: "ion", UID: -1, GID: -1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sudoers.d", "jd-ion")); err == nil {
		t.Fatal("the managed rule stayed")
	}
	if err := s.removeRulesNaming(context.Background(), User{Username: "ubuntu", UID: -1, GID: -1}); err != nil {
		t.Fatal(err)
	}
	for _, rule := range s.sudoRules() {
		if contains(rule.Who, "ubuntu") || contains(rule.Who, "ion") {
			t.Fatalf("still names them: %+v", rule)
		}
	}
	if rules := s.sudoRules(); len(rules) == 0 {
		t.Fatal("everything went")
	} else {
		ruleNaming(t, rules, "%sudo")
	}
}

func containsLine(content, want string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}
