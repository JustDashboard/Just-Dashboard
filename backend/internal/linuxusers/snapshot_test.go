package linuxusers

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDiffSnapshotsNamesWhatAppeared(t *testing.T) {
	before := &Snapshot{
		Accounts:   []string{"root", "ubuntu"},
		Keys:       []string{"root SHA256:a laptop"},
		SudoRules:  []string{"r1 /etc/sudoers %sudo ALL=(ALL:ALL) ALL"},
		RootSignIn: time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC),
	}
	after := &Snapshot{
		Accounts: []string{"mallory", "root", "ubuntu"},
		Keys: []string{
			"root SHA256:a laptop",
			"root SHA256:b my second laptop",
			"mallory SHA256:c",
		},
		SudoRules: []string{
			"r1 /etc/sudoers %sudo ALL=(ALL:ALL) ALL",
			"r2 /etc/sudoers.d/x mallory ALL=(ALL) NOPASSWD: ALL",
		},
		RootSignIn:   time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC),
		RootSignInIP: "198.51.100.23",
	}
	changes := DiffSnapshots(before, after)
	if len(changes) != 5 {
		t.Fatalf("changes = %+v", changes)
	}
	bySubject := map[string]AccountChange{}
	for _, change := range changes {
		bySubject[change.Subject] = change
	}
	if got := bySubject["account:mallory"]; got.Label != "mallory" || !strings.Contains(got.Detail, "new account") {
		t.Fatalf("account = %+v", got)
	}
	if got := bySubject["key:root:SHA256:b"]; !strings.Contains(got.Detail, "my second laptop") || got.Label != "root" {
		t.Fatalf("key = %+v", got)
	}
	if got := bySubject["key:mallory:SHA256:c"]; strings.Contains(got.Detail, ", ,") || !strings.Contains(got.Detail, "SHA256:c") {
		t.Fatalf("key with no label = %+v", got)
	}
	if got := bySubject["sudo:r2"]; !strings.Contains(got.Detail, "mallory ALL=(ALL) NOPASSWD: ALL") || got.Label != "/etc/sudoers.d/x" {
		t.Fatalf("rule = %+v", got)
	}
	if got := bySubject["root:2026-10-30T10:00:00Z"]; !strings.Contains(got.Detail, "198.51.100.23") {
		t.Fatalf("root = %+v", got)
	}
}

func TestWhatDisappearedAndWhatDidNotChangeIsNotAnAlarm(t *testing.T) {
	before := &Snapshot{Accounts: []string{"a", "b"}, Keys: []string{"a SHA256:x"}, SudoRules: []string{"r f t"}}
	after := &Snapshot{Accounts: []string{"a"}}
	if changes := DiffSnapshots(before, after); len(changes) != 0 {
		t.Fatalf("changes = %+v", changes)
	}
	if changes := DiffSnapshots(before, before); len(changes) != 0 {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestRelabellingAKeyIsNotANewKey(t *testing.T) {
	before := &Snapshot{Keys: []string{"ion SHA256:x old label"}}
	after := &Snapshot{Keys: []string{"ion SHA256:x new label"}}
	if changes := DiffSnapshots(before, after); len(changes) != 0 {
		t.Fatalf("changes = %+v", changes)
	}
	// The same key on another account is a new way in.
	after = &Snapshot{Keys: []string{"ion SHA256:x old label", "mira SHA256:x old label"}}
	if changes := DiffSnapshots(before, after); len(changes) != 1 || changes[0].Label != "mira" {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestTakeSnapshotReadsTheHost(t *testing.T) {
	s, _, _ := hostAccounts(t)
	snapshot, err := s.TakeSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(snapshot.Accounts, "ubuntu") || !contains(snapshot.Accounts, "daemon") {
		t.Fatalf("accounts = %v", snapshot.Accounts)
	}
	if snapshot.RootSignIn.IsZero() || snapshot.RootSignInIP != "198.51.100.23" {
		t.Fatalf("root = %v %q", snapshot.RootSignIn, snapshot.RootSignInIP)
	}
	if len(snapshot.SudoRules) != 3 {
		t.Fatalf("rules = %v", snapshot.SudoRules)
	}
}

func TestBuiltinTemplatesAreValidAndAnOperatorsCannotShadowThem(t *testing.T) {
	if _, err := CleanTemplates([]Template{{ID: "mine", Name: "Mine", Sudo: SudoPassword}}); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]Template{
		"shadow":  {ID: "person", Name: "Person"},
		"no name": {ID: "x", Name: " "},
		"bad id":  {ID: "../x", Name: "X"},
		"group":   {ID: "x", Name: "X", Groups: []string{"not a group"}},
		"sudo":    {ID: "x", Name: "X", Sudo: "always"},
	} {
		if _, err := CleanTemplates([]Template{bad}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := CleanTemplates([]Template{{ID: "x", Name: "X"}, {ID: "x", Name: "Y"}}); err == nil {
		t.Error("a repeated id was accepted")
	}
	got, err := CleanTemplates([]Template{{ID: "x", Name: "  X  ", Builtin: true}})
	if err != nil || got[0].Builtin || got[0].Name != "X" || got[0].Sudo != SudoNone || got[0].Groups == nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	for _, template := range BuiltinTemplates() {
		if !template.Builtin || template.ID == "" {
			t.Fatalf("template = %+v", template)
		}
	}
}
