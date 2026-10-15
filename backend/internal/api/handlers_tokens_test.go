package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestDeleteTokenRecordRequiresAnInactiveKeyAndKeepsAudit(t *testing.T) {
	c, s := newClient(t)
	minted := c.do(http.MethodPost, "/api/v1/tokens/", `{"name":"retired job","role":"readonly","ttlDays":30}`, nil)
	var result struct {
		Token  auth.APIToken `json:"token"`
		Secret string        `json:"secret"`
	}
	if err := json.Unmarshal(minted.Body.Bytes(), &result); err != nil || minted.Code != http.StatusCreated {
		t.Fatalf("create = %d %s: %v", minted.Code, minted.Body.String(), err)
	}
	path := "/api/v1/tokens/" + strconv.FormatInt(result.Token.ID, 10)
	if w := c.do(http.MethodDelete, path+"/record", "", nil); w.Code != http.StatusConflict {
		t.Fatalf("deleted active key: %d %s", w.Code, w.Body.String())
	}
	if _, _, _, err := s.Auth.ResolveAPIToken(t.Context(), result.Secret); err != nil {
		t.Fatalf("active key changed: %v", err)
	}
	if w := c.do(http.MethodDelete, path, "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodDelete, path+"/record", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodDelete, path+"/record", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("repeat = %d %s", w.Code, w.Body.String())
	}
	list := c.do(http.MethodGet, "/api/v1/tokens/", "", nil)
	if strings.Contains(list.Body.String(), "retired job") {
		t.Fatal("record survived deletion")
	}
	trail := c.do(http.MethodGet, "/api/v1/audit/?action=dashboard.token", "", nil)
	for _, action := range []string{"dashboard.token.create", "dashboard.token.revoke", "dashboard.token.delete"} {
		if !strings.Contains(trail.Body.String(), action) {
			t.Fatalf("audit lost %s: %s", action, trail.Body.String())
		}
	}
}

func TestDeleteTokenRecordAcceptsExpiredKeysFromOtherAccounts(t *testing.T) {
	c, s := newClient(t)
	other, err := s.Auth.CreateUser(t.Context(), "other-owner", "Correct-Horse-Battery-9", auth.RoleReadOnly, false)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := s.Auth.CreateAPIToken(t.Context(), other, "expired", auth.RoleReadOnly, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.ExecContext(t.Context(), `UPDATE api_tokens SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Second).Unix(), token.ID); err != nil {
		t.Fatal(err)
	}
	if w := c.do(http.MethodDelete, "/api/v1/tokens/"+strconv.FormatInt(token.ID, 10)+"/record", "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("expired delete = %d %s", w.Code, w.Body.String())
	}
}

func TestDeleteTokenRecordRejectsLimitedAccountsAndBearerTokens(t *testing.T) {
	s := testServer(t)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	minted := admin.do(http.MethodPost, "/api/v1/tokens/", `{"name":"machine","role":"admin"}`, nil)
	var result struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(minted.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w := admin.do(http.MethodDelete, "/api/v1/tokens/1/record", "", map[string]string{"Cookie": "", "Authorization": "Bearer " + result.Secret}); w.Code != http.StatusForbidden {
		t.Fatalf("bearer delete = %d %s", w.Code, w.Body.String())
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, string(role), role)}
		if w := c.do(http.MethodDelete, "/api/v1/tokens/1/record", "", nil); w.Code != http.StatusForbidden {
			t.Fatalf("%s delete = %d %s", role, w.Code, w.Body.String())
		}
	}
}

func TestTokenReplacementHandoffKeepsBothUntilExplicitRevoke(t *testing.T) {
	c, s := newClient(t)
	type mintedKey struct {
		Token  auth.APIToken `json:"token"`
		Secret string        `json:"secret"`
	}
	create := func() mintedKey {
		t.Helper()
		w := c.do(http.MethodPost, "/api/v1/tokens/", `{"name":"github-actions","role":"limited","ttlDays":7}`, nil)
		var key mintedKey
		if err := json.Unmarshal(w.Body.Bytes(), &key); err != nil || w.Code != http.StatusCreated {
			t.Fatalf("create = %d %s: %v", w.Code, w.Body.String(), err)
		}
		return key
	}
	previous, replacement := create(), create()
	if previous.Secret == replacement.Secret || previous.Token.ID == replacement.Token.ID {
		t.Fatal("replacement reused the previous credential")
	}
	for _, key := range []mintedKey{previous, replacement} {
		if _, user, role, err := s.Auth.ResolveAPIToken(t.Context(), key.Secret); err != nil || user.ID != key.Token.UserID || role != auth.RoleLimited {
			t.Fatalf("handoff credential did not resolve as its owner and scope: %v", err)
		}
	}
	if w := c.do(http.MethodDelete, "/api/v1/tokens/"+strconv.FormatInt(previous.Token.ID, 10), "", nil); w.Code != http.StatusNoContent {
		t.Fatalf("revoke previous = %d %s", w.Code, w.Body.String())
	}
	if _, _, _, err := s.Auth.ResolveAPIToken(t.Context(), previous.Secret); err == nil {
		t.Fatal("previous credential survived revocation")
	}
	if _, _, _, err := s.Auth.ResolveAPIToken(t.Context(), replacement.Secret); err != nil {
		t.Fatalf("replacement stopped working: %v", err)
	}
}
