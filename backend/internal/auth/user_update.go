package auth

import (
	"context"
	"fmt"
	"strings"
)

// UserUpdate is an administrator's account edit. Omitted fields are unchanged.
type UserUpdate struct {
	Profile
	Role     *Role
	Disabled *bool
	Password *string
}

// UpdateUser commits the complete edit with any required session revocation,
// so a rejected field or a failed revocation cannot leave a partial edit.
func (s *Service) UpdateUser(ctx context.Context, userID int64, update UserUpdate) error {
	profile, err := update.Profile.normalised()
	if err != nil {
		return err
	}
	if update.Role != nil && !update.Role.Valid() {
		return fmt.Errorf("unknown role %q", *update.Role)
	}
	var passwordHash *string
	if update.Password != nil {
		if err := ValidatePasswordStrength(*update.Password); err != nil {
			return err
		}
		hash, err := HashPassword(*update.Password)
		if err != nil {
			return err
		}
		passwordHash = &hash
	}
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if (update.Role != nil && *update.Role != RoleAdmin) || (update.Disabled != nil && *update.Disabled) {
		if err := guardLastAdmin(ctx, tx, userID); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, `UPDATE users SET
		username = COALESCE(?, username), display_name = COALESCE(?, display_name),
		role = COALESCE(?, role), disabled = COALESCE(?, disabled),
		password_hash = COALESCE(?, password_hash),
		must_change_pw = CASE WHEN ? THEN 0 ELSE must_change_pw END WHERE id = ?`,
		profile.Username, profile.DisplayName, update.Role, update.Disabled,
		passwordHash, update.Password != nil, userID)
	if err != nil {
		if profile.Username != nil && strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("user %q already exists", *profile.Username)
		}
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrNotFound
	}
	if update.Password != nil || (update.Disabled != nil && *update.Disabled) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
