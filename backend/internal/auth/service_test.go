package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
	"github.com/pquerna/otp/totp"
)

// newTestService is the service under one of the two two-factor policies.
func newTestService(t *testing.T, require2FA bool) (*Service, *User) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, sealer, time.Hour, time.Minute, require2FA)
	user, err := svc.CreateUser(t.Context(), "tester", testPassword, RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	return svc, user
}

const testPassword = "Correct-Horse-Battery-9"

func TestLoginKeepsASecondFactorOutstandingWhenItIsRequired(t *testing.T) {
	svc, user := newTestService(t, true)

	login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !login.NeedsEnroll || login.NeedsTOTP {
		t.Fatalf("new account login = enroll:%v totp:%v, want enrollment only", login.NeedsEnroll, login.NeedsTOTP)
	}
	session, _, err := svc.ResolveSession(t.Context(), login.Token)
	if err != nil {
		t.Fatal(err)
	}
	if session.TwoFAPassed {
		t.Fatal("password-only login created an elevated session")
	}
	if !svc.Require2FA() {
		t.Fatal("service reported mandatory two-factor as optional")
	}
}

// The policy this dashboard now ships with: an account that has not enrolled
// signs straight in, and the session it gets is a complete one rather than a
// stub that can only reach the enrolment routes.
func TestLoginCompletesWhenTwoFactorIsOptional(t *testing.T) {
	svc, user := newTestService(t, false)

	login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if login.NeedsEnroll || login.NeedsTOTP {
		t.Fatalf("optional-2FA login = enroll:%v totp:%v, want neither", login.NeedsEnroll, login.NeedsTOTP)
	}
	session, _, err := svc.ResolveSession(t.Context(), login.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !session.TwoFAPassed {
		t.Fatal("optional-2FA login left the session half-authenticated")
	}
	if svc.Require2FA() {
		t.Fatal("service reported optional two-factor as mandatory")
	}
}

// Enrolling is a promise the dashboard keeps whatever the policy says: a user
// who has an authenticator is asked for a code even where nobody made them
// set one up.
func TestEnrolledAccountIsAlwaysAskedForACode(t *testing.T) {
	svc, user := newTestService(t, false)
	if _, err := svc.BeginTOTPEnrollment(t.Context(), user.ID); err != nil {
		t.Fatal(err)
	}
	secret, err := svc.totpSecret(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmTOTPEnrollment(t.Context(), user.ID, code); err != nil {
		t.Fatal(err)
	}

	login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if !login.NeedsTOTP {
		t.Fatal("an enrolled account signed in without being asked for a code")
	}
	session, _, err := svc.ResolveSession(t.Context(), login.Token)
	if err != nil {
		t.Fatal(err)
	}
	if session.TwoFAPassed {
		t.Fatal("an enrolled account was elevated by its password alone")
	}
}

// Turning it off is the account holder's to do, and only where the install
// has not made it compulsory.
func TestDisableTOTPRefusedWhereTwoFactorIsRequired(t *testing.T) {
	svc, user := newTestService(t, true)
	if err := svc.DisableTOTP(t.Context(), user.ID); err != ErrTOTPRequired {
		t.Fatalf("DisableTOTP under a mandatory policy = %v, want ErrTOTPRequired", err)
	}
}
