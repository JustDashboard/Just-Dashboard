package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// A burst of wrong passwords each read the same count; only an increment in
// the write itself makes five of them five.
func TestAConcurrentBurstOfWrongPasswordsLocksTheAccount(t *testing.T) {
	svc, user := newTestService(t, false)
	var wg sync.WaitGroup
	results := make(chan error, maxFailedLogins)
	for range maxFailedLogins {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Login(t.Context(), user.Username, "Wrong-Password-Guess-1", "127.0.0.1", "burst")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	locked := 0
	for err := range results {
		switch {
		case errors.Is(err, ErrAccountLocked):
			locked++
		case !errors.Is(err, ErrInvalidCredentials):
			t.Fatalf("a wrong password answered %v", err)
		}
	}
	if locked == 0 {
		t.Error("no attempt in the burst was told the account locked")
	}
	if _, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "after"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("the right password after the burst = %v, want locked", err)
	}
}

func TestFourWrongPasswordsDoNotLockAndASuccessStartsTheCountAgain(t *testing.T) {
	svc, user := newTestService(t, false)
	for range 2 {
		for range maxFailedLogins - 1 {
			if _, err := svc.Login(t.Context(), user.Username, "Wrong-Password-Guess-1", "127.0.0.1", "t"); !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("wrong password = %v", err)
			}
		}
		if _, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "t"); err != nil {
			t.Fatalf("the right password before the limit = %v", err)
		}
	}
}

