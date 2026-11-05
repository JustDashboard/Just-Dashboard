package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Hub sign-in (docs/internal/backend/hub-link-protocol.md section 9) lets a
// person open this server from a Just Hub already signed in. It is the one way
// a session starts complete without this server asking for its own second
// factor, so it is the documented exception to invariant 2, and it is narrow:
// the operator turns it on (JD_HUB_SIGNIN), each account opts in for one hub
// user, a session it makes lasts two hours at most, and that session is
// refused on every route that would leave a credential behind it
// (httpx.RefuseHubSession).
//
// The grants and the redeemed tickets are package hublink's. What lives here is
// what has to happen inside this package's own transactions: a hub session is
// made like every other session, a hub session is checked again every time it
// is used, and every change to an account that ends its grant deletes the
// grant and revokes the account's hub sessions in the same transaction as the
// change itself (section 9.3), so no order of events can leave a hub session
// working after the thing that made it is gone.

// MethodHub is the auth_method of a session a hub sign-in ticket made.
const MethodHub = "hub"

// A hub session lasts two hours at most, and an hour without use ends it
// sooner (section 9.9). The install's own limits still apply when they are
// shorter.
const (
	HubSessionTTL  = 2 * time.Hour
	HubSessionIdle = 60 * time.Minute
)

// Why a grant ended, as hub.signin.revoke records it.
const (
	HubEndPasswordChanged      = "password_changed"
	HubEndPasswordReset        = "password_reset"
	HubEndAuthenticatorChanged = "authenticator_changed"
	HubEndAccountDisabled      = "account_disabled"
	HubEndAccountDeleted       = "account_deleted"
	HubEndSessionsRevoked      = "sessions_revoked"
)

// ErrHubAccountUnavailable is an account a ticket names that this server would
// not sign in now: disabled, locked, owing a password change, or unenrolled
// while two-factor is required (section 9.7 step 13).
var ErrHubAccountUnavailable = errors.New("this account cannot be signed in through Just Hub now; sign in on this server itself")

// HubGrantEnd is a grant ended, or hub sessions revoked, by a change to one
// account. HubUser is empty when the account had no grant.
type HubGrantEnd struct {
	UserID          int64
	HubUser         string
	Reason          string
	SessionsRevoked int64
}

// HubSessionCheck says whether a hub session may still be used: the switches
// are on, the link is stored and not idle, and the account's grant is still
// the one the session was made from (section 9.3). Package hublink provides
// it; without one, no hub session is accepted.
type HubSessionCheck func(ctx context.Context, sess *Session) bool

// SetHubSessionCheck installs the check every hub session passes each time it
// is resolved.
func (s *Service) SetHubSessionCheck(check HubSessionCheck) { s.hubCheck = check }

// SetHubGrantEnded installs what is told, after the change has committed, that
// a change to an account ended its grant or revoked its hub sessions. The API
// writes hub.signin.revoke from it.
func (s *Service) SetHubGrantEnded(fn func(context.Context, HubGrantEnd)) { s.hubEnded = fn }

// RevokeHubSessionsTx deletes the hub sessions of one account, or of every
// account when userID is 0, inside the caller's transaction, and says how
// many there were.
func RevokeHubSessionsTx(ctx context.Context, tx execer, userID int64) (int64, error) {
	var (
		res sql.Result
		err error
	)
	if userID == 0 {
		res, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE auth_method = ?`, MethodHub)
	} else {
		res, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND auth_method = ?`, userID, MethodHub)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// endHubGrantTx is section 9.3's "a grant ends" for one account: its grant row
// is deleted and its hub sessions revoked, inside the transaction that makes
// the change that ends it. It runs before any statement that deletes all of
// the account's sessions, so the count it reports is the hub sessions'.
func endHubGrantTx(ctx context.Context, tx *sql.Tx, userID int64, reason string) (HubGrantEnd, error) {
	end := HubGrantEnd{UserID: userID, Reason: reason}
	err := tx.QueryRowContext(ctx,
		`DELETE FROM hub_signin_grants WHERE user_id = ? RETURNING hub_user`, userID).Scan(&end.HubUser)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return end, err
	}
	end.SessionsRevoked, err = RevokeHubSessionsTx(ctx, tx, userID)
	return end, err
}

// hubEndedAfterCommit reports a grant end once its transaction has committed;
// nothing is reported for an account that had neither a grant nor a hub
// session.
func (s *Service) hubEndedAfterCommit(ctx context.Context, end HubGrantEnd) {
	if s.hubEnded == nil || (end.HubUser == "" && end.SessionsRevoked == 0) {
		return
	}
	s.hubEnded(context.WithoutCancel(ctx), end)
}

// HubSessionStart is the session a redeemed ticket made.
type HubSessionStart struct {
	User      *User
	Session   *Session
	Token     string
	ExpiresAt time.Time
}

// StartHubSessionTx is steps 13 and 14 of section 9.7, inside the redeem's
// write transaction: the account must be one this server would sign in now,
// and the session is complete from the start, made for the grant's hub user,
// two hours long at most. replaceToken is the session cookie the browser sent,
// if any: that session is revoked first, so one browser does not hold two.
func (s *Service) StartHubSessionTx(ctx context.Context, tx *sql.Tx, userID int64, hubUser, ip, userAgent, replaceToken string) (*HubSessionStart, error) {
	u, _, _, _, lockedUntil, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrHubAccountUnavailable
	}
	if err != nil {
		return nil, err
	}
	var totpLocked int64
	if err := tx.QueryRowContext(ctx, `SELECT totp_locked_until FROM users WHERE id = ?`, userID).Scan(&totpLocked); err != nil {
		return nil, err
	}
	now := time.Now()
	switch {
	case u.Disabled, u.MustChangePW,
		lockedUntil > now.Unix(), totpLocked > now.Unix(),
		!u.TOTPEnabled && s.require2FA:
		return nil, ErrHubAccountUnavailable
	}
	if replaceToken != "" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, HashToken(replaceToken)); err != nil {
			return nil, err
		}
	}
	ttl := min(s.sessionTTL, HubSessionTTL)
	if ttl <= 0 {
		ttl = HubSessionTTL
	}
	token := RandomToken(32)
	sess := &Session{
		ID: RandomToken(12), UserID: u.ID, AuthMethod: MethodHub, HubUser: hubUser, TwoFAPassed: true,
		IP: ip, UserAgent: truncate(userAgent, 256), CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(ttl),
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions(id, user_id, token_hash, twofa_passed, auth_method, hub_user, ip, user_agent, created_at, last_seen_at, expires_at)
		 VALUES(?,?,?,1,?,?,?,?,?,?,?)`,
		sess.ID, u.ID, HashToken(token), MethodHub, hubUser, sess.IP, sess.UserAgent,
		now.Unix(), now.Unix(), sess.ExpiresAt.Unix()); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET last_login_at = ? WHERE id = ?`, now.Unix(), u.ID); err != nil {
		return nil, err
	}
	u.LastLoginAt = time.Unix(now.Unix(), 0).UTC()
	return &HubSessionStart{User: u, Session: sess, Token: token, ExpiresAt: sess.ExpiresAt}, nil
}
