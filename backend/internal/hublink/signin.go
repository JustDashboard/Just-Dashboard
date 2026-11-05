package hublink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// Hub sign-in (hub-link-protocol.md section 9) lets a person open this server
// from the hub it is linked to and land on it signed in, without this server's
// own second factor. It is the documented exception to invariant 2, and it
// holds only while three switches are on — JD_HUB_SIGNIN here, the hub's own
// setting, and the account's grant — and the link is stored and not idle.
//
// Everything that ends one of those ends the hub sessions it allowed, in the
// same write transaction as the change: a grant ended by its account (auth
// does those, auth/hub_signin.go), a grant the hub stopped offering (onReply),
// the link ending (onClear), the link going idle (goIdle, through
// revokeHubSessionsTx), and the switch found off at start (PrepareSignIn). And every time a hub session is used, it is
// checked again (checkHubSession), so a revocation that was missed cannot keep
// one working.

const maxGrantsSent = 100

// Why a grant or a hub session ended, beyond auth's own reasons.
const (
	endRevoked          = "revoked"
	endRemovedByAdmin   = "removed_by_admin"
	endReplaced         = "replaced"
	endLinkEnded        = "link_ended"
	endLinkIdle         = "link_idle"
	endSwitchedOff      = "switched_off"
	endHubUserNotOffer  = "hub_user_not_offered"
	endHubSignInNoOffer = "hub_signin_not_offered"
)

var (
	ErrSignInDisabled    = errors.New("hub sign-in is switched off on this server (JD_HUB_SIGNIN)")
	ErrSignInNotOffered  = errors.New("the hub does not offer sign-in; turn it on in the hub's settings first")
	ErrHubUserNotOffered = errors.New("the hub does not offer that hub user")
	ErrHubUserTaken      = errors.New("another account on this server already lets that hub user sign it in")
	ErrNoGrant           = errors.New("that account has not let Just Hub sign it in")
)

// SignInEnabled is JD_HUB_SIGNIN, which counts as off while JD_HUB_LINK is.
func (s *Service) SignInEnabled() bool { return s != nil && s.enabled && s.signIn }

// Grant is one account's permission for one hub user to sign it in.
type Grant struct {
	UserID   int64     `json:"userId"`
	Username string    `json:"username"`
	HubUser  string    `json:"hubUser"`
	Since    time.Time `json:"since"`
}

// MyGrant is the caller's own grant, as the status's signIn.mine says it.
type MyGrant struct {
	HubUser string    `json:"hubUser"`
	Since   time.Time `json:"since"`
}

