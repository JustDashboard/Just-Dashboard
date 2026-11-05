package accountlink

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"time"
)

// What another instance of the engine (package hublink) reads and sets beyond
// the account link's own API.

// Health is how the last exchanges with the peer went.
type Health struct {
	LastHeartbeatAt *time.Time
	// HeartbeatError is the last heartbeat's failure, empty once one succeeds.
	HeartbeatError string
	// PollError is the command poll's, kept apart for the same reason.
	PollError string
}

// Health reads what the last exchanges left behind.
func (s *Service) Health(ctx context.Context) Health {
	rs := s.kv.status(ctx)
	return Health{LastHeartbeatAt: rs.LastHeartbeatAt, HeartbeatError: rs.HeartbeatError, PollError: rs.PollError}
}

// Fingerprint is this instance's identity's fingerprint, once it has one.
func (s *Service) Fingerprint(ctx context.Context) string {
	if s.sealer == nil {
		return ""
	}
	if id, _, err := loadIdentity(ctx, s.kv, s.sealer); err == nil && id != nil {
		return id.Fingerprint()
	}
	return ""
}

// SetIdle stops using the stored link serverID: Idle is stored in its state,
// and also, when set, runs in the same write transaction, so what must end
// with the link's use (hub sign-in's sessions) cannot outlive the decision.
// The running loops end, and nothing is sent on the link again until a person
// unlinks. It reports whether this call made the link idle: false when it
// was already idle, or is gone or another.
func (s *Service) SetIdle(ctx context.Context, serverID string, idle Idle, also func(context.Context, *sql.Tx) error) (bool, error) {
	ctx = context.WithoutCancel(ctx)
	set := false
	_, _, err := s.kv.updateStateTx(ctx, serverID, func(tx *sql.Tx, cur *State) (bool, error) {
		if cur.Idle != nil {
			return false, nil
		}
		if also != nil {
			if err := also(ctx, tx); err != nil {
				return false, err
			}
		}
		cur.Idle, set = &idle, true
		return true, nil
	})
	if err != nil {
		return false, err
	}
	if set {
		s.cancelLinked()
		s.nudge()
	}
	return set, nil
}

// RecordNode pins the peer's machine on the stored link serverID, if it has
// none yet. It reports whether it did.
func (s *Service) RecordNode(ctx context.Context, serverID string, node PeerNode) (bool, error) {
	set := false
	_, _, err := s.kv.updateState(context.WithoutCancel(ctx), serverID, func(cur *State) bool {
		if cur.HubNode != nil {
			return false
		}
		cur.HubNode, set = &node, true
		return true
	})
	return set, err
}

// RecordRequestNode keeps the peer's machine with the pending request id, if
// it has none yet, and reports the request as it stands.
func (s *Service) RecordRequestNode(ctx context.Context, id string, node PeerNode) (Request, bool, error) {
	ctx = context.WithoutCancel(ctx)
	var out Request
	found := false
	err := s.kv.tx(ctx, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, s.kv.key(keyRequest)).Scan(&raw); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		var req Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil || req.ID != id {
			return err
		}
		found, out = true, req
		if req.HubNode != nil {
			return nil
		}
		req.HubNode = &node
		out = req
		return s.kv.putTx(ctx, tx, keyRequest, req)
	})
	return out, found && err == nil, err
}

// Timing shortens the engine's waits, for tests that cannot wait fifteen
// seconds for anything. A zero field keeps the default.
type Timing struct {
	Idle, Watch            time.Duration
	BackoffMin, BackoffMax time.Duration
	PolicyFirst            time.Duration
	PolicyEvery            time.Duration
	// HeartbeatsPerMinute replaces the profile's pacing.
	HeartbeatsPerMinute int
}

// SetTiming applies t. It is for tests and is called before Start.
func (s *Service) SetTiming(t Timing) {
	set := func(dst *time.Duration, v time.Duration) {
		if v > 0 {
			*dst = v
		}
	}
	set(&s.idle, t.Idle)
	set(&s.watch, t.Watch)
	set(&s.backoffMin, t.BackoffMin)
	set(&s.backoffMax, t.BackoffMax)
	set(&s.policyFirst, t.PolicyFirst)
	set(&s.policyEvery, t.PolicyEvery)
	if t.HeartbeatsPerMinute > 0 {
		s.hbPerMinute = t.HeartbeatsPerMinute
	}
}

// The signed strings and the canonical arguments, as the protocol documents
// define them, for another implementation's tests to check against the
// published vectors.

// RequestMessage is the string a request signature covers.
func RequestMessage(method, pathWithQuery string, body []byte, unixTime, sequence int64) string {
	return requestMessage(method, pathWithQuery, body, unixTime, sequence)
}

// SignRequest signs a request the way the client does.
func SignRequest(key ed25519.PrivateKey, method, pathWithQuery string, body []byte, unixTime, sequence int64) string {
	return signRequest(key, method, pathWithQuery, body, unixTime, sequence)
}

// CommandMessage is the string a command signature covers.
func CommandMessage(serverID, commandID, kind string, canonicalArgs []byte, expiresAt int64) string {
	return commandMessage(serverID, commandID, kind, canonicalArgs, expiresAt)
}

// CanonicalArgs decodes a command's arguments in the protocol's value set and
// renders them canonically; anything outside the set is an error.
func CanonicalArgs(raw json.RawMessage) ([]byte, error) {
	v, err := decodeArgs(raw)
	if err != nil {
		return nil, err
	}
	return canonicalJSON(v)
}