// enrolledSession is an enrolled account with a sign-in that owes its code.
func enrolledSession(t *testing.T, svc *Service, user *User) (secret string, recovery []string, session string) {
	t.Helper()
	first, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "enrol")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := svc.BeginTOTPEnrollment(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	recovery, err = svc.ConfirmTOTPEnrollmentForSession(t.Context(), user.ID, first.SessionID, code)
	if err != nil {
		t.Fatal(err)
	}
	// The enrolment spent this step's code; let a later one in.
	if _, err := svc.st.DB.Exec(`UPDATE users SET totp_last_step = -1 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	second, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "sign-in")
	if err != nil {
		t.Fatal(err)
	}
	return enrollment.Secret, recovery, second.SessionID
}

func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestWrongCodesLockTheSecondFactorForLockoutWindow(t *testing.T) {
	svc, user := newTestService(t, false)
	secret, recovery, session := enrolledSession(t, svc, user)
	for i := range maxFailedCodes - 1 {
		// A recovery code's shape counts as much as an authenticator's.
		guess := "000000"
		if i%2 == 1 {
			guess = "not-a-recovery-code"
		}
		if err := svc.VerifySecondFactor(t.Context(), session, user.ID, guess); !errors.Is(err, ErrInvalidTOTP) {
			t.Fatalf("guess %d = %v", i, err)
		}
	}
	if err := svc.VerifySecondFactor(t.Context(), session, user.ID, "000000"); !errors.Is(err, ErrSecondFactorLocked) {
		t.Fatalf("the last guess = %v, want locked", err)
	}
	var lockedUntil int64
	if err := svc.st.DB.QueryRow(`SELECT totp_locked_until FROM users WHERE id = ?`, user.ID).Scan(&lockedUntil); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(time.Unix(lockedUntil, 0)); left < lockoutWindow-time.Minute || left > lockoutWindow {
		t.Errorf("locked for %v, want %v", left, lockoutWindow)
	}
	for name, right := range map[string]string{"authenticator": currentCode(t, secret), "recovery": recovery[0]} {
		if err := svc.VerifySecondFactor(t.Context(), session, user.ID, right); !errors.Is(err, ErrSecondFactorLocked) {
			t.Errorf("a right %s code while locked = %v", name, err)
		}
	}
	// Signing in again with the password does not buy more guesses.
	again, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "again")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifySecondFactor(t.Context(), again.SessionID, user.ID, currentCode(t, secret)); !errors.Is(err, ErrSecondFactorLocked) {
		t.Errorf("a new session's right code while locked = %v", err)
	}

	if _, err := svc.st.DB.Exec(`UPDATE users SET totp_locked_until = ? WHERE id = ?`, time.Now().Add(-time.Second).Unix(), user.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifySecondFactor(t.Context(), again.SessionID, user.ID, recovery[0]); err != nil {
		t.Fatalf("the unused recovery code after the lock = %v", err)
	}
}

func TestAConcurrentBurstOfWrongCodesLocksTheSecondFactor(t *testing.T) {
	svc, user := newTestService(t, false)
	secret, _, session := enrolledSession(t, svc, user)
	var wg sync.WaitGroup
	for range maxFailedCodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = svc.VerifySecondFactor(t.Context(), session, user.ID, "000000")
		}()
	}
	wg.Wait()
	if err := svc.VerifySecondFactor(t.Context(), session, user.ID, currentCode(t, secret)); !errors.Is(err, ErrSecondFactorLocked) {
		t.Fatalf("the right code after the burst = %v, want locked", err)
	}
}

// A guess is spent whether or not anybody stays for the answer.
func TestAWrongCodeFromAClientThatLeftStillCounts(t *testing.T) {
	svc, user := newTestService(t, false)
	_, _, session := enrolledSession(t, svc, user)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := svc.VerifySecondFactor(ctx, session, user.ID, "000000"); !errors.Is(err, ErrInvalidTOTP) {
		t.Fatalf("a wrong code from a cancelled request = %v", err)
	}
	var count int
	if err := svc.st.DB.QueryRow(`SELECT totp_failed_count FROM users WHERE id = ?`, user.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}

func TestARightCodeStartsTheCountAgainAndAPasswordDoesNot(t *testing.T) {
	svc, user := newTestService(t, false)
	secret, _, session := enrolledSession(t, svc, user)
	for range maxFailedCodes - 1 {
		if err := svc.VerifySecondFactor(t.Context(), session, user.ID, "000000"); !errors.Is(err, ErrInvalidTOTP) {
			t.Fatal(err)
		}
	}
	again, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "again")
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := svc.st.DB.QueryRow(`SELECT totp_failed_count FROM users WHERE id = ?`, user.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != maxFailedCodes-1 {
		t.Errorf("a password sign-in left the count at %d", count)
	}
	if err := svc.VerifySecondFactor(t.Context(), again.SessionID, user.ID, currentCode(t, secret)); err != nil {
		t.Fatal(err)
	}
	if err := svc.st.DB.QueryRow(`SELECT totp_failed_count FROM users WHERE id = ?`, user.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("a sign-in with the code left the count at %d", count)
	}
}

// Asking for the code again is the same code, so it is the same count.
func TestReauthenticationCountsWrongCodesAndHonoursTheLock(t *testing.T) {
	svc, user := newTestService(t, false)
	secret, _, _ := enrolledSession(t, svc, user)
	if err := svc.VerifyReauthentication(t.Context(), user.ID, "Wrong-Password-Guess-1", "000000"); !errors.Is(err, ErrReauthentication) {
		t.Fatal(err)
	}
	for range maxFailedCodes - 1 {
		if err := svc.VerifyReauthentication(t.Context(), user.ID, testPassword, "000000"); !errors.Is(err, ErrReauthentication) {
			t.Fatal(err)
		}
	}
	if err := svc.VerifyReauthentication(t.Context(), user.ID, testPassword, "000000"); !errors.Is(err, ErrSecondFactorLocked) {
		t.Fatalf("the last wrong code = %v, want locked", err)
	}
	if err := svc.VerifyReauthentication(t.Context(), user.ID, testPassword, currentCode(t, secret)); !errors.Is(err, ErrSecondFactorLocked) {
		t.Fatalf("a right code while locked = %v", err)
	}
	// Without the password the lock is not disclosed.
	if err := svc.VerifyReauthentication(t.Context(), user.ID, "Wrong-Password-Guess-1", currentCode(t, secret)); !errors.Is(err, ErrReauthentication) {
		t.Fatalf("a wrong password while locked = %v", err)
	}
}

func TestTheWaysBackClearTheLock(t *testing.T) {
	for name, wayBack := range map[string]func(*testing.T, *Service, *User){
		"an administrator's reset": func(t *testing.T, svc *Service, user *User) {
			if err := svc.ResetTOTP(t.Context(), user.ID); err != nil {
				t.Fatal(err)
			}
		},
		"turning it off": func(t *testing.T, svc *Service, user *User) {
			if err := svc.DisableTOTP(t.Context(), user.ID); err != nil {
				t.Fatal(err)
			}
		},
		"a password reset at the host": func(t *testing.T, svc *Service, user *User) {
			if err := svc.ResetPassword(t.Context(), user.ID, testPassword); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, user := newTestService(t, false)
			if _, err := svc.st.DB.Exec(`UPDATE users SET totp_failed_count = 3, totp_locked_until = ? WHERE id = ?`,
				time.Now().Add(time.Hour).Unix(), user.ID); err != nil {
				t.Fatal(err)
			}
			wayBack(t, svc, user)
			var count int
			var lockedUntil int64
			if err := svc.st.DB.QueryRow(`SELECT totp_failed_count, totp_locked_until FROM users WHERE id = ?`, user.ID).Scan(&count, &lockedUntil); err != nil {
				t.Fatal(err)
			}
			if count != 0 || lockedUntil != 0 {
				t.Errorf("count %d, locked until %d", count, lockedUntil)
			}
		})
	}
}