// grantsFor is every grant made for the link serverID, with the account's
// current username. A grant made for an earlier link is void and not listed.
func grantsFor(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, serverID string) ([]Grant, error) {
	rows, err := q.QueryContext(ctx, `SELECT g.user_id, u.username, g.hub_user, g.created_at
		FROM hub_signin_grants g JOIN users u ON u.id = g.user_id
		WHERE g.server_id = ? ORDER BY u.username, g.user_id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		var at int64
		if err := rows.Scan(&g.UserID, &g.Username, &g.HubUser, &at); err != nil {
			return nil, err
		}
		g.Since = time.Unix(at, 0).UTC()
		out = append(out, g)
	}
	return out, rows.Err()
}

// Grants is every grant for the current link, for an administrator.
func (s *Service) Grants(ctx context.Context) ([]Grant, error) {
	st, linked, err := s.engine.Linked(ctx)
	if err != nil || !linked {
		return []Grant{}, err
	}
	return grantsFor(ctx, s.st.DB, st.ServerID)
}

// usableLink is the stored link, if there is one and it is not idle (steps 2
// and 12 of section 9.7): not idle in the store, and passing the URL rule as
// it applies at start, which this decides if the connector has not yet. It
// never asks tailscaled: the hub node check is the connector's, before it
// sends, and a node change sets the link idle in the store.
func (s *Service) usableLink(ctx context.Context) (accountlink.State, bool) {
	st, linked, err := s.engine.Linked(ctx)
	if err != nil || !linked || s.admitRule(ctx, st) != nil {
		return accountlink.State{}, false
	}
	return st, true
}

// stateInTx reads the stored link inside a transaction, so what is decided
// from it cannot be undone by a link change committed meanwhile.
func stateInTx(ctx context.Context, tx *sql.Tx) (accountlink.State, bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, profile.KeyPrefix+".state").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return accountlink.State{}, false, nil
	}
	if err != nil {
		return accountlink.State{}, false, err
	}
	var st accountlink.State
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return accountlink.State{}, false, fmt.Errorf("%s.state is unreadable: %w", profile.KeyPrefix, err)
	}
	return st, st.ServerID != "", nil
}

// --------------------------------------------------------------- grants --

// AllowRequest is an account letting a hub user sign it in.
type AllowRequest struct {
	UserID  int64
	HubUser string
}

// Allow makes or replaces the caller's grant (section 9.3). The caller has
// already proved the password, and the code where the account has an
// authenticator. A grant that existed ends here, with its hub sessions, in
// the transaction that makes the new one, whether or not the hub user is the
// same, so a session made from the old grant never outlives it.
func (s *Service) Allow(ctx context.Context, r AllowRequest) error {
	st, err := s.allowable(ctx, r.HubUser)
	if err != nil {
		return err
	}
	var old auth.HubGrantEnd
	err = s.writeTx(ctx, func(tx *sql.Tx) error {
		cur, linked, err := stateInTx(ctx, tx)
		if err != nil {
			return err
		}
		if !linked || cur.ServerID != st.ServerID {
			return ErrNotLinked
		}
		var other int64
		err = tx.QueryRowContext(ctx, `SELECT user_id FROM hub_signin_grants WHERE hub_user = ? AND user_id <> ?`,
			r.HubUser, r.UserID).Scan(&other)
		if err == nil {
			return ErrHubUserTaken
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if old, err = endGrantTx(ctx, tx, r.UserID, endReplaced); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO hub_signin_grants(user_id, hub_user, server_id, created_at) VALUES(?, ?, ?, ?)`,
			r.UserID, r.HubUser, st.ServerID, s.now().Unix())
		return err
	})
	if err != nil {
		return err
	}
	if old.HubUser != "" || old.SessionsRevoked > 0 {
		s.auditGrantEnd(ctx, old, "", "session")
	}
	s.recordSignIn(ctx, audit.Entry{
		UserID: r.UserID, Username: s.username(ctx, r.UserID), Action: "hub.signin.allow", Target: r.HubUser,
		Success: true, Detail: audit.Detail(map[string]any{"hubUser": r.HubUser}),
	})
	s.engine.HeartbeatSoon()
	return nil
}

// CanAllow says whether a grant for hubUser could be made now, before the
// caller spends a password guess on it: hub sign-in on, a usable link, a hub
// that offers sign-in, and offers that hub user.
func (s *Service) CanAllow(ctx context.Context, hubUser string) error {
	_, err := s.allowable(ctx, hubUser)
	return err
}

func (s *Service) allowable(ctx context.Context, hubUser string) (accountlink.State, error) {
	if !s.SignInEnabled() {
		return accountlink.State{}, ErrSignInDisabled
	}
	st, ok := s.usableLink(ctx)
	if !ok {
		return accountlink.State{}, ErrNotLinked
	}
	v, ok := s.readStoredView(ctx, st)
	if !ok || v.SignIn == nil {
		return accountlink.State{}, ErrSignInNotOffered
	}
	if !slices.Contains(v.SignIn.Users, hubUser) {
		return accountlink.State{}, ErrHubUserNotOffered
	}
	return st, nil
}

// Revoke removes one account's grant and revokes its hub sessions. byAdmin is
// the administrator who removed it; empty, the account removed its own.
func (s *Service) Revoke(ctx context.Context, userID int64, byAdmin string) error {
	reason := endRevoked
	if byAdmin != "" {
		reason = endRemovedByAdmin
	}
	var end auth.HubGrantEnd
	if err := s.writeTx(ctx, func(tx *sql.Tx) error {
		var err error
		end, err = endGrantTx(ctx, tx, userID, reason)
		return err
	}); err != nil {
		return err
	}
	if end.HubUser == "" && end.SessionsRevoked == 0 {
		return ErrNoGrant
	}
	s.auditGrantEnd(ctx, end, byAdmin, "session")
	s.engine.HeartbeatSoon()
	return nil
}

// endGrantTx deletes one account's grant and revokes its hub sessions.
func endGrantTx(ctx context.Context, tx *sql.Tx, userID int64, reason string) (auth.HubGrantEnd, error) {
	end := auth.HubGrantEnd{UserID: userID, Reason: reason}
	err := tx.QueryRowContext(ctx, `DELETE FROM hub_signin_grants WHERE user_id = ? RETURNING hub_user`, userID).Scan(&end.HubUser)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return end, err
	}
	end.SessionsRevoked, err = auth.RevokeHubSessionsTx(ctx, tx, userID)
	return end, err
}

