package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestSessionMethodDistinguishesAccessCompletionFromFactorProof(t *testing.T) {
	svc, user := newTestService(t, false)
	login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod := func(token, method string, completed bool) {
		t.Helper()
		session, _, err := svc.ResolveSession(t.Context(), token)
		if err != nil || session.AuthMethod != method || session.TwoFAPassed != completed {
			t.Fatalf("session method: %+v, %v; want %s complete=%v", session, err, method, completed)
		}
		listed, err := svc.ListSessions(t.Context(), user.ID, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range listed {
			if item.ID == session.ID && item.AuthMethod != method {
				t.Fatalf("session listing lost authentication method: %+v", item)
			}
		}
	}
	assertMethod(login.Token, "password", true)
	// Completing a password login after optional policy changes proves no code.
	if _, err := svc.st.DB.Exec(`UPDATE sessions SET twofa_passed = 0 WHERE id = ?`, login.SessionID); err != nil {
		t.Fatal(err)
	}
	assertMethod(login.Token, "password", true)
	enrollment, err := svc.BeginTOTPEnrollment(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	codes, err := svc.ConfirmTOTPEnrollmentForSession(t.Context(), user.ID, login.SessionID, code)
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(login.Token, "totp", true)
	second, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	assertMethod(second.Token, "password", false)
	if err := svc.VerifySecondFactor(t.Context(), second.SessionID, user.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
	assertMethod(second.Token, "recovery", true)
	if _, err := svc.st.DB.Exec(`UPDATE users SET totp_last_step = -1 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	third, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifySecondFactor(t.Context(), third.SessionID, user.ID, code); err != nil {
		t.Fatal(err)
	}
	assertMethod(third.Token, "totp", true)
}
