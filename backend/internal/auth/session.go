package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrSessionInvalid = errors.New("session invalid or expired")

type Session struct {
	ID          string    `json:"id"`
	UserID      int64     `json:"userId"`
	TwoFAPassed bool      `json:"twoFactorPassed"`
	IP          string    `json:"ip"`
	UserAgent   string    `json:"userAgent"`
	CreatedAt   time.Time `json:"createdAt"`
	LastSeenAt  time.Time `json:"lastSeenAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Current     bool      `json:"current"`
}

func (s *Service) newSession(ctx context.Context, userID int64, ip, ua string, twofaPassed bool) (*Session, string, error) {
	token := RandomToken(32)
	now := time.Now()
	sess := &Session{
		ID:          RandomToken(12),
		UserID:      userID,
		TwoFAPassed: twofaPassed,
		IP:          ip,
		UserAgent:   truncate(ua, 256),
		CreatedAt:   now,
		LastSeenAt:  now,
		ExpiresAt:   now.Add(s.sessionTTL),
	}
	passed := 0
	if twofaPassed {
		passed = 1
	}
	_, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO sessions(id, user_id, token_hash, twofa_passed, ip, user_agent, created_at, last_seen_at, expires_at)
		 VALUES(?,?,?,?,?,?,?,?,?)`,
		sess.ID, userID, HashToken(token), passed, sess.IP, sess.UserAgent,
		now.Unix(), now.Unix(), sess.ExpiresAt.Unix())
	if err != nil {
		return nil, "", err
	}
	return sess, token, nil
}

// ResolveSession validates a session cookie and slides the idle window.
// It returns the session even when the second factor is still outstanding —
// the caller decides whether a half-authenticated session is acceptable for
// the route being served.
func (s *Service) ResolveSession(ctx context.Context, token string) (*Session, *User, error) {
	if token == "" {
		return nil, nil, ErrSessionInvalid
	}
	var (
		sess               Session
		passed             int
		created, seen, exp int64
	)
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT id, user_id, twofa_passed, ip, user_agent, created_at, last_seen_at, expires_at
		 FROM sessions WHERE token_hash = ?`, HashToken(token)).
		Scan(&sess.ID, &sess.UserID, &passed, &sess.IP, &sess.UserAgent, &created, &seen, &exp)
	if err == sql.ErrNoRows {
		return nil, nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	sess.TwoFAPassed = passed == 1
	sess.CreatedAt = time.Unix(created, 0).UTC()
	sess.LastSeenAt = time.Unix(seen, 0).UTC()
	sess.ExpiresAt = time.Unix(exp, 0).UTC()

	if now.After(sess.ExpiresAt) || now.Sub(sess.LastSeenAt) > s.idleTTL {
		s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sess.ID)
		return nil, nil, ErrSessionInvalid
	}
	u, err := s.UserByID(ctx, sess.UserID)
	if err != nil {
		return nil, nil, ErrSessionInvalid
	}
	if u.Disabled {
		s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, u.ID)
		return nil, nil, ErrAccountDisabled
	}
	// A session that was left half-authenticated by a policy which no longer
	// demands a second factor is completed here rather than left unusable.
	// Turning JD_REQUIRE_2FA off otherwise strands everyone who was mid-flow
	// when it changed: their session is valid, it can never be elevated
	// (there is no authenticator to prove), and every route but the enrolment
	// ones refuses it until the cookie expires.
	if !sess.TwoFAPassed && !s.require2FA && !u.TOTPEnabled {
		if err := s.elevate(ctx, sess.ID, u.ID); err == nil {
			sess.TwoFAPassed = true
		}
	}

	if now.Sub(sess.LastSeenAt) > 30*time.Second {
		s.st.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, now.Unix(), sess.ID)
		sess.LastSeenAt = now
	}
	return &sess, u, nil
}

func (s *Service) ListSessions(ctx context.Context, userID int64, currentID string) ([]*Session, error) {
	rows, err := s.st.DB.QueryContext(ctx,
		`SELECT id, user_id, twofa_passed, ip, user_agent, created_at, last_seen_at, expires_at
		 FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Session{}
	for rows.Next() {
		var (
			sess               Session
			passed             int
			created, seen, exp int64
		)
		if err := rows.Scan(&sess.ID, &sess.UserID, &passed, &sess.IP, &sess.UserAgent, &created, &seen, &exp); err != nil {
			return nil, err
		}
		sess.TwoFAPassed = passed == 1
		sess.CreatedAt = time.Unix(created, 0).UTC()
		sess.LastSeenAt = time.Unix(seen, 0).UTC()
		sess.ExpiresAt = time.Unix(exp, 0).UTC()
		sess.Current = sess.ID == currentID
		out = append(out, &sess)
	}
	return out, rows.Err()
}

func (s *Service) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

func (s *Service) RevokeAllSessions(ctx context.Context, userID int64) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// RevokeOtherSessions signs an account out everywhere but the session it is
// asking from: the lever for "I left myself signed in somewhere" that does not
// also cost the operator the session they are holding.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID int64, keepID string) (int64, error) {
	res, err := s.st.DB.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND id != ?`, userID, keepID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PurgeExpired drops sessions past their absolute deadline. Idle expiry is
// enforced on read; this only keeps the table from growing without bound.
func (s *Service) PurgeExpired(ctx context.Context) error {
	_, err := s.st.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
