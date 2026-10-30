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

func TestEveryShapeOfThePasswordFieldReadsAsWhatIsBehindIt(t *testing.T) {
	cases := []struct {
		hash   string
		state  string
		locked bool
	}{
		{"!", PasswordNone, true},
		{"*", PasswordNone, true},
		{"!!", PasswordNone, true},
		{"!*", PasswordNone, true},
		{"*LK*", PasswordNone, true},
		{"!$y$j9T$salt$hash", PasswordLocked, true},
		{"$y$j9T$salt$hash", PasswordSet, false},
		{"", PasswordEmpty, false},
	}
	for _, c := range cases {
		entry := shadowEntry{Hash: c.hash}
		if got := entry.passwordState(); got != c.state {
			t.Errorf("%q: state %q, want %q", c.hash, got, c.state)
		}
		if got := entry.locked(); got != c.locked {
			t.Errorf("%q: locked %v, want %v", c.hash, got, c.locked)
		}
	}
}

func TestParseShadowReadsEveryFieldAndAnEmptyOneIsNotSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shadow")
	content := "ion:$y$x:20000:1:90:7:30:20500:\nbare:!\nnever:*:19000:0:99999:7:::\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, readable := parseShadow(path)
	if !readable {
		t.Fatal("the file is there")
	}
	ion := entries["ion"]
	if ion.LastChange != 20000 || ion.Min != 1 || ion.Max != 90 || ion.Warn != 7 || ion.Inactive != 30 || ion.Expire != 20500 {
		t.Fatalf("ion = %+v", ion)
	}
	if bare := entries["bare"]; bare.Expire != -1 || bare.Max != -1 {
		t.Fatalf("a short line's fields are not set: %+v", bare)
	}
	if _, readable := parseShadow(filepath.Join(t.TempDir(), "missing")); readable {
		t.Fatal("a missing file is not readable")
	}
}

func TestAgingSaysNeverRatherThanTheDateTheFileSpellsItAs(t *testing.T) {
	a := shadowEntry{LastChange: 20000, Min: 0, Max: 99999, Warn: 7, Inactive: -1}.aging()
	if a.MaxDays != nil || a.ExpiresOn != "" || a.InactiveDays != nil || a.MinDays != nil {
		t.Fatalf("aging = %+v", a)
	}
	if a.LastChanged == "" || a.MustChange {
		t.Fatalf("aging = %+v", a)
	}
	b := shadowEntry{LastChange: 20000, Max: 90, Inactive: 14}.aging()
	if b.MaxDays == nil || *b.MaxDays != 90 || b.ExpiresOn != dayString(20090) || b.InactiveDays == nil {
		t.Fatalf("aging = %+v", b)
	}
	if c := (shadowEntry{LastChange: 0, Max: -1, Inactive: -1}).aging(); !c.MustChange {
		t.Fatal("day zero is a password that must change")
	}
}

func TestAnAccountExpiresAtTheStartOfTheDayNamed(t *testing.T) {
	if !(shadowEntry{Expire: 1}).expired(20000) {
		t.Fatal("the disabled sentinel is long past")
	}
	if !(shadowEntry{Expire: 20000}).expired(20000) {
		t.Fatal("expires today means expired today")
	}
	if (shadowEntry{Expire: 20001}).expired(20000) {
		t.Fatal("expires tomorrow is still good today")
	}
	if (shadowEntry{Expire: -1}).expired(20000) {
		t.Fatal("no date never expires")
	}
}

// This is the bug the page shipped with. usermod prints a warning on a bare "!",
// leaves the file alone and exits 0.
func TestUnlockingABarePasswordIsRefusedWithoutAskingUsermod(t *testing.T) {
	for _, hash := range []string{"!", "*", "!!"} {
		s, fake, _ := fixtureHost(t, map[string]string{"shadow": "ubuntu:" + hash + ":20000:0:99999:7:::\n"})
		err := s.SetLocked(context.Background(), "ubuntu", false)
		if !errors.Is(err, ErrNothingToUnlock) {
			t.Fatalf("%q: err = %v", hash, err)
		}
		if fake.ran("usermod") {
			t.Fatalf("%q: usermod was asked", hash)
		}
	}
}

func TestUnlockingARealPasswordRunsUsermodAndChecksItWorked(t *testing.T) {
	s, fake, dir := fixtureHost(t, map[string]string{"shadow": "ion:!$y$x:20000:0:99999:7:::\n"})
	shadow := filepath.Join(dir, "shadow")
	fake.answer = func(_, name string, args ...string) (string, error) {
		if name == "usermod" {
			return "", os.WriteFile(shadow, []byte("ion:$y$x:20000:0:99999:7:::\n"), 0o600)
		}
		return "", nil
	}
	if err := s.SetLocked(context.Background(), "ion", false); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("usermod"); call == nil || call.args[0] != "--unlock" {
		t.Fatalf("call = %+v", call)
	}

	// The same call on a usermod that says yes and does nothing is caught.
	s, fake, _ = fixtureHost(t, map[string]string{"shadow": "ion:!$y$x:20000:0:99999:7:::\n"})
	if err := s.SetLocked(context.Background(), "ion", false); err == nil ||
		!strings.Contains(err.Error(), "did not change") {
		t.Fatalf("err = %v", err)
	}
	_ = fake
}

