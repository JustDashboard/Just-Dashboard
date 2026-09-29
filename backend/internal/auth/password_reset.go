package auth

import "context"

// ResetPassword issues a temporary credential for recovery. Revocation and the
// new hash commit together, so an old session cannot survive a partial reset.
// Authenticator enrollment and disabled state remain the account's decisions.
func (s *Service) ResetPassword(ctx context.Context, userID int64, password string) error {
	if err := ValidatePasswordStrength(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, must_change_pw = 1,
		failed_count = 0, locked_until = 0 WHERE id = ?`, hash, userID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked = 1 WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