// endEveryGrantTx ends every grant and every hub session, for an event about
// the whole link or the switch. It reports each grant, and how many hub
// sessions had no grant behind them.
func endEveryGrantTx(ctx context.Context, tx *sql.Tx, reason string) ([]auth.HubGrantEnd, int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM hub_signin_grants ORDER BY user_id`)
	if err != nil {
		return nil, 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	ends := make([]auth.HubGrantEnd, 0, len(ids))
	for _, id := range ids {
		end, err := endGrantTx(ctx, tx, id, reason)
		if err != nil {
			return nil, 0, err
		}
		ends = append(ends, end)
	}
	rest, err := auth.RevokeHubSessionsTx(ctx, tx, 0)
	return ends, rest, err
}

// writeTx runs fn in one write transaction (the store's begin IMMEDIATE).
func (s *Service) writeTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ------------------------------------------------- the link's own events --

// onClear forgets the view, every grant, every redeemed ticket and every hub
// session with the link they belong to, in the transaction that ends or
// replaces it (sections 8.9 and 9.3). The audit entries are written once it
// has committed.
func (s *Service) onClear(ctx context.Context, tx *sql.Tx) (func(), error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, keyView); err != nil {
		return nil, err
	}
	ends, rest, err := endEveryGrantTx(ctx, tx, endLinkEnded)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM hub_signin_tickets`); err != nil {
		return nil, err
	}
	return func() { s.auditLinkEvent(ctx, ends, rest, endLinkEnded) }, nil
}

