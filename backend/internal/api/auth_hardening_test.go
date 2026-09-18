package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

func TestTemporaryPasswordRestrictsRoutesUntilChanged(t *testing.T) {
	s := testServer(t)
	const oldPassword = "Temporary-Credential-9"
	const newPassword = "Replacement-Credential-9"
	user, err := s.Auth.CreateUser(t.Context(), "temporary", oldPassword, auth.RoleAdmin, true)
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.Auth.Login(t.Context(), user.Username, oldPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, h: s.Routes(), cookie: httpx.SessionCookie + "=" + login.Token}
	status := c.do(http.MethodGet, "/api/v1/auth/session", "", nil)
	var got authStatus
	if err := json.Unmarshal(status.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if status.Code != http.StatusOK || got.Authenticated || !got.NeedsPasswordChange {
		t.Fatalf("pending password state = %d %s", status.Code, status.Body.String())
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/system/host", ""},
		{http.MethodGet, "/api/v1/terminal/", ""},
		{http.MethodPost, "/api/v1/tokens/", `{"name":"bypass","role":"admin"}`},
		{http.MethodPost, "/api/v1/auth/2fa/setup", "{}"},
	} {
		w := c.do(request.method, request.path, request.body, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("temporary password reached %s: %d %s", request.path, w.Code, w.Body.String())
		}
	}
	wrong := c.do(http.MethodPost, "/api/v1/account/password", `{"currentPassword":"wrong","newPassword":"`+newPassword+`"}`, nil)
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("bad password accepted: %d %s", wrong.Code, wrong.Body.String())
	}
	unchanged := c.do(http.MethodPost, "/api/v1/account/password", `{"currentPassword":"`+oldPassword+`","newPassword":"`+oldPassword+`"}`, nil)
	if unchanged.Code != http.StatusBadRequest {
		t.Fatalf("temporary password was retained: %d %s", unchanged.Code, unchanged.Body.String())
	}
	changed := c.do(http.MethodPost, "/api/v1/account/password", `{"currentPassword":"`+oldPassword+`","newPassword":"`+newPassword+`"}`, nil)
	if changed.Code != http.StatusNoContent {
		t.Fatalf("password change failed: %d %s", changed.Code, changed.Body.String())
	}
	if _, _, err := s.Auth.ResolveSession(t.Context(), login.Token); err == nil {
		t.Fatal("old session survived password replacement")
	}
	login, err = s.Auth.Login(t.Context(), user.Username, newPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	c.cookie = httpx.SessionCookie + "=" + login.Token
	if w := c.do(http.MethodGet, "/api/v1/account/sessions", "", nil); w.Code != http.StatusOK {
		t.Fatalf("normal access not restored: %d %s", w.Code, w.Body.String())
	}
}

func TestEnrolledAccountCannotUseEnrollmentToAvoidOTPBudget(t *testing.T) {
	s := testServer(t)
	signIn(t, s)
	login, err := s.Auth.Login(t.Context(), "tester", "Correct-Horse-Battery-9", "127.0.0.1", "pending")
	if err != nil || !login.NeedsTOTP {
		t.Fatalf("expected partial login: %v", err)
	}
	c := &client{t: t, h: s.Routes(), cookie: httpx.SessionCookie + "=" + login.Token}
	for range 5 {
		w := c.do(http.MethodPost, "/api/v1/auth/2fa/enable", `{"code":"invalid"}`, nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("enrolled account could confirm again: %d %s", w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/auth/2fa/verify", "/api/v1/auth/2fa/setup", "/api/v1/auth/2fa/enable"} {
		w := c.do(http.MethodPost, path, `{"code":"invalid"}`, nil)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("shared budget bypass at %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if w := c.do(http.MethodPost, "/api/v1/account/password", `{"currentPassword":"Correct-Horse-Battery-9","newPassword":"New-Correct-Horse-8"}`, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("password route accepted unpaid second factor: %d %s", w.Code, w.Body.String())
	}
}
