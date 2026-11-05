package accountlink

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// Everything an instance keeps lives in the dashboard's settings table, under
// its profile's prefix and these names: account.link.identity for the account
// link, hub.link.identity for the hub link. Nothing but the identity is
// secret, and the identity is sealed.
const (
	keyIdentity = "identity"
	keyState    = "state"
	keyRequest  = "request"
	keySequence = "sequence"
	keyPolicy   = "update-policy"
	keyOutcome  = "outcome"
	keyStatus   = "status"
	keyAttempt  = "update-attempt"
)

// SiteKey is a key the site signs commands with, pinned when the link was made
// or announced since by a key that was already pinned.
type SiteKey struct {
	Kid       string `json:"kid"`
	PublicKey string `json:"publicKey"`
	// NotAfter is when the key stops being trusted, in unix seconds; zero is
	// no end.
	NotAfter int64 `json:"notAfter,omitempty"`
}

func (k SiteKey) key() (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(k.PublicKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("site key %q is not a 32-byte Ed25519 key", k.Kid)
	}
	return ed25519.PublicKey(raw), nil
}

func (k SiteKey) usable(now time.Time) bool { return k.NotAfter == 0 || now.Unix() < k.NotAfter }

// State is the link itself. It holds nothing secret: the site's public keys, the
// scope chosen here and who the link belongs to.
type State struct {
	URL      string    `json:"url"`
	ServerID string    `json:"serverId"`
	SiteKeys []SiteKey `json:"siteKeys"`
	Scope    Scope     `json:"scope"`
	LinkedAt time.Time `json:"linkedAt"`
	// LinkedBy is the dashboard account that began the link, or "cli".
	LinkedBy string `json:"linkedBy"`
	Owner    string `json:"owner"`
	// ServerName is what this server called itself when it asked to link.
	ServerName       string `json:"serverName,omitempty"`
	HeartbeatSeconds int    `json:"heartbeatSeconds"`
	PollWaitSeconds  int    `json:"pollWaitSeconds"`
}

func (s State) siteKey(kid string, now time.Time) (SiteKey, bool) {
	for _, k := range s.SiteKeys {
		if k.Kid == kid && k.usable(now) {
			return k, true
		}
	}
	return SiteKey{}, false
}