func TestRootCannotBeLockedButCanBeUnlocked(t *testing.T) {
	s, _, _ := fixtureHost(t, map[string]string{"shadow": "root:!$y$x:20000:0:99999:7:::\n"})
	if err := s.SetLocked(context.Background(), "root", true); !errors.Is(err, ErrProtected) {
		t.Fatalf("lock: %v", err)
	}
}

func TestSetPasswordGoesOverStdinAndNeverInTheArguments(t *testing.T) {
	s, fake, dir := fixtureHost(t, map[string]string{"shadow": "ion:!:20000:0:99999:7:::\n"})
	shadow := filepath.Join(dir, "shadow")
	fake.answer = func(_, name string, _ ...string) (string, error) {
		if name == "chpasswd" {
			return "", os.WriteFile(shadow, []byte("ion:$y$new:20391:0:99999:7:::\n"), 0o600)
		}
		return "", nil
	}
	secret := "correct horse battery"
	if err := s.SetPassword(context.Background(), "ion", secret); err != nil {
		t.Fatal(err)
	}
	call := fake.lastCall("chpasswd")
	if call == nil || call.stdin != "ion:"+secret+"\n" {
		t.Fatalf("call = %+v", call)
	}
	for _, c := range fake.calls {
		for _, arg := range c.args {
			if strings.Contains(arg, secret) {
				t.Fatalf("the password is in %s's arguments", c.name)
			}
		}
	}
}

