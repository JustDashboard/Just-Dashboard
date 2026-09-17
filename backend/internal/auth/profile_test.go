package auth

import (
	"errors"
	"strings"
	"testing"
)

// The installer asks for a username and the operator types "Wayy"; the account
// must not then introduce itself as "wayy". The sign-in key is lower-cased so
// a capital letter never refuses a login, and the shown name keeps the case.
func TestCreateUserKeepsTheTypedNameAndLowerCasesTheKey(t *testing.T) {
	svc, _ := newTestService(t, false)
	user, err := svc.CreateUser(t.Context(), "  Wayy ", testPassword, RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "wayy" || user.DisplayName != "Wayy" {
		t.Fatalf("username %q display %q, want wayy / Wayy", user.Username, user.DisplayName)
	}
	for _, typed := range []string{"wayy", "WAYY", "Wayy"} {
		if _, err := svc.Login(t.Context(), typed, testPassword, "127.0.0.1", "test"); err != nil {
			t.Fatalf("login as %q: %v", typed, err)
		}
	}
}

func TestSetProfileRenamesAndRefusesCollisions(t *testing.T) {
	svc, user := newTestService(t, false)
	other, err := svc.CreateUser(t.Context(), "Other", testPassword, RoleReadOnly, false)
	if err != nil {
		t.Fatal(err)
	}

	name, display := "Renamed", "The Renamed One"
	if err := svc.SetProfile(t.Context(), user.ID, Profile{Username: &name, DisplayName: &display}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "renamed" || got.DisplayName != display {
		t.Fatalf("after rename: %q / %q", got.Username, got.DisplayName)
	}
	if _, err := svc.Login(t.Context(), "RENAMED", testPassword, "127.0.0.1", "test"); err != nil {
		t.Fatalf("login under the new name: %v", err)
	}

	taken := "OTHER"
	err = svc.SetProfile(t.Context(), user.ID, Profile{Username: &taken})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("rename onto %q: err = %v, want a collision", other.Username, err)
	}
	blank := "   "
	if err := svc.SetProfile(t.Context(), user.ID, Profile{DisplayName: &blank}); err == nil {
		t.Fatal("a blank display name was accepted")
	}
	spaced := "two words"
	if err := svc.SetProfile(t.Context(), user.ID, Profile{Username: &spaced}); err == nil {
		t.Fatal("a username with a space was accepted")
	}
}

func TestAvatarRoundTrip(t *testing.T) {
	svc, user := newTestService(t, false)
	if _, _, err := svc.Avatar(t.Context(), user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("fresh account avatar err = %v, want ErrNotFound", err)
	}
	if user.AvatarVersion != 0 {
		t.Fatalf("fresh account avatar version = %d", user.AvatarVersion)
	}
	if err := svc.SetAvatar(t.Context(), user.ID, []byte{1, 2, 3}, "image/png"); err != nil {
		t.Fatal(err)
	}
	data, mimeType, err := svc.Avatar(t.Context(), user.ID)
	if err != nil || mimeType != "image/png" || len(data) != 3 {
		t.Fatalf("avatar = %v %q %v", data, mimeType, err)
	}
	got, _ := svc.UserByID(t.Context(), user.ID)
	if got.AvatarVersion == 0 {
		t.Fatal("avatar version not stamped")
	}
	if err := svc.SetAvatar(t.Context(), user.ID, make([]byte, MaxAvatarBytes+1), "image/png"); err == nil {
		t.Fatal("oversized avatar accepted")
	}
	if err := svc.ClearAvatar(t.Context(), user.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Avatar(t.Context(), user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cleared avatar err = %v, want ErrNotFound", err)
	}
}

func TestRevokeOtherSessionsKeepsTheCaller(t *testing.T) {
	svc, user := newTestService(t, false)
	var tokens []string
	for range 3 {
		login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, login.Token)
	}
	keep, _, err := svc.ResolveSession(t.Context(), tokens[0])
	if err != nil {
		t.Fatal(err)
	}
	n, err := svc.RevokeOtherSessions(t.Context(), user.ID, keep.ID)
	if err != nil || n != 2 {
		t.Fatalf("revoked %d, err %v; want 2", n, err)
	}
	if _, _, err := svc.ResolveSession(t.Context(), tokens[0]); err != nil {
		t.Fatalf("the caller's own session was revoked: %v", err)
	}
	for _, tok := range tokens[1:] {
		if _, _, err := svc.ResolveSession(t.Context(), tok); err == nil {
			t.Fatal("another session survived")
		}
	}
}
