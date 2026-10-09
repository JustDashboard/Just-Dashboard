package netx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"time"
)

const confirmationWindow = 90 * time.Second
const verificationFreshness = 30 * time.Second

type pendingConfirmationKey struct{}

// WithPendingConfirmation is set only by the authenticated session middleware.
// API callers without this opt-in retain immediate saved-state behavior.
func WithPendingConfirmation(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, pendingConfirmationKey{}, userID)
}

func pendingOwner(ctx context.Context) int64 {
	owner, _ := ctx.Value(pendingConfirmationKey{}).(int64)
	return owner
}

// finishPendingConfirmation is shared by durable spec and runtime-only applies.
// The caller still holds the journal lock and must recover on any error.
func finishPendingConfirmation(j *changeJournal) error {
	if j.OwnerUserID <= 0 {
		return nil
	}
	now := time.Now().UTC()
	if j.ExpiresAt.IsZero() || !now.Before(j.ExpiresAt) {
		return &ConfirmationError{"The pending apply exceeded its confirmation deadline; previous settings must be restored."}
	}
	if j.AppliedAt.IsZero() {
		j.AppliedAt = now
	}
	j.Phase, j.Watchdog = "awaiting_confirmation", "armed"
	return nil
}

type ConfirmationError struct{ Reason string }

func (e *ConfirmationError) Error() string { return e.Reason }

// ConfirmationView exposes lifecycle evidence but never recovery snapshots or
// the challenge digest. Availability is a prerequisite, not proof of arming.
type ConfirmationView struct {
	Available bool          `json:"available"`
	Owned     bool          `json:"owned"`
	Change    *ChangeStatus `json:"change"`
}

func (s *Service) ConfirmationStatus(ctx context.Context, userID int64) (*ConfirmationView, error) {
	j, err := readChange(s.paths.Dir)
	v := &ConfirmationView{Available: s.independentRecovery && has("systemctl") && has("systemd-run")}
	if errors.Is(err, fs.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	v.Change = &j.ChangeStatus
	v.Owned = j.OwnerUserID > 0 && j.OwnerUserID == userID
	return v, nil
}

type ReconnectionVerification struct {
	Challenge  string    `json:"challenge"`
	VerifiedAt time.Time `json:"verifiedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func (s *Service) pendingChange(ctx context.Context, id string, userID int64) (*changeJournal, error) {
	j, err := readChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	if userID <= 0 || j.ID != id || j.OwnerUserID != userID {
		return nil, &ConfirmationError{"This pending change belongs to another account or no longer exists."}
	}
	if j.Phase != "awaiting_confirmation" || j.Watchdog != "armed" {
		return nil, &ConfirmationError{"This change is no longer awaiting confirmation."}
	}
	if j.ExpiresAt.IsZero() || !time.Now().Before(j.ExpiresAt) {
		if err := recoverChange(ctx, j); err != nil {
			return nil, err
		}
		return nil, &ConfirmationError{"The confirmation deadline expired; the previous network settings were restored."}
	}
	return j, nil
}

// VerifyReconnection begins a fresh request/response challenge after apply.
// Receiving this nonce and returning it in a separate confirm request proves
// the dashboard transport carried a response; a route lookup alone cannot.
func (s *Service) VerifyReconnection(ctx context.Context, id string, userID int64, sessionID, transport string) (*ReconnectionVerification, error) {
	if sessionID == "" || transport == "" {
		return nil, &ConfirmationError{"An authenticated interactive session is required."}
	}
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	defer unlockChange(lock)
	j, err := s.pendingChange(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, err
	}
	challenge := hex.EncodeToString(token[:])
	digest := sha256.Sum256([]byte(challenge))
	j.VerificationDigest = hex.EncodeToString(digest[:])
	j.VerificationSession = sessionID
	j.VerificationTransport = transport
	j.VerifiedAt = time.Now().UTC()
	if !j.VerifiedAt.After(j.AppliedAt) {
		return nil, fmt.Errorf("reconnection evidence predates the apply")
	}
	if err := j.save(); err != nil {
		return nil, err
	}
	return &ReconnectionVerification{Challenge: challenge, VerifiedAt: j.VerifiedAt, ExpiresAt: j.ExpiresAt}, nil
}

func (s *Service) ConfirmChange(ctx context.Context, id string, userID int64, sessionID, challenge, transport string) (*ChangeStatus, error) {
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	defer unlockChange(lock)
	if prior, err := readChange(s.paths.Dir); err == nil && prior.ID == id && prior.OwnerUserID == userID && userID > 0 && sessionID != "" && transport != "" && prior.Phase == "confirmed" && hasNativeRecovery(prior) {
		if err := finalizeNativeChange(ctx, prior); err != nil {
			return &prior.ChangeStatus, err
		}
		return &prior.ChangeStatus, nil
	}
	j, err := s.pendingChange(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(challenge))
	encoded := hex.EncodeToString(digest[:])
	if sessionID == "" || transport == "" || j.VerificationTransport != transport || j.VerificationSession != sessionID || len(challenge) != 64 || j.VerificationDigest == "" || subtle.ConstantTimeCompare([]byte(encoded), []byte(j.VerificationDigest)) != 1 || !j.VerifiedAt.After(j.AppliedAt) || time.Now().After(j.VerifiedAt.Add(verificationFreshness)) {
		return nil, &ConfirmationError{"Verify a fresh dashboard reconnection from this session before confirming the change."}
	}
	if err := verifyNativeConfirmation(ctx, j); err != nil {
		return nil, err
	}
	if err := holdNativeCheckpoint(ctx, j); err != nil {
		return nil, err
	}
	if !time.Now().Before(j.ExpiresAt) {
		return nil, &ConfirmationError{"Native verification crossed the confirmation deadline; recover and review the current change before retrying."}
	}
	j.Phase, j.Watchdog = "confirmed", "completed"
	j.VerificationDigest, j.VerificationSession, j.VerificationTransport = "", "", ""
	if err := j.save(); err != nil {
		return nil, err
	}
	if err := finalizeNativeChange(ctx, j); err != nil {
		return &j.ChangeStatus, err
	}
	return &j.ChangeStatus, nil
}

func (s *Service) RecoverOwnedChange(ctx context.Context, id string, userID int64) (*ChangeStatus, error) {
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	defer unlockChange(lock)
	j, err := readChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	if userID <= 0 || j.ID != id || j.OwnerUserID != userID || changeTerminal(j.Phase) {
		return nil, &ConfirmationError{"This account has no recoverable pending change with that ID."}
	}
	if err := recoverChange(ctx, j); err != nil {
		return &j.ChangeStatus, err
	}
	return &j.ChangeStatus, nil
}

func (s *Service) CleanupOwnedChange(ctx context.Context, id string, userID int64) (*ChangeStatus, error) {
	lock, err := lockChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	defer unlockChange(lock)
	j, err := readChange(s.paths.Dir)
	if err != nil {
		return nil, err
	}
	if userID <= 0 || j.ID != id || j.OwnerUserID != userID || !hasNativeRecovery(j) || (j.Phase != "confirmed" && j.Phase != "recovered") {
		return nil, &ConfirmationError{"This account has no terminal native cleanup with that ID."}
	}
	if err := finalizeNativeChange(ctx, j); err != nil {
		return &j.ChangeStatus, err
	}
	return &j.ChangeStatus, nil
}
