package auth

import (
	"context"
	"time"
)

// DeleteInactiveAPIToken removes metadata only after the credential has stopped
// working. Keep that condition in the write so a stale list cannot delete a
// usable credential; the audit trail lives separately and is never removed here.
func (s *Service) DeleteInactiveAPIToken(ctx context.Context, id int64) (bool, error) {
	res, err := s.st.DB.ExecContext(ctx,
		`DELETE FROM api_tokens WHERE id = ? AND (revoked = 1 OR (expires_at > 0 AND expires_at <= ?))`,
		id, time.Now().Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
