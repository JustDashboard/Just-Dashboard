package linuxusers

import (
	"errors"
	"testing"
)

// The last way in, in one account of each shape.
func lastWayInState() *accountState {
	rules := []SudoRule{
		{ID: "group", Who: []string{"%sudo"}, Commands: []string{"ALL"}, AsRoot: true, Full: true},
		{ID: "direct", Who: []string{"ion"}, Commands: []string{"ALL"}, AsRoot: true, Full: true, NoPassword: true},
	}
	return stateOf(defaultPolicy(), rules,
		User{Username: "ion", UID: 1000, Shell: "/bin/bash", Password: PasswordNone, Locked: true, SSHKeys: 1},
		User{Username: "mira", UID: 1001, Shell: "/bin/bash", Password: PasswordSet, Groups: []string{"mira"}},
	)
}

func TestWayInIsSignInAndRootTogether(t *testing.T) {
	st := lastWayInState()
	if got := wayIn(st.users); len(got) != 1 || got[0] != "ion" {
		t.Fatalf("got %v", got)
	}
	// Sudo that asks for a password it does not have is not a way to root.
	st = stateOf(defaultPolicy(),
		[]SudoRule{{ID: "group", Who: []string{"%sudo"}, Commands: []string{"ALL"}, AsRoot: true, Full: true}},
		User{Username: "zoro", UID: 1001, Shell: "/bin/bash", Password: PasswordNone, Locked: true, SSHKeys: 1, Groups: []string{"sudo"}},
	)
	if got := wayIn(st.users); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	// Nor is a rule limited to some commands.
	st = stateOf(defaultPolicy(),
		[]SudoRule{{ID: "narrow", Who: []string{"ion"}, Commands: []string{"/bin/ls"}, AsRoot: true, NoPassword: true}},
		User{Username: "ion", UID: 1000, Shell: "/bin/bash", Password: PasswordSet},
	)
	if got := wayIn(st.users); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestEveryKindOfChangeThatClosesTheLastWayInIsRefused(t *testing.T) {
	cases := map[string]Change{
		"delete":        {Username: "ion", Delete: true},
		"disable":       {Username: "ion", Disabled: boolPtr(true)},
		"shell":         {Username: "ion", Shell: stringPtr("/usr/sbin/nologin")},
		"last key":      {Username: "ion", KeysRemoved: 1},
		"drop the rule": {Username: "ion", DropRule: "direct"},
	}
	for name, change := range cases {
		if err := judge(lastWayInState(), change); !errors.Is(err, ErrLastWayIn) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPasswordsAndGroupsCanCloseTheLastWayIn(t *testing.T) {
	st := stateOf(defaultPolicy(),
		[]SudoRule{{ID: "group", Who: []string{"%sudo"}, Commands: []string{"ALL"}, AsRoot: true, Full: true}},
		User{Username: "ion", UID: 1000, Shell: "/bin/bash", Password: PasswordSet, Groups: []string{"ion", "sudo"}},
	)
	if err := judge(st, Change{Username: "ion", Password: stringPtr(PasswordNone)}); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("remove password: %v", err)
	}
	if err := judge(st, Change{Username: "ion", Password: stringPtr(PasswordLocked)}); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("lock: %v", err)
	}
	if err := judge(st, Change{Username: "ion", Groups: &[]string{"ion"}}); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("leave sudo: %v", err)
	}
	if err := judge(st, Change{Group: "sudo", Members: &[]string{}}); !errors.Is(err, ErrLastWayIn) {
		t.Errorf("empty the group: %v", err)
	}
}

func TestAChangeThatLeavesAnotherWayInIsAllowed(t *testing.T) {
	st := stateOf(defaultPolicy(),
		[]SudoRule{{ID: "group", Who: []string{"%sudo"}, Commands: []string{"ALL"}, AsRoot: true, Full: true, NoPassword: true}},
		User{Username: "ion", UID: 1000, Shell: "/bin/bash", Password: PasswordSet, Groups: []string{"sudo"}},
		User{Username: "mira", UID: 1001, Shell: "/bin/bash", SSHKeys: 1, Password: PasswordNone, Groups: []string{"sudo"}},
	)
	if err := judge(st, Change{Username: "ion", Delete: true}); err != nil {
		t.Fatal(err)
	}
	if err := judge(st, Change{Username: "mira", KeysRemoved: 1}); err != nil {
		t.Fatal(err)
	}
	// Removing one of an account's two keys leaves a key.
	st = stateOf(defaultPolicy(), []SudoRule{{ID: "d", Who: []string{"ion"}, Commands: []string{"ALL"}, AsRoot: true, Full: true, NoPassword: true}},
		User{Username: "ion", UID: 1000, Shell: "/bin/bash", SSHKeys: 2, Password: PasswordNone})
	if err := judge(st, Change{Username: "ion", KeysRemoved: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestAHostWithNoWayInIsNotStoppedFromMendingIt(t *testing.T) {
	st := stateOf(defaultPolicy(), []SudoRule{},
		User{Username: "ubuntu", UID: 1000, Shell: "/bin/bash", Password: PasswordNone, Locked: true},
		User{Username: "zoro", UID: 1001, Shell: "/bin/bash", Password: PasswordNone, Locked: true, Groups: []string{"sudo"}},
	)
	for _, change := range []Change{
		{Username: "zoro", Delete: true},
		{Username: "ubuntu", Disabled: boolPtr(true)},
		{Username: "ubuntu", Shell: stringPtr("/usr/sbin/nologin")},
	} {
		if err := judge(st, change); err != nil {
			t.Fatalf("%+v: %v", change, err)
		}
	}
}

func TestRootByPasswordIsAWayInAndTheOnlyOneOnThisKindOfHost(t *testing.T) {
	policy := defaultPolicy()
	policy.PermitRoot = "yes"
	st := stateOf(policy, []SudoRule{},
		User{Username: "root", UID: 0, Shell: "/bin/bash", Password: PasswordSet},
		User{Username: "ubuntu", UID: 1000, Shell: "/bin/bash", Password: PasswordNone, Locked: true},
	)
	if got := wayIn(st.users); len(got) != 1 || got[0] != "root" {
		t.Fatalf("got %v", got)
	}
	if err := judge(st, Change{Username: "root", Password: stringPtr(PasswordNone)}); !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyChangeDoesNotTouchTheReadingItWasGiven(t *testing.T) {
	st := lastWayInState()
	_ = applyChange(st, Change{Username: "ion", Password: stringPtr(PasswordSet), KeysRemoved: 1, DropRule: "direct"})
	if st.users[0].SSHKeys != 1 || st.users[0].Password != PasswordNone {
		t.Fatalf("users changed: %+v", st.users[0])
	}
	if len(st.rules) != 2 || !contains(st.rules[1].Who, "ion") {
		t.Fatalf("rules changed: %+v", st.rules)
	}
}
