package linuxusers

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// These run the real shadow suite, visudo, gpasswd, tar and sshd, and change real
// accounts, so they refuse to run anywhere but the throwaway image built for them:
// JD_USERS_LIVE=1 is what asks, and the marker file is what proves it is safe.
//
//	FROM ubuntu:24.04 with sudo openssh-server cron procps rsyslog libpam-pwquality,
//	/run/sshd, ssh-keygen -A, and `touch /.jd-users-live-sandbox`.
//
//	go test -c -o /tmp/l/linuxusers.test ./internal/linuxusers
//	docker run --rm --network none -e JD_USERS_LIVE=1 -v /tmp/l:/t:ro <image> \
//	  /t/linuxusers.test -test.run Live -test.v
const liveMarker = "/.jd-users-live-sandbox"

const livePort = "2222"

var (
	liveOnce sync.Once
	liveErr  error
)

func liveHost(t *testing.T) *Service {
	t.Helper()
	if os.Getenv("JD_USERS_LIVE") != "1" {
		t.Skip("set JD_USERS_LIVE=1, inside the sandbox image, to run against the real tools")
	}
	if _, err := os.Stat(liveMarker); err != nil {
		t.Fatalf("refusing to change this machine's accounts: %s is not here", liveMarker)
	}
	liveOnce.Do(func() {
		// The systemd run has no sign-ins to try, and needs /run/sshd left missing.
		if os.Getenv("JD_USERS_LIVE_SYSTEMD") == "1" {
			return
		}
		_ = exec.Command("rsyslogd").Run()
		_ = os.MkdirAll("/run/sshd", 0o755)
		liveErr = exec.Command("/usr/sbin/sshd", "-p", livePort).Run()
		time.Sleep(300 * time.Millisecond)
	})
	if liveErr != nil {
		t.Fatalf("sshd: %v", liveErr)
	}
	return New()
}

