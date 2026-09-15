package auth

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestEnrollmentConsumesCodeAndCompletesOnlyItsSession(t *testing.T) {
	svc, user := newTestService(t, true)
	first, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "second")
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
	codes, err := svc.ConfirmTOTPEnrollmentForSession(t.Context(), user.ID, first.SessionID, code)
	if err != nil || len(codes) != 10 {
		t.Fatalf("enrollment: %v, recovery count %d", err, len(codes))
	}
	session, _, err := svc.ResolveSession(t.Context(), first.Token)
	if err != nil || !session.TwoFAPassed {
		t.Fatalf("enrollment session not completed: %v", err)
	}
	if err := svc.VerifySecondFactor(t.Context(), second.SessionID, user.ID, code); !errors.Is(err, ErrInvalidTOTP) {
		t.Fatalf("consumed enrollment code reused: %v", err)
	}
	if _, err := svc.ConfirmTOTPEnrollmentForSession(t.Context(), user.ID, second.SessionID, code); !errors.Is(err, ErrTOTPAlreadyEnabled) {
		t.Fatalf("enrolled account confirmed again: %v", err)
	}
	if _, err := svc.BeginTOTPEnrollment(t.Context(), user.ID); !errors.Is(err, ErrTOTPAlreadyEnabled) {
		t.Fatalf("enabled secret overwritten: %v", err)
	}
	if err := svc.VerifySecondFactor(t.Context(), second.SessionID, user.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
	if err := svc.VerifySecondFactor(t.Context(), second.SessionID, user.ID, codes[0]); !errors.Is(err, ErrInvalidTOTP) {
		t.Fatalf("recovery code reused: %v", err)
	}
}

func TestEnrollmentRollsBackWhenItsSessionWasRevoked(t *testing.T) {
	svc, user := newTestService(t, true)
	enrollment, err := svc.BeginTOTPEnrollment(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmTOTPEnrollmentForSession(t.Context(), user.ID, "revoked", code); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("revoked session accepted: %v", err)
	}
	u, err := svc.UserByID(t.Context(), user.ID)
	if err != nil || u.TOTPEnabled {
		t.Fatalf("failed enrollment persisted: %v", err)
	}
	var count int
	if err := svc.st.DB.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ?`, user.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed enrollment persisted recovery codes: %d, %v", count, err)
	}
}

func TestOTPVerificationConsumesOneCodeAcrossConcurrentSessions(t *testing.T) {
	svc, user := newTestService(t, true)
	enrollment, err := svc.BeginTOTPEnrollment(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ConfirmTOTPEnrollment(t.Context(), user.ID, code); err != nil {
		t.Fatal(err)
	}
	// Model an enrollment completed in an earlier window without sleeping or
	// depending on a wall-clock boundary during concurrent verification.
	if _, err := svc.st.DB.Exec(`UPDATE users SET totp_last_step = -1 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	var sessions []string
	for range 3 {
		login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, login.SessionID)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, session := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if svc.VerifySecondFactor(t.Context(), session, user.ID, code) == nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("code elevated %d sessions, want one", successes.Load())
	}
}