func TestSetPasswordRefusesWhatCannotBeAPassword(t *testing.T) {
	s, fake, _ := fixtureHost(t, map[string]string{"shadow": "ion:!:20000:0:99999:7:::\n"})
	for _, bad := range []string{"short", "line\nbreak123", "nul\x00inside1"} {
		if err := s.SetPassword(context.Background(), "ion", bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	if fake.ran("chpasswd") {
		t.Fatal("chpasswd was asked")
	}
	// A second line would have set somebody else's password.
	if err := s.SetPassword(context.Background(), "ion", "longenough\nroot:owned"); err == nil {
		t.Fatal("a line break reached chpasswd")
	}
}

func TestAChpasswdThatChangesNothingIsAFailure(t *testing.T) {
	s, _, _ := fixtureHost(t, map[string]string{"shadow": "ion:!:20000:0:99999:7:::\n"})
	if err := s.SetPassword(context.Background(), "ion", "long enough now"); err == nil ||
		!strings.Contains(err.Error(), "did not change") {
		t.Fatalf("err = %v", err)
	}
}

func TestGeneratedPasswordsAreLongUnambiguousAndDifferent(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		password, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(password) != generatedLength {
			t.Fatalf("length %d", len(password))
		}
		if strings.ContainsAny(password, "0O1lI") {
			t.Fatalf("%q has an ambiguous character", password)
		}
		if err := validatePassword(password); err != nil {
			t.Fatal(err)
		}
		if seen[password] {
			t.Fatal("two generated passwords were the same")
		}
		seen[password] = true
	}
}

func TestAPasswordCannotBeRequiredToChangeOnAnAccountWithoutOne(t *testing.T) {
	s, fake, _ := fixtureHost(t, map[string]string{"shadow": "keys:!:20000:0:99999:7:::\nion:$y$x:20000:0:99999:7:::\n"})
	if err := s.RequirePasswordChange(context.Background(), "keys"); err == nil {
		t.Fatal("PAM would refuse this account's keys over an expired password it never had")
	}
	if fake.ran("chage") {
		t.Fatal("chage was asked")
	}
	if err := s.RequirePasswordChange(context.Background(), "ion"); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("chage"); call == nil || strings.Join(call.args, " ") != "--lastday 0 ion" {
		t.Fatalf("call = %+v", call)
	}
}

func accountsFixture(t *testing.T) (*Service, *fakeHost, string) {
	t.Helper()
	dir := t.TempDir()
	return fixtureHost(t, map[string]string{
		"passwd": "root:x:0:0:root:" + dir + "/root:/bin/bash\n" +
			"ion:x:1000:1000:Ion:" + dir + "/ion:/bin/bash\n",
		"group":   "root:x:0:\nion:x:1000:\nsudo:x:27:ion\n",
		"shadow":  "root:!:20000:0:99999:7:::\nion:$y$x:20000:0:99999:7:::\n",
		"shells":  "/bin/bash\n",
		"sudoers": "%sudo ALL=(ALL:ALL) ALL\n",
	})
}

func TestDisablingUsesTheExpiryDateAndEnablingClearsIt(t *testing.T) {
	s, fake, _ := accountsFixture(t)
	// ion is the only way in here, so make the guard's reading say otherwise.
	fake.answer = func(_, name string, _ ...string) (string, error) {
		if strings.HasSuffix(name, "sshd") {
			return "passwordauthentication no\npubkeyauthentication no\n", nil
		}
		return "", nil
	}
	if err := s.SetDisabled(context.Background(), "ion", true); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("chage"); call == nil || strings.Join(call.args, " ") != "--expiredate 1 ion" {
		t.Fatalf("call = %+v", call)
	}
	if err := s.SetDisabled(context.Background(), "ion", false); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("chage"); call == nil || strings.Join(call.args, " ") != "--expiredate -1 ion" {
		t.Fatalf("call = %+v", call)
	}
	if err := s.SetDisabled(context.Background(), "root", true); !errors.Is(err, ErrProtected) {
		t.Fatalf("root: %v", err)
	}
}

func TestAnExpiryDateMustBeADate(t *testing.T) {
	s, fake, _ := accountsFixture(t)
	if err := s.SetExpiry(context.Background(), "ion", "next tuesday"); err == nil {
		t.Fatal("accepted")
	}
	if fake.ran("chage") {
		t.Fatal("chage was asked")
	}
	if err := s.SetExpiry(context.Background(), "ion", "2027-01-01"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetExpiry(context.Background(), "ion", ""); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("chage"); strings.Join(call.args, " ") != "--expiredate -1 ion" {
		t.Fatalf("call = %+v", call)
	}
}

func TestAnExpiryInThePastIsDisablingAndIsGuardedLikeIt(t *testing.T) {
	s, fake, _ := fixtureHost(t, map[string]string{
		"passwd":  "ion:x:1000:1000::/nonexistent:/bin/bash\n",
		"group":   "ion:x:1000:\n",
		"shadow":  "ion:$y$x:20000:0:99999:7:::\n",
		"sudoers": "ion ALL=(ALL) ALL\n",
	})
	ctx := context.Background()
	if err := s.SetExpiry(ctx, "ion", "2001-01-01"); !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("a date in the past: %v", err)
	}
	if fake.ran("chage") {
		t.Fatal("chage was asked")
	}
	if err := s.SetExpiry(ctx, "ion", "2099-01-01"); err != nil {
		t.Fatalf("a date in the future: %v", err)
	}
}

func TestAgingNamesOnlyWhatChangedAndChecksTheRange(t *testing.T) {
	s, fake, _ := accountsFixture(t)
	maxDays, warn := 90, 14
	if err := s.SetAging(context.Background(), "ion", AgingChange{MaxDays: &maxDays, WarnDays: &warn}); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("chage"); strings.Join(call.args, " ") != "--maxdays 90 --warndays 14 ion" {
		t.Fatalf("call = %+v", call)
	}
	bad := 100000
	if err := s.SetAging(context.Background(), "ion", AgingChange{MaxDays: &bad}); err == nil {
		t.Fatal("accepted")
	}
	before := len(fake.calls)
	if err := s.SetAging(context.Background(), "ion", AgingChange{}); err != nil || len(fake.calls) != before {
		t.Fatalf("an empty change ran something: %v", err)
	}
}

func TestParseFaillockCountsTheValidRows(t *testing.T) {
	out := `ion:
When                Type  Source                                           Valid
2026-10-30 09:01:02 RHOST 203.0.113.9                                          V
2026-10-30 09:01:07 RHOST 203.0.113.9                                          I
2026-10-30 09:01:12 TTY   /dev/pts/0                                           V
`
	if got := parseFaillock(out); got != 2 {
		t.Fatalf("got %d", got)
	}
	if got := parseFaillock("ion:\nWhen Type Source Valid\n"); got != 0 {
		t.Fatalf("got %d", got)
	}
}

func TestAHostWithoutFaillockSaysSoRatherThanZero(t *testing.T) {
	s, fake, _ := accountsFixture(t)
	fake.answer = func(_, name string, _ ...string) (string, error) {
		return "", fmt.Errorf("%w: %s", ErrToolMissing, name)
	}
	if got := s.FailedAttempts(context.Background(), "ion"); got.Available {
		t.Fatalf("got %+v", got)
	}
	fake.answer = nil
	if got := s.FailedAttempts(context.Background(), "ion"); !got.Available || got.Count != 0 {
		t.Fatalf("got %+v", got)
	}
}