func liveShell(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func canSignIn(user string, auth ssh.AuthMethod) bool {
	client, err := ssh.Dial("tcp", "127.0.0.1:"+livePort, &ssh.ClientConfig{
		User: user, Auth: []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second,
	})
	if err != nil {
		return false
	}
	client.Close()
	return true
}

// agree is the point of the whole page: what it says about an account has to be
// what sshd does when somebody actually tries.
func agree(t *testing.T, s *Service, name string, password string, signer ssh.Signer, wantPassword, wantKey bool) {
	t.Helper()
	u, err := s.Get(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if u.SignIn.Password.Allowed != wantPassword {
		t.Errorf("%s: the page says password=%+v, want %v", name, u.SignIn.Password, wantPassword)
	}
	if u.SignIn.Key.Allowed != wantKey {
		t.Errorf("%s: the page says key=%+v, want %v", name, u.SignIn.Key, wantKey)
	}
	if password != "" {
		if got := canSignIn(name, ssh.Password(password)); got != wantPassword {
			t.Errorf("%s: sshd says password=%v, want %v", name, got, wantPassword)
		}
	}
	if signer != nil {
		if got := canSignIn(name, ssh.PublicKeys(signer)); got != wantKey {
			t.Errorf("%s: sshd says key=%v, want %v", name, got, wantKey)
		}
	}
}

func shadowHash(t *testing.T, name string) string {
	t.Helper()
	entry, ok := New().shadowEntry(name)
	if !ok {
		t.Fatalf("%s is not in shadow", name)
	}
	return entry.Hash
}

func TestLivePasswordsAndSignInAgreeWithSSHD(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	const name = "ann"
	if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash", Comment: "Ann A"}); err != nil {
		t.Fatal(err)
	}
	u, err := s.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if u.Password != PasswordNone || !u.Locked {
		t.Fatalf("a fresh account: %+v", u)
	}

	// The bug this page shipped with, against the real usermod.
	before := shadowHash(t, name)
	if err := s.SetLocked(ctx, name, false); !errors.Is(err, ErrNothingToUnlock) {
		t.Fatalf("unlock on a bare !: %v", err)
	}
	if got := shadowHash(t, name); got != before {
		t.Fatalf("the hash moved: %q", got)
	}

	const password = "correct horse battery 7"
	if err := s.SetPassword(ctx, name, password); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, name); u.Password != PasswordSet {
		t.Fatalf("after a password: %+v", u.Password)
	}
	agree(t, s, name, password, nil, true, false)

	generated, err := s.GenerateKey(name, "live", "")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(generated.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	agree(t, s, name, password, signer, true, true)

	home := "/home/" + name
	for path, mode := range map[string]os.FileMode{home + "/.ssh": 0o700, home + "/.ssh/authorized_keys": 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("%s mode = %v", path, info.Mode())
		}
	}
	if owner := strings.TrimSpace(liveShell(t, "stat", "-c", "%U", home+"/.ssh/authorized_keys")); owner != name {
		t.Errorf("authorized_keys belongs to %s", owner)
	}

	// Locked: the password stops, the keys do not, and unlocking brings back the
	// same password rather than a new one.
	hash := shadowHash(t, name)
	if err := s.SetLocked(ctx, name, true); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, name); u.Password != PasswordLocked {
		t.Fatalf("after a lock: %+v", u.Password)
	}
	agree(t, s, name, password, signer, false, true)
	if err := s.SetLocked(ctx, name, false); err != nil {
		t.Fatal(err)
	}
	if got := shadowHash(t, name); got != hash {
		t.Fatalf("unlock did not restore the hash: %q != %q", got, hash)
	}
	agree(t, s, name, password, signer, true, true)

	// Disabled: nothing gets in, and it comes back.
	if err := s.SetDisabled(ctx, name, true); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, name); !u.Disabled || u.SignIn.Key.Why != WhyDisabled {
		t.Fatalf("after disabling: %+v", u)
	}
	agree(t, s, name, password, signer, false, false)
	if err := s.SetDisabled(ctx, name, false); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, name); u.Disabled {
		t.Fatalf("after enabling: %+v", u)
	}
	agree(t, s, name, password, signer, true, true)

	// A date in the future changes nothing yet; one in the past is disabled.
	if err := s.SetExpiry(ctx, name, s.now().AddDate(0, 0, 30).Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, name); u.Disabled || u.ExpiresOn == "" {
		t.Fatalf("a future date: %+v", u)
	}
	if err := s.SetExpiry(ctx, name, "2001-01-01"); err != nil {
		t.Fatal(err)
	}
	agree(t, s, name, password, signer, false, false)
	if err := s.SetExpiry(ctx, name, ""); err != nil {
		t.Fatal(err)
	}

	// Rules about the password.
	maxDays, warn, inactive := 90, 14, 7
	if err := s.SetAging(ctx, name, AgingChange{MaxDays: &maxDays, WarnDays: &warn, InactiveDays: &inactive}); err != nil {
		t.Fatal(err)
	}
	u, _ = s.Get(ctx, name)
	if u.Aging.MaxDays == nil || *u.Aging.MaxDays != 90 || u.Aging.WarnDays == nil || *u.Aging.WarnDays != 14 ||
		u.Aging.InactiveDays == nil || u.Aging.ExpiresOn == "" {
		t.Fatalf("aging = %+v", u.Aging)
	}
	never := -1
	if err := s.SetAging(ctx, name, AgingChange{MaxDays: &never, InactiveDays: &never}); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, name); u.Aging.MaxDays != nil || u.Aging.InactiveDays != nil {
		t.Fatalf("aging = %+v", u.Aging)
	}
	if err := s.RequirePasswordChange(ctx, name); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, name); !u.Aging.MustChange {
		t.Fatalf("aging = %+v", u.Aging)
	}

	// Back to keys only: the old password is gone for good.
	if err := s.RemovePassword(ctx, name); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, name); u.Password != PasswordNone {
		t.Fatalf("after removal: %+v", u.Password)
	}
	if err := s.SetLocked(ctx, name, false); !errors.Is(err, ErrNothingToUnlock) {
		t.Fatalf("unlock after removal: %v", err)
	}
	agree(t, s, name, password, signer, false, true)
	if err := s.RequirePasswordChange(ctx, name); err == nil {
		t.Fatal("required a change of a password that is not there")
	}

	// The key is still labelled and restricted through the real file.
	label := "renamed"
	updated, err := s.UpdateKey(name, generated.Key.Fingerprint, KeyEdit{Comment: &label, Options: []string{"restrict"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Options != "restrict" {
		t.Fatalf("key = %+v", updated)
	}
	agree(t, s, name, "", signer, false, true)
	expired := `expiry-time="20010101"`
	if _, err := s.UpdateKey(name, generated.Key.Fingerprint, KeyEdit{Options: []string{expired}}); err != nil {
		t.Fatal(err)
	}
	if canSignIn(name, ssh.PublicKeys(signer)) {
		t.Error("sshd let an expired key in")
	}
	if _, err := s.UpdateKey(name, generated.Key.Fingerprint, KeyEdit{ClearOptions: true}); err != nil {
		t.Fatal(err)
	}
	if !canSignIn(name, ssh.PublicKeys(signer)) {
		t.Error("clearing the restriction did not bring the key back")
	}
	if err := s.RemoveKey(name, generated.Key.Fingerprint); err != nil {
		t.Fatal(err)
	}
	agree(t, s, name, "", signer, false, false)
}

func TestLiveAnEmptyPasswordIsWhatTheToolsSayItIs(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	const name = "emptypw"
	if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	liveShell(t, "passwd", "-d", name)
	u, err := s.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if u.Password != PasswordEmpty || u.SignIn.Password.Why != WhyEmptyRefused {
		t.Fatalf("u = %+v", u)
	}
	if canSignIn(name, ssh.Password("")) {
		t.Error("sshd accepted an empty password the page says it refuses")
	}
}

func TestLiveRootFollowsPermitRootLogin(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	const password = "root password long enough"
	if err := s.SetPassword(ctx, "root", password); err != nil {
		t.Fatal(err)
	}
	generated, err := s.GenerateKey("root", "live-root", "")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(generated.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	agree(t, s, "root", password, signer, false, true)
	u, err := s.Get(ctx, "root")
	if err != nil {
		t.Fatal(err)
	}
	if u.SignIn.Password.Why != WhyRootKeysOnly {
		t.Fatalf("root = %+v", u.SignIn)
	}
	if err := s.SetLocked(ctx, "root", true); !errors.Is(err, ErrProtected) {
		t.Fatalf("lock root: %v", err)
	}
	if err := s.RemoveKey("root", generated.Key.Fingerprint); err != nil {
		t.Fatal(err)
	}
	// Root on keys alone is allowed; it is what the stock image ships.
	if err := s.RemovePassword(ctx, "root"); err != nil {
		t.Fatalf("remove root's password: %v", err)
	}
	if u, _ := s.Get(ctx, "root"); u.Password != PasswordNone {
		t.Fatalf("root = %+v", u.Password)
	}
}

func TestLiveAMatchBlockIsInOneAccountsAnswer(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	for _, name := range []string{"matched", "unmatched"} {
		if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash"}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetPassword(ctx, name, "a long enough password"); err != nil {
			t.Fatal(err)
		}
	}
	dropIn := "/etc/ssh/sshd_config.d/50-live.conf"
	if err := os.WriteFile(dropIn, []byte("Match User matched\n    PasswordAuthentication no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dropIn)
	if u, _ := s.Get(ctx, "matched"); u.SignIn.Password.Why != WhyPasswordAuthOff {
		t.Fatalf("matched = %+v", u.SignIn)
	}
	if u, _ := s.Get(ctx, "unmatched"); !u.SignIn.Password.Allowed {
		t.Fatalf("unmatched = %+v", u.SignIn)
	}
	if err := os.WriteFile(dropIn, []byte("AllowUsers unmatched\nDenyGroups nogroup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, "matched"); u.SignIn.Password.Why != WhyNotAllowed {
		t.Fatalf("not on the allow list = %+v", u.SignIn)
	}
	if u, _ := s.Get(ctx, "unmatched"); !u.SignIn.Password.Allowed {
		t.Fatalf("on the allow list = %+v", u.SignIn)
	}
	if err := os.WriteFile(dropIn, []byte("this is not a directive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := s.sshPolicy(ctx, ""); !p.Installed || p.Known {
		t.Fatalf("an sshd that refuses its config: %+v", p)
	}
}

func TestLiveSudoGrantsAreWhatSudoItselfReads(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	const name = "bea"
	if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	sudoL := func() string {
		out, _ := exec.Command("sudo", "-l", "-U", name).CombinedOutput()
		return string(out)
	}
	path := "/etc/sudoers.d/jd-" + name
	for _, c := range []struct {
		grant SudoGrant
		want  string
	}{
		{SudoGrant{Level: SudoPassword}, "(ALL : ALL) ALL"},
		{SudoGrant{Level: SudoNoPassword}, "(ALL : ALL) NOPASSWD: ALL"},
		{SudoGrant{Level: SudoNoPassword, Commands: []string{"/usr/bin/id", "/usr/bin/env -i"}}, "(root) NOPASSWD: /usr/bin/id, /usr/bin/env -i"},
	} {
		if err := s.SetSudoGrant(ctx, name, c.grant); err != nil {
			t.Fatal(err)
		}
		liveShell(t, "visudo", "--check")
		if got := sudoL(); !strings.Contains(got, c.want) {
			t.Fatalf("sudo -l for %+v:\n%s", c.grant, got)
		}
		back := s.ManagedGrant(name)
		if back.Level != c.grant.Level || len(back.Commands) != len(c.grant.Commands) {
			t.Fatalf("read back %+v, wrote %+v", back, c.grant)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o440 {
		t.Fatalf("mode = %v", info.Mode())
	}

	// Every command the form can send has to survive visudo, and read back as what
	// was typed rather than as sudoers' escaping of it.
	for _, tricky := range []string{"/usr/bin/env A=B", "/usr/bin/foo%bar", "/usr/bin/echo *", "/usr/bin/a-b_c.d+e"} {
		if err := s.SetSudoGrant(ctx, name, SudoGrant{Level: SudoPassword, Commands: []string{tricky}}); err != nil {
			t.Fatalf("%q: %v", tricky, err)
		}
		liveShell(t, "visudo", "--check")
		if back := s.ManagedGrant(name); len(back.Commands) != 1 || back.Commands[0] != tricky {
			t.Fatalf("%q read back as %+v", tricky, back)
		}
	}

	// A rule visudo refuses leaves nothing behind.
	bad := "/etc/sudoers.d/jd-refused"
	if err := s.writeSudoers(ctx, bad, []byte("this is not a sudoers line\n")); err == nil {
		t.Fatal("visudo accepted that")
	}
	if _, err := os.Stat(bad); err == nil {
		t.Fatal("the refused rule is live")
	}
	entries, _ := os.ReadDir("/etc/sudoers.d")
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".jd-") {
			t.Fatalf("left %s behind", entry.Name())
		}
	}
	if err := s.SetSudoGrant(ctx, name, SudoGrant{Level: SudoNone}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("still there")
	}
	if got := sudoL(); !strings.Contains(got, "not allowed") {
		t.Fatalf("sudo -l after none:\n%s", got)
	}

	// Cloud-init's file, as a stock image has it.
	cloud := "/etc/sudoers.d/90-cloud-init-users"
	if err := os.WriteFile(cloud, []byte("# Created by cloud-init v. 24.1\n\n# User rules for "+name+"\n"+name+" ALL=(ALL) NOPASSWD:ALL\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cloud, 0o440); err != nil {
		t.Fatal(err)
	}
	if got := sudoL(); !strings.Contains(got, "NOPASSWD: ALL") {
		t.Fatalf("cloud-init's rule:\n%s", got)
	}
	u, err := s.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	var route *RootRoute
	for i := range u.RootRoutes {
		if u.RootRoutes[i].File == cloud {
			route = &u.RootRoutes[i]
		}
	}
	if route == nil || !route.NoPassword || !route.Usable || route.Limited {
		t.Fatalf("routes = %+v", u.RootRoutes)
	}
	if err := s.RemoveSudoRule(ctx, name, route.RuleID); err != nil {
		t.Fatal(err)
	}
	liveShell(t, "visudo", "--check")
	if got := sudoL(); strings.Contains(got, "NOPASSWD") {
		t.Fatalf("after removal:\n%s", got)
	}
	b, _ := os.ReadFile(cloud)
	if !strings.Contains(string(b), "Created by cloud-init") {
		t.Fatalf("the file's other lines went: %q", b)
	}
	info, _ = os.Stat(cloud)
	if info.Mode().Perm() != 0o440 {
		t.Fatalf("cloud-init's file mode = %v", info.Mode())
	}
	os.Remove(cloud)

	// The stock %sudo rule is read, and a member without a password cannot use it.
	if err := s.SetGroups(ctx, name, []string{"sudo"}); err != nil {
		t.Fatal(err)
	}
	u, _ = s.Get(ctx, name)
	if len(u.RootRoutes) != 1 || u.RootRoutes[0].Via != "sudo" || u.RootRoutes[0].Usable {
		t.Fatalf("routes = %+v", u.RootRoutes)
	}
	if err := s.SetPassword(ctx, name, "bea has one now ok"); err != nil {
		t.Fatal(err)
	}
	u, _ = s.Get(ctx, name)
	if len(u.RootRoutes) != 1 || !u.RootRoutes[0].Usable {
		t.Fatalf("routes = %+v", u.RootRoutes)
	}
	if err := s.RemoveSudoRule(ctx, name, u.RootRoutes[0].RuleID); err == nil || !strings.Contains(err.Error(), "group") {
		t.Fatalf("took a group's rule from one member: %v", err)
	}
}

func TestLiveGroups(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	for _, name := range []string{"cal", "dee"} {
		if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateGroup(ctx, "ops", -1, false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGroup(ctx, "svc", 950, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupMembers(ctx, "ops", []string{"cal", "dee"}); err != nil {
		t.Fatal(err)
	}
	groups := func() map[string]Group {
		list, err := s.ListGroups(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]Group{}
		for _, g := range list {
			out[g.Name] = g
		}
		return out
	}
	if g := groups()["ops"]; strings.Join(g.Members, ",") != "cal,dee" {
		t.Fatalf("ops = %+v", g)
	}
	if g := groups()["svc"]; g.GID != 950 || !g.System {
		t.Fatalf("svc = %+v", g)
	}
	if err := s.SetGroupMembers(ctx, "ops", []string{"dee"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupMembers(ctx, "svc", []string{}); err != nil {
		t.Fatalf("emptying a group that is already empty: %v", err)
	}
	if err := s.SetGroupID(ctx, "ops", 3000); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameGroup(ctx, "ops", "operators"); err != nil {
		t.Fatal(err)
	}
	g := groups()["operators"]
	if g.GID != 3000 || strings.Join(g.Members, ",") != "dee" {
		t.Fatalf("operators = %+v", g)
	}
	if u, _ := s.Get(ctx, "dee"); !contains(u.Groups, "operators") {
		t.Fatalf("dee = %+v", u.Groups)
	}
	if err := s.DeleteGroup(ctx, "cal"); err == nil {
		t.Fatal("deleted a group that is an account's main group")
	}
	if err := s.DeleteGroup(ctx, "operators"); err != nil {
		t.Fatal(err)
	}
	if _, ok := groups()["operators"]; ok {
		t.Fatal("still there")
	}
	if err := s.DeleteGroup(ctx, "sudo"); !errors.Is(err, ErrProtected) {
		t.Fatalf("sudo: %v", err)
	}
}

func TestLiveRenameMovesEverythingKeyedByTheName(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	if err := s.Create(ctx, CreateOptions{Username: "eve", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSudoGrant(ctx, "eve", SudoGrant{Level: SudoPassword}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("crontab", "-u", "eve", "-")
	cmd.Stdin = strings.NewReader("0 1 * * * /bin/true\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("crontab: %v\n%s", err, out)
	}
	final, err := s.UpdateProfile(ctx, "eve", ProfileChange{NewName: stringPtr("eva"), Comment: stringPtr("Eva")})
	if err != nil {
		t.Fatal(err)
	}
	if final != "eva" {
		t.Fatalf("final = %q", final)
	}
	u, err := s.Get(ctx, "eva")
	if err != nil {
		t.Fatal(err)
	}
	if u.PrimaryGroup != "eva" || u.Comment != "Eva" {
		t.Fatalf("eva = %+v", u)
	}
	if _, err := os.Stat("/etc/sudoers.d/jd-eve"); err == nil {
		t.Fatal("the old rule is still live")
	}
	if got := s.ManagedGrant("eva"); got.Level != SudoPassword {
		t.Fatalf("grant = %+v", got)
	}
	liveShell(t, "visudo", "--check")
	if got := liveShell(t, "crontab", "-l", "-u", "eva"); !strings.Contains(got, "/bin/true") {
		t.Fatalf("crontab = %q", got)
	}
	if out, err := exec.Command("crontab", "-l", "-u", "eve").CombinedOutput(); err == nil {
		t.Fatalf("the old name still has a crontab: %s", out)
	}

	// Home, primary group and number, moved with their files.
	if err := s.CreateGroup(ctx, "staff2", -1, false); err != nil {
		t.Fatal(err)
	}
	marker := "/home/eve/marker"
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveShell(t, "chown", "eva:", marker)
	if _, err := s.UpdateProfile(ctx, "eva", ProfileChange{
		Home: stringPtr("/home/eva"), MoveHome: true, UID: intPtr(4242), PrimaryGroup: stringPtr("staff2"),
	}); err != nil {
		t.Fatal(err)
	}
	u, _ = s.Get(ctx, "eva")
	if u.Home != "/home/eva" || u.UID != 4242 || u.PrimaryGroup != "staff2" {
		t.Fatalf("eva = %+v", u)
	}
	if owner := strings.TrimSpace(liveShell(t, "stat", "-c", "%u", "/home/eva/marker")); owner != "4242" {
		t.Fatalf("the moved file belongs to %s", owner)
	}
	if err := s.SetShell(ctx, "eva", "/bin/bash-nope"); err == nil {
		t.Fatal("a shell /etc/shells does not list was accepted")
	}
}

func TestLiveDeleteEveryWay(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	make := func(name string) int {
		t.Helper()
		if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash"}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("/home/"+name+"/file", []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
		u, err := s.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		liveShell(t, "chown", name+":", "/home/"+name+"/file")
		return u.UID
	}
	gone := func(name string) {
		t.Helper()
		if _, err := s.Lookup(name); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: %v", name, err)
		}
	}

	make("delkeep")
	if err := s.SetSudoGrant(ctx, "delkeep", SudoGrant{Level: SudoPassword}); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewRemoval(ctx, "delkeep")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.HomeExists || preview.HomeBytes == 0 || len(preview.SudoRules) != 1 || preview.Blocked != "" {
		t.Fatalf("preview = %+v", preview)
	}
	if _, err := s.Delete(ctx, "delkeep", RemovalOptions{Home: HomeKeep}); err != nil {
		t.Fatal(err)
	}
	gone("delkeep")
	if _, err := os.Stat("/home/delkeep/file"); err != nil {
		t.Fatalf("keep lost the home: %v", err)
	}
	if _, err := os.Stat("/etc/sudoers.d/jd-delkeep"); err == nil {
		t.Fatal("the rule outlived the account")
	}
	liveShell(t, "visudo", "--check")

	make("delhome")
	if _, err := s.Delete(ctx, "delhome", RemovalOptions{Home: HomeDelete}); err != nil {
		t.Fatal(err)
	}
	gone("delhome")
	if _, err := os.Stat("/home/delhome"); err == nil {
		t.Fatal("the home stayed")
	}

	make("delarch")
	result, err := s.Delete(ctx, "delarch", RemovalOptions{Home: HomeArchive})
	if err != nil {
		t.Fatal(err)
	}
	gone("delarch")
	if _, err := os.Stat("/home/delarch"); err == nil {
		t.Fatal("the home stayed")
	}
	if !strings.HasPrefix(result.Archive, archiveRoot+"/delarch-") {
		t.Fatalf("archive = %q", result.Archive)
	}
	if got := liveShell(t, "tar", "--list", "--file", result.Archive); !strings.Contains(got, "delarch/file") {
		t.Fatalf("the archive holds %q", got)
	}
	if info, _ := os.Stat(result.Archive); info.Mode().Perm() != 0o600 {
		t.Fatalf("archive mode = %v", info.Mode())
	}

	// Handing over: the home and what the number owned elsewhere.
	make("heir")
	uid := make("delgive")
	elsewhere := "/var/tmp/delgive-file"
	if err := os.WriteFile(elsewhere, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveShell(t, "chown", "delgive:", elsewhere)
	// A symlink in the home must not redirect a root-run chown.
	victim := "/var/tmp/not-theirs"
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, "/home/delgive/link"); err != nil {
		t.Fatal(err)
	}
	if preview, err := s.PreviewRemoval(ctx, "delgive"); err != nil || !contains(preview.Elsewhere, elsewhere) {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if _, err := s.Delete(ctx, "delgive", RemovalOptions{Home: HomeKeep, TransferTo: "heir"}); err != nil {
		t.Fatal(err)
	}
	gone("delgive")
	for _, path := range []string{"/home/delgive/file", elsewhere} {
		if owner := strings.TrimSpace(liveShell(t, "stat", "-c", "%U", path)); owner != "heir" {
			t.Fatalf("%s belongs to %s", path, owner)
		}
	}
	if owner := strings.TrimSpace(liveShell(t, "stat", "-c", "%U", victim)); owner != "root" {
		t.Fatalf("the symlink redirected a chown: %s belongs to %s", victim, owner)
	}
	_ = uid

	// A busy account stops, and is stopped when asked.
	make("delbusy")
	sleeper := exec.Command("setpriv", "--reuid=delbusy", "--regid=delbusy", "--clear-groups", "sleep", "300")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	go sleeper.Wait()
	time.Sleep(200 * time.Millisecond)
	if count, busiest, err := s.Processes(ctx, "delbusy", 5); err != nil || count != 1 || busiest[0].Name != "sleep" {
		t.Fatalf("processes = %d %+v %v", count, busiest, err)
	}
	if _, err := s.Delete(ctx, "delbusy", RemovalOptions{Home: HomeKeep}); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("busy: %v", err)
	}
	if _, err := s.Delete(ctx, "delbusy", RemovalOptions{Home: HomeDelete, EndSessions: true}); err != nil {
		t.Fatal(err)
	}
	gone("delbusy")

	// Refused outright.
	if _, err := s.Delete(ctx, "root", RemovalOptions{}); !errors.Is(err, ErrProtected) {
		t.Fatalf("root: %v", err)
	}
	if err := s.Create(ctx, CreateOptions{Username: "sharedhome", Shell: "/bin/bash", NoHome: true}); err != nil {
		t.Fatal(err)
	}
	liveShell(t, "usermod", "--home", "/var", "sharedhome")
	if _, err := s.Delete(ctx, "sharedhome", RemovalOptions{Home: HomeDelete}); err == nil {
		t.Fatal("offered to delete /var")
	}
	if _, err := os.Stat("/var/log"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(ctx, "sharedhome", RemovalOptions{Home: HomeKeep}); err != nil {
		t.Fatal(err)
	}
}

func TestLiveTheLastWayInIsRefused(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	const name = "solo"
	if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSudoGrant(ctx, name, SudoGrant{Level: SudoNoPassword}); err != nil {
		t.Fatal(err)
	}
	generated, err := s.GenerateKey(name, "only", "")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(generated.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	if !canSignIn(name, ssh.PublicKeys(signer)) {
		t.Fatal("the way in is not one")
	}
	before, err := s.WayIn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Other tests may have left their own ways in; this is about the last one.
	for _, other := range before {
		if other == name {
			continue
		}
		t.Logf("taking %s out so that %s is the last way in", other, name)
		if err := s.SetSudoGrant(ctx, other, SudoGrant{Level: SudoNone}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetDisabled(ctx, other, true); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := s.WayIn(ctx); len(got) != 1 || got[0] != name {
		t.Fatalf("way in = %v", got)
	}
	if _, err := s.Delete(ctx, name, RemovalOptions{Home: HomeKeep}); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("delete: %v", err)
	}
	if err := s.SetDisabled(ctx, name, true); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("disable: %v", err)
	}
	if err := s.SetShell(ctx, name, "/usr/sbin/nologin"); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("shell: %v", err)
	}
	if err := s.Judge(ctx, Change{Username: name, KeysRemoved: 1}); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("last key: %v", err)
	}
	if err := s.SetSudoGrant(ctx, name, SudoGrant{Level: SudoNone}); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("sudo: %v", err)
	}
	if !canSignIn(name, ssh.PublicKeys(signer)) {
		t.Error("a refused change changed something")
	}
}

func TestLiveDefaultsReachTheToolsThatReadThem(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	got := s.Defaults()
	if got.PasswordMaxDays != -1 || !got.Quality.Available || len(got.Skel) == 0 || len(got.Shells) == 0 {
		t.Fatalf("defaults = %+v", got)
	}
	maxDays, warn, length := 60, 10, 14
	shell := "/bin/bash"
	if err := s.SetDefaults(DefaultsChange{PasswordMaxDays: &maxDays, PasswordWarnDays: &warn, Shell: &shell, MinLength: &length}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, CreateOptions{Username: "fay"}); err != nil {
		t.Fatal(err)
	}
	u, err := s.Get(ctx, "fay")
	if err != nil {
		t.Fatal(err)
	}
	if u.Shell != "/bin/bash" {
		t.Fatalf("useradd ignored the default shell: %q", u.Shell)
	}
	if u.Aging.MaxDays == nil || *u.Aging.MaxDays != 60 {
		t.Fatalf("useradd ignored PASS_MAX_DAYS: %+v", u.Aging)
	}
	if !strings.Contains(liveShell(t, "useradd", "-D"), "SHELL=/bin/bash") {
		t.Fatal("useradd -D does not agree")
	}
	if got := s.Defaults(); got.Quality.MinLength != 14 {
		t.Fatalf("defaults = %+v", got.Quality)
	}
	never := -1
	sh := "/bin/sh"
	if err := s.SetDefaults(DefaultsChange{PasswordMaxDays: &never, Shell: &sh}); err != nil {
		t.Fatal(err)
	}
}

func TestLiveAuthLogAndLastlogAreReadFromTheRealFormats(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	const name = "gil"
	if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	generated, err := s.GenerateKey(name, "logged", "")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey([]byte(generated.PrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	client, err := ssh.Dial("tcp", "127.0.0.1:"+livePort, &ssh.ClientConfig{
		User: name, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	session.Stdout = &out
	if err := session.Run("id"); err != nil {
		t.Logf("session: %v (%s)", err, out.String())
	}
	session.Close()
	client.Close()
	time.Sleep(500 * time.Millisecond)

	lines := s.authLines(ctx)
	t.Logf("auth log has %d lines", len(lines))
	keys, _, err := s.KeysWithUse(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].LastUsed == nil || keys[0].LastUsedFrom != "127.0.0.1" {
		tail := lines
		if len(tail) > 15 {
			tail = tail[len(tail)-15:]
		}
		t.Fatalf("key = %+v, log tail:\n%s", keys, strings.Join(tail, "\n"))
	}
	if age := time.Since(*keys[0].LastUsed); age < -time.Minute || age > time.Minute {
		t.Fatalf("last used %v ago", age)
	}

	if err := s.SetSudoGrant(ctx, name, SudoGrant{Level: SudoNoPassword}); err != nil {
		t.Fatal(err)
	}
	sudo := exec.Command("setpriv", "--reuid="+name, "--regid="+name, "--init-groups", "sudo", "-n", "/usr/bin/id")
	if output, err := sudo.CombinedOutput(); err != nil {
		t.Fatalf("sudo: %v\n%s", err, output)
	}
	refused := exec.Command("setpriv", "--reuid=nobody", "--regid=nogroup", "--clear-groups", "sudo", "-n", "/usr/bin/id")
	_, _ = refused.CombinedOutput()
	time.Sleep(500 * time.Millisecond)
	history := s.SudoHistory(ctx, name, 10)
	if len(history) != 1 || history[0].Command != "/usr/bin/id" || history[0].RunAs != "root" || history[0].Failed {
		t.Fatalf("history = %+v, lines:\n%s", history, strings.Join(s.authLines(ctx), "\n"))
	}

	// wtmp and lastlog, written by a real login.
	u, err := s.Get(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if u.LastLogin == nil || u.LastFrom != "127.0.0.1" {
		t.Logf("lastlog said:\n%s", liveShell(t, "lastlog"))
		t.Fatalf("u = %+v", u)
	}

	count, busiest, err := s.Processes(ctx, "root", 3)
	if err != nil || count == 0 || len(busiest) == 0 {
		t.Fatalf("processes = %d %+v %v", count, busiest, err)
	}
	bytes, err := s.DiskUsage(ctx, name)
	if err != nil || bytes == 0 {
		t.Fatalf("disk = %d, %v", bytes, err)
	}
	if got := s.FailedAttempts(ctx, name); !got.Available || got.Count != 0 {
		t.Fatalf("faillock = %+v", got)
	}
	if err := s.ResetFailedAttempts(ctx, name); err != nil {
		t.Fatal(err)
	}
}

func TestLiveKeysInAHomeThisProcessDidNotMake(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	if err := s.Create(ctx, CreateOptions{Username: "hal", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	line := ed25519Line(t, "hand-written")
	if err := os.MkdirAll("/home/hal/.ssh", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/home/hal/.ssh/authorized_keys", []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	liveShell(t, "chown", "-R", "hal:", "/home/hal/.ssh")
	u, err := s.Get(ctx, "hal")
	if err != nil {
		t.Fatal(err)
	}
	if u.SSHKeys != 1 {
		t.Fatalf("u = %+v", u)
	}
	if !regexp.MustCompile(`^/home/hal$`).MatchString(u.Home) {
		t.Fatalf("home = %q", u.Home)
	}
	// A daemon's home outside /home, which is what the old lookup could not reach.
	if err := s.Create(ctx, CreateOptions{Username: "svc1", System: true, NoHome: true, Shell: "/usr/sbin/nologin"}); err != nil {
		t.Fatal(err)
	}
	liveShell(t, "usermod", "--home", filepath.Join("/var/lib", "svc1"), "svc1")
	if keys, _, err := s.ListKeys("svc1"); err != nil || len(keys) != 0 {
		t.Fatalf("keys = %+v, %v", keys, err)
	}
	if u, err := s.Lookup("svc1"); err != nil || !u.System {
		t.Fatalf("svc1 = %+v, %v", u, err)
	}
}

// The rest needs what a plain container does not have: systemd as PID 1, and PID 1
// being somewhere nsenter can go. That second part is the deployment's own path:
// everything here crosses through hostexec the way the dashboard's container does.
func TestLiveSystemdLimitsAndTheHostPath(t *testing.T) {
	if os.Getenv("JD_USERS_LIVE_SYSTEMD") != "1" {
		t.Skip("needs the sandbox image running systemd as PID 1")
	}
	s := liveHost(t)
	ctx := context.Background()
	if !hostHas("usermod") {
		t.Fatal("nsenter should reach PID 1 here")
	}
	// Socket-activated sshd, as Ubuntu ships it: until the first connection nothing
	// has made /run/sshd, and sshd -T refuses to say anything without it.
	if err := os.RemoveAll("/run/sshd"); err != nil {
		t.Fatal(err)
	}
	if p := s.sshPolicy(ctx, ""); !p.Known {
		t.Fatalf("sshd -T was not recovered: %+v", p)
	}
	const name = "ivy"
	if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	// A password through nsenter's stdin, and keys through /proc/1/root.
	if err := s.SetPassword(ctx, name, "through the namespace door"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Get(ctx, name); u.Password != PasswordSet {
		t.Fatalf("u = %+v", u)
	}
	generated, err := s.GenerateKey(name, "through-proc", "")
	if err != nil {
		t.Fatal(err)
	}
	if keys, _, err := s.ListKeys(name); err != nil || len(keys) != 1 || keys[0].Fingerprint != generated.Key.Fingerprint {
		t.Fatalf("keys = %+v, %v", keys, err)
	}

	u, err := s.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	slice := "user-" + strconv.Itoa(u.UID) + ".slice"
	show := func() string {
		return liveShell(t, "systemctl", "show", slice, "-p", "MemoryMax", "-p", "TasksMax", "-p", "CPUQuotaPerSecUSec")
	}
	if err := s.SetLimits(ctx, name, AccountLimits{CPUPercent: 50, MemoryBytes: 256 << 20, Tasks: 64}); err != nil {
		t.Fatal(err)
	}
	got := show()
	for _, want := range []string{"MemoryMax=268435456", "TasksMax=64", "CPUQuotaPerSecUSec=500ms"} {
		if !strings.Contains(got, want) {
			t.Fatalf("systemd says:\n%s", got)
		}
	}
	read, err := s.Limits(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if !read.Systemd || read.CPUPercent != 50 || read.MemoryBytes != 256<<20 || read.Tasks != 64 {
		t.Fatalf("read back = %+v", read)
	}
	// A real session lands under the slice, and the limit is on its cgroup.
	liveShell(t, "loginctl", "enable-linger", name)
	time.Sleep(time.Second)
	memory := "/sys/fs/cgroup/user.slice/" + slice + "/memory.max"
	if b, err := os.ReadFile(memory); err == nil {
		if strings.TrimSpace(string(b)) != "268435456" {
			t.Fatalf("%s = %q", memory, b)
		}
	} else {
		t.Logf("no cgroup file to check: %v", err)
	}
	// Ending sessions stops what the account is running, through logind and pkill.
	sleeper := exec.Command("systemd-run", "--uid="+name, "--no-block", "--collect", "--unit=ivy-sleep", "/usr/bin/sleep", "300")
	if out, err := sleeper.CombinedOutput(); err != nil {
		t.Fatalf("systemd-run: %v\n%s", err, out)
	}
	time.Sleep(500 * time.Millisecond)
	if count, _, err := s.Processes(ctx, name, 0); err != nil || count == 0 {
		t.Fatalf("processes = %d, %v", count, err)
	}
	liveShell(t, "loginctl", "disable-linger", name)
	if err := s.EndSessions(ctx, name); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if count, _, _ := s.Processes(ctx, name, 0); count != 0 {
		t.Fatalf("%d processes survived", count)
	}

	if err := s.SetLimits(ctx, name, AccountLimits{}); err != nil {
		t.Fatal(err)
	}
	if got := show(); !strings.Contains(got, "MemoryMax=infinity") {
		t.Fatalf("after clearing:\n%s", got)
	}
	if _, err := os.Stat(s.limitsFile(u.UID)); err == nil {
		t.Fatal("the drop-in stayed")
	}
	// Deleting takes a limit with the account, so the next one given the number
	// does not inherit it.
	if err := s.SetLimits(ctx, name, AccountLimits{Tasks: 64}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(ctx, name, RemovalOptions{Home: HomeDelete, EndSessions: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.limitsFile(u.UID)); err == nil {
		t.Fatal("the limit outlived the account")
	}
}

// The dashboard's container shares /etc and /home with the host and not much else.
// This runs under `unshare --mount` with tmpfs laid over /var/backups and /data, so
// that this process cannot see the real ones and only a tool sent through nsenter,
// or a path through /proc/1/root, can. It is where a directory made from here and a
// tar run over there used to miss each other.
func TestLiveAcrossTheMountNamespace(t *testing.T) {
	if os.Getenv("JD_USERS_LIVE_NAMESPACE") != "1" {
		t.Skip("needs `unshare --mount` over the systemd sandbox")
	}
	s := liveHost(t)
	ctx := context.Background()
	host := func(name string, args ...string) string {
		t.Helper()
		out, err := s.run(ctx, "", name, args...)
		if err != nil {
			t.Fatalf("%s %v on the host: %v", name, args, err)
		}
		return out
	}
	const name = "jan"
	if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash", NoHome: true}); err != nil {
		t.Fatal(err)
	}
	host("mkdir", "-p", "/data/jan")
	host("sh", "-c", "echo data > /data/jan/marker")
	host("chown", "-R", "jan:", "/data/jan")
	host("usermod", "--home", "/data/jan", name)
	if _, err := os.Stat("/data/jan"); err == nil {
		t.Fatal("this test is not hiding /data from itself")
	}

	// Keys in a home this process cannot see.
	generated, err := s.GenerateKey(name, "across", "")
	if err != nil {
		t.Fatal(err)
	}
	if keys, _, err := s.ListKeys(name); err != nil || len(keys) != 1 || keys[0].Fingerprint != generated.Key.Fingerprint {
		t.Fatalf("keys = %+v, %v", keys, err)
	}
	if got := host("cat", "/data/jan/.ssh/authorized_keys"); !strings.Contains(got, "across") {
		t.Fatalf("the host's file says %q", got)
	}
	if owner := strings.TrimSpace(host("stat", "--format", "%U", "/data/jan/.ssh/authorized_keys")); owner != name {
		t.Fatalf("authorized_keys belongs to %s", owner)
	}
	if u, err := s.Get(ctx, name); err != nil || u.SSHKeys != 1 || !u.SignIn.Key.Allowed {
		t.Fatalf("u = %+v, %v", u, err)
	}

	// The preview reads the same home.
	preview, err := s.PreviewRemoval(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.HomeExists || preview.HomeBytes == 0 {
		t.Fatalf("preview = %+v", preview)
	}

	// The archive lands where the host can find it, not in this process's tmpfs.
	result, err := s.Delete(ctx, name, RemovalOptions{Home: HomeArchive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.Archive); err == nil {
		t.Fatal("the archive is in this process's own mount, where the host cannot see it")
	}
	if got := host("tar", "--list", "--file", result.Archive); !strings.Contains(got, "jan/marker") {
		t.Fatalf("the archive holds %q", got)
	}
	if mode := strings.TrimSpace(host("stat", "--format", "%a", result.Archive)); mode != "600" {
		t.Fatalf("archive mode = %s", mode)
	}
	if out, err := s.run(ctx, "", "stat", "/data/jan"); err == nil {
		t.Fatalf("the home stayed: %s", out)
	}

	// Handing files over reaches the home too.
	if err := s.Create(ctx, CreateOptions{Username: "kit", Shell: "/bin/bash", NoHome: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, CreateOptions{Username: "lee", Shell: "/bin/bash", NoHome: true}); err != nil {
		t.Fatal(err)
	}
	host("mkdir", "-p", "/data/lee")
	host("sh", "-c", "echo data > /data/lee/file")
	host("chown", "-R", "lee:", "/data/lee")
	host("usermod", "--home", "/data/lee", "lee")
	if _, err := s.Delete(ctx, "lee", RemovalOptions{Home: HomeKeep, TransferTo: "kit"}); err != nil {
		t.Fatal(err)
	}
	if owner := strings.TrimSpace(host("stat", "--format", "%U", "/data/lee/file")); owner != "kit" {
		t.Fatalf("the file belongs to %s", owner)
	}
}

// The security review's findings, against the real tools. Each of these was a way a
// root-run command could be aimed at something it should not touch.

func TestLiveASecondRootLosesItsNameAndNothingElse(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	liveShell(t, "useradd", "-o", "-u", "0", "-g", "0", "-M", "-d", "/root", "-s", "/bin/bash", "toor")
	if err := s.EndSessions(ctx, "toor"); !errors.Is(err, ErrSharedNumber) {
		t.Fatalf("EndSessions: %v", err)
	}
	if _, err := s.Delete(ctx, "toor", RemovalOptions{EndSessions: true}); !errors.Is(err, ErrSharedNumber) {
		t.Fatalf("Delete with EndSessions: %v", err)
	}
	removal, err := s.PreviewRemoval(ctx, "toor")
	if err != nil {
		t.Fatal(err)
	}
	if removal.SharesNumber != "root" || !removal.HomeUnsafe || removal.Processes != 0 {
		t.Fatalf("preview = %+v", removal)
	}
	// This test's own process is uid 0. That it is still here is the assertion.
	if _, err := s.Delete(ctx, "toor", RemovalOptions{Home: HomeKeep}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup("toor"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("toor: %v", err)
	}
	if _, err := s.Lookup("root"); err != nil {
		t.Fatalf("root went with it: %v", err)
	}
	if _, err := os.Stat("/root"); err != nil {
		t.Fatalf("/root went with it: %v", err)
	}
}

func TestLiveAHandoverTouchesOnlyWhatTheAccountOwned(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	for _, name := range []string{"leaver", "taker"} {
		if err := s.Create(ctx, CreateOptions{Username: name, Shell: "/bin/bash"}); err != nil {
			t.Fatal(err)
		}
	}
	liveShell(t, "sh", "-c", `
		echo mine > /home/leaver/mine
		mkdir -p /srv/leaver-dir/sub
		echo elsewhere > /srv/leaver-dir/sub/file
		chown -R leaver: /home/leaver /srv/leaver-dir
		# Three things a leaver could plant in its own home to steer a root chown.
		echo secret > /srv/not-theirs
		ln /srv/not-theirs /home/leaver/hardlink
		ln -s /etc/shadow /home/leaver/symlink
	`)
	if _, err := s.Delete(ctx, "leaver", RemovalOptions{Home: HomeKeep, TransferTo: "taker", EndSessions: true}); err != nil {
		t.Fatal(err)
	}
	owner := func(path string) string {
		return strings.TrimSpace(liveShell(t, "stat", "-c", "%U", path))
	}
	for path, want := range map[string]string{
		"/home/leaver/mine":        "taker",
		"/srv/leaver-dir/sub/file": "taker",
		"/srv/leaver-dir/sub":      "taker",
		"/srv/not-theirs":          "root",
		"/etc/shadow":              "root",
	} {
		if got := owner(path); got != want {
			t.Errorf("%s belongs to %s, want %s", path, got, want)
		}
	}
}

func TestLiveAClosedAccountIsClosedBeforeTheLongWork(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	if err := s.Create(ctx, CreateOptions{Username: "closing", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	// A sudo rule that cannot be rewritten stops the delete with nothing destroyed.
	liveShell(t, "sh", "-c", `printf 'closing ALL=(ALL) ALL\n' > /etc/sudoers.d/live-closing; chmod 440 /etc/sudoers.d/live-closing`)
	if _, err := s.Delete(ctx, "closing", RemovalOptions{Home: HomeKeep}); err != nil {
		t.Fatal(err)
	}
	if out := liveShell(t, "cat", "/etc/sudoers.d/live-closing"); strings.Contains(out, "closing") {
		t.Fatalf("the rule outlived the account: %q", out)
	}
	liveShell(t, "visudo", "--check")
	_ = os.Remove("/etc/sudoers.d/live-closing")
}

func TestLiveSudoAgreesAboutTheRulesThatUsedToHide(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	if err := s.Create(ctx, CreateOptions{Username: "hider", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, CreateOptions{Username: "sharer", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	const file = "/etc/sudoers.d/live-hider"
	defer os.Remove(file)
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0o440); err != nil {
			t.Fatal(err)
		}
		liveShell(t, "visudo", "--check", "--file", file)
	}
	for _, rule := range []string{
		"hider ALL=(ALL) NOPASSWD: ALL, (nobody) /bin/true\n",
		"# a comment \\\nhider ALL=(ALL) NOPASSWD: ALL\n",
		"ADM ALL=(ALL) NOPASSWD: ALL\nUser_Alias ADM = hider\n",
		"\"hider\" ALL=(ALL) NOPASSWD: ALL\n",
		"hider ALL=(ALL) CWD=* NOPASSWD: ALL\n",
		"hider ALL=/bin/ls : ALL=(ALL) NOPASSWD: ALL\n",
	} {
		write(rule)
		out := liveShell(t, "sudo", "-l", "-U", "hider")
		if !strings.Contains(out, "NOPASSWD: ALL") {
			t.Fatalf("sudo itself does not read this as root, so the test is wrong: %q\n%s", rule, out)
		}
		u, err := s.Get(ctx, "hider")
		if err != nil {
			t.Fatal(err)
		}
		full := false
		for _, route := range u.RootRoutes {
			full = full || (route.Kind == "sudo" && !route.Limited && route.NoPassword)
		}
		if !full {
			t.Errorf("the page does not see root in %q: %+v", rule, u.RootRoutes)
		}
	}
	// The everyday spelling of a shared rule, through the real visudo.
	write("hider, sharer ALL=(ALL) ALL\n")
	for _, rule := range s.sudoRules() {
		if rule.File == file {
			if err := s.RemoveSudoRule(ctx, "hider", rule.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if out := liveShell(t, "cat", file); !strings.Contains(out, "sharer ALL=(ALL) ALL") || strings.Contains(out, "hider") {
		t.Fatalf("file = %q", out)
	}
	liveShell(t, "visudo", "--check")
}

func TestLiveKeysSSHDHonoursAreKeysThePageCounts(t *testing.T) {
	s := liveHost(t)
	ctx := context.Background()
	if err := s.Create(ctx, CreateOptions{Username: "twokeys", Shell: "/bin/bash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPassword(ctx, "twokeys", "throwaway-password-1"); err != nil {
		t.Fatal(err)
	}
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	liveShell(t, "sh", "-c", `mkdir -p /home/twokeys/.ssh && chmod 700 /home/twokeys/.ssh`)
	if err := os.WriteFile("/home/twokeys/.ssh/authorized_keys2", ssh.MarshalAuthorizedKey(sshPub), 0o600); err != nil {
		t.Fatal(err)
	}
	liveShell(t, "chown", "-R", "twokeys:", "/home/twokeys/.ssh")
	if !canSignIn("twokeys", ssh.PublicKeys(signer)) {
		t.Fatal("sshd does not read authorized_keys2 here, so this test proves nothing")
	}
	agree(t, s, "twokeys", "", signer, true, true)
	keys, _, err := s.KeysWithUse(ctx, "twokeys")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].File != "/home/twokeys/.ssh/authorized_keys2" {
		t.Fatalf("keys = %+v", keys)
	}
	// A link where the file should be: sshd will read it, and this must not call that zero.
	liveShell(t, "sh", "-c", `rm /home/twokeys/.ssh/authorized_keys2; ln -s /home/twokeys/elsewhere /home/twokeys/.ssh/authorized_keys2`)
	u, err := s.Get(ctx, "twokeys")
	if err != nil {
		t.Fatal(err)
	}
	if !u.KeysUnknown || !u.SignIn.Key.Allowed {
		t.Fatalf("a link read as no keys: %+v %+v", u.KeysUnknown, u.SignIn.Key)
	}
}