// PrepareSignIn runs before the API serves its first request. With hub
// sign-in off (or the hub link off), every grant, every redeemed ticket and
// every hub session is deleted in one transaction, so turning the switch on
// again later never brings an old grant back; if that cannot be written the
// dashboard must not start. With it on, old ticket records are pruned.
func (s *Service) PrepareSignIn(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.SignInEnabled() {
		s.pruneTickets(ctx)
		return nil
	}
	var (
		ends []auth.HubGrantEnd
		rest int64
	)
	if err := s.writeTx(ctx, func(tx *sql.Tx) error {
		var err error
		if ends, rest, err = endEveryGrantTx(ctx, tx, endSwitchedOff); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM hub_signin_tickets`)
		return err
	}); err != nil {
		return fmt.Errorf("hub sign-in is off, and its grants and sessions could not be cleared: %w", err)
	}
	s.auditLinkEvent(ctx, ends, rest, endSwitchedOff)
	return nil
}

// pruneTickets drops ticket records a day after the last moment they could
// have been accepted.
func (s *Service) pruneTickets(ctx context.Context) {
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM hub_signin_tickets WHERE expires_at < ?`,
		s.now().Add(-24*time.Hour).Unix()); err != nil {
		s.log.Debug("could not prune hub sign-in tickets", "err", err)
	}
}

// checkHubSession is auth's check on every use of a hub session (section 9.3,
// "A hub session lives only while what made it still holds"): both switches
// on, the link stored and not idle, and the account's grant for this link
// still the one the session was made from.
func (s *Service) checkHubSession(ctx context.Context, sess *auth.Session) bool {
	if !s.SignInEnabled() {
		return false
	}
	st, ok := s.usableLink(ctx)
	if !ok {
		return false
	}
	var hubUser string
	var created int64
	err := s.st.DB.QueryRowContext(ctx,
		`SELECT hub_user, created_at FROM hub_signin_grants WHERE user_id = ? AND server_id = ?`,
		sess.UserID, st.ServerID).Scan(&hubUser, &created)
	if err != nil {
		return false
	}
	return hubUser == sess.HubUser && created <= sess.CreatedAt.Unix()
}

// heartbeatExtra tells the hub, on every heartbeat while hub sign-in is on,
// which accounts let which of its users sign them in (section 9.3). Off, the
// key is absent and the hub reads that as sign-in off here.
func (s *Service) heartbeatExtra(ctx context.Context, st accountlink.State) (map[string]json.RawMessage, error) {
	if !s.SignInEnabled() {
		return nil, nil
	}
	grants, err := grantsFor(ctx, s.st.DB, st.ServerID)
	if err != nil {
		return nil, err
	}
	type wireGrant struct {
		UserID   int64  `json:"userId"`
		Username string `json:"username"`
		HubUser  string `json:"hubUser"`
	}
	out := []wireGrant{}
	for _, g := range grants {
		if len(out) == maxGrantsSent {
			break
		}
		out = append(out, wireGrant{UserID: g.UserID, Username: g.Username, HubUser: g.HubUser})
	}
	raw, err := json.Marshal(map[string]any{"grants": out})
	if err != nil {
		return nil, err
	}
	return map[string]json.RawMessage{"signIn": raw}, nil
}

// endUnofferedTx ends every grant whose hub user the latest reply no longer
// offers: all of them when the reply carries no signIn (section 6.3). It runs
// inside the transaction that keeps the reply, so it belongs to the link the
// reply came from.
func endUnofferedTx(ctx context.Context, tx *sql.Tx, offer *signInOffer) ([]auth.HubGrantEnd, error) {
	rows, err := tx.QueryContext(ctx, `SELECT user_id, hub_user FROM hub_signin_grants ORDER BY user_id`)
	if err != nil {
		return nil, err
	}
	type row struct {
		id  int64
		hub string
	}
	var gone []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.hub); err != nil {
			rows.Close()
			return nil, err
		}
		if offer == nil || !slices.Contains(offer.Users, r.hub) {
			gone = append(gone, r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reason := endHubUserNotOffer
	if offer == nil {
		reason = endHubSignInNoOffer
	}
	var ends []auth.HubGrantEnd
	for _, r := range gone {
		end, err := endGrantTx(ctx, tx, r.id, reason)
		if err != nil {
			return nil, err
		}
		ends = append(ends, end)
	}
	return ends, nil
}

// ---------------------------------------------------------------- audit --

func (s *Service) username(ctx context.Context, userID int64) string {
	var name string
	if err := s.st.DB.QueryRowContext(ctx, `SELECT username FROM users WHERE id = ?`, userID).Scan(&name); err != nil {
		return fmt.Sprintf("user %d", userID)
	}
	return name
}

func (s *Service) recordSignIn(ctx context.Context, e audit.Entry) {
	if s.audit == nil {
		return
	}
	if e.Method == "" {
		e.Method = "LINK"
	}
	if e.Status == 0 {
		e.Status = http.StatusOK
	}
	s.audit.Record(context.WithoutCancel(ctx), e)
}

// auditGrantEnd writes hub.signin.revoke for one grant that ended, by whoever
// ended it: the account itself, an administrator, or "system" for an event
// nobody asked for (section 9.10).
func (s *Service) auditGrantEnd(ctx context.Context, end auth.HubGrantEnd, username, actor string) {
	account := s.username(ctx, end.UserID)
	if username == "" {
		username = account
	}
	s.recordSignIn(ctx, audit.Entry{
		UserID: end.UserID, Username: username, Actor: actor, Action: "hub.signin.revoke", Target: end.HubUser,
		Success: true, Detail: audit.Detail(map[string]any{
			"account": account, "hubUser": end.HubUser, "reason": end.Reason, "sessionsRevoked": end.SessionsRevoked,
		}),
	})
}

// AuditAccountEnd writes hub.signin.revoke for a grant a change to its account
// ended (auth's events: a password, an authenticator, a disable, a delete, all
// sessions revoked). username and actor are who made the change; empty, the
// system did.
func (s *Service) AuditAccountEnd(ctx context.Context, end auth.HubGrantEnd, username, actor string) {
	if s == nil {
		return
	}
	if username == "" {
		username, actor = "system", "system"
	}
	s.auditGrantEnd(ctx, end, username, actor)
}

// auditLinkEvent writes hub.signin.revoke for each grant a link or switch event
// ended, and hub.session.revoke for hub sessions it revoked with no grant
// behind them.
func (s *Service) auditLinkEvent(ctx context.Context, ends []auth.HubGrantEnd, rest int64, reason string) {
	for _, end := range ends {
		s.auditGrantEnd(ctx, end, "system", "system")
	}
	if rest > 0 {
		s.recordSignIn(ctx, audit.Entry{
			Username: "system", Actor: "system", Action: "hub.session.revoke", Success: true,
			Detail: audit.Detail(map[string]any{"reason": reason, "sessionsRevoked": rest}),
		})
	}
}
