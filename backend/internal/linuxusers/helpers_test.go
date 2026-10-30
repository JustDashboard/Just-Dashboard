package linuxusers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixedNow is a day the fixtures below can name dates around.
var fixedNow = time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)

type recordedCall struct {
	stdin string
	name  string
	args  []string
}

// fakeHost stands in for the host's tools. With no answer every tool succeeds
// and prints nothing, which reads as an sshd on its defaults and no sign-ins.
type fakeHost struct {
	calls  []recordedCall
	answer func(stdin, name string, args ...string) (string, error)
}

func (f *fakeHost) run(_ context.Context, stdin, name string, args ...string) (string, error) {
	f.calls = append(f.calls, recordedCall{stdin, name, args})
	if f.answer != nil {
		return f.answer(stdin, name, args...)
	}
	return "", nil
}

func (f *fakeHost) ran(name string) bool {
	for _, call := range f.calls {
		if call.name == name {
			return true
		}
	}
	return false
}

func (f *fakeHost) lastCall(name string) *recordedCall {
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].name == name {
			return &f.calls[i]
		}
	}
	return nil
}

// fixtureHost writes files under a temporary /etc and returns a service reading
// it. Homes in a fixture's passwd name directories under the same temporary
// root, so nothing here ever reads a real account's keys.
func fixtureHost(t *testing.T, files map[string]string) (*Service, *fakeHost, string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeHost{}
	return &Service{etc: dir, run: fake.run, now: func() time.Time { return fixedNow }}, fake, dir
}

// stateOf assembles a reading the way Service.read does, from made-up accounts.
func stateOf(policy SSHPolicy, rules []SudoRule, users ...User) *accountState {
	for i := range users {
		users[i].RootRoutes = rootRoutesFor(users[i], rules)
		users[i].SignIn = evaluateSignIn(users[i], policy)
	}
	return &accountState{users: users, rules: rules, policy: policy}
}

// defaultPolicy is OpenSSH as a stock Ubuntu ships it.
func defaultPolicy() SSHPolicy {
	return SSHPolicy{Installed: true, Known: true, PasswordAuth: true, KeyAuth: true, UsePAM: true,
		PermitRoot: "prohibit-password"}
}
