package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestAccountProfileRejectsWholeInvalidPatch(t *testing.T) {
	s := testServer(t)
	c, user := signedIn(t, s, "operator")
	before, err := s.Auth.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, display := range []string{"   ", "first\nlast", strings.Repeat("x", 65)} {
		body, err := json.Marshal(map[string]string{"username": "renamed", "displayName": display})
		if err != nil {
			t.Fatal(err)
		}
		w := c.do(http.MethodPatch, "/api/v1/account/profile", string(body), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid display name %q: %d %s", display, w.Code, w.Body.String())
		}
		assertAccountUnchanged(t, s, before)
	}
}

func TestDashboardUserCreateValidatesDisplayNameBeforeInsert(t *testing.T) {
	s := testServer(t)
	c, _ := signedIn(t, s, "operator")
	for _, display := range []string{"   ", "first\nlast", strings.Repeat("x", 65)} {
		body, err := json.Marshal(createUserRequest{
			Username: "NewUser", DisplayName: display,
			Password: "Correct-Horse-Battery-9", Role: auth.RoleReadOnly,
		})
		if err != nil {
			t.Fatal(err)
		}
		w := c.do(http.MethodPost, "/api/v1/dashboard-users", string(body), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid display name %q: %d %s", display, w.Code, w.Body.String())
		}
		users, err := s.Auth.ListUsers(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 1 {
			t.Fatalf("rejected creation left an account: %+v", users)
		}
	}
	for _, tc := range []struct{ username, display, want string }{
		{"NewUser", "  New User  ", "New User"},
		{"AnotherUser", "", "AnotherUser"},
	} {
		body, err := json.Marshal(createUserRequest{
			Username: tc.username, DisplayName: tc.display,
			Password: "Correct-Horse-Battery-9", Role: auth.RoleReadOnly,
		})
		if err != nil {
			t.Fatal(err)
		}
		w := c.do(http.MethodPost, "/api/v1/dashboard-users", string(body), nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("valid creation: %d %s", w.Code, w.Body.String())
		}
		var user auth.User
		if err := json.Unmarshal(w.Body.Bytes(), &user); err != nil {
			t.Fatal(err)
		}
		if user.DisplayName != tc.want || user.Username != strings.ToLower(tc.username) || !user.MustChangePW {
			t.Fatalf("created account lost existing name or password behavior: %+v", user)
		}
	}
}

func TestDashboardUserUpdateRejectsWholeInvalidPatch(t *testing.T) {
	s := testServer(t)
	c, _ := signedIn(t, s, "operator")
	user, err := s.Auth.CreateUser(t.Context(), "target", "Correct-Horse-Battery-9", auth.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.Auth.Login(t.Context(), user.Username, "Correct-Horse-Battery-9", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Auth.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, body string }{
		{"display name", `{"username":"renamed","displayName":" "}`},
		{"role", `{"username":"renamed","displayName":"New Name","role":"unknown"}`},
		{"password", `{"username":"renamed","displayName":"New Name","role":"readonly","disabled":true,"password":"short"}`},
		{"collision", `{"username":"operator","displayName":"New Name","role":"readonly","password":"Replacement-Password-9"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := c.do(http.MethodPatch, "/api/v1/dashboard-users/"+itoa(user.ID), tc.body, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid update: %d %s", w.Code, w.Body.String())
			}
			assertAccountUnchanged(t, s, before)
			if !s.Auth.VerifyUserPassword(t.Context(), user.ID, "Correct-Horse-Battery-9") {
				t.Fatal("rejected edit changed the password")
			}
			if _, _, err := s.Auth.ResolveSession(t.Context(), login.Token); err != nil {
				t.Fatalf("rejected edit revoked the session: %v", err)
			}
		})
	}
}

func TestDashboardUserUpdateKeepsLastAdminAndProfileTogether(t *testing.T) {
	s := testServer(t)
	c, user := signedIn(t, s, "operator")
	before, err := s.Auth.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"username":"renamed","displayName":"New Name","role":"readonly"}`,
		`{"username":"renamed","displayName":"New Name","disabled":true}`,
	} {
		w := c.do(http.MethodPatch, "/api/v1/dashboard-users/"+itoa(user.ID), body, nil)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "last_admin") {
			t.Fatalf("last admin update: %d %s", w.Code, w.Body.String())
		}
		assertAccountUnchanged(t, s, before)
	}
}

func assertAccountUnchanged(t *testing.T, s *Server, before *auth.User) {
	t.Helper()
	after, err := s.Auth.UserByID(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *after != *before {
		t.Fatalf("rejected request changed the account: before=%+v after=%+v", before, after)
	}
}
