package accountlink

import (
	"context"
	"crypto/ed25519"
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
