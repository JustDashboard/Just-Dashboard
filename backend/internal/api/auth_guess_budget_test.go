package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

const signInAsPassword = "Correct-Horse-Battery-9"

// Each form that asks for the password again spends the account's one budget,
// so moving between them does not multiply the guesses.
func TestEveryPasswordFormSpendsTheOneGuessBudget(t *testing.T) {
	forms := []struct{ path, body string }{
		{"/api/v1/account/password", `{"currentPassword":"wrong","newPassword":"New-Correct-Horse-8"}`},
		{"/api/v1/account/2fa/disable", `{"password":"wrong"}`},
		{"/api/v1/account/recovery-codes", `{"password":"wrong"}`},
	}
	for _, form := range forms {
		t.Run(form.path, func(t *testing.T) {
			s := testServer(t)
			c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "guesser", auth.RoleAdmin)}
			limited := false
			for range 20 {
				w := c.do(http.MethodPost, form.path, form.body, nil)
				if w.Code == http.StatusTooManyRequests {
					limited = true
					break
				}
				if w.Code != http.StatusUnauthorized {
					t.Fatalf("a wrong password answered %d: %s", w.Code, w.Body.String())
				}
			}
			if !limited {
				t.Fatal("twenty wrong passwords were all checked")
			}
			// The budget is the account's: the other forms are spent as well.
			for _, other := range forms {
				if w := c.do(http.MethodPost, other.path, other.body, nil); w.Code != http.StatusTooManyRequests {
					t.Errorf("%s after the budget = %d", other.path, w.Code)
				}
			}
		})
	}
}

func TestRegeneratingRecoveryCodesTakesThePassword(t *testing.T) {
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "holder", auth.RoleAdmin)}
	stored := func() (hashes []string) {
		rows, err := s.Store.DB.Query(`SELECT code_hash FROM recovery_codes ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var hash string
			if err := rows.Scan(&hash); err != nil {
				t.Fatal(err)
			}
			hashes = append(hashes, hash)
		}
		return hashes
	}
	before := stored()
	for name, body := range map[string]string{"no body": "", "no password": `{}`, "a wrong one": `{"password":"wrong"}`} {
		w := c.do(http.MethodPost, "/api/v1/account/recovery-codes", body, nil)
		if w.Code < http.StatusBadRequest {
			t.Errorf("%s answered %d: %s", name, w.Code, w.Body.String())
		}
	}
	if after := stored(); len(after) != len(before) || after[0] != before[0] {
		t.Fatal("a refused request replaced the codes")
	}
	entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: "auth.2fa.recovery.regenerate", OnlyFail: true})
	if err != nil || len(entries) == 0 || entries[0].Username != "holder" {
		t.Fatalf("the refusal was not audited: %+v, %v", entries, err)
	}

	w := c.do(http.MethodPost, "/api/v1/account/recovery-codes", `{"password":"`+signInAsPassword+`"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the right password answered %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		RecoveryCodes []string `json:"recoveryCodes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got.RecoveryCodes) != 10 {
		t.Fatalf("recovery codes = %v, %v", got.RecoveryCodes, err)
	}
	if after := stored(); after[0] == before[0] {
		t.Error("the codes were not replaced")
	}
	entries, _, err = s.Audit.List(t.Context(), audit.Filter{Action: "auth.2fa.recovery.regenerate"})
	if err != nil || len(entries) == 0 || !entries[0].Success {
		t.Fatalf("the regeneration was not audited: %+v, %v", entries, err)
	}
}

// The code's route says the lock in its own words, so the page can show them.
func TestAVerifyRefusedByTheLockAnswersIt(t *testing.T) {
	s := testServer(t)
	cookie := signInAs(t, s, "locked", auth.RoleAdmin)
	if _, err := s.Store.DB.Exec(`UPDATE users SET totp_locked_until = strftime('%s','now') + 900 WHERE username = 'locked'`); err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, h: s.Routes(), cookie: cookie}
	w := c.do(http.MethodPost, "/api/v1/auth/2fa/verify", `{"code":"000000"}`, nil)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("verify while locked = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != "second_factor_locked" {
		t.Errorf("body = %s, %v", w.Body.String(), err)
	}
}
