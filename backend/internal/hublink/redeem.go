package hublink

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// RedeemRequest is a POST /api/v1/auth/hub/enter.
type RedeemRequest struct {
	Ticket string
	// Client is the browser's address as the network allowlist reads it, the
	// trusted address behind the dashboard's own proxy.
	Client    string
	UserAgent string
	// Cookie is the session cookie this browser sent, if any: that session is
	// revoked when the new one is made, so one browser holds one session.
	Cookie string
	// Path is the route, for the audit entry.
	Path string
}

// Redeemed is the session a ticket made.
type Redeemed struct {
	*auth.HubSessionStart
	Next string
}

var (
	errSignInDisabled = refuse(http.StatusForbidden, codeSignInDisabled,
		"signing in through Just Hub is not turned on for this server; sign in here")
	errHubNotLinked = refuse(http.StatusForbidden, codeNotLinked,
		"this server is not connected to a Just Hub it can trust right now; sign in here")
	errNotPermitted = refuse(http.StatusForbidden, codeNotPermitted,
		"this account does not let that Just Hub user sign it in; sign in here, and turn it on in Settings → Hub")
	errAccountUnavailable = refuse(http.StatusForbidden, codeAccountUnavailable,
		"this account cannot be signed in through Just Hub now; sign in on this server itself")
)

// Redeem is section 9.7: a ticket the hub signed, checked in order and
// stopped at the first failure, becomes a complete session for the account
// that opted in. Every outcome is audited as hub.signin.
func (s *Service) Redeem(ctx context.Context, r RedeemRequest) (*Redeemed, *SignInError) {
	a := signInAudit{req: r, username: "anonymous", actor: "anonymous"}
	out, err := s.redeem(ctx, r, &a)
	if err != nil {
		a.code = err.Code
		s.recordRedeem(ctx, a, false)
		return nil, err
	}
	s.recordRedeem(ctx, a, true)
	return out, nil
}

// signInAudit is what the hub.signin entry says, filled in as the steps pass
// (section 9.10).
type signInAudit struct {
	req       RedeemRequest
	username  string
	actor     string
	userID    int64
	code      string
	kid       string
	payload   *TicketPayload
	device    *DeviceCheck
	sessionID string
}

func (s *Service) redeem(ctx context.Context, r RedeemRequest, a *signInAudit) (*Redeemed, *SignInError) {
	// Step 1: both switches.
	if !s.SignInEnabled() {
		return nil, errSignInDisabled
	}
	// Step 2: a link the connector has not set idle.
	st, ok := s.usableLink(ctx)
	if !ok {
		return nil, errHubNotLinked
	}
	// Steps 3 to 9: the ticket itself.
	kid, p, signed, refused := checkTicket(r.Ticket, ticketCheck{
		Keys: st.SiteKeys, Hub: st.URL, ServerID: st.ServerID, Now: s.now(),
	})
	a.kid, a.payload = kid, p
	if signed {
		a.actor = "hub:" + st.Owner
	}
	if refused != nil {
		return nil, refused
	}
	// Step 10: the device that asked the hub for it. After the signature and
	// time, so a forged or stale ticket costs no WhoIs; before the nonce, so a
	// stolen ticket tried from the wrong device does not spend it.
	device, refused := checkDevice(ctx, s.whois, r.Client, p)
	a.device = &device
	if refused != nil {
		return nil, refused
	}
	// Step 11: single use, committed on its own before anything else is
	// decided, so a ticket that got this far is spent even if it is refused
	// below.
	used, err := s.spendNonce(ctx, p)
	if err != nil {
		s.log.Warn("could not record a hub sign-in ticket", "err", err)
		return nil, refuse(http.StatusInternalServerError, "internal", "the ticket could not be recorded")
	}
	if used {
		return nil, errTicketUsed
	}
	if s.afterNonce != nil {
		s.afterNonce()
	}
	// Steps 12 to 14 in one write transaction: the link, the grant, the
	// account and the new session. Every event that ends a grant or the link
	// is a write transaction that also revokes hub sessions, so the two
	// serialise: either this commits first and the event revokes what it made,
	// or the event commits first and this finds nothing to stand on.
	var start *auth.HubSessionStart
	txErr := s.writeTx(ctx, func(tx *sql.Tx) error {
		cur, linked, err := stateInTx(ctx, tx)
		if err != nil {
			return err
		}
		if !linked || cur.ServerID != p.ServerID || cur.URL != p.Hub || !s.SignInEnabled() || s.idleOrUndecided(cur) {
			return errHubNotLinked
		}
		var hubUser, serverID string
		err = tx.QueryRowContext(ctx, `SELECT hub_user, server_id FROM hub_signin_grants WHERE user_id = ?`, p.UserID).
			Scan(&hubUser, &serverID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (hubUser != p.HubUser || serverID != cur.ServerID)) {
			return errNotPermitted
		}
		if err != nil {
			return err
		}
		a.userID, a.username = p.UserID, s.usernameTx(ctx, tx, p.UserID)
		if s.auth == nil {
			return errors.New("no account service")
		}
		start, err = s.auth.StartHubSessionTx(ctx, tx, p.UserID, p.HubUser, r.Client, r.UserAgent, r.Cookie)
		if errors.Is(err, auth.ErrHubAccountUnavailable) {
			return errAccountUnavailable
		}
		return err
	})
	var signInErr *SignInError
	switch {
	case errors.As(txErr, &signInErr):
		return nil, signInErr
	case txErr != nil:
		s.log.Warn("a hub sign-in could not be completed", "err", txErr)
		return nil, refuse(http.StatusInternalServerError, "internal", "the session could not be made")
	}
	a.sessionID = start.Session.ID
	return &Redeemed{HubSessionStart: start, Next: p.NextPath()}, nil
}

