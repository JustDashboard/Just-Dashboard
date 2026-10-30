package linuxusers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGroupsAreReadWithWhatMembershipHandsOver(t *testing.T) {
	s, _, _ := hostAccounts(t)
	groups, err := s.ListGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Group{}
	for _, g := range groups {
		byName[g.Name] = g
	}
	sudo := byName["sudo"]
	if strings.Join(sudo.Members, ",") != "ubuntu,zoro" || !sudo.Protected || !sudo.System || len(sudo.SudoRules) != 1 {
		t.Fatalf("sudo = %+v", sudo)
	}
	if lxd := byName["lxd"]; !lxd.RootEquivalent || lxd.Protected {
		t.Fatalf("lxd = %+v", lxd)
	}
	private := byName["ubuntu"]
	if len(private.Primary) != 1 || private.Primary[0] != "ubuntu" || private.System {
		t.Fatalf("ubuntu = %+v", private)
	}
	if groups[0].System {
		t.Fatalf("people's groups come first: %+v", groups[0])
	}
}

func TestProtectedGroupsKeepTheirNameAndNumberAndStayPut(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	ctx := context.Background()
	for _, g := range []string{"sudo", "root"} {
		if err := s.RenameGroup(ctx, g, "other"); !errors.Is(err, ErrProtected) {
			t.Errorf("rename %s: %v", g, err)
		}
		if err := s.SetGroupID(ctx, g, 2000); !errors.Is(err, ErrProtected) {
			t.Errorf("gid %s: %v", g, err)
		}
		if err := s.DeleteGroup(ctx, g); !errors.Is(err, ErrProtected) {
			t.Errorf("delete %s: %v", g, err)
		}
	}
	if fake.ran("groupmod") || fake.ran("groupdel") {
		t.Fatal("a tool was asked")
	}
}

func TestAGroupIsNotDeletedOutFromUnderTheAccountsItIsMainTo(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	err := s.DeleteGroup(context.Background(), "ubuntu")
	if err == nil || !strings.Contains(err.Error(), "main group of ubuntu") {
		t.Fatalf("err = %v", err)
	}
	if fake.ran("groupdel") {
		t.Fatal("groupdel was asked")
	}
}

func TestAGroupASudoRuleNamesCannotBeRenamedOrDeleted(t *testing.T) {
	s, fake, etc := fixtureHostWithGroupRule(t)
	_ = etc
	ctx := context.Background()
	if err := s.RenameGroup(ctx, "ops", "ops2"); err == nil || !strings.Contains(err.Error(), "sudo rule") {
		t.Fatalf("rename: %v", err)
	}
	if err := s.DeleteGroup(ctx, "ops"); err == nil || !strings.Contains(err.Error(), "sudo rule") {
		t.Fatalf("delete: %v", err)
	}
	if fake.ran("groupmod") || fake.ran("groupdel") {
		t.Fatal("a tool was asked")
	}
}

func fixtureHostWithGroupRule(t *testing.T) (*Service, *fakeHost, string) {
	t.Helper()
	return fixtureHost(t, map[string]string{
		"passwd":  "ion:x:1000:1000::/nonexistent:/bin/bash\n",
		"group":   "ion:x:1000:\nops:x:1500:ion\n",
		"shadow":  "ion:$y$x:20000:0:99999:7:::\n",
		"sudoers": "%ops ALL=(ALL) ALL\n",
	})
}

func TestEmptyingTheGroupThatHoldsTheLastWayInIsRefused(t *testing.T) {
	s, fake, _ := fixtureHostWithGroupRule(t)
	err := s.SetGroupMembers(context.Background(), "ops", []string{})
	if !errors.Is(err, ErrLastWayIn) {
		t.Fatalf("err = %v", err)
	}
	if fake.ran("gpasswd") {
		t.Fatal("gpasswd was asked")
	}
}

func TestMembersAreWrittenThroughGpasswd(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	if err := s.SetGroupMembers(context.Background(), "lxd", []string{"ubuntu", "zoro"}); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("gpasswd"); call == nil || strings.Join(call.args, " ") != "--members ubuntu,zoro lxd" {
		t.Fatalf("call = %+v", call)
	}
	if err := s.SetGroupMembers(context.Background(), "lxd", []string{"bad name"}); err == nil {
		t.Fatal("accepted")
	}
	if err := s.SetGroupMembers(context.Background(), "ghost", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreatingAGroup(t *testing.T) {
	s, fake, _ := hostAccounts(t)
	ctx := context.Background()
	if err := s.CreateGroup(ctx, "ops", -1, false); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("groupadd"); strings.Join(call.args, " ") != "ops" {
		t.Fatalf("call = %+v", call)
	}
	if err := s.CreateGroup(ctx, "svc", 950, true); err != nil {
		t.Fatal(err)
	}
	if call := fake.lastCall("groupadd"); strings.Join(call.args, " ") != "--gid 950 --system svc" {
		t.Fatalf("call = %+v", call)
	}
	if err := s.CreateGroup(ctx, "bad name", -1, false); err == nil {
		t.Fatal("accepted")
	}
}
