package auth

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// ConfirmTOTPEnrollmentForSession consumes the enrollment proof and completes
// its session in one transaction, so replay protection never needs an exception.
func (s *Service) ConfirmTOTPEnrollmentForSession(ctx context.Context, userID int64, sessionID, code string) ([]string, error) {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var sealed string
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT totp_secret, totp_enabled FROM users WHERE id = ? AND disabled = 0`, userID).Scan(&sealed, &enabled); err != nil {
		return nil, err
	}
	if enabled != 0 {
		return nil, ErrTOTPAlreadyEnabled
	}
	step, ok := s.validTOTPStep(sealed, code)
	if !ok {
		return nil, ErrInvalidTOTP
	}
	res, err := tx.ExecContext(ctx, `UPDATE users SET totp_enabled = 1, totp_last_step = ?
		WHERE id = ? AND totp_enabled = 0 AND totp_secret = ? AND totp_last_step < ?`, step, userID, sealed, step)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return nil, ErrInvalidTOTP
	}
	codes, err := regenerateRecoveryCodesTx(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if sessionID != "" {
		if err := elevateTx(ctx, tx, sessionID, userID, "totp"); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) validTOTPStep(sealed, code string) (int64, bool) {
	secret, err := s.sealer.Open(sealed)
	if err != nil || secret == "" {
		return 0, false
	}
	code = strings.TrimSpace(strings.ReplaceAll(code, " ", ""))
	current := time.Now().Unix() / 30
	for _, step := range []int64{current, current - 1, current + 1} {
		ok, err := totp.ValidateCustom(code, secret, time.Unix(step*30, 0), totp.ValidateOpts{
			Period: 30, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && ok {
			return step, true
		}
	}
	return 0, false
}

// Codes are guessed one transaction at a time: the store's are immediate, so
// reading the lock and counting a wrong code cannot interleave with another
// guess. Each outlives its request, since a client that hangs up after its guess
// has still spent it.
func secondFactorContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

// countBadCode keeps the failure the transaction exists to record, and says
// what the caller answers: wrong, or wrong and now locked.
func countBadCode(ctx context.Context, tx *sql.Tx, userID int64, wrong error) error {
	locked, err := countFailure(ctx, tx, failedCodes, userID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if locked {
		return ErrSecondFactorLocked
	}
	return wrong
}

func (s *Service) verifySecondFactor(ctx context.Context, sessionID string, userID int64, code string) error {
	ctx, cancel := secondFactorContext(ctx)
	defer cancel()
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var sealed string
	var lockedUntil int64
	if err := tx.QueryRowContext(ctx, `SELECT totp_secret, totp_locked_until FROM users WHERE id = ? AND totp_enabled = 1 AND disabled = 0`, userID).Scan(&sealed, &lockedUntil); err != nil {
		return ErrInvalidTOTP
	}
	// Asked before the code is looked at, so a right one reads like a wrong one.
	if lockedUntil > time.Now().Unix() {
		return ErrSecondFactorLocked
	}
	consumed := false
	method := "totp"
	if step, ok := s.validTOTPStep(sealed, code); ok {
		res, err := tx.ExecContext(ctx, `UPDATE users SET totp_last_step = ?
			WHERE id = ? AND totp_enabled = 1 AND totp_secret = ? AND totp_last_step < ?`, step, userID, sealed, step)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		consumed = n == 1
	} else {
		method = "recovery"
		code = strings.TrimSpace(strings.ReplaceAll(code, " ", ""))
		res, err := tx.ExecContext(ctx, `UPDATE recovery_codes SET used_at = ? WHERE user_id = ? AND code_hash = ? AND used_at = 0`, time.Now().Unix(), userID, HashToken(code))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		consumed = n == 1
	}
	if !consumed {
		return countBadCode(ctx, tx, userID, ErrInvalidTOTP)
	}
	if err := elevateTx(ctx, tx, sessionID, userID, method); err != nil {
		return err
	}
	// Only the code ends the count. A password or a passkey is what comes
	// before it, and starting a new session must not buy more guesses.
	if _, err := tx.ExecContext(ctx, `UPDATE users SET totp_failed_count = 0 WHERE id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// elevateTx completes a session with its second factor. One begun with a
// passkey keeps saying so: it reads "passkey+totp", not "totp", since a code
// after a passkey is not the password-and-code pair "totp" has always meant.
func elevateTx(ctx context.Context, tx *sql.Tx, sessionID string, userID int64, method string) error {
	res, err := tx.ExecContext(ctx, `UPDATE sessions SET twofa_passed = 1,
		auth_method = CASE WHEN auth_method = 'passkey' THEN 'passkey+' || ? ELSE ? END
		WHERE id = ? AND user_id = ? AND expires_at > ?`, method, method, sessionID, userID, time.Now().Unix())
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrSessionInvalid
	}
	_, err = tx.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, time.Now().Unix(), userID)
	return err
}

func regenerateRecoveryCodesTx(ctx context.Context, tx *sql.Tx, userID int64) ([]string, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, 10)
	for range 10 {
		code := RandomToken(6)
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes(user_id, code_hash) VALUES(?, ?)`, userID, HashToken(code)); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}