// spendNonce is step 11: one INSERT, committed on its own. It reports whether
// the nonce had been used already.
func (s *Service) spendNonce(ctx context.Context, p *TicketPayload) (bool, error) {
	res, err := s.st.DB.ExecContext(context.WithoutCancel(ctx),
		`INSERT INTO hub_signin_tickets(nonce, user_id, used_at, expires_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(nonce) DO NOTHING`, p.Nonce, p.UserID, s.now().Unix(), p.Exp+ticketGrace)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 0, err
}

func (s *Service) usernameTx(ctx context.Context, tx *sql.Tx, userID int64) string {
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT username FROM users WHERE id = ?`, userID).Scan(&name); err != nil {
		return "anonymous"
	}
	return name
}

// recordRedeem writes hub.signin: what the ticket said once its signature
// held, the device check, and the session made or the code refused with.
func (s *Service) recordRedeem(ctx context.Context, a signInAudit, ok bool) {
	detail := map[string]any{}
	if !ok {
		detail["code"] = a.code
	}
	if a.payload != nil {
		p := a.payload
		detail["hubUser"], detail["userId"], detail["nonce"] = p.HubUser, p.UserID, p.Nonce
		detail["tsLogin"], detail["tsNode"] = p.TSLogin, p.TSNode
		if ok {
			detail["kid"], detail["iat"], detail["mfaAt"] = a.kid, p.Iat, p.MFAAt
		}
	}
	if a.device != nil {
		switch a.device.Result {
		case "checked":
			detail["deviceCheck"] = "checked"
		case "skipped":
			detail["deviceCheck"], detail["deviceCheckReason"] = "skipped", a.device.Reason
		default:
			if a.device.Reason != "" {
				detail["deviceRefused"] = a.device.Reason
			}
			if a.device.Login != "" || a.device.Node != "" {
				detail["whoisLogin"], detail["whoisNode"] = a.device.Login, a.device.Node
			}
		}
	}
	status := http.StatusOK
	if ok {
		detail["sessionId"] = a.sessionID
	} else {
		status = http.StatusUnauthorized
	}
	target := ""
	if a.payload != nil {
		target = a.payload.HubUser
	}
	s.recordSignIn(ctx, audit.Entry{
		UserID: a.userID, Username: a.username, Actor: a.actor, IP: a.req.Client,
		Action: "hub.signin", Target: target, Method: http.MethodPost, Path: a.req.Path,
		Status: status, Success: ok, Detail: audit.Detail(detail),
	})
}

// SetAfterNonce runs fn between steps 11 and 12 of every redeem, after the
// nonce has committed and before the transaction that makes the session. The
// tests use it to end the link exactly there; nothing else may.
func SetAfterNonce(s *Service, fn func()) { s.afterNonce = fn }