// Request is a link request waiting for a person to approve it on the site.
type Request struct {
	// URL is the origin the request was made to, for a profile that chooses
	// it per link; empty for one whose origin is configured.
	URL         string    `json:"url,omitempty"`
	ID          string    `json:"id"`
	UserCode    string    `json:"userCode"`
	ApproveURL  string    `json:"approveUrl"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Scope       Scope     `json:"scope"`
	PollSeconds int       `json:"pollSeconds"`
	// By is the dashboard account that asked, or "cli".
	By         string `json:"by"`
	ServerName string `json:"serverName,omitempty"`
	// Driver says which process polls the request. A request begun at the
	// terminal is polled by that command alone, so the running dashboard and
	// the command never both collect the one-time approval.
	Driver string `json:"driver"`
}

const (
	driverDashboard = "dashboard"
	driverCLI       = "cli"
)

// Outcome is why the last request ended without a link.
type Outcome struct {
	Result  string    `json:"result"`
	At      time.Time `json:"at"`
	Message string    `json:"message,omitempty"`
}

const (
	outcomeDenied  = "denied"
	outcomeExpired = "expired"
	outcomeFailed  = "failed"
)

// runtimeStatus is what the last exchanges with the site left behind, kept so
// a terminal command can say it too.
type runtimeStatus struct {
	LastHeartbeatAt *time.Time `json:"lastHeartbeatAt,omitempty"`
	// The heartbeat and the command poll fail and recover on their own, so each
	// keeps its own last error; one succeeding does not hide the other failing.
	HeartbeatError string `json:"heartbeatError,omitempty"`
	PollError      string `json:"pollError,omitempty"`
}

// lastError is the error the page shows: the heartbeat's if there is one.
func (r runtimeStatus) lastError() string {
	if r.HeartbeatError != "" {
		return r.HeartbeatError
	}
	return r.PollError
}

// kv is the settings table with the few operations this package needs, under
// one instance's prefix. Every key it is given is a name under that prefix.
type kv struct {
	st     *store.Store
	prefix string
	// clock, when set, puts a floor under the request counter: the current
	// unix milliseconds (Profile.ClockSequence).
	clock func() time.Time
}

func (k kv) key(name string) string { return k.prefix + "." + name }

func (k kv) get(ctx context.Context, name string, out any) (bool, error) {
	key := k.key(name)
	raw, ok, err := k.st.Setting(ctx, key)
	if err != nil || !ok {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return false, fmt.Errorf("%s is unreadable: %w", key, err)
	}
	return true, nil
}

func (k kv) put(ctx context.Context, name string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return k.st.SetSetting(ctx, k.key(name), string(raw))
}

func (k kv) del(ctx context.Context, names ...string) error {
	for _, name := range names {
		if _, err := k.st.DB.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, k.key(name)); err != nil {
			return err
		}
	}
	return nil
}

// tx runs fn in one transaction so a link is never half written.
func (k kv) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := k.st.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (k kv) putTx(ctx context.Context, tx *sql.Tx, name string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k.key(name), string(raw))
	return err
}

func (k kv) delTx(ctx context.Context, tx *sql.Tx, names ...string) error {
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, k.key(name)); err != nil {
			return err
		}
	}
	return nil
}

// updateState changes the stored link inside one transaction, so two writers
// (a heartbeat noting a key rotation, the page changing the scope) cannot read
// the same state and each write back their own copy of it. Transactions here
// take the write lock when they begin. It changes nothing unless the stored
// link is serverID's, and reports the state as it stands afterwards.
func (k kv) updateState(ctx context.Context, serverID string, change func(*State) bool) (State, bool, error) {
	return k.updateStateTx(ctx, serverID, func(_ *sql.Tx, st *State) (bool, error) { return change(st), nil })
}

// updateStateTx is updateState for a change that writes more than the state in
// the same transaction. An error from change rolls all of it back.
func (k kv) updateStateTx(ctx context.Context, serverID string, change func(*sql.Tx, *State) (bool, error)) (State, bool, error) {
	var out State
	found := false
	err := k.tx(ctx, func(tx *sql.Tx) error {
		st, ok, err := k.stateTx(ctx, tx)
		if err != nil || !ok || st.ServerID != serverID {
			return err
		}
		found, out = true, st
		changed, err := change(tx, &st)
		if err != nil || !changed {
			return err
		}
		out = st
		return k.putTx(ctx, tx, keyState, st)
	})
	return out, found && err == nil, err
}

// stateTx reads the stored link inside a transaction.
func (k kv) stateTx(ctx context.Context, tx *sql.Tx) (State, bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, k.key(keyState)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var st State
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return State{}, false, fmt.Errorf("%s is unreadable: %w", k.key(keyState), err)
	}
	return st, st.ServerID != "", nil
}

// statusTx reads the status inside a transaction; one that cannot be read is
// only a missing hint.
func (k kv) statusTx(ctx context.Context, tx *sql.Tx) runtimeStatus {
	var s runtimeStatus
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, k.key(keyStatus)).Scan(&raw); err == nil {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	return s
}

func (k kv) state(ctx context.Context) (State, bool, error) {
	var s State
	ok, err := k.get(ctx, keyState, &s)
	if err != nil || !ok || s.ServerID == "" {
		return State{}, false, err
	}
	return s, true, nil
}

func (k kv) request(ctx context.Context) (Request, bool, error) {
	var r Request
	ok, err := k.get(ctx, keyRequest, &r)
	if err != nil || !ok || r.ID == "" {
		return Request{}, false, err
	}
	return r, true, nil
}

func (k kv) outcome(ctx context.Context) (Outcome, bool, error) {
	var o Outcome
	ok, err := k.get(ctx, keyOutcome, &o)
	return o, ok && o.Result != "", err
}

func (k kv) status(ctx context.Context) runtimeStatus {
	var s runtimeStatus
	// A status that cannot be read is only a missing hint.
	_, _ = k.get(ctx, keyStatus, &s)
	return s
}

// nextSequence takes the next number of the counter every signed request
// carries. One statement both raises it and reads it back, and a commit is
// durable before this returns, so the number is on disk before it is on the
// wire and two processes can never be handed the same one. The counter spans
// the life of the identity: it is not reset by an unlink.
//
// With a clock the new number is max(stored + 1, unix milliseconds), computed
// in that same statement: a database restored from a copy taken before the
// last thousand requests (a VPS snapshot rolled back) would otherwise send
// numbers the peer has already seen until it caught up.
func (k kv) nextSequence(ctx context.Context) (int64, error) {
	var raw string
	var err error
	if k.clock != nil {
		ms := k.clock().UnixMilli()
		err = k.st.DB.QueryRowContext(ctx,
			`INSERT INTO settings(key, value) VALUES(?, CAST(? AS TEXT))
			 ON CONFLICT(key) DO UPDATE SET value = CAST(MAX(CAST(value AS INTEGER) + 1, ?) AS TEXT)
			 RETURNING value`, k.key(keySequence), ms, ms).Scan(&raw)
	} else {
		err = k.st.DB.QueryRowContext(ctx,
			`INSERT INTO settings(key, value) VALUES(?, '1')
			 ON CONFLICT(key) DO UPDATE SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)
			 RETURNING value`, k.key(keySequence)).Scan(&raw)
	}
	if err != nil {
		return 0, fmt.Errorf("persist request sequence: %w", err)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return 0, errors.New("the stored request sequence is corrupt")
	}
	return n, nil
}
