package accountlink

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// ErrIdentityUnreadable means a key was stored but the master key in use cannot
// open it, which is what a changed JD_MASTER_KEY looks like.
var ErrIdentityUnreadable = errors.New("the stored link identity cannot be opened with the current JD_MASTER_KEY")

// Identity is this install's Ed25519 key pair. It is made on first use, sealed
// with the master key like every other secret in the settings table, and kept
// across unlinks so a later link shows the same fingerprint.
type Identity struct{ key ed25519.PrivateKey }

func (i *Identity) public() ed25519.PublicKey {
	return i.key.Public().(ed25519.PublicKey)
}

// PublicKey is the key as the protocol carries it: base64 of the 32 raw bytes.
func (i *Identity) PublicKey() string { return base64.StdEncoding.EncodeToString(i.public()) }

// Fingerprint is what the approval page and this dashboard both show.
func (i *Identity) Fingerprint() string { return Fingerprint(i.public()) }

func (i *Identity) sign(msg []byte) []byte { return ed25519.Sign(i.key, msg) }

func newIdentity() (*Identity, string, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, "", err
	}
	return &Identity{key: ed25519.NewKeyFromSeed(seed)}, base64.StdEncoding.EncodeToString(seed), nil
}

func openIdentity(sealer *auth.Sealer, sealed string) (*Identity, error) {
	plain, err := sealer.Open(sealed)
	if err != nil {
		return nil, ErrIdentityUnreadable
	}
	seed, err := base64.StdEncoding.DecodeString(plain)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, ErrIdentityUnreadable
	}
	return &Identity{key: ed25519.NewKeyFromSeed(seed)}, nil
}

// loadIdentity reads the stored identity without making one.
func loadIdentity(ctx context.Context, st *store.Store, sealer *auth.Sealer) (*Identity, bool, error) {
	sealed, ok, err := st.Setting(ctx, keyIdentity)
	if err != nil || !ok {
		return nil, false, err
	}
	id, err := openIdentity(sealer, sealed)
	if err != nil {
		return nil, true, err
	}
	return id, true, nil
}

// ensureIdentity returns the stored identity, making and storing one the first
// time. The insert keeps an existing row, so two processes starting together
// (the dashboard and a terminal command) end up with the same key.
func ensureIdentity(ctx context.Context, st *store.Store, sealer *auth.Sealer) (*Identity, error) {
	if sealer == nil {
		return nil, errors.New("a link identity needs the master key")
	}
	id, exists, err := loadIdentity(ctx, st, sealer)
	if err != nil || exists {
		return id, err
	}
	_, seed, err := newIdentity()
	if err != nil {
		return nil, err
	}
	sealed, err := sealer.Seal(seed)
	if err != nil {
		return nil, err
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`, keyIdentity, sealed); err != nil {
		return nil, fmt.Errorf("store link identity: %w", err)
	}
	id, _, err = loadIdentity(ctx, st, sealer)
	return id, err
}

// replaceUnreadableIdentity makes a new identity where the stored one cannot be
// opened. It is only for an install that is not linked: there is nothing to
// keep, and without it a changed master key would block linking for good.
func replaceUnreadableIdentity(ctx context.Context, st *store.Store, sealer *auth.Sealer) (*Identity, error) {
	fresh, seed, err := newIdentity()
	if err != nil {
		return nil, err
	}
	sealed, err := sealer.Seal(seed)
	if err != nil {
		return nil, err
	}
	if err := st.SetSetting(ctx, keyIdentity, sealed); err != nil {
		return nil, fmt.Errorf("replace link identity: %w", err)
	}
	return fresh, nil
}
